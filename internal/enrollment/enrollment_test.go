package enrollment

import (
	"context"
	"strings"
	"testing"
	"time"

	"lotsman/internal/store"
)

func TestGenerateUniqueAndPrefixed(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		plaintext, hash, id, err := Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if !strings.HasPrefix(plaintext, "lse_") {
			t.Fatalf("plaintext %q missing lse_ prefix", plaintext)
		}
		if Hash(plaintext) != hash {
			t.Fatalf("returned hash does not match Hash(plaintext)")
		}
		if id == "" {
			t.Fatal("empty id")
		}
		if seen[plaintext] {
			t.Fatalf("duplicate plaintext token %q", plaintext)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[plaintext] = true
		seen[id] = true
	}
}

func TestHashStable(t *testing.T) {
	if Hash("lse_abc") != Hash("lse_abc") {
		t.Fatal("Hash is not deterministic")
	}
	if Hash("a") == Hash("b") {
		t.Fatal("distinct inputs hashed to the same value")
	}
}

// durableMem wraps the in-memory store but reports Durable()==true, so the
// validator's durable-store precondition is satisfied without a live Postgres.
type durableMem struct{ *store.Memory }

func (durableMem) Durable() bool { return true }

// newValidatorWithToken stores a token bound to cluster and returns the
// validator plus the plaintext to present.
func newValidatorWithToken(t *testing.T, cluster string, expiresAt time.Time, revoked bool) (*Validator, string) {
	t.Helper()
	st := store.NewMemory()
	plaintext, hash, id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	rec := store.EnrollmentToken{
		ID:        id,
		Cluster:   cluster,
		Hash:      hash,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
		Revoked:   revoked,
	}
	if err := st.SaveEnrollmentToken(context.Background(), rec); err != nil {
		t.Fatalf("SaveEnrollmentToken: %v", err)
	}
	return NewValidator(durableMem{st}), plaintext
}

// TestValidateRejectsEphemeralStore proves the Postgres-only requirement: even a
// correctly-saved token is rejected when the store is not durable (in-memory),
// because tokens would vanish on restart and silently lock out every agent.
func TestValidateRejectsEphemeralStore(t *testing.T) {
	st := store.NewMemory()
	plaintext, hash, id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := st.SaveEnrollmentToken(context.Background(), store.EnrollmentToken{
		ID: id, Cluster: "prod-eu", Hash: hash, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveEnrollmentToken: %v", err)
	}
	// NewValidator over the raw (non-durable) memory store.
	if err := NewValidator(st).ValidateEnrollment(context.Background(), "prod-eu", plaintext); err == nil {
		t.Fatal("expected rejection on ephemeral store, got nil")
	}
}

func TestValidateAcceptsMatching(t *testing.T) {
	v, token := newValidatorWithToken(t, "prod-eu", time.Time{}, false)
	if err := v.ValidateEnrollment(context.Background(), "prod-eu", token); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestValidateRejectsWrongCluster(t *testing.T) {
	v, token := newValidatorWithToken(t, "prod-eu", time.Time{}, false)
	if err := v.ValidateEnrollment(context.Background(), "staging", token); err == nil {
		t.Fatal("expected cluster-mismatch rejection, got nil")
	}
}

func TestValidateRejectsRevoked(t *testing.T) {
	v, token := newValidatorWithToken(t, "prod-eu", time.Time{}, true)
	if err := v.ValidateEnrollment(context.Background(), "prod-eu", token); err == nil {
		t.Fatal("expected revoked rejection, got nil")
	}
}

func TestValidateRejectsExpired(t *testing.T) {
	v, token := newValidatorWithToken(t, "prod-eu", time.Now().Add(-time.Hour), false)
	if err := v.ValidateEnrollment(context.Background(), "prod-eu", token); err == nil {
		t.Fatal("expected expired rejection, got nil")
	}
}

func TestValidateAcceptsUnexpired(t *testing.T) {
	v, token := newValidatorWithToken(t, "prod-eu", time.Now().Add(time.Hour), false)
	if err := v.ValidateEnrollment(context.Background(), "prod-eu", token); err != nil {
		t.Fatalf("expected accept of unexpired token, got %v", err)
	}
}

func TestValidateRejectsUnknownToken(t *testing.T) {
	v, _ := newValidatorWithToken(t, "prod-eu", time.Time{}, false)
	if err := v.ValidateEnrollment(context.Background(), "prod-eu", "lse_does-not-exist"); err == nil {
		t.Fatal("expected unknown-token rejection, got nil")
	}
}

func TestValidateRejectsEmptyToken(t *testing.T) {
	v, _ := newValidatorWithToken(t, "prod-eu", time.Time{}, false)
	if err := v.ValidateEnrollment(context.Background(), "prod-eu", ""); err == nil {
		t.Fatal("expected empty-token rejection, got nil")
	}
}
