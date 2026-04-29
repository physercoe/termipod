package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

type attentionIn struct {
	ScopeKind   string   `json:"scope_kind"`            // 'team' | 'project' | 'channel'
	ScopeID     string   `json:"scope_id,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
	Kind        string   `json:"kind"`                  // 'select' | 'approval_request' | 'permission_prompt' | 'template_proposal' | 'idle' | ...
	Summary     string   `json:"summary"`
	Severity    string   `json:"severity,omitempty"`
	RefEventID  string   `json:"ref_event_id,omitempty"`
	RefTaskID   string   `json:"ref_task_id,omitempty"`
	Assignees   []string `json:"assignees,omitempty"`
}

type attentionOut struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id,omitempty"`
	ScopeKind   string          `json:"scope_kind"`
	ScopeID     string          `json:"scope_id,omitempty"`
	Kind        string          `json:"kind"`
	Summary     string          `json:"summary"`
	Severity    string          `json:"severity"`
	RefEventID  string          `json:"ref_event_id,omitempty"`
	RefTaskID   string          `json:"ref_task_id,omitempty"`
	ActorKind   string          `json:"actor_kind,omitempty"`
	ActorHandle string          `json:"actor_handle,omitempty"`
	// SessionID names the chat session the originating agent was running
	// in when it raised this attention. Populated by the request_*
	// MCP handlers; empty for system-originated attentions (budget,
	// spawn approval) and pre-v1.0.336 rows. Drives the detail screen's
	// "Open in chat" jump and the recent-transcript context block.
	SessionID   string          `json:"session_id,omitempty"`
	Assignees   json.RawMessage `json:"assignees"`
	Decisions   json.RawMessage `json:"decisions"`
	Escalation  json.RawMessage `json:"escalation_history"`
	Status      string          `json:"status"`
	CreatedAt   string          `json:"created_at"`
	ResolvedAt  *string         `json:"resolved_at,omitempty"`
	ResolvedBy  string          `json:"resolved_by,omitempty"`
	// PendingPayload is the row's pending_payload_json (when present).
	// W1.A reads it on the mobile side to render an inline approval card
	// for kind=permission_prompt items: it carries tool_name, input,
	// agent_id, tool_use_id, and tier (resolved server-side from
	// tiers.go so the agent can't reclassify its own actions).
	PendingPayload json.RawMessage `json:"pending_payload,omitempty"`
}

