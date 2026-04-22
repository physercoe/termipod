// client.go — a tiny REST client for the hub.
//
// The MCP server is a *client* of the hub, not an in-process server: it
// translates MCP tool calls into REST calls. Keeping this client free of
// any dependency on internal/hostrunner or the hub's own packages is
// deliberate — the on-wire contract is the only thing we want to bind to,
// so the hub can evolve its internal client without breaking us.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hubClient is a minimal JSON-over-HTTP client for the hub REST API.
// It is not safe to share across goroutines that mutate its fields, but
// the stdio loop is single-threaded so that is a non-issue here.
type hubClient struct {
	baseURL string // no trailing slash
	token   string
	team    string
	http    *http.Client
}

func newHubClient(baseURL, token, team string) *hubClient {
	return &hubClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		team:    team,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// do issues a single request and decodes the JSON body into `out` (if non-nil).
// A non-nil body is marshalled as JSON. Query params are merged into the URL.
// Non-2xx responses become errors carrying the hub's error body so callers
// (and ultimately the MCP client) see the real cause, not a stripped
// "internal error" string.
func (c *hubClient) do(method, path string, query url.Values, body any, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, reqBody)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("hub %s %s: %d %s", method, path, resp.StatusCode, bytes.TrimSpace(raw))
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	// We decode into json.RawMessage holders rather than strongly-typed
	// structs because MCP tool results are opaque JSON to the client — the
	// MCP server's job is protocol translation, not schema normalization.
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// teamPath prefixes a team-scoped resource path with the configured team id.
func (c *hubClient) teamPath(suffix string) string {
	return "/v1/teams/" + url.PathEscape(c.team) + suffix
}
