package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Durable reports true: Postgres state survives a control-plane restart, so the
// enrollment subsystem is allowed to issue and validate agent tokens against it.
func (p *Postgres) Durable() bool { return true }

func (p *Postgres) SaveEnrollmentToken(ctx context.Context, t EnrollmentToken) error {
	// expires_at NULL means "no expiry" (a zero time.Time in Go), mirroring the
	// 0002 migration; pass an untyped nil so pgx writes SQL NULL.
	var expires any
	if !t.ExpiresAt.IsZero() {
		expires = t.ExpiresAt
	}
	const q = `
INSERT INTO enrollment_tokens (id, cluster, hash, expires_at, revoked)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
    cluster    = EXCLUDED.cluster,
    hash       = EXCLUDED.hash,
    expires_at = EXCLUDED.expires_at,
    revoked    = EXCLUDED.revoked`
	if _, err := p.pool.Exec(ctx, q, t.ID, t.Cluster, t.Hash, expires, t.Revoked); err != nil {
		return fmt.Errorf("store: save enrollment token %s: %w", t.ID, err)
	}
	return nil
}

func (p *Postgres) GetEnrollmentTokenByHash(ctx context.Context, hash string) (EnrollmentToken, error) {
	const q = `
SELECT id, cluster, hash, created_at, expires_at, revoked
FROM enrollment_tokens
WHERE hash = $1`
	t, err := scanEnrollmentToken(p.pool.QueryRow(ctx, q, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return EnrollmentToken{}, ErrNotFound
	}
	if err != nil {
		return EnrollmentToken{}, fmt.Errorf("store: get enrollment token by hash: %w", err)
	}
	return t, nil
}

func (p *Postgres) ListEnrollmentTokens(ctx context.Context) ([]EnrollmentToken, error) {
	const q = `
SELECT id, cluster, hash, created_at, expires_at, revoked
FROM enrollment_tokens
ORDER BY created_at DESC`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: list enrollment tokens: %w", err)
	}
	defer rows.Close()

	out := make([]EnrollmentToken, 0)
	for rows.Next() {
		t, err := scanEnrollmentToken(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan enrollment token: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list enrollment tokens: %w", err)
	}
	return out, nil
}

func (p *Postgres) RevokeEnrollmentToken(ctx context.Context, id string) error {
	// Keep the record so it stays listed; mark it revoked (mirrors Memory).
	const q = `UPDATE enrollment_tokens SET revoked = true WHERE id = $1`
	tag, err := p.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("store: revoke enrollment token %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanEnrollmentToken reads one enrollment_tokens row. A NULL expires_at (no
// expiry) scans back into a zero time.Time, matching the SaveEnrollmentToken
// convention and the Memory store.
func scanEnrollmentToken(r row) (EnrollmentToken, error) {
	var (
		t       EnrollmentToken
		expires *time.Time
	)
	if err := r.Scan(&t.ID, &t.Cluster, &t.Hash, &t.CreatedAt, &expires, &t.Revoked); err != nil {
		return EnrollmentToken{}, err
	}
	if expires != nil {
		t.ExpiresAt = *expires
	}
	return t, nil
}
