package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// defaultPerSourceTimeout bounds each individual source call in a Timeline
// gather so one slow or hung source cannot stall the whole gather. It is used
// when Correlator.PerSourceTimeout is left at its zero value.
const defaultPerSourceTimeout = 10 * time.Second

// Metric-gather defaults. The Correlator has no per-investigation query context,
// so it evaluates a sensible default PromQL over the incident window: the
// per-workload HTTP error rate. These are used when the corresponding Correlator
// fields are left at their zero value, and are easily overridden via the
// Correlator's MetricQuery / MetricStep / MetricAnomalyThreshold fields.
const (
	// defaultMetricQuery is the per-workload rate of application HTTP errors. It
	// targets app_http_requests_errors_total — the error counter the repo's own
	// demo (deploy/local/demo) emits — instead of a generic http_requests_total,
	// which no bundled workload exposes (ENG-1). Grouping by (namespace, app)
	// matches the demo's labels and lets ResourceFromLabels attribute the series.
	// An empty result (no such metric) simply yields no signal — degradation-tolerant.
	defaultMetricQuery = `sum by (namespace, app) (rate(app_http_requests_errors_total[5m]))`
	// defaultMetricStep is the range-query resolution.
	defaultMetricStep = time.Minute
	// defaultMetricThreshold is the value (errors/sec) at/above which a series' peak
	// is treated as an anomaly (SeverityError) and thus eligible to become a ranked
	// hypothesis.
	defaultMetricThreshold = 0.05
)

// Correlator gathers signals across all of a cluster's sources for a resource +
// window and merges them into one time-ordered timeline. It tolerates per-source
// failure: a Loki outage must not blind the metrics or Kubernetes view.
type Correlator struct {
	logger *slog.Logger

	// PerSourceTimeout caps each individual source call. A zero value falls back
	// to defaultPerSourceTimeout.
	PerSourceTimeout time.Duration

	// MetricQuery is the PromQL evaluated over the incident window to surface
	// metric signals. A zero value falls back to defaultMetricQuery.
	MetricQuery string
	// MetricStep is the metric range-query resolution. Zero falls back to
	// defaultMetricStep.
	MetricStep time.Duration
	// MetricAnomalyThreshold is the value at/above which a series' peak point is
	// flagged as an anomaly (SeverityError). Zero falls back to
	// defaultMetricThreshold.
	MetricAnomalyThreshold float64
}

// NewCorrelator constructs a Correlator.
func NewCorrelator(logger *slog.Logger) *Correlator { return &Correlator{logger: logger} }

// perSourceTimeout returns the effective per-source deadline.
func (c *Correlator) perSourceTimeout() time.Duration {
	if c.PerSourceTimeout > 0 {
		return c.PerSourceTimeout
	}
	return defaultPerSourceTimeout
}

// metricQuery returns the effective PromQL.
func (c *Correlator) metricQuery() string {
	if c.MetricQuery != "" {
		return c.MetricQuery
	}
	return defaultMetricQuery
}

// metricStep returns the effective range step.
func (c *Correlator) metricStep() time.Duration {
	if c.MetricStep > 0 {
		return c.MetricStep
	}
	return defaultMetricStep
}

// metricThreshold returns the effective anomaly threshold.
func (c *Correlator) metricThreshold() float64 {
	if c.MetricAnomalyThreshold > 0 {
		return c.MetricAnomalyThreshold
	}
	return defaultMetricThreshold
}

