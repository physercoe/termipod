package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// W6 — Acceptance criteria runtime (A3 §6).
//
// W5b shipped the read-only list endpoint (`GET /criteria`) so the
// deliverable viewer's panel could render. W6 adds the mutation surface:
// create / patch / mark-met / mark-failed / waive plus the gate library
// cascade — when a deliverable is ratified, criteria whose gate
// references that deliverable auto-fire to met. Metric watcher
// (auto-marking based on run output) is the most novel piece and
// remains a W6 follow-up; gate cascade is the demo-critical path.

const (
	criterionStatePending = "pending"
	criterionStateMet     = "met"
	criterionStateFailed  = "failed"
	criterionStateWaived  = "waived"
)

func isValidCriterionState(s string) bool {
	switch s {
	case criterionStatePending, criterionStateMet, criterionStateFailed, criterionStateWaived:
		return true
	}
	return false
}

func isValidCriterionKind(k string) bool {
	switch k {
	case "text", "metric", "gate":
		return true
	}
	return false
}

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

type criterionIn struct {
	Phase         string         `json:"phase"`
	DeliverableID string         `json:"deliverable_id,omitempty"`
	Kind          string         `json:"kind"`
	Body          map[string]any `json:"body"`
	Required      *bool          `json:"required,omitempty"`
	Ord           *int           `json:"ord,omitempty"`
}

type criterionPatchIn struct {
	Body        map[string]any `json:"body,omitempty"`
	EvidenceRef *string        `json:"evidence_ref,omitempty"`
	Required    *bool          `json:"required,omitempty"`
	Ord         *int           `json:"ord,omitempty"`
}

type criterionMarkIn struct {
	EvidenceRef string `json:"evidence_ref,omitempty"`
	Rationale   string `json:"rationale,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func (s *Server) loadCriterion(
	ctx context.Context, project, id string,
) (criterionOut, error) {
	var c criterionOut
	var deliv, metAt, metBy, evid, body sql.NullString
	var req int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, phase, deliverable_id, kind, body, state,
		       met_at, met_by_actor, evidence_ref, required, ord
		  FROM acceptance_criteria
		 WHERE id = ? AND project_id = ?`, id, project).Scan(
		&c.ID, &c.ProjectID, &c.Phase, &deliv, &c.Kind, &body, &c.State,
		&metAt, &metBy, &evid, &req, &c.Ord)
	if err != nil {
		return criterionOut{}, err
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
	return c, nil
}

