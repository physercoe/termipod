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
