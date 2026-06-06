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
	"github.com/termipod/hub/internal/auth"
)

type attentionIn struct {
	ScopeKind   string   `json:"scope_kind"`            // 'team' | 'project' | 'channel'
	ScopeID     string   `json:"scope_id,omitempty"`
	ProjectID   string   `json:"project_id,omitempty"`
	Kind        string   `json:"kind"`                  // 'select' | 'approval_request' | 'permission_prompt' | 'elicit' | 'template_proposal' | 'idle' | ...
	Summary     string   `json:"summary"`
	Severity    string   `json:"severity,omitempty"`
	RefEventID  string   `json:"ref_event_id,omitempty"`
	RefTaskID   string   `json:"ref_task_id,omitempty"`
	Assignees   []string `json:"assignees,omitempty"`
	// SessionID names the chat session the originating agent was in
	// when the attention was raised. Drives the detail screen's
	// "Open in chat" jump and the turn-based fan-out path
	// (dispatchAttentionReply uses it as the agent lookup cursor).
	// Optional — system-originated rows leave it empty.
	SessionID string `json:"session_id,omitempty"`
	// ActorHandle is the agent handle the attention is raised on
	// behalf of when the caller is a host-runner (codex's app-server
	// approval bridge — ADR-012 D3). The hub stamps it as
	// actor_kind='agent' so the mobile UI shows the correct origin
	// badge. Ignored when actor context is already in the request
	// auth (the agent's own MCP calls).
	ActorHandle string `json:"actor_handle,omitempty"`
	// PendingPayload carries the structured ask the principal needs
	// to act on. For codex permission_prompt rows it includes the
	// JSON-RPC method, the item id, the summary, and the codex
	// request id the driver parked locally — driver needs none of
	// the latter back, but the audit trail benefits from having it.
	PendingPayload json.RawMessage `json:"pending_payload,omitempty"`
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
	// ADR-030 columns (migration 0045). Exposed to mobile so the
	// Phase 3 per-kind propose cards (W15-W18) can render without a
	// second fetch. Empty/null on pre-0045 rows and on rows whose
	// kind isn't 'propose'. EscalationState comes from migration 0042
	// (loop-entity columns) and is load-bearing for the D-7 Option 2′
	// stalled-decision UI: a row whose AssignedTier doesn't match the
	// viewer's tier but whose EscalationState has surfaced to that
	// tier renders the "stalled" card variant.
	ChangeKind      string          `json:"change_kind,omitempty"`
	AssignedTier    string          `json:"assigned_tier,omitempty"`
	ChangeSpec      json.RawMessage `json:"change_spec,omitempty"`
	TargetRef       json.RawMessage `json:"target_ref,omitempty"`
	Executed        json.RawMessage `json:"executed,omitempty"`
	EscalationState string          `json:"escalation_state,omitempty"`
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
	// When the caller is a host-runner raising an attention on behalf
	// of an agent (codex permission_prompt bridge — ADR-012 D3), they
	// pass actor_handle in the body and we stamp actor_kind=agent so
	// the mobile UI shows the right origin chip. The agent's own MCP
	// calls leave the body field empty and the auth context wins.
	if in.ActorHandle != "" && actorHandle == "" {
		actorKind = "agent"
		actorHandle = in.ActorHandle
	}
	pending := string(in.PendingPayload)
	if pending == "" || pending == "null" {
		pending = ""
	}
	_, err := s.writeDB.ExecContext(r.Context(), `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			ref_event_id, ref_task_id, summary, severity,
			current_assignees_json, status, created_at,
			actor_kind, actor_handle, session_id,
			pending_payload_json
		) VALUES (?, NULLIF(?, ''), ?, NULLIF(?, ''), ?,
		          NULLIF(?, ''), NULLIF(?, ''), ?, ?,
		          ?, 'open', ?,
		          ?, ?, NULLIF(?, ''),
		          NULLIF(?, ''))`,
		id, in.ProjectID, in.ScopeKind, in.ScopeID, in.Kind,
		in.RefEventID, in.RefTaskID, in.Summary, severity,
		string(assignees), now,
		actorKind, nullIfEmpty(actorHandle), in.SessionID,
		pending)
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
	// ADR-030 W19.6: `include_escalated` is a forward-compat hook
	// for the mobile Me-page widening when the result set ever
	// gets a tier-narrow predicate. In MVP the baseline returns
	// every open row regardless of tier, so the param's effect is
	// purely informational (it changes nothing about the query
	// shape). The Phase 3 mobile client passes it unconditionally
	// so the contract is locked in before any tier-narrowing lands;
	// once `?tier=<t>` is wired, `include_escalated=true` will
	// widen WHERE assigned_tier=? OR escalation_state='escalated_'||?
	// per the plan's W19.6 literal. Currently parsed-but-unused
	// — accepting it ensures clients can ship today without
	// breaking when the narrowing arrives.
	_ = r.URL.Query().Get("include_escalated") // reserved for tier-narrow widening
	q := `
		SELECT id, COALESCE(project_id, ''), scope_kind, COALESCE(scope_id, ''), kind,
		       COALESCE(ref_event_id, ''), COALESCE(ref_task_id, ''),
		       summary, severity,
		       COALESCE(actor_kind, ''), COALESCE(actor_handle, ''),
		       COALESCE(session_id, ''),
		       current_assignees_json, decisions_json, escalation_history_json,
		       status, created_at, resolved_at, COALESCE(resolved_by, ''),
		       COALESCE(pending_payload_json, ''),
		       COALESCE(change_kind, ''), COALESCE(assigned_tier, ''),
		       COALESCE(change_spec_json, ''), COALESCE(target_ref_json, ''),
		       COALESCE(executed_json, ''), COALESCE(escalation_state, 'none')
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
		var changeSpec, targetRef, executed string
		var resolvedAt sql.NullString
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ScopeKind, &a.ScopeID, &a.Kind,
			&a.RefEventID, &a.RefTaskID, &a.Summary, &a.Severity,
			&a.ActorKind, &a.ActorHandle, &a.SessionID,
			&assignees, &decisions, &esc, &a.Status, &a.CreatedAt,
			&resolvedAt, &a.ResolvedBy, &pending,
			&a.ChangeKind, &a.AssignedTier,
			&changeSpec, &targetRef, &executed, &a.EscalationState); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.Assignees = json.RawMessage(assignees)
		a.Decisions = json.RawMessage(decisions)
		a.Escalation = json.RawMessage(esc)
		if pending != "" {
			a.PendingPayload = json.RawMessage(pending)
		}
		if changeSpec != "" {
			a.ChangeSpec = json.RawMessage(changeSpec)
		}
		if targetRef != "" {
			a.TargetRef = json.RawMessage(targetRef)
		}
		if executed != "" {
			a.Executed = json.RawMessage(executed)
		}
		if resolvedAt.Valid {
			a.ResolvedAt = &resolvedAt.String
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetAttention returns a single attention_items row. Added in
// ADR-027 W2i so host-runner's parked-hook coordination can poll a
// specific row by id without filtering through handleListAttention's
// broad scan. Auth follows the same team-scope chain as the other
// handlers in this file.
func (s *Server) handleGetAttention(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}
	var a attentionOut
	var assignees, decisions, esc, pending string
	var changeSpec, targetRef, executed string
	var resolvedAt sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(project_id, ''), scope_kind, COALESCE(scope_id, ''), kind,
		       COALESCE(ref_event_id, ''), COALESCE(ref_task_id, ''),
		       summary, severity,
		       COALESCE(actor_kind, ''), COALESCE(actor_handle, ''),
		       COALESCE(session_id, ''),
		       current_assignees_json, decisions_json, escalation_history_json,
		       status, created_at, resolved_at, COALESCE(resolved_by, ''),
		       COALESCE(pending_payload_json, ''),
		       COALESCE(change_kind, ''), COALESCE(assigned_tier, ''),
		       COALESCE(change_spec_json, ''), COALESCE(target_ref_json, ''),
		       COALESCE(executed_json, ''), COALESCE(escalation_state, 'none')
		FROM attention_items WHERE id = ?`, id).Scan(
		&a.ID, &a.ProjectID, &a.ScopeKind, &a.ScopeID, &a.Kind,
		&a.RefEventID, &a.RefTaskID, &a.Summary, &a.Severity,
		&a.ActorKind, &a.ActorHandle, &a.SessionID,
		&assignees, &decisions, &esc, &a.Status, &a.CreatedAt,
		&resolvedAt, &a.ResolvedBy, &pending,
		&a.ChangeKind, &a.AssignedTier,
		&changeSpec, &targetRef, &executed, &a.EscalationState)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "attention not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.Assignees = json.RawMessage(assignees)
	a.Decisions = json.RawMessage(decisions)
	a.Escalation = json.RawMessage(esc)
	if pending != "" {
		a.PendingPayload = json.RawMessage(pending)
	}
	if changeSpec != "" {
		a.ChangeSpec = json.RawMessage(changeSpec)
	}
	if targetRef != "" {
		a.TargetRef = json.RawMessage(targetRef)
	}
	if executed != "" {
		a.Executed = json.RawMessage(executed)
	}
	if resolvedAt.Valid {
		a.ResolvedAt = &resolvedAt.String
	}
	writeJSON(w, http.StatusOK, a)
}

