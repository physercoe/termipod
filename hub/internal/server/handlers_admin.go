package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/termipod/hub/internal/selfupdate"
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

// handleAdminFleetShutdown is POST /v1/admin/fleet/shutdown — fires
// host.shutdown (exit 0) so each host-runner stays DOWN per ADR-028
// D-2. handleAdminFleetRestart is POST /v1/admin/fleet/restart —
// fires host.restart (exit 75) so systemd respawns each host-runner
// with the same binary (plan W11). Both share fleetStopVerb.
func (s *Server) handleAdminFleetShutdown(w http.ResponseWriter, r *http.Request) {
	s.fleetStopVerb(w, r, "host.shutdown")
}

func (s *Server) handleAdminFleetRestart(w http.ResponseWriter, r *http.Request) {
	s.fleetStopVerb(w, r, "host.restart")
}

// fleetStopVerb is the shared shutdown-all / restart-all orchestrator.
// Owner-scope. Enumerates live hosts, stops every active session on
// each (sharing stopSessionInternal with the mobile-Stop path), then
// fires the given control verb. The verb is the only difference:
// host.shutdown makes the runner exit 0 (systemd leaves it DOWN),
// host.restart makes it exit 75 (systemd respawns it).
//
// Hub-server stays up after this returns either way. Sessions are left
// at status=paused and remain resumable via the existing
// POST /v1/teams/{team}/sessions/{id}/resume route — once the host is
// back (manually for shutdown, automatically for restart).
func (s *Server) fleetStopVerb(w http.ResponseWriter, r *http.Request, verb string) {
	if !s.requireOperator(w, r) {
		return
	}
	action := strings.TrimPrefix(verb, "host.") // "shutdown" | "restart"
	var in AdminFleetShutdownRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	if in.Reason == "" {
		in.Reason = action + "-all"
	}

	hosts, err := s.listLiveHosts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := AdminFleetShutdownResponse{Hosts: make([]AdminFleetHostResult, 0, len(hosts))}
	for _, h := range hosts {
		out.Hosts = append(out.Hosts, s.stopOneHost(r.Context(), verb, h, in))
	}

	writeJSON(w, http.StatusOK, out)
}

// stopOneHost fires one host.shutdown / host.restart verb at a single
// host: stops its active sessions, sends the verb, records the audit
// row, and returns the per-host result. Shared by fleetStopVerb (the
// fleet fan-out) and adminHostStopVerb (the per-host Phase 5 route) so
// both produce an identical audit trail and result shape.
func (s *Server) stopOneHost(ctx context.Context, verb string, h hostRow, in AdminFleetShutdownRequest) AdminFleetHostResult {
	action := strings.TrimPrefix(verb, "host.") // "shutdown" | "restart"
	res := AdminFleetHostResult{HostID: h.id, TeamID: h.teamID, HostName: h.name}

	// 1. Stop every active session on this host. We share
	// stopSessionInternal with the mobile path so the audit trail
	// for each session matches what a manual mobile-Stop would
	// produce: session.stop + agent.terminate audits, MCP revoke,
	// terminate host-command enqueued (the host runner will see it
	// before the verb fires the exit).
	sessionIDs, err := s.listActiveSessionIDsOnHost(ctx, h.teamID, h.id)
	if err != nil {
		res.Error = "list sessions: " + err.Error()
		return res
	}
	for _, sid := range sessionIDs {
		_, _ = s.stopSessionInternal(ctx, h.teamID, sid, StopSessionOpts{
			ForceKill: in.ForceKill,
			Reason:    in.Reason,
		})
	}
	res.SessionsStopped = len(sessionIDs)

	// 2. Fire the control verb via the tunnel queue. The verb posts
	// an ack BEFORE exiting (host-runner schedules os.Exit on a
	// delayed goroutine), so a non-error response here means "the
	// host has accepted the verb and will exit imminently".
	payload, _ := json.Marshal(map[string]any{
		"reason":     in.Reason,
		"force_kill": in.ForceKill,
	})
	ackTimeout := 60 * time.Second
	if in.NoWait {
		ackTimeout = 1 * time.Second
	}
	ackCtx, cancel := context.WithTimeout(ctx, ackTimeout)
	resp, verbErr := s.tunnel.enqueueHostVerb(ackCtx, h.id, verb, payload)
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
	// because the operator intent is what auditors care about, not
	// just successful acks.
	s.recordAudit(ctx, h.teamID, verb, "host", h.id,
		action+" host "+firstNonEmpty(h.name, h.id),
		map[string]any{
			"reason":           in.Reason,
			"force_kill":       in.ForceKill,
			"sessions_stopped": res.SessionsStopped,
			"acked":            res.Acked,
			"no_wait":          in.NoWait,
		})
	return res
}

