// Package remote provides a control-plane-side sources.Provider that proxies
// every call to a remote agent over an agentlink.Link. The correlation engine
// uses it identically to an in-agent concrete Provider — this is the crux of the
// location-agnostic design (ADR-0003).
package remote

import (
	"context"
	"encoding/json"
	"errors"

	"lotsman/internal/agentlink"
	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// NewProvider builds a Provider backed by a connected agent link.
func NewProvider(link agentlink.Link) sources.Provider {
	return sources.NewProvider(
		link.Cluster(),
		&logs{link},
		&metrics{link},
		&deployments{link},
		&resources{link},
	)
}

type logs struct{ link agentlink.Link }

func (s *logs) Name() string { return "remote/loki" }
func (s *logs) QueryLogs(ctx context.Context, q sources.LogQuery) ([]model.Signal, error) {
	var out []model.Signal
	err := call(ctx, s.link, agentlink.ReqQueryLogs, q, &out)
	return out, err
}

type metrics struct{ link agentlink.Link }

func (s *metrics) Name() string { return "remote/victoriametrics" }
func (s *metrics) QueryInstant(ctx context.Context, q sources.MetricQuery) (sources.MetricResult, error) {
	var out sources.MetricResult
	err := call(ctx, s.link, agentlink.ReqQueryMetrics, q, &out)
	return out, err
}
func (s *metrics) QueryRange(ctx context.Context, q sources.MetricRangeQuery) (sources.MetricResult, error) {
	var out sources.MetricResult
	err := call(ctx, s.link, agentlink.ReqQueryRange, q, &out)
	return out, err
}

type deployments struct{ link agentlink.Link }

func (s *deployments) Name() string { return "remote/argocd" }
func (s *deployments) ChangeEvents(ctx context.Context, q sources.ChangeQuery) ([]model.Signal, error) {
	var out []model.Signal
	err := call(ctx, s.link, agentlink.ReqChangeEvents, q, &out)
	return out, err
}

type resources struct{ link agentlink.Link }

func (s *resources) Name() string { return "remote/kubernetes" }
func (s *resources) Events(ctx context.Context, q sources.EventQuery) ([]model.Signal, error) {
	var out []model.Signal
	err := call(ctx, s.link, agentlink.ReqK8sEvents, q, &out)
	return out, err
}
func (s *resources) ListWorkloads(ctx context.Context, namespace string) ([]model.ResourceRef, error) {
	var out []model.ResourceRef
	err := call(ctx, s.link, agentlink.ReqListWorkloads, namespace, &out)
	return out, err
}
func (s *resources) ListNodes(ctx context.Context) ([]model.Node, error) {
	var out []model.Node
	err := call(ctx, s.link, agentlink.ReqListNodes, struct{}{}, &out)
	return out, err
}
func (s *resources) ListPods(ctx context.Context, q sources.PodQuery) ([]model.Pod, error) {
	var out []model.Pod
	err := call(ctx, s.link, agentlink.ReqListPods, q, &out)
	return out, err
}

// WorkloadHistory implements sources.WorkloadHistorian over the agent link, so
// the control plane can serve image/revision history in agent mode just as in
// direct mode.
func (s *resources) WorkloadHistory(ctx context.Context, ref model.ResourceRef) ([]model.WorkloadRevision, error) {
	var out []model.WorkloadRevision
	err := call(ctx, s.link, agentlink.ReqWorkloadHistory, ref, &out)
	return out, err
}
func (s *resources) PodLogs(ctx context.Context, q sources.PodLogsQuery) (model.PodLogsResult, error) {
	var out model.PodLogsResult
	err := call(ctx, s.link, agentlink.ReqPodLogs, q, &out)
	return out, err
}
func (s *resources) ListConfigMaps(ctx context.Context, namespace string) ([]model.ConfigMapRef, error) {
	var out []model.ConfigMapRef
	err := call(ctx, s.link, agentlink.ReqListConfigMaps, namespace, &out)
	return out, err
}
func (s *resources) GetConfigMap(ctx context.Context, ref model.ResourceRef) (model.ConfigMapDetail, error) {
	var out model.ConfigMapDetail
	err := call(ctx, s.link, agentlink.ReqGetConfigMap, ref, &out)
	return out, err
}
func (s *resources) ListSecrets(ctx context.Context, namespace string) ([]model.SecretRef, error) {
	var out []model.SecretRef
	err := call(ctx, s.link, agentlink.ReqListSecrets, namespace, &out)
	return out, err
}
func (s *resources) GetSecret(ctx context.Context, q sources.SecretQuery) (model.SecretDetail, error) {
	var out model.SecretDetail
	err := call(ctx, s.link, agentlink.ReqGetSecret, q, &out)
	return out, err
}

// call marshals a query, sends it over the link, and unmarshals the response.
func call(ctx context.Context, link agentlink.Link, kind agentlink.RequestKind, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := link.Do(ctx, agentlink.Request{Kind: kind, Cluster: link.Cluster(), Payload: payload})
	if err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	if out != nil && len(resp.Payload) > 0 {
		return json.Unmarshal(resp.Payload, out)
	}
	return nil
}

// Compile-time checks that the proxy types satisfy the source interfaces.
var (
	_ sources.LogSource        = (*logs)(nil)
	_ sources.MetricSource     = (*metrics)(nil)
	_ sources.DeploymentSource = (*deployments)(nil)
	_ sources.ClusterSource    = (*resources)(nil)
)
