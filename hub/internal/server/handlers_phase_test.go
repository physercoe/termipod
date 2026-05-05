package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writePhaseTemplate drops a templates/projects/<name>.yaml under
// dataRoot/team/templates/projects so loadProjectTemplates() picks it
// up alongside the embedded ones. The yaml is hand-rolled so the test
// stays free of yaml-encoder dependencies.
func writePhaseTemplate(t *testing.T, dataRoot, name string, phases []string) {
	t.Helper()
	dir := filepath.Join(dataRoot, "team", "templates", "projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "name: " + name + "\nkind: goal\n"
	if len(phases) > 0 {
		body += "phases:\n"
		for _, p := range phases {
			body += "  - " + p + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
}

// phaseTestSetup spins up an isolated hub backed by a tempdir, registers
// a phase-declaring template, and creates a non-template project that
// uses it. Returns the server, owner token, team name, and project ID.
func phaseTestSetup(t *testing.T, phases []string) (s *Server, token, team, project string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	writePhaseTemplate(t, dir, "phased-test-template", phases)

	tok, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	const testTeam = "phase-test"
	now := NowUTC()
	if _, err := srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		testTeam, testTeam, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"name":        "phased-project",
		"kind":        "goal",
		"template_id": "phased-test-template",
	})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+testTeam+"/projects", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rr.Code, rr.Body.String())
	}
	var p projectOut
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if len(phases) > 0 && p.Phase != phases[0] {
		t.Fatalf("expected initial phase=%q got=%q", phases[0], p.Phase)
	}
	return srv, tok, testTeam, p.ID
}

func TestPhase_GetReportsCurrentPhaseAndTemplateOrder(t *testing.T) {
	phases := []string{"idea", "lit-review", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/teams/"+team+"/projects/"+project+"/phase", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET phase: %d %s", rr.Code, rr.Body.String())
	}
	var got phaseOut
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Phase != "idea" {
		t.Errorf("phase=%q want=idea", got.Phase)
	}
	if len(got.Phases) != 3 || got.Phases[1] != "lit-review" {
		t.Errorf("phases=%v want=[idea lit-review method]", got.Phases)
	}
	if len(got.History) != 1 || got.History[0].To != "idea" {
		t.Errorf("history=%v want one transition to idea", got.History)
	}
}

func TestPhase_AdvanceWalksTemplateOrder(t *testing.T) {
	phases := []string{"idea", "lit-review", "method"}
	s, tok, team, project := phaseTestSetup(t, phases)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects/"+project+"/phase/advance", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("advance: %d %s", rr.Code, rr.Body.String())
	}
	var got phaseOut
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Phase != "lit-review" {
		t.Errorf("phase=%q want=lit-review", got.Phase)
	}

	// Verify audit row.
	rows, err := s.listAuditEvents(req.Context(), team, "project.phase_advanced", "", "", 10)
	if err != nil {
		t.Fatalf("listAuditEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 phase_advanced audit row, got %d", len(rows))
	}
	if rows[0].TargetID != project {
		t.Errorf("audit target_id=%q want=%q", rows[0].TargetID, project)
	}
}

func TestPhase_AdvanceBlockedWhenRequiredCriteriaPending(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)

	// Inject a required+pending criterion for the current phase.
	if _, err := s.db.Exec(`
		INSERT INTO acceptance_criteria
			(id, project_id, phase, kind, body, state, required, ord)
		VALUES (?, ?, 'idea', 'text', '{"text":"pending"}', 'pending', 1, 0)`,
		NewID(), project); err != nil {
		t.Fatalf("seed criterion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects/"+project+"/phase/advance", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Result().Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type=%q want application/problem+json", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if body["code"] != "phase-criteria-pending" {
		t.Errorf("code=%v want=phase-criteria-pending", body["code"])
	}
	if body["pending_count"].(float64) != 1 {
		t.Errorf("pending_count=%v want=1", body["pending_count"])
	}
}

func TestPhase_AdvanceUnblocksWhenCriterionMet(t *testing.T) {
	phases := []string{"idea", "lit-review"}
	s, tok, team, project := phaseTestSetup(t, phases)

	if _, err := s.db.Exec(`
		INSERT INTO acceptance_criteria
			(id, project_id, phase, kind, body, state, required, ord)
		VALUES (?, ?, 'idea', 'text', '{"text":"x"}', 'met', 1, 0)`,
		NewID(), project); err != nil {
		t.Fatalf("seed criterion: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects/"+project+"/phase/advance", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPhase_AdvanceFromLastReturnsConflict(t *testing.T) {
	phases := []string{"only"}
	s, tok, team, project := phaseTestSetup(t, phases)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects/"+project+"/phase/advance", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPhase_LegacyProjectReturnsEmptyPhase(t *testing.T) {
	s, tok, team, _ := phaseTestSetup(t, []string{"a", "b"})

	// Create a second project with no template — legacy behavior.
	body, _ := json.Marshal(map[string]any{"name": "legacy", "kind": "goal"})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create legacy: %d %s", rr.Code, rr.Body.String())
	}
	var p projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Phase != "" {
		t.Errorf("legacy phase=%q want empty", p.Phase)
	}

	// GET phase returns empty phase, no template phases.
	req = httptest.NewRequest(http.MethodGet,
		"/v1/teams/"+team+"/projects/"+p.ID+"/phase", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET legacy phase: %d", rr.Code)
	}
	var got phaseOut
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Phase != "" || len(got.Phases) != 0 {
		t.Errorf("legacy phase response = %+v want zero", got)
	}

	// Advance fails — no phase set declared.
	req = httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/phase/advance", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("legacy advance: status=%d want=422", rr.Code)
	}
}

func TestPhase_SetWritesPhaseAndAuditsRevert(t *testing.T) {
	phases := []string{"a", "b", "c"}
	s, tok, team, project := phaseTestSetup(t, phases)

	// Walk forward to "c" via two advances.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost,
			"/v1/teams/"+team+"/projects/"+project+"/phase/advance", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		s.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("advance %d: %d %s", i, rr.Code, rr.Body.String())
		}
	}

	// Revert via POST /phase to "a" — should emit project.phase_reverted.
	body, _ := json.Marshal(map[string]any{"phase": "a"})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+team+"/projects/"+project+"/phase",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revert: %d %s", rr.Code, rr.Body.String())
	}
	rows, err := s.listAuditEvents(req.Context(), team, "project.phase_reverted", "", "", 10)
	if err != nil {
		t.Fatalf("listAuditEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 phase_reverted audit row, got %d", len(rows))
	}
}

func TestPhase_GetAndAdvanceRequireAuth(t *testing.T) {
	s, _, team, project := phaseTestSetup(t, []string{"a", "b"})

	for _, path := range []string{
		"/v1/teams/" + team + "/projects/" + project + "/phase",
		"/v1/teams/" + team + "/projects/" + project + "/phase/advance",
	} {
		method := http.MethodGet
		if path[len(path)-1] == 'e' && path[len(path)-7:] == "advance" {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, path, nil)
		rr := httptest.NewRecorder()
		s.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status=%d want=401", method, path, rr.Code)
		}
	}
}
