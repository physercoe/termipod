package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestRespawnWithSpecMutation_ClaudeModelSwap — happy path. A claude-code
// agent attached to a live session sees its spawn_spec_yaml's
// `--model X` rewritten to `--model Y`, the prior agent is terminated,
// and a fresh agent lands on the same session row with the mutated
// spec. Mirrors the picker→respawn flow that mobile triggers via
// POST /input set_model.
func TestRespawnWithSpecMutation_ClaudeModelSwap(t *testing.T) {
	srv, _ := newTestServer(t)

	priorAgentID, sessionID := seedAgentWithSession(t, srv, agentSeed{
		Kind:   "claude-code",
		Handle: "steward-x",
		Spec: `kind: steward
backend:
  kind: claude-code
  cmd: claude --model claude-3-5-sonnet --print --output-format stream-json
`,
	})

	if err := srv.respawnWithSpecMutation(context.Background(),
		priorAgentID, "model", "claude-3-7-opus"); err != nil {
		t.Fatalf("respawn: %v", err)
	}

	// Session row now points at a fresh agent (current_agent_id !=
	// priorAgentID) and the captured spec carries the new model.
	var newAgentID, newSpec string
	if err := srv.db.QueryRow(
		`SELECT current_agent_id, spawn_spec_yaml FROM sessions WHERE id = ?`,
		sessionID).Scan(&newAgentID, &newSpec); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if newAgentID == priorAgentID || newAgentID == "" {
		t.Errorf("current_agent_id = %q (prior = %q); expected fresh agent",
			newAgentID, priorAgentID)
	}
	if !strings.Contains(newSpec, "--model claude-3-7-opus") {
		t.Errorf("session spec missing new model:\n%s", newSpec)
	}
	if strings.Contains(newSpec, "claude-3-5-sonnet") {
		t.Errorf("session spec still carries old model:\n%s", newSpec)
	}

	// Prior agent must be terminated so the (team, handle) live-handle
	// uniqueness index frees up before the new INSERT — DoSpawn's swap
	// branch enforces this in-tx.
	var priorStatus string
	_ = srv.db.QueryRow(`SELECT status FROM agents WHERE id = ?`,
		priorAgentID).Scan(&priorStatus)
	if priorStatus != "terminated" {
		t.Errorf("prior agent status = %q; want terminated", priorStatus)
	}
}

// TestRespawnWithSpecMutation_UnknownFamily — gemini-cli routes via
// rpc/per_turn_argv at the handler level, so it should never reach
// the helper. If something does call us anyway, surface the typed
// error so the caller maps it to a 422 rather than a 500.
func TestRespawnWithSpecMutation_UnknownFamily(t *testing.T) {
	srv, _ := newTestServer(t)
	agentID, _ := seedAgentWithSession(t, srv, agentSeed{
		Kind:   "gemini-cli",
		Handle: "steward-g",
		Spec: `kind: steward
backend:
  kind: gemini-cli
  cmd: gemini --acp
`,
	})
	err := srv.respawnWithSpecMutation(context.Background(),
		agentID, "model", "gemini-2.5-flash")
	if !errors.Is(err, errUnknownFamilyField) {
		t.Fatalf("err = %v; want errUnknownFamilyField", err)
	}
}

// TestRespawnWithSpecMutation_FlagMissing — claude family but the
// rendered spec lacks `--model`. Returns errFlagNotInCmd so the
// handler surfaces a 422 with a clear "template doesn't expose this
// flag" message rather than silently mutating nothing.
func TestRespawnWithSpecMutation_FlagMissing(t *testing.T) {
	srv, _ := newTestServer(t)
	agentID, _ := seedAgentWithSession(t, srv, agentSeed{
		Kind:   "claude-code",
		Handle: "steward-noflag",
		Spec: `kind: steward
backend:
  kind: claude-code
  cmd: claude --print --output-format stream-json
`,
	})
	err := srv.respawnWithSpecMutation(context.Background(),
		agentID, "model", "claude-3-7-opus")
	if !errors.Is(err, errFlagNotInCmd) {
		t.Fatalf("err = %v; want errFlagNotInCmd", err)
	}
}

