// Package agent is the in-cluster agent. It builds concrete source adapters for
// the cluster it runs in, dials OUT to the control plane, and serves proxied
// source queries against the local backends. It is the agent-side counterpart to
// internal/sources/remote.
package agent

import (
	"context"
	"encoding/json"
	"log/slog"

	"lotsman/internal/agentlink"
	"lotsman/internal/config"
	"lotsman/internal/model"
	"lotsman/internal/sources"
	"lotsman/internal/sources/argocd"
	"lotsman/internal/sources/kubernetes"
	"lotsman/internal/sources/loki"
	"lotsman/internal/sources/victoriametrics"
)

// Agent runs inside a cluster.
type Agent struct {
	cfg      config.Agent
	logger   *slog.Logger
	provider sources.Provider
	dialer   *agentlink.Dialer
}

// New constructs the agent and its concrete source adapters.
func New(cfg config.Agent, logger *slog.Logger) (*Agent, error) {
	// Validate configured backend URLs (scheme + block link-local metadata) so a
	// bad env var fails fast instead of issuing requests to an unexpected host.
	for name, raw := range map[string]string{
		"LOTSMAN_LOKI_URL":     cfg.LokiURL,
		"LOTSMAN_VICTORIA_URL": cfg.VictoriaURL,
		"LOTSMAN_ARGOCD_URL":   cfg.ArgoCDURL,
	} {
		if err := config.ValidateBackendURL(name, raw); err != nil {
			return nil, err
		}
	}
	kube, err := kubernetes.New(cfg.Cluster, "") // in-cluster config
	if err != nil {
		return nil, err
	}
	provider := sources.NewProvider(
		cfg.Cluster,
		loki.New(cfg.LokiURL, nil),
		victoriametrics.New(cfg.VictoriaURL, nil),
		argocd.New(cfg.ArgoCDURL, cfg.ArgoCDToken, nil),
		kube,
	)
	return &Agent{
		cfg:      cfg,
		logger:   logger,
		provider: provider,
		dialer: agentlink.NewDialer(cfg.ControlPlaneAddr, cfg.Token, logger).
			WithIdentity(cfg.Cluster, cfg.Version, []string{"loki", "victoriametrics", "argocd", "kubernetes"}),
	}, nil
}

// Run dials the control plane and serves until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info("agent starting", "cluster", a.cfg.Cluster, "control_plane", a.cfg.ControlPlaneAddr)
	return a.dialer.Run(ctx, a.handle)
}

// handle executes a proxied request from the control plane against the local
// provider, mirroring the request kinds in sources/remote.
func (a *Agent) handle(ctx context.Context, req agentlink.Request) agentlink.Response {
	switch req.Kind {
	case agentlink.ReqQueryLogs:
		var q sources.LogQuery
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Logs().QueryLogs(ctx, q))

	case agentlink.ReqQueryMetrics:
		var q sources.MetricQuery
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Metrics().QueryInstant(ctx, q))

	case agentlink.ReqQueryRange:
		var q sources.MetricRangeQuery
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Metrics().QueryRange(ctx, q))

	case agentlink.ReqChangeEvents:
		var q sources.ChangeQuery
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Deployments().ChangeEvents(ctx, q))

	case agentlink.ReqK8sEvents:
		var q sources.EventQuery
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Resources().Events(ctx, q))

	case agentlink.ReqListWorkloads:
		var ns string
		if err := json.Unmarshal(req.Payload, &ns); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Resources().ListWorkloads(ctx, ns))

	case agentlink.ReqListNodes:
		// Nodes are cluster-scoped and take no query payload (an empty struct is
		// sent over the wire); ignore req.Payload.
		return respond(a.provider.Resources().ListNodes(ctx))

	case agentlink.ReqListPods:
		var q sources.PodQuery
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		// Defense in depth: never trust the wire Reveal flag. The control plane's
		// admin check decides whether to *ask* for resolved secret env values, but
		// an agent only honors it when explicitly opted in via
		// LOTSMAN_ALLOW_ENV_REVEAL (and the matching secrets/configmaps RBAC). A
		// compromised or over-eager control plane therefore cannot make an
		// un-opted-in agent read Secrets. Direct mode (control plane in-process,
		// no agent) does not pass through here and stays governed by the admin
		// check alone.
		if !a.cfg.AllowEnvReveal {
			q.Reveal = false
		}
		return respond(a.provider.Resources().ListPods(ctx, q))

	case agentlink.ReqPodLogs:
		var q sources.PodLogsQuery
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Resources().PodLogs(ctx, q))

	case agentlink.ReqWorkloadHistory:
		var ref model.ResourceRef
		if err := json.Unmarshal(req.Payload, &ref); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		// WorkloadHistory is an optional capability; report cleanly if this
		// provider's ClusterSource doesn't implement it.
		hist, ok := a.provider.Resources().(sources.WorkloadHistorian)
		if !ok {
			return agentlink.Response{Err: "workload history not supported by this source"}
		}
		return respond(hist.WorkloadHistory(ctx, ref))

	case agentlink.ReqListConfigMaps:
		var ns string
		if err := json.Unmarshal(req.Payload, &ns); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Resources().ListConfigMaps(ctx, ns))

	case agentlink.ReqGetConfigMap:
		var ref model.ResourceRef
		if err := json.Unmarshal(req.Payload, &ref); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Resources().GetConfigMap(ctx, ref))

	case agentlink.ReqListSecrets:
		var ns string
		if err := json.Unmarshal(req.Payload, &ns); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		return respond(a.provider.Resources().ListSecrets(ctx, ns))

	case agentlink.ReqGetSecret:
		var q sources.SecretQuery
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return agentlink.Response{Err: err.Error()}
		}
		// Same defense-in-depth gate as ListPods: a wire Reveal=true can never
		// force this agent to expose secret values unless it was explicitly opted
		// in via LOTSMAN_ALLOW_ENV_REVEAL (and granted the secrets RBAC). Public
		// certificate metadata is still returned either way.
		if !a.cfg.AllowEnvReveal {
			q.Reveal = false
		}
		return respond(a.provider.Resources().GetSecret(ctx, q))

	default:
		return agentlink.Response{Err: "unknown request kind: " + string(req.Kind)}
	}
}

// respond marshals a source result (or surfaces its error) into a link Response.
func respond[T any](v T, err error) agentlink.Response {
	if err != nil {
		return agentlink.Response{Err: err.Error()}
	}
	payload, mErr := json.Marshal(v)
	if mErr != nil {
		return agentlink.Response{Err: mErr.Error()}
	}
	return agentlink.Response{Payload: payload}
}
