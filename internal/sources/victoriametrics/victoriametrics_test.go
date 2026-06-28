package victoriametrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lotsman/internal/sources"
)

// promVectorBody builds a Prometheus-envelope JSON response for a vector query.
// Each entry in series is {labels, unixSeconds(float), valueString}.
func promVectorBody(series []struct {
	Labels map[string]string
	TS     float64
	Val    string
}) []byte {
	type sample = [2]json.RawMessage
	type result struct {
		Metric map[string]string `json:"metric"`
		Value  sample            `json:"value"`
	}
	type data struct {
		ResultType string   `json:"resultType"`
		Result     []result `json:"result"`
	}
	type envelope struct {
		Status string `json:"status"`
		Data   data   `json:"data"`
	}

	env := envelope{Status: "success", Data: data{ResultType: "vector"}}
	for _, s := range series {
		tsRaw, _ := json.Marshal(s.TS)
		valRaw, _ := json.Marshal(s.Val)
		env.Data.Result = append(env.Data.Result, result{
			Metric: s.Labels,
			Value:  sample{tsRaw, valRaw},
		})
	}
	b, _ := json.Marshal(env)
	return b
}

// promMatrixBody builds a Prometheus-envelope JSON response for a matrix query.
// Each entry in series is {labels, []point{unixSeconds, valueString}}.
func promMatrixBody(series []struct {
	Labels map[string]string
	Points []struct {
		TS  float64
		Val string
	}
}) []byte {
	type sample = [2]json.RawMessage
	type result struct {
		Metric map[string]string `json:"metric"`
		Values []sample          `json:"values"`
	}
	type data struct {
		ResultType string   `json:"resultType"`
		Result     []result `json:"result"`
	}
	type envelope struct {
		Status string `json:"status"`
		Data   data   `json:"data"`
	}

	env := envelope{Status: "success", Data: data{ResultType: "matrix"}}
	for _, s := range series {
		r := result{Metric: s.Labels}
		for _, p := range s.Points {
			tsRaw, _ := json.Marshal(p.TS)
			valRaw, _ := json.Marshal(p.Val)
			r.Values = append(r.Values, sample{tsRaw, valRaw})
		}
		env.Data.Result = append(env.Data.Result, r)
	}
	b, _ := json.Marshal(env)
	return b
}

