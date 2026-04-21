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

func (s *Server) mcpRequestApproval(ctx context.Context, fromID string, raw json.RawMessage) (any, *jrpcError) {
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at
		) VALUES (?, NULL, ?, NULLIF(?, ''), 'approval_request',
		          ?, ?, '[]', 'open', ?)`,
		id, a.ScopeKind, a.ScopeID, a.Summary, severity, now)
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

func (s *Server) mcpRequestDecision(ctx context.Context, fromID string, raw json.RawMessage) (any, *jrpcError) {
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at
		) VALUES (?, NULL, ?, NULLIF(?, ''), 'decision',
		          ?, 'minor', '[]', 'open', ?)`,
		id, a.ScopeKind, a.ScopeID, summary, now)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"id":          id,
		"kind":        "decision",
		"options":     a.Options,
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

func (s *Server) mcpTemplatesPropose(ctx context.Context, fromID string, raw json.RawMessage) (any, *jrpcError) {
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
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at
		) VALUES (?, NULL, 'team', NULL, 'template_proposal',
		          ?, 'minor', '[]', ?, 'open', ?)`,
		id, summary, string(payload), now)
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
