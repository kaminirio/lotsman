package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testSecret      = "super-secret-key-for-unit-tests!!"     // 34 bytes
	differentSecret = "a-completely-different-secret-key!!!!" // different value
)

type mintArgs struct {
	login, email, name, provider string
	ttl                          time.Duration
}

var defaultArgs = mintArgs{
	login:    "octocat",
	email:    "octocat@github.com",
	name:     "The Octocat",
	provider: "github",
	ttl:      time.Hour,
}

func TestMintVerifyRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		args mintArgs
	}{
		{"typical github user", defaultArgs},
		{"empty optional fields", mintArgs{login: "ghost", provider: "github", ttl: 24 * time.Hour}},
		{"unicode display name", mintArgs{login: "möbius", email: "m@example.com", name: "Möbius Strip", provider: "github", ttl: time.Minute}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.args
			token, err := MintSession([]byte(testSecret), a.login, a.email, a.name, a.provider, nil, a.ttl)
			if err != nil {
				t.Fatalf("MintSession error: %v", err)
			}

			claims, err := VerifySession([]byte(testSecret), token)
			if err != nil {
				t.Fatalf("VerifySession error: %v", err)
			}

			if claims.Login != a.login {
				t.Errorf("Login: want %q, got %q", a.login, claims.Login)
			}
			if claims.Email != a.email {
				t.Errorf("Email: want %q, got %q", a.email, claims.Email)
			}
			if claims.Name != a.name {
				t.Errorf("Name: want %q, got %q", a.name, claims.Name)
			}
			if claims.Provider != a.provider {
				t.Errorf("Provider: want %q, got %q", a.provider, claims.Provider)
			}
		})
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	token, err := MintSession([]byte(testSecret), "octocat", "o@g.com", "Octocat", "github", nil, time.Hour)
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	if _, err := VerifySession([]byte(differentSecret), token); err == nil {
		t.Fatal("expected error when verifying with wrong secret, got nil")
	}
}

func TestVerifyExpiredToken(t *testing.T) {
	token, err := MintSession([]byte(testSecret), "octocat", "o@g.com", "Octocat", "github", nil, -time.Second)
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	if _, err := VerifySession([]byte(testSecret), token); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

// TestVerifyWrongAlgorithm asserts the keyfunc enforces HS256 *exactly*: a token
// signed with HS384 (same HMAC family) must be rejected.
func TestVerifyWrongAlgorithm(t *testing.T) {
	now := time.Now()
	claims := SessionClaims{
		Login:    "octocat",
		Email:    "o@g.com",
		Name:     "Octocat",
		Provider: "github",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			Issuer:    jwtIssuer,
			Audience:  jwt.ClaimStrings{jwtAudience},
		},
	}
	hs384 := jwt.NewWithClaims(jwt.SigningMethodHS384, claims)
	signed, err := hs384.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("manual sign with HS384: %v", err)
	}

	_, err = VerifySession([]byte(testSecret), signed)
	if err == nil {
		t.Fatal("expected HS384-signed token to be rejected; keyfunc must enforce HS256 exactly")
	}
	if !strings.Contains(err.Error(), "unexpected signing method") {
		t.Fatalf("unexpected error text (want 'unexpected signing method'): %v", err)
	}
}

func TestVerifyMalformedToken(t *testing.T) {
	cases := []struct {
		name  string
		token string
	}{
		{"empty string", ""},
		{"not a jwt", "notajwt"},
		{"two parts only", "header.payload"},
		{"garbage base64", "!!!.!!!.!!!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := VerifySession([]byte(testSecret), tc.token); err == nil {
				t.Fatalf("expected error for malformed token %q, got nil", tc.token)
			}
		})
	}
}
