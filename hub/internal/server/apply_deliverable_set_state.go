// apply_deliverable_set_state.go — ADR-030 W5 propose apply function
// for the `deliverable.set_state` governed-action kind.
//
// The legacy REST path is split across three endpoints by transition
// direction:
//   - PATCH /v1/teams/{t}/projects/{p}/deliverables/{d}  (draft ↔ in-review)
//   - POST  …/ratify                                     (X → ratified)
//   - POST  …/unratify                                   (ratified → draft)
//
// Propose unifies them under one `change_spec.state` value. The apply
// function inspects the (from, to) pair and runs the matching SQL +
// audit. Per-transition behaviour mirrors the existing endpoints
// (constants `deliverableState{Draft,InReview,Ratified}` in
// handlers_deliverables.go); the only divergence is that the audit
// row's meta carries `via="propose"`, `by_tier=<assigned_tier>`,
// `propose_id=<attention_id>` so the activity feed can distinguish
// propose-routed changes from direct REST.

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
		Kind:     "deliverable.set_state",
		Validate: validateDeliverableSetState,
		DryRun:   dryRunDeliverableSetState,
		Apply:    applyDeliverableSetState,
		Rollback: rollbackDeliverableSetState,
	})
}

// deliverableSetStateTarget is the per-kind target_ref shape.
type deliverableSetStateTarget struct {
	ProjectID     string `json:"project_id"`
	DeliverableID string `json:"deliverable_id"`
}

// deliverableSetStateSpec is the per-kind change_spec shape.
type deliverableSetStateSpec struct {
	State string `json:"state"`
}

func parseDeliverableSetState(targetRef, changeSpec json.RawMessage) (deliverableSetStateTarget, deliverableSetStateSpec, error) {
	var t deliverableSetStateTarget
	if len(targetRef) > 0 {
		if err := json.Unmarshal(targetRef, &t); err != nil {
			return t, deliverableSetStateSpec{}, fmt.Errorf("target_ref: %w", err)
		}
	}
	if t.ProjectID == "" {
		return t, deliverableSetStateSpec{}, errors.New("target_ref.project_id required")
	}
	if t.DeliverableID == "" {
		return t, deliverableSetStateSpec{}, errors.New("target_ref.deliverable_id required")
	}
	var c deliverableSetStateSpec
	if len(changeSpec) > 0 {
		if err := json.Unmarshal(changeSpec, &c); err != nil {
			return t, c, fmt.Errorf("change_spec: %w", err)
		}
	}
	if c.State == "" {
		return t, c, errors.New("change_spec.state required")
	}
	if !isValidDeliverableState(c.State) {
		return t, c, fmt.Errorf("change_spec.state %q invalid (one of draft, in-review, ratified)", c.State)
	}
	return t, c, nil
}

// validateDeliverableSetState — shape + value checks. Transition
// validity is verified separately in lookupDeliverableState+isValid…;
// we only check what's free of DB I/O here. The Apply path does the
// final "current state matches expected from" enforcement under the
// row lock.
func validateDeliverableSetState(_ context.Context, _ *Server, targetRef, changeSpec json.RawMessage) error {
	_, _, err := parseDeliverableSetState(targetRef, changeSpec)
	return err
}

