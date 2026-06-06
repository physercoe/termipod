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
// They're kept in a separate file purely for review-ergonomics — they
// register the same way as the first batch (buildNativeTools() in
// native_tools.go).

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
	_, err := s.writeDB.ExecContext(ctx, `
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
// request_approval / request_select — create an attention_item
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
	sessionID := s.lookupAgentSession(ctx, fromID)
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, session_id
		) VALUES (?, NULL, ?, NULLIF(?, ''), 'approval_request',
		          ?, ?, '[]', 'open', ?,
		          'agent', NULLIF(?, ''), NULLIF(?, ''))`,
		id, a.ScopeKind, a.ScopeID, a.Summary, severity, now, actorHandle, sessionID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"id":           id,
		"kind":         "approval_request",
		"severity":     severity,
		"requested_by": fromID,
	}), nil
}

type requestSelectArgs struct {
	Question  string   `json:"question"`
	Options   []string `json:"options"`
	ScopeKind string   `json:"scope_kind"`
	ScopeID   string   `json:"scope_id"`
}

func (s *Server) mcpRequestSelect(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a requestSelectArgs
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
	sessionID := s.lookupAgentSession(ctx, fromID)
	// Stored as kind='select' (the noun) — the MCP tool stays
	// `request_select` (the agent-facing verb) so existing prompts
	// don't break, but the resolver UI reads `select` which is sharper
	// than the generic "decision".
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json, session_id
		) VALUES (?, NULL, ?, NULLIF(?, ''), 'select',
		          ?, 'minor', '[]', 'open', ?,
		          'agent', NULLIF(?, ''), ?, NULLIF(?, ''))`,
		id, a.ScopeKind, a.ScopeID, summary, now, actorHandle, string(payload), sessionID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.recordAudit(ctx, team, "select.request", "attention", id,
		"selection awaiting user: "+a.Question,
		map[string]any{"agent_id": fromID, "options": a.Options})

	// Turn-based delivery: return immediately with awaiting_response,
	// then end the turn (per the tool description). The principal's
	// pick is delivered as a fresh user turn through agent_input
	// kind='attention_reply' when /decide resolves the attention. The
	// long-poll model this replaced was fragile against transport
	// idle timeouts and pinned engine resources for nothing.
	return mcpResultJSON(map[string]any{
		"id":           id,
		"kind":         "select",
		"status":       "awaiting_response",
		"requested_by": fromID,
	}), nil
}

// ---------------------------------------------------------------------
// request_help — open-ended ask. Free-text answer back from the principal.
// ---------------------------------------------------------------------
//
// The third attention shape, complementing approval (binary) and select
// (n-ary). Used when the answer space is open: clarification, direction,
// opinion, or hand-back. The cardinality test (`docs/reference/attention-
// kinds.md`) is the load-bearing rule the agent uses to pick between the
// three; the tool description above carries the short form.
//
// The principal's reply lands in decisions_json[…].body via the `decide`
// endpoint and waitForAttentionResolution surfaces the whole last-decision
// dict, so `body` flows back to the agent without a second round-trip.

type requestHelpArgs struct {
	Question  string `json:"question"`
	Context   string `json:"context"`
	Mode      string `json:"mode"`     // 'clarify' (default) | 'handoff'
	Severity  string `json:"severity"` // 'minor' (default) | 'major' | 'critical'
	ScopeKind string `json:"scope_kind"`
	ScopeID   string `json:"scope_id"`
}

