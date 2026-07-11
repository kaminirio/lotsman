// Package controlplane wires the central server: the cluster registry, the
// correlation engine, the persistence store, the REST/UI API, and the agent
// gateway. cmd/server is a thin main around it.
package controlplane

import (
	"context"
	"fmt"
	"log/slog"

	"lotsman/internal/agentlink"
	"lotsman/internal/analyze"
	"lotsman/internal/api"
	"lotsman/internal/auth"
	"lotsman/internal/config"
	"lotsman/internal/engine"
	"lotsman/internal/events"
	"lotsman/internal/sources"
	"lotsman/internal/sources/argocd"
	"lotsman/internal/sources/kubernetes"
	"lotsman/internal/sources/loki"
	"lotsman/internal/sources/victoriametrics"
	"lotsman/internal/store"
)

// ControlPlane is the assembled central server.
type ControlPlane struct {
	cfg           config.Server
	logger        *slog.Logger
	registry      *Registry
	eng           *engine.Engine
	st            store.Store
	apiSrv        *api.Server
	gateway       *agentlink.Gateway
	bus           *events.IncidentBus
	scheduler     *Scheduler
	stopScheduler context.CancelFunc
}

// New assembles the control plane from configuration.
func New(ctx context.Context, cfg config.Server, logger *slog.Logger) (*ControlPlane, error) {
	// Validate operator-configured backend/LLM URLs up front (scheme + block
	// link-local metadata addresses) so a typo'd or hostile env var fails fast
	// rather than becoming a server-side request to an unexpected host.
	for name, raw := range map[string]string{
		"LOTSMAN_LOKI_URL":     cfg.LokiURL,
		"LOTSMAN_VICTORIA_URL": cfg.VictoriaURL,
		"LOTSMAN_ARGOCD_URL":   cfg.ArgoCDURL,
		"LOTSMAN_LLM_URL":      cfg.LLMBaseURL,
	} {
		if err := config.ValidateBackendURL(name, raw); err != nil {
			return nil, err
		}
	}

	registry := NewRegistry()
	registry.logger = logger
	// Bound every per-agent drain goroutine by the control-plane lifecycle ctx (the
	// signal-notify context from cmd/server, cancelled on SIGINT/SIGTERM). Without
	// this the backstop is inert in prod — NewRegistry defaults drainCtx to
	// context.Background(), so drains would only exit on link close. They still exit
	// on link close; this just adds the shutdown-bounded backstop.
	registry.drainCtx = ctx

	// Direct mode: build a concrete provider for the configured cluster so the
	// control plane can investigate the operator's own reachable stack with no
	// agent — the "first, solve my own stack" path.
	if cfg.DirectMode {
		kube, err := kubernetes.New(cfg.Cluster, cfg.KubeconfigPath)
		if err != nil {
			return nil, err
		}
		registry.AddDirect(sources.NewProvider(
			cfg.Cluster,
			loki.New(cfg.LokiURL, nil),
			victoriametrics.New(cfg.VictoriaURL, nil),
			argocd.New(cfg.ArgoCDURL, cfg.ArgoCDToken, nil),
			kube,
		))
		logger.Info("direct mode enabled", "cluster", cfg.Cluster)
	}

	eng := engine.New(registry, logger)

	// Persistence. With a DSN configured, use the durable pgx-backed store; the
	// in-memory store with seed data is the dev fallback (seed is for the UI only).
	var st store.Store
	if cfg.DatabaseURL != "" {
		pg, err := store.NewPostgres(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, err
		}
		st = pg
		logger.Info("persistence: postgres store active")
	} else {
		mem := store.NewMemory()
		if cfg.Seed {
			store.Seed(mem)
			logger.Info("persistence: in-memory store active (seeded)")
		} else {
			logger.Info("persistence: in-memory store active (no seed)")
		}
		st = mem
	}

	// Give the registry the store so live agent connect/disconnect (and the
	// direct-mode cluster below) persist their connection state, not just seeded
	// clusters. Set after the store exists but before the gateway can fire a
	// connect callback.
	registry.store = st
	if cfg.DirectMode {
		registry.persistCluster(cfg.Cluster, true)
	}

	gateway := agentlink.NewGateway(cfg.GatewayAddr, cfg.AgentToken, logger, registry.OnAgentConnect, registry.OnAgentDisconnect)

	// Incident bus fans detected/investigated incidents out to live SSE clients.
	bus := events.NewIncidentBus()

	// Auth. An empty SSO config keeps everyone Anonymous (local dev). A present
	// config enables GitHub OAuth + JWT sessions + RBAC. A present-but-INVALID
	// config is fatal: we refuse to start rather than silently degrade to the
	// anonymous-global-admin path (which would turn a config typo in production
	// into an unauthenticated-admin exposure). The empty-config dev path is
	// unchanged.
	authMgr, authErr := auth.NewManagerErr(cfg.SSOConfig, logger)
	if authErr != nil {
		if cfg.SSOConfig != "" {
			return nil, fmt.Errorf("controlplane: SSO config supplied but invalid (refusing to start in anonymous mode): %w", authErr)
		}
		logger.Error("SSO config error", "error", authErr)
	}
	if authMgr.Enabled() {
		for _, warn := range authMgr.Config().Warnings() {
			logger.Warn("SSO config warning", "warning", warn)
		}
		logger.Info("auth: GitHub OAuth + JWT sessions enabled")
	}

	// Optional LLM incident-explainer. Disabled (Available()==false) when no LLM
	// URL is configured, in which case the explain endpoint responds 503 and the
	// rest of the control plane is unaffected.
	explainer := analyze.NewOllama(cfg.LLMBaseURL, cfg.LLMModel, nil)
	if explainer.Available() {
		logger.Info("LLM incident-explainer enabled", "url", cfg.LLMBaseURL, "model", cfg.LLMModel)
	} else {
		logger.Info("LLM incident-explainer disabled (no LOTSMAN_LLM_URL)")
	}

	apiSrv, err := api.New(api.Config{
		Addr:      cfg.Addr,
		Version:   cfg.Version,
		Engine:    eng,
		Store:     st,
		Auth:      authMgr,
		Bus:       bus,
		Sources:   registry,
		Explainer: explainer,
	}, logger)
	if err != nil {
		return nil, err
	}

	scheduler := NewScheduler(registry, eng, st, bus, cfg.ScanInterval, logger)

	return &ControlPlane{
		cfg:       cfg,
		logger:    logger,
		registry:  registry,
		eng:       eng,
		st:        st,
		apiSrv:    apiSrv,
		gateway:   gateway,
		bus:       bus,
		scheduler: scheduler,
	}, nil
}

// Start runs the agent gateway and the detector scheduler (both background) and
// the API server (blocking) until ctx is cancelled or the API stops. The
// scheduler goroutine is tied to schedCtx, cancelled in Shutdown so it exits
// without leaking.
func (c *ControlPlane) Start(ctx context.Context) error {
	schedCtx, cancel := context.WithCancel(ctx)
	c.stopScheduler = cancel
	go c.scheduler.Run(schedCtx)

	go func() {
		if err := c.gateway.Start(ctx); err != nil {
			c.logger.Error("agent gateway stopped", "err", err)
		}
	}()
	return c.apiSrv.Start()
}

// Shutdown stops the scheduler, gateway, and API server.
func (c *ControlPlane) Shutdown(ctx context.Context) error {
	if c.stopScheduler != nil {
		c.stopScheduler()
	}
	_ = c.gateway.Shutdown(ctx)
	err := c.apiSrv.Shutdown(ctx)
	// Release the persistence pool last, once nothing is querying it. Only the
	// pgx store implements Close; the in-memory store does not.
	if closer, ok := c.st.(interface{ Close() }); ok {
		closer.Close()
	}
	return err
}
