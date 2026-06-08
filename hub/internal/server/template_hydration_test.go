package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// W7 — research template content + minimal idea-phase hydration.
// Verifies that creating a project against the embedded research
// template lands on phase=idea AND hydrates the scope-ratified
// criterion declared in the template's phase_specs.

func TestResearchTemplate_LoadsAndExposesPhases(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	tok, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// Confirm loadProjectTemplates picks up `research` as a registered
	// template (embedded FS walk).
	docs, err := loadProjectTemplates(dir)
	if err != nil {
		t.Fatalf("loadProjectTemplates: %v", err)
	}
	var got *projectTemplateDoc
	for i := range docs {
		if docs[i].Name == "research" {
			got = &docs[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("research template not registered; loaded=%v",
			func() []string {
				out := []string{}
				for _, d := range docs {
					out = append(out, d.Name)
				}
				return out
			}())
	}
	if len(got.Phases) != 5 {
		t.Errorf("phases=%v want 5", got.Phases)
	}
	if got.Phases[0] != "idea" || got.Phases[4] != "paper" {
		t.Errorf("phase order=%v", got.Phases)
	}

	// Sanity: token exists, hub responds.
	req := httptest.NewRequest(http.MethodGet, "/v1/_info", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("info: %d %s", rr.Code, rr.Body.String())
	}
}

func TestResearchTemplate_ProjectCreateHydratesIdeaCriterion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	tok, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	const team = "default-team"
	now := NowUTC()
	if _, err := srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	// Token scoped to `team` to pass the ADR-037 D1 gate.
	tok = mintTeamToken(t, srv, "owner", team)

	// Create project against the research template.
	body, _ := json.Marshal(map[string]any{
		"name":        "investigate-attention",
		"kind":        "goal",
		"template_id": "research",
		"goal":        "Investigate attention dropout schedules",
	})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var p projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Phase != "idea" {
		t.Fatalf("phase=%q want=idea", p.Phase)
	}

	// Hydrated criterion should now exist.
	listReq := httptest.NewRequest(http.MethodGet,
		"/v1/teams/"+team+"/projects/"+p.ID+"/criteria?phase=idea", nil)
	listReq.Header.Set("Authorization", "Bearer "+tok)
	listRR := httptest.NewRecorder()
	srv.router.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list criteria: %d %s", listRR.Code, listRR.Body.String())
	}
	var crits struct {
		Items []criterionOut `json:"items"`
	}
	_ = json.Unmarshal(listRR.Body.Bytes(), &crits)
	if len(crits.Items) != 1 {
		t.Fatalf("hydrated criteria=%d want=1; items=%v",
			len(crits.Items), crits.Items)
	}
	c := crits.Items[0]
	if c.Kind != "text" || c.State != "pending" {
		t.Errorf("hydrated criterion shape kind=%q state=%q", c.Kind, c.State)
	}
	if c.Body["text"] == nil {
		t.Errorf("hydrated body missing text: %v", c.Body)
	}
	if !c.Required {
		t.Errorf("hydrated criterion not required")
	}
}

