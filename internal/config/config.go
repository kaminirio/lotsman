// Package config loads runtime configuration from the environment using an
// explicit struct, LOTSMAN_* env vars, and sane defaults.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

// Server is the control-plane configuration.
type Server struct {
	Addr        string // HTTP listen address for the REST API + embedded UI
	GatewayAddr string // gRPC listen address for incoming agent connections
	// AgentToken is the shared enrollment secret an agent must present in its
	// Hello to connect (LOTSMAN_AGENT_TOKEN). When empty, the gateway is
	// fail-closed by default (SEC-1): it refuses to start and rejects agents,
	// unless LOTSMAN_AGENT_ALLOW_INSECURE=1 re-enables the legacy local-dev
	// "accept any non-empty token" behavior. mTLS replaces this with cert-based
	// identity in production (ADR-0002). The gateway reads the insecure opt-in
	// directly from the environment, so no additional field is threaded here.
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

	// Auth configures first-party username/password accounts + optional SSO
	// (ADR-0011).
	Auth AuthConfig

	// PublicGatewayAddr is the externally reachable agent-gateway address the UI
	// shows operators in the enroll command (may differ from the in-cluster
	// GatewayAddr). AgentChart/AgentChartVersion pin the Helm chart the enroll
	// command references (ADR-0010). Presentation-only; never token material.
	PublicGatewayAddr string
	AgentChart        string
	AgentChartVersion string
}

// AuthConfig configures authentication (ADR-0011). Local username/password auth
// is always on; each SSO provider is offered only when its client id/secret (and
// Azure tenant) are set. AdminUser/AdminPassword idempotently seed the first
// admin on boot.
type AuthConfig struct {
	SessionSecret  string
	BaseURL        string   // control-plane origin for OAuth redirect URIs
	UIURL          string   // UI origin to return to after SSO login
	AllowedDomains []string // verified-email domains eligible for SSO auto-provision

	GitHubClientID     string
	GitHubClientSecret string
	GoogleClientID     string
	GoogleClientSecret string
	AzureClientID      string
	AzureClientSecret  string
	AzureTenant        string

	AdminUser     string
	AdminPassword string
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
		Auth: AuthConfig{
			SessionSecret:      os.Getenv("LOTSMAN_SESSION_SECRET"),
			BaseURL:            env("LOTSMAN_BASE_URL", "http://localhost:8080"),
			UIURL:              env("LOTSMAN_UI_URL", "http://localhost:3000"),
			AllowedDomains:     splitCSV(os.Getenv("LOTSMAN_ALLOWED_EMAIL_DOMAINS")),
			GitHubClientID:     os.Getenv("LOTSMAN_GITHUB_CLIENT_ID"),
			GitHubClientSecret: os.Getenv("LOTSMAN_GITHUB_CLIENT_SECRET"),
			GoogleClientID:     os.Getenv("LOTSMAN_GOOGLE_CLIENT_ID"),
			GoogleClientSecret: os.Getenv("LOTSMAN_GOOGLE_CLIENT_SECRET"),
			AzureClientID:      os.Getenv("LOTSMAN_AZURE_CLIENT_ID"),
			AzureClientSecret:  os.Getenv("LOTSMAN_AZURE_CLIENT_SECRET"),
			AzureTenant:        os.Getenv("LOTSMAN_AZURE_TENANT"),
			AdminUser:          os.Getenv("LOTSMAN_ADMIN_USER"),
			AdminPassword:      os.Getenv("LOTSMAN_ADMIN_PASSWORD"),
		},
		PublicGatewayAddr: env("LOTSMAN_PUBLIC_GATEWAY_ADDR", env("LOTSMAN_GATEWAY_ADDR", ":9090")),
		AgentChart:        env("LOTSMAN_AGENT_CHART", "oci://ghcr.io/kaminirio/charts/lotsman-agent"),
		AgentChartVersion: os.Getenv("LOTSMAN_AGENT_CHART_VERSION"),
	}
}

// splitCSV splits a comma-separated env value into a trimmed, non-empty slice.
// An empty input yields nil so an unset allowlist stays deny-by-default.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
// must be a well-formed http/https URL whose host is not — and does not resolve
// to — a cloud-metadata / link-local address, so a typo'd or hostile env var
// can't point a server-side fetch at instance metadata (169.254.169.254 and the
// rest of 169.254.0.0/16). SSRF hardening (SEC-5):
//
//   - IP literals are checked directly (net.ParseIP normalizes IPv4-mapped IPv6
//     forms like ::ffff:169.254.169.254, so those are covered too).
//   - well-known metadata hostnames (metadata.google.internal, ...) are rejected
//     by name.
//   - other hostnames are best-effort resolved; if any resolved address is
//     link-local — or is loopback for a name that isn't an explicit localhost —
//     the URL is rejected (a non-localhost name pointing at 127/8 is a red flag).
//
// This stays best-effort: these are operator-supplied URLs (low risk) and a
// resolution failure is not treated as fatal, so an air-gapped or DNS-less
// startup still validates. A loopback IP literal and the explicit localhost names
// remain allowed (local-dev backends like an in-pod Ollama).
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

	host := u.Hostname()
	if isMetadataHostname(host) {
		return fmt.Errorf("config: %s: refusing cloud-metadata hostname %q (SSRF risk)", name, host)
	}

	// IP literal: check it directly; loopback literals stay allowed for local dev.
	if ip := net.ParseIP(host); ip != nil {
		if reason := blockedIPReason(ip, false); reason != "" {
			return fmt.Errorf("config: %s: refusing %s %q (SSRF risk)", name, reason, host)
		}
		return nil
	}

	// Explicit localhost names are allowed even though they resolve to loopback.
	if isLocalhostName(host) {
		return nil
	}

	// Hostname: best-effort resolve and reject if it lands on a blocked address.
	ips, lookupErr := net.LookupIP(host)
	if lookupErr != nil {
		return nil // best-effort: don't fail validation on a resolution error
	}
	for _, ip := range ips {
		if reason := blockedIPReason(ip, true); reason != "" {
			return fmt.Errorf("config: %s: host %q resolves to %s %s (SSRF risk)", name, host, reason, ip)
		}
	}
	return nil
}

// blockedIPReason returns a human-readable reason if ip must be refused, or "".
// Link-local (which includes the 169.254.169.254 cloud-metadata address) is
// always blocked. Loopback is blocked only when resolved is true — a non-explicit
// hostname resolving to 127/8 is suspicious, while a loopback IP literal (or the
// explicit localhost names handled by the caller) is a legitimate local-dev target.
func blockedIPReason(ip net.IP, resolved bool) string {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link-local/metadata address"
	}
	if resolved && ip.IsLoopback() {
		return "loopback address"
	}
	return ""
}

// isMetadataHostname reports whether host is a well-known cloud instance-metadata
// hostname (case-insensitive).
func isMetadataHostname(host string) bool {
	switch strings.ToLower(host) {
	case "metadata.google.internal", "metadata", "instance-data", "instance-data.ec2.internal":
		return true
	}
	return false
}

// isLocalhostName reports whether host is an explicit localhost name that is
// deliberately allowed despite resolving to loopback (local-dev backends).
func isLocalhostName(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "localhost.localdomain":
		return true
	}
	return false
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
