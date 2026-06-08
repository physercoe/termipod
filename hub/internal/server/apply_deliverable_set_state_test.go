package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ADR-030 W5 — apply_deliverable_set_state.go.
//
// The apply function is the runtime side; the registry hookup is
// init()-time. These tests run against the live registration (no
// resetProposeKindsForTest at the top) so the init() side is
// covered too.

// seedDeliverable inserts a minimal deliverable row at the given
// ratification state and returns its id.
func seedDeliverable(t *testing.T, s *Server, project, phase, kind, state string) string {
	t.Helper()
	id := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO deliverables
			(id, project_id, phase, kind, ratification_state, required, ord, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, 0, ?, ?)`,
		id, project, phase, kind, state, now, now); err != nil {
		t.Fatalf("seed deliverable: %v", err)
	}
	return id
}

func deliverableState(t *testing.T, s *Server, id string) string {
	t.Helper()
	var st string
	if err := s.db.QueryRow(
		`SELECT ratification_state FROM deliverables WHERE id = ?`, id).Scan(&st); err != nil {
		t.Fatalf("read state: %v", err)
	}
	return st
}

// 1. Init: deliverable.set_state is in the live registry from
// package init().
func TestDeliverableSetState_RegisteredAtInit(t *testing.T) {
	pk, ok := LookupProposeKind("deliverable.set_state")
	if !ok {
		t.Fatal("deliverable.set_state not registered at init()")
	}
	if pk.Validate == nil || pk.DryRun == nil || pk.Apply == nil {
		t.Errorf("registered ProposeKind missing functions: validate=%v dry=%v apply=%v",
			pk.Validate != nil, pk.DryRun != nil, pk.Apply != nil)
	}
}

// 2. Validate happy + error paths.
func TestDeliverableSetState_Validate(t *testing.T) {
	pk, _ := LookupProposeKind("deliverable.set_state")
	cases := []struct {
		name   string
		target string
		spec   string
		wantOK bool
		wantIn string
	}{
		{"happy", `{"project_id":"p","deliverable_id":"d"}`, `{"state":"ratified"}`, true, ""},
		{"missing project_id", `{"deliverable_id":"d"}`, `{"state":"draft"}`, false, "project_id"},
		{"missing deliverable_id", `{"project_id":"p"}`, `{"state":"draft"}`, false, "deliverable_id"},
		{"missing state", `{"project_id":"p","deliverable_id":"d"}`, `{}`, false, "state required"},
		{"invalid state", `{"project_id":"p","deliverable_id":"d"}`, `{"state":"shipped"}`, false, "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pk.Validate(context.Background(), nil,
				json.RawMessage(tc.target), json.RawMessage(tc.spec))
			if tc.wantOK {
				if err != nil {
					t.Errorf("validate: %v; want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want error; got nil")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantIn)
			}
		})
	}
}

// 3. DryRun returns the from/to preview.
func TestDeliverableSetState_DryRun_Preview(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("deliverable.set_state")
	proj := seedProject(t, s, defaultTeamID)
	delID := seedDeliverable(t, s, proj, "design", "research_plan", deliverableStateInReview)

	target, _ := json.Marshal(map[string]any{"project_id": proj, "deliverable_id": delID})
	spec, _ := json.Marshal(map[string]any{"state": "ratified"})
	previewRaw, err := pk.DryRun(context.Background(), s, target, spec)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	var preview map[string]any
	if err := json.Unmarshal(previewRaw, &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview["from_state"] != "in-review" {
		t.Errorf("from_state = %v; want in-review", preview["from_state"])
	}
	if preview["to_state"] != "ratified" {
		t.Errorf("to_state = %v; want ratified", preview["to_state"])
	}
	if preview["target_kind"] != "research_plan" {
		t.Errorf("target_kind = %v; want research_plan", preview["target_kind"])
	}
	// State must NOT have changed — dry_run is read-only.
	if got := deliverableState(t, s, delID); got != deliverableStateInReview {
		t.Errorf("state mutated by DryRun: %q", got)
	}

	// Missing deliverable → error.
	missingTarget, _ := json.Marshal(map[string]any{"project_id": proj, "deliverable_id": "ghost"})
	if _, err := pk.DryRun(context.Background(), s, missingTarget, spec); err == nil {
		t.Error("expected error on missing deliverable")
	}
}

// 4. Apply: in-review → ratified writes the ratified stamp, emits the
// deliverable.ratified audit row with via=propose lineage.
func TestDeliverableSetState_Apply_InReviewToRatified(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("deliverable.set_state")
	proj := seedProject(t, s, defaultTeamID)
	delID := seedDeliverable(t, s, proj, "design", "research_plan", deliverableStateInReview)

	target, _ := json.Marshal(map[string]any{"project_id": proj, "deliverable_id": delID})
	spec, _ := json.Marshal(map[string]any{"state": deliverableStateRatified})
	ac := ProposeApplyContext{
		AttentionID:   "att-001",
		Team:          defaultTeamID,
		AssignedTier:  GovTierPrincipal,
		DeciderHandle: "@principal",
	}
	executedRaw, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	if err := json.Unmarshal(executedRaw, &executed); err != nil {
		t.Fatalf("decode executed: %v", err)
	}
	if executed["audit_action"] != "deliverable.ratified" {
		t.Errorf("audit_action = %v; want deliverable.ratified", executed["audit_action"])
	}
	if executed["from_state"] != "in-review" || executed["to_state"] != "ratified" {
		t.Errorf("from/to wrong: %v", executed)
	}

	if got := deliverableState(t, s, delID); got != deliverableStateRatified {
		t.Errorf("state after apply = %q; want ratified", got)
	}

	// ratified_at + ratified_by_actor stamps set.
	var ratAt, ratActor string
	if err := s.db.QueryRow(
		`SELECT COALESCE(ratified_at,''), COALESCE(ratified_by_actor,'') FROM deliverables WHERE id = ?`,
		delID).Scan(&ratAt, &ratActor); err != nil {
		t.Fatalf("read stamps: %v", err)
	}
	if ratAt == "" {
		t.Error("ratified_at not stamped")
	}
	if ratActor != "@principal" {
		t.Errorf("ratified_by_actor = %q; want @principal", ratActor)
	}

	// Audit row written with via=propose lineage.
	var auditMeta string
	if err := s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'deliverable.ratified' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, delID).Scan(&auditMeta); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(auditMeta, `"via":"propose"`) {
		t.Errorf("audit meta missing via=propose: %q", auditMeta)
	}
	if !strings.Contains(auditMeta, `"propose_id":"att-001"`) {
		t.Errorf("audit meta missing propose_id: %q", auditMeta)
	}
	if !strings.Contains(auditMeta, `"by_tier":"principal"`) {
		t.Errorf("audit meta missing by_tier: %q", auditMeta)
	}
}

// Apply: ratify via the propose path auto-fires a pending gate criterion
// referencing the deliverable, mirroring the REST /ratify cascade (#53).
func TestDeliverableSetState_Apply_RatifyFiresGateCascade(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("deliverable.set_state")
	proj := seedProject(t, s, defaultTeamID)
	delID := seedDeliverable(t, s, proj, "design", "research_plan", deliverableStateInReview)
	// A pending gate criterion in the same phase, bound to this deliverable.
	gateID := seedCriterion(t, s, proj, "design", "gate", map[string]any{
		"gate":   "deliverable.ratified",
		"params": map[string]any{"deliverable_id": delID},
	})

	target, _ := json.Marshal(map[string]any{"project_id": proj, "deliverable_id": delID})
	spec, _ := json.Marshal(map[string]any{"state": deliverableStateRatified})
	ac := ProposeApplyContext{
		AttentionID: "att-gate", Team: defaultTeamID, AssignedTier: GovTierPrincipal,
		DeciderHandle: "@principal",
	}
	if _, err := pk.Apply(context.Background(), s, ac, target, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The gate must have auto-fired to met (was pending before the apply).
	var gateState string
	if err := s.db.QueryRow(
		`SELECT state FROM acceptance_criteria WHERE id = ?`, gateID).Scan(&gateState); err != nil {
		t.Fatalf("read gate state: %v", err)
	}
	if gateState != criterionStateMet {
		t.Errorf("gate criterion state = %q; want %q (propose ratify did not cascade)",
			gateState, criterionStateMet)
	}
}

// 5. Apply: ratified → draft clears the ratified stamps, emits
// deliverable.unratified.
func TestDeliverableSetState_Apply_RatifiedToDraft(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("deliverable.set_state")
	proj := seedProject(t, s, defaultTeamID)
	delID := seedDeliverable(t, s, proj, "design", "research_plan", deliverableStateInReview)
	// Pre-ratify.
	if _, err := s.db.Exec(`
		UPDATE deliverables
		SET ratification_state = 'ratified', ratified_at = ?, ratified_by_actor = 'seed'
		WHERE id = ?`, NowUTC(), delID); err != nil {
		t.Fatalf("pre-ratify: %v", err)
	}

	target, _ := json.Marshal(map[string]any{"project_id": proj, "deliverable_id": delID})
	spec, _ := json.Marshal(map[string]any{"state": deliverableStateDraft})
	ac := ProposeApplyContext{
		AttentionID: "att-002", Team: defaultTeamID, AssignedTier: GovTierPrincipal,
	}
	exec, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(exec, &executed)
	if executed["audit_action"] != "deliverable.unratified" {
		t.Errorf("audit_action = %v; want deliverable.unratified", executed["audit_action"])
	}
	if got := deliverableState(t, s, delID); got != deliverableStateDraft {
		t.Errorf("state = %q; want draft", got)
	}
	var ratAt, ratActor string
	_ = s.db.QueryRow(
		`SELECT COALESCE(ratified_at,''), COALESCE(ratified_by_actor,'') FROM deliverables WHERE id = ?`,
		delID).Scan(&ratAt, &ratActor)
	if ratAt != "" || ratActor != "" {
		t.Errorf("unratify did not clear stamps: at=%q actor=%q", ratAt, ratActor)
	}
}

// 6. Apply: draft → in-review emits deliverable.updated, no stamps.
func TestDeliverableSetState_Apply_DraftToInReview(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("deliverable.set_state")
	proj := seedProject(t, s, defaultTeamID)
	delID := seedDeliverable(t, s, proj, "design", "research_plan", deliverableStateDraft)

	target, _ := json.Marshal(map[string]any{"project_id": proj, "deliverable_id": delID})
	spec, _ := json.Marshal(map[string]any{"state": deliverableStateInReview})
	exec, err := pk.Apply(context.Background(), s, ProposeApplyContext{Team: defaultTeamID}, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(exec, &executed)
	if executed["audit_action"] != "deliverable.updated" {
		t.Errorf("audit_action = %v; want deliverable.updated", executed["audit_action"])
	}
	if got := deliverableState(t, s, delID); got != deliverableStateInReview {
		t.Errorf("state = %q; want in-review", got)
	}
}

// 7. Apply: no-op (from == to) returns executed.no_op = true, doesn't
// touch the row or emit an audit.
func TestDeliverableSetState_Apply_NoOp(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("deliverable.set_state")
	proj := seedProject(t, s, defaultTeamID)
	delID := seedDeliverable(t, s, proj, "design", "research_plan", deliverableStateRatified)

	beforeAudits := countAudits(t, s)
	target, _ := json.Marshal(map[string]any{"project_id": proj, "deliverable_id": delID})
	spec, _ := json.Marshal(map[string]any{"state": deliverableStateRatified})
	exec, err := pk.Apply(context.Background(), s, ProposeApplyContext{Team: defaultTeamID}, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(exec, &executed)
	if executed["no_op"] != true {
		t.Errorf("no_op = %v; want true", executed["no_op"])
	}
	if after := countAudits(t, s); after != beforeAudits {
		t.Errorf("no-op apply emitted an audit (count %d → %d)", beforeAudits, after)
	}
}

// 8. End-to-end via mcpPropose: worker calls propose, row lands,
// then the apply function is callable through the registry with the
// row's attention_id.
func TestDeliverableSetState_EndToEnd_ProposeThenManualApply(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	delID := seedDeliverable(t, s, proj, "design", "research_plan", deliverableStateInReview)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)

	args, _ := json.Marshal(map[string]any{
		"kind":        "deliverable.set_state",
		"target_ref":  map[string]any{"project_id": proj, "deliverable_id": delID},
		"change_spec": map[string]any{"state": deliverableStateRatified},
		"reason":      "review passed",
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	payload := unwrapMcpResult(t, out)
	attID := payload["request_id"].(string)
	if attID == "" {
		t.Fatal("expected request_id")
	}
	// Verify state still untouched (no apply yet — W8 wires that).
	if got := deliverableState(t, s, delID); got != deliverableStateInReview {
		t.Errorf("propose mutated state; got %q want in-review", got)
	}

	// Manually invoke Apply with the row's attention_id (simulates the
	// W8 decide-time dispatch).
	pk, _ := LookupProposeKind("deliverable.set_state")
	target, _ := json.Marshal(map[string]any{"project_id": proj, "deliverable_id": delID})
	spec, _ := json.Marshal(map[string]any{"state": deliverableStateRatified})
	ac := ProposeApplyContext{
		AttentionID: attID, Team: defaultTeamID, AssignedTier: GovTierPrincipal,
		DeciderHandle: "@principal",
	}
	if _, err := pk.Apply(context.Background(), s, ac, target, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := deliverableState(t, s, delID); got != deliverableStateRatified {
		t.Errorf("apply did not ratify: %q", got)
	}

	// Audit row links back to the propose row.
	var meta string
	if err := s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'deliverable.ratified' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, delID).Scan(&meta); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(meta, `"propose_id":"`+attID+`"`) {
		t.Errorf("audit meta should link back to propose row: %q", meta)
	}
}

// --- helpers ---

func countAudits(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
