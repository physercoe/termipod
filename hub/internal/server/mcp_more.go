package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crypto/sha256"

	"github.com/termipod/hub/internal/events"
)

// mcp_more.go is the second batch of MCP tools, added once the original
// happy-path (post_message / get_feed / search / journal / attention /
// excerpt) was in place. These are the tools the plan (§12A) expects
// agents to reach for once they're doing real delegation and approval
// work: delegate, request_* decisions, attach, get_* lookups, self-state,
// templates.propose, and self-lifecycle (pause / shutdown).
//
// They're kept in a separate file purely for review-ergonomics — they're
// wired into the same dispatch switch in mcp.go.

// ---------------------------------------------------------------------
// Tool definitions (appended in mcp.go via mcpToolDefsExtra)
// ---------------------------------------------------------------------

func mcpToolDefsExtra() []map[string]any {
	return []map[string]any{
		{
			"name": "delegate",
			"description": "Hand a task to another agent by handle. Posts a message event " +
				"with to_ids=[handle] + metadata.context_refs — refs, not prose (§10A).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":           map[string]any{"type": "string"},
					"channel_id":   map[string]any{"type": "string"},
					"text":         map[string]any{"type": "string"},
					"context_refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"to", "channel_id", "text"},
			},
		},
		{
			"name":        "request_approval",
			"description": "Ask a human (or higher-tier agent) to approve an action.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tier":       map[string]any{"type": "string"},
					"scope_kind": map[string]any{"type": "string"},
					"scope_id":   map[string]any{"type": "string"},
					"summary":    map[string]any{"type": "string"},
					"severity":   map[string]any{"type": "string"},
				},
				"required": []string{"summary"},
			},
		},
		{
			"name":        "request_decision",
			"description": "Ask for a choice between named options. Creates an attention_item.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question":   map[string]any{"type": "string"},
					"options":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"scope_kind": map[string]any{"type": "string"},
					"scope_id":   map[string]any{"type": "string"},
				},
				"required": []string{"question", "options"},
			},
		},
		{
			"name": "attach",
			"description": "Upload a small file as a content-addressed blob. Accepts either " +
				"content_base64 (inline) or path (server reads — only blessed paths).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filename":       map[string]any{"type": "string"},
					"content_base64": map[string]any{"type": "string"},
					"mime":           map[string]any{"type": "string"},
				},
				"required": []string{"filename", "content_base64"},
			},
		},
		{
			"name":        "get_event",
			"description": "Fetch one event by id, including full parts.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
				"required":   []string{"id"},
			},
		},
		{
			"name":        "get_task",
			"description": "Fetch one task by id (title, body, status, assignee).",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
				"required":   []string{"id"},
			},
		},
		{
			"name":        "get_parent_thread",
			"description": "Fetch recent messages from the spawning agent (parent). Useful for respawn continuity.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer"},
				},
			},
		},
		{
			"name":        "list_agents",
			"description": "List agents on this team, optionally filtered to a project scope.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "update_own_task_status",
			"description": "Update status on a task assigned to this agent. Rejects tasks belonging to others.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string"},
					"status":  map[string]any{"type": "string"},
				},
				"required": []string{"task_id", "status"},
			},
		},
		{
			"name":        "templates_propose",
			"description": "Propose a new or revised template. Creates an attention_item for review.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{"type": "string"},
					"name":     map[string]any{"type": "string"},
					"content":  map[string]any{"type": "string"},
					"rationale": map[string]any{"type": "string"},
				},
				"required": []string{"category", "name", "content"},
			},
		},
		{
			"name":        "pause_self",
			"description": "Ask the host-runner to SIGSTOP this agent's pane. Owner must resume manually.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"reason": map[string]any{"type": "string"}},
			},
		},
		{
			"name":        "shutdown_self",
			"description": "Cleanly terminate this agent. Host-agent removes the tmux pane and may clean up the worktree.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"reason": map[string]any{"type": "string"}},
			},
		},
		{
			"name": "get_audit",
			"description": "List recent audit events for this team (activity timeline). " +
				"Covers agent spawn/terminate/archive, run create/complete, document.create, " +
				"review.request/decide, attention.decide, host.*, plan.*, schedule.*, policy.put, token.*. " +
				"Filter by action (exact match) or since (ISO-8601).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string"},
					"since":  map[string]any{"type": "string"},
					"limit":  map[string]any{"type": "integer"},
				},
			},
		},
		{
			// Anthropic's claude-code "--permission-prompt-tool" contract.
			// The agent runtime calls this with {tool_name, input} whenever
			// it would otherwise prompt the human; we surface the request
			// as an attention_item, long-poll for resolution, and reply
			// {behavior:"allow"|"deny", ...}. Required when the spawn was
			// launched with --permission-prompt-tool mcp__termipod__permission_prompt
			// instead of --dangerously-skip-permissions.
			"name": "permission_prompt",
			"description": "Approval gate for tool calls (Anthropic permission_prompt contract). " +
				"Returns {behavior:'allow'|'deny', updatedInput|message}. Requests are " +
				"surfaced as attention_items so the principal can approve/deny from the inbox.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool_name":   map[string]any{"type": "string"},
					"input":       map[string]any{"type": "object"},
					"tool_use_id": map[string]any{"type": "string"},
				},
				"required": []string{"tool_name", "input"},
			},
		},
	}
}

