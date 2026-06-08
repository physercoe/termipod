package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests lock the four template root-cause fixes a tester filed against
// the project-lifecycle surface (GitHub #38, #41, #21, #27/#29).

// --- #38: phaseNameList tolerates both YAML shapes ------------------------

func TestPhaseNameList_ToleratesScalarAndMappingForms(t *testing.T) {
	cases := map[string]string{
		"scalar": "phases:\n  - env-setup\n  - port\n",
		// The form that yaml.v3 used to silently drop into an empty slice.
		"mapping": "phases:\n  - name: env-setup\n  - name: port\n",
		// Mixed is tolerated too (defensive).
		"mixed": "phases:\n  - env-setup\n  - name: port\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var doc struct {
				Phases phaseNameList `yaml:"phases"`
			}
			if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(doc.Phases) != 2 || doc.Phases[0] != "env-setup" || doc.Phases[1] != "port" {
				t.Fatalf("phases=%v want [env-setup port]", doc.Phases)
			}
		})
	}
}

// --- #38: a mapping-form template hydrates instead of producing 0 rows -----

func TestProjectTemplate_MappingFormPhasesHydrate(t *testing.T) {
	srv, dir, team, tok := newProjectTemplateTestServer(t)

	// Author a template using the broken-but-now-tolerated mapping form.
	const body = "name: rc-map\nkind: migration\ngoal: go\n" +
		"phases:\n  - name: setup\n  - name: ship\n" +
		"phase_specs:\n  setup:\n    criteria:\n" +
		"      - id: ready\n        kind: text\n        body: {text: ok}\n        required: true\n"
	writeOverlayTemplate(t, dir, "rc-map.v1.yaml", body)

	p := createProject(t, srv, team, tok, map[string]any{
		"name": "map-proj", "kind": "goal", "template_id": "rc-map", "goal": "g",
	})
	if p.Phase != "setup" {
		t.Fatalf("phase=%q want setup (mapping-form phases must parse, #38)", p.Phase)
	}
	if got := listCriteria(t, srv, team, tok, p.ID, "setup"); len(got) != 1 {
		t.Fatalf("hydrated criteria=%d want 1 (no hydration means #38 regressed)", len(got))
	}
}

// --- #38: validator accepts the canonical scalar form ----------------------

func TestValidateProjectConfigYAML_AcceptsScalarPhases(t *testing.T) {
	// Scalar form (every shipped template) must validate as a template.
	if msg := validateProjectConfigYAML("phases:\n  - a\n  - b\n", true); msg != "" {
		t.Errorf("scalar phases rejected for template: %q", msg)
	}
	// Mapping form is accepted too.
	if msg := validateProjectConfigYAML("phases:\n  - name: a\n", true); msg != "" {
		t.Errorf("mapping phases rejected for template: %q", msg)
	}
	// Genuinely empty is still rejected for templates.
	if msg := validateProjectConfigYAML("kind: x\n", true); msg == "" {
		t.Error("empty phases accepted for template; want rejection")
	}
	// Non-template with no phases is fine.
	if msg := validateProjectConfigYAML("kind: x\n", false); msg != "" {
		t.Errorf("non-template config rejected: %q", msg)
	}
}

// --- #41: a DB-row ULID passed as template_id resolves to the name ---------

func TestProjectCreate_TemplateIDByULID(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)

	// The embedded `research` template is seeded with a ULID id at Init.
	var ulid string
	if err := srv.db.QueryRow(
		`SELECT id FROM projects WHERE name = 'research' AND is_template = 1`,
	).Scan(&ulid); err != nil {
		t.Fatalf("find research template id: %v", err)
	}

	// Create passing the ULID (what projects_list surfaces), not the name.
	p := createProject(t, srv, team, tok, map[string]any{
		"name": "by-ulid", "kind": "goal", "template_id": ulid,
		"goal": "investigate",
	})
	if p.Phase != "idea" {
		t.Fatalf("phase=%q want idea — ULID template_id must resolve to name (#41)", p.Phase)
	}
	// Hydration must also have run via the ULID path.
	if got := listCriteria(t, srv, team, tok, p.ID, "idea"); len(got) != 1 {
		t.Fatalf("hydrated criteria=%d want 1 (ULID hydration path, #41)", len(got))
	}
}

