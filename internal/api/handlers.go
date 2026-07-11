package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"lotsman/internal/auth"
	"lotsman/internal/model"
	"lotsman/internal/rbac"
	"lotsman/internal/redact"
	"lotsman/internal/sources"
	"lotsman/internal/store"
)

// allNamespaces is the path-segment sentinel selecting every namespace. Adapters
// take an empty Namespace to mean cluster-wide, so handlers translate it.
const allNamespaces = "_all"

// resolveNamespace maps the _all sentinel to the empty (all-namespaces) value and
// returns the namespace to use for RBAC and adapter calls.
func resolveNamespace(seg string) string {
	if seg == allNamespaces {
		return ""
	}
	return seg
}

// errForbidden is returned to clients denied by RBAC. It carries no scope detail
// so the response can't be used to probe which clusters/namespaces exist.
var errForbidden = errors.New("forbidden")

// errNotFound is returned for both genuine misses and out-of-scope resources, so
// the two are indistinguishable (no cross-tenant existence oracle).
var errNotFound = errors.New("not found")

// errUnauthenticated is written (as the standard {"error":...} envelope) when a
// request to the JSON API surface has no valid session. It replaces the old
// empty-body 401s so every /api/v1 error shares one shape (API-5).
var errUnauthenticated = errors.New("unauthorized")

// filterVisibleIncidents returns only the incidents the enforcer may view, by
// each incident's cluster/namespace. A global-admin enforcer (the SSO-disabled
// default) keeps every incident, so the list is unchanged without SSO.
func filterVisibleIncidents(enf *rbac.Enforcer, incs []*model.Incident) []*model.Incident {
	visible := make([]*model.Incident, 0, len(incs))
	for _, inc := range incs {
		if enf.CanView(inc.Resource.Cluster, inc.Resource.Namespace) {
			visible = append(visible, inc)
		}
	}
	return visible
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.cfg.Version})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	// Echo the User fields plus the RBAC summary the UI needs to gate admin-only
	// views (is_admin) and show group memberships (groups). groups is always an
	// array (never null) so the frontend can iterate it unconditionally.
	groups := user.Groups
	if groups == nil {
		groups = []string{}
	}
	writeJSON(w, http.StatusOK, struct {
		auth.User
		IsAdmin bool     `json:"is_admin"`
		Groups  []string `json:"groups"`
	}{
		User:    user,
		IsAdmin: s.cfg.Auth.Enforcer(user).IsAdmin(),
		Groups:  groups,
	})
}

func (s *Server) handleProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": s.cfg.Auth.Enabled(),
		"github":  s.cfg.Auth.Enabled(),
	})
}

// incidentsDefaultLimit / incidentsMaxLimit bound the page size returned by the
// incident list. The cap stops a client asking for an unbounded page.
const (
	incidentsDefaultLimit = 50
	incidentsMaxLimit     = 200
)

// maxIncidentScan bounds how many (newest-first) incidents the list endpoint
// pulls from the store before applying RBAC visibility and paginating. It exists
// to cap memory: without it, ListIncidents with Limit==0 materializes the ENTIRE
// incidents table on every request (the regression this replaces).
//
// It is deliberately a LARGE pre-filter scan cap, not a page limit: the page
// limit must not be applied at the DB before RBAC, because RBAC removes rows the
// caller can't see and would truncate a scoped viewer to fewer than a page (or
// none) — the original truncation bug. maxIncidentScan sits far above any
// realistic per-request visible set, so in practice a scoped viewer still sees
// every visible incident. The residual caveat: a viewer whose visible incidents
// are all OLDER than more than maxIncidentScan newer invisible incidents may not
// see the oldest visible ones. Pushing the caller's cluster scope into the store
// query would remove even that caveat, but is not possible here — incidents
// reference clusters that are not necessarily present in the cluster registry the
// handler can enumerate, and the RBAC enforcer exposes no way to list a viewer's
// bound cluster scopes — so a scan cap is the bounded, non-truncating choice.
const maxIncidentScan = 2000

