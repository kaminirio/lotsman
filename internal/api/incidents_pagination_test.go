package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/store"
)

func incidentAt(id, cluster, ns string, opened time.Time) *model.Incident {
	inc := incident(id, cluster, ns)
	inc.OpenedAt = opened
	inc.UpdatedAt = opened
	return inc
}

// A scoped viewer must receive ALL incidents they can see, not a page truncated
// by a DB limit consumed by rows they cannot see. This is the API-1 bug: with the
// old hardcoded DB Limit:100 applied BEFORE the RBAC filter, a prod-scoped viewer
// swamped by newer staging incidents could get zero prod incidents even though
// many exist. The fix filters first, then paginates.
func TestListIncidentsScopedVisibilityNotTruncatedByLimit(t *testing.T) {
	const cfgScopedViewer = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "bindings": [{"subject": "prod-viewer", "role": "viewer", "cluster": "prod"}],
  "github": {"client_id": "cid", "client_secret": "csecret", "allowed_usernames": ["prod-viewer"]}
}`
	base := time.Unix(1_000_000, 0)
	var incs []*model.Incident
	// 150 NEWER staging incidents the prod-viewer cannot see.
	for i := 0; i < 150; i++ {
		incs = append(incs, incidentAt(fmt.Sprintf("stg-%03d", i), "staging", "team-a", base.Add(time.Duration(i+200)*time.Minute)))
	}
	// 10 OLDER prod incidents the prod-viewer CAN see.
	for i := 0; i < 10; i++ {
		incs = append(incs, incidentAt(fmt.Sprintf("prod-%02d", i), "prod", "team-a", base.Add(time.Duration(i)*time.Minute)))
	}

	srv := testServer(t, cfgScopedViewer, incs...)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?limit=200", nil)
	req.AddCookie(mintCookie(t, "prod-viewer"))
	rec := httptest.NewRecorder()
	srv.handleListIncidents(rec, req)

	got := decodeIncidents(t, rec)
	if len(got) != 10 {
		t.Fatalf("prod-scoped viewer: want all 10 visible incidents, got %d", len(got))
	}
	for _, inc := range got {
		if inc.Resource.Cluster != "prod" {
			t.Fatalf("leaked non-prod incident: %q", inc.Resource.Cluster)
		}
	}
}

// filterSpyStore records the last IncidentFilter passed to ListIncidents while
// delegating every Store method to an embedded backing store.
type filterSpyStore struct {
	store.Store
	lastFilter store.IncidentFilter
}

func (s *filterSpyStore) ListIncidents(ctx context.Context, f store.IncidentFilter) ([]*model.Incident, error) {
	s.lastFilter = f
	return s.Store.ListIncidents(ctx, f)
}

// The list endpoint must bound its pre-filter store fetch (maxIncidentScan)
// rather than pulling the whole table (Limit==0), while STILL returning every
// visible incident — proving the memory fix does not reintroduce truncation.
func TestListIncidentsBoundedFetchNotTruncated(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	var incs []*model.Incident
	// 150 NEWER staging incidents the prod-viewer cannot see, then 10 OLDER prod
	// incidents they can — the same swamped-scoped-viewer shape as the truncation
	// regression test, well under the scan cap so all 10 must survive.
	for i := 0; i < 150; i++ {
		incs = append(incs, incidentAt(fmt.Sprintf("stg-%03d", i), "staging", "team-a", base.Add(time.Duration(i+200)*time.Minute)))
	}
	for i := 0; i < 10; i++ {
		incs = append(incs, incidentAt(fmt.Sprintf("prod-%02d", i), "prod", "team-a", base.Add(time.Duration(i)*time.Minute)))
	}

	const cfgScopedViewer = `{
  "session_secret": "test-session-secret-at-least-32-chars-long",
  "base_url": "http://localhost:8080",
  "ui_url": "http://localhost:3000",
  "init_admin": "admin-user",
  "bindings": [{"subject": "prod-viewer", "role": "viewer", "cluster": "prod"}],
  "github": {"client_id": "cid", "client_secret": "csecret", "allowed_usernames": ["prod-viewer"]}
}`
	srv := testServer(t, cfgScopedViewer, incs...)
	spy := &filterSpyStore{Store: srv.cfg.Store}
	srv.cfg.Store = spy

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?limit=200", nil)
	req.AddCookie(mintCookie(t, "prod-viewer"))
	rec := httptest.NewRecorder()
	srv.handleListIncidents(rec, req)

	// Bounded fetch: the store is asked for a capped, non-zero scan (never the
	// unbounded Limit==0 that materializes the whole table).
	if spy.lastFilter.Limit != maxIncidentScan {
		t.Fatalf("store fetch limit = %d, want bounded maxIncidentScan=%d", spy.lastFilter.Limit, maxIncidentScan)
	}
	// Not truncated: all 10 visible prod incidents come back despite 150 newer
	// invisible staging ones.
	if got := decodeIncidents(t, rec); len(got) != 10 {
		t.Fatalf("bounded-but-not-truncated: want 10 visible incidents, got %d", len(got))
	}
}

// limit/offset select a stable, most-recent-first window.
func TestListIncidentsPagination(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	// inc-0..inc-4 at increasing times -> newest-first order is inc-4..inc-0.
	var incs []*model.Incident
	for i := 0; i < 5; i++ {
		incs = append(incs, incidentAt(fmt.Sprintf("inc-%d", i), "prod", "demo", base.Add(time.Duration(i)*time.Minute)))
	}
	srv := testServer(t, "", incs...) // no SSO -> global admin sees all

	page := func(query string) []*model.Incident {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?"+query, nil)
		rec := httptest.NewRecorder()
		srv.handleListIncidents(rec, req)
		return decodeIncidents(t, rec)
	}

	first := page("limit=2&offset=0")
	if len(first) != 2 || first[0].ID != "inc-4" || first[1].ID != "inc-3" {
		t.Fatalf("first page: got %v", ids(first))
	}
	second := page("limit=2&offset=2")
	if len(second) != 2 || second[0].ID != "inc-2" || second[1].ID != "inc-1" {
		t.Fatalf("second page: got %v", ids(second))
	}
	last := page("limit=2&offset=4")
	if len(last) != 1 || last[0].ID != "inc-0" {
		t.Fatalf("last page: got %v", ids(last))
	}
	if beyond := page("limit=2&offset=99"); len(beyond) != 0 {
		t.Fatalf("offset past end: want empty, got %v", ids(beyond))
	}
}

func ids(incs []*model.Incident) []string {
	out := make([]string, len(incs))
	for i, inc := range incs {
		out[i] = inc.ID
	}
	return out
}

func TestParsePageParams(t *testing.T) {
	mk := func(q string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/x?"+q, nil)
	}
	cases := []struct {
		query            string
		wantLim, wantOff int
	}{
		{"", 50, 0},
		{"limit=10&offset=5", 10, 5},
		{"limit=9999", 200, 0}, // capped
		{"limit=-3", 50, 0},    // non-positive -> default
		{"limit=abc", 50, 0},   // garbage -> default
		{"offset=-1", 50, 0},   // negative -> 0
		{"offset=xyz", 50, 0},  // garbage -> 0
	}
	for _, c := range cases {
		lim, off := parsePageParams(mk(c.query), 50, 200)
		if lim != c.wantLim || off != c.wantOff {
			t.Errorf("parsePageParams(%q) = (%d,%d), want (%d,%d)", c.query, lim, off, c.wantLim, c.wantOff)
		}
	}
}

func TestPaginate(t *testing.T) {
	s := []int{0, 1, 2, 3, 4}
	if got := paginate(s, 0, 2); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("window [0,2): %v", got)
	}
	if got := paginate(s, 4, 2); len(got) != 1 || got[0] != 4 {
		t.Errorf("window clamped to end: %v", got)
	}
	if got := paginate(s, 99, 2); len(got) != 0 {
		t.Errorf("offset past end: want empty, got %v", got)
	}
	// Offset past the end must return a non-nil empty slice so the JSON encodes as
	// [] rather than null (the API's array contract).
	if got := paginate(s, 99, 2); got == nil {
		t.Error("offset past end should yield non-nil empty slice (JSON [], not null)")
	}
	if got := paginate([]int{}, 0, 10); got == nil {
		t.Error("empty input should yield non-nil empty slice (JSON [])")
	}
}
