// mcp_authority.go — in-process bridge for the rich-authority MCP tool
// catalog (projects, plans, runs, agents.spawn, schedules, channels,
// a2a.invoke, …). The catalog itself lives in hubmcpserver/tools.go and
// is consumed by both the standalone daemon (cmd/hub-mcp-server) and
// this in-process hookup. By appending it to the hub's own /mcp/{token}
// endpoint, every spawned agent reaches the full surface through one
// .mcp.json entry instead of running a second MCP daemon alongside the
// stdio↔HTTP bridge.
//
// The trick is the chi-router transport: hubmcpserver's tools build
// HTTP requests against teamPath("/projects") and friends, and the
// transport here invokes s.router.ServeHTTP directly via
// httptest.NewRecorder. No socket, no extra round-trip, no extra
// process — but the existing auth/audit/broadcast middleware all run
// the same way they would for a real network call.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/termipod/hub/internal/hubmcpserver"
)

// chiRouterTransport adapts s.router into an http.RoundTripper so the
// hubmcpserver tool closures (which call hubClient.do, which calls
// http.Client.Do) end up dispatching through the hub's own routes.
//
// The transport is stateless — every call records into a fresh
// httptest.ResponseRecorder — so it's safe to reuse the zero value.
type chiRouterTransport struct{ router http.Handler }

func (t chiRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.router.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// authorityToolDefs returns the rich-authority MCP catalog. Appended to
// mcpToolDefs() in mcp.go.
func authorityToolDefs() []map[string]any {
	return hubmcpserver.ToolCatalog()
}

// hasAuthorityTool reports whether the named tool is part of the
// rich-authority catalog. Used by dispatchTool to decide whether to
// fall through after the in-process switch misses.
func hasAuthorityTool(name string) bool {
	return hubmcpserver.HasTool(name)
}

// dispatchAuthorityTool runs one tool by name in-process. The MCP path
// token is also the agent's HTTP bearer token (same auth_tokens row
// resolves both — see resolveMCPToken), so we forward it as
// Authorization: Bearer on the in-process REST call. Auth middleware
// authenticates the agent the same way it would for a network client.
//
// The base URL is a placeholder ("http://hub") because the transport
// ignores the host part — chi.ServeHTTP routes purely on Method + Path.
func (s *Server) dispatchAuthorityTool(ctx context.Context, agentToken, team, name string, args map[string]any) (any, *jrpcError) {
	_ = ctx // hubmcpserver doesn't take a Context; the transport runs synchronously and short.
	result, err := hubmcpserver.Dispatch(
		chiRouterTransport{router: s.router},
		"http://hub",
		agentToken,
		team,
		name,
		args,
	)
	if err != nil {
		return nil, &jrpcError{Code: -32603, Message: err.Error()}
	}
	return mcpResultJSON(result), nil
}

// dispatchAuthorityToolRaw is the variant called from dispatchTool —
// it parses the standard tools/call params shape (name + arguments),
// then forwards.
func (s *Server) dispatchAuthorityToolRaw(ctx context.Context, agentToken, team, name string, raw json.RawMessage) (any, *jrpcError) {
	var args map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &jrpcError{Code: -32602, Message: "bad arguments: " + err.Error()}
		}
	}
	return s.dispatchAuthorityTool(ctx, agentToken, team, name, args)
}
