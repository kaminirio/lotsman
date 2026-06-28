package kubernetes

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// makeEvent constructs a core/v1 Event for testing. ts is used as LastTimestamp.
func makeEvent(namespace, name, reason, msg, evType, objKind, objName string, ts time.Time) corev1.Event {
	return corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      objKind,
			Name:      objName,
			Namespace: namespace,
		},
		Reason:        reason,
		Message:       msg,
		Type:          evType,
		LastTimestamp: metav1.NewTime(ts),
	}
}

func makeDeployment(namespace, name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func makeStatefulSet(namespace, name string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func makeDaemonSet(namespace, name string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

// ctrlOwnerRef builds a controller (Controller==true) ownerReference.
func ctrlOwnerRef(kind, name string) metav1.OwnerReference {
	t := true
	return metav1.OwnerReference{Kind: kind, Name: name, Controller: &t}
}

// makeReplicaSet builds a ReplicaSet, optionally owned by the named Deployment
// (owner=="" leaves it ownerless).
func makeReplicaSet(namespace, name, deployment string) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if deployment != "" {
		rs.OwnerReferences = []metav1.OwnerReference{ctrlOwnerRef("Deployment", deployment)}
	}
	return rs
}

func TestEvents_FiltersByTimeRange(t *testing.T) {
	// Events outside the range must be dropped; events inside must be kept.
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	withinRange := base.Add(5 * time.Minute)
	beforeRange := base.Add(-10 * time.Minute)
	atEnd := base.Add(time.Hour) // exclusive upper bound

	evIn := makeEvent("payments", "ev-in", "BackOff", "container restarting", "Warning", "Pod", "api-abc", withinRange)
	evBefore := makeEvent("payments", "ev-before", "Pulled", "pulled image", "Normal", "Pod", "api-def", beforeRange)
	evAtEnd := makeEvent("payments", "ev-at-end", "Failed", "liveness probe failed", "Warning", "Pod", "api-xyz", atEnd)

	cs := fake.NewSimpleClientset(&evIn, &evBefore, &evAtEnd)
	c := newWithClient("prod", cs)

	sigs, err := c.Events(context.Background(), sources.EventQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "payments"},
		Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal (within range), got %d", len(sigs))
	}
	s := sigs[0]
	if s.Title != "BackOff" {
		t.Errorf("Title: got %q, want BackOff", s.Title)
	}
	if s.Message != "container restarting" {
		t.Errorf("Message: got %q", s.Message)
	}
}

func TestEvents_SeverityMapping(t *testing.T) {
	// Critical "Warning" reasons (OOMKilled, ...) must escalate to SeverityError so
	// the engine's k8s-events detector can open candidates from them; benign
	// "Warning" reasons stay SeverityWarning; "Normal" maps to SeverityInfo.
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	evCrit := makeEvent("ns", "ev-crit", "OOMKilling", "OOM", "Warning", "Pod", "pod-a", base.Add(time.Minute))
	evWarn := makeEvent("ns", "ev-warn", "Pulling", "pulling image", "Warning", "Pod", "pod-c", base.Add(2*time.Minute))
	evNorm := makeEvent("ns", "ev-norm", "Scheduled", "scheduled", "Normal", "Pod", "pod-b", base.Add(3*time.Minute))

	cs := fake.NewSimpleClientset(&evCrit, &evWarn, &evNorm)
	c := newWithClient("prod", cs)

	sigs, err := c.Events(context.Background(), sources.EventQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "ns"},
		Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 3 {
		t.Fatalf("expected 3 signals, got %d", len(sigs))
	}

	// Map by Title to avoid ordering assumptions.
	bySeverity := map[string]model.Severity{}
	for _, s := range sigs {
		bySeverity[s.Title] = s.Severity
	}
	if bySeverity["OOMKilling"] != model.SeverityError {
		t.Errorf("OOMKilling severity: got %v, want Error", bySeverity["OOMKilling"])
	}
	if bySeverity["Pulling"] != model.SeverityWarning {
		t.Errorf("Pulling severity: got %v, want Warning", bySeverity["Pulling"])
	}
	if bySeverity["Scheduled"] != model.SeverityInfo {
		t.Errorf("Scheduled severity: got %v, want Info", bySeverity["Scheduled"])
	}
}

func TestEvents_CriticalReasonsOpenDetectorCandidate(t *testing.T) {
	// End-to-end: an OOMKilled-class Warning event must reach SeverityError, which
	// is the threshold the engine's k8s-events detector requires to open a
	// candidate (internal/engine/detector/kubernetes.go).
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, reason := range []string{
		"OOMKilling", "OOMKilled", "BackOff", "CrashLoopBackOff", "Failed",
		"FailedMount", "FailedScheduling", "FailedCreatePodSandBox", "Evicted",
		"Unhealthy", "FailedKillPod", "ErrImagePull", "ImagePullBackOff",
	} {
		ev := makeEvent("ns", "ev", reason, "msg", "Warning", "Pod", "pod-a", base.Add(time.Minute))
		cs := fake.NewSimpleClientset(&ev)
		c := newWithClient("prod", cs)

		sigs, err := c.Events(context.Background(), sources.EventQuery{
			Resource: model.ResourceRef{Cluster: "prod", Namespace: "ns"},
			Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
		})
		if err != nil {
			t.Fatalf("reason %q: unexpected error: %v", reason, err)
		}
		if len(sigs) != 1 {
			t.Fatalf("reason %q: expected 1 signal, got %d", reason, len(sigs))
		}
		if sigs[0].Severity < model.SeverityError {
			t.Errorf("reason %q: severity %v is below SeverityError; detector would drop it", reason, sigs[0].Severity)
		}
	}
}

func TestEvents_PodRefSetForPodEvents(t *testing.T) {
	// When InvolvedObject.Kind == "Pod", the signal's Resource.Pod must be set to
	// the object name; for non-Pod objects Pod must be empty.
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	podEvent := makeEvent("ns", "ev-pod", "BackOff", "msg", "Warning", "Pod", "my-pod-abc", base.Add(time.Minute))
	deployEvent := makeEvent("ns", "ev-deploy", "ScalingReplicaSet", "msg", "Normal", "Deployment", "my-deploy", base.Add(2*time.Minute))

	cs := fake.NewSimpleClientset(&podEvent, &deployEvent)
	c := newWithClient("prod", cs)

	sigs, err := c.Events(context.Background(), sources.EventQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "ns"},
		Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 2 {
		t.Fatalf("expected 2 signals, got %d", len(sigs))
	}

	byTitle := map[string]model.Signal{}
	for _, s := range sigs {
		byTitle[s.Title] = s
	}

	podSig := byTitle["BackOff"]
	if podSig.Resource.Pod != "my-pod-abc" {
		t.Errorf("Pod event Resource.Pod: got %q, want my-pod-abc", podSig.Resource.Pod)
	}
	if podSig.Resource.Kind != "Pod" {
		t.Errorf("Pod event Resource.Kind: got %q, want Pod", podSig.Resource.Kind)
	}

	deploySig := byTitle["ScalingReplicaSet"]
	if deploySig.Resource.Pod != "" {
		t.Errorf("Deployment event Resource.Pod should be empty, got %q", deploySig.Resource.Pod)
	}
	if deploySig.Resource.Kind != "Deployment" {
		t.Errorf("Deployment event Resource.Kind: got %q, want Deployment", deploySig.Resource.Kind)
	}
}

func TestEvents_ResourceRefFields(t *testing.T) {
	// The signal's ResourceRef must carry cluster, namespace, kind, and name from
	// the involved object.
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	ev := makeEvent("payments", "ev1", "BackOff", "msg", "Warning", "Pod", "api-pod-123", base.Add(time.Minute))

	cs := fake.NewSimpleClientset(&ev)
	c := newWithClient("my-cluster", cs)

	sigs, err := c.Events(context.Background(), sources.EventQuery{
		Resource: model.ResourceRef{Cluster: "my-cluster", Namespace: "payments"},
		Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	r := sigs[0].Resource
	if r.Cluster != "my-cluster" {
		t.Errorf("Resource.Cluster: got %q, want my-cluster", r.Cluster)
	}
	if r.Namespace != "payments" {
		t.Errorf("Resource.Namespace: got %q, want payments", r.Namespace)
	}
	if r.Name != "api-pod-123" {
		t.Errorf("Resource.Name: got %q, want api-pod-123", r.Name)
	}
	if r.Kind != "Pod" {
		t.Errorf("Resource.Kind: got %q, want Pod", r.Kind)
	}
}

func TestEvents_SignalKindAndSource(t *testing.T) {
	// Kind must be SignalK8sEvent; Source must be "kubernetes".
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	ev := makeEvent("ns", "ev1", "Reason", "msg", "Normal", "Pod", "pod-1", base.Add(time.Minute))

	cs := fake.NewSimpleClientset(&ev)
	c := newWithClient("c", cs)

	sigs, err := c.Events(context.Background(), sources.EventQuery{
		Resource: model.ResourceRef{Cluster: "c", Namespace: "ns"},
		Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	if sigs[0].Kind != model.SignalK8sEvent {
		t.Errorf("Kind: got %q, want %q", sigs[0].Kind, model.SignalK8sEvent)
	}
	if sigs[0].Source != "kubernetes" {
		t.Errorf("Source: got %q, want kubernetes", sigs[0].Source)
	}
}

func TestEvents_EmptyRange_MatchesAll(t *testing.T) {
	// A zero TimeRange must include all events (unbounded on both sides).
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	ev1 := makeEvent("ns", "ev1", "R1", "m1", "Normal", "Pod", "p1", base.Add(-1000*time.Hour))
	ev2 := makeEvent("ns", "ev2", "R2", "m2", "Warning", "Pod", "p2", base.Add(1000*time.Hour))

	cs := fake.NewSimpleClientset(&ev1, &ev2)
	c := newWithClient("c", cs)

	sigs, err := c.Events(context.Background(), sources.EventQuery{
		Resource: model.ResourceRef{Cluster: "c", Namespace: "ns"},
		Range:    sources.TimeRange{}, // zero range = unbounded
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 2 {
		t.Errorf("expected 2 signals, got %d", len(sigs))
	}
}

func TestListWorkloads_ReturnsDeployments(t *testing.T) {
	d1 := makeDeployment("payments", "api-server")
	d2 := makeDeployment("payments", "worker")

	cs := fake.NewSimpleClientset(d1, d2)
	c := newWithClient("prod", cs)

	refs, err := c.ListWorkloads(context.Background(), "payments")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deployRefs := filterByKind(refs, "Deployment")
	if len(deployRefs) != 2 {
		t.Errorf("expected 2 Deployment refs, got %d", len(deployRefs))
	}
	for _, r := range deployRefs {
		if r.Cluster != "prod" {
			t.Errorf("Cluster: got %q, want prod", r.Cluster)
		}
		if r.Namespace != "payments" {
			t.Errorf("Namespace: got %q, want payments", r.Namespace)
		}
	}

	names := refNames(deployRefs)
	if !containsAll(names, "api-server", "worker") {
		t.Errorf("expected api-server and worker in deployment refs, got %v", names)
	}
}

func TestListWorkloads_ReturnsStatefulSets(t *testing.T) {
	ss := makeStatefulSet("data", "postgres")
	cs := fake.NewSimpleClientset(ss)
	c := newWithClient("prod", cs)

	refs, err := c.ListWorkloads(context.Background(), "data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ssRefs := filterByKind(refs, "StatefulSet")
	if len(ssRefs) != 1 {
		t.Fatalf("expected 1 StatefulSet ref, got %d", len(ssRefs))
	}
	if ssRefs[0].Name != "postgres" {
		t.Errorf("Name: got %q, want postgres", ssRefs[0].Name)
	}
	if ssRefs[0].Kind != "StatefulSet" {
		t.Errorf("Kind: got %q, want StatefulSet", ssRefs[0].Kind)
	}
}

func TestListWorkloads_ReturnsDaemonSets(t *testing.T) {
	ds := makeDaemonSet("kube-system", "node-exporter")
	cs := fake.NewSimpleClientset(ds)
	c := newWithClient("prod", cs)

	refs, err := c.ListWorkloads(context.Background(), "kube-system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dsRefs := filterByKind(refs, "DaemonSet")
	if len(dsRefs) != 1 {
		t.Fatalf("expected 1 DaemonSet ref, got %d", len(dsRefs))
	}
	if dsRefs[0].Name != "node-exporter" {
		t.Errorf("Name: got %q, want node-exporter", dsRefs[0].Name)
	}
}

func TestListWorkloads_MixedKinds(t *testing.T) {
	// All three workload kinds in a single namespace must all appear.
	d := makeDeployment("mixed", "api")
	ss := makeStatefulSet("mixed", "db")
	ds := makeDaemonSet("mixed", "log-shipper")

	cs := fake.NewSimpleClientset(d, ss, ds)
	c := newWithClient("prod", cs)

	refs, err := c.ListWorkloads(context.Background(), "mixed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filterByKind(refs, "Deployment")) != 1 {
		t.Errorf("expected 1 Deployment")
	}
	if len(filterByKind(refs, "StatefulSet")) != 1 {
		t.Errorf("expected 1 StatefulSet")
	}
	if len(filterByKind(refs, "DaemonSet")) != 1 {
		t.Errorf("expected 1 DaemonSet")
	}
}

func TestListWorkloads_AllNamespaces(t *testing.T) {
	// namespace=="" must list workloads across all namespaces.
	d1 := makeDeployment("ns-a", "svc-a")
	d2 := makeDeployment("ns-b", "svc-b")

	cs := fake.NewSimpleClientset(d1, d2)
	c := newWithClient("prod", cs)

	refs, err := c.ListWorkloads(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	deployRefs := filterByKind(refs, "Deployment")
	if len(deployRefs) != 2 {
		t.Errorf("expected 2 Deployment refs across all namespaces, got %d", len(deployRefs))
	}
}

func TestListWorkloads_EmptyCluster(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := newWithClient("prod", cs)

	refs, err := c.ListWorkloads(context.Background(), "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refs for empty cluster, got %d", len(refs))
	}
}

func TestListPods_EnvMappingAndStatus(t *testing.T) {
	// A pod with three env vars: a literal, a secretKeyRef, and a configMapKeyRef.
	// With Reveal=false the secret/configMap values stay empty, only Source is set.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-server-abc123",
			Namespace: "payments",
			Labels:    map[string]string{"app": "api-server"},
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{{
				Name:  "api",
				Image: "ghcr.io/acme/api:v1.2.3",
				Env: []corev1.EnvVar{
					{Name: "LOG_LEVEL", Value: "info"},
					{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "db-creds"},
							Key:                  "password",
						},
					}},
					{Name: "FEATURE_FLAGS", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
							Key:                  "flags",
						},
					}},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "api", Ready: true, RestartCount: 2, State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{},
				}},
			},
		},
	}

	cs := fake.NewSimpleClientset(pod)
	c := newWithClient("prod", cs)

	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "payments"},
		Reveal:   false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	p := pods[0]
	if p.Name != "api-server-abc123" || p.Namespace != "payments" {
		t.Errorf("identity: got %q/%q", p.Name, p.Namespace)
	}
	if p.Phase != "Running" {
		t.Errorf("Phase: got %q, want Running", p.Phase)
	}
	if !p.Ready {
		t.Errorf("Ready: got false, want true")
	}
	if p.Restarts != 2 {
		t.Errorf("Restarts: got %d, want 2", p.Restarts)
	}
	if p.Node != "node-1" {
		t.Errorf("Node: got %q, want node-1", p.Node)
	}
	if len(p.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(p.Containers))
	}
	ct := p.Containers[0]
	if ct.Name != "api" || ct.Image != "ghcr.io/acme/api:v1.2.3" {
		t.Errorf("container: got %q/%q", ct.Name, ct.Image)
	}
	if !ct.Ready || ct.State != "running" || ct.Reason != "" || ct.RestartCount != 2 {
		t.Errorf("container status: got ready=%v state=%q reason=%q restarts=%d, want ready=true state=running reason= restarts=2",
			ct.Ready, ct.State, ct.Reason, ct.RestartCount)
	}

	byName := map[string]model.ContainerEnvVar{}
	for _, e := range ct.Env {
		byName[e.Name] = e
	}
	// This pod has no controller ownerRef, so the literal env's Source falls back
	// to the pod itself.
	if got := byName["LOG_LEVEL"]; got.Value != "info" ||
		got.Source == nil || got.Source.Kind != "Pod" || got.Source.Name != "api-server-abc123" || got.Source.Key != "" {
		t.Errorf("LOG_LEVEL: got value=%q source=%+v, want value=info source={Pod api-server-abc123}", got.Value, got.Source)
	}
	pw := byName["DB_PASSWORD"]
	if pw.Source == nil || pw.Source.Kind != "secret" || pw.Source.Name != "db-creds" || pw.Source.Key != "password" {
		t.Errorf("DB_PASSWORD source: got %+v", pw.Source)
	}
	if pw.Value != "" {
		t.Errorf("DB_PASSWORD value should be empty when Reveal=false, got %q", pw.Value)
	}
	ff := byName["FEATURE_FLAGS"]
	if ff.Source == nil || ff.Source.Kind != "configMap" || ff.Source.Name != "app-config" || ff.Source.Key != "flags" {
		t.Errorf("FEATURE_FLAGS source: got %+v", ff.Source)
	}
	if ff.Value != "" {
		t.Errorf("FEATURE_FLAGS value should be empty when Reveal=false, got %q", ff.Value)
	}
}

