package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lotsman/internal/model"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Postgres is the production Store: control-plane state in PostgreSQL via pgx
// (ADR-0005). Only derived state is persisted; logs/metrics are
// queried live through agents (ADR-0004). All queries are hand-written and
// context-propagated; no ORM.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres opens a connection pool to dsn, verifies connectivity, and applies
// any pending schema migrations. Caller must Close the returned store.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping postgres: %w", err)
	}
	p := &Postgres{pool: pool}
	if err := p.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

// migration is one embedded SQL migration, identified by its file name (which
// sorts lexically into apply order).
type migration struct {
	version string
	sql     string
}

// loadMigrations reads and lexically orders the embedded migration files. Pure
// (no DB), so it is unit-testable without a live database.
func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := make([]migration, 0, len(names))
	for _, name := range names {
		b, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("store: read migration %s: %w", name, err)
		}
		out = append(out, migration{version: name, sql: string(b)})
	}
	return out, nil
}

// pendingMigrations returns, in order, the migrations whose version is not yet
// recorded in applied. Pure helper (no DB) so version selection is unit-testable.
func pendingMigrations(all []migration, applied map[string]bool) []migration {
	out := make([]migration, 0, len(all))
	for _, m := range all {
		if !applied[m.version] {
			out = append(out, m)
		}
	}
	return out
}

// migrateAdvisoryLockKey is the fixed application-wide key for the session-scoped
// pg_advisory_lock that serializes migration across processes. Its exact value is
// arbitrary but must stay constant so every replica contends on the same lock;
// it is ASCII "LOTS" so a `pg_locks` inspection is self-identifying.
const migrateAdvisoryLockKey int64 = 0x4C4F5453 // ASCII "LOTS"

// SQL used by migrate(). Kept as named constants so the advisory-lock and
// idempotent-insert guarantees are assertable without a live database.
const (
	advisoryLockSQL   = `SELECT pg_advisory_lock($1)`
	advisoryUnlockSQL = `SELECT pg_advisory_unlock($1)`
	// ON CONFLICT DO NOTHING is belt-and-suspenders under the advisory lock: even
	// if two migrators ever raced the bookkeeping insert, the second is a no-op
	// instead of a PK violation that would fail the tx and crash-loop the replica.
	insertMigrationSQL = `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now()) ON CONFLICT (version) DO NOTHING`
)

// migrate applies embedded SQL migrations in lexical (version) order, recording
// each in schema_migrations and skipping any already applied. Each pending
// migration runs in its own transaction together with its bookkeeping insert, so
// a migration and its record commit atomically (a crash mid-run never leaves a
// half-applied version marked done). 0001_init uses CREATE ... IF NOT EXISTS so
// it records cleanly even against a pre-existing scaffold schema; later
// migrations need not be idempotent.
//
// The whole run is serialized across processes by a session-scoped
// pg_advisory_lock held on a single dedicated connection: when several
// control-plane replicas boot against one database only one migrates at a time,
// so the others don't race the pending computation and PK-collide on the
// bookkeeping insert (which crash-looped the losing replica). The advisory lock
// is per-CONNECTION, so it must be acquired, held, and released on the same
// pooled connection that runs the migrations.
func (p *Postgres) migrate(ctx context.Context) error {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, advisoryLockSQL, migrateAdvisoryLockKey); err != nil {
		return fmt.Errorf("store: acquire migration advisory lock: %w", err)
	}
	// Release on the same connection before it returns to the pool. Best-effort:
	// the session lock is also dropped automatically when the connection closes.
	defer func() { _, _ = conn.Exec(ctx, advisoryUnlockSQL, migrateAdvisoryLockKey) }()

	if _, err := conn.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT        PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return fmt.Errorf("store: ensure schema_migrations: %w", err)
	}

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return err
	}
	all, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range pendingMigrations(all, applied) {
		if err := applyMigration(ctx, conn, m); err != nil {
			return err
		}
	}
	return nil
}

// migrator is the subset of pgx query methods migrate() needs, satisfied by a
// pooled connection (so the advisory lock and the migrations share one session).
type migrator interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// appliedMigrations reads the set of already-recorded migration versions.
func appliedMigrations(ctx context.Context, q migrator) (map[string]bool, error) {
	rows, err := q.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	return applied, nil
}

// applyMigration runs one migration and records its version in a single
// transaction on the given connection.
func applyMigration(ctx context.Context, q migrator, m migration) error {
	tx, err := q.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin migration %s: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once the tx commits
	if _, err := tx.Exec(ctx, m.sql); err != nil {
		return fmt.Errorf("store: apply migration %s: %w", m.version, err)
	}
	if _, err := tx.Exec(ctx, insertMigrationSQL, m.version); err != nil {
		return fmt.Errorf("store: record migration %s: %w", m.version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit migration %s: %w", m.version, err)
	}
	return nil
}

// Close releases the connection pool.
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) SaveIncident(ctx context.Context, inc *model.Incident) error {
	resource, err := json.Marshal(inc.Resource)
	if err != nil {
		return fmt.Errorf("store: marshal resource: %w", err)
	}
	timeline, err := json.Marshal(inc.Timeline)
	if err != nil {
		return fmt.Errorf("store: marshal timeline: %w", err)
	}
	hypotheses, err := json.Marshal(inc.Hypotheses)
	if err != nil {
		return fmt.Errorf("store: marshal hypotheses: %w", err)
	}

	const q = `
INSERT INTO incidents
    (id, cluster, namespace, kind, name, title, status, severity,
     opened_at, updated_at, resource, timeline, hypotheses)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (id) DO UPDATE SET
    cluster    = EXCLUDED.cluster,
    namespace  = EXCLUDED.namespace,
    kind       = EXCLUDED.kind,
    name       = EXCLUDED.name,
    title      = EXCLUDED.title,
    status     = EXCLUDED.status,
    severity   = EXCLUDED.severity,
    opened_at  = EXCLUDED.opened_at,
    updated_at = EXCLUDED.updated_at,
    resource   = EXCLUDED.resource,
    timeline   = EXCLUDED.timeline,
    hypotheses = EXCLUDED.hypotheses`

	_, err = p.pool.Exec(ctx, q,
		inc.ID, inc.Resource.Cluster, inc.Resource.Namespace, inc.Resource.Kind,
		inc.Resource.Name, inc.Title, string(inc.Status), int(inc.Severity),
		inc.OpenedAt, inc.UpdatedAt, resource, timeline, hypotheses,
	)
	if err != nil {
		return fmt.Errorf("store: save incident %s: %w", inc.ID, err)
	}
	return nil
}

