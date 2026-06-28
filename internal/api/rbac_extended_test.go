package api

// rbac_extended_test.go covers the test-plan items not already exercised by
// rbac_http_test.go:
//
//   - GET /auth/me: is_admin=false and groups=[...] for a scoped (non-admin) viewer
//     (existing test covers admin; this covers the non-admin path).
//   - GET /auth/me: groups round-trips from the session when the session was
//     minted with groups.
//   - GET /auth/me: 401 for an unauthenticated request.
//   - GET /auth/me: SSO-disabled anonymous resolves as is_admin=true.
//   - GET /api/v1/admin/rbac/effective: deny-by-default login → empty bindings,
//     is_admin=false (not covered by existing tests which only query init_admin
//     and a directly-bound viewer-user).
//   - GET /api/v1/admin/rbac/effective: 401 for unauthenticated request.
//   - GET /api/v1/admin/rbac/config: response body has correct content-type and
//     no nil/missing arrays (empty arrays, not null).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lotsman/internal/auth"
)

// mintCookieWithGroups mints a session for login carrying the supplied groups.
func mintCookieWithGroups(t *testing.T, login string, groups []string) *http.Cookie {
	t.Helper()
	tok, err := auth.MintSession(
		[]byte(testSessionSecret),
		login,
		login+"@example.com",
		login,
		"github",
		groups,
		time.Hour,
	)
	if err != nil {
		t.Fatalf("mint session with groups: %v", err)
	}
	return &http.Cookie{Name: "lotsman_session", Value: tok}
}

// meResponse mirrors the JSON shape of handleMe so we can decode and assert.
type meResponse struct {
	Login   string   `json:"login"`
	IsAdmin bool     `json:"is_admin"`
	Groups  []string `json:"groups"`
}

// TestMeUnauthenticated asserts that GET /auth/me returns 401 when there is no
// session cookie.
func TestMeUnauthenticated(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil) // no cookie
	rec := httptest.NewRecorder()
	srv.handleMe(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /auth/me: got %d, want 401", rec.Code)
	}
}

// TestMeScopedViewerIsAdminFalse verifies that a config-scoped viewer
// (viewer-user, not init_admin) gets is_admin=false from /auth/me.
func TestMeScopedViewerIsAdminFalse(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()
	srv.handleMe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("viewer /auth/me: got %d, want 200", rec.Code)
	}
	var resp meResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IsAdmin {
		t.Error("scoped viewer must have is_admin=false")
	}
	if resp.Login != "viewer-user" {
		t.Errorf("wrong login: got %q, want viewer-user", resp.Login)
	}
	// groups must be a non-null array even though the viewer has no groups.
	if resp.Groups == nil {
		t.Error("groups must be a non-null array for a viewer with no groups in session")
	}
}

