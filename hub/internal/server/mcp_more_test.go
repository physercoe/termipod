package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Covers the second batch of MCP tools added in mcp_more.go. Each test
// seeds the minimum schema it needs and then calls the dispatch helpers
// directly — going through /mcp/{token} would exercise JSON-RPC plumbing
// which is already covered elsewhere.

// delegate: posts a message event with to_ids populated and a
// metadata.context_refs key so the receiver can see what the parent was
// pointing at.
func TestMCP_Delegate_RecordsMessageWithContextRefs(t *testing.T) {
	s, _ := newTestServer(t)
	channelID, agentID := seedChannelAndAgent(t, s, "%1", "h1")

	args, _ := json.Marshal(map[string]any{
		"to":           "@worker-fe",
		"channel_id":   channelID,
		"text":         "pick up the FE bundle work",
		"context_refs": []string{"event:abc", "task:xyz"},
	})
	if _, jerr := s.mcpDelegate(context.Background(), agentID, args); jerr != nil {
		t.Fatalf("delegate: %+v", jerr)
	}
	var typ, toIDs, meta string
	if err := s.db.QueryRow(`
		SELECT type, to_ids_json, metadata_json FROM events
		WHERE channel_id = ? ORDER BY received_ts DESC LIMIT 1`,
		channelID).Scan(&typ, &toIDs, &meta); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if typ != "delegate" {
		t.Errorf("type = %q, want delegate", typ)
	}
	if !strings.Contains(toIDs, "@worker-fe") {
		t.Errorf("to_ids missing recipient: %s", toIDs)
	}
	if !strings.Contains(meta, "event:abc") || !strings.Contains(meta, "task:xyz") {
		t.Errorf("metadata missing context_refs: %s", meta)
	}
}

// request_approval: the attention_item shows up with kind=approval_request
// and a severity derived from the tier when one wasn't explicitly set.
func TestMCP_RequestApproval_TierDefaultsSeverity(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"tier":    "critical",
		"summary": "please approve prod deploy",
	})
	out, jerr := s.mcpRequestApproval(context.Background(), agentID, args)
	if jerr != nil {
		t.Fatalf("request_approval: %+v", jerr)
	}
	id := firstFieldFromMCPResult(t, out, "id")
	var kind, severity, status string
	if err := s.db.QueryRow(
		`SELECT kind, severity, status FROM attention_items WHERE id = ?`, id,
	).Scan(&kind, &severity, &status); err != nil {
		t.Fatalf("read attention: %v", err)
	}
	if kind != "approval_request" || severity != "critical" || status != "open" {
		t.Errorf("row mismatch: kind=%s severity=%s status=%s", kind, severity, status)
	}
}

// request_decision: missing options -> -32602 (agents calling this with no
// options almost always mean to call request_approval instead).
func TestMCP_RequestDecision_RequiresOptions(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{"question": "which?"})
	_, jerr := s.mcpRequestDecision(context.Background(), agentID, args)
	if jerr == nil || jerr.Code != -32602 {
		t.Errorf("want -32602, got %+v", jerr)
	}
}

// attach: roundtrip a tiny blob, check the row + file landed.
func TestMCP_Attach_StoresBlob(t *testing.T) {
	s, _ := newTestServer(t)
	payload := []byte("hello blob")
	args, _ := json.Marshal(map[string]any{
		"filename":       "hello.txt",
		"content_base64": base64.StdEncoding.EncodeToString(payload),
		"mime":           "text/plain",
	})
	out, jerr := s.mcpAttach(context.Background(), args)
	if jerr != nil {
		t.Fatalf("attach: %+v", jerr)
	}
	sha := firstFieldFromMCPResult(t, out, "sha256")
	var size int
	var mime string
	if err := s.db.QueryRow(`SELECT size, mime FROM blobs WHERE sha256 = ?`, sha).
		Scan(&size, &mime); err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if size != len(payload) || mime != "text/plain" {
		t.Errorf("blob mismatch: size=%d mime=%s", size, mime)
	}
}

