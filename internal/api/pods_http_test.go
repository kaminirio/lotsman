package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lotsman/internal/auth"
	"lotsman/internal/engine"
	"lotsman/internal/model"
	"lotsman/internal/rbac"
	"lotsman/internal/sources"
	"lotsman/internal/store"
)

// stubClusterSource is a sources.ClusterSource that returns a canned pod, so the
// pod handlers can be tested without a real cluster. It records whether Reveal
// was requested so we can assert the admin/non-admin reveal wiring.
type stubClusterSource struct {
	pods       []model.Pod
	lastReveal bool

	workloads     []model.ResourceRef
	events        []model.Signal
	lastNamespace string // namespace passed to ListWorkloads
	lastEventNs   string // namespace passed to Events

	nodes           []model.Node
	configMaps      []model.ConfigMapRef
	configMapDetail model.ConfigMapDetail
	secrets         []model.SecretRef
	secretDetail    model.SecretDetail
	lastSecretQuery sources.SecretQuery
	lastLogsQuery   sources.PodLogsQuery
}

func (s *stubClusterSource) Name() string { return "stub" }
func (s *stubClusterSource) Events(_ context.Context, q sources.EventQuery) ([]model.Signal, error) {
	s.lastEventNs = q.Resource.Namespace
	return s.events, nil
}
func (s *stubClusterSource) ListWorkloads(_ context.Context, ns string) ([]model.ResourceRef, error) {
	s.lastNamespace = ns
	return s.workloads, nil
}
func (s *stubClusterSource) ListNodes(_ context.Context) ([]model.Node, error) {
	return s.nodes, nil
}
func (s *stubClusterSource) ListPods(_ context.Context, q sources.PodQuery) ([]model.Pod, error) {
	s.lastReveal = q.Reveal
	return s.pods, nil
}
func (s *stubClusterSource) PodLogs(_ context.Context, q sources.PodLogsQuery) (model.PodLogsResult, error) {
	s.lastLogsQuery = q
	return model.PodLogsResult{
		Pod:       q.Resource.Pod,
		Namespace: q.Resource.Namespace,
		Container: q.Container,
		Lines:     "hello\nworld\n",
	}, nil
}
func (s *stubClusterSource) ListConfigMaps(_ context.Context, ns string) ([]model.ConfigMapRef, error) {
	s.lastNamespace = ns
	return s.configMaps, nil
}
func (s *stubClusterSource) GetConfigMap(_ context.Context, _ model.ResourceRef) (model.ConfigMapDetail, error) {
	return s.configMapDetail, nil
}
func (s *stubClusterSource) ListSecrets(_ context.Context, ns string) ([]model.SecretRef, error) {
	s.lastNamespace = ns
	return s.secrets, nil
}
func (s *stubClusterSource) GetSecret(_ context.Context, q sources.SecretQuery) (model.SecretDetail, error) {
	s.lastSecretQuery = q
	d := s.secretDetail
	// Mirror the adapter contract the API relies on: values are only present when
	// Reveal is set (the handler additionally masks for non-admins).
	if !q.Reveal {
		entries := make([]model.SecretEntry, len(d.Entries))
		for i, e := range d.Entries {
			e.Value = ""
			e.Masked = true
			entries[i] = e
		}
		d.Entries = entries
	}
	return d, nil
}

// stubResolver returns a fixed provider built around stubClusterSource and
// reports a configurable set of registry-connected cluster names.
type stubResolver struct {
	src      *stubClusterSource
	clusters []string
}

func (r stubResolver) Provider(cluster string) (sources.Provider, error) {
	return sources.NewProvider(cluster,
		stubNoopLog{}, stubNoopMetric{}, stubNoopDeploy{}, r.src), nil
}

func (r stubResolver) Clusters() []string { return r.clusters }

type stubNoopLog struct{}

func (stubNoopLog) Name() string { return "noop" }
func (stubNoopLog) QueryLogs(context.Context, sources.LogQuery) ([]model.Signal, error) {
	return nil, nil
}

type stubNoopMetric struct{}

func (stubNoopMetric) Name() string { return "noop" }
func (stubNoopMetric) QueryInstant(context.Context, sources.MetricQuery) (sources.MetricResult, error) {
	return sources.MetricResult{}, nil
}
func (stubNoopMetric) QueryRange(context.Context, sources.MetricRangeQuery) (sources.MetricResult, error) {
	return sources.MetricResult{}, nil
}

type stubNoopDeploy struct{}

func (stubNoopDeploy) Name() string { return "noop" }
func (stubNoopDeploy) ChangeEvents(context.Context, sources.ChangeQuery) ([]model.Signal, error) {
	return nil, nil
}

