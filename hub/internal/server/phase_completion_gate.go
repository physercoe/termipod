// phase_completion_gate.go — WS1 completion gating (ADR-044 amendment
// 2026-06-08, ADR-046).
//
// Early-bind materializes every phase's deliverables / criteria / tasks at
// project create, and their *definitions* stay editable in any phase (the
// plan adapts as the project advances). Completion, however, is phase-gated:
// you may only *ratify* a deliverable or *mark a criterion met* for the phase
// the project is currently in. This file holds the shared gate check and the
// inverse-of-ratify cascade that re-pends a gate when its deliverable is
// unratified.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// phaseCompletionGate reports whether a completion action on an item in
// itemPhase is blocked by the project's current phase. It returns the
// project's current phase (for the caller's error message) and a blocked
// flag. Two cases are never gated, for back-compat:
//   - the project has no current phase (lifecycle-disabled / pre-phase), and
//   - the item itself carries no phase (an unphased, ad-hoc item).
func (s *Server) phaseCompletionGate(
	ctx context.Context, project, itemPhase string,
) (current string, blocked bool, err error) {
	var cur sql.NullString
	if err = s.db.QueryRowContext(ctx,
		`SELECT phase FROM projects WHERE id = ?`, project).Scan(&cur); err != nil {
		return "", false, err
	}
	current = cur.String
	if current == "" || itemPhase == "" {
		return current, false, nil
	}
	return current, itemPhase != current, nil
}

// phaseCompletionGateMsg is the human-readable reason returned to a caller
// whose completion action was gated.
func phaseCompletionGateMsg(itemPhase, current string) string {
	return fmt.Sprintf(
		"completion is gated to the active phase: this item is in phase %q "+
			"but the project is in phase %q (definitions stay editable; "+
			"ratify / mark-met only in the active phase)", itemPhase, current)
}

// cascadeDeliverableUnratified is the inverse of cascadeDeliverableRatified.
// When a deliverable returns ratified → draft, any gate criterion the
// ratification auto-fired (now `met` and matching this deliverable) is
// re-pended (met → pending), so the phase gate it cleared no longer counts as
// satisfied. Matching mirrors the forward cascade exactly. Best-effort: it
// returns the re-pended criterion ids; a failure is the caller's to log, not
// to fail the unratify on.
func (s *Server) cascadeDeliverableUnratified(
	ctx context.Context, team, project, deliverableID, phase string,
) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, body, deliverable_id
		  FROM acceptance_criteria
		 WHERE project_id = ?
		   AND kind = 'gate'
		   AND state = 'met'`, project)
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

	repended := []string{}
	for _, p := range candidates {
		if !gateMatchesDeliverableRatified(p.body, p.deliv, deliverableID, phase) {
			continue
		}
		if _, err := s.transitionCriterion(ctx, project, p.id,
			criterionStatePending, "", "system:unratify-repend"); err != nil {
			return repended, err
		}
		s.recordAudit(ctx, team, "criterion.repended", "criterion", p.id,
			"gate re-pended by deliverable.unratified",
			map[string]any{
				"project_id":     project,
				"phase":          phase,
				"kind":           "gate",
				"deliverable_id": deliverableID,
				"gate":           "deliverable.ratified",
				"auto":           true,
			})
		repended = append(repended, p.id)
	}
	return repended, nil
}
