package detector

import (
	"context"
	"fmt"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// MetricDetector opens a candidate when a PromQL expression crosses a threshold.
// The default expression targets HTTP error-rate (errors/sec) per app, matching
// the correlator's default metric query so Scan-time and investigate-time metric
// signals agree.
type MetricDetector struct {
	Expr      string
	Threshold float64
}

// NewMetricDetector returns the default error-rate detector. Threshold is in
// errors/sec (Expr is a rate, not a ratio).
func NewMetricDetector() MetricDetector {
	return MetricDetector{
		Expr:      `sum by (namespace, app) (rate(app_http_requests_errors_total[5m]))`,
		Threshold: 0.05,
	}
}

func (d MetricDetector) Name() string { return "metric-anomaly" }

func (d MetricDetector) Detect(ctx context.Context, p sources.Provider, scope Scope) ([]Candidate, error) {
	res, err := p.Metrics().QueryInstant(ctx, sources.MetricQuery{PromQL: d.Expr, At: scope.Range.End})
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, s := range res.Series {
		if len(s.Points) == 0 {
			continue
		}
		v := s.Points[len(s.Points)-1].V
		if v < d.Threshold {
			continue
		}
		ref := model.ResourceFromLabels(p.Cluster(), s.Labels)
		out = append(out, Candidate{
			Resource: ref,
			At:       scope.Range.End,
			Severity: model.SeverityError,
			Title:    fmt.Sprintf("error-rate %.3g/s over threshold on %s", v, ref.Name),
			Trigger: model.Signal{
				Kind: model.SignalMetric, Source: p.Metrics().Name(), Resource: ref,
				Timestamp: scope.Range.End, Severity: model.SeverityError, Title: "metric anomaly",
			},
		})
	}
	return out, nil
}

var _ Detector = MetricDetector{}
