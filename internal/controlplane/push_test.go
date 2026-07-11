package controlplane

import (
	"context"
	"testing"
	"time"

	"lotsman/internal/agentlink"
	"lotsman/internal/config"
	"lotsman/internal/events"
	"lotsman/internal/model"
	"lotsman/internal/store"
)

// pushLink is an agentlink.Link whose Events() channel the test drives, so the
// registry drain + scheduler push consumer can be exercised end to end without
// a real gRPC stream.
type pushLink struct {
	cluster string
	events  chan agentlink.Event
}

func newPushLink(cluster string) *pushLink {
	return &pushLink{cluster: cluster, events: make(chan agentlink.Event, 8)}
}

func (l *pushLink) Cluster() string { return l.cluster }
func (l *pushLink) Do(context.Context, agentlink.Request) (agentlink.Response, error) {
	return agentlink.Response{}, nil
}
func (l *pushLink) Events() <-chan agentlink.Event { return l.events }
func (l *pushLink) Close() error                   { close(l.events); return nil }

// waitWG waits on the registry's drain WaitGroup with a timeout, returning true
// if all drain goroutines exited.
func waitDrains(r *Registry, d time.Duration) bool {
	done := make(chan struct{})
	go func() { r.drainWG.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// A signal an agent pushes must traverse the registry drain and the scheduler's
// push consumer, promoting the affected resource into a published incident —
// without waiting for a poll tick.
func TestPushedSignalPromotedToIncident(t *testing.T) {
	r := NewRegistry()
	link := newPushLink("c1")
	r.OnAgentConnect(link)

	sc := &fakeScanner{updatedAt: time.Unix(1000, 0)}
	bus := events.NewIncidentBus()
	ch, cancelSub := bus.Subscribe()
	defer cancelSub()

	s := NewScheduler(r, sc, store.NewMemory(), bus, time.Minute, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.consumePushedSignals(ctx, r.PushedEvents())

	ref := model.ResourceRef{Cluster: "c1", Namespace: "demo", Kind: "Pod", Name: "checkout"}
	link.events <- agentlink.Event{
		Cluster: "c1",
		Signal:  model.Signal{Kind: model.SignalK8sEvent, Resource: ref, Severity: model.SeverityError, Title: "OOMKilling"},
	}

	select {
	case inc := <-ch:
		if inc.ID != "inc-checkout" {
			t.Fatalf("unexpected incident %q", inc.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pushed signal never produced an incident on the bus")
	}
}

// Sub-Error signals (Info/Warning) must not open an incident via the push path,
// matching the periodic KubernetesDetector's SeverityError threshold.
func TestPushedSignalBelowThresholdIgnored(t *testing.T) {
	r := NewRegistry()
	link := newPushLink("c1")
	r.OnAgentConnect(link)

	sc := &fakeScanner{updatedAt: time.Unix(1000, 0)}
	bus := events.NewIncidentBus()
	ch, cancelSub := bus.Subscribe()
	defer cancelSub()

	s := NewScheduler(r, sc, store.NewMemory(), bus, time.Minute, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.consumePushedSignals(ctx, r.PushedEvents())

	ref := model.ResourceRef{Cluster: "c1", Namespace: "demo", Kind: "Pod", Name: "noisy"}
	link.events <- agentlink.Event{
		Cluster: "c1",
		Signal:  model.Signal{Kind: model.SignalK8sEvent, Resource: ref, Severity: model.SeverityWarning, Title: "Unhealthy"},
	}

	select {
	case inc := <-ch:
		t.Fatalf("warning-severity push should not open an incident, got %q", inc.ID)
	case <-time.After(200 * time.Millisecond):
		// Correct: nothing published.
	}
}

// The per-agent drain goroutine must exit when the link disconnects (its Events()
// channel closes), leaving no leaked goroutine.
func TestDrainStopsOnDisconnect(t *testing.T) {
	r := NewRegistry()
	link := newPushLink("c1")
	r.OnAgentConnect(link)

	// Simulate a disconnect: the gateway closes the link, closing Events().
	_ = link.Close()

	if !waitDrains(r, 2*time.Second) {
		t.Fatal("drain goroutine did not exit after link disconnect")
	}
}

// The per-agent drain goroutine must also exit when the registry's drain context
// is cancelled, even if the link never closes.
func TestDrainStopsOnContextCancel(t *testing.T) {
	r := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	r.drainCtx = ctx // bound the drain to a cancelable context (prod default is Background)

	link := newPushLink("c1") // never closed
	r.OnAgentConnect(link)

	cancel()

	if !waitDrains(r, 2*time.Second) {
		t.Fatal("drain goroutine did not exit after drain context cancel")
	}
}

// TestDrainBoundToLifecycleCtxInProd covers the PROD WIRING: controlplane.New
// must set registry.drainCtx to the lifecycle ctx (not the inert Background
// default) so per-agent drains are bounded by shutdown even if a link never
// closes. Cancelling the ctx passed to New must therefore stop the drains.
func TestDrainBoundToLifecycleCtxInProd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Server{Addr: ":0", GatewayAddr: ":0"}
	cp, err := New(ctx, cfg, testLogger())
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	link := newPushLink("c1") // never closed — only the ctx backstop can stop the drain
	cp.registry.OnAgentConnect(link)

	cancel() // simulate SIGINT/SIGTERM cancelling the lifecycle ctx

	if !waitDrains(cp.registry, 2*time.Second) {
		t.Fatal("prod drain goroutine did not exit after lifecycle ctx cancel; drainCtx not wired in New")
	}
}
