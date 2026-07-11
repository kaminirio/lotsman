package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// fakeLogSource implements sources.LogSource. When errOut is non-nil every
// QueryLogs call returns that error and no signals.
type fakeLogSource struct {
	name   string
	sigs   []model.Signal
	errOut error
}

func (f *fakeLogSource) Name() string { return f.name }
func (f *fakeLogSource) QueryLogs(_ context.Context, _ sources.LogQuery) ([]model.Signal, error) {
	if f.errOut != nil {
		return nil, f.errOut
	}
	return f.sigs, nil
}

// blockingLogSource implements sources.LogSource but blocks in QueryLogs until
// the context is cancelled (e.g. by the per-source timeout), then returns the
// context error. It records whether it observed cancellation.
type blockingLogSource struct {
	name      string
	cancelled chan struct{}
}

func (f *blockingLogSource) Name() string { return f.name }
func (f *blockingLogSource) QueryLogs(ctx context.Context, _ sources.LogQuery) ([]model.Signal, error) {
	<-ctx.Done()
	if f.cancelled != nil {
		close(f.cancelled)
	}
	return nil, ctx.Err()
}

// fakeDeploySource implements sources.DeploymentSource.
type fakeDeploySource struct {
	name string
	sigs []model.Signal
}

func (f *fakeDeploySource) Name() string { return f.name }
func (f *fakeDeploySource) ChangeEvents(_ context.Context, _ sources.ChangeQuery) ([]model.Signal, error) {
	return f.sigs, nil
}

// fakeClusterSource implements sources.ClusterSource with a no-op for every
// method except Events.
type fakeClusterSource struct {
	name string
	sigs []model.Signal
}

func (f *fakeClusterSource) Name() string { return f.name }
func (f *fakeClusterSource) Events(_ context.Context, _ sources.EventQuery) ([]model.Signal, error) {
	return f.sigs, nil
}
func (f *fakeClusterSource) ListWorkloads(_ context.Context, _ string) ([]model.ResourceRef, error) {
	return nil, sources.ErrNotImplemented
}
func (f *fakeClusterSource) ListNodes(_ context.Context) ([]model.Node, error) {
	return nil, sources.ErrNotImplemented
}
func (f *fakeClusterSource) ListPods(_ context.Context, _ sources.PodQuery) ([]model.Pod, error) {
	return nil, sources.ErrNotImplemented
}
func (f *fakeClusterSource) PodLogs(_ context.Context, _ sources.PodLogsQuery) (model.PodLogsResult, error) {
	return model.PodLogsResult{}, sources.ErrNotImplemented
}
func (f *fakeClusterSource) ListConfigMaps(_ context.Context, _ string) ([]model.ConfigMapRef, error) {
	return nil, sources.ErrNotImplemented
}
func (f *fakeClusterSource) GetConfigMap(_ context.Context, _ model.ResourceRef) (model.ConfigMapDetail, error) {
	return model.ConfigMapDetail{}, sources.ErrNotImplemented
}
func (f *fakeClusterSource) ListSecrets(_ context.Context, _ string) ([]model.SecretRef, error) {
	return nil, sources.ErrNotImplemented
}
func (f *fakeClusterSource) GetSecret(_ context.Context, _ sources.SecretQuery) (model.SecretDetail, error) {
	return model.SecretDetail{}, sources.ErrNotImplemented
}

// fakeMetricSource is needed only to satisfy Provider; it always returns
// ErrNotImplemented.
type fakeMetricSource struct{}

func (f *fakeMetricSource) Name() string { return "fake/metrics" }
func (f *fakeMetricSource) QueryInstant(_ context.Context, _ sources.MetricQuery) (sources.MetricResult, error) {
	return sources.MetricResult{}, sources.ErrNotImplemented
}
func (f *fakeMetricSource) QueryRange(_ context.Context, _ sources.MetricRangeQuery) (sources.MetricResult, error) {
	return sources.MetricResult{}, sources.ErrNotImplemented
}

