package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// runCLI executes the root command with args, capturing stdout+stderr and the
// error, exactly as main() would (minus the os.Exit).
func runCLI(args ...string) (string, error) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestClient_Get_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get(csrfHeader) == "" {
			t.Errorf("expected %s header set", csrfHeader)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, "", 5*time.Second)
	raw, err := c.get(context.Background(), "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var body struct{ Status string }
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

func TestClient_Post_SendsBodyAndToken(t *testing.T) {
	var gotBody map[string]string
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		if ck, err := r.Cookie(sessionCookie); err == nil {
			gotCookie = ck.Value
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"inc-9"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, "tok-123", 5*time.Second)
	raw, err := c.post(context.Background(), "/api/v1/investigate", map[string]string{"cluster": "prod"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if gotBody["cluster"] != "prod" {
		t.Errorf("server got body %v, want cluster=prod", gotBody)
	}
	if gotCookie != "tok-123" {
		t.Errorf("session cookie = %q, want tok-123", gotCookie)
	}
	if !strings.Contains(string(raw), "inc-9") {
		t.Errorf("unexpected response %s", raw)
	}
}

func TestClient_Non2xx_JSONError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"forbidden: cluster not visible"}`)
	}))
	defer srv.Close()

	c := newClient(srv.URL, "", 5*time.Second)
	_, err := c.get(context.Background(), "/api/v1/incidents")
	if err == nil {
		t.Fatal("expected error on 403")
	}
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *apiError, got %T: %v", err, err)
	}
	if apiErr.status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", apiErr.status)
	}
	if !strings.Contains(apiErr.Error(), "forbidden: cluster not visible") {
		t.Errorf("error missing server message: %v", apiErr)
	}
}

func TestClient_Non2xx_PlainTextError(t *testing.T) {
	// The auth middleware emits text/plain via http.Error, not the JSON shape.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newClient(srv.URL, "", 5*time.Second)
	_, err := c.get(context.Background(), "/api/v1/incidents")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("expected plain-text body surfaced, got: %v", err)
	}
}

func TestClient_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening

	c := newClient(url, "", 1*time.Second)
	_, err := c.get(context.Background(), "/healthz")
	if err == nil {
		t.Fatal("expected transport error against closed server")
	}
}

func TestInvestigateCmd_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/investigate" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"inc-42","title":"checkout down","status":"open","severity":3,
			"hypotheses":[{"summary":"bad deploy","confidence":0.8,"category":"deploy"}]}`)
	}))
	defer srv.Close()

	out, err := runCLI("--server", srv.URL,
		"investigate", "--cluster", "prod", "--namespace", "shop", "--kind", "Deployment", "--name", "checkout")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{"inc-42", "checkout down", "open", "critical", "bad deploy (80%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestInvestigateCmd_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"inc-42","timeline":[{"id":"s1"}]}`)
	}))
	defer srv.Close()

	out, err := runCLI("--server", srv.URL, "-o", "json",
		"investigate", "--cluster", "prod", "--namespace", "shop", "--kind", "Deployment", "--name", "checkout")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	// JSON mode passes the raw body through (timeline preserved, pretty-printed).
	if !strings.Contains(out, "timeline") || !strings.Contains(out, "\n  \"id\"") {
		t.Errorf("expected pretty JSON with timeline, got:\n%s", out)
	}
}

func TestInvestigateCmd_MissingRequiredFlag(t *testing.T) {
	out, err := runCLI("investigate", "--cluster", "prod")
	if err == nil {
		t.Fatalf("expected error for missing required flags, out:\n%s", out)
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("expected required-flag error, got: %v", err)
	}
}

func TestIncidentsListCmd_Table_WithPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("limit query = %q, want 5", got)
		}
		if got := r.URL.Query().Get("offset"); got != "10" {
			t.Errorf("offset query = %q, want 10", got)
		}
		_, _ = io.WriteString(w, `[{"id":"a","resource":{"cluster":"prod"},"title":"one","status":"open","severity":2}]`)
	}))
	defer srv.Close()

	out, err := runCLI("--server", srv.URL, "incidents", "list", "--limit", "5", "--offset", "10")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{"a", "prod", "one", "open", "error"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestIncidentsGetCmd_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/incidents/inc-7" {
			t.Errorf("path = %s, want /api/v1/incidents/inc-7", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"inc-7","title":"db down","status":"investigating","severity":2}`)
	}))
	defer srv.Close()

	out, err := runCLI("--server", srv.URL, "incidents", "get", "inc-7")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "inc-7") || !strings.Contains(out, "investigating") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestClustersListCmd_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"name":"prod","env":"production","region":"us-east","connected":true,"mode":"connected"}]`)
	}))
	defer srv.Close()

	out, err := runCLI("--server", srv.URL, "clusters", "list")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{"prod", "production", "us-east", "true", "connected"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestCmd_Non2xx_ReturnsError is the exit-code path: a non-2xx from the server
// must surface as a non-nil error from Execute (which main turns into a
// non-zero exit).
func TestCmd_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"backend timeout"}`)
	}))
	defer srv.Close()

	out, err := runCLI("--server", srv.URL,
		"investigate", "--cluster", "prod", "--namespace", "shop", "--kind", "Deployment", "--name", "checkout")
	if err == nil {
		t.Fatalf("expected error on 502, out:\n%s", out)
	}
	if !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "backend timeout") {
		t.Errorf("expected 502 + server message, got: %v", err)
	}
}

func TestInvalidOutputFlag(t *testing.T) {
	_, err := runCLI("-o", "yaml", "version")
	if err == nil {
		t.Fatal("expected error for invalid --output")
	}
	if !strings.Contains(err.Error(), "invalid --output") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVersionCmd(t *testing.T) {
	out, err := runCLI("version")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(out, "lotsman ") {
		t.Errorf("version output = %q, want 'lotsman <version>'", out)
	}
}
