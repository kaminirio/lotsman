// Package config loads runtime configuration from the environment using an
// explicit struct, LOTSMAN_* env vars, and sane defaults.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"time"
)

// Server is the control-plane configuration.
type Server struct {
	Addr        string // HTTP listen address for the REST API + embedded UI
	GatewayAddr string // gRPC listen address for incoming agent connections
	// AgentToken is the shared enrollment secret an agent must present in its
	// Hello to connect (LOTSMAN_AGENT_TOKEN). When empty, the gateway accepts any
	// non-empty token (local-dev convenience) and logs a warning. mTLS replaces
	// this with cert-based identity in production (ADR-0002).
	AgentToken  string
	DatabaseURL string // PostgreSQL DSN for control-plane state
	SSOConfig   string // optional JSON SSO config (GitHub OAuth)
	// DirectMode, when set, makes the control plane talk to a single cluster's
	// backends directly (no agent) using the URLs below. This is the
	// agentless "solve my own stack" mode and the default for local dev.
	DirectMode     bool
	Cluster        string
	LokiURL        string
	VictoriaURL    string
	ArgoCDURL      string
	ArgoCDToken    string
	KubeconfigPath string
	// ScanInterval is how often the detector scheduler scans every registered
	// cluster for new incidents (LOTSMAN_SCAN_INTERVAL, Go duration). Default 30s.
	ScanInterval time.Duration
	// LLMBaseURL enables the OPTIONAL, off-by-default LLM incident-explainer
	// (LOTSMAN_LLM_URL, e.g. http://ollama.lotsman.svc:11434). Empty disables it:
	// the explainer is unavailable and the explain endpoint responds 503. The LLM
	// is assistive only and never on the detection hot path.
	LLMBaseURL string
	// LLMModel is the model the explainer requests (LOTSMAN_LLM_MODEL). Default
	// "gemma3:4b".
	LLMModel string
	// Seed loads demo/sample data (sample clusters + a sample incident) into the
	// in-memory store for local dev (LOTSMAN_SEED, default true). Set false in a
	// real deployment so the cluster list shows only actually-connected clusters.
	// Ignored when a Postgres DSN is configured.
	Seed    bool
	Version string
}

// Agent is the in-cluster agent configuration. The agent dials OUT to the
// control plane (egress-only, NAT/firewall friendly — see ADR-0001).
type Agent struct {
	ControlPlaneAddr string // gRPC address of the control plane to dial
	Cluster          string // logical cluster name this agent represents
	Token            string // bootstrap/enrollment token (mTLS in production)
	LokiURL          string
	VictoriaURL      string
	ArgoCDURL        string
	ArgoCDToken      string
	// AllowEnvReveal opts this agent in to resolving Secret/ConfigMap-sourced env
	// values when the control plane asks (LOTSMAN_ALLOW_ENV_REVEAL). Default false:
	// the agent ignores the wire Reveal flag and never resolves secrets unless an
	// operator explicitly enables it AND grants the secrets/configmaps RBAC (see
	// deploy/local/k8s/21-agent-rbac-reveal.yaml). This is a defense-in-depth gate
	// independent of the control plane's admin check.
	AllowEnvReveal bool
	Version        string
}

// LoadServer reads the control-plane config from the environment.
func LoadServer(version string) Server {
	return Server{
		Addr:           env("LOTSMAN_ADDR", ":8080"),
		GatewayAddr:    env("LOTSMAN_GATEWAY_ADDR", ":9090"),
		AgentToken:     os.Getenv("LOTSMAN_AGENT_TOKEN"),
		DatabaseURL:    os.Getenv("LOTSMAN_DATABASE_URL"),
		SSOConfig:      os.Getenv("LOTSMAN_SSO_CONFIG"),
		DirectMode:     os.Getenv("LOTSMAN_DIRECT_MODE") == "1",
		Cluster:        env("LOTSMAN_CLUSTER", "default"),
		LokiURL:        os.Getenv("LOTSMAN_LOKI_URL"),
		VictoriaURL:    os.Getenv("LOTSMAN_VICTORIA_URL"),
		ArgoCDURL:      os.Getenv("LOTSMAN_ARGOCD_URL"),
		ArgoCDToken:    os.Getenv("LOTSMAN_ARGOCD_TOKEN"),
		KubeconfigPath: os.Getenv("LOTSMAN_KUBECONFIG"),
		ScanInterval:   envDuration("LOTSMAN_SCAN_INTERVAL", 30*time.Second),
		LLMBaseURL:     os.Getenv("LOTSMAN_LLM_URL"),
		LLMModel:       env("LOTSMAN_LLM_MODEL", "gemma3:4b"),
		Seed:           envBool("LOTSMAN_SEED", true),
		Version:        version,
	}
}

// LoadAgent reads the agent config from the environment.
func LoadAgent(version string) Agent {
	return Agent{
		ControlPlaneAddr: env("LOTSMAN_CONTROL_PLANE_ADDR", "localhost:9090"),
		Cluster:          env("LOTSMAN_CLUSTER", "default"),
		Token:            os.Getenv("LOTSMAN_AGENT_TOKEN"),
		LokiURL:          env("LOTSMAN_LOKI_URL", "http://loki-gateway.monitoring.svc:80"),
		VictoriaURL:      env("LOTSMAN_VICTORIA_URL", "http://vmselect.monitoring.svc:8481"),
		ArgoCDURL:        env("LOTSMAN_ARGOCD_URL", "http://argocd-server.argocd.svc:80"),
		ArgoCDToken:      os.Getenv("LOTSMAN_ARGOCD_TOKEN"),
		AllowEnvReveal:   envBool("LOTSMAN_ALLOW_ENV_REVEAL", false),
		Version:          version,
	}
}

// ValidateBackendURL checks an operator-configured backend/LLM URL at startup.
// An empty value is allowed (the source is simply disabled). A non-empty value
// must be a well-formed http/https URL whose host is not a link-local address —
// blocking 169.254.169.254 and the rest of 169.254.0.0/16 so a typo'd or hostile
// env var can't point a server-side fetch at cloud instance metadata (SSRF
// hardening). Loopback is allowed (local-dev backends like an in-pod Ollama).
func ValidateBackendURL(name, raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: %s: invalid URL %q: %w", name, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("config: %s: URL scheme must be http or https, got %q", name, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("config: %s: URL %q has no host", name, raw)
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("config: %s: refusing link-local address %q (cloud metadata SSRF risk)", name, u.Hostname())
		}
	}
	return nil
}

// envBool reports whether key is set to a truthy value ("1" or "true"). Anything
// else, including unset, is false — secret-sensitive flags must default OFF.
func envBool(key string, def bool) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	case "0", "false", "FALSE", "False", "no", "off":
		return false
	default:
		return def
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envDuration parses a Go duration from an env var, falling back to def on an
// empty or invalid value.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
