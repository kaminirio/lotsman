package controlplane

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"lotsman/internal/agentlink"
	"lotsman/internal/engine"
	"lotsman/internal/engine/detector"
	"lotsman/internal/events"
	"lotsman/internal/model"
	"lotsman/internal/sources"
	"lotsman/internal/store"
)

// clusterLister enumerates the clusters a scan pass should cover. *Registry
// implements it; tests supply a fake.
type clusterLister interface {
	Clusters() []string
}

// scanner detects candidates and investigates them into incidents in a single
// pass, resolving each cluster's provider once. It also investigates a single
// resource on demand — the entry point the push path uses to promote a
// server-pushed watch signal into an incident. *engine.Engine implements both;
// tests supply a fake.
type scanner interface {
	ScanAndInvestigate(ctx context.Context, cluster string, scope detector.Scope) ([]*model.Incident, error)
	Investigate(ctx context.Context, ref model.ResourceRef, around time.Time, window time.Duration) (*model.Incident, error)
}

// pushedSignalSource supplies agent-pushed watch Events. *Registry implements it;
// a nil/absent source (e.g. a test's fakeLister) simply disables the push path,
// leaving the poll scheduler as the sole detector.
type pushedSignalSource interface {
	PushedEvents() <-chan agentlink.Event
}

// Scheduler periodically scans every registered cluster, investigates each
// detector candidate into an incident, persists it, and publishes it on the
// incident bus for live SSE consumers. Its run loop is tied to the context
// passed to Start and stops cleanly on cancellation.
type Scheduler struct {
	clusters clusterLister
	eng      scanner
	store    store.Store
	bus      *events.IncidentBus
	interval time.Duration
	logger   *slog.Logger

	// published remembers the last UpdatedAt published per incident ID so an
	// unchanged incident is not re-emitted every tick. To keep the map from
	// growing without bound as incidents age out, each entry records the tick at
	// which it was last seen; sweepPublished evicts entries not seen for
	// publishedTTLTicks ticks (long enough that an active incident is never
	// evicted between consecutive scans, so dedupe stays correct).
	mu        sync.Mutex
	tickNo    uint64
	published map[string]publishState
}

// publishedTTLTicks is how many ticks an unseen incident lingers in the dedupe
// map before eviction.
const publishedTTLTicks = 4

type publishState struct {
	updatedAt time.Time
	lastTick  uint64
}

// NewScheduler builds a Scheduler. A non-positive interval falls back to 30s.
func NewScheduler(clusters clusterLister, eng scanner, st store.Store, bus *events.IncidentBus, interval time.Duration, logger *slog.Logger) *Scheduler {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Scheduler{
		clusters:  clusters,
		eng:       eng,
		store:     st,
		bus:       bus,
		interval:  interval,
		logger:    logger,
		published: make(map[string]publishState),
	}
}

// Run blocks, scanning on each tick until ctx is cancelled. It runs an initial
// pass immediately so live data appears without waiting a full interval. When the
// cluster source also pushes agent watch signals, Run starts a consumer that
// promotes those into incidents between ticks (push is additive/faster; the poll
// loop below is the safety net).
func (s *Scheduler) Run(ctx context.Context) {
	s.logger.Info("incident scheduler started", "interval", s.interval)
	defer s.logger.Info("incident scheduler stopped")

	if src, ok := s.clusters.(pushedSignalSource); ok {
		go s.consumePushedSignals(ctx, src.PushedEvents())
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// consumePushedSignals drains agent-pushed watch Events and routes each into
// incident detection until ctx is cancelled (no leak: the loop exits on ctx.Done
// or when the feed closes).
func (s *Scheduler) consumePushedSignals(ctx context.Context, feed <-chan agentlink.Event) {
	s.logger.Info("incident push consumer started")
	defer s.logger.Info("incident push consumer stopped")
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-feed:
			if !ok {
				return
			}
			s.handlePushedSignal(ctx, ev)
		}
	}
}

// handlePushedSignal promotes one server-pushed watch signal into an incident.
// It gates on severity so the push path opens incidents for the same class of
// signals the periodic KubernetesDetector does (SeverityError and above),
// investigates the affected resource around the signal's timestamp, and runs the
// result through the same dedupe/persist/publish path as a polled incident — so
// a push and a later poll of the same failure don't double-publish.
func (s *Scheduler) handlePushedSignal(ctx context.Context, ev agentlink.Event) {
	if ev.Signal.Severity < model.SeverityError {
		return
	}
	inc, err := s.eng.Investigate(ctx, ev.Signal.Resource, ev.Signal.Timestamp, 0)
	if err != nil {
		s.logger.Debug("push investigate failed", "cluster", ev.Cluster, "resource", ev.Signal.Resource.Key(), "err", err)
		return
	}
	s.publishIncident(ctx, inc)
	s.logger.Debug("push-triggered investigation", "cluster", ev.Cluster, "resource", ev.Signal.Resource.Key())
}

// tick runs one detection pass across all clusters.
func (s *Scheduler) tick(ctx context.Context) {
	clusters := s.clusters.Clusters()
	if len(clusters) == 0 {
		s.logger.Debug("scan tick: no clusters registered")
		return
	}
	s.advanceTick()
	now := time.Now()
	scope := detector.Scope{
		Range: sources.TimeRange{Start: now.Add(-engine.DefaultWindow), End: now},
	}
	for _, cluster := range clusters {
		if ctx.Err() != nil {
			return
		}
		s.scanCluster(ctx, cluster, scope)
	}
}

// advanceTick bumps the tick counter and evicts dedupe entries not seen for the
// last publishedTTLTicks ticks, bounding the published map's size.
func (s *Scheduler) advanceTick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tickNo++
	for id, st := range s.published {
		if s.tickNo-st.lastTick >= publishedTTLTicks {
			delete(s.published, id)
		}
	}
}

func (s *Scheduler) scanCluster(ctx context.Context, cluster string, scope detector.Scope) {
	incidents, err := s.eng.ScanAndInvestigate(ctx, cluster, scope)
	if err != nil {
		s.logger.Debug("scan failed", "cluster", cluster, "err", err)
		return
	}
	s.logger.Debug("scan complete", "cluster", cluster, "incidents", len(incidents))
	for _, inc := range incidents {
		if ctx.Err() != nil {
			return
		}
		s.publishIncident(ctx, inc)
	}
}

// publishIncident persists and publishes an incident if it is new or changed
// since it was last published (dedupe). Shared by the poll scan loop and the push
// consumer so both paths dedupe against one another.
func (s *Scheduler) publishIncident(ctx context.Context, inc *model.Incident) {
	if !s.shouldPublish(inc) {
		return
	}
	if err := s.store.SaveIncident(ctx, inc); err != nil {
		s.logger.Warn("save incident failed", "id", inc.ID, "err", err)
		return
	}
	s.bus.Publish(inc)
	s.logger.Info("incident detected", "id", inc.ID, "resource", inc.Resource.Key(), "severity", inc.Severity)
}

// shouldPublish reports whether inc is new or changed since it was last
// published, so an identical incident is not re-emitted on every tick.
func (s *Scheduler) shouldPublish(inc *model.Incident) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.published[inc.ID]; ok && prev.updatedAt.Equal(inc.UpdatedAt) {
		// Seen and unchanged: refresh the liveness tick so it isn't evicted, but
		// don't re-publish.
		prev.lastTick = s.tickNo
		s.published[inc.ID] = prev
		return false
	}
	s.published[inc.ID] = publishState{updatedAt: inc.UpdatedAt, lastTick: s.tickNo}
	return true
}
