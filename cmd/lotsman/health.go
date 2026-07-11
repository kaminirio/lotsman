package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newHealthCmd probes the control plane's GET /healthz.
func newHealthCmd(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check control-plane health (GET /healthz)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := opts.client().get(cmd.Context(), "/healthz")
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return renderJSON(cmd.OutOrStdout(), raw)
			}
			var body struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				return fmt.Errorf("decode health response: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "status: %s\n", dash(body.Status))
			return nil
		},
	}
}
