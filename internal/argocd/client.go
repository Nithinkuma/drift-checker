package argocd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ClientConfig holds options for the ArgoCD HTTP client.
type ClientConfig struct {
	TLSSkipVerify bool
	Timeout       time.Duration
	MaxApps       int
}

// Client is a read-only ArgoCD REST API client.
type Client struct {
	baseURL    string
	token      string
	maxApps    int
	httpClient *http.Client
}

// NewClient creates a Client. baseURL must not have a trailing slash.
func NewClient(baseURL, token string, cfg ClientConfig) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	maxApps := cfg.MaxApps
	if maxApps <= 0 {
		maxApps = 500
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.TLSSkipVerify}, //nolint:gosec
	}
	return &Client{
		baseURL: baseURL,
		token:   token,
		maxApps: maxApps,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
	}
}

// get performs an authenticated GET and JSON-decodes the response into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &ArgoError{Code: "ARGOCD_UNREACHABLE", Message: err.Error(), Status: 0}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &ArgoError{Code: "ARGOCD_AUTH_FAILED", Message: "token rejected by ArgoCD", Status: resp.StatusCode}
	}
	if resp.StatusCode >= 400 {
		return &ArgoError{
			Code:    "ARGOCD_ERROR",
			Message: fmt.Sprintf("ArgoCD returned HTTP %d for %s", resp.StatusCode, path),
			Status:  resp.StatusCode,
		}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// ListApplicationSets returns all ApplicationSets for a project.
func (c *Client) ListApplicationSets(ctx context.Context, project string) ([]ApplicationSet, error) {
	path := "/api/v1/applicationsets?projects=" + url.QueryEscape(project)
	var list ApplicationSetList
	if err := c.get(ctx, path, &list); err != nil {
		return nil, fmt.Errorf("list applicationsets: %w", err)
	}
	return list.Items, nil
}

// ListApplications returns all Applications for a project, following pagination.
func (c *Client) ListApplications(ctx context.Context, project string) ([]Application, error) {
	var all []Application
	continueToken := ""

	for {
		path := fmt.Sprintf("/api/v1/applications?projects=%s&limit=%d",
			url.QueryEscape(project), c.maxApps)
		if continueToken != "" {
			path += "&continue=" + url.QueryEscape(continueToken)
		}

		var list ApplicationList
		if err := c.get(ctx, path, &list); err != nil {
			return nil, fmt.Errorf("list applications: %w", err)
		}
		all = append(all, list.Items...)

		continueToken = list.Metadata.Continue
		if continueToken == "" {
			break
		}
	}
	return all, nil
}

// ListClusters returns all registered ArgoCD clusters.
func (c *Client) ListClusters(ctx context.Context) ([]Cluster, error) {
	var list ClusterList
	if err := c.get(ctx, "/api/v1/clusters", &list); err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	return list.Items, nil
}

// ListProjects returns all ArgoCD AppProject names.
func (c *Client) ListProjects(ctx context.Context) ([]string, error) {
	var list AppProjectList
	if err := c.get(ctx, "/api/v1/projects", &list); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	names := make([]string, 0, len(list.Items))
	for _, p := range list.Items {
		names = append(names, p.Metadata.Name)
	}
	return names, nil
}
