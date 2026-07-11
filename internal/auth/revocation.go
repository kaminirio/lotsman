package auth

import (
	"sync"
	"time"
)

// revocationSet is an in-memory denylist of revoked session JTIs, each retained
// only until the token would have expired anyway (so the set self-bounds). It
// makes logout effective for the otherwise-stateless JWT sessions: a logged-out
// token is rejected even though it remains cryptographically valid until expiry.
//
// Caveat: in-memory and per-replica. A multi-replica deployment needs a shared
// store (Redis/Postgres) to revoke across replicas, and the set is lost on
// restart — acceptable because tokens are short-lived (8h) and a restart simply
// re-honors only still-unexpired tokens. Tracked for the durable-store milestone.
type revocationSet struct {
	mu      sync.Mutex
	revoked map[string]time.Time // jti -> token expiry
}

func newRevocationSet() *revocationSet {
	return &revocationSet{revoked: make(map[string]time.Time)}
}

// revoke records jti as revoked until exp (the token's own expiry).
func (s *revocationSet) revoke(jti string, exp time.Time) {
	if jti == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[jti] = exp
	s.gcLocked()
}

// isRevoked reports whether jti is currently revoked, opportunistically dropping
// it once its expiry passes.
func (s *revocationSet) isRevoked(jti string) bool {
	if jti == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.revoked[jti]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.revoked, jti)
		return false
	}
	return true
}

// gcLocked drops entries whose expiry has passed. The caller must hold s.mu.
func (s *revocationSet) gcLocked() {
	now := time.Now()
	for jti, exp := range s.revoked {
		if now.After(exp) {
			delete(s.revoked, jti)
		}
	}
}

// isSessionRevoked reports whether a session was explicitly logged out. It checks
// the stable lineage id (sid) first — logout revokes by sid, so this rejects a
// token even after any number of sliding refreshes minted new jtis — and falls
// back to the per-mint jti for older tokens (pre-sid cookies) that carry no sid.
func (m *Manager) isSessionRevoked(claims *SessionClaims) bool {
	if m.revoked == nil || claims == nil {
		return false
	}
	if claims.SID != "" && m.revoked.isRevoked(claims.SID) {
		return true
	}
	return m.revoked.isRevoked(claims.ID)
}
