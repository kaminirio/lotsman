package agentlink

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"lotsman/internal/agentlink/pb"
	"lotsman/internal/model"
)

// e2eHarness wires a Gateway (as a registered AgentServiceServer) and a Dialer
// over an in-memory bufconn, returning the control-plane Link the registry would
// receive once the agent's Hello lands.
type e2eHarness struct {
	link    Link
	srv     *grpc.Server
	cleanup func()
}

func newE2EHarness(t *testing.T, handler Handler, feed func(context.Context) <-chan Event) *e2eHarness {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	linkCh := make(chan Link, 1)
	gw := NewGateway("bufconn", "", logger, func(l Link) { linkCh <- l }, nil)
	// The e2e harness exercises the accept-any-non-empty-token path, which is now
	// gated behind the explicit insecure opt-in (SEC-1). See gateway_test.go for
	// the fail-closed default and token-match paths.
	gw.allowInsecure = true

	srv := grpc.NewServer()
	pb.RegisterAgentServiceServer(srv, gw)
	go func() { _ = srv.Serve(lis) }()

	ctx, cancel := context.WithCancel(context.Background())
	dialer := NewDialer("passthrough:///bufconn", "dev-token", logger).
		WithIdentity("test-cluster", "v-test", []string{"loki"})
	dialer.dialOpts = []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if feed != nil {
		dialer.WithEventFeed(feed)
	}

	go func() { _ = dialer.Run(ctx, handler) }()

	var link Link
	select {
	case link = <-linkCh:
	case <-time.After(3 * time.Second):
		cancel()
		srv.Stop()
		t.Fatal("agent never connected (no link delivered)")
	}

	return &e2eHarness{
		link: link,
		srv:  srv,
		cleanup: func() {
			cancel()
			srv.Stop()
			_ = lis.Close()
		},
	}
}

func TestE2E_DoRoundTrip(t *testing.T) {
	// Fake handler echoes the kind + payload back so we can assert the full
	// round-trip preserves both.
	handler := func(_ context.Context, req Request) Response {
		out, _ := json.Marshal(map[string]string{
			"kind":    string(req.Kind),
			"payload": string(req.Payload),
		})
		return Response{Payload: out}
	}
	h := newE2EHarness(t, handler, nil)
	defer h.cleanup()

	if got := h.link.Cluster(); got != "test-cluster" {
		t.Fatalf("Cluster() = %q, want test-cluster", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := h.link.Do(ctx, Request{Kind: ReqQueryLogs, Payload: []byte(`hello`)})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Err != "" {
		t.Fatalf("unexpected Response.Err: %q", resp.Err)
	}
	var got map[string]string
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["kind"] != string(ReqQueryLogs) || got["payload"] != "hello" {
		t.Fatalf("round-trip mismatch: %v", got)
	}
}

func TestE2E_HandlerError(t *testing.T) {
	handler := func(_ context.Context, _ Request) Response {
		return Response{Err: "boom from agent"}
	}
	h := newE2EHarness(t, handler, nil)
	defer h.cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := h.link.Do(ctx, Request{Kind: ReqListWorkloads, Payload: []byte(`"ns"`)})
	if err != nil {
		t.Fatalf("Do transport error: %v", err)
	}
	if resp.Err != "boom from agent" {
		t.Fatalf("Response.Err = %q, want %q", resp.Err, "boom from agent")
	}
}

func TestE2E_EventPush(t *testing.T) {
	feedCh := make(chan Event, 1)
	feed := func(ctx context.Context) <-chan Event {
		out := make(chan Event)
		go func() {
			defer close(out)
			select {
			case ev := <-feedCh:
				select {
				case out <- ev:
				case <-ctx.Done():
				}
			case <-ctx.Done():
			}
		}()
		return out
	}
	handler := func(_ context.Context, _ Request) Response { return Response{} }
	h := newE2EHarness(t, handler, feed)
	defer h.cleanup()

	want := Event{
		Cluster: "test-cluster",
		Signal: model.Signal{
			ID:    "sig-1",
			Kind:  model.SignalChange,
			Title: "argocd sync",
		},
	}
	feedCh <- want

	select {
	case got := <-h.link.Events():
		if got.Cluster != want.Cluster || got.Signal.ID != want.Signal.ID || got.Signal.Kind != want.Signal.Kind {
			t.Fatalf("event mismatch: got %+v want %+v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event never arrived on Link.Events()")
	}
}

// TestE2E_EventPushK8sSignalFidelity mirrors the real LINK-1 path: the agent-side
// feed emits a Kubernetes-event Signal, which must traverse dialer -> gateway ->
// Link.Events() intact — severity and resource included — so the control-plane
// consumer that drains Link.Events() has what it needs to gate and investigate.
func TestE2E_EventPushK8sSignalFidelity(t *testing.T) {
	feedCh := make(chan Event, 1)
	feed := func(ctx context.Context) <-chan Event {
		out := make(chan Event)
		go func() {
			defer close(out)
			select {
			case ev := <-feedCh:
				select {
				case out <- ev:
				case <-ctx.Done():
				}
			case <-ctx.Done():
			}
		}()
		return out
	}
	handler := func(_ context.Context, _ Request) Response { return Response{} }
	h := newE2EHarness(t, handler, feed)
	defer h.cleanup()

	want := Event{
		Cluster: "test-cluster",
		Signal: model.Signal{
			Kind:     model.SignalK8sEvent,
			Severity: model.SeverityError,
			Source:   "kubernetes",
			Title:    "OOMKilling",
			Resource: model.ResourceRef{Cluster: "test-cluster", Namespace: "prod", Kind: "Pod", Name: "api-0"},
		},
	}
	feedCh <- want

	select {
	case got := <-h.link.Events():
		if got.Cluster != want.Cluster {
			t.Fatalf("cluster = %q, want %q", got.Cluster, want.Cluster)
		}
		if got.Signal.Kind != model.SignalK8sEvent || got.Signal.Severity != model.SeverityError {
			t.Fatalf("signal kind/severity not preserved: %+v", got.Signal)
		}
		if got.Signal.Resource != want.Signal.Resource {
			t.Fatalf("resource not preserved: got %+v want %+v", got.Signal.Resource, want.Signal.Resource)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("k8s event signal never arrived on Link.Events()")
	}
}

func TestE2E_ConcurrentCorrelation(t *testing.T) {
	// Handler reflects the payload it received; if request_id correlation is
	// broken, concurrent calls would observe each other's replies. A small
	// per-request delay maximizes interleaving.
	handler := func(_ context.Context, req Request) Response {
		time.Sleep(5 * time.Millisecond)
		return Response{Payload: req.Payload}
	}
	h := newE2EHarness(t, handler, nil)
	defer h.cleanup()

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			payload := []byte(fmt.Sprintf("req-%d", i))
			resp, err := h.link.Do(ctx, Request{Kind: ReqQueryLogs, Payload: payload})
			if err != nil {
				errs <- fmt.Errorf("req %d: %w", i, err)
				return
			}
			if string(resp.Payload) != string(payload) {
				errs <- fmt.Errorf("req %d: got %q want %q (correlation crossed)", i, resp.Payload, payload)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
