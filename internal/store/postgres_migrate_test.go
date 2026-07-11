package store

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// loadMigrations must return the embedded files in lexical (version) order and
// include the initial migration. This exercises the version-load path without a
// live database (the integration tests are gated on LOTSMAN_TEST_DATABASE_URL).
func TestLoadMigrationsOrdered(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected at least one embedded migration")
	}
	if all[0].version != "0001_init.sql" {
		t.Errorf("first migration: got %q, want 0001_init.sql", all[0].version)
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].version >= all[i].version {
			t.Fatalf("migrations not strictly lexically ordered: %q then %q", all[i-1].version, all[i].version)
		}
		if all[i].sql == "" {
			t.Errorf("migration %q has empty body", all[i].version)
		}
	}
}

// pendingMigrations must skip already-applied versions while preserving order,
// and return nothing when everything is applied. This is the crash-safe
// "apply only what's new" selection the migrator relies on.
func TestPendingMigrations(t *testing.T) {
	all := []migration{
		{version: "0001_init.sql", sql: "a"},
		{version: "0002_add_col.sql", sql: "b"},
		{version: "0003_backfill.sql", sql: "c"},
	}

	// 0001 already applied -> only 0002, 0003 pending, in order.
	pending := pendingMigrations(all, map[string]bool{"0001_init.sql": true})
	if len(pending) != 2 || pending[0].version != "0002_add_col.sql" || pending[1].version != "0003_backfill.sql" {
		t.Fatalf("pending after 0001: got %+v", pending)
	}

	// Nothing applied -> everything pending, order preserved.
	if got := pendingMigrations(all, map[string]bool{}); len(got) != 3 {
		t.Fatalf("pending with none applied: want 3, got %d", len(got))
	}

	// Everything applied -> nothing pending.
	allApplied := map[string]bool{"0001_init.sql": true, "0002_add_col.sql": true, "0003_backfill.sql": true}
	if got := pendingMigrations(all, allApplied); len(got) != 0 {
		t.Fatalf("pending with all applied: want 0, got %d", len(got))
	}
}

// TestMigrateGuardsSQL asserts, without a live database, that migrate() emits the
// two cross-process guards the concurrency fix relies on: a session-scoped
// advisory lock (so only one replica migrates at a time) with a non-zero constant
// key, and an idempotent ON CONFLICT DO NOTHING bookkeeping insert (so a raced
// insert is a no-op, not a PK violation that crash-loops the losing replica).
func TestMigrateGuardsSQL(t *testing.T) {
	if !strings.Contains(advisoryLockSQL, "pg_advisory_lock") {
		t.Errorf("advisoryLockSQL missing pg_advisory_lock: %q", advisoryLockSQL)
	}
	if !strings.Contains(advisoryUnlockSQL, "pg_advisory_unlock") {
		t.Errorf("advisoryUnlockSQL missing pg_advisory_unlock: %q", advisoryUnlockSQL)
	}
	if migrateAdvisoryLockKey == 0 {
		t.Error("migrateAdvisoryLockKey must be a fixed non-zero key")
	}
	if !strings.Contains(insertMigrationSQL, "ON CONFLICT (version) DO NOTHING") {
		t.Errorf("insertMigrationSQL missing ON CONFLICT DO NOTHING: %q", insertMigrationSQL)
	}
}

// TestMigrateConcurrentNoDuplicateRows runs migrate() concurrently against one
// database and asserts no error and exactly one schema_migrations row per version
// — the two-replica boot race that previously crash-looped the losing replica on
// a PK collision. DB-gated: skips when LOTSMAN_TEST_DATABASE_URL is unset.
func TestMigrateConcurrentNoDuplicateRows(t *testing.T) {
	pg := newTestPostgres(t) // skips without a live DB
	ctx := context.Background()

	// Reset the bookkeeping so every migrate() below has real work to race. The
	// only migration (0001_init) is CREATE ... IF NOT EXISTS, so re-applying it
	// against the live schema is safe.
	if _, err := pg.pool.Exec(ctx, `TRUNCATE schema_migrations`); err != nil {
		t.Fatalf("reset schema_migrations: %v", err)
	}

	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- pg.migrate(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate returned error: %v", err)
		}
	}

	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	rows, err := pg.pool.Query(ctx, `SELECT version, count(*) FROM schema_migrations GROUP BY version`)
	if err != nil {
		t.Fatalf("query counts: %v", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var v string
		var c int
		if err := rows.Scan(&v, &c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		counts[v] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(counts) != len(all) {
		t.Fatalf("want %d distinct versions recorded, got %d (%v)", len(all), len(counts), counts)
	}
	for _, m := range all {
		if counts[m.version] != 1 {
			t.Errorf("version %s recorded %d times, want exactly 1", m.version, counts[m.version])
		}
	}
}