// A two-container pod (a healthy app + a crash-looping sidecar) must report each
// container's status independently — the per-container state/reason is what the
// UI's Lens-style status squares render. Spec/status order also differs to prove
// statuses are matched by name, not position.
func TestListPods_PerContainerStatus(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sidecar-app-xyz", Namespace: "shop"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "nginx:1.27"},
				{Name: "sidecar", Image: "busybox:1.36"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				// Reverse order on purpose.
				{Name: "sidecar", Ready: false, RestartCount: 7, State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
				}},
				{Name: "app", Ready: true, State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{},
				}},
			},
		},
	}

	cs := fake.NewSimpleClientset(pod)
	c := newWithClient("prod", cs)
	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "shop"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 || len(pods[0].Containers) != 2 {
		t.Fatalf("expected 1 pod with 2 containers, got %d pods", len(pods))
	}
	byName := map[string]model.Container{}
	for _, ct := range pods[0].Containers {
		byName[ct.Name] = ct
	}
	if app := byName["app"]; !app.Ready || app.State != "running" || app.Reason != "" || app.RestartCount != 0 {
		t.Errorf("app: got %+v, want ready=true state=running reason= restarts=0", app)
	}
	if sc := byName["sidecar"]; sc.Ready || sc.State != "waiting" || sc.Reason != "CrashLoopBackOff" || sc.RestartCount != 7 {
		t.Errorf("sidecar: got %+v, want ready=false state=waiting reason=CrashLoopBackOff restarts=7", sc)
	}
}