func (s *Server) mcpRequestHelp(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a requestHelpArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Question == "" {
		return nil, &jrpcError{Code: -32602, Message: "question required"}
	}
	if a.ScopeKind == "" {
		a.ScopeKind = "team"
	}
	mode := a.Mode
	if mode == "" {
		mode = "clarify"
	}
	if mode != "clarify" && mode != "handoff" {
		return nil, &jrpcError{Code: -32602, Message: "mode must be 'clarify' or 'handoff'"}
	}
	severity := a.Severity
	if severity == "" {
		// Default tracks mode: a hand-back is a stronger signal than a
		// routine clarification, so it surfaces with major severity unless
		// the agent explicitly downgrades.
		if mode == "handoff" {
			severity = "major"
		} else {
			severity = "minor"
		}
	}
	id := NewID()
	now := NowUTC()
	// pending_payload carries the question, mode, and the agent's own
	// framing so the resolver UI can show "what they're asking" + "why
	// they think they need help" without round-tripping the transcript.
	payload, _ := json.Marshal(map[string]any{
		"question": a.Question,
		"context":  a.Context,
		"mode":     mode,
		"agent_id": fromID,
	})
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)
	sessionID := s.lookupAgentSession(ctx, fromID)
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json, session_id
		) VALUES (?, NULL, ?, NULLIF(?, ''), 'help_request',
		          ?, ?, '[]', 'open', ?,
		          'agent', NULLIF(?, ''), ?, NULLIF(?, ''))`,
		id, a.ScopeKind, a.ScopeID, a.Question, severity, now, actorHandle, string(payload), sessionID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.recordAudit(ctx, team, "help.request", "attention", id,
		"help requested ("+mode+"): "+a.Question,
		map[string]any{"agent_id": fromID, "mode": mode, "severity": severity})

	// Turn-based delivery: return immediately with awaiting_response,
	// then end the turn (per the tool description). The principal's
	// reply is delivered as a fresh user turn through agent_input
	// kind='attention_reply' when /decide resolves the attention. The
	// long-poll model this replaced was fragile against transport
	// idle timeouts; turn-based puts persistence in the conversation
	// history, so a 3-day-later reply still reaches the agent.
	return mcpResultJSON(map[string]any{
		"id":           id,
		"kind":         "help_request",
		"status":       "awaiting_response",
		"requested_by": fromID,
	}), nil
}

// ---------------------------------------------------------------------
// post_notice — one-way informational FYI for the principal.
// ---------------------------------------------------------------------
//
// The answerless sibling of the request_* family. request_approval
// (binary), request_select (n-ary), and request_help (open) all ask for
// a response; post_notice asks for nothing — it just surfaces a status
// line or FYI to the principal. It opens an attention_items row with
// kind='notice' and NO pending_payload, so mobile's _filterForAttention
// classifies it under the Me-page "Messages" slice (the FYI bucket,
// alongside the system-raised budget_exceeded), not "Requests".
//
// Fire-and-forget: unlike request_*, the agent does NOT wait — there is
// no awaiting_response, no turn to end, no reply coming back. Use it for
// "phase 2 done, moving on", "deployed v3 to staging", "found nothing
// actionable in the logs" — context the director may want without being
// asked to decide anything.
//
// Steward-only (WorkerEligible=false): a director-facing notice is a
// steward act. Workers report status to their parent steward via
// tasks_complete / a2a_invoke, not straight to the director.

type postNoticeArgs struct {
	Summary   string `json:"summary"`
	Severity  string `json:"severity"` // 'minor' (default) | 'major'
	ScopeKind string `json:"scope_kind"`
	ScopeID   string `json:"scope_id"`
}

func (s *Server) mcpPostNotice(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a postNoticeArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Summary == "" {
		return nil, &jrpcError{Code: -32602, Message: "summary required"}
	}
	if a.ScopeKind == "" {
		a.ScopeKind = "team"
	}
	severity := a.Severity
	if severity == "" {
		severity = "minor"
	}
	// A notice is informational — there is nothing to unblock, so the
	// blocking 'critical' tier doesn't apply. Keep the surface honest:
	// minor (default) or major (worth seeing sooner).
	if severity != "minor" && severity != "major" {
		return nil, &jrpcError{Code: -32602, Message: "severity must be 'minor' or 'major'"}
	}
	id := NewID()
	now := NowUTC()
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)
	sessionID := s.lookupAgentSession(ctx, fromID)
	// No pending_payload_json: that column is the "structured ask" that
	// flips an item into the Requests bucket. A notice carries none, so
	// it lands in Messages as an FYI. Assignee is the principal so it
	// reaches the director's inbox like the other FYI items.
	assignees, _ := json.Marshal([]string{"@principal"})
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, session_id
		) VALUES (?, NULL, ?, NULLIF(?, ''), 'notice',
		          ?, ?, ?, 'open', ?,
		          'agent', NULLIF(?, ''), NULLIF(?, ''))`,
		id, a.ScopeKind, a.ScopeID, a.Summary, severity, string(assignees), now, actorHandle, sessionID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.recordAudit(ctx, team, "notice.post", "attention", id, a.Summary,
		map[string]any{"agent_id": fromID, "severity": severity})
	// 'posted', not 'awaiting_response': the agent should keep working,
	// not end its turn waiting for a reply that never comes.
	return mcpResultJSON(map[string]any{
		"id":        id,
		"kind":      "notice",
		"status":    "posted",
		"posted_by": fromID,
	}), nil
}

