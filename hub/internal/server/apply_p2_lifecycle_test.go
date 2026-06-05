package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// apply_p2_lifecycle_test.go — ADR-044 P2. The governed propose verbs for
// editing the lifecycle roadmap: deliverable.create + criteria.create/
// update/delete. Tests run against the live init() registration and drive
// the Apply/Rollback functions directly (the decide-handler dispatch is
// generic over the registry, covered by the existing propose-dispatch
// suite). Reuses newTestServer / seedProject / defaultTeamID / the
// GovTier* constants from the W5 apply suite.

func p2ApplyCtx() ProposeApplyContext {
	return ProposeApplyContext{
		AttentionID:   "att-p2",
		Team:          defaultTeamID,
		AssignedTier:  GovTierPrincipal,
		DeciderHandle: "@principal",
	}
}

// seedCriterion inserts a minimal acceptance_criteria row and returns its id.
func seedCriterion(t *testing.T, s *Server, project, phase, kind string, body map[string]any) string {
	t.Helper()
	id := NewID()
	now := NowUTC()
	bj := "{}"
	if len(body) > 0 {
		b, _ := json.Marshal(body)
		bj = string(b)
	}
	if _, err := s.db.Exec(`
		INSERT INTO acceptance_criteria
			(id, project_id, phase, kind, body, state, required, ord, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', 1, 0, ?, ?)`,
		id, project, phase, kind, bj, now, now); err != nil {
		t.Fatalf("seed criterion: %v", err)
	}
	return id
}

func criterionExists(t *testing.T, s *Server, id string) bool {
	t.Helper()
	var found string
	err := s.db.QueryRow(`SELECT id FROM acceptance_criteria WHERE id = ?`, id).Scan(&found)
	return err == nil
}

// 1. All four P2 kinds register at init with the full function set.
func TestP2_KindsRegisteredAtInit(t *testing.T) {
	for _, kind := range []string{
		"deliverable.create", "criteria.create", "criteria.update", "criteria.delete",
	} {
		pk, ok := LookupProposeKind(kind)
		if !ok {
			t.Errorf("%s not registered at init()", kind)
			continue
		}
		if pk.Validate == nil || pk.DryRun == nil || pk.Apply == nil || pk.Rollback == nil {
			t.Errorf("%s missing fns: validate=%v dry=%v apply=%v rollback=%v",
				kind, pk.Validate != nil, pk.DryRun != nil, pk.Apply != nil, pk.Rollback != nil)
		}
	}
}

// 2. Validate error paths (shape checks, no DB).
func TestP2_Validate(t *testing.T) {
	cases := []struct {
		kind, target, spec, wantIn string
	}{
		{"deliverable.create", `{}`, `{"phase":"p","kind":"k"}`, "project_id"},
		{"deliverable.create", `{"project_id":"p"}`, `{"kind":"k"}`, "phase"},
		{"deliverable.create", `{"project_id":"p"}`, `{"phase":"p"}`, "kind"},
		{"criteria.create", `{"project_id":"p"}`, `{"phase":"p","kind":"bogus"}`, "invalid"},
		{"criteria.create", `{"project_id":"p"}`, `{"phase":"p"}`, "kind"},
		{"criteria.update", `{"project_id":"p"}`, `{"body":{"text":"x"}}`, "criterion_id"},
		{"criteria.update", `{"project_id":"p","criterion_id":"c"}`, `{}`, "at least one"},
		{"criteria.delete", `{"project_id":"p"}`, `{}`, "criterion_id"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.wantIn, func(t *testing.T) {
			pk, _ := LookupProposeKind(tc.kind)
			err := pk.Validate(context.Background(), nil,
				json.RawMessage(tc.target), json.RawMessage(tc.spec))
			if err == nil {
				t.Fatalf("%s: want error containing %q, got nil", tc.kind, tc.wantIn)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("%s: error %q should contain %q", tc.kind, err.Error(), tc.wantIn)
			}
		})
	}
}

