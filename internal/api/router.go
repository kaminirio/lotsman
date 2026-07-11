package api

import (
	"net/http"

	"lotsman/internal/ui"
)

// Rate-limit budgets (per client IP, in-process). The OAuth handshake routes
// make outbound GitHub calls and investigate runs a live multi-source gather +
// persist, so both are throttled; ordinary reads are not. Bursts are generous
// enough for real interactive use and only bite an abusive hot client.
const (
	// authRefillPerSec / authBurst guard the login+callback handshake.
	authRefillPerSec = 1
	authBurst        = 10
	// investigateRefillPerSec / investigateBurst guard the expensive investigate.
	investigateRefillPerSec = 0.5 // one every 2s sustained
	investigateBurst        = 5
)

// routes builds the HTTP handler. Uses Go 1.22+ method+wildcard mux patterns.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Per-IP limiters, one instance shared across the routes it guards (created
	// once here since routes runs once at server construction).
	authLimiter := newIPRateLimiter(authRefillPerSec, authBurst)
	investigateLimiter := newIPRateLimiter(investigateRefillPerSec, investigateBurst)

	// Health + meta.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	// Machine-readable API spec (OpenAPI 3.1, embedded YAML). Registered under
	// /api/v1 like the rest of the surface, so with SSO enabled it sits behind the
	// same session as every other /api/v1 route; with SSO disabled it is open.
	mux.HandleFunc("GET /api/v1/openapi.yaml", s.handleOpenAPISpec)

	// Auth (shape matches the UI auth context).
	mux.HandleFunc("GET /auth/me", s.handleMe)
	mux.HandleFunc("GET /auth/providers", s.handleProviders)
	// GitHub OAuth flow — handlers hang off the auth.Manager. These are no-ops
	// (404) when SSO is disabled, so local dev is unaffected. Rate-limited per IP:
	// each triggers outbound GitHub calls (brute-force / abuse surface).
	mux.Handle("GET /auth/login/{provider}", authLimiter.middleware(http.HandlerFunc(s.cfg.Auth.HandleLogin)))
	mux.Handle("GET /auth/callback/{provider}", authLimiter.middleware(http.HandlerFunc(s.cfg.Auth.HandleCallback)))
	mux.HandleFunc("POST /auth/logout", s.cfg.Auth.HandleLogout)

	// Incidents + investigation.
	mux.HandleFunc("GET /api/v1/incidents", s.handleListIncidents)
	mux.HandleFunc("GET /api/v1/incidents/{id}", s.handleGetIncident)
	// Optional, off-by-default LLM root-cause explainer (503 when unconfigured).
	mux.HandleFunc("POST /api/v1/incidents/{id}/explain", s.handleExplainIncident)
	// Investigate is rate-limited per IP: it runs a live multi-source gather and
	// persists an incident, so an authed operator (or a stolen session) can't
	// hammer it.
	mux.Handle("POST /api/v1/investigate", investigateLimiter.middleware(http.HandlerFunc(s.handleInvestigate)))

	// Clusters / fleet.
	mux.HandleFunc("GET /api/v1/clusters", s.handleListClusters)

	// Cluster-scoped Nodes inspection (Lens-style "Nodes" view). Nodes have no
	// namespace, so RBAC gates on cluster-level visibility. Read live through the
	// source seam (works in direct and agent mode).
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/nodes", s.handleListNodes)

	// Pod inspection: list pods, view a pod container's logs. Read live through
	// the source seam (works in direct and agent mode).
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/pods", s.handleListPods)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/pods/{pod}/logs", s.handleGetPodLogs)

	// Lens-style namespace browsing: workloads and Kubernetes events. The
	// {namespace} segment accepts the "_all" sentinel for all namespaces.
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/workloads", s.handleListWorkloads)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/workloads/{kind}/{name}/history", s.handleWorkloadHistory)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/events", s.handleListEvents)

	// ConfigMap / Secret / Certificate inspection. List routes accept the "_all"
	// sentinel. Secret values are gated on admin (and the agent's reveal opt-in);
	// certificate metadata is public. Read live through the source seam.
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/configmaps", s.handleListConfigMaps)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/configmaps/{name}", s.handleGetConfigMap)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/secrets", s.handleListSecrets)
	mux.HandleFunc("GET /api/v1/clusters/{cluster}/namespaces/{namespace}/secrets/{name}", s.handleGetSecret)

	// Admin RBAC inspector (read-only; config is immutable at runtime). Both
	// routes are admin-gated in the handler: 401 unauthenticated, 403 non-admin.
	mux.HandleFunc("GET /api/v1/admin/rbac/config", s.handleRBACConfig)
	mux.HandleFunc("GET /api/v1/admin/rbac/effective", s.handleRBACEffective)

	// Live updates.
	mux.HandleFunc("GET /api/v1/stream", s.handleStream)

	// Embedded UI (SPA) catch-all.
	mux.Handle("/", ui.Handler())

	// Auth middleware enforces sessions + CSRF when SSO is enabled, and is a
	// transparent pass-through when it is disabled (local dev). CORS (opt-in via
	// LOTSMAN_CORS_ALLOWED_ORIGINS) wraps OUTSIDE auth so a credentialed preflight
	// is answered without a session; it is a no-op pass-through when unconfigured.
	// Panic recovery (withCommon) stays outermost.
	return withCommon(s.cors(s.cfg.Auth.Middleware(mux)))
}