func (s *Server) handleCreateAttention(w http.ResponseWriter, r *http.Request) {
	var in attentionIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.ScopeKind == "" || in.Kind == "" || in.Summary == "" {
		writeErr(w, http.StatusBadRequest, "scope_kind, kind, summary required")
		return
	}
	severity := in.Severity
	if severity == "" {
		severity = "minor"
	}
	assignees, _ := json.Marshal(coalesceStrings(in.Assignees))
	id := NewID()
	now := NowUTC()
	_, actorKind, actorHandle := actorFromContext(r.Context())
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			ref_event_id, ref_task_id, summary, severity,
			current_assignees_json, status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULLIF(?, ''), ?, NULLIF(?, ''), ?,
		          NULLIF(?, ''), NULLIF(?, ''), ?, ?,
		          ?, 'open', ?,
		          ?, ?)`,
		id, in.ProjectID, in.ScopeKind, in.ScopeID, in.Kind,
		in.RefEventID, in.RefTaskID, in.Summary, severity,
		string(assignees), now,
		actorKind, nullIfEmpty(actorHandle))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "created_at": now})
}

func (s *Server) handleListAttention(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	scope := r.URL.Query().Get("scope")
	q := `
		SELECT id, COALESCE(project_id, ''), scope_kind, COALESCE(scope_id, ''), kind,
		       COALESCE(ref_event_id, ''), COALESCE(ref_task_id, ''),
		       summary, severity,
		       COALESCE(actor_kind, ''), COALESCE(actor_handle, ''),
		       COALESCE(session_id, ''),
		       current_assignees_json, decisions_json, escalation_history_json,
		       status, created_at, resolved_at, COALESCE(resolved_by, ''),
		       COALESCE(pending_payload_json, '')
		FROM attention_items WHERE status = ?`
	args := []any{status}
	if scope != "" {
		q += " AND scope_kind = ?"
		args = append(args, scope)
	}
	q += " ORDER BY created_at DESC LIMIT 200"
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []attentionOut{}
	for rows.Next() {
		var a attentionOut
		var assignees, decisions, esc, pending string
		var resolvedAt sql.NullString
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ScopeKind, &a.ScopeID, &a.Kind,
			&a.RefEventID, &a.RefTaskID, &a.Summary, &a.Severity,
			&a.ActorKind, &a.ActorHandle, &a.SessionID,
			&assignees, &decisions, &esc, &a.Status, &a.CreatedAt,
			&resolvedAt, &a.ResolvedBy, &pending); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.Assignees = json.RawMessage(assignees)
		a.Decisions = json.RawMessage(decisions)
		a.Escalation = json.RawMessage(esc)
		if pending != "" {
			a.PendingPayload = json.RawMessage(pending)
		}
		if resolvedAt.Valid {
			a.ResolvedAt = &resolvedAt.String
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, out)
}

type attentionDecideIn struct {
	Decision string `json:"decision"` // 'approve' | 'reject'
	By       string `json:"by,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// OptionID names the picked option for kind='select' attention
	// items (request_select MCP tool). The request stores the option
	// labels in pending_payload_json; the picked id flows back to the
	// agent via waitForAttentionResolution. Ignored for kinds that
	// don't have options.
	OptionID string `json:"option_id,omitempty"`
	// Body carries the principal's free-text reply for kind='help_request'
	// attention items (request_help MCP tool). Required when decision='approve'
	// on a help_request — a reject signals "dismissed, agent should give up".
	// Ignored for kinds whose answer space is constrained.
	Body string `json:"body,omitempty"`
}

type attentionDecideOut struct {
	AttentionID string          `json:"attention_id"`
	Decision    string          `json:"decision"`
	Resolved    bool            `json:"resolved"`
	Executed    json.RawMessage `json:"executed,omitempty"` // populated when an approve triggers an action
}

