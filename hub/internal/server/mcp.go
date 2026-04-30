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

const (
	mcpProtocolVersion = "2024-11-05"
)

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
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0",
			Error:   &jrpcError{Code: -32700, Message: "parse error"},
		})
		return
	}

	switch req.Method {
	case "initialize":
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0", ID: req.ID,
			Result: map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{
					"name":    "termipod-hub",
					"version": ServerVersion,
				},
			},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusNoContent)
	case "tools/list":
		writeJRPC(w, jrpcResp{
			JSONRPC: "2.0", ID: req.ID,
			Result: map[string]any{"tools": mcpToolDefs()},
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
			resp.Result = res
		}
		writeJRPC(w, resp)
	case "ping":
		writeJRPC(w, jrpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
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
	base := mcpToolDefsBase()
	all := append(base, mcpToolDefsExtra()...)
	all = append(all, orchestrationToolDefs()...)
	// Rich-authority surface (projects, plans, runs, agents.spawn,
	// schedules, channels, a2a.invoke, …) imported from the
	// hubmcpserver package — same catalog the standalone daemon
	// exposes, served in-process so spawned agents only need the
	// single bridge entry in .mcp.json.
	all = append(all, authorityToolDefs()...)
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

func mcpToolDefsBase() []map[string]any {
	return []map[string]any{
		{
			"name":        "post_message",
			"description": "Post a message event to a channel. Text goes into a single text part.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel_id": map[string]any{"type": "string"},
					"text":       map[string]any{"type": "string"},
				},
				"required": []string{"channel_id", "text"},
			},
		},
		{
			"name":        "get_feed",
			"description": "List recent events in a channel, optionally since a received_ts.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel_id": map[string]any{"type": "string"},
					"since":      map[string]any{"type": "string"},
					"limit":      map[string]any{"type": "integer"},
				},
				"required": []string{"channel_id"},
			},
		},
		{
			"name":        "list_channels",
			"description": "List all channels this agent's team can see for a project.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": map[string]any{"type": "string"},
				},
				"required": []string{"project_id"},
			},
		},
		{
			"name":        "search",
			"description": "Full-text search across event contents (FTS5).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q":     map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
				},
				"required": []string{"q"},
			},
		},
		{
			"name":        "journal_append",
			"description": "Append an entry to this agent's journal (identity that survives respawns).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"entry":  map[string]any{"type": "string"},
					"header": map[string]any{"type": "string"},
				},
				"required": []string{"entry"},
			},
		},
		{
			"name":        "journal_read",
			"description": "Read this agent's journal.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "get_project_doc",
			"description": "Fetch a file from a project's docs_root (shared context).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": map[string]any{"type": "string"},
					"path":       map[string]any{"type": "string"},
				},
				"required": []string{"project_id", "path"},
			},
		},
		{
			"name":        "get_attention",
			"description": "List open attention items for this team (decisions / approvals pending).",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"scope": map[string]any{"type": "string"}},
			},
		},
		{
			"name": "post_excerpt",
			"description": "Post an excerpt from this agent's own pane as an event. " +
				"The agent supplies the captured text; the hub records the " +
				"line range and a one-line summary so the dashboard can render " +
				"a compact card with a link back to the source pane.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel_id": map[string]any{"type": "string"},
					"line_from":  map[string]any{"type": "integer"},
					"line_to":    map[string]any{"type": "integer"},
					"content":    map[string]any{"type": "string"},
					"summary":    map[string]any{"type": "string"},
				},
				"required": []string{"channel_id", "content"},
			},
		},
	}
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
	// A2A target restriction (ADR-016 D4). Workers may invoke
	// a2a.invoke only against their parent steward. Stewards are
	// unrestricted. Skipped for principal tokens (agentID == "").
	if call.Name == "a2a.invoke" && agentID != "" {
		var args map[string]any
		if len(call.Arguments) > 0 {
			_ = json.Unmarshal(call.Arguments, &args)
		}
		role := s.resolveAgentRole(agentID, scope.Role)
		if jerr := s.authorizeA2ATarget(agentID, role, scope.Team, args); jerr != nil {
			return nil, jerr
		}
	}
	switch call.Name {
	case "post_message":
		return s.mcpPostMessage(ctx, agentID, call.Arguments)
	case "get_feed":
		return s.mcpGetFeed(ctx, call.Arguments)
	case "list_channels":
		return s.mcpListChannels(ctx, scope.Team, call.Arguments)
	case "search":
		return s.mcpSearch(ctx, call.Arguments)
	case "journal_append":
		return s.mcpJournalAppend(ctx, scope.Team, agentID, call.Arguments)
	case "journal_read":
		return s.mcpJournalRead(ctx, scope.Team, agentID)
	case "get_project_doc":
		return s.mcpGetProjectDoc(ctx, scope.Team, call.Arguments)
	case "get_attention":
		return s.mcpGetAttention(ctx, call.Arguments)
	case "post_excerpt":
		return s.mcpPostExcerpt(ctx, agentID, call.Arguments)
	case "delegate":
		return s.mcpDelegate(ctx, agentID, call.Arguments)
	case "request_approval":
		return s.mcpRequestApproval(ctx, scope.Team, agentID, call.Arguments)
	case "request_select", "request_decision":
		// `request_decision` is the back-compat alias — the tool was
		// renamed to match the attention-kind it produces (`select`)
		// in v1.0.295. Templates with the old name keep working until
		// they re-render.
		return s.mcpRequestSelect(ctx, scope.Team, agentID, call.Arguments)
	case "request_help":
		return s.mcpRequestHelp(ctx, scope.Team, agentID, call.Arguments)
	case "attach":
		return s.mcpAttach(ctx, call.Arguments)
	case "get_event":
		return s.mcpGetEvent(ctx, call.Arguments)
	case "get_task":
		return s.mcpGetTask(ctx, call.Arguments)
	case "get_parent_thread":
		return s.mcpGetParentThread(ctx, agentID, call.Arguments)
	case "list_agents":
		return s.mcpListAgents(ctx, scope.Team, call.Arguments)
	case "update_own_task_status":
		return s.mcpUpdateOwnTaskStatus(ctx, agentID, call.Arguments)
	case "templates_propose", "templates.propose":
		return s.mcpTemplatesPropose(ctx, scope.Team, agentID, call.Arguments)
	case "pause_self":
		return s.mcpPauseSelf(ctx, agentID, call.Arguments)
	case "shutdown_self":
		return s.mcpShutdownSelf(ctx, agentID, call.Arguments)
	case "get_audit":
		return s.mcpGetAudit(ctx, scope.Team, call.Arguments)
	case "permission_prompt":
		return s.mcpPermissionPrompt(ctx, scope.Team, agentID, call.Arguments)
	case "agents.fanout":
		return s.mcpAgentsFanout(ctx, scope.Team, call.Arguments)
	case "agents.gather":
		return s.mcpAgentsGather(ctx, scope.Team, call.Arguments)
	case "reports.post":
		return s.mcpReportsPost(ctx, agentID, call.Arguments)
	default:
		// Fall through to the rich-authority catalog (projects,
		// plans, runs, agents.spawn, schedules, channels, …)
		// imported from hubmcpserver. Auth runs through the chi
		// router via chiRouterTransport, so the agent's bearer
		// authenticates the in-process REST hop just like a real
		// network call would.
		if hasAuthorityTool(call.Name) {
			return s.dispatchAuthorityToolRaw(ctx, agentToken, scope.Team, call.Name, call.Arguments)
		}
		return nil, &jrpcError{Code: -32601, Message: "unknown tool: " + call.Name}
	}
}

func mcpResultText(text string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
}

func mcpResultJSON(v any) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return mcpResultText(string(b))
}

// --- Tool impls ---

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
	_, err := s.db.ExecContext(ctx, `
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, COALESCE(c.project_id, ''), c.scope_kind, c.name, c.created_at
		FROM channels c
		LEFT JOIN projects p ON p.id = c.project_id
		WHERE p.team_id = ? OR (c.project_id IS NULL)
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
	_, _ = s.db.ExecContext(ctx,
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
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	body, err := readFileInRoot(root, a.Path)
	if err != nil {
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
	_, err := s.db.ExecContext(ctx, `
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
