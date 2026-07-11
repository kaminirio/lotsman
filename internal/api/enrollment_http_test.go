package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lotsman/internal/store"
)

func TestCreateEnrollmentTokenUnauthenticated(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens", strings.NewReader(`{"cluster":"prod-eu"}`))
	rec := httptest.NewRecorder()
	srv.handleCreateEnrollmentToken(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestCreateEnrollmentTokenNonAdmin(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens", strings.NewReader(`{"cluster":"prod-eu"}`))
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()
	srv.handleCreateEnrollmentToken(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rec.Code)
	}
}

func TestCreateEnrollmentTokenEmptyCluster(t *testing.T) {
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens", strings.NewReader(`{"cluster":"  "}`))
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()
	srv.handleCreateEnrollmentToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func TestEnrollmentDefaults(t *testing.T) {
	// Non-admin is forbidden.
	srv := testServer(t, testSSOConfig)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/enrollment-defaults", nil)
	req.AddCookie(mintCookie(t, "viewer-user"))
	rec := httptest.NewRecorder()
	srv.handleEnrollmentDefaults(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: got %d, want 403", rec.Code)
	}

	// Admin gets the presentation hints. testServer's store is durable.
	adminReq := httptest.NewRequest(http.MethodGet, "/api/v1/enrollment-defaults", nil)
	adminReq.AddCookie(mintCookie(t, "admin-user"))
	adminRec := httptest.NewRecorder()
	srv.handleEnrollmentDefaults(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin: got %d, want 200", adminRec.Code)
	}
	var got struct {
		Namespace string `json:"namespace"`
		Durable   bool   `json:"durable"`
	}
	if err := json.NewDecoder(adminRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Namespace != "lotsman" || !got.Durable {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	if strings.Contains(adminRec.Body.String(), "lse_") {
		t.Fatalf("defaults leaked token material: %s", adminRec.Body.String())
	}
}

func TestEnrollmentTokenRequiresDurableStore(t *testing.T) {
	// In-memory (non-durable) store: an admin's create must be refused with 503.
	srv := testServerWithStore(t, testSSOConfig, store.NewMemory())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens", strings.NewReader(`{"cluster":"prod-eu"}`))
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()
	srv.handleCreateEnrollmentToken(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 (ephemeral store)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "LOTSMAN_DATABASE_URL") {
		t.Fatalf("503 body should point at LOTSMAN_DATABASE_URL: %s", rec.Body.String())
	}

	// Listing, by contrast, must NOT 503 on a non-durable store — it returns an
	// empty set so the Clusters page can still load (and disable enroll via the
	// defaults endpoint's durable:false). Regression guard for the page-breaking
	// Promise.all rejection.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/enrollment-tokens", nil)
	listReq.AddCookie(mintCookie(t, "admin-user"))
	listRec := httptest.NewRecorder()
	srv.handleListEnrollmentTokens(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list on ephemeral store: got %d, want 200", listRec.Code)
	}
	if !strings.Contains(listRec.Body.String(), `"tokens"`) {
		t.Fatalf("list should return a tokens array: %s", listRec.Body.String())
	}
}

func TestEnrollmentTokenCreateListRevoke(t *testing.T) {
	srv := testServer(t, testSSOConfig)

	// Create -> 201 with an lse_ plaintext token.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens", strings.NewReader(`{"cluster":"prod-eu","ttl_hours":24}`))
	req.AddCookie(mintCookie(t, "admin-user"))
	rec := httptest.NewRecorder()
	srv.handleCreateEnrollmentToken(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201", rec.Code)
	}
	var created struct {
		ID        string  `json:"id"`
		Cluster   string  `json:"cluster"`
		Token     string  `json:"token"`
		ExpiresAt *string `json:"expires_at"`
		Revoked   bool    `json:"revoked"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if !strings.HasPrefix(created.Token, "lse_") {
		t.Fatalf("token missing lse_ prefix: %q", created.Token)
	}
	if created.Cluster != "prod-eu" || created.ID == "" {
		t.Fatalf("unexpected create body: %+v", created)
	}
	if created.ExpiresAt == nil {
		t.Fatal("expires_at should be set for ttl_hours>0")
	}

	// List -> 200; never exposes token or hash.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/enrollment-tokens", nil)
	listReq.AddCookie(mintCookie(t, "admin-user"))
	listRec := httptest.NewRecorder()
	srv.handleListEnrollmentTokens(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: got %d, want 200", listRec.Code)
	}
	body := listRec.Body.String()
	if strings.Contains(body, "lse_") || strings.Contains(body, created.Token) {
		t.Fatalf("list leaked plaintext token: %s", body)
	}
	if strings.Contains(body, `"hash"`) {
		t.Fatalf("list leaked hash: %s", body)
	}
	var listResp struct {
		Tokens []struct {
			ID      string `json:"id"`
			Cluster string `json:"cluster"`
		} `json:"tokens"`
	}
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Tokens) != 1 || listResp.Tokens[0].ID != created.ID {
		t.Fatalf("unexpected list: %+v", listResp.Tokens)
	}

	// Revoke -> 204.
	revReq := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens/"+created.ID+"/revoke", nil)
	revReq.SetPathValue("id", created.ID)
	revReq.AddCookie(mintCookie(t, "admin-user"))
	revRec := httptest.NewRecorder()
	srv.handleRevokeEnrollmentToken(revRec, revReq)
	if revRec.Code != http.StatusNoContent {
		t.Fatalf("revoke: got %d, want 204", revRec.Code)
	}

	// Revoke unknown id -> 404.
	missReq := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment-tokens/nope/revoke", nil)
	missReq.SetPathValue("id", "nope")
	missReq.AddCookie(mintCookie(t, "admin-user"))
	missRec := httptest.NewRecorder()
	srv.handleRevokeEnrollmentToken(missRec, missReq)
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown: got %d, want 404", missRec.Code)
	}
}
