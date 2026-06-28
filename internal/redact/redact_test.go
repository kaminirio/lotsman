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

func TestRedact_Empty(t *testing.T) {
	if got := Redact(""); got != "" {
		t.Errorf("empty input produced %q", got)
	}
}
