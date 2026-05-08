package server

import (
	"context"
	"errors"
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

// agentSeed bundles the inputs seedAgentWithSession needs to set up a
// (agent, session) pair. Spec is the rendered spawn_spec_yaml; the
// helper stores it on both agent_spawns and sessions so the helper
// reads the canonical post-resume copy from sessions.
type agentSeed struct {
	Kind   string
	Handle string
	Spec   string
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
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (id, team_id, handle, kind, status,
		                    host_id, driving_mode, created_at)
		VALUES (?, ?, ?, ?, 'running', ?, 'M2', ?)`,
		agentID, defaultTeamID, seed.Handle, seed.Kind, hostID, now); err != nil {
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
