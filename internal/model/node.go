package model

import "time"

// Node is the neutral, backend-agnostic view of a Kubernetes cluster Node the UI
// renders (a Lens-style "Nodes" view). Nodes are cluster-scoped, so unlike Pod /
// ConfigMapRef / SecretRef there is no Namespace. Capacity/allocatable values are
// the raw Kubernetes quantity strings ("8", "16412236Ki") so the UI can format
// them; Lotsman does not interpret them.
type Node struct {
	Name              string    `json:"name"`
	Ready             bool      `json:"ready"`
	Roles             []string  `json:"roles,omitempty"` // e.g. ["control-plane"], ["worker"]
	KubeletVersion    string    `json:"kubelet_version,omitempty"`
	OS                string    `json:"os,omitempty"`   // status.nodeInfo.operatingSystem (linux)
	Arch              string    `json:"arch,omitempty"` // status.nodeInfo.architecture
	OSImage           string    `json:"os_image,omitempty"`
	KernelVersion     string    `json:"kernel_version,omitempty"`
	ContainerRuntime  string    `json:"container_runtime,omitempty"`
	InternalIP        string    `json:"internal_ip,omitempty"`
	CPUCapacity       string    `json:"cpu_capacity,omitempty"`    // raw quantity, e.g. "8"
	MemoryCapacity    string    `json:"memory_capacity,omitempty"` // raw quantity, e.g. "16412236Ki"
	PodsCapacity      string    `json:"pods_capacity,omitempty"`
	CPUAllocatable    string    `json:"cpu_allocatable,omitempty"`
	MemoryAllocatable string    `json:"memory_allocatable,omitempty"`
	Unschedulable     bool      `json:"unschedulable,omitempty"`
	CreatedAt         time.Time `json:"created_at"` // for age
}
