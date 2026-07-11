package argocd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// buildAppList returns JSON for an ArgoCD /api/v1/applications list containing
// the given apps.
func buildAppList(apps []argoApp) []byte {
	b, _ := json.Marshal(appList{Items: apps})
	return b
}

// makeApp builds an argoApp with the given identity, destination namespace, and
// history entries.
func makeApp(name, destNS string, history []argoHistory) argoApp {
	var app argoApp
	app.Metadata.Name = name
	app.Spec.Destination.Namespace = destNS
	app.Status.History = history
	return app
}

func TestChangeEvents_MatchByNameAndRange(t *testing.T) {
	// An app whose name matches the resource name and whose history has a sync
	// inside the query range must yield a SignalChange. A sync outside the range
	// must be filtered out.
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	withinRange := base.Add(5 * time.Minute)
	beforeRange := base.Add(-10 * time.Minute)
	afterRange := base.Add(2 * time.Hour)

	history := []argoHistory{
		{Revision: "abc123", DeployedAt: withinRange, ID: 1},
		{Revision: "def456", DeployedAt: beforeRange, ID: 2},
		{Revision: "ghi789", DeployedAt: afterRange, ID: 3},
	}
	app := makeApp("payments-api", "payments", history)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(buildAppList([]argoApp{app}))
	}))
	defer srv.Close()

	c := New(srv.URL, "my-token", srv.Client())
	q := sources.ChangeQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "payments", Name: "payments-api"},
		Range: sources.TimeRange{
			Start: base,
			End:   base.Add(1 * time.Hour),
		},
	}
	sigs, err := c.ChangeEvents(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the within-range sync should appear.
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	s := sigs[0]
	if s.Kind != model.SignalChange {
		t.Errorf("Kind: got %q, want %q", s.Kind, model.SignalChange)
	}
	if s.Source != "argocd" {
		t.Errorf("Source: got %q, want argocd", s.Source)
	}
	if s.Change == nil {
		t.Fatal("Change field must not be nil")
	}
	if s.Change.Revision != "abc123" {
		t.Errorf("Revision: got %q, want abc123", s.Change.Revision)
	}
	if s.Change.App != "payments-api" {
		t.Errorf("App: got %q, want payments-api", s.Change.App)
	}
	if s.Change.Source != "argocd" {
		t.Errorf("Change.Source: got %q, want argocd", s.Change.Source)
	}
	if !s.Change.SyncedAt.Equal(withinRange.UTC()) {
		t.Errorf("SyncedAt: got %v, want %v", s.Change.SyncedAt, withinRange.UTC())
	}
	if !s.Timestamp.Equal(withinRange.UTC()) {
		t.Errorf("Timestamp: got %v, want %v", s.Timestamp, withinRange.UTC())
	}
}

func TestChangeEvents_MatchByDestinationNamespace(t *testing.T) {
	// When name doesn't match but destination namespace matches, the app must be
	// selected as the best match.
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	deployedAt := base.Add(30 * time.Second)

	// App name does NOT match the resource name, but namespace does.
	app := makeApp("frontend-app", "ui", []argoHistory{
		{Revision: "rev1", DeployedAt: deployedAt, ID: 1},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(buildAppList([]argoApp{app}))
	}))
	defer srv.Close()

	c := New(srv.URL, "", srv.Client())
	q := sources.ChangeQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "ui", Name: "some-other-workload"},
		Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
	}
	sigs, err := c.ChangeEvents(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	if sigs[0].Change.App != "frontend-app" {
		t.Errorf("App: got %q, want frontend-app", sigs[0].Change.App)
	}
}

func TestChangeEvents_NoMatchingApp(t *testing.T) {
	// When no app owns the resource, must return (nil, nil) — not an error.
	app := makeApp("unrelated-app", "other-ns", nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(buildAppList([]argoApp{app}))
	}))
	defer srv.Close()

	c := New(srv.URL, "", srv.Client())
	q := sources.ChangeQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "payments", Name: "payments-api"},
	}
	sigs, err := c.ChangeEvents(context.Background(), q)
	if err != nil {
		t.Fatalf("expected no error for no matching app, got: %v", err)
	}
	if sigs != nil {
		t.Errorf("expected nil signals for no matching app, got %v", sigs)
	}
}

func TestChangeEvents_AuthorizationHeader(t *testing.T) {
	// When a token is set, Authorization: Bearer <token> must be sent.
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(buildAppList(nil))
	}))
	defer srv.Close()

	const token = "my-super-secret-token"
	c := New(srv.URL, token, srv.Client())
	_, _ = c.ChangeEvents(context.Background(), sources.ChangeQuery{
		Resource: model.ResourceRef{Cluster: "c", Name: "x"},
	})

	wantAuth := "Bearer " + token
	if capturedAuth != wantAuth {
		t.Errorf("Authorization: got %q, want %q", capturedAuth, wantAuth)
	}
}

func TestChangeEvents_EmptyTokenNoAuthHeader(t *testing.T) {
	// When token is empty, no Authorization header must be sent.
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(buildAppList(nil))
	}))
	defer srv.Close()

	c := New(srv.URL, "", srv.Client()) // empty token
	_, _ = c.ChangeEvents(context.Background(), sources.ChangeQuery{
		Resource: model.ResourceRef{Cluster: "c", Name: "x"},
	})

	if capturedAuth != "" {
		t.Errorf("expected no Authorization header, got %q", capturedAuth)
	}
}

