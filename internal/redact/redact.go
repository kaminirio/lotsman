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
// keys first (largest spans), then provider-specific tokens keyed off a
// distinctive prefix (highest precision), then generic credential assignments,
// then PII. All patterns are length-bounded and prefix-anchored so they stay
// precise (no false positives on ordinary words, hashes, UUIDs, or image tags)
// and — being RE2 — run in guaranteed linear time.
var rules = []rule{
	// PEM private key blocks (any key type). Also covers the GCP service-account
	// JSON "private_key" field, whose value embeds a full PEM block.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), "[REDACTED-PRIVATE-KEY]"},
	// JWTs (header.payload.signature, base64url).
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}`), "[REDACTED-JWT]"},
	// Credentials embedded in a URL: scheme://user:password@host -> redact password.
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^:@/\s]+:)[^@/\s]+@`), "${1}[REDACTED]@"},
	// GitHub fine-grained personal access tokens: github_pat_<22 chars>_<59 chars>.
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,255}\b`), "[REDACTED-GITHUB-TOKEN]"},
	// GitHub tokens: classic PAT (ghp_), OAuth (gho_), user-to-server (ghu_),
	// server-to-server (ghs_), refresh (ghr_). Body is ~36 base62 chars; the
	// distinctive gh?_ prefix keeps this off ghcr.io image refs and the word
	// "github".
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,255}\b`), "[REDACTED-GITHUB-TOKEN]"},
	// Slack tokens: bot (xoxb-), app (xoxa-), workspace (xoxp-), refresh (xoxr-),
	// legacy (xoxs-).
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,255}\b`), "[REDACTED-SLACK-TOKEN]"},
	// AWS access key IDs.
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "[REDACTED-AWS-KEY]"},
	// Bearer tokens.
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{8,}`), "Bearer [REDACTED]"},
	// key=value / "key":"value" credential assignments — keep the key, redact
	// value. Covers non-bearer API tokens, GCP "private_key_id", and the AWS
	// secret access key (which has no safe standalone shape) via its key name.
	{regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|access[_-]?key|secret[_-]?key|client[_-]?secret|aws_secret_access_key|access[_-]?token|refresh[_-]?token|private[_-]?token|private[_-]?key[_-]?id|auth|dsn|connection[_-]?string)"?\s*[:=]\s*"?)([^"\s,;}]+)`), "${1}[REDACTED]"},
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