// WorkloadHistory for a Deployment reconstructs the rollout from its owned
// ReplicaSets: newest revision first, each carrying that revision's image, with
// the live revision flagged Current. ReplicaSets not owned by the Deployment are
// ignored, and revision order is by the deployment.kubernetes.io/revision
// annotation (not creation order).
func TestWorkloadHistory_DeploymentRevisions(t *testing.T) {
	depUID := "dep-uid-123"
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "checkout",
			Namespace:   "demo",
			UID:         "dep-uid-123",
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
		},
	}
	ownedBy := []metav1.OwnerReference{{Kind: "Deployment", Name: "checkout", UID: "dep-uid-123"}}
	rsOld := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "checkout-old",
			Namespace:       "demo",
			OwnerReferences: ownedBy,
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": "1",
				"kubernetes.io/change-cause":        "initial rollout",
			},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "checkout", Image: "ghcr.io/acme/checkout:v1.0.0"}}},
		}},
	}
	rsNew := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "checkout-new",
			Namespace:       "demo",
			OwnerReferences: ownedBy,
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": "2",
				"kubernetes.io/change-cause":        "bump to v1.1.0",
			},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "checkout", Image: "ghcr.io/acme/checkout:v1.1.0"}}},
		}},
	}
	// An unrelated ReplicaSet in the same namespace that must be excluded.
	rsOther := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "other-abc",
			Namespace:       "demo",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "other", UID: "other-uid"}},
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "5"},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "other", Image: "other:latest"}}},
		}},
	}
	_ = depUID

	cs := fake.NewSimpleClientset(dep, rsOld, rsNew, rsOther)
	c := newWithClient("prod", cs)

	revs, err := c.WorkloadHistory(context.Background(), model.ResourceRef{
		Cluster: "prod", Namespace: "demo", Kind: "Deployment", Name: "checkout",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions (owned only), got %d: %+v", len(revs), revs)
	}
	// Newest first.
	if revs[0].Revision != 2 || len(revs[0].Images) != 1 || revs[0].Images[0] != "ghcr.io/acme/checkout:v1.1.0" {
		t.Errorf("rev[0]: got %+v, want revision 2 image v1.1.0", revs[0])
	}
	if !revs[0].Current {
		t.Errorf("rev[0] should be Current (matches deployment revision 2)")
	}
	if revs[0].ChangeCause != "bump to v1.1.0" {
		t.Errorf("rev[0] change cause: got %q", revs[0].ChangeCause)
	}
	if revs[1].Revision != 1 || revs[1].Images[0] != "ghcr.io/acme/checkout:v1.0.0" {
		t.Errorf("rev[1]: got %+v, want revision 1 image v1.0.0", revs[1])
	}
	if revs[1].Current {
		t.Errorf("rev[1] should not be Current")
	}
}

