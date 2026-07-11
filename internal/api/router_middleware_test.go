package api

// router_middleware_test.go exercises the FULL composed HTTP stack —
// s.http.Handler, i.e. auth.Middleware wrapped around the real mux built by
// routes() — rather than calling handler methods directly (as the rest of the
// package's tests do). This is the only place that proves the middleware is
// actually wired around every route as router.go's comment claims: every
// /api/v1/* route requires a session, and exactly the documented allowlist
// (health, version, providers, login, the OAuth handshake) is reachable
// without one.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutesUnauthenticatedAPIRejected(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	rec := httptest.NewRecorder()

	srv.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/v1/incidents through the real router: got %d, want 401", rec.Code)
	}
}

func TestRoutesAllowlistReachableWithoutSession(t *testing.T) {
	srv := testServer(t, testSSOConfig)

	for _, path := range []string{"/healthz", "/api/v1/version", "/auth/providers"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("allowlisted path %q through the real router: got %d, want 200", path, rec.Code)
		}
	}
}

func TestRoutesLocalLoginReachableAndCSRFGuarded(t *testing.T) {
	srv := testServer(t, testSSOConfig)

	// POST /auth/login is allowlisted (reachable without a session) but still a
	// mutation, so the CSRF header is required even here.
	t.Run("missing CSRF header -> 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("got %d, want 403 (missing X-Requested-With)", rec.Code)
		}
	})

	// With the CSRF header present the request reaches HandleLocalLogin and is
	// rejected on credentials (401), never blocked by the middleware itself.
	t.Run("with CSRF header, bad creds -> 401 not blocked earlier", func(t *testing.T) {
		body := `{"username":"nobody","password":"wrong"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401 (reachable, rejected on credentials)", rec.Code)
		}
	})
}

func TestRoutesOAuthLoginRedirectsUnauthenticated(t *testing.T) {
	srv := testServer(t, testSSOConfig) // testSSOConfig configures github
	req := httptest.NewRequest(http.MethodGet, "/auth/login/github", nil)
	rec := httptest.NewRecorder()

	srv.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("GET /auth/login/github through the real router: got %d, want 302", rec.Code)
	}
}

func TestRoutesOAuthCallbackReachableUnauthenticated(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	// No state cookie set -> the handler itself rejects with 400, proving the
	// middleware let the (unauthenticated) request through to the handler
	// rather than short-circuiting with 401.
	req := httptest.NewRequest(http.MethodGet, "/auth/callback/github?state=x&code=y", nil)
	rec := httptest.NewRecorder()

	srv.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /auth/callback/github through the real router: got %d, want 400 (missing state cookie)", rec.Code)
	}
}
