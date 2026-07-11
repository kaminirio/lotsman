package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Middleware enforces authentication and CSRF protection on the API.
//
// When SSO is disabled it is a transparent pass-through, so local-dev behavior
// (everyone Anonymous, all endpoints open) is byte-for-byte unchanged.
//
// When SSO is enabled it:
//   - lets unprotected paths through unauthenticated (health, version,
//     providers, and the OAuth flow itself);
//   - requires a valid session cookie for every other /api/v1/* route;
//   - requires the X-Requested-With header on state-changing requests
//     (non-GET/HEAD/OPTIONS) as CSRF defense, matching ADR-0007. Because the
//     session cookie is SameSite=Lax and HttpOnly, a custom header that a
//     cross-site form cannot set is sufficient.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.enabled {
			next.ServeHTTP(w, r)
			return
		}

		if isUnprotected(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Only guard the API; the embedded UI assets are public. /auth/logout is a
		// state-changing endpoint that lives outside /api/v1, so CSRF-guard it here
		// (a cross-site form must not be able to force a logout).
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			if r.URL.Path == "/auth/logout" && isMutation(r.Method) && r.Header.Get("X-Requested-With") == "" {
				http.Error(w, "missing X-Requested-With header", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if _, ok := m.CurrentUser(r); !ok {
			// JSON API surface (/api/v1): use the standard {"error":...} envelope so
			// every API rejection shares one shape (API-5).
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		if isMutation(r.Method) && r.Header.Get("X-Requested-With") == "" {
			writeJSONError(w, http.StatusForbidden, "missing X-Requested-With header")
			return
		}

		// Sliding expiry: re-issue the session cookie when it is near its TTL so an
		// active user is not hard-logged-out at the fixed lifetime (API-7). No-op for
		// a fresh cookie. Must run before next writes the body so the Set-Cookie
		// header lands.
		m.refreshSession(w, r)

		next.ServeHTTP(w, r)
	})
}

// writeJSONError emits the standard {"error":"..."} envelope used across the JSON
// API surface, so the auth middleware's /api/v1 rejections share one shape with
// the api-package handlers (API-5). Browser-flow endpoints (OAuth login/callback
// redirects, /auth/logout CSRF) deliberately keep their text/plain responses.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// isUnprotected reports whether a path is reachable without a session.
func isUnprotected(path string) bool {
	switch path {
	case "/healthz", "/api/v1/version", "/auth/providers":
		return true
	}
	// The OAuth handshake endpoints must be reachable while logged out.
	return strings.HasPrefix(path, "/auth/login") ||
		strings.HasPrefix(path, "/auth/callback")
}

// isMutation reports whether the HTTP method changes state.
func isMutation(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}