func TestPhaselessTemplate_CreatesLifecycleDisabledProject(t *testing.T) {
	// A template that declares no `phases:` must create a lifecycle-disabled
	// project (phase stays empty), unchanged by the inline-spec loader work.
	// (Previously asserted against the write-memo stub, removed in WS3 — now
	// authored as an overlay so the case is self-contained.)
	dir := t.TempDir()
	const overlay = "name: phaseless-memo\nkind: goal\ngoal: just do it\n"
	writeOverlayTemplate(t, dir, "phaseless-memo.v1.yaml", overlay)

	dbPath := filepath.Join(dir, "hub.db")
	tok, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	const team = "default-team"
	now := NowUTC()
	_, _ = srv.db.Exec(`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now)
	// Token scoped to `team` to pass the ADR-037 D1 gate.
	tok = mintTeamToken(t, srv, "owner", team)

	body, _ := json.Marshal(map[string]any{
		"name": "memo-test", "kind": "goal", "template_id": "phaseless-memo",
	})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var p projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Phase != "" {
		t.Errorf("phaseless project phase=%q want empty (no phases declared)",
			p.Phase)
	}
}

// TestPhaseTemplateTiles_ServesYamlTilesPerPhase verifies the W5
// (lifecycle-walkthrough-followups) resolution-chain layer 2: the hub
// reads `phase_specs[<phase>].tiles` from the research template YAML
// and exposes it as a map on the project payload. Mobile reads this
// when no per-project override is set.
func TestPhaseTemplateTiles_ServesYamlTilesPerPhase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	tiles := srv.phaseTemplateTiles("research")
	if tiles == nil {
		t.Fatalf("phaseTemplateTiles(research) returned nil; expected ≥ 4 phases with tiles")
	}
	if got := tiles["idea"]; len(got) == 0 {
		t.Errorf("idea phase tiles empty; want Documents at minimum (per v1.0.483)")
	} else {
		// W5 inlined "Documents" into the idea-phase tiles so the
		// director can find the steward's idea memos.
		found := false
		for _, s := range got {
			if s == "Documents" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("idea phase tiles=%v missing 'Documents'", got)
		}
	}
	// lit-review keeps References + Documents per the template spec §3.
	if got := tiles["lit-review"]; len(got) < 2 {
		t.Errorf("lit-review tiles=%v want References + Documents", got)
	}
	// Unknown template → nil (no panics, no half-loaded state).
	if got := srv.phaseTemplateTiles("does-not-exist"); got != nil {
		t.Errorf("unknown template returned %v; want nil", got)
	}
	// Empty template id → nil (defensive default).
	if got := srv.phaseTemplateTiles(""); got != nil {
		t.Errorf("empty template returned %v; want nil", got)
	}
}

// TestProjectPatch_PhaseTileOverridesRoundTrip verifies the W5
// resolution-chain layer 1: the per-project override stored on
// `projects.phase_tile_overrides_json`. PATCH writes; GET reads it
// back as `phase_tile_overrides` in the response payload.
func TestProjectPatch_PhaseTileOverridesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	tok, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	const team = "default-team"
	now := NowUTC()
	_, _ = srv.db.Exec(`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now)
	// Token scoped to `team` to pass the ADR-037 D1 gate.
	tok = mintTeamToken(t, srv, "owner", team)

	// Create a research project.
	body, _ := json.Marshal(map[string]any{
		"name": "tile-override-test", "kind": "goal", "template_id": "research",
	})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var p projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.ID == "" {
		t.Fatal("create returned empty id")
	}
	// Fresh project has no override; payload also carries the YAML
	// template tiles so the resolver has fallback data.
	if len(p.PhaseTileOverrides) != 0 {
		t.Errorf("fresh project carries override %q; want empty", p.PhaseTileOverrides)
	}
	if p.PhaseTilesTemplate == nil {
		t.Error("fresh project payload missing phase_tiles_template")
	}

	// PATCH a per-project override: just Outputs on idea.
	patch, _ := json.Marshal(map[string]any{
		"phase_tile_overrides": map[string]any{
			"idea": []string{"Outputs"},
		},
	})
	preq := httptest.NewRequest(http.MethodPatch,
		"/v1/teams/"+team+"/projects/"+p.ID, bytes.NewReader(patch))
	preq.Header.Set("Authorization", "Bearer "+tok)
	preq.Header.Set("Content-Type", "application/json")
	prr := httptest.NewRecorder()
	srv.router.ServeHTTP(prr, preq)
	if prr.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", prr.Code, prr.Body.String())
	}
	var p2 projectOut
	_ = json.Unmarshal(prr.Body.Bytes(), &p2)
	if len(p2.PhaseTileOverrides) == 0 {
		t.Fatalf("post-patch override empty; want {\"idea\":[\"Outputs\"]}")
	}
	var got map[string][]string
	if err := json.Unmarshal(p2.PhaseTileOverrides, &got); err != nil {
		t.Fatalf("unmarshal override: %v (raw=%s)", err, p2.PhaseTileOverrides)
	}
	if v := got["idea"]; len(v) != 1 || v[0] != "Outputs" {
		t.Errorf("idea override=%v; want [Outputs]", v)
	}
}

