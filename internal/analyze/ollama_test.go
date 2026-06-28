package analyze

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaAvailable(t *testing.T) {
	if NewOllama("", "gemma3:4b", nil).Available() {
		t.Error("empty base URL must be unavailable")
	}
	o := NewOllama("http://ollama:11434/", "gemma3:4b", nil)
	if !o.Available() {
		t.Error("configured base URL must be available")
	}
	if o.Model() != "gemma3:4b" {
		t.Errorf("Model() = %q", o.Model())
	}
	// Trailing slash trimmed so we don't build "//api/chat".
	if o.baseURL != "http://ollama:11434" {
		t.Errorf("baseURL not trimmed: %q", o.baseURL)
	}
}

// mockOllama returns an httptest server that asserts the request shape and
// replies with content as the chat message content.
func mockOllama(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		var req ollamaChatRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Stream {
			t.Error("stream should be false")
		}
		if req.Format != "json" {
			t.Errorf("format = %q, want json", req.Format)
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ollamaChatResponse{Message: ollamaChatMsg{Role: "assistant", Content: content}})
	}))
}

func TestExplainCleanJSON(t *testing.T) {
	srv := mockOllama(t, `{"summary":"A deploy of checkout caused a crash loop.","category":"deploy","confidence":"high"}`)
	defer srv.Close()

	o := NewOllama(srv.URL, "gemma3:4b", srv.Client())
	exp, err := o.Explain(context.Background(), sampleIncident())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.Summary != "A deploy of checkout caused a crash loop." {
		t.Errorf("summary = %q", exp.Summary)
	}
	if exp.Category != "deploy" || exp.Confidence != "high" {
		t.Errorf("category=%q confidence=%q", exp.Category, exp.Confidence)
	}
	if exp.Model != "gemma3:4b" {
		t.Errorf("model = %q, want gemma3:4b (must be set by us)", exp.Model)
	}
}

func TestExplainWrappedJSON(t *testing.T) {
	// Small model wraps JSON in prose/fences — the first {...} block must be rescued.
	content := "Sure! Here is the analysis:\n```json\n{\"summary\":\"Memory pressure.\",\"category\":\"resource\",\"confidence\":\"medium\"}\n```"
	srv := mockOllama(t, content)
	defer srv.Close()

	exp, err := NewOllama(srv.URL, "gemma3:4b", srv.Client()).Explain(context.Background(), sampleIncident())
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.Summary != "Memory pressure." || exp.Category != "resource" || exp.Confidence != "medium" {
		t.Errorf("wrapped JSON not rescued: %+v", exp)
	}
}

func TestExplainMalformedFallback(t *testing.T) {
	// Not JSON at all and no {...} block — lenient fallback uses raw text.
	srv := mockOllama(t, "the deploy probably broke it")
	defer srv.Close()

	exp, err := NewOllama(srv.URL, "gemma3:4b", srv.Client()).Explain(context.Background(), sampleIncident())
	if err != nil {
		t.Fatalf("Explain must not error on malformed content: %v", err)
	}
	if exp.Summary != "the deploy probably broke it" {
		t.Errorf("fallback summary = %q", exp.Summary)
	}
	if exp.Category != "unknown" || exp.Confidence != "low" {
		t.Errorf("fallback category=%q confidence=%q, want unknown/low", exp.Category, exp.Confidence)
	}
	if exp.Model != "gemma3:4b" {
		t.Errorf("fallback model = %q", exp.Model)
	}
}

func TestExplainHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := NewOllama(srv.URL, "gemma3:4b", srv.Client()).Explain(context.Background(), sampleIncident())
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error = %v, want status 500 detail", err)
	}
}

func TestFirstJSONObject(t *testing.T) {
	cases := map[string]string{
		`prefix {"a":1} suffix`:  `{"a":1}`,
		`{"a":{"b":2}} trailing`: `{"a":{"b":2}}`,
		`no object here`:         ``,
		`unbalanced {"a":1`:      ``,
	}
	for in, want := range cases {
		if got := firstJSONObject(in); got != want {
			t.Errorf("firstJSONObject(%q) = %q, want %q", in, got, want)
		}
	}
}
