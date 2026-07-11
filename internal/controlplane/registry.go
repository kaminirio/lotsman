package controlplane

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"lotsman/internal/agentlink"
	"lotsman/internal/sources"
	"lotsman/internal/sources/remote"
	"lotsman/internal/store"
)

// Registry tracks how to reach each cluster's signals and resolves a
// sources.Provider per cluster. It implements engine.ProviderResolver, and is
// the single place that knows whether a cluster is served directly (agentless)
// or via a connected agent (remote proxy) — the engine never finds out.
type Registry struct {
	mu     sync.RWMutex
	logger *slog.Logger                // optional; nil in tests
	store  store.Store                 // optional; persists cluster connection state
	links  map[string]agentlink.Link   // cluster -> agent link (agent mode)
	direct map[string]sources.Provider // cluster -> concrete provider (direct mode)
	remote map[string]remoteEntry      // cluster -> memoized remote proxy (agent mode)

	// pushCh fans every connected agent's server-pushed watch Events (LINK-1) into
	// a single stream the scheduler consumes via PushedEvents. Buffered so a brief
	// consumer stall doesn't back-pressure a per-agent drain; on overflow signals
	// are dropped (the 30s poll scheduler is the safety net). Never closed — its
	// lifetime is the registry's.
	pushCh chan agentlink.Event
	// drainCtx bounds every per-agent drain goroutine so they exit on shutdown even
	// if a link somehow never closes. Defaults to context.Background(); each drain
	// also exits when its link's Events() channel closes on disconnect.
	drainCtx context.Context
	// drainWG tracks live drain goroutines so shutdown/tests can wait them out.
	drainWG sync.WaitGroup
}

// pushBuffer is the depth of the fan-in channel between per-agent drains and the
// scheduler's push consumer.
const pushBuffer = 256

// remoteEntry caches a remote proxy together with the link it wraps, so a
// reconnect that replaces the link transparently invalidates the stale wrapper.
type remoteEntry struct {
	link     agentlink.Link
	provider sources.Provider
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		links:    make(map[string]agentlink.Link),
		direct:   make(map[string]sources.Provider),
		remote:   make(map[string]remoteEntry),
		pushCh:   make(chan agentlink.Event, pushBuffer),
		drainCtx: context.Background(),
	}
}

// PushedEvents exposes the fan-in stream of agent-pushed watch Events so the
// scheduler can route each into incident detection. Implements the scheduler's
// pushedSignalSource seam.
func (r *Registry) PushedEvents() <-chan agentlink.Event { return r.pushCh }

// AddDirect registers a concrete provider for a cluster (direct/agentless mode).
func (r *Registry) AddDirect(p sources.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.direct[p.Cluster()] = p
}

// OnAgentConnect registers an agent link; passed to the gateway as its callback.
// A connection for a cluster that already has a live link replaces it (the normal
// reconnect path), but is logged: with token/mTLS enrollment in place this should
// only ever be a genuine reconnect, so an unexpected replacement is worth seeing.
func (r *Registry) OnAgentConnect(link agentlink.Link) {
	r.mu.Lock()
	if _, exists := r.links[link.Cluster()]; exists && r.logger != nil {
		r.logger.Warn("agent link replaced for already-connected cluster", "cluster", link.Cluster())
	}
	r.links[link.Cluster()] = link
	r.mu.Unlock()

	// Drain this agent's server-pushed watch Events into the shared push stream so
	// the scheduler can investigate them immediately (LINK-1). Started outside the
	// lock — the goroutine never touches r.mu — and self-terminating: it exits when
	// the link's Events() channel closes on disconnect (or on drainCtx cancel).
	r.startEventDrain(link)

	// Persist the cluster as connected so its existence/history survives a control
	// plane restart and it appears in the fleet list even after the agent drops.
	// Done outside the lock (no DB I/O under r.mu) and best-effort: the read-time
	// registry union in handleListClusters remains the liveness fallback.
	r.persistCluster(link.Cluster(), true)
}

