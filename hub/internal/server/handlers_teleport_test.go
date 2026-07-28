package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

// openWorktreeSessionForAgent opens a session with a worktree spec that DECLARES
// a git repo — the shape teleport requires (T1 is worktree-only).
func openWorktreeSessionForAgent(t *testing.T, s *Server, token, agentID string) sessionOut {
	t.Helper()
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":         "teleportable",
			"agent_id":      agentID,
			"worktree_path": "/home/src/hub-work/team/pid/worker",
			"spawn_spec_yaml": "kind: claude-code\n" +
				"backend:\n  cmd: claude\n" +
				"worktree:\n  repo: /home/src/repos/proj\n  branch: hub/worker\n",
		})
	if status != http.StatusCreated {
		t.Fatalf("open worktree session: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)
	return ses
}

// fakeHost drains pending host_commands and completes them with canned results,
// standing in for a polling host-runner so the hub-side orchestration can be
// tested end-to-end without one. It runs until stop() is called.
func fakeHost(t *testing.T, s *Server, targetWorktree string) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	var once sync.Once
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			rows, err := s.db.QueryContext(context.Background(),
				`SELECT id, kind FROM host_commands WHERE status = 'pending'`)
			if err != nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			type cmd struct{ id, kind string }
			var pending []cmd
			for rows.Next() {
				var c cmd
				_ = rows.Scan(&c.id, &c.kind)
				pending = append(pending, c)
			}
			rows.Close()
			for _, c := range pending {
				var result string
				switch c.kind {
				case "session_handoff_pack":
					result = `{"branch":"hub/worker","head_sha":"deadbeef",` +
						`"remote":"origin","manifest_sha":"m-1",` +
						`"portable_worktree_path":"~/hub-work/team/pid/worker",` +
						`"portable_repo":"~/repos/proj"}`
				case "session_handoff_unpack":
					b, _ := json.Marshal(map[string]any{"worktree_path": targetWorktree})
					result = string(b)
				default:
					continue
				}
				_, _ = s.writeDB.ExecContext(context.Background(),
					`UPDATE host_commands SET status='done', result_json=?, completed_at=? WHERE id=?`,
					result, NowUTC(), c.id)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

func TestTeleportSession_MovesToTargetHost(t *testing.T) {
	// Fast polling so the two awaits don't dominate the test.
	oldPoll := teleportCmdPoll
	teleportCmdPoll = 10 * time.Millisecond
	defer func() { teleportCmdPoll = oldPoll }()

	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-src")
	seedTestHost(t, s, defaultTeamID, "host-tgt", "gpu-box")
	ses := openWorktreeSessionForAgent(t, s, token, agentID)

	// Pause the session (teleport is pause-first).
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/stop", nil)
	if status != http.StatusNoContent {
		t.Fatalf("stop: %d %s", status, body)
	}

	const targetWt = "/data/agents/hub-work/team/pid/worker"
	stop := fakeHost(t, s, targetWt)
	defer stop()

	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/teleport",
		map[string]any{"target_host_id": "host-tgt"})
	if status != http.StatusOK {
		t.Fatalf("teleport: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgent, _ := resp["new_agent_id"].(string)
	if newAgent == "" || newAgent == agentID {
		t.Fatalf("want a fresh agent id, got %q (old=%q)", newAgent, agentID)
	}

	// The session now points at the new agent, on the TARGET host, at the
	// target-side worktree, and is active again.
	var curAgent, sesStatus, wt string
	_ = s.db.QueryRow(
		`SELECT COALESCE(current_agent_id,''), status, COALESCE(worktree_path,'') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&curAgent, &sesStatus, &wt)
	if curAgent != newAgent {
		t.Fatalf("session current_agent_id=%q; want %q", curAgent, newAgent)
	}
	if sesStatus != "active" {
		t.Fatalf("session status=%q; want active", sesStatus)
	}
	if wt != targetWt {
		t.Fatalf("session worktree_path=%q; want %q", wt, targetWt)
	}
	var newHost string
	_ = s.db.QueryRow(`SELECT COALESCE(host_id,'') FROM agents WHERE id = ?`, newAgent).Scan(&newHost)
	if newHost != "host-tgt" {
		t.Fatalf("resumed agent host_id=%q; want host-tgt", newHost)
	}
}

func TestTeleportSession_RefusesActiveSession(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-src")
	seedTestHost(t, s, defaultTeamID, "host-tgt", "gpu-box")
	ses := openWorktreeSessionForAgent(t, s, token, agentID) // active, not paused

	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/teleport",
		map[string]any{"target_host_id": "host-tgt"})
	if status != http.StatusConflict {
		t.Fatalf("teleport of active session: want 409, got %d", status)
	}
}

func TestTeleportSession_RefusesSameHost(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-src")
	ses := openWorktreeSessionForAgent(t, s, token, agentID)
	doReq(t, s, token, http.MethodPost, "/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/stop", nil)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/teleport",
		map[string]any{"target_host_id": "host-src"})
	if status != http.StatusConflict {
		t.Fatalf("teleport to same host: want 409, got %d %s", status, body)
	}
}

func TestTeleportSession_RefusesOfflineTarget(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-src")
	ses := openWorktreeSessionForAgent(t, s, token, agentID)
	doReq(t, s, token, http.MethodPost, "/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/stop", nil)

	// host-tgt was never registered → offline/unknown.
	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/teleport",
		map[string]any{"target_host_id": "host-tgt"})
	if status != http.StatusConflict {
		t.Fatalf("teleport to unregistered host: want 409, got %d", status)
	}
}
