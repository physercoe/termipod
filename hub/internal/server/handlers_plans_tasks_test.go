package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestPlanStep_HumanDecisionMaterializesTask covers the W2 contract: a
// plan step created with kind=human_decision (a blueprint human_gated
// phase) spawns a task row linked via plan_step_id, visible at the
// project's tasks list with source="plan".
func TestPlanStep_HumanDecisionMaterializesTask(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	token, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const team = "w2-plan-tasks"
	now := NowUTC()
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	const projectID = "proj-w2"
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES (?, ?, 'w2-test', ?, 'goal', 0)`,
		projectID, team, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	do := func(method, url string, body any) *httptest.ResponseRecorder {
		var r *http.Request
		if body != nil {
			buf, _ := json.Marshal(body)
			r = httptest.NewRequest(method, url, bytes.NewReader(buf))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, url, nil)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		s.router.ServeHTTP(rr, r)
		return rr
	}

	// Create a plan, then a human_decision step under it.
	rr := do("POST", "/v1/teams/"+team+"/plans", map[string]any{
		"project_id": projectID,
		"spec_json":  map[string]any{"phases": []any{map[string]any{"name": "review"}}},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create plan: %d %s", rr.Code, rr.Body.String())
	}
	var plan planOut
	if err := json.Unmarshal(rr.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}

	rr = do("POST", "/v1/teams/"+team+"/plans/"+plan.ID+"/steps", map[string]any{
		"phase_idx": 0,
		"step_idx":  0,
		"kind":      "human_decision",
		"spec_json": map[string]any{"prompt": "Approve staging deploy"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create plan step: %d %s", rr.Code, rr.Body.String())
	}
	var step planStepOut
	if err := json.Unmarshal(rr.Body.Bytes(), &step); err != nil {
		t.Fatalf("decode step: %v", err)
	}

	// The task list for this project must now contain a single row linked
	// to the plan step via plan_step_id, with source="plan" and the spec
	// prompt lifted as the title.
	rr = do("GET", "/v1/teams/"+team+"/projects/"+projectID+"/tasks", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list tasks: %d %s", rr.Code, rr.Body.String())
	}
	var tasks []taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1: %s", len(tasks), rr.Body.String())
	}
	got := tasks[0]
	if got.PlanStepID != step.ID {
		t.Errorf("plan_step_id = %q, want %q", got.PlanStepID, step.ID)
	}
	if got.Source != "plan" {
		t.Errorf("source = %q, want plan", got.Source)
	}
	if got.Status != "todo" {
		t.Errorf("status = %q, want todo", got.Status)
	}
	if got.Title != "Approve staging deploy" {
		t.Errorf("title = %q, want \"Approve staging deploy\"", got.Title)
	}

	// Advancing the plan step to completed must carry the task row to
	// done so the Kanban mirrors the executor's source of truth.
	rr = do("PATCH", "/v1/teams/"+team+"/plans/"+plan.ID+"/steps/"+step.ID, map[string]any{
		"status": "completed",
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("patch step: %d %s", rr.Code, rr.Body.String())
	}
	rr = do("GET", "/v1/teams/"+team+"/projects/"+projectID+"/tasks/"+got.ID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task: %d %s", rr.Code, rr.Body.String())
	}
	var after taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if after.Status != "done" {
		t.Errorf("post-complete task status = %q, want done", after.Status)
	}

	// A deterministic step (shell) must NOT create a task — that is the
	// policy default, deliberate to keep the board from drowning in
	// executor noise.
	rr = do("POST", "/v1/teams/"+team+"/plans/"+plan.ID+"/steps", map[string]any{
		"phase_idx": 1,
		"step_idx":  0,
		"kind":      "shell",
		"spec_json": map[string]any{"cmd": "echo hi"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create shell step: %d %s", rr.Code, rr.Body.String())
	}
	rr = do("GET", "/v1/teams/"+team+"/projects/"+projectID+"/tasks", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list tasks post-shell: %d %s", rr.Code, rr.Body.String())
	}
	tasks = tasks[:0]
	if err := json.Unmarshal(rr.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("task count after shell step = %d, want 1", len(tasks))
	}

	// Ad-hoc POST /tasks still works and is reported as source="ad_hoc".
	rr = do("POST", "/v1/teams/"+team+"/projects/"+projectID+"/tasks", map[string]any{
		"title": "manual task",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create ad-hoc task: %d %s", rr.Code, rr.Body.String())
	}
	var ad taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &ad); err != nil {
		t.Fatalf("decode ad-hoc: %v", err)
	}
	if ad.Source != "ad_hoc" {
		t.Errorf("ad-hoc source = %q, want ad_hoc", ad.Source)
	}
	if ad.PlanStepID != "" {
		t.Errorf("ad-hoc plan_step_id = %q, want empty", ad.PlanStepID)
	}
}
