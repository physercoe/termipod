package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
	"github.com/termipod/hub/internal/events"
	"github.com/termipod/hub/internal/hubmcpserver"
	"github.com/termipod/hub/internal/mcpver"
	"github.com/termipod/hub/internal/mcpwire"
)

// mcp.go implements a minimal MCP-over-HTTP server mounted at
// /mcp/{token}. Each agent gets its own URL with its bearer token
// baked in (this is the "MCP HTTP per-agent URL encoding" from the
// plan §12A) so claude-code can be pointed at it without any custom
// auth headers.
//
// Wire format: JSON-RPC 2.0 inside the request/response body.
// Methods implemented:
//   initialize       — protocol handshake
//   tools/list       — enumerate the tools this agent can call
//   tools/call       — dispatch a tool by name
//
// Each tool call resolves the agent from the path token, then runs the
// equivalent HTTP-level operation. The agent's team/role scope in the
// token gates what it can see — identical trust model to the bearer path.

// --- JSON-RPC envelopes ---

type jrpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jrpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jrpcError      `json:"error,omitempty"`
}

type jrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// The supported protocol-version set and the negotiation function live
// in internal/mcpver (ADR-063 D1) — this file used to carry one of the
// three byte-identical copies, and the agy downgrade-fatal rationale
// moved there with it. The response-shape vocabulary of the revisions
// we speak lives in internal/mcpwire (ADR-063 D3).

// mcpCommonParams is the slice of a request's params every method shares
// — the initialize negotiation ask and the 2026-07-28 per-request `_meta`
// envelope. Decoded once, before the method switch, because both feed
// the same question: which revision is this exchange speaking?
type mcpCommonParams struct {
	ProtocolVersion string              `json:"protocolVersion"`
	Meta            mcpwire.RequestMeta `json:"_meta"`
}

// mcpExchangeVersion resolves the revision one request is speaking, or
// "" when the client declared nothing at all.
//
// Three channels can carry the declaration and they are ranked, not
// merged: the initialize params (the client's explicit ask, and the only
// signal a 2024/2025-era handshake has), then the MCP-Protocol-Version
// header (SEP-2243 — a 2025-06-18+ client MUST send it on every request
// after initialization), then the `_meta` envelope (SEP-2575 — how a
// stateless 2026-07-28 request identifies itself with no handshake at
// all). Unknown values in one channel fall through to the next, and the
// floor answers when no channel names a revision we implement.
//
// The empty return is load-bearing. This transport is stateless — every
// POST arrives with a fresh token and no memory of an earlier
// initialize — so when a client declares nothing we genuinely do not
// know what it speaks. Stamping a guessed version on the response would
// be a claim we cannot support; the caller omits the header instead.
func mcpExchangeVersion(r *http.Request, p mcpCommonParams) string {
	asks := []string{
		p.ProtocolVersion,
		r.Header.Get(mcpwire.HeaderProtocolVersion),
		p.Meta.ProtocolVersion,
	}
	declared := false
	for _, a := range asks {
		if a != "" {
			declared = true
			break
		}
	}
	if !declared {
		return ""
	}
	return mcpver.NegotiateFirst(asks...)
}

