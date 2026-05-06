package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// W5b — Deliverables + components (A3 §4 + §5; A5 viewer chassis).
//
// A deliverable wraps one or more components (document / artifact / run /
// commit refs) under a single ratification gesture per phase. Templates
// hydrate them on phase entry; W5b ships the runtime CRUD + ratify path
// so directors can ratify in the demo. Template hydration itself lands
// with W7's research template content; tests here exercise the API by
// hand-creating rows.

const (
	deliverableStateDraft     = "draft"
	deliverableStateInReview  = "in-review"
	deliverableStateRatified  = "ratified"
)

func isValidDeliverableState(s string) bool {
	switch s {
	case deliverableStateDraft, deliverableStateInReview, deliverableStateRatified:
		return true
	}
	return false
}

func isValidComponentKind(k string) bool {
	switch k {
	case "document", "artifact", "run", "commit":
		return true
	}
	return false
}

type deliverableComponentOut struct {
	ID            string `json:"id"`
	DeliverableID string `json:"deliverable_id"`
	Kind          string `json:"kind"`
	RefID         string `json:"ref_id"`
	Required      bool   `json:"required"`
	Ord           int    `json:"ord"`
	CreatedAt     string `json:"created_at"`
}

type deliverableOut struct {
	ID                string                    `json:"id"`
	ProjectID         string                    `json:"project_id"`
	Phase             string                    `json:"phase"`
	Kind              string                    `json:"kind"`
	RatificationState string                    `json:"ratification_state"`
	RatifiedAt        string                    `json:"ratified_at,omitempty"`
	RatifiedByActor   string                    `json:"ratified_by_actor,omitempty"`
	Required          bool                      `json:"required"`
	Ord               int                       `json:"ord"`
	CreatedAt         string                    `json:"created_at"`
	UpdatedAt         string                    `json:"updated_at"`
	Components        []deliverableComponentOut `json:"components"`
}

type deliverableIn struct {
	Phase      string                  `json:"phase"`
	Kind       string                  `json:"kind"`
	Required   *bool                   `json:"required,omitempty"`
	Ord        *int                    `json:"ord,omitempty"`
	Components []deliverableComponentIn `json:"components,omitempty"`
}

type deliverableComponentIn struct {
	Kind     string `json:"kind"`
	RefID    string `json:"ref_id"`
	Required *bool  `json:"required,omitempty"`
	Ord      *int   `json:"ord,omitempty"`
}

type deliverablePatchIn struct {
	RatificationState *string `json:"ratification_state,omitempty"`
	Required          *bool   `json:"required,omitempty"`
	Ord               *int    `json:"ord,omitempty"`
}

type deliverableRatifyIn struct {
	Rationale string `json:"rationale,omitempty"`
}