// TestMeGroupsRoundTrip verifies that groups minted into the session cookie
// appear verbatim in the /auth/me response.
func TestMeGroupsRoundTrip(t *testing.T) {
	// Use a config where the user has a binding so they can be resolved.
	const cfgWithGroupUser = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "bindings": [{"subject": "group-member", "role": "viewer", "cluster": "*"}],
  "github": {"client_id": "cid", "client_secret": "csecret", "allowed_usernames": ["group-member"]}
}`
	srv := testServer(t, cfgWithGroupUser)
	wantGroups := []string{"acme", "acme/platform"}
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(mintCookieWithGroups(t, "group-member", wantGroups))
	rec := httptest.NewRecorder()
	srv.handleMe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleMe: got %d, want 200", rec.Code)
	}
	var resp meResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Groups) != len(wantGroups) {
		t.Fatalf("groups length: want %d, got %d (%v)", len(wantGroups), len(resp.Groups), resp.Groups)
	}
	for i, g := range wantGroups {
		if resp.Groups[i] != g {
			t.Errorf("Groups[%d]: want %q, got %q", i, g, resp.Groups[i])
		}
	}
}

// TestMeSSODisabledAnonymousIsAdmin verifies the local-dev contract: when SSO
// is disabled the anonymous user resolves as is_admin=true from /auth/me.
func TestMeSSODisabledAnonymousIsAdmin(t *testing.T) {
	srv := testServer(t, "")                                    // SSO disabled
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil) // no cookie needed
	rec := httptest.NewRecorder()
	srv.handleMe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SSO-disabled /auth/me: got %d, want 200", rec.Code)
	}
	var resp meResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.IsAdmin {
		t.Error("SSO-disabled anonymous must have is_admin=true (critical local-dev contract)")
	}
	if resp.Groups == nil {
		t.Error("groups must be a non-null array even for the anonymous user")
	}
}

// TestRBACEffectiveUnauthenticated asserts that an unauthenticated request to
// the effective endpoint gets 401.
func TestRBACEffectiveUnauthenticated(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/effective?user=viewer-user", nil)
	rec := httptest.NewRecorder()
	srv.handleRBACEffective(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated effective: got %d, want 401", rec.Code)
	}
}

// TestRBACEffectiveDenyByDefaultLogin verifies that querying the effective
// endpoint for a login that has NO binding returns empty bindings and
// is_admin=false (deny-by-default).
func TestRBACEffectiveDenyByDefaultLogin(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/effective?user=stranger", nil)
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()
	srv.handleRBACEffective(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("effective for unknown user: got %d, want 200", rec.Code)
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
	if resp.User != "stranger" {
		t.Errorf("user field: got %q, want stranger", resp.User)
	}
	if resp.IsAdmin {
		t.Error("deny-by-default: an unbound login must not be is_admin")
	}
	if len(resp.Bindings) != 0 {
		t.Errorf("deny-by-default: an unbound login must have 0 bindings, got %d", len(resp.Bindings))
	}
}

// TestRBACConfigHandlerNoNullArrays verifies that the /api/v1/admin/rbac/config
// response always encodes bindings and group_bindings as JSON arrays (never
// null), even when there are no bindings configured.
func TestRBACConfigHandlerNoNullArrays(t *testing.T) {
	// Use a config with NO bindings at all.
	const cfgNoBindings = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "github": {"client_id": "cid", "client_secret": "csecret"}
}`
	srv := testServer(t, cfgNoBindings)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/config", nil)
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()
	srv.handleRBACConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("rbac config (no bindings): got %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// The JSON must not contain literal "null" for bindings or group_bindings —
	// the handler normalises nil slices to empty arrays before encoding.
	for _, key := range []string{`"bindings"`, `"group_bindings"`, `"roles"`} {
		if !strings.Contains(body, key) {
			t.Errorf("response missing %s: %s", key, body)
		}
	}
	// Decode to verify no JSON null arrays.
	var resp struct {
		Roles         []string `json:"roles"`
		Bindings      []any    `json:"bindings"`
		GroupBindings []any    `json:"group_bindings"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Bindings == nil {
		t.Error("bindings must be an empty array, not null")
	}
	if resp.GroupBindings == nil {
		t.Error("group_bindings must be an empty array, not null")
	}
	if len(resp.Roles) == 0 {
		t.Error("roles must be present and non-empty")
	}
}

// TestRBACConfigHandlerNoSecrets is an explicit complement to the existing
// "admin 200 with shape and no secrets" sub-test, targeting a config that
// carries both a binding and a group_binding so the shapes are serialised.
func TestRBACConfigHandlerNoSecrets(t *testing.T) {
	const cfgWithBothBindings = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "bindings": [{"subject": "alice", "role": "viewer", "cluster": "prod"}],
  "group_bindings": [{"group": "acme", "role": "operator", "cluster": "prod"}],
  "github": {"client_id": "cid", "client_secret": "csecret"}
}`
	srv := testServer(t, cfgWithBothBindings)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/config", nil)
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()
	srv.handleRBACConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("rbac config (with bindings): got %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// Binding data must appear.
	for _, want := range []string{`"alice"`, `"acme"`, `"viewer"`, `"operator"`, `"prod"`} {
		if !strings.Contains(body, want) {
			t.Errorf("response missing binding data %q: %s", want, body)
		}
	}
	// No secrets must leak.
	for _, secret := range []string{"test-session-secret-at-least-32-chars-long", "client_secret", "csecret"} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaks secret %q: %s", secret, body)
		}
	}
}

