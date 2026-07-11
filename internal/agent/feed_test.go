package agent

import (
	"io"
	"log/slog"
	"testing"

	"lotsman/internal/config"
)

// New must wire the Kubernetes watch-event feed onto the dialer (LINK-1): without
// this the push path is dead and detection falls back to poll-only.
func TestNewWiresEventFeed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := New(config.Agent{
		Cluster:          "test",
		ControlPlaneAddr: "127.0.0.1:0",
		Token:            "t",
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !a.dialer.HasEventFeed() {
		t.Fatal("agent dialer has no event feed: the watch-event push path is not wired")
	}
}
