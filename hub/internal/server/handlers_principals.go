package server

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
)

// handleListPrincipals coalesces role=principal tokens for a team into one
// row per unique scope.handle. Tokens without a handle collapse into a
// single synthetic "@principal (unnamed)" row so the caller still sees
// that legacy tokens exist.
//
// Shape: [{handle, first_issued_at, token_count, has_unnamed}]. Kept
// deliberately small — last_seen_at is omitted until we add a touch-on-use
// column to auth_tokens (tracked as roadmap).
type principalOut struct {
	Handle         string `json:"handle"`
	FirstIssuedAt  string `json:"first_issued_at"`
	TokenCount     int    `json:"token_count"`
	Unnamed        bool   `json:"unnamed,omitempty"`
}

func (s *Server) handleListPrincipals(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT scope_json, created_at
		   FROM auth_tokens
		  WHERE revoked_at IS NULL
		  ORDER BY created_at`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	// Coalesce by handle. Legacy tokens (no handle, role=principal) share a
	// single synthetic row keyed by "" → rendered as "@principal (unnamed)".
	type agg struct {
		firstIssued string
		count       int
	}
	byHandle := map[string]*agg{}
	for rows.Next() {
		var scopeJSON, createdAt string
		if err := rows.Scan(&scopeJSON, &createdAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		var sc struct {
			Team   string `json:"team"`
			Role   string `json:"role"`
			Handle string `json:"handle"`
		}
		if err := json.Unmarshal([]byte(scopeJSON), &sc); err != nil {
			continue
		}
		if sc.Team != team || sc.Role != "principal" {
			continue
		}
		key := sc.Handle
		cur, ok := byHandle[key]
		if !ok {
			byHandle[key] = &agg{firstIssued: createdAt, count: 1}
			continue
		}
		cur.count++
		if createdAt < cur.firstIssued {
			cur.firstIssued = createdAt
		}
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]principalOut, 0, len(byHandle))
	for handle, a := range byHandle {
		display := handle
		unnamed := false
		if display == "" {
			display = "principal (unnamed)"
			unnamed = true
		}
		out = append(out, principalOut{
			Handle:        display,
			FirstIssuedAt: a.firstIssued,
			TokenCount:    a.count,
			Unnamed:       unnamed,
		})
	}
	// Stable order: named first (alphabetical), unnamed bucket last.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Unnamed != out[j].Unnamed {
			return !out[i].Unnamed
		}
		return out[i].Handle < out[j].Handle
	})
	writeJSON(w, http.StatusOK, out)
}
