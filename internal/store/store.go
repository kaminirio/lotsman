// Package store is the control-plane persistence boundary. Lotsman persists only
// its own derived state — incidents, change history, clusters, config, users —
// and queries telemetry (logs/metrics) live through agents (ADR-0004). The
// production implementation is PostgreSQL via pgx (ADR-0005), auto-selected when
// a DSN is configured; an in-memory implementation backs development and tests.
package store

import (
	"context"
	"errors"
	"time"

	"lotsman/internal/model"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("lotsman: not found")

// ErrConflict is returned when a write would violate a uniqueness constraint
// (e.g. a duplicate username or email on CreateUser). The API maps it to 409.
var ErrConflict = errors.New("lotsman: conflict")

// Store persists control-plane state.
type Store interface {
	SaveIncident(ctx context.Context, inc *model.Incident) error
	GetIncident(ctx context.Context, id string) (*model.Incident, error)
	ListIncidents(ctx context.Context, f IncidentFilter) ([]*model.Incident, error)

	SaveCluster(ctx context.Context, c Cluster) error
	ListClusters(ctx context.Context) ([]Cluster, error)

	SaveEnrollmentToken(ctx context.Context, t EnrollmentToken) error
	GetEnrollmentTokenByHash(ctx context.Context, hash string) (EnrollmentToken, error) // store.ErrNotFound if absent
	ListEnrollmentTokens(ctx context.Context) ([]EnrollmentToken, error)                // newest first
	RevokeEnrollmentToken(ctx context.Context, id string) error                         // store.ErrNotFound if unknown id

	// First-party user accounts (ADR-0011). Username and email matches are
	// case-insensitive. CreateUser returns ErrConflict on a duplicate
	// username/email; the getters return ErrNotFound when absent.
	CreateUser(ctx context.Context, u User) error
	GetUserByID(ctx context.Context, id string) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	GetUserByEmail(ctx context.Context, email string) (User, error)
	// GetUserBySSO returns the account linked to the given (provider, subject)
	// pair — the stable primary key for a returning SSO user. Returns ErrNotFound
	// when no account is linked or when either argument is empty.
	GetUserBySSO(ctx context.Context, provider, subject string) (User, error)
	ListUsers(ctx context.Context) ([]User, error) // newest first
	// UpdateUser applies a partial patch (only non-nil fields) to the user with
	// the given id and returns the updated record. ErrNotFound for unknown id.
	// When patch.GuardLastActiveAdmin is set, the write is refused with ErrConflict
	// if it would demote/deactivate the last active admin (checked atomically with
	// the mutation).
	UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error)
	// DeleteUser removes a user by id. ErrNotFound for unknown id. When
	// guardLastActiveAdmin is set, the delete is refused with ErrConflict if it
	// would remove the last active admin (checked atomically with the delete).
	DeleteUser(ctx context.Context, id string, guardLastActiveAdmin bool) error
	// CountActiveAdmins reports how many active is_admin users exist, backing the
	// last-admin lockout guard.
	CountActiveAdmins(ctx context.Context) (int, error)

	// Durable reports whether state survives a control-plane restart. The
	// in-memory store returns false; the Postgres store returns true. Agent
	// enrollment tokens are NOT re-derivable, so the enrollment subsystem refuses
	// to issue or validate them unless the store is durable.
	Durable() bool
}

// DefaultIncidentListLimit is the safety cap applied by ListIncidents when the
// caller leaves IncidentFilter.Limit unset (<= 0). It bounds an otherwise
// unbounded SELECT so a large incident table cannot be pulled into memory in one
// query. The API layer typically sets an explicit smaller limit; this is the
// backstop that keeps the store safe by default.
const DefaultIncidentListLimit = 500

// IncidentFilter narrows ListIncidents.
type IncidentFilter struct {
	Cluster string
	Status  model.IncidentStatus
	Limit   int
}

// effectiveLimit resolves IncidentFilter.Limit to the value actually applied by
// a store: the caller's positive limit, or DefaultIncidentListLimit when unset.
func (f IncidentFilter) effectiveLimit() int {
	if f.Limit > 0 {
		return f.Limit
	}
	return DefaultIncidentListLimit
}

// Cluster is a registered cluster plus its agent connection state. Field shape
// (env/region) maps onto the clusters table.
type Cluster struct {
	Name         string `json:"name"`
	Env          string `json:"env"`
	Region       string `json:"region"`
	Connected    bool   `json:"connected"`
	AgentVersion string `json:"agent_version,omitempty"`
	// Mode is "connected" for a cluster currently reachable through the registry
	// (a direct provider or a live agent link); empty for clusters known only from
	// the persisted store. Derived at read time by the API, not persisted.
	Mode string `json:"mode,omitempty"`
}

// User is a first-party account (ADR-0011). PasswordHash is a bcrypt hash and is
// empty for SSO-only accounts. SSOProvider/SSOSubject link the account to an
// external identity after its first SSO sign-in. Maps onto the users table.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"` // bcrypt; never serialized to clients
	IsAdmin      bool      `json:"is_admin"`
	Active       bool      `json:"active"`
	SSOProvider  string    `json:"sso_provider"`
	SSOSubject   string    `json:"-"` // never serialized to clients
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserPatch carries an optional partial update for UpdateUser. Only non-nil
// fields are applied, so a caller can flip is_admin without touching the
// password, etc.
type UserPatch struct {
	IsAdmin      *bool
	Active       *bool
	PasswordHash *string
	SSOProvider  *string
	SSOSubject   *string
	// GuardLastActiveAdmin, when true, makes UpdateUser refuse (ErrConflict) any
	// change that would leave zero active admins. The check runs in the same
	// critical section as the write so concurrent demotions cannot each observe a
	// safe count and both commit. It is not itself a mutated column.
	GuardLastActiveAdmin bool
}

// EnrollmentToken is a per-cluster agent enrollment secret. Only the SHA-256
// Hash is persisted; the plaintext is shown to the operator once at creation and
// never stored or serialized to clients. Maps onto the enrollment_tokens table.
type EnrollmentToken struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Hash      string    `json:"-"` // never serialized to clients
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Revoked   bool      `json:"revoked"`
}