// --- #21 + #27 + #29: gate rewrite + body/goal substitution ---------------

func TestProjectTemplate_GateRefAndParamSubstitution(t *testing.T) {
	srv, dir, team, tok := newProjectTemplateTestServer(t)

	const body = "name: rc-sub\nkind: migration\n" +
		"goal: \"Migrate {task_name} to {target}.\"\n" +
		"phases:\n  - setup\n" +
		"phase_specs:\n  setup:\n" +
		"    deliverables:\n      - id: setup-report\n        kind: report\n        required: true\n" +
		"    criteria:\n" +
		"      - id: tool-ready\n        kind: text\n" +
		"        body: {text: \"{target} installed and importable.\"}\n        required: true\n" +
		"      - id: setup-ratified\n        kind: gate\n" +
		"        body:\n          gate: deliverable.ratified\n          params: {deliverable_id: setup-report}\n" +
		"        required: true\n"
	writeOverlayTemplate(t, dir, "rc-sub.v1.yaml", body)

	p := createProject(t, srv, team, tok, map[string]any{
		"name": "sub-proj", "kind": "goal", "template_id": "rc-sub",
		"parameters_json": map[string]any{"task_name": "G1", "target": "IsaacLab"},
	})
	if p.Phase != "setup" {
		t.Fatalf("phase=%q want setup", p.Phase)
	}

	// #29: goal placeholders resolved from the template goal + params.
	if p.Goal != "Migrate G1 to IsaacLab." {
		t.Errorf("goal=%q want %q (#29)", p.Goal, "Migrate G1 to IsaacLab.")
	}

	// The runtime deliverable got a fresh ULID.
	delivs := listDeliverables(t, srv, team, tok, p.ID, "setup")
	if len(delivs) != 1 {
		t.Fatalf("deliverables=%d want 1", len(delivs))
	}
	delivULID := delivs[0].ID
	if delivULID == "" || delivULID == "setup-report" {
		t.Fatalf("deliverable id=%q want a runtime ULID", delivULID)
	}

	crits := listCriteria(t, srv, team, tok, p.ID, "setup")
	if len(crits) != 2 {
		t.Fatalf("criteria=%d want 2", len(crits))
	}
	var sawText, sawGate bool
	for _, c := range crits {
		switch c.Kind {
		case "text":
			sawText = true
			// #27: criterion body text placeholders resolved.
			if got, _ := c.Body["text"].(string); got != "IsaacLab installed and importable." {
				t.Errorf("text body=%q want substituted (#27)", got)
			}
		case "gate":
			sawGate = true
			// #21: gate's params.deliverable_id rewritten template-id → ULID.
			params, _ := c.Body["params"].(map[string]any)
			gotRef, _ := params["deliverable_id"].(string)
			if gotRef != delivULID {
				t.Errorf("gate deliverable_id=%q want runtime ULID %q (#21)", gotRef, delivULID)
			}
		}
	}
	if !sawText || !sawGate {
		t.Fatalf("missing criteria: text=%v gate=%v", sawText, sawGate)
	}
}

// --- #21 end-to-end: ratifying the deliverable fires the rewritten gate ----

