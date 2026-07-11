package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"
)

// apiClient is a tiny REST client for the control-plane API. cookie, when set,
// carries the lotsman_session for SSO-enabled control planes (unneeded in dev
// where SSO is off).
type apiClient struct {
	base   string
	cookie string
	http   *http.Client
}

// newAPIClient builds a client for base. When cookie is empty it falls back to the
// cached session written by `lotsman login`, so authenticated calls work without
// re-passing --cookie. An explicit --cookie always wins.
func newAPIClient(base, cookie string) *apiClient {
	if cookie == "" {
		cookie = loadCachedCookie()
	}
	return &apiClient{base: base, cookie: cookie, http: &http.Client{Timeout: 30 * time.Second}}
}

// do issues a request to path and decodes a JSON response body into out (when
// non-nil). It returns an error for any non-2xx status, surfacing the server's
// error message when present.
func (c *apiClient) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The control-plane API requires the X-Requested-With CSRF header on all
	// mutations (ADR-0011); set it unconditionally (harmless on GETs).
	req.Header.Set("X-Requested-With", "lotsman-cli")
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "lotsman_session", Value: c.cookie})
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			return fmt.Errorf("%s %s: %s (%s)", method, path, e.Error, resp.Status)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// commonFlags registers the --api and --cookie flags shared by every
// cluster-token subcommand.
func commonFlags(fs *flag.FlagSet) (*string, *string) {
	apiDefault := os.Getenv("LOTSMAN_API")
	if apiDefault == "" {
		apiDefault = "http://localhost:8080"
	}
	api := fs.String("api", apiDefault, "control-plane API base URL (env LOTSMAN_API)")
	cookie := fs.String("cookie", "", "lotsman_session cookie value (defaults to the cached `lotsman login` session)")
	return api, cookie
}

func runClusterToken(args []string) error {
	if len(args) == 0 {
		clusterTokenUsage()
		return fmt.Errorf("cluster-token: missing subcommand")
	}
	switch args[0] {
	case "generate":
		return clusterTokenGenerate(args[1:])
	case "list":
		return clusterTokenList(args[1:])
	case "revoke":
		return clusterTokenRevoke(args[1:])
	default:
		clusterTokenUsage()
		return fmt.Errorf("cluster-token: unknown subcommand %q", args[0])
	}
}

func clusterTokenUsage() {
	fmt.Fprint(os.Stderr, `lotsman cluster-token — manage per-cluster agent enrollment tokens

Usage:
  lotsman cluster-token generate <cluster> [--ttl-hours N] [--api URL] [--cookie SESSION]
  lotsman cluster-token list [--api URL] [--cookie SESSION]
  lotsman cluster-token revoke <id> [--api URL] [--cookie SESSION]
`)
}

func clusterTokenGenerate(args []string) error {
	fs := flag.NewFlagSet("cluster-token generate", flag.ContinueOnError)
	api, cookie := commonFlags(fs)
	ttl := fs.Int("ttl-hours", 0, "token lifetime in hours (0 = no expiry)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("generate: expected exactly one <cluster> argument")
	}
	cluster := fs.Arg(0)

	var resp struct {
		ID        string `json:"id"`
		Cluster   string `json:"cluster"`
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	body := map[string]any{"cluster": cluster, "ttl_hours": *ttl}
	if err := newAPIClient(*api, *cookie).do(http.MethodPost, "/api/v1/enrollment-tokens", body, &resp); err != nil {
		return err
	}
	// The plaintext token is printed to stdout ONCE and nowhere else — the hint
	// on stderr refers to "the token above" rather than echoing the secret onto a
	// second stream (which would widen exposure into CI logs / scrollback).
	fmt.Println(resp.Token)
	fmt.Fprintf(os.Stderr, "set agentToken.value to the token above on the lotsman-agent Helm release for cluster %s\n", resp.Cluster)
	return nil
}

func clusterTokenList(args []string) error {
	fs := flag.NewFlagSet("cluster-token list", flag.ContinueOnError)
	api, cookie := commonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	var resp struct {
		Tokens []struct {
			ID        string     `json:"id"`
			Cluster   string     `json:"cluster"`
			CreatedAt time.Time  `json:"created_at"`
			ExpiresAt *time.Time `json:"expires_at"`
			Revoked   bool       `json:"revoked"`
		} `json:"tokens"`
	}
	if err := newAPIClient(*api, *cookie).do(http.MethodGet, "/api/v1/enrollment-tokens", nil, &resp); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCLUSTER\tCREATED\tEXPIRES\tREVOKED")
	for _, t := range resp.Tokens {
		expires := "never"
		if t.ExpiresAt != nil {
			expires = t.ExpiresAt.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\n", t.ID, t.Cluster, t.CreatedAt.Format(time.RFC3339), expires, t.Revoked)
	}
	return tw.Flush()
}

func clusterTokenRevoke(args []string) error {
	fs := flag.NewFlagSet("cluster-token revoke", flag.ContinueOnError)
	api, cookie := commonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("revoke: expected exactly one <id> argument")
	}
	id := fs.Arg(0)
	if err := newAPIClient(*api, *cookie).do(http.MethodPost, "/api/v1/enrollment-tokens/"+id+"/revoke", nil, nil); err != nil {
		return err
	}
	fmt.Println("ok")
	return nil
}
