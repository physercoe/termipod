package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// WS1 — materialize a project from its own config_yaml (ADR-046) with
// early-bind + completion gating (ADR-044 amendment 2026-06-08). These tests
// drive an inline-spec project (no template_id) end-to-end through the REST
// surface using the shared harness in project_template_root_causes_test.go.

// inlineSpecYAML is a two-phase project spec carried entirely inline: each
// phase has a required deliverable; alpha additionally has a gate (bound to
// its deliverable) + a text criterion + a task; beta has a text criterion.
const inlineSpecYAML = `phases:
  - alpha
  - beta
phase_specs:
  alpha:
    deliverables:
      - id: a-rep
        kind: report
        required: true
    criteria:
      - id: a-gate
        kind: gate
        body:
          gate: deliverable.ratified
          params: {deliverable_id: a-rep}
        required: true
      - id: a-text
        kind: text
        body: {text: "do the alpha thing"}
        required: true
    tasks:
      - id: a-task
        title: "Scaffold alpha"
        ord: 0
  beta:
    deliverables:
      - id: b-rep
        kind: report
        required: true
    criteria:
      - id: b-text
        kind: text
        body: {text: "do the beta thing"}
        required: true
`

func createInlineProject(t *testing.T, srv *Server, team, tok, name string) projectOut {
	t.Helper()
	return createProject(t, srv, team, tok, map[string]any{
		"name": name, "kind": "goal", "goal": "g", "config_yaml": inlineSpecYAML,
	})
}

func listTasksPhase(t *testing.T, srv *Server, team, tok, projectID, phase string) []map[string]any {
	t.Helper()
	rr := rcDo(t, srv, tok, http.MethodGet,
		"/v1/teams/"+team+"/projects/"+projectID+"/tasks?phase="+phase, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list tasks: %d %s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return out
}

func critByKind(crits []criterionOut, kind string) (criterionOut, bool) {
	for _, c := range crits {
		if c.Kind == kind {
			return c, true
		}
	}
	return criterionOut{}, false
}

// --- early-bind: every phase materializes at create -----------------------

func TestInlineSpec_AllPhasesHydrateAtCreate(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	p := createInlineProject(t, srv, team, tok, "inline-early-bind")

	if p.Phase != "alpha" {
		t.Fatalf("phase=%q want alpha (lands on phases[0])", p.Phase)
	}
	// Both phases hydrate at create, not just the first (early-bind).
	if got := listCriteria(t, srv, team, tok, p.ID, "alpha"); len(got) != 2 {
		t.Fatalf("alpha criteria=%d want 2", len(got))
	}
	if got := listCriteria(t, srv, team, tok, p.ID, "beta"); len(got) != 1 {
		t.Fatalf("beta criteria=%d want 1 (future phase must hydrate at create)", len(got))
	}
	if got := listDeliverables(t, srv, team, tok, p.ID, "alpha"); len(got) != 1 {
		t.Fatalf("alpha deliverables=%d want 1", len(got))
	}
	if got := listDeliverables(t, srv, team, tok, p.ID, "beta"); len(got) != 1 {
		t.Fatalf("beta deliverables=%d want 1 (future phase)", len(got))
	}
	// Tasks materialize with their phase (#22).
	if got := listTasksPhase(t, srv, team, tok, p.ID, "alpha"); len(got) != 1 {
		t.Fatalf("alpha tasks=%d want 1", len(got))
	}
	if got := listTasksPhase(t, srv, team, tok, p.ID, "beta"); len(got) != 0 {
		t.Fatalf("beta tasks=%d want 0 (beta declares none)", len(got))
	}
}

// --- completion gating: ratify only in the current phase -------------------

func TestCompletionGate_CrossPhaseRatifyRejected(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	p := createInlineProject(t, srv, team, tok, "inline-gate-ratify")

	betaDelivs := listDeliverables(t, srv, team, tok, p.ID, "beta")
	if len(betaDelivs) != 1 {
		t.Fatalf("beta deliverables=%d want 1", len(betaDelivs))
	}
	// Ratifying a future-phase deliverable while in alpha is blocked.
	rr := rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/deliverables/"+betaDelivs[0].ID+"/ratify",
		map[string]any{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("cross-phase ratify=%d want 409 (completion gated)", rr.Code)
	}

	// The current-phase deliverable ratifies fine.
	alphaDelivs := listDeliverables(t, srv, team, tok, p.ID, "alpha")
	rr = rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/deliverables/"+alphaDelivs[0].ID+"/ratify",
		map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("current-phase ratify=%d want 200: %s", rr.Code, rr.Body.String())
	}
}

