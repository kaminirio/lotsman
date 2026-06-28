// Command agent is the in-cluster Lotsman agent. It dials OUT to the control
// plane and serves source queries against the cluster's Loki / VictoriaMetrics /
// ArgoCD / Kubernetes backends.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"lotsman/internal/agent"
	"lotsman/internal/config"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if slices.ContainsFunc(os.Args[1:], isVersionFlag) {
		fmt.Println("lotsman-agent " + version)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.LoadAgent(version)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := agent.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create agent", "err", err)
		os.Exit(1)
	}

	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("agent stopped with error", "err", err)
		os.Exit(1)
	}
	logger.Info("agent stopped")
}

// isVersionFlag reports whether an argument requests the version and exits.
func isVersionFlag(arg string) bool {
	return arg == "-version" || arg == "--version"
}
