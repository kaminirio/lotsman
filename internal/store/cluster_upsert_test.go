package store

import (
	"context"
	"testing"
)

// SaveCluster is an upsert whose descriptive fields (env/region/agent_version)
// are only overwritten by non-empty incoming values. This guards the STORE-2
// path: a live agent connect carries just name+connected and must not wipe the
// env/region/version recorded by seed, while it must still flip connected.
func TestMemorySaveClusterPreservesMetadataOnConnect(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	// Seeded/full record.
	if err := m.SaveCluster(ctx, Cluster{Name: "prod", Env: "prod", Region: "eu-west-1", Connected: false, AgentVersion: "v1"}); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	// Agent connect: name+connected only.
	if err := m.SaveCluster(ctx, Cluster{Name: "prod", Connected: true}); err != nil {
		t.Fatalf("connect save: %v", err)
	}

	got := clusterByName(t, m, "prod")
	if !got.Connected {
		t.Error("connect should set Connected=true")
	}
	if got.Env != "prod" || got.Region != "eu-west-1" || got.AgentVersion != "v1" {
		t.Fatalf("connect wiped metadata: %+v", got)
	}

	// Disconnect: connected flips false, metadata still preserved (history).
	if err := m.SaveCluster(ctx, Cluster{Name: "prod", Connected: false}); err != nil {
		t.Fatalf("disconnect save: %v", err)
	}
	got = clusterByName(t, m, "prod")
	if got.Connected {
		t.Error("disconnect should set Connected=false")
	}
	if got.Env != "prod" || got.Region != "eu-west-1" {
		t.Fatalf("disconnect wiped metadata: %+v", got)
	}

	// A later save WITH metadata still overwrites (non-empty wins).
	if err := m.SaveCluster(ctx, Cluster{Name: "prod", Region: "us-east-1", AgentVersion: "v2", Connected: true}); err != nil {
		t.Fatalf("update save: %v", err)
	}
	got = clusterByName(t, m, "prod")
	if got.Region != "us-east-1" || got.AgentVersion != "v2" {
		t.Fatalf("non-empty update did not overwrite: %+v", got)
	}
}

func clusterByName(t *testing.T, m *Memory, name string) Cluster {
	t.Helper()
	cs, err := m.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cluster %q not found", name)
	return Cluster{}
}
