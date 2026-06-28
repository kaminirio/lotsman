package auth

import (
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/oauth2"

	"lotsman/internal/rbac"
)

// SSOConfig holds the GitHub SSO configuration parsed from LOTSMAN_SSO_CONFIG.
// lotsman deliberately avoids a DB-backed user store and AES token encryption
// (it does not persist GitHub tokens): the login allowlist lives inline here, in
// allowed_usernames.
type SSOConfig struct {
	SessionSecret string         `json:"session_secret"`       // HMAC key for the session JWT (>= 32 chars)
	BaseURL       string         `json:"base_url"`             // control plane, e.g. http://localhost:8080
	UIURL         string         `json:"ui_url"`               // UI origin, e.g. http://localhost:3000
	InitAdmin     string         `json:"init_admin,omitempty"` // GitHub username always allowed + granted admin
	GitHub        GitHubProvider `json:"github"`

	// Bindings grant a role over a cluster/namespace scope to a single GitHub
	// login (config-driven "strong RBAC"). A user named here is also implicitly
	// allowed to log in (see IsGitHubUsernameAllowed).
	Bindings []RoleBinding `json:"bindings,omitempty"`
	// GroupBindings grant a role over a scope to every member of a GitHub group:
	// an org slug ("acme") or an "org/team" slug. Group membership is resolved
	// from the user's session (populated at login from the GitHub API).
	GroupBindings []GroupRoleBinding `json:"group_bindings,omitempty"`
}

// GitHubProvider configures GitHub OAuth2 with a username allowlist.
type GitHubProvider struct {
	ClientID         string   `json:"client_id"`
	ClientSecret     string   `json:"client_secret"`
	AllowedUsernames []string `json:"allowed_usernames"`
}

// RoleBinding grants a Role over a cluster/namespace scope to one GitHub login.
//
// Scope semantics mirror rbac.Binding (verified against internal/rbac/rbac.go):
//   - Cluster: "*" (rbac.Wildcard) matches any cluster; otherwise an exact,
//     case-insensitive match. An empty Cluster is rejected at parse time — a
//     binding must name its cluster (or the explicit wildcard) so an omitted
//     field can't silently grant global scope.
//   - Namespace: "" means all namespaces in that cluster (matches rbac.Binding,
//     where an empty Namespace is treated as the wildcard).
type RoleBinding struct {
	Subject   string `json:"subject"` // GitHub login (case-insensitive)
	Role      string `json:"role"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
}

// GroupRoleBinding grants a Role over a scope to a GitHub group. Group is an org
// slug ("acme") or an "org/team" slug. Scope semantics match RoleBinding.
type GroupRoleBinding struct {
	Group     string `json:"group"`
	Role      string `json:"role"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
}

// knownPlaceholderSecrets are common placeholder values that must not be used.
var knownPlaceholderSecrets = []string{
	"change-me-at-least-32-characters-long",
	"your-secret-here-at-least-32-characters",
	"00000000000000000000000000000000",
}

// ParseSSOConfig parses and validates the JSON SSO configuration string.
func ParseSSOConfig(jsonStr string) (*SSOConfig, error) {
	var cfg SSOConfig
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return nil, fmt.Errorf("auth: parsing SSO config: %w", err)
	}

	if cfg.SessionSecret == "" {
		return nil, fmt.Errorf("auth: session_secret is required in SSO config")
	}
	if len(cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("auth: session_secret must be at least 32 characters")
	}
	for _, placeholder := range knownPlaceholderSecrets {
		if cfg.SessionSecret == placeholder {
			return nil, fmt.Errorf("auth: session_secret is a known placeholder value — generate a real secret with: openssl rand -hex 32")
		}
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("auth: base_url is required in SSO config")
	}
	if cfg.UIURL == "" {
		return nil, fmt.Errorf("auth: ui_url is required in SSO config")
	}
	if cfg.GitHub.ClientID == "" || cfg.GitHub.ClientSecret == "" {
		return nil, fmt.Errorf("auth: github client_id and client_secret are required")
	}

	// Validate RBAC bindings: role must be a known rbac role, and the cluster
	// scope must be named (use the explicit "*" wildcard for global scope rather
	// than relying on an omitted field).
	for i, b := range cfg.Bindings {
		if b.Subject == "" {
			return nil, fmt.Errorf("auth: bindings[%d]: subject is required", i)
		}
		if !rbac.IsValidRole(b.Role) {
			return nil, fmt.Errorf("auth: bindings[%d]: unknown role %q (want one of %s)", i, b.Role, strings.Join(knownRoles(), ", "))
		}
		if b.Cluster == "" {
			return nil, fmt.Errorf("auth: bindings[%d]: cluster is required (use %q for all clusters)", i, rbac.Wildcard)
		}
	}
	for i, b := range cfg.GroupBindings {
		if b.Group == "" {
			return nil, fmt.Errorf("auth: group_bindings[%d]: group is required", i)
		}
		if !rbac.IsValidRole(b.Role) {
			return nil, fmt.Errorf("auth: group_bindings[%d]: unknown role %q (want one of %s)", i, b.Role, strings.Join(knownRoles(), ", "))
		}
		if b.Cluster == "" {
			return nil, fmt.Errorf("auth: group_bindings[%d]: cluster is required (use %q for all clusters)", i, rbac.Wildcard)
		}
	}

	return &cfg, nil
}

