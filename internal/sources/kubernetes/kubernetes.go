// Package kubernetes implements sources.ClusterSource against the Kubernetes API.
//
// The sources.ClusterSource interface has no Start/Close lifecycle hook, so this
// adapter deliberately avoids SharedInformerFactory (which would need a
// background goroutine the interface cannot manage). Instead it issues direct
// typed-client List calls and filters in Go. client-go types never leak past
// this package — every method returns neutral model.* values (ADR-0003).
package kubernetes

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// podLogsCap bounds how many bytes of a pod's logs are read into a result, so a
// chatty container can't exhaust control-plane memory. Hitting it sets Truncated.
const podLogsCap = 1 << 20 // 1 MiB

// defaultTailLines is the log tail used when a PodLogsQuery requests <= 0 lines.
const defaultTailLines int64 = 200

// Client reads Kubernetes resource state + events. Runs inside the agent, using
// the pod's in-cluster ServiceAccount (or a kubeconfig in direct mode).
//
// Construction is lazy: New stores connection parameters and the clientset is
// built on first use. This keeps New non-failing (mirroring the other source
// adapters) so a Provider can be assembled even where no cluster is reachable —
// the failure surfaces from the method call instead, satisfying the engine's
// graceful-degradation contract.
type Client struct {
	cluster        string
	kubeconfigPath string

	mu   sync.Mutex
	cs   kubernetes.Interface // cached clientset, built on first use
	init bool                 // whether cs/initErr have been resolved
	err  error
}

// New constructs a Kubernetes source for the given logical cluster name.
// kubeconfigPath == "" selects in-cluster configuration; otherwise the named
// kubeconfig is loaded. The clientset is built lazily on the first method call.
func New(cluster, kubeconfigPath string) (*Client, error) {
	return &Client{cluster: cluster, kubeconfigPath: kubeconfigPath}, nil
}

// newWithClient builds a Client around an already-constructed clientset. It is
// the injection seam used by tests (fake.NewSimpleClientset) and by any caller
// that resolves the clientset itself.
func newWithClient(cluster string, cs kubernetes.Interface) *Client {
	return &Client{cluster: cluster, cs: cs, init: true}
}

func (c *Client) Name() string { return "kubernetes" }

// clientset returns the cached clientset, building it from the configured
// kubeconfig (or in-cluster config) on first use.
func (c *Client) clientset() (kubernetes.Interface, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.init {
		return c.cs, c.err
	}
	c.init = true

	cfg, err := c.restConfig()
	if err != nil {
		c.err = err
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		c.err = fmt.Errorf("kubernetes: build clientset: %w", err)
		return nil, c.err
	}
	c.cs = cs
	return c.cs, nil
}

func (c *Client) restConfig() (*rest.Config, error) {
	if c.kubeconfigPath == "" {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("kubernetes: in-cluster config: %w", err)
		}
		return cfg, nil
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", c.kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: load kubeconfig %q: %w", c.kubeconfigPath, err)
	}
	return cfg, nil
}

// Events returns Kubernetes event signals (OOMKilled, BackOff, FailedMount,
// probe failures, ...) within q.Range. Events are listed from the namespace
// carried by q.Resource ("" lists across all namespaces) and filtered in Go to
// the requested window.
func (c *Client) Events(ctx context.Context, q sources.EventQuery) ([]model.Signal, error) {
	cs, err := c.clientset()
	if err != nil {
		return nil, err
	}

	list, err := cs.CoreV1().Events(q.Resource.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes events: %w", err)
	}

	out := make([]model.Signal, 0, len(list.Items))
	for i := range list.Items {
		ev := &list.Items[i]
		ts := eventTime(ev)
		if !inRange(ts, q.Range) {
			continue
		}

		obj := ev.InvolvedObject
		ref := model.ResourceRef{
			Cluster:   c.cluster,
			Namespace: obj.Namespace,
			Kind:      obj.Kind,
			Name:      obj.Name,
		}
		if obj.Kind == "Pod" {
			ref.Pod = obj.Name
		}

		out = append(out, model.Signal{
			Kind:      model.SignalK8sEvent,
			Source:    "kubernetes",
			Timestamp: ts.UTC(),
			Severity:  severityFromEvent(ev.Type, ev.Reason),
			Title:     ev.Reason,
			Message:   ev.Message,
			Resource:  ref,
		})
	}
	return out, nil
}

