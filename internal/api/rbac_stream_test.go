package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lotsman/internal/events"
)

// scopedViewerSSO binds viewer-user to a single namespace (prod/team-a) so the
// stream filter has something narrower than the cluster to enforce.
const scopedViewerSSO = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "bindings": [{"subject": "viewer-user", "role": "viewer", "cluster": "prod", "namespace": "team-a"}],
  "github": {"client_id": "cid", "client_secret": "csecret", "allowed_usernames": ["viewer-user"]}
}`

// TestStreamScopedSubscriberOnlySeesInScopeIncidents is the regression test for
// the SSE RBAC-bypass finding: the incident bus is a global broadcast, so the
// stream handler must drop incidents outside the subscriber's binding scope just
// like handleListIncidents/handleGetIncident do.
func TestStreamScopedSubscriberOnlySeesInScopeIncidents(t *testing.T) {
	srv := testServer(t, scopedViewerSSO)
	bus := events.NewIncidentBus()
	srv.cfg.Bus = bus

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil).WithContext(ctx)
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.handleStream(rec, req)
		close(done)
	}()

	// Wait for the subscription to register before publishing, else the sends race
	// the Subscribe call.
	deadline := time.Now().Add(2 * time.Second)
	for bus.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("stream subscriber never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}

	bus.Publish(incident("in-scope", "prod", "team-a"))
	bus.Publish(incident("wrong-namespace", "prod", "team-b"))
	bus.Publish(incident("wrong-cluster", "staging", "team-a"))

	// Let the handler drain the (buffered, non-dropping) channel, then stop it.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "in-scope") {
		t.Errorf("expected in-scope incident delivered, body = %q", body)
	}
	if strings.Contains(body, "wrong-namespace") {
		t.Errorf("stream leaked an out-of-namespace incident, body = %q", body)
	}
	if strings.Contains(body, "wrong-cluster") {
		t.Errorf("stream leaked an out-of-cluster incident, body = %q", body)
	}
}

// TestStreamUnauthenticatedRejected confirms the stream requires a session when
// SSO is enabled (no cookie -> 401, no subscription created).
func TestStreamUnauthenticatedRejected(t *testing.T) {
	srv := testServer(t, scopedViewerSSO)
	bus := events.NewIncidentBus()
	srv.cfg.Bus = bus

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil) // no cookie
	rec := httptest.NewRecorder()
	srv.handleStream(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for unauthenticated stream, got %d", rec.Code)
	}
	if bus.SubscriberCount() != 0 {
		t.Errorf("unauthenticated request must not subscribe, count = %d", bus.SubscriberCount())
	}
}
