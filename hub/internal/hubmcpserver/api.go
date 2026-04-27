// api.go — exported helpers that let the hub itself reuse this package's
// tool catalog in-process. The standalone hub-mcp-server daemon and the
// hub's own /mcp/{token} endpoint both walk buildTools(), so there's a
// single source of truth for the rich-authority surface (projects, plans,
// runs, agents.spawn, schedules, channels, …).
//
// In-process callers inject an http.RoundTripper that dispatches into the
// hub's chi router via httptest.NewRecorder, so no real network hop is
// required. Out-of-process callers (the daemon) leave Transport nil and
// the default http.Client talks to a public hub URL.
package hubmcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ToolCatalog returns MCP tool definitions in the shape mcp.go expects:
// `[]map[string]any` with name/description/inputSchema. The hub's own MCP
// server appends this to its own catalog, so a single bridge entry in
// every spawned agent's .mcp.json reaches the union of both surfaces.
func ToolCatalog() []map[string]any {
	tools := buildTools()
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		var schemaObj any
		_ = json.Unmarshal(t.InputSchema, &schemaObj)
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schemaObj,
		})
	}
	return out
}

// ToolNames returns just the names from buildTools() — useful for tier
// annotations, schema audits, and feature-flag tables on the hub side.
func ToolNames() []string {
	tools := buildTools()
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// HasTool reports whether the named tool exists in this package's catalog.
// The hub's MCP dispatcher uses this to decide whether to fall through to
// Dispatch() before returning "unknown tool".
func HasTool(name string) bool {
	_, ok := findTool(buildTools(), name)
	return ok
}

// Dispatch runs one tool by name. When `transport` is non-nil the call is
// in-process (the transport's RoundTrip handles the request directly);
// when nil, the package's default http.Client makes real HTTP calls to
// `baseURL`. `token` is forwarded as Authorization: Bearer; `team` scopes
// the team-scoped paths the tools build via teamPath().
func Dispatch(transport http.RoundTripper, baseURL, token, team, name string, args map[string]any) (any, error) {
	c := newHubClient(baseURL, token, team)
	if transport != nil {
		c.http = &http.Client{Transport: transport, Timeout: 30 * time.Second}
	}
	tool, ok := findTool(buildTools(), name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return tool.call(c, args)
}