// knownIncidentStatus reports whether s is one of the model.IncidentStatus
// lifecycle values, used to validate the incident-list ?status filter.
func knownIncidentStatus(s model.IncidentStatus) bool {
	switch s {
	case model.IncidentOpen, model.IncidentInvestigating, model.IncidentResolved, model.IncidentClosed:
		return true
	}
	return false
}

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	limit, offset := parsePageParams(r, incidentsDefaultLimit, incidentsMaxLimit)

	// Fetch a bounded, newest-first scan (cluster/status pushdown), then apply RBAC
	// visibility, and only then paginate. maxIncidentScan bounds memory (Limit==0
	// would materialize the whole table — the regression this replaces) while being
	// large enough that RBAC + pagination still see every visible incident in
	// practice. Applying the PAGE limit at the DB before the RBAC filter is the
	// truncation bug this must not reintroduce: the limit would be consumed by rows
	// the caller can't see, so a scoped viewer could get far fewer than a page of
	// visible incidents (or none) even when more existed.
	// Validate the optional ?status filter LENIENTLY, matching the events
	// endpoint's defensive query parsing (and unlike investigate, which 400s): an
	// unknown value is dropped rather than rejected, so a typo returns the
	// unfiltered list instead of an error. Empty means "no status filter".
	status := model.IncidentStatus(r.URL.Query().Get("status"))
	if status != "" && !knownIncidentStatus(status) {
		status = ""
	}
	f := store.IncidentFilter{
		Cluster: r.URL.Query().Get("cluster"),
		Status:  status,
		Limit:   maxIncidentScan,
	}
	incs, err := s.cfg.Store.ListIncidents(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Filter to the incidents this user is allowed to view, so a viewer scoped to
	// one cluster never sees another's. Without SSO the enforcer is global admin
	// and this keeps every incident.
	visible := filterVisibleIncidents(s.cfg.Auth.Enforcer(user), incs)
	writeJSON(w, http.StatusOK, paginate(visible, offset, limit))
}

