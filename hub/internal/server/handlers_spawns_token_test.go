package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/termipod/hub/internal/auth"
)

// W2.2: handleListSpawns must surface the plaintext mcp_token to the
// host-runner (which needs it to materialize the agent's .mcp.json) but
// never to user/owner-kind callers — that token is an agent-scoped
// bearer and a dashboard caller should not be able to harvest it.
func TestListSpawns_McpTokenGatedByHostKind(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {
			"claude-code": {"installed": true, "supports": ["M2","M4"]}
		}
	}`)

	if _, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "tok-test",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\n",
	}); err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	// Mint a host-kind token alongside the owner token from Init().
	hostScope, _ := json.Marshal(map[string]any{
		"team":    defaultTeamID,
		"role":    "host",
		"host_id": hostID,
	})
	hostTok := auth.NewToken()
	if err := auth.InsertToken(context.Background(), c.s.db,
		"host", string(hostScope), hostTok, NewID(), NowUTC()); err != nil {
		t.Fatalf("seed host token: %v", err)
	}

	listURL := c.srv.URL + "/v1/teams/" + defaultTeamID + "/agents/spawns"

	// Owner-kind caller must not see the plaintext bearer.
	status, raw := rawCall(t, c.token, listURL, "GET", nil)
	if status != 200 {
		t.Fatalf("owner list = %d body=%s", status, raw)
	}
	var ownerSpawns []spawnListOut
	if err := json.Unmarshal(raw, &ownerSpawns); err != nil {
		t.Fatalf("decode owner: %v body=%s", err, raw)
	}
	if len(ownerSpawns) == 0 {
		t.Fatalf("owner list empty: %s", raw)
	}
	for _, sp := range ownerSpawns {
		if sp.McpToken != "" {
			t.Fatalf("owner caller leaked mcp_token=%q for spawn %s", sp.McpToken, sp.SpawnID)
		}
	}

	// Host-kind caller (the host-runner) must see the plaintext.
	status, raw = rawCall(t, hostTok, listURL, "GET", nil)
	if status != 200 {
		t.Fatalf("host list = %d body=%s", status, raw)
	}
	var hostSpawns []spawnListOut
	if err := json.Unmarshal(raw, &hostSpawns); err != nil {
		t.Fatalf("decode host: %v body=%s", err, raw)
	}
	if len(hostSpawns) == 0 {
		t.Fatalf("host list empty: %s", raw)
	}
	gotPlaintext := false
	for _, sp := range hostSpawns {
		if sp.Handle == "tok-test" && sp.McpToken != "" {
			gotPlaintext = true
			break
		}
	}
	if !gotPlaintext {
		t.Fatalf("host caller missing mcp_token: %+v", hostSpawns)
	}
}
