package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// osReadFile is a tiny indirection so the helper below doesn't need to
// drag more imports into the call site.
var osReadFile = os.ReadFile

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
	out, jerr := s.mcpRequestApproval(context.Background(), defaultTeamID, agentID, args)
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
	_, jerr := s.mcpRequestDecision(context.Background(), defaultTeamID, agentID, args)
	if jerr == nil || jerr.Code != -32602 {
		t.Errorf("want -32602, got %+v", jerr)
	}
}

// request_decision: stores options in pending_payload_json so the
// resolver UI can render one button per option, and long-polls until
// the user picks an option — returning the chosen option_id back to
// the agent so request_decision is no longer fire-and-forget.
func TestMCP_RequestDecision_StoresOptionsAndLongPolls(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"question": "pick a color",
		"options":  []string{"red", "green", "blue"},
	})

	type result struct {
		out  any
		jerr *jrpcError
	}
	resCh := make(chan result, 1)
	go func() {
		out, jerr := s.mcpRequestDecision(
			context.Background(), defaultTeamID, agentID, args)
		resCh <- result{out: out, jerr: jerr}
	}()

	// The MCP call is now a long-poll; the attention row needs to exist
	// before we can decide on it. Spin briefly until it shows up.
	var attnID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var id string
		err := s.db.QueryRow(
			`SELECT id FROM attention_items WHERE kind = 'decision' ORDER BY created_at DESC LIMIT 1`,
		).Scan(&id)
		if err == nil {
			attnID = id
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attnID == "" {
		t.Fatal("attention row never inserted")
	}

	// pending_payload_json carries the structured options + agent_id so
	// the mobile decision card can render option buttons and route them
	// to the right agent's transcript.
	var payloadJSON string
	_ = s.db.QueryRow(
		`SELECT COALESCE(pending_payload_json, '') FROM attention_items WHERE id = ?`,
		attnID,
	).Scan(&payloadJSON)
	var payload map[string]any
	_ = json.Unmarshal([]byte(payloadJSON), &payload)
	gotOpts, _ := payload["options"].([]any)
	if len(gotOpts) != 3 {
		t.Fatalf("payload.options len=%d; want 3 (raw=%s)", len(gotOpts), payloadJSON)
	}
	if (payload["agent_id"]).(string) != agentID {
		t.Errorf("payload.agent_id = %v; want %s", payload["agent_id"], agentID)
	}

	// User picks "green" — flows through the same decide handler the
	// approve/reject buttons use, with option_id appended.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attnID+"/decide",
		map[string]any{
			"decision":  "approve",
			"by":        "@user",
			"option_id": "green",
		})
	if status != http.StatusOK {
		t.Fatalf("decide: status=%d body=%s", status, body)
	}

	// Long-poll should have unblocked with the chosen option.
	select {
	case r := <-resCh:
		if r.jerr != nil {
			t.Fatalf("request_decision: %+v", r.jerr)
		}
		picked := firstFieldFromMCPResult(t, r.out, "option_id")
		if picked != "green" {
			t.Errorf("returned option_id = %q; want green", picked)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_decision never returned after decide")
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
	out, jerr := s.mcpTemplatesPropose(context.Background(), defaultTeamID, agentID, args)
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

// Approving a template_proposal installs the proposed body to
// <dataRoot>/team/templates/<category>/<name>.yaml so the next
// templates.list picks it up.
func TestMCP_TemplatesPropose_ApproveInstalls(t *testing.T) {
	s, dataRoot := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	body := "handle: nurse\nrole: support\n"
	args, _ := json.Marshal(map[string]any{
		"category": "agents",
		"name":     "nurse.v1",
		"content":  body,
	})
	out, jerr := s.mcpTemplatesPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("propose: %+v", jerr)
	}
	attnID := firstFieldFromMCPResult(t, out, "attention_id")

	// Simulate the reviewer pressing "Approve" in the mobile UI.
	installed, err := s.installProposedTemplate(mustPendingPayload(t, s, attnID))
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	var res map[string]any
	_ = json.Unmarshal(installed, &res)
	path, _ := res["path"].(string)
	if path == "" {
		t.Fatalf("install result missing path: %s", installed)
	}
	got, err := readFile(t, path)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if got != body {
		t.Errorf("installed body mismatch:\nwant %q\ngot  %q", body, got)
	}
	// Sanity: file landed under the expected team templates dir.
	if !strings.HasPrefix(path, dataRoot) {
		t.Errorf("path outside dataRoot: %s (data=%s)", path, dataRoot)
	}
}

func mustPendingPayload(t *testing.T, s *Server, attnID string) string {
	t.Helper()
	var payload string
	if err := s.db.QueryRow(
		`SELECT COALESCE(pending_payload_json, '') FROM attention_items WHERE id = ?`,
		attnID,
	).Scan(&payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if payload == "" {
		t.Fatalf("attention %s has no pending_payload_json", attnID)
	}
	return payload
}

func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := osReadFile(path)
	return string(b), err
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

// permission_prompt: an Approve decision recorded in decisions_json
// resolves the long-poll into {behavior:"allow", updatedInput}.
func TestMCP_PermissionPrompt_ApproveReturnsAllow(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"tool_name": "Task", // tier=significant per tiers.go; escalates to attention
		"input":     map[string]any{"command": "echo hi"},
	})

	type res struct {
		out  any
		jerr *jrpcError
	}
	done := make(chan res, 1)
	go func() {
		o, e := s.mcpPermissionPrompt(context.Background(), defaultTeamID, agentID, args)
		done <- res{out: o, jerr: e}
	}()

	// Wait for the attention_item to land, then resolve via decisions_json.
	var attnID string
	for i := 0; i < 50; i++ {
		_ = s.db.QueryRow(
			`SELECT id FROM attention_items WHERE kind = 'permission_prompt' ORDER BY created_at DESC LIMIT 1`,
		).Scan(&attnID)
		if attnID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attnID == "" {
		t.Fatal("attention_items row never appeared")
	}
	decisions, _ := json.Marshal([]map[string]any{{
		"at": "now", "by": "@principal", "decision": "approve",
	}})
	if _, err := s.db.Exec(
		`UPDATE attention_items SET status='resolved', decisions_json = ? WHERE id = ?`,
		string(decisions), attnID,
	); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	r := <-done
	if r.jerr != nil {
		t.Fatalf("permission_prompt: %+v", r.jerr)
	}
	body := mcpResultTextBody(t, r.out)
	if !strings.Contains(body, `"behavior": "allow"`) {
		t.Errorf("expected allow, got %s", body)
	}
	if !strings.Contains(body, `"command"`) {
		t.Errorf("updatedInput should pass through input, got %s", body)
	}
}

// permission_prompt: a Reject decision returns {behavior:"deny", message}.
func TestMCP_PermissionPrompt_RejectReturnsDeny(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"tool_name": "Task", // tier=significant per tiers.go; escalates to attention
		"input":     map[string]any{"command": "rm -rf /"},
	})

	type res struct {
		out  any
		jerr *jrpcError
	}
	done := make(chan res, 1)
	go func() {
		o, e := s.mcpPermissionPrompt(context.Background(), defaultTeamID, agentID, args)
		done <- res{out: o, jerr: e}
	}()

	var attnID string
	for i := 0; i < 50; i++ {
		_ = s.db.QueryRow(
			`SELECT id FROM attention_items WHERE kind = 'permission_prompt' ORDER BY created_at DESC LIMIT 1`,
		).Scan(&attnID)
		if attnID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attnID == "" {
		t.Fatal("attention_items row never appeared")
	}
	decisions, _ := json.Marshal([]map[string]any{{
		"at": "now", "by": "@principal", "decision": "reject",
		"reason": "looks dangerous",
	}})
	if _, err := s.db.Exec(
		`UPDATE attention_items SET status='resolved', decisions_json = ? WHERE id = ?`,
		string(decisions), attnID,
	); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	r := <-done
	if r.jerr != nil {
		t.Fatalf("permission_prompt: %+v", r.jerr)
	}
	body := mcpResultTextBody(t, r.out)
	if !strings.Contains(body, `"behavior": "deny"`) {
		t.Errorf("expected deny, got %s", body)
	}
	if !strings.Contains(body, "looks dangerous") {
		t.Errorf("expected reason in message, got %s", body)
	}
}

