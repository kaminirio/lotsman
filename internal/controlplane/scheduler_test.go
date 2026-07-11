package controlplane

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"lotsman/internal/engine/detector"
	"lotsman/internal/events"
	"lotsman/internal/model"
	"lotsman/internal/sources"
	"lotsman/internal/store"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeLister implements clusterLister.
type fakeLister struct{ names []string }

func (f fakeLister) Clusters() []string { return f.names }

// fakeScanner implements scanner: one incident per candidate, with a
// controllable UpdatedAt so dedupe behavior can be exercised.
type fakeScanner struct {
	candidates []detector.Candidate
	updatedAt  time.Time
}

func (f *fakeScanner) ScanAndInvestigate(_ context.Context, _ string, _ detector.Scope) ([]*model.Incident, error) {
	out := make([]*model.Incident, 0, len(f.candidates))
	for _, c := range f.candidates {
		out = append(out, &model.Incident{
			ID:        "inc-" + c.Resource.Name,
			Resource:  c.Resource,
			OpenedAt:  c.At,
			UpdatedAt: f.updatedAt,
		})
	}
	return out, nil
}

// Investigate builds one incident for the given resource, so the push path can be
// exercised without a real engine.
func (f *fakeScanner) Investigate(_ context.Context, ref model.ResourceRef, around time.Time, _ time.Duration) (*model.Incident, error) {
	return &model.Incident{
		ID:        "inc-" + ref.Name,
		Resource:  ref,
		OpenedAt:  around,
		UpdatedAt: f.updatedAt,
	}, nil
}

func TestSchedulerTickPublishesAndDedupes(t *testing.T) {
	ref := model.ResourceRef{Cluster: "c1", Namespace: "demo", Kind: "Deployment", Name: "checkout"}
	sc := &fakeScanner{
		candidates: []detector.Candidate{{Resource: ref, At: time.Now(), Severity: model.SeverityCritical}},
		updatedAt:  time.Unix(1000, 0),
	}
	bus := events.NewIncidentBus()
	ch, cancel := bus.Subscribe()
	defer cancel()

	s := NewScheduler(fakeLister{names: []string{"c1"}}, sc, store.NewMemory(), bus, time.Minute, testLogger())

	s.tick(context.Background())
	select {
	case inc := <-ch:
		if inc.ID != "inc-checkout" {
			t.Fatalf("unexpected incident %q", inc.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected an incident on the bus after first tick")
	}

	// Second tick with identical incident must NOT republish.
	s.tick(context.Background())
	select {
	case inc := <-ch:
		t.Fatalf("unexpected republish of unchanged incident %q", inc.ID)
	case <-time.After(100 * time.Millisecond):
	}

	// A changed UpdatedAt republishes.
	sc.updatedAt = time.Unix(2000, 0)
	s.tick(context.Background())
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected republish after UpdatedAt changed")
	}
}

func TestSchedulerRunStopsOnContextCancel(t *testing.T) {
	bus := events.NewIncidentBus()
	s := NewScheduler(fakeLister{}, &fakeScanner{}, store.NewMemory(), bus, 10*time.Millisecond, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestRegistryClustersUnion(t *testing.T) {
	r := NewRegistry()
	r.AddDirect(sources.NewProvider("a", nil, nil, nil, nil))
	r.AddDirect(sources.NewProvider("b", nil, nil, nil, nil))
	r.links["b"] = nil // overlapping name must dedupe
	r.links["c"] = nil

	got := r.Clusters()
	sort.Strings(got)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