func TestListPods_LiteralEnvAttributedToOwner(t *testing.T) {
	// A pod owned (Pod -> ReplicaSet -> Deployment) by a Deployment, with a literal
	// env, a secretKeyRef env, and a configMapKeyRef env. The literal env's Source
	// must be the owning Deployment; the valueFrom sources are unchanged.
	ns := "payments"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "api-server-rs-abc123",
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{ctrlOwnerRef("ReplicaSet", "api-server-rs")},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "api",
				Image: "img",
				Env: []corev1.EnvVar{
					{Name: "LOG_LEVEL", Value: "info"},
					{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "db-creds"},
							Key:                  "password",
						},
					}},
					{Name: "FEATURE_FLAGS", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
							Key:                  "flags",
						},
					}},
				},
			}},
		},
	}
	rs := makeReplicaSet(ns, "api-server-rs", "api-server")

	cs := fake.NewSimpleClientset(pod, rs)
	c := newWithClient("prod", cs)

	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: ns},
		Reveal:   false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	if got := pods[0].Owner; got == nil || got.Kind != "Deployment" || got.Name != "api-server" {
		t.Fatalf("owner: got %+v, want {Deployment api-server}", got)
	}

	byName := map[string]model.ContainerEnvVar{}
	for _, e := range pods[0].Containers[0].Env {
		byName[e.Name] = e
	}

	// (a) literal env attributed to the owning Deployment, no Key.
	ll := byName["LOG_LEVEL"]
	if ll.Value != "info" ||
		ll.Source == nil || ll.Source.Kind != "Deployment" || ll.Source.Name != "api-server" || ll.Source.Key != "" {
		t.Errorf("LOG_LEVEL: got value=%q source=%+v, want value=info source={Deployment api-server}", ll.Value, ll.Source)
	}
	// (b) secret source unchanged.
	pw := byName["DB_PASSWORD"]
	if pw.Source == nil || pw.Source.Kind != "secret" || pw.Source.Name != "db-creds" || pw.Source.Key != "password" {
		t.Errorf("DB_PASSWORD source: got %+v, want {secret db-creds password}", pw.Source)
	}
	// (c) configMap source unchanged.
	ff := byName["FEATURE_FLAGS"]
	if ff.Source == nil || ff.Source.Kind != "configMap" || ff.Source.Name != "app-config" || ff.Source.Key != "flags" {
		t.Errorf("FEATURE_FLAGS source: got %+v, want {configMap app-config flags}", ff.Source)
	}
}