// permission_prompt: missing tool_name → -32602.
func TestMCP_PermissionPrompt_RequiresToolName(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"input": map[string]any{"x": 1},
	})
	_, jerr := s.mcpPermissionPrompt(context.Background(), defaultTeamID, agentID, args)
	if jerr == nil || jerr.Code != -32602 {
		t.Errorf("want -32602, got %+v", jerr)
	}
}

// permission_prompt: trivial-tier tool (Read) auto-allows immediately,
// without creating an attention_items row. Routine tools follow the
// same path. This is the W1.A tier-gating contract — director sees
// significant+ only.
func TestMCP_PermissionPrompt_TrivialAutoAllows(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"tool_name": "Read",
		"input":     map[string]any{"path": "/tmp/x"},
	})

	out, jerr := s.mcpPermissionPrompt(
		context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("permission_prompt: %+v", jerr)
	}
	body := mcpResultTextBody(t, out)
	if !strings.Contains(body, `"behavior": "allow"`) {
		t.Errorf("expected immediate allow, got %s", body)
	}
	if !strings.Contains(body, "auto-allowed") {
		t.Errorf("expected auto-allow message, got %s", body)
	}

	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM attention_items WHERE kind = 'permission_prompt'`,
	).Scan(&n)
	if n != 0 {
		t.Errorf("trivial tier should NOT create attention; got %d row(s)", n)
	}
}

func TestMCP_PermissionPrompt_RoutineAutoAllows(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"tool_name": "Edit",
		"input":     map[string]any{"path": "/tmp/x", "content": "hi"},
	})

	out, jerr := s.mcpPermissionPrompt(
		context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("permission_prompt: %+v", jerr)
	}
	body := mcpResultTextBody(t, out)
	if !strings.Contains(body, `"behavior": "allow"`) {
		t.Errorf("expected immediate allow, got %s", body)
	}

	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM attention_items WHERE kind = 'permission_prompt'`,
	).Scan(&n)
	if n != 0 {
		t.Errorf("routine tier should NOT create attention; got %d row(s)", n)
	}
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