// ---------------------------------------------------------------------
// request_project_steward — ADR-025 W4 delegation attention item.
// ---------------------------------------------------------------------
//
// The general steward calls this when the principal asks it to operate
// inside a project that has no live steward yet. Per ADR-025 D2 the
// general steward can't spawn workers directly; it must hand off to a
// project steward, and project stewards are materialized lazily with
// director consent. This tool is the delegation channel: raise an
// attention item the principal can tap to open the host-picker sheet
// (W7) prefilled with the general steward's suggestion.
//
// kind=`project_steward_request` is the mobile-recognized rendering
// hook for this flow. Severity is `major` — it's principal-blocking
// (the general steward is waiting on this to proceed) but not
// critical.

type requestProjectStewardArgs struct {
	ProjectID       string `json:"project_id"`
	Reason          string `json:"reason"`
	SuggestedHostID string `json:"suggested_host_id"`
}

func (s *Server) mcpRequestProjectSteward(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a requestProjectStewardArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.ProjectID == "" || a.Reason == "" {
		return nil, &jrpcError{Code: -32602, Message: "project_id and reason required"}
	}
	// Validate the project exists in this team so a typo can't park a
	// permanent open attention against a nonexistent project_id.
	if err := s.validateProjectInTeam(ctx, team, a.ProjectID); err != nil {
		return nil, &jrpcError{Code: -32602, Message: err.Error()}
	}
	id := NewID()
	now := NowUTC()
	payload, _ := json.Marshal(map[string]any{
		"project_id":        a.ProjectID,
		"reason":            a.Reason,
		"suggested_host_id": a.SuggestedHostID,
		"requested_by":      fromID,
	})
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)
	sessionID := s.lookupAgentSession(ctx, fromID)
	summary := "Spawn a project steward: " + a.Reason
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json, session_id
		) VALUES (?, ?, 'project', ?, 'project_steward_request',
		          ?, 'major', '[]', 'open', ?,
		          'agent', NULLIF(?, ''), ?, NULLIF(?, ''))`,
		id, a.ProjectID, a.ProjectID, summary, now,
		actorHandle, string(payload), sessionID)
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	s.recordAudit(ctx, team, "project_steward.request", "attention", id,
		"project steward requested for "+a.ProjectID+": "+a.Reason,
		map[string]any{
			"agent_id":          fromID,
			"project_id":        a.ProjectID,
			"suggested_host_id": a.SuggestedHostID,
		})
	return mcpResultJSON(map[string]any{
		"id":           id,
		"kind":         "project_steward_request",
		"status":       "awaiting_response",
		"requested_by": fromID,
	}), nil
}

// ---------------------------------------------------------------------
// attach / blob_get — upload + read the hub blob store
// ---------------------------------------------------------------------
//
// attach takes a base64-encoded body and writes it to the
// content-addressed blob store at <DataRoot>/blobs/<aa>/<bb>/<sha>.
// blob_get is the inverse — sha in, bytes out — and is what makes
// cross-host file transfer work end-to-end at the agent layer.
// Without it, an attach on host A could only be consumed by mobile;
// the receiving agent on host B had the URI but no way to fetch
// the bytes.
//
// Naming: native registry uses verb-first names (get_feed, get_event,
// get_attention, get_project_doc); blob_get keeps that house style.
// The dotted form (blob.get) is reserved for the authority registry
// and is not exposed today — current callers are all native dispatch.

type attachArgs struct {
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
	Content       string `json:"content"`
	Mime          string `json:"mime"`
}

// stripBase64Whitespace removes ASCII whitespace (space, tab, CR, LF) from
// a base64 string. Most encoders — the `base64` CLI, openssl, and many
// language libraries — line-wrap at 76 columns, and Go's
// base64.StdEncoding rejects the embedded newlines as "illegal base64
// data". Stripping first means a correctly-encoded but wrapped payload
// still decodes, instead of erroring in a way agents misread as a size
// limit.
func stripBase64Whitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
}

func (s *Server) mcpAttach(ctx context.Context, raw json.RawMessage) (any, *jrpcError) {
	var a attachArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.Filename == "" {
		return nil, &jrpcError{Code: -32602, Message: "filename required"}
	}
	var body []byte
	switch {
	case a.ContentBase64 != "" && a.Content != "":
		return nil, &jrpcError{Code: -32602, Message: "provide exactly one of content_base64 or content, not both"}
	case a.ContentBase64 != "":
		decoded, err := base64.StdEncoding.DecodeString(stripBase64Whitespace(a.ContentBase64))
		if err != nil {
			return nil, &jrpcError{Code: -32602, Message: "content_base64 is not valid base64 (whitespace/newlines are tolerated): " + err.Error() + ". If this is plain text, pass it via `content` instead — that field needs no encoding."}
		}
		body = decoded
	case a.Content != "":
		// Plaintext convenience: the agent passes raw text/JSON and the
		// hub stores the UTF-8 bytes verbatim. Use this for text; reserve
		// content_base64 for true binary.
		body = []byte(a.Content)
	default:
		return nil, &jrpcError{Code: -32602, Message: "one of content_base64 (binary) or content (plain text) is required"}
	}
	if len(body) > maxBlobBytes {
		return nil, &jrpcError{Code: -32602, Message: fmt.Sprintf("blob exceeds the %d MiB cap (%d bytes)", maxBlobBytes/(1024*1024), maxBlobBytes)}
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
	if _, err := s.writeDB.ExecContext(ctx, `
		INSERT OR IGNORE INTO blobs (sha256, scope_path, size, mime, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		sha, path, len(body), mime, NowUTC()); err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"sha256":   sha,
		"size":     len(body),
		"mime":     mime,
		"filename": a.Filename,
	}), nil
}

