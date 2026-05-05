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

// phaseOut is the read shape for GET /v1/teams/{team}/projects/{project_id}/phase
// and the body returned by POST /phase/advance + POST /phase. All fields can
// be empty when the project is lifecycle-disabled (template declared no
// phase set); callers treat phase=="" as "render the legacy Overview".
type phaseOut struct {
	ProjectID string             `json:"project_id"`
	Phase     string             `json:"phase,omitempty"`
	Phases    []string           `json:"phases,omitempty"`
	History   []phaseTransition  `json:"history,omitempty"`
}

// phaseTransition is one entry in projects.phase_history's `transitions`
// array (see reference/project-phase-schema.md §4.3). audit_events remains
// the canonical log; this column is denormalized for fast reads.
type phaseTransition struct {
	From         string `json:"from"`
	To           string `json:"to"`
	At           string `json:"at"`
	ByActor      string `json:"by_actor,omitempty"`
	AuditEventID string `json:"audit_event_id,omitempty"`
}

// phaseHistoryDoc is the JSON shape persisted in projects.phase_history.
type phaseHistoryDoc struct {
	Transitions []phaseTransition `json:"transitions"`
}

// projectPhaseAdvanceIn is the request body for POST /phase/advance.
// `to_phase` is optional — if omitted, the hub advances to the phase
// immediately following the current one in the template's phase list.
type projectPhaseAdvanceIn struct {
	ToPhase string `json:"to_phase,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// projectPhaseSetIn is the request body for POST /phase. Used for admin
// hydration / repair only. `phase` is required; templates' first-phase
// hydration on project create goes through the create handler instead.
type projectPhaseSetIn struct {
	Phase string `json:"phase"`
}

// loadProjectPhaseRow reads the phase + phase_history + template_id for
// one project. Returns sql.ErrNoRows if the project doesn't exist or
// belongs to a different team.
func (s *Server) loadProjectPhaseRow(
	ctx context.Context, team, project string,
) (phase string, history phaseHistoryDoc, templateID string, err error) {
	var phaseNS, historyNS, tplNS sql.NullString
	row := s.db.QueryRowContext(ctx, `
		SELECT phase, phase_history, template_id
		FROM projects
		WHERE team_id = ? AND id = ?`, team, project)
	if err = row.Scan(&phaseNS, &historyNS, &tplNS); err != nil {
		return "", phaseHistoryDoc{}, "", err
	}
	if phaseNS.Valid {
		phase = phaseNS.String
	}
	if historyNS.Valid && historyNS.String != "" {
		_ = json.Unmarshal([]byte(historyNS.String), &history)
	}
	if tplNS.Valid {
		templateID = tplNS.String
	}
	return phase, history, templateID, nil
}

// templatePhases returns the phase order declared by templateID, or nil
// if the template doesn't exist or doesn't declare phases. Reads off
// disk + embedded FS on each call; cardinality stays small (cf. the
// `resolveOverviewWidget` rationale in handlers_projects.go).
func (s *Server) templatePhases(templateID string) []string {
	if templateID == "" {
		return nil
	}
	docs, err := loadProjectTemplates(s.cfg.DataRoot)
	if err != nil {
		s.log.Warn("loadProjectTemplates failed during phase resolve",
			"err", err, "template", templateID)
		return nil
	}
	for _, d := range docs {
		if d.Name == templateID {
			return d.Phases
		}
	}
	return nil
}

// nextPhase returns the phase following current in phases, or "" when
// current is empty (no phase yet) returns the first phase, or when
// current is the last phase returns "" to mean "no phase to advance to."
func nextPhase(phases []string, current string) string {
	if len(phases) == 0 {
		return ""
	}
	if current == "" {
		return phases[0]
	}
	for i, p := range phases {
		if p == current && i+1 < len(phases) {
			return phases[i+1]
		}
	}
	return ""
}

// requiredCriteriaPending counts acceptance_criteria rows for project +
// phase that are required and still pending or failed. W1 always sees
// zero (no criteria are hydrated until W7's template content lands +
// W6's criterion runtime), so phase advance is unblocked by default —
// W6 supplies the actual gating semantics.
func (s *Server) requiredCriteriaPending(
	ctx context.Context, project, phase string,
) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM acceptance_criteria
		WHERE project_id = ?
		  AND phase = ?
		  AND required = 1
		  AND state IN ('pending','failed')`, project, phase).Scan(&n)
	return n, err
}

