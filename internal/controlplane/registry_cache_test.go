package controlplane

import (
	"context"
	"testing"

	"lotsman/internal/agentlink"
	"lotsman/internal/sources"
)

// fakeLink is a no-op agentlink.Link used to exercise remote-provider memoization.
type fakeLink struct{ cluster string }

func (l *fakeLink) Cluster() string { return l.cluster }
func (l *fakeLink) Do(context.Context, agentlink.Request) (agentlink.Response, error) {
	return agentlink.Response{}, nil
}
func (l *fakeLink) Events() <-chan agentlink.Event { return nil }
func (l *fakeLink) Close() error                   { return nil }

// Repeated Provider calls for an agent-served cluster must return the SAME
// remote wrapper (memoized), but a reconnect that swaps the link must rebuild it.
func TestProviderMemoizesRemoteWrapper(t *testing.T) {
	r := NewRegistry()
	link := &fakeLink{cluster: "agent-c"}
	r.OnAgentConnect(link)

	p1, err := r.Provider("agent-c")
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	p2, err := r.Provider("agent-c")
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	if p1 != p2 {
		t.Fatal("expected memoized remote provider to be reused across calls")
	}

	// A reconnect with a new link must invalidate the cached wrapper.
	link2 := &fakeLink{cluster: "agent-c"}
	r.OnAgentConnect(link2)
	p3, err := r.Provider("agent-c")
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	if p3 == p1 {
		t.Fatal("expected a fresh remote provider after the link was replaced")
	}

	// Disconnecting the current link makes the cluster unreachable again.
	r.OnAgentDisconnect(link2)
	if _, err := r.Provider("agent-c"); err != agentlink.ErrNotConnected {
		t.Fatalf("after disconnect err = %v, want ErrNotConnected", err)
	}
}

// Direct providers bypass the remote cache entirely and are returned verbatim.
func TestProviderDirectUnaffectedByCache(t *testing.T) {
	r := NewRegistry()
	direct := sources.NewProvider("direct-c", nil, nil, nil, nil)
	r.AddDirect(direct)

	p1, err := r.Provider("direct-c")
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	if p1 != direct {
		t.Fatal("direct provider should be returned as-is")
	}
}
