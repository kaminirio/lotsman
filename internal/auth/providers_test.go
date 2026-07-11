package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
)

func TestBuildProvidersConfiguredOnly(t *testing.T) {
	hc := http.DefaultClient
	providers := buildProviders(
		"http://localhost:8080",
		ProviderCreds{ClientID: "gh", ClientSecret: "s"}, // github configured
		ProviderCreds{},                                  // google not configured
		ProviderCreds{ClientID: "az", ClientSecret: "s"}, // azure missing tenant -> not configured
		hc,
	)
	if _, ok := providers["github"]; !ok {
		t.Error("github should be configured")
	}
	if _, ok := providers["google"]; ok {
		t.Error("google must not be configured (no creds)")
	}
	if _, ok := providers["azure"]; ok {
		t.Error("azure must not be configured (missing tenant)")
	}

	providers = buildProviders("http://x", ProviderCreds{}, ProviderCreds{}, ProviderCreds{ClientID: "a", ClientSecret: "b", Tenant: "t"}, hc)
	if _, ok := providers["azure"]; !ok {
		t.Error("azure with tenant should be configured")
	}
}

// TestBuildProvidersAzureTenantValidation verifies the Azure provider is only
// enabled for a concrete single-tenant authority. Empty or multi-tenant
// authorities (common/organizations/consumers) must be rejected: userinfo carries
// no email_verified, so a shared authority would trust an attacker-set email.
func TestBuildProvidersAzureTenantValidation(t *testing.T) {
	hc := http.DefaultClient
	creds := func(tenant string) ProviderCreds {
		return ProviderCreds{ClientID: "az", ClientSecret: "s", Tenant: tenant}
	}
	cases := []struct {
		tenant string
		enable bool
	}{
		{"", false},
		{"common", false},
		{"COMMON", false},
		{" common ", false},
		{"organizations", false},
		{"consumers", false},
		{"Consumers", false},
		{"contoso.onmicrosoft.com", true},
		{"11111111-2222-3333-4444-555555555555", true},
	}
	for _, tc := range cases {
		t.Run(tc.tenant, func(t *testing.T) {
			providers := buildProviders("http://x", ProviderCreds{}, ProviderCreds{}, creds(tc.tenant), hc)
			if _, ok := providers["azure"]; ok != tc.enable {
				t.Errorf("tenant %q: azure enabled = %v, want %v", tc.tenant, ok, tc.enable)
			}
		})
	}
}

func TestGitHubFetchIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"octocat","name":"The Octocat","id":42}`))
		case "/user/emails":
			_, _ = w.Write([]byte(`[{"email":"unverified@x.com","primary":false,"verified":false},{"email":"octo@github.com","primary":true,"verified":true}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &oauthProvider{name: "github", hc: srv.Client(), apiBase: srv.URL, fetch: fetchGitHubIdentity}
	id, err := p.FetchIdentity(context.Background(), &oauth2.Token{AccessToken: "tok"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if id.Email != "octo@github.com" || !id.Verified {
		t.Errorf("want verified primary email, got %+v", id)
	}
	if id.Subject != "42" || id.DisplayName != "The Octocat" {
		t.Errorf("unexpected subject/display: %+v", id)
	}
}

func TestGitHubFetchIdentityNoVerifiedEmail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"octocat","id":42}`))
		case "/user/emails":
			_, _ = w.Write([]byte(`[{"email":"x@x.com","primary":true,"verified":false}]`))
		}
	}))
	defer srv.Close()

	p := &oauthProvider{name: "github", hc: srv.Client(), apiBase: srv.URL, fetch: fetchGitHubIdentity}
	id, err := p.FetchIdentity(context.Background(), &oauth2.Token{AccessToken: "tok"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if id.Verified {
		t.Errorf("no verified email must yield Verified=false, got %+v", id)
	}
}

func TestOIDCFetchIdentity(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantEmail    string
		wantVerified bool
	}{
		{"google verified bool", `{"sub":"g1","email":"a@corp.com","email_verified":true,"name":"A"}`, "a@corp.com", true},
		{"verified false", `{"sub":"g2","email":"b@corp.com","email_verified":false,"name":"B"}`, "b@corp.com", false},
		{"verified as string", `{"sub":"g3","email":"c@corp.com","email_verified":"true","name":"C"}`, "c@corp.com", true},
		// Google-style (trustDirectoryEmail=false): an absent email_verified is NOT
		// trusted — unflagged email yields Verified=false so resolveSSOUser denies.
		{"absent flag untrusted", `{"sub":"g4","email":"d@corp.com","name":"D"}`, "d@corp.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			p := &oauthProvider{name: "oidc", hc: srv.Client(), fetch: fetchOIDCUserinfo(srv.URL, false)}
			id, err := p.FetchIdentity(context.Background(), &oauth2.Token{AccessToken: "tok"})
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if id.Email != tc.wantEmail || id.Verified != tc.wantVerified {
				t.Errorf("got %+v, want email=%s verified=%v", id, tc.wantEmail, tc.wantVerified)
			}
		})
	}
}

// TestOIDCAzureDirectoryTrust covers the trustDirectoryEmail=true fetcher used by
// the single-tenant Azure provider: an ABSENT email_verified is trusted (the
// tenant is a pinned managed directory, so the email is authoritative), but an
// EXPLICIT email_verified:false is still honored as unverified.
func TestOIDCAzureDirectoryTrust(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantVerified bool
	}{
		{"absent flag trusted (single-tenant directory)", `{"sub":"az1","email":"d@corp.com","name":"D"}`, true},
		{"explicit false still denied", `{"sub":"az2","email":"e@corp.com","email_verified":false,"name":"E"}`, false},
		{"explicit true trusted", `{"sub":"az3","email":"f@corp.com","email_verified":true,"name":"F"}`, true},
		{"absent flag with empty email untrusted", `{"sub":"az4","name":"G"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			p := &oauthProvider{name: "azure", hc: srv.Client(), fetch: fetchOIDCUserinfo(srv.URL, true)}
			id, err := p.FetchIdentity(context.Background(), &oauth2.Token{AccessToken: "tok"})
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if id.Verified != tc.wantVerified {
				t.Errorf("got verified=%v, want %v (%+v)", id.Verified, tc.wantVerified, id)
			}
		})
	}
}
