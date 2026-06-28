package detector

import (
	"context"
	"fmt"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// LogDetector opens a candidate when error-level log volume for a workload
// exceeds MinErrors within the scope window.
type LogDetector struct {
	MinErrors int
}

// NewLogDetector returns the default log-error-burst detector.
func NewLogDetector() LogDetector { return LogDetector{MinErrors: 25} }

func (d LogDetector) Name() string { return "log-error-burst" }

func (d LogDetector) Detect(ctx context.Context, p sources.Provider, scope Scope) ([]Candidate, error) {
	sigs, err := p.Logs().QueryLogs(ctx, sources.LogQuery{
		Resource: model.ResourceRef{Cluster: p.Cluster(), Namespace: scope.Namespace},
		Range:    scope.Range,
		Limit:    1000,
	})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	trigger := map[string]model.Signal{}
	for _, s := range sigs {
		if s.Severity < model.SeverityError {
			continue
		}
		k := s.Resource.Workload().Key()
		counts[k]++
		if _, ok := trigger[k]; !ok {
			trigger[k] = s
		}
	}
	var out []Candidate
	for k, n := range counts {
		if n < d.MinErrors {
			continue
		}
		t := trigger[k]
		out = append(out, Candidate{
			Resource: t.Resource.Workload(),
			At:       t.Timestamp,
			Severity: model.SeverityError,
			Title:    fmt.Sprintf("%d error logs in window on %s", n, t.Resource.Name),
			Trigger:  t,
		})
	}
	return out, nil
}

var _ Detector = LogDetector{}
