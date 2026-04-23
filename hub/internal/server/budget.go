package server

import (
	"context"
	"database/sql"
	"encoding/json"
)

// accumulateSpend bumps agents.spent_cents and, if the agent has a
// budget_cents and has crossed it on this insert, enqueues a pause
// command to the agent's host and opens a synthetic attention item.
//
// We read the row back after the UPDATE so the "did we just cross the
// line" check sees the committed total — important when multiple events
// land concurrently.
func (s *Server) accumulateSpend(ctx context.Context, agentID string, deltaCents int) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE agents SET spent_cents = spent_cents + ? WHERE id = ?`,
		deltaCents, agentID); err != nil {
		s.log.Warn("accumulate spend failed", "agent", agentID, "err", err)
		return
	}
	var (
		hostID, paneID, pauseState, team, handle string
		budget                                   sql.NullInt64
		spent                                    int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT team_id, handle, COALESCE(host_id, ''), COALESCE(pane_id, ''),
		       pause_state, budget_cents, spent_cents
		FROM agents WHERE id = ?`, agentID).
		Scan(&team, &handle, &hostID, &paneID, &pauseState, &budget, &spent)
	if err != nil || !budget.Valid || pauseState == "paused" || hostID == "" {
		return
	}
	if int64(spent) < budget.Int64 {
		return
	}
	// Mark agent as paused locally and enqueue the host-side pause.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE agents SET pause_state = 'paused' WHERE id = ?`, agentID); err != nil {
		s.log.Warn("budget auto-pause update failed", "agent", agentID, "err", err)
	}
	if _, err := s.enqueueHostCommand(ctx, hostID, agentID, "pause",
		map[string]any{"pane_id": paneID, "reason": "budget_exceeded"}); err != nil {
		s.log.Warn("enqueue pause failed", "agent", agentID, "err", err)
	}
	// Raise an attention item so an operator sees why the agent went quiet.
	assignees, _ := json.Marshal([]string{"@principal"})
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, tier,
			current_assignees_json, status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULL, 'team', ?, 'budget_exceeded',
		          ?, 'major', 'moderate',
		          ?, 'open', ?,
		          'system', NULL)`,
		NewID(), team,
		"budget exceeded: "+handle+" paused",
		string(assignees), NowUTC())
}
