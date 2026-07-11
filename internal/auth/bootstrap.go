package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"lotsman/internal/store"
)

// BootstrapStore is the slice of the store used to seed the first admin.
type BootstrapStore interface {
	GetUserByUsername(ctx context.Context, username string) (store.User, error)
	CreateUser(ctx context.Context, u store.User) error
	UpdateUser(ctx context.Context, id string, patch store.UserPatch) (store.User, error)
}

// EnsureBootstrapAdmin idempotently seeds the first admin from env. When the named
// account is missing it is created (active, admin, password hashed). When it
// already exists it is force-set active+admin (password is left untouched so an
// operator-rotated password survives restarts). A blank username or password is a
// no-op. Safe to call on every boot.
func EnsureBootstrapAdmin(ctx context.Context, st BootstrapStore, username, password string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if username == "" || password == "" {
		return nil
	}

	existing, err := st.GetUserByUsername(ctx, username)
	switch {
	case err == nil:
		if existing.Active && existing.IsAdmin {
			return nil
		}
		active, admin := true, true
		if _, uerr := st.UpdateUser(ctx, existing.ID, store.UserPatch{Active: &active, IsAdmin: &admin}); uerr != nil {
			return fmt.Errorf("auth: promoting bootstrap admin %q: %w", username, uerr)
		}
		logger.Info("bootstrap admin re-activated/promoted", "username", username)
		return nil
	case errors.Is(err, store.ErrNotFound):
		hash, herr := HashPassword(password)
		if herr != nil {
			return fmt.Errorf("auth: hashing bootstrap admin password: %w", herr)
		}
		if cerr := st.CreateUser(ctx, store.User{
			ID:           newUserID(),
			Username:     username,
			Email:        username + "@lotsman.local",
			PasswordHash: hash,
			IsAdmin:      true,
			Active:       true,
			CreatedAt:    time.Now(),
		}); cerr != nil {
			return fmt.Errorf("auth: creating bootstrap admin %q: %w", username, cerr)
		}
		logger.Info("bootstrap admin created", "username", username)
		return nil
	default:
		return fmt.Errorf("auth: looking up bootstrap admin %q: %w", username, err)
	}
}