// handleMCP is the single HTTP entry point for the bridge. It routes on
// the JSON-RPC method name.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	agent, scope, err := s.resolveMCPToken(r.Context(), tok)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid mcp token")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req jrpcReq
	if err := json.Unmarshal(body, &req); err != nil {
		// Distinguish "your bytes were not JSON" from "your JSON was
		// not a request object". The second case is almost always a
		// JSON-RPC batch — a well-formed array we have never
		// implemented — and answering -32700 parse error told that
		// client its transport was corrupt, which is the one diagnosis
		// that leads nowhere. -32600 invalid request points at the
		// envelope, and the message names the fix.
		code, msg := -32700, "parse error"
		if json.Valid(body) {
			code, msg = -32600, "invalid request: send one JSON-RPC request object per POST (batches are not supported)"
		}
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0",
			Error:   &jrpcError{Code: code, Message: msg},
		})
		return
	}

	var params mcpCommonParams
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}
	ver := mcpExchangeVersion(r, params)
	if ver != "" {
		w.Header().Set(mcpwire.HeaderProtocolVersion, ver)
	}

	// JSON-RPC 2.0 §4.1: a request without an `id` is a Notification —
	// "The Server MUST NOT reply to a Notification." Pre-v1.0.656 the
	// default-case error path below blindly wrote an error response for
	// every unknown method, including notifications, which produced an
	// unsolicited frame on the client's stdin. agy 1.0.1 sends
	// `notifications/roots/list_changed` to every MCP server it
	// connects to; the unsolicited error frame our hub returned looked
	// to agy like a protocol violation, agy closed its MCP client, and
	// every subsequent tools/call surfaced as `connection closed:
	// client is closing: invalid request`. Drop responses to all
	// notifications here, before the per-method switch — most methods
	// arrive as requests, the few that arrive as notifications
	// (`notifications/initialized`, `notifications/roots/list_changed`,
	// `notifications/cancelled`, …) all want the same 204-no-content
	// treatment.
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if isNotification {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch req.Method {
	case "initialize":
		// Echo the client's requested revision when we know it — strict
		// clients (agy 1.0.1 observed) treat a downgrade as a fatal
		// protocol error. mcpExchangeVersion already ranked the
		// channels; a handshake that named nothing negotiates the floor.
		negotiated := ver
		if negotiated == "" {
			negotiated = mcpver.Floor
			w.Header().Set(mcpwire.HeaderProtocolVersion, negotiated)
		}
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0", ID: req.ID,
			Result: mcpIdentity(mcpwire.StampResult(map[string]any{
				"protocolVersion": negotiated,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				// 2026-07-28 moved server identity into every result's
				// `_meta` (SEP-2575); the body copy stays for the
				// 2024/2025-era clients that read it here.
				"serverInfo": map[string]any{
					"name":    mcpServerName,
					"version": ServerVersion,
				},
			})),
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusNoContent)
	case "server/discover":
		// 2026-07-28 SEP-2575: the stateless replacement for the
		// initialize handshake, and the blessed backwards-compat probe
		// on stdio. A client MAY call it before anything else to pick a
		// revision up front — so unlike initialize it answers with the
		// whole set rather than one negotiated choice.
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0", ID: req.ID,
			Result: mcpIdentity(mcpwire.StampCacheableList(map[string]any{
				"protocolVersions": mcpver.Supported(),
				"capabilities":     map[string]any{"tools": map[string]any{}},
			})),
		})
	case "tools/list":
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0", ID: req.ID,
			Result: mcpIdentity(mcpwire.StampCacheableList(map[string]any{
				"tools": mcpToolListDefs(),
			})),
		})
	case "tools/call":
		// Pass the path token through to dispatchTool so the
		// authority-tool fall-through can forward it as the bearer
		// when invoking the hub's REST surface in-process. The
		// path token IS the agent's auth_tokens row, so the same
		// string authenticates both MCP and HTTP.
		res, jerr := s.dispatchTool(r.Context(), agent, tok, scope, req.Params)
		resp := jrpcResp{JSONRPC: "2.0", ID: req.ID}
		if jerr != nil {
			resp.Error = jerr
		} else {
			// Tool results are already resultType-stamped by the
			// mcpResult* builders — every dispatch path goes through
			// one. Identity rides on the envelope regardless of which.
			if m, ok := res.(map[string]any); ok {
				res = mcpIdentity(m)
			}
			resp.Result = res
		}
		writeJRPC(w, resp)
	case "ping":
		// Removed in 2026-07-28 (SEP-2575). Still served: the revisions
		// that have it are the ones our engines actually speak, and a
		// 2026-era client simply never calls it.
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0", ID: req.ID,
			Result: mcpIdentity(mcpwire.StampResult(map[string]any{})),
		})
	default:
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0", ID: req.ID,
			Error: &jrpcError{Code: -32601, Message: "method not found: " + req.Method},
		})
	}
}

