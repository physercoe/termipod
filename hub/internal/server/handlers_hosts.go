package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO hosts (id, team_id, name, status, last_seen_at, capabilities_json, ssh_hint_json, created_at)
		VALUES (?, ?, ?, 'online', ?, ?, ?, ?)
		ON CONFLICT(team_id, name) DO UPDATE SET
		    status = 'online',
		    last_seen_at = excluded.last_seen_at,
		    capabilities_json = excluded.capabilities_json,
		    ssh_hint_json = COALESCE(excluded.ssh_hint_json, hosts.ssh_hint_json)`,
		id, team, in.Name, now, caps, hintArg, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
		       ssh_hint_json, capabilities_probed_at, created_at
		FROM hosts WHERE team_id = ? ORDER BY created_at`, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []hostOut{}
	for rows.Next() {
		var h hostOut
		var lastSeen, hint, probed sql.NullString
		var caps string
		if err := rows.Scan(&h.ID, &h.TeamID, &h.Name, &h.Status, &lastSeen, &caps, &hint, &probed, &h.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
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
		out = append(out, h)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHostHeartbeat updates last_seen_at and keeps status = online.
// Called every ~10s by the host-runner loop.
func (s *Server) handleHostHeartbeat(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE hosts SET status='online', last_seen_at = ?
		WHERE team_id = ? AND id = ?`, NowUTC(), team, host)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteHost removes a host row. Refuses if any agents on this host
// are still alive (status not in terminated/failed) — otherwise those rows
// would silently lose their host_id via the ON DELETE SET NULL edge and
// confuse the org chart. host_commands cascade-delete.
func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")
	var alive int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM agents
		WHERE team_id = ? AND host_id = ?
		  AND status NOT IN ('terminated','failed')`, team, host).Scan(&alive)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if alive > 0 {
		writeErr(w, http.StatusConflict,
			"host still has active agents — terminate them first")
		return
	}
	var name string
	_ = s.db.QueryRowContext(r.Context(),
		`SELECT name FROM hosts WHERE team_id = ? AND id = ?`, team, host).Scan(&name)
	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM hosts WHERE team_id = ? AND id = ?`, team, host)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	s.recordAudit(r.Context(), team, "host.delete", "host", host,
		summary, map[string]any{"name": name})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetHost(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")
	var h hostOut
	var lastSeen, hint, probed sql.NullString
	var caps string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, team_id, name, status, last_seen_at, capabilities_json,
		       ssh_hint_json, capabilities_probed_at, created_at
		FROM hosts WHERE team_id = ? AND id = ?`, team, host).Scan(
		&h.ID, &h.TeamID, &h.Name, &h.Status, &lastSeen, &caps, &hint, &probed, &h.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	res, err := s.db.ExecContext(r.Context(),
		`UPDATE hosts SET ssh_hint_json = ? WHERE team_id = ? AND id = ?`,
		hintArg, team, host)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	res, err := s.db.ExecContext(r.Context(),
		`UPDATE hosts SET capabilities_json = ?, capabilities_probed_at = ?
		 WHERE team_id = ? AND id = ?`,
		payload, NowUTC(), team, host)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
