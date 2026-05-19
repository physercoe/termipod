package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// seedTestAgent inserts a minimal agent row for the admin-agents tests.
func seedTestAgent(t *testing.T, s *Server, team, id, handle, status string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		 VALUES (?, ?, ?, 'worker.v1', ?, ?)`,
		id, team, handle, status, NowUTC()); err != nil {
		t.Fatalf("seed agent %s: %v", id, err)
	}
}

func adminAgentsContain(agents []AdminAgentRow, id string) bool {
	for _, a := range agents {
		if a.AgentID == id {
			return true
		}
	}
	return false
}

// TestAdminListAgents_LiveFilter checks the default response excludes
// terminal agents and ?all=1 includes them.
func TestAdminListAgents_LiveFilter(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestAgent(t, s, defaultTeamID, "agent-live", "@live", "running")
	seedTestAgent(t, s, defaultTeamID, "agent-done", "@done", "terminated")

	_, body := doReq(t, s, token, http.MethodGet, "/v1/admin/agents", nil)
	var out struct {
		Agents []AdminAgentRow `json:"agents"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !adminAgentsContain(out.Agents, "agent-live") {
		t.Error("default list should contain the live agent")
	}
	if adminAgentsContain(out.Agents, "agent-done") {
		t.Error("default list must exclude the terminated agent")
	}

	_, body = doReq(t, s, token, http.MethodGet, "/v1/admin/agents?all=1", nil)
	out.Agents = nil
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !adminAgentsContain(out.Agents, "agent-live") ||
		!adminAgentsContain(out.Agents, "agent-done") {
		t.Errorf("all=1 list should contain both agents; got %+v", out.Agents)
	}
}

// TestAdminListAgents_NonOwnerGets403 locks the owner-scope gate.
func TestAdminListAgents_NonOwnerGets403(t *testing.T) {
	s, _ := newA2ATestServer(t)
	memberToken := mintNonOwnerToken(t, s, defaultTeamID)
	status, _ := doReq(t, s, memberToken, http.MethodGet, "/v1/admin/agents", nil)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
}

// TestAdminKillAgent_TerminatesAndAudits kills a session-less running
// agent and asserts the status flip plus the agent.terminate audit row.
func TestAdminKillAgent_TerminatesAndAudits(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestAgent(t, s, defaultTeamID, "agent-x", "@worker", "running")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/agents/agent-x/kill", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out struct {
		Killed bool `json:"killed"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Killed {
		t.Fatalf("killed=false: %s", body)
	}

	var st string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT status FROM agents WHERE id = 'agent-x'`).Scan(&st); err != nil {
		t.Fatalf("query agent: %v", err)
	}
	if st != "terminated" {
		t.Errorf("agent status = %q, want terminated", st)
	}
	var n int
	_ = s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM audit_events
		  WHERE action = 'agent.terminate' AND target_id = 'agent-x'`).Scan(&n)
	if n != 1 {
		t.Errorf("agent.terminate audit rows = %d, want 1", n)
	}
}

// TestAdminKillAgent_Idempotent confirms killing an already-terminal
// agent reports killed=false rather than erroring.
func TestAdminKillAgent_Idempotent(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestAgent(t, s, defaultTeamID, "agent-dead", "@worker", "terminated")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/agents/agent-dead/kill", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out struct {
		Killed  bool   `json:"killed"`
		Already string `json:"already"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Killed || out.Already != "terminated" {
		t.Errorf("out = %+v, want killed=false already=terminated", out)
	}
}

// TestAdminKillAgent_NotFound confirms an unknown agent id 404s.
func TestAdminKillAgent_NotFound(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/admin/agents/ghost/kill", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}
