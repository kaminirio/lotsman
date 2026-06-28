package model

import "time"

// WorkloadRevision is one point in a workload's rollout history: the container
// images that revision ran, when it was rolled out, and the recorded change
// cause. It is reconstructed from a Deployment's ReplicaSet revisions (the
// kube-native history, keyed by the deployment.kubernetes.io/revision
// annotation); controllers without queryable revision history report only their
// current revision. Revisions are returned newest-first; exactly one is Current.
type WorkloadRevision struct {
	Revision    int64     `json:"revision"`
	Images      []string  `json:"images"`
	CreatedAt   time.Time `json:"created_at"`
	ChangeCause string    `json:"change_cause,omitempty"`
	Current     bool      `json:"current,omitempty"`
}