// handleDecideAttention records an approve/reject on an attention_item and
// resolves it once the tier's quorum is reached. Quorum is looked up via
// s.policy.QuorumFor(tier); a tier of "" or a missing `quorum` entry both
// fall through to 1, which preserves the previous single-approver behavior.
// A reject always resolves (veto-wins); approvals accumulate in
// decisions_json until the threshold is hit. When an approve_-resolved
// attention has a pending_payload, this handler executes it (currently:
// spawn, template_proposal) so the caller can observe the downstream effect
// in a single call.
//
// Concurrency note: two simultaneous approvals can both read approves=N-1
// and each write approves=N without noticing the other. That's tolerable
// today — the net effect is one executed action, not two, and the duplicate
// decision row is visible in the trail. Tightening this needs a CAS on the
// status column; deferred until we have >1 active approver per tier.
func (s *Server) handleDecideAttention(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "id")
	var in attentionDecideIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Decision != "approve" && in.Decision != "reject" {
		writeErr(w, http.StatusBadRequest, "decision must be approve|reject")
		return
	}

	var kind, tier, payload, decisions, status, scopeID string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT kind, COALESCE(tier, ''), COALESCE(pending_payload_json, ''),
		       decisions_json, status, COALESCE(scope_id, '')
		FROM attention_items WHERE id = ?`, id).
		Scan(&kind, &tier, &payload, &decisions, &status, &scopeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "attention not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status != "open" {
		writeErr(w, http.StatusConflict, "attention already resolved")
		return
	}

	// help_request answers via free text in `body`. An approve without a
	// body is meaningless (the agent is waiting for the user's reply, not
	// just an "ok"); reject is fine — it dismisses the request without an
	// answer and the agent should treat it as "give up or try alternative".
	if kind == "help_request" && in.Decision == "approve" && in.Body == "" {
		writeErr(w, http.StatusBadRequest,
			"body required when approving a help_request")
		return
	}

	// Append the decision to decisions_json.
	var list []map[string]any
	_ = json.Unmarshal([]byte(decisions), &list)
	now := NowUTC()
	entry := map[string]any{
		"at":       now,
		"by":       in.By,
		"decision": in.Decision,
		"reason":   in.Reason,
	}
	if in.OptionID != "" {
		entry["option_id"] = in.OptionID
	}
	if in.Body != "" {
		// waitForAttentionResolution returns the last decision dict
		// verbatim, so request_help's long-poll picks up `body` here
		// without a separate lookup against pending_payload.
		entry["body"] = in.Body
	}
	list = append(list, entry)
	newDecisions, _ := json.Marshal(list)

	// Policy-driven quorum: count approves including the one we just
	// appended, compare against the tier threshold. A reject always
	// resolves so a single vetoer can halt the action. When the threshold
	// isn't yet met, persist the decision and leave status='open' so
	// further approvers can weigh in.
	approves := 0
	for _, d := range list {
		if s, _ := d["decision"].(string); s == "approve" {
			approves++
		}
	}
	need := s.policy.QuorumFor(tier)
	resolved := in.Decision == "reject" || approves >= need

	// resolved_by has a FK to agents(id); in.By is a handle used only for
	// the decision trail, so it lands in decisions_json, not the FK column.
	if resolved {
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE attention_items SET
				decisions_json = ?,
				status = 'resolved',
				resolved_at = ?
			WHERE id = ?`, string(newDecisions), now, id)
	} else {
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE attention_items SET
				decisions_json = ?
			WHERE id = ?`, string(newDecisions), id)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := attentionDecideOut{AttentionID: id, Decision: in.Decision, Resolved: resolved}
	if resolved && in.Decision == "approve" && kind == "approval_request" && payload != "" {
		// Dispatch the gated action. Today we only understand spawn payloads.
		var sp spawnIn
		if err := json.Unmarshal([]byte(payload), &sp); err == nil && sp.ChildHandle != "" {
			result, _, err := s.DoSpawn(r.Context(), team, sp)
			if err == nil {
				b, _ := json.Marshal(map[string]any{
					"kind":      "spawn",
					"spawn_id":  result.SpawnID,
					"agent_id":  result.AgentID,
					"spawned_at": result.SpawnedAt,
				})
				out.Executed = b
			} else {
				b, _ := json.Marshal(map[string]any{
					"kind":  "spawn",
					"error": err.Error(),
				})
				out.Executed = b
			}
		}
	}
	if resolved && in.Decision == "approve" && kind == "template_proposal" && payload != "" {
		// Install the proposed template body to team/templates/<cat>/<name>.
		// Reviewer's approval is the authorization; we copy the blob, not the
		// request content, so the on-disk file is byte-identical to what was
		// reviewed even if the blob store is the source of truth.
		installed, installErr := s.installProposedTemplate(payload)
		if installErr == nil {
			out.Executed = installed
		} else {
			b, _ := json.Marshal(map[string]any{
				"kind":  "template_install",
				"error": installErr.Error(),
			})
			out.Executed = b
		}
	}
	// Turn-based fan-out: when an attention raised by an agent is
	// resolved, deliver the resolution back to the agent as a fresh
	// user turn (input.attention_reply). The agent's request_*
	// MCP tool returned immediately with awaiting_response and ended
	// its turn; this is what wakes it up. Best-effort — the resolve
	// itself is what counts; fan-out failures don't roll back the
	// decide. permission_prompt + template_proposal are excluded:
	// the first is sync-blocking by vendor contract (waitForAttentionDecision
	// path, separate from this handler's response semantics) and
	// the second has its own follow-up flow.
	if resolved && (kind == "approval_request" || kind == "select" || kind == "help_request") {
		_ = s.dispatchAttentionReply(r.Context(), id, kind, &in)
	}
	s.recordAudit(r.Context(), team, "attention.decide", "attention", id,
		in.Decision+" attention ("+kind+")",
		map[string]any{
			"decision": in.Decision,
			"kind":     kind,
			"tier":     tier,
			"by":       in.By,
			"reason":   in.Reason,
		})
	writeJSON(w, http.StatusOK, out)
}

// installProposedTemplate reads the proposed blob and writes it to the
// team's templates/<category>/<name> path. Returns the JSON-encoded result
// so it can be surfaced to the reviewer.
func (s *Server) installProposedTemplate(payload string) ([]byte, error) {
	var p struct {
		Category   string `json:"category"`
		Name       string `json:"name"`
		BlobSHA256 string `json:"blob_sha256"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	if p.Category == "" || p.Name == "" || p.BlobSHA256 == "" {
		return nil, errors.New("payload missing category/name/blob_sha256")
	}
	body, err := os.ReadFile(s.blobPath(p.BlobSHA256))
	if err != nil {
		return nil, fmt.Errorf("read blob: %w", err)
	}
	dstDir := filepath.Join(s.cfg.DataRoot, "team", "templates", p.Category)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	// Trailing .yaml keeps the file discoverable by listTemplates; the
	// agent's proposed "name" already includes a version suffix like v1.
	name := p.Name
	if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
		name += ".yaml"
	}
	dst := filepath.Join(dstDir, name)
	if err := os.WriteFile(dst, body, 0o644); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"kind":     "template_install",
		"category": p.Category,
		"name":     p.Name,
		"path":     dst,
		"bytes":    len(body),
	})
}

