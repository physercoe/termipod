package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// W5b test fixture — drives the deliverable + component handlers from a
// hand-seeded research-template-shaped project. Reuses phaseTestSetup
// from handlers_phase_test.go.

func authedJSON(t *testing.T, s *Server, method, tok, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewBuffer(b)
	}
	var req *http.Request
	if buf != nil {
		req = httptest.NewRequest(method, path, buf)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	return rr
}

func TestDeliverable_CreateListGet(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{
			"phase":    "lit-review",
			"kind":     "lit-review-doc",
			"required": true,
			"ord":      0,
			"components": []map[string]any{
				{"kind": "document", "ref_id": "doc-citations", "required": true, "ord": 0},
			},
		})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created deliverableOut
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.RatificationState != "draft" {
		t.Errorf("state=%q want=draft", created.RatificationState)
	}
	if len(created.Components) != 1 || created.Components[0].Kind != "document" {
		t.Errorf("components=%v want=1 document", created.Components)
	}

	listRR := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables?include=components", nil)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRR.Code, listRR.Body.String())
	}
	var list struct {
		Items []deliverableOut `json:"items"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Errorf("list items=%v", list.Items)
	}
	if len(list.Items[0].Components) != 1 {
		t.Errorf("list components missing")
	}

	getRR := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+created.ID, nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get: %d %s", getRR.Code, getRR.Body.String())
	}
	var fetched deliverableOut
	if err := json.Unmarshal(getRR.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if len(fetched.Components) != 1 {
		t.Errorf("fetched components missing")
	}
}

func TestDeliverable_RatifyAndUnratify(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{"phase": "lit-review", "kind": "lit-review-doc"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var d deliverableOut
	_ = json.Unmarshal(rr.Body.Bytes(), &d)

	ratRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID+"/ratify",
		map[string]any{"rationale": "looks good"})
	if ratRR.Code != http.StatusOK {
		t.Fatalf("ratify: %d %s", ratRR.Code, ratRR.Body.String())
	}
	var ratified deliverableOut
	_ = json.Unmarshal(ratRR.Body.Bytes(), &ratified)
	if ratified.RatificationState != "ratified" {
		t.Errorf("after ratify state=%q want=ratified", ratified.RatificationState)
	}
	if ratified.RatifiedAt == "" {
		t.Errorf("ratified_at empty")
	}
	if ratified.RatifiedByActor == "" {
		t.Errorf("ratified_by_actor empty")
	}

	// Re-ratify is a 409 (idempotency-by-conflict per A3 §4.5).
	dupRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID+"/ratify", nil)
	if dupRR.Code != http.StatusConflict {
		t.Errorf("re-ratify: %d want 409", dupRR.Code)
	}

	// Unratify returns to draft.
	unRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID+"/unratify",
		map[string]any{"reason": "found typo"})
	if unRR.Code != http.StatusOK {
		t.Fatalf("unratify: %d %s", unRR.Code, unRR.Body.String())
	}
	var unrat deliverableOut
	_ = json.Unmarshal(unRR.Body.Bytes(), &unrat)
	if unrat.RatificationState != "draft" {
		t.Errorf("after unratify state=%q want=draft", unrat.RatificationState)
	}
	if unrat.RatifiedAt != "" || unrat.RatifiedByActor != "" {
		t.Errorf("unratify did not clear stamps: at=%q by=%q",
			unrat.RatifiedAt, unrat.RatifiedByActor)
	}
}

func TestDeliverable_AddRemoveComponent(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	createRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{"phase": "method", "kind": "method-doc"})
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", createRR.Code, createRR.Body.String())
	}
	var d deliverableOut
	_ = json.Unmarshal(createRR.Body.Bytes(), &d)

	addRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID+"/components",
		map[string]any{"kind": "artifact", "ref_id": "art-fig1", "required": true, "ord": 1})
	if addRR.Code != http.StatusCreated {
		t.Fatalf("add component: %d %s", addRR.Code, addRR.Body.String())
	}
	var c deliverableComponentOut
	_ = json.Unmarshal(addRR.Body.Bytes(), &c)
	if c.Kind != "artifact" || c.RefID != "art-fig1" {
		t.Errorf("component out=%+v", c)
	}

	delRR := authedJSON(t, s, http.MethodDelete, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID+"/components/"+c.ID, nil)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("remove component: %d %s", delRR.Code, delRR.Body.String())
	}

	// Component gone from get response.
	getRR := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID, nil)
	var afterDel deliverableOut
	_ = json.Unmarshal(getRR.Body.Bytes(), &afterDel)
	if len(afterDel.Components) != 0 {
		t.Errorf("after delete components=%v want=empty", afterDel.Components)
	}
}

func TestDeliverable_PatchValidation(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{"phase": "method", "kind": "method-doc"})
	var d deliverableOut
	_ = json.Unmarshal(rr.Body.Bytes(), &d)

	// PATCH ratification_state=ratified is rejected — must use /ratify.
	bad := authedJSON(t, s, http.MethodPatch, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID,
		map[string]any{"ratification_state": "ratified"})
	if bad.Code != http.StatusBadRequest {
		t.Errorf("PATCH→ratified: %d want 400", bad.Code)
	}

	// PATCH to in-review is fine.
	good := authedJSON(t, s, http.MethodPatch, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d.ID,
		map[string]any{"ratification_state": "in-review", "ord": 5})
	if good.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", good.Code, good.Body.String())
	}
	var updated deliverableOut
	_ = json.Unmarshal(good.Body.Bytes(), &updated)
	if updated.RatificationState != "in-review" {
		t.Errorf("after patch state=%q want=in-review", updated.RatificationState)
	}
	if updated.Ord != 5 {
		t.Errorf("ord=%d want=5", updated.Ord)
	}
}

func TestDeliverable_OverviewBundlesPhaseAndDeliverables(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)

	// Advance to lit-review so the active-phase filter exercises a non-first phase.
	advanceRR := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/phase/advance", nil)
	if advanceRR.Code != http.StatusOK {
		t.Fatalf("advance: %d %s", advanceRR.Code, advanceRR.Body.String())
	}
	for i := 0; i < 2; i++ {
		_ = authedJSON(t, s, http.MethodPost, tok,
			"/v1/teams/"+team+"/projects/"+project+"/deliverables",
			map[string]any{"phase": "lit-review", "kind": "lit-review-doc", "ord": i})
	}
	rr := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/projects/"+project+"/overview", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("overview: %d %s", rr.Code, rr.Body.String())
	}
	var ov projectOverviewOut
	if err := json.Unmarshal(rr.Body.Bytes(), &ov); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ov.Phase != "lit-review" {
		t.Errorf("phase=%q want=lit-review", ov.Phase)
	}
	if ov.PhaseIndex != 1 {
		t.Errorf("phase_index=%d want=1", ov.PhaseIndex)
	}
	if len(ov.Deliverables) != 2 {
		t.Errorf("deliverables=%d want=2", len(ov.Deliverables))
	}
	if ov.Counts.DeliverablesTotal != 2 {
		t.Errorf("counts total=%d want=2", ov.Counts.DeliverablesTotal)
	}
}

func TestDeliverable_ProjectScopedAuditFiltersDeliverableEvents(t *testing.T) {
	phases := []string{"idea", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{"phase": "method", "kind": "method-doc"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	auditRR := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/audit?project_id="+project, nil)
	if auditRR.Code != http.StatusOK {
		t.Fatalf("audit list: %d %s", auditRR.Code, auditRR.Body.String())
	}
	var rows []AuditRow
	if err := json.Unmarshal(auditRR.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	gotCreated := false
	for _, e := range rows {
		if e.Action == "deliverable.created" {
			gotCreated = true
			if pid, _ := e.Meta["project_id"].(string); pid != project {
				t.Errorf("audit meta.project_id=%v want=%v", pid, project)
			}
		}
	}
	if !gotCreated {
		t.Errorf("no deliverable.created in audit feed; events=%v", rows)
	}
}

// ADR-020 W2 — send-back-with-notes tests.

// createDeliverableForSendBack returns (deliverableID, docID, [annotationIDs])
// for a typed lit-review document component. Helper keeps the test bodies
// focused on the transitions/validations rather than the wire-up.
func createDeliverableForSendBack(
	t *testing.T, s *Server, tok, team, project string,
) (string, string, []string) {
	t.Helper()
	docID := createTypedDocument(t, s, tok, project, "lit-review", "research-lit-review-v1",
		[]map[string]any{
			{"slug": "gaps", "title": "Gaps", "body": "draft", "status": "draft"},
			{"slug": "prior-work", "title": "Prior work", "body": "ok", "status": "ratified"},
		})

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables",
		map[string]any{
			"phase": "lit-review", "kind": "lit-review-doc",
			"components": []map[string]any{{"kind": "document", "ref_id": docID}},
		})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create deliverable: %d %s", rr.Code, rr.Body.String())
	}
	var d deliverableOut
	_ = json.Unmarshal(rr.Body.Bytes(), &d)

	a1 := mustCreateAnnotation(t, s, tok, docID, "gaps", "comment", "tighten this")
	a2 := mustCreateAnnotation(t, s, tok, docID, "prior-work", "redline", "drop §2")
	return d.ID, docID, []string{a1, a2}
}

func mustCreateAnnotation(
	t *testing.T, s *Server, tok, docID, section, kind, body string,
) string {
	t.Helper()
	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/annotations",
		map[string]any{"section_slug": section, "kind": kind, "body": body})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed annotation: %d %s", rr.Code, rr.Body.String())
	}
	var a Annotation
	_ = json.Unmarshal(rr.Body.Bytes(), &a)
	return a.ID
}

func TestDeliverable_SendBackFromDraftRaisesAttention(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)
	d, _, ids := createDeliverableForSendBack(t, s, tok, team, project)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d+"/send-back",
		map[string]any{
			"note":           "Tighten the gaps section; the prior-work redline is non-negotiable.",
			"annotation_ids": ids,
		})
	if rr.Code != http.StatusOK {
		t.Fatalf("send-back: %d %s", rr.Code, rr.Body.String())
	}
	var out sendBackDeliverableOut
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Deliverable.RatificationState != "in-review" {
		t.Errorf("state after send-back: got %q want in-review",
			out.Deliverable.RatificationState)
	}
	if out.AttentionItemID == "" {
		t.Errorf("attention_item_id empty")
	}

	// Confirm the attention row landed with the right kind + payload.
	listRR := authedJSON(t, s, http.MethodGet, tok,
		"/v1/teams/"+team+"/attention?status=open", nil)
	if listRR.Code != http.StatusOK {
		t.Fatalf("attention list: %d %s", listRR.Code, listRR.Body.String())
	}
	var items []struct {
		ID             string          `json:"id"`
		Kind           string          `json:"kind"`
		ProjectID      string          `json:"project_id"`
		Summary        string          `json:"summary"`
		PendingPayload json.RawMessage `json:"pending_payload,omitempty"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode attention list: %v body=%s", err, listRR.Body.String())
	}
	var found bool
	for _, it := range items {
		if it.ID != out.AttentionItemID {
			continue
		}
		found = true
		if it.Kind != "revision_requested" {
			t.Errorf("kind=%q want=revision_requested", it.Kind)
		}
		if it.ProjectID != project {
			t.Errorf("project_id=%q want=%q", it.ProjectID, project)
		}
		var p map[string]any
		if err := json.Unmarshal(it.PendingPayload, &p); err != nil {
			t.Errorf("payload not json: %v body=%s", err, it.PendingPayload)
		}
		if p["deliverable_id"] != d {
			t.Errorf("payload.deliverable_id=%v want=%s", p["deliverable_id"], d)
		}
		annIDs, _ := p["annotation_ids"].([]any)
		if len(annIDs) != 2 {
			t.Errorf("payload.annotation_ids len=%d want=2", len(annIDs))
		}
	}
	if !found {
		t.Errorf("attention item %s not in feed", out.AttentionItemID)
	}
}

