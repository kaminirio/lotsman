// Package auth implements authentication (ADR-0011): first-party username +
// password accounts backed by the store, plus optional GitHub/Google/Azure OAuth
// SSO that links to or auto-provisions those accounts. Session state is a signed
// HttpOnly JWT cookie; RBAC is deny-by-default (see Enforcer).
//
// A Manager built by NewManagerFromEnv is store-backed and ALWAYS enforces auth
// (there is no anonymous path). The legacy SSOConfig-driven constructors
// (NewManager/NewManagerErr, in compat.go) preserve the older "SSO disabled ⇒
// everyone is Anonymous / all endpoints open" local-dev behavior for tests.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lotsman/internal/rbac"
	"lotsman/internal/store"
)

// minSessionSecretLen is the shortest HMAC session secret NewManagerFromEnv
// accepts. Anything shorter is a fatal misconfiguration (the control plane
// refuses to start) rather than a silently-weak signing key.
const minSessionSecretLen = 32

// githubHTTPTimeout bounds each outbound OAuth-provider API call.
const githubHTTPTimeout = 10 * time.Second

// User is the authenticated principal, shaped to match what the UI auth context
// expects.
type User struct {
	Login    string   `json:"login"`
	Name     string   `json:"name"`
	Email    string   `json:"email"`
	Provider string   `json:"provider"`
	Groups   []string `json:"groups,omitempty"` // GitHub org/team memberships, for group-based RBAC
}

// Anonymous is returned when SSO is disabled (local dev).
func Anonymous() User { return User{Login: "anonymous", Name: "Anonymous", Provider: "none"} }

// Config is the flat, env-derived auth configuration consumed by
// NewManagerFromEnv (ADR-0011). Bindings/GroupBindings are only populated by the
// legacy compat shim; production RBAC admin comes from the store's is_admin flag.
type Config struct {
	SessionSecret  string
	BaseURL        string // control plane origin, e.g. http://localhost:8080
	UIURL          string // UI origin to redirect to after SSO login
	AllowedDomains []string
	GitHub         ProviderCreds
	Google         ProviderCreds
	Azure          ProviderCreds
	// Bindings/GroupBindings carry the legacy SSOConfig RBAC policy through the
	// compat shim; NewManagerFromEnv keeps them for Enforcer. Empty in production.
	Bindings      []RoleBinding
	GroupBindings []GroupRoleBinding
}

// Manager validates sessions, runs the OAuth flow, authenticates local logins,
// and resolves the current user. It is safe for concurrent use: after
// construction it is read-only except for the store it delegates writes to.
type Manager struct {
	// enabled gates auth enforcement. A store-backed Manager (NewManagerFromEnv)
	// is always enabled; the legacy compat "" config disables it for local-dev
	// Anonymous mode.
	enabled bool
	// cfg is the parsed legacy SSOConfig, set ONLY by the compat constructors. When
	// non-nil it drives the RBAC Enforcer and the Config() accessor; nil in the
	// store-backed production path.
	cfg    *SSOConfig
	logger *slog.Logger
	// hc is the timeout-bounded HTTP client for outbound OAuth-provider calls.
	hc *http.Client
	// revoked is the in-memory session-revocation denylist so logout invalidates a
	// token before its expiry.
	revoked *revocationSet

	// --- store-backed first-party auth (ADR-0011) ---
	store         store.Store
	providers     map[string]Provider
	sessionSecret []byte
	secureCookies bool
	uiURL         string
	// allowedDomains is the normalized (lower-case, trimmed) SSO auto-provision
	// allowlist: a verified email whose domain is listed provisions a new account.
	allowedDomains []string
	// bindings/groupBindings carry the config RBAC policy for the store-backed
	// Enforcer path (empty in production).
	bindings      []RoleBinding
	groupBindings []GroupRoleBinding
}

