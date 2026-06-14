// apply_agent_spawn.go — ADR-030 W8 propose apply function for the
// `agent.spawn` governed-action kind. Wraps the existing DoSpawn so
// the same code path handles BOTH:
//
//   - propose(kind="agent.spawn", ...) — the new ADR-030 verb. The
//     decide handler dispatches through the registry with
//     ProposeApplyContext.Via = "propose".
//   - The legacy `approval_request + spawnIn` path. The W8 decide-
//     handler refactor routes this through the same Apply with
//     ProposeApplyContext.Via = "alias_legacy". This is the
//     "deprecated alias" path the plan W8 names; old MCP callers
//     keep working unchanged but the audit row's meta.via tags the
//     dispatch hop so consumers can tell new from legacy.
//
// `change_spec` is the spawnIn struct directly (same JSON shape the
// legacy pending_payload carried) so the two dispatch paths can
// share the same unmarshal. `target_ref` is cosmetic for this
// kind — there's no separate target identifier; the spawn details
// live entirely in change_spec.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

func init() {
	RegisterProposeKind(ProposeKind{
		Kind:     "agent.spawn",
		Validate: validateAgentSpawn,
		DryRun:   dryRunAgentSpawn,
		Apply:    applyAgentSpawn,
		Rollback: rollbackAgentSpawn,
	})
}

func parseAgentSpawnChangeSpec(changeSpec json.RawMessage) (spawnIn, error) {
	var sp spawnIn
	if len(changeSpec) == 0 {
		return sp, errors.New("change_spec required (spawnIn JSON)")
	}
	if err := json.Unmarshal(changeSpec, &sp); err != nil {
		return sp, fmt.Errorf("change_spec: %w", err)
	}
	if sp.ChildHandle == "" {
		return sp, errors.New("change_spec.child_handle required")
	}
	if sp.Kind == "" {
		return sp, errors.New("change_spec.kind required (engine kind)")
	}
	return sp, nil
}

// validateAgentSpawn is a pure shape check. DoSpawn's own validation
// (host capability lookup, role-gate, etc.) runs at Apply time when
// the row context is full.
func validateAgentSpawn(_ context.Context, _ *Server, _, changeSpec json.RawMessage) error {
	_, err := parseAgentSpawnChangeSpec(changeSpec)
	return err
}

// dryRunAgentSpawn returns the preview the propose handler's
// `dry_run=true` branch echoes back. No DoSpawn call; the actual
// host-capability check happens at Apply time.
func dryRunAgentSpawn(_ context.Context, _ *Server, _, changeSpec json.RawMessage) (json.RawMessage, error) {
	sp, err := parseAgentSpawnChangeSpec(changeSpec)
	if err != nil {
		return nil, err
	}
	preview := map[string]any{
		"child_handle":    sp.ChildHandle,
		"engine_kind":     sp.Kind,
		"host_id":         sp.HostID,
		"project_id":      sp.ProjectID,
		"parent_agent_id": sp.ParentID,
		"has_task_inline": sp.Task != nil,
		"task_id":         sp.TaskID,
	}
	return json.Marshal(preview)
}

// applyAgentSpawn calls DoSpawn and emits the `agent.spawn` audit row
// with the propose lineage on meta. Mirrors the audit shape the
// REST handler emits at handlers_agents.go::handlePostSpawn so the
// activity feed reads identically; `meta.via` is the discriminator.
func applyAgentSpawn(
	ctx context.Context, s *Server, ac ProposeApplyContext, _, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	sp, err := parseAgentSpawnChangeSpec(changeSpec)
	if err != nil {
		return nil, err
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("agent.spawn: apply context missing team")
	}
	out, _, err := s.DoSpawn(ctx, team, sp)
	if err != nil {
		return nil, fmt.Errorf("agent.spawn DoSpawn: %w", err)
	}

	via := ac.ViaOrDefault()
	meta := map[string]any{
		"handle":     sp.ChildHandle,
		"kind":       sp.Kind,
		"host_id":    sp.HostID,
		"via":        via,
		"by_tier":    ac.AssignedTier,
		"propose_id": ac.AttentionID,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, "agent.spawn", "agent", out.AgentID,
		"spawn "+sp.ChildHandle+" ("+sp.Kind+") via "+via, meta)

	executed := map[string]any{
		"kind":       "spawn",
		"spawn_id":   out.SpawnID,
		"agent_id":   out.AgentID,
		"spawned_at": out.SpawnedAt,
		"handle":     sp.ChildHandle,
	}
	return json.Marshal(executed)
}

// rollbackAgentSpawn emits a TODO audit pointing the principal at
// the spawned agent_id. The MVP does NOT auto-terminate because
// `agent.terminate` is a post-MVP propose kind — terminating
// without a governance-tracked attention row would bypass the
// authorisation ladder ADR-030 just established.
//
// The audit row's `action="agent.spawn.rollback_todo"` is distinct
// from `agent.spawn` so the activity feed can render it with a
// "needs manual cleanup" badge. The override handler still treats
// the rollback as successful — the audit row IS the rollback
// artefact in this MVP path.
func rollbackAgentSpawn(
	ctx context.Context, s *Server, ac ProposeApplyContext, originalSpec, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var origExec struct {
		AgentID string `json:"agent_id"`
		Handle  string `json:"handle"`
	}
	if err := json.Unmarshal(originalExecuted, &origExec); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	if origExec.AgentID == "" {
		return nil, errors.New("rollback: original_executed missing agent_id")
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("agent.spawn rollback: apply context missing team")
	}
	via := ac.ViaOrDefault()
	meta := map[string]any{
		"agent_id":   origExec.AgentID,
		"handle":     origExec.Handle,
		"via":        via,
		"by_tier":    ac.AssignedTier,
		"propose_id": ac.AttentionID,
		"rollback":   true,
		"hint":       "agent.terminate is post-MVP; manually terminate via DELETE /v1/teams/{team}/agents/{id}",
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, "agent.spawn.rollback_todo", "agent",
		origExec.AgentID,
		"override of agent.spawn — manual terminate required for "+origExec.Handle,
		meta)
	return json.Marshal(map[string]any{
		"kind":             "spawn_rollback_todo",
		"agent_id":         origExec.AgentID,
		"handle":           origExec.Handle,
		"manual_terminate": true,
		"hint":             meta["hint"],
		"rollback":         true,
	})
}