type attentionDecideIn struct {
	Decision string `json:"decision"` // 'approve' | 'reject'
	// By is the decider handle. INPUT IS IGNORED — the server
	// overwrites it with the authenticated token's scope handle
	// (F-04). The field is retained so the same struct can be reused
	// for the fan-back reply, where the server-derived value rides
	// back out.
	By     string `json:"by,omitempty"`
	Reason string `json:"reason,omitempty"`
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
	// Override flips the "already resolved" 409 into the ADR-030 W9
	// principal-override path. Requires the authenticated token to be
	// principal-tier (owner|user kind — see principalActor, F-04) and
	// `policy.KindFor(change_kind).OverrideAllowed == true`. The
	// override path appends an override decision entry, dispatches
	// through `ProposeKind.Rollback`, emits an `attention.override`
	// audit row, and updates `executed_json`.
	Override bool `json:"override,omitempty"`
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
// principalActor resolves the authenticated caller for an attention
// decision. The recorded decider handle is always derived from the
// token scope — never from the request body — so an agent cannot
// attribute a decision to "@principal" (or anyone) by setting `by` in
// the payload (F-04). `isPrincipal` is true only for the human token
// kinds — `owner`/`user`, plus `operator` (the hub root, which is
// strictly more privileged than an owner; ADR-037 D2); agents and hosts
// can decide within policy quorum but can never wield principal-tier
// override authority.
//
// With no token in context (only reachable off the authed router — the
// middleware rejects unauthenticated callers before this handler) it
// fails closed: empty handle, not a principal.
func principalActor(ctx context.Context) (handle string, isPrincipal bool) {
	tok, ok := auth.FromContext(ctx)
	if !ok || tok == nil {
		return "", false
	}
	return principalFromScope(tok.ScopeJSON),
		tok.Kind == "owner" || tok.Kind == "user" || tok.Kind == "operator"
}

func (s *Server) handleDecideAttention(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "id")
	var in attentionDecideIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// F-04: bind the decider identity to the authenticated token, not
	// the request body. Every downstream site (decisions_json,
	// ProposeApplyContext.DeciderHandle, the attention.decide audit
	// row, the fan-back reply, and the override gate) reads `in.By`, so
	// overwriting it here is the single chokepoint that makes a
	// caller-supplied `by` inert.
	in.By, _ = principalActor(r.Context())
	// approve | reject are the standard decisions; `override` is
	// accepted only when paired with override=true (ADR-030 W9).
	// The override handler appends its own "override" entry to
	// decisions_json regardless of the incoming Decision field, so
	// callers MAY pass either decision="approve" or decision="override"
	// — both reach the override path when override=true.
	switch in.Decision {
	case "approve", "reject":
		// fall through
	case "override":
		if !in.Override {
			writeErr(w, http.StatusBadRequest,
				"decision='override' requires override=true (ADR-030 W9)")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "decision must be approve|reject|override")
		return
	}

	var (
		kind, tier, decisions, status, scopeID string
		// ADR-030 columns (W1). NULL on rows whose kind isn't 'propose'.
		// The dispatcher arm at the bottom of this handler reads them to
		// route through the propose-kind registry.
		changeKind, assignedTier, changeSpecJSON, targetRefJSON string
		// pending_payload carries the install spec for the
		// template_proposal kind (which predates the change_spec column).
		pendingPayload string
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT kind, COALESCE(tier, ''),
		       decisions_json, status, COALESCE(scope_id, ''),
		       COALESCE(change_kind, ''), COALESCE(assigned_tier, ''),
		       COALESCE(change_spec_json, ''), COALESCE(target_ref_json, ''),
		       COALESCE(pending_payload_json, '')
		FROM attention_items WHERE id = ?`, id).
		Scan(&kind, &tier, &decisions, &status, &scopeID,
			&changeKind, &assignedTier, &changeSpecJSON, &targetRefJSON,
			&pendingPayload)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "attention not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status != "open" {
		// ADR-030 W9 principal-override path. The default 409 stays
		// for non-override calls; an explicit `override: true` from
		// the principal against a kind whose policy allows override
		// re-enters the dispatcher with a Rollback call instead.
		if in.Override {
			s.handleAttentionOverride(w, r, attentionOverrideArgs{
				ID:             id,
				Team:           team,
				Kind:           kind,
				Status:         status,
				ChangeKind:     changeKind,
				AssignedTier:   assignedTier,
				ChangeSpecJSON: changeSpecJSON,
				Decisions:      decisions,
				In:             in,
			})
			return
		}
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
		_, err = s.writeDB.ExecContext(r.Context(), `
			UPDATE attention_items SET
				decisions_json = ?,
				status = 'resolved',
				resolved_at = ?
			WHERE id = ?`, string(newDecisions), now, id)
	} else {
		_, err = s.writeDB.ExecContext(r.Context(), `
			UPDATE attention_items SET
				decisions_json = ?
			WHERE id = ?`, string(newDecisions), id)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := attentionDecideOut{AttentionID: id, Decision: in.Decision, Resolved: resolved}
	// ADR-030 W8 dispatcher — an approved propose row routes through the
	// propose-kind registry. The registered Apply function emits the
	// per-kind audit row (with `meta.via="propose"`). The pre-ADR-030
	// alias arms (approval_request / template_proposal pending_payload)
	// and the legacy `{kind, error}` error shape were retired in W1.4 —
	// an apply error is now logged, not encoded into the response.
	if resolved && in.Decision == "approve" {
		var (
			dispatchKind string
			dispatchSpec json.RawMessage
			dispatchTRef json.RawMessage
		)
		dispatchVia := "propose"
		if kind == "propose" && changeKind != "" {
			// change_spec / target_ref came in through W4's mcpPropose;
			// assigned_tier is the row's tier (not the `tier` column).
			dispatchKind = changeKind
			dispatchSpec = json.RawMessage(changeSpecJSON)
			dispatchTRef = json.RawMessage(targetRefJSON)
		} else if kind == "template_proposal" {
			// The templates_propose path (pre-ADR-030): pending_payload
			// carries the {category, name, blob_sha256, …} install spec.
			// Route an approve through the same template.install Apply the
			// propose path uses, so approval actually installs — the W8
			// refactor retired the old inline alias but never rewired this,
			// leaving approve a no-op. target_ref is unused for installs.
			dispatchKind = "template.install"
			dispatchSpec = json.RawMessage(pendingPayload)
			dispatchVia = "alias_legacy"
		}

		if dispatchKind != "" {
			if pk, ok := LookupProposeKind(dispatchKind); ok && pk.Apply != nil {
				ac := ProposeApplyContext{
					AttentionID:   id,
					Team:          team,
					AssignedTier:  assignedTier,
					DeciderHandle: in.By,
					Via:           dispatchVia,
				}
				executed, applyErr := pk.Apply(r.Context(), s, ac, dispatchTRef, dispatchSpec)
				if applyErr != nil {
					s.log.Warn("propose apply failed",
						"attention_id", id, "kind", dispatchKind, "err", applyErr)
				} else {
					out.Executed = executed
				}
			}
		}

		// Mirror Executed into the ADR-030 column so post-mortem
		// queries on the attention row see what landed. Best-effort —
		// failure here doesn't roll back the decision.
		if len(out.Executed) > 0 {
			if _, err := s.writeDB.ExecContext(r.Context(),
				`UPDATE attention_items SET executed_json = ? WHERE id = ?`,
				string(out.Executed), id); err != nil {
				s.log.Warn("update executed_json", "attention_id", id, "err", err)
			}
		}
	}
	// Turn-based fan-out: when an attention raised by an agent is
	// resolved, deliver the resolution back to the agent as a fresh
	// user turn (input.attention_reply). The agent's request_*
	// MCP tool returned immediately with awaiting_response and ended
	// its turn; this is what wakes it up. Best-effort — the resolve
	// itself is what counts; fan-out failures don't roll back the
	// decide. template_proposal joins the allowlist (it used to be
	// excluded with "its own follow-up flow" — that flow was the W8
	// alias drop, which left the proposing steward with no feedback at
	// all); the fan-back tells it whether its template installed.
	//
	// permission_prompt is included as of ADR-012 D3: codex's
	// app-server JSON-RPC protocol exposes deferrable per-tool-call
	// approval requests, so a permission_prompt raised by codex is
	// turn-based on the wire (the JSON-RPC request stays open
	// indefinitely on the long-lived stdio pipe). The driver-side
	// AppServerDriver tracks the parked JSON-RPC request id by
	// attention id and uses the attention_reply event to drive its
	// JSON-RPC response. Claude's permission_prompt (sync canUseTool
	// hook) won't reach this branch because Claude resolves via
	// waitForAttentionDecision and never lands in /decide for a
	// pending hook — see ADR-011 D6.
	// project_steward_request joins this allowlist because the general
	// steward parks on the resolution: approve fans `body=<new agent id>`
	// back so it can A2A the project steward it just got; reject lets it
	// back off cleanly instead of waiting forever. session_id is recorded
	// at request time (mcpRequestProjectSteward), so dispatchAttentionReply
	// already knows where to deliver.
	if resolved && attentionAwaitsAgentReply(kind) {
		// ADR-030 W11: propose joins the allowlist. The fan-back
		// carries change_kind + executed so the requester's session
		// shows what landed in addition to the decision; the ADR-032
		// envelope rides under payload["envelope"].
		fanChangeKind := changeKind
		if kind == "template_proposal" {
			// changeKind column is empty on the template_proposal row;
			// report the effective install kind so the steward sees what
			// landed.
			fanChangeKind = "template.install"
		}
		extras := attentionReplyExtras{
			ChangeKind: fanChangeKind,
			Executed:   out.Executed,
		}
		_ = s.dispatchAttentionReply(r.Context(), id, kind, &in, extras)
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

// attentionOverrideArgs bundles the row-state the override handler
// needs from handleDecideAttention's SELECT so the call site stays a
// single line.
type attentionOverrideArgs struct {
	ID             string
	Team           string
	Kind           string // attention_items.kind ("propose", "approval_request", …)
	Status         string // already-resolved status (not "open")
	ChangeKind     string // propose change_kind, "" for non-propose rows
	AssignedTier   string
	ChangeSpecJSON string
	Decisions      string
	In             attentionDecideIn
}

// handleAttentionOverride is the ADR-030 W9 principal-override path.
// Reached only when handleDecideAttention sees `status != "open"` AND
// `in.Override == true`. Guard rails:
//
//  1. Caller must be principal-tier — the authenticated token's kind
//     is owner or user (principalActor, F-04). The request body's `by`
//     is never trusted for this gate.
//  2. The row must come from the propose ladder. `change_kind == ""`
//     means a legacy non-propose row (request_help, select, …);
//     override is not defined for those.
//  3. The kind's policy must opt in via `override_allowed: true`.
//  4. The kind must register a `Rollback` function. Kinds without
//     one explicitly refuse override (422 with hint).
//
// On success: appends an "override" decision entry to the row,
// calls Rollback through the registry, emits an `attention.override`
// audit row with the rollback executed_json in meta, mirrors the
// rollback's executed payload into `attention_items.executed_json`.
// The row stays in `status = 'resolved'` (the override doesn't
// re-open it — the override IS the next state).
func (s *Server) handleAttentionOverride(w http.ResponseWriter, r *http.Request, a attentionOverrideArgs) {
	if _, isPrincipal := principalActor(r.Context()); !isPrincipal {
		writeErr(w, http.StatusForbidden,
			"override requires a principal-tier caller (operator, owner, or user token); agents and hosts cannot override a resolved decision")
		return
	}
	if a.Status != "resolved" {
		// Other terminal statuses (e.g. "expired") shouldn't reach
		// override — there's nothing to roll back from.
		writeErr(w, http.StatusConflict,
			"override only applies to status='resolved' rows; got status='"+a.Status+"'")
		return
	}
	if a.ChangeKind == "" {
		writeErr(w, http.StatusUnprocessableEntity,
			"override is defined only for ADR-030 propose rows; this row has no change_kind")
		return
	}
	pol, _ := s.policy.KindFor(a.ChangeKind)
	if !pol.OverrideAllowed {
		writeErrHint(w, http.StatusBadRequest,
			"kind '"+a.ChangeKind+"' has override_allowed=false in policy.yaml",
			Hint{SeeTool: "policy_read"})
		return
	}
	pk, ok := LookupProposeKind(a.ChangeKind)
	if !ok || pk.Rollback == nil {
		writeErr(w, http.StatusUnprocessableEntity,
			"kind '"+a.ChangeKind+"' has no Rollback registered (override unsupported)")
		return
	}

	// Read the row's original executed_json — that's the input the
	// per-kind Rollback needs to compute the inverse transition.
	var executedJSON string
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(executed_json, '') FROM attention_items WHERE id = ?`,
		a.ID).Scan(&executedJSON); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if executedJSON == "" {
		writeErr(w, http.StatusUnprocessableEntity,
			"row has no executed_json — was the prior decision approved? rollback needs the original Apply result")
		return
	}

	// Guard against double-override: if the most recent decision is
	// already an "override", refuse. The principal can re-approve
	// the original via a regular call instead.
	var list []map[string]any
	_ = json.Unmarshal([]byte(a.Decisions), &list)
	if n := len(list); n > 0 {
		if d, _ := list[n-1]["decision"].(string); d == "override" {
			writeErr(w, http.StatusConflict,
				"row already overridden; further overrides not supported in MVP")
			return
		}
	}

	now := NowUTC()
	ac := ProposeApplyContext{
		AttentionID:   a.ID,
		Team:          a.Team,
		AssignedTier:  a.AssignedTier,
		DeciderHandle: a.In.By,
		Via:           "override",
	}
	rollbackExecuted, rbErr := pk.Rollback(r.Context(), s, ac,
		json.RawMessage(a.ChangeSpecJSON), json.RawMessage(executedJSON))
	if rbErr != nil {
		writeErr(w, http.StatusInternalServerError,
			"rollback ("+a.ChangeKind+"): "+rbErr.Error())
		return
	}

	// Append override decision to decisions_json. Schema additive:
	// `decision="override"` is a new entry kind alongside approve /
	// reject; consumers that switch on decision should add a
	// case.
	overrideEntry := map[string]any{
		"at":       now,
		"by":       a.In.By,
		"decision": "override",
		"reason":   a.In.Reason,
	}
	list = append(list, overrideEntry)
	newDecisions, _ := json.Marshal(list)
	if _, err := s.writeDB.ExecContext(r.Context(), `
		UPDATE attention_items
		   SET decisions_json = ?,
		       executed_json  = ?
		 WHERE id = ?`, string(newDecisions), string(rollbackExecuted), a.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// attention.override audit row — per ADR-030 D-8. Meta carries
	// the change_kind, tier, principal handle, and the rollback's
	// own executed payload so downstream queries don't need to join
	// the row to see what landed.
	s.recordAudit(r.Context(), a.Team, "attention.override", "attention", a.ID,
		"override "+a.ChangeKind+" via principal",
		map[string]any{
			"change_kind":       a.ChangeKind,
			"by":                a.In.By,
			"by_tier":           a.AssignedTier,
			"original_decision": priorTerminalDecision(list),
			"reason":            a.In.Reason,
			"rollback_executed": json.RawMessage(rollbackExecuted),
		})

	// ADR-030 W11: fan-back the override too. The requester saw
	// "approved + applied" on the first decide; without this
	// dispatch they'd never learn the apply was overridden + the
	// state reverted. Best-effort — dispatch failures don't
	// roll back the override itself (the resolve already
	// committed).
	overrideIn := *(&a.In) // copy so we can pin Decision to "override"
	overrideIn.Decision = "override"
	_ = s.dispatchAttentionReply(r.Context(), a.ID, a.Kind, &overrideIn, attentionReplyExtras{
		ChangeKind: a.ChangeKind,
		Executed:   rollbackExecuted,
	})

	out := attentionDecideOut{
		AttentionID: a.ID,
		Decision:    "override",
		Resolved:    true,
		Executed:    rollbackExecuted,
	}
	writeJSON(w, http.StatusOK, out)
}

// priorTerminalDecision walks back through decisions_json (skipping
// the override entry we just appended) and returns the last
// approve/reject decision string, or "" if none. Used to stamp the
// `original_decision` field of the override audit row.
func priorTerminalDecision(list []map[string]any) string {
	for i := len(list) - 1; i >= 0; i-- {
		d, _ := list[i]["decision"].(string)
		if d == "approve" || d == "reject" {
			return d
		}
	}
	return ""
}

// installProposedTemplate reads the proposed blob and writes it to the
// team's templates/<category>/<name> path. Returns the JSON-encoded result
// so it can be surfaced to the reviewer.
func (s *Server) installProposedTemplate(team, payload string) ([]byte, error) {
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
	// F-03: every component flows into a filesystem path, so validate
	// before use. category/name must be safe single segments (no
	// separators, parent refs, or hidden-file prefixes) or the
	// filepath.Join calls below escape team/templates; the sha must be
	// 64 hex chars or blobPath's sha[:2]/sha[2:4] slicing turns a
	// crafted "../" into an arbitrary-read primitive.
	if !safeCategoryName(p.Category) {
		return nil, fmt.Errorf("unsafe template category %q", p.Category)
	}
	if !safeTemplateName(p.Name) {
		return nil, fmt.Errorf("unsafe template name %q", p.Name)
	}
	if !isHexSHA256(p.BlobSHA256) {
		return nil, fmt.Errorf("invalid blob_sha256 %q (want 64 lowercase hex chars)", p.BlobSHA256)
	}
	body, err := os.ReadFile(s.blobPath(p.BlobSHA256))
	if err != nil {
		return nil, fmt.Errorf("read blob: %w", err)
	}
	// Per-team override dir (W4 / ADR-037 D5): an agent-proposed
	// template install lands in its own team's overlay, invisible to
	// other teams. Falls back to the global baseline only when no team
	// is supplied (defensive — applyTemplateInstall always passes one).
	base := teamTemplatesDir(s.cfg.DataRoot, team)
	if base == "" {
		base = filepath.Join(s.cfg.DataRoot, "team", "templates")
	}
	dstDir := filepath.Join(base, p.Category)
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

// attentionAwaitsAgentReply reports whether an attention kind owes a
// turn-based reply to a waiting agent. These are the kinds whose
// resolution fans an `input.attention_reply` back through
// dispatchAttentionReply (the request_* / propose / permission_prompt
// family). They MUST be resolved through /decide so the agent is woken;
// /resolve (the no-fan-out dismiss path) refuses them so a director
// can't silently strand an agent that ended its turn awaiting an answer.
func attentionAwaitsAgentReply(kind string) bool {
	switch kind {
	case "approval_request", "select", "help_request", "permission_prompt",
		"elicit", "project_steward_request", "propose", "template_proposal":
		return true
	}
	return false
}

type attentionResolveIn struct {
	ResolvedBy string          `json:"resolved_by,omitempty"`
	Decision   json.RawMessage `json:"decision,omitempty"`
}

// handleResolveAttention is the no-decision dismiss path — it flips an
// open item to resolved without fanning a reply to any agent. It is the
// director's "acknowledge / clear" affordance for informational rows
// (kind='notice', 'budget_exceeded', 'agent_error', …) that surface in
// the Me-page Messages slice and otherwise pile up unbounded.
//
// It refuses kinds that owe a waiting agent a reply (attentionAwaits-
// AgentReply): those carry a parked turn and must go through /decide so
// dispatchAttentionReply wakes the agent. Dismissing one here would
// resolve the row while leaving the agent blocked forever.
func (s *Server) handleResolveAttention(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "id")
	var in attentionResolveIn
	_ = json.NewDecoder(r.Body).Decode(&in)

	var kind, status string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT kind, status FROM attention_items WHERE id = ?`, id).
		Scan(&kind, &status)
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
	if attentionAwaitsAgentReply(kind) {
		writeErr(w, http.StatusConflict,
			"kind '"+kind+"' awaits an agent reply — resolve it via /decide, not /resolve")
		return
	}

	now := NowUTC()
	// resolved_by REFERENCES agents(id): it names the agent that closed
	// the row, not the director. A human dismiss leaves it NULL (the
	// dismisser's identity is captured in the audit row via the token
	// context, below); a caller-supplied agent id is honoured if valid.
	res, err := s.writeDB.ExecContext(r.Context(), `
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
		// Lost a race with a concurrent resolve/decide between the
		// SELECT and the UPDATE.
		writeErr(w, http.StatusConflict, "attention already resolved")
		return
	}
	s.recordAudit(r.Context(), team, "attention.dismiss", "attention", id,
		"dismissed ("+kind+")", map[string]any{"kind": kind})
	w.WriteHeader(http.StatusNoContent)
}

