package detector

import (
	"context"
	"fmt"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// MetricDetector opens a candidate when a PromQL expression crosses a threshold.
// The default expression targets HTTP 5xx error-rate per workload.
type MetricDetector struct {
	Expr      string
	Threshold float64
}

// NewMetricDetector returns the default error-rate detector.
func NewMetricDetector() MetricDetector {
	return MetricDetector{
		Expr:      `sum by (namespace, workload) (rate(http_requests_total{code=~"5.."}[5m])) / sum by (namespace, workload) (rate(http_requests_total[5m]))`,
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
			Title:    fmt.Sprintf("error-rate %.1f%% over threshold on %s", v*100, ref.Name),
			Trigger: model.Signal{
				Kind: model.SignalMetric, Source: p.Metrics().Name(), Resource: ref,
				Timestamp: scope.Range.End, Severity: model.SeverityError, Title: "metric anomaly",
			},
		})
	}
	return out, nil
}

var _ Detector = MetricDetector{}