func (s *Server) handleGetCriterion(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "criterion")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	c, err := s.loadCriterion(r.Context(), project, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "criterion not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleCreateCriterion(w http.ResponseWriter, r *http.Request) {
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
	var in criterionIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Phase == "" || in.Kind == "" {
		writeErr(w, http.StatusBadRequest, "phase and kind required")
		return
	}
	if !isValidCriterionKind(in.Kind) {
		writeErr(w, http.StatusBadRequest,
			"kind must be one of: text, metric, gate")
		return
	}
	if in.DeliverableID != "" {
		var found string
		err := s.db.QueryRowContext(r.Context(),
			`SELECT id FROM deliverables WHERE id = ? AND project_id = ?`,
			in.DeliverableID, project).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "deliverable_id not found in project")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	required := 1
	if in.Required != nil && !*in.Required {
		required = 0
	}
	ord := 0
	if in.Ord != nil {
		ord = *in.Ord
	}
	bodyJSON := "{}"
	if len(in.Body) > 0 {
		b, err := json.Marshal(in.Body)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "body must be a JSON object")
			return
		}
		bodyJSON = string(b)
	}
	id := NewID()
	now := NowUTC()
	var deliv any
	if in.DeliverableID != "" {
		deliv = in.DeliverableID
	}
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO acceptance_criteria (id, project_id, phase, deliverable_id,
			kind, body, state, required, ord, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`,
		id, project, in.Phase, deliv, in.Kind, bodyJSON, required, ord, now, now); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "criterion.created",
		"criterion", id,
		fmt.Sprintf("created %s criterion in phase %s", in.Kind, in.Phase),
		map[string]any{
			"project_id":     project,
			"phase":          in.Phase,
			"kind":           in.Kind,
			"deliverable_id": in.DeliverableID,
		})
	c, err := s.loadCriterion(r.Context(), project, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handlePatchCriterion(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	id := chi.URLParam(r, "criterion")
	if err := s.projectInTeam(r, team, project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var in criterionPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var found string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM acceptance_criteria WHERE id = ? AND project_id = ?`,
		id, project).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "criterion not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := NowUTC()
	q := `UPDATE acceptance_criteria SET updated_at = ?`
	args := []any{now}
	if in.Body != nil {
		b, err := json.Marshal(in.Body)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "body must be a JSON object")
			return
		}
		q += `, body = ?`
		args = append(args, string(b))
	}
	if in.EvidenceRef != nil {
		q += `, evidence_ref = NULLIF(?, '')`
		args = append(args, *in.EvidenceRef)
	}
	if in.Required != nil {
		req := 0
		if *in.Required {
			req = 1
		}
		q += `, required = ?`
		args = append(args, req)
	}
	if in.Ord != nil {
		q += `, ord = ?`
		args = append(args, *in.Ord)
	}
	q += ` WHERE id = ? AND project_id = ?`
	args = append(args, id, project)
	if _, err := s.db.ExecContext(r.Context(), q, args...); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	c, err := s.loadCriterion(r.Context(), project, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// transitionCriterion moves a criterion to one of the non-pending states.
// Returns the post-transition row + the audit kind to emit; callers wrap
// HTTP handling. terminal=true rejects re-firing on already-final state.
func (s *Server) transitionCriterion(
	ctx context.Context, project, id, newState, evidenceRef, actor string,
) (criterionOut, error) {
	if !isValidCriterionState(newState) {
		return criterionOut{}, fmt.Errorf("invalid state: %s", newState)
	}
	now := NowUTC()
	if newState == criterionStateMet {
		_, err := s.db.ExecContext(ctx, `
			UPDATE acceptance_criteria
			   SET state = ?, met_at = ?, met_by_actor = ?,
			       evidence_ref = COALESCE(NULLIF(?, ''), evidence_ref),
			       updated_at = ?
			 WHERE id = ? AND project_id = ?`,
			newState, now, actor, evidenceRef, now, id, project)
		if err != nil {
			return criterionOut{}, err
		}
	} else {
		_, err := s.db.ExecContext(ctx, `
			UPDATE acceptance_criteria
			   SET state = ?, met_at = NULL, met_by_actor = NULL,
			       evidence_ref = COALESCE(NULLIF(?, ''), evidence_ref),
			       updated_at = ?
			 WHERE id = ? AND project_id = ?`,
			newState, evidenceRef, now, id, project)
		if err != nil {
			return criterionOut{}, err
		}
	}
	return s.loadCriterion(ctx, project, id)
}

func (s *Server) handleMarkCriterion(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		team := chi.URLParam(r, "team")
		project := chi.URLParam(r, "project")
		id := chi.URLParam(r, "criterion")
		if err := s.projectInTeam(r, team, project); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "project not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		var in criterionMarkIn
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeErr(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		// Existence + current state check.
		c, err := s.loadCriterion(r.Context(), project, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "criterion not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Auto gate criteria are hub-internal-only — directors cannot
		// mark them by hand. The mark-met endpoint is for text + metric
		// (manual evaluation); gates auto-fire via the cascade below.
		if c.Kind == "gate" && action == "mark-met" {
			writeErr(w, http.StatusForbidden,
				"gate criteria are evaluated by the chassis; cannot be marked manually")
			return
		}
		newState := ""
		auditKind := ""
		switch action {
		case "mark-met":
			newState = criterionStateMet
			auditKind = "criterion.met"
		case "mark-failed":
			newState = criterionStateFailed
			auditKind = "criterion.failed"
		case "waive":
			newState = criterionStateWaived
			auditKind = "criterion.waived"
		default:
			writeErr(w, http.StatusBadRequest, "unknown action")
			return
		}
		_, actorKind, actorHandle := actorFromContext(r.Context())
		actor := actorKind
		if actorHandle != "" {
			actor = actorKind + ":" + actorHandle
		}
		out, err := s.transitionCriterion(r.Context(), project, id, newState,
			in.EvidenceRef, actor)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		summary := fmt.Sprintf("%s criterion · %s", newState, c.Kind)
		s.recordAudit(r.Context(), team, auditKind, "criterion", id, summary,
			map[string]any{
				"project_id":     project,
				"phase":          c.Phase,
				"kind":           c.Kind,
				"deliverable_id": c.DeliverableID,
				"evidence_ref":   in.EvidenceRef,
				"rationale":      in.Rationale,
				"reason":         in.Reason,
			})
		writeJSON(w, http.StatusOK, out)
	}
}

// cascadeDeliverableRatified — gate library entry. When a deliverable
// transitions to ratified, criteria with kind=gate and a body matching
// the `deliverable.ratified` gate (referencing this deliverable, or
// scoped to the deliverable's phase with no specific id) auto-fire to
// met. Emits criterion.met audit per fired criterion. Callers (the
// ratify handler) drive this synchronously after the state transition.
//
// Returns the IDs of criteria that fired so the caller can include them
// in its response payload (for client-side cache invalidation).
func (s *Server) cascadeDeliverableRatified(
	ctx context.Context, team, project, deliverableID, phase string,
) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, body, deliverable_id
		  FROM acceptance_criteria
		 WHERE project_id = ?
		   AND kind = 'gate'
		   AND state = 'pending'`, project)
	if err != nil {
		return nil, err
	}
	type pending struct {
		id    string
		body  map[string]any
		deliv string
	}
	var candidates []pending
	for rows.Next() {
		var id, bodyStr string
		var delivNS sql.NullString
		if err := rows.Scan(&id, &bodyStr, &delivNS); err != nil {
			rows.Close()
			return nil, err
		}
		body := map[string]any{}
		_ = json.Unmarshal([]byte(bodyStr), &body)
		deliv := ""
		if delivNS.Valid {
			deliv = delivNS.String
		}
		candidates = append(candidates, pending{id: id, body: body, deliv: deliv})
	}
	rows.Close()

	fired := []string{}
	for _, p := range candidates {
		if !gateMatchesDeliverableRatified(p.body, p.deliv, deliverableID, phase) {
			continue
		}
		_, err := s.transitionCriterion(ctx, project, p.id, criterionStateMet,
			"deliverable://"+deliverableID, "system:gate")
		if err != nil {
			return fired, err
		}
		s.recordAudit(ctx, team, "criterion.met", "criterion", p.id,
			"gate auto-fired by deliverable.ratified",
			map[string]any{
				"project_id":     project,
				"phase":          phase,
				"kind":           "gate",
				"deliverable_id": deliverableID,
				"gate":           "deliverable.ratified",
				"evidence_ref":   "deliverable://" + deliverableID,
				"auto":           true,
			})
		fired = append(fired, p.id)
	}
	return fired, nil
}

// gateMatchesDeliverableRatified decides whether a pending gate criterion
// fires for this ratification event. Body shape per A2 §7:
//
//	{ "gate": "deliverable.ratified",
//	  "params": { "deliverable_id": "deliv-..."  // optional
//	            } }
//
// Match rules:
//  1. body.gate must equal "deliverable.ratified" — other gates are
//     handled by their own cascades (W6 follow-up).
//  2. If body.params.deliverable_id is set, it must equal the just-
//     ratified deliverable.
//  3. Otherwise, the criterion's own deliverable_id column (if set) must
//     equal the ratified one. A gate criterion with no deliverable_id
//     and no params.deliverable_id only fires when the *phase* matches —
//     this lets templates declare "any deliverable in phase X ratified"
//     gates without naming the deliverable up front.
func gateMatchesDeliverableRatified(
	body map[string]any, criterionDeliverableID, ratifiedID, ratifiedPhase string,
) bool {
	gate, _ := body["gate"].(string)
	if gate != "deliverable.ratified" {
		return false
	}
	if params, ok := body["params"].(map[string]any); ok {
		if v, ok := params["deliverable_id"].(string); ok && v != "" {
			return v == ratifiedID
		}
	}
	if criterionDeliverableID != "" {
		return criterionDeliverableID == ratifiedID
	}
	// Fallback — phase-scoped gate; matches any ratification in this phase.
	// Caller restricts the cascade to criteria already in the same project,
	// so an unscoped gate in another phase won't get here unless we relax
	// that filter in the future.
	return ratifiedPhase != ""
}
