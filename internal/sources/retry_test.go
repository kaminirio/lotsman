package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestDoWithRetry_TransientThenOK proves a 503 followed by a 200 succeeds: the
// helper retries the transient status and returns the eventual success.
func TestDoWithRetry_TransientThenOK(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := DoWithRetry(srv.Client(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 after retry, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("want 2 requests (503 then 200), got %d", got)
	}
}

// TestDoWithRetry_PersistentServerError proves a persistently failing backend is
// retried exactly RetryMaxAttempts times and then handed back to the caller (the
// adapter formats its own error from the final response).
func TestDoWithRetry_PersistentServerError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := DoWithRetry(srv.Client(), req)
	if err != nil {
		t.Fatalf("final attempt should return the response, not an error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want the final 500 response, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != RetryMaxAttempts {
		t.Fatalf("want exactly %d attempts, got %d", RetryMaxAttempts, got)
	}
}

// TestDoWithRetry_ClientErrorNotRetried proves a 404 (a non-429 4xx) is returned
// immediately without retrying.
func TestDoWithRetry_ClientErrorNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(srv.Client(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("4xx must not be retried; want 1 request, got %d", got)
	}
}

// TestDoWithRetry_ContextCancelled proves a cancelled context aborts retries
// rather than sleeping out the backoff.
func TestDoWithRetry_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if _, err := DoWithRetry(srv.Client(), req); err == nil {
		t.Fatal("expected an error once the context is cancelled")
	}
}
