package redact

import (
	"strings"
	"testing"
)

func TestRedact_Secrets(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		mustHide string // substring that must NOT survive
		mustKeep string // substring that must survive (context)
	}{
		{
			name:     "jwt",
			in:       "auth header: eyJhbGciOiJIUzI1NiI.eyJzdWIiOiIxMjM0NTY3ODkw.dozjgNryP4J3jVmNHl0w",
			mustHide: "eyJzdWIiOiIxMjM0NTY3ODkw",
			mustKeep: "auth header:",
		},
		{
			name:     "url credentials",
			in:       "dialing postgres://app:s3cr3tpw@db.internal:5432/app",
			mustHide: "s3cr3tpw",
			mustKeep: "db.internal",
		},
		{
			name:     "key=value password",
			in:       "starting with DATABASE_PASSWORD=hunter2 ok",
			mustHide: "hunter2",
			mustKeep: "starting with",
		},
		{
			name:     "json token",
			in:       `{"token":"abcd1234secret","level":"info"}`,
			mustHide: "abcd1234secret",
			mustKeep: "info",
		},
		{
			name:     "bearer",
			in:       "Authorization: Bearer abcdef0123456789",
			mustHide: "abcdef0123456789",
			mustKeep: "Authorization",
		},
		{
			name:     "aws key",
			in:       "key AKIAIOSFODNN7EXAMPLE used",
			mustHide: "AKIAIOSFODNN7EXAMPLE",
			mustKeep: "used",
		},
		{
			name:     "aws secret key by name",
			in:       `env AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY set`,
			mustHide: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			mustKeep: "set",
		},
		{
			name:     "github classic pat",
			in:       "token ghp_1234567890abcdefghijklmnopqrstuvwxyz here",
			mustHide: "ghp_1234567890abcdefghijklmnopqrstuvwxyz",
			mustKeep: "here",
		},
		{
			name:     "github oauth token",
			in:       "using gho_ABCDEFGH1234567890abcdefghijklmnopqr now",
			mustHide: "gho_ABCDEFGH1234567890abcdefghijklmnopqr",
			mustKeep: "now",
		},
		{
			name:     "github server token",
			in:       "actions ghs_wxyz1234567890ABCDEFGHIJKLMNOPQRSTuv ok",
			mustHide: "ghs_wxyz1234567890ABCDEFGHIJKLMNOPQRSTuv",
			mustKeep: "ok",
		},
		{
			name:     "github fine-grained pat",
			in:       "cfg github_pat_11ABCDEFG0abcdefghijklmno_1234567890abcdefghijklmnopqrstuvwxyzABCDEF done",
			mustHide: "github_pat_11ABCDEFG0abcdefghijklmno_1234567890abcdefghijklmnopqrstuvwxyzABCDEF",
			mustKeep: "done",
		},
		{
			name:     "slack bot token",
			in:       "slack xoxb-EXAMPLE-FAKE-SLACK-TOKEN-000000 wired",
			mustHide: "xoxb-EXAMPLE-FAKE-SLACK-TOKEN-000000",
			mustKeep: "wired",
		},
		{
			name:     "slack app token",
			in:       "hook xoxp-EXAMPLE-FAKE-SLACK-TOKEN-111111 fine",
			mustHide: "xoxp-EXAMPLE-FAKE-SLACK-TOKEN-111111",
			mustKeep: "fine",
		},
		{
			name:     "gcp private key id json",
			in:       `{"private_key_id":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678","client_email":"svc@proj.iam"}`,
			mustHide: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
			mustKeep: "client_email",
		},
		{
			name:     "gcp private key pem in json",
			in:       `{"private_key":"-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBg...\n-----END PRIVATE KEY-----\n","type":"service_account"}`,
			mustHide: "MIIEvQIBADANBg",
			mustKeep: "service_account",
		},
		{
			name:     "non-bearer access token json",
			in:       `{"access_token":"ya29.a0AfH6SMByzExampleTokenValue","expires_in":3600}`,
			mustHide: "ya29.a0AfH6SMByzExampleTokenValue",
			mustKeep: "expires_in",
		},
		{
			name:     "refresh token key=value",
			in:       "refresh_token=1//0eXampleRefreshTokenValue123 stored",
			mustHide: "1//0eXampleRefreshTokenValue123",
			mustKeep: "stored",
		},
		{
			name:     "email pii",
			in:       "user alice@example.com signed in",
			mustHide: "alice@example.com",
			mustKeep: "signed in",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in)
			if tc.mustHide != "" && strings.Contains(got, tc.mustHide) {
				t.Errorf("secret survived redaction: %q still in %q", tc.mustHide, got)
			}
			if tc.mustKeep != "" && !strings.Contains(got, tc.mustKeep) {
				t.Errorf("context lost: %q missing from %q", tc.mustKeep, got)
			}
		})
	}
}

func TestRedact_PrivateKeyBlock(t *testing.T) {
	in := "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEv...lines...AB\n-----END RSA PRIVATE KEY-----\nafter"
	got := Redact(in)
	if strings.Contains(got, "MIIEv") {
		t.Errorf("private key body survived: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("surrounding text lost: %q", got)
	}
}

func TestRedact_BenignUntouched(t *testing.T) {
	in := "level=info msg=\"reconcile complete\" ns=demo pod=web-1 duration=12ms"
	if got := Redact(in); got != in {
		t.Errorf("benign log line was modified:\n in:  %q\n got: %q", in, got)
	}
}

// TestRedact_NoFalsePositives proves ordinary text that superficially resembles
// a secret (image tags, UUIDs, commit SHAs, resource names, prose mentioning
// the word "github"/"token") survives untouched. Precision guard for SEC-2.
func TestRedact_NoFalsePositives(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"image tag", "pulling ghcr.io/lotsman/agent:1.2.3 for pod web"},
		{"docker io image", "image docker.io/library/nginx:1.27.4-alpine ready"},
		{"uuid", "request id 550e8400-e29b-41d4-a716-446655440000 handled"},
		{"commit sha in url", "see https://github.com/org/repo/commit/9f8e7d6c5b4a39281706 details"},
		{"word github", "the github integration is enabled for this org"},
		{"word token", "the token bucket refilled after 5s"},
		{"resource name", "deployment ghost-blog-frontend scaled to 3 replicas"},
		{"short gh-like", "flag gho_x set to on"},
		{"prose sentence", "The scheduler completed a reconcile pass over 12 namespaces without error."},
		{"semver list", "versions 1.2.3, 4.5.6 and 7.8.9 are supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in); got != tc.in {
				t.Errorf("ordinary text was redacted:\n in:  %q\n got: %q", tc.in, got)
			}
		})
	}
}

func TestRedact_Empty(t *testing.T) {
	if got := Redact(""); got != "" {
		t.Errorf("empty input produced %q", got)
	}
}
