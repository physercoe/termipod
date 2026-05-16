package server

import (
	"context"
	"database/sql"
	"errors"

	"github.com/termipod/hub/internal/auth"
)

// StopSessionOpts shapes the optional knobs every "stop one session"
// caller needs. ADR-028 W2.5: the mobile PATCH-agent path, the
// hub-server shutdown-all fleet command, and any future ops surface
// share this helper so the audit trail and side-effects stay aligned.
type StopSessionOpts struct {
	// ForceKill propagates SIGKILL through the enqueued host-runner
	// terminate command instead of the default SIGTERM+grace. Used by
	// `hub-server shutdown-all --force-kill` when an agent is stuck.
	ForceKill bool

	// Reason is recorded on the session.stop audit row's meta. Surfaces
	// in the activity feed so "who/why" is grep-able after the fact.
	// Empty falls back to "stop".
	Reason string
}

// stopSessionInternal is the load-bearing "stop one session" path. Side
// effects (idempotent where noted):
//
//  1. The session's current agent flips to status=terminated.
//  2. The session flips to status=paused (if it was active).
//  3. The agent's MCP bearer tokens are revoked.
//  4. A host-runner `terminate` command is enqueued for the agent's
//     pane, carrying ForceKill into the args.
//  5. Two audit rows: session.stop (new in W2.5) + agent.terminate
//     (existing — the user-facing agent operation stays visible).
//  6. The ADR-029 task auto-derive runs against the agent's terminal
//     transition; tasks linked via the most-recent spawn move to
//     status=done.
//
// Returns the affected agent_id (empty if the session had no current
// agent — a legitimate no-op for already-paused sessions). The session
// row itself is preserved so it can be resumed via the existing
// /v1/teams/{team}/sessions/{id}/resume route.
func (s *Server) stopSessionInternal(ctx context.Context, team, sessionID string, opts StopSessionOpts) (string, error) {
	var (
		agentID    sql.NullString
		hostID     sql.NullString
		paneID     sql.NullString
		handle     string
		agentState string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.current_agent_id,
		       a.host_id,
		       a.pane_id,
		       COALESCE(a.handle, ''),
		       COALESCE(a.status, '')
		  FROM sessions s
		  LEFT JOIN agents a ON a.id = s.current_agent_id
		 WHERE s.team_id = ? AND s.id = ?`, team, sessionID).
		Scan(&agentID, &hostID, &paneID, &handle, &agentState)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !agentID.Valid || agentID.String == "" {
		// Session with no live agent — already paused or never spawned.
		// Still write the session.stop audit so the fleet log shows the
		// operator intent.
		s.recordAudit(ctx, team, "session.stop", "session", sessionID,
			summaryStop("", opts.Reason),
			map[string]any{"reason": opts.Reason, "force_kill": opts.ForceKill})
		return "", nil
	}
	aid := agentID.String
	now := NowUTC()

	// 1. Agent → terminated (idempotent: skip if already in a terminal state).
	terminal := agentState == "terminated" || agentState == "crashed" || agentState == "failed"
	if !terminal {
		_, err = s.db.ExecContext(ctx,
			`UPDATE agents SET status = 'terminated', terminated_at = ?
			  WHERE team_id = ? AND id = ?`, now, team, aid)
		if err != nil {
			return aid, err
		}
	}

	// 2. Session → paused (idempotent via the active-only WHERE).
	_, err = s.db.ExecContext(ctx, `
		UPDATE sessions
		   SET status = 'paused', last_active_at = ?
		 WHERE team_id = ? AND current_agent_id = ? AND status = 'active'`,
		now, team, aid)
	if err != nil {
		return aid, err
	}

	// 3. MCP bearer revoke (no-op if there were no tokens).
	_, _ = auth.RevokeAgentTokens(ctx, s.db, aid, now)

	// 4. Host-runner terminate command. force_kill rides in args so the
	// host-side terminate handler can escalate straight to SIGKILL.
	if hostID.Valid && hostID.String != "" {
		args := map[string]any{"pane_id": paneID.String}
		if opts.ForceKill {
			args["force_kill"] = true
		}
		_, _ = s.enqueueHostCommand(ctx, hostID.String, aid, "terminate", args)
	}

	// 5. Audit rows. session.stop is the new W2.5 row; agent.terminate
	// keeps the existing wire so feeds that grep on it stay correct.
	s.recordAudit(ctx, team, "session.stop", "session", sessionID,
		summaryStop(handle, opts.Reason),
		map[string]any{
			"agent_id":   aid,
			"handle":     handle,
			"reason":     opts.Reason,
			"force_kill": opts.ForceKill,
		})
	s.recordAudit(ctx, team, "agent.terminate", "agent", aid,
		"terminate "+handle,
		map[string]any{"handle": handle})

	// 6. ADR-029 task auto-derive. The flip we just made is "terminated"
	// so the linked task (if any) moves to done.
	_ = s.deriveTaskStatusFromAgent(ctx, team, aid, "terminated")

	return aid, nil
}

// summaryStop builds the human-readable audit summary line. Keeps
// "stop session @handle: reason" when both are present, falls back
// gracefully when one is empty.
func summaryStop(handle, reason string) string {
	out := "stop session"
	if handle != "" {
		out += " " + handle
	}
	if reason != "" {
		out += ": " + reason
	}
	return out
}
