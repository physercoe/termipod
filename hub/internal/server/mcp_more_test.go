package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	_, jerr := s.mcpRequestSelect(context.Background(), defaultTeamID, agentID, args)
	if jerr == nil || jerr.Code != -32602 {
		t.Errorf("want -32602, got %+v", jerr)
	}
}

// request_decision: stores options in pending_payload_json so the
// resolver UI can render one button per option, and long-polls until
// the user picks an option — returning the chosen option_id back to
// the agent so request_decision is no longer fire-and-forget.
// request_select is now turn-based (v1.0.338): the MCP call returns
// immediately with awaiting_response, and the principal's pick is
// delivered back as a fresh user turn via input.attention_reply when
// /decide resolves the attention. This test pins (a) the synchronous
// return shape, (b) the attention row carries the structured options,
// (c) /decide fans out an attention_reply with the picked option.
func TestMCP_RequestSelect_TurnBasedRoundTrip(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"question": "pick a color",
		"options":  []string{"red", "green", "blue"},
	})

	// Turn-based: must return synchronously, not block on a long-poll.
	start := time.Now()
	out, jerr := s.mcpRequestSelect(
		context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("request_select: %+v", jerr)
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("mcpRequestSelect held the call for %v; expected immediate return", elapsed)
	}
	if firstFieldFromMCPResult(t, out, "status") != "awaiting_response" {
		t.Fatalf("status field missing/wrong: %+v", out)
	}
	attnID := firstFieldFromMCPResult(t, out, "id")
	if attnID == "" {
		t.Fatalf("attention id missing from result")
	}

	// pending_payload_json still carries the structured options + agent_id
	// so the mobile decision card can render option buttons and route them
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

	// User picks "green" — flows through the same /decide handler the
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

	// The attention_reply event carries the chosen option back to the
	// agent's stream as a user turn. seedChannelAndAgent doesn't auto-
	// open a session, so we look up via actor_handle (the agent that
	// raised the attention) rather than session_id; the dispatch path
	// uses session_id → current_agent_id, so without a session the
	// fan-out is a no-op. That's correct: turn-based delivery requires
	// a live session pointer. For the round-trip assertion here we
	// verify the attention is resolved + the decisions_json carries
	// option_id; the in-session fan-out path is covered by
	// TestDecide_HelpRequestFansOutAttentionReply.
	var st, decisions string
	if err := s.db.QueryRow(
		`SELECT status, decisions_json FROM attention_items WHERE id = ?`,
		attnID,
	).Scan(&st, &decisions); err != nil {
		t.Fatalf("attention status: %v", err)
	}
	if st != "resolved" {
		t.Errorf("attention status = %q; want resolved", st)
	}
	if !strings.Contains(decisions, "\"option_id\":\"green\"") {
		t.Errorf("decisions_json missing picked option: %s", decisions)
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

// attach tolerates line-wrapped base64. Most encoders (the `base64` CLI,
// openssl, many libraries) wrap at 76 columns; Go's StdEncoding rejects
// the embedded newlines. The tester's agent hit this and misread the
// decode error as a size limit ("too much for inline"). Wrapped base64
// must now decode to the same bytes as unwrapped.
func TestMCP_Attach_ToleratesWrappedBase64(t *testing.T) {
	s, _ := newTestServer(t)
	payload := bytes.Repeat([]byte("the quick brown fox jumps. "), 64) // > 76 b64 cols
	// Wrap the base64 at 76 columns with newlines, as `base64` CLI does.
	enc := base64.StdEncoding.EncodeToString(payload)
	var wrapped strings.Builder
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		wrapped.WriteString(enc[i:end])
		wrapped.WriteByte('\n')
	}
	args, _ := json.Marshal(map[string]any{
		"filename":       "wrapped.txt",
		"content_base64": wrapped.String(),
	})
	out, jerr := s.mcpAttach(context.Background(), args)
	if jerr != nil {
		t.Fatalf("wrapped base64 should decode, got: %+v", jerr)
	}
	sha := firstFieldFromMCPResult(t, out, "sha256")
	want := sha256.Sum256(payload)
	if sha != hex.EncodeToString(want[:]) {
		t.Errorf("wrapped attach produced wrong sha: got %s", sha)
	}
}

