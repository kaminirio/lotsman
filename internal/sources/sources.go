// Package sources defines the backend-agnostic interfaces through which Lotsman
// reads cluster signals. This is the seam that makes Lotsman "cloud- and
// environment-agnostic" (see ADR-0003): the correlation engine depends only on
// these interfaces, never on Loki / VictoriaMetrics / ArgoCD / Kubernetes
// directly.
//
// Each interface has at least two implementations:
//   - a concrete adapter that lives in the agent and talks to a real backend
//     (sources/loki, sources/victoriametrics, sources/argocd, sources/kubernetes);
//   - a remote adapter in the control plane that proxies calls to the right
//     agent over the agent link (sources/remote).
//
// The engine cannot tell the difference, so single-cluster "direct mode" and
// multi-cluster "agent mode" run identical correlation logic.
package sources

import (
	"context"
	"errors"
	"time"

	"lotsman/internal/model"
)

// ErrNotImplemented is returned by scaffold stubs. Concrete adapters replace it
// with real backend calls.
var ErrNotImplemented = errors.New("lotsman: source not implemented")

// TimeRange is a half-open [Start, End) query window.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// LogSource queries a log backend. First implementation: Loki via LogQL.
type LogSource interface {
	Name() string
	// QueryLogs returns log signals for a resource within a window. If Query is
	// empty the adapter derives a backend-native selector from Resource.
	QueryLogs(ctx context.Context, q LogQuery) ([]model.Signal, error)
}

// LogQuery selects logs by resource + window, optionally with a backend-native
// query (LogQL) for advanced use.
type LogQuery struct {
	Resource model.ResourceRef
	Range    TimeRange
	Query    string // optional backend-native query (e.g. LogQL)
	Limit    int
}

// MetricSource queries a metrics backend. First implementation: VictoriaMetrics
// via the Prometheus HTTP API (so Prometheus itself is drop-in).
type MetricSource interface {
	Name() string
	// QueryInstant evaluates a PromQL expression at a single instant.
	QueryInstant(ctx context.Context, q MetricQuery) (MetricResult, error)
	// QueryRange evaluates a PromQL expression over a window at a fixed step.
	QueryRange(ctx context.Context, q MetricRangeQuery) (MetricResult, error)
}

type MetricQuery struct {
	PromQL string
	At     time.Time
}

type MetricRangeQuery struct {
	PromQL string
	Range  TimeRange
	Step   time.Duration
}

type MetricResult struct {
	Series []MetricSeries
}

type MetricSeries struct {
	Labels map[string]string
	Points []MetricPoint
}

type MetricPoint struct {
	T time.Time
	V float64
}

// DeploymentSource exposes deployment/change events. First implementation:
// ArgoCD (sync/rollout history). Change events are the backbone of investigation
// (see ADR-0008), so this is modeled as a first-class event stream, not status
// polling.
type DeploymentSource interface {
	Name() string
	// ChangeEvents returns SignalChange signals for a resource within a window.
	ChangeEvents(ctx context.Context, q ChangeQuery) ([]model.Signal, error)
}

type ChangeQuery struct {
	Resource model.ResourceRef
	Range    TimeRange
}

// ClusterSource exposes Kubernetes resource state + events. First (and only)
// implementation: client-go informers/clients.
type ClusterSource interface {
	Name() string
	// Events returns Kubernetes event signals (OOMKilled, BackOff, FailedMount,
	// probe failures, ...) for a resource within a window.
	Events(ctx context.Context, q EventQuery) ([]model.Signal, error)
	// ListWorkloads enumerates workloads in a namespace ("" = all namespaces).
	ListWorkloads(ctx context.Context, namespace string) ([]model.ResourceRef, error)
	// ListNodes enumerates the cluster's Nodes (cluster-scoped, no namespace) with
	// status, roles, kubelet version, node info, and capacity/allocatable.
	ListNodes(ctx context.Context) ([]model.Node, error)
	// ListPods enumerates pods in a namespace, optionally narrowed to one
	// workload's pods, with each pod's containers and applied env.
	ListPods(ctx context.Context, q PodQuery) ([]model.Pod, error)
	// PodLogs returns a tail of one pod container's stdout/stderr logs.
	PodLogs(ctx context.Context, q PodLogsQuery) (model.PodLogsResult, error)
	// ListConfigMaps enumerates ConfigMaps in a namespace ("" = all namespaces),
	// returning each one's identity and sorted data keys (not values).
	ListConfigMaps(ctx context.Context, namespace string) ([]model.ConfigMapRef, error)
	// GetConfigMap returns a single ConfigMap's data. Resource.Namespace and
	// Resource.Name are required.
	GetConfigMap(ctx context.Context, ref model.ResourceRef) (model.ConfigMapDetail, error)
	// ListSecrets enumerates Secrets in a namespace ("" = all namespaces),
	// returning identity, type, sorted keys, and — for TLS secrets — parsed public
	// certificate metadata. Values are never exposed by a listing.
	ListSecrets(ctx context.Context, namespace string) ([]model.SecretRef, error)
	// GetSecret returns a single Secret's entries. Values are revealed only when
	// q.Reveal is set (the API gates this on admin, and the agent additionally
	// gates it on its env-reveal opt-in); certificate metadata is always returned.
	GetSecret(ctx context.Context, q SecretQuery) (model.SecretDetail, error)
}

// WorkloadHistorian is an OPTIONAL capability a ClusterSource may also implement:
// the image/revision history of a single workload, newest-first. It is kept off
// the core ClusterSource interface so adding it does not force every
// implementation (and test fake) to change — callers type-assert for it and a
// source that lacks it simply yields no history. Both the concrete Kubernetes
// adapter and the remote proxy implement it.
type WorkloadHistorian interface {
	WorkloadHistory(ctx context.Context, ref model.ResourceRef) ([]model.WorkloadRevision, error)
}

type EventQuery struct {
	Resource model.ResourceRef
	Range    TimeRange
}

// PodQuery selects pods to list. Resource.Namespace is required. When
// Resource.Name is set the result is narrowed to that workload's pods. Reveal
// requests that secret/configMap-sourced env vars be resolved to their actual
// values (the API only sets this for admins).
type PodQuery struct {
	Resource model.ResourceRef
	Reveal   bool
}

// PodLogsQuery selects one pod container's logs. Resource.Pod must be set.
// TailLines <= 0 selects the adapter default (200).
type PodLogsQuery struct {
	Resource  model.ResourceRef
	Container string
	TailLines int64
}

// SecretQuery selects one Secret to fetch. Resource.Namespace and Resource.Name
// are required. Reveal requests that secret entry values be returned in the clear;
// when false, non-certificate entries are masked. Certificate metadata for a TLS
// secret is returned regardless of Reveal (it is public).
type SecretQuery struct {
	Resource model.ResourceRef
	Reveal   bool
}

// Provider bundles the four sources for a single cluster. The engine resolves a
// Provider per cluster and reads all signal kinds through it.
type Provider interface {
	Cluster() string
	Logs() LogSource
	Metrics() MetricSource
	Deployments() DeploymentSource
	Resources() ClusterSource
}
