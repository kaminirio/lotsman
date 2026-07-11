package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"lotsman/internal/model"
)

// Memory is an in-memory Store for development and tests. The PostgreSQL
// implementation (store/postgres, pgx) replaces it in production.
type Memory struct {
	mu        sync.RWMutex
	incidents map[string]*model.Incident
	clusters  map[string]Cluster
	// tokens is keyed by token ID; tokensByHash maps a token's hash to its ID so
	// the gateway's by-hash lookup stays O(1).
	tokens       map[string]EnrollmentToken
	tokensByHash map[string]string
	// users is keyed by user ID.
	users map[string]User
}

// NewMemory constructs an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		incidents:    make(map[string]*model.Incident),
		clusters:     make(map[string]Cluster),
		tokens:       make(map[string]EnrollmentToken),
		tokensByHash: make(map[string]string),
		users:        make(map[string]User),
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
	// Always bound the result set, mirroring the Postgres store: an unset Limit
	// falls back to DefaultIncidentListLimit rather than returning everything
	// (STORE-3).
	if lim := f.effectiveLimit(); len(out) > lim {
		out = out[:lim]
	}
	return out, nil
}

func (m *Memory) SaveCluster(_ context.Context, c Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Preserve descriptive fields the caller left empty (e.g. a live agent connect
	// carries only name+connected) so it can't wipe env/region/version recorded by
	// seed or an earlier save — mirroring the Postgres COALESCE upsert. connected
	// always reflects the latest call.
	if prev, ok := m.clusters[c.Name]; ok {
		if c.Env == "" {
			c.Env = prev.Env
		}
		if c.Region == "" {
			c.Region = prev.Region
		}
		if c.AgentVersion == "" {
			c.AgentVersion = prev.AgentVersion
		}
	}
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

func (m *Memory) SaveEnrollmentToken(_ context.Context, t EnrollmentToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[t.ID] = t
	m.tokensByHash[t.Hash] = t.ID
	return nil
}

func (m *Memory) GetEnrollmentTokenByHash(_ context.Context, hash string) (EnrollmentToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.tokensByHash[hash]
	if !ok {
		return EnrollmentToken{}, ErrNotFound
	}
	return m.tokens[id], nil
}

func (m *Memory) ListEnrollmentTokens(_ context.Context) ([]EnrollmentToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]EnrollmentToken, 0, len(m.tokens))
	for _, t := range m.tokens {
		out = append(out, t)
	}
	// Newest first.
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) RevokeEnrollmentToken(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok {
		return ErrNotFound
	}
	// Keep the record so it stays listed; mark it revoked.
	t.Revoked = true
	m.tokens[id] = t
	return nil
}

func (m *Memory) CreateUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if strings.EqualFold(existing.Username, u.Username) || strings.EqualFold(existing.Email, u.Email) {
			return ErrConflict
		}
	}
	m.users[u.ID] = u
	return nil
}

func (m *Memory) GetUserByID(_ context.Context, id string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *Memory) GetUserByUsername(_ context.Context, username string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Username, username) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *Memory) GetUserByEmail(_ context.Context, email string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *Memory) GetUserBySSO(_ context.Context, provider, subject string) (User, error) {
	if provider == "" || subject == "" {
		return User{}, ErrNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.SSOProvider == provider && u.SSOSubject == subject {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *Memory) ListUsers(_ context.Context) ([]User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	// Newest first.
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) UpdateUser(_ context.Context, id string, patch UserPatch) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	// Atomic last-admin guard: while the mutex is held, refuse a change that would
	// strip active-admin status from the last active admin. Holding the lock across
	// the count-and-write closes the check-then-act race the API handler alone
	// cannot (two concurrent demotions each seeing a safe count).
	if patch.GuardLastActiveAdmin && u.IsAdmin && u.Active && m.lockedActiveAdmins() <= 1 {
		return User{}, ErrConflict
	}
	if patch.IsAdmin != nil {
		u.IsAdmin = *patch.IsAdmin
	}
	if patch.Active != nil {
		u.Active = *patch.Active
	}
	if patch.PasswordHash != nil {
		u.PasswordHash = *patch.PasswordHash
	}
	if patch.SSOProvider != nil {
		u.SSOProvider = *patch.SSOProvider
	}
	if patch.SSOSubject != nil {
		u.SSOSubject = *patch.SSOSubject
	}
	u.UpdatedAt = time.Now()
	m.users[id] = u
	return u, nil
}

func (m *Memory) DeleteUser(_ context.Context, id string, guardLastActiveAdmin bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return ErrNotFound
	}
	// Atomic last-admin guard (see UpdateUser): refuse to delete the last active
	// admin while the mutex is held.
	if guardLastActiveAdmin && u.IsAdmin && u.Active && m.lockedActiveAdmins() <= 1 {
		return ErrConflict
	}
	delete(m.users, id)
	return nil
}

func (m *Memory) CountActiveAdmins(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lockedActiveAdmins(), nil
}

// lockedActiveAdmins counts active admins. The caller must already hold m.mu
// (read or write); it exists so the last-admin guard can run inside the same
// critical section as a mutation.
func (m *Memory) lockedActiveAdmins() int {
	n := 0
	for _, u := range m.users {
		if u.IsAdmin && u.Active {
			n++
		}
	}
	return n
}

// Durable reports false: in-memory state is lost on restart.
func (m *Memory) Durable() bool { return false }

var _ Store = (*Memory)(nil)
