// apply_criteria.go — ADR-044 P2 propose apply functions for the
// `criteria.create` / `criteria.update` / `criteria.delete` governed-action
// kinds.
//
// A project template's acceptance criteria are a *draft roadmap*, not a
// frozen contract (ADR-044): as a project meets reality the gates the
// director ratifies against must be addable, revisable, and removable. But
// because criteria define those gates, every *definition* change is
// governed — the steward/agent proposes, the director approves. (Marking a
// criterion met/failed/waived is the separate direct action in
// handlers_criteria.go + the P1 criteria.set_state tool, not a propose.)
//
// Three single-purpose verbs rather than one criteria.set so each
// Apply/Rollback stays trivial and the director's approval card states the
// intent (ADR-044 Q2). create/update mirror handleCreateCriterion /
// handlePatchCriterion; delete is net-new (no REST DELETE route today).

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
		Kind:     "criteria.create",
		Validate: validateCriteriaCreate,
		DryRun:   dryRunCriteriaCreate,
		Apply:    applyCriteriaCreate,
		Rollback: rollbackCriteriaCreate,
	})
	RegisterProposeKind(ProposeKind{
		Kind:     "criteria.update",
		Validate: validateCriteriaUpdate,
		DryRun:   dryRunCriteriaUpdate,
		Apply:    applyCriteriaUpdate,
		Rollback: rollbackCriteriaUpdate,
	})
	RegisterProposeKind(ProposeKind{
		Kind:     "criteria.delete",
		Validate: validateCriteriaDelete,
		DryRun:   dryRunCriteriaDelete,
		Apply:    applyCriteriaDelete,
		Rollback: rollbackCriteriaDelete,
	})
}

// --- shared target/spec shapes ---

// criteriaCreateTarget addresses the project the new criterion lands in.
type criteriaCreateTarget struct {
	ProjectID string `json:"project_id"`
}

// criteriaRefTarget addresses one existing criterion (update/delete).
type criteriaRefTarget struct {
	ProjectID   string `json:"project_id"`
	CriterionID string `json:"criterion_id"`
}

// criteriaCreateSpec mirrors criterionIn (the create body).
type criteriaCreateSpec struct {
	Phase         string         `json:"phase"`
	DeliverableID string         `json:"deliverable_id,omitempty"`
	Kind          string         `json:"kind"`
	Body          map[string]any `json:"body,omitempty"`
	Required      *bool          `json:"required,omitempty"`
	Ord           *int           `json:"ord,omitempty"`
}

// criteriaUpdateSpec mirrors the editable subset of criterionPatchIn — the
// rubric *definition* (body/required/ord). Marking (met/failed/waived) is a
// direct action, not part of an edit proposal.
type criteriaUpdateSpec struct {
	Body     map[string]any `json:"body,omitempty"`
	Required *bool          `json:"required,omitempty"`
	Ord      *int           `json:"ord,omitempty"`
}

// --- criteria.create ---

func parseCriteriaCreate(targetRef, changeSpec json.RawMessage) (criteriaCreateTarget, criteriaCreateSpec, error) {
	var t criteriaCreateTarget
	if len(targetRef) > 0 {
		if err := json.Unmarshal(targetRef, &t); err != nil {
			return t, criteriaCreateSpec{}, fmt.Errorf("target_ref: %w", err)
		}
	}
	if t.ProjectID == "" {
		return t, criteriaCreateSpec{}, errors.New("target_ref.project_id required")
	}
	var c criteriaCreateSpec
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
	if !isValidCriterionKind(c.Kind) {
		return t, c, fmt.Errorf("change_spec.kind %q invalid (one of text, metric, gate)", c.Kind)
	}
	return t, c, nil
}

func validateCriteriaCreate(_ context.Context, _ *Server, targetRef, changeSpec json.RawMessage) error {
	_, _, err := parseCriteriaCreate(targetRef, changeSpec)
	return err
}

func dryRunCriteriaCreate(_ context.Context, _ *Server, targetRef, changeSpec json.RawMessage) (json.RawMessage, error) {
	t, c, err := parseCriteriaCreate(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"project_id": t.ProjectID,
		"phase":      c.Phase,
		"kind":       c.Kind,
		"new_state":  "pending",
	})
}

