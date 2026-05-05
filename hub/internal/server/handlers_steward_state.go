package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// stewardStateOut is the read shape for
// GET /v1/teams/{team}/projects/{project_id}/steward/state
// per docs/reference/hub-api-deliverables.md §10.1. The mobile
// steward strip polls this every few seconds while the Project
// Detail screen is visible (Cache-Control: private, no-cache).
type stewardStateOut struct {
	Scope         string             `json:"scope"`
	AgentID       string             `json:"agent_id,omitempty"`
	State         string             `json:"state"`
	CurrentAction *stewardAction     `json:"current_action,omitempty"`
	Handoff       *stewardHandoff    `json:"handoff,omitempty"`
}

type stewardAction struct {
	Kind          string         `json:"kind"`
	Target        map[string]any `json:"target,omitempty"`
	StartedAt     string         `json:"started_at,omitempty"`
	ExpectedUntil string         `json:"expected_until,omitempty"`
}

type stewardHandoff struct {
	FromScope  string `json:"from_scope"`
	ToScope    string `json:"to_scope"`
	ToAgentID  string `json:"to_agent_id,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	StartedAt  string `json:"started_at"`
}

// State derivation thresholds. Tuned for demo cadence: a steward that
// emitted an event in the last 60s is "still working" for the strip's
// poll loop (default 5s on mobile); a handoff in flight clears within
// 30s when the receiving steward responds.
const (
	stewardWorkingWindow  = 60 * time.Second
	stewardHandoffWindow  = 30 * time.Second
)

func (s *Server) handleGetStewardState(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")

	stewardAgentID, err := s.lookupProjectStewardAgent(r.Context(), team, project)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := stewardStateOut{Scope: "project"}
	if stewardAgentID == "" {
		out.State = "not-spawned"
		w.Header().Set("Cache-Control", "private, no-cache")
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.AgentID = stewardAgentID

	out.State, out.CurrentAction, out.Handoff = s.deriveStewardState(
		r.Context(), team, project, stewardAgentID)
	w.Header().Set("Cache-Control", "private, no-cache")
	writeJSON(w, http.StatusOK, out)
}

// lookupProjectStewardAgent resolves the agent powering a project's
// steward. Returns "" (no error) when the project exists but has no
// steward configured. Returns sql.ErrNoRows when the project itself
// doesn't exist for that team.
func (s *Server) lookupProjectStewardAgent(
	ctx context.Context, team, project string,
) (string, error) {
	var stewardNS sql.NullString
	row := s.db.QueryRowContext(ctx, `
		SELECT steward_agent_id
		FROM projects
		WHERE team_id = ? AND id = ?`, team, project)
	if err := row.Scan(&stewardNS); err != nil {
		return "", err
	}
	if !stewardNS.Valid {
		return "", nil
	}
	return stewardNS.String, nil
}

// deriveStewardState reads the agent + session + spawn + attention +
// agent_events tables to produce the strip's display state. Order of
// checks matters: handoff and error short-circuit; awaiting-director
// outranks active-session because demo flow expects an attention pip
// to win attention even when the session is mid-edit.
func (s *Server) deriveStewardState(
	ctx context.Context, team, project, stewardAgentID string,
) (string, *stewardAction, *stewardHandoff) {
	var status sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT status FROM agents WHERE id = ?`, stewardAgentID,
	).Scan(&status); err != nil {
		// Steward agent_id pointed at a row that no longer exists
		// (archived / cascaded delete during a fork). Surface as
		// not-spawned so the strip prompts to start a fresh one.
		return "not-spawned", nil, nil
	}
	switch status.String {
	case "pending":
		return "not-spawned", nil, nil
	case "paused", "stale", "terminated":
		return "error", nil, nil
	}

	if h := s.recentStewardHandoff(ctx, stewardAgentID); h != nil {
		return "handoff_in_progress", nil, h
	}

	if s.projectHasOpenAttention(ctx, project) {
		return "awaiting-director", nil, nil
	}

	if s.stewardHasActiveSession(ctx, stewardAgentID) {
		return "active-session", nil, nil
	}

	if s.stewardHasRunningChild(ctx, stewardAgentID) {
		return "worker-dispatched", nil, nil
	}

	if action := s.stewardCurrentAction(ctx, stewardAgentID); action != nil {
		return "working", action, nil
	}

	return "idle", nil, nil
}

func (s *Server) projectHasOpenAttention(ctx context.Context, project string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM attention_items
		WHERE project_id = ? AND status = 'open'`, project).Scan(&n)
	return n > 0
}

func (s *Server) stewardHasActiveSession(ctx context.Context, agentID string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sessions
		WHERE current_agent_id = ? AND status = 'open'`, agentID).Scan(&n)
	return n > 0
}

func (s *Server) stewardHasRunningChild(ctx context.Context, agentID string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM agent_spawns sp
		JOIN agents a ON a.id = sp.child_agent_id
		WHERE sp.parent_agent_id = ? AND a.status = 'running'`,
		agentID).Scan(&n)
	return n > 0
}

func (s *Server) stewardCurrentAction(
	ctx context.Context, agentID string,
) *stewardAction {
	cutoff := time.Now().UTC().Add(-stewardWorkingWindow).Format(time.RFC3339)
	var ts, kind string
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, kind
		FROM agent_events
		WHERE agent_id = ? AND ts >= ?
		ORDER BY ts DESC LIMIT 1`, agentID, cutoff).Scan(&ts, &kind)
	if err != nil {
		return nil
	}
	return &stewardAction{Kind: kind, StartedAt: ts}
}

// recentStewardHandoff peeks for a recent A2A invocation by the
// project steward toward another agent. The kind/payload shape is
// engine-side data — we only need the most recent envelope plus its
// timestamp to flag "handoff in progress" for the strip indicator.
func (s *Server) recentStewardHandoff(
	ctx context.Context, agentID string,
) *stewardHandoff {
	cutoff := time.Now().UTC().Add(-stewardHandoffWindow).Format(time.RFC3339)
	var ts, payload string
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, payload_json
		FROM agent_events
		WHERE agent_id = ?
		  AND ts >= ?
		  AND kind LIKE 'a2a.%'
		  AND kind NOT LIKE 'a2a.response%'
		ORDER BY ts DESC LIMIT 1`, agentID, cutoff).Scan(&ts, &payload)
	if err != nil {
		return nil
	}
	h := &stewardHandoff{
		FromScope: "project",
		ToScope:   "team",
		StartedAt: ts,
	}
	var doc map[string]any
	if json.Unmarshal([]byte(payload), &doc) == nil {
		if to, ok := doc["to_agent_id"].(string); ok {
			h.ToAgentID = to
		}
		if p, ok := doc["purpose"].(string); ok {
			h.Purpose = p
		}
	}
	if h.Purpose == "" {
		h.Purpose = "consulting_general_steward"
	}
	return h
}
