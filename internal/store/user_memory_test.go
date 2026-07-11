package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func mkUser(id, username, email string, admin bool) User {
	return User{ID: id, Username: username, Email: email, IsAdmin: admin, Active: true, CreatedAt: time.Now()}
}

func TestMemoryUserCRUD(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	if err := m.CreateUser(ctx, mkUser("u1", "alice", "alice@example.com", true)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Duplicate username (case-insensitive) -> conflict.
	if err := m.CreateUser(ctx, mkUser("u2", "ALICE", "other@example.com", false)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username: want ErrConflict, got %v", err)
	}
	// Duplicate email (case-insensitive) -> conflict.
	if err := m.CreateUser(ctx, mkUser("u3", "bob", "Alice@Example.com", false)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate email: want ErrConflict, got %v", err)
	}

	got, err := m.GetUserByUsername(ctx, "Alice")
	if err != nil || got.ID != "u1" {
		t.Fatalf("get by username (case-insensitive): %v, %+v", err, got)
	}
	if _, err := m.GetUserByEmail(ctx, "ALICE@example.com"); err != nil {
		t.Fatalf("get by email (case-insensitive): %v", err)
	}
	if _, err := m.GetUserByID(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown id: want ErrNotFound, got %v", err)
	}
}

func TestMemoryUpdateUserPartial(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.CreateUser(ctx, mkUser("u1", "alice", "a@x.com", true))

	active := false
	updated, err := m.UpdateUser(ctx, "u1", UserPatch{Active: &active})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Active {
		t.Error("active should be false after patch")
	}
	if !updated.IsAdmin {
		t.Error("is_admin must be untouched by a partial patch")
	}

	if _, err := m.UpdateUser(ctx, "missing", UserPatch{Active: &active}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: want ErrNotFound, got %v", err)
	}
}

func TestMemoryCountActiveAdmins(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.CreateUser(ctx, mkUser("u1", "alice", "a@x.com", true))
	_ = m.CreateUser(ctx, mkUser("u2", "bob", "b@x.com", true))
	inactive := mkUser("u3", "carol", "c@x.com", true)
	inactive.Active = false
	_ = m.CreateUser(ctx, inactive)
	_ = m.CreateUser(ctx, mkUser("u4", "dave", "d@x.com", false))

	n, err := m.CountActiveAdmins(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("active admins: want 2, got %d", n)
	}
}

// TestMemoryLastAdminGuardAtomic asserts the store-level guard: a guarded
// demote/deactivate/delete of the last active admin is refused with ErrConflict
// inside the same critical section as the mutation, while the same operation on a
// non-last admin (or with the guard off) succeeds.
func TestMemoryLastAdminGuardAtomic(t *testing.T) {
	ctx := context.Background()
	demote := false

	t.Run("demote last admin refused", func(t *testing.T) {
		m := NewMemory()
		_ = m.CreateUser(ctx, mkUser("u1", "alice", "a@x.com", true))
		if _, err := m.UpdateUser(ctx, "u1", UserPatch{IsAdmin: &demote, GuardLastActiveAdmin: true}); !errors.Is(err, ErrConflict) {
			t.Fatalf("demote last admin: want ErrConflict, got %v", err)
		}
		if u, _ := m.GetUserByID(ctx, "u1"); !u.IsAdmin {
			t.Error("last admin must remain admin after refused demotion")
		}
	})

	t.Run("deactivate last admin refused", func(t *testing.T) {
		m := NewMemory()
		_ = m.CreateUser(ctx, mkUser("u1", "alice", "a@x.com", true))
		if _, err := m.UpdateUser(ctx, "u1", UserPatch{Active: &demote, GuardLastActiveAdmin: true}); !errors.Is(err, ErrConflict) {
			t.Fatalf("deactivate last admin: want ErrConflict, got %v", err)
		}
	})

	t.Run("delete last admin refused", func(t *testing.T) {
		m := NewMemory()
		_ = m.CreateUser(ctx, mkUser("u1", "alice", "a@x.com", true))
		if err := m.DeleteUser(ctx, "u1", true); !errors.Is(err, ErrConflict) {
			t.Fatalf("delete last admin: want ErrConflict, got %v", err)
		}
		if _, err := m.GetUserByID(ctx, "u1"); err != nil {
			t.Error("last admin must survive a refused delete")
		}
	})

	t.Run("non-last admin allowed", func(t *testing.T) {
		m := NewMemory()
		_ = m.CreateUser(ctx, mkUser("u1", "alice", "a@x.com", true))
		_ = m.CreateUser(ctx, mkUser("u2", "bob", "b@x.com", true))
		if _, err := m.UpdateUser(ctx, "u1", UserPatch{IsAdmin: &demote, GuardLastActiveAdmin: true}); err != nil {
			t.Fatalf("demote non-last admin: %v", err)
		}
		// Now u2 is the last admin: deleting it is refused.
		if err := m.DeleteUser(ctx, "u2", true); !errors.Is(err, ErrConflict) {
			t.Fatalf("delete new last admin: want ErrConflict, got %v", err)
		}
	})

	t.Run("guard off allows demotion", func(t *testing.T) {
		m := NewMemory()
		_ = m.CreateUser(ctx, mkUser("u1", "alice", "a@x.com", true))
		if _, err := m.UpdateUser(ctx, "u1", UserPatch{IsAdmin: &demote}); err != nil {
			t.Fatalf("unguarded demote: %v", err)
		}
	})
}

func TestMemoryDeleteUser(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.CreateUser(ctx, mkUser("u1", "alice", "a@x.com", true))
	if err := m.DeleteUser(ctx, "u1", false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := m.DeleteUser(ctx, "u1", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: want ErrNotFound, got %v", err)
	}
}
