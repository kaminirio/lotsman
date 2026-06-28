package analyze

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"lotsman/internal/model"
)

// ollamaTimeout is generous because gemma3:4b runs on CPU: a single 4B-parameter
// inference of a few-hundred-token completion can take tens of seconds.
const ollamaTimeout = 90 * time.Second

// ollamaMaxBody caps how much of the Ollama response we read, so a misbehaving
// or compromised backend can't stream an unbounded body into memory.
const ollamaMaxBody = 1 << 20 // 1 MiB

// OllamaExplainer calls a self-hosted Ollama server's chat API to explain an
// incident. It is the only concrete Explainer; an unconfigured base URL makes
// Available() false so the API layer can short-circuit to 503 without a call.
type OllamaExplainer struct {
	baseURL string
	model   string
	hc      *http.Client
}

// NewOllama constructs an OllamaExplainer. baseURL is the Ollama server root
// (e.g. http://ollama.lotsman.svc:11434); an empty baseURL yields a disabled
// explainer (Available() == false). A nil hc gets a client with a CPU-friendly
// timeout.
func NewOllama(baseURL, model string, hc *http.Client) *OllamaExplainer {
	if hc == nil {
		hc = &http.Client{Timeout: ollamaTimeout}
	}
	return &OllamaExplainer{baseURL: strings.TrimRight(baseURL, "/"), model: model, hc: hc}
}

// Available reports whether an Ollama backend is configured.
func (o *OllamaExplainer) Available() bool { return o.baseURL != "" }

// Model returns the configured model name.
func (o *OllamaExplainer) Model() string { return o.model }

// ollamaChatRequest is the POST /api/chat body. stream=false and format=json ask
// Ollama for a single non-streamed response whose content is valid JSON.
type ollamaChatRequest struct {
	Model    string            `json:"model"`
	Stream   bool              `json:"stream"`
	Format   string            `json:"format"`
	Options  ollamaChatOptions `json:"options"`
	Messages []ollamaChatMsg   `json:"messages"`
}

type ollamaChatOptions struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict"`
}

type ollamaChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaChatResponse is the subset of /api/chat we consume.
type ollamaChatResponse struct {
	Message ollamaChatMsg `json:"message"`
}

// Explain builds a grounded prompt from the incident and asks Ollama to turn it
// into an Explanation. It is lenient about the model's JSON: small models
// occasionally wrap their output, so we try a strict parse, then extract the
// first {...} block, and finally fall back to using the raw text as the summary
// rather than erroring. The Model field is always set by us, never trusted from
// the model.
func (o *OllamaExplainer) Explain(ctx context.Context, inc *model.Incident) (Explanation, error) {
	if inc == nil {
		return Explanation{}, fmt.Errorf("analyze: nil incident")
	}

	reqBody := ollamaChatRequest{
		Model:   o.model,
		Stream:  false,
		Format:  "json",
		Options: ollamaChatOptions{Temperature: 0.2, NumPredict: 512},
		Messages: []ollamaChatMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildPrompt(inc)},
		},
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return Explanation{}, fmt.Errorf("analyze: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(buf))
	if err != nil {
		return Explanation{}, fmt.Errorf("analyze: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.hc.Do(req)
	if err != nil {
		return Explanation{}, fmt.Errorf("analyze: call ollama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, ollamaMaxBody))
	if err != nil {
		return Explanation{}, fmt.Errorf("analyze: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Explanation{}, fmt.Errorf("analyze: ollama status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var chat ollamaChatResponse
	if err := json.Unmarshal(body, &chat); err != nil {
		return Explanation{}, fmt.Errorf("analyze: decode response envelope: %w", err)
	}

	return parseContent(chat.Message.Content, o.model), nil
}

// parseContent turns the model's message content into an Explanation. Because
// the request asked for format=json the content is normally clean JSON, but we
// degrade gracefully: strict parse -> first {...} block parse -> raw-text
// fallback. The fallback never errors so a chatty small model can't break the
// endpoint; it just yields a low-confidence, unknown-category explanation.
func parseContent(content, model string) Explanation {
	content = strings.TrimSpace(content)

	if exp, ok := tryJSON(content); ok {
		exp.Model = model
		return exp
	}
	if block := firstJSONObject(content); block != "" {
		if exp, ok := tryJSON(block); ok {
			exp.Model = model
			return exp
		}
	}

	return Explanation{
		Summary:    content,
		Category:   "unknown",
		Confidence: "low",
		Model:      model,
	}
}

// tryJSON attempts to unmarshal s into an Explanation, requiring at least a
// non-empty summary to consider it a real result (an empty {} should fall
// through to the raw-text fallback rather than masquerade as a parse).
func tryJSON(s string) (Explanation, bool) {
	var exp Explanation
	if err := json.Unmarshal([]byte(s), &exp); err != nil {
		return Explanation{}, false
	}
	if strings.TrimSpace(exp.Summary) == "" {
		return Explanation{}, false
	}
	return exp, true
}

// firstJSONObject returns the substring from the first '{' to its matching '}'
// (by brace depth), or "" if none balances. This rescues output wrapped in
// prose or code fences, e.g. "Here you go: {...}".
func firstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
