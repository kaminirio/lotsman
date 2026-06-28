package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// lokiStreamBody builds a minimal Loki query_range response with the given
// streams. Each stream is {labels, []value}, where each value is [tsNano, line].
func lokiStreamBody(streams []struct {
	Labels map[string]string
	Values [][2]string // [tsNano_string, line]
}) []byte {
	type lokiVal = [2]string
	type lokiStream struct {
		Stream map[string]string `json:"stream"`
		Values []lokiVal         `json:"values"`
	}
	type lokiData struct {
		ResultType string       `json:"resultType"`
		Result     []lokiStream `json:"result"`
	}
	type lokiResp struct {
		Status string   `json:"status"`
		Data   lokiData `json:"data"`
	}

	resp := lokiResp{
		Status: "success",
		Data: lokiData{
			ResultType: "streams",
		},
	}
	for _, s := range streams {
		ls := lokiStream{Stream: s.Labels, Values: s.Values}
		resp.Data.Result = append(resp.Data.Result, ls)
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestQueryLogs_PathAndParams(t *testing.T) {
	// Verify that the correct path is hit and query params are encoded properly.
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)

	var capturedPath string
	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(lokiStreamBody(nil))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "payments", Name: "api"},
		Range:    sources.TimeRange{Start: start, End: end},
		Limit:    50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedPath != "/loki/api/v1/query_range" {
		t.Errorf("wrong path: got %q, want /loki/api/v1/query_range", capturedPath)
	}
	if capturedQuery.Get("limit") != "50" {
		t.Errorf("limit param: got %q, want 50", capturedQuery.Get("limit"))
	}
	wantStart := strconv.FormatInt(start.UnixNano(), 10)
	if capturedQuery.Get("start") != wantStart {
		t.Errorf("start param: got %q, want %q", capturedQuery.Get("start"), wantStart)
	}
	wantEnd := strconv.FormatInt(end.UnixNano(), 10)
	if capturedQuery.Get("end") != wantEnd {
		t.Errorf("end param: got %q, want %q", capturedQuery.Get("end"), wantEnd)
	}
}

func TestQueryLogs_SynthesizedSelector(t *testing.T) {
	// When Query is empty, the adapter must derive a LogQL selector from Resource.
	var capturedQueryParam string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQueryParam = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(lokiStreamBody(nil))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{
			Cluster:   "prod",
			Namespace: "payments",
			Name:      "api-server",
			Pod:       "api-server-abc123",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The synthesized selector must include namespace, app (name), and pod.
	for _, want := range []string{`namespace="payments"`, `app="api-server"`, `pod="api-server-abc123"`} {
		if !strings.Contains(capturedQueryParam, want) {
			t.Errorf("synthesized selector %q missing %q", capturedQueryParam, want)
		}
	}
	// Must be a stream selector: {...}
	if !strings.HasPrefix(capturedQueryParam, "{") || !strings.HasSuffix(capturedQueryParam, "}") {
		t.Errorf("selector not wrapped in braces: %q", capturedQueryParam)
	}
}

func TestQueryLogs_ExplicitQueryOverride(t *testing.T) {
	// When Query is set it must be forwarded verbatim; no synthesis happens.
	const explicitLogQL = `{app="payments"} |= "error"`
	var capturedQueryParam string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQueryParam = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(lokiStreamBody(nil))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "payments", Name: "api"},
		Query:    explicitLogQL,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedQueryParam != explicitLogQL {
		t.Errorf("query param: got %q, want %q", capturedQueryParam, explicitLogQL)
	}
}

func TestQueryLogs_DefaultLimit(t *testing.T) {
	// Limit == 0 must be replaced with defaultLimit (1000).
	var capturedLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(lokiStreamBody(nil))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "ns", Name: "app"},
		Limit:    0, // explicitly zero → defaultLimit
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedLimit != strconv.Itoa(defaultLimit) {
		t.Errorf("limit param: got %q, want %d", capturedLimit, defaultLimit)
	}
}

