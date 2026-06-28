// Package loki implements sources.LogSource against a Grafana Loki endpoint
// using LogQL over Loki's HTTP API.
package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// defaultLimit caps the number of log entries returned when LogQuery.Limit is 0.
const defaultLimit = 1000

// maxBody bounds how much of the Loki response we read, guarding against a
// runaway backend.
const maxBody = 16 * 1024 * 1024

// Client queries Loki. Runs inside the agent (the agent is the single egress
// point that can reach in-cluster Loki).
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New constructs a Loki client. A nil http.Client uses http.DefaultClient.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: hc}
}

func (c *Client) Name() string { return "loki" }

// lokiResponse is the Loki query_range "streams" envelope.
type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"` // [ [ts_ns, line], ... ]
		} `json:"result"`
	} `json:"data"`
}

// QueryLogs maps a ResourceRef + window to a LogQL query and returns log signals.
func (c *Client) QueryLogs(ctx context.Context, q sources.LogQuery) ([]model.Signal, error) {
	query := q.Query
	if strings.TrimSpace(query) == "" {
		query = selectorFor(q.Resource)
	}
	if query == "" {
		return nil, fmt.Errorf("loki: empty query and unresolvable resource %q", q.Resource.Key())
	}

	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("limit", strconv.Itoa(limit))
	if !q.Range.Start.IsZero() {
		params.Set("start", strconv.FormatInt(q.Range.Start.UnixNano(), 10))
	}
	if !q.Range.End.IsZero() {
		params.Set("end", strconv.FormatInt(q.Range.End.UnixNano(), 10))
	}

	endpoint := c.BaseURL + "/loki/api/v1/query_range?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("loki: create request: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("loki: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var lr lokiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&lr); err != nil {
		return nil, fmt.Errorf("loki: decode response: %w", err)
	}
	if lr.Status != "" && lr.Status != "success" {
		return nil, fmt.Errorf("loki: query status %q", lr.Status)
	}

	cluster := q.Resource.Cluster
	out := make([]model.Signal, 0, len(lr.Data.Result))
	for _, stream := range lr.Data.Result {
		ref := model.ResourceFromLabels(cluster, stream.Stream)
		for _, v := range stream.Values {
			tsNano, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("loki: parse timestamp %q: %w", v[0], err)
			}
			out = append(out, model.Signal{
				Kind:      model.SignalLog,
				Source:    "loki",
				Timestamp: time.Unix(0, tsNano).UTC(),
				Message:   v[1],
				Resource:  ref,
				Labels:    stream.Stream,
				Severity:  severityFromLabels(stream.Stream),
			})
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// selectorFor synthesizes a LogQL stream selector from a ResourceRef when no
// explicit query was provided.
func selectorFor(r model.ResourceRef) string {
	var sel []string
	if r.Namespace != "" {
		sel = append(sel, fmt.Sprintf("namespace=%q", r.Namespace))
	}
	if r.Name != "" {
		sel = append(sel, fmt.Sprintf("app=%q", r.Name))
	}
	if r.Pod != "" {
		sel = append(sel, fmt.Sprintf("pod=%q", r.Pod))
	}
	if len(sel) == 0 {
		return ""
	}
	return "{" + strings.Join(sel, ", ") + "}"
}

// severityFromLabels derives a coarse severity from a stream's level label, the
// Loki/Promtail convention for structured log levels.
func severityFromLabels(labels map[string]string) model.Severity {
	level := labels["level"]
	if level == "" {
		level = labels["detected_level"]
	}
	switch strings.ToLower(level) {
	case "fatal", "critical", "crit":
		return model.SeverityCritical
	case "error", "err":
		return model.SeverityError
	case "warn", "warning":
		return model.SeverityWarning
	default:
		return model.SeverityInfo
	}
}

var _ sources.LogSource = (*Client)(nil)