func TestListPods_LiteralEnvBarePodAttributedToPod(t *testing.T) {
	// A bare pod (no controller ownerRef) attributes its literal env to the pod
	// itself, so provenance is always present.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "standalone", Namespace: "ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "main",
				Image: "img",
				Env:   []corev1.EnvVar{{Name: "LOG_LEVEL", Value: "debug"}},
			}},
		},
	}

	cs := fake.NewSimpleClientset(pod)
	c := newWithClient("prod", cs)

	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "ns"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	if pods[0].Owner != nil {
		t.Fatalf("bare pod owner: got %+v, want nil", pods[0].Owner)
	}
	got := pods[0].Containers[0].Env[0]
	if got.Value != "debug" ||
		got.Source == nil || got.Source.Kind != "Pod" || got.Source.Name != "standalone" || got.Source.Key != "" {
		t.Errorf("bare-pod LOG_LEVEL: got value=%q source=%+v, want value=debug source={Pod standalone}", got.Value, got.Source)
	}
}

func TestListPods_RevealResolvesSecretAndConfigMap(t *testing.T) {
	// With Reveal=true the secretKeyRef/configMapKeyRef env values are resolved
	// from the seeded Secret/ConfigMap. Secret data is []byte (client-go decodes
	// base64 into Data), ConfigMap data is string.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-xyz", Namespace: "payments", Labels: map[string]string{"app": "api"}},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "api",
				Image: "img",
				Env: []corev1.EnvVar{
					{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "db-creds"},
							Key:                  "password",
						},
					}},
					{Name: "FLAGS", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
							Key:                  "flags",
						},
					}},
				},
			}},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "payments"},
		Data:       map[string][]byte{"password": []byte("s3cr3t")},
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "payments"},
		Data:       map[string]string{"flags": "a,b,c"},
	}

	cs := fake.NewSimpleClientset(pod, secret, cm)
	c := newWithClient("prod", cs)

	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "payments"},
		Reveal:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	byName := map[string]model.ContainerEnvVar{}
	for _, e := range pods[0].Containers[0].Env {
		byName[e.Name] = e
	}
	if got := byName["DB_PASSWORD"]; got.Value != "s3cr3t" {
		t.Errorf("revealed DB_PASSWORD: got %q, want s3cr3t", got.Value)
	}
	if byName["DB_PASSWORD"].Source == nil {
		t.Errorf("revealed DB_PASSWORD should retain Source provenance")
	}
	if got := byName["FLAGS"]; got.Value != "a,b,c" {
		t.Errorf("revealed FLAGS: got %q, want a,b,c", got.Value)
	}
}