// ---------------------------------------------------------------------
// delegate
// ---------------------------------------------------------------------

type delegateArgs struct {
	To          string   `json:"to"`
	ChannelID   string   `json:"channel_id"`
	Text        string   `json:"text"`
	ContextRefs []string `json:"context_refs,omitempty"`
}

func (s *Server) mcpDelegate(ctx context.Context, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a delegateArgs
	if err := json.Unmarshal(raw, &a); err != nil ||
		a.To == "" || a.ChannelID == "" || a.Text == "" {
		return nil, &jrpcError{Code: -32602, Message: "to, channel_id, text required"}
	}
	parts := []events.Part{{Kind: "text", Text: a.Text}}
	partsJSON, _ := json.Marshal(parts)
	toIDs, _ := json.Marshal([]string{a.To})
	metadata := map[string]any{"context_refs": a.ContextRefs}
	if a.ContextRefs == nil {
		metadata["context_refs"] = []string{}
	}
	metaJSON, _ := json.Marshal(metadata)

	now := time.Now().UTC()
	id := NewID()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events (
			id, schema_version, ts, received_ts, channel_id, type,
			from_id, to_ids_json, parts_json, metadata_json
		) VALUES (?, 1, ?, ?, ?, 'delegate',
		          NULLIF(?, ''), ?, ?, ?)`,
		id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		a.ChannelID, fromID, string(toIDs), string(partsJSON), string(metaJSON))
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.logEventJSONL(ctx, id)
	evt := map[string]any{
		"id": id, "channel_id": a.ChannelID, "type": "delegate",
		"from_id": fromID, "to_ids": []string{a.To},
		"parts":       parts,
		"metadata":    metadata,
		"received_ts": now.Format(time.RFC3339Nano),
	}
	s.bus.Publish(a.ChannelID, evt)
	return mcpResultJSON(map[string]any{"id": id, "to": a.To}), nil
}

// ---------------------------------------------------------------------
// request_approval / request_decision — create an attention_item
// ---------------------------------------------------------------------

type requestApprovalArgs struct {
	Tier      string `json:"tier"`
	ScopeKind string `json:"scope_kind"`
	ScopeID   string `json:"scope_id"`
	Summary   string `json:"summary"`
	Severity  string `json:"severity"`
}

func (s *Server) mcpRequestApproval(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a requestApprovalArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Summary == "" {
		return nil, &jrpcError{Code: -32602, Message: "summary required"}
	}
	if a.ScopeKind == "" {
		a.ScopeKind = "team"
	}
	severity := a.Severity
	if severity == "" {
		// Map tier → severity as a first-cut default. Orchestrator (§13)
		// can override these once the policy YAML is wired in.
		switch a.Tier {
		case "critical":
			severity = "critical"
		case "major":
			severity = "major"
		default:
			severity = "minor"
		}
	}
	id := NewID()
	now := NowUTC()
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULL, ?, NULLIF(?, ''), 'approval_request',
		          ?, ?, '[]', 'open', ?,
		          'agent', NULLIF(?, ''))`,
		id, a.ScopeKind, a.ScopeID, a.Summary, severity, now, actorHandle)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"id":         id,
		"kind":       "approval_request",
		"severity":   severity,
		"requested_by": fromID,
	}), nil
}

type requestDecisionArgs struct {
	Question  string   `json:"question"`
	Options   []string `json:"options"`
	ScopeKind string   `json:"scope_kind"`
	ScopeID   string   `json:"scope_id"`
}

