package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lotsman/internal/auth"
	"lotsman/internal/engine"
	"lotsman/internal/model"
	"lotsman/internal/rbac"
	"lotsman/internal/sources"
	"lotsman/internal/store"
)

const testSessionSecret = "test-session-secret-at-least-32-chars-long"

// testSSOConfig enables auth so RBAC is actually enforced. "admin-user" is the
// init_admin (global admin); "viewer-user" is granted a global viewer binding
// (config-driven strong RBAC, deny-by-default — a login with no binding sees
// nothing).
const testSSOConfig = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "bindings": [{"subject": "viewer-user", "role": "viewer", "cluster": "*"}],
  "github": {"client_id": "cid", "client_secret": "csecret", "allowed_usernames": ["viewer-user"]}
}`

// failingResolver implements engine.ProviderResolver and always errors, so
// Engine.Investigate fails with HTTP 502 once RBAC has allowed the request
// through — letting us assert "allowed past RBAC" without real sources.
type failingResolver struct{}

func (failingResolver) Provider(string) (sources.Provider, error) {
	return nil, errors.New("no provider configured in test")
}

func (failingResolver) Clusters() []string { return nil }

func testServer(t *testing.T, ssoJSON string, incs ...*model.Incident) *Server {
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
		Addr:    ":0",
		Version: "test",
		Engine:  engine.New(failingResolver{}, logger),
		Store:   st,
		Auth:    mgr,
		Sources: failingResolver{},
	}, logger)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return srv
}

func mintCookie(t *testing.T, login string) *http.Cookie {
	t.Helper()
	tok, err := auth.MintSession([]byte(testSessionSecret), login, login+"@example.com", login, "github", nil, time.Hour)
	if err != nil {
		t.Fatalf("mint session: %v", err)
	}
	return &http.Cookie{Name: "lotsman_session", Value: tok}
}

func incident(id, cluster, namespace string) *model.Incident {
	return &model.Incident{
		ID:        id,
		Resource:  model.ResourceRef{Cluster: cluster, Namespace: namespace, Kind: "Deployment", Name: id},
		Title:     id,
		Status:    model.IncidentOpen,
		UpdatedAt: time.Unix(1000, 0),
		OpenedAt:  time.Unix(900, 0),
	}
}

func TestInvestigateViewerForbidden(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	body := `{"cluster":"prod","namespace":"demo","kind":"Deployment","name":"checkout"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", strings.NewReader(body))
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()

	srv.handleInvestigate(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer investigate: got %d, want 403", rec.Code)
	}
}

func TestInvestigateAdminAllowedPastRBAC(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	body := `{"cluster":"prod","namespace":"demo","kind":"Deployment","name":"checkout"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", strings.NewReader(body))
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()

	srv.handleInvestigate(rec, req)

	// Admin passes RBAC; the engine then fails (no provider) -> 502, never 403.
	if rec.Code == http.StatusForbidden {
		t.Fatalf("admin investigate was forbidden by RBAC, want allowed")
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("admin investigate: got %d, want 502 (allowed past RBAC, engine fails)", rec.Code)
	}
}

func TestInvestigateAnonymousAllowedWhenSSODisabled(t *testing.T) {
	// No SSO config -> Anonymous global admin -> identical to today's open behavior.
	srv := testServer(t, "")
	body := `{"cluster":"prod","namespace":"demo","kind":"Deployment","name":"checkout"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", strings.NewReader(body))
	rec := httptest.NewRecorder()

	srv.handleInvestigate(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("anonymous (no SSO) investigate forbidden; the no-SSO path must stay open")
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("anonymous investigate: got %d, want 502 (allowed, engine fails)", rec.Code)
	}
}

func TestInvestigateBodyTooLarge(t *testing.T) {
	// A body larger than investigateMaxBody is rejected at the decoder with 413
	// before any work happens. The decode runs before auth, so SSO mode is
	// irrelevant; use the no-SSO server.
	srv := testServer(t, "")
	// Oversized "name" value pushes the JSON past the 4 KiB cap.
	body := `{"cluster":"prod","name":"` + strings.Repeat("x", investigateMaxBody+100) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", strings.NewReader(body))
	rec := httptest.NewRecorder()

	srv.handleInvestigate(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized investigate body: got %d, want 413", rec.Code)
	}
}

func TestInvestigateMalformedBody(t *testing.T) {
	// A small but malformed body still gets the existing 400, not 413.
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()

	srv.handleInvestigate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed investigate body: got %d, want 400", rec.Code)
	}
}

func TestListIncidentsUnauthenticated(t *testing.T) {
	srv := testServer(t, testSSOConfig, incident("inc-a", "prod", "demo"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil) // no cookie
	rec := httptest.NewRecorder()

	srv.handleListIncidents(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: got %d, want 401", rec.Code)
	}
}

func decodeIncidents(t *testing.T, rec *httptest.ResponseRecorder) []*model.Incident {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list incidents: got %d, want 200", rec.Code)
	}
	var got []*model.Incident
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return got
}

