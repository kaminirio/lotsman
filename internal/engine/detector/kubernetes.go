package detector

import (
	"context"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// KubernetesDetector opens candidates from error-level Kubernetes events
// (CrashLoopBackOff, OOMKilled, FailedScheduling, probe failures, ...).
type KubernetesDetector struct{}

func (KubernetesDetector) Name() string { return "k8s-events" }

func (KubernetesDetector) Detect(ctx context.Context, p sources.Provider, scope Scope) ([]Candidate, error) {
	sigs, err := p.Resources().Events(ctx, sources.EventQuery{
		Resource: model.ResourceRef{Cluster: p.Cluster(), Namespace: scope.Namespace},
		Range:    scope.Range,
	})
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, s := range sigs {
		if s.Severity >= model.SeverityError {
			out = append(out, Candidate{Resource: s.Resource, At: s.Timestamp, Severity: s.Severity, Title: s.Title, Trigger: s})
		}
	}
	return out, nil
}

var _ Detector = KubernetesDetector{}