func TestQueryLogs_SignalMapping(t *testing.T) {
	// Entries must be mapped to model.Signal with correct fields.
	tsNano := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC).UnixNano()
	streams := []struct {
		Labels map[string]string
		Values [][2]string
	}{
		{
			Labels: map[string]string{
				"namespace": "payments",
				"app":       "api-server",
				"pod":       "api-server-xyz",
				"level":     "error",
			},
			Values: [][2]string{
				{strconv.FormatInt(tsNano, 10), "oh no, something broke"},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(lokiStreamBody(streams))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	sigs, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "payments", Name: "api-server"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(sigs))
	}
	s := sigs[0]

	if s.Kind != model.SignalLog {
		t.Errorf("Kind: got %q, want %q", s.Kind, model.SignalLog)
	}
	if s.Source != "loki" {
		t.Errorf("Source: got %q, want loki", s.Source)
	}
	if s.Message != "oh no, something broke" {
		t.Errorf("Message: got %q", s.Message)
	}
	// Timestamp must be derived from the nanosecond timestamp.
	wantTS := time.Unix(0, tsNano).UTC()
	if !s.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp: got %v, want %v", s.Timestamp, wantTS)
	}
	// Severity from level="error"
	if s.Severity != model.SeverityError {
		t.Errorf("Severity: got %v, want error", s.Severity)
	}
	// Resource derived from labels
	if s.Resource.Namespace != "payments" {
		t.Errorf("Resource.Namespace: got %q", s.Resource.Namespace)
	}
	if s.Resource.Name != "api-server" {
		t.Errorf("Resource.Name: got %q", s.Resource.Name)
	}
	if s.Resource.Pod != "api-server-xyz" {
		t.Errorf("Resource.Pod: got %q", s.Resource.Pod)
	}
	if s.Resource.Cluster != "prod" {
		t.Errorf("Resource.Cluster: got %q", s.Resource.Cluster)
	}
}

func TestQueryLogs_LimitCapsResults(t *testing.T) {
	// When limit is set the output slice must not exceed it.
	tsNano := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	var values [][2]string
	for i := 0; i < 10; i++ {
		values = append(values, [2]string{
			strconv.FormatInt(tsNano+int64(i), 10),
			fmt.Sprintf("line %d", i),
		})
	}
	streams := []struct {
		Labels map[string]string
		Values [][2]string
	}{
		{Labels: map[string]string{"namespace": "ns", "app": "a"}, Values: values},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(lokiStreamBody(streams))
	}))
	defer srv.Close()

	const wantLimit = 3
	c := New(srv.URL, srv.Client())
	sigs, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "c", Namespace: "ns", Name: "a"},
		Limit:    wantLimit,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) > wantLimit {
		t.Errorf("got %d signals, limit was %d", len(sigs), wantLimit)
	}
}

func TestQueryLogs_SeverityLevels(t *testing.T) {
	// Table-driven test for all severity mappings.
	cases := []struct {
		level   string
		wantSev model.Severity
	}{
		{"fatal", model.SeverityCritical},
		{"critical", model.SeverityCritical},
		{"crit", model.SeverityCritical},
		{"error", model.SeverityError},
		{"err", model.SeverityError},
		{"warn", model.SeverityWarning},
		{"warning", model.SeverityWarning},
		{"info", model.SeverityInfo},
		{"debug", model.SeverityInfo},
		{"", model.SeverityInfo},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("level="+tc.level, func(t *testing.T) {
			tsNano := time.Now().UnixNano()
			streams := []struct {
				Labels map[string]string
				Values [][2]string
			}{
				{
					Labels: map[string]string{"namespace": "ns", "app": "svc", "level": tc.level},
					Values: [][2]string{{strconv.FormatInt(tsNano, 10), "msg"}},
				},
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write(lokiStreamBody(streams))
			}))
			defer srv.Close()

			c := New(srv.URL, srv.Client())
			sigs, err := c.QueryLogs(context.Background(), sources.LogQuery{
				Resource: model.ResourceRef{Cluster: "c", Namespace: "ns", Name: "svc"},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(sigs) != 1 {
				t.Fatalf("expected 1 signal, got %d", len(sigs))
			}
			if sigs[0].Severity != tc.wantSev {
				t.Errorf("severity: got %v, want %v", sigs[0].Severity, tc.wantSev)
			}
		})
	}
}

func TestQueryLogs_Non200Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend gone", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "c", Namespace: "ns", Name: "app"},
	})
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("expected 502 in error message, got: %v", err)
	}
}

func TestQueryLogs_MalformedBodyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json at all {{{"))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "c", Namespace: "ns", Name: "app"},
	})
	if err == nil {
		t.Fatal("expected error for malformed body, got nil")
	}
}

func TestQueryLogs_EmptyResourceAndQuery(t *testing.T) {
	// A completely empty resource with no query must return an error before
	// even touching the network.
	c := New("http://unused", http.DefaultClient)
	_, err := c.QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{}, // empty
		Query:    "",
	})
	if err == nil {
		t.Fatal("expected error for unresolvable resource, got nil")
	}
}
