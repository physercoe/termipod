package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Admin endpoints back the ops CLI (`hub-server shutdown-all`,
// `update-all`, `restart-all`) and the future mobile Admin pane
// (ADR-028 D-3 / Phase 5). All routes are owner-scope and hub-wide;
// they live outside the per-team subtree because fleet operations span
// teams.

// AdminFleetShutdownRequest is the wire shape for the shutdown-all
// orchestrator. JSON-only.
type AdminFleetShutdownRequest struct {
	NoWait    bool   `json:"no_wait,omitempty"`
	ForceKill bool   `json:"force_kill,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// AdminFleetHostResult is the per-host outcome row.
type AdminFleetHostResult struct {
	HostID          string `json:"host_id"`
	TeamID          string `json:"team_id"`
	HostName        string `json:"host_name,omitempty"`
	SessionsStopped int    `json:"sessions_stopped"`
	Acked           bool   `json:"acked"`
	Error           string `json:"error,omitempty"`
}

// AdminFleetShutdownResponse is the synchronous summary the CLI prints.
type AdminFleetShutdownResponse struct {
	Hosts []AdminFleetHostResult `json:"hosts"`
}

// handleAdminFleetShutdown is the POST /v1/admin/fleet/shutdown handler.
// Owner-scope. Enumerates live hosts, stops every active session on
// each (sharing stopSessionInternal with the mobile-Stop path), then
// fires the host.shutdown verb so each host-runner exits 0 — systemd's
// Restart=on-failure leaves them DOWN per ADR-028 D-2.
//
// Hub-server stays up after this returns. Resuming a fleet means
// `systemctl start termipod-host@<id>` per host; the sessions left at
// status=paused are then resumable via the existing
// POST /v1/teams/{team}/sessions/{id}/resume route.
func (s *Server) handleAdminFleetShutdown(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	var in AdminFleetShutdownRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	if in.Reason == "" {
		in.Reason = "shutdown-all"
	}

	hosts, err := s.listLiveHostsForShutdown(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := AdminFleetShutdownResponse{Hosts: make([]AdminFleetHostResult, 0, len(hosts))}
	for _, h := range hosts {
		res := AdminFleetHostResult{HostID: h.id, TeamID: h.teamID, HostName: h.name}

		// 1. Stop every active session on this host. We share
		// stopSessionInternal with the mobile path so the audit trail
		// for each session matches what a manual mobile-Stop would
		// produce: session.stop + agent.terminate audits, MCP revoke,
		// terminate host-command enqueued (the host runner will see it
		// before the host.shutdown verb fires the exit).
		sessionIDs, err := s.listActiveSessionIDsOnHost(r.Context(), h.teamID, h.id)
		if err != nil {
			res.Error = "list sessions: " + err.Error()
			out.Hosts = append(out.Hosts, res)
			continue
		}
		for _, sid := range sessionIDs {
			_, _ = s.stopSessionInternal(r.Context(), h.teamID, sid, StopSessionOpts{
				ForceKill: in.ForceKill,
				Reason:    in.Reason,
			})
		}
		res.SessionsStopped = len(sessionIDs)

		// 2. Fire host.shutdown verb via the tunnel queue. The verb
		// posts an ack BEFORE exiting (host-runner schedules os.Exit
		// on a delayed goroutine), so a non-error response here means
		// "the host has accepted the verb and will exit imminently".
		payload, _ := json.Marshal(map[string]any{
			"reason":     in.Reason,
			"force_kill": in.ForceKill,
		})
		ackTimeout := 60 * time.Second
		if in.NoWait {
			ackTimeout = 1 * time.Second
		}
		ackCtx, cancel := context.WithTimeout(r.Context(), ackTimeout)
		resp, verbErr := s.tunnel.enqueueHostVerb(ackCtx, h.id, "host.shutdown", payload)
		cancel()
		switch {
		case verbErr != nil:
			res.Error = "verb: " + verbErr.Error()
		case resp == nil:
			res.Error = "verb: no response"
		case resp.Status >= 200 && resp.Status < 300:
			res.Acked = true
		default:
			res.Error = fmt.Sprintf("verb: status %d", resp.Status)
		}

		// 3. Per-host audit row. Survives even when the verb timed out
		// because the operator intent ("shutdown was requested") is
		// what auditors care about, not just successful acks.
		s.recordAudit(r.Context(), h.teamID, "host.shutdown", "host", h.id,
			"shutdown host "+firstNonEmpty(h.name, h.id),
			map[string]any{
				"reason":           in.Reason,
				"force_kill":       in.ForceKill,
				"sessions_stopped": res.SessionsStopped,
				"acked":            res.Acked,
				"no_wait":          in.NoWait,
			})
		out.Hosts = append(out.Hosts, res)
	}

	writeJSON(w, http.StatusOK, out)
}

// hostRow is the projection of `hosts` shutdown-all iterates over.
type hostRow struct {
	id, teamID, name string
}

// listLiveHostsForShutdown returns the hosts that have heartbeated in
// the last 5 minutes. Hosts that are already down (or have never
// registered) get filtered out — there's nothing to send the verb to.
// The order is stable (team, name) so the CLI's per-host progress
// output is reproducible.
func (s *Server) listLiveHostsForShutdown(ctx context.Context) ([]hostRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, team_id, COALESCE(name, '')
		  FROM hosts
		 WHERE last_seen_at IS NOT NULL
		   AND last_seen_at > datetime('now', '-5 minutes')
		 ORDER BY team_id, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hostRow
	for rows.Next() {
		var h hostRow
		if err := rows.Scan(&h.id, &h.teamID, &h.name); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// listActiveSessionIDsOnHost returns ids of sessions whose current
// agent is bound to the given host. Distinct because a session might
// briefly point at two agents during a resume race.
func (s *Server) listActiveSessionIDsOnHost(ctx context.Context, teamID, hostID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT s.id
		  FROM sessions s
		  JOIN agents a ON a.id = s.current_agent_id
		 WHERE s.team_id = ?
		   AND s.status = 'active'
		   AND a.host_id = ?`, teamID, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

