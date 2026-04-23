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

func seedTestProject(t *testing.T, s *Server, team, id string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO projects (id, team_id, name, status, created_at)
		VALUES (?, ?, 'p', 'active', ?)`,
		id, team, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

// TestAuditCoverage_RunDocumentReview exercises the mutating endpoints that
// drive the research-demo activity timeline and asserts each one emits an
// audit row. Catches regressions where a handler silently skips recordAudit.
func TestAuditCoverage_RunDocumentReview(t *testing.T) {
	s, token := newA2ATestServer(t)
	projID := NewID()
	seedTestProject(t, s, defaultTeamID, projID)

	// run.create
	status, body := doReq(t, s, token, http.MethodPost,
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
		"run.create", "run.complete",
		"document.create",
		"review.request", "review.decide",
	} {
		if counts[a] < 1 {
			t.Errorf("expected audit action %s, got counts=%v", a, counts)
		}
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
