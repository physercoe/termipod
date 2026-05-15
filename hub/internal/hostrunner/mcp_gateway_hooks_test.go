package hostrunner

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingHookSink captures every OnHook call and returns a canned
// response. Optionally a hook function lets a test inject blocking
// behaviour to verify parking semantics.
type recordingHookSink struct {
	mu     sync.Mutex
	calls  []recordedHookCall
	resp   map[string]any
	err    error
	hookFn func(name string, payload map[string]any) (map[string]any, error)
}

type recordedHookCall struct {
	name    string
	payload map[string]any
}

func (s *recordingHookSink) OnHook(_ context.Context, name string, payload map[string]any) (map[string]any, error) {
	s.mu.Lock()
	s.calls = append(s.calls, recordedHookCall{name: name, payload: payload})
	s.mu.Unlock()
	if s.hookFn != nil {
		return s.hookFn(name, payload)
	}
	return s.resp, s.err
}

func (s *recordingHookSink) snapshot() []recordedHookCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedHookCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func TestGateway_HookToolsRegistered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g, cleanup, err := StartGateway(ctx, "test-hook-list-"+randID(t), nil)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	defer cleanup()

	conn, r := dialGateway(t, g)
	writeJRPCLine(t, conn, "initialize", 1, map[string]any{})
	_ = readJRPCLine(t, r)
	writeJRPCLine(t, conn, "tools/list", 2, nil)
	resp := readJRPCLine(t, r)
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	got := map[string]bool{}
	for _, t := range tools {
		m, _ := t.(map[string]any)
		if name, _ := m["name"].(string); name != "" {
			got[name] = true
		}
	}
	for hook := range claudeHookToolNames {
		if !got[hook] {
			t.Errorf("tools/list missing %s", hook)
		}
	}
}

func TestGateway_HookCall_NoSinkReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g, cleanup, err := StartGateway(ctx, "test-hook-nosink-"+randID(t), nil)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	defer cleanup()

	conn, r := dialGateway(t, g)
	writeJRPCLine(t, conn, "initialize", 1, map[string]any{})
	_ = readJRPCLine(t, r)
	writeJRPCLine(t, conn, "tools/call", 2, map[string]any{
		"name":      "hook_stop",
		"arguments": map[string]any{"last_assistant_message": "ok"},
	})
	resp := readJRPCLine(t, r)
	if resp["error"] == nil {
		t.Fatalf("want error when HookSink is nil, got result: %v", resp["result"])
	}
}

func TestGateway_HookCall_DispatchesAndReturnsResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := &recordingHookSink{resp: map[string]any{}}
	g, cleanup, err := StartGateway(ctx, "test-hook-disp-"+randID(t), nil)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	g.HookSink = sink
	defer cleanup()

	conn, r := dialGateway(t, g)
	writeJRPCLine(t, conn, "initialize", 1, map[string]any{})
	_ = readJRPCLine(t, r)
	writeJRPCLine(t, conn, "tools/call", 2, map[string]any{
		"name":      "hook_stop",
		"arguments": map[string]any{"last_assistant_message": "done", "permission_mode": "default"},
	})
	resp := readJRPCLine(t, r)
	if resp["error"] != nil {
		t.Fatalf("hook_stop returned error: %v", resp["error"])
	}
	calls := sink.snapshot()
	if len(calls) != 1 {
		t.Fatalf("sink calls = %d, want 1", len(calls))
	}
	if calls[0].name != "Stop" {
		t.Errorf("sink call name = %q, want Stop (translated from hook_stop)", calls[0].name)
	}
	if calls[0].payload["last_assistant_message"] != "done" {
		t.Errorf("payload last_assistant_message = %v, want done", calls[0].payload["last_assistant_message"])
	}
}

func TestGateway_HookCall_TranslatesEventNames(t *testing.T) {
	want := map[string]string{
		"hook_pre_tool_use":  "PreToolUse",
		"hook_post_tool_use": "PostToolUse",
		"hook_notification":  "Notification",
		"hook_pre_compact":   "PreCompact",
		"hook_stop":          "Stop",
		"hook_subagent_stop": "SubagentStop",
		"hook_user_prompt":   "UserPromptSubmit",
		"hook_session_start": "SessionStart",
		"hook_session_end":   "SessionEnd",
	}
	for toolName, eventName := range want {
		if claudeHookEventByTool[toolName] != eventName {
			t.Errorf("claudeHookEventByTool[%s] = %q, want %q",
				toolName, claudeHookEventByTool[toolName], eventName)
		}
	}
}

func TestGateway_HookCall_ParkingBlocksUntilSinkReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	releaseCh := make(chan struct{})
	sink := &recordingHookSink{
		hookFn: func(_ string, _ map[string]any) (map[string]any, error) {
			<-releaseCh
			return map[string]any{"decision": "block"}, nil
		},
	}
	g, cleanup, err := StartGateway(ctx, "test-hook-park-"+randID(t), nil)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	g.HookSink = sink
	defer cleanup()

	conn, r := dialGateway(t, g)
	writeJRPCLine(t, conn, "initialize", 1, map[string]any{})
	_ = readJRPCLine(t, r)

	respCh := make(chan map[string]any, 1)
	go func() {
		writeJRPCLine(t, conn, "tools/call", 2, map[string]any{
			"name":      "hook_pre_compact",
			"arguments": map[string]any{"trigger": "manual"},
		})
		respCh <- readJRPCLine(t, r)
	}()

	// Verify the sink received the call (i.e. dispatch reached it)
	// but the connection has NOT yet received a response.
	if !waitFor(func() bool { return len(sink.snapshot()) == 1 }) {
		t.Fatal("sink.OnHook was never called")
	}
	// Give the gateway 50ms to (incorrectly) deliver a response if
	// the parking path were broken. With the real implementation
	// dispatchHookTool is blocked in sink.OnHook so no bytes can land
	// on the conn during this window.
	select {
	case resp := <-respCh:
		t.Fatalf("response delivered before sink released: %v", resp)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseCh)
	resp := <-respCh
	if resp["error"] != nil {
		t.Fatalf("hook_pre_compact returned error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("result content empty: %v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !contains(text, `"decision": "block"`) && !contains(text, `"decision":"block"`) {
		t.Errorf("result missing decision:block; got: %s", text)
	}
}

// waitFor polls fn at 10ms cadence for up to ~1s. Returns true if fn
// ever returned true within the window.
func waitFor(fn func() bool) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
