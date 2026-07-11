// Package enrollment issues and validates per-cluster agent enrollment tokens.
// Each cluster gets its own token (replacing the single shared
// LOTSMAN_AGENT_TOKEN model): the control plane stores only the SHA-256 hash, the
// plaintext is shown once at creation, and the agent presents it in its Hello.
// The gateway validates a Hello{Cluster,Token} pair SOLELY against tokens issued
// here — there is no shared-token or accept-any fallback (direct mode has no
// gateway and is unaffected).
package enrollment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"lotsman/internal/store"
)

// tokenPrefix marks a Lotsman enrollment token ("lotsman secret, enrollment").
const tokenPrefix = "lse_"

// Sentinel errors. The gateway maps ANY of them to codes.Unauthenticated, so the
// distinctions are for logs/tests only and never become an oracle to a caller.
var (
	errUnauthorized    = errors.New("enrollment: unauthorized")
	errExpired         = errors.New("enrollment: token expired")
	errClusterMismatch = errors.New("enrollment: token not bound to this cluster")
	// errStoreEphemeral is returned when the backing store cannot persist tokens
	// across restarts (in-memory). Enrollment tokens are not re-derivable, so the
	// validator refuses to authenticate any agent rather than rely on volatile
	// state — set LOTSMAN_DATABASE_URL to enable agent onboarding.
	errStoreEphemeral = errors.New("enrollment: token store is not durable (set LOTSMAN_DATABASE_URL)")
)

// Hash returns the hex-encoded SHA-256 of a plaintext token. Only the hash is
// persisted; lookups hash the presented token and compare against stored hashes.
// Plain (unsalted/un-peppered) SHA-256 is sufficient here because the token
// carries 256 bits of crypto/rand entropy — there is no dictionary or rainbow
// attack to defend against. Revisit if the token length ever shrinks.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Generate mints a fresh enrollment token. plaintext is shown to the operator
// exactly once ("lse_" + base64url of 32 random bytes); hash is what gets stored;
// id is a short random handle used to list/revoke the token.
func Generate() (plaintext, hash, id string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", err
	}
	idBytes := make([]byte, 8)
	if _, err = rand.Read(idBytes); err != nil {
		return "", "", "", err
	}
	plaintext = tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash = Hash(plaintext)
	id = hex.EncodeToString(idBytes)
	return plaintext, hash, id, nil
}

// Validator authenticates an agent's Hello against the persisted enrollment
// tokens. It is the only TokenValidator the gateway uses in production.
type Validator struct {
	store store.Store
	now   func() time.Time
}

// NewValidator builds a Validator backed by the control-plane store.
func NewValidator(s store.Store) *Validator {
	return &Validator{store: s, now: time.Now}
}

// ValidateEnrollment reports whether token authorizes an agent for cluster. It
// returns a non-nil error on any failure; the gateway maps every error to
// Unauthenticated. The store-error and not-found paths return the same generic
// error so a caller cannot distinguish "no such token" from a backend fault (no
// enumeration oracle).
func (v *Validator) ValidateEnrollment(ctx context.Context, cluster, token string) error {
	// Tokens are issued and stored durably or not at all: an ephemeral store would
	// "forget" every token on restart and silently lock out all agents, so refuse
	// outright rather than validate against volatile state.
	if !v.store.Durable() {
		return errStoreEphemeral
	}
	if token == "" {
		return errUnauthorized
	}
	rec, err := v.store.GetEnrollmentTokenByHash(ctx, Hash(token))
	if err != nil {
		return errUnauthorized
	}
	if rec.Revoked {
		return errUnauthorized
	}
	if !rec.ExpiresAt.IsZero() && v.now().After(rec.ExpiresAt) {
		return errExpired
	}
	// Cluster binding: a valid token may only enroll the cluster it was issued
	// for, closing the spoofing gap where any valid token could claim any name.
	// A plain compare is fine — the cluster name is attacker-supplied and not a
	// secret, and this runs only after the secret token hash already matched.
	if rec.Cluster != cluster {
		return errClusterMismatch
	}
	return nil
}
