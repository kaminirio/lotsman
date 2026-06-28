package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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
// the (idempotent) schema migrations. Caller must Close the returned store.
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

// migrate applies every embedded migration. Statements use CREATE ... IF NOT
// EXISTS, so applying them on every startup is safe and needs no version table.
func (p *Postgres) migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	for _, e := range entries {
		sql, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("store: read migration %s: %w", e.Name(), err)
		}
		if _, err := p.pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("store: apply migration %s: %w", e.Name(), err)
		}
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
	// first, optional limit — mirroring Memory.ListIncidents semantics.
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
	if f.Limit > 0 {
		args = append(args, f.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}

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
	const q = `
INSERT INTO clusters (name, env, region, connected, agent_version)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (name) DO UPDATE SET
    env           = EXCLUDED.env,
    region        = EXCLUDED.region,
    connected     = EXCLUDED.connected,
    agent_version = EXCLUDED.agent_version`
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