// startEventDrain launches one goroutine that copies a link's pushed watch Events
// onto the registry's fan-in channel until the link disconnects (Events() closes)
// or drainCtx is cancelled. The forward is non-blocking: if the scheduler's push
// consumer is backlogged the signal is dropped rather than stalling the drain, so
// a slow consumer can never wedge an agent's stream (poll remains the safety net).
// A nil Events() channel (a link that pushes nothing) starts no goroutine.
func (r *Registry) startEventDrain(link agentlink.Link) {
	events := link.Events()
	if events == nil {
		return
	}
	r.drainWG.Add(1)
	go func() {
		defer r.drainWG.Done()
		for {
			select {
			case <-r.drainCtx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return // link closed on disconnect
				}
				select {
				case r.pushCh <- ev:
				default:
					if r.logger != nil {
						r.logger.Warn("pushed signal dropped: consumer backlog", "cluster", ev.Cluster)
					}
				}
			}
		}
	}()
}

// persistCluster upserts a cluster's connection state into the store when one is
// configured. Best-effort and non-fatal: the registry stays the source of truth
// for liveness; persistence only adds durability and history. It carries just
// name+connected, so the store's upsert preserves any env/region/version already
// recorded (see store.SaveCluster).
func (r *Registry) persistCluster(name string, connected bool) {
	if r.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.store.SaveCluster(ctx, store.Cluster{Name: name, Connected: connected}); err != nil && r.logger != nil {
		r.logger.Warn("persist cluster state failed", "cluster", name, "connected", connected, "err", err)
	}
}

// OnAgentDisconnect removes a cluster's agent link when its stream ends, so the
// scheduler stops scanning a dead cluster. It deletes only if the stored link is
// still the one disconnecting: if the agent already reconnected and replaced the
// link, the newer link is left intact.
func (r *Registry) OnAgentDisconnect(link agentlink.Link) {
	r.mu.Lock()
	removed := false
	if cur, ok := r.links[link.Cluster()]; ok && cur == link {
		delete(r.links, link.Cluster())
		delete(r.remote, link.Cluster())
		removed = true
	}
	r.mu.Unlock()
	// Record the cluster as disconnected (history preserved) once the live link is
	// actually gone — skip a stale-link callback that lost the reconnect race.
	if removed {
		r.persistCluster(link.Cluster(), false)
	}
}

// Clusters returns the union of all known cluster names — those served directly
// and those reachable via a connected agent — so callers (e.g. the scheduler)
// know what to scan. Order is unspecified.
func (r *Registry) Clusters() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{}, len(r.direct)+len(r.links))
	for name := range r.direct {
		seen[name] = struct{}{}
	}
	for name := range r.links {
		seen[name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// Provider resolves the provider for a cluster: a direct provider if registered,
// otherwise a remote proxy over the agent link. The remote proxy is memoized per
// cluster (and rebuilt only when the underlying link changes), so the scheduler's
// repeated per-tick resolution reuses one wrapper instead of allocating a fresh
// one each call. Direct providers are returned as-is, unaffected. Implements
// engine.ProviderResolver.
func (r *Registry) Provider(cluster string) (sources.Provider, error) {
	r.mu.RLock()
	if p, ok := r.direct[cluster]; ok {
		r.mu.RUnlock()
		return p, nil
	}
	link, ok := r.links[cluster]
	if !ok {
		r.mu.RUnlock()
		return nil, agentlink.ErrNotConnected
	}
	if e, ok := r.remote[cluster]; ok && e.link == link {
		r.mu.RUnlock()
		return e.provider, nil
	}
	r.mu.RUnlock()

	// Cache miss (or stale link): build and store the wrapper under the write lock.
	r.mu.Lock()
	defer r.mu.Unlock()
	// Re-check direct: AddDirect may have raced in between the locks.
	if p, ok := r.direct[cluster]; ok {
		return p, nil
	}
	link, ok = r.links[cluster]
	if !ok {
		return nil, agentlink.ErrNotConnected
	}
	if e, ok := r.remote[cluster]; ok && e.link == link {
		return e.provider, nil
	}
	p := remote.NewProvider(link)
	r.remote[cluster] = remoteEntry{link: link, provider: p}
	return p, nil
}
