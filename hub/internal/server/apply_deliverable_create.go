// apply_deliverable_create.go — ADR-044 P2 propose apply function for the
// `deliverable.create` governed-action kind.
//
// Hydration (issue #20) materializes the template's deliverable slots on
// phase entry; the agent fills them via the P1 direct tools. But adding a
// deliverable BEYOND the template changes the ratification surface the
// director is accountable for, so a *new* deliverable is governed: the
// steward/agent proposes it, the director approves, and the apply inserts a
// draft row the agent then materializes (attach components, submit) as
// usual. Mirrors the row-insert half of handleCreateDeliverable; components
// are attached afterward via deliverables.add_component, so the apply stays
// a single-row create with a clean DELETE rollback.

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
		Kind:     "deliverable.create",
		Validate: validateDeliverableCreate,
		DryRun:   dryRunDeliverableCreate,
		Apply:    applyDeliverableCreate,
		Rollback: rollbackDeliverableCreate,
	})
}

// deliverableCreateTarget is the per-kind target_ref shape.
type deliverableCreateTarget struct {
	ProjectID string `json:"project_id"`
}

// deliverableCreateSpec is the per-kind change_spec shape — the new
// deliverable's slot fields (kind/phase), mirroring deliverableIn minus
// the components, which attach later via the direct tool.
type deliverableCreateSpec struct {
	Phase    string `json:"phase"`
	Kind     string `json:"kind"`
	Required *bool  `json:"required,omitempty"`
	Ord      *int   `json:"ord,omitempty"`
}

func parseDeliverableCreate(targetRef, changeSpec json.RawMessage) (deliverableCreateTarget, deliverableCreateSpec, error) {
	var t deliverableCreateTarget
	if len(targetRef) > 0 {
		if err := json.Unmarshal(targetRef, &t); err != nil {
			return t, deliverableCreateSpec{}, fmt.Errorf("target_ref: %w", err)
		}
	}
	if t.ProjectID == "" {
		return t, deliverableCreateSpec{}, errors.New("target_ref.project_id required")
	}
	var c deliverableCreateSpec
	if len(changeSpec) > 0 {
		if err := json.Unmarshal(changeSpec, &c); err != nil {
			return t, c, fmt.Errorf("change_spec: %w", err)
		}
	}
	if c.Phase == "" {
		return t, c, errors.New("change_spec.phase required")
	}
	if c.Kind == "" {
		return t, c, errors.New("change_spec.kind required")
	}
	return t, c, nil
}

func validateDeliverableCreate(_ context.Context, _ *Server, targetRef, changeSpec json.RawMessage) error {
	_, _, err := parseDeliverableCreate(targetRef, changeSpec)
	return err
}

func dryRunDeliverableCreate(_ context.Context, _ *Server, targetRef, changeSpec json.RawMessage) (json.RawMessage, error) {
	t, c, err := parseDeliverableCreate(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"project_id": t.ProjectID,
		"phase":      c.Phase,
		"kind":       c.Kind,
		"new_state":  deliverableStateDraft,
	})
}

func applyDeliverableCreate(
	ctx context.Context, s *Server, ac ProposeApplyContext, targetRef, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	t, c, err := parseDeliverableCreate(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	// The project must exist in the apply context's team — the propose
	// row was scoped to that team, so a mismatch is a bad target_ref.
	if err := s.projectInTeamCtx(ctx, ac.Team, t.ProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s not found in team", t.ProjectID)
		}
		return nil, err
	}
	required := 1
	if c.Required != nil && !*c.Required {
		required = 0
	}
	ord := 0
	if c.Ord != nil {
		ord = *c.Ord
	}
	id := NewID()
	now := NowUTC()
	if _, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO deliverables (id, project_id, phase, kind,
			ratification_state, required, ord, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'draft', ?, ?, ?, ?)`,
		id, t.ProjectID, c.Phase, c.Kind, required, ord, now, now); err != nil {
		return nil, err
	}

	meta := map[string]any{
		"project_id":     t.ProjectID,
		"deliverable_id": id,
		"phase":          c.Phase,
		"kind":           c.Kind,
		"via":            ac.ViaOrDefault(),
		"by_tier":        ac.AssignedTier,
		"propose_id":     ac.AttentionID,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, ac.Team, "deliverable.created", "deliverable", id,
		fmt.Sprintf("created %s deliverable in phase %s via propose", c.Kind, c.Phase), meta)

	return json.Marshal(map[string]any{
		"deliverable_id":     id,
		"project_id":         t.ProjectID,
		"phase":              c.Phase,
		"kind":               c.Kind,
		"required":           required != 0,
		"ord":                ord,
		"ratification_state": deliverableStateDraft,
		"created_at":         now,
	})
}

// rollbackDeliverableCreate deletes the deliverable the prior Apply
// inserted (and any components attached to it since), reversing the
// create. originalExecuted carries the deliverable_id.
func rollbackDeliverableCreate(
	ctx context.Context, s *Server, ac ProposeApplyContext, _, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var orig struct {
		DeliverableID string `json:"deliverable_id"`
		ProjectID     string `json:"project_id"`
	}
	if err := json.Unmarshal(originalExecuted, &orig); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	if orig.DeliverableID == "" {
		return nil, errors.New("rollback: original_executed missing deliverable_id")
	}
	// Cascade the two deletes in one transaction: a deliverable row left
	// behind with its components already gone (component DELETE ok, parent
	// DELETE failed) is the inconsistent rollback state #76 flagged.
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // no-op once Commit succeeds
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM deliverable_components WHERE deliverable_id = ?`, orig.DeliverableID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM deliverables WHERE id = ? AND project_id = ?`,
		orig.DeliverableID, orig.ProjectID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, ac.Team, "deliverable.deleted", "deliverable", orig.DeliverableID,
		"deliverable.create rolled back", map[string]any{
			"project_id":     orig.ProjectID,
			"deliverable_id": orig.DeliverableID,
			"via":            "rollback",
			"propose_id":     ac.AttentionID,
		})
	return json.Marshal(map[string]any{
		"deliverable_id": orig.DeliverableID,
		"rolled_back":    true,
	})
}