// podTestServer builds a Server wired with a stub provider returning the given
// pod, so the pod handlers run end-to-end through RBAC + env masking.
func podTestServer(t *testing.T, ssoJSON string, src *stubClusterSource) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := auth.NewManagerErr(ssoJSON, logger)
	if err != nil {
		t.Fatalf("build auth manager: %v", err)
	}
	srv, err := New(Config{
		Addr:    ":0",
		Version: "test",
		Engine:  engine.New(failingResolver{}, logger),
		Store:   store.NewMemory(),
		Auth:    mgr,
		Sources: stubResolver{src: src},
	}, logger)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return srv
}

func podWithSecretEnv() model.Pod {
	return model.Pod{
		Name:      "api-1",
		Namespace: "demo",
		Phase:     "Running",
		Ready:     true,
		Containers: []model.Container{{
			Name:  "api",
			Image: "img",
			Env: []model.ContainerEnvVar{
				// LOG_LEVEL and DATABASE_URL are NOT credential-named, but under the
				// default-deny policy every literal value is masked for non-admins.
				{Name: "LOG_LEVEL", Value: "debug"},
				{Name: "DATABASE_URL", Value: "postgres://u:p@db:5432/app"},
				{Name: "DB_PASSWORD", Value: "s3cr3t"},
				{Name: "API_TOKEN", Value: "abc123"},
			},
		}},
	}
}

func decodePods(t *testing.T, rec *httptest.ResponseRecorder) []model.Pod {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list pods: got %d, want 200", rec.Code)
	}
	var got []model.Pod
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode pods: %v", err)
	}
	return got
}

func podEnvByName(p model.Pod) map[string]model.ContainerEnvVar {
	m := map[string]model.ContainerEnvVar{}
	for _, e := range p.Containers[0].Env {
		m[e.Name] = e
	}
	return m
}

func TestListPods_NonAdminMasksAllLiteralEnv(t *testing.T) {
	src := &stubClusterSource{pods: []model.Pod{podWithSecretEnv()}}
	srv := podTestServer(t, testSSOConfig, src)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()

	srv.handleListPods(rec, req)

	pods := decodePods(t, rec)
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	env := podEnvByName(pods[0])
	// Default-deny: non-credential-named literals are masked too. LOG_LEVEL and
	// DATABASE_URL must not leak even though their names aren't "secretish".
	if !env["LOG_LEVEL"].Masked || env["LOG_LEVEL"].Value == "debug" {
		t.Errorf("LOG_LEVEL should be masked for non-admin, got %+v", env["LOG_LEVEL"])
	}
	if !env["DATABASE_URL"].Masked || strings.Contains(env["DATABASE_URL"].Value, "postgres") {
		t.Errorf("DATABASE_URL should be masked for non-admin, got %+v", env["DATABASE_URL"])
	}
	if !env["DB_PASSWORD"].Masked || env["DB_PASSWORD"].Value == "s3cr3t" {
		t.Errorf("DB_PASSWORD should be masked, got %+v", env["DB_PASSWORD"])
	}
	if !env["API_TOKEN"].Masked || env["API_TOKEN"].Value == "abc123" {
		t.Errorf("API_TOKEN should be masked, got %+v", env["API_TOKEN"])
	}
	if src.lastReveal {
		t.Errorf("non-admin must not request Reveal")
	}
}

func TestListPods_AdminGetsVerbatim(t *testing.T) {
	src := &stubClusterSource{pods: []model.Pod{podWithSecretEnv()}}
	srv := podTestServer(t, testSSOConfig, src)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()

	srv.handleListPods(rec, req)

	pods := decodePods(t, rec)
	env := podEnvByName(pods[0])
	if env["DB_PASSWORD"].Value != "s3cr3t" || env["DB_PASSWORD"].Masked {
		t.Errorf("admin DB_PASSWORD should be verbatim, got %+v", env["DB_PASSWORD"])
	}
	// Non-credential-named literals are also verbatim for admins (no false masking).
	if env["DATABASE_URL"].Value != "postgres://u:p@db:5432/app" || env["DATABASE_URL"].Masked {
		t.Errorf("admin DATABASE_URL should be verbatim, got %+v", env["DATABASE_URL"])
	}
	if env["LOG_LEVEL"].Value != "debug" || env["LOG_LEVEL"].Masked {
		t.Errorf("admin LOG_LEVEL should be verbatim, got %+v", env["LOG_LEVEL"])
	}
	if !src.lastReveal {
		t.Errorf("admin must request Reveal")
	}
}