// resolveMCPToken: the URL token is the plaintext bearer token (we hash
// and look up in auth_tokens). scope_json is parsed for team + agent_id.
func (s *Server) resolveMCPToken(ctx context.Context, tok string) (agentID string, scope mcpScope, err error) {
	hash := auth.HashToken(tok)
	var scopeJSON string
	err = s.db.QueryRowContext(ctx,
		`SELECT scope_json FROM auth_tokens WHERE token_hash = ? AND revoked_at IS NULL`, hash).
		Scan(&scopeJSON)
	if err != nil {
		return "", scope, err
	}
	if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
		return "", scope, err
	}
	if scope.Team == "" {
		scope.Team = defaultTeamID
	}
	return scope.AgentID, scope, nil
}

type mcpScope struct {
	Team    string `json:"team"`
	Role    string `json:"role"`
	AgentID string `json:"agent_id"`
}

func writeJRPC(w http.ResponseWriter, resp jrpcResp) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// --- Tool catalog ---

func mcpToolDefs() []map[string]any {
	// ADR-033 (W1–W6): every MCP tool is in one of the two ToolSpec
	// registries — the authority registry in hubmcpserver (REST-adapter
	// tools) and the native registry in native_tools.go ((*Server)-
	// method tools). The catalog is their composition: each tool's
	// canonical entry plus one [DEPRECATED] entry per alias. The
	// hand-written legacy defs (mcpToolDefsBase/Extra/orchestrationToolDefs)
	// were retired in W6.2.
	var all []map[string]any
	all = append(all, hubmcpserver.RegistryCatalogDefs()...)
	all = append(all, nativeRegistryCatalogDefs()...)
	// Annotate each definition with its tier (server-authored,
	// per tiers.go). Custom field; MCP clients ignore unknown
	// keys, so this is purely informational over the wire.
	// The authoritative gating lives in `permission_prompt`'s
	// attention payload (mcp_more.go), so even an MCP client
	// that strips this field still sees the right approval card.
	for _, def := range all {
		if name, _ := def["name"].(string); name != "" {
			def["tier"] = tierFor(name)
		}
	}
	return all
}

// mcpToolListDefs is the slim projection of the catalog served over the
// wire by tools/list (ADR-031 W2.a). It substitutes each tool's
// one-line `short` into the MCP-standard `description` field and drops
// the long body — that body ships in every dispatch's context if left
// in the catalog (~30KB), so it is fetched per-tool via tools_get
// instead (~5KB catalog). `description` stays present and meaningful,
// so a client that reads only `description` keeps working.
//
// MCP-spec-noncompliant tool names (dots, spaces — anything outside
// `[A-Za-z0-9_-]`) are dropped from the wire here. ADR-031/033 retains
// dot-named aliases like `documents.list` for backwards-compat with
// agents still calling the legacy names; the dispatcher accepts them.
// But strict MCP clients (agy 1.0.1 confirmed) reject the WHOLE
// tools/list response with `invalid request` when ANY tool name
// violates the regex — so an agy session sees zero tools and every
// call_mcp_tool fails with `server name termipod failed to load:
// failed to get tools: calling "tools/list": invalid request`. Every
// dot-named entry has a snake_case sibling in the catalog (verified
// at the call site below), so this filter loses no functionality on
// the wire; legacy callers can still dispatch dot-named tools via
// tools/call because the registry resolves both spellings.
func mcpToolListDefs() []map[string]any {
	defs := mcpToolDefs()
	out := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		name, _ := def["name"].(string)
		if !isMCPCompliantToolName(name) {
			continue
		}
		short, _ := def["short"].(string)
		entry := map[string]any{
			"name":        def["name"],
			"short":       short,
			"description": short,
			"inputSchema": def["inputSchema"],
		}
		if tier, ok := def["tier"]; ok {
			entry["tier"] = tier
		}
		// The spec's own metadata survives the slim projection (ADR-063
		// D5): `annotations.readOnlyHint` is what drives a spec-aware
		// client's auto-approve UX, and tools/list is the only place
		// most clients ever look. Dropping it here would leave the
		// dual-publish visible only to whoever calls tools_get.
		if ann, ok := def["annotations"]; ok {
			entry["annotations"] = ann
		}
		out = append(out, entry)
	}
	return out
}

// isMCPCompliantToolName reports whether `name` matches the MCP spec's
// tool-name production: a non-empty string of `[A-Za-z0-9_-]`. Anything
// else (dots, dollar signs, slashes, …) is dropped from the wire to
// keep strict clients (agy 1.0.1) from rejecting the whole tools/list
// payload — they validate every entry and reject the batch on the
// first failure.
func isMCPCompliantToolName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// --- Dispatch ---