// attach accepts plain text via `content` with no base64 round-trip — the
// convenience path agents should pick for text/JSON.
func TestMCP_Attach_PlaintextContent(t *testing.T) {
	s, _ := newTestServer(t)
	text := `{"note":"plain json, no base64"}`
	args, _ := json.Marshal(map[string]any{
		"filename": "note.json",
		"content":  text,
		"mime":     "application/json",
	})
	out, jerr := s.mcpAttach(context.Background(), args)
	if jerr != nil {
		t.Fatalf("plaintext content attach: %+v", jerr)
	}
	sha := firstFieldFromMCPResult(t, out, "sha256")
	want := sha256.Sum256([]byte(text))
	if sha != hex.EncodeToString(want[:]) {
		t.Errorf("plaintext attach stored wrong bytes: got sha %s", sha)
	}
}

// attach rejects ambiguous and empty payloads with actionable messages.
func TestMCP_Attach_PayloadValidation(t *testing.T) {
	s, _ := newTestServer(t)
	both, _ := json.Marshal(map[string]any{
		"filename": "x", "content": "hi", "content_base64": base64.StdEncoding.EncodeToString([]byte("hi")),
	})
	if _, jerr := s.mcpAttach(context.Background(), both); jerr == nil ||
		!strings.Contains(jerr.Message, "exactly one") {
		t.Errorf("both fields should be rejected with 'exactly one': %+v", jerr)
	}
	neither, _ := json.Marshal(map[string]any{"filename": "x"})
	if _, jerr := s.mcpAttach(context.Background(), neither); jerr == nil ||
		!strings.Contains(jerr.Message, "required") {
		t.Errorf("no payload should be rejected as required: %+v", jerr)
	}
	// Raw un-encoded text in content_base64 still fails, but the error
	// steers to `content` rather than looking like a size problem.
	badB64, _ := json.Marshal(map[string]any{
		"filename": "x", "content_base64": "this is not base64!!!",
	})
	if _, jerr := s.mcpAttach(context.Background(), badB64); jerr == nil ||
		!strings.Contains(jerr.Message, "content") {
		t.Errorf("invalid base64 error should mention the content alternative: %+v", jerr)
	}
}

// blob_get: round-trip a blob via attach + blob_get, confirm the bytes
// come back equal. This is the load-bearing assertion for cross-host
// file transfer: host A's attach writes, host B's blob_get reads.
func TestMCP_BlobGet_RoundtripsAttachedBytes(t *testing.T) {
	s, _ := newTestServer(t)
	payload := []byte("the quick brown fox\x00\x01\x02") // include non-printable

	attachArgs, _ := json.Marshal(map[string]any{
		"filename":       "fox.bin",
		"content_base64": base64.StdEncoding.EncodeToString(payload),
		"mime":           "application/octet-stream",
	})
	out, jerr := s.mcpAttach(context.Background(), attachArgs)
	if jerr != nil {
		t.Fatalf("attach: %+v", jerr)
	}
	sha := firstFieldFromMCPResult(t, out, "sha256")

	getArgs, _ := json.Marshal(map[string]any{"sha256": sha})
	got, jerr := s.mcpGetBlob(context.Background(), getArgs)
	if jerr != nil {
		t.Fatalf("blob_get: %+v", jerr)
	}
	gotShape := mcpResultMap(t, got)
	if gotShape["sha256"] != sha {
		t.Errorf("sha256 = %v, want %v", gotShape["sha256"], sha)
	}
	if int(gotShape["size"].(float64)) != len(payload) {
		t.Errorf("size = %v, want %d", gotShape["size"], len(payload))
	}
	if gotShape["mime"] != "application/octet-stream" {
		t.Errorf("mime = %v, want application/octet-stream", gotShape["mime"])
	}
	b64, _ := gotShape["content_base64"].(string)
	bytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("content_base64 not decodable: %v", err)
	}
	if string(bytes) != string(payload) {
		t.Errorf("round-tripped bytes mismatch: got %q want %q", bytes, payload)
	}
}

