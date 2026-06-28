// Package model holds Lotsman's backend-agnostic domain types. Nothing in this
// package may import an adapter (Loki, VictoriaMetrics, ArgoCD, Kubernetes) or a
// transport — these types are the lingua franca the correlation engine speaks,
// independent of where signals come from.
package model

import "strings"

// ResourceRef is the canonical identity for everything in Lotsman. Every log
// line, metric series, change event, and Kubernetes event is normalized to a
// ResourceRef + timestamp so the correlation engine can join signals that
// originate in completely different systems.
//
// Identity is hierarchical: Cluster -> Namespace -> Workload (Kind/Name) -> Pod.
// Higher levels may be empty when a signal is cluster- or namespace-scoped.
type ResourceRef struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind,omitempty"` // Deployment, StatefulSet, DaemonSet, Pod, Node, ...
	Name      string `json:"name,omitempty"`
	Pod       string `json:"pod,omitempty"`
}

// Workload returns the ref narrowed to its owning workload (Pod stripped). Used
// when correlating pod-level signals up to the Deployment/StatefulSet that owns
// them.
func (r ResourceRef) Workload() ResourceRef {
	r.Pod = ""
	return r
}

// Key is a stable, comparable string identity used for grouping signals.
func (r ResourceRef) Key() string {
	parts := []string{r.Cluster, r.Namespace, r.Kind, r.Name}
	if r.Pod != "" {
		parts = append(parts, r.Pod)
	}
	return strings.Join(parts, "/")
}

func (r ResourceRef) String() string { return r.Key() }

// Matches reports whether other refers to the same resource at workload
// granularity (Pod ignored). Used by the correlator to attribute pod- and
// workload-level signals to the same incident.
func (r ResourceRef) Matches(other ResourceRef) bool {
	return r.Cluster == other.Cluster &&
		r.Namespace == other.Namespace &&
		r.Kind == other.Kind &&
		r.Name == other.Name
}
