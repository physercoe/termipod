package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// countAuditActions returns a map of action → count of audit rows with that
// action in the default team. Tests assert on these counts rather than row
// shape — shape is already covered by listAuditEvents tests.
func countAuditActions(t *testing.T, s *Server) map[string]int {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT action FROM audit_events WHERE team_id = ?`, defaultTeamID)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[a]++
	}
	return out
}

// TestAuditCoverage_RunDocumentReview exercises the mutating endpoints that
// drive the research-demo activity timeline and asserts each one emits an
// audit row. Catches regressions where a handler silently skips recordAudit.
func TestAuditCoverage_RunDocumentReview(t *testing.T) {
	s, token := newA2ATestServer(t)

	// project.create via HTTP so the row ID matches the audit target_id.
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
	projID := proj.ID

	// project.update
	status, body = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/projects/"+projID,
		map[string]any{"goal": "win"})
	if status != http.StatusOK {
		t.Fatalf("patch project: status=%d body=%s", status, body)
	}

	// channel.create (project-scope)
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/projects/"+projID+"/channels",
		map[string]any{"name": "room"})
	if status != http.StatusCreated {
		t.Fatalf("create channel: status=%d body=%s", status, body)
	}

	// channel.create (team-scope)
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/channels",
		map[string]any{"name": "hub-meta"})
	if status != http.StatusCreated {
		t.Fatalf("create team channel: status=%d body=%s", status, body)
	}

	// run.create
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/runs",
		map[string]any{"project_id": projID})
	if status != http.StatusCreated {
		t.Fatalf("create run: status=%d body=%s", status, body)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}

	// run.complete
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/runs/"+run.ID+"/complete",
		map[string]any{"status": "completed"})
	if status != http.StatusNoContent {
		t.Fatalf("complete run: status=%d body=%s", status, body)
	}

	// document.create
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents",
		map[string]any{
			"project_id":     projID,
			"kind":           "memo",
			"title":          "t",
			"content_inline": "hello",
		})
	if status != http.StatusCreated {
		t.Fatalf("create doc: status=%d body=%s", status, body)
	}
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode doc: %v", err)
	}

	// review.request
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/reviews",
		map[string]any{
			"project_id":  projID,
			"target_kind": "document",
			"target_id":   doc.ID,
		})
	if status != http.StatusCreated {
		t.Fatalf("create review: status=%d body=%s", status, body)
	}
	var rv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rv); err != nil {
		t.Fatalf("decode review: %v", err)
	}

	// review.decide
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/reviews/"+rv.ID+"/decide",
		map[string]any{"state": "approved", "user_id": "steward"})
	if status != http.StatusOK {
		t.Fatalf("decide review: status=%d body=%s", status, body)
	}

	counts := countAuditActions(t, s)
	for _, a := range []string{
		"project.create", "project.update",
		"channel.create",
		"run.create", "run.complete",
		"document.create",
		"review.request", "review.decide",
	} {
		if counts[a] < 1 {
			t.Errorf("expected audit action %s, got counts=%v", a, counts)
		}
	}
	// Two channel.create calls (project + team scope) should both record.
	if counts["channel.create"] < 2 {
		t.Errorf("expected >=2 channel.create rows, got counts=%v", counts)
	}
}

// TestMCPGetAudit_ReturnsTeamScopedRows verifies the MCP get_audit tool
// returns the same rows as the HTTP handler for the current team, with
// filter + limit applied.
func TestMCPGetAudit_ReturnsTeamScopedRows(t *testing.T) {
	s, _ := newA2ATestServer(t)
	// Seed two audit rows directly: one for our team, one for a different team.
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO teams (id, name, created_at) VALUES ('other', 'other', ?)`,
		NowUTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	for _, row := range []struct{ team, action string }{
		{defaultTeamID, "run.create"},
		{defaultTeamID, "document.create"},
		{"other", "run.create"},
	} {
		if _, err := s.db.ExecContext(context.Background(), `
			INSERT INTO audit_events (
				id, team_id, ts, actor_kind, action, summary, meta_json
			) VALUES (?, ?, ?, 'system', ?, 's', '{}')`,
			NewID(), row.team, NowUTC(), row.action); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
	}

	// No filter — returns our team's rows only.
	res, jErr := s.mcpGetAudit(context.Background(), defaultTeamID, json.RawMessage(`{}`))
	if jErr != nil {
		t.Fatalf("get_audit: %+v", jErr)
	}
	text := mcpResultJSONText(t, res)
	if !containsAllStrings(text, "run.create", "document.create") {
		t.Errorf("expected both actions in result; got %s", text)
	}

	// action=run.create — filters down.
	res, jErr = s.mcpGetAudit(context.Background(), defaultTeamID,
		json.RawMessage(`{"action":"run.create"}`))
	if jErr != nil {
		t.Fatalf("get_audit filtered: %+v", jErr)
	}
	text = mcpResultJSONText(t, res)
	if !containsAllStrings(text, "run.create") || containsAllStrings(text, "document.create") {
		t.Errorf("expected only run.create; got %s", text)
	}

	// Empty team scope — refuses.
	if _, jErr := s.mcpGetAudit(context.Background(), "", json.RawMessage(`{}`)); jErr == nil {
		t.Errorf("expected error for empty team scope")
	}
}

// TestListAudit_FiltersByProjectID covers W2's project-scoped Activity
// feed. The project_id query filter must include both rows whose target
// is the project itself (target_kind='project') AND rows whose meta_json
// carries that project_id (covers agent.spawn / run.create / etc.).
// Rows from other projects must be excluded.
func TestListAudit_FiltersByProjectID(t *testing.T) {
	s, _ := newA2ATestServer(t)
	ctx := context.Background()
	mustExec := func(action, targetKind, targetID, meta string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO audit_events (
				id, team_id, ts, actor_kind, action,
				target_kind, target_id, summary, meta_json
			) VALUES (?, ?, ?, 'system', ?, ?, ?, 's', ?)`,
			NewID(), defaultTeamID, NowUTC(), action,
			nullIfEmpty(targetKind), nullIfEmpty(targetID), meta); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
	}
	mustExec("project.phase_advanced", "project", "p1", "{}")
	mustExec("project.phase_advanced", "project", "p2", "{}")
	mustExec("agent.spawn", "agent", "a1", `{"project_id":"p1","kind":"steward"}`)
	mustExec("run.create", "run", "r1", `{"project_id":"p2"}`)
	mustExec("document.create", "document", "d1", `{"project_id":"p1"}`)
	// Row with neither target=project nor project_id meta — must NOT match.
	mustExec("template.created", "template", "t1", "{}")

	rows, err := s.listAuditEvents(ctx, defaultTeamID, "", "", "p1", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows for p1, got %d: %+v", len(rows), rows)
	}
	actions := map[string]bool{}
	for _, r := range rows {
		actions[r.Action] = true
		// p2 rows must not appear.
		if r.TargetID == "p2" || r.TargetID == "r1" {
			t.Errorf("unexpected row from p2: %+v", r)
		}
	}
	if !actions["project.phase_advanced"] || !actions["agent.spawn"] || !actions["document.create"] {
		t.Errorf("missing expected action; got %+v", actions)
	}
}

// mcpResultJSONText pulls the text content out of an mcpResultJSON wrapper.
func mcpResultJSONText(t *testing.T, res any) string {
	t.Helper()
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", res)
	}
	content, ok := m["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content: %+v", m)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] not a map: %T", content[0])
	}
	s, _ := first["text"].(string)
	return s
}

func containsAllStrings(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}