func TestProjectTemplate_RatifyFiresHydratedGate(t *testing.T) {
	srv, dir, team, tok := newProjectTemplateTestServer(t)

	const body = "name: rc-gate\nkind: migration\ngoal: go\n" +
		"phases:\n  - setup\n  - ship\n" +
		"phase_specs:\n  setup:\n" +
		"    deliverables:\n      - id: rep\n        kind: report\n        required: true\n" +
		"    criteria:\n" +
		"      - id: ratified\n        kind: gate\n" +
		"        body:\n          gate: deliverable.ratified\n          params: {deliverable_id: rep}\n" +
		"        required: true\n"
	writeOverlayTemplate(t, dir, "rc-gate.v1.yaml", body)

	p := createProject(t, srv, team, tok, map[string]any{
		"name": "gate-proj", "kind": "goal", "template_id": "rc-gate", "goal": "g",
	})
	d := listDeliverables(t, srv, team, tok, p.ID, "setup")
	if len(d) != 1 {
		t.Fatalf("deliverables=%d want 1", len(d))
	}

	// Ratify the deliverable; the rewritten gate should fire and (it being
	// the only required criterion) auto-advance the phase to ship (#21).
	rr := rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/deliverables/"+d[0].ID+"/ratify",
		map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("ratify: %d %s", rr.Code, rr.Body.String())
	}

	ph := rcDo(t, srv, tok, http.MethodGet,
		"/v1/teams/"+team+"/projects/"+p.ID+"/phase", nil)
	var out phaseOut
	_ = json.Unmarshal(ph.Body.Bytes(), &out)
	if out.Phase != "ship" {
		t.Fatalf("phase after ratify=%q want ship (rewritten gate must fire, #21)", out.Phase)
	}
}

// --- #27/#29 unit: substituteTemplateParams -------------------------------

func TestSubstituteTemplateParams(t *testing.T) {
	params := map[string]any{"a": "X", "n": float64(500)}
	cases := map[string]string{
		"{a} and {n}":   "X and 500",
		"no tokens":     "no tokens",
		"{missing} {a}": "{missing} X", // unknown left verbatim
		"":              "",
	}
	for in, want := range cases {
		if got := substituteTemplateParams(in, params); got != want {
			t.Errorf("substitute(%q)=%q want %q", in, got, want)
		}
	}
	// No params → unchanged.
	if got := substituteTemplateParams("{a}", nil); got != "{a}" {
		t.Errorf("nil params changed text: %q", got)
	}
}

// --- shared test helpers ---------------------------------------------------

func newProjectTemplateTestServer(t *testing.T) (*Server, string, string, string) {
	t.Helper()
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
	const team = "rc-team"
	if _, err := srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, NowUTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	tok := mintTeamToken(t, srv, "owner", team)
	return srv, dir, team, tok
}

func writeOverlayTemplate(t *testing.T, dir, filename, body string) {
	t.Helper()
	overlay := filepath.Join(dir, "teams", defaultTeamID, "templates", "projects")
	if err := os.MkdirAll(overlay, 0o700); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	if err := os.WriteFile(filepath.Join(overlay, filename), []byte(body), 0o600); err != nil {
		t.Fatalf("write overlay template: %v", err)
	}
}

func rcDo(t *testing.T, srv *Server, tok, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
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

func createProject(t *testing.T, srv *Server, team, tok string, payload map[string]any) projectOut {
	t.Helper()
	rr := rcDo(t, srv, tok, http.MethodPost, "/v1/teams/"+team+"/projects", payload)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rr.Code, rr.Body.String())
	}
	var p projectOut
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	return p
}

func listCriteria(t *testing.T, srv *Server, team, tok, projectID, phase string) []criterionOut {
	t.Helper()
	rr := rcDo(t, srv, tok, http.MethodGet,
		"/v1/teams/"+team+"/projects/"+projectID+"/criteria?phase="+phase, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list criteria: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Items []criterionOut `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return out.Items
}

func listDeliverables(t *testing.T, srv *Server, team, tok, projectID, phase string) []deliverableOut {
	t.Helper()
	rr := rcDo(t, srv, tok, http.MethodGet,
		"/v1/teams/"+team+"/projects/"+projectID+"/deliverables?phase="+phase, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list deliverables: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Items []deliverableOut `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return out.Items
}