func TestListPods_NarrowToWorkload(t *testing.T) {
	// Resource.Name narrows to the workload's pods via label OR name-prefix.
	pMatchPrefix := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-server-aaa", Namespace: "ns"}}
	pMatchLabel := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "totally-different", Namespace: "ns", Labels: map[string]string{"app.kubernetes.io/name": "api-server"}}}
	pOther := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker-bbb", Namespace: "ns"}}

	cs := fake.NewSimpleClientset(pMatchPrefix, pMatchLabel, pOther)
	c := newWithClient("prod", cs)

	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "ns", Name: "api-server"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("expected 2 pods narrowed to api-server, got %d", len(pods))
	}
	names := map[string]bool{}
	for _, p := range pods {
		names[p.Name] = true
	}
	if !names["api-server-aaa"] || !names["totally-different"] {
		t.Errorf("narrowed set wrong: %v", names)
	}
	if names["worker-bbb"] {
		t.Errorf("worker-bbb should not match api-server narrowing")
	}
}

func TestListPods_OwnerResolution(t *testing.T) {
	// Four owner shapes resolved by ListPods:
	//   - Deployment-owned pod: Pod -> ReplicaSet -> Deployment.
	//   - StatefulSet-owned pod: reported directly.
	//   - bare pod (no ownerRefs): Owner nil.
	//   - ReplicaSet-owned pod whose RS has no Deployment owner: falls back to RS.
	ns := "demo"

	deployPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "api-server-rs-aaa", Namespace: ns,
		OwnerReferences: []metav1.OwnerReference{ctrlOwnerRef("ReplicaSet", "api-server-rs")},
	}}
	deployRS := makeReplicaSet(ns, "api-server-rs", "api-server")

	ssPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "postgres-0", Namespace: ns,
		OwnerReferences: []metav1.OwnerReference{ctrlOwnerRef("StatefulSet", "postgres")},
	}}

	barePod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "standalone", Namespace: ns}}

	orphanPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "legacy-rs-bbb", Namespace: ns,
		OwnerReferences: []metav1.OwnerReference{ctrlOwnerRef("ReplicaSet", "legacy-rs")},
	}}
	orphanRS := makeReplicaSet(ns, "legacy-rs", "") // no Deployment owner

	cs := fake.NewSimpleClientset(deployPod, deployRS, ssPod, barePod, orphanPod, orphanRS)
	c := newWithClient("prod", cs)

	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: ns},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	owners := map[string]*model.WorkloadRef{}
	for _, p := range pods {
		owners[p.Name] = p.Owner
	}

	if got := owners["api-server-rs-aaa"]; got == nil || got.Kind != "Deployment" || got.Name != "api-server" {
		t.Errorf("Deployment-owned pod: got %+v, want {Deployment api-server}", got)
	}
	if got := owners["postgres-0"]; got == nil || got.Kind != "StatefulSet" || got.Name != "postgres" {
		t.Errorf("StatefulSet-owned pod: got %+v, want {StatefulSet postgres}", got)
	}
	if got := owners["standalone"]; got != nil {
		t.Errorf("bare pod: got %+v, want nil", got)
	}
	if got := owners["legacy-rs-bbb"]; got == nil || got.Kind != "ReplicaSet" || got.Name != "legacy-rs" {
		t.Errorf("orphan-RS pod: got %+v, want {ReplicaSet legacy-rs} fallback", got)
	}
}