// requestDecisionTimeout caps the long-poll for a decision. Mirrors
// permissionPromptTimeout — long enough that the user has time to read
// and answer on a phone, short enough that an unanswered prompt times
// out before claude's outer turn budget gives up on the agent.
const requestDecisionTimeout = 10 * time.Minute

func (s *Server) mcpRequestDecision(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a requestDecisionArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Question == "" || len(a.Options) == 0 {
		return nil, &jrpcError{Code: -32602, Message: "question and non-empty options required"}
	}
	if a.ScopeKind == "" {
		a.ScopeKind = "team"
	}
	id := NewID()
	now := NowUTC()
	summary := a.Question
	if len(a.Options) > 0 {
		summary = a.Question + " [" + strings.Join(a.Options, " / ") + "]"
	}
	// pending_payload_json carries the structured options so the resolver
	// UI can render one button per option (Me page + steward inline card).
	// Without this, decide handlers can only flip approve/reject and the
	// "pick a color" semantics collapse into a binary.
	payload, _ := json.Marshal(map[string]any{
		"question": a.Question,
		"options":  a.Options,
		"agent_id": fromID,
	})
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)
	// Stored as kind='select' (the noun) — the MCP tool stays
	// `request_decision` (the agent-facing verb) so existing prompts
	// don't break, but the resolver UI reads `select` which is sharper
	// than the generic "decision".
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json
		) VALUES (?, NULL, ?, NULLIF(?, ''), 'select',
		          ?, 'minor', '[]', 'open', ?,
		          'agent', NULLIF(?, ''), ?)`,
		id, a.ScopeKind, a.ScopeID, summary, now, actorHandle, string(payload))
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.recordAudit(ctx, team, "select.request", "attention", id,
		"selection awaiting user: "+a.Question,
		map[string]any{"agent_id": fromID, "options": a.Options})

	// Long-poll for resolution so the agent receives the chosen option.
	// Without this, request_decision was fire-and-forget — the steward
	// would ask "pick a color" and have no way to know what the user
	// chose.
	pctx, cancel := context.WithTimeout(ctx, requestDecisionTimeout)
	defer cancel()
	last, err := s.waitForAttentionResolution(pctx, id)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			_, _ = s.db.ExecContext(context.Background(), `
				UPDATE attention_items
				   SET status = 'resolved', resolved_at = ?
				 WHERE id = ? AND status = 'open'`, NowUTC(), id)
			return mcpResultJSON(map[string]any{
				"id":      id,
				"kind":    "select",
				"timeout": true,
			}), nil
		}
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	optionID, _ := last["option_id"].(string)
	decision, _ := last["decision"].(string)
	reason, _ := last["reason"].(string)
	return mcpResultJSON(map[string]any{
		"id":           id,
		"kind":         "select",
		"option_id":    optionID,
		"decision":     decision,
		"reason":       reason,
		"requested_by": fromID,
	}), nil
}

// ---------------------------------------------------------------------
// attach — upload base64 content as a blob, return {sha256, size}
// ---------------------------------------------------------------------

type attachArgs struct {
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
	Mime          string `json:"mime"`
}

func (s *Server) mcpAttach(ctx context.Context, raw json.RawMessage) (any, *jrpcError) {
	var a attachArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Filename == "" || a.ContentBase64 == "" {
		return nil, &jrpcError{Code: -32602, Message: "filename and content_base64 required"}
	}
	body, err := base64.StdEncoding.DecodeString(a.ContentBase64)
	if err != nil {
		return nil, &jrpcError{Code: -32602, Message: "content_base64: " + err.Error()}
	}
	if len(body) > maxBlobBytes {
		return nil, &jrpcError{Code: -32602, Message: fmt.Sprintf("blob exceeds %d bytes", maxBlobBytes)}
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])

	path := s.blobPath(sha)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
	}
	mime := a.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO blobs (sha256, scope_path, size, mime, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		sha, path, len(body), mime, NowUTC())
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"sha256":   sha,
		"size":     len(body),
		"mime":     mime,
		"filename": a.Filename,
	}), nil
}

// ---------------------------------------------------------------------
// get_event / get_task — single-record lookups
// ---------------------------------------------------------------------

type idArg struct {
	ID string `json:"id"`
}

func (s *Server) mcpGetEvent(ctx context.Context, raw json.RawMessage) (any, *jrpcError) {
	var a idArg
	if err := json.Unmarshal(raw, &a); err != nil || a.ID == "" {
		return nil, &jrpcError{Code: -32602, Message: "id required"}
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, schema_version, ts, received_ts, channel_id, type,
		       COALESCE(from_id, ''), to_ids_json, parts_json,
		       task_id, correlation_id,
		       pane_ref_json, usage_tokens_json, metadata_json
		FROM events WHERE id = ?`, a.ID)
	m, err := scanEventRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32000, Message: "event not found"}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(m), nil
}

