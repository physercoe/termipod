package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	Title          string `json:"title,omitempty"`
	ScopeKind      string `json:"scope_kind,omitempty"`
	ScopeID        string `json:"scope_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	WorktreePath   string `json:"worktree_path,omitempty"`
	SpawnSpecYAML  string `json:"spawn_spec_yaml,omitempty"`
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
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
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
			"scope_kind":     scopeKind,
			"scope_id":       in.ScopeID,
			"agent_id":       in.AgentID,
			"worktree_path":  in.WorktreePath,
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
		       COALESCE(worktree_path, ''), COALESCE(spawn_spec_yaml, '')
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
			&ses.WorktreePath, &ses.SpawnSpecYAML); err != nil {
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
		       COALESCE(worktree_path, ''), COALESCE(spawn_spec_yaml, '')
		FROM sessions
		WHERE team_id = ? AND id = ?`, team, id).Scan(
		&ses.ID, &ses.TeamID, &ses.Title,
		&ses.ScopeKind, &ses.ScopeID, &ses.CurrentAgentID,
		&ses.Status, &ses.OpenedAt, &ses.LastActiveAt, &closedAt,
		&ses.WorktreePath, &ses.SpawnSpecYAML,
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
	writeJSON(w, http.StatusOK, ses)
}

// handleArchiveSession is the canonical action; /close is kept as an
// alias for one release per plan §8 so an in-flight app build doesn't
// break during coordinated rollout.
func (s *Server) handleArchiveSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")
	now := NowUTC()
	res, err := s.db.ExecContext(r.Context(), `
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
	res, err := s.db.ExecContext(r.Context(), `
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
	tx, err := s.db.BeginTx(r.Context(), nil)
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

// handleForkSession creates a new active session pre-loaded from
// an archived source session. Per ADR-009 D4, fork is the
// resume-from-archive primitive: same scope as the source, points
// at the team's live steward (or a caller-provided agent), no
// worktree_path (fork is conversational, not task-resuming).
//
// Pre-loading the system prompt from the archived session's
// distillation + last-K transcript is engine-side work that lands
// when the engine prompt-assembly path supports it. The endpoint
// today creates the session shell with the right scope; the app
// fetches the source transcript by session_id when the user wants
// to refer back.
//
// Body: {agent_id?: string, title?: string}. agent_id defaults to
// the team's currently-running steward; title defaults to the
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

	// Resolve target agent. Caller-provided wins; otherwise pick the
	// team's live steward. If neither is available, 409 — fork needs
	// an engine to attach to.
	targetAgent := in.AgentID
	if targetAgent == "" {
		// Find a live (running) steward in this team. Steward handles
		// match the convention in steward_handle.dart / handlers_agents.go;
		// here we accept anything not in a terminal state with kind
		// indicating a steward role.
		err := s.db.QueryRowContext(r.Context(), `
			SELECT id FROM agents
			 WHERE team_id = ? AND status = 'running'
			   AND (handle = 'steward' OR handle LIKE 'steward-%')
			 ORDER BY created_at DESC LIMIT 1`, team).Scan(&targetAgent)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusConflict,
				"no live steward to attach the fork to; pass agent_id explicitly")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
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

	newID := NewID()
	now := NowUTC()
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO sessions (
			id, team_id, title, scope_kind, scope_id, current_agent_id,
			status, opened_at, last_active_at,
			worktree_path, spawn_spec_yaml
		) VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?,
		          'active', ?, ?,
		          NULL, NULL)`,
		newID, team, title, scopeKind, srcScopeID.String,
		targetAgent, now, now,
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

	var (
		status, currentAgentID, worktreePath, spawnSpecYAML sql.NullString
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT status, current_agent_id, worktree_path, spawn_spec_yaml
		  FROM sessions WHERE team_id = ? AND id = ?`, team, id).Scan(
		&status, &currentAgentID, &worktreePath, &spawnSpecYAML)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status.String != "paused" {
		writeErr(w, http.StatusConflict,
			"session not paused (status="+status.String+")")
		return
	}
	if !currentAgentID.Valid || currentAgentID.String == "" {
		writeErr(w, http.StatusConflict,
			"session has no current_agent_id to derive handle/host from")
		return
	}
	if !spawnSpecYAML.Valid || spawnSpecYAML.String == "" {
		writeErr(w, http.StatusConflict,
			"session has no spawn_spec_yaml — likely opened pre-resume; close + start fresh")
		return
	}

	// Pull the dead agent's identity (handle, kind, host, parent) so the
	// new spawn looks like a continuation rather than a brand-new entity.
	var (
		deadHandle, deadKind, deadHostID, deadParentID sql.NullString
	)
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT handle, kind, host_id,
		       (SELECT parent_agent_id FROM agent_spawns
		         WHERE child_agent_id = agents.id
		         ORDER BY spawned_at DESC LIMIT 1)
		  FROM agents WHERE team_id = ? AND id = ?`,
		team, currentAgentID.String).Scan(
		&deadHandle, &deadKind, &deadHostID, &deadParentID,
	); err != nil {
		writeErr(w, http.StatusInternalServerError,
			"lookup dead agent: "+err.Error())
		return
	}

	in := spawnIn{
		ParentID:     deadParentID.String,
		ChildHandle:  deadHandle.String,
		Kind:         deadKind.String,
		HostID:       deadHostID.String,
		SpawnSpec:    spawnSpecYAML.String,
		WorktreePath: worktreePath.String,
	}
	out, code, derr := s.DoSpawn(r.Context(), team, in)
	if derr != nil {
		writeErr(w, code, derr.Error())
		return
	}

	// Stamp the new agent onto the session and flip back to active.
	now := NowUTC()
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE sessions
		   SET current_agent_id = ?, status = 'active', last_active_at = ?
		 WHERE team_id = ? AND id = ?`,
		out.AgentID, now, team, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "session.resume", "session", id,
		"resumed; new agent="+out.AgentID,
		map[string]any{
			"prior_agent_id": currentAgentID.String,
			"new_agent_id":   out.AgentID,
		})
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":     id,
		"new_agent_id":   out.AgentID,
		"prior_agent_id": currentAgentID.String,
		"spawn_id":       out.SpawnID,
	})
}

// lookupSessionForAgent returns the live (active or paused) session
// whose current agent matches the given id, or "" if none exists.
// Cheap: indexed lookup keyed by current_agent_id. Used by the
// event-insert path to stamp session_id without the caller having to
// know about sessions.
func (s *Server) lookupSessionForAgent(ctx context.Context, agentID string) string {
	if s.db == nil || agentID == "" {
		return ""
	}
	var id string
	_ = s.db.QueryRowContext(ctx, `
		SELECT id FROM sessions
		 WHERE current_agent_id = ? AND status IN ('active','paused')
		 ORDER BY last_active_at DESC LIMIT 1`, agentID).Scan(&id)
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
	_, _ = s.db.ExecContext(ctx,
		`UPDATE sessions SET last_active_at = ? WHERE id = ?`,
		NowUTC(), sessionID)
}

func coalesceTitle(provided, fallback string) string {
	if provided != "" {
		return provided
	}
	return fallback
}
