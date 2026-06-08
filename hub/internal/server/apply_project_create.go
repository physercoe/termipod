// apply_project_create.go — ADR-046 / WS4 propose apply function for the
// `project.create` governed-action kind. This is the steward's ONLY path to
// create a project: the director directs, the steward composes an inline
// project spec (ADR-046 — the spec IS the project's config_yaml) and
// `propose(kind="project.create", …)`; the principal reviews the spec on the
// approval card; approval materializes the project.
//
// The approval IS the install — there is no separate `template.install` step
// in the steward surface (WS5 removes that verb for stewards). On Apply the
// project's bound domain steward is recorded (projects.on_create_template_id)
// but NOT spawned; an explicit `POST …/projects/{id}/start` (handleStartProject)
// spawns it.
//
// `change_spec` carries the full project spec inline so the proposal's
// `pending_payload_json` shows the spec for review (#39/#40): {name,
// config_yaml, parameters_json, goal?, kind?, docs_root?, parent_project_id?,
// on_create_template_id?}. `target_ref` is cosmetic for this kind — a create
// has no pre-existing target; the new project's id is minted at Apply time and
// returned in the executed payload.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

func init() {
	RegisterProposeKind(ProposeKind{
		Kind:     "project.create",
		Validate: validateProjectCreate,
		DryRun:   dryRunProjectCreate,
		Apply:    applyProjectCreate,
		Rollback: rollbackProjectCreate,
	})
}

// projectCreateSpec is the change_spec shape for project.create. It mirrors
// the concrete-project subset of projectIn (the REST create body); is_template
// is intentionally absent — a governed create always materializes a concrete,
// runnable project (templates are authored references, not proposed).
type projectCreateSpec struct {
	Name               string          `json:"name"`
	Goal               string          `json:"goal,omitempty"`
	Kind               string          `json:"kind,omitempty"`
	ConfigYAML         string          `json:"config_yaml,omitempty"`
	ParametersJSON     json.RawMessage `json:"parameters_json,omitempty"`
	DocsRoot           string          `json:"docs_root,omitempty"`
	ParentProjectID    string          `json:"parent_project_id,omitempty"`
	OnCreateTemplateID string          `json:"on_create_template_id,omitempty"`
}

func parseProjectCreate(changeSpec json.RawMessage) (projectCreateSpec, error) {
	var p projectCreateSpec
	if len(changeSpec) == 0 {
		return p, errors.New("change_spec required (project spec JSON)")
	}
	if err := json.Unmarshal(changeSpec, &p); err != nil {
		return p, fmt.Errorf("change_spec: %w", err)
	}
	if p.Name == "" {
		return p, errors.New("change_spec.name required")
	}
	return p, nil
}

// projectInFromCreateSpec maps the propose change_spec onto the projectIn the
// shared createProjectCore consumes. is_template stays false — see the type
// doc.
func projectInFromCreateSpec(p projectCreateSpec) projectIn {
	return projectIn{
		Name:               p.Name,
		Goal:               p.Goal,
		Kind:               p.Kind,
		ConfigYML:          p.ConfigYAML,
		ParametersJSON:     p.ParametersJSON,
		DocsRoot:           p.DocsRoot,
		ParentProjectID:    p.ParentProjectID,
		OnCreateTemplateID: p.OnCreateTemplateID,
	}
}

// validateProjectCreate is a pure shape check — the full spec validation
// (config_yaml shape, typed-parameter values, parent-depth, docs-root bounds)
// runs at Apply time inside createProjectCore, the same code the REST path
// uses, so the two cannot diverge.
func validateProjectCreate(_ context.Context, _ *Server, _, changeSpec json.RawMessage) error {
	_, err := parseProjectCreate(changeSpec)
	return err
}