func (s *Server) mcpGetTask(ctx context.Context, raw json.RawMessage) (any, *jrpcError) {
	var a idArg
	if err := json.Unmarshal(raw, &a); err != nil || a.ID == "" {
		return nil, &jrpcError{Code: -32602, Message: "id required"}
	}
	var out struct {
		ID          string         `json:"id"`
		ProjectID   string         `json:"project_id"`
		ParentID    sql.NullString `json:"-"`
		Title       string         `json:"title"`
		Body        string         `json:"body_md"`
		Status      string         `json:"status"`
		AssigneeID  sql.NullString `json:"-"`
		CreatedByID sql.NullString `json:"-"`
		MilestoneID sql.NullString `json:"-"`
		CreatedAt   string         `json:"created_at"`
		UpdatedAt   string         `json:"updated_at"`
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, parent_task_id, title, COALESCE(body_md, ''), status,
		       assignee_id, created_by_id, milestone_id, created_at, updated_at
		FROM tasks WHERE id = ?`, a.ID).Scan(
		&out.ID, &out.ProjectID, &out.ParentID, &out.Title, &out.Body, &out.Status,
		&out.AssigneeID, &out.CreatedByID, &out.MilestoneID, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32000, Message: "task not found"}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	result := map[string]any{
		"id":           out.ID,
		"project_id":   out.ProjectID,
		"title":        out.Title,
		"body_md":      out.Body,
		"status":       out.Status,
		"created_at":   out.CreatedAt,
		"updated_at":   out.UpdatedAt,
		"parent_id":    nullStringOrEmpty(out.ParentID),
		"assignee_id":  nullStringOrEmpty(out.AssigneeID),
		"created_by":   nullStringOrEmpty(out.CreatedByID),
		"milestone_id": nullStringOrEmpty(out.MilestoneID),
	}
	return mcpResultJSON(result), nil
}

func nullStringOrEmpty(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// ---------------------------------------------------------------------
// get_parent_thread — resolve parent agent via agent_spawns, list recent events
// ---------------------------------------------------------------------

type getParentThreadArgs struct {
	Limit int `json:"limit"`
}

func (s *Server) mcpGetParentThread(ctx context.Context, agentID string, raw json.RawMessage) (any, *jrpcError) {
	var a getParentThreadArgs
	_ = json.Unmarshal(raw, &a)
	if a.Limit <= 0 || a.Limit > 100 {
		a.Limit = 20
	}
	var parentID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT parent_agent_id FROM agent_spawns
		WHERE child_agent_id = ? ORDER BY spawned_at DESC LIMIT 1`,
		agentID).Scan(&parentID)
	if errors.Is(err, sql.ErrNoRows) || !parentID.Valid {
		return mcpResultJSON(map[string]any{"parent_agent_id": "", "events": []any{}}), nil
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, received_ts, channel_id, type, parts_json
		FROM events WHERE from_id = ?
		ORDER BY received_ts DESC LIMIT ?`, parentID.String, a.Limit)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	defer rows.Close()
	evts := []map[string]any{}
	for rows.Next() {
		var id, ts, chID, typ, parts string
		if err := rows.Scan(&id, &ts, &chID, &typ, &parts); err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
		evts = append(evts, map[string]any{
			"id": id, "received_ts": ts, "channel_id": chID,
			"type": typ, "parts": json.RawMessage(parts),
		})
	}
	return mcpResultJSON(map[string]any{
		"parent_agent_id": parentID.String,
		"events":          evts,
	}), nil
}

// ---------------------------------------------------------------------
// list_agents
// ---------------------------------------------------------------------

type listAgentsArgs struct {
	ProjectID string `json:"project_id"`
}

func (s *Server) mcpListAgents(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a listAgentsArgs
	_ = json.Unmarshal(raw, &a)
	// project_id is accepted but currently ignored — agents are team-scoped
	// in the schema; we keep the param so future per-project agent scoping
	// doesn't need a tool-signature change.
	_ = a.ProjectID
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, handle, kind, status,
		       COALESCE(host_id, ''), COALESCE(pane_id, ''), created_at
		FROM agents WHERE team_id = ?
		ORDER BY created_at DESC`, team)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, handle, kind, status, hostID, paneID, createdAt string
		if err := rows.Scan(&id, &handle, &kind, &status, &hostID, &paneID, &createdAt); err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
		out = append(out, map[string]any{
			"id": id, "handle": handle, "kind": kind,
			"status": status, "host_id": hostID, "pane_id": paneID,
			"created_at": createdAt,
		})
	}
	return mcpResultJSON(out), nil
}