// knownRoles returns the rbac role slugs accepted in bindings, for error
// messages. It is the single source of truth the inspector API also reports.
func knownRoles() []string {
	return []string{rbac.RoleAdmin, rbac.RoleOperator, rbac.RoleViewer}
}

// Warnings returns non-fatal configuration warnings.
func (c *SSOConfig) Warnings() []string {
	var warnings []string
	if !strings.HasPrefix(c.BaseURL, "https") && !strings.Contains(c.BaseURL, "localhost") {
		warnings = append(warnings, "base_url is not HTTPS — session cookies will not have the Secure flag")
	}
	if c.InitAdmin == "" && len(c.GitHub.AllowedUsernames) == 0 {
		warnings = append(warnings, "no init_admin and empty allowed_usernames — every GitHub login will be denied")
	}
	return warnings
}

// Secure reports whether cookies should carry the Secure flag.
func (c *SSOConfig) Secure() bool { return strings.HasPrefix(c.BaseURL, "https") }

// GitHubOAuth2Config builds the oauth2.Config for GitHub. lotsman reads the
// user's identity and, for group-based RBAC, their org/team memberships, so it
// requests read:org in addition to the identity scopes. If GitHub does not grant
// read:org the group lookups simply degrade to empty groups (see
// fetchGitHubGroups), so group_bindings just won't apply for that user.
func (c *SSOConfig) GitHubOAuth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.GitHub.ClientID,
		ClientSecret: c.GitHub.ClientSecret,
		RedirectURL:  c.BaseURL + "/auth/callback/github",
		Scopes:       []string{"read:user", "user:email", "read:org"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://github.com/login/oauth/authorize",
			TokenURL: "https://github.com/login/oauth/access_token",
		},
	}
}

// IsGitHubUsernameAllowed reports whether login may sign in. The init_admin
// always passes; otherwise a user passes if they are in the inline allowlist OR
// are named as the Subject of a RoleBinding (the two sets are auto-unioned, so
// granting someone a binding implicitly authorizes their login — there is no
// need to also list them in allowed_usernames). All matches are
// case-insensitive. When allowed_usernames is empty the existing semantics hold:
// only init_admin and binding subjects pass.
//
// It intentionally does NOT consider group memberships (it has no session to read
// them from). The login gate is IsLoginAllowed, which layers the group-binding
// check on top of this; callers gating sign-in should use IsLoginAllowed.
func (c *SSOConfig) IsGitHubUsernameAllowed(login string) bool {
	if c.InitAdmin != "" && strings.EqualFold(c.InitAdmin, login) {
		return true
	}
	for _, allowed := range c.GitHub.AllowedUsernames {
		if strings.EqualFold(allowed, login) {
			return true
		}
	}
	for _, b := range c.Bindings {
		if strings.EqualFold(b.Subject, login) {
			return true
		}
	}
	return false
}

// IsLoginAllowed reports whether a user with the given login and (session-
// resolved) group memberships may sign in. It is the login gate used by the
// OAuth callback. It returns true if IsGitHubUsernameAllowed(login) (init_admin,
// the inline allowlist, or a direct binding subject) OR the user is a member
// (case-insensitive) of any group named in a GroupBinding. The group arm closes
// the lockout where a user authorized ONLY via a group binding — with no
// per-user binding or allowlist entry — would otherwise be rejected at login,
// stranding their group binding. This stays deny-by-default safe: logging in
// grants no access without a matching binding (see Manager.Enforcer).
func (c *SSOConfig) IsLoginAllowed(login string, groups []string) bool {
	if c.IsGitHubUsernameAllowed(login) {
		return true
	}
	if len(c.GroupBindings) == 0 || len(groups) == 0 {
		return false
	}
	member := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		member[strings.ToLower(g)] = struct{}{}
	}
	for _, b := range c.GroupBindings {
		if _, ok := member[strings.ToLower(b.Group)]; ok {
			return true
		}
	}
	return false
}