func TestListPods_AnonymousGetsVerbatim(t *testing.T) {
	// No SSO: Anonymous is a global admin, so values are verbatim and Reveal set.
	src := &stubClusterSource{pods: []model.Pod{podWithSecretEnv()}}
	srv := podTestServer(t, "", src)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	rec := httptest.NewRecorder()

	srv.handleListPods(rec, req)

	pods := decodePods(t, rec)
	env := podEnvByName(pods[0])
	if env["DB_PASSWORD"].Value != "s3cr3t" || env["DB_PASSWORD"].Masked {
		t.Errorf("anonymous (admin) DB_PASSWORD should be verbatim, got %+v", env["DB_PASSWORD"])
	}
	// No-SSO local-dev path: anonymous=admin sees literals verbatim, incl.
	// non-credential-named ones — the default-deny masking must not touch admins.
	if env["DATABASE_URL"].Value != "postgres://u:p@db:5432/app" || env["DATABASE_URL"].Masked {
		t.Errorf("anonymous (admin) DATABASE_URL should be verbatim, got %+v", env["DATABASE_URL"])
	}
	if !src.lastReveal {
		t.Errorf("anonymous admin must request Reveal")
	}
}

func TestListPods_ForbiddenNamespace(t *testing.T) {
	// A viewer scoped to "prod" only must be 403 on a different cluster. The lean
	// default policy mints global viewers, so we exercise the forbidden path with
	// an unauthenticated request instead (401) and a scoped check below.
	src := &stubClusterSource{pods: []model.Pod{podWithSecretEnv()}}
	srv := podTestServer(t, testSSOConfig, src)

	// Unauthenticated -> 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	rec := httptest.NewRecorder()
	srv.handleListPods(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list pods: got %d, want 401", rec.Code)
	}
}

func TestListPods_ScopedViewerForbidden(t *testing.T) {
	// The handler gates on enf.CanView(cluster, namespace) and returns 403 when it
	// is false. A viewer scoped to "prod" is denied "staging" — assert the exact
	// enforcer decision the handler relies on.
	enf := rbac.New([]rbac.Binding{{Role: rbac.RoleViewer, Cluster: "prod"}})
	if !enf.CanView("prod", "demo") {
		t.Fatalf("prod-scoped viewer should be allowed prod/demo")
	}
	if enf.CanView("staging", "demo") {
		t.Fatalf("prod-scoped viewer must be denied staging/demo (handler returns 403)")
	}
}

func TestGetPodLogs_ReturnsLines(t *testing.T) {
	src := &stubClusterSource{}
	srv := podTestServer(t, testSSOConfig, src)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods/api-1/logs?container=api&tail=50", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	req.SetPathValue("pod", "api-1")
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()

	srv.handleGetPodLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get pod logs: got %d, want 200", rec.Code)
	}
	var res model.PodLogsResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if res.Pod != "api-1" || res.Container != "api" {
		t.Errorf("identity: got %q/%q", res.Pod, res.Container)
	}
	if res.Lines != "hello\nworld\n" {
		t.Errorf("Lines: got %q", res.Lines)
	}
}

func TestGetPodLogs_Unauthenticated(t *testing.T) {
	src := &stubClusterSource{}
	srv := podTestServer(t, testSSOConfig, src)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods/api-1/logs", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	req.SetPathValue("pod", "api-1")
	rec := httptest.NewRecorder()

	srv.handleGetPodLogs(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated logs: got %d, want 401", rec.Code)
	}
}

// An oversized tail must be clamped to the ceiling before it reaches the source,
// so tail=99999999 can't pull an unbounded log body (API-3).
func TestGetPodLogs_TailClampedToMax(t *testing.T) {
	src := &stubClusterSource{}
	srv := podTestServer(t, testSSOConfig, src)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods/api-1/logs?tail=99999999", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	req.SetPathValue("pod", "api-1")
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()

	srv.handleGetPodLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get pod logs: got %d, want 200", rec.Code)
	}
	if src.lastLogsQuery.TailLines != podLogsMaxTail {
		t.Fatalf("oversized tail not clamped: got %d, want %d", src.lastLogsQuery.TailLines, podLogsMaxTail)
	}
}

// An absent tail must default (not 0/unbounded); a valid in-range tail is passed
// through verbatim.
func TestGetPodLogs_TailDefaultAndPassthrough(t *testing.T) {
	src := &stubClusterSource{}
	srv := podTestServer(t, testSSOConfig, src)

	// Absent -> default.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods/api-1/logs", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	req.SetPathValue("pod", "api-1")
	req.AddCookie(mintCookie(t, "viewer-user"))
	srv.handleGetPodLogs(httptest.NewRecorder(), req)
	if src.lastLogsQuery.TailLines != podLogsDefaultTail {
		t.Fatalf("absent tail: got %d, want default %d", src.lastLogsQuery.TailLines, podLogsDefaultTail)
	}

	// In-range value -> passed through.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/clusters/prod/namespaces/demo/pods/api-1/logs?tail=250", nil)
	req.SetPathValue("cluster", "prod")
	req.SetPathValue("namespace", "demo")
	req.SetPathValue("pod", "api-1")
	req.AddCookie(mintCookie(t, "viewer-user"))
	srv.handleGetPodLogs(httptest.NewRecorder(), req)
	if src.lastLogsQuery.TailLines != 250 {
		t.Fatalf("in-range tail: got %d, want 250", src.lastLogsQuery.TailLines)
	}
}
