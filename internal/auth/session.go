package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	jwtIssuer   = "lotsman"
	jwtAudience = "lotsman-api"
	sessionTTL  = 8 * time.Hour
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
	jwt.RegisteredClaims
}

// MintSession creates a signed HS256 JWT session token. groups may be nil.
func MintSession(secret []byte, login, email, name, provider string, groups []string, ttl time.Duration) (string, error) {
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

func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
