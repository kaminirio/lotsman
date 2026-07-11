package api

import (
	"net/http"
	"strings"
)

// corsOriginsEnv is the environment variable — read DIRECTLY in this package,
// deliberately NOT threaded through internal/config — that opts a deployment into
// CORS. Its value is a comma-separated allowlist of exact origins
// (scheme://host[:port]), e.g. "https://ui.example.com,https://admin.example.com".
// Unset or empty means NO CORS headers are emitted at all: the secure same-origin
// default for the embedded-UI deployment.
const corsOriginsEnv = "LOTSMAN_CORS_ALLOWED_ORIGINS"

// parseCORSOrigins parses the comma-separated allowlist into a set of exact
// origins. Blank entries are ignored; a bare "*" is deliberately dropped rather
// than honored, because credentialed CORS (Access-Control-Allow-Credentials:
// true) forbids the "*" wildcard origin — allowed origins must be echoed back
// explicitly. An empty result disables CORS entirely.
func parseCORSOrigins(raw string) map[string]struct{} {
	origins := make(map[string]struct{})
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" && o != "*" {
			origins[o] = struct{}{}
		}
	}
	return origins
}

// cors wraps next with OPT-IN CORS for cross-origin browser access (API-8). When
// no origins are configured it returns next unchanged (zero overhead, same-origin
// only). When configured it emits Vary: Origin UNCONDITIONALLY (so a shared cache
// can never serve the no-CORS variant to an allowed origin), and for a request
// carrying an allowlisted Origin it:
//   - echoes Access-Control-Allow-Origin: <origin> (never "*", so credentialed
//     calls work);
//   - sets Access-Control-Allow-Credentials: true so the session cookie is sent;
//   - short-circuits ANY preflight (OPTIONS) with 204 plus the allowed
//     methods/headers — regardless of path prefix. This deliberately covers the
//     state-changing /auth/logout endpoint (which lives OUTSIDE /api/v1 and, needing
//     the X-Requested-With CSRF header, triggers a preflight): gating the preflight
//     on /api/v1 would 405 a split-origin logout. This wrapper sits OUTSIDE the auth
//     middleware, so a preflight — which carries no cookie — is answered instead of
//     401'd.
//
// A request whose Origin is absent or not allowlisted receives no CORS headers
// and falls through to normal handling; the browser then blocks the cross-origin
// read. This keeps the default deny-by-omission.
func (s *Server) cors(next http.Handler) http.Handler {
	if len(s.corsOrigins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary on Origin whenever CORS is enabled — before the allowlist check — so a
		// shared cache keyed on the URL alone cannot serve a cached no-CORS response
		// (built for a non-allowed origin) to an allowed one, or vice versa.
		h := w.Header()
		h.Add("Vary", "Origin")

		origin := r.Header.Get("Origin")
		if _, ok := s.corsOrigins[origin]; ok {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			// Answer any allowlisted-origin preflight (not just /api/v1) so cross-origin
			// state-changing endpoints like /auth/logout can be preflighted.
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				// Echo the browser's requested headers, defaulting to the ones the API
				// needs (JSON body + the CSRF header the auth middleware requires).
				reqHeaders := r.Header.Get("Access-Control-Request-Headers")
				if reqHeaders == "" {
					reqHeaders = "Content-Type, X-Requested-With"
				}
				h.Set("Access-Control-Allow-Headers", reqHeaders)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