// blob_get accepts the URI verbatim — agents reading the URI from
// an A2A file part shouldn't have to slice "blob:sha256/" themselves.
func TestMCP_BlobGet_AcceptsBlobURIForm(t *testing.T) {
	s, _ := newTestServer(t)
	payload := []byte("uri form")
	attachArgs, _ := json.Marshal(map[string]any{
		"filename":       "u.txt",
		"content_base64": base64.StdEncoding.EncodeToString(payload),
	})
	out, _ := s.mcpAttach(context.Background(), attachArgs)
	sha := firstFieldFromMCPResult(t, out, "sha256")

	for _, uri := range []string{
		"blob:sha256/" + sha,
		"hub-blob://" + sha,
	} {
		args, _ := json.Marshal(map[string]any{"uri": uri})
		got, jerr := s.mcpGetBlob(context.Background(), args)
		if jerr != nil {
			t.Errorf("uri %q: %+v", uri, jerr)
			continue
		}
		shape := mcpResultMap(t, got)
		if shape["sha256"] != sha {
			t.Errorf("uri %q: sha = %v, want %v", uri, shape["sha256"], sha)
		}
	}
}

// blob_get fails closed on inputs that look like a sha but aren't,
// so caller bugs (passing a ULID, a path, an empty string) surface
// as -32602 rather than being silently swallowed as "not found".
func TestMCP_BlobGet_RejectsMalformedInputs(t *testing.T) {
	s, _ := newTestServer(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"no sha or uri", map[string]any{}},
		{"empty sha", map[string]any{"sha256": ""}},
		{"sha wrong length", map[string]any{"sha256": "abc"}},
		{"sha uppercase", map[string]any{"sha256": strings.Repeat("A", 64)}},
		{"sha non-hex", map[string]any{"sha256": strings.Repeat("z", 64)}},
		{"unknown URI scheme", map[string]any{"uri": "s3://bucket/key"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args, _ := json.Marshal(c.args)
			_, jerr := s.mcpGetBlob(context.Background(), args)
			if jerr == nil {
				t.Fatal("err = nil, want validation error")
			}
			if jerr.Code != -32602 {
				t.Errorf("code = %d, want -32602 (invalid params)", jerr.Code)
			}
		})
	}
}

// Unknown sha (well-formed shape, no matching row) returns -32000
// with the sha in the error message so callers can confirm they
// passed the right value (same discipline as documents_get).
func TestMCP_BlobGet_NotFoundReturnsClearError(t *testing.T) {
	s, _ := newTestServer(t)
	sha := strings.Repeat("0", 64)
	args, _ := json.Marshal(map[string]any{"sha256": sha})
	_, jerr := s.mcpGetBlob(context.Background(), args)
	if jerr == nil {
		t.Fatal("err = nil, want not-found error")
	}
	if jerr.Code != -32000 {
		t.Errorf("code = %d, want -32000", jerr.Code)
	}
	if !strings.Contains(jerr.Message, sha) {
		t.Errorf("error message %q does not echo the sha", jerr.Message)
	}
}