func applyCriteriaCreate(
	ctx context.Context, s *Server, ac ProposeApplyContext, targetRef, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	t, c, err := parseCriteriaCreate(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	if err := s.projectInTeamCtx(ctx, ac.Team, t.ProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s not found in team", t.ProjectID)
		}
		return nil, err
	}
	// A gate/deliverable-scoped criterion must reference a deliverable in
	// the same project (mirrors handleCreateCriterion's check).
	if c.DeliverableID != "" {
		var found string
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM deliverables WHERE id = ? AND project_id = ?`,
			c.DeliverableID, t.ProjectID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("change_spec.deliverable_id not found in project")
		}
		if err != nil {
			return nil, err
		}
	}
	required := 1
	if c.Required != nil && !*c.Required {
		required = 0
	}
	ord := 0
	if c.Ord != nil {
		ord = *c.Ord
	}
	bodyJSON := "{}"
	if len(c.Body) > 0 {
		b, err := json.Marshal(c.Body)
		if err != nil {
			return nil, errors.New("change_spec.body must be a JSON object")
		}
		bodyJSON = string(b)
	}
	id := NewID()
	now := NowUTC()
	var deliv any
	if c.DeliverableID != "" {
		deliv = c.DeliverableID
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO acceptance_criteria (id, project_id, phase, deliverable_id,
			kind, body, state, required, ord, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`,
		id, t.ProjectID, c.Phase, deliv, c.Kind, bodyJSON, required, ord, now, now); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, ac.Team, "criterion.created", "criterion", id,
		fmt.Sprintf("created %s criterion in phase %s via propose", c.Kind, c.Phase),
		criteriaAuditMeta(ac, t.ProjectID, id, map[string]any{"phase": c.Phase, "kind": c.Kind}))
	return json.Marshal(map[string]any{
		"criterion_id": id,
		"project_id":   t.ProjectID,
		"phase":        c.Phase,
		"kind":         c.Kind,
		"state":        "pending",
		"created_at":   now,
	})
}