// TestCorrelatorPartialSourceFailure pins the ADR-0008 graceful-degradation
// invariant: when Logs() returns an error the correlator must still return the
// signals from Deployments() and Resources(), time-sorted, with no panic.
func TestCorrelatorPartialSourceFailure(t *testing.T) {
	t0 := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(2 * time.Minute)

	changeSig := model.Signal{
		ID:        "c1",
		Kind:      model.SignalChange,
		Timestamp: t0,
		Title:     "ArgoCD synced",
		Change:    &model.ChangeRef{App: "api", Revision: "abc123"},
	}
	eventSig := model.Signal{
		ID:        "e1",
		Kind:      model.SignalK8sEvent,
		Timestamp: t1,
		Title:     "BackOff",
		Message:   "container restarting",
	}

	provider := sources.NewProvider(
		"test-cluster",
		&fakeLogSource{name: "fake/loki", errOut: errors.New("loki unavailable")},
		&fakeMetricSource{},
		&fakeDeploySource{name: "fake/argocd", sigs: []model.Signal{changeSig}},
		&fakeClusterSource{name: "fake/k8s", sigs: []model.Signal{eventSig}},
	)

	corr := NewCorrelator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ref := model.ResourceRef{Cluster: "test-cluster", Namespace: "default", Kind: "Deployment", Name: "api"}
	rng := sources.TimeRange{Start: t0.Add(-5 * time.Minute), End: t1.Add(5 * time.Minute)}

	got := corr.Timeline(context.Background(), provider, ref, rng)

	if len(got) != 2 {
		t.Fatalf("expected 2 signals (log error must not blank results), got %d: %+v", len(got), got)
	}
	// Timeline must be time-sorted ascending.
	if !got[0].Timestamp.Before(got[1].Timestamp) {
		t.Fatalf("signals not time-sorted: %v >= %v", got[0].Timestamp, got[1].Timestamp)
	}
	// The change signal should come first (earlier timestamp).
	if got[0].ID != "c1" {
		t.Fatalf("first signal: want ID=c1 (change), got %q", got[0].ID)
	}
	if got[1].ID != "e1" {
		t.Fatalf("second signal: want ID=e1 (k8s event), got %q", got[1].ID)
	}
}

// TestCorrelatorPerSourceTimeout pins ENG-2: a source that blocks past the
// per-source timeout must be skipped (via the deadline), while the remaining
// sources still return. It also proves the timeout context derives from ctx so
// cancellation propagates into the blocked source.
func TestCorrelatorPerSourceTimeout(t *testing.T) {
	t0 := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(2 * time.Minute)

	changeSig := model.Signal{ID: "c1", Kind: model.SignalChange, Timestamp: t0, Title: "synced"}
	eventSig := model.Signal{ID: "e1", Kind: model.SignalK8sEvent, Timestamp: t1, Title: "BackOff"}

	blocked := &blockingLogSource{name: "fake/loki", cancelled: make(chan struct{})}
	provider := sources.NewProvider(
		"test-cluster",
		blocked,
		&fakeMetricSource{},
		&fakeDeploySource{name: "fake/argocd", sigs: []model.Signal{changeSig}},
		&fakeClusterSource{name: "fake/k8s", sigs: []model.Signal{eventSig}},
	)

	corr := NewCorrelator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	corr.PerSourceTimeout = 50 * time.Millisecond // short so the test is fast

	ref := model.ResourceRef{Cluster: "test-cluster", Namespace: "default", Kind: "Deployment", Name: "api"}
	rng := sources.TimeRange{Start: t0.Add(-5 * time.Minute), End: t1.Add(5 * time.Minute)}

	start := time.Now()
	got := corr.Timeline(context.Background(), provider, ref, rng)
	elapsed := time.Since(start)

	// The blocked source must have observed cancellation (timeout propagated).
	select {
	case <-blocked.cancelled:
	default:
		t.Fatal("blocked source was not cancelled by the per-source timeout")
	}

	// The blocked source contributes nothing; the other two still return.
	if len(got) != 2 {
		t.Fatalf("expected 2 signals (blocked source skipped), got %d: %+v", len(got), got)
	}
	if got[0].ID != "c1" || got[1].ID != "e1" {
		t.Fatalf("unexpected signals: %+v", got)
	}

	// Gather must not have waited far beyond the per-source timeout.
	if elapsed > 2*time.Second {
		t.Fatalf("gather took too long (%v); per-source timeout not enforced", elapsed)
	}
}

// TestCorrelatorContextCancellationPropagates proves an already-cancelled parent
// context still unblocks a blocked source (cancellation propagates through the
// derived per-source timeout context).
func TestCorrelatorContextCancellationPropagates(t *testing.T) {
	blocked := &blockingLogSource{name: "fake/loki"}
	provider := sources.NewProvider(
		"test-cluster",
		blocked,
		&fakeMetricSource{},
		&fakeDeploySource{name: "fake/argocd"},
		&fakeClusterSource{name: "fake/k8s"},
	)

	corr := NewCorrelator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ref := model.ResourceRef{Cluster: "test-cluster", Namespace: "default", Kind: "Deployment", Name: "api"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	done := make(chan []model.Signal, 1)
	go func() { done <- corr.Timeline(ctx, provider, ref, sources.TimeRange{}) }()

	select {
	case <-done:
		// returned promptly — cancellation propagated
	case <-time.After(2 * time.Second):
		t.Fatal("Timeline did not return after parent context cancellation")
	}
}
