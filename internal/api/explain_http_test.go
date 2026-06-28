package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"lotsman/internal/analyze"
	"lotsman/internal/auth"
	"lotsman/internal/engine"
	"lotsman/internal/model"
	"lotsman/internal/rbac"
	"lotsman/internal/store"
)

// fakeExplainer is a hermetic analyze.Explainer: no network, no real Ollama. It
// can be toggled unavailable and can be made to error to exercise the 502 path.
type fakeExplainer struct {
	available bool
	err       error
	exp       analyze.Explanation
}

func (f fakeExplainer) Available() bool { return f.available }
func (f fakeExplainer) Model() string   { return f.exp.Model }
func (f fakeExplainer) Explain(context.Context, *model.Incident) (analyze.Explanation, error) {
	if f.err != nil {
		return analyze.Explanation{}, f.err
	}
	return f.exp, nil
}

// explainServer builds a test server with an explicit Explainer (nil-able) plus a
// seeded incident, reusing the auth/store wiring from the RBAC test helpers.
func explainServer(t *testing.T, ssoJSON string, ex analyze.Explainer, incs ...*model.Incident) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := auth.NewManagerErr(ssoJSON, logger)
	if err != nil {
		t.Fatalf("build auth manager: %v", err)
	}
	st := store.NewMemory()
	for _, inc := range incs {
		if err := st.SaveIncident(context.Background(), inc); err != nil {
			t.Fatalf("seed incident: %v", err)
		}
	}
	srv, err := New(Config{
		Addr:      ":0",
		Version:   "test",
		Engine:    engine.New(failingResolver{}, logger),
		Store:     st,
		Auth:      mgr,
		Sources:   failingResolver{},
		Explainer: ex,
	}, logger)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return srv
}

func TestExplainUnavailableReturns503(t *testing.T) {
	// nil Explainer => not configured => 503, no panic.
	srv := explainServer(t, "", nil, incident("inc-a", "prod", "demo"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-a/explain", nil)
	req.SetPathValue("id", "inc-a")
	rec := httptest.NewRecorder()

	srv.handleExplainIncident(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	var body map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "LLM analyzer not configured" {
		t.Fatalf("error body = %q", body["error"])
	}
}

func TestExplainUnavailableExplainerReturns503(t *testing.T) {
	// Present but Available()==false (e.g. empty base URL) => still 503.
	srv := explainServer(t, "", fakeExplainer{available: false}, incident("inc-a", "prod", "demo"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-a/explain", nil)
	req.SetPathValue("id", "inc-a")
	rec := httptest.NewRecorder()

	srv.handleExplainIncident(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}

func TestExplainSuccess(t *testing.T) {
	ex := fakeExplainer{
		available: true,
		exp:       analyze.Explanation{Summary: "deploy broke it", Category: "deploy", Confidence: "high", Model: "gemma3:4b"},
	}
	srv := explainServer(t, "", ex, incident("inc-a", "prod", "demo"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-a/explain", nil)
	req.SetPathValue("id", "inc-a")
	rec := httptest.NewRecorder()

	srv.handleExplainIncident(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var got analyze.Explanation
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != ex.exp {
		t.Fatalf("explanation = %+v, want %+v", got, ex.exp)
	}
}

func TestExplainBackendErrorReturns502(t *testing.T) {
	ex := fakeExplainer{available: true, err: errors.New("ollama down")}
	srv := explainServer(t, "", ex, incident("inc-a", "prod", "demo"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-a/explain", nil)
	req.SetPathValue("id", "inc-a")
	rec := httptest.NewRecorder()

	srv.handleExplainIncident(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", rec.Code)
	}
}

func TestExplainUnknownIncidentReturns404(t *testing.T) {
	srv := explainServer(t, "", fakeExplainer{available: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/missing/explain", nil)
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()

	srv.handleExplainIncident(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestExplainUnauthenticatedReturns401(t *testing.T) {
	// SSO on, no cookie: the authentication gate runs first and the explainer is
	// never consulted.
	ex := fakeExplainer{available: true, exp: analyze.Explanation{Summary: "x"}}
	srv := explainServer(t, testSSOConfig, ex, incident("inc-b", "staging", "demo"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-b/explain", nil)
	req.SetPathValue("id", "inc-b")
	rec := httptest.NewRecorder()

	srv.handleExplainIncident(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 for unauthenticated explain", rec.Code)
	}
}

// TestExplainRBACGatesExplainer proves the handler order auth -> load -> RBAC ->
// explain: an authenticated GLOBAL viewer passes CanView and therefore REACHES
// the (erroring) explainer, yielding 502 — never 200, and never short-circuiting
// before the store load. The hard-deny 403 branch is the same CanView seam
// exercised by TestFilterVisibleScopedViewer (rbac_http_test.go), which shows a
// prod-scoped viewer cannot view a staging incident.
func TestExplainRBACGatesExplainer(t *testing.T) {
	// Sanity: a cluster-scoped viewer would be denied view of a staging incident,
	// which is exactly the condition the handler turns into a 403.
	enf := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod"}})
	if enf.CanView("staging", "demo") {
		t.Fatal("prod-scoped viewer must not view a staging incident")
	}

	ex := fakeExplainer{available: true, err: errors.New("ollama down")}
	srv := explainServer(t, testSSOConfig, ex, incident("inc-b", "staging", "demo"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/inc-b/explain", nil)
	req.SetPathValue("id", "inc-b")
	req.AddCookie(mintCookie(t, "viewer-user")) // global viewer => passes CanView
	rec := httptest.NewRecorder()

	srv.handleExplainIncident(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("explainer error leaked a 200")
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502 (viewer passes RBAC, explainer errors)", rec.Code)
	}
}