// ListWorkloads enumerates workloads in a namespace ("" = all namespaces).
// Deployments are the primary unit; StatefulSets and DaemonSets are included
// when present so correlation can attribute pod signals to any owning workload.
func (c *Client) ListWorkloads(ctx context.Context, namespace string) ([]model.ResourceRef, error) {
	cs, err := c.clientset()
	if err != nil {
		return nil, err
	}

	var out []model.ResourceRef

	deploys, err := cs.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes deployments: %w", err)
	}
	for i := range deploys.Items {
		d := &deploys.Items[i]
		out = append(out, model.ResourceRef{
			Cluster:   c.cluster,
			Namespace: d.Namespace,
			Kind:      "Deployment",
			Name:      d.Name,
		})
	}

	statefulSets, err := cs.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes statefulsets: %w", err)
	}
	for i := range statefulSets.Items {
		s := &statefulSets.Items[i]
		out = append(out, model.ResourceRef{
			Cluster:   c.cluster,
			Namespace: s.Namespace,
			Kind:      "StatefulSet",
			Name:      s.Name,
		})
	}

	daemonSets, err := cs.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes daemonsets: %w", err)
	}
	for i := range daemonSets.Items {
		ds := &daemonSets.Items[i]
		out = append(out, model.ResourceRef{
			Cluster:   c.cluster,
			Namespace: ds.Namespace,
			Kind:      "DaemonSet",
			Name:      ds.Name,
		})
	}

	return out, nil
}

// Annotations Kubernetes stamps on revision-bearing objects.
const (
	annoRevision    = "deployment.kubernetes.io/revision"
	annoChangeCause = "kubernetes.io/change-cause"
)

// WorkloadHistory returns a workload's image/revision history, newest-first
// (implements sources.WorkloadHistorian). For a Deployment it reconstructs the
// rollout from the owned ReplicaSets — each is one revision (keyed by the
// deployment.kubernetes.io/revision annotation), carrying that revision's
// container images, creation time, and kubernetes.io/change-cause. For
// StatefulSets/DaemonSets (no equivalent queryable ReplicaSet history) it
// returns a single current revision built from the live pod template, so the UI
// still shows the running image version.
func (c *Client) WorkloadHistory(ctx context.Context, ref model.ResourceRef) ([]model.WorkloadRevision, error) {
	cs, err := c.clientset()
	if err != nil {
		return nil, err
	}
	switch ref.Kind {
	case "Deployment":
		return c.deploymentHistory(ctx, cs, ref)
	case "StatefulSet":
		s, err := cs.AppsV1().StatefulSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("kubernetes statefulset: %w", err)
		}
		return []model.WorkloadRevision{currentRevision(&s.Spec.Template.Spec, s.CreationTimestamp, s.Annotations)}, nil
	case "DaemonSet":
		ds, err := cs.AppsV1().DaemonSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("kubernetes daemonset: %w", err)
		}
		return []model.WorkloadRevision{currentRevision(&ds.Spec.Template.Spec, ds.CreationTimestamp, ds.Annotations)}, nil
	default:
		return nil, nil
	}
}

