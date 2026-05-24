package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ADR-030 W6 — apply_phase_advance.go. Mirrors W5's test layout
// against the apply function registered at init().

// seedProjectWithPhase creates a project and sets its starting phase
// (which seedProject leaves NULL by default).
func seedProjectWithPhase(t *testing.T, s *Server, team, phase string) string {
	t.Helper()
	id := seedProject(t, s, team)
	if phase != "" {
		if _, err := s.db.Exec(
			`UPDATE projects SET phase = ? WHERE id = ?`, phase, id); err != nil {
			t.Fatalf("seed phase: %v", err)
		}
	}
	return id
}

func projectPhase(t *testing.T, s *Server, id string) string {
	t.Helper()
	var p sql_NullStringLike
	if err := s.db.QueryRow(
		`SELECT COALESCE(phase,'') FROM projects WHERE id = ?`, id).Scan(&p.S); err != nil {
		t.Fatalf("read phase: %v", err)
	}
	return p.S
}

type sql_NullStringLike struct{ S string }

// 1. Registered at init.
func TestPhaseAdvance_RegisteredAtInit(t *testing.T) {
	pk, ok := LookupProposeKind("phase.advance")
	if !ok {
		t.Fatal("phase.advance not registered at init()")
	}
	if pk.Validate == nil || pk.DryRun == nil || pk.Apply == nil {
		t.Errorf("missing functions: validate=%v dry=%v apply=%v",
			pk.Validate != nil, pk.DryRun != nil, pk.Apply != nil)
	}
}

// 2. Validate — shape checks.
func TestPhaseAdvance_Validate(t *testing.T) {
	pk, _ := LookupProposeKind("phase.advance")
	cases := []struct {
		name   string
		target string
		spec   string
		wantOK bool
		wantIn string
	}{
		{"happy", `{"project_id":"p"}`, `{"to_phase":"design"}`, true, ""},
		{"happy with from", `{"project_id":"p"}`, `{"from_phase":"intake","to_phase":"design"}`, true, ""},
		{"missing project_id", `{}`, `{"to_phase":"design"}`, false, "project_id"},
		{"missing to_phase", `{"project_id":"p"}`, `{"from_phase":"intake"}`, false, "to_phase"},
		{"empty change_spec", `{"project_id":"p"}`, `{}`, false, "to_phase"},
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
				t.Errorf("err %q should contain %q", err.Error(), tc.wantIn)
			}
		})
	}
}

// 3. DryRun preview shape. No template phases configured so the
// `to_phase_not_in_template` flag should be false (no template set).
func TestPhaseAdvance_DryRun_Preview(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("phase.advance")
	proj := seedProjectWithPhase(t, s, defaultTeamID, "intake")

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{"from_phase": "intake", "to_phase": "design"})
	raw, err := pk.DryRun(context.Background(), s, target, spec)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	var preview map[string]any
	_ = json.Unmarshal(raw, &preview)
	if preview["from_phase"] != "intake" {
		t.Errorf("from_phase = %v; want intake", preview["from_phase"])
	}
	if preview["to_phase"] != "design" {
		t.Errorf("to_phase = %v; want design", preview["to_phase"])
	}
	if preview["no_op"] != false {
		t.Errorf("no_op = %v; want false", preview["no_op"])
	}
	if preview["from_phase_drifted"] != false {
		t.Errorf("from_phase_drifted = %v; want false", preview["from_phase_drifted"])
	}

	// Phase must NOT have changed — DryRun is read-only.
	if got := projectPhase(t, s, proj); got != "intake" {
		t.Errorf("phase mutated by DryRun: %q", got)
	}
}

// 4. DryRun flags from_phase drift when caller's expectation doesn't
// match current.
func TestPhaseAdvance_DryRun_FlagsFromPhaseDrift(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("phase.advance")
	proj := seedProjectWithPhase(t, s, defaultTeamID, "design")

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{"from_phase": "intake", "to_phase": "build"})
	raw, _ := pk.DryRun(context.Background(), s, target, spec)
	var preview map[string]any
	_ = json.Unmarshal(raw, &preview)
	if preview["from_phase_drifted"] != true {
		t.Errorf("from_phase_drifted = %v; want true (proposer staked on intake, current is design)",
			preview["from_phase_drifted"])
	}
}