func rollbackCriteriaCreate(
	ctx context.Context, s *Server, ac ProposeApplyContext, _, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var orig struct {
		CriterionID string `json:"criterion_id"`
		ProjectID   string `json:"project_id"`
	}
	if err := json.Unmarshal(originalExecuted, &orig); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	if orig.CriterionID == "" {
		return nil, errors.New("rollback: original_executed missing criterion_id")
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM acceptance_criteria WHERE id = ? AND project_id = ?`,
		orig.CriterionID, orig.ProjectID); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, ac.Team, "criterion.deleted", "criterion", orig.CriterionID,
		"criteria.create rolled back",
		criteriaAuditMeta(ProposeApplyContext{Via: "rollback", AttentionID: ac.AttentionID},
			orig.ProjectID, orig.CriterionID, nil))
	return json.Marshal(map[string]any{"criterion_id": orig.CriterionID, "rolled_back": true})
}

// --- criteria.update ---

func parseCriteriaRef(targetRef json.RawMessage) (criteriaRefTarget, error) {
	var t criteriaRefTarget
	if len(targetRef) > 0 {
		if err := json.Unmarshal(targetRef, &t); err != nil {
			return t, fmt.Errorf("target_ref: %w", err)
		}
	}
	if t.ProjectID == "" {
		return t, errors.New("target_ref.project_id required")
	}
	if t.CriterionID == "" {
		return t, errors.New("target_ref.criterion_id required")
	}
	return t, nil
}

func parseCriteriaUpdate(targetRef, changeSpec json.RawMessage) (criteriaRefTarget, criteriaUpdateSpec, error) {
	t, err := parseCriteriaRef(targetRef)
	if err != nil {
		return t, criteriaUpdateSpec{}, err
	}
	var c criteriaUpdateSpec
	if len(changeSpec) > 0 {
		if err := json.Unmarshal(changeSpec, &c); err != nil {
			return t, c, fmt.Errorf("change_spec: %w", err)
		}
	}
	if c.Body == nil && c.Required == nil && c.Ord == nil {
		return t, c, errors.New("change_spec: at least one of body, required, ord required")
	}
	return t, c, nil
}

func validateCriteriaUpdate(_ context.Context, _ *Server, targetRef, changeSpec json.RawMessage) error {
	_, _, err := parseCriteriaUpdate(targetRef, changeSpec)
	return err
}

func dryRunCriteriaUpdate(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) (json.RawMessage, error) {
	t, c, err := parseCriteriaUpdate(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	cur, err := s.loadCriterion(ctx, t.ProjectID, t.CriterionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("criterion %s not found in project %s", t.CriterionID, t.ProjectID)
		}
		return nil, err
	}
	return json.Marshal(map[string]any{
		"criterion_id":  t.CriterionID,
		"changes_body":  c.Body != nil,
		"changes_req":   c.Required != nil,
		"changes_ord":   c.Ord != nil,
		"current_state": cur.State,
		"current_kind":  cur.Kind,
	})
}

func applyCriteriaUpdate(
	ctx context.Context, s *Server, ac ProposeApplyContext, targetRef, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	t, c, err := parseCriteriaUpdate(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	// Capture the pre-update values for the rollback inverse.
	before, err := s.loadCriterion(ctx, t.ProjectID, t.CriterionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("criterion %s not found in project %s", t.CriterionID, t.ProjectID)
		}
		return nil, err
	}
	now := NowUTC()
	q := `UPDATE acceptance_criteria SET updated_at = ?`
	args := []any{now}
	if c.Body != nil {
		b, err := json.Marshal(c.Body)
		if err != nil {
			return nil, errors.New("change_spec.body must be a JSON object")
		}
		q += `, body = ?`
		args = append(args, string(b))
	}
	if c.Required != nil {
		req := 0
		if *c.Required {
			req = 1
		}
		q += `, required = ?`
		args = append(args, req)
	}
	if c.Ord != nil {
		q += `, ord = ?`
		args = append(args, *c.Ord)
	}
	q += ` WHERE id = ? AND project_id = ?`
	args = append(args, t.CriterionID, t.ProjectID)
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, ac.Team, "criterion.updated", "criterion", t.CriterionID,
		"updated criterion definition via propose",
		criteriaAuditMeta(ac, t.ProjectID, t.CriterionID, nil))
	// executed carries before+after so rollback can restore the prior
	// definition without re-querying.
	return json.Marshal(map[string]any{
		"criterion_id": t.CriterionID,
		"project_id":   t.ProjectID,
		"before": map[string]any{
			"body":     before.Body,
			"required": before.Required,
			"ord":      before.Ord,
		},
		"updated_at": now,
	})
}

func rollbackCriteriaUpdate(
	ctx context.Context, s *Server, ac ProposeApplyContext, _, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var orig struct {
		CriterionID string `json:"criterion_id"`
		ProjectID   string `json:"project_id"`
		Before      struct {
			Body     map[string]any `json:"body"`
			Required bool           `json:"required"`
			Ord      int            `json:"ord"`
		} `json:"before"`
	}
	if err := json.Unmarshal(originalExecuted, &orig); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	if orig.CriterionID == "" {
		return nil, errors.New("rollback: original_executed missing criterion_id")
	}
	bodyJSON := "{}"
	if len(orig.Before.Body) > 0 {
		b, _ := json.Marshal(orig.Before.Body)
		bodyJSON = string(b)
	}
	req := 0
	if orig.Before.Required {
		req = 1
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE acceptance_criteria
		   SET body = ?, required = ?, ord = ?, updated_at = ?
		 WHERE id = ? AND project_id = ?`,
		bodyJSON, req, orig.Before.Ord, NowUTC(), orig.CriterionID, orig.ProjectID); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, ac.Team, "criterion.updated", "criterion", orig.CriterionID,
		"criteria.update rolled back",
		criteriaAuditMeta(ProposeApplyContext{Via: "rollback", AttentionID: ac.AttentionID},
			orig.ProjectID, orig.CriterionID, nil))
	return json.Marshal(map[string]any{"criterion_id": orig.CriterionID, "rolled_back": true})
}

// --- criteria.delete ---