// deploymentHistory builds the revision list from the ReplicaSets a Deployment
// owns. The Deployment's own revision annotation marks which one is live.
func (c *Client) deploymentHistory(ctx context.Context, cs kubernetes.Interface, ref model.ResourceRef) ([]model.WorkloadRevision, error) {
	dep, err := cs.AppsV1().Deployments(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes deployment: %w", err)
	}
	currentRev := dep.Annotations[annoRevision]

	rsList, err := cs.AppsV1().ReplicaSets(ref.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes replicasets: %w", err)
	}
	out := make([]model.WorkloadRevision, 0, len(rsList.Items))
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		owned := false
		for j := range rs.OwnerReferences {
			if rs.OwnerReferences[j].UID == dep.UID {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		revStr := rs.Annotations[annoRevision]
		rev, _ := strconv.ParseInt(revStr, 10, 64)
		out = append(out, model.WorkloadRevision{
			Revision:    rev,
			Images:      containerImages(&rs.Spec.Template.Spec),
			CreatedAt:   rs.CreationTimestamp.Time.UTC(),
			ChangeCause: rs.Annotations[annoChangeCause],
			Current:     revStr != "" && revStr == currentRev,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision > out[j].Revision })
	return out, nil
}

// currentRevision builds a single "live" revision from a controller's pod
// template (used for kinds without queryable ReplicaSet history).
func currentRevision(spec *corev1.PodSpec, created metav1.Time, ann map[string]string) model.WorkloadRevision {
	rev, _ := strconv.ParseInt(ann[annoRevision], 10, 64)
	return model.WorkloadRevision{
		Revision:    rev,
		Images:      containerImages(spec),
		CreatedAt:   created.Time.UTC(),
		ChangeCause: ann[annoChangeCause],
		Current:     true,
	}
}

// containerImages returns the images of a pod template's (non-init) containers,
// in spec order.
func containerImages(spec *corev1.PodSpec) []string {
	out := make([]string, 0, len(spec.Containers))
	for i := range spec.Containers {
		out = append(out, spec.Containers[i].Image)
	}
	return out
}

// ListNodes enumerates the cluster's Nodes (cluster-scoped) and maps each to a
// neutral model.Node: readiness from the NodeReady condition, roles from
// node-role.kubernetes.io/<role> labels, version/OS/arch/kernel/runtime from
// status.nodeInfo, the internal IP from status.addresses, and the raw
// capacity/allocatable quantity strings for cpu/memory/pods.
func (c *Client) ListNodes(ctx context.Context) ([]model.Node, error) {
	cs, err := c.clientset()
	if err != nil {
		return nil, err
	}
	list, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes nodes: %w", err)
	}
	out := make([]model.Node, 0, len(list.Items))
	for i := range list.Items {
		n := &list.Items[i]
		info := n.Status.NodeInfo
		out = append(out, model.Node{
			Name:              n.Name,
			Ready:             nodeReady(n),
			Roles:             nodeRoles(n),
			KubeletVersion:    info.KubeletVersion,
			OS:                info.OperatingSystem,
			Arch:              info.Architecture,
			OSImage:           info.OSImage,
			KernelVersion:     info.KernelVersion,
			ContainerRuntime:  info.ContainerRuntimeVersion,
			InternalIP:        nodeInternalIP(n),
			CPUCapacity:       quantityString(n.Status.Capacity, corev1.ResourceCPU),
			MemoryCapacity:    quantityString(n.Status.Capacity, corev1.ResourceMemory),
			PodsCapacity:      quantityString(n.Status.Capacity, corev1.ResourcePods),
			CPUAllocatable:    quantityString(n.Status.Allocatable, corev1.ResourceCPU),
			MemoryAllocatable: quantityString(n.Status.Allocatable, corev1.ResourceMemory),
			Unschedulable:     n.Spec.Unschedulable,
			CreatedAt:         n.CreationTimestamp.Time.UTC(),
		})
	}
	return out, nil
}

// nodeReady reports whether the node carries a NodeReady condition with status
// True.
func nodeReady(n *corev1.Node) bool {
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// nodeRoles extracts the <role> suffix of every node-role.kubernetes.io/<role>
// label, sorted. Returns nil when the node carries no role labels (the UI shows
// "<none>").
func nodeRoles(n *corev1.Node) []string {
	const prefix = "node-role.kubernetes.io/"
	var roles []string
	for k := range n.Labels {
		if role, ok := strings.CutPrefix(k, prefix); ok && role != "" {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return roles
}

// nodeInternalIP returns the node's first NodeInternalIP address, or "" if none.
func nodeInternalIP(n *corev1.Node) string {
	for _, addr := range n.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			return addr.Address
		}
	}
	return ""
}

// quantityString returns the raw quantity string for a resource in a
// capacity/allocatable list, or "" when the resource is absent.
func quantityString(rl corev1.ResourceList, name corev1.ResourceName) string {
	if q, ok := rl[name]; ok {
		return q.String()
	}
	return ""
}

// ListPods enumerates pods in q.Resource.Namespace, with each pod's containers
// and applied env. When q.Resource.Name is set the list is narrowed to that
// workload's pods: we keep a pod if its name has the workload name as a prefix
// (the standard ReplicaSet/StatefulSet naming) OR its app / app.kubernetes.io/name
// label equals the workload name. This label-or-prefix heuristic avoids an
// owner-chain walk (ReplicaSet -> Deployment) while still attributing pods to
// the workloads the UI lists.
//
// When q.Reveal is true, secret/configMap-sourced env vars are resolved to their
// actual values (Secrets are base64-decoded by client-go into []byte). A
// resolution error leaves Value empty rather than failing the whole call; Source
// is always retained for provenance.
func (c *Client) ListPods(ctx context.Context, q sources.PodQuery) ([]model.Pod, error) {
	cs, err := c.clientset()
	if err != nil {
		return nil, err
	}

	ns := q.Resource.Namespace
	list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes pods: %w", err)
	}

	// Caches so repeated valueFrom refs to the same object hit the API once.
	secretCache := map[string]*corev1.Secret{}
	configMapCache := map[string]*corev1.ConfigMap{}
	// replicaSetCache memoises ReplicaSet owner-resolution within one call so
	// multiple pods of one Deployment don't refetch the same RS.
	replicaSetCache := map[string]*model.WorkloadRef{}

	out := make([]model.Pod, 0, len(list.Items))
	for i := range list.Items {
		p := &list.Items[i]
		if q.Resource.Name != "" && !podMatchesWorkload(p, q.Resource.Name) {
			continue
		}

		// Resolve the owning workload once per pod (also used to attribute inline
		// literal env vars to their owner below).
		owner := c.resolveOwner(ctx, cs, p, replicaSetCache)

		out = append(out, model.Pod{
			Name:       p.Name,
			Namespace:  p.Namespace,
			Phase:      string(p.Status.Phase),
			Ready:      podReady(p),
			Restarts:   podRestarts(p),
			Node:       p.Spec.NodeName,
			Owner:      owner,
			Containers: c.containers(ctx, cs, ns, p, owner, q.Reveal, secretCache, configMapCache),
		})
	}
	return out, nil
}

// controllerOwnerRef returns the ownerReference marked Controller==true, falling
// back to the first ownerReference when none is so marked. Returns nil when the
// object has no owners (a bare pod).
func controllerOwnerRef(refs []metav1.OwnerReference) *metav1.OwnerReference {
	if len(refs) == 0 {
		return nil
	}
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return &refs[0]
}

// resolveOwner finds the user-facing workload that owns a pod. The pod's
// controller ownerReference is reported directly for StatefulSet/DaemonSet/Job/
// etc.; a ReplicaSet is followed up one level to its owning Deployment (the unit
// the UI lists). Resolution failures degrade gracefully: a ReplicaSet that can't
// be followed is reported as-is, and a bare pod yields nil.
func (c *Client) resolveOwner(ctx context.Context, cs kubernetes.Interface, p *corev1.Pod, rsCache map[string]*model.WorkloadRef) *model.WorkloadRef {
	owner := controllerOwnerRef(p.OwnerReferences)
	if owner == nil {
		return nil
	}
	if owner.Kind != "ReplicaSet" {
		return &model.WorkloadRef{Kind: owner.Kind, Name: owner.Name}
	}
	// Resolve the ReplicaSet in the pod's own namespace, not the query namespace
	// (which is "" for an all-namespaces listing).
	return c.resolveReplicaSetOwner(ctx, cs, p.Namespace, owner.Name, rsCache)
}

// resolveReplicaSetOwner maps a ReplicaSet to its controlling workload (normally
// a Deployment). On a fetch error or an RS with no controller owner it falls back
// to reporting the ReplicaSet itself. Results are cached per ListPods call.
func (c *Client) resolveReplicaSetOwner(ctx context.Context, cs kubernetes.Interface, ns, name string, cache map[string]*model.WorkloadRef) *model.WorkloadRef {
	key := ns + "/" + name // namespaced: RS names can repeat across namespaces
	if ref, ok := cache[key]; ok {
		return ref
	}
	fallback := &model.WorkloadRef{Kind: "ReplicaSet", Name: name}
	rs, err := cs.AppsV1().ReplicaSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		cache[key] = fallback
		return fallback
	}
	if owner := controllerOwnerRef(rs.OwnerReferences); owner != nil {
		ref := &model.WorkloadRef{Kind: owner.Kind, Name: owner.Name}
		cache[key] = ref
		return ref
	}
	cache[key] = fallback
	return fallback
}

