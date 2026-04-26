package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Session lifecycle:
//   open        — current_agent_id is alive, session is the active focal frame
//   interrupted — host-runner restart killed the agent process; transcript
//                 preserved by session_id stamping. Mobile shows Resume.
//                 (W2-S3 lands the resume endpoint + interruption detector.)
//   closed      — explicit teardown; transcript stays queryable forever,
//                 worktree retained until separate GC.
//
// Artifact loading at open and distillation at close are deferred per
// the active workband (`docs/steward-sessions.md` §11). The schema
// carries the columns those wedges will need (worktree_path,
// spawn_spec_yaml) without forcing them now — present-day handlers
// just record + retrieve.

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
		          'open', ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
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
		CurrentAgentID: in.AgentID, Status: "open",
		OpenedAt: now, LastActiveAt: now,
		WorktreePath: in.WorktreePath, SpawnSpecYAML: in.SpawnSpecYAML,
	})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	status := r.URL.Query().Get("status") // "" = all
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

func (s *Server) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "session")
	now := NowUTC()
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE sessions
		   SET status = 'closed', closed_at = ?, last_active_at = ?
		 WHERE team_id = ? AND id = ? AND status != 'closed'`,
		now, now, team, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish "not found" from "already closed" — both are 404 today.
		writeErr(w, http.StatusNotFound,
			"session not found or already closed")
		return
	}
	s.recordAudit(r.Context(), team, "session.close", "session", id,
		"session closed", nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleResumeSession respawns the agent inside an interrupted
// session. Reuses the session's worktree_path and spawn_spec_yaml so
// the new claude/codex/etc. process picks up exactly where it left
// off — the worktree's uncommitted edits, branch state, and
// in-progress files are all preserved. The transcript stays attached
// to the session via session_id stamping, so the user sees their
// prior turns plus the new ones in the same chat.
//
// Contract: session must be interrupted (a session that's still
// `open` doesn't need resume; a `closed` one is final). The dead
// agent stays in the agents table with its terminal status — we
// don't try to revive it, just spawn a fresh one with the same
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
	if status.String != "interrupted" {
		writeErr(w, http.StatusConflict,
			"session not interrupted (status="+status.String+")")
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

	// Stamp the new agent onto the session and flip back to open.
	now := NowUTC()
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE sessions
		   SET current_agent_id = ?, status = 'open', last_active_at = ?
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

// lookupSessionForAgent returns the open session whose current agent
// matches the given id, or "" if none exists. Cheap: indexed lookup
// keyed by current_agent_id. Used by the event-insert path to stamp
// session_id without the caller having to know about sessions.
func (s *Server) lookupSessionForAgent(ctx context.Context, agentID string) string {
	if s.db == nil || agentID == "" {
		return ""
	}
	var id string
	_ = s.db.QueryRowContext(ctx, `
		SELECT id FROM sessions
		 WHERE current_agent_id = ? AND status IN ('open','interrupted')
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
