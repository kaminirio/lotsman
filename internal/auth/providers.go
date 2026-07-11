package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
)

// Identity is the verified profile an OAuth provider returns for the signed-in
// user. Email + Verified drive the SSO account-mapping rule (ADR-0011): only a
// verified email is trusted to link or auto-provision a local account.
type Identity struct {
	Email       string
	Subject     string // stable provider-side user id
	Verified    bool
	DisplayName string
}

// Provider abstracts an OAuth/OIDC identity provider so the login/callback
// handlers are provider-agnostic. Each configured provider (github, google,
// azure) supplies one implementation.
type Provider interface {
	Name() string
	AuthCodeURL(state string) string
	Exchange(ctx context.Context, code string) (*oauth2.Token, error)
	FetchIdentity(ctx context.Context, token *oauth2.Token) (Identity, error)
}

// ProviderCreds is the flat OAuth client configuration for a single provider. A
// provider is "configured" iff ClientID and ClientSecret (and, for azure,
// Tenant) are all non-empty.
type ProviderCreds struct {
	ClientID     string
	ClientSecret string
	Tenant       string // azure only
}

func (c ProviderCreds) configured(azure bool) bool {
	if c.ClientID == "" || c.ClientSecret == "" {
		return false
	}
	if azure && !validAzureTenant(c.Tenant) {
		return false
	}
	return true
}

// validAzureTenant reports whether tenant is a concrete single-tenant identifier
// (a directory GUID or a verified domain). An empty tenant and the multi-tenant
// authorities "common"/"organizations"/"consumers" are rejected: the
// graph.microsoft.com/oidc/userinfo endpoint does not return email_verified, so
// under a shared authority an attacker could sign in from an arbitrary tenant
// with a self-set email claim that would then be linked to an existing local
// account (account takeover). Requiring a concrete tenant confines logins to a
// directory the operator controls.
func validAzureTenant(tenant string) bool {
	switch strings.ToLower(strings.TrimSpace(tenant)) {
	case "", "common", "organizations", "consumers":
		return false
	}
	return true
}

// oauthProvider is the shared oauth2-backed implementation; the per-provider
// identity fetch differs and is injected via fetch. apiBase is the identity-API
// root (GitHub's api.github.com); it is a field so tests can point it at an
// httptest server.
type oauthProvider struct {
	name    string
	cfg     *oauth2.Config
	hc      *http.Client
	apiBase string
	fetch   func(ctx context.Context, p *oauthProvider, token *oauth2.Token) (Identity, error)
}

func (p *oauthProvider) Name() string { return p.name }

func (p *oauthProvider) AuthCodeURL(state string) string {
	return p.cfg.AuthCodeURL(state)
}

func (p *oauthProvider) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return p.cfg.Exchange(ctx, code)
}

func (p *oauthProvider) FetchIdentity(ctx context.Context, token *oauth2.Token) (Identity, error) {
	return p.fetch(ctx, p, token)
}

// getJSON performs a bearer-authenticated GET against url and decodes the JSON
// body into out.
func (p *oauthProvider) getJSON(ctx context.Context, token *oauth2.Token, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s API request failed: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s API returned %d: %s", p.name, resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s response: %w", p.name, err)
	}
	return nil
}

// buildProviders constructs the set of ENABLED providers from flat credentials.
// A provider absent from the map is simply not offered (its /auth/login/{name}
// 404s and /auth/providers reports it false).
func buildProviders(baseURL string, github, google ProviderCreds, azure ProviderCreds, hc *http.Client) map[string]Provider {
	providers := make(map[string]Provider)
	if github.configured(false) {
		providers["github"] = newGitHubProvider(baseURL, github, hc)
	}
	if google.configured(false) {
		providers["google"] = newGoogleProvider(baseURL, google, hc)
	}
	if azure.configured(true) {
		providers["azure"] = newAzureProvider(baseURL, azure, hc)
	}
	return providers
}

// --- GitHub -----------------------------------------------------------------

func newGitHubProvider(baseURL string, creds ProviderCreds, hc *http.Client) *oauthProvider {
	return &oauthProvider{
		name:    "github",
		hc:      hc,
		apiBase: "https://api.github.com",
		cfg: &oauth2.Config{
			ClientID:     creds.ClientID,
			ClientSecret: creds.ClientSecret,
			RedirectURL:  baseURL + "/auth/callback/github",
			Scopes:       []string{"read:user", "user:email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://github.com/login/oauth/authorize",
				TokenURL: "https://github.com/login/oauth/access_token",
			},
		},
		fetch: fetchGitHubIdentity,
	}
}

