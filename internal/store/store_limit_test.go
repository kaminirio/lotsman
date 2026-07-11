package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"lotsman/internal/model"
)

// TestListIncidentsDefaultCap pins STORE-3: an unset Limit (0) must not return an
// unbounded result set — the store caps it at DefaultIncidentListLimit.
func TestListIncidentsDefaultCap(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	total := DefaultIncidentListLimit + 25
	for i := 0; i < total; i++ {
		inc := &model.Incident{
			ID:       fmt.Sprintf("inc-%04d", i),
			OpenedAt: base.Add(time.Duration(i) * time.Minute),
			Resource: model.ResourceRef{Cluster: "c"},
		}
		if err := m.SaveIncident(ctx, inc); err != nil {
			t.Fatal(err)
		}
	}

	got, err := m.ListIncidents(ctx, IncidentFilter{}) // Limit unset
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != DefaultIncidentListLimit {
		t.Fatalf("unset Limit must cap at %d, got %d", DefaultIncidentListLimit, len(got))
	}
	// The cap keeps the most-recent incidents (newest-first ordering).
	if got[0].ID != fmt.Sprintf("inc-%04d", total-1) {
		t.Fatalf("expected newest incident first, got %s", got[0].ID)
	}
}

// TestListIncidentsExplicitLimitHonored proves an explicit, smaller limit is
// still respected (the cap is only a backstop for the unset case).
func TestListIncidentsExplicitLimitHonored(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := m.SaveIncident(ctx, &model.Incident{ID: fmt.Sprintf("i%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.ListIncidents(ctx, IncidentFilter{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("explicit Limit=3 must be honored, got %d", len(got))
	}
}

// TestEffectiveLimit unit-checks the shared limit resolution used by both stores.
func TestEffectiveLimit(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, DefaultIncidentListLimit},
		{-5, DefaultIncidentListLimit},
		{10, 10},
		{DefaultIncidentListLimit + 1, DefaultIncidentListLimit + 1},
	}
	for _, c := range cases {
		if got := (IncidentFilter{Limit: c.in}).effectiveLimit(); got != c.want {
			t.Errorf("effectiveLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
