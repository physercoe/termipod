package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// request_help is the third attention shape — open-ended free text.
// Sister to request_approval (binary) and request_select (n-ary).
// These tests pin the contract end-to-end: agent calls the tool,
// principal decides via the existing /decide endpoint with a `body`
// field, the long-poll returns that body to the agent verbatim.

func TestRequestHelp_CreatesAttentionWithExpectedShape(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	out, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "help-asker",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	// Kick off the agent's request_help in a goroutine — it long-polls
	// for a reply, so we resolve it from the test thread below.
	args, _ := json.Marshal(map[string]any{
		"question": "Should I refactor auth before or after the cache layer?",
		"context":  "Both touch User; I see arguments either way.",
		"mode":     "clarify",
	})
	var (
		mu        sync.Mutex
		toolReply any
		toolErr   error
		done      = make(chan struct{})
	)
	go func() {
		defer close(done)
		res, jerr := c.s.mcpRequestHelp(
			context.Background(), defaultTeamID, out.AgentID, args,
		)
		mu.Lock()
		toolReply = res
		if jerr != nil {
			toolErr = &mcpToolError{msg: jerr.Message}
		}
		mu.Unlock()
	}()

	// Poll until the attention row is visible to the listing endpoint —
	// the agent's INSERT happens before the long-poll starts.
	deadline := time.Now().Add(2 * time.Second)
	var attentionID string
	for time.Now().Before(deadline) {
		status, body := c.call("GET",
			"/v1/teams/"+c.teamID+"/attention?status=open", nil)
		if status != 200 {
			t.Fatalf("list attention = %d", status)
		}
		raw, _ := body["json"].(string)
		_ = raw
		// e2eCtx.call decodes into map[string]any but the response is
		// an array — fall back to a fresh request for the typed list.
		atts := listOpenAttentionsTyped(t, c)
		for _, a := range atts {
			if a.Kind == "help_request" {
				attentionID = a.ID
				if a.Severity != "minor" {
					t.Fatalf("clarify-mode help_request severity = %q; want minor", a.Severity)
				}
				if a.Summary == "" {
					t.Fatalf("help_request summary empty")
				}
				var pending map[string]any
				_ = json.Unmarshal(a.PendingPayload, &pending)
				if pending["mode"] != "clarify" {
					t.Fatalf("pending.mode = %v; want clarify", pending["mode"])
				}
				if pending["question"] == "" {
					t.Fatalf("pending.question empty: %+v", pending)
				}
				if pending["agent_id"] != out.AgentID {
					t.Fatalf("pending.agent_id = %v; want %s", pending["agent_id"], out.AgentID)
				}
				break
			}
		}
		if attentionID != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if attentionID == "" {
		t.Fatalf("help_request attention never appeared")
	}

	// Resolve via /decide with body=<reply>. This is the same endpoint
	// the mobile composer hits.
	status, _ := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+attentionID+"/decide",
		map[string]any{
			"decision": "approve",
			"by":       "@principal",
			"body":     "Refactor auth first — cache changes will reuse the new User shape.",
		})
	if status != 200 {
		t.Fatalf("decide = %d", status)
	}

	// Long-poll should return now with body verbatim.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("mcpRequestHelp never returned after decide")
	}
	mu.Lock()
	defer mu.Unlock()
	if toolErr != nil {
		t.Fatalf("mcpRequestHelp errored: %v", toolErr)
	}
	got := mcpToolBodyField(toolReply, "body")
	if got != "Refactor auth first — cache changes will reuse the new User shape." {
		t.Fatalf("agent received body = %q; want the principal's reply verbatim", got)
	}
	if mcpToolBodyField(toolReply, "decision") != "approve" {
		t.Fatalf("agent received decision != approve: %+v", toolReply)
	}
}

func TestDecide_HelpRequestRejectsApproveWithoutBody(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	// Seed a help_request directly via INSERT so the test doesn't need a
	// real spawn — we're exercising the decide endpoint, not the tool.
	id := NewID()
	now := NowUTC()
	if _, err := c.s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json
		) VALUES (?, NULL, 'team', NULL, 'help_request',
		          'q?', 'minor', '[]', 'open', ?,
		          'agent', 'someone', '{}')`, id, now,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// approve without body must 400.
	status, body := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+id+"/decide",
		map[string]any{"decision": "approve", "by": "@me"})
	if status != 400 {
		t.Fatalf("approve-without-body = %d (body %v); want 400", status, body)
	}

	// reject without body must succeed (dismissal, no answer needed).
	status, _ = c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+id+"/decide",
		map[string]any{"decision": "reject", "by": "@me",
			"reason": "not for me"})
	if status != 200 {
		t.Fatalf("reject = %d; want 200", status)
	}
}

// mcpToolBodyField reaches into mcpResultJSON's wrapper to read a single
// top-level field from the inner content[0].text payload. mcpResultJSON
// returns {content:[{type:'text', text:<json-string>}]}.
func mcpToolBodyField(reply any, field string) string {
	m, ok := reply.(map[string]any)
	if !ok {
		return ""
	}
	contentArr, ok := m["content"].([]any)
	if !ok || len(contentArr) == 0 {
		return ""
	}
	first, ok := contentArr[0].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := first["text"].(string)
	var inner map[string]any
	if err := json.Unmarshal([]byte(text), &inner); err != nil {
		return ""
	}
	v, _ := inner[field].(string)
	return v
}

type mcpToolError struct{ msg string }

func (e *mcpToolError) Error() string { return e.msg }

// listOpenAttentionsTyped does a typed list-open call so the test can
// read the structured fields rather than navigating an unstructured map.
func listOpenAttentionsTyped(t *testing.T, c *e2eCtx) []attentionOut {
	t.Helper()
	req, _ := http.NewRequest("GET",
		c.srv.URL+"/v1/teams/"+c.teamID+"/attention?status=open", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	var out []attentionOut
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}