// The half-state — blobs row exists but bytes are gone — surfaces
// as a distinct error message so operators can tell "you typed the
// wrong sha" from "your hub's DataRoot is partially restored."
func TestMCP_BlobGet_MissingFileSurfacedSeparately(t *testing.T) {
	s, _ := newTestServer(t)
	payload := []byte("about to vanish")
	attachArgs, _ := json.Marshal(map[string]any{
		"filename":       "v.bin",
		"content_base64": base64.StdEncoding.EncodeToString(payload),
	})
	out, _ := s.mcpAttach(context.Background(), attachArgs)
	sha := firstFieldFromMCPResult(t, out, "sha256")

	// Pull the on-disk path and delete the file out from under the
	// row, simulating the half-state we want to surface.
	var path string
	if err := s.db.QueryRow(`SELECT scope_path FROM blobs WHERE sha256 = ?`, sha).
		Scan(&path); err != nil {
		t.Fatalf("read scope_path: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove blob file: %v", err)
	}

	args, _ := json.Marshal(map[string]any{"sha256": sha})
	_, jerr := s.mcpGetBlob(context.Background(), args)
	if jerr == nil {
		t.Fatal("err = nil, want bytes-missing error")
	}
	if !strings.Contains(jerr.Message, "bytes missing on disk") {
		t.Errorf("error message %q doesn't name the half-state", jerr.Message)
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
// TestMCP_TemplatesPropose_ApproveInstalls drives the REAL decide
// endpoint (not installProposedTemplate directly), which is the path a
// reviewer actually exercises and the one that regressed: the W8
// refactor dropped template_proposal's auto-install alias, so approve
// became a silent no-op (issue #4) and the proposer got no feedback
// (issue #3). This locks both: approve installs, and the proposing
// steward receives a fan-back turn.
func TestMCP_TemplatesPropose_ApproveInstalls(t *testing.T) {
	s, token := newA2ATestServer(t)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "steward", "claude-code", "")
	_ = seedSessionForAgent(t, s, agentID)

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

	// Approve via the decide endpoint — the reviewer's real action.
	status, respBody := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attnID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal", "reason": "lgtm"})
	if status != 200 {
		t.Fatalf("decide: %d %s", status, respBody)
	}

	// (#4) Approve actually installed: executed carries the written path.
	var dr struct {
		Executed struct {
			Path string `json:"path"`
		} `json:"executed"`
	}
	_ = json.Unmarshal(respBody, &dr)
	if dr.Executed.Path == "" {
		t.Fatalf("approve did not install — no executed.path: %s", respBody)
	}
	got, err := readFile(t, dr.Executed.Path)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if got != body {
		t.Errorf("installed body mismatch:\nwant %q\ngot  %q", body, got)
	}

	// (#3) The proposing steward got fan-back feedback: an
	// input.attention_reply turn carrying the decision + change_kind.
	reply := readLatestAttentionReply(t, s, agentID)
	if reply["decision"] != "approve" {
		t.Errorf("fan-back decision = %v; want approve", reply["decision"])
	}
	if reply["change_kind"] != "template.install" {
		t.Errorf("fan-back change_kind = %v; want template.install", reply["change_kind"])
	}
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

// mcpResultMap decodes the full top-level JSON object out of an MCP
// tool result. Use when the test needs more than one field (e.g.
// blob_get returns sha256 + size + mime + content_base64 together).
func mcpResultMap(t *testing.T, out any) map[string]any {
	t.Helper()
	body := mcpResultTextBody(t, out)
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal mcp body: %v — body=%s", err, body)
	}
	return m
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

// ADR-027 W4: AskUserQuestion is auto-allowed at the gate; the picker
// UX is handled separately via the host-runner hook surface (W5b) +
// tmux send-keys (W3). No attention_items row should be created.
func TestMCP_PermissionPrompt_AskUserQuestionAutoAllows(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"tool_name": "AskUserQuestion",
		"input":     map[string]any{"questions": []map[string]any{{"question": "Color?", "options": []map[string]any{{"label": "Red"}, {"label": "Blue"}}}}},
	})

	out, jerr := s.mcpPermissionPrompt(
		context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("permission_prompt: %+v", jerr)
	}
	body := mcpResultTextBody(t, out)
	if !strings.Contains(body, `"behavior": "allow"`) {
		t.Errorf("expected immediate allow for AskUserQuestion, got %s", body)
	}
	// updatedInput should pass through so claude-code keeps the
	// questionnaire structure intact for its TUI picker.
	if !strings.Contains(body, "questions") {
		t.Errorf("updatedInput should pass through, got %s", body)
	}

	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM attention_items WHERE kind = 'permission_prompt'`,
	).Scan(&n)
	if n != 0 {
		t.Errorf("AskUserQuestion gate should NOT create attention; got %d row(s)", n)
	}
}

// ADR-027 W4: ExitPlanMode escalates regardless of tier; the resolver
// renders the proposed plan as markdown (dialog_type=plan_approval,
// plan_body from tool_input.plan).
func TestMCP_PermissionPrompt_ExitPlanModeParksWithPlanApproval(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	planBody := "1. Sketch the API surface\n2. Land the migration\n3. Run the smoke tests"
	args, _ := json.Marshal(map[string]any{
		"tool_name": "ExitPlanMode",
		"input":     map[string]any{"plan": planBody},
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

	var attnID, payloadJSON string
	for i := 0; i < 50; i++ {
		_ = s.db.QueryRow(
			`SELECT id, pending_payload_json FROM attention_items WHERE kind = 'permission_prompt' ORDER BY created_at DESC LIMIT 1`,
		).Scan(&attnID, &payloadJSON)
		if attnID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if attnID == "" {
		t.Fatal("attention_items row never appeared for ExitPlanMode")
	}
	if !strings.Contains(payloadJSON, `"dialog_type":"plan_approval"`) {
		t.Errorf("payload missing dialog_type=plan_approval: %s", payloadJSON)
	}
	if !strings.Contains(payloadJSON, "Sketch the API surface") {
		t.Errorf("payload missing plan_body content: %s", payloadJSON)
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
		t.Errorf("expected allow after plan approval, got %s", body)
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