// attentionContextOut is the payload returned by /attention/{id}/context.
// Two layers of context: the originating session's identity (so the
// detail screen can render an "Open in chat" jump) and the recent
// transcript turns leading up to the request (the actual *why*).
//
// `events` is newest-first (seq DESC) so the renderer can take the
// first N for the most relevant slice without sorting; the typical
// caller wants the last 5-10 turns. ts is bounded by the attention's
// created_at to avoid leaking events that happened *after* the
// attention was raised — those weren't context for the agent's ask.
type attentionContextOut struct {
	AttentionID string                   `json:"attention_id"`
	SessionID   string                   `json:"session_id,omitempty"`
	AgentID     string                   `json:"agent_id,omitempty"`
	AgentHandle string                   `json:"agent_handle,omitempty"`
	Events      []map[string]any         `json:"events"`
}

func (s *Server) handleAttentionContext(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "id")

	var (
		sessionID, createdAt string
		actorHandle          sql.NullString
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(session_id, ''), created_at, actor_handle
		  FROM attention_items
		 WHERE id = ?`, id).Scan(&sessionID, &createdAt, &actorHandle)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "attention not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := attentionContextOut{
		AttentionID: id,
		SessionID:   sessionID,
		Events:      []map[string]any{},
	}
	if actorHandle.Valid {
		out.AgentHandle = actorHandle.String
	}

	// No session pointer = no context to render. Return empty events
	// rather than 404 so the mobile detail screen can degrade gracefully
	// (older attention rows from before this column was populated).
	if sessionID == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Resolve the agent_id via the session row. Sessions point at their
	// current_agent_id; for resumed sessions this is the live agent, for
	// archived sessions it's the most recent. Either way, the recent
	// transcript belongs to that agent_id.
	var currentAgentID sql.NullString
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT current_agent_id FROM sessions
		 WHERE team_id = ? AND id = ?`, team, sessionID).Scan(&currentAgentID); err == nil {
		if currentAgentID.Valid {
			out.AgentID = currentAgentID.String
		}
	}

	// Pull the last 10 events from this session up to the attention's
	// created_at. Newest-first so the caller can slice without sorting.
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, agent_id, seq, ts, kind, producer, payload_json
		  FROM agent_events
		 WHERE session_id = ?
		   AND ts <= ?
		 ORDER BY seq DESC
		 LIMIT 10`, sessionID, createdAt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var (
			eid, agentID, kind, producer, payload, ts string
			seq                                       int64
		)
		if err := rows.Scan(&eid, &agentID, &seq, &ts, &kind, &producer, &payload); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out.Events = append(out.Events, map[string]any{
			"id":       eid,
			"agent_id": agentID,
			"seq":      seq,
			"ts":       ts,
			"kind":     kind,
			"producer": producer,
			"payload":  json.RawMessage(payload),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type attentionResolveIn struct {
	ResolvedBy string          `json:"resolved_by,omitempty"`
	Decision   json.RawMessage `json:"decision,omitempty"`
}

func (s *Server) handleResolveAttention(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var in attentionResolveIn
	_ = json.NewDecoder(r.Body).Decode(&in)

	now := NowUTC()
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE attention_items SET
			status = 'resolved',
			resolved_at = ?,
			resolved_by = NULLIF(?, '')
		WHERE id = ? AND status = 'open'`,
		now, in.ResolvedBy, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "item not found or already resolved")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dispatchAttentionReply posts an `input.attention_reply` agent_event so
