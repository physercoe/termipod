package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestListProjects_IsTemplateFilter(t *testing.T) {
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

	// Isolate this test from built-in seeds in the default team by running in
	// its own team — Init() seeds project-templates into `default`.
	const testTeam = "projfilter-test"
	now := NowUTC()
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		testTeam, testTeam, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES (?, ?, ?, ?, 'goal', 0)`,
		"proj-concrete", testTeam, "concrete", now); err != nil {
		t.Fatalf("seed concrete: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES (?, ?, ?, ?, 'goal', 1)`,
		"proj-template", testTeam, "ablation-sweep", now); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	cases := []struct {
		query    string
		wantIDs  []string
		wantCode int
	}{
		{"", []string{"proj-concrete", "proj-template"}, http.StatusOK},
		{"?is_template=true", []string{"proj-template"}, http.StatusOK},
		{"?is_template=1", []string{"proj-template"}, http.StatusOK},
		{"?is_template=false", []string{"proj-concrete"}, http.StatusOK},
		{"?is_template=0", []string{"proj-concrete"}, http.StatusOK},
		{"?is_template=nope", nil, http.StatusBadRequest},
	}

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/teams/"+testTeam+"/projects"+c.query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		s.router.ServeHTTP(rr, req)
		if rr.Code != c.wantCode {
			t.Errorf("query=%q: status=%d want=%d body=%s",
				c.query, rr.Code, c.wantCode, rr.Body.String())
			continue
		}
		if c.wantCode != http.StatusOK {
			continue
		}
		var out []projectOut
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Errorf("query=%q: decode: %v", c.query, err)
			continue
		}
		got := make(map[string]bool, len(out))
		for _, p := range out {
			got[p.ID] = true
		}
		for _, want := range c.wantIDs {
			if !got[want] {
				t.Errorf("query=%q: missing id %s in result", c.query, want)
			}
		}
		if len(out) != len(c.wantIDs) {
			t.Errorf("query=%q: got %d rows want %d", c.query, len(out), len(c.wantIDs))
		}
	}
}

// Max sub-project depth is 2 (Blueprint §6.1 / IA §6.2 W5). Creating a child
// off a top-level project works; creating a grandchild (parent already has a
// parent) must 400 with a readable message. Covers both wire paths since MCP
// projects.create delegates to the same handler.
func TestCreateProject_SubProjectDepthCap(t *testing.T) {
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

	const team = "depth-test"
	now := NowUTC()
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	// Seed a top-level parent.
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES (?, ?, ?, ?, 'goal', 0)`,
		"parent", team, "parent", now); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			"/v1/teams/"+team+"/projects", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.router.ServeHTTP(rr, req)
		return rr
	}

	// Depth-1 child off the top-level parent: must succeed.
	rr := post(`{"name":"child","kind":"goal","parent_project_id":"parent"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create depth-1 child: status=%d body=%s",
			rr.Code, rr.Body.String())
	}
	var child projectOut
	if err := json.Unmarshal(rr.Body.Bytes(), &child); err != nil {
		t.Fatalf("decode child: %v", err)
	}
	if child.ParentProjectID != "parent" {
		t.Fatalf("child parent_project_id=%q want parent", child.ParentProjectID)
	}

	// Depth-2 grandchild: must 400 with the depth message.
	rr = post(`{"name":"grandchild","kind":"goal","parent_project_id":"` +
		child.ID + `"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("create depth-2 grandchild: status=%d want 400 body=%s",
			rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "max sub-project depth") {
		t.Fatalf("depth error body missing canonical text: %s", rr.Body.String())
	}

	// Unknown parent id: 400.
	rr = post(`{"name":"orphan","kind":"goal","parent_project_id":"does-not-exist"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("create with unknown parent: status=%d want 400 body=%s",
			rr.Code, rr.Body.String())
	}
}

// TestProjectUpdate_AuditMetaCapturesNewValues asserts the project.update
// audit row carries the new values for identifying scalar fields, not just
// the list of changed columns. This is what makes the activity page able
// to answer "what was it set to?" instead of only "which column changed?".
func TestProjectUpdate_AuditMetaCapturesNewValues(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Seed a project.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/projects",
		map[string]any{"name": "p", "kind": "goal"})
	if status != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", status, body)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	// PATCH with a mix of scalar fields the enrichment cares about.
	patch := map[string]any{
		"goal":                  "ship the demo by Friday",
		"steward_agent_id":      "01KRP01BY6G6G6Y9T0QQG615AQ",
		"on_create_template_id": "research.v1",
		"budget_cents":          15000,
	}
	status, body = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/projects/"+proj.ID, patch)
	if status != http.StatusOK {
		t.Fatalf("patch project: status=%d body=%s", status, body)
	}

	// Pull the project.update audit row and verify meta carries new values.
	var metaJSON string
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT meta_json FROM audit_events
		 WHERE team_id = ? AND action = 'project.update' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`,
		defaultTeamID, proj.ID).Scan(&metaJSON); err != nil {
		t.Fatalf("query audit row: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	// fields list still present for back-compat.
	if _, ok := meta["fields"].([]any); !ok {
		t.Errorf("meta.fields missing or wrong type: %v", meta["fields"])
	}
	// New values present.
	if got := meta["goal"]; got != "ship the demo by Friday" {
		t.Errorf("meta.goal = %v; want %q", got, "ship the demo by Friday")
	}
	if got := meta["steward_agent_id"]; got != "01KRP01BY6G6G6Y9T0QQG615AQ" {
		t.Errorf("meta.steward_agent_id = %v; want the ULID", got)
	}
	if got := meta["on_create_template_id"]; got != "research.v1" {
		t.Errorf("meta.on_create_template_id = %v; want research.v1", got)
	}
	if got, _ := meta["budget_cents"].(float64); got != 15000 {
		t.Errorf("meta.budget_cents = %v; want 15000", meta["budget_cents"])
	}
}

// TestProjectUpdate_AuditMetaTruncatesLongGoal asserts free-text fields
// are clipped before landing in audit meta so a 10kB goal doesn't bloat
// the activity timeline.
func TestProjectUpdate_AuditMetaTruncatesLongGoal(t *testing.T) {
	s, token := newA2ATestServer(t)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/projects",
		map[string]any{"name": "p", "kind": "goal"})
	if status != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", status, body)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	longGoal := strings.Repeat("x", 300)
	status, _ = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/projects/"+proj.ID,
		map[string]any{"goal": longGoal})
	if status != http.StatusOK {
		t.Fatalf("patch project: status=%d", status)
	}
	var metaJSON string
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT meta_json FROM audit_events
		 WHERE team_id = ? AND action = 'project.update' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`,
		defaultTeamID, proj.ID).Scan(&metaJSON); err != nil {
		t.Fatalf("query audit row: %v", err)
	}
	var meta map[string]any
	_ = json.Unmarshal([]byte(metaJSON), &meta)
	got, _ := meta["goal"].(string)
	if len(got) > 200 || !strings.HasSuffix(got, "…") {
		t.Errorf("meta.goal=%q (len=%d); expected truncated with trailing ellipsis", got, len(got))
	}
}