func (p *Postgres) GetIncident(ctx context.Context, id string) (*model.Incident, error) {
	const q = `
SELECT id, title, status, severity, opened_at, updated_at, resource, timeline, hypotheses
FROM incidents
WHERE id = $1`
	inc, err := scanIncident(p.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get incident %s: %w", id, err)
	}
	return inc, nil
}

func (p *Postgres) ListIncidents(ctx context.Context, f IncidentFilter) ([]*model.Incident, error) {
	// Build the query dynamically: optional cluster/status filters, most-recent
	// first, always-bounded limit (defaulting to DefaultIncidentListLimit) —
	// mirroring Memory.ListIncidents semantics.
	q := `
SELECT id, title, status, severity, opened_at, updated_at, resource, timeline, hypotheses
FROM incidents`
	var (
		args  []any
		conds []string
	)
	if f.Cluster != "" {
		args = append(args, f.Cluster)
		conds = append(conds, fmt.Sprintf("cluster = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, string(f.Status))
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	for i, c := range conds {
		if i == 0 {
			q += " WHERE "
		} else {
			q += " AND "
		}
		q += c
	}
	q += " ORDER BY opened_at DESC"
	// Always bound the result set: an unset Limit falls back to
	// DefaultIncidentListLimit rather than an unbounded SELECT (STORE-3).
	args = append(args, f.effectiveLimit())
	q += fmt.Sprintf(" LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list incidents: %w", err)
	}
	defer rows.Close()

	var out []*model.Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan incident: %w", err)
		}
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list incidents: %w", err)
	}
	return out, nil
}

// row abstracts *pgxpool.Pool.QueryRow and pgx.Rows so scanIncident serves both
// the single-get and list paths.
type row interface {
	Scan(dest ...any) error
}

func scanIncident(r row) (*model.Incident, error) {
	var (
		inc                            model.Incident
		status                         string
		severity                       int
		resource, timeline, hypotheses []byte
	)
	if err := r.Scan(
		&inc.ID, &inc.Title, &status, &severity,
		&inc.OpenedAt, &inc.UpdatedAt, &resource, &timeline, &hypotheses,
	); err != nil {
		return nil, err
	}
	inc.Status = model.IncidentStatus(status)
	inc.Severity = model.Severity(severity)
	if err := json.Unmarshal(resource, &inc.Resource); err != nil {
		return nil, fmt.Errorf("unmarshal resource: %w", err)
	}
	if err := json.Unmarshal(timeline, &inc.Timeline); err != nil {
		return nil, fmt.Errorf("unmarshal timeline: %w", err)
	}
	if err := json.Unmarshal(hypotheses, &inc.Hypotheses); err != nil {
		return nil, fmt.Errorf("unmarshal hypotheses: %w", err)
	}
	return &inc, nil
}

func (p *Postgres) SaveCluster(ctx context.Context, c Cluster) error {
	// Upsert connection state. Descriptive fields (env/region/agent_version) are
	// only overwritten when the incoming value is non-empty, so a live agent
	// connect — which carries just name+connected — cannot wipe env/region/version
	// previously recorded by seed or an earlier connect. connected always reflects
	// the latest call.
	const q = `
INSERT INTO clusters (name, env, region, connected, agent_version)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (name) DO UPDATE SET
    env           = COALESCE(NULLIF(EXCLUDED.env, ''), clusters.env),
    region        = COALESCE(NULLIF(EXCLUDED.region, ''), clusters.region),
    connected     = EXCLUDED.connected,
    agent_version = COALESCE(NULLIF(EXCLUDED.agent_version, ''), clusters.agent_version)`
	if _, err := p.pool.Exec(ctx, q, c.Name, c.Env, c.Region, c.Connected, c.AgentVersion); err != nil {
		return fmt.Errorf("store: save cluster %s: %w", c.Name, err)
	}
	return nil
}

func (p *Postgres) ListClusters(ctx context.Context) ([]Cluster, error) {
	const q = `SELECT name, env, region, connected, agent_version FROM clusters ORDER BY name ASC`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list clusters: %w", err)
	}
	defer rows.Close()

	out := make([]Cluster, 0)
	for rows.Next() {
		var c Cluster
		if err := rows.Scan(&c.Name, &c.Env, &c.Region, &c.Connected, &c.AgentVersion); err != nil {
			return nil, fmt.Errorf("store: scan cluster: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list clusters: %w", err)
	}
	return out, nil
}

var _ Store = (*Postgres)(nil)
