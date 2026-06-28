// Package engine is Lotsman's correlation/investigation core — the defensible
// product differentiator. It joins logs, metrics, change events, and Kubernetes
// events on (ResourceRef, time) to build incident timelines and rank probable
// causes. It depends only on sources.Provider, so it is backend- and
// location-agnostic (ADR-0003): the same code runs over a local concrete
// provider (direct mode) or a remote agent proxy (agent mode).
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"lotsman/internal/engine/detector"
	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// ProviderResolver returns the sources.Provider for a cluster.
type ProviderResolver interface {
	Provider(cluster string) (sources.Provider, error)
}

// DefaultWindow is how far back Investigate looks when no window is given.
const DefaultWindow = 30 * time.Minute

// Engine orchestrates detection, correlation, and ranking.
type Engine struct {
	resolver   ProviderResolver
	detectors  []detector.Detector
	correlator *Correlator
	ranker     *Ranker
	logger     *slog.Logger
	now        func() time.Time
}

// New builds an Engine. With no detectors, DefaultDetectors() is used.
func New(resolver ProviderResolver, logger *slog.Logger, detectors ...detector.Detector) *Engine {
	if len(detectors) == 0 {
		detectors = DefaultDetectors()
	}
	return &Engine{
		resolver:   resolver,
		detectors:  detectors,
		correlator: NewCorrelator(logger),
		ranker:     NewRanker(),
		logger:     logger,
		now:        time.Now,
	}
}

// DefaultDetectors is the standard detector set (k8s events, metric anomaly,
// log error burst).
func DefaultDetectors() []detector.Detector {
	return []detector.Detector{
		detector.KubernetesDetector{},
		detector.NewMetricDetector(),
		detector.NewLogDetector(),
	}
}

// Investigate builds an Incident for a resource around a point in time: gather
// the correlated timeline across all sources, then rank probable causes.
func (e *Engine) Investigate(ctx context.Context, ref model.ResourceRef, around time.Time, window time.Duration) (*model.Incident, error) {
	p, err := e.resolver.Provider(ref.Cluster)
	if err != nil {
		return nil, err
	}
	return e.investigateWith(ctx, p, ref, around, window), nil
}

// investigateWith builds an Incident using an already-resolved provider, so a
// caller that investigates many candidates in one cluster (the scheduler) can
// resolve the provider once and reuse it across the loop.
func (e *Engine) investigateWith(ctx context.Context, p sources.Provider, ref model.ResourceRef, around time.Time, window time.Duration) *model.Incident {
	if window <= 0 {
		window = DefaultWindow
	}
	// Look mostly backward from the incident, with a short look-ahead.
	rng := sources.TimeRange{Start: around.Add(-window), End: around.Add(window / 4)}
	timeline := e.correlator.Timeline(ctx, p, ref, rng)
	inc := &model.Incident{
		ID:        incidentID(ref, around),
		Resource:  ref,
		Title:     fmt.Sprintf("Investigation: %s/%s", ref.Namespace, ref.Name),
		Status:    model.IncidentInvestigating,
		Severity:  maxSeverity(timeline),
		OpenedAt:  around,
		UpdatedAt: e.now(),
		Timeline:  timeline,
	}
	inc.Hypotheses = e.ranker.Rank(inc)
	return inc
}

// Scan runs all detectors over a cluster scope and returns candidate incidents.
// A scheduler (not part of the scaffold) calls this periodically and promotes
// candidates via Investigate.
func (e *Engine) Scan(ctx context.Context, cluster string, scope detector.Scope) ([]detector.Candidate, error) {
	p, err := e.resolver.Provider(cluster)
	if err != nil {
		return nil, err
	}
	return e.detect(ctx, p, cluster, scope), nil
}

// detect runs all detectors over an already-resolved provider, tolerating
// per-detector failure.
func (e *Engine) detect(ctx context.Context, p sources.Provider, cluster string, scope detector.Scope) []detector.Candidate {
	var candidates []detector.Candidate
	for _, d := range e.detectors {
		cs, err := d.Detect(ctx, p, scope)
		if err != nil {
			e.logger.Warn("detector failed; continuing", "detector", d.Name(), "cluster", cluster, "err", err)
			continue
		}
		candidates = append(candidates, cs...)
	}
	return candidates
}

// ScanAndInvestigate is the scheduler's hot path: it resolves the cluster's
// provider ONCE, runs detection, then investigates every candidate against that
// same provider — avoiding the per-candidate provider re-resolution that
// calling Scan followed by a separate Investigate per candidate would incur.
// The resulting incidents are returned in detection order, each built with the
// default investigation window. On any error — including ctx cancellation
// mid-loop — it returns a nil slice and the error: a partial batch is not
// returned, because the sole caller (scanCluster) discards results on error and
// the publish path bails on a cancelled context anyway. The next tick re-scans.
func (e *Engine) ScanAndInvestigate(ctx context.Context, cluster string, scope detector.Scope) ([]*model.Incident, error) {
	p, err := e.resolver.Provider(cluster)
	if err != nil {
		return nil, err
	}
	candidates := e.detect(ctx, p, cluster, scope)
	incidents := make([]*model.Incident, 0, len(candidates))
	for _, c := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		incidents = append(incidents, e.investigateWith(ctx, p, c.Resource, c.At, 0))
	}
	return incidents, nil
}

func incidentID(ref model.ResourceRef, t time.Time) string {
	slug := strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(ref.Key())
	return fmt.Sprintf("inc-%s-%d", slug, t.Unix())
}
