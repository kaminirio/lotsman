package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Exceeding a route's burst yields 429; refilling over time restores capacity.
func TestRateLimiterMiddleware429(t *testing.T) {
	// Freeze time so refill is deterministic: burst 2, refill 1 token/sec.
	now := time.Unix(0, 0)
	lim := newIPRateLimiter(1, 2)
	lim.now = func() time.Time { return now }

	var served int
	h := lim.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		w.WriteHeader(http.StatusOK)
	}))

	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/auth/login/github", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Two allowed (burst), third rejected.
	if code := call(); code != http.StatusOK {
		t.Fatalf("req 1: got %d, want 200", code)
	}
	if code := call(); code != http.StatusOK {
		t.Fatalf("req 2: got %d, want 200", code)
	}
	if code := call(); code != http.StatusTooManyRequests {
		t.Fatalf("req 3: got %d, want 429", code)
	}
	if served != 2 {
		t.Fatalf("handler served %d times, want 2 (429 short-circuits)", served)
	}

	// After ~1s a token refills -> one more allowed.
	now = now.Add(1100 * time.Millisecond)
	if code := call(); code != http.StatusOK {
		t.Fatalf("after refill: got %d, want 200", code)
	}
}

// Distinct client IPs get independent buckets (one hot IP can't starve another).
func TestRateLimiterPerIP(t *testing.T) {
	now := time.Unix(0, 0)
	lim := newIPRateLimiter(1, 1)
	lim.now = func() time.Time { return now }

	if !lim.allow("10.0.0.1") {
		t.Fatal("first IP first request should be allowed")
	}
	if lim.allow("10.0.0.1") {
		t.Fatal("first IP second request should be limited")
	}
	if !lim.allow("10.0.0.2") {
		t.Fatal("second IP should have its own fresh bucket")
	}
}

// Eviction bounds the bucket map under churn of distinct IPs: after time advances
// far enough for every touched bucket to refill to full, the next allow() evicts
// them, so the map does not grow without bound.
func TestRateLimiterEvictionBounded(t *testing.T) {
	now := time.Unix(0, 0)
	lim := newIPRateLimiter(1, 1) // refill 1/s, burst 1
	lim.now = func() time.Time { return now }

	// Touch many distinct IPs at t0; each creates a bucket.
	const distinct = 1000
	for i := 0; i < distinct; i++ {
		lim.allow(fmt.Sprintf("10.1.%d.%d", i/256, i%256))
	}
	if got := len(lim.buckets); got < distinct/2 {
		t.Fatalf("expected the map to hold the touched buckets (~%d), got %d", distinct, got)
	}

	// Advance well past the refill time so every old bucket is back at full
	// capacity (indistinguishable from fresh), then a single touch triggers
	// eviction of all of them.
	now = now.Add(10 * time.Second)
	lim.allow("172.16.0.1")

	if got := len(lim.buckets); got > 5 {
		t.Fatalf("eviction not bounded: map still holds %d buckets after refill", got)
	}
}

// clientIP keys on RemoteAddr by default (spoof-safe) and honors the right-most
// X-Forwarded-For hop only when LOTSMAN_TRUSTED_PROXY opts in.
func TestClientIP(t *testing.T) {
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}

	t.Run("default uses RemoteAddr and ignores spoofed XFF", func(t *testing.T) {
		if got := clientIP(mk("203.0.113.7:5555", "1.2.3.4")); got != "203.0.113.7" {
			t.Fatalf("default: got %q, want RemoteAddr host 203.0.113.7", got)
		}
	})

	t.Run("trusted proxy honors right-most XFF hop", func(t *testing.T) {
		t.Setenv(trustedProxyEnvVar, "true")
		// A spoofed left-most value can't displace the real client the trusted proxy
		// appended as the right-most hop.
		if got := clientIP(mk("10.0.0.1:5555", "9.9.9.9, 203.0.113.7")); got != "203.0.113.7" {
			t.Fatalf("trusted: got %q, want right-most hop 203.0.113.7", got)
		}
		// No XFF -> fall back to RemoteAddr even when trust is enabled.
		if got := clientIP(mk("10.0.0.1:5555", "")); got != "10.0.0.1" {
			t.Fatalf("trusted, no XFF: got %q, want RemoteAddr 10.0.0.1", got)
		}
	})

	t.Run("non-bool env value stays on secure RemoteAddr default", func(t *testing.T) {
		t.Setenv(trustedProxyEnvVar, "banana")
		if got := clientIP(mk("203.0.113.7:5555", "1.2.3.4")); got != "203.0.113.7" {
			t.Fatalf("garbage env: got %q, want RemoteAddr 203.0.113.7", got)
		}
	})
}
