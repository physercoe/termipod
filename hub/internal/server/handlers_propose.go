// handlers_propose.go — the ADR-030 W4 generic `propose` MCP verb.
//
// One handler that opens an attention_items row with `kind="propose"`
// and the per-(change_kind, addressee_tier) policy applied at insert
// time. Dispatch on `change_kind` happens at /decide time via the
// registered ProposeKind.Apply function (W5/W6/W7 register them;
// W8 wires the decide-handler dispatch). W4 is the input/insert side
// only — propose rows that resolve before W5+ ship will sit decided
// but unapplied; that's acceptable because nothing yet calls propose.
//
// Cross-references:
//   - ProposeKind registry (W3): hub/internal/server/propose_kinds.go
//   - Policy.KindFor (W2):       hub/internal/server/policy.go
//   - Schema columns (W1):       hub/migrations/0045_*.up.sql
//   - Tool registration:         hub/internal/server/native_tools.go
//   - Tier entry:                hub/internal/server/tiers.go
//   - Per-kind apply (W5-W7):    hub/internal/server/apply_*.go (not yet)
//   - Decide-time dispatch (W8): hub/internal/server/handlers_attention.go
//   - Fan-back envelope (W11):   ADR-030 plan §2.2 W11

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// proposeArgs is the wire shape of the `propose` MCP verb's arguments.
type proposeArgs struct {
	// Kind names the governed action (e.g. "deliverable.set_state").
	// Must be registered in the propose-kind registry; the lint
	// (lint-governed-actions.sh) also requires a matching policy.yaml
	// entry.
	Kind string `json:"kind"`

	// TargetRef identifies what the action mutates. Shape is per-kind:
	// for `task.set_status` → `{project_id, task_id}`; for
	// `deliverable.set_state` → `{project_id, deliverable_id}`; for
	// `template.install` → `{}` (template installs operate above the
	// project scope and skip the scope check).
	TargetRef json.RawMessage `json:"target_ref"`

	// ChangeSpec is the per-kind mutation payload. Round-tripped through
	// `attention_items.change_spec_json`; the apply function reads it
	// at /decide(approve) time.
	ChangeSpec json.RawMessage `json:"change_spec"`

	// Reason is a free-text human-facing explanation appended to the
	// attention summary. Strongly recommended; the authoriser sees it
	// before deciding.
	Reason string `json:"reason,omitempty"`

	// AddresseeTier optionally pins the addressee tier; empty falls
	// through to the policy's `default_tier` for the kind, then to
	// `principal` if the policy is silent.
	AddresseeTier string `json:"addressee_tier,omitempty"`

	// DryRun skips the insert and returns the per-kind preview from
	// ProposeKind.DryRun. Useful when the agent is uncertain whether
	// the change_spec is well-formed.
	DryRun bool `json:"dry_run,omitempty"`
}

