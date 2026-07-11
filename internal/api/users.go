package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"lotsman/internal/auth"
	"lotsman/internal/store"
)

// newUserID returns a random, unguessable account id.
func newUserID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "usr_" + hex.EncodeToString(b)
}

// usersMaxBody caps the create/update user request body.
const usersMaxBody = 4 << 10 // 4 KiB

// userView is the list/metadata DTO for an account. It never carries the password
// hash or SSO subject (both are store.User `json:"-"` fields).
type userView struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	IsAdmin     bool      `json:"is_admin"`
	Active      bool      `json:"active"`
	SSOProvider string    `json:"sso_provider"`
	CreatedAt   time.Time `json:"created_at"`
}

func toUserView(u store.User) userView {
	return userView{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		IsAdmin:     u.IsAdmin,
		Active:      u.Active,
		SSOProvider: u.SSOProvider,
		CreatedAt:   u.CreatedAt,
	}
}

// createUserRequest is the POST /api/v1/users body.
type createUserRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

// handleListUsers lists all accounts, newest first. Admin-gated.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	users, err := s.cfg.Store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]userView, 0, len(users))
	for _, u := range users {
		views = append(views, toUserView(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": views})
}

// handleCreateUser provisions a new local account. Admin-gated. 201 on success,
// 409 on a duplicate username/email.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, usersMaxBody)
	var req createUserRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, err)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, errors.New("username, email and password are required"))
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Users work on both stores, but on an ephemeral store a provisioned account is
	// lost on restart (only the bootstrap admin is re-seeded), so warn.
	if !s.cfg.Store.Durable() {
		s.logger.Warn("creating a user on a non-durable store; it will NOT survive a restart (set LOTSMAN_DATABASE_URL)", "username", req.Username)
	}

	u := store.User{
		ID:           newUserID(),
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
		IsAdmin:      req.IsAdmin,
		Active:       true,
		CreatedAt:    time.Now(),
	}
	if err := s.cfg.Store.CreateUser(r.Context(), u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, errors.New("username or email already exists"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"email":    u.Email,
		"is_admin": u.IsAdmin,
		"active":   u.Active,
	})
}

// updateUserRequest is the PATCH body; every field is optional.
type updateUserRequest struct {
	IsAdmin  *bool   `json:"is_admin"`
	Active   *bool   `json:"active"`
	Password *string `json:"password"`
}

// handleUpdateUser applies a partial update. Admin-gated. It refuses any change
// that would remove the last active admin or lock the caller out (409).
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	target, err := s.cfg.Store.GetUserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, usersMaxBody)
	var req updateUserRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, err)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Guard: reject a change that demotes or deactivates the last active admin.
	// The handler check gives the precise 409 message (and enforces the self-lockout
	// rail), but it is not atomic with the write; the store re-checks under lock
	// (GuardLastActiveAdmin) so a concurrent demotion of a different admin can't slip
	// through and reach zero admins.
	demotes := (req.IsAdmin != nil && !*req.IsAdmin) || (req.Active != nil && !*req.Active)
	guardAdmin := demotes && target.IsAdmin && target.Active
	if guardAdmin {
		if err := s.guardLastAdmin(r.Context(), w, caller, target); err != nil {
			return
		}
	}

	patch := store.UserPatch{IsAdmin: req.IsAdmin, Active: req.Active, GuardLastActiveAdmin: guardAdmin}
	if req.Password != nil {
		if *req.Password == "" {
			writeError(w, http.StatusBadRequest, errors.New("password must not be empty"))
			return
		}
		hash, herr := auth.HashPassword(*req.Password)
		if herr != nil {
			writeError(w, http.StatusBadRequest, herr)
			return
		}
		patch.PasswordHash = &hash
	}

	updated, err := s.cfg.Store.UpdateUser(r.Context(), id, patch)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if errors.Is(err, store.ErrConflict) {
		// The atomic store guard rejected the last-active-admin demotion (race with
		// a concurrent demotion). Same 409 the handler guard returns.
		writeError(w, http.StatusConflict, errors.New("cannot remove the last active admin"))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserView(updated))
}

// handleDeleteUser deletes an account. Admin-gated. 204 on success; 409 if it
// would remove the last active admin or the caller themselves as sole admin.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")

	target, err := s.cfg.Store.GetUserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	guardAdmin := target.IsAdmin && target.Active
	if guardAdmin {
		if err := s.guardLastAdmin(r.Context(), w, caller, target); err != nil {
			return
		}
	}

	// guardLastActiveAdmin re-checks under lock in the store so a concurrent delete
	// of a different admin cannot race past the handler check to zero admins.
	if err := s.cfg.Store.DeleteUser(r.Context(), id, guardAdmin); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errNotFound)
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, errors.New("cannot remove the last active admin"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// guardLastAdmin writes a 409 and returns an error when demoting/removing target
// would leave no active admin, or when the caller is removing their own last-admin
// access (self-lockout). Returns nil (no response written) when the change is safe.
func (s *Server) guardLastAdmin(ctx context.Context, w http.ResponseWriter, caller auth.User, target store.User) error {
	n, err := s.cfg.Store.CountActiveAdmins(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return err
	}
	if n <= 1 {
		err := errors.New("cannot remove the last active admin")
		writeError(w, http.StatusConflict, err)
		return err
	}
	// Self-lockout rail: even with other admins, block the caller from stripping
	// their OWN admin access in a single call (they can ask another admin).
	if strings.EqualFold(caller.Login, target.Username) {
		err := errors.New("cannot remove your own admin access")
		writeError(w, http.StatusConflict, err)
		return err
	}
	return nil
}
