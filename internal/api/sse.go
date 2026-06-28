package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"lotsman/internal/model"
)

// heartbeatInterval is how often a comment line is written to keep the SSE
// connection (and any intermediary proxies) from idling out.
const heartbeatInterval = 15 * time.Second

// handleStream is a Server-Sent Events endpoint for live incident updates. It
// subscribes to the incident bus and writes `data: <json>\n\n` per incident,
// with a periodic `: ping` heartbeat, until the client disconnects.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	// Scope the stream to what the user may view: the incident bus is a global
	// broadcast across every cluster, so without this filter a namespace-scoped
	// viewer would receive incidents they have no binding for — the same
	// deny-by-default scoping handleListIncidents/handleGetIncident enforce. The
	// SSO-disabled enforcer is global admin, so local dev still sees everything.
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, nil)
		return
	}
	enf := s.cfg.Auth.Enforcer(user)
	// Clear the server WriteTimeout for this connection only: an SSE stream is
	// long-lived and must not be cut off by the default write deadline.
	clearWriteDeadline(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if s.cfg.Bus == nil {
		// No bus wired (shouldn't happen in normal assembly): hold the
		// connection open without delivering events.
		_, _ = w.Write([]byte(": connected\n\n"))
		flusher.Flush()
		<-r.Context().Done()
		return
	}

	incidents, unsubscribe := s.cfg.Bus.Subscribe()
	defer unsubscribe()

	ctx := r.Context()
	if _, err := w.Write([]byte(": connected\n\n")); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case inc, ok := <-incidents:
			if !ok {
				return
			}
			// Drop incidents outside the subscriber's RBAC scope.
			if !enf.CanView(inc.Resource.Cluster, inc.Resource.Namespace) {
				continue
			}
			if !s.writeIncident(w, flusher, inc) {
				return
			}
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeIncident marshals and writes one SSE data frame. It returns false if the
// client connection is gone so the caller can stop.
func (s *Server) writeIncident(w http.ResponseWriter, flusher http.Flusher, inc *model.Incident) bool {
	payload, err := json.Marshal(inc)
	if err != nil {
		s.logger.Warn("sse: marshal incident failed", "id", inc.ID, "err", err)
		return true // skip this one, keep the stream open
	}
	// Assemble the full event frame in one buffer and write it once, so an event
	// isn't fragmented across multiple TCP segments.
	var buf bytes.Buffer
	buf.Grow(len("data: ") + len(payload) + len("\n\n"))
	buf.WriteString("data: ")
	buf.Write(payload)
	buf.WriteString("\n\n")
	if _, err := w.Write(buf.Bytes()); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
