package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
)

// Tokens REST is the owner-only surface for inviting humans onto a team.
// It mirrors what the `hub-server tokens issue` CLI does, just reachable
// from the mobile app instead of requiring shell access to the host.
//
// Issuance returns the plaintext bearer exactly once — we never store it.
// Listing returns metadata only (id, kind, role, handle, timestamps).

type tokenOut struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Role       string  `json:"role,omitempty"`
	Handle     string  `json:"handle,omitempty"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
}

type tokenIssueIn struct {
	Kind      string `json:"kind"`               // 'owner' | 'user' | 'host' | 'agent'; default 'user'
	Role      string `json:"role"`               // default 'principal' for kind=user, preserved otherwise
	Handle    string `json:"handle"`             // display name (shown on Members tab)
	ExpiresAt string `json:"expires_at,omitempty"`
}

type tokenIssueOut struct {
	ID        string `json:"id"`
	Plaintext string `json:"plaintext"`
	Kind      string `json:"kind"`
	Role      string `json:"role,omitempty"`
	Handle    string `json:"handle,omitempty"`
	CreatedAt string `json:"created_at"`
}

// requireOwner gates token management to owner-kind tokens. Returns true
// if the caller passed the check; otherwise writes 403 and returns false.
func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request) bool {
	tok, ok := auth.FromContext(r.Context())
	if !ok || tok == nil || tok.Kind != "owner" {
		writeErr(w, http.StatusForbidden, "owner token required")
		return false
	}
	return true
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	team := chi.URLParam(r, "team")
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, kind, scope_json, created_at,
		       COALESCE(expires_at, ''), COALESCE(revoked_at, '')
		  FROM auth_tokens
		 ORDER BY created_at DESC`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []tokenOut{}
	for rows.Next() {
		var (
			id, kind, scopeJSON, createdAt string
			expires, revoked               string
		)
		if err := rows.Scan(&id, &kind, &scopeJSON, &createdAt,
			&expires, &revoked); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		var sc struct {
			Team   string `json:"team"`
			Role   string `json:"role"`
			Handle string `json:"handle"`
		}
		_ = json.Unmarshal([]byte(scopeJSON), &sc)
		if sc.Team != "" && sc.Team != team {
			continue
		}
		row := tokenOut{
			ID: id, Kind: kind, Role: sc.Role, Handle: sc.Handle,
			CreatedAt: createdAt,
		}
		if expires != "" {
			row.ExpiresAt = &expires
		}
		if revoked != "" {
			row.RevokedAt = &revoked
		}
		out = append(out, row)
	}
	// Active tokens first, revoked last; newest inside each group.
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].RevokedAt == nil, out[j].RevokedAt == nil
		if ai != aj {
			return ai
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	team := chi.URLParam(r, "team")
	var in tokenIssueIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Kind == "" {
		in.Kind = "user"
	}
	if in.Role == "" {
		in.Role = "principal"
	}
	if in.Kind != "owner" && in.Kind != "user" &&
		in.Kind != "host" && in.Kind != "agent" {
		writeErr(w, http.StatusBadRequest, "kind must be owner|user|host|agent")
		return
	}
	scopeMap := map[string]any{"team": team, "role": in.Role}
	if in.Handle != "" {
		scopeMap["handle"] = in.Handle
	}
	scope, _ := json.Marshal(scopeMap)
	plain := auth.NewToken()
	id := NewID()
	now := NowUTC()
	if err := auth.InsertToken(r.Context(), s.db, in.Kind, string(scope),
		plain, id, now); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if in.ExpiresAt != "" {
		if _, err := s.db.ExecContext(r.Context(),
			`UPDATE auth_tokens SET expires_at = ? WHERE id = ?`,
			in.ExpiresAt, id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.recordAudit(r.Context(), team, "token.issue", "token", id,
		"issue "+in.Kind+" token "+in.Handle,
		map[string]any{"kind": in.Kind, "role": in.Role, "handle": in.Handle},
	)
	writeJSON(w, http.StatusCreated, tokenIssueOut{
		ID: id, Plaintext: plain, Kind: in.Kind, Role: in.Role,
		Handle: in.Handle, CreatedAt: now,
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "id")
	// Refuse to revoke the caller's own token so an owner can't lock
	// themselves out by tapping the wrong row.
	if tok, ok := auth.FromContext(r.Context()); ok && tok != nil && tok.ID == id {
		writeErr(w, http.StatusConflict, "cannot revoke the calling token")
		return
	}
	var revoked sql.NullString
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT revoked_at FROM auth_tokens WHERE id = ?`, id).Scan(&revoked); err != nil {
		if err == sql.ErrNoRows {
			writeErr(w, http.StatusNotFound, "token not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if revoked.Valid {
		writeErr(w, http.StatusConflict, "already revoked")
		return
	}
	now := NowUTC()
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE auth_tokens SET revoked_at = ? WHERE id = ?`, now, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "token.revoke", "token", id,
		"revoke token", map[string]any{"id": id})
	w.WriteHeader(http.StatusNoContent)
}