type toolCallIn struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) dispatchTool(ctx context.Context, agentID, agentToken string, scope mcpScope, params json.RawMessage) (any, *jrpcError) {
	var call toolCallIn
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &jrpcError{Code: -32602, Message: "invalid params"}
	}
	// Operation-scope role gate (ADR-016). Returns nil for principal
	// tokens (agentID == ""); denies tools not in the role's allow set.
	// Engine-internal subagents share their parent's MCP client, hence
	// inherit this gate by construction (ADR-016 D5).
	if jerr := s.authorizeMCPCall(ctx, agentID, scope.Role, call.Name); jerr != nil {
		return nil, jerr
	}
	// Resolve the canonical tool name once. A registry tool (ADR-033)
	// may be called by its canonical name or a deprecated alias; the
	// security gates below key on the canonical name so they fire
	// whichever spelling the caller used — renaming a gated tool must
	// not silently bypass its gate.
	canonicalName := call.Name
	if spec, ok, _ := hubmcpserver.LookupToolSpec(call.Name); ok {
		canonicalName = spec.Name
	}
	// A2A target restriction (ADR-016 D4). Workers may invoke
	// a2a_invoke only against their parent steward. Stewards are
	// unrestricted. Skipped for principal tokens (agentID == "").
	if canonicalName == "a2a_invoke" && agentID != "" {
		var args map[string]any
		if len(call.Arguments) > 0 {
			_ = json.Unmarshal(call.Arguments, &args)
		}
		role := s.resolveAgentRole(agentID, scope.Role)
		if jerr := s.authorizeA2ATarget(agentID, role, scope.Team, args); jerr != nil {
			return nil, jerr
		}
	}
	// agents_spawn project-binding gate (ADR-025 W9). General steward
	// blocked outright; project-bound spawns require the caller to be
	// that project's steward. Principal tokens bypass.
	if canonicalName == "agents_spawn" {
		if jerr := s.authorizeAgentsSpawn(agentID, call.Arguments); jerr != nil {
			return nil, jerr
		}
		// Auto-inject parent_agent_id from the calling agent so
		// agent_spawns.parent_agent_id is never NULL on the MCP path.
		// Without this both get_parent_thread (returns empty) and the
		// a2a worker→parent permission check (denies with "not
		// permitted") fail downstream — the child has no traceable
		// parent in the spawn table even though the gate above already
		// proved who the caller is. Caller-supplied value wins (REST
		// clients with a principal token still pass their own).
		if agentID != "" {
			if injected, ok := injectParentAgentID(call.Arguments, agentID); ok {
				call.Arguments = injected
			}
		}
	}
	// Enforce the tool's declared InputSchema at the dispatcher
	// boundary, ahead of either branch below. Required fields, enums,
	// and types declared in the catalog become real rejections
	// instead of being silently forwarded to a handler that may or
	// may not check the same fields. The host_id-missing agents.spawn
	// incident motivated this gate (and the matching audit of every
	// schema vs handler in the catalog).
	//
	// We surface the violation as an isError content block (matching
	// how tool-call handler errors already surface) so an LLM agent
	// reading the result sees the "host_id required" sentence and can
	// self-correct on the next turn.
	if spec, ok, _ := lookupToolSpec(call.Name); ok && len(spec.InputSchema) > 0 {
		var args map[string]any
		if len(call.Arguments) > 0 {
			_ = json.Unmarshal(call.Arguments, &args)
		}
		if err := hubmcpserver.ValidateArgs(spec.InputSchema, args); err != nil {
			return mcpResultError(err.Error()), nil
		}
	}
	// ADR-033 (W1–W6): unified-registry dispatch. No tool is routed by
	// a literal switch case any more — every tool, including the catalog
	// meta-tool tools_get, resolves through the two ToolSpec registries
	// under a canonical name or a deprecated alias.
	//
	// Authority registry (W1–W4): the tool has a buildTools() REST
	// adapter — forward to it under spec.Backend (== spec.Name, the same
	// canonical snake_case name the agent called).
	if spec, ok, _ := hubmcpserver.LookupToolSpec(call.Name); ok {
		return s.dispatchAuthorityToolRaw(ctx, agentToken, scope.Team, spec.Backend, call.Arguments)
	}
	// Native registry (W4n + W6.2): the tool's handler is a (*Server)
	// method — invoke it via the native handler map.
	if h, ok := nativeHandlerFor(call.Name); ok {
		return h(s, ctx, agentID, scope, call.Arguments)
	}
	// No defensive fall-through to a raw buildTools() name. Every tool is
	// registered under its canonical name (TestEveryAuthorityToolRegistered),
	// so anything reaching here is a non-canonical spelling — a retired
	// dotted/legacy alias (WS1.1) — and must 404 rather than silently
	// dispatch by its REST-adapter name.
	return nil, &jrpcError{Code: -32601, Message: "unknown tool: " + call.Name}
}

