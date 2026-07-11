package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"lotsman/internal/enrollment"
	"lotsman/internal/store"
)

// enrollmentMaxBody caps the create-token request body. The payload is tiny
// (cluster name + ttl); anything larger is rejected before decoding.
const enrollmentMaxBody = 4 << 10 // 4 KiB

// createEnrollmentTokenRequest is the POST body. ttl_hours is optional; 0 or
// absent means the token never expires.
type createEnrollmentTokenRequest struct {
	Cluster  string `json:"cluster"`
	TTLHours int    `json:"ttl_hours"`
}

// enrollmentTokenView is the list/metadata DTO. It deliberately never carries the
// plaintext token or its hash. expires_at is null when the token has no expiry.
type enrollmentTokenView struct {
	ID        string     `json:"id"`
	Cluster   string     `json:"cluster"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Revoked   bool       `json:"revoked"`
}

func toEnrollmentTokenView(t store.EnrollmentToken) enrollmentTokenView {
	v := enrollmentTokenView{
		ID:        t.ID,
		Cluster:   t.Cluster,
		CreatedAt: t.CreatedAt,
		Revoked:   t.Revoked,
	}
	if !t.ExpiresAt.IsZero() {
		exp := t.ExpiresAt
		v.ExpiresAt = &exp
	}
	return v
}

// requireDurableStore rejects enrollment-token operations when the backing store
// cannot persist tokens across restarts (in-memory mode). Enrollment tokens are
// not re-derivable, so issuing them against volatile state would silently lock out
// every agent on the next control-plane restart. Returns false (and writes 503)
// when the store is ephemeral.
func (s *Server) requireDurableStore(w http.ResponseWriter) bool {
	if !s.cfg.Store.Durable() {
		writeError(w, http.StatusServiceUnavailable,
			errors.New("enrollment tokens require a durable store; set LOTSMAN_DATABASE_URL on the control plane"))
		return false
	}
	return true
}

// handleEnrollmentDefaults returns the presentation-only hints the UI uses to
// assemble the copy-paste `helm install lotsman-agent` enroll command: the
// externally reachable agent-gateway address, the public Helm chart reference and
// its version pin, the install namespace, and whether enrollment is even possible
// (durable store). Admin-gated. It never returns any token material.
func (s *Server) handleEnrollmentDefaults(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"gateway_addr":  s.cfg.PublicGatewayAddr,
		"chart":         s.cfg.AgentChart,
		"chart_version": s.cfg.AgentChartVersion,
		"namespace":     "lotsman",
		"durable":       s.cfg.Store.Durable(),
	})
}

// handleCreateEnrollmentToken issues a new per-cluster agent enrollment token.
// Admin-gated. The response is the ONLY place the plaintext token is ever shown.
func (s *Server) handleCreateEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if !s.requireDurableStore(w) {
		return
	}

	// Cap the request body so a malformed or hostile client can't stream an
	// unbounded payload into the decoder (mirrors handleInvestigate).
	r.Body = http.MaxBytesReader(w, r.Body, enrollmentMaxBody)
	var req createEnrollmentTokenRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, err)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	cluster := strings.TrimSpace(req.Cluster)
	if cluster == "" {
		writeError(w, http.StatusBadRequest, errors.New("cluster is required"))
		return
	}

	plaintext, hash, id, err := enrollment.Generate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	now := time.Now()
	tok := store.EnrollmentToken{
		ID:        id,
		Cluster:   cluster,
		Hash:      hash,
		CreatedAt: now,
	}
	if req.TTLHours > 0 {
		tok.ExpiresAt = now.Add(time.Duration(req.TTLHours) * time.Hour)
	}
	if err := s.cfg.Store.SaveEnrollmentToken(r.Context(), tok); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// 201 with the plaintext shown exactly once, alongside the same metadata the
	// list endpoint returns (minus the hash, which is never exposed).
	view := toEnrollmentTokenView(tok)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         view.ID,
		"cluster":    view.Cluster,
		"token":      plaintext,
		"created_at": view.CreatedAt,
		"expires_at": view.ExpiresAt,
		"revoked":    view.Revoked,
	})
}

// handleListEnrollmentTokens lists token metadata, newest first. Admin-gated. It
// never returns the plaintext token or its hash. Unlike minting, listing is NOT
// durability-gated: on an in-memory store it harmlessly returns an empty set, so
// the Clusters page (which loads the list + defaults together) still renders and
// can show the "enrollment disabled, set LOTSMAN_DATABASE_URL" state from defaults.
func (s *Server) handleListEnrollmentTokens(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	tokens, err := s.cfg.Store.ListEnrollmentTokens(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]enrollmentTokenView, 0, len(tokens))
	for _, t := range tokens {
		views = append(views, toEnrollmentTokenView(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": views})
}

// handleRevokeEnrollmentToken revokes a token by id. Admin-gated. 204 on success,
// 404 for an unknown id.
func (s *Server) handleRevokeEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	// Not durability-gated: on an in-memory store there are simply no tokens, so an
	// unknown id falls through to the normal 404. Only minting requires durability.
	id := r.PathValue("id")
	err := s.cfg.Store.RevokeEnrollmentToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
