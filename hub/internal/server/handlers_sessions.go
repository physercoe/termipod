package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Session lifecycle (per ADR-009):
//   active   — engine attached; session is the live focal frame
//   paused   — host-runner restart killed the agent process; transcript
//              preserved by session_id stamping. Mobile shows Resume.
//   archived — explicit teardown after distillation; transcript stays
//              queryable forever, worktree retained until separate GC.
//              Resumable via fork (Phase 2).
//   deleted  — soft-deleted; tombstone for audit chain.
//
// Endpoints: POST /archive is the canonical action; /close is kept
// as a deprecated alias for one release per plan §8.

type sessionIn struct {
	Title         string `json:"title,omitempty"`
	ScopeKind     string `json:"scope_kind,omitempty"`
	ScopeID       string `json:"scope_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	WorktreePath  string `json:"worktree_path,omitempty"`
	SpawnSpecYAML string `json:"spawn_spec_yaml,omitempty"`
}

type sessionOut struct {
	ID             string  `json:"id"`
	TeamID         string  `json:"team_id"`
	Title          string  `json:"title,omitempty"`
	ScopeKind      string  `json:"scope_kind,omitempty"`
	ScopeID        string  `json:"scope_id,omitempty"`
	CurrentAgentID string  `json:"current_agent_id,omitempty"`
	Status         string  `json:"status"`
	OpenedAt       string  `json:"opened_at"`
	LastActiveAt   string  `json:"last_active_at"`
	ClosedAt       *string `json:"closed_at,omitempty"`
	WorktreePath   string  `json:"worktree_path,omitempty"`
	SpawnSpecYAML  string  `json:"spawn_spec_yaml,omitempty"`
	// SessionNameHint is the latest non-empty `session_name` value
	// claude-code's statusLine has emitted for this session (ADR-036
	// v1.0.705 polish). Persisted on every status_line event ingest
	// via captureSessionNameHint. Mobile renders it as the second
	// fallback in the session-row title precedence —
	// user title > session_name_hint > "(untitled session)". Stays
	// empty for sessions on engines that don't surface a session_name
	// (codex/gemini/kimi today) and for sessions whose first
	// status_line frame hasn't fired yet; mobile's empty-string check
	// handles both. NOT a substitute for user-set titles — those are
	// load-bearing across surfaces (search index, audit log, voice).
	SessionNameHint string `json:"session_name_hint,omitempty"`
	// SessionCostUSDImputed is the derived hub-side total cost in USD
	// across the session's usage events, applied against the active
	// pricing table (ADR-036 D8 chip 2). Populated only on single-
	// session GET; omitted from list-sessions to keep that payload
	// small (and to keep list latency O(N) sessions, not O(N) sessions
	// × O(M) usage events each). nil = session has zero usage events
	// OR every model id seen was absent from the pricing table — the
	// chip self-gates on null per ADR-036 D9 "blank > wrong". Mobile
	// hits GET /sessions/{id}/cost for the per-model breakdown.
	SessionCostUSDImputed *float64 `json:"session_cost_usd_imputed,omitempty"`
}

func (s *Server) handleOpenSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in sessionIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	scopeKind := in.ScopeKind
	if scopeKind == "" {
		scopeKind = "team"
	}
	// ADR-025 project-binding guard: a project-scoped session must be
	// backed by an agent that is itself bound to the same project.
	// Without this, the mobile path can (and did, pre-v1.0.609) create
	// a sessions row whose scope=(project, X) but current_agent_id=
	// <general_steward>. lookupSessionForAgent then picks the freshly-
	// opened row as the agent's "current" session and starts stamping
	// the general steward's transcript with the project session_id —
	// observable as "general session stops updating, a phantom project
	// session shows all the general history". Fixing the mobile caller
	// alone leaves the next caller (REST script, MCP tool, ops tool)
	// free to repeat the same data corruption, so we gate it here.
	if scopeKind == "project" && in.AgentID != "" {
		if in.ScopeID == "" {
			writeErr(w, http.StatusBadRequest,
				"scope_kind=project requires scope_id")
			return
		}
		var agentProjectID sql.NullString
		err := s.db.QueryRowContext(r.Context(),
			`SELECT project_id FROM agents WHERE team_id = ? AND id = ?`,
			team, in.AgentID).Scan(&agentProjectID)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "agent not found in team")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !agentProjectID.Valid || agentProjectID.String != in.ScopeID {
			writeErr(w, http.StatusBadRequest,
				"agent not bound to project; cross-scope session refused (ADR-025)")
			return
		}
	}
	id := NewID()
	now := NowUTC()
	_, err := s.writeDB.ExecContext(r.Context(), `
		INSERT INTO sessions (
			id, team_id, title, scope_kind, scope_id, current_agent_id,
			status, opened_at, last_active_at, worktree_path, spawn_spec_yaml
		) VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''),
		          'active', ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
		id, team, in.Title, scopeKind, in.ScopeID, in.AgentID,
		now, now, in.WorktreePath, in.SpawnSpecYAML,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "session.open", "session", id,
		coalesceTitle(in.Title, "session opened"),
		map[string]any{
			"scope_kind":    scopeKind,
			"scope_id":      in.ScopeID,
			"agent_id":      in.AgentID,
			"worktree_path": in.WorktreePath,
		})
	writeJSON(w, http.StatusCreated, sessionOut{
		ID: id, TeamID: team, Title: in.Title,
		ScopeKind: scopeKind, ScopeID: in.ScopeID,
		CurrentAgentID: in.AgentID, Status: "active",
		OpenedAt: now, LastActiveAt: now,
		WorktreePath: in.WorktreePath, SpawnSpecYAML: in.SpawnSpecYAML,
	})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	status := r.URL.Query().Get("status") // "" = all-non-deleted
	q := `
		SELECT id, team_id, COALESCE(title, ''), COALESCE(scope_kind, ''),
		       COALESCE(scope_id, ''), COALESCE(current_agent_id, ''),
		       status, opened_at, last_active_at, closed_at,
		       COALESCE(worktree_path, ''), COALESCE(spawn_spec_yaml, ''),
		       COALESCE(session_name_hint, '')
		FROM sessions
		WHERE team_id = ?`
	args := []any{team}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	} else {
		// Default list excludes soft-deleted rows — explicit ?status=deleted
		// is still allowed for ops/debug callers that want to see them.
		q += " AND status != 'deleted'"
	}
	q += " ORDER BY last_active_at DESC LIMIT 200"
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []sessionOut{}
	for rows.Next() {
		var (
			ses      sessionOut
			closedAt sql.NullString
		)
		if err := rows.Scan(&ses.ID, &ses.TeamID, &ses.Title,
			&ses.ScopeKind, &ses.ScopeID, &ses.CurrentAgentID,
			&ses.Status, &ses.OpenedAt, &ses.LastActiveAt, &closedAt,
			&ses.WorktreePath, &ses.SpawnSpecYAML,
			&ses.SessionNameHint); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if closedAt.Valid {
			ses.ClosedAt = &closedAt.String
		}
		out = append(out, ses)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")
	var (
		ses      sessionOut
		closedAt sql.NullString
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, team_id, COALESCE(title, ''), COALESCE(scope_kind, ''),
		       COALESCE(scope_id, ''), COALESCE(current_agent_id, ''),
		       status, opened_at, last_active_at, closed_at,
		       COALESCE(worktree_path, ''), COALESCE(spawn_spec_yaml, ''),
		       COALESCE(session_name_hint, '')
		FROM sessions
		WHERE team_id = ? AND id = ?`, team, id).Scan(
		&ses.ID, &ses.TeamID, &ses.Title,
		&ses.ScopeKind, &ses.ScopeID, &ses.CurrentAgentID,
		&ses.Status, &ses.OpenedAt, &ses.LastActiveAt, &closedAt,
		&ses.WorktreePath, &ses.SpawnSpecYAML,
		&ses.SessionNameHint,
	)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if closedAt.Valid {
		ses.ClosedAt = &closedAt.String
	}
	// Derived cost field (ADR-036 D8 chip 2). Errors are swallowed —
	// a missing cost must NEVER prevent the session GET from returning
	// (the chip self-gates on a null field per D9).
	if s.pricing != nil {
		if res, err := pricingSessionCost(r.Context(), s, id); err == nil {
			if res.TotalUSD > 0 || len(res.Breakdown) > 0 {
				v := res.TotalUSD
				ses.SessionCostUSDImputed = &v
			}
		} else {
			s.log.Warn("pricing.session_cost", "session", id, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, ses)
}

// handleArchiveSession is the canonical archive action (ADR-009). The
// deprecated /close alias route was retired in WS1.2.
func (s *Server) handleArchiveSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")
	now := NowUTC()
	res, err := s.writeDB.ExecContext(r.Context(), `
		UPDATE sessions
		   SET status = 'archived', closed_at = ?, last_active_at = ?
		 WHERE team_id = ? AND id = ? AND status != 'archived'`,
		now, now, team, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish "not found" from "already archived" — both are 404 today.
		writeErr(w, http.StatusNotFound,
			"session not found or already archived")
		return
	}
	s.recordAudit(r.Context(), team, "session.archive", "session", id,
		"session archived", nil)
	w.WriteHeader(http.StatusNoContent)
}

// handlePatchSession updates mutable session fields. Today only `title`
// is editable — sessions default to NULL title so they render as
// "(untitled session)" in the list, and the user needs a way to rename
// them after the fact. An empty title clears it (back to NULL); a
// non-empty title replaces it. Status/scope/agent are owned by the
// lifecycle endpoints; this handler refuses to touch them.
func (s *Server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")
	var in struct {
		Title *string `json:"title,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Title == nil {
		writeErr(w, http.StatusBadRequest, "no editable fields in body")
		return
	}
	res, err := s.writeDB.ExecContext(r.Context(), `
		UPDATE sessions SET title = NULLIF(?, '')
		 WHERE team_id = ? AND id = ? AND status != 'deleted'`,
		*in.Title, team, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	s.recordAudit(r.Context(), team, "session.rename", "session", id,
		coalesceTitle(*in.Title, "session title cleared"),
		map[string]any{"title": *in.Title})
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSession is a soft delete: marks the session row as
// `status='deleted'` and clears its session_id from agent_events,
// audit_events, and attention_items so the transcript-linkage no
// longer resolves through this session. The events themselves stay —
// they're the agent's history, owned by audit, not by the session.
//
// Refuses to delete an active or paused session: the contract is
// "archive first" so an active conversation can't be silently lost.
// Resume after delete is impossible (the session is no longer
// listable) which is the point — delete is meant to be the final
// disposition for sessions the user has explicitly walked away from.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")

	var status string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT status FROM sessions WHERE team_id = ? AND id = ?`,
		team, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status == "active" || status == "paused" {
		writeErr(w, http.StatusConflict,
			"archive the session before deleting (status="+status+")")
		return
	}
	if status == "deleted" {
		// Already deleted — idempotent success is friendlier than 404
		// for users who tap delete twice.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Single tx so partial deletes can't leak references to a
	// soft-deleted session if one of the unlinks fails.
	tx, err := s.writeDB.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	now := NowUTC()
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE sessions SET status='deleted', closed_at = COALESCE(closed_at, ?)
		   WHERE team_id = ? AND id = ?`,
		now, team, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, table := range []string{
		"agent_events", "audit_events", "attention_items",
	} {
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE `+table+` SET session_id = NULL WHERE session_id = ?`,
			id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "session.delete", "session", id,
		"session deleted", nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleForkSession creates a new session shell from an archived
// source. Per ADR-009 D4, fork is the resume-from-archive primitive:
// same scope as the source, no worktree_path (fork is
// conversational, not task-resuming).
//
// Attachment model: fork does NOT pick a live steward to attach to.
// A running steward agent is bound to its own active session via a
// single stream-json connection; double-binding would race events
// between two sessions. The fork lands as `paused` with
// `current_agent_id = NULL`; the caller drives a spawn or
// replace-steward into the new session to make it live. Callers
// that genuinely have a session-less steward (e.g. just stopped)
// may pass `agent_id` to attach explicitly — the server validates
// the target isn't already owning an active session.
//
// Pre-loading the system prompt from the archived session's
// distillation + last-K transcript is engine-side work that lands
// when the engine prompt-assembly path supports it. The endpoint
// today creates the session shell with the right scope; the app
// fetches the source transcript by session_id when the user wants
// to refer back.
//
// Body: {agent_id?: string, title?: string}. title defaults to the
// source's title (so the fork reads as "continuation of X" by
// default).
//
// Contract: source session must be archived. Active/paused
// sessions can't be forked because they're already live; fork on
// a non-archived session returns 409.
func (s *Server) handleForkSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	srcID := chi.URLParam(r, "session")

	var in struct {
		AgentID string `json:"agent_id,omitempty"`
		Title   string `json:"title,omitempty"`
	}
	// Empty body is fine — defaults handle the common case.
	_ = json.NewDecoder(r.Body).Decode(&in)

	var (
		srcStatus, srcTitle, srcScopeKind, srcScopeID, srcAgentID sql.NullString
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT status, COALESCE(title, ''), COALESCE(scope_kind, ''),
		       COALESCE(scope_id, ''), COALESCE(current_agent_id, '')
		  FROM sessions
		 WHERE team_id = ? AND id = ?`,
		team, srcID).Scan(
		&srcStatus, &srcTitle, &srcScopeKind, &srcScopeID, &srcAgentID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if srcStatus.String != "archived" {
		writeErr(w, http.StatusConflict,
			"fork requires archived source (status="+srcStatus.String+")")
		return
	}

	// Fork never auto-attaches to an existing live steward. A
	// running steward agent is bound to its own active session
	// (one stream-json at a time); pointing a second active
	// session at it would race events between the two — newer
	// turns would land on whichever session lookupSessionForAgent
	// resolves last, and the older session would silently strand
	// mid-conversation. So fork always returns an unattached
	// (paused) session by default; the caller (mobile or CLI)
	// drives a spawn or replace into it to make it live.
	//
	// Caller-provided agent_id is still honoured — operators who
	// know the target agent is between sessions (e.g. just stopped)
	// can attach explicitly. We validate that the target isn't
	// already owning an active session to keep the one-active-per-
	// steward invariant.
	targetAgent := in.AgentID
	if targetAgent != "" {
		var n int
		err := s.db.QueryRowContext(r.Context(), `
			SELECT COUNT(1) FROM sessions
			 WHERE team_id = ? AND current_agent_id = ?
			   AND status = 'active'`, team, targetAgent).Scan(&n)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n > 0 {
			writeErr(w, http.StatusConflict,
				"agent_id already owns an active session; "+
					"archive it first or fork without agent_id "+
					"and spawn a fresh steward")
			return
		}
	}

	title := in.Title
	if title == "" {
		title = srcTitle.String
	}
	scopeKind := srcScopeKind.String
	if scopeKind == "" {
		scopeKind = "team"
	}

	// Status follows attachment: attached forks are immediately live,
	// unattached forks land in paused so the multi-steward invariant
	// ("every active session has a current_agent_id") still holds.
	forkStatus := "active"
	if targetAgent == "" {
		forkStatus = "paused"
	}

	newID := NewID()
	now := NowUTC()
	_, err = s.writeDB.ExecContext(r.Context(), `
		INSERT INTO sessions (
			id, team_id, title, scope_kind, scope_id, current_agent_id,
			status, opened_at, last_active_at,
			worktree_path, spawn_spec_yaml
		) VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''),
		          ?, ?, ?,
		          NULL, NULL)`,
		newID, team, title, scopeKind, srcScopeID.String,
		targetAgent, forkStatus, now, now,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.recordAudit(r.Context(), team, "session.fork", "session", newID,
		"forked from "+srcID,
		map[string]any{
			"source_session_id": srcID,
			"scope_kind":        scopeKind,
			"scope_id":          srcScopeID.String,
			"agent_id":          targetAgent,
		})

	writeJSON(w, http.StatusCreated, map[string]any{
		"session_id":        newID,
		"source_session_id": srcID,
		"agent_id":          targetAgent,
		"scope_kind":        scopeKind,
		"scope_id":          srcScopeID.String,
		"title":             title,
	})
}

// handleResumeSession respawns the agent inside a paused session.
// Reuses the session's worktree_path and spawn_spec_yaml so the new
// claude/codex/etc. process picks up exactly where it left off — the
// worktree's uncommitted edits, branch state, and in-progress files
// are all preserved. The transcript stays attached to the session
// via session_id stamping, so the user sees their prior turns plus
// the new ones in the same chat.
//
// Contract: session must be paused (an active session doesn't need
// resume; an archived one is reachable via fork in Phase 2). The
// dead agent stays in the agents table with its terminal status —
// we don't try to revive it, just spawn a fresh one with the same
// handle (the unique-handle constraint accepts this because dead
// agents drop out of the "live" index).
func (s *Server) handleResumeSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")
	out, code, err := s.resumePausedSession(r.Context(), team, id)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// resumePausedSession respawns the agent inside a paused session and
// returns the response body (or an HTTP status + error). Shared by the
// session-keyed REST handler and the agent-keyed steward path
// (handleResumeAgentSession), so both respawn identically. See
// handleResumeSession's doc comment for the continuity contract.
func (s *Server) resumePausedSession(ctx context.Context, team, id string) (map[string]any, int, error) {
	var (
		status, currentAgentID, worktreePath, spawnSpecYAML sql.NullString
		engineSessionID                                     sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT status, current_agent_id, worktree_path, spawn_spec_yaml,
		       engine_session_id
		  FROM sessions WHERE team_id = ? AND id = ?`, team, id).Scan(
		&status, &currentAgentID, &worktreePath, &spawnSpecYAML,
		&engineSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, http.StatusNotFound, errors.New("session not found")
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if status.String != "paused" {
		return nil, http.StatusConflict,
			errors.New("session not paused (status=" + status.String + ")")
	}
	if !currentAgentID.Valid || currentAgentID.String == "" {
		return nil, http.StatusConflict,
			errors.New("session has no current_agent_id to derive handle/host from")
	}
	if !spawnSpecYAML.Valid || spawnSpecYAML.String == "" {
		return nil, http.StatusConflict,
			errors.New("session has no spawn_spec_yaml — likely opened pre-resume; close + start fresh")
	}

	// Pull the dead agent's identity (handle, kind, host, parent, project) so
	// the new spawn looks like a continuation rather than a brand-new entity.
	// project_id matters: without it the resumed agent is unbound and never
	// shows in the project Agents tab — the operator sees only the stale
	// terminated row while the live worker hides off-project. DoSpawn lets a
	// `project_id:` in the reused spawn_spec_yaml win, so threading it here is
	// the fallback for specs that don't carry it (ADR-025 W2 precedence).
	var (
		deadHandle, deadKind, deadHostID, deadParentID, deadProjectID sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT handle, kind, host_id, project_id,
		       (SELECT parent_agent_id FROM agent_spawns
		         WHERE child_agent_id = agents.id
		         ORDER BY spawned_at DESC LIMIT 1)
		  FROM agents WHERE team_id = ? AND id = ?`,
		team, currentAgentID.String).Scan(
		&deadHandle, &deadKind, &deadHostID, &deadProjectID, &deadParentID,
	); err != nil {
		return nil, http.StatusInternalServerError,
			fmt.Errorf("lookup dead agent: %w", err)
	}

	// ADR-014: thread the captured engine session id back into the
	// spawn cmd so the freshly-launched claude resumes its prior
	// conversation instead of cold-starting. The sessions row carries
	// the same spawn_spec_yaml across resumes, so we re-splice fresh
	// each time and never persist a stale --resume flag in `sessions`.
	//
	// ADR-021 W1.2: ACP-capable families (gemini-cli today; future
	// claude-code SDK ACP) carry the cursor at the protocol level via
	// session/load, not via cmd argv. The hub injects a YAML field that
	// ACPDriver reads via SpawnSpec.ResumeSessionID; M2/M4 launch paths
	// ignore it. Inject regardless of mode — the field is harmless when
	// the spawn ends up M2 (gemini exec-per-turn captures its own
	// cursor independently).
	specYAML := spawnSpecYAML.String
	if engineSessionID.Valid && engineSessionID.String != "" {
		switch deadKind.String {
		case "claude-code":
			specYAML = spliceClaudeResume(specYAML, engineSessionID.String)
		case "gemini-cli", "kimi-code", "codex":
			// Codex shares the ACP splice shape (top-level
			// `resume_session_id` YAML field) by design: AppServerDriver
			// reads SpawnSpec.ResumeSessionID and threads it as the
			// `thread/resume` JSON-RPC method's `threadId` param
			// (driver_appserver.go::handshake → upstream
			// `codex-rs/app-server-protocol/src/protocol/common.rs:457`).
			// One YAML field, two protocol surfaces.
			specYAML = spliceACPResume(specYAML, engineSessionID.String)
		case "antigravity":
			specYAML = spliceAntigravityResume(specYAML, engineSessionID.String)
		}
	}

	in := spawnIn{
		ParentID:    deadParentID.String,
		ChildHandle: deadHandle.String,
		Kind:        deadKind.String,
		HostID:      deadHostID.String,
		ProjectID:   deadProjectID.String,
		SpawnSpec:   specYAML,
		// Resume stamps the EXISTING paused session below; suppress the
		// project auto-open so threading project_id doesn't mint a second
		// session that collides on (team_id, worktree_path).
		SuppressAutoSession: true,
		WorktreePath:        worktreePath.String,
	}
	out, code, derr := s.DoSpawn(ctx, team, in)
	if derr != nil {
		return nil, code, derr
	}

	// Stamp the new agent onto the session and flip back to active.
	now := NowUTC()
	if _, err := s.writeDB.ExecContext(ctx, `
		UPDATE sessions
		   SET current_agent_id = ?, status = 'active', last_active_at = ?
		 WHERE team_id = ? AND id = ?`,
		out.AgentID, now, team, id); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	// ADR-026 W7 — carry forward the prior agent's last advertised
	// mode/model state. kimi-cli's session/load returns an empty `{}`
	// response (the ACP spec permits this; agents MAY omit echoing
	// state on load), so ACPDriver.Start emits no synthetic
	// `currentModeId`/`currentModelId` system event and mobile's
	// modeModelStateFromEvents returns null — the picker stays hidden
	// on the resumed agent even though the daemon's session is fully
	// alive. Re-post the prior agent's last state event under the new
	// agent_id so the picker survives the resume. Engine-neutral:
	// gemini-cli echoes state on load anyway, so the duplicate lands
	// on a list mobile reduces to the same final state. Best-effort —
	// failure leaves the picker hidden but doesn't fail the resume.
	s.carryModeModelStateAcrossResume(ctx,
		currentAgentID.String, out.AgentID)
	s.recordAudit(ctx, team, "session.resume", "session", id,
		"resumed; new agent="+out.AgentID,
		map[string]any{
			"prior_agent_id": currentAgentID.String,
			"new_agent_id":   out.AgentID,
		})
	return map[string]any{
		"session_id":     id,
		"new_agent_id":   out.AgentID,
		"prior_agent_id": currentAgentID.String,
		"spawn_id":       out.SpawnID,
	}, http.StatusOK, nil
}

// handleResumeAgentSession is POST /v1/teams/{team}/agents/{agent}/resume-session.
// The steward-facing inverse of agents.terminate: it finds the paused
// session whose current agent is {agent} (terminate leaves the session
// paused) and respawns it — a fresh process continues from the
// worktree + transcript cursor. Distinct from POST /agents/{id}/resume,
// which SIGCONTs a still-alive paused process.
func (s *Server) handleResumeAgentSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	agentID := chi.URLParam(r, "agent")

	var sessionID string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id FROM sessions
		 WHERE team_id = ? AND current_agent_id = ? AND status = 'paused'
		 ORDER BY last_active_at DESC LIMIT 1`,
		team, agentID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusConflict,
			"no paused session for this agent (nothing to resume — the agent may still be live, or its session was archived)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, code, rerr := s.resumePausedSession(r.Context(), team, sessionID)
	if rerr != nil {
		writeErr(w, code, rerr.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// lookupSessionForAgent returns the live (active or paused) session
// whose current agent matches the given id, or "" if none exists.
// Cheap: indexed lookup keyed by current_agent_id. Used by the
// event-insert path to stamp session_id without the caller having to
// know about sessions.
//
// Scope preference (added v1.0.609 alongside the openSession guard):
// when an agent has multiple live sessions, prefer the one whose
// scope matches the agent's intrinsic binding — project-bound agents
// prefer matching project-scoped sessions; non-project agents prefer
// team/null-scoped sessions. This is defense-in-depth against the
// pre-guard data corruption where a stray cross-scope session row
// could "win" the last_active_at race and capture future events.
// Same ordering by last_active_at within the preferred bucket so
// existing behavior is preserved for the common single-session case.
func (s *Server) lookupSessionForAgent(ctx context.Context, agentID string) string {
	if s.db == nil || agentID == "" {
		return ""
	}
	var id string
	_ = s.db.QueryRowContext(ctx, `
		SELECT s.id FROM sessions s
		 LEFT JOIN agents a ON a.id = s.current_agent_id
		 WHERE s.current_agent_id = ? AND s.status IN ('active','paused')
		 ORDER BY
		   CASE
		     WHEN a.project_id IS NOT NULL AND a.project_id != ''
		          AND s.scope_kind = 'project' AND s.scope_id = a.project_id THEN 0
		     WHEN (a.project_id IS NULL OR a.project_id = '')
		          AND (s.scope_kind = 'team' OR s.scope_kind IS NULL) THEN 0
		     ELSE 1
		   END,
		   s.last_active_at DESC
		 LIMIT 1`, agentID).Scan(&id)
	return id
}

// touchSession bumps last_active_at on the session containing this
// event. Best-effort: an error here doesn't fail the event insert,
// since session bookkeeping is a tracking concern, not a data
// integrity one.
func (s *Server) touchSession(ctx context.Context, sessionID string) {
	if s.db == nil || sessionID == "" {
		return
	}
	_, _ = s.writeDB.ExecContext(ctx,
		`UPDATE sessions SET last_active_at = ? WHERE id = ?`,
		NowUTC(), sessionID)
}

// captureEngineSessionID lifts the engine-side session id out of
// session.init events and stores it on the live session so a future
// resume can thread it back into the spawn cmd. ADR-014 — wedge for
// claude-code's `--resume <id>` continuity, but the column is engine-
// neutral: gemini-cli's stream-json uses the same `session_id` field
// shape, and codex (whose threadId arrives via a different channel)
// can land in this column from its own capture path later.
//
// Best-effort: an error here can't fail the event insert. The worst
// case is a resume that starts a fresh engine session — exactly the
// pre-ADR-014 behaviour, with no transcript loss on the hub side.
func (s *Server) captureEngineSessionID(ctx context.Context, sessionID, kind, producer, payloadJSON string) {
	if s.db == nil || sessionID == "" {
		return
	}
	if kind != "session.init" || producer != "agent" {
		return
	}
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		return
	}
	if p.SessionID == "" {
		return
	}
	_, _ = s.writeDB.ExecContext(ctx,
		`UPDATE sessions SET engine_session_id = ? WHERE id = ?`,
		p.SessionID, sessionID)
}

// captureSessionNameHint lifts the auto-derived display label out of
// claude-code's statusLine `session_name` field and stores it on the
// session row so the session list page can show it as a fallback when
// no user-set title exists. v1.0.705 polish on top of ADR-036 W6.
//
// Mirrors captureEngineSessionID's shape: same call site, same
// kind+producer gate (status_line / agent), same best-effort silent
// failure mode. The store-update is fire-and-forget — losing one
// status_line frame's worth of name is harmless (the next ~10s frame
// will re-stamp it, claude refreshes the name continuously).
//
// Stickiness: only writes when the latest payload carries a NON-EMPTY
// session_name. Empty / missing values are a no-op (so a `/clear` that
// rotates the engine_session_id before claude has auto-named the
// fresh conversation keeps the prior visible name for ~10s rather
// than blinking through "(untitled session)"). This matches the
// reducer semantics on the mobile side — sessionNameFromEvents walks
// backwards for the latest non-empty name.
//
// Reset on rotation: not done here. The hub already knows when an
// engine_session_id rotation happens (status_line W3); a future
// version could clear session_name_hint on rotation, but the current
// "next frame overwrites" semantics are close enough that the
// rotation handler isn't load-bearing for this column.
func (s *Server) captureSessionNameHint(ctx context.Context, sessionID, kind, producer, payloadJSON string) {
	if s.db == nil || sessionID == "" {
		return
	}
	if kind != "status_line" || producer != "agent" {
		return
	}
	var p struct {
		SessionName string `json:"session_name"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		return
	}
	if p.SessionName == "" {
		return
	}
	_, _ = s.writeDB.ExecContext(ctx,
		`UPDATE sessions SET session_name_hint = ?
		  WHERE id = ?
		    AND COALESCE(session_name_hint, '') != ?`,
		p.SessionName, sessionID, p.SessionName)
}

// carryModeModelStateAcrossResume copies the prior agent's most recent
// mode/model state event (`kind=system, producer=system` carrying
// currentModeId/currentModelId/availableModes/availableModels) onto
// the freshly-resumed agent. ADR-026 W7. Without this, kimi-cli's
// empty `session/load` response leaves the resumed agent with no
// state event for mobile to walk, hiding the picker even though the
// daemon's session is alive and routable.
//
// Best-effort: any DB error is logged and swallowed so the resume
// itself completes successfully — the worst case is a hidden picker,
// which is the pre-W7 status quo.
func (s *Server) carryModeModelStateAcrossResume(ctx context.Context, priorAgentID, newAgentID string) {
	if s.db == nil || priorAgentID == "" || newAgentID == "" {
		return
	}
	// W7c — walk the prior agent's system events newest-first and
	// independently capture each of the four picker-relevant fields
	// from the LATEST event that carries it. Pre-W7c the query grabbed
	// only the single latest matching row; once W7b synthetic events
	// (which ship only currentModeId/currentModelId, no available*
	// lists) landed after a set_mode/set_model RPC, that single row
	// was id-only and the carried event lost the lists — the resumed
	// agent's picker stayed hidden because mobile's hasMode/hasModel
	// gate requires the list to be non-empty. Composing across events
	// mirrors the mobile-side reducer, so the picker survives any
	// fragmentation of the underlying event stream.
	rows, err := s.db.QueryContext(ctx, `
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'system' AND producer = 'system'
		   AND (payload_json LIKE '%currentModeId%'
		     OR payload_json LIKE '%currentModelId%'
		     OR payload_json LIKE '%availableModes%'
		     OR payload_json LIKE '%availableModels%')
		 ORDER BY seq DESC`,
		priorAgentID)
	if err != nil {
		return
	}
	defer rows.Close()
	carried := map[string]any{}
	want := map[string]bool{
		"currentModeId":   true,
		"availableModes":  true,
		"currentModelId":  true,
		"availableModels": true,
	}
	for rows.Next() {
		var payloadJSON sql.NullString
		if err := rows.Scan(&payloadJSON); err != nil {
			continue
		}
		if !payloadJSON.Valid || payloadJSON.String == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(payloadJSON.String), &raw); err != nil {
			continue
		}
		for k := range want {
			if _, already := carried[k]; already {
				continue
			}
			if v, ok := raw[k]; ok && v != nil {
				carried[k] = v
			}
		}
		if len(carried) == len(want) {
			break
		}
	}
	if len(carried) == 0 {
		return
	}
	carriedJSON, err := json.Marshal(carried)
	if err != nil {
		return
	}
	sessionID := s.lookupSessionForAgent(ctx, newAgentID)
	// Best-effort marker; the carried mode/model state already applied.
	_, _, _, _, _ = insertAgentEvent(ctx, s.writeDB, agentEventInsert{
		AgentID:     newAgentID,
		SessionID:   sessionID,
		Kind:        "system",
		Producer:    "system",
		PayloadJSON: string(carriedJSON),
	})
}