// mcpServerName is what this server calls itself on the wire — the
// `.mcp.json` key every spawned agent gets, so it is effectively frozen
// (ADR-033 D-5: wire-visible names do not change for spec reasons).
const mcpServerName = "termipod-hub"

// mcpIdentity attaches this server's identity to a result's `_meta`,
// where 2026-07-28 moved it from the initialize response. Applied on the
// way out for every method, so a stateless client that never handshakes
// still learns who answered.
func mcpIdentity(m map[string]any) map[string]any {
	return mcpwire.WithServerInfo(m, mcpServerName, ServerVersion)
}

func mcpResultText(text string) map[string]any {
	return mcpwire.StampResult(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	})
}

// mcpResultJSON renders a value as the portable text block every MCP
// client can read, AND — for object-shaped values — as
// `structuredContent` for those that prefer parsed data (ADR-063 D3;
// see mcpwire.AttachStructuredContent for why arrays stay text-only
// until responses stop being version-blind).
//
// Both, not either. The text block is not a leftover to be retired: it
// is what an LLM actually reads when a client renders the result into
// the model's context, and dropping it would leave older clients — and
// any client that renders content blocks only — with an empty answer.
// The structured copy costs one extra marshal of data we already have.
func mcpResultJSON(v any) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return mcpwire.AttachStructuredContent(mcpResultText(string(b)), v, b)
}

// mcpResultError builds a tool result flagged isError. Used for
// recoverable, agent-visible failures — the agent sees the message
// inline and can retry — as opposed to a *jrpcError protocol fault.
func mcpResultError(text string) map[string]any {
	r := mcpResultText(text)
	r["isError"] = true
	return r
}

// --- Tool impls ---

type toolsGetArgs struct {
	ToolName string `json:"tool_name"`
}

// mcpToolsGet returns the full catalog entry — description + input
// schema — for one tool by name (ADR-031 D-2). It resolves against
// the composed mcpToolDefs() catalog (both ToolSpec registries), so
// it can describe any tool an agent can actually call. tools_get is
// itself a native tool (W6.2); this handler is its dispatch target.
func (s *Server) mcpToolsGet(raw json.RawMessage) (any, *jrpcError) {
	var a toolsGetArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ToolName == "" {
		return nil, &jrpcError{Code: -32602, Message: "tool_name required"}
	}
	for _, def := range mcpToolDefs() {
		if name, _ := def["name"].(string); name == a.ToolName {
			return mcpResultJSON(def), nil
		}
	}
	// An unknown name is a recoverable, agent-visible failure — not a
	// protocol fault — so return an isError content block: the agent
	// sees the message inline and can retry against tools/list.
	return mcpResultError(fmt.Sprintf(
		"unknown tool %q; call tools/list for the available set", a.ToolName)), nil
}

type postMessageArgs struct {
	ChannelID string `json:"channel_id"`
	Text      string `json:"text"`
}

