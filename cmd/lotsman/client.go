package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// sessionCookie is the name of the control-plane session cookie. When --token
// is set it is sent as this cookie so the CLI authenticates against an
// SSO-protected server exactly as a browser session would.
const sessionCookie = "lotsman_session"

// csrfHeader is the custom header the server's CSRF defense requires on
// state-changing (non-GET) requests. It is harmless when SSO is disabled.
const csrfHeader = "X-Requested-With"

// Client is a thin HTTP client over the control-plane REST API. It targets a
// fixed base URL, applies a per-request timeout, and returns raw JSON bodies so
// callers can either pretty-print them (-o json) or decode into typed structs
// (-o table) without losing fidelity.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// newClient builds a Client for the given base URL, optional session token, and
// per-request timeout.
func newClient(base, token string, timeout time.Duration) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: timeout},
	}
}

// apiError is a non-2xx response from the control plane. It carries the HTTP
// status and the server's error message (from the JSON {"error":...} shape when
// present, else the raw body), and drives a non-zero process exit.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("server returned HTTP %d", e.status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.status, e.message)
}

// get issues a GET and returns the raw JSON body on 2xx.
func (c *Client) get(ctx context.Context, path string) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

// post issues a POST with a JSON body and returns the raw JSON body on 2xx.
func (c *Client) post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPost, path, body)
}

// do performs the request against the base URL, sets auth + CSRF headers, and
// maps the response to either the raw body (2xx) or an *apiError (non-2xx). A
// transport error (connection refused, timeout, DNS) is returned as-is.
func (c *Client) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// Always send the CSRF header; the server requires it on mutations when SSO
	// is enabled and ignores it otherwise.
	req.Header.Set(csrfHeader, "lotsman-cli")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.token})
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &apiError{status: resp.StatusCode, message: extractErrorMessage(raw)}
	}
	return json.RawMessage(raw), nil
}

// extractErrorMessage pulls a human message out of an error response. The API
// uses {"error":"..."} (writeError) but the auth middleware emits text/plain
// (http.Error), so fall back to the trimmed raw body.
func extractErrorMessage(raw []byte) string {
	var shape struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &shape); err == nil && shape.Error != "" {
		return shape.Error
	}
	return strings.TrimSpace(string(raw))
}
