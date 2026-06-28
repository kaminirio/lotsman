package engine

import (
	"context"
	"log/slog"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// Correlator gathers signals across all of a cluster's sources for a resource +
// window and merges them into one time-ordered timeline. It tolerates per-source
// failure: a Loki outage must not blind the metrics or Kubernetes view.
type Correlator struct {
	logger *slog.Logger
}

// NewCorrelator constructs a Correlator.
func NewCorrelator(logger *slog.Logger) *Correlator { return &Correlator{logger: logger} }

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

	logs, err := p.Logs().QueryLogs(ctx, sources.LogQuery{Resource: ref, Range: rng, Limit: 500})
	add(p.Logs().Name(), logs, err)

	changes, err := p.Deployments().ChangeEvents(ctx, sources.ChangeQuery{Resource: ref, Range: rng})
	add(p.Deployments().Name(), changes, err)

	events, err := p.Resources().Events(ctx, sources.EventQuery{Resource: ref, Range: rng})
	add(p.Resources().Name(), events, err)

	sortSignals(out)
	return out
}
