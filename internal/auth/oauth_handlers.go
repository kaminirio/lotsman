package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

const (
	sessionCookie  = "lotsman_session"
	stateCookie    = "lotsman_oauth_state"
	stateCookieTTL = 5 * time.Minute
)

// HandleLogin redirects the user to the named provider's authorization URL.
// Route: GET /auth/login/{provider}. A provider that is not configured 404s.
func (m *Manager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	p, ok := m.providers[r.PathValue("provider")]
	if !ok {
		http.Error(w, "unknown or unconfigured provider", http.StatusNotFound)
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
		Secure:   m.secureCookies,
	})

	http.Redirect(w, r, p.AuthCodeURL(state), http.StatusFound)
}

// HandleCallback processes an OAuth2 callback: it verifies the CSRF state,
// exchanges the code, fetches the verified identity, resolves it to a store
// account (ADR-0011 mapping rule), and issues the session cookie. Route:
// GET /auth/callback/{provider}.
func (m *Manager) HandleCallback(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	p, ok := m.providers[provider]
	if !ok {
		http.Error(w, "unknown or unconfigured provider", http.StatusBadRequest)
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

	token, err := p.Exchange(r.Context(), code)
	if err != nil {
		m.logger.Error("OAuth code exchange failed", "provider", provider, "error", err)
		m.redirectError(w, r, "Authentication failed")
		return
	}

	ident, err := p.FetchIdentity(r.Context(), token)
	if err != nil {
		m.logger.Error("failed to fetch identity", "provider", provider, "error", err)
		m.redirectError(w, r, "Authentication failed")
		return
	}

	user, err := m.resolveSSOUser(r, provider, ident)
	if err != nil {
		m.logger.Warn("SSO login denied", "provider", provider, "email", ident.Email, "reason", err.Error())
		m.redirectError(w, r, "Access denied: no authorized account for this identity")
		return
	}

	sessionToken, err := MintSession(m.sessionSecret, user.Username, user.Email, user.Username, provider, nil, sessionTTL)
	if err != nil {
		m.logger.Error("failed to mint session", "error", err)
		m.redirectError(w, r, "Authentication failed")
		return
	}
	m.setSessionCookie(w, sessionToken)

	m.logger.Info("user authenticated", "login", user.Username, "provider", provider)
	http.Redirect(w, r, m.uiURL, http.StatusFound)
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
			if claims, err := VerifySession(m.sessionSecret, ck.Value); err == nil && claims.ExpiresAt != nil {
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
	target := m.uiURL + "/login?error=" + url.QueryEscape(msg)
	http.Redirect(w, r, target, http.StatusFound)
}

func generateState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