// --- completion gating: mark-met only in the current phase -----------------

func TestCompletionGate_CrossPhaseMarkMetRejected(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	p := createInlineProject(t, srv, team, tok, "inline-gate-markmet")

	betaText, ok := critByKind(listCriteria(t, srv, team, tok, p.ID, "beta"), "text")
	if !ok {
		t.Fatal("beta text criterion missing")
	}
	rr := rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/criteria/"+betaText.ID+"/mark-met",
		map[string]any{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("cross-phase mark-met=%d want 409 (completion gated)", rr.Code)
	}

	alphaText, ok := critByKind(listCriteria(t, srv, team, tok, p.ID, "alpha"), "text")
	if !ok {
		t.Fatal("alpha text criterion missing")
	}
	rr = rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/criteria/"+alphaText.ID+"/mark-met",
		map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("current-phase mark-met=%d want 200: %s", rr.Code, rr.Body.String())
	}
}

// --- early-bind: future-phase *definitions* stay editable ------------------

func TestEarlyBind_FuturePhaseDefinitionEditAllowed(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	p := createInlineProject(t, srv, team, tok, "inline-future-edit")

	// Adding a criterion to a future phase (a *definition* change) is allowed
	// even though the project is in alpha — the plan adapts as it advances.
	rr := rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/criteria",
		map[string]any{
			"phase": "beta", "kind": "text",
			"body": map[string]any{"text": "added later"},
		})
	if rr.Code >= 300 {
		t.Fatalf("future-phase criterion create=%d want <300 (definitions stay editable): %s",
			rr.Code, rr.Body.String())
	}
	if got := listCriteria(t, srv, team, tok, p.ID, "beta"); len(got) != 2 {
		t.Fatalf("beta criteria=%d want 2 after add", len(got))
	}
}

// --- unratify re-pends the gate it fired -----------------------------------

func TestUnratify_RepondsFiredGate(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	p := createInlineProject(t, srv, team, tok, "inline-unratify")

	alphaDelivs := listDeliverables(t, srv, team, tok, p.ID, "alpha")
	if len(alphaDelivs) != 1 {
		t.Fatalf("alpha deliverables=%d want 1", len(alphaDelivs))
	}
	delivID := alphaDelivs[0].ID

	// Ratify → the bound gate fires (met). The required text criterion stays
	// pending, so the phase does NOT advance — project remains in alpha.
	rr := rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/deliverables/"+delivID+"/ratify",
		map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("ratify=%d want 200: %s", rr.Code, rr.Body.String())
	}
	gate, ok := critByKind(listCriteria(t, srv, team, tok, p.ID, "alpha"), "gate")
	if !ok || gate.State != criterionStateMet {
		t.Fatalf("after ratify gate.state=%q want met", gate.State)
	}
	assertPhase(t, srv, team, tok, p.ID, "alpha")

	// Unratify → the gate it fired is re-pended.
	rr = rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/deliverables/"+delivID+"/unratify",
		map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("unratify=%d want 200: %s", rr.Code, rr.Body.String())
	}
	gate, _ = critByKind(listCriteria(t, srv, team, tok, p.ID, "alpha"), "gate")
	if gate.State != criterionStatePending {
		t.Fatalf("after unratify gate.state=%q want pending (re-pend)", gate.State)
	}
}

func assertPhase(t *testing.T, srv *Server, team, tok, projectID, want string) {
	t.Helper()
	rr := rcDo(t, srv, tok, http.MethodGet,
		"/v1/teams/"+team+"/projects/"+projectID+"/phase", nil)
	var out phaseOut
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Phase != want {
		t.Fatalf("phase=%q want %q", out.Phase, want)
	}
}
