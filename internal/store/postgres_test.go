package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"lotsman/internal/model"
)

// newTestPostgres connects to the database named by LOTSMAN_TEST_DATABASE_URL,
// skipping the test when it is unset so the default `go test ./...` never
// requires a database.
func newTestPostgres(t *testing.T) *Postgres {
	t.Helper()
	dsn := os.Getenv("LOTSMAN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LOTSMAN_TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pg, err := NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPostgres: %v", err)
	}
	t.Cleanup(pg.Close)
	return pg
}

// uniqueID namespaces rows per test run so reruns are safe.
func uniqueID(prefix string) string {
	return prefix + "-" + time.Now().Format("20060102150405.000000000")
}

func sampleIncident(id, cluster string, status model.IncidentStatus, openedAt time.Time) *model.Incident {
	ref := model.ResourceRef{Cluster: cluster, Namespace: "payments", Kind: "Deployment", Name: "payments-api"}
	change := &model.ChangeRef{Source: "argocd", App: "payments-api", Revision: "9f4c2a18b7", SyncedAt: openedAt.Add(-3 * time.Minute)}
	return &model.Incident{
		ID:        id,
		Resource:  ref,
		Title:     "payments-api: 5xx spike",
		Status:    status,
		Severity:  model.SeverityCritical,
		OpenedAt:  openedAt,
		UpdatedAt: openedAt,
		Timeline: []model.Signal{
			{ID: "s1", Kind: model.SignalChange, Source: "argocd", Resource: ref, Timestamp: openedAt, Severity: model.SeverityInfo, Title: "ArgoCD synced", Change: change},
		},
		Hypotheses: []model.Hypothesis{
			{Summary: "Deploy synced before incident", Confidence: 0.86, Category: "deploy", Change: change},
		},
	}
}

func TestPostgresIncidents(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := context.Background()

	cluster := uniqueID("cl")
	id := uniqueID("inc")
	opened := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Microsecond)
	t.Cleanup(func() { _, _ = pg.pool.Exec(ctx, "DELETE FROM incidents WHERE cluster = $1", cluster) })

	inc := sampleIncident(id, cluster, model.IncidentInvestigating, opened)
	if err := pg.SaveIncident(ctx, inc); err != nil {
		t.Fatalf("SaveIncident insert: %v", err)
	}

	got, err := pg.GetIncident(ctx, id)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if got.Title != inc.Title || got.Status != inc.Status || got.Severity != inc.Severity {
		t.Fatalf("GetIncident scalar mismatch: %+v", got)
	}
	if got.Resource != inc.Resource {
		t.Fatalf("resource mismatch: got %+v want %+v", got.Resource, inc.Resource)
	}
	if len(got.Timeline) != 1 || got.Timeline[0].Change == nil || got.Timeline[0].Change.Revision != "9f4c2a18b7" {
		t.Fatalf("timeline JSONB roundtrip failed: %+v", got.Timeline)
	}
	if len(got.Hypotheses) != 1 || got.Hypotheses[0].Confidence != 0.86 {
		t.Fatalf("hypotheses JSONB roundtrip failed: %+v", got.Hypotheses)
	}

	// Upsert: same id, changed status/title.
	inc.Status = model.IncidentResolved
	inc.Title = "payments-api: resolved"
	if err := pg.SaveIncident(ctx, inc); err != nil {
		t.Fatalf("SaveIncident update: %v", err)
	}
	got, err = pg.GetIncident(ctx, id)
	if err != nil {
		t.Fatalf("GetIncident after upsert: %v", err)
	}
	if got.Status != model.IncidentResolved || got.Title != "payments-api: resolved" {
		t.Fatalf("upsert did not update row: %+v", got)
	}

	// ErrNotFound.
	if _, err := pg.GetIncident(ctx, "does-not-exist-"+id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetIncident missing: want ErrNotFound, got %v", err)
	}
}