// ---------------------------------------------------------------------
// update_own_task_status — enforce assignee == self
// ---------------------------------------------------------------------

type updateOwnTaskStatusArgs struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

func (s *Server) mcpUpdateOwnTaskStatus(ctx context.Context, agentID string, raw json.RawMessage) (any, *jrpcError) {
	var a updateOwnTaskStatusArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.TaskID == "" || a.Status == "" {
		return nil, &jrpcError{Code: -32602, Message: "task_id and status required"}
	}
	var assignee sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT assignee_id FROM tasks WHERE id = ?`, a.TaskID).Scan(&assignee)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32000, Message: "task not found"}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	// Only the assignee may update-own. Tasks with no assignee aren't
	// "owned" by anyone; we reject rather than silently mutate state that
	// might belong to a human tracker.
	if !assignee.Valid || assignee.String != agentID {
		return nil, &jrpcError{Code: -32000, Message: "task is not assigned to this agent"}
	}
	now := NowUTC()
	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
		a.Status, now, a.TaskID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"id": a.TaskID, "status": a.Status, "updated_at": now,
	}), nil
}

// ---------------------------------------------------------------------
// templates_propose — store content as a blob, file an attention_item
// ---------------------------------------------------------------------

type templatesProposeArgs struct {
	Category  string `json:"category"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Rationale string `json:"rationale"`
}

func (s *Server) mcpTemplatesPropose(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a templatesProposeArgs
	if err := json.Unmarshal(raw, &a); err != nil ||
		a.Category == "" || a.Name == "" || a.Content == "" {
		return nil, &jrpcError{Code: -32602, Message: "category, name, content required"}
	}
	// Store the proposed body as a blob so the reviewer can fetch it by
	// sha. Inline-in-attention would bloat the attention_items table and
	// force a schema bump to accommodate the largest template.
	body := []byte(a.Content)
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	path := s.blobPath(sha)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return nil, &jrpcError{Code: -32000, Message: err.Error()}
		}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO blobs (sha256, scope_path, size, mime, created_at)
		VALUES (?, ?, ?, 'text/yaml', ?)`,
		sha, path, len(body), NowUTC())
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	summary := fmt.Sprintf("Template proposal: %s/%s", a.Category, a.Name)
	if a.Rationale != "" {
		summary += " — " + a.Rationale
	}
	// pending_payload_json carries everything the decide handler needs to
	// install the template on approve — no need to re-fetch from the blob
	// table or parse the summary string.
	payload, _ := json.Marshal(map[string]any{
		"category":    a.Category,
		"name":        a.Name,
		"blob_sha256": sha,
		"rationale":   a.Rationale,
		"proposed_by": fromID,
	})
	id := NewID()
	now := NowUTC()
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULL, 'team', NULL, 'template_proposal',
		          ?, 'minor', '[]', ?, 'open', ?,
		          'agent', NULLIF(?, ''))`,
		id, summary, string(payload), now, actorHandle)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"attention_id": id,
		"blob_sha256":  sha,
		"category":     a.Category,
		"name":         a.Name,
		"proposed_by":  fromID,
	}), nil
}

// ---------------------------------------------------------------------
// pause_self / shutdown_self — enqueue a host command against own pane
// ---------------------------------------------------------------------

type selfLifecycleArgs struct {
	Reason string `json:"reason"`
}

func (s *Server) mcpPauseSelf(ctx context.Context, agentID string, raw json.RawMessage) (any, *jrpcError) {
	return s.enqueueSelfLifecycle(ctx, agentID, "pause", raw)
}

