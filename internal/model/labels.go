package model

// ResourceFromLabels maps a backend label set (Prometheus/Loki conventions) to a
// ResourceRef, so metric and log signals land on the same identity as Kubernetes
// events and ArgoCD changes. This normalization is what lets the engine correlate
// across otherwise-incompatible systems.
func ResourceFromLabels(cluster string, labels map[string]string) ResourceRef {
	ref := ResourceRef{Cluster: cluster}
	ref.Namespace = firstNonEmpty(labels, "namespace", "k8s_namespace_name", "kubernetes_namespace")
	if name := firstNonEmpty(labels, "workload", "deployment", "app", "app_kubernetes_io_name"); name != "" {
		ref.Name = name
		ref.Kind = "Deployment"
	}
	ref.Pod = firstNonEmpty(labels, "pod", "pod_name", "instance")
	return ref
}

func firstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
	}
	return ""
}
