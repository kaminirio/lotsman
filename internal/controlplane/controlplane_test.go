package controlplane

import (
	"context"
	"strings"
	"testing"

	"lotsman/internal/config"
)

// A too-short session secret must be FATAL: the control plane refuses to start
// rather than run with a weak signing key (ADR-0011).
func TestNew_FailsClosedOnShortSessionSecret(t *testing.T) {
	cfg := config.Server{
		Addr:        ":0",
		GatewayAddr: ":0",
		Auth:        config.AuthConfig{SessionSecret: "too-short"},
	}
	cp, err := New(context.Background(), cfg, testLogger())
	if err == nil {
		t.Fatal("expected New to fail closed on a too-short session secret, got nil error")
	}
	if cp != nil {
		t.Error("expected nil control plane when startup fails")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("error should mention auth, got: %v", err)
	}
}

// The local-dev path (no admin configured, empty session secret) must still start:
// auth is always on with an ephemeral secret, and a warning notes there is no
// admin yet (ADR-0011).
func TestNew_NoAdminConfiguredStillStarts(t *testing.T) {
	cfg := config.Server{Addr: ":0", GatewayAddr: ":0"}
	cp, err := New(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatalf("empty auth config should still start, got: %v", err)
	}
	if cp == nil {
		t.Fatal("expected a control plane")
	}
}

// The bootstrap admin is seeded idempotently from env and can then log in.
func TestNew_SeedsBootstrapAdmin(t *testing.T) {
	cfg := config.Server{
		Addr:        ":0",
		GatewayAddr: ":0",
		Auth: config.AuthConfig{
			SessionSecret: "a-sufficiently-long-session-secret-value",
			AdminUser:     "root",
			AdminPassword: "supersecret",
		},
	}
	cp, err := New(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatalf("New with bootstrap admin: %v", err)
	}
	u, err := cp.st.GetUserByUsername(context.Background(), "root")
	if err != nil {
		t.Fatalf("bootstrap admin not seeded: %v", err)
	}
	if !u.IsAdmin || !u.Active {
		t.Errorf("bootstrap admin must be active admin, got %+v", u)
	}
}

// A configured backend URL pointing at the cloud-metadata link-local address must
// be rejected at startup (SSRF hardening).
func TestNew_RejectsLinkLocalBackendURL(t *testing.T) {
	cfg := config.Server{Addr: ":0", GatewayAddr: ":0", LokiURL: "http://169.254.169.254/loki"}
	if _, err := New(context.Background(), cfg, testLogger()); err == nil {
		t.Fatal("expected New to reject a link-local backend URL")
	}
}
