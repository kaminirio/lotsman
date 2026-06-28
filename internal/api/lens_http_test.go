package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lotsman/internal/auth"
	"lotsman/internal/engine"
	"lotsman/internal/model"
	"lotsman/internal/rbac"
	"lotsman/internal/store"
)

// lensTestServer builds a Server whose Sources is the given stubResolver, so the
// clusters/workloads/events handlers run end-to-end through RBAC against canned
// adapter data. The store is seeded with prod-eu + staging (via store.Seed) so
// the /clusters merge can be exercised against persisted records.
func lensTestServer(t *testing.T, ssoJSON string, res stubResolver) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := auth.NewManagerErr(ssoJSON, logger)
	if err != nil {
		t.Fatalf("build auth manager: %v", err)
	}
	st := store.NewMemory()
	store.Seed(st)
	srv, err := New(Config{
		Addr:    ":0",
		Version: "test",
		Engine:  engine.New(failingResolver{}, logger),
		Store:   st,
		Auth:    mgr,
		Sources: res,
	}, logger)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return srv
}

func TestListClusters_MergesRegistryConnected(t *testing.T) {
	// Registry reports a "local" cluster that the seed store does not know, plus
	// "staging" which the store also has. Expect: local appears as connected with
	// mode "connected", staging is forced connected, prod-eu stays store-only
	// (mode empty), sorted by name.
	res := stubResolver{src: &stubClusterSource{}, clusters: []string{"local", "staging"}}
	srv := lensTestServer(t, "", res)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	rec := httptest.NewRecorder()
	srv.handleListClusters(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list clusters: got %d, want 200", rec.Code)
	}
	var got []store.Cluster
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode clusters: %v", err)
	}

	byName := map[string]store.Cluster{}
	var names []string
	for _, c := range got {
		byName[c.Name] = c
		names = append(names, c.Name)
	}
	// Sorted by name: local, prod-eu, staging.
	if len(names) != 3 || names[0] != "local" || names[1] != "prod-eu" || names[2] != "staging" {
		t.Fatalf("expected sorted [local prod-eu staging], got %v", names)
	}
	local, ok := byName["local"]
	if !ok {
		t.Fatal("registry-only cluster 'local' must appear in /clusters")
	}
	if !local.Connected || local.Mode != "connected" {
		t.Errorf("local: want connected+mode=connected, got connected=%v mode=%q", local.Connected, local.Mode)
	}
	if st := byName["staging"]; !st.Connected || st.Mode != "connected" {
		t.Errorf("staging (store+registry): want connected+mode=connected, got connected=%v mode=%q", st.Connected, st.Mode)
	}
	if pe := byName["prod-eu"]; pe.Mode != "" {
		t.Errorf("prod-eu (store-only, not in registry) should have empty mode, got %q", pe.Mode)
	}
	// Store-derived fields survive the merge.
	if byName["prod-eu"].Env != "prod" || byName["prod-eu"].Region != "eu-west-1" {
		t.Errorf("prod-eu store fields lost: %+v", byName["prod-eu"])
	}
}

func TestListClusters_Unauthenticated(t *testing.T) {
	// With SSO enabled, an unauthenticated request must be rejected (no longer a
	// transparent pass-through). The store is seeded so a leak would be observable.
	srv := lensTestServer(t, testSSOConfig, stubResolver{src: &stubClusterSource{}, clusters: []string{"local"}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil) // no cookie
	rec := httptest.NewRecorder()
	srv.handleListClusters(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated clusters: got %d, want 401", rec.Code)
	}
}

func TestListClusters_GlobalViewerSeesAll(t *testing.T) {
	// A global viewer (lean default for an allowed login) may enumerate every
	// cluster — the guard only filters cluster-scoped viewers.
	res := stubResolver{src: &stubClusterSource{}, clusters: []string{"local", "staging"}}
	srv := lensTestServer(t, testSSOConfig, res)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()
	srv.handleListClusters(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("global viewer clusters: got %d, want 200", rec.Code)
	}
	var got []store.Cluster
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode clusters: %v", err)
	}
	// local (registry-only) + prod-eu, staging (seeded) = 3.
	if len(got) != 3 {
		t.Fatalf("global viewer should see all clusters, got %d: %+v", len(got), got)
	}
}

// TestListClusters_ScopedViewerFiltered proves the per-cluster filter the handler
// applies: a viewer scoped to one cluster never enumerates the others. The lean
// default policy only mints global viewers, so we exercise the gate the handler
// relies on directly (CanViewCluster), mirroring TestFilterVisibleScopedViewer.
func TestListClusters_ScopedViewerFiltered(t *testing.T) {
	enf := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod-eu"}})
	if !enf.CanViewCluster("prod-eu") {
		t.Fatalf("prod-eu-scoped viewer should see prod-eu")
	}
	if enf.CanViewCluster("staging") || enf.CanViewCluster("local") {
		t.Fatalf("prod-eu-scoped viewer must not enumerate staging/local (handler skips them)")
	}
}