// maybeEmitContextMutationMarker inspects an input.text body for a
// claude/gemini context-mutation slash command and, if matched,
// inserts a typed `agent_event` row immediately after the input row
// the caller already wrote. ADR-014 OQ-4 — lets the mobile transcript
// surface engine-side context truncations the engines themselves
// don't announce in stream-json.
//
// Pre-conditions: the caller has already inserted the input.text
// event and bumped session activity, so the marker arrives one seq
// behind the user's text and the transcript reads as
// "[user] /compact" → "[system] context compacted".
//
// Best-effort: any error here can't fail the input write that the
// caller already committed — the engine will still receive the
// command and execute it; the user just loses the visual marker.
// Silent failure is the correct policy because every request for an
// unrelated agent kind would otherwise trip a no-op error path.
func (s *Server) maybeEmitContextMutationMarker(
	ctx context.Context, team, agentID, sessionID, body string,
) {
	if s.db == nil || agentID == "" {
		return
	}
	var agentKind string
	if err := s.db.QueryRowContext(ctx,
		`SELECT kind FROM agents WHERE team_id = ? AND id = ?`,
		team, agentID).Scan(&agentKind); err != nil {
		return
	}
	mut, ok := detectContextMutation(agentKind, body)
	if !ok {
		return
	}
	payload := map[string]any{
		"verb":       mut.Verb,
		"agent_kind": agentKind,
		"trigger":    "user_input",
		// Note explains the divergence the marker is recording so a
		// reader scrolling the raw events table doesn't have to
		// re-derive ADR-014 OQ-4 from first principles.
		"note": "engine-side context mutation; hub transcript " +
			"continues but engine view diverges here",
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}
	id, seq, _, ts, err := insertAgentEvent(ctx, s.writeDB, agentEventInsert{
		AgentID:     agentID,
		SessionID:   sessionID,
		Kind:        mut.Kind,
		Producer:    "system",
		PayloadJSON: string(payloadBytes),
	})
	if err != nil {
		return
	}
	s.bus.Publish(agentBusKey(agentID), map[string]any{
		"id":         id,
		"agent_id":   agentID,
		"seq":        seq,
		"ts":         ts,
		"kind":       mut.Kind,
		"producer":   "system",
		"payload":    json.RawMessage(payloadBytes),
		"session_id": sessionID,
	})
}

func coalesceTitle(provided, fallback string) string {
	if provided != "" {
		return provided
	}
	return fallback
}