// podMatchesWorkload reports whether pod belongs to the named workload by the
// label-or-name-prefix heuristic documented on ListPods.
func podMatchesWorkload(p *corev1.Pod, workload string) bool {
	if p.Labels["app"] == workload || p.Labels["app.kubernetes.io/name"] == workload {
		return true
	}
	return strings.HasPrefix(p.Name, workload+"-")
}

// podReady reports whether every container in the pod is ready.
func podReady(p *corev1.Pod) bool {
	if len(p.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, cs := range p.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

// podRestarts sums RestartCount across the pod's container statuses.
func podRestarts(p *corev1.Pod) int32 {
	var total int32
	for _, cs := range p.Status.ContainerStatuses {
		total += cs.RestartCount
	}
	return total
}

// containers maps a pod's spec containers (Name/Image/Env) to neutral model
// types, resolving valueFrom env when reveal is set. owner is the pod's resolved
// owning workload (nil for a bare pod), used to attribute inline literal env vars.
func (c *Client) containers(ctx context.Context, cs kubernetes.Interface, ns string, p *corev1.Pod, owner *model.WorkloadRef, reveal bool, secretCache map[string]*corev1.Secret, configMapCache map[string]*corev1.ConfigMap) []model.Container {
	// Index live container statuses by name so each spec container can be paired
	// with its kubelet-reported readiness/state (a container the kubelet has not
	// reported yet simply has no entry and stays State "").
	statusByName := make(map[string]*corev1.ContainerStatus, len(p.Status.ContainerStatuses))
	for i := range p.Status.ContainerStatuses {
		s := &p.Status.ContainerStatuses[i]
		statusByName[s.Name] = s
	}

	out := make([]model.Container, 0, len(p.Spec.Containers))
	for ci := range p.Spec.Containers {
		ct := &p.Spec.Containers[ci]
		env := make([]model.ContainerEnvVar, 0, len(ct.Env))
		for _, e := range ct.Env {
			env = append(env, c.envVar(ctx, cs, ns, p, owner, e, reveal, secretCache, configMapCache))
		}
		mc := model.Container{
			Name:  ct.Name,
			Image: ct.Image,
			Env:   env,
		}
		if s := statusByName[ct.Name]; s != nil {
			mc.Ready = s.Ready
			mc.RestartCount = s.RestartCount
			mc.State, mc.Reason = containerState(s.State)
		}
		out = append(out, mc)
	}
	return out
}

// containerState reduces a corev1.ContainerState (a union with at most one of
// Running/Waiting/Terminated set) to a neutral state string and reason. Running
// has no reason; a terminated container reports its kubelet reason (e.g.
// "Completed", "Error", "OOMKilled"), and a waiting one its reason (e.g.
// "CrashLoopBackOff", "ImagePullBackOff").
func containerState(s corev1.ContainerState) (state, reason string) {
	switch {
	case s.Running != nil:
		return "running", ""
	case s.Waiting != nil:
		return "waiting", s.Waiting.Reason
	case s.Terminated != nil:
		return "terminated", s.Terminated.Reason
	default:
		return "", ""
	}
}

// envVar maps one corev1.EnvVar to a neutral ContainerEnvVar. Literal Value is
// copied verbatim and attributed to the pod's owning workload (or the pod itself
// for a bare pod) so every env var carries a Source. ValueFrom sources are
// recorded as EnvVarSource provenance; when reveal is set, secret/configMap
// sources are resolved to their actual value (resolution failures leave Value
// empty).
func (c *Client) envVar(ctx context.Context, cs kubernetes.Interface, ns string, p *corev1.Pod, owner *model.WorkloadRef, e corev1.EnvVar, reveal bool, secretCache map[string]*corev1.Secret, configMapCache map[string]*corev1.ConfigMap) model.ContainerEnvVar {
	out := model.ContainerEnvVar{Name: e.Name}
	if e.ValueFrom == nil {
		out.Value = e.Value
		// Inline literal: attribute it to the owning workload, falling back to
		// the pod itself when the pod has no controller (a bare pod).
		if owner != nil {
			out.Source = &model.EnvVarSource{Kind: owner.Kind, Name: owner.Name}
		} else {
			out.Source = &model.EnvVarSource{Kind: "Pod", Name: p.Name}
		}
		return out
	}

	switch {
	case e.ValueFrom.SecretKeyRef != nil:
		ref := e.ValueFrom.SecretKeyRef
		out.Source = &model.EnvVarSource{Kind: "secret", Name: ref.Name, Key: ref.Key}
		if reveal {
			if v, ok := c.resolveSecret(ctx, cs, ns, ref.Name, ref.Key, secretCache); ok {
				out.Value = v
			}
		}
	case e.ValueFrom.ConfigMapKeyRef != nil:
		ref := e.ValueFrom.ConfigMapKeyRef
		out.Source = &model.EnvVarSource{Kind: "configMap", Name: ref.Name, Key: ref.Key}
		if reveal {
			if v, ok := c.resolveConfigMap(ctx, cs, ns, ref.Name, ref.Key, configMapCache); ok {
				out.Value = v
			}
		}
	case e.ValueFrom.FieldRef != nil:
		out.Source = &model.EnvVarSource{Kind: "field", Key: e.ValueFrom.FieldRef.FieldPath}
	case e.ValueFrom.ResourceFieldRef != nil:
		ref := e.ValueFrom.ResourceFieldRef
		out.Source = &model.EnvVarSource{Kind: "resource", Name: ref.ContainerName, Key: ref.Resource}
	}
	return out
}

// resolveSecret fetches secret/key, decoding the []byte value to a string. The
// fetched Secret is cached for the duration of one ListPods call.
func (c *Client) resolveSecret(ctx context.Context, cs kubernetes.Interface, ns, name, key string, cache map[string]*corev1.Secret) (string, bool) {
	sec, ok := cache[name]
	if !ok {
		s, err := cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			cache[name] = nil
			return "", false
		}
		sec, cache[name] = s, s
	}
	if sec == nil {
		return "", false
	}
	if v, ok := sec.Data[key]; ok {
		return string(v), true
	}
	if v, ok := sec.StringData[key]; ok {
		return v, true
	}
	return "", false
}

// resolveConfigMap fetches configMap/key. The fetched ConfigMap is cached for the
// duration of one ListPods call.
func (c *Client) resolveConfigMap(ctx context.Context, cs kubernetes.Interface, ns, name, key string, cache map[string]*corev1.ConfigMap) (string, bool) {
	cm, ok := cache[name]
	if !ok {
		m, err := cs.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			cache[name] = nil
			return "", false
		}
		cm, cache[name] = m, m
	}
	if cm == nil {
		return "", false
	}
	if v, ok := cm.Data[key]; ok {
		return v, true
	}
	if v, ok := cm.BinaryData[key]; ok {
		return string(v), true
	}
	return "", false
}

