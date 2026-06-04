package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/termipod/hub/internal/auth"
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

type rotateTeamTokenOut struct {
	TeamID       string `json:"team_id"`
	NewToken     string `json:"new_token"` // plaintext — shown once, never stored
	NewTokenID   string `json:"new_token_id"`
	RevokedCount int    `json:"revoked_count"`
	CreatedAt    string `json:"created_at"`
}

// handleAdminRotateTeamToken is POST /v1/admin/teams/{team}/rotate-token —
// operator-gated. It mints a fresh `owner` token for the named team and
// revokes that team's prior owner tokens, returning the new plaintext
// once. Unlike host-token rotation there is no fleet broadcast: an owner
// token is held by a human director, so rotation is a local issue-then-
// revoke. The team's display handle is carried over from its most recent
// owner token so the Members tab is unchanged.
//
// `operator`-kind tokens are NEVER touched (the filter is kind='owner'),
// so rotating the `default` team — whose director is the operator token,
// with no separate owner minted at Init (init.go) — issues a dedicated
// owner token and revokes nothing. The hub root credential is preserved.
func (s *Server) handleAdminRotateTeamToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(w, r) {
		return
	}
	team := chi.URLParam(r, "team")

	// The team must exist — rotating a token for a phantom team is a 404,
	// not a silent mint.
	var exists string
	switch err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM teams WHERE id = ?`, team).Scan(&exists); {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, "team not found")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Gather the team's current non-revoked owner tokens. scope_json is
	// matched in Go (mirrors handleAdminTokensRotate) rather than via a
	// json_extract that would couple us to the sqlite build. The most
	// recent token's handle is carried forward.
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, scope_json FROM auth_tokens
		  WHERE kind = 'owner' AND revoked_at IS NULL
		  ORDER BY created_at DESC`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var staleIDs []string
	var handle string
	for rows.Next() {
		var id, scopeJSON string
		if err := rows.Scan(&id, &scopeJSON); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		var sc struct {
			Team   string `json:"team"`
			Handle string `json:"handle"`
		}
		_ = json.Unmarshal([]byte(scopeJSON), &sc)
		if sc.Team != team {
			continue
		}
		staleIDs = append(staleIDs, id)
		if handle == "" {
			handle = sc.Handle // first match = newest (ORDER BY DESC)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Issue the replacement owner token, scoped exactly like ProvisionTeam.
	plain := auth.NewToken()
	newID := NewID()
	now := NowUTC()
	scopeMap := map[string]any{"team": team, "role": "principal"}
	if handle != "" {
		scopeMap["handle"] = handle
	}
	scope, _ := json.Marshal(scopeMap)
	if err := auth.InsertToken(r.Context(), s.db, "owner", string(scope), plain, newID, now); err != nil {
		writeErr(w, http.StatusInternalServerError, "issue new token: "+err.Error())
		return
	}

	// Revoke the prior owner tokens for this team. The new token id is
	// never in staleIDs (collected before the insert), so it survives.
	revoked := 0
	for _, id := range staleIDs {
		res, rerr := s.db.ExecContext(r.Context(),
			`UPDATE auth_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
			now, id)
		if rerr != nil {
			writeErr(w, http.StatusInternalServerError, "revoke old token: "+rerr.Error())
			return
		}
		if n, _ := res.RowsAffected(); n > 0 {
			revoked++
		}
	}

	s.recordAudit(r.Context(), team, "team.rotate_token", "team", team,
		"rotate owner token for "+team,
		map[string]any{"new_token_id": newID, "revoked_count": revoked})

	writeJSON(w, http.StatusOK, rotateTeamTokenOut{
		TeamID: team, NewToken: plain, NewTokenID: newID,
		RevokedCount: revoked, CreatedAt: now,
	})
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
