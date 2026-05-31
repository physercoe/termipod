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
	if _, err := Init(dir, dbPath); err != nil {
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

	// One owner token per team — the ADR-037 D1 gate binds each to its
	// own team, so the cross-team probe below uses team-b's token to
	// reach the team-b path and still exercise the data-layer 404.
	tokenA := mintTeamToken(t, s, "owner", "team-a")
	tokenB := mintTeamToken(t, s, "owner", "team-b")

	do := func(token, method, url string, body any) *httptest.ResponseRecorder {
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

	rr := do(tokenA, "POST", "/v1/teams/team-a/projects/proj-a/tasks", map[string]any{
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
	rr = do(tokenA, "GET", "/v1/teams/team-a/tasks/"+created.ID, nil)
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

	// Cross-team isolation, data layer: team-b's own token addressing
	// team-b must not see team-a's task → 404 (the by-id team join).
	if rr := do(tokenB, "GET", "/v1/teams/team-b/tasks/"+created.ID, nil); rr.Code != http.StatusNotFound {
		t.Errorf("cross-team get (team-b token): %d, want 404", rr.Code)
	}
	// Cross-team isolation, auth layer: team-a's token cannot even
	// address the team-b path → 403 at the ADR-037 D1 gate.
	if rr := do(tokenA, "GET", "/v1/teams/team-b/tasks/"+created.ID, nil); rr.Code != http.StatusForbidden {
		t.Errorf("cross-team get (team-a token): %d, want 403", rr.Code)
	}
	// Unknown id is a 404.
	if rr := do(tokenA, "GET", "/v1/teams/team-a/tasks/nonesuch", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown-id get: %d, want 404", rr.Code)
	}
}