func TestListIncidentsGlobalViewerSeesAll(t *testing.T) {
	srv := testServer(t, testSSOConfig,
		incident("inc-a", "prod", "demo"),
		incident("inc-b", "staging", "demo"),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()

	srv.handleListIncidents(rec, req)

	if got := decodeIncidents(t, rec); len(got) != 2 {
		t.Fatalf("global viewer should see both incidents, got %d", len(got))
	}
}

func TestListIncidentsNoSSOSeesAll(t *testing.T) {
	// No-SSO path: Anonymous global admin, unchanged from today — sees everything.
	srv := testServer(t, "",
		incident("inc-a", "prod", "demo"),
		incident("inc-b", "staging", "demo"),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	rec := httptest.NewRecorder()

	srv.handleListIncidents(rec, req)

	if got := decodeIncidents(t, rec); len(got) != 2 {
		t.Fatalf("no-SSO path should see both incidents, got %d", len(got))
	}
}

func TestRBACConfigHandler(t *testing.T) {
	srv := testServer(t, testSSOConfig)

	t.Run("unauthenticated 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/config", nil)
		rec := httptest.NewRecorder()
		srv.handleRBACConfig(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", rec.Code)
		}
	})

	t.Run("non-admin 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/config", nil)
		req.AddCookie(mintCookie(t, "viewer-user"))
		rec := httptest.NewRecorder()
		srv.handleRBACConfig(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("got %d, want 403", rec.Code)
		}
	})

	t.Run("admin 200 with shape and no secrets", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/config", nil)
		req.AddCookie(mintCookie(t, "admin-user"))
		rec := httptest.NewRecorder()
		srv.handleRBACConfig(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		for _, key := range []string{`"roles"`, `"bindings"`, `"group_bindings"`} {
			if !strings.Contains(body, key) {
				t.Errorf("response missing %s: %s", key, body)
			}
		}
		for _, secret := range []string{"session_secret", "client_secret", "csecret", testSessionSecret} {
			if strings.Contains(body, secret) {
				t.Errorf("response leaked secret %q: %s", secret, body)
			}
		}
	})
}

func TestRBACEffectiveHandler(t *testing.T) {
	srv := testServer(t, testSSOConfig)

	t.Run("non-admin 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/effective?user=viewer-user", nil)
		req.AddCookie(mintCookie(t, "viewer-user"))
		rec := httptest.NewRecorder()
		srv.handleRBACEffective(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("got %d, want 403", rec.Code)
		}
	})

	t.Run("admin sees init_admin as global admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/effective?user=admin-user", nil)
		req.AddCookie(mintCookie(t, "admin-user"))
		rec := httptest.NewRecorder()
		srv.handleRBACEffective(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		var resp struct {
			User     string `json:"user"`
			IsAdmin  bool   `json:"is_admin"`
			Bindings []struct {
				Role    string `json:"role"`
				Cluster string `json:"cluster"`
			} `json:"bindings"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.User != "admin-user" || !resp.IsAdmin {
			t.Fatalf("want admin-user is_admin=true, got %+v", resp)
		}
	})

	t.Run("admin sees viewer-user direct binding", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/effective?user=viewer-user", nil)
		req.AddCookie(mintCookie(t, "admin-user"))
		rec := httptest.NewRecorder()
		srv.handleRBACEffective(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		var resp struct {
			IsAdmin  bool `json:"is_admin"`
			Bindings []struct {
				Role string `json:"role"`
			} `json:"bindings"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.IsAdmin {
			t.Error("viewer-user must not be admin")
		}
		if len(resp.Bindings) != 1 || resp.Bindings[0].Role != "viewer" {
			t.Fatalf("want one viewer binding, got %+v", resp.Bindings)
		}
	})
}

func TestMeIncludesIsAdminAndGroups(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()
	srv.handleMe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp struct {
		Login   string   `json:"login"`
		IsAdmin bool     `json:"is_admin"`
		Groups  []string `json:"groups"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Login != "admin-user" || !resp.IsAdmin {
		t.Fatalf("want admin-user is_admin=true, got %+v", resp)
	}
	if resp.Groups == nil {
		t.Error("groups must be a non-null array")
	}
}

// TestFilterVisibleScopedViewer exercises the handler's incident filtering with a
// cluster-scoped enforcer directly (the lean default policy only mints global
// viewers, so we build the scoped enforcer here to prove the filter excludes
// out-of-scope clusters).
func TestFilterVisibleScopedViewer(t *testing.T) {
	enf := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod"}})
	incs := []*model.Incident{
		incident("inc-a", "prod", "demo"),
		incident("inc-b", "staging", "demo"),
	}
	got := filterVisibleIncidents(enf, incs)
	if len(got) != 1 || got[0].Resource.Cluster != "prod" {
		t.Fatalf("prod-scoped viewer should see only the prod incident, got %#v", got)
	}
}
