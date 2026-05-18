package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestGetTaskByID_TeamScoped covers the ADR-033 W5 team-scoped
// task-by-id endpoint: it resolves a task by ULID alone, returns the
// full field union, and does not leak a task across team boundaries.
func TestGetTaskByID_TeamScoped(t *testing.T) {
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

	now := NowUTC()
	for _, team := range []string{"team-a", "team-b"} {
		if _, err := s.db.Exec(
			`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
			team, team, now); err != nil {
			t.Fatalf("seed team %s: %v", team, err)
		}
	}
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES ('proj-a', 'team-a', 'a', ?, 'goal', 0)`, now); err != nil {
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

	rr := do("POST", "/v1/teams/team-a/projects/proj-a/tasks", map[string]any{
		"project_id": "proj-a",
		"title":      "review the memo",
		"priority":   "high",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rr.Code, rr.Body.String())
	}
	var created taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}

	// Resolve by id alone, team-scoped — the ADR-033 W5 path.
	rr = do("GET", "/v1/teams/team-a/tasks/"+created.ID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task by id: %d %s", rr.Code, rr.Body.String())
	}
	var got taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if got.ID != created.ID || got.Title != "review the memo" || got.Priority != "high" {
		t.Errorf("got %+v, want id=%s title/priority preserved", got, created.ID)
	}

	// Cross-team isolation: team-b must not see team-a's task.
	if rr := do("GET", "/v1/teams/team-b/tasks/"+created.ID, nil); rr.Code != http.StatusNotFound {
		t.Errorf("cross-team get: %d, want 404", rr.Code)
	}
	// Unknown id is a 404.
	if rr := do("GET", "/v1/teams/team-a/tasks/nonesuch", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown-id get: %d, want 404", rr.Code)
	}
}