func TestChangeEvents_ChangeRefURL(t *testing.T) {
	// The ChangeRef.URL must point back to the ArgoCD app in the UI.
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	deployedAt := base.Add(1 * time.Minute)

	app := makeApp("shop", "shop-ns", []argoHistory{
		{Revision: "rev99", DeployedAt: deployedAt, ID: 1},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(buildAppList([]argoApp{app}))
	}))
	defer srv.Close()

	c := New(srv.URL, "", srv.Client())
	sigs, err := c.ChangeEvents(context.Background(), sources.ChangeQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "shop-ns", Name: "shop"},
		Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	wantURL := srv.URL + "/applications/shop"
	if sigs[0].Change.URL != wantURL {
		t.Errorf("Change.URL: got %q, want %q", sigs[0].Change.URL, wantURL)
	}
}

func TestChangeEvents_ResourcePreservedInSignal(t *testing.T) {
	// The resource from the query must be carried through to the signal.
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	deployedAt := base.Add(1 * time.Minute)
	app := makeApp("my-app", "my-ns", []argoHistory{
		{Revision: "sha", DeployedAt: deployedAt},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(buildAppList([]argoApp{app}))
	}))
	defer srv.Close()

	queryResource := model.ResourceRef{Cluster: "prod", Namespace: "my-ns", Name: "my-app", Kind: "Deployment"}
	c := New(srv.URL, "", srv.Client())
	sigs, err := c.ChangeEvents(context.Background(), sources.ChangeQuery{
		Resource: queryResource,
		Range:    sources.TimeRange{Start: base, End: base.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	if sigs[0].Resource != queryResource {
		t.Errorf("Resource: got %+v, want %+v", sigs[0].Resource, queryResource)
	}
}

func TestChangeEvents_HTTP500Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "argocd unavailable", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "", srv.Client())
	_, err := c.ChangeEvents(context.Background(), sources.ChangeQuery{
		Resource: model.ResourceRef{Cluster: "prod", Name: "app"},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestInRange(t *testing.T) {
	// Table-driven tests for the inRange helper.
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		t      time.Time
		r      sources.TimeRange
		wantIn bool
	}{
		{"within range", base.Add(5 * time.Minute), sources.TimeRange{Start: base, End: base.Add(time.Hour)}, true},
		{"at start (inclusive)", base, sources.TimeRange{Start: base, End: base.Add(time.Hour)}, true},
		{"at end (exclusive)", base.Add(time.Hour), sources.TimeRange{Start: base, End: base.Add(time.Hour)}, false},
		{"before start", base.Add(-1 * time.Minute), sources.TimeRange{Start: base, End: base.Add(time.Hour)}, false},
		{"after end", base.Add(2 * time.Hour), sources.TimeRange{Start: base, End: base.Add(time.Hour)}, false},
		{"zero start (unbounded left)", base.Add(-1000 * time.Hour), sources.TimeRange{End: base.Add(time.Hour)}, true},
		{"zero end (unbounded right)", base.Add(1000 * time.Hour), sources.TimeRange{Start: base}, true},
		{"zero range (match all)", base, sources.TimeRange{}, true},
		{"zero time", time.Time{}, sources.TimeRange{Start: base, End: base.Add(time.Hour)}, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := inRange(tc.t, tc.r)
			if got != tc.wantIn {
				t.Errorf("inRange(%v, %+v) = %v, want %v", tc.t, tc.r, got, tc.wantIn)
			}
		})
	}
}

func TestBestMatch(t *testing.T) {
	// Table-driven tests for the bestMatch helper.
	appExact := makeApp("payments-api", "payments", nil)
	appNS := makeApp("other-app", "payments", nil)
	appUnrelated := makeApp("unrelated", "backend", nil)

	cases := []struct {
		name    string
		apps    []argoApp
		ref     model.ResourceRef
		wantApp string
		wantOK  bool
	}{
		{
			name:    "exact name match wins",
			apps:    []argoApp{appNS, appExact},
			ref:     model.ResourceRef{Namespace: "payments", Name: "payments-api"},
			wantApp: "payments-api",
			wantOK:  true,
		},
		{
			name:    "namespace fallback",
			apps:    []argoApp{appNS, appUnrelated},
			ref:     model.ResourceRef{Namespace: "payments", Name: "no-match"},
			wantApp: "other-app",
			wantOK:  true,
		},
		{
			name:   "no match",
			apps:   []argoApp{appUnrelated},
			ref:    model.ResourceRef{Namespace: "payments", Name: "payments-api"},
			wantOK: false,
		},
		{
			name:   "empty apps",
			apps:   nil,
			ref:    model.ResourceRef{Namespace: "payments", Name: "payments-api"},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := bestMatch(tc.apps, tc.ref)
			if ok != tc.wantOK {
				t.Fatalf("bestMatch ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Metadata.Name != tc.wantApp {
				t.Errorf("bestMatch app = %q, want %q", got.Metadata.Name, tc.wantApp)
			}
		})
	}
}

func TestNew_NilClientHasTimeout(t *testing.T) {
	// SRC-1: a nil client must fall back to a client WITH a non-zero timeout,
	// not the timeout-less http.DefaultClient.
	c := New("http://argocd", "tok", nil)
	if c.HTTP == nil {
		t.Fatal("expected non-nil HTTP client for nil input")
	}
	if c.HTTP == http.DefaultClient {
		t.Fatal("nil client fell back to http.DefaultClient (no timeout)")
	}
	if c.HTTP.Timeout <= 0 {
		t.Fatalf("expected non-zero timeout, got %v", c.HTTP.Timeout)
	}
}

func TestNew_ExplicitClientHonored(t *testing.T) {
	// SRC-1: an explicitly-injected client must not be overridden.
	custom := &http.Client{Timeout: 5 * time.Second}
	c := New("http://argocd", "tok", custom)
	if c.HTTP != custom {
		t.Fatal("explicit client was overridden")
	}
}
