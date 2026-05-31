package server

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Team provisioning — the operator-gated onboarding surface (ADR-037
// D3). An operator creates a team and receives its first owner token;
// that (team_id, owner_token) pair is what a tester is handed. Per-team
// owners cannot reach this surface (requireOperator), so a tester cannot
// mint sibling teams.

type provisionTeamIn struct {
	TeamID string `json:"team_id"`
	Name   string `json:"name"`   // optional; defaults to team_id
	Handle string `json:"handle"` // optional owner display handle (Members tab)
}

type provisionTeamOut struct {
	TeamID       string `json:"team_id"`
	Name         string `json:"name"`
	OwnerToken   string `json:"owner_token"` // plaintext — shown once, never stored
	OwnerTokenID string `json:"owner_token_id"`
	CreatedAt    string `json:"created_at"`
}

func (s *Server) handleAdminCreateTeam(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(w, r) {
		return
	}
	var in provisionTeamIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	name := in.Name
	if name == "" {
		name = in.TeamID
	}
	token, tokenID, createdAt, err := ProvisionTeam(r.Context(), s.db, in.TeamID, in.Name, in.Handle)
	switch {
	case errors.Is(err, ErrTeamExists):
		writeErr(w, http.StatusConflict, "team already exists")
		return
	case errors.Is(err, ErrInvalidTeamID):
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Attribute the genesis event to the new team so its own audit trail
	// opens with its creation; the cross-team admin audit shows it too.
	s.recordAudit(r.Context(), in.TeamID, "team.create", "team", in.TeamID,
		"provision team "+in.TeamID,
		map[string]any{"name": name, "owner_token_id": tokenID})
	writeJSON(w, http.StatusCreated, provisionTeamOut{
		TeamID: in.TeamID, Name: name,
		OwnerToken: token, OwnerTokenID: tokenID, CreatedAt: createdAt,
	})
}

type teamOut struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleAdminListTeams(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(w, r) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, name, created_at FROM teams ORDER BY created_at`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []teamOut{}
	for rows.Next() {
		var t teamOut
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}
