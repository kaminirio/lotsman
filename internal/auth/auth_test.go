package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lotsman/internal/rbac"
)

// validConfigJSON returns a minimal-but-valid SSO config for the given allowlist.
func validConfigJSON(t *testing.T, initAdmin string, allowed ...string) string {
	t.Helper()
	cfg := map[string]any{
		"session_secret": testSecret,
		"base_url":       "http://localhost:8080",
		"ui_url":         "http://localhost:3000",
		"init_admin":     initAdmin,
		"github": map[string]any{
			"client_id":         "cid",
			"client_secret":     "csecret",
			"allowed_usernames": allowed,
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(b)
}

// --- ParseSSOConfig + allowlist -------------------------------------------

func TestParseSSOConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg, err := ParseSSOConfig(validConfigJSON(t, "octocat", "alice"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.GitHub.ClientID != "cid" {
			t.Errorf("client id not parsed: %q", cfg.GitHub.ClientID)
		}
	})

	bad := []struct {
		name string
		json string
	}{
		{"not json", "{"},
		{"short secret", `{"session_secret":"too-short","base_url":"http://x","ui_url":"http://y","github":{"client_id":"a","client_secret":"b"}}`},
		{"placeholder secret", `{"session_secret":"00000000000000000000000000000000","base_url":"http://x","ui_url":"http://y","github":{"client_id":"a","client_secret":"b"}}`},
		{"missing client", `{"session_secret":"super-secret-key-for-unit-tests!!","base_url":"http://x","ui_url":"http://y","github":{}}`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSSOConfig(tc.json); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParseSSOConfigBindingValidation(t *testing.T) {
	t.Run("valid bindings parse", func(t *testing.T) {
		if _, err := ParseSSOConfig(configWithBindings(t)); err != nil {
			t.Fatalf("valid bindings should parse: %v", err)
		}
	})

	bad := []struct {
		name string
		json string
	}{
		{"unknown role", `{"session_secret":"super-secret-key-for-unit-tests!!","base_url":"http://x","ui_url":"http://y","bindings":[{"subject":"a","role":"superuser","cluster":"*"}],"github":{"client_id":"a","client_secret":"b"}}`},
		{"empty cluster", `{"session_secret":"super-secret-key-for-unit-tests!!","base_url":"http://x","ui_url":"http://y","bindings":[{"subject":"a","role":"viewer","cluster":""}],"github":{"client_id":"a","client_secret":"b"}}`},
		{"empty subject", `{"session_secret":"super-secret-key-for-unit-tests!!","base_url":"http://x","ui_url":"http://y","bindings":[{"subject":"","role":"viewer","cluster":"*"}],"github":{"client_id":"a","client_secret":"b"}}`},
		{"group unknown role", `{"session_secret":"super-secret-key-for-unit-tests!!","base_url":"http://x","ui_url":"http://y","group_bindings":[{"group":"acme","role":"nope","cluster":"*"}],"github":{"client_id":"a","client_secret":"b"}}`},
		{"group empty group", `{"session_secret":"super-secret-key-for-unit-tests!!","base_url":"http://x","ui_url":"http://y","group_bindings":[{"group":"","role":"viewer","cluster":"*"}],"github":{"client_id":"a","client_secret":"b"}}`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSSOConfig(tc.json); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestIsGitHubUsernameAllowedBindingUnion(t *testing.T) {
	cfg, err := ParseSSOConfig(configWithBindings(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// alice is named only as a binding subject (not in allowed_usernames) yet is
	// auto-unioned into the allowlist.
	if !cfg.IsGitHubUsernameAllowed("Alice") {
		t.Error("a binding subject must be allowed to log in (case-insensitive)")
	}
	if cfg.IsGitHubUsernameAllowed("mallory") {
		t.Error("a user with no binding and not allowlisted must be denied login")
	}
}

func TestRBACConfigAccessor(t *testing.T) {
	t.Run("disabled returns roles only", func(t *testing.T) {
		m := NewManager("")
		roles, bindings, groups := m.RBACConfig()
		if len(roles) != 3 {
			t.Errorf("want 3 known roles, got %d", len(roles))
		}
		if bindings != nil || groups != nil {
			t.Error("disabled SSO must expose no bindings")
		}
	})
	t.Run("enabled returns configured bindings", func(t *testing.T) {
		m := NewManager(configWithBindings(t))
		_, bindings, groups := m.RBACConfig()
		if len(bindings) != 1 || len(groups) != 1 {
			t.Fatalf("want 1 binding + 1 group binding, got %d/%d", len(bindings), len(groups))
		}
	})
}

func TestIsGitHubUsernameAllowed(t *testing.T) {
	cfg, err := ParseSSOConfig(validConfigJSON(t, "OctoCat", "Alice", "bob"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tests := []struct {
		login string
		want  bool
	}{
		{"octocat", true},  // init_admin, case-insensitive
		{"OctoCat", true},  // init_admin exact
		{"alice", true},    // allowlist, case-insensitive
		{"BOB", true},      // allowlist, case-insensitive
		{"mallory", false}, // not allowed
		{"", false},        // empty
	}
	for _, tc := range tests {
		if got := cfg.IsGitHubUsernameAllowed(tc.login); got != tc.want {
			t.Errorf("IsGitHubUsernameAllowed(%q) = %v, want %v", tc.login, got, tc.want)
		}
	}
}

// TestIsLoginAllowedGroupMember is the FIX-2 regression: a user authorized ONLY
// via a group binding (no per-user binding, not in allowed_usernames) was rejected
// at the login gate, stranding their group binding (lockout). IsLoginAllowed must
// admit them when they are a member of a GroupBinding's group, while still denying
// a user with no allowlist entry, no binding, and no matching group.
func TestIsLoginAllowedGroupMember(t *testing.T) {
	// configWithBindings has a group_binding for org "acme" but does NOT list
	// "carol" in allowed_usernames or as a binding subject.
	cfg, err := ParseSSOConfig(configWithBindings(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// carol is in "acme" -> allowed via the group binding even with no per-user grant.
	if !cfg.IsLoginAllowed("carol", []string{"acme"}) {
		t.Error("group-only member must be allowed to log in (FIX-2)")
	}
	// Case-insensitive group match.
	if !cfg.IsLoginAllowed("carol", []string{"ACME"}) {
		t.Error("group match at the login gate must be case-insensitive")
	}
	// carol with no matching group is still denied (deny-by-default at the gate).
	if cfg.IsLoginAllowed("carol", []string{"other-org"}) {
		t.Error("a user with no allowlist/binding and no matching group must be denied")
	}
	if cfg.IsLoginAllowed("carol", nil) {
		t.Error("a group-less user with no allowlist/binding must be denied")
	}
	// The allowlist/binding arm still admits even without groups (init_admin, here).
	if !cfg.IsLoginAllowed("octocat", nil) {
		t.Error("init_admin must still be allowed via the username arm")
	}
	if !cfg.IsLoginAllowed("alice", nil) {
		t.Error("a binding subject must still be allowed via the username arm")
	}
}

// --- Manager / CurrentUser -------------------------------------------------

func TestManagerHTTPClientHasTimeout(t *testing.T) {
	// The constructor must wire a timeout-bounded HTTP client for the GitHub OAuth
	// calls so a slow/hung GitHub can't pin a goroutine.
	for _, cfg := range []string{"", validConfigJSON(t, "admin", "alice")} {
		m, err := NewManagerErr(cfg, nil)
		if err != nil {
			t.Fatalf("build manager: %v", err)
		}
		hc := m.httpClient()
		if hc == nil {
			t.Fatal("httpClient() must never return nil")
		}
		if hc.Timeout != githubHTTPTimeout {
			t.Errorf("github client timeout: got %v, want %v", hc.Timeout, githubHTTPTimeout)
		}
	}

	// Even a zero-value Manager (not built via the constructor) returns a
	// timeout-bounded client defensively.
	var m Manager
	if to := m.httpClient().Timeout; to != githubHTTPTimeout {
		t.Errorf("zero-value manager client timeout: got %v, want %v", to, githubHTTPTimeout)
	}
}

func TestCurrentUserAnonymousWhenDisabled(t *testing.T) {
	m := NewManager("") // SSO disabled
	if m.Enabled() {
		t.Fatal("manager should be disabled with empty config")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	u, ok := m.CurrentUser(req)
	if !ok {
		t.Fatal("anonymous user should be ok=true when SSO disabled")
	}
	if u.Login != "anonymous" {
		t.Errorf("want anonymous login, got %q", u.Login)
	}
}

func TestCurrentUserMalformedConfigDisablesSSO(t *testing.T) {
	m, err := NewManagerErr("{not json", nil)
	if err == nil {
		t.Fatal("expected parse error for malformed config")
	}
	if m.Enabled() {
		t.Fatal("malformed config must fail open to disabled (Anonymous) mode")
	}
}

func TestCurrentUserEnabledNoCookie(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	if !m.Enabled() {
		t.Fatal("manager should be enabled with valid config")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	if _, ok := m.CurrentUser(req); ok {
		t.Fatal("expected ok=false when SSO enabled but no cookie present")
	}
}

func TestCurrentUserEnabledValidCookie(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	token, err := MintSession([]byte(testSecret), "octocat", "o@g.com", "Octo", "github", nil, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	u, ok := m.CurrentUser(req)
	if !ok {
		t.Fatal("expected valid session to resolve a user")
	}
	if u.Login != "octocat" || u.Provider != "github" {
		t.Errorf("unexpected user: %+v", u)
	}
}

func TestCurrentUserEnabledTamperedCookie(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	token, _ := MintSession([]byte(differentSecret), "mallory", "", "", "github", nil, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	if _, ok := m.CurrentUser(req); ok {
		t.Fatal("expected ok=false for a cookie signed with a different secret")
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	token, err := MintSession([]byte(testSecret), "octocat", "o@g.com", "Octo", "github", nil, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	before := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	before.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	if _, ok := m.CurrentUser(before); !ok {
		t.Fatal("session should be valid before logout")
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	m.HandleLogout(httptest.NewRecorder(), logoutReq)

	// The same still-unexpired, still-cryptographically-valid token must now be
	// rejected because logout revoked its JTI.
	after := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	after.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	if _, ok := m.CurrentUser(after); ok {
		t.Fatal("session should be revoked after logout")
	}
}

// --- Enforcer derivation ---------------------------------------------------

func TestManagerEnforcer(t *testing.T) {
	t.Run("disabled grants global admin", func(t *testing.T) {
		m := NewManager("")
		e := m.Enforcer(Anonymous())
		if !e.IsAdmin() {
			t.Error("anonymous in local dev should be global admin")
		}
	})

	t.Run("init_admin is admin", func(t *testing.T) {
		m := NewManager(validConfigJSON(t, "octocat"))
		e := m.Enforcer(User{Login: "OctoCat"})
		if !e.IsAdmin() {
			t.Error("init_admin should be global admin (case-insensitive)")
		}
	})

	t.Run("user with no binding is denied (deny-by-default)", func(t *testing.T) {
		m := NewManager(validConfigJSON(t, "octocat", "alice"))
		e := m.Enforcer(User{Login: "alice"})
		if e.IsAdmin() {
			t.Error("non-admin should not be global admin")
		}
		if e.CanView("any", "any") {
			t.Error("a user with no binding must see nothing (deny-by-default)")
		}
		if e.CanInvestigate("any", "any") {
			t.Error("a user with no binding must not investigate")
		}
	})

	t.Run("subject binding grants scoped viewer", func(t *testing.T) {
		cfg := configWithBindings(t)
		m := NewManager(cfg)
		e := m.Enforcer(User{Login: "alice"})
		if !e.CanView("prod", "demo") {
			t.Error("alice's viewer binding should grant view in prod/demo")
		}
		if e.CanView("staging", "demo") {
			t.Error("alice's binding is scoped to prod; staging must be denied")
		}
		if e.CanInvestigate("prod", "demo") {
			t.Error("viewer binding must not grant investigate")
		}
	})

	t.Run("group binding applies via session groups", func(t *testing.T) {
		m := NewManager(configWithBindings(t))
		// carol is not a subject of any binding, but is a member of the "acme" org.
		e := m.Enforcer(User{Login: "carol", Groups: []string{"acme"}})
		if !e.CanInvestigate("prod", "demo") {
			t.Error("acme operator group binding should grant investigate in prod")
		}
		// Without the group membership, carol gets nothing.
		eNoGroup := m.Enforcer(User{Login: "carol"})
		if eNoGroup.CanView("prod", "demo") {
			t.Error("carol with no groups must be denied (deny-by-default)")
		}
	})

	// FIX-3 regression: with SSO ENABLED, the global-admin pass-through must key on
	// the anonymous PROVIDER ("none"), not the login string. A real GitHub user
	// (Provider "github") who happens to be named "anonymous", with no binding, must
	// be denied — not handed global admin.
	t.Run("github user named anonymous is not admin", func(t *testing.T) {
		m := NewManager(validConfigJSON(t, "octocat", "alice"))
		e := m.Enforcer(User{Login: "anonymous", Provider: "github"})
		if e.IsAdmin() {
			t.Error("an SSO GitHub user named 'anonymous' must NOT be global admin")
		}
		if e.CanView("any", "any") {
			t.Error("an SSO user named 'anonymous' with no binding must see nothing")
		}
	})
}

// configWithBindings returns a valid SSO config carrying a user binding (alice ->
// viewer in prod) and a group binding (acme org -> operator in prod).
func configWithBindings(t *testing.T) string {
	t.Helper()
	cfg := map[string]any{
		"session_secret": testSecret,
		"base_url":       "http://localhost:8080",
		"ui_url":         "http://localhost:3000",
		"init_admin":     "octocat",
		"bindings": []map[string]any{
			{"subject": "alice", "role": "viewer", "cluster": "prod"},
		},
		"group_bindings": []map[string]any{
			{"group": "acme", "role": "operator", "cluster": "prod"},
		},
		"github": map[string]any{
			"client_id":     "cid",
			"client_secret": "csecret",
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(b)
}

// --- Middleware (CSRF + auth gating) --------------------------------------

func newOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddlewareDisabledPassThrough(t *testing.T) {
	m := NewManager("") // disabled
	h := m.Middleware(newOKHandler())

	// A mutation with no cookie and no CSRF header must pass through unchanged.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled middleware must pass through; got %d", rec.Code)
	}
}

func TestMiddlewareEnabledUnauthenticated(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for unauthenticated API request, got %d", rec.Code)
	}
}

func TestMiddlewareUnprotectedPaths(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())

	for _, path := range []string{"/healthz", "/api/v1/version", "/auth/providers", "/auth/login/github"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("path %q should be reachable unauthenticated; got %d", path, rec.Code)
		}
	}
}

func TestMiddlewareCSRFHeaderRequired(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())
	token, _ := MintSession([]byte(testSecret), "octocat", "", "", "github", nil, time.Hour)

	// Authenticated mutation WITHOUT the CSRF header -> 403.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 without X-Requested-With, got %d", rec.Code)
	}

	// Same request WITH the header -> 200.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", nil)
	req2.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	req2.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("want 200 with X-Requested-With, got %d", rec2.Code)
	}
}

func TestMiddlewareGetNoCSRFNeeded(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())
	token, _ := MintSession([]byte(testSecret), "octocat", "", "", "github", nil, time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated GET should not need CSRF header; got %d", rec.Code)
	}
}

// API-5: the /api/v1 middleware rejections use the JSON {"error":...} envelope
// rather than the old http.Error text/plain body.
func TestMiddlewareUnauthenticatedJSONEnvelope(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil) // no cookie
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("want application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body must be JSON: %v (%q)", err, rec.Body.String())
	}
	if body["error"] == "" {
		t.Errorf("want non-empty error field, got %v", body)
	}
}

// --- Sliding session expiry (API-7) ---------------------------------------

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// A session within sessionRefreshWindow of expiry is re-issued as a fresh cookie
// on the response, with security flags preserved and expiry pushed out.
func TestMiddlewareRefreshesNearExpirySession(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())

	nearExpiry, err := MintSession([]byte(testSecret), "octocat", "", "", "github", nil, 30*time.Minute)
	if err != nil {
		t.Fatalf("mint near-expiry: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: nearExpiry})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("near-expiry authed GET: got %d, want 200", rec.Code)
	}
	ck := findCookie(rec.Result().Cookies(), sessionCookie)
	if ck == nil {
		t.Fatal("near-expiry session must be refreshed with a new Set-Cookie, got none")
	}
	if ck.Value == "" || ck.Value == nearExpiry {
		t.Fatalf("refreshed cookie must carry a new token; got %q", ck.Value)
	}
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
		t.Errorf("refreshed cookie must keep HttpOnly + SameSite=Lax; got HttpOnly=%v SameSite=%v", ck.HttpOnly, ck.SameSite)
	}
	claims, err := VerifySession([]byte(testSecret), ck.Value)
	if err != nil {
		t.Fatalf("refreshed token must verify: %v", err)
	}
	if time.Until(claims.ExpiresAt.Time) <= sessionRefreshWindow {
		t.Error("refreshed token must extend expiry beyond the refresh window")
	}
}

// A full-lifetime session (well outside the refresh window) must NOT be
// re-issued, so refresh only kicks in near expiry.
func TestMiddlewareDoesNotRefreshFreshSession(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())

	fresh, err := MintSession([]byte(testSecret), "octocat", "", "", "github", nil, sessionTTL)
	if err != nil {
		t.Fatalf("mint fresh: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: fresh})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("fresh authed GET: got %d, want 200", rec.Code)
	}
	if ck := findCookie(rec.Result().Cookies(), sessionCookie); ck != nil {
		t.Fatalf("fresh session must NOT be refreshed; got Set-Cookie %q", ck.Value)
	}
}

// compile-time guard: rbac roles referenced from auth stay valid.
var _ = rbac.RoleAdmin
