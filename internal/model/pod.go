package model

// EnvVarSource records the provenance of a container env var. For a valueFrom
// var, Kind is one of "secret", "configMap", "field", or "resource" (Secret,
// ConfigMap, downward API field, or resource field). For an inline literal var,
// Kind is the owning workload's kind — "Deployment", "StatefulSet", "DaemonSet",
// ... — or "Pod" for a bare pod with no controller, so every env var carries a
// source for the UI.
type EnvVarSource struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Key  string `json:"key,omitempty"`
}

// ContainerEnvVar is one resolved environment variable applied to a container.
// Value carries the literal, or the resolved value when the API revealed a
// secret/configMap source. Masked is set when the API replaced a value to avoid
// leaking a credential. Source is set when the var came from valueFrom.
type ContainerEnvVar struct {
	Name   string        `json:"name"`
	Value  string        `json:"value,omitempty"`
	Masked bool          `json:"masked,omitempty"`
	Source *EnvVarSource `json:"source,omitempty"`
}

// Container is a single container within a pod, with its applied env and live
// status. Ready/State/Reason/RestartCount come from the pod's container status
// (absent for a container the kubelet has not reported yet, leaving State ""),
// and let the UI render a per-container health indicator (Lens-style squares).
// State is one of "running", "waiting", or "terminated"; Reason carries the
// kubelet's reason for a waiting/terminated container (e.g. "CrashLoopBackOff",
// "ImagePullBackOff", "Completed", "Error"), empty while running.
type Container struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Ready        bool              `json:"ready"`
	State        string            `json:"state,omitempty"`
	Reason       string            `json:"reason,omitempty"`
	RestartCount int32             `json:"restart_count,omitempty"`
	Env          []ContainerEnvVar `json:"env,omitempty"`
}

// WorkloadRef identifies the controller that owns a pod (the user-facing
// workload, e.g. a Deployment rather than the intermediate ReplicaSet).
type WorkloadRef struct {
	Kind string `json:"kind"` // Deployment | StatefulSet | DaemonSet | ReplicaSet | Job | ...
	Name string `json:"name"`
}

// Pod is the neutral, backend-agnostic view of a Kubernetes pod the UI renders.
type Pod struct {
	Name       string       `json:"name"`
	Namespace  string       `json:"namespace"`
	Phase      string       `json:"phase"`
	Ready      bool         `json:"ready"`
	Restarts   int32        `json:"restarts"`
	Node       string       `json:"node,omitempty"`
	Owner      *WorkloadRef `json:"owner,omitempty"`
	Containers []Container  `json:"containers"`
}

// PodLogsResult is a pod container's stdout/stderr log tail. Lines is the raw
// (newline-delimited) log body; Truncated is set when the body hit the size cap.
type PodLogsResult struct {
	Pod       string `json:"pod"`
	Namespace string `json:"namespace"`
	Container string `json:"container"`
	Lines     string `json:"lines"`
	Truncated bool   `json:"truncated,omitempty"`
}
