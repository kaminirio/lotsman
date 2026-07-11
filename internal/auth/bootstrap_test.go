package auth

import (
	"context"
	"testing"

	"lotsman/internal/store"
)

func TestEnsureBootstrapAdminIdempotent(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()

	// First run creates the admin.
	if err := EnsureBootstrapAdmin(ctx, st, "root", "s3cret-pw", nil); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	u1, err := st.GetUserByUsername(ctx, "root")
	if err != nil {
		t.Fatalf("admin not created: %v", err)
	}
	if !u1.IsAdmin || !u1.Active || u1.PasswordHash == "" {
		t.Fatalf("bootstrap admin should be active admin with a password, got %+v", u1)
	}

	// Second run is a no-op (no duplicate, no error), and leaves the password.
	if err := EnsureBootstrapAdmin(ctx, st, "root", "different-pw", nil); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	u2, _ := st.GetUserByUsername(ctx, "root")
	if u2.ID != u1.ID {
		t.Errorf("bootstrap must not recreate the admin (id changed %q -> %q)", u1.ID, u2.ID)
	}
	if u2.PasswordHash != u1.PasswordHash {
		t.Error("existing admin password must be left untouched on re-seed")
	}
}

func TestEnsureBootstrapAdminPromotesExisting(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	_ = st.CreateUser(ctx, store.User{ID: "x", Username: "root", Email: "root@x", Active: false, IsAdmin: false})

	if err := EnsureBootstrapAdmin(ctx, st, "root", "pw", nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	u, _ := st.GetUserByUsername(ctx, "root")
	if !u.IsAdmin || !u.Active {
		t.Errorf("existing account must be promoted active+admin, got %+v", u)
	}
}

func TestEnsureBootstrapAdminNoop(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if err := EnsureBootstrapAdmin(ctx, st, "", "", nil); err != nil {
		t.Fatalf("blank creds should be a no-op: %v", err)
	}
	if _, err := st.GetUserByUsername(ctx, ""); err == nil {
		t.Error("no admin should have been created for blank credentials")
	}
}
