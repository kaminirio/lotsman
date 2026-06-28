package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// clearWriteDeadline removes the server's WriteTimeout for this connection only.
// The server sets a 60s WriteTimeout (see api.New) as slow-client protection for
// ordinary responses, but a handful of handlers legitimately run longer than
// that — the SSE stream (unbounded), on-demand investigation (live multi-source
// gather), the LLM explainer (its backend budget exceeds 60s), and pod-log
// fetches. Those handlers call this so the deadline can't sever a valid
// in-flight response. The error is best-effort: if the ResponseWriter can't be
// unwrapped to a deadline-setter the handler simply runs under the global cap.
func clearWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

// withCommon wraps the mux with panic recovery so a handler bug can't take the
// process down.
func withCommon(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic in handler", "err", rec, "path", r.URL.Path)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