// the originating agent receives the principal's decision as a fresh user
// turn. This is the wake-up path for the turn-based request_approval /
// request_select / request_help flow: those tools return immediately with
// `awaiting_response`, the agent ends its turn, and this delivery is what
// resumes the conversation.
//
// Target agent: lookup via attention.session_id → sessions.current_agent_id.
// The current_agent_id may differ from the agent that raised the attention
// (e.g. session was resumed with a fresh agent in the meantime); that's
// correct — the new agent inherits the conversation context and is who
// should receive the reply.
//
// Best-effort: returns the first error encountered but the decide handler
// ignores it (the resolve already committed; we don't want a dispatch hiccup
// to make the decision look failed). For attentions with no session_id
// (system-originated rows, legacy rows pre-v1.0.336), we silently skip
// the fan-out — there's no agent to deliver to.
func (s *Server) dispatchAttentionReply(ctx context.Context, attentionID, kind string, in *attentionDecideIn) error {
	var sessionID sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT session_id FROM attention_items WHERE id = ?`, attentionID,
	).Scan(&sessionID); err != nil {
		return err
	}
	if !sessionID.Valid || sessionID.String == "" {
		return nil
	}
	var currentAgentID sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT current_agent_id FROM sessions WHERE id = ?`, sessionID.String,
	).Scan(&currentAgentID); err != nil {
		return err
	}
	if !currentAgentID.Valid || currentAgentID.String == "" {
		return nil
	}

	// payload carries the structured fields the driver needs to build a
	// readable user turn for the engine. The driver formats the surface
	// representation (text content) per kind; carrying the raw fields here
	// keeps the dispatch policy layered above the engine wire shape.
	payloadMap := map[string]any{
		"request_id": attentionID,
		"kind":       kind,
		"decision":   in.Decision,
	}
	if in.Body != "" {
		payloadMap["body"] = in.Body
	}
	if in.OptionID != "" {
		payloadMap["option_id"] = in.OptionID
	}
	if in.Reason != "" {
		payloadMap["reason"] = in.Reason
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		return err
	}

	id := NewID()
	ts := NowUTC()
	agentID := currentAgentID.String
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, ?
		  FROM agent_events WHERE agent_id = ?`,
		id, agentID, ts, "input.attention_reply", "user", string(payload), sessionID.String, agentID,
	); err != nil {
		return err
	}
	s.touchSession(ctx, sessionID.String)
	s.bus.Publish(agentBusKey(agentID), map[string]any{
		"id":         id,
		"agent_id":   agentID,
		"ts":         ts,
		"kind":       "input.attention_reply",
		"producer":   "user",
		"payload":    json.RawMessage(payload),
		"session_id": sessionID.String,
	})
	return nil
}
