package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newClustersCmd is the parent for cluster inspection subcommands.
func newClustersCmd(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clusters",
		Short: "Inspect clusters",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newClustersListCmd(opts))
	return cmd
}

// newClustersListCmd lists known clusters (GET /api/v1/clusters).
func newClustersListCmd(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List clusters (GET /api/v1/clusters)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := opts.client().get(cmd.Context(), "/api/v1/clusters")
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return renderJSON(cmd.OutOrStdout(), raw)
			}
			var cs []cluster
			if err := json.Unmarshal(raw, &cs); err != nil {
				return fmt.Errorf("decode clusters: %w", err)
			}
			return renderClusterList(cmd.OutOrStdout(), cs)
		},
	}
}
