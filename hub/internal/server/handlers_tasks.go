package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type taskIn struct {
	Title        string  `json:"title"`
	BodyMD       string  `json:"body_md,omitempty"`
	ParentTaskID string  `json:"parent_task_id,omitempty"`
	AssigneeID   string  `json:"assignee_id,omitempty"`
	CreatedByID  string  `json:"created_by_id,omitempty"`
	MilestoneID  string  `json:"milestone_id,omitempty"`
	Status       string  `json:"status,omitempty"`
}

type taskOut struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"project_id"`
	ParentTaskID string  `json:"parent_task_id,omitempty"`
	Title        string  `json:"title"`
	BodyMD       string  `json:"body_md"`
	Status       string  `json:"status"`
	AssigneeID   string  `json:"assignee_id,omitempty"`
	CreatedByID  string  `json:"created_by_id,omitempty"`
	MilestoneID  string  `json:"milestone_id,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	var in taskIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title required")
		return
	}
	status := in.Status
	if status == "" {
		status = "todo"
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO tasks (id, project_id, parent_task_id, title, body_md, status,
		                   assignee_id, created_by_id, milestone_id, created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?,
		        NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
		id, proj, in.ParentTaskID, in.Title, in.BodyMD, status,
		in.AssigneeID, in.CreatedByID, in.MilestoneID, now, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, taskOut{
		ID: id, ProjectID: proj, ParentTaskID: in.ParentTaskID, Title: in.Title,
		BodyMD: in.BodyMD, Status: status, AssigneeID: in.AssigneeID,
		CreatedByID: in.CreatedByID, MilestoneID: in.MilestoneID,
		CreatedAt: now, UpdatedAt: now,
	})
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	status := r.URL.Query().Get("status")
	q := `
		SELECT id, project_id, COALESCE(parent_task_id, ''), title, body_md, status,
		       COALESCE(assignee_id, ''), COALESCE(created_by_id, ''),
		       COALESCE(milestone_id, ''), created_at, updated_at
		FROM tasks WHERE project_id = ?`
	args := []any{proj}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC"
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []taskOut{}
	for rows.Next() {
		var t taskOut
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.ParentTaskID, &t.Title, &t.BodyMD,
			&t.Status, &t.AssigneeID, &t.CreatedByID, &t.MilestoneID,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

type taskPatchIn struct {
	Title      *string `json:"title,omitempty"`
	BodyMD     *string `json:"body_md,omitempty"`
	Status     *string `json:"status,omitempty"`
	AssigneeID *string `json:"assignee_id,omitempty"`
}

func (s *Server) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	id := chi.URLParam(r, "task")
	var in taskPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	sets, args := []string{}, []any{}
	if in.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *in.Title)
	}
	if in.BodyMD != nil {
		sets = append(sets, "body_md = ?")
		args = append(args, *in.BodyMD)
	}
	if in.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *in.Status)
	}
	if in.AssigneeID != nil {
		sets = append(sets, "assignee_id = NULLIF(?, '')")
		args = append(args, *in.AssigneeID)
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no fields to update")
		return
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, NowUTC(), proj, id)
	q := "UPDATE tasks SET " + strings.Join(sets, ", ") + " WHERE project_id = ? AND id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	id := chi.URLParam(r, "task")
	var t taskOut
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, project_id, COALESCE(parent_task_id, ''), title, body_md, status,
		       COALESCE(assignee_id, ''), COALESCE(created_by_id, ''),
		       COALESCE(milestone_id, ''), created_at, updated_at
		FROM tasks WHERE project_id = ? AND id = ?`, proj, id).Scan(
		&t.ID, &t.ProjectID, &t.ParentTaskID, &t.Title, &t.BodyMD,
		&t.Status, &t.AssigneeID, &t.CreatedByID, &t.MilestoneID,
		&t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}
