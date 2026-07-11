// Package argocd implements sources.DeploymentSource against ArgoCD. Change
// events (sync / rollout / image bumps) are the backbone of investigation, so
// this adapter is a first-class change-event source, not a status poller.
//
// It talks to ArgoCD over its REST API, configured per cluster (argocd_url /
// token), and should be kept self-contained behind the DeploymentSource seam.
package argocd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// maxBody bounds how much of an ArgoCD response we read.
const maxBody = 8 * 1024 * 1024

// defaultTimeout is applied to the fallback HTTP client so a hung ArgoCD backend
// cannot stall an investigation indefinitely. Production callers construct this
// adapter with a nil client, so the timeout must live at the adapter level.
const defaultTimeout = 30 * time.Second

// Client reads ArgoCD application history. Runs inside the agent.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New constructs an ArgoCD client. A nil http.Client falls back to a client with
// a sane default timeout (defaultTimeout) rather than http.DefaultClient, which
// has none — production callers pass nil.
func New(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: hc}
}

func (c *Client) Name() string { return "argocd" }

// argoApp is the subset of an ArgoCD Application we need: identity, destination,
// and sync history.
type argoApp struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Destination struct {
			Namespace string `json:"namespace"`
			Server    string `json:"server"`
			Name      string `json:"name"`
		} `json:"destination"`
	} `json:"spec"`
	Status struct {
		History []argoHistory `json:"history"`
	} `json:"status"`
}

// argoHistory is one entry in an Application's deployment history.
type argoHistory struct {
	Revision   string    `json:"revision"`
	DeployedAt time.Time `json:"deployedAt"`
	ID         int64     `json:"id"`
	Source     struct {
		RepoURL string `json:"repoURL"`
	} `json:"source"`
}

// appList is the /api/v1/applications envelope.
type appList struct {
	Items []argoApp `json:"items"`
}

// ChangeEvents returns deploy/rollout change signals for a resource within a
// window. It resolves the ArgoCD Application owning q.Resource, then maps each
// sync in q.Range to a SignalChange.
func (c *Client) ChangeEvents(ctx context.Context, q sources.ChangeQuery) ([]model.Signal, error) {
	apps, err := c.listApplications(ctx, q.Resource.Name)
	if err != nil {
		return nil, err
	}

	app, ok := bestMatch(apps, q.Resource)
	if !ok {
		// No application owns this resource — not an error, just no changes.
		return nil, nil
	}

	out := make([]model.Signal, 0, len(app.Status.History))
	for _, h := range app.Status.History {
		if !inRange(h.DeployedAt, q.Range) {
			continue
		}
		out = append(out, model.Signal{
			Kind:      model.SignalChange,
			Source:    "argocd",
			Timestamp: h.DeployedAt.UTC(),
			Title:     "deploy " + app.Metadata.Name,
			Resource:  q.Resource,
			Change: &model.ChangeRef{
				Source:   "argocd",
				App:      app.Metadata.Name,
				Revision: h.Revision,
				SyncedAt: h.DeployedAt.UTC(),
				URL:      c.appURL(app.Metadata.Name),
			},
		})
	}
	return out, nil
}

// listApplications fetches applications, optionally narrowing by a search term
// (the resource name) to keep the payload small on large ArgoCD instances.
func (c *Client) listApplications(ctx context.Context, search string) ([]argoApp, error) {
	params := url.Values{}
	if search != "" {
		params.Set("search", search)
	}
	endpoint := c.BaseURL + "/api/v1/applications"
	if enc := params.Encode(); enc != "" {
		endpoint += "?" + enc
	}

	var list appList
	if err := c.getJSON(ctx, endpoint, &list); err != nil {
		return nil, err
	}
	// A search filter may return nothing on servers that don't support it the
	// way we expect; fall back to the unfiltered list so matching still works.
	if len(list.Items) == 0 && search != "" {
		var all appList
		if err := c.getJSON(ctx, c.BaseURL+"/api/v1/applications", &all); err != nil {
			return nil, err
		}
		return all.Items, nil
	}
	return list.Items, nil
}

// getJSON performs an authenticated GET and decodes the body into v.
func (c *Client) getJSON(ctx context.Context, endpoint string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("argocd: create request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := sources.DoWithRetry(c.HTTP, req)
	if err != nil {
		return fmt.Errorf("argocd: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("argocd: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(v); err != nil {
		return fmt.Errorf("argocd: decode response: %w", err)
	}
	return nil
}

// bestMatch picks the application most likely to own ref. An exact name match
// wins; otherwise a destination-namespace match; otherwise no match.
func bestMatch(apps []argoApp, ref model.ResourceRef) (argoApp, bool) {
	var nsMatch *argoApp
	for i := range apps {
		app := apps[i]
		if ref.Name != "" && app.Metadata.Name == ref.Name {
			return app, true
		}
		if ref.Namespace != "" && app.Spec.Destination.Namespace == ref.Namespace && nsMatch == nil {
			nsMatch = &apps[i]
		}
	}
	if nsMatch != nil {
		return *nsMatch, true
	}
	return argoApp{}, false
}

// inRange reports whether t falls in the half-open window [Start, End). A zero
// bound is treated as unbounded.
func inRange(t time.Time, r sources.TimeRange) bool {
	if t.IsZero() {
		return false
	}
	if !r.Start.IsZero() && t.Before(r.Start) {
		return false
	}
	if !r.End.IsZero() && !t.Before(r.End) {
		return false
	}
	return true
}

// appURL builds a best-effort link to the application in the ArgoCD UI.
func (c *Client) appURL(app string) string {
	if c.BaseURL == "" || app == "" {
		return ""
	}
	return c.BaseURL + "/applications/" + app
}

var _ sources.DeploymentSource = (*Client)(nil)
