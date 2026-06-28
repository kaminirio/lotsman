// Command server is the Lotsman control plane: REST/UI API + agent gateway +
// correlation engine. Standard New -> Start -> Shutdown lifecycle.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"lotsman/internal/config"
	"lotsman/internal/controlplane"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if slices.ContainsFunc(os.Args[1:], isVersionFlag) {
		fmt.Println("lotsman-server " + version)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.LoadServer(version)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cp, err := controlplane.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("failed to create control plane", "err", err)
		os.Exit(1)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- cp.Start(ctx) }()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			logger.Error("control plane error", "err", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := cp.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "err", err)
		os.Exit(1)
	}
	logger.Info("control plane stopped")
}

// isVersionFlag reports whether an argument requests the version and exits.
func isVersionFlag(arg string) bool {
	return arg == "-version" || arg == "--version"
}
