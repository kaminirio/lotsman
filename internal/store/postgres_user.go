package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolationCode is the PostgreSQL SQLSTATE for unique_violation, raised by
// the case-insensitive username/email indexes on a duplicate CreateUser.
const uniqueViolationCode = "23505"

// isUniqueViolation reports whether err is a Postgres unique-constraint failure,
// which CreateUser maps to ErrConflict.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode
}

func (p *Postgres) CreateUser(ctx context.Context, u User) error {
	// created_at/updated_at default to now() in the 0003 migration, so they are
	// left out of the column list and stamped by the database.
	const q = `
INSERT INTO users (id, username, email, password_hash, is_admin, active, sso_provider, sso_subject)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := p.pool.Exec(ctx, q,
		u.ID, u.Username, u.Email, u.PasswordHash, u.IsAdmin, u.Active, u.SSOProvider, u.SSOSubject,
	)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("store: create user %s: %w", u.ID, err)
	}
	return nil
}

const selectUser = `
SELECT id, username, email, password_hash, is_admin, active, sso_provider, sso_subject, created_at, updated_at
FROM users`

func (p *Postgres) GetUserByID(ctx context.Context, id string) (User, error) {
	return p.getUser(ctx, selectUser+` WHERE id = $1`, id)
}

func (p *Postgres) GetUserByUsername(ctx context.Context, username string) (User, error) {
	// Case-insensitive, matching the users_username_lower_idx unique index.
	return p.getUser(ctx, selectUser+` WHERE lower(username) = lower($1)`, username)
}

func (p *Postgres) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return p.getUser(ctx, selectUser+` WHERE lower(email) = lower($1)`, email)
}

func (p *Postgres) GetUserBySSO(ctx context.Context, provider, subject string) (User, error) {
	// An empty provider/subject never matches a linked account (mirrors Memory);
	// short-circuit so unlinked accounts with empty sso_subject can't be resolved.
	if provider == "" || subject == "" {
		return User{}, ErrNotFound
	}
	return p.getUser(ctx, selectUser+` WHERE sso_provider = $1 AND sso_subject = $2`, provider, subject)
}

// getUser runs a single-row user query and maps pgx.ErrNoRows to ErrNotFound.
func (p *Postgres) getUser(ctx context.Context, q string, args ...any) (User, error) {
	u, err := scanUser(p.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("store: get user: %w", err)
	}
	return u, nil
}

func (p *Postgres) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := p.pool.Query(ctx, selectUser+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()

	out := make([]User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan user: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	return out, nil
}

func (p *Postgres) UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("store: begin update user %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	// Lock the target row for the duration of the guard-and-write so two concurrent
	// demotions cannot each observe a safe admin count and both commit — the same
	// invariant Memory.UpdateUser holds under its mutex.
	cur, err := scanUser(tx.QueryRow(ctx, selectUser+` WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("store: update user %s: %w", id, err)
	}
	if patch.GuardLastActiveAdmin && cur.IsAdmin && cur.Active {
		n, err := lockedActiveAdminCount(ctx, tx)
		if err != nil {
			return User{}, fmt.Errorf("store: update user %s: %w", id, err)
		}
		if n <= 1 {
			return User{}, ErrConflict
		}
	}

	// COALESCE applies only the non-nil patch fields; unset ones keep their column
	// value. Explicit casts give the nil parameters a concrete type.
	const upd = `
UPDATE users SET
    is_admin      = COALESCE($2::boolean, is_admin),
    active        = COALESCE($3::boolean, active),
    password_hash = COALESCE($4::text, password_hash),
    sso_provider  = COALESCE($5::text, sso_provider),
    sso_subject   = COALESCE($6::text, sso_subject),
    updated_at    = now()
WHERE id = $1
RETURNING id, username, email, password_hash, is_admin, active, sso_provider, sso_subject, created_at, updated_at`
	updated, err := scanUser(tx.QueryRow(ctx, upd, id,
		patch.IsAdmin, patch.Active, patch.PasswordHash, patch.SSOProvider, patch.SSOSubject,
	))
	if err != nil {
		return User{}, fmt.Errorf("store: update user %s: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("store: commit update user %s: %w", id, err)
	}
	return updated, nil
}

func (p *Postgres) DeleteUser(ctx context.Context, id string, guardLastActiveAdmin bool) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin delete user %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	// Lock the row and evaluate the last-admin guard in the same tx as the delete
	// (see UpdateUser).
	cur, err := scanUser(tx.QueryRow(ctx, selectUser+` WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: delete user %s: %w", id, err)
	}
	if guardLastActiveAdmin && cur.IsAdmin && cur.Active {
		n, err := lockedActiveAdminCount(ctx, tx)
		if err != nil {
			return fmt.Errorf("store: delete user %s: %w", id, err)
		}
		if n <= 1 {
			return ErrConflict
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: delete user %s: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit delete user %s: %w", id, err)
	}
	return nil
}

func (p *Postgres) CountActiveAdmins(ctx context.Context) (int, error) {
	n, err := lockedActiveAdminCount(ctx, p.pool)
	if err != nil {
		return 0, fmt.Errorf("store: count active admins: %w", err)
	}
	return n, nil
}

// lockedActiveAdminCount counts active admins using the given querier, which may
// be the pool (CountActiveAdmins) or an open tx (the guard inside UpdateUser /
// DeleteUser, so the count and the mutation share one transaction).
func lockedActiveAdminCount(ctx context.Context, q rowQuerier) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_admin AND active`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// rowQuerier is the QueryRow subset shared by *pgxpool.Pool and pgx.Tx.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func scanUser(r row) (User, error) {
	var u User
	if err := r.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.Active,
		&u.SSOProvider, &u.SSOSubject, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		return User{}, err
	}
	return u, nil
}
