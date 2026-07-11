package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	sessionCookie  = "lotsman_session"
	stateCookie    = "lotsman_oauth_state"
	stateCookieTTL = 5 * time.Minute
)

// HandleLogin redirects the user to GitHub's authorization URL.
// Route: GET /auth/login/{provider}.
func (m *Manager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if !m.enabled {
		http.Error(w, "SSO is not configured", http.StatusNotFound)
		return
	}
	if r.PathValue("provider") != "github" {
		http.Error(w, "unknown provider: only github is supported", http.StatusBadRequest)
		return
	}

	state, err := generateState()
	if err != nil {
		m.logger.Error("failed to generate state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state,
		Path:     "/auth/callback",
		MaxAge:   int(stateCookieTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.cfg.Secure(),
	})

	authURL := m.cfg.GitHubOAuth2Config().AuthCodeURL(state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback processes the GitHub OAuth2 callback, applies the allowlist,
// and issues the session cookie. Route: GET /auth/callback/{provider}.
func (m *Manager) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if !m.enabled {
		http.Error(w, "SSO is not configured", http.StatusNotFound)
		return
	}
	if r.PathValue("provider") != "github" {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	// Verify state (CSRF protection for the OAuth handshake).
	stateCk, err := r.Cookie(stateCookie)
	if err != nil || stateCk.Value == "" {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("state") != stateCk.Value {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    "",
		Path:     "/auth/callback",
		MaxAge:   -1,
		HttpOnly: true,
	})

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		m.logger.Error("OAuth callback error", "error", errParam, "description", desc)
		m.redirectError(w, r, "Authentication failed: "+errParam)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	oauthCfg := m.cfg.GitHubOAuth2Config()
	oauth2Token, err := oauthCfg.Exchange(r.Context(), code)
	if err != nil {
		m.logger.Error("OAuth code exchange failed", "error", err)
		m.redirectError(w, r, "Authentication failed")
		return
	}
	accessToken := oauth2Token.AccessToken
	if accessToken == "" {
		m.logger.Error("no access token in OAuth response")
		m.redirectError(w, r, "Authentication failed")
		return
	}

	ghUser, err := m.fetchGitHubUser(r.Context(), accessToken)
	if err != nil {
		m.logger.Error("failed to fetch GitHub user", "error", err)
		m.redirectError(w, r, "Authentication failed")
		return
	}

	// Resolve org/team memberships BEFORE the login gate: a user authorized only
	// via a group binding (no per-user binding or allowlist entry) must be allowed
	// in, so the gate has to know their groups first. Still resolve only when group
	// bindings are configured — no point spending two extra GitHub API calls
	// otherwise. On error (including a missing read:org grant) groups degrade to
	// empty and group_bindings simply won't apply for this user (graceful
	// degradation).
	var groups []string
	if len(m.cfg.GroupBindings) > 0 {
		groups = m.fetchGitHubGroups(r.Context(), accessToken)
	}

	if !m.cfg.IsLoginAllowed(ghUser.Login, groups) {
		m.logger.Warn("GitHub login denied: not in allowlist and no matching group binding", "login", ghUser.Login)
		m.redirectError(w, r, "Access denied: your GitHub account is not authorized")
		return
	}

	email := ghUser.Email
	if email == "" {
		email, _ = m.fetchGitHubPrimaryEmail(r.Context(), accessToken)
	}

	sessionToken, err := MintSession([]byte(m.cfg.SessionSecret), ghUser.Login, email, ghUser.Name, "github", groups, sessionTTL)
	if err != nil {
		m.logger.Error("failed to mint session", "error", err)
		m.redirectError(w, r, "Authentication failed")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionToken,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.cfg.Secure(),
	})

	m.logger.Info("user authenticated", "login", ghUser.Login, "provider", "github")
	http.Redirect(w, r, m.cfg.UIURL, http.StatusFound)
}

// HandleLogout revokes the current session and clears the cookie. Revoking the
// session lineage (not just clearing the cookie) is what makes logout effective
// for the stateless JWT — the token is rejected on subsequent requests even
// though it is still within its validity window. It revokes by the stable
// lineage id (sid) so a token that has been slid to a new jti by refresh is still
// killed; for older tokens without a sid it falls back to the per-mint jti.
// Route: POST /auth/logout.
func (m *Manager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if m.enabled && m.revoked != nil {
		if ck, err := r.Cookie(sessionCookie); err == nil && ck.Value != "" {
			if claims, err := VerifySession([]byte(m.cfg.SessionSecret), ck.Value); err == nil && claims.ExpiresAt != nil {
				// Revoke the lineage (sid) so the whole refresh chain dies; fall back to
				// the jti for pre-sid tokens. Both are self-bounded to the token's expiry.
				key := claims.SID
				if key == "" {
					key = claims.ID
				}
				m.revoked.revoke(key, claims.ExpiresAt.Time)
			}
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *Manager) redirectError(w http.ResponseWriter, r *http.Request, msg string) {
	target := m.cfg.UIURL + "/login?error=" + url.QueryEscape(msg)
	http.Redirect(w, r, target, http.StatusFound)
}

func generateState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// gitHubUser represents a GitHub user from the /user API.
type gitHubUser struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	ID        int64  `json:"id"`
}

// fetchGitHubUser calls the GitHub API to get the authenticated user.
func (m *Manager) fetchGitHubUser(ctx context.Context, token string) (*gitHubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := m.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var user gitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decoding GitHub user: %w", err)
	}
	return &user, nil
}

// fetchGitHubPrimaryEmail fetches the user's primary verified email from GitHub.
func (m *Manager) fetchGitHubPrimaryEmail(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := m.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub emails API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub emails API returned %d", resp.StatusCode)
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", fmt.Errorf("decoding GitHub emails: %w", err)
	}

	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("no verified email found")
}

// fetchGitHubGroups resolves the authenticated user's group memberships for
// group-based RBAC: org slugs (e.g. "acme") from GET /user/orgs and "org/team"
// slugs from GET /user/teams. Both require the read:org scope. On any error
// (including a missing read:org grant) it logs a Warn and returns an empty slice
// — group_bindings then simply won't apply, which is intentional graceful
// degradation rather than a login failure.
func (m *Manager) fetchGitHubGroups(ctx context.Context, token string) []string {
	var groups []string

	var orgs []struct {
		Login string `json:"login"`
	}
	if err := m.getGitHubJSON(ctx, token, "https://api.github.com/user/orgs", &orgs); err != nil {
		m.logger.Warn("failed to fetch GitHub orgs; group bindings will not apply", "error", err)
		return nil
	}
	for _, o := range orgs {
		if o.Login != "" {
			groups = append(groups, o.Login)
		}
	}

	var teams []struct {
		Slug         string `json:"slug"`
		Organization struct {
			Login string `json:"login"`
		} `json:"organization"`
	}
	if err := m.getGitHubJSON(ctx, token, "https://api.github.com/user/teams", &teams); err != nil {
		m.logger.Warn("failed to fetch GitHub teams; team bindings will not apply", "error", err)
		return groups
	}
	for _, t := range teams {
		if t.Organization.Login != "" && t.Slug != "" {
			groups = append(groups, t.Organization.Login+"/"+t.Slug)
		}
	}
	return groups
}

// getGitHubJSON performs a timeout-bounded authenticated GET against the GitHub
// API and decodes the JSON body into out.
func (m *Manager) getGitHubJSON(ctx context.Context, token, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := m.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding GitHub response: %w", err)
	}
	return nil
}
