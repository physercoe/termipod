package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// phase_auto_advance_test.go — ADR-044 P3. AC-driven auto-advance: a phase
// advances automatically once all its required criteria are satisfied, the
// human gate lives in a `gate` criterion, an unmet required criterion
// blocks, and the propose phase.advance verb is retired. Drives the real
// REST mark/ratify endpoints (which fire the hook) via phaseTestSetup +
// authedJSON.

// currentPhase reads the project's current phase over the REST phase read.
func currentPhase(t *testing.T, s *Server, tok, team, project string) string {
	t.Helper()
	rr := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/projects/"+project+"/phase", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get phase: %d %s", rr.Code, rr.Body.String())
	}
	var out phaseOut
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode phase: %v", err)
	}
	return out.Phase
}

func mkCriterion(t *testing.T, s *Server, tok, team, project string, body map[string]any) string {
	t.Helper()
	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create criterion: %d %s", rr.Code, rr.Body.String())
	}
	var c criterionOut
	_ = json.Unmarshal(rr.Body.Bytes(), &c)
	return c.ID
}

// 1. All required criteria met → the phase auto-advances one step.
func TestP3_AutoAdvance_AllRequiredMet(t *testing.T) {
	s, tok, team, project := phaseTestSetup(t, []string{"idea", "method"})
	crit := mkCriterion(t, s, tok, team, project, map[string]any{
		"phase": "idea", "kind": "text", "required": true,
		"body": map[string]any{"text": "scope bounded"}})

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+crit+"/mark-met", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("mark-met: %d %s", rr.Code, rr.Body.String())
	}
	if got := currentPhase(t, s, tok, team, project); got != "method" {
		t.Errorf("phase after mark-met = %q; want method (auto-advanced)", got)
	}
}

// 2. One of two required criteria met → still blocked (no advance).
func TestP3_AutoAdvance_BlocksOnUnmet(t *testing.T) {
	s, tok, team, project := phaseTestSetup(t, []string{"idea", "method"})
	c1 := mkCriterion(t, s, tok, team, project, map[string]any{
		"phase": "idea", "kind": "text", "required": true,
		"body": map[string]any{"text": "first"}})
	mkCriterion(t, s, tok, team, project, map[string]any{
		"phase": "idea", "kind": "text", "required": true,
		"body": map[string]any{"text": "second"}})

	authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c1+"/mark-met", nil)
	if got := currentPhase(t, s, tok, team, project); got != "idea" {
		t.Errorf("phase = %q; want idea (one required criterion still pending)", got)
	}
}

// 3. A phase with NO required criteria does not auto-advance on an
// unrelated (non-required) mark — it waits for a manual advance.
func TestP3_NoAutoAdvance_WhenNoRequiredCriteria(t *testing.T) {
	s, tok, team, project := phaseTestSetup(t, []string{"idea", "method"})
	c := mkCriterion(t, s, tok, team, project, map[string]any{
		"phase": "idea", "kind": "text", "required": false,
		"body": map[string]any{"text": "nice to have"}})

	authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c+"/mark-met", nil)
	if got := currentPhase(t, s, tok, team, project); got != "idea" {
		t.Errorf("phase = %q; want idea (no required criteria to gate on)", got)
	}
}

// 4. Waiving the last required criterion also satisfies the phase.
func TestP3_AutoAdvance_WaivedCountsSatisfied(t *testing.T) {
	s, tok, team, project := phaseTestSetup(t, []string{"idea", "method"})
	c := mkCriterion(t, s, tok, team, project, map[string]any{
		"phase": "idea", "kind": "text", "required": true,
		"body": map[string]any{"text": "out of scope now"}})

	authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c+"/waive", nil)
	if got := currentPhase(t, s, tok, team, project); got != "method" {
		t.Errorf("phase after waive = %q; want method", got)
	}
}

// 5. A required GATE criterion auto-fires on deliverable ratify, and the
// cascade triggers the auto-advance (the human gate is the ratification).
func TestP3_AutoAdvance_GateCascade(t *testing.T) {
	s, tok, team, project := phaseTestSetup(t, []string{"idea", "method"})

	delRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{"phase": "idea", "kind": "scope-doc"})
	if delRR.Code != http.StatusCreated {
		t.Fatalf("create deliverable: %d %s", delRR.Code, delRR.Body.String())
	}
	var del deliverableOut
	_ = json.Unmarshal(delRR.Body.Bytes(), &del)

	mkCriterion(t, s, tok, team, project, map[string]any{
		"phase": "idea", "kind": "gate", "required": true,
		"deliverable_id": del.ID,
		"body":           map[string]any{"gate": "deliverable.ratified"}})

	ratRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+del.ID+"/ratify", nil)
	if ratRR.Code != http.StatusOK {
		t.Fatalf("ratify: %d %s", ratRR.Code, ratRR.Body.String())
	}
	if got := currentPhase(t, s, tok, team, project); got != "method" {
		t.Errorf("phase after ratify = %q; want method (gate cascade → auto-advance)", got)
	}
}

// 6. The final phase never auto-advances (no further phase).
func TestP3_NoAutoAdvance_LastPhase(t *testing.T) {
	s, tok, team, project := phaseTestSetup(t, []string{"idea"})
	c := mkCriterion(t, s, tok, team, project, map[string]any{
		"phase": "idea", "kind": "text", "required": true,
		"body": map[string]any{"text": "done"}})

	authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c+"/mark-met", nil)
	if got := currentPhase(t, s, tok, team, project); got != "idea" {
		t.Errorf("phase = %q; want idea (last phase, nothing to advance to)", got)
	}
}

// 7. propose phase.advance is retired (Q4) — no longer in the registry.
func TestP3_PhaseAdvanceProposeRetired(t *testing.T) {
	if _, ok := LookupProposeKind("phase.advance"); ok {
		t.Error("phase.advance is still registered — Q4 retires the propose verb")
	}
}