// blobGetArgs accepts either the bare sha256 hex or a full URI
// (`blob:sha256/<hex>` or `hub-blob://<hex>`). Accepting both shapes
// means agents can pass the URI they read out of an A2A inbound file
// part verbatim, without slicing the scheme prefix themselves.
type blobGetArgs struct {
	SHA256 string `json:"sha256"`
	URI    string `json:"uri"`
}

// mcpGetBlob is the read companion to attach. Returns the bytes
// base64-encoded so the wire transport (JSON-RPC over stdio) stays
// printable; 25 MiB raw → ~33 MiB base64 still fits a single frame.
// The body inside `content_base64` is the SAME shape attach accepts —
// round-tripping a blob between two agents is symmetric.
func (s *Server) mcpGetBlob(ctx context.Context, raw json.RawMessage) (any, *jrpcError) {
	var a blobGetArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &jrpcError{Code: -32602, Message: "invalid args: " + err.Error()}
	}
	sha := strings.TrimSpace(a.SHA256)
	if sha == "" && a.URI != "" {
		sha = shaFromBlobURI(a.URI)
	}
	if sha == "" {
		return nil, &jrpcError{Code: -32602, Message: "sha256 (or uri) is required"}
	}
	// Cheap shape check before hitting the DB. sha256 hex is exactly 64
	// lowercase-hex characters; rejecting other shapes here keeps the
	// blob lookup from masking caller bugs as "not found".
	if !isHexSHA256(sha) {
		return nil, &jrpcError{Code: -32602, Message: "sha256 must be 64 lowercase hex characters"}
	}

	var path, mime string
	var size int64
	err := s.db.QueryRowContext(ctx,
		`SELECT scope_path, size, mime FROM blobs WHERE sha256 = ?`, sha).
		Scan(&path, &size, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &jrpcError{Code: -32000, Message: "blob not found: " + sha}
	}
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		// Row exists but file is gone — surface as a distinct error so
		// operators can diagnose the half-state (typically a manual rm
		// under DataRoot or a partial restore).
		if errors.Is(err, os.ErrNotExist) {
			return nil, &jrpcError{Code: -32000,
				Message: "blob row exists but bytes missing on disk: " + sha}
		}
		return nil, &jrpcError{Code: -32000, Message: err.Error()}
	}
	return mcpResultJSON(map[string]any{
		"sha256":         sha,
		"size":           size,
		"mime":           mime,
		"content_base64": base64.StdEncoding.EncodeToString(body),
	}), nil
}