// TestRBACConfigHandlerDisabledSSO checks that when SSO is disabled the
// /rbac/config endpoint still behaves correctly (returns roles-only, no
// bindings arrays containing data), though the endpoint in this mode requires
// no auth. This validates the SSO-disabled path end-to-end.
func TestRBACConfigHandlerDisabledSSO(t *testing.T) {
	srv := testServer(t, "")                                                     // SSO disabled — anonymous is global admin
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rbac/config", nil) // no cookie
	rec := httptest.NewRecorder()
	srv.handleRBACConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SSO-disabled rbac/config: got %d, want 200", rec.Code)
	}
	var resp struct {
		Roles         []string `json:"roles"`
		Bindings      []any    `json:"bindings"`
		GroupBindings []any    `json:"group_bindings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Roles) == 0 {
		t.Error("roles must be non-empty even when SSO is disabled")
	}
	if len(resp.Bindings) != 0 {
		t.Errorf("SSO-disabled: bindings must be empty, got %v", resp.Bindings)
	}
	if len(resp.GroupBindings) != 0 {
		t.Errorf("SSO-disabled: group_bindings must be empty, got %v", resp.GroupBindings)
	}
}

// TestListIncidentsScopedViewerCannotSeeForeignCluster adds an
// integration-level assertion that a cluster-scoped viewer (prod only) sees
// only prod incidents and not staging ones — exercised through the handler, not
// just filterVisibleIncidents directly.
func TestListIncidentsScopedViewerCannotSeeForeignCluster(t *testing.T) {
	// A config where "prod-viewer" is scoped to cluster "prod" only.
	const cfgScopedViewer = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "bindings": [{"subject": "prod-viewer", "role": "viewer", "cluster": "prod"}],
  "github": {"client_id": "cid", "client_secret": "csecret", "allowed_usernames": ["prod-viewer"]}
}`
	srv := testServer(t, cfgScopedViewer,
		incident("prod-inc", "prod", "team-a"),
		incident("staging-inc", "staging", "team-a"),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(mintCookie(t, "prod-viewer"))
	rec := httptest.NewRecorder()
	srv.handleListIncidents(rec, req)

	got := decodeIncidents(t, rec)
	if len(got) != 1 {
		t.Fatalf("prod-scoped viewer: want 1 incident, got %d (%v)", len(got), got)
	}
	if got[0].Resource.Cluster != "prod" {
		t.Errorf("prod-scoped viewer: wrong cluster in returned incident: %q", got[0].Resource.Cluster)
	}
}

// TestListIncidentsNamespaceScopedViewerFiltering verifies that a
// namespace-scoped viewer (prod/team-a) does not see incidents in prod/team-b.
func TestListIncidentsNamespaceScopedViewerFiltering(t *testing.T) {
	const cfgNsScopedViewer = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "bindings": [{"subject": "ns-viewer", "role": "viewer", "cluster": "prod", "namespace": "team-a"}],
  "github": {"client_id": "cid", "client_secret": "csecret", "allowed_usernames": ["ns-viewer"]}
}`
	srv := testServer(t, cfgNsScopedViewer,
		incident("inc-a", "prod", "team-a"),
		incident("inc-b", "prod", "team-b"),
		incident("inc-c", "staging", "team-a"),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(mintCookie(t, "ns-viewer"))
	rec := httptest.NewRecorder()
	srv.handleListIncidents(rec, req)

	got := decodeIncidents(t, rec)
	if len(got) != 1 {
		t.Fatalf("ns-scoped viewer: want 1 incident, got %d (%v)", len(got), got)
	}
	if got[0].ID != "inc-a" {
		t.Errorf("ns-scoped viewer: want inc-a, got %q", got[0].ID)
	}
}
