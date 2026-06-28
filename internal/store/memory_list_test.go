package store

import (
	"context"
	"testing"
	"time"

	"lotsman/internal/model"
)

func mkInc(id string, opened time.Time) *model.Incident {
	return &model.Incident{
		ID:       id,
		OpenedAt: opened,
		Resource: model.ResourceRef{Cluster: "c"},
	}
}

// ListIncidents with Limit==1 takes the O(N) max-scan fast path; it must still
// return the single most-recently-opened incident.
func TestListIncidentsLimitOneReturnsNewest(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	base := time.Unix(1_000_000, 0)
	_ = m.SaveIncident(ctx, mkInc("old", base))
	_ = m.SaveIncident(ctx, mkInc("newest", base.Add(2*time.Hour)))
	_ = m.SaveIncident(ctx, mkInc("mid", base.Add(time.Hour)))

	got, err := m.ListIncidents(ctx, IncidentFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(got))
	}
	if got[0].ID != "newest" {
		t.Fatalf("expected newest, got %q", got[0].ID)
	}
}

// Limit==1 on an empty (or fully filtered-out) set returns no incidents.
func TestListIncidentsLimitOneEmpty(t *testing.T) {
	m := NewMemory()
	got, err := m.ListIncidents(context.Background(), IncidentFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no incidents, got %d", len(got))
	}
}

// The normal (no/limit>1) path preserves OpenedAt-descending order.
func TestListIncidentsDescendingOrder(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	base := time.Unix(1_000_000, 0)
	_ = m.SaveIncident(ctx, mkInc("a", base))
	_ = m.SaveIncident(ctx, mkInc("c", base.Add(2*time.Hour)))
	_ = m.SaveIncident(ctx, mkInc("b", base.Add(time.Hour)))

	got, err := m.ListIncidents(ctx, IncidentFilter{})
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	want := []string{"c", "b", "a"}
	if len(got) != len(want) {
		t.Fatalf("got %d incidents, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("position %d = %q, want %q", i, got[i].ID, want[i])
		}
	}
}

// GetIncident returns a copy: mutating the result must not corrupt the store.
func TestGetIncidentReturnsCopy(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	_ = m.SaveIncident(ctx, mkInc("x", time.Unix(1, 0)))

	got, err := m.GetIncident(ctx, "x")
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	got.Title = "mutated"

	again, err := m.GetIncident(ctx, "x")
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if again.Title == "mutated" {
		t.Fatal("caller mutation leaked into the stored incident")
	}
}