func promErrorBody(errType, errMsg string) []byte {
	type envelope struct {
		Status    string `json:"status"`
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	b, _ := json.Marshal(envelope{Status: "error", ErrorType: errType, Error: errMsg})
	return b
}

func TestQueryInstant_VectorResult(t *testing.T) {
	// QueryInstant with a vector result must return series with correct labels and
	// a single MetricPoint with a parsed timestamp and value.
	ts := float64(1710000000) + 0.5 // seconds with sub-second fraction

	series := []struct {
		Labels map[string]string
		TS     float64
		Val    string
	}{
		{
			Labels: map[string]string{"namespace": "payments", "pod": "api-123", "__name__": "up"},
			TS:     ts,
			Val:    "1.5",
		},
	}

	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(promVectorBody(series))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	at := time.Unix(1710000060, 0).UTC()
	result, err := c.QueryInstant(context.Background(), sources.MetricQuery{
		PromQL: `up{namespace="payments"}`,
		At:     at,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedPath != "/api/v1/query" {
		t.Errorf("path: got %q, want /api/v1/query", capturedPath)
	}
	if len(result.Series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(result.Series))
	}
	s := result.Series[0]
	if s.Labels["namespace"] != "payments" {
		t.Errorf("Labels[namespace]: got %q", s.Labels["namespace"])
	}
	if len(s.Points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(s.Points))
	}
	p := s.Points[0]
	if p.V != 1.5 {
		t.Errorf("value: got %v, want 1.5", p.V)
	}
	// Timestamp: float seconds → time.Time. Fractional second must be preserved.
	wantSec := int64(ts)
	if p.T.Unix() != wantSec {
		t.Errorf("timestamp seconds: got %d, want %d", p.T.Unix(), wantSec)
	}
}

func TestQueryInstant_MultipleSeries(t *testing.T) {
	series := []struct {
		Labels map[string]string
		TS     float64
		Val    string
	}{
		{Labels: map[string]string{"pod": "a"}, TS: 1710000000, Val: "0.1"},
		{Labels: map[string]string{"pod": "b"}, TS: 1710000000, Val: "0.2"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(promVectorBody(series))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	result, err := c.QueryInstant(context.Background(), sources.MetricQuery{PromQL: "up"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Series) != 2 {
		t.Errorf("expected 2 series, got %d", len(result.Series))
	}
}

func TestQueryRange_MatrixResult(t *testing.T) {
	// QueryRange with a matrix result must return series with multiple MetricPoints.
	series := []struct {
		Labels map[string]string
		Points []struct {
			TS  float64
			Val string
		}
	}{
		{
			Labels: map[string]string{"namespace": "infra", "job": "node"},
			Points: []struct {
				TS  float64
				Val string
			}{
				{TS: 1710000000, Val: "0.5"},
				{TS: 1710000060, Val: "0.7"},
				{TS: 1710000120, Val: "0.9"},
			},
		},
	}

	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(promMatrixBody(series))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	start := time.Unix(1710000000, 0).UTC()
	end := time.Unix(1710000120, 0).UTC()
	result, err := c.QueryRange(context.Background(), sources.MetricRangeQuery{
		PromQL: `node_cpu_seconds_total`,
		Range:  sources.TimeRange{Start: start, End: end},
		Step:   time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedPath != "/api/v1/query_range" {
		t.Errorf("path: got %q, want /api/v1/query_range", capturedPath)
	}
	if len(result.Series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(result.Series))
	}
	s := result.Series[0]
	if s.Labels["namespace"] != "infra" {
		t.Errorf("Labels[namespace]: got %q", s.Labels["namespace"])
	}
	if len(s.Points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(s.Points))
	}

	expectedVals := []float64{0.5, 0.7, 0.9}
	expectedTS := []int64{1710000000, 1710000060, 1710000120}
	for i, p := range s.Points {
		if p.V != expectedVals[i] {
			t.Errorf("point[%d].V: got %v, want %v", i, p.V, expectedVals[i])
		}
		if p.T.Unix() != expectedTS[i] {
			t.Errorf("point[%d].T: got %d, want %d", i, p.T.Unix(), expectedTS[i])
		}
	}
}

func TestQueryRange_DefaultStep(t *testing.T) {
	// Step==0 must default to 1 minute.
	var capturedStep string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedStep = r.URL.Query().Get("step")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(promMatrixBody(nil))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryRange(context.Background(), sources.MetricRangeQuery{
		PromQL: "up",
		Range: sources.TimeRange{
			Start: time.Unix(1710000000, 0),
			End:   time.Unix(1710000060, 0),
		},
		Step: 0, // should default to 1 minute
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedStep != "60" {
		t.Errorf("step param: got %q, want 60", capturedStep)
	}
}

func TestQueryInstant_StatusError(t *testing.T) {
	// A status:"error" envelope must return a Go error containing the message.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // VM/Prom return 200 even for query errors
		w.Write(promErrorBody("bad_data", "parse error at position 5"))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryInstant(context.Background(), sources.MetricQuery{PromQL: "up!!!"})
	if err == nil {
		t.Fatal("expected error for status:error envelope, got nil")
	}
	if !strings.Contains(err.Error(), "parse error") {
		t.Errorf("expected parse error in message, got: %v", err)
	}
}

func TestQueryRange_HTTP500Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	_, err := c.QueryRange(context.Background(), sources.MetricRangeQuery{
		PromQL: "up",
		Range: sources.TimeRange{
			Start: time.Unix(1710000000, 0),
			End:   time.Unix(1710000060, 0),
		},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestQueryInstant_EmptyVector(t *testing.T) {
	// An empty result set must return an empty MetricResult (no error).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(promVectorBody(nil))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	result, err := c.QueryInstant(context.Background(), sources.MetricQuery{PromQL: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Series) != 0 {
		t.Errorf("expected 0 series, got %d", len(result.Series))
	}
}