// mcpPropose is the W4 propose verb. Inserts an attention row tagged
// with the ADR-030 governed-actions columns; the decide path resolves
// it normally (W8+ dispatch through the registry to call the Apply
// function on approve).
//
// Errors are surfaced as JSON-RPC -32602 (invalid params) for input
// faults and -32000 (server) for storage faults. The descriptive
// message names the kind on every error so the agent can re-propose
// without round-tripping.
func (s *Server) mcpPropose(ctx context.Context, team, fromID string, raw json.RawMessage) (any, *jrpcError) {
	var a proposeArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &jrpcError{Code: -32602, Message: "propose: invalid arguments JSON: " + err.Error()}
	}
	if a.Kind == "" {
		return nil, &jrpcError{Code: -32602, Message: "propose: kind required"}
	}

	// 1. Validate kind against the registry (W3 surface).
	pk, ok := LookupProposeKind(a.Kind)
	if !ok {
		known := ListProposeKinds()
		return nil, &jrpcError{
			Code: -32602,
			Message: fmt.Sprintf("propose: unknown kind %q (registered: [%s])",
				a.Kind, strings.Join(known, ", ")),
		}
	}

	// 2. Per-kind structural validation (target_ref / change_spec shape).
	if pk.Validate != nil {
		if err := pk.Validate(ctx, s, a.TargetRef, a.ChangeSpec); err != nil {
			return nil, &jrpcError{
				Code: -32602, Message: "propose: validate " + a.Kind + ": " + err.Error(),
			}
		}
	}

	// 3. Cross-project scope check per pre-W1 decision #4.
	if jerr := s.checkProposeScope(ctx, team, fromID, a.Kind, a.TargetRef); jerr != nil {
		return nil, jerr
	}

	// 4. Resolve addressee tier (caller hint > policy default > principal).
	policy, _ := s.policy.KindFor(a.Kind)
	tier := a.AddresseeTier
	if tier == "" {
		tier = policy.DefaultTier
	}
	if tier == "" {
		tier = GovTierPrincipal
	}
	if !isValidGovTier(tier) {
		return nil, &jrpcError{
			Code: -32602,
			Message: fmt.Sprintf("propose: addressee_tier %q invalid (one of [%s])",
				tier, strings.Join(validGovTiers(), ", ")),
		}
	}

	// 5. Compute current_assignees_json from tier resolution.
	assignees := s.resolveAssigneesForTier(ctx, team, tier, a.TargetRef)
	assigneesJSON, _ := json.Marshal(assignees)

	// 6. Dry-run branch — call the kind's DryRun and return without insert.
	if a.DryRun {
		var preview json.RawMessage
		if pk.DryRun != nil {
			var err error
			preview, err = pk.DryRun(ctx, s, a.TargetRef, a.ChangeSpec)
			if err != nil {
				return nil, &jrpcError{Code: -32000, Message: "propose: dry_run " + a.Kind + ": " + err.Error()}
			}
		}
		out := map[string]any{
			"status":        "dry_run",
			"kind":          "propose",
			"change_kind":   a.Kind,
			"assigned_tier": tier,
			"would_address": assignees,
		}
		if preview != nil {
			out["preview"] = preview
		}
		return mcpResultJSON(out), nil
	}

	// 7. Extract target project_id so the row joins the project's queue
	// in the Me / project-detail surfaces. Null is fine — kinds that
	// operate above project scope (template.install) skip this.
	projectID := extractTargetProjectID(a.TargetRef)

	// 8. Insert the attention row.
	id := NewID()
	now := NowUTC()
	actorHandle, _ := s.lookupHandleByID(ctx, team, fromID)
	sessionID := s.lookupAgentSession(ctx, fromID)
	summary := "Propose " + a.Kind
	if a.Reason != "" {
		summary += " — " + a.Reason
	}

	// pending_payload_json carries the full propose envelope so
	// existing decide-time consumers (and future W8 alias dispatch)
	// can read it without rejoining the new columns. The new columns
	// are the canonical surface; pending_payload is a mirror.
	pending, _ := json.Marshal(map[string]any{
		"kind":           a.Kind,
		"target_ref":     json.RawMessage(nonEmptyJSON(a.TargetRef)),
		"change_spec":    json.RawMessage(nonEmptyJSON(a.ChangeSpec)),
		"reason":         a.Reason,
		"addressee_tier": tier,
		"proposed_by":    fromID,
	})

	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at,
			actor_kind, actor_handle, session_id,
			change_kind, assigned_tier, change_spec_json, target_ref_json
		) VALUES (?, NULLIF(?,''), 'team', NULL, 'propose',
		          ?, 'minor', ?,
		          ?, 'open', ?,
		          'agent', NULLIF(?,''), NULLIF(?,''),
		          ?, ?, NULLIF(?,''), NULLIF(?,''))`,
		id, projectID,
		summary, string(assigneesJSON),
		string(pending), now,
		actorHandle, sessionID,
		a.Kind, tier, string(a.ChangeSpec), string(a.TargetRef))
	if err != nil {
		return nil, &jrpcError{Code: -32000, Message: "propose: insert: " + err.Error()}
	}

	s.recordAudit(ctx, team, "propose.raised", "attention", id, summary,
		map[string]any{
			"change_kind":   a.Kind,
			"assigned_tier": tier,
			"agent_id":      fromID,
			"project_id":    projectID,
		})

	return mcpResultJSON(map[string]any{
		"request_id":    id,
		"kind":          "propose",
		"change_kind":   a.Kind,
		"assigned_tier": tier,
		"status":        "awaiting_response",
	}), nil
}

// checkProposeScope enforces the cross-project rule from the
// 2026-05-20 pre-W1 decision #4:
//
//   - Worker callers may only propose against their own project.
//   - Steward callers (kind LIKE 'steward.%') may cross projects.
//   - When target_ref carries no project_id (kinds operating above
//     project scope), the check is skipped.
//
// On reject we surface "out_of_scope" in the message so the agent
// recognises the failure class without parsing tier-membership.
func (s *Server) checkProposeScope(
	ctx context.Context, team, fromID, kind string, targetRef json.RawMessage,
) *jrpcError {
	targetProject := extractTargetProjectID(targetRef)
	if targetProject == "" {
		return nil // above-project scope
	}
	var callerKind, callerProject sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT kind, project_id FROM agents WHERE team_id = ? AND id = ?`,
		team, fromID).Scan(&callerKind, &callerProject)
	if errors.Is(err, sql.ErrNoRows) {
		// Unknown caller — fail closed. Should not happen via the MCP
		// dispatch path (it always passes a resolved agent_id), but
		// belt-and-suspenders.
		return &jrpcError{Code: -32602, Message: "propose: caller agent not found"}
	}
	if err != nil {
		return &jrpcError{Code: -32000, Message: "propose: caller lookup: " + err.Error()}
	}
	if isStewardKind(callerKind.String) {
		return nil // stewards may cross projects
	}
	if callerProject.String == targetProject {
		return nil // worker on own project
	}
	return &jrpcError{
		Code: -32602,
		Message: fmt.Sprintf(
			"propose: out_of_scope — %s targets project %s but caller is bound to project %q",
			kind, targetProject, callerProject.String),
	}
}

