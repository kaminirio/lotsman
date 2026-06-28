package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"lotsman/internal/agentlink"
	"lotsman/internal/config"
	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// revealRecordingSource records the Reveal flag passed to ListPods/GetSecret so we
// can assert the agent's wire-flag gate.
type revealRecordingSource struct {
	lastReveal       bool
	lastSecretReveal bool
}

func (s *revealRecordingSource) Name() string { return "stub" }
func (s *revealRecordingSource) Events(context.Context, sources.EventQuery) ([]model.Signal, error) {
	return nil, nil
}
func (s *revealRecordingSource) ListWorkloads(context.Context, string) ([]model.ResourceRef, error) {
	return nil, nil
}
func (s *revealRecordingSource) ListNodes(context.Context) ([]model.Node, error) {
	return nil, nil
}
func (s *revealRecordingSource) ListPods(_ context.Context, q sources.PodQuery) ([]model.Pod, error) {
	s.lastReveal = q.Reveal
	return nil, nil
}
func (s *revealRecordingSource) PodLogs(context.Context, sources.PodLogsQuery) (model.PodLogsResult, error) {
	return model.PodLogsResult{}, nil
}
func (s *revealRecordingSource) ListConfigMaps(context.Context, string) ([]model.ConfigMapRef, error) {
	return nil, nil
}
func (s *revealRecordingSource) GetConfigMap(context.Context, model.ResourceRef) (model.ConfigMapDetail, error) {
	return model.ConfigMapDetail{}, nil
}
func (s *revealRecordingSource) ListSecrets(context.Context, string) ([]model.SecretRef, error) {
	return nil, nil
}
func (s *revealRecordingSource) GetSecret(_ context.Context, q sources.SecretQuery) (model.SecretDetail, error) {
	s.lastSecretReveal = q.Reveal
	return model.SecretDetail{}, nil
}

type noopLog struct{}

func (noopLog) Name() string { return "noop" }
func (noopLog) QueryLogs(context.Context, sources.LogQuery) ([]model.Signal, error) {
	return nil, nil
}

type noopMetric struct{}

func (noopMetric) Name() string { return "noop" }
func (noopMetric) QueryInstant(context.Context, sources.MetricQuery) (sources.MetricResult, error) {
	return sources.MetricResult{}, nil
}
func (noopMetric) QueryRange(context.Context, sources.MetricRangeQuery) (sources.MetricResult, error) {
	return sources.MetricResult{}, nil
}

type noopDeploy struct{}

func (noopDeploy) Name() string { return "noop" }
func (noopDeploy) ChangeEvents(context.Context, sources.ChangeQuery) ([]model.Signal, error) {
	return nil, nil
}

// newTestAgent builds an Agent around a recording cluster source without dialing
// a control plane, so handle() can be exercised directly.
func newTestAgent(allowReveal bool, src sources.ClusterSource) *Agent {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Agent{
		cfg:      config.Agent{Cluster: "test", AllowEnvReveal: allowReveal},
		logger:   logger,
		provider: sources.NewProvider("test", noopLog{}, noopMetric{}, noopDeploy{}, src),
	}
}

func listPodsRequest(t *testing.T, reveal bool) agentlink.Request {
	t.Helper()
	payload, err := json.Marshal(sources.PodQuery{Reveal: reveal})
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	return agentlink.Request{Kind: agentlink.ReqListPods, Payload: payload}
}

func TestHandleListPods_GateOffForcesRevealFalse(t *testing.T) {
	src := &revealRecordingSource{}
	a := newTestAgent(false, src)

	// Control plane asks for reveal, but the agent is not opted in.
	resp := a.handle(context.Background(), listPodsRequest(t, true))
	if resp.Err != "" {
		t.Fatalf("handle returned error: %s", resp.Err)
	}
	if src.lastReveal {
		t.Fatalf("agent without LOTSMAN_ALLOW_ENV_REVEAL must force Reveal=false even when the wire flag is true")
	}
}

func TestHandleListPods_GateOnHonorsReveal(t *testing.T) {
	src := &revealRecordingSource{}
	a := newTestAgent(true, src)

	resp := a.handle(context.Background(), listPodsRequest(t, true))
	if resp.Err != "" {
		t.Fatalf("handle returned error: %s", resp.Err)
	}
	if !src.lastReveal {
		t.Fatalf("opted-in agent should honor Reveal=true from the control plane")
	}
}

func getSecretRequest(t *testing.T, reveal bool) agentlink.Request {
	t.Helper()
	payload, err := json.Marshal(sources.SecretQuery{Reveal: reveal})
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	return agentlink.Request{Kind: agentlink.ReqGetSecret, Payload: payload}
}

func TestHandleGetSecret_GateOffForcesRevealFalse(t *testing.T) {
	src := &revealRecordingSource{}
	a := newTestAgent(false, src)

	resp := a.handle(context.Background(), getSecretRequest(t, true))
	if resp.Err != "" {
		t.Fatalf("handle returned error: %s", resp.Err)
	}
	if src.lastSecretReveal {
		t.Fatalf("agent without LOTSMAN_ALLOW_ENV_REVEAL must force GetSecret Reveal=false even when the wire flag is true")
	}
}

func TestHandleGetSecret_GateOnHonorsReveal(t *testing.T) {
	src := &revealRecordingSource{}
	a := newTestAgent(true, src)

	resp := a.handle(context.Background(), getSecretRequest(t, true))
	if resp.Err != "" {
		t.Fatalf("handle returned error: %s", resp.Err)
	}
	if !src.lastSecretReveal {
		t.Fatalf("opted-in agent should honor GetSecret Reveal=true from the control plane")
	}
}
