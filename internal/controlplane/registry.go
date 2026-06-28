package controlplane

import (
	"log/slog"
	"sync"

	"lotsman/internal/agentlink"
	"lotsman/internal/sources"
	"lotsman/internal/sources/remote"
)

// Registry tracks how to reach each cluster's signals and resolves a
// sources.Provider per cluster. It implements engine.ProviderResolver, and is
// the single place that knows whether a cluster is served directly (agentless)
// or via a connected agent (remote proxy) — the engine never finds out.
type Registry struct {
	mu     sync.RWMutex
	logger *slog.Logger                // optional; nil in tests
	links  map[string]agentlink.Link   // cluster -> agent link (agent mode)
	direct map[string]sources.Provider // cluster -> concrete provider (direct mode)
	remote map[string]remoteEntry      // cluster -> memoized remote proxy (agent mode)
}

// remoteEntry caches a remote proxy together with the link it wraps, so a
// reconnect that replaces the link transparently invalidates the stale wrapper.
type remoteEntry struct {
	link     agentlink.Link
	provider sources.Provider
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		links:  make(map[string]agentlink.Link),
		direct: make(map[string]sources.Provider),
		remote: make(map[string]remoteEntry),
	}
}

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
	defer r.mu.Unlock()
	if _, exists := r.links[link.Cluster()]; exists && r.logger != nil {
		r.logger.Warn("agent link replaced for already-connected cluster", "cluster", link.Cluster())
	}
	r.links[link.Cluster()] = link
}

// OnAgentDisconnect removes a cluster's agent link when its stream ends, so the
// scheduler stops scanning a dead cluster. It deletes only if the stored link is
// still the one disconnecting: if the agent already reconnected and replaced the
// link, the newer link is left intact.
func (r *Registry) OnAgentDisconnect(link agentlink.Link) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.links[link.Cluster()]; ok && cur == link {
		delete(r.links, link.Cluster())
		delete(r.remote, link.Cluster())
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
