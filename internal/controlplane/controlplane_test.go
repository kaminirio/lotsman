package controlplane

import (
	"context"
	"strings"
	"testing"

	"lotsman/internal/config"
)

// A supplied-but-invalid SSO config must be FATAL: the control plane refuses to
// start rather than silently degrading to the anonymous-global-admin path (which
// would turn a prod config typo into an unauthenticated-admin exposure).
func TestNew_FailsClosedOnInvalidSSOConfig(t *testing.T) {
	cfg := config.Server{
		Addr:        ":0",
		GatewayAddr: ":0",
		SSOConfig:   "{not valid json",
	}
	cp, err := New(context.Background(), cfg, testLogger())
	if err == nil {
		t.Fatal("expected New to fail closed on an invalid SSO config, got nil error")
	}
	if cp != nil {
		t.Error("expected nil control plane when startup fails")
	}
	if !strings.Contains(err.Error(), "SSO") {
		t.Errorf("error should mention SSO, got: %v", err)
	}
}

// An empty SSO config is the local-dev path and must still start (anonymous mode).
func TestNew_EmptySSOConfigStarts(t *testing.T) {
	cfg := config.Server{Addr: ":0", GatewayAddr: ":0"}
	cp, err := New(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatalf("empty SSO config should start in anonymous mode, got: %v", err)
	}
	if cp == nil {
		t.Fatal("expected a control plane")
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
