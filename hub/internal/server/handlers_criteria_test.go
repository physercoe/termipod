package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// W6 test fixture — exercises the criterion mutation surface and the
// deliverable.ratified gate cascade. Reuses phaseTestSetup +
// authedJSON helpers from the W5b suite.

func TestCriterion_CreateAndPatch(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria",
		map[string]any{
			"phase":    "method",
			"kind":     "text",
			"body":     map[string]any{"text": "Method section ratified by director"},
			"required": true,
			"ord":      0,
		})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var c criterionOut
	_ = json.Unmarshal(rr.Body.Bytes(), &c)
	if c.State != "pending" {
		t.Errorf("state=%q want=pending", c.State)
	}
	if c.Body["text"] != "Method section ratified by director" {
		t.Errorf("body=%v", c.Body)
	}

	patchRR := authedJSON(t, s, http.MethodPatch, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c.ID,
		map[string]any{
			"body":     map[string]any{"text": "Updated text"},
			"ord":      5,
		})
	if patchRR.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", patchRR.Code, patchRR.Body.String())
	}
	var patched criterionOut
	_ = json.Unmarshal(patchRR.Body.Bytes(), &patched)
	if patched.Body["text"] != "Updated text" {
		t.Errorf("post-patch body=%v", patched.Body)
	}
	if patched.Ord != 5 {
		t.Errorf("ord=%d want=5", patched.Ord)
	}
}

func TestCriterion_MarkMetTextEmitsAudit(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria",
		map[string]any{"phase": "method", "kind": "text", "body": map[string]any{"text": "ok"}})
	var c criterionOut
	_ = json.Unmarshal(rr.Body.Bytes(), &c)

	markRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c.ID+"/mark-met",
		map[string]any{"evidence_ref": "document://doc-1#method", "rationale": "looks good"})
	if markRR.Code != http.StatusOK {
		t.Fatalf("mark-met: %d %s", markRR.Code, markRR.Body.String())
	}
	var met criterionOut
	_ = json.Unmarshal(markRR.Body.Bytes(), &met)
	if met.State != "met" {
		t.Errorf("state=%q want=met", met.State)
	}
	if met.MetAt == "" || met.MetByActor == "" {
		t.Errorf("met stamps empty: at=%q by=%q", met.MetAt, met.MetByActor)
	}
	if met.EvidenceRef != "document://doc-1#method" {
		t.Errorf("evidence_ref=%q", met.EvidenceRef)
	}

	auditRR := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/audit?project_id="+project, nil)
	var rows []AuditRow
	_ = json.Unmarshal(auditRR.Body.Bytes(), &rows)
	gotMet := false
	for _, e := range rows {
		if e.Action == "criterion.met" {
			gotMet = true
		}
	}
	if !gotMet {
		t.Errorf("no criterion.met in audit")
	}
}

func TestCriterion_MarkFailedAndWaiveClearsMetStamps(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria",
		map[string]any{"phase": "method", "kind": "text", "body": map[string]any{"text": "x"}})
	var c criterionOut
	_ = json.Unmarshal(rr.Body.Bytes(), &c)

	// Mark met first.
	authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c.ID+"/mark-met", nil)

	// Then waive — should clear met stamps and emit criterion.waived.
	wRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c.ID+"/waive",
		map[string]any{"reason": "pivot"})
	if wRR.Code != http.StatusOK {
		t.Fatalf("waive: %d %s", wRR.Code, wRR.Body.String())
	}
	var waived criterionOut
	_ = json.Unmarshal(wRR.Body.Bytes(), &waived)
	if waived.State != "waived" {
		t.Errorf("state=%q want=waived", waived.State)
	}
	if waived.MetAt != "" || waived.MetByActor != "" {
		t.Errorf("waive did not clear stamps: at=%q by=%q",
			waived.MetAt, waived.MetByActor)
	}
}