// shaFromBlobURI strips the canonical scheme prefixes (`blob:sha256/`
// and `hub-blob://`) and returns the bare sha. Returns "" if the
// input doesn't match either prefix; the caller treats that as a
// missing sha.
func shaFromBlobURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if rest, ok := strings.CutPrefix(uri, "blob:sha256/"); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(uri, "hub-blob://"); ok {
		return rest
	}
	return ""
}

// isHexSHA256 reports whether s is exactly 64 lowercase hex chars.
// Inline so the cost is a single allocation-free pass over the bytes;
// importing regexp for a constant-shape check would be more code.
func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------
// get_event — single-record lookup
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
	_, err = s.writeDB.ExecContext(ctx,
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
	_, err := s.writeDB.ExecContext(ctx, `
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
	// Record the proposer's session so the decide handler can fan the
	// approve/reject outcome back to it as a fresh turn (parity with the
	// propose path, mcpPropose). Without this the steward never learns
	// whether its template landed.
	sessionID := s.lookupAgentSession(ctx, fromID)
	_, err = s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at,
			actor_kind, actor_handle, session_id
		) VALUES (?, NULL, 'team', NULL, 'template_proposal',
		          ?, 'minor', '[]', ?, 'open', ?,
		          'agent', NULLIF(?, ''), NULLIF(?, ''))`,
		id, summary, string(payload), now, actorHandle, sessionID)
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

	// ADR-027 W4: per-tool dispatch for two special cases that don't
	// fit the tier ladder.
	//
	// AskUserQuestion: claude-code routes this through the permission
	// prompt because it's "a tool that takes user attention" — but the
	// actual UX is a TUI picker rendered by claude itself, driven by
	// arrow-key send-keys from mobile (ADR-027 D-amend-4 / plan §5.B.1).
	// The permission gate is purely a "is this allowed?" check, which
	// for AskUserQuestion is always yes; the structured questionnaire
	// is handled separately via the host-runner hook surface (W5b).
	// Auto-allow here so the gate doesn't block the picker rendering.
	if a.ToolName == "AskUserQuestion" {
		s.recordAudit(ctx, team, "permission_prompt.auto_allowed",
			"agent", fromID,
			"AskUserQuestion gate auto-allowed; picker via hook surface",
			map[string]any{
				"tool_name": a.ToolName,
				"reason":    "askuserquestion_picker_via_hook",
			})
		return mcpResultJSON(map[string]any{
			"behavior":     "allow",
			"updatedInput": json.RawMessage(a.Input),
		}), nil
	}

	// Tier gate (W1.A): only escalate to a user prompt for tier ≥
	// significant. Trivial reads (file/glob/web search) and routine
	// writes (edits in scope, journal_append, etc.) auto-allow with
	// an audit trail and skip the attention queue entirely. This is
	// what makes the "director decides important things, not every
	// read" promise from docs/steward-sessions.md §6.5 real — under
	// --permission-prompt-tool the agent would otherwise prompt on
	// every tool call.
	//
	// ExitPlanMode is exempt from tier auto-allow: it always carries
	// dialog_type=plan_approval so mobile can render the proposed plan
	// as markdown rather than a tool-input preview. See dispatch below.
	tier := tierFor(a.ToolName)
	if a.ToolName != "ExitPlanMode" && (tier == TierTrivial || tier == TierRoutine) {
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

	// Per-tool payload + summary shaping. dialog_type drives the mobile
	// approval-card branch (plan_approval | tool_permission). plan_body
	// is set only for ExitPlanMode so the renderer can show the proposed
	// plan as markdown without re-deriving from input.
	dialogType := "tool_permission"
	summary := "tool: " + a.ToolName
	payloadMap := map[string]any{
		"tool_name":   a.ToolName,
		"input":       a.Input,
		"agent_id":    fromID,
		"tool_use_id": a.ToolUseID,
		"tier":        tier,
		"dialog_type": dialogType,
	}
	if a.ToolName == "ExitPlanMode" {
		dialogType = "plan_approval"
		payloadMap["dialog_type"] = dialogType
		summary = "plan-mode exit: review proposed plan"
		// Extract the plan body from claude-code's ExitPlanMode
		// tool_input. Schema (per docs/reference/claude-code-hook-schema.md):
		// {"plan": "<markdown body>"}. We surface it as plan_body so the
		// mobile renderer can show it as markdown without re-deriving.
		var inp struct {
			Plan string `json:"plan"`
		}
		_ = json.Unmarshal(a.Input, &inp)
		payloadMap["plan_body"] = inp.Plan
	}
	payload, _ := json.Marshal(payloadMap)

	id := NewID()
	now := NowUTC()
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)

	// ADR-030 W10: re-address the row to the worker's parent steward
	// when the strict same-project predicate holds. Otherwise leave
	// it team-wide-addressed (assignees='[]', assigned_tier=NULL) so
	// the existing behaviour is unchanged for orphan workers,
	// non-steward parents, and binding-drift cases.
	assignees := "[]"
	assignedTier := sql.NullString{}
	if stewardID := s.permissionPromptAddressee(ctx, team, fromID); stewardID != "" {
		b, _ := json.Marshal([]string{stewardID})
		assignees = string(b)
		assignedTier = sql.NullString{String: GovTierProjectSteward, Valid: true}
	}

	if _, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json,
			assigned_tier
		) VALUES (?, NULL, 'team', NULL, 'permission_prompt',
		          ?, 'minor', ?, 'open', ?,
		          'agent', NULLIF(?, ''), ?,
		          ?)`,
		id, summary, assignees, now, actorHandle, string(payload),
		assignedTier,
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
			_, _ = s.writeDB.ExecContext(context.Background(), `
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