// 5. Apply happy path: advances phase, appends to history, writes audit.
func TestPhaseAdvance_Apply_HappyPath(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("phase.advance")
	proj := seedProjectWithPhase(t, s, defaultTeamID, "intake")

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{
		"from_phase": "intake", "to_phase": "design", "reason": "ready",
	})
	ac := ProposeApplyContext{
		AttentionID: "att-phase-1", Team: defaultTeamID,
		AssignedTier: GovTierPrincipal, DeciderHandle: "@principal",
	}
	raw, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(raw, &executed)
	if executed["audit_action"] != "project.phase_advanced" {
		t.Errorf("audit_action = %v; want project.phase_advanced", executed["audit_action"])
	}
	if got := projectPhase(t, s, proj); got != "design" {
		t.Errorf("phase = %q; want design", got)
	}

	// Audit row carries via=propose lineage.
	var meta string
	if err := s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'project.phase_advanced' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, proj).Scan(&meta); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{
		`"via":"propose"`, `"propose_id":"att-phase-1"`,
		`"by_tier":"principal"`, `"by_actor":"@principal"`,
	} {
		if !strings.Contains(meta, want) {
			t.Errorf("audit meta missing %s: %q", want, meta)
		}
	}

	// phase_history appended.
	var history string
	_ = s.db.QueryRow(`SELECT COALESCE(phase_history,'') FROM projects WHERE id = ?`, proj).Scan(&history)
	if !strings.Contains(history, `"from":"intake"`) || !strings.Contains(history, `"to":"design"`) {
		t.Errorf("phase_history missing transition: %q", history)
	}
}

// 6. Apply: stale from_phase rejection with descriptive error
//
//	(optimistic-concurrency check).
func TestPhaseAdvance_Apply_StaleFromPhase_Rejects(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("phase.advance")
	// Project is at design; proposer staked on intake.
	proj := seedProjectWithPhase(t, s, defaultTeamID, "design")

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{"from_phase": "intake", "to_phase": "build"})
	ac := ProposeApplyContext{Team: defaultTeamID, AttentionID: "att-x", AssignedTier: GovTierPrincipal}
	_, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err == nil {
		t.Fatal("expected stale from_phase rejection")
	}
	for _, want := range []string{"stale from_phase", `"intake"`, `"design"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q missing %q", err.Error(), want)
		}
	}
	// Phase MUST be unchanged.
	if got := projectPhase(t, s, proj); got != "design" {
		t.Errorf("phase mutated on stale-reject: %q", got)
	}
}

// 7. Apply: no-op (current == to_phase) returns executed.no_op,
// no row mutation, no audit.
func TestPhaseAdvance_Apply_NoOp(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("phase.advance")
	proj := seedProjectWithPhase(t, s, defaultTeamID, "design")
	beforeAudits := countAudits(t, s)

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{"to_phase": "design"})
	ac := ProposeApplyContext{Team: defaultTeamID, AttentionID: "att-noop"}
	raw, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(raw, &executed)
	if executed["no_op"] != true {
		t.Errorf("no_op = %v; want true", executed["no_op"])
	}
	if after := countAudits(t, s); after != beforeAudits {
		t.Errorf("no-op apply emitted an audit (count %d → %d)", beforeAudits, after)
	}
}

// 8. Apply: empty from_phase skips the optimistic check (caller
// accepts whatever current is).
func TestPhaseAdvance_Apply_EmptyFromPhase_SkipsCheck(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("phase.advance")
	proj := seedProjectWithPhase(t, s, defaultTeamID, "design")

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{"to_phase": "build"}) // no from
	ac := ProposeApplyContext{Team: defaultTeamID, AttentionID: "att-skip", AssignedTier: GovTierPrincipal}
	if _, err := pk.Apply(context.Background(), s, ac, target, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := projectPhase(t, s, proj); got != "build" {
		t.Errorf("phase = %q; want build", got)
	}
}

// 9. Apply: project in another team → not found.
func TestPhaseAdvance_Apply_WrongTeam_NotFound(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("phase.advance")
	proj := seedProjectWithPhase(t, s, defaultTeamID, "intake")

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{"to_phase": "design"})
	ac := ProposeApplyContext{Team: "other-team", AttentionID: "att-x"}
	_, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err %q should say not found", err.Error())
	}
}
