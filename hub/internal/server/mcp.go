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
		res, jerr := s.dispatchTool(r.Context(), agent, scope, req.Params)
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
	}
}

// --- Dispatch ---

type toolCallIn struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) dispatchTool(ctx context.Context, agentID string, scope mcpScope, params json.RawMessage) (any, *jrpcError) {
	var call toolCallIn
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &jrpcError{Code: -32602, Message: "invalid params"}
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
	default:
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