func (s *Server) mcpPostMessage(ctx context.Context, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a postMessageArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ChannelID == "" || a.Text == "" {
		return nil, &jrpcError{Code: -32602, Message: "channel_id and text required"}
	}
	parts := []events.Part{{Kind: "text", Text: a.Text}}
	partsJSON, _ := json.Marshal(parts)
	now := time.Now().UTC()
	id := NewID()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO events (
			id, schema_version, ts, received_ts, channel_id, type,
			from_id, to_ids_json, parts_json, metadata_json
		) VALUES (?, 1, ?, ?, ?, 'message',
		          NULLIF(?, ''), '[]', ?, '{}')`,
		id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		a.ChannelID, fromID, string(partsJSON))
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.logEventJSONL(ctx, id)
	evt := map[string]any{
		"id": id, "channel_id": a.ChannelID, "type": "message",
		"from_id": fromID, "parts": parts,
		"received_ts": now.Format(time.RFC3339Nano),
	}
	s.bus.Publish(a.ChannelID, evt)
	return mcpResultJSON(map[string]any{"id": id, "received_ts": evt["received_ts"]}), nil
}

type getFeedArgs struct {
	ChannelID string `json:"channel_id"`
	Since     string `json:"since"`
	Limit     int    `json:"limit"`
}

func (s *Server) mcpGetFeed(ctx context.Context, raw json.RawMessage) (any, *jrpcError) {
	var a getFeedArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ChannelID == "" {
		return nil, &jrpcError{Code: -32602, Message: "channel_id required"}
	}
	if a.Limit <= 0 || a.Limit > 200 {
		a.Limit = 50
	}
	var rows *sql.Rows
	var err error
	q := `SELECT id, schema_version, ts, received_ts, channel_id, type,
	             COALESCE(from_id, ''), to_ids_json, parts_json,
	             task_id, correlation_id,
	             pane_ref_json, usage_tokens_json, metadata_json
	      FROM events WHERE channel_id = ? `
	if a.Since != "" {
		rows, err = s.db.QueryContext(ctx, q+"AND received_ts > ? ORDER BY received_ts ASC LIMIT ?",
			a.ChannelID, a.Since, a.Limit)
	} else {
		rows, err = s.db.QueryContext(ctx, q+"ORDER BY received_ts DESC LIMIT ?",
			a.ChannelID, a.Limit)
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	defer rows.Close()
	events := []map[string]any{}
	for rows.Next() {
		m, err := scanEventRow(rows)
		if err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
		events = append(events, m)
	}
	return mcpResultJSON(events), nil
}

type listChannelsArgs struct {
	ProjectID string `json:"project_id"`
}

func (s *Server) mcpListChannels(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a listChannelsArgs
	_ = json.Unmarshal(raw, &a)
	// channels.team_id (ADR-037 W6) scopes both project- and team-scope
	// channels uniformly. Before it, `OR c.project_id IS NULL` returned
	// EVERY team's team-scope channels (#hub-meta) to any caller —
	// a cross-team leak.
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, COALESCE(c.project_id, ''), c.scope_kind, c.name, c.created_at
		FROM channels c
		WHERE c.team_id = ?
		ORDER BY c.created_at`, team)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	defer rows.Close()
	out := []channelOut{}
	for rows.Next() {
		var c channelOut
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.ScopeKind, &c.Name, &c.CreatedAt); err == nil {
			if a.ProjectID == "" || c.ProjectID == a.ProjectID || c.ScopeKind == "team" {
				out = append(out, c)
			}
		}
	}
	return mcpResultJSON(out), nil
}

type searchArgs struct {
	Q     string `json:"q"`
	Limit int    `json:"limit"`
}