// attentionReplyEnvelopeText composes the short human-readable text
// for the ADR-032 envelope. Format: "<decision> <attention_kind>:
// <change_kind?> — <reason?>". Used by engine drivers that surface
// the envelope inline without parsing the propose-specific payload.
func attentionReplyEnvelopeText(attentionKind, changeKind, decision, reason string) string {
	parts := []string{decision + " " + attentionKind}
	if changeKind != "" {
		parts = append(parts, ": "+changeKind)
	}
	if reason != "" {
		parts = append(parts, " — "+reason)
	}
	return strings.Join(parts, "")
}

// stringOrDefault returns s when non-empty, else dflt. Tiny helper
// to keep the envelope-composition table readable.
func stringOrDefault(s, dflt string) string {
	if s != "" {
		return s
	}
	return dflt
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
// attentionReplyExtras carries ADR-030 W11 propose-specific fields
// into the fan-back payload. Both fields are optional; the
// envelope-composition path tolerates "" / nil.
type attentionReplyExtras struct {
	// ChangeKind is the propose row's change_kind (e.g.
	// "task.set_status"). Empty for non-propose attention kinds.
	ChangeKind string
	// Executed is the apply (or rollback) result, mirrored here so
	// the agent's session shows what landed without a second
	// hub round-trip. Empty when the decision was reject.
	Executed json.RawMessage
}

func (s *Server) dispatchAttentionReply(ctx context.Context, attentionID, kind string, in *attentionDecideIn, extras attentionReplyExtras) error {
	var (
		sessionID    sql.NullString
		actorHandle  sql.NullString
		cause        sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT session_id, actor_handle, cause
		  FROM attention_items WHERE id = ?`, attentionID,
	).Scan(&sessionID, &actorHandle, &cause); err != nil {
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
	//
	// ADR-030 W11 additions:
	//   - change_kind / executed: propose-specific extras the agent's
	//     surface uses to render "your propose(task.set_status) was
	//     approved; status went todo→done".
	//   - envelope: nested ADR-032 message envelope so directive-trace
	//     queries resolve a propose-decision edge uniformly with other
	//     directed-input edges. Nested (not flat) here because the
	//     payload's top-level `kind` already holds the attention kind
	//     ("propose", "approval_request", …); flattening would collide
	//     with envelope.kind. Envelope-aware consumers read
	//     payload["envelope"]["from"|"to"|"kind"|"text"|"cause"|"thread"].
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
	if extras.ChangeKind != "" {
		payloadMap["change_kind"] = extras.ChangeKind
	}
	if len(extras.Executed) > 0 {
		payloadMap["executed"] = json.RawMessage(extras.Executed)
	}

	// Build the envelope. Best-effort — a missing target handle or
	// cause leaves those fields blank rather than dropping the whole
	// fan-back.
	requesterHandle := ""
	if actorHandle.Valid {
		requesterHandle = actorHandle.String
	}
	envCause := ""
	if cause.Valid {
		envCause = cause.String
	}
	envelope := map[string]any{
		// From: the authoriser. Use the decider handle; fall back
		// to "@principal" if missing (defensive — the override path
		// gates on this already).
		"from": map[string]any{
			"role":   RolePrincipal,
			"handle": stringOrDefault(in.By, "@principal"),
		},
		// To: the requester (the agent that called propose). Worker
		// is the realistic case for the MVP propose kinds; stewards
		// CAN propose too (W4 cross-project check permits it). We
		// stamp peer_worker as the conservative default; the surface
		// driver routes by agent_id regardless of role.
		"to": map[string]any{
			"role":     RolePeerWorker,
			"handle":   requesterHandle,
			"agent_id": currentAgentID.String,
		},
		// Kind: ADR-032 D-2's closed enum. A propose-decision
		// CLOSES the loop the propose opened, so "report" fits. We
		// do NOT use "attention_reply" here (that's the agent_event
		// kind, not the envelope kind — the closed enum is
		// directive|question|report|notification).
		"kind": KindReport,
		// Text: human-readable summary so engines that present the
		// envelope inline can render without parsing the payload
		// fields.
		"text":  attentionReplyEnvelopeText(kind, extras.ChangeKind, in.Decision, in.Reason),
		"cause": envCause,
		"thread": map[string]any{
			"transport": TransportAttention,
			"id":        attentionID,
		},
	}
	payloadMap["envelope"] = envelope

	payload, err := json.Marshal(payloadMap)
	if err != nil {
		return err
	}

	agentID := currentAgentID.String
	id, _, _, ts, err := insertAgentEvent(ctx, s.eventsWriteDB, agentEventInsert{
		AgentID:     agentID,
		SessionID:   sessionID.String,
		Kind:        "input.attention_reply",
		Producer:    "user",
		PayloadJSON: string(payload),
	})
	if err != nil {
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