// update_own_task_status: assignment check rejects foreign tasks.
func TestMCP_UpdateOwnTaskStatus_EnforcesAssignee(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// Seed a project + task assigned to someone else.
	projectID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, status, created_at)
		VALUES (?, ?, 'p', 'active', ?)`,
		projectID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	otherAgentID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, created_at)
		VALUES (?, ?, 'other', 'claude-code', ?)`,
		otherAgentID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed other agent: %v", err)
	}
	taskID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (
			id, project_id, title, body_md, status, assignee_id,
			created_by_id, created_at, updated_at
		) VALUES (?, ?, 't', '', 'open', ?, ?, ?, ?)`,
		taskID, projectID, otherAgentID, otherAgentID, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	args, _ := json.Marshal(map[string]any{"task_id": taskID, "status": "done"})
	_, jerr := s.mcpUpdateOwnTaskStatus(context.Background(), agentID, args)
	if jerr == nil {
		t.Fatalf("expected error for foreign task")
	}

	// Reassign to self — now it should succeed.
	if _, err := s.db.Exec(`UPDATE tasks SET assignee_id = ? WHERE id = ?`, agentID, taskID); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	if _, jerr := s.mcpUpdateOwnTaskStatus(context.Background(), agentID, args); jerr != nil {
		t.Fatalf("self-task update: %+v", jerr)
	}
	var status string
	_ = s.db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, taskID).Scan(&status)
	if status != "done" {
		t.Errorf("status = %q, want done", status)
	}
}

// templates_propose: writes a blob and files an attention_item of kind
// template_proposal so a reviewer can approve/reject through the same
// Pending UI as other decisions.
func TestMCP_TemplatesPropose_FilesAttentionAndBlob(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"category":  "agents",
		"name":      "nurse.v1",
		"content":   "handle: nurse\nrole: support\n",
		"rationale": "need a dedicated first-responder role",
	})
	out, jerr := s.mcpTemplatesPropose(context.Background(), agentID, args)
	if jerr != nil {
		t.Fatalf("propose: %+v", jerr)
	}
	attnID := firstFieldFromMCPResult(t, out, "attention_id")
	sha := firstFieldFromMCPResult(t, out, "blob_sha256")

	var kind string
	if err := s.db.QueryRow(
		`SELECT kind FROM attention_items WHERE id = ?`, attnID,
	).Scan(&kind); err != nil || kind != "template_proposal" {
		t.Errorf("attention kind = %q (err=%v), want template_proposal", kind, err)
	}
	var size int
	if err := s.db.QueryRow(
		`SELECT size FROM blobs WHERE sha256 = ?`, sha,
	).Scan(&size); err != nil || size == 0 {
		t.Errorf("blob row missing / empty: err=%v size=%d", err, size)
	}
}

// pause_self / shutdown_self require a host binding — otherwise the host
// command queue would take orphaned rows that nothing consumes.
func TestMCP_PauseSelf_RequiresHostBinding(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "") // no host

	_, jerr := s.mcpPauseSelf(context.Background(), agentID, json.RawMessage(`{}`))
	if jerr == nil {
		t.Fatalf("expected error for host-less agent")
	}
}

func TestMCP_ShutdownSelf_EnqueuesTerminate(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "%5", "h1")

	out, jerr := s.mcpShutdownSelf(context.Background(), agentID,
		json.RawMessage(`{"reason":"done for the day"}`))
	if jerr != nil {
		t.Fatalf("shutdown_self: %+v", jerr)
	}
	if got := firstFieldFromMCPResult(t, out, "command"); got != "terminate" {
		t.Errorf("command = %q, want terminate", got)
	}
}

// list_agents returns the seeded agent for the team.
func TestMCP_ListAgents_ReturnsTeamAgents(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")
	out, jerr := s.mcpListAgents(context.Background(), defaultTeamID, json.RawMessage(`{}`))
	if jerr != nil {
		t.Fatalf("list_agents: %+v", jerr)
	}
	body := mcpResultTextBody(t, out)
	if !strings.Contains(body, agentID) {
		t.Errorf("agent id %q not in result: %s", agentID, body)
	}
}

// firstFieldFromMCPResult pulls a top-level string field out of the JSON-
// serialized MCP tool result (result.content[0].text is a JSON blob).
func firstFieldFromMCPResult(t *testing.T, out any, key string) string {
	t.Helper()
	body := mcpResultTextBody(t, out)
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal mcp body: %v — body=%s", err, body)
	}
	v, _ := m[key].(string)
	return v
}

func mcpResultTextBody(t *testing.T, out any) string {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", out)
	}
	content, ok := m["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result has no content: %+v", m)
	}
	entry, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] not a map: %T", content[0])
	}
	text, _ := entry["text"].(string)
	return text
}
