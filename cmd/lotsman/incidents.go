package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

// newIncidentsCmd is the parent for incident inspection subcommands.
func newIncidentsCmd(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "incidents",
		Short: "Inspect incidents",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newIncidentsListCmd(opts), newIncidentsGetCmd(opts))
	return cmd
}

// newIncidentsListCmd lists incidents (GET /api/v1/incidents) with optional
// pagination and cluster/status filters.
func newIncidentsListCmd(opts *globalOptions) *cobra.Command {
	var (
		limit   int
		offset  int
		cluster string
		status  string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List incidents (GET /api/v1/incidents)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			if offset > 0 {
				q.Set("offset", strconv.Itoa(offset))
			}
			if cluster != "" {
				q.Set("cluster", cluster)
			}
			if status != "" {
				q.Set("status", status)
			}
			path := "/api/v1/incidents"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			raw, err := opts.client().get(cmd.Context(), path)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return renderJSON(cmd.OutOrStdout(), raw)
			}
			var incs []incident
			if err := json.Unmarshal(raw, &incs); err != nil {
				return fmt.Errorf("decode incidents: %w", err)
			}
			return renderIncidentList(cmd.OutOrStdout(), incs)
		},
	}
	f := cmd.Flags()
	f.IntVar(&limit, "limit", 0, "Maximum number of incidents to return")
	f.IntVar(&offset, "offset", 0, "Number of incidents to skip (pagination)")
	f.StringVar(&cluster, "cluster", "", "Filter by cluster name")
	f.StringVar(&status, "status", "", "Filter by status (open|investigating|resolved|closed)")
	return cmd
}

// newIncidentsGetCmd fetches a single incident (GET /api/v1/incidents/{id}).
func newIncidentsGetCmd(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single incident (GET /api/v1/incidents/{id})",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/incidents/" + url.PathEscape(args[0])
			raw, err := opts.client().get(cmd.Context(), path)
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
}
