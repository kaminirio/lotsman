package kubernetes

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"lotsman/internal/model"
)

// WatchEvents must emit a real, incident-worthy event from the backing source
// (the minimal-viable poll-feed producer for LINK-1), tagged with the cluster and
// carrying the escalated severity.
func TestWatchEvents_EmitsNewEvent(t *testing.T) {
	now := time.Now()
	// OOMKilling is a Warning that escalates to SeverityError (criticalEventReasons).
	ev := makeEvent("prod", "e1", "OOMKilling", "container OOM", "Warning", "Pod", "api-0", now)
	cs := fake.NewSimpleClientset(&ev)
	c := newWithClient("prod", cs)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	feed := c.WatchEvents(ctx, 20*time.Millisecond)

	select {
	case sig, ok := <-feed:
		if !ok {
			t.Fatal("feed closed before emitting an event")
		}
		if sig.Title != "OOMKilling" {
			t.Fatalf("Title = %q, want OOMKilling", sig.Title)
		}
		if sig.Resource.Cluster != "prod" || sig.Resource.Name != "api-0" {
			t.Fatalf("unexpected resource %+v", sig.Resource)
		}
		if sig.Severity < model.SeverityError {
			t.Fatalf("Severity = %v, want >= Error", sig.Severity)
		}
	case <-ctx.Done():
		t.Fatal("WatchEvents never emitted an event")
	}
}

// Each Kubernetes event must be emitted at most once even though overlapping poll
// windows re-list it every tick.
func TestWatchEvents_DeduplicatesAcrossPolls(t *testing.T) {
	now := time.Now()
	ev := makeEvent("prod", "e1", "OOMKilling", "container OOM", "Warning", "Pod", "api-0", now)
	cs := fake.NewSimpleClientset(&ev)
	c := newWithClient("prod", cs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	feed := c.WatchEvents(ctx, 15*time.Millisecond)

	// First emission.
	select {
	case <-feed:
	case <-time.After(2 * time.Second):
		t.Fatal("no first emission")
	}

	// The same event must not be re-emitted across subsequent polls.
	select {
	case sig, ok := <-feed:
		if ok {
			t.Fatalf("duplicate emission of already-seen event: %+v", sig)
		}
	case <-time.After(120 * time.Millisecond):
		// No further emission across several poll cycles: correct.
	}
}

// Normal (Info-severity) events are noise and must never cross the wire.
func TestWatchEvents_SkipsNormalEvents(t *testing.T) {
	now := time.Now()
	ev := makeEvent("prod", "e1", "Scheduled", "assigned node", "Normal", "Pod", "api-0", now)
	cs := fake.NewSimpleClientset(&ev)
	c := newWithClient("prod", cs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	feed := c.WatchEvents(ctx, 15*time.Millisecond)

	select {
	case sig, ok := <-feed:
		if ok {
			t.Fatalf("Normal event should not be emitted: %+v", sig)
		}
	case <-time.After(120 * time.Millisecond):
		// Correct: nothing emitted.
	}
}

// The producer goroutine must stop and close its channel when ctx is cancelled
// (no leak across agent reconnects).
func TestWatchEvents_ClosesOnContextCancel(t *testing.T) {
	cs := fake.NewSimpleClientset()
	c := newWithClient("prod", cs)

	ctx, cancel := context.WithCancel(context.Background())
	feed := c.WatchEvents(ctx, 15*time.Millisecond)
	cancel()

	select {
	case _, ok := <-feed:
		if ok {
			// Drain any in-flight value, then expect close.
			select {
			case _, ok2 := <-feed:
				if ok2 {
					t.Fatal("feed kept emitting after cancel")
				}
			case <-time.After(time.Second):
				t.Fatal("feed did not close after cancel")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("feed did not close after cancel")
	}
}
