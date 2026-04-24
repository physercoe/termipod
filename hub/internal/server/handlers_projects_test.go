package server

import (
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