// PodLogs streams a tail of one pod container's logs, reading at most podLogsCap
// bytes. q.Resource.Pod must be set; q.TailLines <= 0 selects defaultTailLines.
func (c *Client) PodLogs(ctx context.Context, q sources.PodLogsQuery) (model.PodLogsResult, error) {
	cs, err := c.clientset()
	if err != nil {
		return model.PodLogsResult{}, err
	}

	ns := q.Resource.Namespace
	pod := q.Resource.Pod
	tail := q.TailLines
	if tail <= 0 {
		tail = defaultTailLines
	}

	req := cs.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{
		Container: q.Container,
		TailLines: &tail,
	})
	rc, err := req.Stream(ctx)
	if err != nil {
		return model.PodLogsResult{}, fmt.Errorf("kubernetes pod logs: %w", err)
	}
	defer rc.Close()

	// Read one byte past the cap so we can tell whether the body was truncated.
	buf, err := io.ReadAll(io.LimitReader(rc, podLogsCap+1))
	if err != nil {
		return model.PodLogsResult{}, fmt.Errorf("kubernetes pod logs read: %w", err)
	}
	truncated := false
	if len(buf) > podLogsCap {
		buf = buf[:podLogsCap]
		truncated = true
	}

	return model.PodLogsResult{
		Pod:       pod,
		Namespace: ns,
		Container: q.Container,
		Lines:     string(buf),
		Truncated: truncated,
	}, nil
}

