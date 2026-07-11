package api

import (
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// errRateLimited is returned to clients that exceed a route's rate limit. Shape
// matches writeError so a 429 body looks like every other error response.
var errRateLimited = errors.New("rate limit exceeded")

// ipRateLimiter is a lightweight in-process, per-key token-bucket limiter. It is
// std-lib only (no new dependency): each key gets a bucket that refills at rate
// tokens/second up to burst, and each allowed request spends one token. Buckets
// are created lazily and evicted once idle past their fill time, so the map can't
// grow without bound under churn of distinct client IPs.
//
// It is deliberately best-effort and process-local: with multiple replicas the
// effective limit is per-replica. That is acceptable for the abuse surfaces it
// guards (the OAuth handshake's outbound GitHub calls and the expensive
// investigate gather) — it caps a single hot client, not a coordinated fleet.
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    float64 // tokens added per second
	burst   float64 // bucket capacity (max burst)
	now     func() time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// newIPRateLimiter builds a limiter that permits burst requests immediately and
// refills at rate per second thereafter.
func newIPRateLimiter(rate, burst float64) *ipRateLimiter {
	return &ipRateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rate,
		burst:   burst,
		now:     time.Now,
	}
}

// allow reports whether a request keyed by key may proceed, spending a token if
// so. It also opportunistically evicts buckets that have fully refilled and are
// unused, bounding memory under IP churn.
func (l *ipRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * l.rate
			if b.tokens > l.burst {
				b.tokens = l.burst
			}
			b.last = now
		}
	}

	allowed := false
	if b.tokens >= 1 {
		b.tokens--
		allowed = true
	}

	l.evictFull(now)
	return allowed
}

// evictFull drops buckets that, refilled to now, would be back at full capacity:
// such a bucket is indistinguishable from a fresh one, so removing it bounds the
// map under churn of distinct client IPs without changing behavior. Runs under
// the held lock on the calling goroutine.
func (l *ipRateLimiter) evictFull(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// middleware wraps next, rejecting requests from a client IP that has exhausted
// its bucket with 429 (and a Retry-After hint) before next runs.
func (l *ipRateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, errRateLimited)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// trustedProxyEnvVar opts into deriving the rate-limit key from X-Forwarded-For.
// Set it (to a truthy value) ONLY when a trusted L7 proxy/ingress sits in front
// and appends the real client to XFF — see clientIP.
const trustedProxyEnvVar = "LOTSMAN_TRUSTED_PROXY"

// trustedProxyEnabled reports whether the deployment has opted into trusting the
// X-Forwarded-For header (LOTSMAN_TRUSTED_PROXY parses as a truthy bool:
// 1/t/T/TRUE/true/… per strconv.ParseBool). Default (unset/empty/false) keeps the
// secure RemoteAddr behavior.
func trustedProxyEnabled() bool {
	v := os.Getenv(trustedProxyEnvVar)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

// clientIP extracts the remote host used as the rate-limit key.
//
// Default (secure): it uses the transport peer (RemoteAddr), NOT a
// client-settable X-Forwarded-For header, so a caller can't evade the limit by
// spoofing a forwarding header. This is correct when the ingress preserves the
// client source IP (rewrites RemoteAddr / connects with the real peer).
//
// Behind an L7 ingress that sets X-Forwarded-For but does NOT rewrite RemoteAddr,
// every request arrives from the ingress pod IP, so keying on RemoteAddr collapses
// all clients into one shared bucket and a single abuser locks out everyone.
// DEPLOYMENT REQUIREMENT: either configure the ingress to preserve the client
// source IP, OR — when the proxy is trusted — set LOTSMAN_TRUSTED_PROXY truthy so
// the key is taken from the RIGHT-MOST X-Forwarded-For hop. The trusted proxy
// appends the peer it actually observed as the last XFF entry, so a spoofed
// left-most value cannot displace it; taking the right-most hop is spoof-resistant
// for a single trusted proxy.
func clientIP(r *http.Request) string {
	if trustedProxyEnabled() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
