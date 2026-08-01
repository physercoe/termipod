package server

import (
	"context"
	"net/http"
	"testing"
)

// The host-delete guard, and the orphan reaping that lets a dead machine
// actually leave the fleet. Before this suite the handler had no tests at
// all, which is how it came to disagree with every other live-agent query in
// the package about whether 'crashed' means alive.

func seedHostStatus(t *testing.T, s *Server, team, id, name, status string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO hosts (id, team_id, name, status, created_at)
		 VALUES (?, ?, ?, ?, ?)`, id, team, name, status, NowUTC()); err != nil {
		t.Fatalf("seed host %s: %v", id, err)
	}
}

func seedAgentOnHost(t *testing.T, s *Server, team, id, handle, hostID, status string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO agents (id, team_id, handle, kind, status, host_id, created_at)
		 VALUES (?, ?, ?, 'claude-code', ?, ?, ?)`,
		id, team, handle, status, hostID, NowUTC()); err != nil {
		t.Fatalf("seed agent %s: %v", id, err)
	}
}

func agentStatus(t *testing.T, s *Server, id string) string {
	t.Helper()
	var st string
	if err := s.db.QueryRow(`SELECT status FROM agents WHERE id = ?`, id).Scan(&st); err != nil {
		t.Fatalf("read agent %s: %v", id, err)
	}
	return st
}

func hostExists(t *testing.T, s *Server, id string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("count host %s: %v", id, err)
	}
	return n > 0
}

// The bug the director hit: an agent the reconcile loop had already declared
// dead still blocked its host's deletion, because this one query spelled the
// terminal set differently from the rest of the package.
func TestDeleteHost_TerminalAgentsNeverBlock(t *testing.T) {
	for _, status := range agentTerminalStatuses {
		t.Run(status, func(t *testing.T) {
			s, token := newA2ATestServer(t)
			host := NewID()
			seedHostStatus(t, s, defaultTeamID, host, "box-"+status, "online")
			seedAgentOnHost(t, s, defaultTeamID, NewID(), "w-"+status, host, status)

			code, body := doReq(t, s, token, http.MethodDelete,
				"/v1/teams/"+defaultTeamID+"/hosts/"+host, nil)
			if code != http.StatusNoContent {
				t.Fatalf("delete host with a %s agent: got %d %s, want 204",
					status, code, body)
			}
			if hostExists(t, s, host) {
				t.Fatal("host row survived a 204")
			}
		})
	}
}

// The guard still earns its keep while somebody is home to honour it.
func TestDeleteHost_OnlineWithLiveAgentsRefuses(t *testing.T) {
	s, token := newA2ATestServer(t)
	host := NewID()
	agent := NewID()
	seedHostStatus(t, s, defaultTeamID, host, "live-box", "online")
	seedAgentOnHost(t, s, defaultTeamID, agent, "worker", host, "running")

	code, _ := doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/hosts/"+host, nil)
	if code != http.StatusConflict {
		t.Fatalf("delete online host with a running agent: got %d, want 409", code)
	}
	if !hostExists(t, s, host) {
		t.Fatal("refused delete still removed the host")
	}
	if got := agentStatus(t, s, agent); got != "running" {
		t.Fatalf("a refused delete must not touch agents: got %q", got)
	}
}

