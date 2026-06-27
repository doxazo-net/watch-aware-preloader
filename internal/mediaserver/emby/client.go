// Package emby is a minimal read-only client for the Emby API.
package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a single Emby server with an API key.
type Client struct {
	base   string
	apiKey string
	http   *http.Client
}

// New validates the base URL and returns a Client. The base URL must be
// http/https with no embedded credentials, query, or fragment.
func New(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("base URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("base URL has no host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("base URL must not contain credentials, query, or fragment")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		base:   strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"),
		apiKey: apiKey,
		http:   httpClient,
	}, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	full := c.base + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // reason: close error on response body is not actionable

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("emby GET %s: status %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