func (s *Server) mcpSearch(ctx context.Context, raw json.RawMessage) (any, *jrpcError) {
	var a searchArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Q == "" {
		return nil, &jrpcError{Code: -32602, Message: "q required"}
	}
	if a.Limit <= 0 || a.Limit > 100 {
		a.Limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.received_ts, e.channel_id, e.type,
		       COALESCE(e.from_id, ''), e.parts_json
		FROM events_fts f
		JOIN events e ON e.id = f.event_id
		WHERE events_fts MATCH ?
		ORDER BY e.received_ts DESC LIMIT ?`, a.Q, a.Limit)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, ts, chID, typ, from, parts string
		if err := rows.Scan(&id, &ts, &chID, &typ, &from, &parts); err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
		out = append(out, map[string]any{
			"id": id, "received_ts": ts, "channel_id": chID,
			"type": typ, "from_id": from, "parts": json.RawMessage(parts),
		})
	}
	return mcpResultJSON(out), nil
}

type journalAppendArgs struct {
	Entry  string `json:"entry"`
	Header string `json:"header,omitempty"`
}

func (s *Server) mcpJournalAppend(ctx context.Context, team, agentID string, raw json.RawMessage) (any, *jrpcError) {
	var a journalAppendArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Entry == "" {
		return nil, &jrpcError{Code: -32602, Message: "entry required"}
	}
	handle, err := s.lookupHandleByID(ctx, team, agentID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	path, err := s.journalPath(team, handle)
	if err != nil {
		return nil, &jrpcError{Code: -32602, Message: err.Error()}
	}
	if err := appendJournal(path, a.Header, a.Entry); err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	_, _ = s.writeDB.ExecContext(ctx,
		`UPDATE agents SET journal_path = ? WHERE team_id = ? AND id = ?`,
		path, team, agentID)
	return mcpResultText("appended"), nil
}

func (s *Server) mcpJournalRead(ctx context.Context, team, agentID string) (any, *jrpcError) {
	handle, err := s.lookupHandleByID(ctx, team, agentID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	path, err := s.journalPath(team, handle)
	if err != nil {
		return nil, &jrpcError{Code: -32602, Message: err.Error()}
	}
	body, err := readFileOrEmpty(path)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultText(body), nil
}

type getProjectDocArgs struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
}

func (s *Server) mcpGetProjectDoc(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a getProjectDocArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ProjectID == "" || a.Path == "" {
		return nil, &jrpcError{Code: -32602, Message: "project_id and path required"}
	}
	root, err := s.resolveDocsRoot(ctx, team, a.ProjectID)
	if err != nil {
		if errors.Is(err, errNoDocsRoot) {
			// The common miss: docs_root is unset (the default), so this
			// project serves no filesystem docs. A bare ErrNotExist here
			// reads like "your file is missing" and sends agents in
			// circles — name the real cause and point at documents.get.
			return nil, &jrpcError{Code: -32000, Message: "this project has no docs_root configured, so it serves no filesystem docs. get_project_doc only reads files under docs_root. To read a document created via documents.create, call documents.get with its ULID instead."}
		}
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	body, err := readFileInRoot(root, a.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &jrpcError{Code: -32000, Message: fmt.Sprintf("no file at %q under the project's docs_root. get_project_doc reads filesystem files only; for a documents.create row use documents.get with its ULID.", a.Path)}
		}
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultText(body), nil
}

type getAttentionArgs struct {
	Scope string `json:"scope"`
}

func (s *Server) mcpGetAttention(ctx context.Context, raw json.RawMessage) (any, *jrpcError) {
	var a getAttentionArgs
	_ = json.Unmarshal(raw, &a)
	q := `SELECT id, scope_kind, COALESCE(scope_id, ''), kind, summary, severity, created_at
	      FROM attention_items WHERE status = 'open'`
	args := []any{}
	if a.Scope != "" {
		q += " AND scope_kind = ?"
		args = append(args, a.Scope)
	}
	q += " ORDER BY created_at DESC LIMIT 50"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, kind, summary, sev, scopeKind, scopeID, createdAt string
		if err := rows.Scan(&id, &scopeKind, &scopeID, &kind, &summary, &sev, &createdAt); err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
		out = append(out, map[string]any{
			"id": id, "scope_kind": scopeKind, "scope_id": scopeID,
			"kind": kind, "summary": summary, "severity": sev,
			"created_at": createdAt,
		})
	}
	return mcpResultJSON(out), nil
}

type postExcerptArgs struct {
	ChannelID string `json:"channel_id"`
	LineFrom  int    `json:"line_from"`
	LineTo    int    `json:"line_to"`
	Content   string `json:"content"`
	Summary   string `json:"summary"`
}

// mcpPostExcerpt records a slice of the calling agent's own pane as an
// event with kind='excerpt'. The agent supplies the captured text — hub
// does not shell out to tmux itself (that would require a host round-trip
// we deliberately avoid in the MCP path). The pane_ref is populated from
// the agent's registered pane_id / host_id so the dashboard's "↗ open pane"
// affordance can resolve the source.
//
// Design choice: content is stored inline (parts_json.excerpt.content)
// rather than uploaded as a blob. Excerpts are expected to be ≤ a few
// hundred lines — the channel feed shouldn't fan out a blob lookup for
// every read.
func (s *Server) mcpPostExcerpt(ctx context.Context, agentID string, raw json.RawMessage) (any, *jrpcError) {
	var a postExcerptArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ChannelID == "" || a.Content == "" {
		return nil, &jrpcError{Code: -32602, Message: "channel_id and content required"}
	}

	// Look up the agent's pane binding so the dashboard can jump back to
	// the source. An agent that has no pane (e.g. a service bot) still gets
	// a useful record — we leave pane_id/host_id empty and surface only the
	// excerpt content.
	var paneID, hostID sql.NullString
	_ = s.db.QueryRowContext(ctx,
		`SELECT pane_id, host_id FROM agents WHERE id = ?`, agentID,
	).Scan(&paneID, &hostID)

	now := time.Now().UTC()
	paneRef := events.PaneRef{
		HostID:   hostID.String,
		PaneID:   paneID.String,
		TsAnchor: now,
	}
	excerpt := events.PaneExcerpt{
		PaneRef:  paneRef,
		LineFrom: a.LineFrom,
		LineTo:   a.LineTo,
		Content:  a.Content,
	}
	parts := []events.Part{
		{Kind: "excerpt", Excerpt: &excerpt},
	}
	if strings.TrimSpace(a.Summary) != "" {
		parts = append([]events.Part{{Kind: "text", Text: a.Summary}}, parts...)
	}
	partsJSON, _ := json.Marshal(parts)
	paneRefJSON, _ := json.Marshal(paneRef)

	id := NewID()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO events (
			id, schema_version, ts, received_ts, channel_id, type,
			from_id, to_ids_json, parts_json, pane_ref_json, metadata_json
		) VALUES (?, 1, ?, ?, ?, 'message',
		          NULLIF(?, ''), '[]', ?, ?, '{}')`,
		id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		a.ChannelID, agentID, string(partsJSON), string(paneRefJSON))
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.logEventJSONL(ctx, id)
	evt := map[string]any{
		"id": id, "channel_id": a.ChannelID, "type": "message",
		"from_id":     agentID,
		"parts":       parts,
		"pane_ref":    paneRef,
		"received_ts": now.Format(time.RFC3339Nano),
	}
	s.bus.Publish(a.ChannelID, evt)
	return mcpResultJSON(map[string]any{
		"id":          id,
		"received_ts": evt["received_ts"],
	}), nil
}

