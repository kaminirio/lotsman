package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// stubMetricSource implements sources.MetricSource with a canned range result.
// When rangeErr is non-nil QueryRange returns it (simulating an errored/timed-out
// metric backend). QueryInstant is unused by the correlator.
type stubMetricSource struct {
	name     string
	rangeRes sources.MetricResult
	rangeErr error
}

func (f *stubMetricSource) Name() string { return f.name }
func (f *stubMetricSource) QueryInstant(_ context.Context, _ sources.MetricQuery) (sources.MetricResult, error) {
	return sources.MetricResult{}, sources.ErrNotImplemented
}
func (f *stubMetricSource) QueryRange(_ context.Context, _ sources.MetricRangeQuery) (sources.MetricResult, error) {
	if f.rangeErr != nil {
		return sources.MetricResult{}, f.rangeErr
	}
	return f.rangeRes, nil
}

func newTestCorrelator() *Correlator {
	return NewCorrelator(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestCorrelatorMetricsInTimeline pins ENG-1: an anomalous metric series must
// reach the timeline as a SeverityError SignalMetric, and that flagged signal
// must produce a metric-anomaly hypothesis from the ranker.
func TestCorrelatorMetricsInTimeline(t *testing.T) {
	t0 := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(2 * time.Minute)

	metricRes := sources.MetricResult{Series: []sources.MetricSeries{{
		Labels: map[string]string{"namespace": "default", "workload": "api"},
		Points: []sources.MetricPoint{
			{T: t0, V: 0.01},
			{T: t1, V: 0.42}, // peak, well over the default 0.05 threshold
		},
	}}}

	provider := sources.NewProvider(
		"test-cluster",
		&fakeLogSource{name: "fake/loki"},
		&stubMetricSource{name: "fake/vm", rangeRes: metricRes},
		&fakeDeploySource{name: "fake/argocd"},
		&fakeClusterSource{name: "fake/k8s"},
	)

	corr := newTestCorrelator()
	ref := model.ResourceRef{Cluster: "test-cluster", Namespace: "default", Kind: "Deployment", Name: "api"}
	rng := sources.TimeRange{Start: t0.Add(-5 * time.Minute), End: t1.Add(5 * time.Minute)}

	tl := corr.Timeline(context.Background(), provider, ref, rng)

	var metricSig *model.Signal
	for i := range tl {
		if tl[i].Kind == model.SignalMetric {
			metricSig = &tl[i]
		}
	}
	if metricSig == nil {
		t.Fatalf("metric signal absent from timeline: %+v", tl)
	}
	if metricSig.Severity != model.SeverityError {
		t.Fatalf("anomalous metric peak should be SeverityError, got %v", metricSig.Severity)
	}
	if !metricSig.Timestamp.Equal(t1) {
		t.Fatalf("metric signal should be placed at the peak point %v, got %v", t1, metricSig.Timestamp)
	}
	if metricSig.Resource.Name != "api" {
		t.Fatalf("metric signal should attribute to workload from labels, got %q", metricSig.Resource.Name)
	}

	inc := &model.Incident{OpenedAt: t1.Add(time.Minute), Timeline: tl}
	hyps := NewRanker().Rank(inc)
	var hasMetric bool
	for _, h := range hyps {
		if h.Category == "metric" {
			hasMetric = true
		}
	}
	if !hasMetric {
		t.Fatalf("expected a metric-anomaly hypothesis, got %+v", hyps)
	}
}

// TestCorrelatorMetricSignalsSkipNonFinite pins the NaN/Inf BLOCKER: the default
// 5xx/total PromQL yields 0/0 = NaN for idle workloads and the VM adapter passes
// NaN/±Inf through as real values. A non-finite sample must never reach a Signal
// (its Payload["value"]/Title), a series with a NaN seed must still report its
// finite peak (0.02, not NaN), an all-non-finite series must emit no signal, and
// a timeline carrying such a series must json.Marshal cleanly — the exact
// Postgres.SaveIncident (and memory-store API encoder) failure path.
func TestCorrelatorMetricSignalsSkipNonFinite(t *testing.T) {
	t0 := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	tp := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Minute) }

	metricRes := sources.MetricResult{Series: []sources.MetricSeries{
		{
			// All non-finite: NaN + ±Inf → no finite point → no signal.
			Labels: map[string]string{"namespace": "default", "workload": "idle"},
			Points: []sources.MetricPoint{
				{T: tp(0), V: math.NaN()},
				{T: tp(1), V: math.Inf(1)},
				{T: tp(2), V: math.Inf(-1)},
			},
		},
		{
			// NaN seed followed by a finite point: peak must be 0.02, not NaN.
			Labels: map[string]string{"namespace": "default", "workload": "api"},
			Points: []sources.MetricPoint{
				{T: tp(0), V: math.NaN()},
				{T: tp(1), V: 0.02},
			},
		},
	}}

	provider := sources.NewProvider(
		"test-cluster",
		&fakeLogSource{name: "fake/loki"},
		&stubMetricSource{name: "fake/vm", rangeRes: metricRes},
		&fakeDeploySource{name: "fake/argocd"},
		&fakeClusterSource{name: "fake/k8s"},
	)

	corr := newTestCorrelator()
	ref := model.ResourceRef{Cluster: "test-cluster", Namespace: "default", Kind: "Deployment", Name: "api"}
	rng := sources.TimeRange{Start: tp(0).Add(-5 * time.Minute), End: tp(2).Add(5 * time.Minute)}

	tl := corr.Timeline(context.Background(), provider, ref, rng)

	var metricSigs []model.Signal
	for _, s := range tl {
		if s.Kind == model.SignalMetric {
			metricSigs = append(metricSigs, s)
		}
	}
	// Only the finite-bearing "api" series should emit a signal; the all-NaN/Inf
	// "idle" series is dropped.
	if len(metricSigs) != 1 {
		t.Fatalf("expected exactly one metric signal (all-non-finite series dropped), got %d: %+v", len(metricSigs), metricSigs)
	}

	sig := metricSigs[0]
	// (b) correct finite peak: 0.02, placed at its point, not NaN.
	v, ok := sig.Payload["value"].(float64)
	if !ok {
		t.Fatalf("metric signal Payload[value] not a float64: %#v", sig.Payload["value"])
	}
	if v != 0.02 {
		t.Fatalf("finite peak must be 0.02, got %v", v)
	}
	if !sig.Timestamp.Equal(tp(1)) {
		t.Fatalf("peak must be placed at the finite point %v, got %v", tp(1), sig.Timestamp)
	}

	// (a) no NaN/Inf anywhere in any emitted signal's Payload values.
	for _, s := range metricSigs {
		for k, val := range s.Payload {
			if f, ok := val.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
				t.Fatalf("emitted signal Payload[%q] is non-finite: %v", k, f)
			}
		}
	}

	// (c) an incident carrying this timeline must marshal cleanly — this is the
	// exact SaveIncident (json.Marshal) failure path that NaN/Inf would break.
	inc := &model.Incident{OpenedAt: tp(2), Timeline: tl}
	if _, err := json.Marshal(inc.Timeline); err != nil {
		t.Fatalf("timeline with a NaN-bearing series must marshal cleanly, got: %v", err)
	}
}

