package store

import (
	"context"
	"sort"
	"sync"

	"lotsman/internal/model"
)

// Memory is an in-memory Store for development and tests. The PostgreSQL
// implementation (store/postgres, pgx) replaces it in production.
type Memory struct {
	mu        sync.RWMutex
	incidents map[string]*model.Incident
	clusters  map[string]Cluster
}

// NewMemory constructs an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		incidents: make(map[string]*model.Incident),
		clusters:  make(map[string]Cluster),
	}
}

func (m *Memory) SaveIncident(_ context.Context, inc *model.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incidents[inc.ID] = inc
	return nil
}

func (m *Memory) GetIncident(_ context.Context, id string) (*model.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inc, ok := m.incidents[id]
	if !ok {
		return nil, ErrNotFound
	}
	// Return a copy of the struct header so a caller can't reassign fields on the
	// stored incident through the shared pointer, matching the Postgres store's
	// fresh-struct semantics. The Timeline/Hypotheses slices still share backing
	// arrays with the stored value; callers treat them as read-only (the engine
	// builds incidents fresh rather than mutating retrieved ones).
	cp := *inc
	return &cp, nil
}

func (m *Memory) ListIncidents(_ context.Context, f IncidentFilter) ([]*model.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*model.Incident
	for _, inc := range m.incidents {
		if f.Cluster != "" && inc.Resource.Cluster != f.Cluster {
			continue
		}
		if f.Status != "" && inc.Status != f.Status {
			continue
		}
		out = append(out, inc)
	}
	// For the common "newest single incident" query, a full O(N log N) sort is
	// wasteful: an O(N) max-by-OpenedAt scan yields the same first element.
	if f.Limit == 1 {
		if len(out) == 0 {
			return out, nil
		}
		newest := out[0]
		for _, inc := range out[1:] {
			if inc.OpenedAt.After(newest.OpenedAt) {
				newest = inc
			}
		}
		return []*model.Incident{newest}, nil
	}
	// Most recent first.
	sort.SliceStable(out, func(i, j int) bool { return out[i].OpenedAt.After(out[j].OpenedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (m *Memory) SaveCluster(_ context.Context, c Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clusters[c.Name] = c
	return nil
}

func (m *Memory) ListClusters(_ context.Context) ([]Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Cluster, 0, len(m.clusters))
	for _, c := range m.clusters {
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

var _ Store = (*Memory)(nil)