// TestRespawnWithSpecMutation_StewardResolvesEngine — a STEWARD, which is
// the agent class this product is built around and the one every earlier
// test in this file failed to represent.
//
// `agents.kind` is the engine only for a direct spawn. For a steward it is
// the persona template (`steward.claude-m4`), and the engine family lives in
// `backend_json.kind` — the column spawn writes for exactly this reason. The
// flag table is keyed by FAMILY, so looking it up with `kind` meant every
// steward's model/mode switch returned errUnknownFamilyField and surfaced as
// "this engine doesn't support runtime switching" — silently wrong, for the
// only agents that matter.
//
// The separating input the older tests could not provide: Kind is a template
// id, and only Backend names the engine. A test seeding Kind:"claude-code"
// passes whether the code reads `kind` or `backend_json`.
func TestRespawnWithSpecMutation_StewardResolvesEngine(t *testing.T) {
	srv, _ := newTestServer(t)
	agentID, sessionID := seedAgentWithSession(t, srv, agentSeed{
		Kind:    "steward.claude-m4",
		Backend: "claude-code",
		Handle:  "steward-persona",
		Spec: `kind: steward
backend:
  kind: claude-code
  cmd: claude --model claude-3-5-sonnet --print --output-format stream-json
`,
	})

	if err := srv.respawnWithSpecMutation(context.Background(),
		agentID, "model", "claude-3-7-opus"); err != nil {
		t.Fatalf("steward respawn: %v (a persona template must still resolve to its engine)", err)
	}

	var newSpec string
	if err := srv.db.QueryRow(
		`SELECT spawn_spec_yaml FROM sessions WHERE id = ?`, sessionID).Scan(&newSpec); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !strings.Contains(newSpec, "--model claude-3-7-opus") {
		t.Errorf("session spec missing new model:\n%s", newSpec)
	}
}

// TestRespawnWithSpecMutation_StewardKeepsResumeCursor — the trap inside the
// fix, and the reason both family-keyed lookups had to move together.
//
// `spliceResume` also takes a FAMILY. While step 2 rejected stewards outright,
// this line was unreachable for them; the moment step 2 learned to resolve a
// steward's engine, a persona template reaching `spliceResume` would match no
// family, return the spec untouched, and cold-start the agent — trading a loud
// 422 for a SILENT transcript break. The file's own antigravity regression test
// had predicted exactly this shape of failure.
func TestRespawnWithSpecMutation_StewardKeepsResumeCursor(t *testing.T) {
	srv, _ := newTestServer(t)
	agentID, sessionID := seedAgentWithSession(t, srv, agentSeed{
		Kind:    "steward.claude-m4",
		Backend: "claude-code",
		Handle:  "steward-resume",
		Spec: `kind: steward
backend:
  kind: claude-code
  cmd: claude --model claude-3-5-sonnet --print --output-format stream-json
`,
	})
	if _, err := srv.db.Exec(
		`UPDATE sessions SET engine_session_id = ? WHERE id = ?`,
		"engine-sess-42", sessionID); err != nil {
		t.Fatalf("seed engine_session_id: %v", err)
	}

	if err := srv.respawnWithSpecMutation(context.Background(),
		agentID, "model", "claude-3-7-opus"); err != nil {
		t.Fatalf("steward respawn: %v", err)
	}

	var newSpec string
	if err := srv.db.QueryRow(
		`SELECT spawn_spec_yaml FROM sessions WHERE id = ?`, sessionID).Scan(&newSpec); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !strings.Contains(newSpec, "--resume engine-sess-42") {
		t.Errorf("steward respawn dropped the resume cursor — the new agent cold-starts:\n%s", newSpec)
	}
	if !strings.Contains(newSpec, "--model claude-3-7-opus") {
		t.Errorf("model not mutated:\n%s", newSpec)
	}
}

// TestRespawnWithSpecMutation_LegacyRowFallsBackToKind — a row written before
// backend_json was populated carries `{}`. Those must keep working off `kind`,
// so the fix may not simply swap one source for the other.
func TestRespawnWithSpecMutation_LegacyRowFallsBackToKind(t *testing.T) {
	srv, _ := newTestServer(t)
	agentID, sessionID := seedAgentWithSession(t, srv, agentSeed{
		Kind:   "claude-code",
		Handle: "legacy-direct",
		Spec: `kind: agent
backend:
  kind: claude-code
  cmd: claude --model claude-3-5-sonnet --print --output-format stream-json
`,
	})
	if err := srv.respawnWithSpecMutation(context.Background(),
		agentID, "model", "claude-3-7-opus"); err != nil {
		t.Fatalf("legacy respawn: %v", err)
	}
	var newSpec string
	_ = srv.db.QueryRow(`SELECT spawn_spec_yaml FROM sessions WHERE id = ?`, sessionID).Scan(&newSpec)
	if !strings.Contains(newSpec, "--model claude-3-7-opus") {
		t.Errorf("legacy row must still resolve via kind:\n%s", newSpec)
	}
}

// TestResolveRuntimeModeSwitch_StewardRoutes — the same defect one layer up,
// at the gate. `resolveRuntimeModeSwitch` looks the family up to read its
// `runtime_mode_switch` table; with `kind` a steward matched no family and the
// handler answered 422 "engine does not support runtime mode switching". So
// the feature was dead for stewards at BOTH the routing gate and the executor
// — fixing either alone leaves it broken.
func TestResolveRuntimeModeSwitch_StewardRoutes(t *testing.T) {
	srv, _ := newTestServer(t)
	for i, tc := range []struct {
		name    string
		kind    string
		backend string
		want    string
	}{
		{"steward over claude-code", "steward.claude-m4", "claude-code", "respawn"},
		{"steward over codex", "steward.codex", "codex", "respawn"},
		{"direct spawn still works", "claude-code", "", "respawn"},
		{"unknown engine stays unsupported", "steward.mystery", "no-such-engine", "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The handle must be unique per case: `agents` has a live-handle
			// uniqueness index, and NewID()'s leading chars are a timestamp
			// that repeats inside one millisecond.
			agentID, _ := seedAgentWithSession(t, srv, agentSeed{
				Kind:    tc.kind,
				Backend: tc.backend,
				Handle:  fmt.Sprintf("route-case-%d", i),
				Spec:    "kind: steward\n",
			})
			got, err := srv.resolveRuntimeModeSwitch(context.Background(), agentID)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("route = %q; want %q", got, tc.want)
			}
		})
	}
}

// agentSeed bundles the inputs seedAgentWithSession needs to set up a
// (agent, session) pair. Spec is the rendered spawn_spec_yaml; the
// helper stores it on both agent_spawns and sessions so the helper
// reads the canonical post-resume copy from sessions.
type agentSeed struct {
	Kind   string
	Handle string
	Spec   string
	// Backend is the engine family spawn writes into `agents.backend_json`
	// (handlers_agents.go:1567). It is what distinguishes a steward — whose
	// `Kind` is a persona template like `steward.claude-m4` — from a direct
	// engine spawn where `Kind` IS the family. Empty leaves the column at the
	// `{}` a pre-column row carries, which is the legacy-row case.
	Backend string
}

func seedAgentWithSession(t *testing.T, s *Server, seed agentSeed) (agentID, sessionID string) {
	t.Helper()
	ctx := context.Background()
	agentID = NewID()
	sessionID = NewID()
	now := NowUTC()
	hostID := NewID()

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO hosts (id, team_id, name, created_at)
		 VALUES (?, ?, ?, ?)`,
		hostID, defaultTeamID, "host-"+hostID[len(hostID)-6:], now); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	backendJSON := "{}"
	if seed.Backend != "" {
		backendJSON = `{"kind":"` + seed.Backend + `"}`
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (id, team_id, handle, kind, status,
		                    host_id, driving_mode, backend_json, created_at)
		VALUES (?, ?, ?, ?, 'running', ?, 'M2', ?, ?)`,
		agentID, defaultTeamID, seed.Handle, seed.Kind, hostID, backendJSON, now); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	// Spawn row anchors parent_agent_id lookups in the helper.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_spawns (id, child_agent_id, spawn_spec_yaml,
		                         spawn_authority_json, spawned_at)
		VALUES (?, ?, ?, '{}', ?)`,
		NewID(), agentID, seed.Spec, now); err != nil {
		t.Fatalf("seed spawn: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, team_id, current_agent_id, status,
		                     scope_kind, opened_at, last_active_at,
		                     spawn_spec_yaml)
		VALUES (?, ?, ?, 'active', 'agent', ?, ?, ?)`,
		sessionID, defaultTeamID, agentID, now, now, seed.Spec); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return agentID, sessionID
}
