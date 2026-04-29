package server

import (
	"context"
	"encoding/json"
	"testing"
)

// TestDecide_PermissionPromptFansOutAttentionReply pins the slice-4
// extension to dispatchAttentionReply: attentions with
// kind=permission_prompt now fan out an input.attention_reply event
// the same way the three async kinds do, because codex's
// permission_prompt is turn-based on the wire (ADR-012 D3 update to
// ADR-011 D6). Without this fan-out, the codex driver never wakes
// up to send the JSON-RPC response and the agent stalls.
//
// Setup mirrors the help_request test: spawn an agent with an
// auto-opened session, insert a permission_prompt attention scoped
// to that session, /decide it, and assert the agent_events table
// has the matching input.attention_reply row.
func TestDecide_PermissionPromptFansOutAttentionReply(t *testing.T) {
	c := newE2E(t)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"codex": {"installed": true, "supports": ["M2"]}}
	}`)
	out, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle:     "codex-steward",
		Kind:            "codex",
		HostID:          hostID,
		SpawnSpec:       "driving_mode: M2\n",
		AutoOpenSession: true,
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	var sessionID string
	if err := c.s.db.QueryRow(
		`SELECT id FROM sessions WHERE current_agent_id = ?`, out.AgentID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("session lookup: %v", err)
	}

	// Insert the permission_prompt directly via the /attention POST —
	// this is the surface the codex AppServerDriver uses to bridge a
	// server-initiated approval request to a hub attention row.
	pending, _ := json.Marshal(map[string]any{
		"engine":     "codex",
		"method":     "item/commandExecution/requestApproval",
		"jsonrpc_id": 42,
	})
	createBody := map[string]any{
		"scope_kind":      "team",
		"kind":            "permission_prompt",
		"summary":         "Run: rm -rf /repo/build",
		"severity":        "major",
		"session_id":      sessionID,
		"actor_handle":    "codex-steward",
		"pending_payload": json.RawMessage(pending),
	}
	status, body := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention", createBody)
	if status != 201 {
		t.Fatalf("create attention: status %d body %v", status, body)
	}
	createdID, _ := body["id"].(string)
	if createdID == "" {
		t.Fatalf("create attention: empty id (body %v)", body)
	}

	// Snapshot agent seq, then /decide.
	var seqBefore int64
	_ = c.s.db.QueryRow(`
		SELECT COALESCE(MAX(seq), 0) FROM agent_events WHERE agent_id = ?`,
		out.AgentID,
	).Scan(&seqBefore)

	status, _ = c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+createdID+"/decide",
		map[string]any{
			"decision": "approve",
			"by":       "@principal",
		})
	if status != 200 {
		t.Fatalf("decide = %d", status)
	}

	// Verify input.attention_reply landed for the agent. Same shape
	// the three other async kinds emit: producer=user, request_id
	// pointing at the attention, kind=permission_prompt so the
	// codex driver routes to its JSON-RPC response path instead of
	// the user-text turn path.
	rows, err := c.s.db.Query(`
		SELECT kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND seq > ?
		 ORDER BY seq ASC`, out.AgentID, seqBefore)
	if err != nil {
		t.Fatalf("agent_events query: %v", err)
	}
	defer rows.Close()
	var matched bool
	for rows.Next() {
		var kind, producer, payload string
		if err := rows.Scan(&kind, &producer, &payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if kind != "input.attention_reply" {
			continue
		}
		if producer != "user" {
			t.Errorf("attention_reply producer = %q; want user", producer)
		}
		var p map[string]any
		_ = json.Unmarshal([]byte(payload), &p)
		if p["request_id"] != createdID {
			t.Errorf("request_id = %v; want %s", p["request_id"], createdID)
		}
		if p["kind"] != "permission_prompt" {
			t.Errorf("kind = %v; want permission_prompt", p["kind"])
		}
		if p["decision"] != "approve" {
			t.Errorf("decision = %v; want approve", p["decision"])
		}
		matched = true
		break
	}
	if !matched {
		t.Fatal("no input.attention_reply event posted after permission_prompt /decide")
	}

	// Also verify the attention's pending_payload survived through
	// the create handler — slice-4 widened the column write so the
	// audit trail (and the slice-4 detail screen) sees the codex
	// context.
	var stored string
	_ = c.s.db.QueryRow(
		`SELECT COALESCE(pending_payload_json, '') FROM attention_items WHERE id = ?`,
		createdID,
	).Scan(&stored)
	if stored == "" {
		t.Errorf("pending_payload_json: empty after create — slice-4 widening regressed")
	}
}
