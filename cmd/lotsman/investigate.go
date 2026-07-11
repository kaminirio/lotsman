package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newInvestigateCmd runs an on-demand investigation (POST /api/v1/investigate)
// and prints the ranked incident it produces.
func newInvestigateCmd(opts *globalOptions) *cobra.Command {
	var req struct {
		Cluster   string `json:"cluster"`
		Namespace string `json:"namespace"`
		Kind      string `json:"kind"`
		Name      string `json:"name"`
	}

	cmd := &cobra.Command{
		Use:   "investigate",
		Short: "Run an investigation for a resource and print the ranked incident",
		Long: "investigate runs a live multi-source investigation for a single\n" +
			"Kubernetes resource and prints the resulting incident with its top\n" +
			"ranked probable cause. Use -o json for the full timeline + hypotheses.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := opts.client().post(cmd.Context(), "/api/v1/investigate", req)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return renderJSON(cmd.OutOrStdout(), raw)
			}
			var inc incident
			if err := json.Unmarshal(raw, &inc); err != nil {
				return fmt.Errorf("decode incident: %w", err)
			}
			return renderIncidentSummary(cmd.OutOrStdout(), inc)
		},
	}

	f := cmd.Flags()
	f.StringVar(&req.Cluster, "cluster", "", "Cluster name (required)")
	f.StringVar(&req.Namespace, "namespace", "", "Namespace (required)")
	f.StringVar(&req.Kind, "kind", "", "Resource kind, e.g. Deployment (required)")
	f.StringVar(&req.Name, "name", "", "Resource name (required)")
	_ = cmd.MarkFlagRequired("cluster")
	_ = cmd.MarkFlagRequired("namespace")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
