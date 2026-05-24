// apply_phase_advance.go — ADR-030 W6 propose apply function for the
// `phase.advance` governed-action kind.
//
// The legacy REST equivalent is POST /v1/teams/{t}/projects/{p}/phase/advance
// (handleAdvanceProjectPhase). Propose differs in two ways:
//
//   1. Optimistic concurrency. The caller passes the `from_phase` they
//      expect; Apply re-reads the row under the lock and rejects if
//      the project has already advanced. The legacy endpoint walks
//      "current → next" implicitly; here the proposer staked on a
//      specific transition at propose time, and the principal is
//      approving THAT transition, not whatever's current now.
//   2. Acceptance-criteria gating is intentionally NOT enforced at
//      Apply time. The legacy endpoint 409's when required criteria
//      are pending; here, the approver IS the gate — if the principal
//      approves a phase advance, they're overriding criteria
//      explicitly. The propose summary should make that clear in the
//      `reason` field; the audit row carries the same `via="propose"`
//      stamp the deliverable apply uses.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func init() {
	RegisterProposeKind(ProposeKind{
		Kind:     "phase.advance",
		Validate: validatePhaseAdvance,
		DryRun:   dryRunPhaseAdvance,
		Apply:    applyPhaseAdvance,
	})
}

// phaseAdvanceTarget is the per-kind target_ref shape.
type phaseAdvanceTarget struct {
	ProjectID string `json:"project_id"`
}

// phaseAdvanceSpec is the per-kind change_spec shape. `from_phase` is
// optional; when present, Apply rejects on mismatch (optimistic-
// concurrency check). `to_phase` is required.
type phaseAdvanceSpec struct {
	FromPhase string `json:"from_phase,omitempty"`
	ToPhase   string `json:"to_phase"`
	Reason    string `json:"reason,omitempty"`
}

func parsePhaseAdvance(targetRef, changeSpec json.RawMessage) (phaseAdvanceTarget, phaseAdvanceSpec, error) {
	var t phaseAdvanceTarget
	if len(targetRef) > 0 {
		if err := json.Unmarshal(targetRef, &t); err != nil {
			return t, phaseAdvanceSpec{}, fmt.Errorf("target_ref: %w", err)
		}
	}
	if t.ProjectID == "" {
		return t, phaseAdvanceSpec{}, errors.New("target_ref.project_id required")
	}
	var c phaseAdvanceSpec
	if len(changeSpec) > 0 {
		if err := json.Unmarshal(changeSpec, &c); err != nil {
			return t, c, fmt.Errorf("change_spec: %w", err)
		}
	}
	if c.ToPhase == "" {
		return t, c, errors.New("change_spec.to_phase required")
	}
	return t, c, nil
}

// validatePhaseAdvance is a pure shape check — DB I/O is deferred to
// Apply (the row-read happens once, under the lock).
func validatePhaseAdvance(_ context.Context, _ *Server, targetRef, changeSpec json.RawMessage) error {
	_, _, err := parsePhaseAdvance(targetRef, changeSpec)
	return err
}

