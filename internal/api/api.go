// Package api serves the REST API consumed by the UI and the embedded UI assets.
// UI<->control-plane is REST + SSE; agent<->control-plane is a separate gRPC
// channel (see internal/agentlink). Server lifecycle: New -> Start -> Shutdown.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"lotsman/internal/analyze"
	"lotsman/internal/auth"
	"lotsman/internal/engine"
	"lotsman/internal/events"
	"lotsman/internal/sources"
	"lotsman/internal/store"
)

// Sources resolves the per-cluster sources.Provider used to read live cluster
// state (pods, logs, workloads, events) and enumerates the clusters reachable
// through the registry (direct providers + connected agents). *controlplane.Registry
// satisfies it, so it is a superset of engine.ProviderResolver.
type Sources interface {
	Provider(cluster string) (sources.Provider, error)
	Clusters() []string
}

// Config configures the API server.
type Config struct {
	Addr    string
	Version string
	Engine  *engine.Engine
	Store   store.Store
	Auth    *auth.Manager
	// Sources resolves the per-cluster sources.Provider used to read live pod
	// state and logs, and lists the registry's reachable clusters (the *Registry
	// implements it).
	Sources Sources
	// Bus fans out incidents to live SSE subscribers (GET /api/v1/stream). The
	// detector scheduler and manual investigations publish to it.
	Bus *events.IncidentBus
	// Explainer is the OPTIONAL, off-by-default LLM incident-explainer. It may be
	// nil or report Available()==false, both meaning "not configured": the explain
	// endpoint then responds 503 and nothing else changes. It is assistive only and
	// never on the detection hot path.
	Explainer analyze.Explainer
}

// Server serves the REST API + embedded UI.
type Server struct {
	cfg    Config
	logger *slog.Logger
	http   *http.Server
}

// New constructs the API server.
func New(cfg Config, logger *slog.Logger) (*Server, error) {
	s := &Server{cfg: cfg, logger: logger}
	s.http = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout is slow-client protection for ordinary responses. Handlers
		// that legitimately run longer — SSE streaming, on-demand investigation,
		// the LLM explainer, and pod-log fetches — clear this deadline per
		// connection via clearWriteDeadline so it can't sever a valid response.
		WriteTimeout: 60 * time.Second,
	}
	return s, nil
}

// Start listens and serves until Shutdown is called.
func (s *Server) Start() error {
	s.logger.Info("api listening", "addr", s.cfg.Addr, "version", s.cfg.Version)
	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }
