package server

import (
	"bytes"
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

func TestResearchTemplate_LegacyTemplateUnaffected(t *testing.T) {
	// Belt-and-braces — make sure the existing write-memo template's
	// behaviour didn't shift when we extended the loader's data shape.
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

	body, _ := json.Marshal(map[string]any{
		"name": "memo-test", "kind": "goal", "template_id": "write-memo",
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
		t.Errorf("write-memo project phase=%q want empty (no phases declared)",
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