// The deadlock this fixes: the host-runner is gone, so nothing will ever
// report those agents terminal, and nothing can reach them to stop. The
// delete reaps them instead of refusing forever.
func TestDeleteHost_OfflineReapsOrphanedAgents(t *testing.T) {
	s, token := newA2ATestServer(t)
	host := NewID()
	running, pending := NewID(), NewID()
	seedHostStatus(t, s, defaultTeamID, host, "dead-box", "offline")
	seedAgentOnHost(t, s, defaultTeamID, running, "worker-1", host, "running")
	seedAgentOnHost(t, s, defaultTeamID, pending, "worker-2", host, "pending")

	code, body := doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/hosts/"+host, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete offline host: got %d %s, want 204", code, body)
	}
	if hostExists(t, s, host) {
		t.Fatal("host row survived a 204")
	}
	// 'crashed', not 'terminated' — nobody stopped these agents, and ADR-029
	// D-3 reads the difference (blocked vs cancelled) onto their tasks.
	for _, id := range []string{running, pending} {
		if got := agentStatus(t, s, id); got != "crashed" {
			t.Fatalf("orphaned agent %s: got %q, want crashed", id, got)
		}
	}
	var terminated int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM agents WHERE terminated_at IS NOT NULL`).Scan(&terminated); err != nil {
		t.Fatalf("count terminated_at: %v", err)
	}
	if terminated != 2 {
		t.Fatalf("reaped agents must be stamped terminated_at: got %d of 2", terminated)
	}
}

// Reaping is not just a status write — the aftermath has to run, or a dead
// agent's bearer outlives it and its session keeps claiming to be live.
func TestDeleteHost_ReapRunsCrashAftermath(t *testing.T) {
	s, token := newA2ATestServer(t)
	host := NewID()
	agent := NewID()
	session := NewID()
	seedHostStatus(t, s, defaultTeamID, host, "dead-box", "offline")
	seedAgentOnHost(t, s, defaultTeamID, agent, "worker", host, "running")
	if _, err := s.db.Exec(`
		INSERT INTO sessions
			(id, team_id, scope_kind, current_agent_id, status, opened_at, last_active_at)
		VALUES (?, ?, 'agent', ?, 'active', ?, ?)`,
		session, defaultTeamID, agent, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if code, body := doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/hosts/"+host, nil); code != http.StatusNoContent {
		t.Fatalf("delete offline host: got %d %s, want 204", code, body)
	}

	var st string
	if err := s.db.QueryRow(
		`SELECT status FROM sessions WHERE id = ?`, session).Scan(&st); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if st != "paused" {
		t.Fatalf("session pointing at a reaped agent: got %q, want paused", st)
	}
}

// The delete must not reach across teams, and a host that was never there is
// a 404 — not a silent 204 that reports success for nothing.
func TestDeleteHost_MissingIs404(t *testing.T) {
	s, token := newA2ATestServer(t)
	code, _ := doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/hosts/"+NewID(), nil)
	if code != http.StatusNotFound {
		t.Fatalf("delete absent host: got %d, want 404", code)
	}
}

// An agent parked on ANOTHER host must never be dragged into this delete.
func TestDeleteHost_ReapIsScopedToTheHost(t *testing.T) {
	s, token := newA2ATestServer(t)
	dead, alive := NewID(), NewID()
	bystander := NewID()
	seedHostStatus(t, s, defaultTeamID, dead, "dead-box", "offline")
	seedHostStatus(t, s, defaultTeamID, alive, "live-box", "online")
	seedAgentOnHost(t, s, defaultTeamID, NewID(), "doomed", dead, "running")
	seedAgentOnHost(t, s, defaultTeamID, bystander, "bystander", alive, "running")

	if code, _ := doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/hosts/"+dead, nil); code != http.StatusNoContent {
		t.Fatalf("delete offline host: got %d, want 204", code)
	}
	if got := agentStatus(t, s, bystander); got != "running" {
		t.Fatalf("agent on a different host got reaped: %q", got)
	}
	if !hostExists(t, s, alive) {
		t.Fatal("the wrong host was deleted")
	}
}

// The two spellings of the terminal set must not drift apart again — this is
// the invariant the original bug violated, stated directly.
func TestAgentTerminalStatusesAgreeWithSQL(t *testing.T) {
	s, _ := newA2ATestServer(t)
	host := NewID()
	seedHostStatus(t, s, defaultTeamID, host, "box", "online")

	all := append([]string{"pending", "running", "paused"}, agentTerminalStatuses...)
	for _, st := range all {
		seedAgentOnHost(t, s, defaultTeamID, NewID(), "a-"+st, host, st)
	}
	live, err := s.liveAgentsOnHost(context.Background(), defaultTeamID, host)
	if err != nil {
		t.Fatalf("liveAgentsOnHost: %v", err)
	}
	// Whatever isAgentTerminal says in Go, the SQL must have filtered.
	if len(live) != 3 {
		t.Fatalf("live agents: got %d, want 3 (pending/running/paused)", len(live))
	}
	for _, ag := range live {
		var st string
		if err := s.db.QueryRow(`SELECT status FROM agents WHERE id = ?`, ag.ID).Scan(&st); err != nil {
			t.Fatalf("read agent: %v", err)
		}
		if isAgentTerminal(st) {
			t.Fatalf("SQL returned %q as live but isAgentTerminal calls it terminal", st)
		}
	}
}
