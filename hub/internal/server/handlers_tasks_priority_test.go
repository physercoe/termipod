package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestTasks_PriorityEnumAndSort covers the W3 contract: tasks expose a
// four-value priority enum, the list endpoint sorts by priority DESC
// then updated_at DESC by default, `?sort=updated` falls back to
// reverse-chronological, and invalid priorities are rejected with 400.
func TestTasks_PriorityEnumAndSort(t *testing.T) {
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

	const team = "w3-priority"
	now := NowUTC()
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	// Token scoped to `team` to pass the ADR-037 D1 gate (the bootstrap
	// Init token is scoped to `default`).
	token = mintTeamToken(t, s, "owner", team)
	const projectID = "proj-w3"
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES (?, ?, 'w3-test', ?, 'goal', 0)`,
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

	// Create four tasks spanning the enum plus one with no explicit
	// priority (must default to "med"). Creation order is deliberately
	// shuffled so the default sort has real work to do.
	type seed struct {
		title    string
		priority string
	}
	seeds := []seed{
		{"low-task", "low"},
		{"urgent-task", "urgent"},
		{"med-default-task", ""},
		{"high-task", "high"},
		{"med-explicit-task", "med"},
	}
	for _, sd := range seeds {
		body := map[string]any{"title": sd.title}
		if sd.priority != "" {
			body["priority"] = sd.priority
		}
		rr := do("POST", "/v1/teams/"+team+"/projects/"+projectID+"/tasks", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %q: %d %s", sd.title, rr.Code, rr.Body.String())
		}
		var out taskOut
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", sd.title, err)
		}
		want := sd.priority
		if want == "" {
			want = "med"
		}
		if out.Priority != want {
			t.Errorf("%q priority = %q, want %q", sd.title, out.Priority, want)
		}
	}

	// Default sort: urgent → high → (med × 2) → low. Within the med
	// bucket the order is updated_at DESC, which matches creation order
	// reversed.
	rr := do("GET", "/v1/teams/"+team+"/projects/"+projectID+"/tasks", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list tasks: %d %s", rr.Code, rr.Body.String())
	}
	var got []taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(got) != len(seeds) {
		t.Fatalf("task count = %d, want %d", len(got), len(seeds))
	}
	wantOrder := []string{"urgent", "high", "med", "med", "low"}
	for i, task := range got {
		if task.Priority != wantOrder[i] {
			t.Errorf("pos %d priority = %q, want %q (title=%q)",
				i, task.Priority, wantOrder[i], task.Title)
		}
	}

	// Priority filter narrows to a single row.
	rr = do("GET", "/v1/teams/"+team+"/projects/"+projectID+"/tasks?priority=urgent", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list urgent: %d %s", rr.Code, rr.Body.String())
	}
	var urgent []taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &urgent); err != nil {
		t.Fatalf("decode urgent: %v", err)
	}
	if len(urgent) != 1 || urgent[0].Title != "urgent-task" {
		t.Errorf("urgent filter = %+v, want [urgent-task]", urgent)
	}

	// sort=updated escape hatch flips to reverse-chronological. The last
	// created task ("med-explicit-task") must lead the list regardless
	// of its mid-tier priority.
	rr = do("GET", "/v1/teams/"+team+"/projects/"+projectID+"/tasks?sort=updated", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list sort=updated: %d %s", rr.Code, rr.Body.String())
	}
	var byUpdated []taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &byUpdated); err != nil {
		t.Fatalf("decode sort=updated: %v", err)
	}
	if len(byUpdated) == 0 || byUpdated[0].Title != "med-explicit-task" {
		t.Errorf("sort=updated head = %+v, want med-explicit-task first", byUpdated)
	}

	// Bad priority on create → 400.
	rr = do("POST", "/v1/teams/"+team+"/projects/"+projectID+"/tasks", map[string]any{
		"title":    "bogus",
		"priority": "critical",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad-priority create = %d, want 400 (%s)", rr.Code, rr.Body.String())
	}

	// PATCH can move an existing task between priorities, and rejects
	// nonsense values.
	target := got[4] // low-task
	rr = do("PATCH",
		"/v1/teams/"+team+"/projects/"+projectID+"/tasks/"+target.ID,
		map[string]any{"priority": "urgent"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("patch priority: %d %s", rr.Code, rr.Body.String())
	}
	rr = do("GET", "/v1/teams/"+team+"/projects/"+projectID+"/tasks/"+target.ID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get after patch: %d %s", rr.Code, rr.Body.String())
	}
	var after taskOut
	if err := json.Unmarshal(rr.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode after patch: %v", err)
	}
	if after.Priority != "urgent" {
		t.Errorf("post-patch priority = %q, want urgent", after.Priority)
	}

	rr = do("PATCH",
		"/v1/teams/"+team+"/projects/"+projectID+"/tasks/"+target.ID,
		map[string]any{"priority": "bogus"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad-priority patch = %d, want 400", rr.Code)
	}
}

// Creating a task under a non-existent project returns a clean 404 with a
// helpful message, not the raw SQLite FOREIGN KEY constraint error as a
// 500 (#55).
func TestTasks_CreateUnknownProjectIs404(t *testing.T) {
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

	const team = "fk-team"
	now := NowUTC()
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	token := mintTeamToken(t, s, "owner", team)

	body, _ := json.Marshal(map[string]any{"title": "orphan"})
	r := httptest.NewRequest("POST",
		"/v1/teams/"+team+"/projects/NONEXISTENT-ID/tasks", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, r)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("create under unknown project = %d %s; want 404",
			rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("project not found")) {
		t.Errorf("body = %q; want a 'project not found' message", rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("FOREIGN KEY")) {
		t.Errorf("raw FK constraint leaked to client: %q", rr.Body.String())
	}
}

// TestTasks_BlockReasonDoesNotClobberBody walks the exact #54 reproduction:
// a block reason is recorded in the dedicated block_reason field (not body_md),
// status-only transitions preserve body_md, and leaving the blocked state
// auto-clears the now-stale block reason. The original description survives to
// the completed task.
func TestTasks_BlockReasonDoesNotClobberBody(t *testing.T) {
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

	const team = "block-team"
	const projectID = "proj-block"
	now := NowUTC()
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		team, team, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO projects (id, team_id, name, created_at, kind, is_template)
		 VALUES (?, ?, 'block-test', ?, 'goal', 0)`,
		projectID, team, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	token := mintTeamToken(t, s, "owner", team)
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

	const origBody = "Fix login.js:42 — replace event.preventDefault() with form.requestSubmit()"
	base := "/v1/teams/" + team + "/projects/" + projectID + "/tasks"

	// 1. Create with a real description.
	rr := do("POST", base, map[string]any{"title": "fix login", "body_md": origBody})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created taskOut
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	get := func() taskOut {
		rr := do("GET", base+"/"+created.ID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("get: %d %s", rr.Code, rr.Body.String())
		}
		var out taskOut
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		return out
	}

	// 2. Block with a reason — in the dedicated field, NOT body_md.
	rr = do("PATCH", base+"/"+created.ID, map[string]any{
		"status": "blocked", "block_reason": "Blocked: need Safari 17.4 test env"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("block: %d %s", rr.Code, rr.Body.String())
	}
	if b := get(); b.BodyMD != origBody || b.BlockReason != "Blocked: need Safari 17.4 test env" || b.Status != "blocked" {
		t.Fatalf("after block: body=%q reason=%q status=%q", b.BodyMD, b.BlockReason, b.Status)
	}

	// 3. Unblock with a status-only patch → body preserved, reason auto-cleared.
	rr = do("PATCH", base+"/"+created.ID, map[string]any{"status": "in_progress"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("unblock: %d %s", rr.Code, rr.Body.String())
	}
	if b := get(); b.BodyMD != origBody || b.BlockReason != "" {
		t.Fatalf("after unblock: body=%q reason=%q (want orig body, empty reason)", b.BodyMD, b.BlockReason)
	}

	// 4. Complete → the original description is intact, not a stale block reason.
	rr = do("PATCH", base+"/"+created.ID, map[string]any{"status": "done"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("complete: %d %s", rr.Code, rr.Body.String())
	}
	if b := get(); b.BodyMD != origBody {
		t.Fatalf("after done: body=%q; want original description preserved", b.BodyMD)
	}
}
