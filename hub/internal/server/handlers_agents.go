package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
)

type agentIn struct {
	Handle       string          `json:"handle"`
	Kind         string          `json:"kind"`
	Backend      json.RawMessage `json:"backend,omitempty"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	ParentID     string          `json:"parent_agent_id,omitempty"`
	HostID       string          `json:"host_id,omitempty"`
	BudgetCents  *int            `json:"budget_cents,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	JournalPath  string          `json:"journal_path,omitempty"`
}

type agentOut struct {
	ID           string          `json:"id"`
	TeamID       string          `json:"team_id"`
	Handle       string          `json:"handle"`
	Kind         string          `json:"kind"`
	Backend      json.RawMessage `json:"backend"`
	Capabilities json.RawMessage `json:"capabilities"`
	ParentID     string          `json:"parent_agent_id,omitempty"`
	HostID       string          `json:"host_id,omitempty"`
	// ProjectID binds the agent to a project per ADR-025. Empty for
	// pre-ADR (v1.0.563-) rows that predate the column. Mobile uses
	// this to populate the project detail Agents tab; the W9 spawn
	// gate (v1.0.565) authorizes against it.
	ProjectID    string  `json:"project_id,omitempty"`
	Status       string  `json:"status"`
	PaneID       string  `json:"pane_id,omitempty"`
	WorktreePath string  `json:"worktree_path,omitempty"`
	JournalPath  string  `json:"journal_path,omitempty"`
	BudgetCents  *int    `json:"budget_cents,omitempty"`
	SpentCents   int     `json:"spent_cents"`
	PauseState   string  `json:"pause_state"`
	IdleSince    *string `json:"idle_since,omitempty"`
	CreatedAt    string  `json:"created_at"`
	TerminatedAt *string `json:"terminated_at,omitempty"`
	ArchivedAt   *string `json:"archived_at,omitempty"`
	// LastEventAt is the ts of the most recent agent_events row for this
	// agent, or nil if no events have been recorded yet. Mobile uses this
	// to classify a `running` agent as healthy / stale / stuck — a wedged
	// claude process keeps `status='running'` but stops emitting events,
	// so `(status, age(LastEventAt))` is the load-bearing liveness signal.
	LastEventAt *string `json:"last_event_at,omitempty"`
	// Mode is the resolved driving mode (M1|M2|M4). Empty for legacy
	// rows predating the resolver; host-runner interprets empty as M4.
	Mode string `json:"mode,omitempty"`
	// Populated on the single-agent GET by joining agent_spawns; omitted
	// from list-agents to keep that payload small.
	SpawnSpecYaml  string          `json:"spawn_spec_yaml,omitempty"`
	SpawnAuthority json.RawMessage `json:"spawn_authority,omitempty"`
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwnerOrSteward(w, r) { // #75
		return
	}
	team := chi.URLParam(r, "team")
	var in agentIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Handle == "" || in.Kind == "" {
		writeErr(w, http.StatusBadRequest, "handle and kind required")
		return
	}
	// Bare-handle convention (post-v1.0.636): handles are stored without
	// the `@` display sigil. A caller that passes `@worker` is treated
	// the same as `worker` so legacy templates keep working while the
	// data stays clean. See migration 0044 + glossary entry.
	in.Handle = normalizeAgentHandle(in.Handle)
	backend := defaultRawObject(in.Backend)
	caps := defaultRawArray(in.Capabilities)
	id := NewID()
	now := NowUTC()
	_, err := s.writeDB.ExecContext(r.Context(), `
		INSERT INTO agents (
			id, team_id, handle, kind, backend_json, capabilities_json,
			parent_agent_id, host_id, budget_cents, worktree_path, journal_path,
			status, pause_state, created_at
		) VALUES (?, ?, ?, ?, ?, ?,
		          NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''),
		          'pending', 'running', ?)`,
		id, team, in.Handle, in.Kind, string(backend), string(caps),
		in.ParentID, in.HostID, nullableInt(in.BudgetCents),
		in.WorktreePath, in.JournalPath, now)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, agentOut{
		ID: id, TeamID: team, Handle: in.Handle, Kind: in.Kind,
		Backend: json.RawMessage(backend), Capabilities: json.RawMessage(caps),
		ParentID: in.ParentID, HostID: in.HostID,
		Status: "pending", PauseState: "running",
		WorktreePath: in.WorktreePath, JournalPath: in.JournalPath,
		BudgetCents: in.BudgetCents, CreatedAt: now,
	})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	// last_event_at (MAX(ts) over agent_events) is fetched separately from
	// the event store after the agent rows are read — a correlated subquery
	// would cross the control↔event store boundary (ADR-045 D2). lastEventAtForAgents
	// batches the lookup over the (bounded) page of agent ids.
	q := `
		SELECT id, team_id, handle, kind, backend_json, capabilities_json,
		       COALESCE(parent_agent_id, ''), COALESCE(host_id, ''),
		       COALESCE(project_id, ''),
		       status, COALESCE(pane_id, ''),
		       COALESCE(worktree_path, ''), COALESCE(journal_path, ''),
		       budget_cents, spent_cents, pause_state, idle_since,
		       created_at, terminated_at, COALESCE(driving_mode, ''),
		       archived_at
		FROM agents WHERE team_id = ?`
	args := []any{team}
	if host := r.URL.Query().Get("host_id"); host != "" {
		q += " AND host_id = ?"
		args = append(args, host)
	}
	// `status` is the most specific filter — when set it pins to exactly
	// one engine state, regardless of live / include_terminated defaults.
	// `live=1` is a convenience superset for the canonical "alive" set
	// (running/idle/paused); useful from the MCP path where workers want
	// "anyone I could plausibly talk to" without enumerating each state.
	// When neither is set the default hides terminated/failed/crashed —
	// stale rows pile up in long-running teams and clutter the response.
	// Operators who want the full history pass include_terminated=1.
	st := r.URL.Query().Get("status")
	live := r.URL.Query().Get("live") == "1" || r.URL.Query().Get("live") == "true"
	includeTerminated := r.URL.Query().Get("include_terminated") == "1" ||
		r.URL.Query().Get("include_terminated") == "true"
	switch {
	case st != "":
		q += " AND status = ?"
		args = append(args, st)
	case live:
		q += " AND status IN ('running','idle','paused')"
	case !includeTerminated:
		q += " AND status NOT IN ('terminated','failed','crashed')"
	}
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		q += " AND project_id = ?"
		args = append(args, pid)
	}
	// Archived rows stay in the DB for audit/history resolution but drop
	// out of the default list. Pass ?include_archived=1 to see them
	// (mobile uses this when the operator taps "Show archived").
	inc := r.URL.Query().Get("include_archived")
	if inc != "1" && inc != "true" {
		q += " AND archived_at IS NULL"
	}
	q += " ORDER BY created_at"
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	out := []agentOut{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			s.writeDBErr(w, err)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	// Hydrate last_event_at from the event store (ADR-045 D2 cross-store read).
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	if lastEv, err := s.lastEventAtForAgents(r.Context(), ids); err == nil {
		for i := range out {
			if ts, ok := lastEv[out[i].ID]; ok {
				out[i].LastEventAt = &ts
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// lastEventAtForAgents returns agent_id -> MAX(agent_events.ts) for the given
// agents, read from the event store. Agents with no events are absent from the
// map (their last_event_at stays nil), matching the old correlated subquery's
// NULL. Chunked so a large team's id list can't exceed SQLite's bound-variable
// limit (ADR-045 D2 — the event store isn't per-team-sharded yet, P2).
func (s *Server) lastEventAtForAgents(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	er, err := s.eventsReaderForAgents(ctx, ids)
	if err != nil {
		return nil, err
	}
	const chunk = 900
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := er.QueryContext(ctx,
			`SELECT agent_id, MAX(ts) FROM agent_events
			  WHERE agent_id IN (`+ph+`) GROUP BY agent_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var aid string
			var ts sql.NullString
			if err := rows.Scan(&aid, &ts); err != nil {
				rows.Close()
				return nil, err
			}
			if ts.Valid {
				out[aid] = ts.String
			}
		}
		rows.Close()
	}
	return out, nil
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")
	row := s.db.QueryRowContext(r.Context(), `
		SELECT id, team_id, handle, kind, backend_json, capabilities_json,
		       COALESCE(parent_agent_id, ''), COALESCE(host_id, ''),
		       COALESCE(project_id, ''),
		       status, COALESCE(pane_id, ''),
		       COALESCE(worktree_path, ''), COALESCE(journal_path, ''),
		       budget_cents, spent_cents, pause_state, idle_since,
		       created_at, terminated_at, COALESCE(driving_mode, ''),
		       archived_at
		FROM agents WHERE team_id = ? AND id = ?`, team, id)
	a, err := scanAgent(row)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	// last_event_at from the event store (ADR-045 D2 cross-store read).
	if lastEv, lerr := s.lastEventAtForAgents(r.Context(), []string{a.ID}); lerr == nil {
		if ts, ok := lastEv[a.ID]; ok {
			a.LastEventAt = &ts
		}
	}
	// Pull the original spawn spec so the UI can show and reuse it. Agents
	// created outside the spawn flow (e.g. via handleCreateAgent) have no
	// row here — in that case we just leave the fields empty.
	var (
		specYaml  sql.NullString
		authority sql.NullString
	)
	if qerr := s.db.QueryRowContext(r.Context(), `
		SELECT spawn_spec_yaml, spawn_authority_json
		  FROM agent_spawns
		 WHERE child_agent_id = ?
		 ORDER BY spawned_at DESC LIMIT 1`, id).
		Scan(&specYaml, &authority); qerr == nil {
		if specYaml.Valid {
			a.SpawnSpecYaml = specYaml.String
		}
		if authority.Valid && authority.String != "" {
			a.SpawnAuthority = json.RawMessage(authority.String)
		}
	}
	writeJSON(w, http.StatusOK, a)
}

type agentPatchIn struct {
	Status     *string `json:"status,omitempty"`
	PauseState *string `json:"pause_state,omitempty"`
	PaneID     *string `json:"pane_id,omitempty"`
	// Handle renames the agent. Used by the multi-steward UX so the
	// principal can label stewards (research-steward, infra-steward,
	// …) without respawning. Server enforces the live-handle uniqueness
	// constraint via the existing `(team_id, handle, status='live')`
	// index; collisions surface as 409.
	Handle *string `json:"handle,omitempty"`
}

// handlePatchAgent lets host-runners / steward update lifecycle fields
// as the backing process moves through its states.
func (s *Server) handlePatchAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")
	var in agentPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	sets, args := []string{}, []any{}
	if in.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *in.Status)
		// Stamp terminated_at for any terminal state so the row's
		// lifecycle is self-describing without reading history.
		if *in.Status == "terminated" || *in.Status == "crashed" || *in.Status == "failed" {
			sets = append(sets, "terminated_at = ?")
			args = append(args, NowUTC())
		}
	}
	if in.PauseState != nil {
		sets = append(sets, "pause_state = ?")
		args = append(args, *in.PauseState)
	}
	if in.PaneID != nil {
		sets = append(sets, "pane_id = NULLIF(?, '')")
		args = append(args, *in.PaneID)
	}
	if in.Handle != nil {
		// Cheap shape-check before the SQL writes: an empty handle is
		// useless ("which agent?") and would conflict with NOT NULL.
		// Convention enforcement (must end in -steward for stewards,
		// etc.) is mobile-side; the server only owns uniqueness.
		if *in.Handle == "" {
			writeErr(w, http.StatusBadRequest, "handle may not be empty")
			return
		}
		sets = append(sets, "handle = ?")
		args = append(args, *in.Handle)
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no fields to update")
		return
	}
	args = append(args, team, id)
	q := "UPDATE agents SET " + joinComma(sets) + " WHERE team_id = ? AND id = ?"
	res, err := s.writeDB.ExecContext(r.Context(), q, args...)
	if err != nil {
		// SQLite raises constraint failures as a generic error string —
		// recognise the unique-handle case so the mobile UX can show
		// "handle already in use" instead of an opaque 500.
		msg := err.Error()
		if in.Handle != nil &&
			(strings.Contains(msg, "UNIQUE constraint failed") ||
				strings.Contains(msg, "constraint failed")) {
			writeErr(w, http.StatusConflict,
				"handle already in use by another live agent on this team")
			return
		}
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	// When an agent flips to a terminal status, the active session
	// pointing at it has no live process to talk to and must auto-pause
	// so the session list shows a Resume affordance instead of a dead
	// row that still claims to be active. ADR-009 D6 / the mobile Stop
	// session contract both depend on this.
	//
	// 'terminated' is the operator-driven stop — it goes through
	// stopSessionInternal so the audit trail matches the shutdown-all
	// fleet path (ADR-028 W2.5). 'crashed' / 'failed' are agent-side
	// outcomes (no host command, no agent.terminate audit) so the
	// session-paused + token-revoke logic stays inline.
	if in.Status != nil && *in.Status == "terminated" {
		// PATCH status=terminated is the resumable STOP (session → paused),
		// preserved for back-compat + the mobile path. The permanent
		// TERMINATE (session → archived) is the dedicated
		// POST /agents/{id}/terminate verb. See glossary "stop"/"terminate".
		s.applyAgentTerminationEffects(r.Context(), team, id, "agent stopped via PATCH", false)
	} else if in.Status != nil &&
		(*in.Status == "crashed" || *in.Status == "failed") {
		_, _ = s.writeDB.ExecContext(r.Context(), `
			UPDATE sessions
			   SET status = 'paused', last_active_at = ?
			 WHERE team_id = ?
			   AND current_agent_id = ?
			   AND status = 'active'`,
			NowUTC(), team, id)
		_, _ = auth.RevokeAgentTokens(r.Context(), s.writeDB, id, NowUTC())
		// Fold + stamp the run digest now (#118 §4). The operator stop path
		// finalizes via stopSessionInternal; a crash/failure flows through
		// here instead, so without this the first Insight open after the crash
		// pays the full O(n) backfill. finalizeDigestOutcome brings the digest
		// current off the read path.
		s.finalizeDigestOutcome(r.Context(), team, id)
	}
	// ADR-029 D-3: auto-derive the linked task's status from the
	// agent's terminal transition. Most-recent-spawn drives; older
	// spawns for the same task stay in the audit chain. Skips when
	// task.status='cancelled' (terminal override that auto-derive
	// must never overwrite). stopSessionInternal calls this too with
	// 'terminated'; the non-terminated path (crashed/failed) needs
	// the explicit call here so its task moves to blocked.
	if in.Status != nil && *in.Status != "terminated" {
		_ = s.deriveTaskStatusFromAgent(r.Context(), team, id, *in.Status)
	}
	w.WriteHeader(http.StatusNoContent)
}

// applyAgentTerminationEffects runs the side-effects of an agent
// reaching the 'terminated' status — the caller has already flipped
// agents.status. When a live session points at the agent it goes
// through stopSessionInternal so the audit trail matches the fleet
// shutdown-all path (ADR-028 W2.5). For a session-less agent (rare —
// typically already-archived) the legacy side-effects run inline:
// revoke the agent's tokens, enqueue the host terminate command, and
// write the agent.terminate audit row.
//
// Shared by handlePatchAgent (status→terminated) and the admin
// `agents kill` endpoint (ADR-028 plan W17) so both produce an
// identical lifecycle + audit trail. reason is threaded into the
// session-stop audit.
func (s *Server) applyAgentTerminationEffects(ctx context.Context, team, id, reason string, archive bool) {
	var sessionID string
	_ = s.db.QueryRowContext(ctx,
		`SELECT id FROM sessions
		  WHERE team_id = ? AND current_agent_id = ? AND status = 'active'
		  LIMIT 1`, team, id).Scan(&sessionID)
	if sessionID != "" {
		_, _ = s.stopSessionInternal(ctx, team, sessionID,
			StopSessionOpts{Reason: reason, Archive: archive})
		return
	}
	_, _ = auth.RevokeAgentTokens(ctx, s.writeDB, id, NowUTC())
	var hostID, paneID sql.NullString
	var handle string
	qerr := s.db.QueryRowContext(ctx,
		`SELECT host_id, pane_id, handle FROM agents WHERE team_id = ? AND id = ?`,
		team, id).Scan(&hostID, &paneID, &handle)
	if qerr == nil && hostID.Valid && hostID.String != "" {
		_, _ = s.enqueueHostCommand(ctx, hostID.String, id,
			"terminate", map[string]any{"pane_id": paneID.String})
	}
	s.recordAudit(ctx, team, "agent.terminate", "agent", id,
		"terminate "+handle, map[string]any{"handle": handle})
	// Seal the run digest for the session-less terminate too (#118 §4) — the
	// live-session branch above already finalizes via stopSessionInternal.
	s.finalizeDigestOutcome(ctx, team, id)
}

// handleStopAgent is POST /v1/teams/{team}/agents/{agent}/stop — the
// RESUMABLE kill. Kills the agent and flips its session to `paused`, so
// `agents.resume` can respawn it. The principal's "Stop session" does
// the same. See glossary "stop" (vs "terminate").
func (s *Server) handleStopAgent(w http.ResponseWriter, r *http.Request) {
	s.stopOrTerminateAgent(w, r, false)
}

// handleTerminateAgent is POST /v1/teams/{team}/agents/{agent}/terminate
// — the PERMANENT end. Kills the agent and ARCHIVES its session
// (fork-only, not resumable), the inverse of `stop`. See glossary
// "terminate".
func (s *Server) handleTerminateAgent(w http.ResponseWriter, r *http.Request) {
	s.stopOrTerminateAgent(w, r, true)
}

// stopOrTerminateAgent flips the agent terminal and applies the stop
// (archive=false → session paused, resumable) or terminate
// (archive=true → session archived, permanent) side effects.
func (s *Server) stopOrTerminateAgent(w http.ResponseWriter, r *http.Request, archive bool) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")

	var cur string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT status FROM agents WHERE team_id = ? AND id = ?`, team, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	// Flip to terminated unless already in a terminal agent state — the
	// session-fate side effect still runs so a re-issued verb is idempotent.
	if cur != "terminated" && cur != "crashed" && cur != "failed" {
		if _, err := s.writeDB.ExecContext(r.Context(),
			`UPDATE agents SET status = 'terminated', terminated_at = ?
			  WHERE team_id = ? AND id = ?`, NowUTC(), team, id); err != nil {
			s.writeDBErr(w, err)
			return
		}
	}
	reason := "agent stopped"
	if archive {
		reason = "agent terminated"
	}
	s.applyAgentTerminationEffects(r.Context(), team, id, reason, archive)
	w.WriteHeader(http.StatusNoContent)
}

// deriveTaskStatusFromAgent implements ADR-029 D-3 auto-derive. Called
// from any code path that flips an agent's status; safe to call with
// non-terminal statuses (returns nil without touching the task). Looks
// up the agent's most-recent spawn's task_id, and:
//
//   - 'terminated' AND result_summary populated  → task.status='in_review'
//     (W2: work is done *when reviewed*, not when the agent stops — the
//     human accepts → 'done' or sends back → 'in_progress'. Was 'done'.)
//   - 'terminated' AND result_summary empty      → task.status='cancelled'
//     (worker never called
//     tasks.complete — task
//     was abandoned, not
//     finished; recording it
//     as 'done' would be a
//     lie. v1.0.619 rule.)
//   - 'crashed' / 'failed'                       → task.status='blocked'
//   - other agent statuses                       → no-op
//
// Cancelled/blocked/in_review/done tasks are never overwritten (explicit
// operator override, worker's verdict, pending review, reviewer's accept).
// Audit row is written with source='spawn' per ADR-029 D-4 W4; the
// summary line names the trigger so feed readers can tell auto-derived
// 'cancelled' apart from explicit operator cancellation.
func (s *Server) deriveTaskStatusFromAgent(ctx context.Context, team, agentID, agentStatus string) error {
	var (
		taskID, curStatus string
		resultSummary     sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sp.task_id, ''), COALESCE(t.status, ''),
		       t.result_summary
		  FROM agent_spawns sp
		  LEFT JOIN tasks t ON t.id = sp.task_id
		 WHERE sp.child_agent_id = ?
		 ORDER BY sp.spawned_at DESC
		 LIMIT 1`, agentID).Scan(&taskID, &curStatus, &resultSummary)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if taskID == "" {
		return nil
	}
	// Cancelled is the explicit terminal override; never overwrite.
	// Blocked is the worker's explicit "I can't finish" declaration —
	// also never overwrite. v1.0.628: pre-bundle, manually stopping a
	// blocked worker for cleanup would flip the task to cancelled (no
	// summary) or done (summary present), erasing the worker's verdict
	// and posting a misleading "Task X cancelled" wake to the steward.
	// The operator's cleanup is cleanup; the worker's verdict is the
	// task outcome.
	// in_review joins the never-overwrite set (W2): once a worker has handed
	// off completed work for review, a *later* attempt that abandons (would
	// derive 'cancelled') or crashes ('blocked') must not silently erase the
	// pending-review verdict — only a human accept/send-back moves it out.
	// done is also never-overwrite (D-8): post-W2 it means a reviewer
	// ACCEPTED the work — an accept landing between the worker's hand-off
	// and its terminate event must not be demoted back to in_review by the
	// derive, nor flipped to blocked by a straggling crash report.
	if curStatus == "cancelled" || curStatus == "blocked" ||
		curStatus == "in_review" || curStatus == "done" {
		return nil
	}

	// Map agent status → task status. Terminated splits by
	// result_summary presence so abandoned tasks don't look completed.
	var newStatus string
	hasSummary := resultSummary.Valid && strings.TrimSpace(resultSummary.String) != ""
	switch agentStatus {
	case "terminated":
		if hasSummary {
			newStatus = "in_review"
		} else {
			newStatus = "cancelled"
		}
	case "crashed", "failed":
		newStatus = "blocked"
	default:
		return nil
	}

	// Idempotent: don't re-stamp the same status over an already-equal row.
	if curStatus == newStatus {
		return nil
	}
	now := NowUTC()
	// done / in_review / cancelled all stamp completed_at: the worker
	// finished its run (done/in_review = handed off; in_review records the
	// review hand-off time), and cancelled stamps so the Tasks tab reads
	// "cancelled <N>m ago" without falling back to updated_at (which moves on
	// every patch). blocked leaves completed_at untouched (work isn't done).
	if newStatus == "done" || newStatus == "in_review" || newStatus == "cancelled" {
		if _, err := s.writeDB.ExecContext(ctx, `
			UPDATE tasks
			   SET status = ?, completed_at = ?, updated_at = ?
			 WHERE id = ?`, newStatus, now, now, taskID); err != nil {
			return err
		}
	} else {
		if _, err := s.writeDB.ExecContext(ctx, `
			UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
			newStatus, now, taskID); err != nil {
			return err
		}
	}
	auditSummary := "auto-derive: " + curStatus + " → " + newStatus
	auditMeta := map[string]any{
		"from":   curStatus,
		"to":     newStatus,
		"source": "spawn",
		"agent":  agentID,
	}
	// Distinguish abandoned-task cancellation from operator cancellation
	// in the feed so the user can tell "I gave up on a stuck worker"
	// from "the worker reported its result." Activity readers and
	// future replay tools can grep on the abandoned flag.
	if newStatus == "cancelled" && agentStatus == "terminated" {
		auditSummary += " (no result_summary; worker abandoned)"
		auditMeta["abandoned"] = true
	}
	s.recordAudit(ctx, team, "task.status", "task", taskID,
		auditSummary, auditMeta)
	// W2.9: surface the auto-derived flip in the assigner's chat too.
	// The agent-terminated case is the canonical "worker finished and
	// the steward needs to know" path; without this, the steward only
	// sees the agent disappear from the live list with no narrative.
	s.notifyTaskAssigner(ctx, team, taskID, curStatus, newStatus)
	return nil
}

// handleArchiveAgent soft-deletes a terminated/failed/crashed agent so
// it drops off the live list. The row stays in the DB to keep audit
// events and spawn history resolvable. Refuses to archive a live agent —
// operators must terminate it first.
func (s *Server) handleArchiveAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")
	var status, handle string
	var archived sql.NullString
	err := s.db.QueryRowContext(r.Context(),
		`SELECT status, handle, archived_at FROM agents WHERE team_id = ? AND id = ?`,
		team, id).Scan(&status, &handle, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if archived.Valid {
		writeErr(w, http.StatusConflict, "already archived")
		return
	}
	if status != "terminated" && status != "failed" && status != "crashed" {
		writeErr(w, http.StatusConflict, "terminate the agent before archiving")
		return
	}
	now := NowUTC()
	if _, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE agents SET archived_at = ? WHERE team_id = ? AND id = ?`,
		now, team, id); err != nil {
		s.writeDBErr(w, err)
		return
	}
	s.recordAudit(r.Context(), team, "agent.archive", "agent", id,
		"archive "+handle,
		map[string]any{"handle": handle, "status": status},
	)
	w.WriteHeader(http.StatusNoContent)
}

type spawnIn struct {
	ParentID    string `json:"parent_agent_id,omitempty"`
	ChildHandle string `json:"child_handle"`
	Kind        string `json:"kind"`
	HostID      string `json:"host_id,omitempty"`
	// ProjectID binds the spawned agent to a project per ADR-025 W2.
	// Precedence: `project_id:` inside SpawnSpec YAML wins (the
	// canonical site every template + mobile sheet writes to). This
	// body field is a precedence-low fallback for callers that build
	// the YAML elsewhere and want to pass the binding out-of-band.
	ProjectID string          `json:"project_id,omitempty"`
	SpawnSpec string          `json:"spawn_spec_yaml"`
	Authority json.RawMessage `json:"spawn_authority,omitempty"`
	// LegacyTaskJSON is the pre-ADR-029 orchestrator-worker handoff blob
	// that lands in `agent_spawns.task_json`. No caller populates it today
	// (mcp_orchestrate uses its own DoSpawn shape). Kept on the struct so
	// the existing column write stays safe; the wire name is renamed so
	// the post-ADR-029 `task` JSON field can carry the inline-task
	// semantics below.
	LegacyTaskJSON json.RawMessage `json:"legacy_task_json,omitempty"`
	// TaskID links this spawn to an existing tasks row (ADR-029 D-2).
	// Mutually exclusive with Task below. The hub validates the task
	// belongs to the same project as the spawn (400 on mismatch) and that
	// the task is not in a terminal status (409 with hint to update
	// status='in_progress' first; `blocked` is exempt — a fresh spawn is
	// the canonical unblock path).
	TaskID string `json:"task_id,omitempty"`
	// Task, when set, asks the hub to materialize a fresh tasks row in
	// the same transaction as the spawn (ADR-029 D-2). Assignee is the
	// new agent; created_by_id is parent_agent_id (NULL when the caller
	// is principal-direct — i.e., no parent_agent_id on the request).
	// Mutually exclusive with TaskID.
	Task         *spawnTaskInline `json:"task,omitempty"`
	WorktreePath string           `json:"worktree_path,omitempty"`
	BudgetCents  *int             `json:"budget_cents,omitempty"`
	// Mode is an optional override of the template's driving_mode.
	// When set it's strict — the resolver tries only this candidate,
	// no fallback. Empty means "use template + fallbacks".
	Mode string `json:"mode,omitempty"`
	// PersonaSeed is a free-form addendum the user types into the
	// mobile bootstrap sheet. The hub appends it to the rendered
	// CLAUDE.md as a "Persona override" section so the agent sees
	// both the template body and the user's customization on first
	// turn. Empty means no override.
	PersonaSeed string `json:"persona_seed,omitempty"`
	// PermissionMode controls how claude (or any backend that templates
	// against {{permission_flag}}) handles tool-call approval. Recognised
	// values:
	//   - "skip"   → expand to `--dangerously-skip-permissions` (auto-allow,
	//                matches a local `claude` session on a PC).
	//   - "prompt" → expand to `--permission-prompt-tool mcp__termipod__
	//                permission_prompt` (route every tool call through the
	//                hub MCP gateway → attention_items). Only useful once
	//                that MCP tool is registered.
	//   - ""       → backendVarsFromSpec rewrites to "skip". The earlier
	//                "empty expansion, claude defaults" behaviour broke any
	//                caller that forgot to pass the field: claude in
	//                stream-json --print mode with no permission flag
	//                denies destructive tools (Write/Edit/Bash) and the
	//                worker stalls with no attention_item to surface.
	//                v1.0.617 made "" ≡ "skip" so the demo flow works
	//                whether the caller is explicit or not.
	// The mobile bootstrap sheet, general-steward bootstrap, and
	// project-steward delegation all set "skip" explicitly; the MCP
	// `agents.spawn` schema exposes the field so stewards can override
	// to "prompt" when they want the per-tool attention gate.
	PermissionMode string `json:"permission_mode,omitempty"`
	// SessionID, when set, attaches this spawn to an existing session
	// rather than creating a free-standing agent. Used by the
	// "switch engine / upgrade model" recreate flow: the prior
	// agent on the session is terminated, the new spawn happens with
	// the operator's chosen engine/model, and the session is
	// rewritten to point at the new agent_id with the new
	// spawn_spec_yaml — all in the same transaction. Transcript
	// (queried by session_id) carries forward; the session's
	// closed_at stays NULL throughout.
	SessionID string `json:"session_id,omitempty"`
	// AutoOpenSession asks DoSpawn to open a fresh session pointing at
	// the new agent inside the same transaction, when SessionID is
	// empty. Used by the "spawn new steward" flow so the resulting
	// steward never exists agent-without-session — the session is the
	// thing the principal talks to, an instance without one is just a
	// template definition. Ignored when SessionID is set (the swap
	// path already updates the named session in-tx).
	AutoOpenSession bool `json:"auto_open_session,omitempty"`
	// SuppressAutoSession turns OFF the otherwise-forced project auto-open
	// (ADR-025 D5: project spawns always get a session). Set ONLY by the
	// resume path, which lands the new agent in a pre-existing paused session
	// it stamps itself afterwards — without this, threading the resumed
	// agent's project_id would auto-open a SECOND session that collides on the
	// (team_id, worktree_path) uniqueness index.
	SuppressAutoSession bool `json:"-"`
	// Wait, when true (the default for the agents.spawn MCP path),
	// asks handleSpawn to block until the new agent reaches `running`
	// or `failed`, bounded by WaitSeconds. The response Status field
	// then carries one of {running, failed, pending} reflecting the
	// real engine state — not the pre-bundle misleading "spawned"
	// label that fired the moment the spawn row was inserted. W9 of
	// docs/plans/spawn-robustness-and-validators.md.
	//
	// Wait is a *pointer* so the absence of the field on the wire
	// distinguishes "explicit false" from "default behavior." The
	// MCP path treats nil + true identically (sync wait); the legacy
	// REST path keeps the prior async behavior unless the caller
	// explicitly opts in.
	Wait *bool `json:"wait,omitempty"`
	// WaitSeconds caps the wait window. Default 30, hard-capped at
	// 50 by handleSpawn to stay below Claude Code's 60-second MCP
	// tool timeout (MCP_TOOL_TIMEOUT) with margin for transport
	// latency. Values >50 silently cap; values <=0 use the default.
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

// buildTaskInstructions returns the "## Task" body that should land in
// the worker's CLAUDE.md when the spawn carries an ADR-029 task linkage.
// Without this the body_md the steward typed into `task: {…}` would never
// reach the worker — the steward would have to follow up with an
// `a2a.invoke` text="do this" call, defeating the point of the inline-
// create shape (D-2 A3). The returned string is empty when neither
// linkage shape is present or when the linked task has no body. The
// `task_id` branch reads from the existing tasks row; the inline branch
// reads from the spawnTaskInline carried on the request. Failures (DB
// hiccup, deleted task) silently degrade to an empty string — the spawn
// still succeeds, the worker just lacks the instructions, and the
// principal sees the gap on the Tasks tab.
//
// projectID + inlineTaskID feed the close-out protocol footer rendered
// by renderTaskInstructions: workers need the literal IDs to call
// tasks.complete / tasks.update without first running tasks.list. The
// caller pre-mints inlineTaskID for the inline branch (so the same ID
// is reused when the in-tx INSERT runs); for the task_id branch the
// caller passes in.TaskID.
func buildTaskInstructions(ctx context.Context, db *sql.DB, in spawnIn, projectID, inlineTaskID string) string {
	if in.Task != nil {
		return renderTaskInstructions(in.Task.Title, in.Task.BodyMD, projectID, inlineTaskID)
	}
	if in.TaskID != "" && db != nil {
		var title, body sql.NullString
		err := db.QueryRowContext(ctx,
			`SELECT title, COALESCE(body_md, '') FROM tasks WHERE id = ?`,
			in.TaskID).Scan(&title, &body)
		if err != nil {
			return ""
		}
		return renderTaskInstructions(title.String, body.String, projectID, in.TaskID)
	}
	return ""
}

// renderTaskInstructions formats title + body_md for the CLAUDE.md
// "## Task" section. Title becomes the first H1; body_md follows as
// the rest. If only title is set, the body is the title line alone.
// If neither is set returns "" so the header is omitted.
//
// When projectID + taskID are both non-empty, a "Task close-out
// protocol" footer is appended carrying the literal IDs the worker
// needs to call tasks.complete / tasks.update. The footer's
// "protocol-not-domain" framing is load-bearing: it overrides any
// `TOOLS:` / `BOUNDARIES:` lines a steward might write into body_md
// that would otherwise forbid the close-out call (the bug that
// motivated W2.6.1). With empty IDs the footer is omitted so legacy
// call sites that pass only title+body still render cleanly.
func renderTaskInstructions(title, body, projectID, taskID string) string {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" && body == "" {
		return ""
	}
	var out strings.Builder
	if title != "" {
		out.WriteString("# ")
		out.WriteString(title)
		out.WriteString("\n")
	}
	if body != "" {
		if title != "" {
			out.WriteString("\n")
		}
		out.WriteString(body)
		out.WriteString("\n")
	}
	if projectID != "" && taskID != "" {
		out.WriteString("\n---\n\n")
		out.WriteString("### Task close-out protocol (system-rendered)\n\n")
		out.WriteString("When you finish your assigned task, close it out by calling:\n\n")
		out.WriteString("```\n")
		out.WriteString("tasks_complete(\n")
		out.WriteString("  project_id=\"" + projectID + "\",\n")
		out.WriteString("  task=\"" + taskID + "\",\n")
		out.WriteString("  summary=\"<one-line summary of what you produced>\",\n")
		out.WriteString(")\n")
		out.WriteString("```\n\n")
		out.WriteString("If you cannot complete the task, call instead:\n\n")
		out.WriteString("```\n")
		out.WriteString("tasks_update(\n")
		out.WriteString("  project_id=\"" + projectID + "\",\n")
		out.WriteString("  task=\"" + taskID + "\",\n")
		out.WriteString("  status=\"blocked\",\n")
		out.WriteString("  block_reason=\"<short note on why you are stuck>\",\n")
		out.WriteString(")\n")
		out.WriteString("```\n\n")
		out.WriteString("`tasks_complete` and `tasks_update` are orchestration protocol, not\n")
		out.WriteString("domain tools — any `TOOLS:` / `BOUNDARIES:` restrictions written into\n")
		out.WriteString("the task body above do NOT apply to them. The hub hands the task off\n")
		out.WriteString("to review (`in_review`), stamps `result_summary`, and pushes a\n")
		out.WriteString("`task.notify` event into your assigner's session automatically when\n")
		out.WriteString("you call `tasks_complete`; your reviewer accepts it to `done`.\n")
	}
	return out.String()
}

// spawnTaskInline is the fields accepted on `agents.spawn`'s `task` body
// field. Mirrors taskIn minus the assignee/created_by axes — those are
// derived from the spawn itself (assignee=new agent, created_by=parent
// agent). Status defaults to `in_progress` (the spawn is starting the
// work) and priority defaults to `med` if absent, matching taskIn's
// HTTP create path. ADR-029 D-2.
type spawnTaskInline struct {
	Title        string `json:"title"`
	BodyMD       string `json:"body_md,omitempty"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	MilestoneID  string `json:"milestone_id,omitempty"`
	Priority     string `json:"priority,omitempty"`
}

type spawnOut struct {
	SpawnID   string `json:"spawn_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	SpawnedAt string `json:"spawned_at,omitempty"`
	// Status reflects the agent's real lifecycle state at response
	// time. Post-W9 (v1.0.620+) one of:
	//   - "running"          — engine confirmed alive within the wait window
	//   - "failed"           — hostrunner refused or engine crashed; FailureReason populated
	//   - "pending"          — wait window expired without state transition; caller polls agents.get
	//   - "spawned"          — legacy async return (caller passed wait=false or via REST without wait)
	//   - "pending_approval" — gated by spawn-approval policy; AttentionID populated
	Status      string `json:"status,omitempty"`
	AttentionID string `json:"attention_id,omitempty"` // set when gated on approval
	Tier        string `json:"tier,omitempty"`
	// Mode is the concrete driving mode the resolver picked (M1|M2|M4).
	// Empty when no mode info was declared anywhere — host-runner stays
	// on its default M4 in that case.
	Mode string `json:"mode,omitempty"`
	// FailureReason carries the lifecycle.failed payload.reason when
	// Status == "failed". Empty otherwise. W9 surfaces this so the
	// steward calling agents.spawn knows WHY the spawn failed without
	// a follow-up agents.get round-trip.
	FailureReason string `json:"failure_reason,omitempty"`
}

// normalizeAgentHandle strips a single leading `@` from a handle so
// the stored form is bare. The `@` is the display sigil
// (`docs/reference/glossary.md` → handle), never part of the name.
// Pre-fix templates passed `child_handle="@coder"` literally, which
// stored `@coder`, which downstream `@{{parent.handle}}` rendering
// then double-prefixed to `@@coder` — the failure mode that
// motivated this helper. Idempotent: bare handles pass through
// unchanged; only one prefix is stripped so a hypothetical
// double-prefixed input still ends in a well-formed name.
func normalizeAgentHandle(h string) string {
	h = strings.TrimSpace(h)
	return strings.TrimPrefix(h, "@")
}

// checkSpawnHostReachable verifies the named host exists in the team
// and is online before the spawn is committed. Without this check a
// caller can pass a stale, offline, or junk host_id and the resulting
// agents row carries a host_id that no host-runner ever polls for,
// leaving the agent stuck in pending forever (the same end state the
// missing-host_id case produced). Returns a non-nil error with a
// human-readable reason on rejection; nil on success.
func (s *Server) checkSpawnHostReachable(ctx context.Context, team, hostID string) error {
	var status string
	err := s.db.QueryRowContext(ctx,
		`SELECT status FROM hosts WHERE team_id = ? AND id = ?`,
		team, hostID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("host_id " + hostID + " is not registered for this team")
	}
	if err != nil {
		return errors.New("host_id check failed: " + err.Error())
	}
	if status != "online" {
		return errors.New("host_id " + hostID + " is not online (status=" + status + ")")
	}
	return nil
}

// handleSpawn creates a pending agent + an agent_spawns audit row, unless
// policy gates the action behind an approval_request attention. In the
// gated case we return 202 + attention_id and the actual spawn happens on
// approve.
func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwnerOrSteward(w, r) { // #75
		return
	}
	team := chi.URLParam(r, "team")
	var in spawnIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// host_id boundary check (matches the MCP schema's required[] now
	// that the dispatcher-side validator enforces it). REST callers
	// (mobile bootstrap sheet, internal handlers) pass through this
	// gate too — a spawn with no host_id lands as a row with host_id=
	// NULL that no runner ever claims (host-runner polls WHERE host_id
	// = self), so the agent sits "pending" forever. Fail fast with a
	// pointer at hosts.list so callers can resolve the id and retry.
	// DoSpawn (the internal direct-call path used by tests + internal
	// hub code) is intentionally NOT gated here — that path is for
	// callers who have already established the host themselves.
	if strings.TrimSpace(in.HostID) == "" {
		writeErrHint(w, http.StatusUnprocessableEntity,
			"host_id is required; resolve via hosts_list",
			Hint{
				HintText: "Call hosts_list to discover online host_ids, then retry agents_spawn with the chosen id.",
				SeeTool:  "hosts_list",
			})
		return
	}
	// Confirm the named host exists in this team and is reachable
	// before we burn the spawn row. Offline / stale / unknown hosts
	// would all leave the agent stuck in pending the same way as a
	// missing id; surface it at the API boundary with the same hint.
	if jerr := s.checkSpawnHostReachable(r.Context(), team, in.HostID); jerr != nil {
		writeErrHint(w, http.StatusUnprocessableEntity, jerr.Error(),
			Hint{
				HintText: "Call hosts_list to discover online host_ids, then retry agents_spawn with one of them.",
				SeeTool:  "hosts_list",
			})
		return
	}
	if s.policy != nil {
		tier := s.policy.Decide("spawn")
		if tier != "" && tier != TierAuto {
			approvers := s.policy.ApproversFor(tier)
			if len(approvers) > 0 {
				attID, err := s.createSpawnApproval(r.Context(), team, tier, approvers, in)
				if err != nil {
					s.writeDBErr(w, err)
					return
				}
				writeJSON(w, http.StatusAccepted, spawnOut{
					Status:      "pending_approval",
					AttentionID: attID,
					Tier:        tier,
				})
				return
			}
		}
	}
	out, status, err := s.DoSpawn(r.Context(), team, in)
	if err != nil {
		// ADR-035 W2: a mode-floor rejection carries a structured Hint
		// (which modes the engine supports) so the agent can retry.
		var me *ModeUnsupportedError
		if errors.As(err, &me) {
			writeErrHint(w, status, err.Error(), me.Hint())
			return
		}
		writeErr(w, status, err.Error())
		return
	}
	out.Status = "spawned"
	s.recordAudit(r.Context(), team, "agent.spawn", "agent", out.AgentID,
		"spawn "+in.ChildHandle+" ("+in.Kind+")",
		map[string]any{"handle": in.ChildHandle, "kind": in.Kind, "host_id": in.HostID},
	)
	// W9: sync-wait three-state return. When the caller opts in
	// (in.Wait == true, which is the default for the agents.spawn
	// MCP path — wrapper sets it explicitly), tail the new agent's
	// event bus for lifecycle.started → "running" or lifecycle.failed
	// → "failed". Timeout → "pending"; caller polls agents.get to
	// learn final state. See docs/discussions/validate-at-every-boundary.md
	// §1 for the misleading "spawned" return that motivated this.
	if in.Wait != nil && *in.Wait {
		final, reason := s.waitForSpawnOutcome(r.Context(), out.AgentID, in.WaitSeconds)
		out.Status = final
		if reason != "" {
			out.FailureReason = reason
		}
	}
	writeJSON(w, status, out)
}

// waitForSpawnOutcome blocks until the agent emits a terminal
// lifecycle event (`phase: "started"` → "running", `phase: "failed"`
// → "failed") or the wait window expires (→ "pending"). The window
// defaults to 30s when waitSeconds <= 0 and is hard-capped at 50s to
// stay below Claude Code's 60s MCP_TOOL_TIMEOUT with margin for
// transport latency.
//
// reason is non-empty only on the "failed" path; the value is the
// `payload.reason` field of the observed lifecycle.failed event when
// present, else a generic "agent reached terminal status" string.
func (s *Server) waitForSpawnOutcome(parent context.Context, agentID string, waitSeconds int) (status, reason string) {
	if waitSeconds <= 0 {
		waitSeconds = 30
	}
	if waitSeconds > 50 {
		waitSeconds = 50
	}
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()

	// Subscribe BEFORE checking current status: prevents missing a
	// lifecycle event that races the subscribe call.
	key := agentBusKey(agentID)
	sub := s.bus.Subscribe(key)
	defer s.bus.Unsubscribe(key, sub)

	// Catch the case where the agent already failed before subscribe
	// (W7 hostrunner refusal fires synchronously from launchOne).
	if cur := s.lookupAgentStatus(ctx, agentID); cur == "failed" {
		return "failed", s.lookupRecentLifecycleReason(ctx, agentID)
	}
	if cur := s.lookupAgentStatus(ctx, agentID); cur == "running" {
		return "running", ""
	}

	for {
		select {
		case <-ctx.Done():
			return "pending", ""
		case evt, ok := <-sub:
			if !ok {
				return "pending", ""
			}
			kind, _ := evt["kind"].(string)
			if kind != "lifecycle" {
				continue
			}
			payload, _ := evt["payload"].(map[string]any)
			// Some publishers wrap payload as json.RawMessage; decode if so.
			if payload == nil {
				if raw, ok := evt["payload"].(json.RawMessage); ok {
					_ = json.Unmarshal(raw, &payload)
				}
			}
			phase, _ := payload["phase"].(string)
			switch phase {
			case "started":
				return "running", ""
			case "failed":
				r, _ := payload["reason"].(string)
				if r == "" {
					r = "agent reached terminal status"
				}
				return "failed", r
			}
		}
	}
}

// lookupAgentStatus reads agents.status for the given agent.
// Returns "" on any error so callers fall through to the bus-wait
// path rather than failing the spawn response on a DB hiccup.
func (s *Server) lookupAgentStatus(ctx context.Context, agentID string) string {
	var st string
	if err := s.db.QueryRowContext(ctx,
		`SELECT status FROM agents WHERE id = ?`, agentID).Scan(&st); err != nil {
		return ""
	}
	return st
}

// lookupRecentLifecycleReason fetches payload.reason from the most
// recent lifecycle.failed agent_event for the given agent. Returns
// "" when no such event exists or on any decode error.
func (s *Server) lookupRecentLifecycleReason(ctx context.Context, agentID string) string {
	er, rerr := s.eventsReaderForAgent(ctx, agentID)
	if rerr != nil {
		return ""
	}
	var payload string
	err := er.QueryRowContext(ctx,
		`SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'lifecycle'
		 ORDER BY seq DESC LIMIT 1`, agentID).Scan(&payload)
	if err != nil {
		return ""
	}
	var p struct {
		Phase  string `json:"phase"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return ""
	}
	if p.Phase != "failed" {
		return ""
	}
	return p.Reason
}

// createSpawnApproval inserts an attention_items row tagged with tier and a
// pending_payload_json holding the spawnIn, ready for the approval-decide
// handler to dispatch once quorum is reached.
func (s *Server) createSpawnApproval(ctx context.Context, team, tier string, approvers []string, in spawnIn) (string, error) {
	id := NewID()
	payload, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	assignees, _ := json.Marshal(approvers)
	severity := "major"
	if tier == TierCritical {
		severity = "critical"
	}
	summary := "spawn " + in.ChildHandle + " (" + in.Kind + ")"
	_, actorKind, actorHandle := actorFromContext(ctx)
	_, err = s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, tier,
			current_assignees_json, pending_payload_json,
			status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULL, 'team', ?, 'approval_request',
		          ?, ?, ?,
		          ?, ?,
		          'open', ?,
		          ?, ?)`,
		id, team, summary, severity, tier,
		string(assignees), string(payload), NowUTC(),
		actorKind, nullIfEmpty(actorHandle))
	return id, err
}

// DoSpawn is the reusable core called by both the HTTP handler and the
// scheduler. It creates the agent row + audit row transactionally.
// Returns (out, httpStatus, err); on err the httpStatus is the suggested
// status for HTTP callers, ignored by internal callers.
func (s *Server) DoSpawn(ctx context.Context, team string, in spawnIn) (spawnOut, int, error) {
	if in.ChildHandle == "" || in.Kind == "" || in.SpawnSpec == "" {
		return spawnOut{}, http.StatusBadRequest,
			errors.New("child_handle, kind, spawn_spec_yaml required")
	}
	// Bare-handle convention (post-v1.0.636): the `@` is a display
	// sigil, not part of the stored name. We strip a single leading
	// `@` so bundled templates that historically passed
	// `child_handle="@coder"` continue to work, but the column always
	// carries the bare form going forward. See migration 0044 +
	// glossary entry.
	in.ChildHandle = normalizeAgentHandle(in.ChildHandle)

	// Expand template vars ({{handle}}, {{journal}}, {{principal}}, …) before
	// persisting. Render failures are treated as client errors — a malformed
	// placeholder means the spec can never spawn usefully, better to reject.
	principal := "@principal"
	if tok, ok := auth.FromContext(ctx); ok {
		principal = principalFromScope(tok.ScopeJSON)
	}
	rendered, err := s.renderSpawnSpec(ctx, team, in, principal)
	if err != nil {
		return spawnOut{}, http.StatusBadRequest, err
	}
	// W4 fail-fast: reject if the rendered spec has no backend.cmd.
	// Before this gate the spec would reach host-runner with an empty
	// Backend.Cmd, M4 LocalLogTail would hard-fail, PaneDriver fallback
	// would land on the launcher placeholder (interactive bash), and
	// the rendered task prompt would be keystroke-pumped into a shell
	// → "tasks.complete: command not found" + respawn loop. See
	// docs/discussions/validate-at-every-boundary.md §1 for the
	// incident; docs/plans/spawn-robustness-and-validators.md W4 for
	// the wedge.
	if cmd := parsedBackendCmd(rendered); cmd == "" {
		return spawnOut{}, http.StatusUnprocessableEntity,
			errors.New("rendered spawn_spec_yaml has no backend.cmd; " +
				"spec must declare `backend.cmd` directly or reference a " +
				"template with backend.cmd (e.g. `template: agents.coder`). " +
				"Call tools_get('agents_spawn') for the full input shape.")
	}
	// Inline the agent's CLAUDE.md (resolved from the template's `prompt:`
	// field). The host-runner launcher writes context_files entries into
	// the workdir before spawn so Claude Code sees the persona on
	// startup. Failures here are a config bug — a template that names a
	// missing prompt should not silently spawn a contextless agent.
	vars, err := s.buildSpawnVars(ctx, team, in, principal)
	if err != nil {
		return spawnOut{}, http.StatusBadRequest, err
	}
	// Resolve project_id per ADR-025 W2: YAML wins (canonical site
	// every template + mobile sheet writes to), body field is the
	// precedence-low fallback for callers that don't render YAML.
	// Resolved BEFORE buildTaskInstructions so the close-out protocol
	// footer can carry the literal project_id the worker needs to
	// call tasks.complete.
	projectID := in.ProjectID
	if y := parseSpawnModeYAML(rendered); y.ProjectID != "" {
		projectID = y.ProjectID
	}

	// Pre-mint the inline task ID so the same ID is both rendered into
	// the CLAUDE.md close-out footer AND used by the in-tx INSERT below.
	// For the task_id linkage branch the caller already supplied the ID;
	// buildTaskInstructions reads it from in.TaskID directly.
	inlineTaskID := ""
	if in.Task != nil {
		inlineTaskID = NewID()
	}

	taskInstructions := buildTaskInstructions(ctx, s.db, in, projectID, inlineTaskID)
	rendered, err = s.resolveContextFiles(team, rendered, vars, in.PersonaSeed, taskInstructions)
	if err != nil {
		return spawnOut{}, http.StatusBadRequest, err
	}
	in.SpawnSpec = rendered

	// Resolve the driving mode before we open the tx so a 400 exits
	// without a rollback. Empty mode is legal (opt-in): DoSpawn stores
	// NULL and host-runner defaults to M4 at launch time.
	mode, err := s.resolveSpawnMode(ctx, in)
	if err != nil {
		// ADR-035 W2: a family-level mode-floor rejection is a 422
		// (the request is well-formed but asks for a mode the engine
		// can't speak); handleSpawn surfaces its Hint. Other resolution
		// failures stay 400.
		var me *ModeUnsupportedError
		if errors.As(err, &me) {
			return spawnOut{}, http.StatusUnprocessableEntity, err
		}
		return spawnOut{}, http.StatusBadRequest, err
	}

	// ADR-029 D-2: validate the task linkage before opening the tx so
	// 4xx exits cleanly. Mutual exclusion (TaskID vs inline Task) is
	// 400; spawn against a terminal task (done/cancelled) is 409 with
	// a hint to flip status='in_progress' first (blocked is exempt —
	// a fresh spawn is the canonical unblock path).
	if in.TaskID != "" && in.Task != nil {
		return spawnOut{}, http.StatusBadRequest,
			errors.New("task_id and task are mutually exclusive")
	}
	if (in.TaskID != "" || in.Task != nil) && projectID == "" {
		return spawnOut{}, http.StatusBadRequest,
			errors.New("project_id required when linking a task")
	}
	if in.Task != nil && strings.TrimSpace(in.Task.Title) == "" {
		return spawnOut{}, http.StatusBadRequest,
			errors.New("task.title required")
	}
	if in.Task != nil && in.Task.Priority != "" && !taskPriorities[in.Task.Priority] {
		return spawnOut{}, http.StatusBadRequest,
			errors.New("task.priority must be one of low|med|high|urgent")
	}
	var linkedTaskExistingStatus string
	if in.TaskID != "" {
		var taskProj, taskStatus string
		err := s.db.QueryRowContext(ctx, `
			SELECT project_id, status FROM tasks WHERE id = ?`,
			in.TaskID).Scan(&taskProj, &taskStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return spawnOut{}, http.StatusNotFound,
				errors.New("task_id not found")
		}
		if err != nil {
			return spawnOut{}, http.StatusInternalServerError, err
		}
		if taskProj != projectID {
			return spawnOut{}, http.StatusBadRequest,
				errors.New("task_id project mismatch with spawn")
		}
		if taskStatus == "done" || taskStatus == "cancelled" {
			return spawnOut{}, http.StatusConflict,
				errors.New("task is " + taskStatus + "; call tasks_update status='in_progress' first to reopen")
		}
		linkedTaskExistingStatus = taskStatus
	}
	if in.Task != nil {
		if err := s.validateProjectInTeam(ctx, team, projectID); err != nil {
			return spawnOut{}, http.StatusBadRequest, err
		}
	}

	// Validate the session-swap path before opening the tx so a 4xx
	// exits without a rollback. SessionID is the W2 follow-up that
	// lets "Recreate steward" or "Switch engine" land the new agent
	// inside the existing session, transcript intact, instead of
	// orphaning the prior session and minting a fresh one.
	var (
		swapSessionID string
		priorAgentID  string
	)
	if in.SessionID != "" {
		var status, current sql.NullString
		err := s.db.QueryRowContext(ctx, `
			SELECT status, current_agent_id
			  FROM sessions WHERE team_id = ? AND id = ?`,
			team, in.SessionID).Scan(&status, &current)
		if errors.Is(err, sql.ErrNoRows) {
			return spawnOut{}, http.StatusNotFound,
				errors.New("session not found")
		}
		if err != nil {
			return spawnOut{}, http.StatusInternalServerError, err
		}
		if status.String == "deleted" {
			return spawnOut{}, http.StatusConflict,
				errors.New("session is deleted; cannot swap")
		}
		swapSessionID = in.SessionID
		if current.Valid {
			priorAgentID = current.String
		}
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}
	defer tx.Rollback()

	agentID := NewID()
	spawnID := NewID()
	now := NowUTC()

	// On session-swap, terminate the prior agent inside the tx so the
	// (team_id, handle) live-handle uniqueness index frees up before
	// the new INSERT below. Without this, a swap with the same handle
	// (typical for steward) would 409 even though the user's intent
	// is exactly "replace the live one".
	if swapSessionID != "" && priorAgentID != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agents
			   SET status = 'terminated', terminated_at = ?
			 WHERE team_id = ? AND id = ?
			   AND status NOT IN ('terminated','failed','crashed')`,
			now, team, priorAgentID); err != nil {
			return spawnOut{}, http.StatusInternalServerError, err
		}
		// Revoke the prior agent's MCP bearer in the same tx so a
		// rolled-back swap also rolls back the revoke (no orphaned
		// "revoked but agent still alive" rows).
		if _, err := auth.RevokeAgentTokens(ctx, tx, priorAgentID, now); err != nil {
			return spawnOut{}, http.StatusInternalServerError, err
		}
	}

	// Persist the engine family in backend_json so mobile can resolve the
	// engine (agent['backend']['kind']) for the agent sheet (#67) and the
	// compose-snippet profile (#68). Without this the column stays '{}' and
	// the Flutter side falls back to the template name. Prefer the rendered
	// spec's backend.kind — the real family for template/steward spawns
	// where in.Kind carries a template id — and fall back to in.Kind for
	// mobile direct-engine spawns that pass the family there.
	backendFamily := backendKindFromSpec(in.SpawnSpec)
	if backendFamily == "" {
		backendFamily = in.Kind
	}
	backendJSON := "{}"
	if backendFamily != "" {
		b, _ := json.Marshal(map[string]string{"kind": backendFamily})
		backendJSON = string(b)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agents (
			id, team_id, handle, kind, backend_json, capabilities_json,
			parent_agent_id, host_id, budget_cents, worktree_path,
			driving_mode, project_id,
			status, pause_state, created_at
		) VALUES (?, ?, ?, ?, ?, '[]',
		          NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''),
		          NULLIF(?, ''), NULLIF(?, ''),
		          'pending', 'running', ?)`,
		agentID, team, in.ChildHandle, in.Kind, backendJSON,
		in.ParentID, in.HostID, nullableInt(in.BudgetCents),
		in.WorktreePath, mode, projectID, now); err != nil {
		return spawnOut{}, http.StatusConflict, err
	}

	// Mint the agent's MCP bearer token. scope_json carries team + agent_id
	// so the existing /mcp/{token} resolver (resolveMCPToken) routes the
	// session to this agent without further lookup. Plaintext is stashed
	// on agent_spawns; host-runner reads it once at launch to materialize
	// .mcp.json and never asks again. auth_tokens stores the hash only,
	// so the plaintext is unrecoverable via that table alone. We insert
	// inside the spawn tx so a rollback also unwinds the token (no orphan
	// rows if e.g. agent_spawns INSERT fails).
	mcpTokenPlaintext := auth.NewToken()
	mcpTokenID := NewID()
	// Stamp role per ADR-016 — derive from agent_kind AND
	// spawn_spec_yaml via the active operation-scope manifest. Two
	// roles in MVP: steward / worker. The middleware in dispatchTool
	// reads scope.Role first; legacy tokens (role="agent") still work
	// via the resolveAgentRole fallback that re-derives from the
	// agents row, but new tokens land with the explicit role here.
	//
	// **RoleForSpec, not RoleFor.** Mobile historically conflates
	// engine kind (claude-code) with persona kind (steward.*.v1),
	// sending the engine name as `kind` for stewards — which made
	// every steward spawned from mobile land with role=worker and
	// hit "tool not permitted for role: worker" the moment it tried
	// to spawn a child agent. RoleForSpec consults the
	// `default_role:` line from spawn_spec_yaml as a fallback so
	// `default_role: team.*` (the canonical steward declaration in
	// every steward template) escalates the role correctly.
	role := "worker"
	if r := activeRoles(); r != nil {
		role = r.RoleForSpec(in.Kind, in.SpawnSpec)
	}
	mcpScopeJSON, _ := json.Marshal(map[string]any{
		"team":     team,
		"role":     role,
		"agent_id": agentID,
		"handle":   in.ChildHandle,
	})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO auth_tokens (id, kind, token_hash, scope_json, created_at)
		VALUES (?, 'agent', ?, ?, ?)`,
		mcpTokenID, auth.HashToken(mcpTokenPlaintext),
		string(mcpScopeJSON), now); err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}

	// ADR-029 D-2 + D-3: materialize or flip the linked task in-tx so
	// the spawn either lands a (task, agent, spawn) triad or rolls back
	// all three. Mobile sees a consistent Tasks tab from the very first
	// poll after the spawn returns.
	linkedTaskID := in.TaskID
	if in.Task != nil {
		// Reuse the ID pre-minted above so the same value lands in both
		// the CLAUDE.md close-out footer and the tasks row.
		linkedTaskID = inlineTaskID
		priority := in.Task.Priority
		if priority == "" {
			priority = "med"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (id, project_id, parent_task_id, title, body_md,
			                   status, priority, assignee_id, created_by_id,
			                   milestone_id, started_at, created_at, updated_at)
			VALUES (?, ?, NULLIF(?, ''), ?, ?,
			        'in_progress', ?, ?, NULLIF(?, ''),
			        NULLIF(?, ''), ?, ?, ?)`,
			linkedTaskID, projectID, in.Task.ParentTaskID, in.Task.Title, in.Task.BodyMD,
			priority, agentID, in.ParentID,
			in.Task.MilestoneID, now, now, now,
		); err != nil {
			return spawnOut{}, http.StatusInternalServerError, err
		}
	} else if linkedTaskID != "" {
		// Existing-task flip-on-spawn: deterministic per ADR-029 D-3.
		// 'todo' / 'blocked' / unset all advance to 'in_progress' and
		// stamp started_at if not already set. 'in_progress' keeps the
		// original started_at (resume-onto-running-task case).
		// 'done' / 'cancelled' were rejected pre-tx.
		switch linkedTaskExistingStatus {
		case "todo", "blocked", "":
			if _, err := tx.ExecContext(ctx, `
				UPDATE tasks
				   SET status = 'in_progress',
				       started_at = COALESCE(started_at, ?),
				       updated_at = ?
				 WHERE id = ?`, now, now, linkedTaskID); err != nil {
				return spawnOut{}, http.StatusInternalServerError, err
			}
		}
		// in_progress: stamp neither status nor started_at; the
		// most-recent-spawn rule (W3) still routes terminal events
		// through this task.
	}

	authority := defaultRawObject(in.Authority)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_spawns (
			id, parent_agent_id, child_agent_id, spawn_spec_yaml,
			spawn_authority_json, task_json, spawned_at, worktree_path,
			mcp_token_plaintext, task_id
		) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''))`,
		spawnID, in.ParentID, agentID, in.SpawnSpec,
		string(authority), nullBytes(in.LegacyTaskJSON), now, in.WorktreePath,
		mcpTokenPlaintext, linkedTaskID); err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}

	// On session-swap, point the session at the new agent and refresh
	// the captured spawn_spec / worktree so future resumes use the
	// freshly-chosen engine/model rather than the prior one. Status
	// flips back to 'active' (covers the case where the swap happened
	// against a paused session — the new agent is alive now).
	if swapSessionID != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions
			   SET current_agent_id = ?,
			       status = 'active',
			       spawn_spec_yaml = NULLIF(?, ''),
			       worktree_path = COALESCE(NULLIF(?, ''), worktree_path),
			       last_active_at = ?
			 WHERE team_id = ? AND id = ?`,
			agentID, in.SpawnSpec, in.WorktreePath, now,
			team, swapSessionID); err != nil {
			return spawnOut{}, http.StatusInternalServerError, err
		}
	}

	// Auto-open path: when the caller asks for it (multi-steward UX
	// invariant: "every live steward has a session") and isn't doing
	// a swap, open a fresh session pointing at the new agent inside
	// the same tx. This guarantees the spawn either lands a complete
	// (agent + session) pair or rolls back both — the caller never
	// observes an agent-without-session intermediate state.
	//
	// ADR-025 D5: project-scoped spawns always get an auto-opened
	// session, regardless of in.AutoOpenSession. Workers materialized
	// for a project must be debuggable through the standard session
	// viewer, so every (worker agent, session) pair is born together.
	// The session inherits scope_kind='project', scope_id=projectID
	// so the project detail surfaces can find it.
	autoOpen := (in.AutoOpenSession || projectID != "") && !in.SuppressAutoSession
	if swapSessionID == "" && autoOpen {
		newSessionID := NewID()
		scopeKind := "team"
		scopeID := ""
		if projectID != "" {
			scopeKind = "project"
			scopeID = projectID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sessions (
				id, team_id, title, scope_kind, scope_id, current_agent_id,
				status, opened_at, last_active_at,
				worktree_path, spawn_spec_yaml
			) VALUES (?, ?, NULL, ?, NULLIF(?, ''), ?,
			          'active', ?, ?,
			          NULLIF(?, ''), NULLIF(?, ''))`,
			newSessionID, team, scopeKind, scopeID, agentID, now, now,
			in.WorktreePath, in.SpawnSpec); err != nil {
			return spawnOut{}, http.StatusInternalServerError, err
		}
	}

	if err := tx.Commit(); err != nil {
		return spawnOut{}, http.StatusInternalServerError, err
	}
	// ADR-029 D-4 W4: audit the task lifecycle hook from this spawn.
	// Inline-create lands `task.create source=spawn`; existing-task
	// flip-on-spawn lands `task.status source=spawn`. Both happen
	// post-commit so a tx rollback leaves no orphan audit row.
	if in.Task != nil && linkedTaskID != "" {
		s.recordAudit(ctx, team, "task.create", "task", linkedTaskID,
			in.Task.Title, map[string]any{
				"project_id": projectID,
				"agent_id":   agentID,
				"spawn_id":   spawnID,
				"source":     "spawn",
			})
	} else if in.TaskID != "" {
		// Only audit a flip when the existing-task path actually
		// flipped (todo/blocked → in_progress). The early branch in
		// the in-tx code keeps in_progress untouched, so re-emitting
		// here would be noise.
		switch linkedTaskExistingStatus {
		case "todo", "blocked", "":
			s.recordAudit(ctx, team, "task.status", "task", linkedTaskID,
				linkedTaskExistingStatus+" → in_progress",
				map[string]any{
					"from":     linkedTaskExistingStatus,
					"to":       "in_progress",
					"source":   "spawn",
					"agent_id": agentID,
					"spawn_id": spawnID,
				})
		}
	}
	// ADR-029 W2.7: auto-post the task body as the worker's first user
	// input so it starts the turn without waiting for an `a2a.invoke`
	// follow-up. CLAUDE.md (W2.6) carries the standing reference; this
	// event is the actual trigger via InputRouter. Best-effort: a post
	// failure leaves the spawn intact and the principal can fire input
	// manually from mobile. taskInstructions was computed pre-tx from
	// either the inline Task or a SELECT on TaskID, so this works for
	// both linkage shapes.
	if taskInstructions != "" {
		if perr := s.postSyntheticUserInput(ctx, agentID, taskInstructions); perr != nil {
			s.log.Warn("post task input failed",
				"agent_id", agentID, "task_id", linkedTaskID, "err", perr)
		}
	}
	return spawnOut{SpawnID: spawnID, AgentID: agentID, SpawnedAt: now, Mode: mode}, http.StatusCreated, nil
}

type spawnListOut struct {
	SpawnID      string          `json:"spawn_id"`
	ParentID     string          `json:"parent_agent_id,omitempty"`
	ChildID      string          `json:"child_agent_id"`
	Handle       string          `json:"handle"`
	Kind         string          `json:"kind"`
	HostID       string          `json:"host_id,omitempty"`
	Status       string          `json:"status"`
	SpawnSpec    string          `json:"spawn_spec_yaml"`
	Authority    json.RawMessage `json:"spawn_authority"`
	Task         json.RawMessage `json:"task,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	SpawnedAt    string          `json:"spawned_at"`
	// Mode is the resolved driving mode; empty if no mode was declared.
	Mode string `json:"mode,omitempty"`
	// McpToken is the plaintext bearer the spawned agent uses to call
	// /mcp/{token} on the hub. Surfaced to host-runner only — this
	// endpoint requires a host-kind auth token. Treated as a per-agent
	// secret: host-runner writes it into the agent's local .mcp.json
	// and never exposes it to mobile/principal clients. Empty for
	// pre-W2.2 spawns that predate the column.
	McpToken string `json:"mcp_token,omitempty"`
	// ProjectID binds the spawned agent to a project per ADR-025 W2.
	// host-runner reads this in launch_m2 to derive the project-
	// scoped workdir when the template's default_workdir is empty.
	ProjectID string `json:"project_id,omitempty"`
}

// handleListSpawns returns agent_spawns rows, filtered by host and/or status.
// Primary caller is the host-runner polling for agents pending launch on its box.
func (s *Server) handleListSpawns(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := r.URL.Query().Get("host_id")
	status := r.URL.Query().Get("status") // e.g. "pending"

	q := `
		SELECT sp.id, COALESCE(sp.parent_agent_id, ''), sp.child_agent_id,
		       a.handle, a.kind, COALESCE(a.host_id, ''), a.status,
		       sp.spawn_spec_yaml, sp.spawn_authority_json, sp.task_json,
		       COALESCE(sp.worktree_path, ''), sp.spawned_at,
		       COALESCE(a.driving_mode, ''),
		       COALESCE(sp.mcp_token_plaintext, ''),
		       COALESCE(a.project_id, '')
		FROM agent_spawns sp
		JOIN agents a ON a.id = sp.child_agent_id
		WHERE a.team_id = ?`
	args := []any{team}
	if host != "" {
		q += " AND a.host_id = ?"
		args = append(args, host)
	}
	if status != "" {
		q += " AND a.status = ?"
		args = append(args, status)
	}
	q += " ORDER BY sp.spawned_at DESC LIMIT 200"

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	// Only host-kind callers (the host-runner) may see the plaintext MCP
	// bearer; user-kind dashboard callers must not be able to read agent
	// secrets via the spawn-list endpoint.
	revealMcpToken := false
	if tok, ok := auth.FromContext(r.Context()); ok && tok.Kind == "host" {
		revealMcpToken = true
	}
	out := []spawnListOut{}
	for rows.Next() {
		var sp spawnListOut
		var authority string
		var task sql.NullString
		if err := rows.Scan(&sp.SpawnID, &sp.ParentID, &sp.ChildID,
			&sp.Handle, &sp.Kind, &sp.HostID, &sp.Status,
			&sp.SpawnSpec, &authority, &task,
			&sp.WorktreePath, &sp.SpawnedAt, &sp.Mode,
			&sp.McpToken, &sp.ProjectID); err != nil {
			s.writeDBErr(w, err)
			return
		}
		sp.Authority = json.RawMessage(authority)
		if task.Valid {
			sp.Task = json.RawMessage(task.String)
		}
		if !revealMcpToken {
			sp.McpToken = ""
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- helpers ----

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAgent(r rowScanner) (agentOut, error) {
	var (
		a                         agentOut
		backend, caps             string
		budget                    sql.NullInt64
		idleSince, termAt, archAt sql.NullString
	)
	if err := r.Scan(&a.ID, &a.TeamID, &a.Handle, &a.Kind, &backend, &caps,
		&a.ParentID, &a.HostID, &a.ProjectID, &a.Status, &a.PaneID,
		&a.WorktreePath, &a.JournalPath,
		&budget, &a.SpentCents, &a.PauseState, &idleSince,
		&a.CreatedAt, &termAt, &a.Mode, &archAt); err != nil {
		return a, err
	}
	a.Backend = json.RawMessage(backend)
	a.Capabilities = json.RawMessage(caps)
	if budget.Valid {
		b := int(budget.Int64)
		a.BudgetCents = &b
	}
	if idleSince.Valid {
		a.IdleSince = &idleSince.String
	}
	if termAt.Valid {
		a.TerminatedAt = &termAt.String
	}
	if archAt.Valid {
		a.ArchivedAt = &archAt.String
	}
	return a, nil
}

func defaultRawObject(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func defaultRawArray(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
