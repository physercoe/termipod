package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type reviewIn struct {
	ProjectID        string `json:"project_id"`
	TargetKind       string `json:"target_kind"`
	TargetID         string `json:"target_id"`
	RequesterAgentID string `json:"requester_agent_id,omitempty"`
	Comment          string `json:"comment,omitempty"`
}

type reviewDecideIn struct {
	State   string `json:"state"`
	Comment string `json:"comment,omitempty"`
	UserID  string `json:"user_id,omitempty"`
}

type reviewOut struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"project_id"`
	TargetKind       string  `json:"target_kind"`
	TargetID         string  `json:"target_id"`
	RequesterAgentID string  `json:"requester_agent_id,omitempty"`
	State            string  `json:"state"`
	DecidedByUserID  string  `json:"decided_by_user_id,omitempty"`
	DecidedAt        *string `json:"decided_at,omitempty"`
	Comment          string  `json:"comment,omitempty"`
	CreatedAt        string  `json:"created_at"`
}

func isValidTargetKind(k string) bool {
	return k == "document" || k == "artifact"
}

func isValidDecisionState(s string) bool {
	switch s {
	case "approved", "request_changes", "rejected":
		return true
	}
	return false
}

func (s *Server) handleCreateReview(w http.ResponseWriter, r *http.Request) {
	var in reviewIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.ProjectID == "" || in.TargetKind == "" || in.TargetID == "" {
		writeErr(w, http.StatusBadRequest, "project_id, target_kind, target_id required")
		return
	}
	if !isValidTargetKind(in.TargetKind) {
		writeErr(w, http.StatusBadRequest, "target_kind must be document or artifact")
		return
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO reviews (
			id, project_id, target_kind, target_id,
			requester_agent_id, state, comment, created_at
		) VALUES (?, ?, ?, ?, NULLIF(?, ''), 'pending', NULLIF(?, ''), ?)`,
		id, in.ProjectID, in.TargetKind, in.TargetID,
		in.RequesterAgentID, in.Comment, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, reviewOut{
		ID: id, ProjectID: in.ProjectID,
		TargetKind: in.TargetKind, TargetID: in.TargetID,
		RequesterAgentID: in.RequesterAgentID,
		State:            "pending",
		Comment:          in.Comment,
		CreatedAt:        now,
	})
}

func (s *Server) handleListReviews(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	state := r.URL.Query().Get("state")
	targetKind := r.URL.Query().Get("target_kind")

	q := `SELECT id, project_id, target_kind, target_id,
	             requester_agent_id, state, decided_by_user_id, decided_at,
	             comment, created_at
	      FROM reviews WHERE 1=1`
	args := []any{}
	if project != "" {
		q += ` AND project_id = ?`
		args = append(args, project)
	}
	if state != "" {
		if state != "pending" && !isValidDecisionState(state) {
			writeErr(w, http.StatusBadRequest, "invalid state filter")
			return
		}
		q += ` AND state = ?`
		args = append(args, state)
	}
	if targetKind != "" {
		if !isValidTargetKind(targetKind) {
			writeErr(w, http.StatusBadRequest, "invalid target_kind filter")
			return
		}
		q += ` AND target_kind = ?`
		args = append(args, targetKind)
	}
	q += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []reviewOut{}
	for rows.Next() {
		rv, err := scanReview(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, rv)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetReview(w http.ResponseWriter, r *http.Request) {
	review := chi.URLParam(r, "review")
	row := s.db.QueryRowContext(r.Context(), `
		SELECT id, project_id, target_kind, target_id,
		       requester_agent_id, state, decided_by_user_id, decided_at,
		       comment, created_at
		FROM reviews WHERE id = ?`, review)
	rv, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "review not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rv)
}

func (s *Server) handleDecideReview(w http.ResponseWriter, r *http.Request) {
	review := chi.URLParam(r, "review")
	var in reviewDecideIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !isValidDecisionState(in.State) {
		writeErr(w, http.StatusBadRequest, "state must be approved, request_changes, or rejected")
		return
	}
	now := NowUTC()
	// Only transition from pending — idempotency / prevent double-decide.
	// COALESCE preserves the original comment if the decider didn't supply one;
	// otherwise the decision comment replaces it.
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE reviews SET state = ?, decided_by_user_id = NULLIF(?, ''),
		                   decided_at = ?, comment = COALESCE(NULLIF(?, ''), comment)
		WHERE id = ? AND state = 'pending'`,
		in.State, in.UserID, now, in.Comment, review)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish not-found from already-decided.
		var curState string
		err := s.db.QueryRowContext(r.Context(),
			`SELECT state FROM reviews WHERE id = ?`, review).Scan(&curState)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "review not found")
			return
		}
		writeErr(w, http.StatusConflict, "review already decided: "+curState)
		return
	}
	// Return the updated row.
	row := s.db.QueryRowContext(r.Context(), `
		SELECT id, project_id, target_kind, target_id,
		       requester_agent_id, state, decided_by_user_id, decided_at,
		       comment, created_at
		FROM reviews WHERE id = ?`, review)
	rv, err := scanReview(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rv)
}

// scanReview decodes one reviews row from a *sql.Row or *sql.Rows.
// The rowScanner interface is defined in handlers_agents.go.
func scanReview(s rowScanner) (reviewOut, error) {
	var rv reviewOut
	var requester, decidedBy, decidedAt, comment sql.NullString
	err := s.Scan(&rv.ID, &rv.ProjectID, &rv.TargetKind, &rv.TargetID,
		&requester, &rv.State, &decidedBy, &decidedAt, &comment, &rv.CreatedAt)
	if err != nil {
		return rv, err
	}
	if requester.Valid {
		rv.RequesterAgentID = requester.String
	}
	if decidedBy.Valid {
		rv.DecidedByUserID = decidedBy.String
	}
	if decidedAt.Valid {
		s := decidedAt.String
		rv.DecidedAt = &s
	}
	if comment.Valid {
		rv.Comment = comment.String
	}
	return rv, nil
}
