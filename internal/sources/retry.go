package sources

import (
	"io"
	"net/http"
	"time"
)

// Bounded HTTP retry on transient failures, shared by the Loki / VictoriaMetrics
// / ArgoCD adapters (SRC-4). Backends behind these adapters are queried live on
// the investigation path, so a momentary blip (a rolling restart, a 503 from an
// overloaded vmselect, a rate-limit 429) should not fail the whole gather when a
// second attempt would succeed. Retries are few and backoff is capped so a truly
// down backend still fails fast within the caller's per-source timeout.
const (
	// RetryMaxAttempts is the total number of attempts (initial + retries).
	RetryMaxAttempts = 3
	// retryBaseBackoff is the delay before the first retry; it doubles each
	// subsequent retry up to retryMaxBackoff.
	retryBaseBackoff = 50 * time.Millisecond
	retryMaxBackoff  = 2 * time.Second
)

// isRetryableStatus reports whether an HTTP status code is worth retrying: 429
// (rate limited) and any 5xx (server-side/transient). All other 4xx are the
// client's fault and are never retried.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// DoWithRetry issues req using client, retrying transient failures — network
// errors and retryable HTTP statuses (429 / 5xx) — up to RetryMaxAttempts with
// capped exponential backoff. It respects req's context: a cancelled or expired
// context aborts immediately and no retry is attempted after cancellation.
//
// On the final attempt the response is returned as-is even if its status is
// retryable, so the caller keeps ownership of status handling and error
// formatting (e.g. echoing the backend body). req must be safe to resend — the
// adapters use bodyless GETs, which are.
func DoWithRetry(client *http.Client, req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	backoff := retryBaseBackoff
	var lastErr error
	for attempt := 0; attempt < RetryMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > retryMaxBackoff {
				backoff = retryMaxBackoff
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			// A cancelled/expired context is terminal, not transient.
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		// Retry a transient status only if we have another attempt left; on the
		// last attempt hand the response back for the caller to format.
		if attempt < RetryMaxAttempts-1 && isRetryableStatus(resp.StatusCode) {
			// Drain a little of the body so the connection can be reused, then close.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}
