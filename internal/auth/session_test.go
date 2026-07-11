package auth

import (
	"net/http"
	"net/http/httptest"
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

// --- Session-lineage (sid) + absolute-lifetime cap (FIX 1) ----------------

// A new login carries a stable lineage id (sid) and an original login time
// (auth_time), and a sliding refresh PRESERVES both while rotating the per-mint
// jti. This is what lets logout revoke a whole refresh chain by lineage.
func TestRefreshPreservesSID(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())

	nearExpiry, err := MintSession([]byte(testSecret), "octocat", "", "", "github", nil, 30*time.Minute)
	if err != nil {
		t.Fatalf("mint near-expiry: %v", err)
	}
	orig, err := VerifySession([]byte(testSecret), nearExpiry)
	if err != nil {
		t.Fatalf("verify original: %v", err)
	}
	if orig.SID == "" {
		t.Fatal("a new login must carry a non-empty sid")
	}
	if orig.AuthTime == nil {
		t.Fatal("a new login must carry auth_time")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: nearExpiry})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	ck := findCookie(rec.Result().Cookies(), sessionCookie)
	if ck == nil {
		t.Fatal("near-expiry session must be refreshed with a new Set-Cookie")
	}
	refreshed, err := VerifySession([]byte(testSecret), ck.Value)
	if err != nil {
		t.Fatalf("verify refreshed: %v", err)
	}
	if refreshed.SID != orig.SID {
		t.Errorf("refresh must preserve sid: orig %q, refreshed %q", orig.SID, refreshed.SID)
	}
	if refreshed.ID == orig.ID {
		t.Error("refresh must rotate the per-mint jti")
	}
	if refreshed.AuthTime == nil || !refreshed.AuthTime.Time.Equal(orig.AuthTime.Time) {
		t.Error("refresh must carry the original auth_time forward unchanged")
	}
}

// Logout revokes the session lineage (sid), so a token that has been slid to a
// NEW jti by refresh is still rejected — even when logout is performed with the
// original cookie. This is the security property a per-mint-jti scheme broke: a
// stolen, repeatedly-refreshed cookie can no longer escape the victim's logout.
func TestLogoutAfterRefreshRevokesLineage(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())

	orig, err := MintSession([]byte(testSecret), "octocat", "", "", "github", nil, 30*time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// Slide the session forward: this mints a refreshed token with a NEW jti but the
	// SAME sid.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: orig})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	ck := findCookie(rec.Result().Cookies(), sessionCookie)
	if ck == nil {
		t.Fatal("expected a refreshed cookie")
	}
	refreshed := ck.Value

	origClaims, _ := VerifySession([]byte(testSecret), orig)
	refClaims, _ := VerifySession([]byte(testSecret), refreshed)
	if refClaims.SID != origClaims.SID || refClaims.ID == origClaims.ID {
		t.Fatalf("test precondition: refreshed token must share sid but differ in jti (sid %q/%q id %q/%q)",
			origClaims.SID, refClaims.SID, origClaims.ID, refClaims.ID)
	}

	// Refreshed token is accepted before logout.
	pre := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	pre.AddCookie(&http.Cookie{Name: sessionCookie, Value: refreshed})
	if _, ok := m.CurrentUser(pre); !ok {
		t.Fatal("refreshed token should be valid before logout")
	}

	// Log out using the ORIGINAL cookie (same lineage, different jti).
	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: orig})
	m.HandleLogout(httptest.NewRecorder(), logoutReq)

	// The refreshed token — a different jti the logout never saw — must now be
	// rejected via its shared lineage.
	after := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	after.AddCookie(&http.Cookie{Name: sessionCookie, Value: refreshed})
	if _, ok := m.CurrentUser(after); ok {
		t.Fatal("refreshed token must be rejected after logout revoked its lineage")
	}

	// And a revoked lineage must not be resurrected by another refresh attempt.
	resurrect := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	resurrect.AddCookie(&http.Cookie{Name: sessionCookie, Value: refreshed})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, resurrect)
	if ck := findCookie(rec2.Result().Cookies(), sessionCookie); ck != nil {
		t.Error("a revoked lineage must not be refreshed back to life")
	}
}

// A session whose original login (auth_time) is older than the absolute cap is
// NOT refreshed even though it is within the sliding refresh window: it is left
// to expire so the user must re-authenticate. This bounds an indefinitely-slid
// stolen cookie.
func TestRefreshStopsAtAbsoluteCap(t *testing.T) {
	m := NewManager(validConfigJSON(t, "octocat"))
	h := m.Middleware(newOKHandler())

	// Near expiry (inside the refresh window) but the lineage is past its absolute cap.
	loggedInAt := time.Now().Add(-(sessionAbsoluteMaxLifetime + time.Hour))
	token, err := mintSession([]byte(testSecret), "octocat", "", "", "github", nil, 30*time.Minute, "lineage-abc", loggedInAt)
	if err != nil {
		t.Fatalf("mint capped session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("capped-but-still-valid session should still be served: got %d", rec.Code)
	}
	if ck := findCookie(rec.Result().Cookies(), sessionCookie); ck != nil {
		t.Fatalf("session past the absolute cap must NOT be refreshed; got Set-Cookie %q", ck.Value)
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
