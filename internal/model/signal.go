package model

import "time"

// SignalKind enumerates the signal families Lotsman correlates. The whole point
// of the platform is joining these on (ResourceRef, time): a Change followed by
// a Metric anomaly and a Log error burst on the same workload is an incident
// with a probable cause.
type SignalKind string

const (
	SignalLog      SignalKind = "log"       // a log line / pattern (Loki)
	SignalMetric   SignalKind = "metric"    // a metric anomaly/threshold (VictoriaMetrics)
	SignalChange   SignalKind = "change"    // a deploy / rollout / config change (ArgoCD)
	SignalK8sEvent SignalKind = "k8s_event" // a Kubernetes event (OOMKilled, BackOff, ...)
)

// Severity is an ordered severity scale shared by all signal kinds.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityError
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityCritical:
		return "critical"
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	default:
		return "info"
	}
}

// Signal is a single normalized observation about a resource at a point in time.
// Payload carries the raw, kind-specific detail (a Prometheus sample, a Loki
// stream entry, an ArgoCD sync record) so the UI can show source data without
// the engine needing to understand every backend's schema.
type Signal struct {
	ID        string            `json:"id"`
	Kind      SignalKind        `json:"kind"`
	Resource  ResourceRef       `json:"resource"`
	Timestamp time.Time         `json:"timestamp"`
	Severity  Severity          `json:"severity"`
	Source    string            `json:"source"` // adapter name: "loki", "victoriametrics", "argocd", "kubernetes"
	Title     string            `json:"title"`
	Message   string            `json:"message,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Payload   map[string]any    `json:"payload,omitempty"`

	// Change is set when Kind == SignalChange. It is the highest-precision
	// root-cause signal (see ADR-0008, change-first ranking).
	Change *ChangeRef `json:"change,omitempty"`
}

// ChangeRef describes a deployment/rollout/config change. ArgoCD is the first
// backend, but the shape is provider-neutral.
type ChangeRef struct {
	Source   string    `json:"source"`   // "argocd"
	App      string    `json:"app"`      // ArgoCD Application name
	Revision string    `json:"revision"` // git SHA / image tag
	SyncedAt time.Time `json:"synced_at"`
	URL      string    `json:"url,omitempty"`
}