// --- helpers used across MCP + HTTP ---

func (s *Server) lookupHandleByID(ctx context.Context, team, agentID string) (string, error) {
	var h string
	err := s.db.QueryRowContext(ctx,
		`SELECT handle FROM agents WHERE team_id = ? AND id = ?`, team, agentID).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("agent %s not found", agentID)
	}
	return h, err
}

// lookupAgentSession returns the session_id this agent is currently the
// current_agent_id of, or "" if the agent has no live session pointer.
// Used by the request_* MCP tools to stamp attention_items.session_id so
// the mobile detail screen can render the originating chat's recent
// turns and offer "Open in chat" without a second lookup. Empty agentID
// or no-row both return ""; callers don't need a hard error here, the
// session pointer is decorative — attention semantics are unchanged
// when it's missing.
func (s *Server) lookupAgentSession(ctx context.Context, agentID string) string {
	if agentID == "" {
		return ""
	}
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM sessions
		 WHERE current_agent_id = ?
		 ORDER BY last_active_at DESC
		 LIMIT 1`, agentID).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

func appendJournal(path, header, entry string) error {
	if header == "" {
		header = "## " + NowUTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.WriteString(f, "\n"+header+"\n\n"+entry+"\n")
	return err
}

func readFileOrEmpty(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

func readFileInRoot(root, rel string) (string, error) {
	clean := strings.TrimPrefix(rel, "/")
	target := filepath.Join(root, filepath.Clean("/"+clean))
	if !strings.HasPrefix(target+string(os.PathSeparator), root+string(os.PathSeparator)) &&
		target != root {
		return "", fmt.Errorf("invalid path")
	}
	b, err := os.ReadFile(target)
	return string(b), err
}
