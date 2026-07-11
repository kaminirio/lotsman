package controlplane

import (
	"context"
	"testing"

	"lotsman/internal/store"
)

// A live agent connect must persist the cluster (connected) so it survives a
// control-plane restart and appears in the fleet list even after the agent drops
// — not just seed-inserted clusters (STORE-2).
func TestOnAgentConnectPersistsCluster(t *testing.T) {
	r := NewRegistry()
	st := store.NewMemory()
	r.store = st

	r.OnAgentConnect(&fakeLink{cluster: "agent-c"})

	got := clusterFromStore(t, st, "agent-c")
	if !got.Connected {
		t.Errorf("connected: got %v, want true", got.Connected)
	}

	// Disconnect persists the transition to not-connected, keeping the record
	// (history) rather than dropping it.
	link := &fakeLink{cluster: "agent-c"}
	r.OnAgentConnect(link)
	r.OnAgentDisconnect(link)
	got = clusterFromStore(t, st, "agent-c")
	if got.Connected {
		t.Errorf("after disconnect: connected=%v, want false", got.Connected)
	}
}

// A connect for a cluster already recorded (e.g. seeded with region/env) must
// preserve that metadata — the connect carries only name+connected.
func TestOnAgentConnectPreservesSeededMetadata(t *testing.T) {
	r := NewRegistry()
	st := store.NewMemory()
	if err := st.SaveCluster(context.Background(), store.Cluster{Name: "prod", Env: "prod", Region: "eu-west-1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r.store = st

	r.OnAgentConnect(&fakeLink{cluster: "prod"})

	got := clusterFromStore(t, st, "prod")
	if !got.Connected {
		t.Error("connect should mark prod connected")
	}
	if got.Env != "prod" || got.Region != "eu-west-1" {
		t.Fatalf("connect wiped seeded metadata: %+v", got)
	}
}

// A registry with no store configured must not panic on connect/disconnect (the
// persistence hook is optional; the read-time registry union is the fallback).
func TestOnAgentConnectNoStoreIsNoop(t *testing.T) {
	r := NewRegistry()
	link := &fakeLink{cluster: "agent-c"}
	r.OnAgentConnect(link)
	r.OnAgentDisconnect(link)
}

func clusterFromStore(t *testing.T, st *store.Memory, name string) store.Cluster {
	t.Helper()
	cs, err := st.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cluster %q not persisted", name)
	return store.Cluster{}
}