// permissionPromptAddressee returns the parent steward's agent_id
// when the ADR-030 W10 strict same-project parent-steward predicate
// holds for the requesting worker, or "" otherwise. All three
// clauses MUST hold:
//
//  1. The worker has a non-NULL `parent_agent_id`.
//  2. The parent agent's `kind` matches the kind-based steward
//     predicate `LIKE 'steward.%'` (v1.0.607 detection rule).
//  3. The parent agent's `project_id` matches the worker's
//     `project_id`. This third clause is the binding-drift guard
//     — without it, a v1.0.605-class bug where the parent-id
//     pointer survives but the project binding has drifted would
//     route the row to a steward that no longer owns the worker.
//
// `project_id IS NOT NULL` is required on both sides so SQL's
// `NULL = NULL → NULL` semantics don't accidentally accept two
// unbound rows as "matching".
//
// Best-effort. Any DB error returns "" + a Warn log; the row stays
// team-wide-addressed (the existing pre-W10 behaviour), so a
// transient DB issue degrades to safe.
func (s *Server) permissionPromptAddressee(ctx context.Context, team, workerID string) string {
	var stewardID string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.id
		  FROM agents w
		  JOIN agents p ON p.id = w.parent_agent_id
		 WHERE w.team_id = ?
		   AND w.id = ?
		   AND w.parent_agent_id IS NOT NULL
		   AND p.kind LIKE 'steward.%'
		   AND p.project_id IS NOT NULL
		   AND w.project_id IS NOT NULL
		   AND p.project_id = w.project_id`,
		team, workerID).Scan(&stewardID)
	if err == nil {
		return stewardID
	}
	if !errors.Is(err, sql.ErrNoRows) {
		s.log.Warn("permission_prompt addressee lookup", "worker_id", workerID, "err", err)
	}
	return ""
}

// waitForAttentionResolution polls attention_items until status='resolved'
// (or ctx fires). Returns the full last decision dict so callers that
// care about extra fields (notably option_id from request_select) can
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