// hostRow is the projection of `hosts` shutdown-all iterates over.
type hostRow struct {
	id, teamID, name string
}

// listLiveHosts returns the hosts that have heartbeated in the last 5
// minutes. Hosts that are already down (or have never registered) get
// filtered out — there's nothing to send a verb to. The order is
// stable (team, name) so the CLI's per-host progress output is
// reproducible. Shared by shutdown-all and update-all.
func (s *Server) listLiveHosts(ctx context.Context) ([]hostRow, error) {
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

// hubExit and hubSelfUpdateDelay are package-level seams for the
// scheduled hub self-update so tests can drive it without terminating
// the test process.
var (
	hubExit            = os.Exit
	hubSelfUpdateDelay = 500 * time.Millisecond
)

// AdminFleetUpdateRequest is the wire shape for the update-all
// orchestrator (ADR-028 D-2 / plan W9). JSON-only.
type AdminFleetUpdateRequest struct {
	Target       string `json:"target,omitempty"`        // hosts | hub | both; default both
	Version      string `json:"version,omitempty"`       // explicit release tag; overrides Channel
	Channel      string `json:"channel,omitempty"`       // stable | alpha
	UpstreamRepo string `json:"upstream_repo,omitempty"` // owner/name; default physercoe/termipod
	DryRun       bool   `json:"dry_run,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// AdminFleetUpdateHostResult is the per-host update outcome row.
type AdminFleetUpdateHostResult struct {
	HostID      string `json:"host_id"`
	TeamID      string `json:"team_id"`
	HostName    string `json:"host_name,omitempty"`
	FromVersion string `json:"from_version,omitempty"`
	ToVersion   string `json:"to_version,omitempty"`
	Acked       bool   `json:"acked"`
	WouldUpdate bool   `json:"would_update,omitempty"` // dry-run only
	Error       string `json:"error,omitempty"`
}

// AdminFleetUpdateResponse is the synchronous summary the CLI prints.
type AdminFleetUpdateResponse struct {
	Target  string                       `json:"target"`
	DryRun  bool                         `json:"dry_run"`
	Hosts   []AdminFleetUpdateHostResult `json:"hosts"`
	HubNote string                       `json:"hub_note,omitempty"`
}

// handleAdminFleetUpdate is the POST /v1/admin/fleet/update handler.
// Owner-scope. Fans the host.update verb out to every live host, then
// — when the host phase is clean and the hub is in scope — schedules
// the hub's own self-update (ADR-028 D-2 / plan W9).
//
// The hub self-update runs on a delayed goroutine so this HTTP
// response posts before the daemon replaces its binary and exits 75;
// the CLI therefore reports the host outcomes but not the hub's — the
// operator confirms the hub with `hub-server version` once it respawns.
func (s *Server) handleAdminFleetUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(w, r) {
		return
	}
	var in AdminFleetUpdateRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	switch in.Target {
	case "":
		in.Target = "both"
	case "hosts", "hub", "both":
		// ok
	default:
		writeErr(w, http.StatusBadRequest, "target must be hosts|hub|both")
		return
	}
	if in.Reason == "" {
		in.Reason = "update-all"
	}

	out := AdminFleetUpdateResponse{
		Target: in.Target,
		DryRun: in.DryRun,
		Hosts:  []AdminFleetUpdateHostResult{},
	}

	hostErrors := 0
	if in.Target == "hosts" || in.Target == "both" {
		hosts, err := s.listLiveHosts(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, h := range hosts {
			res := s.updateOneHost(r.Context(), h, in)
			if res.Error != "" {
				hostErrors++
			}
			out.Hosts = append(out.Hosts, res)
		}
	}

	// Hub self-update. Skipped on dry-run, and skipped when a host
	// errored — don't bounce the hub onto a new version while part of
	// the fleet is stuck on the old one (plan W9: "after all hosts
	// succeed").
	wantHub := in.Target == "hub" || in.Target == "both"
	switch {
	case !wantHub:
		// host-only run; nothing to note.
	case in.DryRun:
		out.HubNote = "dry run: hub-server would self-update and exit 75 (systemd respawn)"
	case hostErrors > 0:
		out.HubNote = fmt.Sprintf("hub self-update SKIPPED: %d host(s) errored — "+
			"re-run once the fleet is consistent", hostErrors)
	default:
		out.HubNote = "hub self-update scheduled: the daemon will download, verify, " +
			"and exit 75 to respawn on the new binary — confirm with " +
			"`hub-server version` after it comes back"
		s.scheduleHubSelfUpdate(in)
	}

	writeJSON(w, http.StatusOK, out)
}

// scheduleHubSelfUpdate runs the hub-server self-update on a detached
// goroutine after a short delay, so the update-all HTTP response posts
// before the daemon replaces its binary and exits 75. A failed
// self-update logs and leaves the hub running on the current binary.
func (s *Server) scheduleHubSelfUpdate(in AdminFleetUpdateRequest) {
	go func() {
		time.Sleep(hubSelfUpdateDelay)
		res, err := selfupdate.Run(context.Background(), selfupdate.Options{
			Binary:  "hub-server",
			Repo:    in.UpstreamRepo,
			Channel: in.Channel,
			Version: in.Version,
			Log:     s.log,
		})
		if err != nil {
			s.log.Error("hub self-update failed; staying on the current binary", "err", err)
			return
		}
		s.log.Info("hub self-update installed; exiting 75 for respawn",
			"from", res.FromVersion, "to", res.ToVersion)
		hubExit(75)
	}()
}

// updateOneHost fires one host.update verb at a single host: it
// fans the verb, decodes the from/to versions, records the audit
// row, and returns the per-host result. A dry-run skips the verb and
// just flags WouldUpdate. Shared by handleAdminFleetUpdate (the fleet
// fan-out) and adminHostUpdate (the per-host Phase 5 route).
func (s *Server) updateOneHost(ctx context.Context, h hostRow, in AdminFleetUpdateRequest) AdminFleetUpdateHostResult {
	res := AdminFleetUpdateHostResult{HostID: h.id, TeamID: h.teamID, HostName: h.name}
	if in.DryRun {
		res.WouldUpdate = true
		return res
	}
	payload, _ := json.Marshal(map[string]any{
		"version":       in.Version,
		"channel":       in.Channel,
		"upstream_repo": in.UpstreamRepo,
		"reason":        in.Reason,
	})
	// host.update blocks for the length of a download; allow a
	// generous ack window before giving up on the host.
	ackCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	resp, verbErr := s.tunnel.enqueueHostVerb(ackCtx, h.id, "host.update", payload)
	cancel()
	switch {
	case verbErr != nil:
		res.Error = "verb: " + verbErr.Error()
	case resp == nil:
		res.Error = "verb: no response"
	case resp.Status >= 200 && resp.Status < 300:
		res.Acked = true
		res.FromVersion, res.ToVersion = parseUpdateAck(resp.BodyB64)
	default:
		res.Error = fmt.Sprintf("verb: status %d — %s",
			resp.Status, updateAckError(resp.BodyB64))
	}
	s.recordAudit(ctx, h.teamID, "host.update", "host", h.id,
		"update host "+firstNonEmpty(h.name, h.id),
		map[string]any{
			"reason":       in.Reason,
			"acked":        res.Acked,
			"from_version": res.FromVersion,
			"to_version":   res.ToVersion,
			"error":        res.Error,
		})
	return res
}

// parseUpdateAck pulls the from/to version out of a host.update ack
// body (base64-encoded JSON). Missing fields yield empty strings.
func parseUpdateAck(bodyB64 string) (from, to string) {
	raw, err := base64.StdEncoding.DecodeString(bodyB64)
	if err != nil {
		return "", ""
	}
	var b struct {
		FromVersion string `json:"from_version"`
		ToVersion   string `json:"to_version"`
	}
	_ = json.Unmarshal(raw, &b)
	return b.FromVersion, b.ToVersion
}

// updateAckError pulls the error string out of a non-2xx host.update
// response body.
func updateAckError(bodyB64 string) string {
	raw, err := base64.StdEncoding.DecodeString(bodyB64)
	if err != nil {
		return ""
	}
	var b struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &b)
	return b.Error
}
