package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// P3.3a — A2A agent-card directory.
//
// Host-runners sit behind NAT with no public address; only the hub does.
// The relay design (P3.3) has the hub serve all externally-visible A2A
// endpoints, proxying them through an outbound tunnel to the host-runner
// that owns each agent. Before the tunnel lands, the hub must at least
// know which host hosts which agent so the steward can discover workers
// by handle. That is the job of this directory.
//
// Protocol:
//   - PUT  /v1/teams/{team}/hosts/{host}/a2a/cards
//       body: {"cards":[{"agent_id":"...","handle":"...","card":{...}}, ...]}
//       Replaces the host's entire card set atomically.
//   - GET  /v1/teams/{team}/a2a/cards?handle=worker.ml
//       Returns matching cards across all hosts.
//
// The card_json payload is stored verbatim; once the relay lands the hub
// will rewrite the `url` field to point at its own /a2a/relay/... endpoint.
// Consumers today should route via host_id + agent_id.

type a2aCardIn struct {
	AgentID string          `json:"agent_id"`
	Handle  string          `json:"handle"`
	Card    json.RawMessage `json:"card"`
}

type a2aCardsPutIn struct {
	Cards []a2aCardIn `json:"cards"`
}

type a2aCardOut struct {
	HostID       string          `json:"host_id"`
	AgentID      string          `json:"agent_id"`
	Handle       string          `json:"handle"`
	Card         json.RawMessage `json:"card"`
	RegisteredAt string          `json:"registered_at"`
}

// handlePutHostA2ACards replaces the entire card set for one host. Any
// agents previously registered under this host that aren't in the payload
// get dropped — the host is authoritative for its own directory entry.
func (s *Server) handlePutHostA2ACards(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")

	var in a2aCardsPutIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	for _, c := range in.Cards {
		if c.AgentID == "" {
			writeErr(w, http.StatusBadRequest, "cards[].agent_id required")
			return
		}
		if c.Handle == "" {
			writeErr(w, http.StatusBadRequest, "cards[].handle required")
			return
		}
		if len(c.Card) == 0 {
			writeErr(w, http.StatusBadRequest, "cards[].card required")
			return
		}
		if !json.Valid(c.Card) {
			writeErr(w, http.StatusBadRequest, "cards[].card must be valid JSON")
			return
		}
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	// Confirm host belongs to team — protect against cross-team writes.
	var existing string
	err = tx.QueryRowContext(r.Context(),
		`SELECT id FROM hosts WHERE team_id = ? AND id = ?`, team, host).Scan(&existing)
	if err != nil {
		writeErr(w, http.StatusNotFound, "host not found in team")
		return
	}

	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM a2a_cards WHERE team_id = ? AND host_id = ?`, team, host); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := NowUTC()
	for _, c := range in.Cards {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO a2a_cards (id, team_id, host_id, agent_id, handle, card_json, registered_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			NewID(), team, host, c.AgentID, c.Handle, string(c.Card), now); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(in.Cards)})
}

// handleListTeamA2ACards returns cards across all hosts in the team.
// Supports ?handle=<handle> to filter (steward calls this to find workers).
func (s *Server) handleListTeamA2ACards(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	handle := r.URL.Query().Get("handle")

	query := `SELECT host_id, agent_id, handle, card_json, registered_at
	          FROM a2a_cards WHERE team_id = ?`
	args := []any{team}
	if handle != "" {
		query += ` AND handle = ?`
		args = append(args, handle)
	}
	query += ` ORDER BY host_id, agent_id`

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []a2aCardOut{}
	for rows.Next() {
		var (
			hostID, agentID, h, cardJSON, regAt string
		)
		if err := rows.Scan(&hostID, &agentID, &h, &cardJSON, &regAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, a2aCardOut{
			HostID:       hostID,
			AgentID:      agentID,
			Handle:       h,
			Card:         json.RawMessage(cardJSON),
			RegisteredAt: regAt,
		})
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

