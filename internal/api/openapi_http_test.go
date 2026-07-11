package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The spec route is wired into the mux and returns the embedded OpenAPI YAML with
// the right content-type and a non-empty, well-formed-looking body. Driven through
// routes() (not the handler directly) so route registration is exercised; a valid
// session is supplied because the fixture enables SSO.
func TestOpenAPISpec_ServedThroughMux(t *testing.T) {
	srv := testServer(t, testSSOConfig)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil)
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()

	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Fatalf("content-type = %q, want application/yaml", ct)
	}

	body := rec.Body.String()
	if strings.TrimSpace(body) == "" {
		t.Fatal("spec body is empty")
	}
	// Sanity: it looks like the OpenAPI document we authored (top-level keys and a
	// couple of routes/schemas that must be present), not an accidental blank/HTML.
	for _, want := range []string{
		"openapi: 3.1",
		"paths:",
		"/api/v1/incidents",
		"/api/v1/investigate",
		"/api/v1/clusters/{cluster}/namespaces/{namespace}/pods/{pod}/logs",
		"Error:",
		"tail",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("spec body missing %q", want)
		}
	}
}

// The spec is served without auth when SSO is disabled (local dev), matching the
// transparent pass-through the rest of the API uses.
func TestOpenAPISpec_OpenWhenSSODisabled(t *testing.T) {
	srv := testServer(t, "") // empty SSO config => auth disabled

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (no cookie, SSO disabled)", rec.Code)
	}
}
