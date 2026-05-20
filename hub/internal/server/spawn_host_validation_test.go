package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSpawn_RequiresHostID covers the agents.spawn host_id boundary
// added after the v1.0.636 incident: a spawn with no host_id (or a
// host_id that doesn't exist / is offline) is rejected at the REST
// handler with HTTP 422 + a hint pointing at hosts.list, instead of
// silently creating a `host_id=NULL` row that no host-runner ever
// claims. Three cases:
//
//   1. missing host_id  → 422, hint names hosts_list
//   2. unknown host_id → 422, error mentions the id
//   3. offline host_id → 422, error mentions "not online"
//
// The MCP-path schema validator covers the same field at the
// dispatcher boundary (TestValidateArgs_RequiredFields); this test
// covers the REST surface so REST callers (mobile bootstrap, internal
// handlers) get the same fail-fast.
func TestSpawn_RequiresHostID(t *testing.T) {
	s, token := newA2ATestServer(t)

	// (1) Missing host_id.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":    "worker",
			"kind":            "claude-code",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("missing host_id: status=%d body=%s; want 422", status, body)
	}
	var resp struct {
		Error string `json:"error"`
		Hint  struct {
			HintText string `json:"hint_text"`
			SeeTool  string `json:"see_tool"`
		} `json:"hint"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if !strings.Contains(resp.Error, "host_id") {
		t.Errorf("error should mention host_id; got %q", resp.Error)
	}
	if resp.Hint.SeeTool != "hosts_list" {
		t.Errorf("hint should point at hosts_list; got %q", resp.Hint.SeeTool)
	}

	// (2) host_id refers to a host that doesn't exist for this team.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":    "worker",
			"kind":            "claude-code",
			"host_id":         "host-does-not-exist",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown host_id: status=%d body=%s; want 422", status, body)
	}
	if !strings.Contains(string(body), "host-does-not-exist") {
		t.Errorf("error should name the unknown host; got %s", body)
	}

	// (3) host_id refers to a host that's offline.
	seedTestHost(t, s, defaultTeamID, "host-offline", "h-off")
	if _, err := s.db.Exec(
		`UPDATE hosts SET status='offline' WHERE id = ?`, "host-offline",
	); err != nil {
		t.Fatalf("offline flip: %v", err)
	}
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":    "worker",
			"kind":            "claude-code",
			"host_id":         "host-offline",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("offline host: status=%d body=%s; want 422", status, body)
	}
	if !strings.Contains(string(body), "not online") {
		t.Errorf("error should say not online; got %s", body)
	}

	// Happy path: online host + valid args → no 422 from the host gate.
	// (The spawn may still fail later for other reasons in this minimal
	// fixture — what matters is that the host_id boundary lets it past.)
	seedTestHost(t, s, defaultTeamID, "host-online", "h-on")
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":    "worker",
			"kind":            "claude-code",
			"host_id":         "host-online",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	// 422 from the host gate is the failure mode this test is asserting
	// against. 201 (created) or any non-422 means the host gate let us
	// past. We accept 5xx too — those would be a different bug, not the
	// boundary we're checking here.
	if status == http.StatusUnprocessableEntity && strings.Contains(string(body), "host_id") {
		t.Errorf("happy path: still rejected by host gate: %s", body)
	}
}
