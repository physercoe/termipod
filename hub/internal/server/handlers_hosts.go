package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

type hostIn struct {
	Name         string          `json:"name"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	// SSHHint is a raw JSON object string holding hostname, port, username, and
	// an optional jump_hint. It is *non-secret* — forbidden-pattern #15 (§7)
	// and the data-ownership law (§4) forbid storing passwords, private keys,
	// passphrases, tokens, or any secret material here. The handler runs a
	// belt-and-suspenders key-denylist check and rejects such payloads with
	// HTTP 400.
	SSHHint string `json:"ssh_hint_json,omitempty"`
}

type hostOut struct {
	ID                   string          `json:"id"`
	TeamID               string          `json:"team_id"`
	Name                 string          `json:"name"`
	Status               string          `json:"status"`
	LastSeenAt           *string         `json:"last_seen_at,omitempty"`
	Capabilities         json.RawMessage `json:"capabilities"`
	SSHHint              string          `json:"ssh_hint_json,omitempty"`
	CapabilitiesJSON     string          `json:"capabilities_json,omitempty"`
	CapabilitiesProbedAt string          `json:"capabilities_probed_at,omitempty"`
	CreatedAt            string          `json:"created_at"`
	// Build metadata reported by the host-runner via the heartbeat body
	// (HeartbeatIn). Empty when the host hasn't heartbeated since the
	// host-runner build that started reporting these (v1.0.261+) or when
	// the binary was built outside a git tree.
	RunnerCommit    string `json:"runner_commit,omitempty"`
	RunnerBuildTime string `json:"runner_build_time,omitempty"`
	RunnerModified  bool   `json:"runner_modified,omitempty"`
}

// heartbeatIn mirrors hostrunner.HeartbeatIn. Duplicated here (rather than
// imported) because hub server packages must not depend on host-runner
// packages — keeps the build dependency one-way.
type heartbeatIn struct {
	RunnerCommit    string `json:"runner_commit,omitempty"`
	RunnerBuildTime string `json:"runner_build_time,omitempty"`
	RunnerModified  bool   `json:"runner_modified,omitempty"`
}

// sshHintSecretKeys is the belt-and-suspenders denylist for ssh_hint_json.
// Matched case-insensitively against every top-level key in the parsed object.
// Host-runner and the mobile app must never submit these, but we defend
// against mistakes here rather than silently absorbing a leaked secret.
var sshHintSecretKeys = []string{
	"password", "private_key", "privatekey", "passphrase", "secret", "token",
}

// validateSSHHint parses hint (expected to be a JSON object, empty string is
// allowed and treated as "no hint"). It returns the canonical JSON form to
// store, or a non-nil error if the hint is unparseable or contains a
// denylisted key.
func validateSSHHint(hint string) (string, error) {
	h := strings.TrimSpace(hint)
	if h == "" {
		return "", nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(h), &obj); err != nil {
		return "", errors.New("ssh_hint_json must be a JSON object")
	}
	for k := range obj {
		lk := strings.ToLower(k)
		for _, deny := range sshHintSecretKeys {
			if lk == deny {
				return "", errors.New(
					"SSH secrets must not be stored in hub; use ssh_hint_json for non-secret hints only (rejected key: " + k + ")")
			}
		}
	}
	return h, nil
}

// handleRegisterHost creates a host record. Host-agents call this on boot
// with their owner token; subsequent heartbeats use the returned host id.
func (s *Server) handleRegisterHost(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in hostIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	caps := "{}"
	if len(in.Capabilities) > 0 {
		caps = string(in.Capabilities)
	}
	hint, err := validateSSHHint(in.SSHHint)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := NewID()
	now := NowUTC()
	// Upsert on (team_id, name): a host-runner that crashes and restarts
	// should re-bind to its existing row rather than 409'ing. The returned
	// id is whichever row now exists, old or new.
	var hintArg any
	if hint == "" {
		hintArg = nil
	} else {
		hintArg = hint
	}
	_, err = s.writeDB.ExecContext(r.Context(), `
		INSERT INTO hosts (id, team_id, name, status, last_seen_at, capabilities_json, ssh_hint_json, created_at)
		VALUES (?, ?, ?, 'online', ?, ?, ?, ?)
		ON CONFLICT(team_id, name) DO UPDATE SET
		    status = 'online',
		    last_seen_at = excluded.last_seen_at,
		    capabilities_json = excluded.capabilities_json,
		    ssh_hint_json = COALESCE(excluded.ssh_hint_json, hosts.ssh_hint_json)`,
		id, team, in.Name, now, caps, hintArg, now)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	// Always read back: on conflict, the real id is the existing row's.
	var (
		outID      string
		createdAt  string
		storedHint sql.NullString
		probedAt   sql.NullString
		storedCaps string
	)
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT id, created_at, capabilities_json, ssh_hint_json, capabilities_probed_at
		 FROM hosts WHERE team_id = ? AND name = ?`,
		team, in.Name).Scan(&outID, &createdAt, &storedCaps, &storedHint, &probedAt); err != nil {
		s.writeDBErr(w, err)
		return
	}
	out := hostOut{
		ID: outID, TeamID: team, Name: in.Name, Status: "online",
		LastSeenAt:       &now,
		Capabilities:     json.RawMessage(storedCaps),
		CapabilitiesJSON: storedCaps,
		CreatedAt:        createdAt,
	}
	if storedHint.Valid {
		out.SSHHint = storedHint.String
	}
	if probedAt.Valid {
		out.CapabilitiesProbedAt = probedAt.String
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, team_id, name, status, last_seen_at, capabilities_json,
		       ssh_hint_json, capabilities_probed_at, created_at,
		       runner_commit, runner_build_time, runner_modified
		FROM hosts WHERE team_id = ? ORDER BY created_at`, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	out := []hostOut{}
	for rows.Next() {
		var h hostOut
		var lastSeen, hint, probed, runnerCommit, runnerBuild sql.NullString
		var runnerMod sql.NullInt64
		var caps string
		if err := rows.Scan(&h.ID, &h.TeamID, &h.Name, &h.Status, &lastSeen, &caps,
			&hint, &probed, &h.CreatedAt,
			&runnerCommit, &runnerBuild, &runnerMod); err != nil {
			s.writeDBErr(w, err)
			return
		}
		if lastSeen.Valid {
			h.LastSeenAt = &lastSeen.String
		}
		h.Capabilities = json.RawMessage(caps)
		h.CapabilitiesJSON = caps
		if hint.Valid {
			h.SSHHint = hint.String
		}
		if probed.Valid {
			h.CapabilitiesProbedAt = probed.String
		}
		if runnerCommit.Valid {
			h.RunnerCommit = runnerCommit.String
		}
		if runnerBuild.Valid {
			h.RunnerBuildTime = runnerBuild.String
		}
		if runnerMod.Valid && runnerMod.Int64 != 0 {
			h.RunnerModified = true
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHostHeartbeat updates last_seen_at and keeps status = online.
// Called every ~10s by the host-runner loop. Optional JSON body carries
// host-runner build metadata (commit / build_time / modified) so the
// hub can show "host-runner is at commit X" without a separate endpoint.
// Empty body is fine — older host-runners still heartbeat.
func (s *Server) handleHostHeartbeat(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")

	var in heartbeatIn
	if r.Body != nil {
		// Tolerate empty body, malformed body, and no Content-Type — older
		// host-runners send no body at all. We only persist build info when
		// we successfully parsed it.
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		if len(body) > 0 {
			_ = json.Unmarshal(body, &in)
		}
	}

	now := NowUTC()
	var res sql.Result
	var err error
	if in.RunnerCommit != "" || in.RunnerBuildTime != "" {
		modified := 0
		if in.RunnerModified {
			modified = 1
		}
		res, err = s.writeDB.ExecContext(r.Context(), `
			UPDATE hosts SET status='online', last_seen_at = ?,
			    runner_commit = ?, runner_build_time = ?, runner_modified = ?
			WHERE team_id = ? AND id = ?`,
			now, in.RunnerCommit, in.RunnerBuildTime, modified, team, host)
	} else {
		res, err = s.writeDB.ExecContext(r.Context(), `
			UPDATE hosts SET status='online', last_seen_at = ?
			WHERE team_id = ? AND id = ?`, now, team, host)
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteHost removes a host row, and answers the question "what
// happens to the agents that were on it?" differently depending on whether
// anyone is still home:
//
//   - host ONLINE with live agents → 409. The host-runner is right there and
//     can stop them properly; deleting underneath it would strand real
//     processes and null out their host_id via the ON DELETE SET NULL edge,
//     leaving running agents that claim to live nowhere.
//
//   - host OFFLINE with live agents → the agents are ORPHANED, so reap them
//     to 'crashed' and proceed. Nothing can reach them: the hub only ever
//     talks to an agent through its host-runner, and the queued host_commands
//     that a stop would enqueue cascade-delete with this row anyway. Refusing
//     here used to be a dead end — the fleet view showed agents "running" on
//     a machine that no longer exists, and the only way to clear them was to
//     stop each one by hand first. The director's DELETE *is* the judgement
//     that the machine is gone; a status of 'running' on a host we are about
//     to erase is a lie either way, and 'crashed' is the honest reading.
//
// Deliberately NOT a background timer: an offline host is not proof of a dead
// agent. M4 agents live in tmux panes that outlive a host-runner restart
// (tmux is a separate server — see hostrunner/reconcile.go), so an agent can
// be perfectly healthy while its runner is being upgraded. A reaper on a
// clock would kill those, and irreversibly: tickReconcile skips agents in a
// terminal status, so a wrong 'crashed' verdict never heals back to running
// and takes the driver down with it. Reaping only on an explicit delete keeps
// the call where the evidence is — with the human who knows the box is gone.
func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")

	var name, status string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT name, status FROM hosts WHERE team_id = ? AND id = ?`,
		team, host).Scan(&name, &status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}

	live, err := s.liveAgentsOnHost(r.Context(), team, host)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if len(live) > 0 && status == "online" {
		writeErr(w, http.StatusConflict,
			"host still has active agents — terminate them first")
		return
	}
	reaped := make([]string, 0, len(live))
	for _, ag := range live {
		if err := s.reapOrphanedAgent(r.Context(), team, ag.ID); err != nil {
			s.log.Warn("reap orphaned agent failed",
				"agent", ag.ID, "host", host, "err", err)
			continue
		}
		reaped = append(reaped, ag.Handle)
	}

	res, err := s.writeDB.ExecContext(r.Context(),
		`DELETE FROM hosts WHERE team_id = ? AND id = ?`, team, host)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	summary := "delete host"
	if name != "" {
		summary = "delete host " + name
	}
	if len(reaped) > 0 {
		summary += " (orphaned " + strconv.Itoa(len(reaped)) + " agent(s))"
	}
	s.recordAudit(r.Context(), team, "host.delete", "host", host, summary,
		map[string]any{"name": name, "status": status, "orphaned_agents": reaped})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetHost(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")
	var h hostOut
	var lastSeen, hint, probed, runnerCommit, runnerBuild sql.NullString
	var runnerMod sql.NullInt64
	var caps string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, team_id, name, status, last_seen_at, capabilities_json,
		       ssh_hint_json, capabilities_probed_at, created_at,
		       runner_commit, runner_build_time, runner_modified
		FROM hosts WHERE team_id = ? AND id = ?`, team, host).Scan(
		&h.ID, &h.TeamID, &h.Name, &h.Status, &lastSeen, &caps, &hint, &probed, &h.CreatedAt,
		&runnerCommit, &runnerBuild, &runnerMod)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if lastSeen.Valid {
		h.LastSeenAt = &lastSeen.String
	}
	h.Capabilities = json.RawMessage(caps)
	h.CapabilitiesJSON = caps
	if hint.Valid {
		h.SSHHint = hint.String
	}
	if probed.Valid {
		h.CapabilitiesProbedAt = probed.String
	}
	if runnerCommit.Valid {
		h.RunnerCommit = runnerCommit.String
	}
	if runnerBuild.Valid {
		h.RunnerBuildTime = runnerBuild.String
	}
	if runnerMod.Valid && runnerMod.Int64 != 0 {
		h.RunnerModified = true
	}
	writeJSON(w, http.StatusOK, h)
}

// handleUpdateHostSSHHint accepts a PATCH body of {"ssh_hint_json": "..."} and
// overwrites the host's non-secret SSH hint. The key-denylist check (see
// validateSSHHint) rejects payloads whose hint object contains password,
// private_key, passphrase, secret, or token — enforcing the data-ownership
// law (§4) belt-and-suspenders style.
func (s *Server) handleUpdateHostSSHHint(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")
	var body struct {
		SSHHint string `json:"ssh_hint_json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	hint, err := validateSSHHint(body.SSHHint)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var hintArg any
	if hint == "" {
		hintArg = nil
	} else {
		hintArg = hint
	}
	res, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE hosts SET ssh_hint_json = ? WHERE team_id = ? AND id = ?`,
		hintArg, team, host)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUpdateHostCapabilities is the endpoint the host-runner calls on every
// capability probe (typically piggy-backed on heartbeat). The body is treated
// as an opaque JSON string — the hub does not schema-validate agent-binary
// presence or mode lists; that is the UI's job (§5.3.2). capabilities_probed_at
// is stamped server-side so clients never have to supply a timestamp.
func (s *Server) handleUpdateHostCapabilities(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	payload := strings.TrimSpace(string(raw))
	if payload == "" {
		payload = "{}"
	}
	// Parse-but-re-serialise: we accept any valid JSON value (object or
	// array) but reject garbage so the column never holds malformed data.
	var probe any
	if err := json.Unmarshal([]byte(payload), &probe); err != nil {
		writeErr(w, http.StatusBadRequest, "capabilities_json must be valid JSON")
		return
	}
	res, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE hosts SET capabilities_json = ?, capabilities_probed_at = ?
		 WHERE team_id = ? AND id = ?`,
		payload, NowUTC(), team, host)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