func TestDeliverable_SendBackFromInReviewIsIdempotent(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)
	d, _, _ := createDeliverableForSendBack(t, s, tok, team, project)

	// Manually transition to in-review first.
	rr1 := authedJSON(t, s, http.MethodPatch, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d,
		map[string]any{"ratification_state": "in-review"})
	if rr1.Code != http.StatusOK {
		t.Fatalf("patch to in-review: %d %s", rr1.Code, rr1.Body.String())
	}

	rr2 := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d+"/send-back",
		map[string]any{"note": "still off"})
	if rr2.Code != http.StatusOK {
		t.Fatalf("send-back from in-review: %d %s", rr2.Code, rr2.Body.String())
	}
	var out sendBackDeliverableOut
	_ = json.Unmarshal(rr2.Body.Bytes(), &out)
	if out.Deliverable.RatificationState != "in-review" {
		t.Errorf("expected stays in-review; got %q", out.Deliverable.RatificationState)
	}
}

func TestDeliverable_SendBackFromRatifiedReturns409(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)
	d, _, _ := createDeliverableForSendBack(t, s, tok, team, project)

	if r := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d+"/ratify",
		nil); r.Code != http.StatusOK {
		t.Fatalf("ratify: %d %s", r.Code, r.Body.String())
	}

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d+"/send-back",
		map[string]any{"note": "actually no"})
	if rr.Code != http.StatusConflict {
		t.Errorf("send-back from ratified: %d want 409", rr.Code)
	}
}

func TestDeliverable_SendBackForeignAnnotationReturns422(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)

	// Two deliverables on different docs; pass d2's annotation to d1's
	// send-back. Should reject 422.
	d1, _, _ := createDeliverableForSendBack(t, s, tok, team, project)
	_, _, foreignIDs := createDeliverableForSendBack(t, s, tok, team, project)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d1+"/send-back",
		map[string]any{
			"note":           "with foreign annotations",
			"annotation_ids": foreignIDs,
		})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("foreign annotations: %d want 422", rr.Code)
	}
}

func TestDeliverable_SendBackRequiresNote(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)
	d, _, _ := createDeliverableForSendBack(t, s, tok, team, project)

	rr := authedJSON(t, s, http.MethodPost, tok,
		"/v1/teams/"+team+"/projects/"+project+"/deliverables/"+d+"/send-back",
		map[string]any{"note": "   "})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty note: %d want 400", rr.Code)
	}
}