func validateCriteriaDelete(_ context.Context, _ *Server, targetRef, _ json.RawMessage) error {
	_, err := parseCriteriaRef(targetRef)
	return err
}

func dryRunCriteriaDelete(ctx context.Context, s *Server, targetRef, _ json.RawMessage) (json.RawMessage, error) {
	t, err := parseCriteriaRef(targetRef)
	if err != nil {
		return nil, err
	}
	cur, err := s.loadCriterion(ctx, t.ProjectID, t.CriterionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("criterion %s not found in project %s", t.CriterionID, t.ProjectID)
		}
		return nil, err
	}
	return json.Marshal(map[string]any{
		"criterion_id": t.CriterionID,
		"kind":         cur.Kind,
		"phase":        cur.Phase,
		"state":        cur.State,
		"removes":      true,
	})
}

func applyCriteriaDelete(
	ctx context.Context, s *Server, ac ProposeApplyContext, targetRef, _ json.RawMessage,
) (json.RawMessage, error) {
	t, err := parseCriteriaRef(targetRef)
	if err != nil {
		return nil, err
	}
	// Capture the full row so the rollback can re-insert it verbatim.
	before, err := s.loadCriterion(ctx, t.ProjectID, t.CriterionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("criterion %s not found in project %s", t.CriterionID, t.ProjectID)
		}
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM acceptance_criteria WHERE id = ? AND project_id = ?`,
		t.CriterionID, t.ProjectID); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, ac.Team, "criterion.deleted", "criterion", t.CriterionID,
		fmt.Sprintf("deleted %s criterion in phase %s via propose", before.Kind, before.Phase),
		criteriaAuditMeta(ac, t.ProjectID, t.CriterionID, nil))
	snap, _ := json.Marshal(before)
	return json.Marshal(map[string]any{
		"criterion_id": t.CriterionID,
		"project_id":   t.ProjectID,
		"snapshot":     json.RawMessage(snap),
	})
}

func rollbackCriteriaDelete(
	ctx context.Context, s *Server, ac ProposeApplyContext, _, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var orig struct {
		ProjectID string       `json:"project_id"`
		Snapshot  criterionOut `json:"snapshot"`
	}
	if err := json.Unmarshal(originalExecuted, &orig); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	c := orig.Snapshot
	if c.ID == "" {
		return nil, errors.New("rollback: snapshot missing criterion id")
	}
	bodyJSON := "{}"
	if len(c.Body) > 0 {
		b, _ := json.Marshal(c.Body)
		bodyJSON = string(b)
	}
	req := 0
	if c.Required {
		req = 1
	}
	var deliv any
	if c.DeliverableID != "" {
		deliv = c.DeliverableID
	}
	var metAt, metBy, evid any
	if c.MetAt != "" {
		metAt = c.MetAt
	}
	if c.MetByActor != "" {
		metBy = c.MetByActor
	}
	if c.EvidenceRef != "" {
		evid = c.EvidenceRef
	}
	now := NowUTC()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO acceptance_criteria (id, project_id, phase, deliverable_id,
			kind, body, state, met_at, met_by_actor, evidence_ref,
			required, ord, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ProjectID, c.Phase, deliv, c.Kind, bodyJSON, c.State,
		metAt, metBy, evid, req, c.Ord, now, now); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, ac.Team, "criterion.created", "criterion", c.ID,
		"criteria.delete rolled back",
		criteriaAuditMeta(ProposeApplyContext{Via: "rollback", AttentionID: ac.AttentionID},
			c.ProjectID, c.ID, nil))
	return json.Marshal(map[string]any{"criterion_id": c.ID, "restored": true})
}

// criteriaAuditMeta builds the propose-lineage audit meta shared by the
// criteria apply functions. extra merges kind-specific fields.
func criteriaAuditMeta(ac ProposeApplyContext, projectID, criterionID string, extra map[string]any) map[string]any {
	m := map[string]any{
		"project_id":   projectID,
		"criterion_id": criterionID,
		"via":          ac.ViaOrDefault(),
		"by_tier":      ac.AssignedTier,
		"propose_id":   ac.AttentionID,
	}
	if ac.DeciderHandle != "" {
		m["by_actor"] = ac.DeciderHandle
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}