func TestPostgresListIncidents(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := context.Background()

	cluster := uniqueID("cl")
	other := uniqueID("other")
	now := time.Now().UTC().Truncate(time.Microsecond)
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(ctx, "DELETE FROM incidents WHERE cluster = ANY($1)", []string{cluster, other})
	})

	// Three in `cluster` at staggered times; one in `other`.
	older := sampleIncident(uniqueID("inc-old"), cluster, model.IncidentInvestigating, now.Add(-30*time.Minute))
	newer := sampleIncident(uniqueID("inc-new"), cluster, model.IncidentInvestigating, now.Add(-5*time.Minute))
	resolved := sampleIncident(uniqueID("inc-res"), cluster, model.IncidentResolved, now.Add(-20*time.Minute))
	elsewhere := sampleIncident(uniqueID("inc-oth"), other, model.IncidentInvestigating, now.Add(-1*time.Minute))
	for _, inc := range []*model.Incident{older, newer, resolved, elsewhere} {
		if err := pg.SaveIncident(ctx, inc); err != nil {
			t.Fatalf("SaveIncident: %v", err)
		}
	}

	// Cluster filter + ordering (most recent first).
	list, err := pg.ListIncidents(ctx, IncidentFilter{Cluster: cluster})
	if err != nil {
		t.Fatalf("ListIncidents cluster: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("cluster filter: want 3, got %d", len(list))
	}
	if !(list[0].OpenedAt.After(list[1].OpenedAt) && list[1].OpenedAt.After(list[2].OpenedAt)) {
		t.Fatalf("not ordered most-recent-first: %v %v %v", list[0].OpenedAt, list[1].OpenedAt, list[2].OpenedAt)
	}
	if list[0].ID != newer.ID {
		t.Fatalf("first should be newest: got %s want %s", list[0].ID, newer.ID)
	}

	// Status filter.
	res, err := pg.ListIncidents(ctx, IncidentFilter{Cluster: cluster, Status: model.IncidentResolved})
	if err != nil {
		t.Fatalf("ListIncidents status: %v", err)
	}
	if len(res) != 1 || res[0].ID != resolved.ID {
		t.Fatalf("status filter: want [%s], got %+v", resolved.ID, res)
	}

	// Limit.
	limited, err := pg.ListIncidents(ctx, IncidentFilter{Cluster: cluster, Limit: 2})
	if err != nil {
		t.Fatalf("ListIncidents limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limit: want 2, got %d", len(limited))
	}
}

func TestPostgresClusters(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := context.Background()

	a := uniqueID("zeta")
	b := uniqueID("alpha")
	t.Cleanup(func() { _, _ = pg.pool.Exec(ctx, "DELETE FROM clusters WHERE name = ANY($1)", []string{a, b}) })

	if err := pg.SaveCluster(ctx, Cluster{Name: a, Env: "prod", Region: "eu-west-1", Connected: true, AgentVersion: "dev"}); err != nil {
		t.Fatalf("SaveCluster a: %v", err)
	}
	if err := pg.SaveCluster(ctx, Cluster{Name: b, Env: "stg", Region: "eu-west-1", Connected: false}); err != nil {
		t.Fatalf("SaveCluster b: %v", err)
	}

	// Upsert update.
	if err := pg.SaveCluster(ctx, Cluster{Name: a, Env: "prod", Region: "us-east-1", Connected: false, AgentVersion: "v2"}); err != nil {
		t.Fatalf("SaveCluster upsert: %v", err)
	}

	list, err := pg.ListClusters(ctx)
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	// Find ours; verify ASC ordering between the two we inserted (b="alpha..." < a="zeta...").
	var idxA, idxB = -1, -1
	for i, c := range list {
		switch c.Name {
		case a:
			idxA = i
			if c.Region != "us-east-1" || c.AgentVersion != "v2" || c.Connected {
				t.Fatalf("cluster a not upserted: %+v", c)
			}
		case b:
			idxB = i
		}
	}
	if idxA == -1 || idxB == -1 {
		t.Fatalf("inserted clusters not found in list")
	}
	if idxB > idxA {
		t.Fatalf("clusters not name-ASC: %q at %d should precede %q at %d", b, idxB, a, idxA)
	}
}
