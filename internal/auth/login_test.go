package auth

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lotsman/internal/store"
)

func testManager(t *testing.T, cfg Config) (*Manager, *store.Memory) {
	t.Helper()
	if cfg.SessionSecret == "" {
		cfg.SessionSecret = "a-test-session-secret-at-least-32-chars"
	}
	st := store.NewMemory()
	m, err := NewManagerFromEnv(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	return m, st
}

func seedLocalUser(t *testing.T, st *store.Memory, username, password string, admin, active bool) {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := st.CreateUser(context.Background(), store.User{
		ID: username, Username: username, Email: username + "@corp.com",
		PasswordHash: hash, IsAdmin: admin, Active: active,
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func postLogin(m *Manager, username, password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	m.HandleLocalLogin(rec, req)
	return rec
}

func TestHandleLocalLoginSuccess(t *testing.T) {
	m, st := testManager(t, Config{})
	seedLocalUser(t, st, "alice", "hunter2hunter2", true, true)

	rec := postLogin(m, "alice", "hunter2hunter2")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("successful login must set the session cookie")
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["is_admin"] != true {
		t.Errorf("is_admin should be true, got %v", resp["is_admin"])
	}
}

func TestHandleLocalLoginFailures(t *testing.T) {
	m, st := testManager(t, Config{})
	seedLocalUser(t, st, "alice", "hunter2hunter2", false, true)
	seedLocalUser(t, st, "inactive", "hunter2hunter2", false, false)

	cases := []struct{ name, user, pass string }{
		{"wrong password", "alice", "nope"},
		{"unknown user", "ghost", "hunter2hunter2"},
		{"inactive user", "inactive", "hunter2hunter2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postLogin(m, tc.user, tc.pass)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", rec.Code)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Error("failed login must not set a cookie")
			}
		})
	}
}

// --- SSO account-mapping rule (resolveSSOUser) -----------------------------

func TestResolveSSOUserLinksExisting(t *testing.T) {
	m, st := testManager(t, Config{})
	seedLocalUser(t, st, "bob", "irrelevant-pw-xx", false, true)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/github", nil)
	rec, err := m.resolveSSOUser(req, "github", Identity{Email: "bob@corp.com", Subject: "gh-1", Verified: true})
	if err != nil {
		t.Fatalf("link existing: %v", err)
	}
	if rec.Username != "bob" || rec.SSOProvider != "github" || rec.SSOSubject != "gh-1" {
		t.Errorf("existing account must be linked, got %+v", rec)
	}
}

func TestResolveSSOUserDeniesInactive(t *testing.T) {
	m, st := testManager(t, Config{})
	seedLocalUser(t, st, "bob", "irrelevant-pw-xx", false, false)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/github", nil)
	if _, err := m.resolveSSOUser(req, "github", Identity{Email: "bob@corp.com", Verified: true}); err == nil || err.Error() != "inactive" {
		t.Fatalf("inactive account must be denied with 'inactive', got %v", err)
	}
}

func TestResolveSSOUserAutoProvisionAllowedDomain(t *testing.T) {
	m, _ := testManager(t, Config{AllowedDomains: []string{"corp.com"}})

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/google", nil)
	rec, err := m.resolveSSOUser(req, "google", Identity{Email: "new@corp.com", Subject: "g-9", Verified: true, DisplayName: "New"})
	if err != nil {
		t.Fatalf("auto-provision: %v", err)
	}
	if rec.Username != "new@corp.com" || rec.IsAdmin || !rec.Active || rec.SSOProvider != "google" {
		t.Errorf("auto-provisioned account must be active non-admin, got %+v", rec)
	}
}

func TestResolveSSOUserDeniesUnknownDomain(t *testing.T) {
	m, _ := testManager(t, Config{AllowedDomains: []string{"corp.com"}})

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/google", nil)
	if _, err := m.resolveSSOUser(req, "google", Identity{Email: "x@evil.com", Verified: true}); err == nil || err.Error() != "no_account" {
		t.Fatalf("unknown-domain email must be denied with 'no_account', got %v", err)
	}
}

// TestResolveSSOUserDeniesByDefaultWithNoAllowedDomains verifies deny-by-default:
// with AllowedDomains unset entirely (not just "wrong domain"), an unknown email
// must never be auto-provisioned.
func TestResolveSSOUserDeniesByDefaultWithNoAllowedDomains(t *testing.T) {
	m, _ := testManager(t, Config{}) // AllowedDomains left nil

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/google", nil)
	if _, err := m.resolveSSOUser(req, "google", Identity{Email: "new@corp.com", Subject: "g-1", Verified: true}); err == nil || err.Error() != "no_account" {
		t.Fatalf("no allowlist configured: unknown email must be denied with 'no_account', got %v", err)
	}
}

// TestResolveSSOUserAutoProvisionDomainCaseInsensitive verifies that the
// allowlist match is case-insensitive on the incoming email's domain (the
// allowlist itself is normalized lower-case at config load).
func TestResolveSSOUserAutoProvisionDomainCaseInsensitive(t *testing.T) {
	m, _ := testManager(t, Config{AllowedDomains: []string{"Corp.Com"}})

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/google", nil)
	rec, err := m.resolveSSOUser(req, "google", Identity{Email: "new@CORP.COM", Subject: "g-2", Verified: true})
	if err != nil {
		t.Fatalf("case-insensitive domain match: %v", err)
	}
	if rec.Username != "new@CORP.COM" || rec.IsAdmin || !rec.Active {
		t.Errorf("auto-provisioned account must be active non-admin, got %+v", rec)
	}
}

// TestResolveSSOUserLinkExistingIsIdempotent verifies that resolving the SAME
// SSO identity twice (e.g. two logins) does not error or duplicate the account
// — the second call just re-links the same provider/subject.
func TestResolveSSOUserLinkExistingIsIdempotent(t *testing.T) {
	m, st := testManager(t, Config{})
	seedLocalUser(t, st, "bob", "irrelevant-pw-xx", false, true)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/github", nil)
	ident := Identity{Email: "bob@corp.com", Subject: "gh-1", Verified: true}
	rec1, err := m.resolveSSOUser(req, "github", ident)
	if err != nil {
		t.Fatalf("first link: %v", err)
	}
	rec2, err := m.resolveSSOUser(req, "github", ident)
	if err != nil {
		t.Fatalf("second link: %v", err)
	}
	if rec1.ID != rec2.ID {
		t.Errorf("re-resolving the same identity must not create a new account: %+v vs %+v", rec1, rec2)
	}
}

// TestResolveSSOUserRejectsCrossProviderHijack verifies that once an account is
// linked to a specific (provider, subject), a login from a DIFFERENT provider (or
// subject) that merely shares the same verified email is denied — it must not
// silently overwrite the existing link (email-only account takeover).
func TestResolveSSOUserRejectsCrossProviderHijack(t *testing.T) {
	m, st := testManager(t, Config{})
	seedLocalUser(t, st, "bob", "irrelevant-pw-xx", false, true)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback/github", nil)

	// First link: github/X binds to bob by verified email.
	if _, err := m.resolveSSOUser(req, "github", Identity{Email: "bob@corp.com", Subject: "X", Verified: true}); err != nil {
		t.Fatalf("first link by email: %v", err)
	}
	linked, _ := st.GetUserByUsername(context.Background(), "bob")
	if linked.SSOProvider != "github" || linked.SSOSubject != "X" {
		t.Fatalf("account should be linked to github/X, got %+v", linked)
	}

	// A different provider with the same email must be DENIED, link unchanged.
	if _, err := m.resolveSSOUser(req, "google", Identity{Email: "bob@corp.com", Subject: "Y", Verified: true}); err == nil || err.Error() != "no_account" {
		t.Fatalf("cross-provider hijack must be denied with 'no_account', got %v", err)
	}
	// A different subject on the same provider with the same email is also denied.
	if _, err := m.resolveSSOUser(req, "github", Identity{Email: "bob@corp.com", Subject: "Z", Verified: true}); err == nil || err.Error() != "no_account" {
		t.Fatalf("subject-swap hijack must be denied with 'no_account', got %v", err)
	}

	after, _ := st.GetUserByUsername(context.Background(), "bob")
	if after.SSOProvider != "github" || after.SSOSubject != "X" {
		t.Errorf("link must be unchanged after denied hijack, got %+v", after)
	}

	// The original identity still logs in (resolved by (provider, subject)).
	rec, err := m.resolveSSOUser(req, "github", Identity{Email: "bob@corp.com", Subject: "X", Verified: true})
	if err != nil || rec.Username != "bob" {
		t.Fatalf("original identity must still resolve, got %+v err=%v", rec, err)
	}
}

func TestProviderStatusReflectsConfig(t *testing.T) {
	m, _ := testManager(t, Config{GitHub: ProviderCreds{ClientID: "a", ClientSecret: "b"}})
	st := m.ProviderStatus()
	if !st["github"] {
		t.Error("github should be reported configured")
	}
	if st["google"] || st["azure"] {
		t.Error("google/azure should be unconfigured")
	}
}
