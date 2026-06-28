// Package redact scrubs secrets and PII from free-text that Lotsman surfaces
// from cluster data — pod log bodies, event/signal messages, and ConfigMap
// values — before it is returned to non-admin viewers or sent to the LLM
// explainer. It is best-effort defense-in-depth, not a guarantee: it catches the
// common high-signal shapes (tokens, credentials in URLs/key=value, private
// keys, emails) but cannot understand every application's log format. Admins
// receive un-redacted data; the masking of structured env/secret values is
// handled separately at the API layer.
package redact

import "regexp"

// placeholder is what a matched secret/PII span is replaced with.
const placeholder = "[REDACTED]"

// rule is a compiled pattern plus its replacement. For key=value style matches
// the replacement keeps the key (capture group 1) and redacts only the value.
type rule struct {
	re   *regexp.Regexp
	repl string
}

// rules are applied in order; earlier rules win on overlapping spans. Private
// keys first (largest spans), then tokens, then credential assignments, then
// PII.
var rules = []rule{
	// PEM private key blocks (any key type).
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), "[REDACTED-PRIVATE-KEY]"},
	// JWTs (header.payload.signature, base64url).
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}`), "[REDACTED-JWT]"},
	// Credentials embedded in a URL: scheme://user:password@host -> redact password.
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^:@/\s]+:)[^@/\s]+@`), "${1}[REDACTED]@"},
	// AWS access key IDs.
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "[REDACTED-AWS-KEY]"},
	// Bearer tokens.
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{8,}`), "Bearer [REDACTED]"},
	// key=value / "key":"value" credential assignments — keep the key, redact value.
	{regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|access[_-]?key|secret[_-]?key|client[_-]?secret|aws_secret_access_key|auth|dsn|connection[_-]?string)"?\s*[:=]\s*"?)([^"\s,;}]+)`), "${1}[REDACTED]"},
	// Email addresses (PII).
	{regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`), "[REDACTED-EMAIL]"},
}

// Redact returns s with secrets and PII replaced by placeholders. Safe on empty
// input and idempotent enough for repeated application.
func Redact(s string) string {
	if s == "" {
		return s
	}
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
