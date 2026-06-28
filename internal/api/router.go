package api

import (
	"net/http"

	"lotsman/internal/ui"
)

// routes builds the HTTP handler. Uses Go 1.22+ method+wildcard mux patterns.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Health + meta.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)

	// Auth (shape matches the UI auth context).
	mux.HandleFunc("GET /auth/me", s.handleMe)
	mux.HandleFunc("GET /auth/providers", s.handleProviders)
	// GitHub OAuth flow — handlers hang off the auth.Manager. These are no-ops
	// (404) when SSO is disabled, so local dev is unaffected.
	mux.HandleFunc("GET /auth/login/{provider}", s.cfg.Auth.HandleLogin)
	mux.HandleFunc("GET /auth/callback/{provider}", s.cfg.Auth.HandleCallback)
	mux.HandleFunc("POST /auth/logout", s.cfg.Auth.HandleLogout)

	// Incidents + investigation.
	mux.HandleFunc("GET /api/v1/incidents", s.handleListIncidents)
	mux.HandleFunc("GET /api/v1/incidents/{id}", s.handleGetIncident)
	// Optional, off-by-default LLM root-cause explainer (503 when unconfigured).
	mux.HandleFunc("POST /api/v1/incidents/{id}/explain", s.handleExplainIncident)
	mux.HandleFunc("POST /api/v1/investigate", s.handleInvestigate)

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
	// transparent pass-through when it is disabled (local dev). Panic recovery
	// (withCommon) stays outermost.
	return withCommon(s.cfg.Auth.Middleware(mux))
}
