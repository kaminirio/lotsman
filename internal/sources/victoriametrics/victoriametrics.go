// Package victoriametrics implements sources.MetricSource against VictoriaMetrics
// using the Prometheus-compatible HTTP API. Because the API is Prometheus's, a
// vanilla Prometheus is a drop-in alternative behind the same adapter.
package victoriametrics

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

	"lotsman/internal/sources"
)

// maxBody bounds how much of a metrics response we read.
const maxBody = 32 * 1024 * 1024

// defaultTimeout is applied to the fallback HTTP client so a hung metrics backend
// cannot stall an investigation indefinitely. Production callers construct this
// adapter with a nil client, so the timeout must live at the adapter level.
const defaultTimeout = 30 * time.Second

// Client queries VictoriaMetrics (vmselect) or Prometheus. Runs inside the agent.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New constructs a metrics client. A nil http.Client falls back to a client with
// a sane default timeout (defaultTimeout) rather than http.DefaultClient, which
// has none — production callers pass nil.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: hc}
}

func (c *Client) Name() string { return "victoriametrics" }

// promResponse is the Prometheus HTTP API envelope. A vector result carries a
// single "value" per series; a matrix result carries "values".
type promResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  promSample        `json:"value"`  // vector: [ts, "val"]
			Values []promSample      `json:"values"` // matrix: [[ts, "val"], ...]
		} `json:"result"`
	} `json:"data"`
}

// promSample is a [unix_seconds(float), value(string)] pair.
type promSample [2]json.RawMessage

func (s promSample) point() (sources.MetricPoint, bool, error) {
	if len(s[0]) == 0 || len(s[1]) == 0 {
		return sources.MetricPoint{}, false, nil
	}
	var ts float64
	if err := json.Unmarshal(s[0], &ts); err != nil {
		return sources.MetricPoint{}, false, fmt.Errorf("victoriametrics: parse timestamp: %w", err)
	}
	var valStr string
	if err := json.Unmarshal(s[1], &valStr); err != nil {
		return sources.MetricPoint{}, false, fmt.Errorf("victoriametrics: parse value: %w", err)
	}
	v, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		// Prometheus emits "NaN"/"+Inf"/"-Inf" as strings; ParseFloat handles
		// them, but guard anyway.
		return sources.MetricPoint{}, false, fmt.Errorf("victoriametrics: parse float %q: %w", valStr, err)
	}
	secs := int64(ts)
	nanos := int64((ts - float64(secs)) * float64(time.Second))
	return sources.MetricPoint{T: time.Unix(secs, nanos).UTC(), V: v}, true, nil
}

// QueryInstant evaluates PromQL at a single instant.
func (c *Client) QueryInstant(ctx context.Context, q sources.MetricQuery) (sources.MetricResult, error) {
	params := url.Values{}
	params.Set("query", q.PromQL)
	if !q.At.IsZero() {
		params.Set("time", formatUnix(q.At))
	}
	resp, err := c.fetch(ctx, "/api/v1/query", params)
	if err != nil {
		return sources.MetricResult{}, err
	}
	return buildResult(resp, false)
}

// QueryRange evaluates PromQL over a window at a fixed step.
func (c *Client) QueryRange(ctx context.Context, q sources.MetricRangeQuery) (sources.MetricResult, error) {
	step := q.Step
	if step <= 0 {
		step = time.Minute
	}
	params := url.Values{}
	params.Set("query", q.PromQL)
	params.Set("start", formatUnix(q.Range.Start))
	params.Set("end", formatUnix(q.Range.End))
	params.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))
	resp, err := c.fetch(ctx, "/api/v1/query_range", params)
	if err != nil {
		return sources.MetricResult{}, err
	}
	return buildResult(resp, true)
}

// fetch issues a GET to the Prometheus HTTP API and decodes the envelope,
// surfacing a non-success status as an error.
func (c *Client) fetch(ctx context.Context, path string, params url.Values) (*promResponse, error) {
	endpoint := c.BaseURL + path + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("victoriametrics: create request: %w", err)
	}
	resp, err := sources.DoWithRetry(c.HTTP, req)
	if err != nil {
		return nil, fmt.Errorf("victoriametrics: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("victoriametrics: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var pr promResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&pr); err != nil {
		return nil, fmt.Errorf("victoriametrics: decode response: %w", err)
	}
	if pr.Status != "success" {
		if pr.Error != "" {
			return nil, fmt.Errorf("victoriametrics: query %s: %s", pr.ErrorType, pr.Error)
		}
		return nil, fmt.Errorf("victoriametrics: query status %q", pr.Status)
	}
	return &pr, nil
}

// buildResult flattens a Prometheus envelope into a MetricResult. When matrix is
// true each series carries multiple points (values); otherwise a single point.
func buildResult(pr *promResponse, matrix bool) (sources.MetricResult, error) {
	out := sources.MetricResult{Series: make([]sources.MetricSeries, 0, len(pr.Data.Result))}
	for _, r := range pr.Data.Result {
		series := sources.MetricSeries{Labels: r.Metric}
		if matrix {
			series.Points = make([]sources.MetricPoint, 0, len(r.Values))
			for _, s := range r.Values {
				p, ok, err := s.point()
				if err != nil {
					return sources.MetricResult{}, err
				}
				if ok {
					series.Points = append(series.Points, p)
				}
			}
		} else {
			p, ok, err := r.Value.point()
			if err != nil {
				return sources.MetricResult{}, err
			}
			if ok {
				series.Points = append(series.Points, p)
			}
		}
		out.Series = append(out.Series, series)
	}
	return out, nil
}

// formatUnix renders a time as unix seconds with fractional precision, the form
// the Prometheus HTTP API expects for time/start/end.
func formatUnix(t time.Time) string {
	if t.IsZero() {
		return strconv.FormatFloat(float64(time.Now().Unix()), 'f', -1, 64)
	}
	return strconv.FormatFloat(float64(t.UnixNano())/float64(time.Second), 'f', -1, 64)
}

var _ sources.MetricSource = (*Client)(nil)
