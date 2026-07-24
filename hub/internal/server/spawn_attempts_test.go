package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// listSpawnsHTTP drives the real GET /agents/spawns endpoint the desktop
// attempts section calls (task-board W4) with an owner-kind team token.
func listSpawnsHTTP(t *testing.T, s *Server, query string) []spawnListOut {
	t.Helper()
	token := mintTeamToken(t, s, "owner", defaultTeamID)
	r := httptest.NewRequest("GET",
		"/v1/teams/"+defaultTeamID+"/agents/spawns"+query, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list spawns %q = %d body=%s", query, w.Code, w.Body.String())
	}
	var out []spawnListOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode spawns: %v body=%s", err, w.Body.String())
	}
	return out
}

// terminateAgentHTTP drives the real PATCH /agents/{id} lifecycle path —
// the one that stamps agents.terminated_at (handlePatchAgent) — instead
// of seeding the terminal state via SQL, which would bypass the exact
// writer this test exists to pin.
func terminateAgentHTTP(t *testing.T, s *Server, agentID string) {
	t.Helper()
	token := mintTeamToken(t, s, "owner", defaultTeamID)
	buf, _ := json.Marshal(map[string]any{"status": "terminated"})
	r := httptest.NewRequest("PATCH",
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID,
		bytes.NewReader(buf))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("terminate agent = %d body=%s", w.Code, w.Body.String())
	}
}

// Task-board W4: ?task_id= scopes the spawn list to one task's attempts,
// and a finished attempt carries a non-empty terminated_at. The
// termination time reads the child agent's stamp through the JOIN —
// agent_spawns.terminated_at itself has no writer (dead column since
// 0001), and agent↔spawn is 1:1, so the agent's stamp IS the attempt's
// end. Regression target: the W4 UI's "ended Xm ago" vs "started Xm ago"
// split, which silently always read "started" while the query selected
// only the writer-less spawn column.
func TestListSpawns_TaskIDFilterAndTerminatedAt(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-attempts")
	_, workerA, taskA := spawnWorkerWithTask(t, s, proj,
		"Attempted task", "The task whose attempts we list.")

	// A second tasked spawn on a DIFFERENT task must not leak into
	// taskA's attempts listing.
	if _, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "@worker.other",
		Kind:        "claude-code",
		ProjectID:   proj,
		SpawnSpec:   "project_id: " + proj + "\nkind: claude-code\nbackend:\n  cmd: echo test\n",
		Task:        &spawnTaskInline{Title: "Other task", BodyMD: "Unrelated."},
	}); err != nil {
		t.Fatalf("DoSpawn other: %v", err)
	}

	attempts := listSpawnsHTTP(t, s, "?task_id="+taskA)
	if len(attempts) != 1 {
		t.Fatalf("attempts for task %s = %d rows, want 1: %+v", taskA, len(attempts), attempts)
	}
	sp := attempts[0]
	if sp.ChildID != workerA || sp.TaskID != taskA {
		t.Errorf("attempt row = child %q task %q, want %q/%q",
			sp.ChildID, sp.TaskID, workerA, taskA)
	}
	// Live attempt: no termination stamp yet, and no secret leaks to the
	// owner-kind dashboard caller on this filtered path either.
	if sp.TerminatedAt != "" {
		t.Errorf("live attempt has terminated_at = %q, want empty", sp.TerminatedAt)
	}
	if sp.McpToken != "" {
		t.Errorf("owner caller leaked mcp_token on ?task_id= path")
	}

	terminateAgentHTTP(t, s, workerA)

	attempts = listSpawnsHTTP(t, s, "?task_id="+taskA)
	if len(attempts) != 1 {
		t.Fatalf("attempts after terminate = %d rows, want 1", len(attempts))
	}
	if attempts[0].TerminatedAt == "" {
		t.Errorf("terminated attempt has empty terminated_at — the JOIN fallback to agents.terminated_at regressed")
	}
}