func TestListPods_OwnerResolution_AllNamespaces(t *testing.T) {
	// Regression: in an all-namespaces listing the query namespace is "", but the
	// ReplicaSet -> Deployment hop must use each pod's OWN namespace. Otherwise the
	// RS Get hits namespace "" and falls back to reporting the ReplicaSet.
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "checkout-rs-aaa", Namespace: "demo",
		OwnerReferences: []metav1.OwnerReference{ctrlOwnerRef("ReplicaSet", "checkout-rs")},
	}}
	rs := makeReplicaSet("demo", "checkout-rs", "checkout")

	cs := fake.NewSimpleClientset(pod, rs)
	c := newWithClient("prod", cs)

	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: ""}, // all namespaces
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("got %d pods, want 1", len(pods))
	}
	if got := pods[0].Owner; got == nil || got.Kind != "Deployment" || got.Name != "checkout" {
		t.Errorf("all-namespaces owner: got %+v, want {Deployment checkout}", got)
	}
}

func TestListPods_OwnerResolution_RSFetchErrorFallsBack(t *testing.T) {
	// The pod references a ReplicaSet that doesn't exist in the cluster; the RS
	// Get fails, and ListPods must fall back to reporting the ReplicaSet itself
	// rather than failing the whole call.
	ns := "demo"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "api-missing-rs-aaa", Namespace: ns,
		OwnerReferences: []metav1.OwnerReference{ctrlOwnerRef("ReplicaSet", "missing-rs")},
	}}

	cs := fake.NewSimpleClientset(pod) // no ReplicaSet seeded
	c := newWithClient("prod", cs)

	pods, err := c.ListPods(context.Background(), sources.PodQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: ns},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	got := pods[0].Owner
	if got == nil || got.Kind != "ReplicaSet" || got.Name != "missing-rs" {
		t.Errorf("RS-fetch-error fallback: got %+v, want {ReplicaSet missing-rs}", got)
	}
}

