// Package emby is a minimal read-only client for the Emby API.
package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to a single Emby server with an API key.
type Client struct {
	base       *url.URL
	apiKey     string
	httpClient *http.Client
}

// validateBaseURL enforces the media-server trust boundary. The configured base
// URL must be a plain absolute http/https URL with a host and nothing that could
// divert a request elsewhere: no opaque form, embedded credentials, query, or
// fragment. It returns the normalized base (scheme/host/path only, trailing
// slash trimmed) so request URLs can be built with url.URL.JoinPath rather than
// string concatenation, which keeps a server-supplied path element from
// escaping the configured host.
//
// Private/LAN hosts are intentionally allowed: this tool's purpose is to talk to
// a media server that normally lives on the local network, so blocking RFC1918
// addresses would break the documented setup rather than harden it.
func validateBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL: %w", err)
	}
	if u.Opaque != "" {
		return nil, fmt.Errorf("base URL must be a plain absolute URL, not opaque")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("base URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("base URL has no host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("base URL must not contain credentials, query, or fragment")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("base URL has invalid port %q", p)
		}
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host, Path: strings.TrimRight(u.Path, "/")}, nil
}

// New validates the base URL at the trust boundary and returns a Client.
func New(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	base, err := validateBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	// Refuse to follow redirects: X-Emby-Token is a non-sensitive header that
	// net/http would re-send on a cross-host 30x hop, leaking the API key.
	// Copy the client so we don't mutate the caller's value.
	hc := *httpClient
	hc.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		base:       base,
		apiKey:     apiKey,
		httpClient: &hc,
	}, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	// JoinPath joins and cleans the path against the validated base, so a path
	// element can never redirect the request to another host.
	u := c.base.JoinPath(path)
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
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