func fetchGitHubIdentity(ctx context.Context, p *oauthProvider, token *oauth2.Token) (Identity, error) {
	var user struct {
		Login string `json:"login"`
		Name  string `json:"name"`
		ID    int64  `json:"id"`
	}
	if err := p.getJSON(ctx, token, p.apiBase+"/user", &user); err != nil {
		return Identity{}, err
	}
	display := user.Name
	if display == "" {
		display = user.Login
	}

	// GitHub's /user email is unreliable (may be null/unverified); the verified
	// primary email lives in /user/emails, which is the identity we trust.
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := p.getJSON(ctx, token, p.apiBase+"/user/emails", &emails); err != nil {
		return Identity{}, err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return Identity{Email: e.Email, Subject: fmt.Sprintf("%d", user.ID), Verified: true, DisplayName: display}, nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			return Identity{Email: e.Email, Subject: fmt.Sprintf("%d", user.ID), Verified: true, DisplayName: display}, nil
		}
	}
	return Identity{Subject: fmt.Sprintf("%d", user.ID), DisplayName: display}, nil
}

// --- Google (OIDC) ----------------------------------------------------------

func newGoogleProvider(baseURL string, creds ProviderCreds, hc *http.Client) *oauthProvider {
	return &oauthProvider{
		name: "google",
		hc:   hc,
		cfg: &oauth2.Config{
			ClientID:     creds.ClientID,
			ClientSecret: creds.ClientSecret,
			RedirectURL:  baseURL + "/auth/callback/google",
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL: "https://oauth2.googleapis.com/token",
			},
		},
		fetch: fetchOIDCUserinfo("https://openidconnect.googleapis.com/v1/userinfo", false),
	}
}

// --- Azure AD / Entra (OIDC v2) --------------------------------------------

func newAzureProvider(baseURL string, creds ProviderCreds, hc *http.Client) *oauthProvider {
	base := "https://login.microsoftonline.com/" + creds.Tenant + "/oauth2/v2.0"
	return &oauthProvider{
		name: "azure",
		hc:   hc,
		cfg: &oauth2.Config{
			ClientID:     creds.ClientID,
			ClientSecret: creds.ClientSecret,
			RedirectURL:  baseURL + "/auth/callback/azure",
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  base + "/authorize",
				TokenURL: base + "/token",
			},
		},
		fetch: fetchOIDCUserinfo("https://graph.microsoft.com/oidc/userinfo", true),
	}
}

// oidcUserinfo is the standard OIDC userinfo response shared by Google and Azure.
// email_verified may arrive as a bool or a "true"/"false" string depending on the
// provider, so it is decoded leniently.
type oidcUserinfo struct {
	Sub           string          `json:"sub"`
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
	Name          string          `json:"name"`
}

// explicitVerified decodes the email_verified claim as a tri-state: val is its
// boolean value, present reports whether the provider sent the claim at all. The
// claim may arrive as a bool or a "true"/"false" string.
func (u oidcUserinfo) explicitVerified() (val, present bool) {
	switch string(u.EmailVerified) {
	case "true", `"true"`:
		return true, true
	case "false", `"false"`:
		return false, true
	default:
		return false, false
	}
}

// fetchOIDCUserinfo builds an identity fetcher that reads the standard OIDC
// userinfo endpoint. Shared by Google and Azure.
//
// trustDirectoryEmail governs the ABSENT-claim case only. Google returns
// email_verified, so it passes false: an unflagged email is untrusted. Azure's
// graph.microsoft.com/oidc/userinfo omits the claim entirely, so it passes true —
// safe ONLY because the Azure provider is pinned to a single managed tenant
// (validAzureTenant rejects common/organizations/consumers), making the
// directory-issued email authoritative. An EXPLICIT email_verified:false is always
// honored (denied) regardless of the flag.
func fetchOIDCUserinfo(endpoint string, trustDirectoryEmail bool) func(ctx context.Context, p *oauthProvider, token *oauth2.Token) (Identity, error) {
	return func(ctx context.Context, p *oauthProvider, token *oauth2.Token) (Identity, error) {
		var info oidcUserinfo
		if err := p.getJSON(ctx, token, endpoint, &info); err != nil {
			return Identity{}, err
		}
		val, present := info.explicitVerified()
		verified := val
		if !present && trustDirectoryEmail && info.Email != "" {
			verified = true
		}
		return Identity{
			Email:       info.Email,
			Subject:     info.Sub,
			Verified:    verified,
			DisplayName: info.Name,
		}, nil
	}
}
