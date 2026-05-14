package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestPostAgentEvent_CapturesEngineSessionID pins the slice-1
// behaviour: when a claude-code agent emits its session.init frame,
// the hub's event-insert path must lift `payload.session_id` into
// `sessions.engine_session_id` so the next resume has a cursor to
// thread. Without this, the resume handler has nothing to splice and
// claude-code starts a fresh engine session every time — exactly the
// bug ADR-014 is fixing.
func TestPostAgentEvent_CapturesEngineSessionID(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// Open a session pointing at the agent so lookupSessionForAgent
	// resolves a non-empty sessionID inside handlePostAgentEvent.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "engine cursor test", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open session: %d %s", status, body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Drive the same shape the StdioDriver emits on first init:
	// kind=session.init, producer=agent, payload carries the engine
	// session_id captured from claude's stream-json `system/init`
	// frame (driver_stdio.go:295).
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "agent",
			"payload": map[string]any{
				"session_id": "engine-uuid-aaa",
				"model":      "claude-opus-4-7",
				"cwd":        "/tmp/wt",
			},
		})
	if status != http.StatusCreated {
		t.Fatalf("post init: %d %s", status, body)
	}

	var got string
	_ = s.db.QueryRow(
		`SELECT COALESCE(engine_session_id, '') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&got)
	if got != "engine-uuid-aaa" {
		t.Errorf("sessions.engine_session_id = %q; want %q",
			got, "engine-uuid-aaa")
	}
}

// TestPostAgentEvent_IgnoresNonInitForCapture — only session.init
// (producer=agent) frames update the cursor. A `text` frame happens
// to carry session_id under some translator shapes; we explicitly
// don't want those to overwrite the captured cursor with whatever
// transient identifier they hold.
func TestPostAgentEvent_IgnoresNonInitForCapture(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "non-init test", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Seed a real cursor first.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "agent",
			"payload":  map[string]any{"session_id": "real-cursor"},
		})

	// Now post a different kind that happens to have session_id in
	// its payload. The cursor must not change.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "text",
			"producer": "agent",
			"payload":  map[string]any{"session_id": "junk-id", "text": "hi"},
		})

	var got string
	_ = s.db.QueryRow(
		`SELECT COALESCE(engine_session_id, '') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&got)
	if got != "real-cursor" {
		t.Errorf("cursor was overwritten by non-init event: got %q want %q",
			got, "real-cursor")
	}
}

// TestPostAgentEvent_CapturesEngineSessionID_GeminiCLI is the ADR-021
// W1.1 pin: the same engine-neutral capture path that lifts claude's
// stream-json `system/init` cursor must also lift gemini's ACP
// `session/new` cursor when the M1 driver emits its dedicated
// `session.init` event (driver_acp.go Start()). The capture is gated on
// the (kind, producer) tuple of the event — not on `agents.kind` — so
// the same SQL fires regardless of engine. This test pins that
// invariant against an agent stamped `kind=gemini-cli` so a future
// refactor that accidentally adds a kind filter would fail loudly.
func TestPostAgentEvent_CapturesEngineSessionID_GeminiCLI(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Seed a channel + a gemini-cli agent directly so we don't have to
	// extend seedChannelAndAgent's signature for this one test.
	channelID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO channels (id, scope_kind, name, created_at)
		VALUES (?, 'team', 'meta', ?)`, channelID, NowUTC()); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	agentID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, created_at)
		VALUES (?, ?, 'worker', 'gemini-cli', ?)`,
		agentID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed gemini agent: %v", err)
	}

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "gemini cursor test", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open session: %d %s", status, body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Same shape ACPDriver.Start() emits after a successful session/new
	// handshake. The session_id is gemini's UUID, not claude's.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "agent",
			"payload": map[string]any{
				"session_id": "gemini-uuid-bbb",
			},
		})
	if status != http.StatusCreated {
		t.Fatalf("post init: %d %s", status, body)
	}

	var got string
	_ = s.db.QueryRow(
		`SELECT COALESCE(engine_session_id, '') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&got)
	if got != "gemini-uuid-bbb" {
		t.Errorf("sessions.engine_session_id = %q; want %q "+
			"(engine-neutral capture must work for gemini-cli agents)",
			got, "gemini-uuid-bbb")
	}
}

// TestPostAgentEvent_IgnoresUserProducerInit — a `producer=user` init
// (echo of our own input, hypothetically) shouldn't update the
// cursor. Only frames produced by the engine carry an authoritative
// session id.
func TestPostAgentEvent_IgnoresUserProducerInit(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "user init test", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "user",
			"payload":  map[string]any{"session_id": "user-supplied-id"},
		})

	var got string
	_ = s.db.QueryRow(
		`SELECT COALESCE(engine_session_id, '') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&got)
	if got != "" {
		t.Errorf("user-producer init updated cursor: got %q want empty", got)
	}
}

// TestSessions_ResumeThreadsClaudeResume is the end-to-end pin for
// ADR-014: open a claude-code session, post a session.init that
// captures the cursor, crash the agent, resume, and check that the
// new agent_spawns row's spawn_spec_yaml carries `--resume <id>`.
// Without this the bug regresses silently — the audit row would still
// look fine, only the running agent's behaviour would diverge.
func TestSessions_ResumeThreadsClaudeResume(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, oldAgentID := seedChannelAndAgent(t, s, "", "host-x")

	specYAML := "kind: claude-code\n" +
		"backend:\n" +
		"  cmd: \"claude --model claude-opus-4-7 --print " +
		"--output-format stream-json --input-format stream-json " +
		"--verbose --dangerously-skip-permissions\"\n"

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "resume threads --resume",
			"agent_id":        oldAgentID,
			"worktree_path":   "/tmp/wt/resume-thread",
			"spawn_spec_yaml": specYAML,
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Capture the engine cursor via the agent_events POST path.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "agent",
			"payload":  map[string]any{"session_id": "engine-cursor-xyz"},
		})

	// Crash → session auto-pauses (TestSessions_PauseOnAgentTerminal
	// pins this side of the contract).
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID,
		map[string]any{"status": "crashed"})

	// Resume.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/resume", nil)
	if status != http.StatusOK {
		t.Fatalf("resume: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgentID, _ := resp["new_agent_id"].(string)
	if newAgentID == "" {
		t.Fatalf("no new_agent_id: %s", body)
	}

	// agent_spawns for the new agent must carry the spliced cmd.
	var newSpec string
	_ = s.db.QueryRow(
		`SELECT spawn_spec_yaml FROM agent_spawns
		   WHERE child_agent_id = ?`, newAgentID).Scan(&newSpec)
	if !strings.Contains(newSpec, "--resume engine-cursor-xyz") {
		t.Errorf("new spawn_spec missing --resume engine-cursor-xyz:\n%s",
			newSpec)
	}
	if !strings.Contains(newSpec, "claude --resume engine-cursor-xyz") {
		t.Errorf("--resume not placed after `claude` token:\n%s", newSpec)
	}

	// Sessions row must NOT have been mutated — it carries the
	// original cmd so a future resume re-splices fresh from a clean
	// state. Without this invariant, repeated resumes would stack
	// stale `--resume` flags.
	var sesSpec string
	_ = s.db.QueryRow(
		`SELECT COALESCE(spawn_spec_yaml, '') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&sesSpec)
	if strings.Contains(sesSpec, "--resume") {
		t.Errorf("sessions.spawn_spec_yaml leaked --resume:\n%s", sesSpec)
	}
}

// TestSessions_ResumeThreadsACPCursor pins the gemini/ACP analogue of
// TestSessions_ResumeThreadsClaudeResume: ADR-021 W1.2. After a
// captured cursor + crash + resume cycle on a gemini-cli agent, the
// new agent_spawns row's spawn_spec_yaml carries
// `resume_session_id: <id>` so the host-runner-side ACPDriver can
// dispatch session/load instead of session/new on the next launch.
// Without this, the cursor stays stranded in the sessions table and
// every "resume" cold-starts a fresh ACP session — the exact bug
// W1.1+W1.2 are closing.
func TestSessions_ResumeThreadsACPCursor(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Inline-seed a gemini-cli agent + host (seedChannelAndAgent
	// hardcodes claude-code; reusing it would route the resume
	// through the wrong splice path).
	channelID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO channels (id, scope_kind, name, created_at)
		VALUES (?, 'team', 'meta', ?)`, channelID, NowUTC()); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, 'h-gemini', 'online', '{}', ?)`,
		"host-gemini", defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	oldAgentID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, host_id, created_at)
		VALUES (?, ?, 'worker', 'gemini-cli', 'host-gemini', ?)`,
		oldAgentID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	specYAML := "kind: gemini-cli\n" +
		"backend:\n" +
		"  cmd: \"gemini --acp\"\n" +
		"  default_workdir: /tmp/wt\n"

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "resume threads acp cursor",
			"agent_id":        oldAgentID,
			"worktree_path":   "/tmp/wt/acp-resume",
			"spawn_spec_yaml": specYAML,
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Capture the engine cursor — the same shape ACPDriver.Start emits
	// after a successful session/new (W1.1).
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "agent",
			"payload":  map[string]any{"session_id": "gemini-engine-cursor-zzz"},
		})

	// Crash → session auto-pauses.
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID,
		map[string]any{"status": "crashed"})

	// Resume.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/resume", nil)
	if status != http.StatusOK {
		t.Fatalf("resume: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgentID, _ := resp["new_agent_id"].(string)
	if newAgentID == "" {
		t.Fatalf("no new_agent_id: %s", body)
	}

	// agent_spawns for the new agent must carry the YAML field.
	var newSpec string
	_ = s.db.QueryRow(
		`SELECT spawn_spec_yaml FROM agent_spawns
		   WHERE child_agent_id = ?`, newAgentID).Scan(&newSpec)
	if !strings.Contains(newSpec, "resume_session_id: gemini-engine-cursor-zzz") {
		t.Errorf("new spawn_spec missing resume_session_id field:\n%s", newSpec)
	}
	// And NOT the claude flag — the gemini path must not route through
	// spliceClaudeResume even by accident.
	if strings.Contains(newSpec, "--resume gemini-engine-cursor-zzz") {
		t.Errorf("gemini resume incorrectly added cmd-line --resume flag:\n%s",
			newSpec)
	}

	// Sessions row must NOT carry the spliced field — same invariant
	// claude follows; we re-splice fresh on every resume.
	var sesSpec string
	_ = s.db.QueryRow(
		`SELECT COALESCE(spawn_spec_yaml, '') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&sesSpec)
	if strings.Contains(sesSpec, "resume_session_id:") {
		t.Errorf("sessions.spawn_spec_yaml leaked resume_session_id:\n%s", sesSpec)
	}
}

// TestSessions_ResumeThreadsACPCursor_KimiCode pins the kimi-code
// arm of the resume splice switch (ADR-026 W6). Identical contract
// to gemini-cli: same protocol-level `resume_session_id` field, same
// no-`--resume`-cmd-flag invariant. Closes the bug where a kimi-code
// agent crashed + resumed would cold-start via session/new because
// the handleResumeSession switch hadn't enumerated kimi-code.
func TestSessions_ResumeThreadsACPCursor_KimiCode(t *testing.T) {
	s, token := newA2ATestServer(t)

	channelID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO channels (id, scope_kind, name, created_at)
		VALUES (?, 'team', 'meta', ?)`, channelID, NowUTC()); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, 'h-kimi', 'online', '{}', ?)`,
		"host-kimi", defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	oldAgentID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, host_id, created_at)
		VALUES (?, ?, 'worker', 'kimi-code', 'host-kimi', ?)`,
		oldAgentID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	specYAML := "kind: kimi-code\n" +
		"backend:\n" +
		"  cmd: \"kimi --yolo --thinking acp\"\n" +
		"  default_workdir: /tmp/wt\n"

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "kimi resume threads acp cursor",
			"agent_id":        oldAgentID,
			"worktree_path":   "/tmp/wt/kimi-resume",
			"spawn_spec_yaml": specYAML,
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "agent",
			"payload":  map[string]any{"session_id": "kimi-engine-cursor-abc"},
		})

	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID,
		map[string]any{"status": "crashed"})

	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/resume", nil)
	if status != http.StatusOK {
		t.Fatalf("resume: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgentID, _ := resp["new_agent_id"].(string)
	if newAgentID == "" {
		t.Fatalf("no new_agent_id: %s", body)
	}

	var newSpec string
	_ = s.db.QueryRow(
		`SELECT spawn_spec_yaml FROM agent_spawns
		   WHERE child_agent_id = ?`, newAgentID).Scan(&newSpec)
	if !strings.Contains(newSpec, "resume_session_id: kimi-engine-cursor-abc") {
		t.Errorf("new spawn_spec missing resume_session_id field:\n%s", newSpec)
	}
	if strings.Contains(newSpec, "--resume kimi-engine-cursor-abc") {
		t.Errorf("kimi resume incorrectly added cmd-line --resume flag:\n%s",
			newSpec)
	}

	var sesSpec string
	_ = s.db.QueryRow(
		`SELECT COALESCE(spawn_spec_yaml, '') FROM sessions WHERE id = ?`,
		ses.ID).Scan(&sesSpec)
	if strings.Contains(sesSpec, "resume_session_id:") {
		t.Errorf("sessions.spawn_spec_yaml leaked resume_session_id:\n%s", sesSpec)
	}
}

// TestSessions_ResumeCarriesModeModelState — ADR-026 W7. kimi-cli's
// session/load returns an empty `{}` response (the ACP spec permits
// agents to omit echoing state on load), so ACPDriver.Start emits
// no synthetic `currentModeId`/`currentModelId` system event for the
// resumed agent. Mobile's modeModelStateFromEvents then returns null
// and the picker is hidden on the resumed agent even though the
// daemon's session is alive and routable. Fix:
// handleResumeSession copies the prior agent's most recent mode/model
// state event under the new agent_id. This test pins the carryover
// end-to-end: seed a kimi-code agent with a mode/model state event,
// pause+resume the session, assert the new agent has a system event
// with the same picker-relevant fields.
func TestSessions_ResumeCarriesModeModelState(t *testing.T) {
	s, token := newA2ATestServer(t)

	channelID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO channels (id, scope_kind, name, created_at)
		VALUES (?, 'team', 'meta', ?)`, channelID, NowUTC()); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, 'h-kimi', 'online', '{}', ?)`,
		"host-kimi-w7", defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	oldAgentID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, host_id, created_at)
		VALUES (?, ?, 'worker', 'kimi-code', 'host-kimi-w7', ?)`,
		oldAgentID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	specYAML := "kind: kimi-code\n" +
		"backend:\n" +
		"  cmd: \"kimi --yolo --thinking acp\"\n" +
		"  default_workdir: /tmp/wt\n"

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "kimi resume carries mode/model state",
			"agent_id":        oldAgentID,
			"worktree_path":   "/tmp/wt/kimi-w7",
			"spawn_spec_yaml": specYAML,
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Emit the engine-state event the ACPDriver synthesizes from
	// session/new — the carryover query is field-shape-driven, so it
	// works regardless of which engine wrote it originally.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/events",
		map[string]any{
			"kind":     "system",
			"producer": "system",
			"payload": map[string]any{
				"currentModelId": "kimi-code/kimi-for-coding,thinking",
				"availableModels": []map[string]any{
					{"modelId": "kimi-code/kimi-for-coding", "name": "kimi-for-coding"},
					{"modelId": "kimi-code/kimi-for-coding,thinking", "name": "kimi-for-coding (thinking)"},
				},
				"currentModeId": "default",
				"availableModes": []map[string]any{
					{"id": "default", "name": "Default", "description": "The default mode."},
				},
			},
		})

	// Capture cursor + pause + resume.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "agent",
			"payload":  map[string]any{"session_id": "kimi-cursor-w7"},
		})
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID,
		map[string]any{"status": "crashed"})
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/resume", nil)
	if status != http.StatusOK {
		t.Fatalf("resume: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgentID, _ := resp["new_agent_id"].(string)
	if newAgentID == "" {
		t.Fatalf("no new_agent_id: %s", body)
	}

	// Assert the new agent has at least one system event carrying the
	// picker-relevant fields. Strict shape: same currentModelId +
	// availableModels survived the carryover so mobile's
	// modeModelStateFromEvents will hydrate the picker.
	var carriedJSON string
	err := s.db.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'system' AND producer = 'system'
		   AND payload_json LIKE '%currentModelId%'
		 ORDER BY seq DESC LIMIT 1`,
		newAgentID).Scan(&carriedJSON)
	if err != nil {
		t.Fatalf("no carried mode/model event on new agent: %v", err)
	}
	if !strings.Contains(carriedJSON, "kimi-code/kimi-for-coding,thinking") {
		t.Errorf("carried event missing currentModelId:\n%s", carriedJSON)
	}
	if !strings.Contains(carriedJSON, "availableModels") {
		t.Errorf("carried event missing availableModels:\n%s", carriedJSON)
	}
	if !strings.Contains(carriedJSON, "default") {
		t.Errorf("carried event missing mode state:\n%s", carriedJSON)
	}
}

// TestSessions_ForkDoesNotInheritEngineSessionID is the ADR-014
// defensive guard: fork must mint a fresh engine cursor, never
// inherit the source's. Engine session stores aren't multi-writer —
// claude's `~/.claude/projects/<cwd>/<sid>.jsonl`, gemini's
// `<projdir>/.gemini/sessions/<uuid>`, codex's CLI thread store all
// assume a single live attacher. Two parallel sessions resuming the
// same engine id would corrupt each other on the next turn and
// silently break the archived source's "frozen" state. The
// invariant is implicit today (handleForkSession writes a fresh
// row with no carryover from `engine_session_id`); this test makes
// it explicit so a future "helpfully" inheriting change fails loudly
// at CI rather than mid-conversation in production.
func TestSessions_ForkDoesNotInheritEngineSessionID(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-fork")

	// Promote the seeded agent to a steward shape so the source
	// session can be opened against it.
	if _, err := s.db.Exec(
		`UPDATE agents SET handle='steward', status='running' WHERE id=?`,
		agentID); err != nil {
		t.Fatalf("promote agent: %v", err)
	}

	// Open a source session and capture an engine cursor on it via
	// the standard session.init capture path. This is the realistic
	// pre-archive state of a session that's done useful work.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":      "fork source",
			"agent_id":   agentID,
			"scope_kind": "project",
			"scope_id":   "proj-fork-guard",
		})
	if status != http.StatusCreated {
		t.Fatalf("open source: %s", body)
	}
	var src sessionOut
	_ = json.Unmarshal(body, &src)

	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "session.init",
			"producer": "agent",
			"payload":  map[string]any{"session_id": "source-engine-cursor"},
		})

	// Confirm the source actually has a cursor; without this the
	// rest of the test is trivially passing for the wrong reason.
	var srcCursor string
	_ = s.db.QueryRow(
		`SELECT COALESCE(engine_session_id, '') FROM sessions WHERE id = ?`,
		src.ID).Scan(&srcCursor)
	if srcCursor != "source-engine-cursor" {
		t.Fatalf("source engine_session_id not captured: got %q", srcCursor)
	}

	// Archive the source so it's fork-eligible.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+src.ID+"/archive", nil)

	// Fork.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+src.ID+"/fork", nil)
	if status != http.StatusCreated {
		t.Fatalf("fork: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	forkID, _ := resp["session_id"].(string)
	if forkID == "" || forkID == src.ID {
		t.Fatalf("fork returned bad session_id=%q (source=%q)", forkID, src.ID)
	}

	// The invariant: fork's engine_session_id must be NULL.
	// Inheriting the source's cursor would let a spawn into the fork
	// resume into the same engine session the archived source was
	// frozen at — corrupting the archive on the next user turn.
	var forkCursor sql.NullString
	if err := s.db.QueryRow(
		`SELECT engine_session_id FROM sessions WHERE id = ?`,
		forkID).Scan(&forkCursor); err != nil {
		t.Fatalf("read fork cursor: %v", err)
	}
	if forkCursor.Valid {
		t.Errorf("fork inherited engine_session_id=%q; want NULL "+
			"(see ADR-014 fork-is-cold-start invariant)",
			forkCursor.String)
	}

	// Source's cursor must be untouched — fork is non-destructive.
	var srcAfter string
	_ = s.db.QueryRow(
		`SELECT COALESCE(engine_session_id, '') FROM sessions WHERE id = ?`,
		src.ID).Scan(&srcAfter)
	if srcAfter != "source-engine-cursor" {
		t.Errorf("fork mutated source engine_session_id: got %q want %q",
			srcAfter, "source-engine-cursor")
	}
}

// TestSessions_ResumeWithoutCursor_NoSplice — a session whose agent
// died before emitting session.init has no captured cursor. Resume
// must still succeed (cold-start), with no `--resume` in the new
// spawn cmd. This is the pre-ADR-014 baseline behaviour preserved.
func TestSessions_ResumeWithoutCursor_NoSplice(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, oldAgentID := seedChannelAndAgent(t, s, "", "host-y")

	specYAML := "kind: claude-code\nbackend:\n  cmd: \"claude --model M\"\n"
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "no cursor",
			"agent_id":        oldAgentID,
			"worktree_path":   "/tmp/wt/no-cursor",
			"spawn_spec_yaml": specYAML,
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID,
		map[string]any{"status": "crashed"})

	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/resume", nil)
	if status != http.StatusOK {
		t.Fatalf("resume: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgentID, _ := resp["new_agent_id"].(string)

	var newSpec string
	_ = s.db.QueryRow(
		`SELECT spawn_spec_yaml FROM agent_spawns
		   WHERE child_agent_id = ?`, newAgentID).Scan(&newSpec)
	if strings.Contains(newSpec, "--resume") {
		t.Errorf("cold resume spliced unexpectedly:\n%s", newSpec)
	}
}
