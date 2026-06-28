package remote

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lotsman/internal/agentlink"
	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// stubLink is a fake agentlink.Link that captures the outgoing request and
// returns a caller-supplied response or error. It is single-use per call.
type stubLink struct {
	cluster     string
	capturedReq agentlink.Request // filled on first Do() call
	respPayload []byte
	respErr     string
	doErr       error // transport-level error (returned by Do itself)
}

func (s *stubLink) Cluster() string { return s.cluster }
func (s *stubLink) Do(_ context.Context, req agentlink.Request) (agentlink.Response, error) {
	s.capturedReq = req
	if s.doErr != nil {
		return agentlink.Response{}, s.doErr
	}
	return agentlink.Response{Payload: s.respPayload, Err: s.respErr}, nil
}
func (s *stubLink) Events() <-chan agentlink.Event {
	ch := make(chan agentlink.Event)
	close(ch)
	return ch
}
func (s *stubLink) Close() error { return nil }

// marshalSignals encodes a []model.Signal to JSON for use as stub response payloads.
func marshalSignals(t *testing.T, sigs []model.Signal) []byte {
	t.Helper()
	b, err := json.Marshal(sigs)
	if err != nil {
		t.Fatalf("marshal signals: %v", err)
	}
	return b
}

// TestRemoteLogsQueryLogsRoundTrip verifies that QueryLogs marshals a LogQuery
// as JSON, sends it with the right RequestKind, and correctly unmarshals the
// agent's JSON response back into []model.Signal.
func TestRemoteLogsQueryLogsRoundTrip(t *testing.T) {
	t0 := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	expected := []model.Signal{
		{
			ID:        "log-1",
			Kind:      model.SignalLog,
			Timestamp: t0,
			Source:    "loki",
			Title:     "error: connection refused",
			Severity:  model.SeverityError,
		},
		{
			ID:        "log-2",
			Kind:      model.SignalLog,
			Timestamp: t0.Add(30 * time.Second),
			Source:    "loki",
			Title:     "panic: nil pointer",
			Severity:  model.SeverityCritical,
		},
	}

	link := &stubLink{
		cluster:     "prod",
		respPayload: marshalSignals(t, expected),
	}
	p := NewProvider(link)

	q := sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "default", Kind: "Deployment", Name: "api"},
		Range:    sources.TimeRange{Start: t0.Add(-5 * time.Minute), End: t0.Add(5 * time.Minute)},
		Limit:    100,
	}

	got, err := p.Logs().QueryLogs(context.Background(), q)
	if err != nil {
		t.Fatalf("QueryLogs: unexpected error: %v", err)
	}

	// Verify the request kind that was sent.
	if link.capturedReq.Kind != agentlink.ReqQueryLogs {
		t.Fatalf("request kind: want %q, got %q", agentlink.ReqQueryLogs, link.capturedReq.Kind)
	}
	if link.capturedReq.Cluster != "prod" {
		t.Fatalf("request cluster: want %q, got %q", "prod", link.capturedReq.Cluster)
	}

	// Verify the query was marshalled into the request payload.
	var sentQuery sources.LogQuery
	if err := json.Unmarshal(link.capturedReq.Payload, &sentQuery); err != nil {
		t.Fatalf("unmarshal sent payload: %v", err)
	}
	if sentQuery.Resource != q.Resource {
		t.Fatalf("sent resource mismatch: want %+v, got %+v", q.Resource, sentQuery.Resource)
	}
	if sentQuery.Limit != q.Limit {
		t.Fatalf("sent limit mismatch: want %d, got %d", q.Limit, sentQuery.Limit)
	}

	// Verify the response was unmarshalled correctly.
	if len(got) != len(expected) {
		t.Fatalf("signal count: want %d, got %d", len(expected), len(got))
	}
	for i, want := range expected {
		if got[i].ID != want.ID {
			t.Errorf("signal[%d].ID: want %q, got %q", i, want.ID, got[i].ID)
		}
		if got[i].Kind != want.Kind {
			t.Errorf("signal[%d].Kind: want %q, got %q", i, want.Kind, got[i].Kind)
		}
		if got[i].Severity != want.Severity {
			t.Errorf("signal[%d].Severity: want %v, got %v", i, want.Severity, got[i].Severity)
		}
		if !got[i].Timestamp.Equal(want.Timestamp) {
			t.Errorf("signal[%d].Timestamp: want %v, got %v", i, want.Timestamp, got[i].Timestamp)
		}
	}
}

// TestRemoteLogsQueryLogsErrField verifies that a non-empty resp.Err is
// propagated as a Go error (without panicking or swallowing).
func TestRemoteLogsQueryLogsErrField(t *testing.T) {
	link := &stubLink{
		cluster: "prod",
		respErr: "loki: backend timeout",
	}
	p := NewProvider(link)

	_, err := p.Logs().QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "ns", Kind: "Deployment", Name: "svc"},
	})
	if err == nil {
		t.Fatal("expected an error from resp.Err, got nil")
	}
	if err.Error() != "loki: backend timeout" {
		t.Fatalf("error message: want %q, got %q", "loki: backend timeout", err.Error())
	}
}

// TestRemoteLogsTransportError verifies that a transport-level error (from
// link.Do itself) is returned unchanged.
func TestRemoteLogsTransportError(t *testing.T) {
	transportErr := errors.New("grpc: connection refused")
	link := &stubLink{
		cluster: "prod",
		doErr:   transportErr,
	}
	p := NewProvider(link)

	_, err := p.Logs().QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "prod"},
	})
	if !errors.Is(err, transportErr) {
		t.Fatalf("want transport error %v, got %v", transportErr, err)
	}
}

// TestRemoteLogsEmptyPayload verifies that an empty payload (with no resp.Err)
// returns an empty slice and no error. This mirrors the `out != nil &&
// len(resp.Payload) > 0` guard in call().
func TestRemoteLogsEmptyPayload(t *testing.T) {
	link := &stubLink{
		cluster:     "prod",
		respPayload: nil, // empty
	}
	p := NewProvider(link)

	got, err := p.Logs().QueryLogs(context.Background(), sources.LogQuery{
		Resource: model.ResourceRef{Cluster: "prod"},
	})
	if err != nil {
		t.Fatalf("empty payload: unexpected error: %v", err)
	}
	// out is initialised as var out []model.Signal (nil slice); no unmarshal
	// should leave it nil/empty.
	if len(got) != 0 {
		t.Fatalf("empty payload: want empty result, got %d signals", len(got))
	}
}