// NewManagerFromEnv builds the store-backed auth manager (ADR-0011). Local
// username/password auth is always enforced; the GitHub/Google/Azure providers
// are each active only when their credentials are configured. The manager and
// the API share the same store so admin-provisioned accounts are usable at once.
func NewManagerFromEnv(cfg Config, st store.Store, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	// Session-secret policy: an UNSET secret gets a random per-process key so the
	// control plane still starts (e.g. before an admin is configured) with secure —
	// if non-persistent — sessions. A SET-but-too-short secret is a fatal
	// misconfiguration (a silently-weak signing key), so fail closed.
	sessionSecret := []byte(cfg.SessionSecret)
	switch {
	case len(sessionSecret) == 0:
		sessionSecret = make([]byte, minSessionSecretLen)
		if _, err := rand.Read(sessionSecret); err != nil {
			return nil, fmt.Errorf("auth: generating ephemeral session secret: %w", err)
		}
		logger.Warn("auth: no LOTSMAN_SESSION_SECRET set — using a random per-process key; sessions will not survive a restart")
	case len(sessionSecret) < minSessionSecretLen:
		return nil, fmt.Errorf("auth: session_secret must be at least %d characters", minSessionSecretLen)
	}
	hc := &http.Client{Timeout: githubHTTPTimeout}

	domains := make([]string, 0, len(cfg.AllowedDomains))
	for _, d := range cfg.AllowedDomains {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			domains = append(domains, d)
		}
	}

	return &Manager{
		enabled:        true,
		logger:         logger,
		hc:             hc,
		revoked:        newRevocationSet(),
		store:          st,
		providers:      buildProviders(cfg.BaseURL, cfg.GitHub, cfg.Google, cfg.Azure, hc),
		sessionSecret:  sessionSecret,
		secureCookies:  strings.HasPrefix(cfg.BaseURL, "https"),
		uiURL:          cfg.UIURL,
		allowedDomains: domains,
		bindings:       cfg.Bindings,
		groupBindings:  cfg.GroupBindings,
	}, nil
}

// httpClient returns the Manager's HTTP client, defaulting to a timeout-bounded
// client if one was not set (defensive for Managers built without a constructor).
func (m *Manager) httpClient() *http.Client {
	if m.hc == nil {
		return &http.Client{Timeout: githubHTTPTimeout}
	}
	return m.hc
}

// Enabled reports whether auth is enforced.
func (m *Manager) Enabled() bool { return m.enabled }

// Config returns the parsed legacy SSO config (nil in the store-backed path).
func (m *Manager) Config() *SSOConfig { return m.cfg }

// ProviderStatus reports which SSO providers are configured, always including all
// three keys so a caller can render a stable set of login buttons.
func (m *Manager) ProviderStatus() map[string]bool {
	status := map[string]bool{"github": false, "google": false, "azure": false}
	for name := range m.providers {
		status[name] = true
	}
	return status
}

// CurrentUser resolves the user for a request.
//
//   - auth disabled: everyone is Anonymous (ok=true).
//   - enabled, valid session cookie: the authenticated User (ok=true).
//   - enabled, missing/invalid/revoked cookie: zero User, ok=false.
func (m *Manager) CurrentUser(r *http.Request) (User, bool) {
	if !m.enabled {
		return Anonymous(), true
	}

	ck, err := r.Cookie(sessionCookie)
	if err != nil || ck.Value == "" {
		return User{}, false
	}
	claims, err := VerifySession(m.sessionSecret, ck.Value)
	if err != nil {
		return User{}, false
	}
	// Reject tokens explicitly logged out (revoked) before their expiry. Keys on the
	// session lineage (sid) so a refreshed token is still rejected, with a jti
	// fallback for older pre-sid tokens.
	if m.isSessionRevoked(claims) {
		return User{}, false
	}
	return User{
		Login:    claims.Login,
		Name:     claims.Name,
		Email:    claims.Email,
		Provider: claims.Provider,
		Groups:   claims.Groups,
	}, true
}

