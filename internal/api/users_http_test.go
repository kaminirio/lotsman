package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lotsman/internal/auth"
	"lotsman/internal/engine"
	"lotsman/internal/store"
)

// usersTestServer builds a server whose API and auth manager SHARE one store, so
// provisioned accounts are immediately authenticatable. Seeds "admin" (admin) and
// "viewer" (non-admin), both active.
func usersTestServer(t *testing.T) (*Server, *store.Memory) {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()

	seed := func(username string, admin bool) {
		hash, _ := auth.HashPassword("password-123456")
		if err := st.CreateUser(ctx, store.User{
			ID: username, Username: username, Email: username + "@corp.com",
			PasswordHash: hash, IsAdmin: admin, Active: true,
		}); err != nil {
			t.Fatalf("seed %s: %v", username, err)
		}
	}
	seed("admin", true)
	seed("viewer", false)

	mgr, err := auth.NewManagerFromEnv(auth.Config{SessionSecret: testSessionSecret}, st, logger)
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	srv, err := New(Config{
		Addr:    ":0",
		Version: "test",
		Engine:  engine.New(failingResolver{}, logger),
		Store:   st,
		Auth:    mgr,
		Sources: failingResolver{},
	}, logger)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return srv, st
}

func TestListUsersAuthz(t *testing.T) {
	srv, _ := usersTestServer(t)

	t.Run("unauthenticated 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		rec := httptest.NewRecorder()
		srv.handleListUsers(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", rec.Code)
		}
	})
	t.Run("non-admin 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		req.AddCookie(mintCookie(t, "viewer"))
		rec := httptest.NewRecorder()
		srv.handleListUsers(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("got %d, want 403", rec.Code)
		}
	})
	t.Run("admin 200 with users, no secrets", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
		req.AddCookie(mintCookie(t, "admin"))
		rec := httptest.NewRecorder()
		srv.handleListUsers(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"admin"`) || !strings.Contains(body, `"viewer"`) {
			t.Errorf("expected both users, got %s", body)
		}
		if strings.Contains(body, "password") || strings.Contains(body, "PasswordHash") {
			t.Errorf("user list must not leak password material: %s", body)
		}
	})
}

func TestCreateUser(t *testing.T) {
	srv, _ := usersTestServer(t)

	body := `{"username":"carol","email":"carol@corp.com","password":"secret-123456","is_admin":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
	req.AddCookie(mintCookie(t, "admin"))
	rec := httptest.NewRecorder()
	srv.handleCreateUser(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", rec.Code, rec.Body.String())
	}

	// Duplicate username -> 409.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
	req2.AddCookie(mintCookie(t, "admin"))
	rec2 := httptest.NewRecorder()
	srv.handleCreateUser(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("duplicate: got %d, want 409", rec2.Code)
	}

	// Missing fields -> 400.
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{"username":"x"}`))
	req3.AddCookie(mintCookie(t, "admin"))
	rec3 := httptest.NewRecorder()
	srv.handleCreateUser(rec3, req3)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("missing fields: got %d, want 400", rec3.Code)
	}

	// Duplicate EMAIL with a different username -> 409 (username and email are
	// independently unique).
	body4 := `{"username":"someone-else","email":"carol@corp.com","password":"secret-123456","is_admin":false}`
	req4 := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body4))
	req4.AddCookie(mintCookie(t, "admin"))
	rec4 := httptest.NewRecorder()
	srv.handleCreateUser(rec4, req4)
	if rec4.Code != http.StatusConflict {
		t.Fatalf("duplicate email: got %d, want 409", rec4.Code)
	}
}

func userID(t *testing.T, st *store.Memory, username string) string {
	t.Helper()
	u, err := st.GetUserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("lookup %s: %v", username, err)
	}
	return u.ID
}

func patchUser(t *testing.T, srv *Server, id, caller, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+id, strings.NewReader(body))
	req.SetPathValue("id", id)
	req.AddCookie(mintCookie(t, caller))
	rec := httptest.NewRecorder()
	srv.handleUpdateUser(rec, req)
	return rec
}

func TestUpdateUserLastAdminGuard(t *testing.T) {
	srv, st := usersTestServer(t)
	adminID := userID(t, st, "admin")

	// "admin" is the only active admin -> demoting it is refused (409).
	rec := patchUser(t, srv, adminID, "admin", `{"is_admin":false}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("demoting last admin: got %d, want 409", rec.Code)
	}
	rec = patchUser(t, srv, adminID, "admin", `{"active":false}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("deactivating last admin: got %d, want 409", rec.Code)
	}
}

func TestUpdateUserSelfLockoutGuard(t *testing.T) {
	srv, st := usersTestServer(t)
	// Promote viewer to admin so there are two admins.
	viewerID := userID(t, st, "viewer")
	if rec := patchUser(t, srv, viewerID, "admin", `{"is_admin":true}`); rec.Code != http.StatusOK {
		t.Fatalf("promote viewer: got %d, want 200", rec.Code)
	}

	// Now admin tries to demote THEMSELVES -> blocked even with another admin.
	adminID := userID(t, st, "admin")
	if rec := patchUser(t, srv, adminID, "admin", `{"is_admin":false}`); rec.Code != http.StatusConflict {
		t.Fatalf("self-demote: got %d, want 409", rec.Code)
	}

	// But admin CAN demote the other admin (viewer) now.
	if rec := patchUser(t, srv, viewerID, "admin", `{"is_admin":false}`); rec.Code != http.StatusOK {
		t.Fatalf("demote other admin: got %d, want 200", rec.Code)
	}
}

func TestUpdateUserPasswordRotate(t *testing.T) {
	srv, st := usersTestServer(t)
	viewerID := userID(t, st, "viewer")

	rec := patchUser(t, srv, viewerID, "admin", `{"password":"a-brand-new-password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate password: got %d, want 200", rec.Code)
	}
	u, _ := st.GetUserByUsername(context.Background(), "viewer")
	if !auth.ComparePassword(u.PasswordHash, "a-brand-new-password") {
		t.Error("password should have been rotated")
	}
}

func TestDeleteUserGuards(t *testing.T) {
	srv, st := usersTestServer(t)
	adminID := userID(t, st, "admin")
	viewerID := userID(t, st, "viewer")

	// Deleting the sole admin is refused.
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+adminID, nil)
	req.SetPathValue("id", adminID)
	req.AddCookie(mintCookie(t, "admin"))
	rec := httptest.NewRecorder()
	srv.handleDeleteUser(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete last admin: got %d, want 409", rec.Code)
	}

	// Deleting a non-admin is fine.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+viewerID, nil)
	req.SetPathValue("id", viewerID)
	req.AddCookie(mintCookie(t, "admin"))
	rec = httptest.NewRecorder()
	srv.handleDeleteUser(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete viewer: got %d, want 204", rec.Code)
	}

	// Unknown id -> 404.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/users/usr_missing", nil)
	req.SetPathValue("id", "usr_missing")
	req.AddCookie(mintCookie(t, "admin"))
	rec = httptest.NewRecorder()
	srv.handleDeleteUser(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown: got %d, want 404", rec.Code)
	}
}

func TestProvidersEndpointShape(t *testing.T) {
	srv, _ := usersTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/auth/providers", nil)
	rec := httptest.NewRecorder()
	srv.handleProviders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp["local"] {
		t.Error("local must always be true")
	}
	for _, p := range []string{"github", "google", "azure"} {
		if _, ok := resp[p]; !ok {
			t.Errorf("providers response missing %q", p)
		}
	}
}
