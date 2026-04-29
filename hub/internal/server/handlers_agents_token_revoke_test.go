package server

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"
)

// Spawning an agent mints a kind='agent' auth_tokens row scoped to that
// agent_id. Without revoke-on-terminate, every spawn → terminate cycle
// leaves the row valid forever — and pause/resume compounds it (each
// resume mints another). These tests pin the lifecycle so the Auth
// screen + the /mcp/{token} resolver agree on which tokens are dead.

// agentTokenRevokedAt reads the revoked_at column for whichever auth_tokens
// row was minted for agentID. Returns "" if not revoked, error if missing.
func agentTokenRevokedAt(t *testing.T, db *sql.DB, agentID string) string {
	t.Helper()
	var revoked sql.NullString
	err := db.QueryRow(`
		SELECT revoked_at FROM auth_tokens
		 WHERE kind = 'agent'
		   AND json_extract(scope_json, '$.agent_id') = ?`,
		agentID,
	).Scan(&revoked)
	if err != nil {
		t.Fatalf("lookup agent token for %s: %v", agentID, err)
	}
	if revoked.Valid {
		return revoked.String
	}
	return ""
}

func TestPatchAgent_TerminateRevokesMCPToken(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)

	out, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "revoke-test",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	if got := agentTokenRevokedAt(t, c.s.db, out.AgentID); got != "" {
		t.Fatalf("freshly-spawned agent token should not be revoked; got revoked_at=%q", got)
	}

	// PATCH status=terminated mirrors what host-runner POSTs after a kill.
	status, _ := c.call("PATCH",
		"/v1/teams/"+c.teamID+"/agents/"+out.AgentID,
		map[string]any{"status": "terminated"})
	if status != 200 && status != 204 {
		t.Fatalf("PATCH terminate = %d", status)
	}

	if got := agentTokenRevokedAt(t, c.s.db, out.AgentID); got == "" {
		t.Fatalf("agent token should be revoked after PATCH terminate")
	}

	// A redundant PATCH (host-runner retries, double-kill) must not error
	// or unrevoke; revoked_at stays the first-revoke timestamp.
	first := agentTokenRevokedAt(t, c.s.db, out.AgentID)
	status, _ = c.call("PATCH",
		"/v1/teams/"+c.teamID+"/agents/"+out.AgentID,
		map[string]any{"status": "terminated"})
	if status != 200 && status != 204 {
		t.Fatalf("redundant PATCH = %d", status)
	}
	if got := agentTokenRevokedAt(t, c.s.db, out.AgentID); got != first {
		t.Fatalf("revoked_at flipped on redundant terminate: was %q now %q", first, got)
	}
}

func TestSpawn_SessionSwapRevokesPriorAgentToken(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)

	first, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle:     "swap-steward",
		Kind:            "claude-code",
		HostID:          hostID,
		SpawnSpec:       "driving_mode: M2\n",
		AutoOpenSession: true,
	})
	if err != nil {
		t.Fatalf("first DoSpawn: %v", err)
	}

	// Find the session that auto-opened so we can target the swap path.
	var sessionID string
	if err := c.s.db.QueryRow(
		`SELECT id FROM sessions WHERE current_agent_id = ?`, first.AgentID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("lookup auto-opened session: %v", err)
	}

	// Swap = same handle, session_id targets the live session.
	second, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "swap-steward",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\n",
		SessionID:   sessionID,
	})
	if err != nil {
		t.Fatalf("swap DoSpawn: %v", err)
	}

	if got := agentTokenRevokedAt(t, c.s.db, first.AgentID); got == "" {
		t.Fatalf("prior agent token should be revoked after session-swap")
	}
	if got := agentTokenRevokedAt(t, c.s.db, second.AgentID); got != "" {
		t.Fatalf("new agent token should be live; got revoked_at=%q", got)
	}
}