// eventTime extracts the most meaningful timestamp from an event, preferring the
// last-seen time and falling back through the modern EventTime and the creation
// time. Any of these may be zero on a given event.
func eventTime(ev *corev1.Event) time.Time {
	if !ev.LastTimestamp.Time.IsZero() {
		return ev.LastTimestamp.Time
	}
	if !ev.EventTime.Time.IsZero() {
		return ev.EventTime.Time
	}
	if !ev.FirstTimestamp.Time.IsZero() {
		return ev.FirstTimestamp.Time
	}
	return ev.CreationTimestamp.Time
}

// inRange reports whether t falls inside the half-open window [Start, End). A
// zero bound is treated as unbounded on that side, and a wholly-zero range
// matches everything.
func inRange(t time.Time, r sources.TimeRange) bool {
	if !r.Start.IsZero() && t.Before(r.Start) {
		return false
	}
	if !r.End.IsZero() && !t.Before(r.End) {
		return false
	}
	return true
}

// criticalEventReasons is the set of Kubernetes event Reasons that, while emitted
// as Type=="Warning", represent genuine failures and must escalate to
// SeverityError. The engine's k8s-events detector only opens incident candidates
// at SeverityError or above, so without this escalation these failures — the very
// ones the detector documents (OOMKilled, CrashLoopBackOff, FailedScheduling,
// probe failures, ...) — would never open an incident.
var criticalEventReasons = map[string]bool{
	"OOMKilling":             true,
	"OOMKilled":              true,
	"BackOff":                true,
	"CrashLoopBackOff":       true,
	"Failed":                 true,
	"FailedMount":            true,
	"FailedScheduling":       true,
	"FailedCreatePodSandBox": true,
	"Evicted":                true,
	"Unhealthy":              true,
	"FailedKillPod":          true,
	"ErrImagePull":           true,
	"ImagePullBackOff":       true,
}

