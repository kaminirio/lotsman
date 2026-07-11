package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// pgUser returns a User row scoped to this test run (uniqueID keeps reruns
// against a persistent database collision-free), matching mkUser's shape in
// user_memory_test.go.
func pgUser(id, username, email string, admin bool) User {
	return User{ID: id, Username: username, Email: email, IsAdmin: admin, Active: true, CreatedAt: time.Now()}
}

// TestPostgresUserCRUD exercises the pgx-backed User store end to end against a
// real database: the 0003_users.sql migration (table + case-insensitive unique
// indexes), CreateUser/duplicate conflicts, the three getters, ListUsers,
// UpdateUser's partial COALESCE patch, CountActiveAdmins, and DeleteUser. Gated
// by LOTSMAN_TEST_DATABASE_URL like the rest of this file's Postgres tests.
func TestPostgresUserCRUD(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := context.Background()

	id1, id2, id3 := uniqueID("u1"), uniqueID("u2"), uniqueID("u3")
	uname := uniqueID("alice")
	email := uname + "@example.com"

	if err := pg.CreateUser(ctx, pgUser(id1, uname, email, true)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Duplicate username (case-insensitive) -> ErrConflict, via the unique index.
	if err := pg.CreateUser(ctx, pgUser(id2, strings.ToUpper(uname), uniqueID("other")+"@example.com", false)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username: want ErrConflict, got %v", err)
	}
	// Duplicate email (case-insensitive) -> ErrConflict.
	if err := pg.CreateUser(ctx, pgUser(id3, uniqueID("bob"), strings.ToUpper(email), false)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate email: want ErrConflict, got %v", err)
	}

	// Getters, case-insensitive.
	got, err := pg.GetUserByUsername(ctx, strings.ToUpper(uname))
	if err != nil || got.ID != id1 {
		t.Fatalf("get by username (case-insensitive): err=%v got=%+v", err, got)
	}
	if _, err := pg.GetUserByEmail(ctx, strings.ToUpper(email)); err != nil {
		t.Fatalf("get by email (case-insensitive): %v", err)
	}
	if _, err := pg.GetUserByID(ctx, id1); err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if _, err := pg.GetUserByID(ctx, "nonexistent-"+id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown id: want ErrNotFound, got %v", err)
	}

	// ListUsers includes the row we just created.
	all, err := pg.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, u := range all {
		if u.ID == id1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListUsers did not include %s", id1)
	}

	// CountActiveAdmins reflects the seeded admin.
	n, err := pg.CountActiveAdmins(ctx)
	if err != nil {
		t.Fatalf("count active admins: %v", err)
	}
	if n < 1 {
		t.Fatalf("count active admins: want >=1, got %d", n)
	}

	// UpdateUser: partial patch only touches the given fields.
	active := false
	updated, err := pg.UpdateUser(ctx, id1, UserPatch{Active: &active})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Active {
		t.Error("active should be false after patch")
	}
	if !updated.IsAdmin {
		t.Error("is_admin must be untouched by a partial patch")
	}
	if !updated.UpdatedAt.After(got.UpdatedAt) && !updated.UpdatedAt.Equal(got.UpdatedAt) {
		t.Errorf("updated_at should not go backwards: before=%v after=%v", got.UpdatedAt, updated.UpdatedAt)
	}

	// UpdateUser on an unknown id -> ErrNotFound.
	if _, err := pg.UpdateUser(ctx, "nonexistent-"+id1, UserPatch{Active: &active}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: want ErrNotFound, got %v", err)
	}

	// DeleteUser, then delete again -> ErrNotFound.
	if err := pg.DeleteUser(ctx, id1, false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := pg.DeleteUser(ctx, id1, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: want ErrNotFound, got %v", err)
	}
}

// TestPostgresLastAdminGuardAtomic verifies the SQL-level guard: a guarded
// demote/delete of the last active admin refuses with ErrConflict (the WHERE
// clause matches no row), while GetUserBySSO resolves a linked account. Gated by
// LOTSMAN_TEST_DATABASE_URL. The guard assertions run only when the created admin
// is observed to be the sole active admin, so the test is robust whether or not
// the test database already holds other admin rows.
func TestPostgresLastAdminGuardAtomic(t *testing.T) {
	pg := newTestPostgres(t)
	ctx := context.Background()
	demote := false

	adminID := uniqueID("guard-admin")
	uname := uniqueID("guard-alice")
	admin := pgUser(adminID, uname, uname+"@example.com", true)
	admin.SSOProvider = "github"
	admin.SSOSubject = uniqueID("sub")
	if err := pg.CreateUser(ctx, admin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	t.Cleanup(func() { _ = pg.DeleteUser(ctx, adminID, false) })

	// GetUserBySSO resolves the linked account; empty args -> ErrNotFound.
	if got, err := pg.GetUserBySSO(ctx, "github", admin.SSOSubject); err != nil || got.ID != adminID {
		t.Fatalf("get by sso: err=%v got=%+v", err, got)
	}
	if _, err := pg.GetUserBySSO(ctx, "github", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get by sso empty subject: want ErrNotFound, got %v", err)
	}

	n, err := pg.CountActiveAdmins(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n == 1 {
		// Our admin is the only active admin: the guard must refuse.
		if _, err := pg.UpdateUser(ctx, adminID, UserPatch{IsAdmin: &demote, GuardLastActiveAdmin: true}); !errors.Is(err, ErrConflict) {
			t.Fatalf("guarded demote of last admin: want ErrConflict, got %v", err)
		}
		if err := pg.DeleteUser(ctx, adminID, true); !errors.Is(err, ErrConflict) {
			t.Fatalf("guarded delete of last admin: want ErrConflict, got %v", err)
		}
		if u, err := pg.GetUserByID(ctx, adminID); err != nil || !u.IsAdmin || !u.Active {
			t.Fatalf("last admin must be unchanged after refused guarded ops: err=%v got=%+v", err, u)
		}
	}

	// With the guard OFF, the same demotion always succeeds.
	if _, err := pg.UpdateUser(ctx, adminID, UserPatch{IsAdmin: &demote}); err != nil {
		t.Fatalf("unguarded demote: %v", err)
	}
}
