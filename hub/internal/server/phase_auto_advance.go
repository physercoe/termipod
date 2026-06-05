// phase_auto_advance.go — ADR-044 P3. AC-driven, system-approved phase
// advance.
//
// When a required acceptance criterion becomes satisfied (met or waived),
// the hub checks whether the project's current phase now has ALL its
// required criteria satisfied and, if so, advances to the next phase
// automatically — no propose, no separate human approval step. Human
// judgment is expressed as a `gate`-kind criterion (a human marks it, or
// the deliverable-ratify cascade fires it), so the human-in-the-loop lives
// INSIDE the criteria set rather than as a standalone phase-advance gate
// (ADR-044 decision 3, Q3/Q4). An unmet required criterion blocks the
// advance — the evaluator simply does not fire.
//
// This replaces the retired propose `phase.advance` kind (Q4). The
// director's manual REST advance (handleAdvanceProjectPhase) remains for
// explicit, off-criteria moves. The criterion-mark paths
// (handleMarkCriterion, cascadeDeliverableRatified) call this after a
// transition; it is best-effort, so a failure here must not fail the mark.

package server

import (
	"context"
	"encoding/json"
)

// maybeAutoAdvancePhase advances the project one phase when every required
// criterion for its current phase is satisfied (met or waived). It is a
// no-op (advanced=false, err=nil) when:
//
//   - the project has no current phase (nothing to advance from);
//   - the template declares no further phase (already at the last);
//   - the current phase declares no required criteria (not AC-gated — it
//     waits for a manual advance rather than cascading forward on an
//     unrelated mark);
//   - any required criterion is still pending or failed (blocked, Q3).
//
// One step per call (no recursion): a freshly-hydrated destination phase
// has pending criteria, so a follow-on mark drives the next step. Idempotent
// and safe to call after every criterion transition.
func (s *Server) maybeAutoAdvancePhase(
	ctx context.Context, team, project string,
) (advanced bool, err error) {
	phase, history, templateID, err := s.loadProjectPhaseRow(ctx, team, project)
	if err != nil {
		return false, err
	}
	if phase == "" {
		return false, nil
	}
	to := nextPhase(s.templatePhases(templateID), phase)
	if to == "" {
		return false, nil
	}
	total, pending, err := s.requiredCriteriaCounts(ctx, project, phase)
	if err != nil {
		return false, err
	}
	if total == 0 || pending > 0 {
		return false, nil
	}

	now := NowUTC()
	history.Transitions = append(history.Transitions,
		phaseTransition{From: phase, To: to, At: now, ByActor: "system:auto"})
	historyJSON, err := json.Marshal(history)
	if err != nil {
		return false, err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE projects SET phase = ?, phase_history = ? WHERE team_id = ? AND id = ?`,
		to, string(historyJSON), team, project); err != nil {
		return false, err
	}
	s.recordAudit(ctx, team, "project.phase_advanced", "project", project,
		"auto-advance "+phase+" → "+to+" (all required criteria met)",
		map[string]any{
			"from":     phase,
			"to":       to,
			"via":      "auto-advance",
			"trigger":  "criteria-met",
			"required": total,
		})
	// Hydrate the destination phase's deliverables + criteria (issue #20).
	s.hydratePhase(ctx, team, project, templateID, to)
	return true, nil
}