func (s *Server) mcpShutdownSelf(ctx context.Context, agentID string, raw json.RawMessage) (any, *jrpcError) {
	return s.enqueueSelfLifecycle(ctx, agentID, "terminate", raw)
}

func (s *Server) enqueueSelfLifecycle(ctx context.Context, agentID, cmd string, raw json.RawMessage) (any, *jrpcError) {
	var a selfLifecycleArgs
	_ = json.Unmarshal(raw, &a)

	var hostID, paneID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT host_id, pane_id FROM agents WHERE id = ?`, agentID,
	).Scan(&hostID, &paneID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32000, Message: "agent not found"}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	if !hostID.Valid || hostID.String == "" {
		return nil, &jrpcError{Code: -32000, Message: "agent has no host binding"}
	}
	cmdID, err := s.enqueueHostCommand(ctx, hostID.String, agentID, cmd,
		map[string]any{"pane_id": paneID.String, "reason": a.Reason})
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"command":    cmd,
		"command_id": cmdID,
		"agent_id":   agentID,
	}), nil
}

// ---------------------------------------------------------------------
// get_audit — unified activity timeline
// ---------------------------------------------------------------------

type getAuditArgs struct {
	Action string `json:"action"`
	Since  string `json:"since"`
	Limit  int    `json:"limit"`
}

func (s *Server) mcpGetAudit(ctx context.Context, team string, raw json.RawMessage) (any, *jrpcError) {
	var a getAuditArgs
	_ = json.Unmarshal(raw, &a)
	if team == "" {
		return nil, &jrpcError{Code: -32602, Message: "team scope required"}
	}
	limit := a.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.listAuditEvents(ctx, team, a.Action, a.Since, limit)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	if rows == nil {
		rows = []AuditRow{}
	}
	return mcpResultJSON(rows), nil
}

// ---------------------------------------------------------------------
// permission_prompt — Anthropic --permission-prompt-tool contract
// ---------------------------------------------------------------------

// permissionPromptArgs matches the shape claude-code sends when the
// agent attempts a tool call under --permission-prompt-tool. Anthropic
// reserves the right to add fields, so we keep the input opaque
// (json.RawMessage) and forward it untouched on allow.
type permissionPromptArgs struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
}

// permissionPromptTimeout caps how long we'll hold the MCP call open
// while waiting for a human decision. Longer than the 30s claude default
// would happily wait, shorter than the 15-min "they fell asleep with
// the phone in their pocket" window where we'd rather fail closed.
const permissionPromptTimeout = 10 * time.Minute

func (s *Server) mcpPermissionPrompt(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a permissionPromptArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ToolName == "" {
		return nil, &jrpcError{Code: -32602, Message: "tool_name and input required"}
	}
	if len(a.Input) == 0 {
		// Empty input is legal for some tools but the agent always sends
		// at least `{}`; reject `null`/missing so the resolver UI doesn't
		// have to special-case it.
		return nil, &jrpcError{Code: -32602, Message: "input required"}
	}

	// Tier gate (W1.A): only escalate to a user prompt for tier ≥
	// significant. Trivial reads (file/glob/web search) and routine
	// writes (edits in scope, journal_append, etc.) auto-allow with
	// an audit trail and skip the attention queue entirely. This is
	// what makes the "director decides important things, not every
	// read" promise from docs/steward-sessions.md §6.5 real — under
	// --permission-prompt-tool the agent would otherwise prompt on
	// every tool call.
	tier := tierFor(a.ToolName)
	if tier == TierTrivial || tier == TierRoutine {
		s.recordAudit(ctx, team, "permission_prompt.auto_allowed",
			"agent", fromID,
			"tier="+tier+" auto-allowed "+a.ToolName,
			map[string]any{
				"tool_name": a.ToolName,
				"tier":      tier,
			})
		return mcpResultJSON(map[string]any{
			"behavior": "allow",
			"message":  "auto-allowed (tier=" + tier + ")",
		}), nil
	}

	// pending_payload_json carries the data the resolver UI needs to render
	// a meaningful approve/deny prompt (tool name + redacted input preview).
	// agent_id lets the mobile inbox associate the prompt with the calling
	// agent for back-navigation into its transcript. `tier` is resolved
	// server-side from the tool name (see tiers.go) so the mobile approval
	// card can pick the right card class without re-deriving the tier
	// itself — and so the agent can't reclassify its own actions by
	// claiming a lower tier.
	payload, _ := json.Marshal(map[string]any{
		"tool_name":   a.ToolName,
		"input":       a.Input,
		"agent_id":    fromID,
		"tool_use_id": a.ToolUseID,
		"tier":        tier,
	})

	summary := "tool: " + a.ToolName
	id := NewID()
	now := NowUTC()
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json
		) VALUES (?, NULL, 'team', NULL, 'permission_prompt',
		          ?, 'minor', '[]', 'open', ?,
		          'agent', NULLIF(?, ''), ?)`,
		id, summary, now, actorHandle, string(payload),
	); err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.recordAudit(ctx, team, "permission_prompt.request", "attention", id,
		"tool call awaiting approval: "+a.ToolName,
		map[string]any{"tool_name": a.ToolName, "agent_id": fromID})

	pctx, cancel := context.WithTimeout(ctx, permissionPromptTimeout)
	defer cancel()

	decision, reason, err := s.waitForAttentionDecision(pctx, id)
	if err != nil {
		// Timeout / context cancel — fail closed (deny). Mark the row as
		// resolved so it doesn't loiter in the inbox forever; the audit
		// trail captures the no-decision outcome.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			_, _ = s.db.ExecContext(context.Background(), `
				UPDATE attention_items
				   SET status = 'resolved', resolved_at = ?
				 WHERE id = ? AND status = 'open'`, NowUTC(), id)
			return mcpResultJSON(map[string]any{
				"behavior": "deny",
				"message":  "no decision within timeout — denied",
			}), nil
		}
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}

	// decisions_json uses approve/reject (existing decide handler). Map to
	// the permission_prompt response vocabulary (allow/deny). updatedInput
	// is pass-through today; a future revision can let the principal edit
	// arguments before approving.
	if decision == "approve" {
		return mcpResultJSON(map[string]any{
			"behavior":     "allow",
			"updatedInput": json.RawMessage(a.Input),
		}), nil
	}
	msg := reason
	if msg == "" {
		msg = "user denied"
	}
	return mcpResultJSON(map[string]any{
		"behavior": "deny",
		"message":  msg,
	}), nil
}

// waitForAttentionResolution polls attention_items until status='resolved'
// (or ctx fires). Returns the full last decision dict so callers that
// care about extra fields (notably option_id from request_decision) can
// read them without re-parsing decisions_json. Same backoff as
// waitForAttentionDecision.
func (s *Server) waitForAttentionResolution(ctx context.Context, id string) (map[string]any, error) {
	delay := 100 * time.Millisecond
	const maxDelay = 2 * time.Second
	for {
		var status, decisions string
		row := s.db.QueryRowContext(ctx,
			`SELECT status, decisions_json FROM attention_items WHERE id = ?`, id)
		if err := row.Scan(&status, &decisions); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("attention %s not found", id)
			}
			return nil, err
		}
		if status == "resolved" {
			var arr []map[string]any
			_ = json.Unmarshal([]byte(decisions), &arr)
			if len(arr) == 0 {
				return map[string]any{
					"decision": "reject",
					"reason":   "resolved without decision",
				}, nil
			}
			return arr[len(arr)-1], nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

// waitForAttentionDecision polls attention_items until status='resolved'
// (or ctx fires). Returns the last decision recorded in decisions_json
// (decision, reason). Backoff is exponential 100ms→2s — fast enough for
// typical phone-tap latencies, slow enough to not hammer the DB on stuck
// rows.
func (s *Server) waitForAttentionDecision(ctx context.Context, id string) (decision string, reason string, err error) {
	delay := 100 * time.Millisecond
	const maxDelay = 2 * time.Second
	for {
		var status, decisions string
		row := s.db.QueryRowContext(ctx,
			`SELECT status, decisions_json FROM attention_items WHERE id = ?`, id)
		if err := row.Scan(&status, &decisions); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", "", fmt.Errorf("attention %s not found", id)
			}
			return "", "", err
		}
		if status == "resolved" {
			var arr []map[string]any
			_ = json.Unmarshal([]byte(decisions), &arr)
			if len(arr) == 0 {
				// resolved without a decide call — treat as deny so the
				// agent doesn't proceed under an unspecified outcome.
				return "reject", "resolved without decision", nil
			}
			last := arr[len(arr)-1]
			d, _ := last["decision"].(string)
			r, _ := last["reason"].(string)
			return d, r, nil
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}