// Enforcer returns the RBAC enforcer for a user — the SINGLE place that maps a
// user to their bindings. Deny-by-default:
//
//   - auth disabled, or the anonymous local-dev principal (Provider "none"):
//     global admin (transparent pass-through so local dev stays open). Keys on the
//     provider, not the login, so a real user named "anonymous" is NOT treated as
//     the local-dev principal.
//   - legacy SSOConfig path (cfg != nil): init_admin is global admin; everyone
//     else gets the union of config Bindings (by login) and GroupBindings (by
//     session group).
//   - store-backed path (cfg == nil): an active is_admin account is global admin;
//     everyone else gets the union of config Bindings/GroupBindings (empty in
//     production ⇒ deny-by-default).
func (m *Manager) Enforcer(u User) *rbac.Enforcer {
	// Local dev / auth disabled: full access. Gate on the anonymous PROVIDER
	// ("none"), NOT the login, so an SSO user merely named "anonymous" is not
	// handed global admin.
	if !m.enabled || u.Provider == Anonymous().Provider {
		return rbac.New([]rbac.Binding{{Role: rbac.RoleAdmin, Cluster: rbac.Wildcard}})
	}

	if m.cfg != nil {
		if m.cfg.InitAdmin != "" && strings.EqualFold(m.cfg.InitAdmin, u.Login) {
			return rbac.New([]rbac.Binding{{Role: rbac.RoleAdmin, Cluster: rbac.Wildcard}})
		}
		return rbac.New(bindingsFor(m.cfg.Bindings, m.cfg.GroupBindings, u))
	}

	// Store-backed path: admin is the account's is_admin flag.
	if m.isStoreAdmin(u.Login) {
		return rbac.New([]rbac.Binding{{Role: rbac.RoleAdmin, Cluster: rbac.Wildcard}})
	}
	return rbac.New(bindingsFor(m.bindings, m.groupBindings, u))
}

// bindingsFor computes the union of the config user bindings (subject matches the
// login, case-insensitive) and group bindings (group is one of the user's session
// groups, case-insensitive). An empty result denies everything.
func bindingsFor(userBindings []RoleBinding, groupBindings []GroupRoleBinding, u User) []rbac.Binding {
	userGroups := make(map[string]struct{}, len(u.Groups))
	for _, g := range u.Groups {
		userGroups[strings.ToLower(g)] = struct{}{}
	}

	var bindings []rbac.Binding
	for _, b := range userBindings {
		if strings.EqualFold(b.Subject, u.Login) {
			bindings = append(bindings, rbac.Binding{Role: b.Role, Cluster: b.Cluster, Namespace: b.Namespace})
		}
	}
	for _, b := range groupBindings {
		if _, ok := userGroups[strings.ToLower(b.Group)]; ok {
			bindings = append(bindings, rbac.Binding{Role: b.Role, Cluster: b.Cluster, Namespace: b.Namespace})
		}
	}
	return bindings
}

// isStoreAdmin reports whether login resolves to an active, is_admin account.
func (m *Manager) isStoreAdmin(login string) bool {
	if m.store == nil || login == "" {
		return false
	}
	u, err := m.store.GetUserByUsername(context.Background(), login)
	if err != nil {
		return false
	}
	return u.Active && u.IsAdmin
}

// RBACConfig exposes the read-only RBAC policy for the admin inspector API
// without leaking the whole SSOConfig (and never any secret). Returns empty
// slices when SSO is disabled.
func (m *Manager) RBACConfig() (roles []string, bindings []RoleBinding, groupBindings []GroupRoleBinding) {
	roles = knownRoles()
	if !m.enabled || m.cfg == nil {
		return roles, nil, nil
	}
	return roles, m.cfg.Bindings, m.cfg.GroupBindings
}