// TestProjectPatch_OverviewWidgetOverridesRoundTrip verifies the D10
// hero-override resolution-chain layer 1 (chassis-followup wave 1,
// ADR-024): per-project override stored on
// `projects.overview_widget_overrides_json`. PATCH writes; GET reads it
// back as `overview_widget_overrides`; and the resolved `overview_widget`
// field reflects the override when the project's current phase is in
// the override map.
func TestProjectPatch_OverviewWidgetOverridesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	tok, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	const team = "default-team"
	now := NowUTC()
	_, _ = srv.db.Exec(`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now)
	// Token scoped to `team` to pass the ADR-037 D1 gate.
	tok = mintTeamToken(t, srv, "owner", team)

	// Create a research project (starts in 'idea' phase).
	body, _ := json.Marshal(map[string]any{
		"name": "hero-override-test", "kind": "goal", "template_id": "research",
	})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var p projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.ID == "" {
		t.Fatal("create returned empty id")
	}
	if len(p.OverviewWidgetOverrides) != 0 {
		t.Errorf("fresh project carries override %q; want empty",
			p.OverviewWidgetOverrides)
	}
	// Fresh research project resolves to its template's idea hero
	// (idea_conversation per research.v1.yaml). Capture for the
	// override-clears-fallback assertion below.
	templateHero := p.OverviewWidget
	if templateHero == "" {
		t.Fatal("fresh research project missing resolved overview_widget")
	}

	// PATCH a per-project override pointing the idea phase at
	// `task_milestone_list` (a hero the research template wouldn't
	// pick at idea phase) to prove the override wins.
	patch, _ := json.Marshal(map[string]any{
		"overview_widget_overrides": map[string]any{
			"idea": "task_milestone_list",
		},
	})
	preq := httptest.NewRequest(http.MethodPatch,
		"/v1/teams/"+team+"/projects/"+p.ID, bytes.NewReader(patch))
	preq.Header.Set("Authorization", "Bearer "+tok)
	preq.Header.Set("Content-Type", "application/json")
	prr := httptest.NewRecorder()
	srv.router.ServeHTTP(prr, preq)
	if prr.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", prr.Code, prr.Body.String())
	}
	var p2 projectOut
	_ = json.Unmarshal(prr.Body.Bytes(), &p2)
	if len(p2.OverviewWidgetOverrides) == 0 {
		t.Fatalf("post-patch override empty; want {\"idea\":\"task_milestone_list\"}")
	}
	var got map[string]string
	if err := json.Unmarshal(p2.OverviewWidgetOverrides, &got); err != nil {
		t.Fatalf("unmarshal override: %v (raw=%s)",
			err, p2.OverviewWidgetOverrides)
	}
	if got["idea"] != "task_milestone_list" {
		t.Errorf("idea override=%q; want task_milestone_list", got["idea"])
	}
	// Resolution chain layer 1 wins: resolved overview_widget on the
	// payload must reflect the override now.
	if p2.OverviewWidget != "task_milestone_list" {
		t.Errorf("resolved overview_widget=%q after override; want task_milestone_list",
			p2.OverviewWidget)
	}

	// Unknown slug in the override map is silently rejected by the
	// resolver — falls back to the template-side value.
	patch2, _ := json.Marshal(map[string]any{
		"overview_widget_overrides": map[string]any{
			"idea": "does-not-exist",
		},
	})
	preq2 := httptest.NewRequest(http.MethodPatch,
		"/v1/teams/"+team+"/projects/"+p.ID, bytes.NewReader(patch2))
	preq2.Header.Set("Authorization", "Bearer "+tok)
	preq2.Header.Set("Content-Type", "application/json")
	prr2 := httptest.NewRecorder()
	srv.router.ServeHTTP(prr2, preq2)
	if prr2.Code != http.StatusOK {
		t.Fatalf("patch2: %d %s", prr2.Code, prr2.Body.String())
	}
	var p3 projectOut
	_ = json.Unmarshal(prr2.Body.Bytes(), &p3)
	if p3.OverviewWidget != templateHero {
		t.Errorf("unknown-slug override produced %q; want fall-through to template %q",
			p3.OverviewWidget, templateHero)
	}
}

// TestResearchTemplate_PhaseAdvanceHydratesDeliverables locks issue #20:
// a phase's template-declared deliverables are instantiated as draft rows
// when the phase is entered. The research template's `idea` phase declares
// no deliverables (so create hydrates none), and `lit-review` declares one
// (kind: lit-review) — entered via a real phase advance after the idea
// criterion is met. Also asserts hydration is idempotent.
func TestResearchTemplate_PhaseAdvanceHydratesDeliverables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	const team = "deliv-team"
	now := NowUTC()
	if _, err := srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	tok := mintTeamToken(t, srv, "owner", team)

	do := func(method, path string, payload any) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.router.ServeHTTP(rr, req)
		return rr
	}

	// Create from research → phase idea.
	rr := do(http.MethodPost, "/v1/teams/"+team+"/projects", map[string]any{
		"name": "deliv-proj", "kind": "goal", "template_id": "research",
		"goal": "x",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var p projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Phase != "idea" {
		t.Fatalf("phase=%q want idea", p.Phase)
	}

	getDeliv := func(phase string) []deliverableOut {
		r := do(http.MethodGet,
			"/v1/teams/"+team+"/projects/"+p.ID+"/deliverables?phase="+phase, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("list deliverables: %d %s", r.Code, r.Body.String())
		}
		var out struct {
			Items []deliverableOut `json:"items"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		return out.Items
	}

	// idea declares no deliverables → none hydrated at create.
	if got := getDeliv("idea"); len(got) != 0 {
		t.Fatalf("idea deliverables=%d want 0", len(got))
	}

	// Mark the idea criterion met. With AC-driven auto-advance (ADR-044
	// P3), satisfying the only required criterion in `idea` advances the
	// project to lit-review automatically — no manual advance call.
	cr := do(http.MethodGet, "/v1/teams/"+team+"/projects/"+p.ID+"/criteria?phase=idea", nil)
	var crits struct {
		Items []criterionOut `json:"items"`
	}
	_ = json.Unmarshal(cr.Body.Bytes(), &crits)
	if len(crits.Items) != 1 {
		t.Fatalf("idea criteria=%d want 1", len(crits.Items))
	}
	if mr := do(http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/criteria/"+crits.Items[0].ID+"/mark-met",
		map[string]any{}); mr.Code != http.StatusOK {
		t.Fatalf("mark-met: %d %s", mr.Code, mr.Body.String())
	}

	// The mark auto-advanced idea → lit-review.
	pr := do(http.MethodGet, "/v1/teams/"+team+"/projects/"+p.ID+"/phase", nil)
	var ph phaseOut
	_ = json.Unmarshal(pr.Body.Bytes(), &ph)
	if ph.Phase != "lit-review" {
		t.Fatalf("phase after mark-met=%q want lit-review (auto-advanced)", ph.Phase)
	}

	// lit-review's deliverable should now be hydrated.
	got := getDeliv("lit-review")
	if len(got) != 1 {
		t.Fatalf("lit-review deliverables=%d want 1; %v", len(got), got)
	}
	d := got[0]
	if d.Kind != "lit-review" || d.RatificationState != "draft" || !d.Required {
		t.Errorf("hydrated deliverable shape kind=%q state=%q required=%v",
			d.Kind, d.RatificationState, d.Required)
	}

	// Idempotent: re-hydrating the same phase must not duplicate.
	srv.hydratePhase(context.Background(), team, p.ID, "research", "lit-review")
	if got := getDeliv("lit-review"); len(got) != 1 {
		t.Fatalf("after re-hydrate lit-review deliverables=%d want 1 (idempotent)", len(got))
	}

	// #56: lit-review's criteria must be bound to that deliverable, so the
	// deliverable viewer (which filters by deliverable_id) finds them.
	// research.v1 declares two lit-review criteria, both deliverable_ref
	// lit-review-doc.
	cr2 := do(http.MethodGet,
		"/v1/teams/"+team+"/projects/"+p.ID+"/criteria?deliverable_id="+d.ID, nil)
	if cr2.Code != http.StatusOK {
		t.Fatalf("list criteria by deliverable: %d %s", cr2.Code, cr2.Body.String())
	}
	var byDeliv struct {
		Items []criterionOut `json:"items"`
	}
	_ = json.Unmarshal(cr2.Body.Bytes(), &byDeliv)
	if len(byDeliv.Items) != 2 {
		t.Fatalf("criteria bound to lit-review deliverable=%d want 2 (deliverable_id not set on hydration)",
			len(byDeliv.Items))
	}
	for _, c := range byDeliv.Items {
		if c.DeliverableID != d.ID {
			t.Errorf("criterion %s deliverable_id=%q want %q", c.ID, c.DeliverableID, d.ID)
		}
	}
}