// parsePageParams reads limit/offset query params defensively: non-numeric or
// non-positive limit falls back to def; limit is capped at max; a negative or
// garbage offset becomes 0. Mirrors the events endpoint's defensive parsing.
func parsePageParams(r *http.Request, def, max int) (limit, offset int) {
	limit = def
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > max {
		limit = max
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	return limit, offset
}

// paginate returns the [offset, offset+limit) window of s, clamped to bounds. It
// never returns nil for an in-range empty window (so the JSON stays []), and
// never panics on an offset past the end.
func paginate[T any](s []T, offset, limit int) []T {
	if offset >= len(s) {
		return []T{}
	}
	end := offset + limit
	if end > len(s) {
		end = len(s)
	}
	return s[offset:end]
}

func (s *Server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	inc, err := s.cfg.Store.GetIncident(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	enf := s.cfg.Auth.Enforcer(user)
	if !enf.CanView(inc.Resource.Cluster, inc.Resource.Namespace) {
		// Return 404 (not 403) so a scoped viewer can't distinguish "exists but
		// forbidden" from "doesn't exist" — closing the cross-tenant existence
		// oracle on the (predictable) incident IDs.
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	// The stored incident timeline embeds raw log/event bodies (and raw backend
	// payloads). Redact them for non-admins, on a copy so the shared store object
	// is never mutated.
	if !enf.IsAdmin() {
		inc = redactedIncident(inc)
	}
	writeJSON(w, http.StatusOK, inc)
}

// handleExplainIncident produces an OPTIONAL, assistive LLM root-cause narrative
// for an already-correlated incident. It is grounded ONLY in the incident's
// stored findings (timeline + ranked hypotheses) and never runs detection. RBAC
// mirrors handleGetIncident: a user must be able to view the incident's
// cluster/namespace. Responds 503 when no LLM is configured, 502 on backend
// error, 200 with the Explanation otherwise.
func (s *Server) handleExplainIncident(w http.ResponseWriter, r *http.Request) {
	// LLM generation can run longer than the server WriteTimeout (the explainer's
	// own backend budget exceeds it), so exempt this connection.
	clearWriteDeadline(w)
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	inc, err := s.cfg.Store.GetIncident(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if !s.cfg.Auth.Enforcer(user).CanView(inc.Resource.Cluster, inc.Resource.Namespace) {
		// 404 (not 403): don't reveal existence of out-of-scope incidents.
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if s.cfg.Explainer == nil || !s.cfg.Explainer.Available() {
		writeError(w, http.StatusServiceUnavailable, errors.New("LLM analyzer not configured"))
		return
	}
	exp, err := s.cfg.Explainer.Explain(r.Context(), inc)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, exp)
}

// investigateMaxBody caps the investigate request body. The payload is four short
// strings, so 4 KiB is generous.
const investigateMaxBody = 4096

// maxRefFieldLen bounds each investigate resource-ref field. Kubernetes object
// names and namespaces are DNS subdomains (<=253 chars); cluster and kind are
// comfortably shorter, so one generous cap covers all four and rejects absurd
// input before it reaches the engine.
const maxRefFieldLen = 253

// validateInvestigateRef validates the on-demand investigate request: cluster,
// namespace, kind, and name are all REQUIRED (non-blank) and each is
// length-bounded, so empty/garbage refs are rejected with 400 before the engine
// runs a live multi-source gather. Whitespace-only values count as empty. Returns
// a descriptive error (used as the {"error":...} body) or nil when valid.
func validateInvestigateRef(cluster, namespace, kind, name string) error {
	for _, f := range []struct{ label, val string }{
		{"cluster", cluster},
		{"namespace", namespace},
		{"kind", kind},
		{"name", name},
	} {
		if strings.TrimSpace(f.val) == "" {
			return fmt.Errorf("%s is required", f.label)
		}
		if len(f.val) > maxRefFieldLen {
			return fmt.Errorf("%s exceeds %d characters", f.label, maxRefFieldLen)
		}
	}
	return nil
}

// handleInvestigate runs an on-demand investigation for a resource and persists
// the resulting incident.
func (s *Server) handleInvestigate(w http.ResponseWriter, r *http.Request) {
	// On-demand investigation gathers logs/metrics/deployments live across the
	// source seam, which can exceed the server WriteTimeout against a slow or
	// cold backend — the exact degraded-cluster case this tool targets.
	clearWriteDeadline(w)
	var req struct {
		Cluster   string `json:"cluster"`
		Namespace string `json:"namespace"`
		Kind      string `json:"kind"`
		Name      string `json:"name"`
	}
	// Cap the request body so a malformed or hostile client can't stream an
	// unbounded payload into the decoder.
	r.Body = http.MaxBytesReader(w, r.Body, investigateMaxBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, err)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	if !s.cfg.Auth.Enforcer(user).CanInvestigate(req.Cluster, req.Namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}
	// Reject empty/garbage refs with 400 before the engine's live gather. Placed
	// after RBAC so an unauthorized caller can't use validation errors to probe.
	if err := validateInvestigateRef(req.Cluster, req.Namespace, req.Kind, req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ref := model.ResourceRef{Cluster: req.Cluster, Namespace: req.Namespace, Kind: req.Kind, Name: req.Name}
	inc, err := s.cfg.Engine.Investigate(r.Context(), ref, time.Now(), 0)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if err := s.cfg.Store.SaveIncident(r.Context(), inc); err != nil {
		s.logger.Warn("save incident failed", "id", inc.ID, "err", err)
	}
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(inc)
	}
	writeJSON(w, http.StatusOK, inc)
}

// maskedEnvValue is the placeholder substituted for a literal env var value when
// the requesting user is not an admin.
const maskedEnvValue = "••••••"

// redactedIncident returns a copy of inc with raw log/event bodies scrubbed for
// non-admins: each timeline signal's Message is run through the redactor and its
// raw Payload (the unmodified backend object) is dropped. The shared store object
// is never mutated.
func redactedIncident(inc *model.Incident) *model.Incident {
	cp := *inc
	cp.Timeline = make([]model.Signal, len(inc.Timeline))
	for i, sig := range inc.Timeline {
		sig.Message = redact.Redact(sig.Message)
		sig.Payload = nil
		cp.Timeline[i] = sig
	}
	return &cp
}

// maskPodSecrets applies a default-deny policy to literal env values for
// non-admins: EVERY env var carrying a non-empty literal Value is masked,
// regardless of name. A name-based denylist is unsafe — it misses
// credential-bearing names like DATABASE_URL, REDIS_URL, SENTRY_DSN, or
// CONNECTION_STRING — so we redact all literals and let admins see verbatim.
//
// valueFrom-sourced env already has an empty Value for non-admins (it was left
// unresolved upstream), so it stays a reference chip and is not touched here.
// Name and Source are preserved so the table is still useful when masked.
func maskPodSecrets(pods []model.Pod) {
	for pi := range pods {
		for ci := range pods[pi].Containers {
			env := pods[pi].Containers[ci].Env
			for ei := range env {
				e := &env[ei]
				if e.Value != "" {
					e.Value = maskedEnvValue
					e.Masked = true
				}
			}
		}
	}
}

// handleListPods lists a namespace's pods (optionally narrowed to one workload
// via ?workload=), reading live cluster state through the source seam. Admins
// get verbatim + resolved env; non-admins get ALL literal env values masked
// (default-deny) and valueFrom secrets left unresolved.
func (s *Server) handleListPods(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := resolveNamespace(r.PathValue("namespace"))
	enf := s.cfg.Auth.Enforcer(user)
	if !enf.CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}

	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	admin := enf.IsAdmin()
	pods, err := provider.Resources().ListPods(r.Context(), sources.PodQuery{
		Resource: model.ResourceRef{
			Cluster:   cluster,
			Namespace: namespace,
			Name:      r.URL.Query().Get("workload"),
		},
		Reveal: admin,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !admin {
		maskPodSecrets(pods)
	}
	writeJSON(w, http.StatusOK, pods)
}

// podLogsDefaultTail / podLogsMaxTail bound the number of log lines tailed. An
// absent or invalid tail defaults; an oversized one is clamped so tail=99999999
// can't be used to pull an unbounded log body (resource-abuse), mirroring how the
// events endpoint caps limit/since.
const (
	podLogsDefaultTail int64 = 1000
	podLogsMaxTail     int64 = 5000
)

// handleGetPodLogs returns a tail of one pod container's stdout/stderr logs.
func (s *Server) handleGetPodLogs(w http.ResponseWriter, r *http.Request) {
	// Log fetches read live through the source seam and can be large/slow, so
	// exempt this connection from the server WriteTimeout.
	clearWriteDeadline(w)
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := r.PathValue("namespace")
	pod := r.PathValue("pod")
	enf := s.cfg.Auth.Enforcer(user)
	if !enf.CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}

	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	tail := podLogsDefaultTail
	if v := r.URL.Query().Get("tail"); v != "" {
		// Ignore a non-numeric or non-positive tail (fall back to the default) and
		// clamp anything above the ceiling.
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			tail = n
		}
	}
	if tail > podLogsMaxTail {
		tail = podLogsMaxTail
	}
	res, err := provider.Resources().PodLogs(r.Context(), sources.PodLogsQuery{
		Resource: model.ResourceRef{
			Cluster:   cluster,
			Namespace: namespace,
			Pod:       pod,
		},
		Container: r.URL.Query().Get("container"),
		TailLines: tail,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	// Pod log bodies routinely carry secrets/PII. Admins get them verbatim;
	// non-admins get a best-effort redaction pass (defense-in-depth — the same
	// reason env/Secret literals are masked for non-admins).
	if !enf.IsAdmin() {
		res.Lines = redact.Redact(res.Lines)
	}
	writeJSON(w, http.StatusOK, res)
}

// handleListConfigMaps lists a namespace's ConfigMaps as []model.ConfigMapRef,
// read live through the source seam. The _all sentinel lists across all
// namespaces. RBAC: CanView(cluster, namespace). ConfigMap values are not secret,
// so no masking is applied. (A listing returns keys only, not values.)
func (s *Server) handleListConfigMaps(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := resolveNamespace(r.PathValue("namespace"))
	if !s.cfg.Auth.Enforcer(user).CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}
	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	cms, err := provider.Resources().ListConfigMaps(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, cms)
}

// handleGetConfigMap returns one ConfigMap's data as model.ConfigMapDetail.
// RBAC: CanView(cluster, namespace).
func (s *Server) handleGetConfigMap(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := r.PathValue("namespace")
	enf := s.cfg.Auth.Enforcer(user)
	if !enf.CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}
	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	cm, err := provider.Resources().GetConfigMap(r.Context(), model.ResourceRef{
		Cluster:   cluster,
		Namespace: namespace,
		Name:      r.PathValue("name"),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	// ConfigMaps are not Secrets, but operators sometimes place sensitive config
	// (connection strings, tokens) in them. For non-admins, run values through the
	// best-effort redactor so obvious credentials don't leak; admins see verbatim.
	if !enf.IsAdmin() {
		for k, v := range cm.Data {
			cm.Data[k] = redact.Redact(v)
		}
	}
	writeJSON(w, http.StatusOK, cm)
}

// handleListSecrets lists a namespace's Secrets as []model.SecretRef (identity,
// type, keys, IsTLS, and parsed public cert metadata for TLS secrets), read live
// through the source seam. The _all sentinel lists across all namespaces. A
// listing never includes secret values. RBAC: CanView(cluster, namespace).
func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := resolveNamespace(r.PathValue("namespace"))
	if !s.cfg.Auth.Enforcer(user).CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}
	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	secrets, err := provider.Resources().ListSecrets(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, secrets)
}

// maskSecretValues applies a defensive default-deny to a SecretDetail's entries:
// every entry's Value is cleared and Masked set. Used for non-admins so a value
// can never leak even if an upstream adapter populated it. Certificate metadata
// (detail.Cert) is left intact — it is public.
func maskSecretValues(detail *model.SecretDetail) {
	for i := range detail.Entries {
		detail.Entries[i].Value = ""
		detail.Entries[i].Masked = true
	}
}

// handleGetSecret returns one Secret's entries as model.SecretDetail. Admins (and
// the SSO-disabled global-admin default) get values revealed; non-admins get every
// entry value masked (default-deny, applied defensively even though the adapter
// already withholds them). Parsed certificate metadata is returned regardless.
// RBAC: CanView(cluster, namespace).
func (s *Server) handleGetSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := r.PathValue("namespace")
	enf := s.cfg.Auth.Enforcer(user)
	if !enf.CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}
	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	admin := enf.IsAdmin()
	detail, err := provider.Resources().GetSecret(r.Context(), sources.SecretQuery{
		Resource: model.ResourceRef{
			Cluster:   cluster,
			Namespace: namespace,
			Name:      r.PathValue("name"),
		},
		Reveal: admin,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !admin {
		maskSecretValues(&detail)
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleListClusters returns the union of persisted clusters and the clusters
// currently reachable through the registry (direct providers + connected agents).
// A registry cluster absent from the store still appears (e.g. the local agent
// cluster), and any store cluster also in the registry is forced Connected with
// Mode "connected". Sorted by name.
func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	enf := s.cfg.Auth.Enforcer(user)

	cs, err := s.cfg.Store.ListClusters(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Set of registry-connected cluster names.
	connected := make(map[string]struct{})
	for _, name := range s.cfg.Sources.Clusters() {
		connected[name] = struct{}{}
	}

	out := make([]store.Cluster, 0, len(cs)+len(connected))
	seen := make(map[string]struct{}, len(cs))
	for _, c := range cs {
		seen[c.Name] = struct{}{}
		// Cluster ENUMERATION (existence): a namespace-scoped viewer must still see
		// that their cluster exists, while a viewer with no binding for it must not.
		// CanViewCluster ignores namespace scope (unlike CanView(name, ""), which is
		// the strict cluster-wide access query). The SSO-disabled default is global
		// admin, so this keeps every cluster (transparent pass-through in local dev).
		if !enf.CanViewCluster(c.Name) {
			continue
		}
		if _, ok := connected[c.Name]; ok {
			c.Connected = true
			c.Mode = "connected"
		}
		out = append(out, c)
	}
	// Registry clusters with no store record (e.g. the live "local" cluster).
	for name := range connected {
		if _, ok := seen[name]; ok {
			continue
		}
		if !enf.CanViewCluster(name) {
			continue
		}
		out = append(out, store.Cluster{Name: name, Connected: true, Mode: "connected"})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

// handleListWorkloads lists a namespace's workloads (Deployments/StatefulSets/…)
// as []model.ResourceRef, read live through the source seam. The _all namespace
// sentinel lists across all namespaces. RBAC: CanView(cluster, namespace).
func (s *Server) handleListWorkloads(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := resolveNamespace(r.PathValue("namespace"))
	if !s.cfg.Auth.Enforcer(user).CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}

	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	workloads, err := provider.Resources().ListWorkloads(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, workloads)
}

// handleWorkloadHistory returns a workload's image/revision history as
// []model.WorkloadRevision (newest-first), read live through the source seam's
// optional WorkloadHistorian capability. Same RBAC scope as the workloads
// listing — CanView(cluster, namespace). Returns an empty list (not an error)
// when the source doesn't support history, so the UI degrades gracefully.
func (s *Server) handleWorkloadHistory(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := resolveNamespace(r.PathValue("namespace"))
	if !s.cfg.Auth.Enforcer(user).CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}

	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	historian, ok := provider.Resources().(sources.WorkloadHistorian)
	if !ok {
		writeJSON(w, http.StatusOK, []model.WorkloadRevision{})
		return
	}
	ref := model.ResourceRef{
		Cluster:   cluster,
		Namespace: namespace,
		Kind:      r.PathValue("kind"),
		Name:      r.PathValue("name"),
	}
	revisions, err := historian.WorkloadHistory(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, revisions)
}

// handleListNodes lists a cluster's Nodes as []model.Node, read live through the
// source seam. Nodes are cluster-scoped (no namespace), so RBAC gates on
// cluster-level visibility: CanView(cluster, ""). Node objects carry no secret
// data, so no masking is applied.
func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	if !s.cfg.Auth.Enforcer(user).CanView(cluster, "") {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}

	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	nodes, err := provider.Resources().ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

// eventsDefaultSince is the default lookback for the events endpoint.
const eventsDefaultSince = 60 * time.Minute

// eventsMaxSince caps the events lookback so a client can't request an unbounded
// scan.
const eventsMaxSince = 24 * time.Hour

// eventsDefaultLimit is the default cap on returned events (newest first).
const eventsDefaultLimit = 200

// eventsMaxLimit caps the client-supplied limit so a request can't ask for an
// unbounded result set.
const eventsMaxLimit = 1000

// handleListEvents returns Kubernetes event signals for a namespace as
// []model.Signal, newest first, read live through the source seam. The _all
// sentinel lists across all namespaces. Query params: since=<duration> (default
// 60m, capped 24h), limit=<n> (default 200, capped 1000). RBAC: CanView(cluster, namespace).
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	cluster := r.PathValue("cluster")
	namespace := resolveNamespace(r.PathValue("namespace"))
	if !s.cfg.Auth.Enforcer(user).CanView(cluster, namespace) {
		writeError(w, http.StatusForbidden, errForbidden)
		return
	}

	since := eventsDefaultSince
	if v := r.URL.Query().Get("since"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			since = d
		}
	}
	if since > eventsMaxSince {
		since = eventsMaxSince
	}
	limit := eventsDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > eventsMaxLimit {
		limit = eventsMaxLimit
	}

	provider, err := s.cfg.Sources.Provider(cluster)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	now := time.Now()
	sigs, err := provider.Resources().Events(r.Context(), sources.EventQuery{
		Resource: model.ResourceRef{Cluster: cluster, Namespace: namespace},
		Range:    sources.TimeRange{Start: now.Add(-since), End: now},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	// Newest first, then apply the limit.
	sort.SliceStable(sigs, func(i, j int) bool { return sigs[i].Timestamp.After(sigs[j].Timestamp) })
	if len(sigs) > limit {
		sigs = sigs[:limit]
	}
	writeJSON(w, http.StatusOK, sigs)
}

// requireAdmin resolves the current user and verifies they are a global admin.
// It returns the user and true on success; otherwise it writes the response
// (401 when unauthenticated, 403 when not admin) and returns false.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := s.cfg.Auth.CurrentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return auth.User{}, false
	}
	if !s.cfg.Auth.Enforcer(user).IsAdmin() {
		writeError(w, http.StatusForbidden, errForbidden)
		return auth.User{}, false
	}
	return user, true
}

// handleRBACConfig returns the read-only RBAC policy (immutable at runtime):
// the known role slugs and the configured user/group bindings. Admin-gated. It
// deliberately surfaces only the binding shapes, NEVER any secret
// (session_secret, client_secret, etc.).
func (s *Server) handleRBACConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	roles, bindings, groupBindings := s.cfg.Auth.RBACConfig()
	if bindings == nil {
		bindings = []auth.RoleBinding{}
	}
	if groupBindings == nil {
		groupBindings = []auth.GroupRoleBinding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"roles":          roles,
		"bindings":       bindings,
		"group_bindings": groupBindings,
	})
}

// effectiveBinding is the scope-only view of a binding returned by the effective
// endpoint (no subject/group — those are implied by the query).
type effectiveBinding struct {
	Role      string `json:"role"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
}

// handleRBACEffective reports the effective bindings for an arbitrary GitHub
// login, computed from config. Admin-gated.
//
// Note: group-derived bindings can't be resolved for an arbitrary login without
// that user's session (GitHub group membership lives in their token, not in
// config), so this returns only the user's direct config Bindings plus the
// implicit init_admin grant. is_admin reflects those config-derived bindings.
func (s *Server) handleRBACEffective(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	login := r.URL.Query().Get("user")

	_, allBindings, _ := s.cfg.Auth.RBACConfig()

	bindings := []effectiveBinding{}
	isAdmin := false

	// Implicit init_admin grant: a global admin binding.
	if cfg := s.cfg.Auth.Config(); cfg != nil && cfg.InitAdmin != "" && strings.EqualFold(cfg.InitAdmin, login) {
		bindings = append(bindings, effectiveBinding{Role: rbac.RoleAdmin, Cluster: rbac.Wildcard})
		isAdmin = true
	}
	for _, b := range allBindings {
		if strings.EqualFold(b.Subject, login) {
			bindings = append(bindings, effectiveBinding{Role: b.Role, Cluster: b.Cluster, Namespace: b.Namespace})
			if b.Role == rbac.RoleAdmin && b.Cluster == rbac.Wildcard {
				isAdmin = true
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user":     login,
		"bindings": bindings,
		"is_admin": isAdmin,
	})
}