// dryRunProjectCreate echoes the headline spec fields for the approval-card
// preview. It deliberately does NOT mint a row — the full materialization
// happens only on approve.
func dryRunProjectCreate(_ context.Context, _ *Server, _, changeSpec json.RawMessage) (json.RawMessage, error) {
	p, err := parseProjectCreate(changeSpec)
	if err != nil {
		return nil, err
	}
	preview := map[string]any{
		"name":                  p.Name,
		"kind":                  p.Kind,
		"on_create_template_id": p.OnCreateTemplateID,
		"has_config_yaml":       p.ConfigYAML != "",
	}
	if specs, perr := parseProjectParamSpecs(p.ConfigYAML); perr == nil && len(specs) > 0 {
		preview["parameter_count"] = len(specs)
	}
	if phases := projectPhasesFromConfig(p.ConfigYAML); len(phases) > 0 {
		preview["phases"] = phases
	}
	return json.Marshal(preview)
}

// projectPhasesFromConfig is the dry-run-side phase reader. It parses ONLY the
// inline spec (no server / template-file fallback) because the dry run runs
// before any row exists; an empty result is fine for the preview.
func projectPhasesFromConfig(configYAML string) []string {
	if configYAML == "" {
		return nil
	}
	var doc struct {
		Phases phaseNameList `yaml:"phases"`
	}
	if yaml.Unmarshal([]byte(configYAML), &doc) != nil {
		return nil
	}
	return []string(doc.Phases)
}

// applyProjectCreate materializes the proposed project via the shared
// createProjectCore path (steward bound, NOT spawned) and emits a
// `project.create` audit row carrying the propose lineage on meta. The
// returned executed payload carries the new project id so the override path
// and the requester's fan-back know what landed.
func applyProjectCreate(
	ctx context.Context, s *Server, ac ProposeApplyContext, _, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	p, err := parseProjectCreate(changeSpec)
	if err != nil {
		return nil, err
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("project.create: apply context missing team")
	}
	out, _, err := s.createProjectCore(ctx, team, projectInFromCreateSpec(p))
	if err != nil {
		return nil, fmt.Errorf("project.create: %w", err)
	}

	via := ac.ViaOrDefault()
	meta := map[string]any{
		"project_id":            out.ID,
		"name":                  out.Name,
		"on_create_template_id": out.OnCreateTemplateID,
		"via":                   via,
		"by_tier":               ac.AssignedTier,
		"propose_id":            ac.AttentionID,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, "project.create.proposed", "project", out.ID,
		fmt.Sprintf("materialize project %s via %s", out.Name, via), meta)

	executed := map[string]any{
		"kind":       "project_create",
		"project_id": out.ID,
		"name":       out.Name,
		"phase":      out.Phase,
		"phases":     out.Phases,
	}
	return json.Marshal(executed)
}

// rollbackProjectCreate archives the project minted by the Apply. It mirrors
// handleArchiveProject's effect (status='archived' + archived_at) so the
// override leaves no runnable project behind. The row is kept (not deleted)
// for the audit trail, matching the rest of the lifecycle surface.
func rollbackProjectCreate(
	ctx context.Context, s *Server, ac ProposeApplyContext, _, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var origExec struct {
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(originalExecuted, &origExec); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	if origExec.ProjectID == "" {
		return nil, errors.New("rollback: original_executed missing project_id")
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("project.create rollback: apply context missing team")
	}
	res, err := s.writeDB.ExecContext(ctx, `
		UPDATE projects SET status='archived', archived_at=?
		 WHERE team_id = ? AND id = ? AND status != 'archived'`,
		NowUTC(), team, origExec.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("rollback archive %s: %w", origExec.ProjectID, err)
	}
	n, _ := res.RowsAffected()

	via := ac.ViaOrDefault()
	meta := map[string]any{
		"project_id": origExec.ProjectID,
		"name":       origExec.Name,
		"archived":   n > 0,
		"via":        via,
		"by_tier":    ac.AssignedTier,
		"propose_id": ac.AttentionID,
		"rollback":   true,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, "project.archive", "project", origExec.ProjectID,
		"archive project (rollback of project.create)", meta)
	return json.Marshal(map[string]any{
		"kind":       "project_archive",
		"project_id": origExec.ProjectID,
		"name":       origExec.Name,
		"archived":   n > 0,
		"rollback":   true,
	})
}