// TestCorrelatorMetricSourceErrorSkipped proves a metric backend that errors (or
// times out) is skipped without failing the timeline: the other sources' signals
// still come through.
func TestCorrelatorMetricSourceErrorSkipped(t *testing.T) {
	t0 := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	eventSig := model.Signal{ID: "e1", Kind: model.SignalK8sEvent, Timestamp: t0, Title: "BackOff"}

	provider := sources.NewProvider(
		"test-cluster",
		&fakeLogSource{name: "fake/loki"},
		&stubMetricSource{name: "fake/vm", rangeErr: errors.New("vm unavailable")},
		&fakeDeploySource{name: "fake/argocd"},
		&fakeClusterSource{name: "fake/k8s", sigs: []model.Signal{eventSig}},
	)

	corr := newTestCorrelator()
	ref := model.ResourceRef{Cluster: "test-cluster", Namespace: "default", Kind: "Deployment", Name: "api"}
	rng := sources.TimeRange{Start: t0.Add(-5 * time.Minute), End: t0.Add(5 * time.Minute)}

	tl := corr.Timeline(context.Background(), provider, ref, rng)

	if len(tl) != 1 || tl[0].ID != "e1" {
		t.Fatalf("metric error must not blank the timeline; want just the k8s event, got %+v", tl)
	}
	for _, s := range tl {
		if s.Kind == model.SignalMetric {
			t.Fatalf("errored metric source should contribute no signals, got %+v", s)
		}
	}
}

// TestRankerMetricRanksBelowDeploy pins the ordering invariant: even a
// MINIMALLY-recent deploy-before-incident (near the decayed 0.4 confidence
// floor) stays the top change-first hypothesis, ahead of a metric anomaly.
// A 14-min-old deploy has conf = 0.9 - 0.5*(14/15) ≈ 0.433, just above the
// 0.39 metric cap — the tightest in-window case, so it guards the invariant.
func TestRankerMetricRanksBelowDeploy(t *testing.T) {
	opened := time.Now()
	inc := &model.Incident{
		OpenedAt: opened,
		Timeline: []model.Signal{
			{Kind: model.SignalChange, Timestamp: opened.Add(-14 * time.Minute), Change: &model.ChangeRef{App: "api", Revision: "deadbeef"}},
			{Kind: model.SignalMetric, Timestamp: opened.Add(-1 * time.Minute), Severity: model.SeverityError, Title: "metric peaked at 0.42 on api"},
		},
	}
	hyps := NewRanker().Rank(inc)
	if len(hyps) < 2 {
		t.Fatalf("expected deploy + metric hypotheses, got %+v", hyps)
	}
	if hyps[0].Category != "deploy" {
		t.Fatalf("deploy-before-incident must stay top, got %q", hyps[0].Category)
	}
	var metricConf, deployConf float64
	for _, h := range hyps {
		switch h.Category {
		case "metric":
			metricConf = h.Confidence
		case "deploy":
			deployConf = h.Confidence
		}
	}
	if !(deployConf > metricConf) {
		t.Fatalf("recent deploy (%.2f) must outrank metric anomaly (%.2f)", deployConf, metricConf)
	}
}
