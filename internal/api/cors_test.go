package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testCORSOrigin = "https://ui.example.com"

// corsServer builds a no-SSO server with the CORS allowlist set via env, and
// returns its full handler chain so requests pass through the cors + auth
// wrappers exactly as in production. No SSO keeps auth a pass-through, isolating
// the CORS behavior under test.
func corsServer(t *testing.T, origins string) http.Handler {
	t.Helper()
	t.Setenv(corsOriginsEnv, origins)
	srv := testServer(t, "")
	return srv.http.Handler
}

// An allowlisted Origin gets its ACAO echoed back (never "*") plus credentials on
// a normal (non-preflight) request, and the request is still served.
func TestCORSAllowedOriginActualRequest(t *testing.T) {
	h := corsServer(t, testCORSOrigin)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.Header.Set("Origin", testCORSOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != testCORSOrigin {
		t.Errorf("ACAO = %q, want %q (echoed, not wildcard)", got, testCORSOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("actual GET should still be served (200), got %d", rec.Code)
	}
}

// A preflight OPTIONS from an allowlisted origin is answered 204 with the CORS
// headers, WITHOUT a session — it short-circuits before the auth middleware.
func TestCORSPreflightAllowed(t *testing.T) {
	h := corsServer(t, testCORSOrigin)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/investigate", nil)
	req.Header.Set("Origin", testCORSOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: got %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != testCORSOrigin {
		t.Errorf("preflight ACAO = %q, want %q", got, testCORSOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("preflight must advertise Access-Control-Allow-Methods")
	}
}

// A preflight OPTIONS for /auth/logout (state-changing, OUTSIDE /api/v1) from an
// allowlisted origin must be answered 204 with the full CORS response headers, so
// a split-origin UI can actually preflight and then log out. Regression for the
// prior /api/v1-only preflight gate that 405'd cross-origin logout.
func TestCORSPreflightLogoutOutsideAPIV1(t *testing.T) {
	h := corsServer(t, testCORSOrigin)
	req := httptest.NewRequest(http.MethodOptions, "/auth/logout", nil)
	req.Header.Set("Origin", testCORSOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "X-Requested-With")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight /auth/logout: got %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != testCORSOrigin {
		t.Errorf("ACAO = %q, want %q", got, testCORSOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("preflight must advertise Access-Control-Allow-Methods")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("preflight must advertise Access-Control-Allow-Headers")
	}
}

// Vary: Origin is emitted whenever CORS is enabled, EVEN for a non-allowlisted
// origin, so a shared cache can never serve the no-CORS variant to an allowed one.
func TestCORSVaryOriginUnconditional(t *testing.T) {
	h := corsServer(t, testCORSOrigin)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.Header.Set("Origin", "https://not-allowed.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Values("Vary"); !containsFold(got, "Origin") {
		t.Errorf("Vary must include Origin even for a non-allowed origin, got %v", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("non-allowed origin must still get no ACAO, got %q", got)
	}
}

func containsFold(vals []string, want string) bool {
	for _, v := range vals {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// A non-allowlisted origin gets NO CORS headers, so the browser blocks the
// cross-origin read.
func TestCORSDisallowedOrigin(t *testing.T) {
	h := corsServer(t, testCORSOrigin)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin must get no ACAO, got %q", got)
	}
}

// With no allowlist configured, CORS is entirely off (secure same-origin
// default): no headers even for a would-be origin.
func TestCORSDisabledByDefault(t *testing.T) {
	h := corsServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.Header.Set("Origin", testCORSOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS must be off by default, got ACAO %q", got)
	}
}

func TestParseCORSOrigins(t *testing.T) {
	got := parseCORSOrigins(" https://a.example.com , , https://b.example.com ,* ")
	if len(got) != 2 {
		t.Fatalf("want 2 origins (blank and * dropped), got %d: %v", len(got), got)
	}
	for _, want := range []string{"https://a.example.com", "https://b.example.com"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing origin %q", want)
		}
	}
	if _, ok := got["*"]; ok {
		t.Error("wildcard * must be dropped (incompatible with credentialed CORS)")
	}
	if len(parseCORSOrigins("")) != 0 {
		t.Error("empty input must yield no origins (CORS disabled)")
	}
}
