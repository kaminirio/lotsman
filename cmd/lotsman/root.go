package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// Environment variables consulted as fallbacks for the persistent flags. An
// explicit flag always wins; the env var only supplies the default.
const (
	envServer  = "LOTSMAN_SERVER"
	envToken   = "LOTSMAN_TOKEN"
	envOutput  = "LOTSMAN_OUTPUT"
	envTimeout = "LOTSMAN_TIMEOUT"
)

const (
	defaultServer  = "http://localhost:8080"
	defaultOutput  = outputTable
	defaultTimeout = 30 * time.Second
)

// Output formats.
const (
	outputTable = "table"
	outputJSON  = "json"
)

// globalOptions holds the resolved persistent flags shared by every subcommand.
// It is populated by the root command's flags (with env fallbacks) and read by
// each subcommand to build its HTTP client.
type globalOptions struct {
	server  string
	token   string
	output  string
	timeout time.Duration
}

// client builds an HTTP client for the control-plane API from the resolved
// options.
func (o *globalOptions) client() *Client {
	return newClient(o.server, o.token, o.timeout)
}

// validate checks the resolved options before any subcommand runs.
func (o *globalOptions) validate() error {
	switch o.output {
	case outputTable, outputJSON:
	default:
		return fmt.Errorf("invalid --output %q (want %q or %q)", o.output, outputTable, outputJSON)
	}
	if o.timeout <= 0 {
		return fmt.Errorf("invalid --timeout %s (must be positive)", o.timeout)
	}
	if o.server == "" {
		return fmt.Errorf("empty --server")
	}
	return nil
}

// newRootCmd assembles the full command tree.
func newRootCmd() *cobra.Command {
	opts := &globalOptions{}

	root := &cobra.Command{
		Use:   "lotsman",
		Short: "Operator CLI for the Lotsman control plane",
		Long: "lotsman is the operator CLI for the Lotsman Kubernetes monitoring &\n" +
			"investigation control plane. It talks to the control-plane REST API to run\n" +
			"investigations and inspect incidents and clusters from the terminal.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Resolve env fallbacks and validate once, before any subcommand runs.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// version is a leaf that needs no server; still cheap to validate.
			return opts.validate()
		},
	}

	// Persistent flags, defaulted from the environment so LOTSMAN_* works without
	// repeating flags. An explicit flag on the command line overrides the env.
	f := root.PersistentFlags()
	f.StringVar(&opts.server, "server", envOr(envServer, defaultServer),
		"Control-plane base URL (env "+envServer+")")
	f.StringVar(&opts.token, "token", os.Getenv(envToken),
		"Session token for an SSO-protected server (env "+envToken+")")
	f.StringVarP(&opts.output, "output", "o", envOr(envOutput, defaultOutput),
		"Output format: table|json (env "+envOutput+")")
	f.DurationVar(&opts.timeout, "timeout", envDurationOr(envTimeout, defaultTimeout),
		"Per-request timeout (env "+envTimeout+")")

	root.AddCommand(
		newVersionCmd(opts),
		newHealthCmd(opts),
		newInvestigateCmd(opts),
		newIncidentsCmd(opts),
		newClustersCmd(opts),
	)
	return root
}

// envOr returns the environment variable value or the fallback if unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envDurationOr parses a duration from the environment, falling back on unset or
// unparseable values (a bad env var should not hard-fail flag registration; the
// user can still override with --timeout).
func envDurationOr(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