// dryRunPhaseAdvance reads the project's current phase + the template's
// phase set so the preview can flag two things the caller might not
// have realised:
//
//   - `no_op` when current == to_phase.
//   - `to_phase_not_in_template` when the template declares phases
//     and the proposed to_phase isn't one of them. Apply still
//     permits the transition (templates evolve; an admin override
//     might legitimately pin an off-template phase), but the preview
//     surfaces the disagreement.
func dryRunPhaseAdvance(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) (json.RawMessage, error) {
	t, c, err := parsePhaseAdvance(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	curPhase, _, templateID, err := s.loadProjectPhaseRow(ctx, "", t.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		// loadProjectPhaseRow needs team_id for its WHERE; without it
		// we fall back to a permissive read so DryRun still works
		// across teams. The Apply path takes ac.Team, which is the
		// authoritative scope.
		curPhase, _, templateID, err = s.loadProjectPhaseRowAnyTeam(ctx, t.ProjectID)
	}
	if err != nil {
		return nil, fmt.Errorf("read project %s: %w", t.ProjectID, err)
	}
	phases := s.templatePhases(templateID)
	preview := map[string]any{
		"project_id":               t.ProjectID,
		"from_phase":               curPhase,
		"to_phase":                 c.ToPhase,
		"no_op":                    curPhase == c.ToPhase,
		"to_phase_not_in_template": len(phases) > 0 && !phaseInSet(phases, c.ToPhase),
	}
	if c.FromPhase != "" {
		preview["from_phase_expected"] = c.FromPhase
		preview["from_phase_drifted"] = c.FromPhase != curPhase
	}
	return json.Marshal(preview)
}

// loadProjectPhaseRowAnyTeam mirrors loadProjectPhaseRow but skips the
// team_id scope so the DryRun preview can read a project the apply-
// context doesn't yet know the team of. Apply itself always knows the
// team (via ac.Team) and uses the scoped loader for safety.
func (s *Server) loadProjectPhaseRowAnyTeam(
	ctx context.Context, project string,
) (phase string, history phaseHistoryDoc, templateID string, err error) {
	var phaseNS, historyNS, tplNS sql.NullString
	row := s.db.QueryRowContext(ctx, `
		SELECT phase, phase_history, template_id
		FROM projects
		WHERE id = ?`, project)
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

// applyPhaseAdvance performs the phase advance and emits the audit row.
//
// Three rejection paths:
//
//   - Project not found in the apply context's team → "project not found"
//     (treats team mismatch as not-found per established convention).
//   - Stale from_phase: caller staked on `from_phase = X`, current is Y.
//     Rejected with a descriptive message so the agent can re-propose
//     against the new current phase.
//   - No-op (current == to_phase): returns `executed.no_op = true`,
//     does not touch the row or emit an audit. Mirrors W5's no-op
//     semantics.
//
// Otherwise: append a phaseTransition to phase_history, UPDATE the row,
// emit `project.phase_advanced` audit with the propose lineage on meta.
func applyPhaseAdvance(
	ctx context.Context, s *Server, ac ProposeApplyContext, targetRef, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	t, c, err := parsePhaseAdvance(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("phase.advance: apply context missing team")
	}
	curPhase, history, _, err := s.loadProjectPhaseRow(ctx, team, t.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("project %s not found in team %s", t.ProjectID, team)
	}
	if err != nil {
		return nil, fmt.Errorf("read project: %w", err)
	}
	if c.FromPhase != "" && c.FromPhase != curPhase {
		return nil, fmt.Errorf(
			"phase.advance: stale from_phase — proposed %q but project is now at %q",
			c.FromPhase, curPhase)
	}

	executed := map[string]any{
		"project_id": t.ProjectID,
		"from_phase": curPhase,
		"to_phase":   c.ToPhase,
	}
	if curPhase == c.ToPhase {
		executed["no_op"] = true
		return json.Marshal(executed)
	}

	now := NowUTC()
	transition := phaseTransition{From: curPhase, To: c.ToPhase, At: now}
	if ac.DeciderHandle != "" {
		transition.ByActor = ac.DeciderHandle
	} else {
		transition.ByActor = "propose"
	}
	history.Transitions = append(history.Transitions, transition)
	historyJSON, err := json.Marshal(history)
	if err != nil {
		return nil, fmt.Errorf("marshal history: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE projects SET phase = ?, phase_history = ?
		WHERE team_id = ? AND id = ?`,
		c.ToPhase, string(historyJSON), team, t.ProjectID); err != nil {
		return nil, fmt.Errorf("update phase: %w", err)
	}

	// Action mirrors the legacy phase-advance handler. `phase_set`
	// would imply NULL → first-phase hydration, which can't reach
	// the Apply path through propose (callers don't propose
	// initial-hydration). Stay strict on phase_advanced.
	action := "project.phase_advanced"
	summary := fmt.Sprintf("advance phase %s → %s via propose", curPhase, c.ToPhase)
	meta := map[string]any{
		"project_id": t.ProjectID,
		"from":       curPhase,
		"to":         c.ToPhase,
		"reason":     c.Reason,
		"via":        "propose",
		"by_tier":    ac.AssignedTier,
		"propose_id": ac.AttentionID,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, action, "project", t.ProjectID, summary, meta)

	executed["audit_action"] = action
	executed["updated_at"] = now
	return json.Marshal(executed)
}
