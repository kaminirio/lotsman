package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryEnrollmentTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	older := EnrollmentToken{ID: "id-old", Cluster: "prod", Hash: "hash-old", CreatedAt: time.Unix(1000, 0)}
	newer := EnrollmentToken{ID: "id-new", Cluster: "stg", Hash: "hash-new", CreatedAt: time.Unix(2000, 0)}
	if err := m.SaveEnrollmentToken(ctx, older); err != nil {
		t.Fatalf("save older: %v", err)
	}
	if err := m.SaveEnrollmentToken(ctx, newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}

	// GetByHash resolves the right record.
	got, err := m.GetEnrollmentTokenByHash(ctx, "hash-old")
	if err != nil {
		t.Fatalf("get by hash: %v", err)
	}
	if got.ID != "id-old" || got.Cluster != "prod" {
		t.Fatalf("get by hash returned wrong record: %+v", got)
	}

	// Unknown hash -> ErrNotFound.
	if _, err := m.GetEnrollmentTokenByHash(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown hash: got %v, want ErrNotFound", err)
	}

	// List is newest-first.
	list, err := m.ListEnrollmentTokens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != "id-new" || list[1].ID != "id-old" {
		t.Fatalf("list not newest-first: %+v", list)
	}

	// Revoke keeps the record and flips Revoked.
	if err := m.RevokeEnrollmentToken(ctx, "id-old"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, err = m.GetEnrollmentTokenByHash(ctx, "hash-old")
	if err != nil {
		t.Fatalf("get after revoke: %v", err)
	}
	if !got.Revoked {
		t.Fatal("token not marked revoked")
	}

	// Revoke unknown id -> ErrNotFound.
	if err := m.RevokeEnrollmentToken(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke unknown: got %v, want ErrNotFound", err)
	}
}
