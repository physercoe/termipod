package hostrunner

import (
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// TestProfile_Codex_TranslatesAppServerNotifications drives the codex
// frame profile (ADR-012 D4) through a representative corpus of
// app-server JSON-RPC notifications and asserts the emitted
// agent_event kinds. There is no legacy translator to parity-check
// against — for codex the profile is authoritative — so this test is
// expectation-based rather than parity-style.
//
// Operator workflow for extending coverage:
//
//  1. Run a real `codex app-server --listen stdio://` session and
//     append the captured notifications to
//     hub/internal/hostrunner/testdata/profiles/codex/corpus.jsonl.
//  2. Add the expected (method → emitted kind) pair to wantKinds
//     below, or — for unprofiled methods that should fall through
//     to kind=raw — leave it out of wantKinds and the test will
//     accept the default.
//  3. Re-run: go test ./internal/hostrunner/ -run Codex_Translates -v
//
// The test is intentionally lenient about field-level payload
// shape: full payload assertions live in
// TestProfile_Codex_PayloadFields below, scoped to the rules whose
// payloads carry load-bearing data (session_id, item ids, the bits
// the driver and mobile UI read).
func TestProfile_Codex_TranslatesAppServerNotifications(t *testing.T) {
	corpusPath := filepath.Join(
		"testdata", "profiles", "codex", "corpus.jsonl")
	corpus := readCorpus(t, corpusPath)
	if len(corpus) == 0 {
		t.Fatalf("corpus %q is empty", corpusPath)
	}

	f, ok := agentfamilies.ByName("codex")
	if !ok || f.FrameProfile == nil {
		t.Fatal("codex frame_profile not embedded — slice 2 should have shipped it")
	}
	profile := f.FrameProfile

	// (method, item.type) → expected emitted kind.
	// item.type is "" for methods that don't dispatch on it.
	type matcher struct{ method, itemType string }
	wantKinds := map[matcher]string{
		{"thread/started", ""}:                          "session.init",
		{"thread/status/changed", ""}:                   "system",
		{"turn/started", ""}:                            "system",
		{"turn/completed", ""}:                          "turn.result",
		{"turn/diff/updated", ""}:                       "system",
		{"turn/plan/updated", ""}:                       "system",
		{"item/started", "agentMessage"}:                "system",
		{"item/started", "commandExecution"}:            "tool_call",
		{"item/started", "fileChange"}:                  "tool_call",
		{"item/started", "mcpToolCall"}:                 "tool_call",
		{"item/completed", "agentMessage"}:              "text",
		{"item/completed", "commandExecution"}:          "tool_result",
		{"item/completed", "fileChange"}:                "tool_result",
		{"item/completed", "mcpToolCall"}:               "tool_result",
		{"thread/tokenUsage/updated", ""}:               "usage",
		{"account/rateLimits/updated", ""}:              "rate_limit",
		{"mcpServer/startupStatus/updated", ""}:         "system",
		// Unprofiled — falls through to kind=raw via ApplyProfile's
		// fallback. Forward-compatibility, not a bug.
		{"item/reasoning/textDelta", ""}:                "raw",
	}

	for i, frame := range corpus {
		method, _ := frame["method"].(string)
		itemType := ""
		if params, ok := frame["params"].(map[string]any); ok {
			if item, ok := params["item"].(map[string]any); ok {
				itemType, _ = item["type"].(string)
			}
		}
		want, ok := wantKinds[matcher{method, itemType}]
		if !ok {
			t.Errorf("frame %d: no expectation for method=%q item.type=%q — extend wantKinds",
				i, method, itemType)
			continue
		}
		got := ApplyProfile(frame, profile)
		if len(got) != 1 {
			t.Errorf("frame %d (method=%q): want 1 emit, got %d", i, method, len(got))
			continue
		}
		if got[0].Kind != want {
			t.Errorf("frame %d (method=%q item.type=%q): kind = %q; want %q",
				i, method, itemType, got[0].Kind, want)
		}
	}
}

// TestProfile_Codex_PayloadFields pins the load-bearing payload
// fields on the rules the driver (slice 3) and mobile UI depend on.
// Field-level coverage outside this list is left to the operator-
// extended corpus + the wantKinds map above; this test focuses on
// the contract no rename can silently break.
func TestProfile_Codex_PayloadFields(t *testing.T) {
	f, _ := agentfamilies.ByName("codex")
	profile := f.FrameProfile

	// session.init carries the thread id we persist as session_id —
	// the resume cursor under ADR-012 D2.
	threadStarted := map[string]any{
		"jsonrpc": "2.0",
		"method":  "thread/started",
		"params": map[string]any{
			"thread": map[string]any{
				"id":            "thr_xyz",
				"modelProvider": "gpt-5.4",
				"createdAt":     "2026-04-29T10:00:00Z",
			},
		},
	}
	got := ApplyProfile(threadStarted, profile)
	if len(got) != 1 || got[0].Kind != "session.init" {
		t.Fatalf("thread/started: want one session.init, got %+v", got)
	}
	if got[0].Payload["session_id"] != "thr_xyz" {
		t.Errorf("session.init.session_id = %v; want thr_xyz",
			got[0].Payload["session_id"])
	}
	if got[0].Payload["model"] != "gpt-5.4" {
		t.Errorf("session.init.model = %v; want gpt-5.4",
			got[0].Payload["model"])
	}

	// item/completed agentMessage carries the user-visible text and
	// pairs message_id with the tool_call/tool_result correlation id
	// the driver writes into the transcript.
	msgCompleted := map[string]any{
		"jsonrpc": "2.0",
		"method":  "item/completed",
		"params": map[string]any{
			"item": map[string]any{
				"id":    "item_msg_1",
				"type":  "agentMessage",
				"text":  "Done.",
				"phase": "final_answer",
			},
		},
	}
	got = ApplyProfile(msgCompleted, profile)
	if len(got) != 1 || got[0].Kind != "text" {
		t.Fatalf("item/completed agentMessage: want one text, got %+v", got)
	}
	if got[0].Payload["text"] != "Done." {
		t.Errorf("text.text = %v; want Done.", got[0].Payload["text"])
	}
	if got[0].Payload["message_id"] != "item_msg_1" {
		t.Errorf("text.message_id = %v; want item_msg_1",
			got[0].Payload["message_id"])
	}

	// commandExecution pair: tool_call on start, tool_result on
	// complete, both keyed on the same item.id so the renderer can
	// pair them.
	cmdStarted := map[string]any{
		"jsonrpc": "2.0",
		"method":  "item/started",
		"params": map[string]any{
			"item": map[string]any{
				"id":   "item_cmd_1",
				"type": "commandExecution",
			},
		},
	}
	got = ApplyProfile(cmdStarted, profile)
	if len(got) != 1 || got[0].Kind != "tool_call" {
		t.Fatalf("item/started commandExecution: want one tool_call, got %+v", got)
	}
	if got[0].Payload["id"] != "item_cmd_1" {
		t.Errorf("tool_call.id = %v; want item_cmd_1", got[0].Payload["id"])
	}
	if got[0].Payload["name"] != "commandExecution" {
		t.Errorf("tool_call.name = %v; want commandExecution",
			got[0].Payload["name"])
	}

	cmdCompleted := map[string]any{
		"jsonrpc": "2.0",
		"method":  "item/completed",
		"params": map[string]any{
			"item": map[string]any{
				"id":               "item_cmd_1",
				"type":             "commandExecution",
				"aggregatedOutput": "ok\n",
				"exitCode":         float64(0),
				"status":           "completed",
			},
		},
	}
	got = ApplyProfile(cmdCompleted, profile)
	if len(got) != 1 || got[0].Kind != "tool_result" {
		t.Fatalf("item/completed commandExecution: want one tool_result, got %+v", got)
	}
	if got[0].Payload["tool_use_id"] != "item_cmd_1" {
		t.Errorf("tool_result.tool_use_id = %v; want item_cmd_1 (must pair with the tool_call)",
			got[0].Payload["tool_use_id"])
	}
	if got[0].Payload["content"] != "ok\n" {
		t.Errorf("tool_result.content = %v; want ok\\n", got[0].Payload["content"])
	}
}

// TestProfile_Codex_NestedMatcher exercises the dotted-path
// extension to matchesAll that this slice added — codex's
// item/started rules dispatch on params.item.type, which the
// pre-extension matcher couldn't express. Pin the behavior so a
// future cleanup pass that reverts to flat-only doesn't silently
// break the codex profile.
func TestProfile_Codex_NestedMatcher(t *testing.T) {
	f, _ := agentfamilies.ByName("codex")
	profile := f.FrameProfile

	// item/started with no item.type at all — matches the agentMessage
	// rule? No — agentMessage rule requires params.item.type=agentMessage.
	// Should fall through to raw.
	frameNoType := map[string]any{
		"method": "item/started",
		"params": map[string]any{
			"item": map[string]any{"id": "x"},
		},
	}
	got := ApplyProfile(frameNoType, profile)
	if len(got) != 1 || got[0].Kind != "raw" {
		t.Errorf("item/started with no item.type should fall through to raw, got %+v", got)
	}

	// Three different item.types each route to their dedicated rule.
	for _, tc := range []struct {
		itemType string
		wantKind string
	}{
		{"commandExecution", "tool_call"},
		{"fileChange", "tool_call"},
		{"mcpToolCall", "tool_call"},
		{"webSearch", "tool_call"},
	} {
		frame := map[string]any{
			"method": "item/started",
			"params": map[string]any{
				"item": map[string]any{"id": "x", "type": tc.itemType},
			},
		}
		got := ApplyProfile(frame, profile)
		if len(got) != 1 || got[0].Kind != tc.wantKind {
			t.Errorf("item/started + item.type=%s: want %s, got %+v",
				tc.itemType, tc.wantKind, got)
		}
	}
}