func TestPodLogs_ReturnsBody(t *testing.T) {
	// The fake clientset's GetLogs returns a fixed "fake logs" body. Assert it is
	// returned with identity fields, and that an undersized body is not truncated.
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "ns"}}
	cs := fake.NewSimpleClientset(pod)
	c := newWithClient("prod", cs)

	res, err := c.PodLogs(context.Background(), sources.PodLogsQuery{
		Resource:  model.ResourceRef{Cluster: "prod", Namespace: "ns", Pod: "api-1"},
		Container: "api",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Pod != "api-1" || res.Namespace != "ns" || res.Container != "api" {
		t.Errorf("identity: got %q/%q/%q", res.Pod, res.Namespace, res.Container)
	}
	if res.Lines != "fake logs" {
		t.Errorf("Lines: got %q, want \"fake logs\"", res.Lines)
	}
	if res.Truncated {
		t.Errorf("Truncated should be false for a small body")
	}
}

// helpers

func filterByKind(refs []model.ResourceRef, kind string) []model.ResourceRef {
	var out []model.ResourceRef
	for _, r := range refs {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func refNames(refs []model.ResourceRef) []string {
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Name
	}
	return names
}

func containsAll(haystack []string, needles ...string) bool {
	set := make(map[string]bool, len(haystack))
	for _, s := range haystack {
		set[s] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}

// makeNode builds a core/v1 Node for ListNodes tests. ready sets the NodeReady
// condition status; roleLabels are added as node-role.kubernetes.io/<role>=""
// labels.
func makeNode(name string, ready bool, roleLabels ...string) *corev1.Node {
	labels := map[string]string{}
	for _, r := range roleLabels {
		labels["node-role.kubernetes.io/"+r] = ""
	}
	readyStatus := corev1.ConditionFalse
	if ready {
		readyStatus = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Labels:            labels,
			CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		},
		Spec: corev1.NodeSpec{Unschedulable: false},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodeReady, Status: readyStatus},
			},
			NodeInfo: corev1.NodeSystemInfo{
				KubeletVersion:          "v1.30.2",
				OperatingSystem:         "linux",
				Architecture:            "amd64",
				OSImage:                 "Ubuntu 22.04",
				KernelVersion:           "6.5.0",
				ContainerRuntimeVersion: "containerd://1.7.2",
			},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: name},
				{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16412236Ki"),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("7800m"),
				corev1.ResourceMemory: resource.MustParse("16000001Ki"),
			},
		},
	}
}

func TestListNodes_MapsFields(t *testing.T) {
	n := makeNode("cp-1", true, "control-plane")
	c := newWithClient("prod", fake.NewSimpleClientset(n))

	nodes, err := c.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	got := nodes[0]
	if got.Name != "cp-1" {
		t.Errorf("Name: got %q, want cp-1", got.Name)
	}
	if !got.Ready {
		t.Errorf("Ready: got false, want true")
	}
	if len(got.Roles) != 1 || got.Roles[0] != "control-plane" {
		t.Errorf("Roles: got %v, want [control-plane]", got.Roles)
	}
	if got.KubeletVersion != "v1.30.2" {
		t.Errorf("KubeletVersion: got %q", got.KubeletVersion)
	}
	if got.OS != "linux" || got.Arch != "amd64" {
		t.Errorf("OS/Arch: got %q/%q, want linux/amd64", got.OS, got.Arch)
	}
	if got.OSImage != "Ubuntu 22.04" || got.KernelVersion != "6.5.0" {
		t.Errorf("OSImage/Kernel: got %q/%q", got.OSImage, got.KernelVersion)
	}
	if got.ContainerRuntime != "containerd://1.7.2" {
		t.Errorf("ContainerRuntime: got %q", got.ContainerRuntime)
	}
	if got.InternalIP != "10.0.0.5" {
		t.Errorf("InternalIP: got %q, want 10.0.0.5", got.InternalIP)
	}
	if got.CPUCapacity != "8" || got.MemoryCapacity != "16412236Ki" || got.PodsCapacity != "110" {
		t.Errorf("Capacity: cpu=%q mem=%q pods=%q", got.CPUCapacity, got.MemoryCapacity, got.PodsCapacity)
	}
	if got.CPUAllocatable != "7800m" || got.MemoryAllocatable != "16000001Ki" {
		t.Errorf("Allocatable: cpu=%q mem=%q", got.CPUAllocatable, got.MemoryAllocatable)
	}
	if got.Unschedulable {
		t.Errorf("Unschedulable: got true, want false")
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be set")
	}
}

func TestListNodes_NotReadyAndNoRoles(t *testing.T) {
	n := makeNode("worker-1", false) // not ready, no role labels
	c := newWithClient("prod", fake.NewSimpleClientset(n))

	nodes, err := c.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	got := nodes[0]
	if got.Ready {
		t.Errorf("Ready: got true, want false for a NodeReady=False node")
	}
	if len(got.Roles) != 0 {
		t.Errorf("Roles: got %v, want empty (UI shows <none>)", got.Roles)
	}
}