// HandleLocalLogin authenticates a username/password against the store and, on
// success, issues the session cookie. Route: POST /auth/login. All failures
// (unknown user, inactive account, wrong password, or an SSO-only account with no
// password hash) return an indistinguishable 401 so the endpoint cannot be used
// to enumerate accounts.
func (m *Manager) HandleLocalLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeAuthError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	u, err := m.store.GetUserByUsername(r.Context(), body.Username)
	if err != nil || !u.Active || !ComparePassword(u.PasswordHash, body.Password) {
		writeAuthError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := MintSession(m.sessionSecret, u.Username, u.Email, u.Username, "local", nil, sessionTTL)
	if err != nil {
		m.logger.Error("failed to mint session", "error", err)
		writeAuthError(w, http.StatusInternalServerError, "internal error")
		return
	}
	m.setSessionCookie(w, token)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"login":    u.Username,
		"email":    u.Email,
		"provider": "local",
		"is_admin": u.IsAdmin,
	})
}

// resolveSSOUser applies the ADR-0011 SSO account-mapping rule and returns the
// store account the verified identity should log in as. The error string is a
// stable, caller-facing code: "inactive" (account disabled) or "no_account" (no
// linkable/provisionable account) — the OAuth callback maps both to an access-
// denied redirect.
//
// Resolution order:
//  1. by the stable (provider, subject) link — a returning SSO user;
//  2. otherwise, only with a VERIFIED email, by that email:
//     - already linked to a different identity ⇒ denied (no email-only takeover);
//     - inactive ⇒ denied;
//     - unlinked & active ⇒ linked to this (provider, subject);
//  3. otherwise auto-provisioned as an active non-admin account iff the email's
//     domain is in the allowlist; else denied.
func (m *Manager) resolveSSOUser(r *http.Request, provider string, ident Identity) (store.User, error) {
	ctx := r.Context()

	// 1. Stable (provider, subject) link.
	if ident.Subject != "" {
		if u, err := m.store.GetUserBySSO(ctx, provider, ident.Subject); err == nil {
			if !u.Active {
				return store.User{}, errors.New("inactive")
			}
			return u, nil
		}
	}

	// 2. Only a verified email is trusted to link or provision.
	if !ident.Verified || ident.Email == "" {
		return store.User{}, errors.New("no_account")
	}

	u, err := m.store.GetUserByEmail(ctx, ident.Email)
	switch {
	case err == nil:
		// An account already bound to a (different) identity must not be hijacked by
		// a mere email match — step 1 would have returned it if this were its link.
		if u.SSOProvider != "" || u.SSOSubject != "" {
			return store.User{}, errors.New("no_account")
		}
		if !u.Active {
			return store.User{}, errors.New("inactive")
		}
		linked, uerr := m.store.UpdateUser(ctx, u.ID, store.UserPatch{SSOProvider: &provider, SSOSubject: &ident.Subject})
		if uerr != nil {
			return store.User{}, uerr
		}
		return linked, nil
	case errors.Is(err, store.ErrNotFound):
		// 3. Auto-provision on an allowlisted domain.
		if !m.domainAllowed(ident.Email) {
			return store.User{}, errors.New("no_account")
		}
		nu := store.User{
			ID:          newUserID(),
			Username:    ident.Email,
			Email:       ident.Email,
			Active:      true,
			SSOProvider: provider,
			SSOSubject:  ident.Subject,
			CreatedAt:   time.Now(),
		}
		if cerr := m.store.CreateUser(ctx, nu); cerr != nil {
			return store.User{}, cerr
		}
		return nu, nil
	default:
		return store.User{}, err
	}
}

// domainAllowed reports whether email's domain is in the auto-provision
// allowlist (case-insensitive; the allowlist is normalized at construction).
func (m *Manager) domainAllowed(email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range m.allowedDomains {
		if d == domain {
			return true
		}
	}
	return false
}

// setSessionCookie writes the signed session JWT as an HttpOnly, SameSite=Lax
// cookie (Secure when the base URL is HTTPS).
func (m *Manager) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.secureCookies,
	})
}

// writeAuthError emits the standard JSON {"error":...} envelope.
func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