// 3. deliverable.create Apply inserts a draft deliverable; Rollback removes it.
func TestP2_DeliverableCreate_ApplyRollback(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	pk, _ := LookupProposeKind("deliverable.create")

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{"phase": "idea", "kind": "scope-doc"})
	execRaw, err := pk.Apply(context.Background(), s, p2ApplyCtx(), target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var exec map[string]any
	_ = json.Unmarshal(execRaw, &exec)
	delID, _ := exec["deliverable_id"].(string)
	if delID == "" {
		t.Fatalf("no deliverable_id in executed: %s", execRaw)
	}
	if got := deliverableState(t, s, delID); got != deliverableStateDraft {
		t.Errorf("created state=%q want=draft", got)
	}

	// Rollback removes it.
	if _, err := pk.Rollback(context.Background(), s, p2ApplyCtx(), nil, execRaw); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	var found string
	if err := s.db.QueryRow(`SELECT id FROM deliverables WHERE id = ?`, delID).Scan(&found); err == nil {
		t.Errorf("deliverable %s still present after rollback", delID)
	}
}

// 4. criteria.create Apply inserts a pending criterion; Rollback removes it.
func TestP2_CriteriaCreate_ApplyRollback(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	pk, _ := LookupProposeKind("criteria.create")

	target, _ := json.Marshal(map[string]any{"project_id": proj})
	spec, _ := json.Marshal(map[string]any{
		"phase": "idea", "kind": "text", "body": map[string]any{"text": "scope bounded"}})
	execRaw, err := pk.Apply(context.Background(), s, p2ApplyCtx(), target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var exec map[string]any
	_ = json.Unmarshal(execRaw, &exec)
	critID, _ := exec["criterion_id"].(string)
	if critID == "" || !criterionExists(t, s, critID) {
		t.Fatalf("criterion not created: %s", execRaw)
	}

	if _, err := pk.Rollback(context.Background(), s, p2ApplyCtx(), nil, execRaw); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if criterionExists(t, s, critID) {
		t.Errorf("criterion %s still present after rollback", critID)
	}
}

// 5. criteria.update Apply edits the rubric; Rollback restores prior values.
func TestP2_CriteriaUpdate_ApplyRollback(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	critID := seedCriterion(t, s, proj, "idea", "text", map[string]any{"text": "original"})
	pk, _ := LookupProposeKind("criteria.update")

	target, _ := json.Marshal(map[string]any{"project_id": proj, "criterion_id": critID})
	spec, _ := json.Marshal(map[string]any{"body": map[string]any{"text": "revised"}, "required": false})
	execRaw, err := pk.Apply(context.Background(), s, p2ApplyCtx(), target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after, _ := s.loadCriterion(context.Background(), proj, critID)
	if after.Body["text"] != "revised" {
		t.Errorf("body after update=%v want=revised", after.Body["text"])
	}
	if after.Required {
		t.Errorf("required after update=true want=false")
	}

	// Rollback restores the original definition.
	if _, err := pk.Rollback(context.Background(), s, p2ApplyCtx(), nil, execRaw); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	restored, _ := s.loadCriterion(context.Background(), proj, critID)
	if restored.Body["text"] != "original" {
		t.Errorf("body after rollback=%v want=original", restored.Body["text"])
	}
	if !restored.Required {
		t.Errorf("required after rollback=false want=true (restored)")
	}
}

// 6. criteria.delete Apply removes the criterion; Rollback re-inserts it
// verbatim (same id, kind, state).
func TestP2_CriteriaDelete_ApplyRollback(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	critID := seedCriterion(t, s, proj, "idea", "metric", map[string]any{"metric": "loss<0.1"})
	pk, _ := LookupProposeKind("criteria.delete")

	target, _ := json.Marshal(map[string]any{"project_id": proj, "criterion_id": critID})
	execRaw, err := pk.Apply(context.Background(), s, p2ApplyCtx(), target, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if criterionExists(t, s, critID) {
		t.Fatalf("criterion %s still present after delete", critID)
	}

	// Rollback re-inserts the captured snapshot.
	if _, err := pk.Rollback(context.Background(), s, p2ApplyCtx(), nil, execRaw); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	restored, err := s.loadCriterion(context.Background(), proj, critID)
	if err != nil {
		t.Fatalf("criterion not restored: %v", err)
	}
	if restored.Kind != "metric" || restored.State != "pending" {
		t.Errorf("restored row wrong: kind=%q state=%q", restored.Kind, restored.State)
	}
}
