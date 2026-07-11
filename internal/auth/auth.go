// Package auth implements authentication (ADR-0007): GitHub OAuth + JWT session
// cookies (HttpOnly) + RBAC, driven by a JSON SSO config. With SSO unconfigured,
// everyone is the Anonymous user and all endpoints are open (local dev).
package auth

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lotsman/internal/rbac"
)

// githubHTTPTimeout bounds each outbound GitHub API call during the OAuth flow.
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

// Manager validates sessions, runs the OAuth flow, and resolves the current
// user. It is safe for concurrent use: after construction it is read-only.
type Manager struct {
	enabled bool
	cfg     *SSOConfig
	logger  *slog.Logger
	// hc is the HTTP client used for outbound GitHub API calls during the OAuth
	// flow. It carries a timeout so a slow/hung GitHub can't pin a goroutine.
	hc *http.Client
	// revoked is the in-memory session-revocation denylist so logout actually
	// invalidates a token before its expiry.
	revoked *revocationSet
}

// NewManager builds an auth manager from the SSO config JSON. An empty string
// disables SSO (Anonymous mode); a present-but-invalid JSON also disables SSO
// (fail-open to local-dev behavior) and the error is surfaced via NewManagerErr.
// Most callers use NewManager and rely on Enabled().
func NewManager(ssoConfigJSON string) *Manager {
	m, _ := NewManagerErr(ssoConfigJSON, slog.Default())
	return m
}

// NewManagerErr builds an auth manager and returns any SSO parse error so the
// caller can log it. SSO is enabled only when the config parses and validates.
func NewManagerErr(ssoConfigJSON string, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	hc := &http.Client{Timeout: githubHTTPTimeout}
	if ssoConfigJSON == "" {
		return &Manager{enabled: false, logger: logger, hc: hc, revoked: newRevocationSet()}, nil
	}
	cfg, err := ParseSSOConfig(ssoConfigJSON)
	if err != nil {
		// Return a disabled manager AND the error; the control plane decides
		// whether to fail closed (it does, for a supplied-but-invalid config).
		return &Manager{enabled: false, logger: logger, hc: hc, revoked: newRevocationSet()}, err
	}
	return &Manager{enabled: true, cfg: cfg, logger: logger, hc: hc, revoked: newRevocationSet()}, nil
}

// httpClient returns the Manager's HTTP client, defaulting to a timeout-bounded
// client if one was not set (defensive for Managers built without the
// constructor).
func (m *Manager) httpClient() *http.Client {
	if m.hc == nil {
		return &http.Client{Timeout: githubHTTPTimeout}
	}
	return m.hc
}

// Enabled reports whether SSO is configured and valid.
func (m *Manager) Enabled() bool { return m.enabled }

// Config returns the parsed SSO config (nil when SSO is disabled).
func (m *Manager) Config() *SSOConfig { return m.cfg }

// CurrentUser resolves the user for a request.
//
//   - SSO disabled: everyone is Anonymous (ok=true).
//   - SSO enabled, valid session cookie: the authenticated User (ok=true).
//   - SSO enabled, missing/invalid cookie: zero User, ok=false (unauthenticated).
func (m *Manager) CurrentUser(r *http.Request) (User, bool) {
	if !m.enabled {
		return Anonymous(), true
	}

	ck, err := r.Cookie(sessionCookie)
	if err != nil || ck.Value == "" {
		return User{}, false
	}
	claims, err := VerifySession([]byte(m.cfg.SessionSecret), ck.Value)
	if err != nil {
		return User{}, false
	}
	// Reject tokens that were explicitly logged out (revoked) before their expiry.
	// Keys on the session lineage (sid) so a refreshed token is still rejected, with
	// a jti fallback for older pre-sid tokens.
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
// user to their bindings. The policy is config-driven "strong RBAC" with
// deny-by-default:
//
//   - SSO disabled, or the anonymous local-dev principal (Provider "none"): global
//     admin (transparent pass-through so local dev stays fully open). This keys on
//     the provider, not the login, so a real GitHub user named "anonymous" is NOT
//     treated as the local-dev principal.
//   - init_admin: global admin.
//   - everyone else: the union of (a) config Bindings whose Subject matches the
//     login (case-insensitive) and (b) config GroupBindings whose Group is one of
//     the user's session Groups. No bindings -> the enforcer denies everything.
func (m *Manager) Enforcer(u User) *rbac.Enforcer {
	// Local dev / SSO disabled: full access. Gate on the anonymous PROVIDER ("none"),
	// NOT the login string: keying on the login would hand global admin to an
	// SSO-authenticated GitHub user who simply happens to be named "anonymous".
	// Real authenticated users always carry Provider "github" (see CurrentUser /
	// MintSession), so this only matches the genuine local-dev Anonymous principal.
	if !m.enabled || u.Provider == Anonymous().Provider {
		return rbac.New([]rbac.Binding{{Role: rbac.RoleAdmin, Cluster: rbac.Wildcard}})
	}
	if m.cfg != nil && m.cfg.InitAdmin != "" && strings.EqualFold(m.cfg.InitAdmin, u.Login) {
		return rbac.New([]rbac.Binding{{Role: rbac.RoleAdmin, Cluster: rbac.Wildcard}})
	}
	if m.cfg == nil {
		return rbac.New(nil)
	}

	// Index the user's group memberships for O(1) lookup (case-insensitive).
	userGroups := make(map[string]struct{}, len(u.Groups))
	for _, g := range u.Groups {
		userGroups[strings.ToLower(g)] = struct{}{}
	}

	var bindings []rbac.Binding
	for _, b := range m.cfg.Bindings {
		if strings.EqualFold(b.Subject, u.Login) {
			bindings = append(bindings, rbac.Binding{Role: b.Role, Cluster: b.Cluster, Namespace: b.Namespace})
		}
	}
	for _, b := range m.cfg.GroupBindings {
		if _, ok := userGroups[strings.ToLower(b.Group)]; ok {
			bindings = append(bindings, rbac.Binding{Role: b.Role, Cluster: b.Cluster, Namespace: b.Namespace})
		}
	}
	// Empty set -> deny everything (rbac.New(nil) denies all).
	return rbac.New(bindings)
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