// TestListClusters_NamespaceScopedSeesClusterButNotNodes is the FIX-1 enumeration
// regression: a NAMESPACE-scoped viewer ({viewer, prod-eu, team-a}) must still see
// its cluster EXISTS in the cluster list (CanViewCluster, used by
// handleListClusters), yet must NOT be granted cluster-wide data access — the
// strict CanView(cluster, "") gate handleListNodes uses denies node enumeration.
func TestListClusters_NamespaceScopedSeesClusterButNotNodes(t *testing.T) {
	enf := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod-eu", Namespace: "team-a"}})
	if !enf.CanViewCluster("prod-eu") {
		t.Fatalf("namespace-scoped viewer should see its cluster listed")
	}
	if enf.CanView("prod-eu", "") {
		t.Fatalf("namespace-scoped viewer must NOT be granted cluster-wide node listing (handler returns 403)")
	}
	if enf.CanViewCluster("staging") {
		t.Fatalf("namespace-scoped viewer must not enumerate an out-of-scope cluster")
	}
}

func TestListWorkloads_ReturnsList(t *testing.T) {
	src := &stubClusterSource{workloads: []model.ResourceRef{
		{Cluster: "prod", Namespace: "demo", Kind: "Deployment", Name: "api"},
		{Cluster: "prod", Namespace: "demo", Kind: "StatefulSet", Name: "db"},
	}}
	srv := lensTestServer(t, testSSOConfig, stubResolver{src: src})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/workloads", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()
	srv.handleListWorkloads(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list workloads: got %d, want 200", rec.Code)
	}
	var got []model.ResourceRef
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode workloads: %v", err)
	}
	if len(got) != 2 || got[0].Name != "api" || got[1].Kind != "StatefulSet" {
		t.Fatalf("unexpected workloads: %+v", got)
	}
	if src.lastNamespace != "demo" {
		t.Errorf("ListWorkloads namespace: got %q, want demo", src.lastNamespace)
	}
}

func TestListWorkloads_AllNamespaces(t *testing.T) {
	src := &stubClusterSource{workloads: []model.ResourceRef{{Cluster: "prod", Name: "api"}}}
	srv := lensTestServer(t, "", stubResolver{src: src})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/_all/workloads", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "_all")
	rec := httptest.NewRecorder()
	srv.handleListWorkloads(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list workloads _all: got %d, want 200", rec.Code)
	}
	if src.lastNamespace != "" {
		t.Errorf("_all must pass empty namespace to adapter, got %q", src.lastNamespace)
	}
}

func TestListWorkloads_Unauthenticated(t *testing.T) {
	srv := lensTestServer(t, testSSOConfig, stubResolver{src: &stubClusterSource{}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/workloads", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	rec := httptest.NewRecorder()
	srv.handleListWorkloads(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated workloads: got %d, want 401", rec.Code)
	}
}

func TestListWorkloads_GlobalViewerAllowed(t *testing.T) {
	// The lean default policy mints global viewers, so an allowed viewer reaches
	// the adapter (200). The forbidden-namespace decision is the same CanView gate
	// proven in TestListWorkloads_ScopedViewerForbidden below.
	srv := lensTestServer(t, testSSOConfig, stubResolver{src: &stubClusterSource{}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/staging/namespaces/demo/workloads", nil)
	req.SetPathValue("cluster", "staging")
	req.SetPathValue("namespace", "demo")
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()
	srv.handleListWorkloads(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("global viewer should be allowed: got %d, want 200", rec.Code)
	}
}

func TestListWorkloads_ScopedViewerForbidden(t *testing.T) {
	// The workloads handler gates on enf.CanView(cluster, namespace) — identical to
	// the pods handler — and returns 403 when false. Assert the enforcer decision
	// the handler relies on: a prod-scoped viewer is denied staging.
	enf := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod"}})
	if !enf.CanView("prod", "demo") {
		t.Fatalf("prod-scoped viewer should be allowed prod/demo")
	}
	if enf.CanView("staging", "demo") {
		t.Fatalf("prod-scoped viewer must be denied staging/demo (handler returns 403)")
	}
	// _all maps to CanView(cluster, "") — a CLUSTER-wide viewer (namespace
	// wildcard/empty) is allowed prod/_all but not staging/_all.
	if !enf.CanView("prod", "") {
		t.Fatalf("cluster-wide prod viewer should be allowed prod/_all")
	}
	if enf.CanView("staging", "") {
		t.Fatalf("prod viewer must be denied staging/_all")
	}

	// FIX-1: a NAMESPACE-scoped viewer is allowed its own namespace but DENIED the
	// cluster-wide _all query (CanView(cluster, "")), so it can't list prod/_all.
	nsScoped := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod", Namespace: "team-a"}})
	if !nsScoped.CanView("prod", "team-a") {
		t.Fatalf("namespace-scoped viewer should be allowed its own namespace (prod/team-a)")
	}
	if nsScoped.CanView("prod", "") {
		t.Fatalf("namespace-scoped viewer must be DENIED prod/_all (cluster-wide leak)")
	}
}

func TestListEvents_FiltersAndLimits(t *testing.T) {
	now := time.Now()
	src := &stubClusterSource{events: []model.Signal{
		{ID: "e1", Kind: model.SignalK8sEvent, Timestamp: now.Add(-5 * time.Minute), Title: "newest"},
		{ID: "e2", Kind: model.SignalK8sEvent, Timestamp: now.Add(-30 * time.Minute), Title: "middle"},
		{ID: "e3", Kind: model.SignalK8sEvent, Timestamp: now.Add(-50 * time.Minute), Title: "oldest"},
	}}
	srv := lensTestServer(t, "", stubResolver{src: src})

	// limit=2 -> the two newest, ordered newest-first.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/events?limit=2&since=90m", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	rec := httptest.NewRecorder()
	srv.handleListEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list events: got %d, want 200", rec.Code)
	}
	var got []model.Signal
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("limit=2 should return 2 events, got %d", len(got))
	}
	if got[0].Title != "newest" || got[1].Title != "middle" {
		t.Errorf("events not newest-first: %q, %q", got[0].Title, got[1].Title)
	}
	if src.lastEventNs != "demo" {
		t.Errorf("Events namespace: got %q, want demo", src.lastEventNs)
	}
}

func TestListEvents_LimitCappedAtMax(t *testing.T) {
	// A client-supplied limit above eventsMaxLimit is clamped, so a request can't
	// ask for an unbounded result set. We seed more than the cap and assert the
	// returned slice is capped at eventsMaxLimit.
	now := time.Now()
	sigs := make([]model.Signal, eventsMaxLimit+50)
	for i := range sigs {
		sigs[i] = model.Signal{
			ID:        "e",
			Kind:      model.SignalK8sEvent,
			Timestamp: now.Add(-time.Duration(i) * time.Second),
		}
	}
	src := &stubClusterSource{events: sigs}
	srv := lensTestServer(t, "", stubResolver{src: src})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/events?limit=100000", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	rec := httptest.NewRecorder()
	srv.handleListEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list events: got %d, want 200", rec.Code)
	}
	var got []model.Signal
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(got) != eventsMaxLimit {
		t.Fatalf("limit must be capped at %d, got %d", eventsMaxLimit, len(got))
	}
}

func TestListEvents_AllNamespaces(t *testing.T) {
	src := &stubClusterSource{events: []model.Signal{{ID: "e1", Kind: model.SignalK8sEvent, Timestamp: time.Now()}}}
	srv := lensTestServer(t, "", stubResolver{src: src})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/_all/events", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "_all")
	rec := httptest.NewRecorder()
	srv.handleListEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list events _all: got %d, want 200", rec.Code)
	}
	if src.lastEventNs != "" {
		t.Errorf("_all must pass empty namespace to Events, got %q", src.lastEventNs)
	}
}