func (s *Server) handleGetProjectPhase(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	phase, history, templateID, err := s.loadProjectPhaseRow(r.Context(), team, project)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := phaseOut{
		ProjectID: project,
		Phase:     phase,
		Phases:    s.templatePhases(templateID),
		History:   history.Transitions,
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdvanceProjectPhase(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")

	var in projectPhaseAdvanceIn
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
	}

	phase, history, templateID, err := s.loadProjectPhaseRow(r.Context(), team, project)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	phases := s.templatePhases(templateID)
	if len(phases) == 0 {
		writeErr(w, http.StatusUnprocessableEntity,
			"project's template declares no phase set")
		return
	}

	// Decide the destination. Explicit `to_phase` wins (and must be in the
	// template's set); otherwise we walk one step forward. Either way the
	// destination must be reachable from the current phase — no leap over
	// gates.
	to := in.ToPhase
	if to == "" {
		to = nextPhase(phases, phase)
		if to == "" {
			writeErr(w, http.StatusConflict, "no further phase to advance to")
			return
		}
	} else if !phaseInSet(phases, to) {
		writeErr(w, http.StatusBadRequest,
			"to_phase is not in the project's template phase set")
		return
	}

	// Required criteria for the *current* phase must be cleared. When the
	// project has no phase yet (NULL → first phase hydration), there's
	// nothing to gate against; treat that as a phase_set rather than an
	// advance.
	if phase != "" {
		pending, err := s.requiredCriteriaPending(r.Context(), project, phase)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if pending > 0 {
			writeProblem(w, http.StatusConflict, "phase-criteria-pending",
				fmt.Sprintf("%d required criteria for phase %q are not yet met",
					pending, phase),
				map[string]any{
					"project_id":      project,
					"phase":           phase,
					"pending_count":   pending,
					"target_phase":    to,
				})
			return
		}
	}

	now := NowUTC()
	transition := phaseTransition{
		From: phase,
		To:   to,
		At:   now,
	}
	if _, k, h := actorFromContext(r.Context()); k != "" {
		if h != "" {
			transition.ByActor = k + ":" + h
		} else {
			transition.ByActor = k
		}
	}
	history.Transitions = append(history.Transitions, transition)
	historyJSON, err := json.Marshal(history)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE projects SET phase = ?, phase_history = ? WHERE team_id = ? AND id = ?`,
		to, string(historyJSON), team, project); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	action := "project.phase_advanced"
	summary := "advance phase " + phase + " → " + to
	if phase == "" {
		action = "project.phase_set"
		summary = "set initial phase " + to
	}
	s.recordAudit(r.Context(), team, action, "project", project, summary,
		map[string]any{
			"from":          phase,
			"to":            to,
			"criteria_met":  []string{},
			"reason":        in.Reason,
		})

	writeJSON(w, http.StatusOK, phaseOut{
		ProjectID: project,
		Phase:     to,
		Phases:    phases,
		History:   history.Transitions,
	})
}

// handleSetProjectPhase is the admin/hydration endpoint. It writes
// projects.phase directly without consulting acceptance criteria.
// Use cases: initial phase hydration during project create, repair
// after a botched migration, admin-triggered phase revert.
func (s *Server) handleSetProjectPhase(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := chi.URLParam(r, "project")
	var in projectPhaseSetIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Phase == "" {
		writeErr(w, http.StatusBadRequest, "phase required")
		return
	}

	phase, history, templateID, err := s.loadProjectPhaseRow(r.Context(), team, project)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	phases := s.templatePhases(templateID)
	if len(phases) > 0 && !phaseInSet(phases, in.Phase) {
		writeErr(w, http.StatusBadRequest,
			"phase is not in the project's template phase set")
		return
	}

	now := NowUTC()
	transition := phaseTransition{From: phase, To: in.Phase, At: now}
	if _, k, h := actorFromContext(r.Context()); k != "" {
		if h != "" {
			transition.ByActor = k + ":" + h
		} else {
			transition.ByActor = k
		}
	}
	history.Transitions = append(history.Transitions, transition)
	historyJSON, err := json.Marshal(history)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE projects SET phase = ?, phase_history = ? WHERE team_id = ? AND id = ?`,
		in.Phase, string(historyJSON), team, project); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// phase_set fires when going from NULL → phase or admin-setting an
	// arbitrary phase; phase_reverted fires when moving to an earlier
	// phase in the template's order. Forward jumps that aren't simple
	// "advance one" still emit phase_set since they bypass the gate.
	action := "project.phase_set"
	if phase != "" && phaseIndex(phases, in.Phase) < phaseIndex(phases, phase) {
		action = "project.phase_reverted"
	}
	s.recordAudit(r.Context(), team, action, "project", project,
		"set phase to "+in.Phase,
		map[string]any{"from": phase, "to": in.Phase})

	writeJSON(w, http.StatusOK, phaseOut{
		ProjectID: project,
		Phase:     in.Phase,
		Phases:    phases,
		History:   history.Transitions,
	})
}

func phaseInSet(set []string, p string) bool {
	for _, x := range set {
		if x == p {
			return true
		}
	}
	return false
}

func phaseIndex(set []string, p string) int {
	for i, x := range set {
		if x == p {
			return i
		}
	}
	return -1
}

// writeProblem emits an RFC 7807 problem-detail body. Reused by
// lifecycle handlers that need to return structured error context
// (pending-count, target_phase, etc.) rather than the bare {"error":...}
// shape that writeErr produces.
func writeProblem(w http.ResponseWriter, status int, code, detail string, ext map[string]any) {
	body := map[string]any{
		"type":   "about:blank",
		"title":  http.StatusText(status),
		"status": status,
		"code":   code,
		"detail": detail,
	}
	for k, v := range ext {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