// resolveAssigneesForTier returns the symbolic-handle list that lands
// in `attention_items.current_assignees_json`. For now the Me-page and
// inbox queries gate on `assigned_tier` rather than walking this list,
// so the values are decorative — mobile renders them in the row card
// to show who the row is addressed to.
//
// Strategy: prefer a live steward agent's handle when one exists in
// the target project (project-steward tier). Falls through to a
// symbolic role-handle (`@steward.project`, `@steward.general`,
// `@principal`) when no live agent matches — kept stable so the
// mobile card always has something legible to render.
func (s *Server) resolveAssigneesForTier(
	ctx context.Context, team, tier string, targetRef json.RawMessage,
) []string {
	switch tier {
	case GovTierPrincipal:
		return []string{"@principal"}
	case GovTierGeneralSteward:
		return []string{"@steward.general"}
	case GovTierProjectSteward:
		project := extractTargetProjectID(targetRef)
		if project != "" {
			if id, err := s.findRunningProjectSteward(ctx, team, project); err == nil && id != "" {
				if h, err := s.lookupHandleByID(ctx, team, id); err == nil && h != "" {
					return []string{h}
				}
			}
		}
		return []string{"@steward.project"}
	case GovTierWorker:
		return []string{}
	default:
		return []string{"@principal"}
	}
}

// extractTargetProjectID returns the `project_id` field from a
// target_ref JSON object, or "" if the field is absent / target_ref is
// empty / not a JSON object. Used for the scope check + the row's
// project_id column.
func extractTargetProjectID(targetRef json.RawMessage) string {
	if len(targetRef) == 0 {
		return ""
	}
	var probe struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(targetRef, &probe); err != nil {
		return ""
	}
	return probe.ProjectID
}

// nonEmptyJSON returns the input if non-empty, else "null" — so the
// pending_payload's nested fields round-trip as JSON `null` rather
// than as parse errors when the caller omits them.
func nonEmptyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

// isStewardKind matches the kind-based steward predicate locked at
// v1.0.607 (kind LIKE 'steward.%'). Kept as a free function so the
// /decide path and future per-kind apply functions can reuse it
// without a method-receiver dependency.
func isStewardKind(kind string) bool {
	return kind == "steward.v1" || strings.HasPrefix(kind, "steward.")
}

func isValidGovTier(tier string) bool {
	switch tier {
	case GovTierWorker, GovTierProjectSteward, GovTierGeneralSteward, GovTierPrincipal:
		return true
	}
	return false
}

func validGovTiers() []string {
	return []string{GovTierWorker, GovTierProjectSteward, GovTierGeneralSteward, GovTierPrincipal}
}
