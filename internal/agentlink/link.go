// Package agentlink defines the bidirectional channel between an in-cluster agent
// and the control plane. The agent dials OUT to the control plane (egress-only,
// NAT/firewall friendly — ADR-0001); the control plane never connects inward.
//
// The wire implementation (ADR-0002) is a single long-lived gRPC bidi stream per
// agent, secured with mTLS, that multiplexes two things:
//   - request/response: control plane proxies source queries down to the agent;
//   - server push: the agent streams Kubernetes/ArgoCD watch events upward.
//
// This package is transport-agnostic on purpose: only the Link/Gateway/Dialer
// implementations know about gRPC.
package agentlink

import (
	"context"
	"errors"

	"lotsman/internal/model"
)

// ErrNotConnected is returned when no agent is connected for a cluster.
var ErrNotConnected = errors.New("lotsman: agent link not connected")

// RequestKind identifies a control-plane -> agent query routed over the link.
type RequestKind string

const (
	ReqQueryLogs       RequestKind = "query_logs"
	ReqQueryMetrics    RequestKind = "query_metrics_instant"
	ReqQueryRange      RequestKind = "query_metrics_range"
	ReqChangeEvents    RequestKind = "change_events"
	ReqK8sEvents       RequestKind = "k8s_events"
	ReqListWorkloads   RequestKind = "list_workloads"
	ReqListNodes       RequestKind = "list_nodes"
	ReqListPods        RequestKind = "list_pods"
	ReqPodLogs         RequestKind = "pod_logs"
	ReqListConfigMaps  RequestKind = "list_configmaps"
	ReqGetConfigMap    RequestKind = "get_configmap"
	ReqListSecrets     RequestKind = "list_secrets"
	ReqGetSecret       RequestKind = "get_secret"
	ReqWorkloadHistory RequestKind = "workload_history"
)

// Request is a query sent down the link to an agent. Payload is the JSON-encoded
// source query (sources.LogQuery, sources.MetricQuery, ...).
type Request struct {
	Kind    RequestKind
	Cluster string
	Payload []byte
}

// Response is the agent's reply. Payload is the JSON-encoded result; Err carries
// a remote error message when the agent's source call failed.
type Response struct {
	Payload []byte
	Err     string
}

// Event is a server-pushed signal streamed up from an agent (k8s/ArgoCD watch).
type Event struct {
	Cluster string
	Signal  model.Signal
}

// Link is the control plane's handle to one connected agent: a request/response
// channel for query proxying plus an inbound stream of watch events.
type Link interface {
	Cluster() string
	Do(ctx context.Context, req Request) (Response, error)
	Events() <-chan Event
	Close() error
}