func TestListEvents_Unauthenticated(t *testing.T) {
	srv := lensTestServer(t, testSSOConfig, stubResolver{src: &stubClusterSource{}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/events", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	rec := httptest.NewRecorder()
	srv.handleListEvents(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated events: got %d, want 401", rec.Code)
	}
}

func TestListNodes_ReturnsList(t *testing.T) {
	src := &stubClusterSource{nodes: []model.Node{
		{Name: "cp-1", Ready: true, Roles: []string{"control-plane"}, KubeletVersion: "v1.30.2"},
		{Name: "worker-1", Ready: false},
	}}
	srv := lensTestServer(t, testSSOConfig, stubResolver{src: src})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/nodes", nil)
	req.SetPathValue("cluster", "prod")
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()
	srv.handleListNodes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list nodes: got %d, want 200", rec.Code)
	}
	var got []model.Node
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode nodes: %v", err)
	}
	if len(got) != 2 || got[0].Name != "cp-1" || !got[0].Ready || got[1].Name != "worker-1" || got[1].Ready {
		t.Fatalf("unexpected nodes: %+v", got)
	}
}

func TestListNodes_Unauthenticated(t *testing.T) {
	srv := lensTestServer(t, testSSOConfig, stubResolver{src: &stubClusterSource{}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/nodes", nil)
	req.SetPathValue("cluster", "prod")
	rec := httptest.NewRecorder()
	srv.handleListNodes(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated nodes: got %d, want 401", rec.Code)
	}
}

func TestListNodes_ScopedViewerForbidden(t *testing.T) {
	// Nodes gate on cluster-level visibility: CanView(cluster, "") (now strict,
	// post FIX-1). A CLUSTER-wide prod viewer reaches its own nodes but not
	// staging's. We assert the enforcer gate the handler relies on.
	enf := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod"}})
	if !enf.CanView("prod", "") {
		t.Fatalf("cluster-wide prod viewer should be allowed prod cluster-level")
	}
	if enf.CanView("staging", "") {
		t.Fatalf("prod viewer must be denied staging cluster-level (handler returns 403)")
	}

	// FIX-1: a NAMESPACE-scoped viewer must NOT enumerate cluster nodes — the
	// strict CanView(cluster, "") gate denies it even for its own cluster.
	nsScoped := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod", Namespace: "team-a"}})
	if nsScoped.CanView("prod", "") {
		t.Fatalf("namespace-scoped viewer must be denied cluster node listing (handler returns 403)")
	}
}