// projectInTeam returns sql.ErrNoRows if the project is not in the team.
func (s *Server) projectInTeam(r *http.Request, team, project string) error {
	var found string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM projects WHERE id = ? AND team_id = ?`,
		project, team).Scan(&found)
	return err
}

func (s *Server) loadDeliverableComponents(
	r *http.Request, deliverableID string,
) ([]deliverableComponentOut, error) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, deliverable_id, kind, ref_id, required, ord, created_at
		  FROM deliverable_components
		 WHERE deliverable_id = ?
		 ORDER BY ord ASC, created_at ASC`, deliverableID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []deliverableComponentOut{}
	for rows.Next() {
		var c deliverableComponentOut
		var req int
		if err := rows.Scan(&c.ID, &c.DeliverableID, &c.Kind, &c.RefID,
			&req, &c.Ord, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Required = req != 0
		out = append(out, c)
	}
	return out, nil
}

func (s *Server) scanDeliverableRow(row scannable) (deliverableOut, error) {
	var d deliverableOut
	var ratifiedAt, ratifiedBy sql.NullString
	var req int
	err := row.Scan(&d.ID, &d.ProjectID, &d.Phase, &d.Kind,
		&d.RatificationState, &ratifiedAt, &ratifiedBy, &req,
		&d.Ord, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return deliverableOut{}, err
	}
	d.Required = req != 0
	if ratifiedAt.Valid {
		d.RatifiedAt = ratifiedAt.String
	}
	if ratifiedBy.Valid {
		d.RatifiedByActor = ratifiedBy.String
	}
	return d, nil
}

// scannable lets scanDeliverableRow accept either *sql.Row or *sql.Rows.
type scannable interface {
	Scan(...any) error
}

const deliverableSelectCols = `id, project_id, phase, kind,
	ratification_state, ratified_at, ratified_by_actor, required,
	ord, created_at, updated_at`

func (s *Server) handleListDeliverables(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := `SELECT ` + deliverableSelectCols + `
		  FROM deliverables WHERE project_id = ?`
	args := []any{project}
	if phase := r.URL.Query().Get("phase"); phase != "" {
		q += ` AND phase = ?`
		args = append(args, phase)
	}
	if state := r.URL.Query().Get("state"); state != "" {
		q += ` AND ratification_state = ?`
		args = append(args, state)
	}
	q += ` ORDER BY ord ASC, created_at ASC`

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	include := r.URL.Query().Get("include") == "components"
	out := []deliverableOut{}
	for rows.Next() {
		d, err := s.scanDeliverableRow(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if include {
			comps, cerr := s.loadDeliverableComponents(r, d.ID)
			if cerr != nil {
				writeErr(w, http.StatusInternalServerError, cerr.Error())
				return
			}
			d.Components = comps
		} else {
			d.Components = []deliverableComponentOut{}
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleGetDeliverable(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "deliverable")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := s.db.QueryRowContext(r.Context(), `SELECT `+deliverableSelectCols+`
		FROM deliverables WHERE id = ? AND project_id = ?`, id, project)
	d, err := s.scanDeliverableRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "deliverable not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	comps, err := s.loadDeliverableComponents(r, d.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Components = comps
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleCreateDeliverable(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var in deliverableIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Phase == "" || in.Kind == "" {
		writeErr(w, http.StatusBadRequest, "phase and kind required")
		return
	}
	required := 1
	if in.Required != nil && !*in.Required {
		required = 0
	}
	ord := 0
	if in.Ord != nil {
		ord = *in.Ord
	}
	for _, c := range in.Components {
		if !isValidComponentKind(c.Kind) {
			writeErr(w, http.StatusBadRequest,
				"component kind must be one of: document, artifact, run, commit")
			return
		}
		if c.RefID == "" {
			writeErr(w, http.StatusBadRequest, "component ref_id required")
			return
		}
	}

	id := NewID()
	now := NowUTC()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO deliverables (id, project_id, phase, kind,
			ratification_state, required, ord, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'draft', ?, ?, ?, ?)`,
		id, project, in.Phase, in.Kind, required, ord, now, now); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range in.Components {
		creq := 1
		if c.Required != nil && !*c.Required {
			creq = 0
		}
		cord := 0
		if c.Ord != nil {
			cord = *c.Ord
		}
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO deliverable_components (id, deliverable_id, kind,
				ref_id, required, ord, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			NewID(), id, c.Kind, c.RefID, creq, cord, now); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.recordAudit(r.Context(), team, "deliverable.created",
		"deliverable", id,
		fmt.Sprintf("created %s deliverable in phase %s", in.Kind, in.Phase),
		map[string]any{
			"project_id": project,
			"phase":      in.Phase,
			"kind":       in.Kind,
		})
	for _, c := range in.Components {
		s.recordAudit(r.Context(), team, "deliverable_component.added",
			"deliverable", id,
			fmt.Sprintf("added %s component %s", c.Kind, c.RefID),
			map[string]any{
				"project_id":     project,
				"deliverable_id": id,
				"kind":           c.Kind,
				"ref_id":         c.RefID,
			})
	}

	row := s.db.QueryRowContext(r.Context(), `SELECT `+deliverableSelectCols+`
		FROM deliverables WHERE id = ?`, id)
	d, err := s.scanDeliverableRow(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	comps, err := s.loadDeliverableComponents(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Components = comps
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handlePatchDeliverable(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "deliverable")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var in deliverablePatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Existence check.
	var curState string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT ratification_state FROM deliverables WHERE id = ? AND project_id = ?`,
		id, project).Scan(&curState)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "deliverable not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if in.RatificationState != nil {
		st := *in.RatificationState
		if !isValidDeliverableState(st) {
			writeErr(w, http.StatusBadRequest,
				"ratification_state must be one of: draft, in-review, ratified")
			return
		}
		if st == deliverableStateRatified {
			writeErr(w, http.StatusBadRequest,
				"use POST /ratify to ratify; PATCH only changes draft/in-review")
			return
		}
	}
	now := NowUTC()
	changed := []string{}
	q := `UPDATE deliverables SET updated_at = ?`
	args := []any{now}
	if in.RatificationState != nil {
		q += `, ratification_state = ?`
		args = append(args, *in.RatificationState)
		changed = append(changed, "ratification_state")
		// Moving back from ratified to draft via PATCH is rejected above; on
		// non-ratified transitions, clear ratified stamps to keep state clean.
		if curState == deliverableStateRatified {
			q += `, ratified_at = NULL, ratified_by_actor = NULL`
		}
	}
	if in.Required != nil {
		req := 0
		if *in.Required {
			req = 1
		}
		q += `, required = ?`
		args = append(args, req)
		changed = append(changed, "required")
	}
	if in.Ord != nil {
		q += `, ord = ?`
		args = append(args, *in.Ord)
		changed = append(changed, "ord")
	}
	q += ` WHERE id = ? AND project_id = ?`
	args = append(args, id, project)
	if _, err := s.db.ExecContext(r.Context(), q, args...); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "deliverable.updated",
		"deliverable", id,
		"updated deliverable",
		map[string]any{
			"project_id":     project,
			"deliverable_id": id,
			"changed_fields": changed,
		})

	row := s.db.QueryRowContext(r.Context(), `SELECT `+deliverableSelectCols+`
		FROM deliverables WHERE id = ?`, id)
	d, err := s.scanDeliverableRow(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	comps, err := s.loadDeliverableComponents(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Components = comps
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleRatifyDeliverable(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "deliverable")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var in deliverableRatifyIn
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
	}
	var curState string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT ratification_state FROM deliverables WHERE id = ? AND project_id = ?`,
		id, project).Scan(&curState)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "deliverable not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if curState == deliverableStateRatified {
		writeErr(w, http.StatusConflict, "deliverable already ratified")
		return
	}
	now := NowUTC()
	_, actorKind, actorHandle := actorFromContext(r.Context())
	actor := actorKind
	if actorHandle != "" {
		actor = actorKind + ":" + actorHandle
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE deliverables
		SET ratification_state = 'ratified',
		    ratified_at = ?,
		    ratified_by_actor = ?,
		    updated_at = ?
		WHERE id = ? AND project_id = ?`,
		now, actor, now, id, project); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "deliverable.ratified",
		"deliverable", id, "ratified deliverable",
		map[string]any{
			"project_id":     project,
			"deliverable_id": id,
			"rationale":      in.Rationale,
		})

	// W6 — gate cascade. Auto-fire any pending gate criteria whose body
	// references this deliverable (or scope this phase). Errors here are
	// logged but do not fail the ratification — audit row is already in.
	row := s.db.QueryRowContext(r.Context(), `SELECT `+deliverableSelectCols+`
		FROM deliverables WHERE id = ?`, id)
	d, err := s.scanDeliverableRow(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, cerr := s.cascadeDeliverableRatified(
		r.Context(), team, project, id, d.Phase); cerr != nil {
		s.log.Warn("criterion gate cascade", "err", cerr,
			"deliverable_id", id, "project_id", project)
	}
	comps, err := s.loadDeliverableComponents(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Components = comps
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleUnratifyDeliverable(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "deliverable")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var in deliverableRatifyIn
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	var curState string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT ratification_state FROM deliverables WHERE id = ? AND project_id = ?`,
		id, project).Scan(&curState)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "deliverable not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if curState != deliverableStateRatified {
		writeErr(w, http.StatusConflict, "deliverable is not ratified")
		return
	}
	now := NowUTC()
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE deliverables
		SET ratification_state = 'draft',
		    ratified_at = NULL,
		    ratified_by_actor = NULL,
		    updated_at = ?
		WHERE id = ? AND project_id = ?`, now, id, project); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "deliverable.unratified",
		"deliverable", id, "unratified deliverable",
		map[string]any{
			"project_id":     project,
			"deliverable_id": id,
			"reason":         in.Rationale,
		})
	row := s.db.QueryRowContext(r.Context(), `SELECT `+deliverableSelectCols+`
		FROM deliverables WHERE id = ?`, id)
	d, err := s.scanDeliverableRow(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	comps, err := s.loadDeliverableComponents(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Components = comps
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleAddDeliverableComponent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	deliverable := chi.URLParam(r, "deliverable")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var in deliverableComponentIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !isValidComponentKind(in.Kind) {
		writeErr(w, http.StatusBadRequest,
			"kind must be one of: document, artifact, run, commit")
		return
	}
	if in.RefID == "" {
		writeErr(w, http.StatusBadRequest, "ref_id required")
		return
	}
	var found string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM deliverables WHERE id = ? AND project_id = ?`,
		deliverable, project).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "deliverable not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	required := 1
	if in.Required != nil && !*in.Required {
		required = 0
	}
	ord := 0
	if in.Ord != nil {
		ord = *in.Ord
	}
	id := NewID()
	now := NowUTC()
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO deliverable_components (id, deliverable_id, kind, ref_id,
			required, ord, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, deliverable, in.Kind, in.RefID, required, ord, now); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "deliverable_component.added",
		"deliverable", deliverable,
		fmt.Sprintf("added %s component %s", in.Kind, in.RefID),
		map[string]any{
			"project_id":     project,
			"deliverable_id": deliverable,
			"component_id":   id,
			"kind":           in.Kind,
			"ref_id":         in.RefID,
		})
	out := deliverableComponentOut{
		ID: id, DeliverableID: deliverable, Kind: in.Kind,
		RefID: in.RefID, Required: required != 0, Ord: ord, CreatedAt: now,
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleRemoveDeliverableComponent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	deliverable := chi.URLParam(r, "deliverable")
	component := chi.URLParam(r, "component")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := s.db.ExecContext(r.Context(), `
		DELETE FROM deliverable_components
		 WHERE id = ?
		   AND deliverable_id = ?
		   AND deliverable_id IN (SELECT id FROM deliverables WHERE project_id = ?)`,
		component, deliverable, project)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "component not found")
		return
	}
	s.recordAudit(r.Context(), team, "deliverable_component.removed",
		"deliverable", deliverable,
		"removed component "+component,
		map[string]any{
			"project_id":     project,
			"deliverable_id": deliverable,
			"component_id":   component,
		})
	w.WriteHeader(http.StatusNoContent)
}

// handleListProjectCriteria — GET /v1/teams/{team}/projects/{project}/criteria.
// W6 will introduce mark-met / waive / fail; W5b only ships the read path
// so the deliverable viewer can render its criteria panel inline.
func (s *Server) handleListProjectCriteria(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := `SELECT id, project_id, phase, deliverable_id, kind, body, state,
	             met_at, met_by_actor, evidence_ref, required, ord
	        FROM acceptance_criteria
	       WHERE project_id = ?`
	args := []any{project}
	if phase := r.URL.Query().Get("phase"); phase != "" {
		q += ` AND phase = ?`
		args = append(args, phase)
	}
	if d := r.URL.Query().Get("deliverable_id"); d != "" {
		q += ` AND deliverable_id = ?`
		args = append(args, d)
	}
	q += ` ORDER BY ord ASC, id ASC`
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type criterionOut struct {
		ID            string         `json:"id"`
		ProjectID     string         `json:"project_id"`
		Phase         string         `json:"phase"`
		DeliverableID string         `json:"deliverable_id,omitempty"`
		Kind          string         `json:"kind"`
		Body          map[string]any `json:"body"`
		State         string         `json:"state"`
		MetAt         string         `json:"met_at,omitempty"`
		MetByActor    string         `json:"met_by_actor,omitempty"`
		EvidenceRef   string         `json:"evidence_ref,omitempty"`
		Required      bool           `json:"required"`
		Ord           int            `json:"ord"`
	}
	out := []criterionOut{}
	for rows.Next() {
		var c criterionOut
		var deliv, metAt, metBy, evid, body sql.NullString
		var req int
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Phase, &deliv, &c.Kind,
			&body, &c.State, &metAt, &metBy, &evid, &req, &c.Ord); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		c.Required = req != 0
		if deliv.Valid {
			c.DeliverableID = deliv.String
		}
		if metAt.Valid {
			c.MetAt = metAt.String
		}
		if metBy.Valid {
			c.MetByActor = metBy.String
		}
		if evid.Valid {
			c.EvidenceRef = evid.String
		}
		c.Body = map[string]any{}
		if body.Valid && body.String != "" {
			_ = json.Unmarshal([]byte(body.String), &c.Body)
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// projectOverviewOut wraps the composed phase-aware view (A3 §9.1).
type projectOverviewOut struct {
	ProjectID    string                    `json:"project_id"`
	Phase        string                    `json:"phase,omitempty"`
	Phases       []string                  `json:"phases,omitempty"`
	PhaseIndex   int                       `json:"phase_index"`
	Deliverables []deliverableOut          `json:"deliverables"`
	Counts       projectOverviewCounts     `json:"counts"`
}

type projectOverviewCounts struct {
	DeliverablesTotal     int `json:"deliverables_total"`
	DeliverablesRatified  int `json:"deliverables_ratified"`
	CriteriaTotal         int `json:"criteria_total"`
	CriteriaMet           int `json:"criteria_met"`
}

// handleGetProjectOverview — GET /projects/{project}/overview. Composed
// read used by the mobile Project Detail screen; bundles phase + active-
// phase deliverables (with components) + counts. W6 will tack criteria
// onto the deliverables once the criteria runtime ships.
func (s *Server) handleGetProjectOverview(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")

	phase, _, templateID, err := s.loadProjectPhaseRow(r.Context(), team, project)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	phases := s.templatePhases(templateID)
	out := projectOverviewOut{
		ProjectID:    project,
		Phase:        phase,
		Phases:       phases,
		PhaseIndex:   phaseIndex(phases, phase),
		Deliverables: []deliverableOut{},
	}

	// Active-phase deliverables (or all when no phase set).
	q := `SELECT ` + deliverableSelectCols + `
	       FROM deliverables WHERE project_id = ?`
	args := []any{project}
	if phase != "" {
		q += ` AND phase = ?`
		args = append(args, phase)
	}
	q += ` ORDER BY ord ASC, created_at ASC`
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		d, err := s.scanDeliverableRow(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		comps, cerr := s.loadDeliverableComponents(r, d.ID)
		if cerr != nil {
			writeErr(w, http.StatusInternalServerError, cerr.Error())
			return
		}
		d.Components = comps
		out.Deliverables = append(out.Deliverables, d)
	}

	// Counts (active-phase scoped).
	out.Counts.DeliverablesTotal = len(out.Deliverables)
	for _, d := range out.Deliverables {
		if d.RatificationState == deliverableStateRatified {
			out.Counts.DeliverablesRatified++
		}
	}
	if phase != "" {
		var metNS sql.NullInt64
		_ = s.db.QueryRowContext(r.Context(), `
			SELECT COUNT(*),
			       SUM(CASE WHEN state = 'met' THEN 1 ELSE 0 END)
			  FROM acceptance_criteria
			 WHERE project_id = ? AND phase = ?`,
			project, phase).Scan(&out.Counts.CriteriaTotal, &metNS)
		if metNS.Valid {
			out.Counts.CriteriaMet = int(metNS.Int64)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ADR-020 W2 — Send back with notes. Director returns a draft or
// in-review deliverable to the steward with a structured payload
// (free-text note + selected annotation IDs the steward should address).
// Transitions ratification_state to in-review (D5) and raises a
// `revision_requested` attention item scoped to the project.

const attentionKindRevisionRequested = "revision_requested"

type sendBackDeliverableIn struct {
	Note          string   `json:"note"`
	AnnotationIDs []string `json:"annotation_ids,omitempty"`
}

type sendBackDeliverableOut struct {
	Deliverable     deliverableOut `json:"deliverable"`
	AttentionItemID string         `json:"attention_item_id"`
}

func (s *Server) handleSendBackDeliverable(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "deliverable")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var in sendBackDeliverableIn
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		writeErr(w, http.StatusBadRequest, "note required")
		return
	}

	var curState string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT ratification_state FROM deliverables WHERE id = ? AND project_id = ?`,
		id, project).Scan(&curState)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "deliverable not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if curState == deliverableStateRatified {
		// Per ADR-020 D5 — ratified deliverables must be unratified
		// first, deliberately, before being sent back.
		writeErr(w, http.StatusConflict,
			"deliverable is ratified; unratify before sending back")
		return
	}

	// Annotation cross-reference guard (per the wedge plan): every
	// supplied annotation id must point to a section of a document that
	// is one of the deliverable's components. Prevents directors from
	// accidentally attaching notes from another deliverable to this
	// revision request.
	if len(in.AnnotationIDs) > 0 {
		valid, vErr := s.annotationsBelongToDeliverable(r, id, in.AnnotationIDs)
		if vErr != nil {
			writeErr(w, http.StatusInternalServerError, vErr.Error())
			return
		}
		if !valid {
			writeErr(w, http.StatusUnprocessableEntity,
				"annotation_ids must reference sections of this deliverable's documents")
			return
		}
	}

	now := NowUTC()
	if curState != deliverableStateInReview {
		if _, err := s.db.ExecContext(r.Context(), `
			UPDATE deliverables
			SET ratification_state = 'in-review', updated_at = ?
			WHERE id = ? AND project_id = ?`,
			now, id, project); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// Raise the attention item. scope_kind = 'project' / scope_id =
	// projectID per the existing schema; the deliverable_id and
	// annotation_ids ride in pending_payload_json so the mobile attention
	// renderer can deep-link without a schema change.
	payload, _ := json.Marshal(map[string]any{
		"deliverable_id": id,
		"note":           note,
		"annotation_ids": in.AnnotationIDs,
	})
	_, actorKind, actorHandle := actorFromContext(r.Context())
	attID := NewID()
	summary := fmt.Sprintf("Revision requested · %s",
		truncateForAudit(note, 80))
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind, summary, severity,
			current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json
		) VALUES (?, ?, 'project', ?, ?, ?, 'major',
		          '[]', 'open', ?, ?, ?, ?)`,
		attID, project, project, attentionKindRevisionRequested,
		summary, now, actorKind, nullIfEmpty(actorHandle),
		string(payload),
	); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.recordAudit(r.Context(), team, "deliverable.send_back",
		"deliverable", id, summary,
		map[string]any{
			"project_id":         project,
			"deliverable_id":     id,
			"attention_item_id":  attID,
			"annotation_count":   len(in.AnnotationIDs),
		})

	row := s.db.QueryRowContext(r.Context(),
		`SELECT `+deliverableSelectCols+` FROM deliverables WHERE id = ?`, id)
	d, err := s.scanDeliverableRow(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	comps, err := s.loadDeliverableComponents(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Components = comps
	writeJSON(w, http.StatusOK, sendBackDeliverableOut{
		Deliverable: d, AttentionItemID: attID,
	})
}

// annotationsBelongToDeliverable returns true when every id in [ids]
// points to a section of a document that's a component of [deliverableID].
// Returns false when any id is missing or anchored to a foreign document.
func (s *Server) annotationsBelongToDeliverable(
	r *http.Request, deliverableID string, ids []string,
) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	// Build the IN clause + check count by joining annotations →
	// documents → deliverable_components(kind='document').
	args := make([]any, 0, len(ids)+1)
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	args = append(args, deliverableID)
	q := `
		SELECT COUNT(DISTINCT da.id)
		  FROM document_annotations da
		  JOIN deliverable_components dc
		    ON dc.kind = 'document' AND dc.ref_id = da.document_id
		 WHERE da.id IN (` + strings.Join(placeholders, ",") + `)
		   AND dc.deliverable_id = ?`
	var matched int
	if err := s.db.QueryRowContext(r.Context(), q, args...).Scan(&matched); err != nil {
		return false, err
	}
	return matched == len(ids), nil
}