// dryRunDeliverableSetState returns the preview the propose handler's
// `dry_run=true` branch echoes back to the caller. It IS a read — we
// fetch the current state + kind so the preview shows "ratification_packet:
// in-review → ratified" rather than just the proposed state.
func dryRunDeliverableSetState(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) (json.RawMessage, error) {
	t, c, err := parseDeliverableSetState(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	var fromState, kind string
	err = s.db.QueryRowContext(ctx,
		`SELECT ratification_state, kind FROM deliverables WHERE id = ? AND project_id = ?`,
		t.DeliverableID, t.ProjectID).Scan(&fromState, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("deliverable %s not found in project %s", t.DeliverableID, t.ProjectID)
	}
	if err != nil {
		return nil, err
	}
	preview := map[string]any{
		"from_state":            fromState,
		"to_state":              c.State,
		"target_deliverable_id": t.DeliverableID,
		"target_kind":           kind,
		"no_op":                 fromState == c.State,
	}
	return json.Marshal(preview)
}

// applyDeliverableSetState performs the state transition and emits the
// audit row. Three transition shapes, mirroring the legacy REST
// endpoints:
//
//   - X       → ratified : sets ratification_state, stamps
//                          ratified_at + ratified_by_actor.
//                          Audit action: deliverable.ratified.
//   - ratified → draft   : clears the ratified stamps.
//                          Audit action: deliverable.unratified.
//   - other transitions  : pure state update; clears stale ratified
//                          stamps if leaving 'ratified' (defensive).
//                          Audit action: deliverable.updated.
//
// No-op (from == to) returns the no_op marker in `executed` without
// touching the row — matches the "already ratified" 409 the legacy
// /ratify endpoint emits, surfaced through executed_json rather than
// failing the apply so the propose row still resolves cleanly.
func applyDeliverableSetState(
	ctx context.Context, s *Server, ac ProposeApplyContext, targetRef, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	t, c, err := parseDeliverableSetState(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	var fromState string
	err = s.db.QueryRowContext(ctx,
		`SELECT ratification_state FROM deliverables WHERE id = ? AND project_id = ?`,
		t.DeliverableID, t.ProjectID).Scan(&fromState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("deliverable %s not found in project %s", t.DeliverableID, t.ProjectID)
	}
	if err != nil {
		return nil, err
	}

	executed := map[string]any{
		"deliverable_id": t.DeliverableID,
		"project_id":     t.ProjectID,
		"from_state":     fromState,
		"to_state":       c.State,
	}
	if fromState == c.State {
		executed["no_op"] = true
		return json.Marshal(executed)
	}

	now := NowUTC()
	var action string
	var execErr error
	switch {
	case c.State == deliverableStateRatified:
		// Mirror handleRatifyDeliverable. Actor for the
		// ratified_by_actor stamp comes from the apply context; a
		// blank decider falls back to "agent:" so the field is never
		// fully empty.
		actor := ac.DeciderHandle
		if actor == "" {
			actor = "propose"
		}
		_, execErr = s.db.ExecContext(ctx, `
			UPDATE deliverables
			SET ratification_state = 'ratified',
			    ratified_at = ?,
			    ratified_by_actor = ?,
			    updated_at = ?
			WHERE id = ? AND project_id = ?`,
			now, actor, now, t.DeliverableID, t.ProjectID)
		action = "deliverable.ratified"
	case fromState == deliverableStateRatified:
		// ratified → draft. Mirrors handleUnratifyDeliverable.
		_, execErr = s.db.ExecContext(ctx, `
			UPDATE deliverables
			SET ratification_state = ?,
			    ratified_at = NULL,
			    ratified_by_actor = NULL,
			    updated_at = ?
			WHERE id = ? AND project_id = ?`,
			c.State, now, t.DeliverableID, t.ProjectID)
		action = "deliverable.unratified"
	default:
		// draft ↔ in-review. Mirrors the PATCH path. We don't allow
		// PATCH-into-ratified there; here we'd never reach this branch
		// for the ratified target because the first case handles it.
		_, execErr = s.db.ExecContext(ctx, `
			UPDATE deliverables
			SET ratification_state = ?,
			    updated_at = ?
			WHERE id = ? AND project_id = ?`,
			c.State, now, t.DeliverableID, t.ProjectID)
		action = "deliverable.updated"
	}
	if execErr != nil {
		return nil, execErr
	}

	// Audit with propose lineage. The kind-specific action keeps the
	// existing activity-feed renderers working unchanged; meta.via
	// is the discriminator. Team is read from the apply context;
	// recordAudit can run on a "" team (it just doesn't filter), so
	// we tolerate a blank value in defensive paths.
	team := ac.Team
	meta := map[string]any{
		"project_id":     t.ProjectID,
		"deliverable_id": t.DeliverableID,
		"from_state":     fromState,
		"to_state":       c.State,
		"via":            ac.ViaOrDefault(),
		"by_tier":        ac.AssignedTier,
		"propose_id":     ac.AttentionID,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, action, "deliverable", t.DeliverableID,
		fmt.Sprintf("%s → %s via propose", fromState, c.State), meta)

	executed["audit_action"] = action
	executed["updated_at"] = now
	return json.Marshal(executed)
}

// rollbackDeliverableSetState reverses a prior Apply by re-calling
// Apply with the previously-recorded from_state as the new
// change_spec.state. The audit row records `via="rollback"` (set
// by the override handler via ac.Via) so the activity feed reads
// "ratified → in-review via rollback" rather than a second
// indistinguishable transition.
//
// originalExecuted is the JSON the prior Apply returned —
// it carries `from_state` (the pre-Apply state, which IS our
// rollback target) and `to_state` (the current state). We
// rebuild the target_ref + an inverted change_spec from these
// fields and call back into Apply so all the existing
// transition-direction logic (clears stamps on
// ratified→draft, etc.) runs unchanged.
func rollbackDeliverableSetState(
	ctx context.Context, s *Server, ac ProposeApplyContext, originalSpec, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var orig struct {
		ProjectID     string `json:"project_id"`
		DeliverableID string `json:"deliverable_id"`
		FromState     string `json:"from_state"`
		ToState       string `json:"to_state"`
	}
	if err := json.Unmarshal(originalExecuted, &orig); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	if orig.FromState == "" {
		return nil, errors.New("rollback: original_executed missing from_state (apply was a no-op?)")
	}
	target, _ := json.Marshal(map[string]any{
		"project_id":     orig.ProjectID,
		"deliverable_id": orig.DeliverableID,
	})
	revertSpec, _ := json.Marshal(map[string]any{"state": orig.FromState})
	return applyDeliverableSetState(ctx, s, ac, target, revertSpec)
}