// severityFromEvent maps a core/v1 Event to a neutral severity. Type=="Warning"
// events normally map to SeverityWarning, but known-critical Reasons (see
// criticalEventReasons) escalate to SeverityError so the engine's detector can
// open incident candidates from them. Non-Warning events map to SeverityInfo.
func severityFromEvent(eventType, reason string) model.Severity {
	if eventType != "Warning" {
		return model.SeverityInfo
	}
	if criticalEventReasons[reason] {
		return model.SeverityError
	}
	return model.SeverityWarning
}

// ListConfigMaps enumerates ConfigMaps in namespace ("" = all namespaces),
// returning each one's identity and the sorted union of its Data and BinaryData
// keys. Values are not exposed by a listing.
func (c *Client) ListConfigMaps(ctx context.Context, namespace string) ([]model.ConfigMapRef, error) {
	cs, err := c.clientset()
	if err != nil {
		return nil, err
	}
	list, err := cs.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes configmaps: %w", err)
	}
	out := make([]model.ConfigMapRef, 0, len(list.Items))
	for i := range list.Items {
		cm := &list.Items[i]
		out = append(out, model.ConfigMapRef{
			Cluster:   c.cluster,
			Namespace: cm.Namespace,
			Name:      cm.Name,
			Keys:      configMapKeys(cm),
		})
	}
	return out, nil
}

// GetConfigMap returns a single ConfigMap's data. Binary (BinaryData) entries are
// surfaced with the "<binary>" sentinel rather than raw bytes.
func (c *Client) GetConfigMap(ctx context.Context, ref model.ResourceRef) (model.ConfigMapDetail, error) {
	cs, err := c.clientset()
	if err != nil {
		return model.ConfigMapDetail{}, err
	}
	cm, err := cs.CoreV1().ConfigMaps(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return model.ConfigMapDetail{}, fmt.Errorf("kubernetes configmap: %w", err)
	}
	data := make(map[string]string, len(cm.Data)+len(cm.BinaryData))
	for k, v := range cm.Data {
		data[k] = v
	}
	for k := range cm.BinaryData {
		data[k] = "<binary>"
	}
	return model.ConfigMapDetail{
		Cluster:   c.cluster,
		Namespace: cm.Namespace,
		Name:      cm.Name,
		Data:      data,
	}, nil
}

// ListSecrets enumerates Secrets in namespace ("" = all namespaces), returning
// identity, type, sorted keys, and IsTLS. For kubernetes.io/tls secrets it also
// parses the leaf certificate from tls.crt into public CertInfo. A listing never
// exposes secret values.
func (c *Client) ListSecrets(ctx context.Context, namespace string) ([]model.SecretRef, error) {
	cs, err := c.clientset()
	if err != nil {
		return nil, err
	}
	list, err := cs.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes secrets: %w", err)
	}
	now := time.Now()
	out := make([]model.SecretRef, 0, len(list.Items))
	for i := range list.Items {
		sec := &list.Items[i]
		isTLS := sec.Type == corev1.SecretTypeTLS
		ref := model.SecretRef{
			Cluster:   c.cluster,
			Namespace: sec.Namespace,
			Name:      sec.Name,
			Type:      string(sec.Type),
			Keys:      secretKeys(sec),
			IsTLS:     isTLS,
		}
		if isTLS {
			ref.Cert = parseCertFromSecret(sec, now)
		}
		out = append(out, ref)
	}
	return out, nil
}

