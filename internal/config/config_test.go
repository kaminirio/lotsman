package config

import (
	"strings"
	"testing"
)

// TestValidateBackendURL covers SEC-5: empty and ordinary URLs pass, while
// cloud-metadata / link-local targets (as IP literals, IPv4-mapped IPv6, or
// well-known hostnames) are refused. Loopback literals and explicit localhost
// stay allowed for local-dev backends.
func TestValidateBackendURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"empty allowed", "", false},
		// Non-resolving hostname: best-effort lookup fails, so it is allowed.
		{"ordinary hostname", "https://backend.invalid:8481", false},
		{"loopback literal allowed", "http://127.0.0.1:8080", false},
		{"localhost allowed", "http://localhost:11434", false},
		{"localhost.localdomain allowed", "http://localhost.localdomain:9090", false},

		{"link-local metadata literal", "http://169.254.169.254/latest/meta-data", true},
		{"link-local range literal", "http://169.254.10.10/", true},
		{"ipv4-mapped ipv6 link-local", "http://[::ffff:169.254.169.254]/", true},
		{"gcp metadata hostname", "http://metadata.google.internal/computeMetadata/v1/", true},
		{"bare metadata hostname", "https://metadata/", true},

		{"bad scheme", "ftp://example.com/", true},
		{"no host", "http:///path", true},
		{"unparseable", "http://[::1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateBackendURL("test", c.raw)
			if c.wantErr && err == nil {
				t.Fatalf("ValidateBackendURL(%q) = nil, want error", c.raw)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("ValidateBackendURL(%q) = %v, want nil", c.raw, err)
			}
			if err != nil && !strings.Contains(err.Error(), "test") {
				t.Errorf("error should carry the config name: %v", err)
			}
		})
	}
}