func TestCriterion_GateRejectsManualMark(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria",
		map[string]any{
			"phase": "method", "kind": "gate",
			"body": map[string]any{"gate": "deliverable.ratified"},
		})
	var c criterionOut
	_ = json.Unmarshal(rr.Body.Bytes(), &c)

	mark := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c.ID+"/mark-met", nil)
	if mark.Code != http.StatusForbidden {
		t.Errorf("manual gate mark: %d want 403", mark.Code)
	}
}

func TestCriterion_DeliverableRatifyCascadesGate(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	// Create a deliverable in phase=method.
	delivRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{"phase": "method", "kind": "method-doc"})
	var d deliverableOut
	_ = json.Unmarshal(delivRR.Body.Bytes(), &d)

	// Gate criterion that references this deliverable.
	critRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria",
		map[string]any{
			"phase":          "method",
			"deliverable_id": d.ID,
			"kind":           "gate",
			"body":           map[string]any{"gate": "deliverable.ratified"},
		})
	if critRR.Code != http.StatusCreated {
		t.Fatalf("create gate criterion: %d %s", critRR.Code, critRR.Body.String())
	}
	var c criterionOut
	_ = json.Unmarshal(critRR.Body.Bytes(), &c)

	// Ratify the deliverable; cascade should fire.
	ratRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID+"/ratify", nil)
	if ratRR.Code != http.StatusOK {
		t.Fatalf("ratify: %d %s", ratRR.Code, ratRR.Body.String())
	}

	// Re-read the criterion — should be met.
	getRR := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c.ID, nil)
	var post criterionOut
	_ = json.Unmarshal(getRR.Body.Bytes(), &post)
	if post.State != "met" {
		t.Errorf("post-cascade state=%q want=met", post.State)
	}
	if post.EvidenceRef != "deliverable://"+d.ID {
		t.Errorf("evidence_ref=%q", post.EvidenceRef)
	}
	if post.MetByActor != "system:gate" {
		t.Errorf("met_by=%q want=system:gate", post.MetByActor)
	}
}

func TestCriterion_GateScopedByParamsDoesNotFireForOtherDeliverable(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	// Two deliverables.
	d1RR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{"phase": "method", "kind": "method-doc"})
	var d1 deliverableOut
	_ = json.Unmarshal(d1RR.Body.Bytes(), &d1)
	d2RR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{"phase": "method", "kind": "method-doc"})
	var d2 deliverableOut
	_ = json.Unmarshal(d2RR.Body.Bytes(), &d2)

	// Gate that names d2 explicitly via body.params.deliverable_id, but
	// is otherwise unscoped (no deliverable_id column).
	critRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria",
		map[string]any{
			"phase": "method",
			"kind":  "gate",
			"body": map[string]any{
				"gate":   "deliverable.ratified",
				"params": map[string]any{"deliverable_id": d2.ID},
			},
		})
	var c criterionOut
	_ = json.Unmarshal(critRR.Body.Bytes(), &c)

	// Ratifying d1 must NOT fire.
	authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d1.ID+"/ratify", nil)
	getRR := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c.ID, nil)
	var afterD1 criterionOut
	_ = json.Unmarshal(getRR.Body.Bytes(), &afterD1)
	if afterD1.State != "pending" {
		t.Errorf("after d1 ratify state=%q want=pending", afterD1.State)
	}

	// Ratifying d2 fires.
	authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d2.ID+"/ratify", nil)
	getRR2 := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria/"+c.ID, nil)
	var afterD2 criterionOut
	_ = json.Unmarshal(getRR2.Body.Bytes(), &afterD2)
	if afterD2.State != "met" {
		t.Errorf("after d2 ratify state=%q want=met", afterD2.State)
	}
}

func TestCriterion_PhaseAdvanceBlocksWhenRequiredCriterionPending(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	// Required text criterion in phase=idea.
	authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/criteria",
		map[string]any{
			"phase": "idea", "kind": "text",
			"body": map[string]any{"text": "scope agreed"}, "required": true,
		})

	// Advance from idea → method should 409 (W1's gate logic).
	advRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/phase/advance", nil)
	if advRR.Code != http.StatusConflict {
		t.Fatalf("advance with pending criterion: %d want 409 — %s",
			advRR.Code, advRR.Body.String())
	}
}