// GetSecret returns a single Secret's entries. Entry values are revealed only when
// q.Reveal is set, with one exception: PUBLIC certificate entries (tls.crt /
// ca.crt) are always shown — they are not secret. The private key (tls.key) and
// every other entry are masked unless reveal is set. For a TLS secret the public
// certificate metadata is parsed and returned regardless of reveal.
func (c *Client) GetSecret(ctx context.Context, q sources.SecretQuery) (model.SecretDetail, error) {
	cs, err := c.clientset()
	if err != nil {
		return model.SecretDetail{}, err
	}
	sec, err := cs.CoreV1().Secrets(q.Resource.Namespace).Get(ctx, q.Resource.Name, metav1.GetOptions{})
	if err != nil {
		return model.SecretDetail{}, fmt.Errorf("kubernetes secret: %w", err)
	}

	keys := secretKeys(sec)
	entries := make([]model.SecretEntry, 0, len(keys))
	for _, k := range keys {
		raw := string(sec.Data[k])
		entry := model.SecretEntry{Key: k, IsCert: isCertKey(k, raw)}
		switch {
		case q.Reveal:
			entry.Value = raw
		case entry.IsCert && k != corev1.TLSPrivateKeyKey:
			// Public certificate material (tls.crt / ca.crt) is not secret; show it
			// even without reveal. tls.key is excluded — it is private even if it
			// happens to look PEM-shaped.
			entry.Value = raw
		default:
			entry.Masked = true
		}
		entries = append(entries, entry)
	}

	detail := model.SecretDetail{
		Cluster:   c.cluster,
		Namespace: sec.Namespace,
		Name:      sec.Name,
		Type:      string(sec.Type),
		Entries:   entries,
	}
	if sec.Type == corev1.SecretTypeTLS {
		detail.Cert = parseCertFromSecret(sec, time.Now())
	}
	return detail, nil
}

// configMapKeys returns the sorted union of a ConfigMap's Data and BinaryData keys.
func configMapKeys(cm *corev1.ConfigMap) []string {
	keys := make([]string, 0, len(cm.Data)+len(cm.BinaryData))
	for k := range cm.Data {
		keys = append(keys, k)
	}
	for k := range cm.BinaryData {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// secretKeys returns a Secret's data keys, sorted.
func secretKeys(sec *corev1.Secret) []string {
	keys := make([]string, 0, len(sec.Data))
	for k := range sec.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// isCertKey reports whether a Secret entry is a PEM certificate: the well-known
// tls.crt / ca.crt keys, or any value whose decoded text contains a CERTIFICATE
// PEM block. tls.key is never treated as a public cert here.
func isCertKey(key, value string) bool {
	if key == corev1.TLSCertKey || key == corev1.ServiceAccountRootCAKey {
		return true
	}
	if key == corev1.TLSPrivateKeyKey {
		return false
	}
	return utf8.ValidString(value) && strings.Contains(value, "-----BEGIN CERTIFICATE-----")
}

// parseCertFromSecret parses the leaf certificate from a TLS secret's tls.crt
// (falling back to ca.crt), returning nil — never erroring — when no certificate
// can be parsed, so a malformed secret degrades gracefully rather than failing the
// whole call. now is injected so callers control the Expired/ExpiresInDays clock.
func parseCertFromSecret(sec *corev1.Secret, now time.Time) *model.CertInfo {
	for _, key := range []string{corev1.TLSCertKey, corev1.ServiceAccountRootCAKey} {
		if pemBytes, ok := sec.Data[key]; ok {
			if info := parseCertPEM(pemBytes, now); info != nil {
				return info
			}
		}
	}
	return nil
}

// parseCertPEM decodes the first CERTIFICATE PEM block in pemBytes and fills a
// CertInfo. It returns nil on any decode/parse failure (graceful degradation).
func parseCertPEM(pemBytes []byte, now time.Time) *model.CertInfo {
	rest := pemBytes
	var der []byte
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			der = block.Bytes
			break
		}
	}
	if der == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil
	}
	info := &model.CertInfo{
		SubjectCN:     cert.Subject.CommonName,
		IssuerCN:      cert.Issuer.CommonName,
		NotBefore:     cert.NotBefore.UTC(),
		NotAfter:      cert.NotAfter.UTC(),
		DNSNames:      cert.DNSNames,
		IsCA:          cert.IsCA,
		KeyAlgorithm:  cert.PublicKeyAlgorithm.String(),
		Expired:       now.After(cert.NotAfter),
		ExpiresInDays: int(math.Floor(cert.NotAfter.Sub(now).Hours() / 24)),
	}
	if cert.SerialNumber != nil {
		info.Serial = cert.SerialNumber.String()
	}
	return info
}

var _ sources.ClusterSource = (*Client)(nil)
