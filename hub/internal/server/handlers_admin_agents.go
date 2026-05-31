package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Owner-scope agent inspection + termination for the ops CLI (ADR-028
// Phase 4 W17). GET /v1/admin/agents lists live agents hub-wide;
// POST /v1/admin/agents/{agent}/kill terminates one. The kill path
// shares applyAgentTerminationEffects with handlePatchAgent so the
// lifecycle + audit trail is identical to a mobile-driven stop.

// AdminAgentRow is one row of GET /v1/admin/agents.
type AdminAgentRow struct {
	AgentID string `json:"agent_id"`
	TeamID  string `json:"team_id"`
	Handle  string `json:"handle,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Status  string `json:"status"`
	HostID  string `json:"host_id,omitempty"`
}

// handleAdminListAgents is GET /v1/admin/agents — owner-scope. Lists
// live agents (status not in the terminal set) across every team;
// ?all=1 includes terminated / crashed / failed / archived rows too.
func (s *Server) handleAdminListAgents(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	q := `SELECT id, team_id, COALESCE(handle, ''), COALESCE(kind, ''),
	             COALESCE(status, ''), COALESCE(host_id, '')
	        FROM agents`
	if r.URL.Query().Get("all") != "1" && r.URL.Query().Get("all") != "true" {
		q += ` WHERE status NOT IN ('terminated', 'crashed', 'failed', 'archived')`
	}
	q += ` ORDER BY team_id, handle`

	rows, err := s.db.QueryContext(r.Context(), q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	agents := []AdminAgentRow{}
	for rows.Next() {
		var a AdminAgentRow
		if err := rows.Scan(&a.AgentID, &a.TeamID, &a.Handle, &a.Kind,
			&a.Status, &a.HostID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

// handleAdminKillAgent is POST /v1/admin/agents/{agent}/kill —
// owner-scope. Flips the agent to 'terminated' and runs the shared
// termination side-effects. Idempotent: an agent already in a terminal
// state is reported as killed=false with its existing status.
func (s *Server) handleAdminKillAgent(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	id := chi.URLParam(r, "agent")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "agent id required")
		return
	}
	var team, status, handle string
	switch err := s.db.QueryRowContext(r.Context(),
		`SELECT team_id, COALESCE(status, ''), COALESCE(handle, '')
		   FROM agents WHERE id = ?`, id).Scan(&team, &status, &handle); {
	case err == nil:
		// found
	case err.Error() == "sql: no rows in result set":
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	switch status {
	case "terminated", "crashed", "failed", "archived":
		writeJSON(w, http.StatusOK, map[string]any{
			"agent_id": id, "team_id": team, "handle": handle,
			"killed": false, "already": status,
		})
		return
	}

	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE agents SET status = 'terminated', terminated_at = ? WHERE id = ?`,
		NowUTC(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// archive=false: an admin kill is an emergency stop — the session
	// stays paused/resumable, matching its pre-split behaviour.
	s.applyAgentTerminationEffects(r.Context(), team, id, "agent killed via admin CLI", false)
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": id, "team_id": team, "handle": handle, "killed": true,
	})
}
