package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
					// Carries both worktree AND workdir fields so the fake serves
					// either mode; the hub reads only the ones for the session's mode.
					result = `{"branch":"hub/worker","head_sha":"deadbeef",` +
						`"remote":"origin","manifest_sha":"m-1",` +
						`"portable_worktree_path":"~/hub-work/team/pid/worker",` +
						`"portable_repo":"~/repos/proj",` +
						`"workdir_manifest_sha":"wd-1",` +
						`"portable_workdir_path":"~/hub-work/default/_team/worker"}`
				case "session_handoff_unpack":
					b, _ := json.Marshal(map[string]any{
						"worktree_path": targetWorktree, "workdir": targetWorktree,
					})
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

// openNonWorktreeSessionForAgent opens a session with NO worktree — the shape
// T2a teleports by moving the workdir itself rather than pushing a git branch.
func openNonWorktreeSessionForAgent(t *testing.T, s *Server, token, agentID string) sessionOut {
	t.Helper()
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "scratch-session",
			"agent_id":        agentID,
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusCreated {
		t.Fatalf("open non-worktree session: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)
	return ses
}

// A non-worktree session (T2a) teleports by moving its workdir bundle; the hub
// derives the workdir from identity, moves it, and the respawn re-derives the
// same path on the target — so the session's worktree_path stays empty.
func TestTeleportSession_NonWorktree(t *testing.T) {
	oldPoll := teleportCmdPoll
	teleportCmdPoll = 10 * time.Millisecond
	defer func() { teleportCmdPoll = oldPoll }()

	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-src")
	seedTestHost(t, s, defaultTeamID, "host-tgt", "gpu-box")
	ses := openNonWorktreeSessionForAgent(t, s, token, agentID)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/stop", nil)
	if status != http.StatusNoContent {
		t.Fatalf("stop: %d %s", status, body)
	}

	const targetWorkdir = "/home/tgt/hub-work/default/_team/worker"
	stop := fakeHost(t, s, targetWorkdir)
	defer stop()

	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/teleport",
		map[string]any{"target_host_id": "host-tgt"})
	if status != http.StatusOK {
		t.Fatalf("non-worktree teleport: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgent, _ := resp["new_agent_id"].(string)
	if newAgent == "" || newAgent == agentID {
		t.Fatalf("want a fresh agent id, got %q (old=%q)", newAgent, agentID)
	}

	// The respawn is on the target and active; a non-worktree session keeps an
	// EMPTY worktree_path (the respawn re-derives its workdir on the target).
	var curAgent, sesStatus, wt, newHost string
	_ = s.db.QueryRow(
		`SELECT COALESCE(current_agent_id,''), status, COALESCE(worktree_path,'') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&curAgent, &sesStatus, &wt)
	if curAgent != newAgent {
		t.Fatalf("session current_agent_id=%q; want %q", curAgent, newAgent)
	}
	if sesStatus != "active" {
		t.Fatalf("session status=%q; want active", sesStatus)
	}
	if wt != "" {
		t.Fatalf("non-worktree session should keep an empty worktree_path, got %q", wt)
	}
	_ = s.db.QueryRow(`SELECT COALESCE(host_id,'') FROM agents WHERE id = ?`, newAgent).Scan(&newHost)
	if newHost != "host-tgt" {
		t.Fatalf("resumed agent host_id=%q; want host-tgt", newHost)
	}
	// Both handoff commands (pack on source, unpack on target) must have run.
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM host_commands WHERE kind LIKE 'session_handoff_%'`).Scan(&n)
	if n < 2 {
		t.Fatalf("want pack + unpack handoff commands, got %d", n)
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

// ADR-056 D-6 / ADR-057: a secret-bearing session's envelope is sealed to the
// SOURCE host's key — the hub cannot re-seal it to the target and the re-spawn
// cannot mint a new one. Teleport must refuse UP FRONT, before any pack /
// unpack byte-moving, not fail late (project-inherited profile → 422 after
// the work) or silently respawn without secrets (explicitly-attached profile).
func TestTeleportSession_RefusesSecretBearing(t *testing.T) {
	s, token := newA2ATestServer(t)
	sesID, _ := spawnSecretBearingPausedSession(t, s, token)

	// No re-sealed envelope on the request → refuse up front.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+sesID+"/teleport",
		map[string]any{"target_host_id": "host-tgt"})
	if status != http.StatusConflict {
		t.Fatalf("teleport of secret-bearing session: want 409, got %d %s", status, body)
	}
	if !strings.Contains(string(body), "secret") {
		t.Fatalf("409 should name the secret refusal, got: %s", body)
	}
	// The refusal must precede the byte-moving: no handoff command may have
	// been enqueued (the stop's `terminate` command is expected and excluded).
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM host_commands WHERE kind LIKE 'session_handoff_%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("refusal must precede pack/unpack; found %d handoff commands", n)
	}
}

// spawnSecretBearingPausedSession seeds a paused secret-bearing worktree session
// on host-src (target host-tgt seeded too) and returns its id + the source agent.
func spawnSecretBearingPausedSession(t *testing.T, s *Server, token string) (sesID, agentID string) {
	t.Helper()
	ctx := context.Background()
	seedTestHost(t, s, defaultTeamID, "host-src", "src-box")
	seedTestHost(t, s, defaultTeamID, "host-tgt", "gpu-box")
	prof, err := s.createEnvProfile(ctx, defaultTeamID, envProfileBody{
		Name:       "prod-secrets",
		SecretRefs: []secretRef{{Key: "OPENAI_API_KEY", VaultItem: "openai-prod"}},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	out, spawnStatus, err := s.DoSpawn(ctx, defaultTeamID, spawnIn{
		ChildHandle:       "w",
		Kind:              "claude-code",
		HostID:            "host-src",
		EnvProfileID:      prof.ID,
		EnvSecretEnvelope: `{"v":1,"host_id":"host-src","epk":"AAAA","nonce":"BBBB","ct":"CCCC"}`,
		SpawnSpec:         "backend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, spawnStatus)
	}
	ses := openWorktreeSessionForAgent(t, s, token, out.AgentID)
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+out.AgentID+"/stop", nil)
	if status != http.StatusNoContent {
		t.Fatalf("stop: %d %s", status, body)
	}
	return ses.ID, out.AgentID
}

// D-7: a vault-holding client CAN teleport a secret-bearing session by re-sealing
// its secrets to the TARGET host and passing the envelope on the request. The hub
// accepts it (after verifying it is bound to the target) and threads it onto the
// target respawn — the source envelope never travels.
func TestTeleportSession_ResealAcceptsTargetEnvelope(t *testing.T) {
	oldPoll := teleportCmdPoll
	teleportCmdPoll = 10 * time.Millisecond
	defer func() { teleportCmdPoll = oldPoll }()

	s, token := newA2ATestServer(t)
	sesID, srcAgent := spawnSecretBearingPausedSession(t, s, token)

	const targetWt = "/data/agents/hub-work/team/pid/worker"
	stop := fakeHost(t, s, targetWt)
	defer stop()

	const resealed = `{"v":1,"host_id":"host-tgt","profile_id":"prod-secrets","epk":"DDDD","nonce":"EEEE","ct":"FFFF"}`
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+sesID+"/teleport",
		map[string]any{"target_host_id": "host-tgt", "env_secret_envelope": resealed})
	if status != http.StatusOK {
		t.Fatalf("teleport with re-sealed envelope: want 200, got %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgent, _ := resp["new_agent_id"].(string)
	if newAgent == "" || newAgent == srcAgent {
		t.Fatalf("want a fresh agent id, got %q (old=%q)", newAgent, srcAgent)
	}
	// The TARGET respawn row must carry the client's re-sealed envelope, not
	// the source one.
	var landed string
	_ = s.db.QueryRow(
		`SELECT COALESCE(env_secret_envelope,'') FROM agent_spawns WHERE child_agent_id = ?`,
		newAgent).Scan(&landed)
	if landed != resealed {
		t.Fatalf("target respawn envelope:\n got %q\nwant %q", landed, resealed)
	}
}

// D-7: the hub cannot open the envelope, but it verifies the client sealed it to
// the TARGET host (the AAD host_id is authenticated and the target host would
// refuse a mismatch). A wrong-host envelope is a 400, before any byte-moving.
func TestTeleportSession_ResealRejectsWrongHostEnvelope(t *testing.T) {
	s, token := newA2ATestServer(t)
	sesID, _ := spawnSecretBearingPausedSession(t, s, token)

	// Envelope sealed to some other host, not host-tgt.
	const wrong = `{"v":1,"host_id":"host-elsewhere","epk":"DDDD","nonce":"EEEE","ct":"FFFF"}`
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+sesID+"/teleport",
		map[string]any{"target_host_id": "host-tgt", "env_secret_envelope": wrong})
	if status != http.StatusBadRequest {
		t.Fatalf("teleport with wrong-host envelope: want 400, got %d %s", status, body)
	}
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM host_commands WHERE kind LIKE 'session_handoff_%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejection must precede pack/unpack; found %d handoff commands", n)
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
