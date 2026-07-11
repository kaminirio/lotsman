package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	jwtIssuer   = "lotsman"
	jwtAudience = "lotsman-api"
	sessionTTL  = 8 * time.Hour
	// sessionRefreshWindow is the sliding-expiry threshold (API-7): when a valid
	// session has less than this remaining, the middleware re-issues a fresh cookie
	// so an active user is not hard-logged-out at the fixed sessionTTL. Each renewal
	// grants a full sessionTTL but PRESERVES the session lineage (sid) and original
	// login time (auth_time), so two properties hold that a naive per-mint jti scheme
	// broke: (1) logout revokes the lineage (sid), so it kills the token even after
	// any number of refreshes minted new jtis; (2) sessionAbsoluteMaxLifetime caps the
	// total sliding lifetime, so a stolen cookie cannot be refreshed forever — past the
	// cap it is left to expire, forcing a fresh login.
	sessionRefreshWindow = 2 * time.Hour
	// sessionAbsoluteMaxLifetime bounds the total lifetime of a session lineage across
	// all sliding refreshes, measured from the original login (auth_time). Once a
	// session has lived this long, refreshSession stops renewing it so it expires
	// naturally at its current sessionTTL and the user must re-authenticate.
	sessionAbsoluteMaxLifetime = 24 * time.Hour
)

// SessionClaims are the claims stored in the session JWT cookie. Groups carries
// the user's GitHub org/team memberships (resolved at login) so group-based RBAC
// bindings can be evaluated without re-querying GitHub on every request. The
// field is omitempty and optional, so tokens minted before it existed parse with
// an empty Groups (backward compatible).
type SessionClaims struct {
	Login    string   `json:"login"` // GitHub username — primary identity
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Provider string   `json:"provider"`         // "github"
	Groups   []string `json:"groups,omitempty"` // org slugs and "org/team" slugs
	// SID is the stable session-lineage id: generated once at initial login and
	// PRESERVED across every sliding refresh (unlike the per-mint jti, which changes
	// each refresh). Revocation and logout key on it so logging out kills the whole
	// lineage. omitempty for backward compat: tokens minted before it existed parse
	// with an empty SID and fall back to jti-based revocation.
	SID string `json:"sid,omitempty"`
	// AuthTime is the original login time, carried forward unchanged across refreshes
	// so the absolute-lifetime cap (sessionAbsoluteMaxLifetime) can be enforced.
	// omitempty/optional for backward compat with pre-existing tokens.
	AuthTime *jwt.NumericDate `json:"auth_time,omitempty"`
	jwt.RegisteredClaims
}

// MintSession creates a signed HS256 JWT session token for a NEW login. groups
// may be nil. It generates a fresh session-lineage id (sid) and stamps auth_time
// to now; sliding refreshes go through mintSessionRefresh, which preserves both.
func MintSession(secret []byte, login, email, name, provider string, groups []string, ttl time.Duration) (string, error) {
	sid, err := generateJTI()
	if err != nil {
		return "", fmt.Errorf("auth: generating SID: %w", err)
	}
	return mintSession(secret, login, email, name, provider, groups, ttl, sid, time.Now())
}

// mintSession signs a session token with an explicit session-lineage id (sid)
// and original login time (authTime). A fresh per-mint jti is always generated.
// Callers: MintSession (new login → fresh sid/authTime) and refreshSession
// (sliding renewal → sid/authTime preserved from the current token).
func mintSession(secret []byte, login, email, name, provider string, groups []string, ttl time.Duration, sid string, authTime time.Time) (string, error) {
	now := time.Now()

	jti, err := generateJTI()
	if err != nil {
		return "", fmt.Errorf("auth: generating JTI: %w", err)
	}

	claims := SessionClaims{
		Login:    login,
		Email:    email,
		Name:     name,
		Provider: provider,
		Groups:   groups,
		SID:      sid,
		AuthTime: jwt.NewNumericDate(authTime),
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    jwtIssuer,
			Audience:  jwt.ClaimStrings{jwtAudience},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("auth: signing session token: %w", err)
	}
	return signed, nil
}

// VerifySession validates and parses a session JWT token. It enforces the exact
// HS256 algorithm (not just the HMAC family), issuer, audience, and expiry.
func VerifySession(secret []byte, tokenStr string) (*SessionClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &SessionClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	},
		jwt.WithIssuer(jwtIssuer),
		jwt.WithAudience(jwtAudience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: verifying session token: %w", err)
	}

	claims, ok := token.Claims.(*SessionClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("auth: invalid session token claims")
	}
	return claims, nil
}

// refreshSession implements sliding expiry (API-7). When the request carries a
// valid, non-revoked session cookie whose remaining lifetime is under
// sessionRefreshWindow, it re-issues a fresh token (a full sessionTTL) that
// PRESERVES the session lineage (sid) and original login time (auth_time), and
// sets it on the response with identical security flags (HttpOnly,
// SameSite=Lax, Secure). It is a no-op for a disabled Manager, a missing/invalid
// cookie, a cookie not yet near expiry, a revoked lineage, or a session past the
// absolute-lifetime cap (which is deliberately left to expire so the user must
// re-login). Best-effort: a mint failure is logged and the existing (still-valid)
// cookie is left in place.
func (m *Manager) refreshSession(w http.ResponseWriter, r *http.Request) {
	if !m.enabled || m.cfg == nil {
		return
	}
	ck, err := r.Cookie(sessionCookie)
	if err != nil || ck.Value == "" {
		return
	}
	claims, err := VerifySession([]byte(m.cfg.SessionSecret), ck.Value)
	if err != nil {
		return
	}
	if claims.ExpiresAt == nil || time.Until(claims.ExpiresAt.Time) > sessionRefreshWindow {
		return
	}
	// Never resurrect a token that was explicitly logged out (by lineage or jti).
	if m.isSessionRevoked(claims) {
		return
	}
	// Absolute lifetime cap: carry the original login time forward and, once the
	// lineage has lived past sessionAbsoluteMaxLifetime, stop refreshing so it
	// expires naturally — this is what bounds an indefinitely-refreshed stolen cookie.
	authTime := time.Now()
	if claims.AuthTime != nil {
		authTime = claims.AuthTime.Time
	}
	if time.Since(authTime) > sessionAbsoluteMaxLifetime {
		return
	}
	// Preserve the lineage id across the refresh; older cookies without one get a
	// fresh sid so subsequent refreshes and logout share a lineage going forward.
	sid := claims.SID
	if sid == "" {
		if sid, err = generateJTI(); err != nil {
			m.logger.Warn("session refresh: sid generation failed", "error", err)
			return
		}
	}
	token, err := mintSession([]byte(m.cfg.SessionSecret), claims.Login, claims.Email, claims.Name, claims.Provider, claims.Groups, sessionTTL, sid, authTime)
	if err != nil {
		m.logger.Warn("session refresh: mint failed", "error", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.cfg.Secure(),
	})
}

func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