// Timeline returns the merged, time-sorted signal timeline for a resource.
func (c *Correlator) Timeline(ctx context.Context, p sources.Provider, ref model.ResourceRef, rng sources.TimeRange) []model.Signal {
	var out []model.Signal
	add := func(source string, sigs []model.Signal, err error) {
		if err != nil {
			c.logger.Warn("source query failed; continuing", "source", source, "resource", ref.Key(), "err", err)
			return
		}
		out = append(out, sigs...)
	}

	timeout := c.perSourceTimeout()

	// withTimeout wraps a per-source call in its own deadline so a single slow or
	// hung source cannot stall the whole gather. Parent-context cancellation still
	// propagates because the timeout context derives from ctx.
	withTimeout := func(fn func(ctx context.Context)) {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		fn(callCtx)
	}

	withTimeout(func(callCtx context.Context) {
		logs, err := p.Logs().QueryLogs(callCtx, sources.LogQuery{Resource: ref, Range: rng, Limit: 500})
		add(p.Logs().Name(), logs, err)
	})

	withTimeout(func(callCtx context.Context) {
		changes, err := p.Deployments().ChangeEvents(callCtx, sources.ChangeQuery{Resource: ref, Range: rng})
		add(p.Deployments().Name(), changes, err)
	})

	withTimeout(func(callCtx context.Context) {
		events, err := p.Resources().Events(callCtx, sources.EventQuery{Resource: ref, Range: rng})
		add(p.Resources().Name(), events, err)
	})

	withTimeout(func(callCtx context.Context) {
		res, err := p.Metrics().QueryRange(callCtx, sources.MetricRangeQuery{
			PromQL: c.metricQuery(),
			Range:  rng,
			Step:   c.metricStep(),
		})
		add(p.Metrics().Name(), c.metricSignals(ref, p.Metrics().Name(), res), err)
	})

	sortSignals(out)
	return out
}

// metricSignals converts a MetricResult into timeline signals: one signal per
// series, placed at that series' peak point in the window. A peak at/above the
// anomaly threshold is flagged SeverityError so the ranker can turn it into a
// metric-anomaly hypothesis; quieter series land as SeverityInfo context. It is
// defensively coded: empty results and point-less series yield no signals.
func (c *Correlator) metricSignals(ref model.ResourceRef, source string, res sources.MetricResult) []model.Signal {
	threshold := c.metricThreshold()
	var out []model.Signal
	for i, s := range res.Series {
		if len(s.Points) == 0 {
			continue
		}
		// Skip non-finite samples (NaN/±Inf). PromQL readily produces them (e.g. a
		// ratio's 0/0 for an idle workload), and the VM adapter passes NaN/±Inf
		// through as real values. Letting one reach Signal.Payload/Title poisons the whole
		// incident: Postgres.SaveIncident's json.Marshal(Timeline) ERRORS on NaN/Inf
		// (dropping even a deploy-caused incident that merely gathered one NaN
		// series), and the memory-store API JSON encoder fails the same way. Seed the
		// peak from the FIRST finite point (a NaN seed makes `pt.V > peak.V` always
		// false, yielding a NaN peak even when finite points exist); if a series has
		// no finite point at all, emit no signal for it.
		var peak sources.MetricPoint
		havePeak := false
		for _, pt := range s.Points {
			if math.IsNaN(pt.V) || math.IsInf(pt.V, 0) {
				continue
			}
			if !havePeak || pt.V > peak.V {
				peak = pt
				havePeak = true
			}
		}
		if !havePeak {
			continue
		}
		// Attribute the series to the resource its labels identify; fall back to
		// the queried resource when the labels carry no workload identity.
		sigRef := ref
		if r := model.ResourceFromLabels(ref.Cluster, s.Labels); r.Name != "" {
			sigRef = r
		}
		sev := model.SeverityInfo
		if peak.V >= threshold {
			sev = model.SeverityError
		}
		out = append(out, model.Signal{
			ID:        fmt.Sprintf("metric-%s-%d", sigRef.Key(), i),
			Kind:      model.SignalMetric,
			Resource:  sigRef,
			Timestamp: peak.T,
			Severity:  sev,
			Source:    source,
			Title:     fmt.Sprintf("metric peaked at %.4g on %s", peak.V, sigRef.Name),
			Labels:    s.Labels,
			Payload:   map[string]any{"value": peak.V, "threshold": threshold, "query": c.metricQuery()},
		})
	}
	return out
}
