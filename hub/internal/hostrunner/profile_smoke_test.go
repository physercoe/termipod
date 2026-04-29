package hostrunner

import (
	"reflect"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// claudeProfile loads the embedded claude-code profile for the smoke
// tests below. Sourced from agent_families.yaml so the canonical
// rules + their inline comments are the SoT — these tests prove the
// authored profile produces the right shapes.
func claudeProfile(t *testing.T) *agentfamilies.FrameProfile {
	t.Helper()
	f, ok := agentfamilies.ByName("claude-code")
	if !ok {
		t.Fatal("claude-code family missing")
	}
	if f.FrameProfile == nil {
		t.Fatal("claude-code frame_profile not embedded")
	}
	return f.FrameProfile
}

// pickEvent returns the first emitted event with the given kind, or
// fatals. Smoke-test helper; the parity test in Phase 1.5 will diff
// the full event slice.
func pickEvent(t *testing.T, evts []EmittedEvent, kind string) EmittedEvent {
	t.Helper()
	for _, e := range evts {
		if e.Kind == kind {
			return e
		}
	}
	t.Fatalf("no event with kind=%q in %+v", kind, evts)
	return EmittedEvent{}
}

// TestProfile_ClaudeCode_SystemInit — v1.0.328 frame shape with both
// camelCase and snake_case fields scattered across the SDK. Profile
// must lift them all.
func TestProfile_ClaudeCode_SystemInit(t *testing.T) {
	frame := map[string]any{
		"type":           "system",
		"subtype":        "init",
		"session_id":     "sess-abc",
		"model":          "claude-opus-4-7",
		"cwd":            "/tmp/wt",
		"permissionMode": "acceptEdits",
		"tools":          []any{"Read", "Write", "Bash"},
		"mcpServers":     []any{map[string]any{"name": "git"}},
		"slashCommands":  []any{"/help", "/compact"},
		"claude_code_version": "1.5.0",
	}
	got := ApplyProfile(frame, claudeProfile(t))
	e := pickEvent(t, got, "session.init")
	if e.Producer != "agent" {
		t.Errorf("producer = %q; want agent", e.Producer)
	}
	checks := map[string]any{
		"session_id":      "sess-abc",
		"model":           "claude-opus-4-7",
		"cwd":             "/tmp/wt",
		"permission_mode": "acceptEdits", // camelCase coalesce hit
		"version":         "1.5.0",       // claude_code_version coalesce hit
	}
	for k, want := range checks {
		if !reflect.DeepEqual(e.Payload[k], want) {
			t.Errorf("session.init.%s = %v; want %v", k, e.Payload[k], want)
		}
	}
	// Coalesce that didn't hit any field is nil — explicitly check
	// so a future regression is loud.
	if e.Payload["fast_mode_state"] != nil {
		t.Errorf("fast_mode_state should be nil when SDK omits it; got %v",
			e.Payload["fast_mode_state"])
	}
}

// TestProfile_ClaudeCode_RateLimit_AllShapes — all three rate-limit
// shapes (the v1.0.326 + v1.0.328 motivation). Profile collapses them
// to the same rate_limit emit.
func TestProfile_ClaudeCode_RateLimit_AllShapes(t *testing.T) {
	cases := []struct {
		name  string
		frame map[string]any
	}{
		{
			name: "flat_top_level",
			frame: map[string]any{
				"type":          "rate_limit_event",
				"rateLimitType": "5h",
				"status":        "warn",
				"resetsAt":      "2026-04-25T13:00:00Z",
			},
		},
		{
			name: "system_subtype",
			frame: map[string]any{
				"type":          "system",
				"subtype":       "rate_limit_event",
				"rateLimitType": "5h",
				"status":        "allowed",
			},
		},
		{
			name: "nested_rate_limit_info",
			frame: map[string]any{
				"type": "rate_limit_event",
				"rate_limit_info": map[string]any{
					"rateLimitType":         "five_hour",
					"status":                "allowed",
					"resetsAt":              float64(1777443000),
					"overageDisabledReason": "org_level_disabled_until",
				},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ApplyProfile(c.frame, claudeProfile(t))
			e := pickEvent(t, got, "rate_limit")
			if e.Payload["status"] == nil {
				t.Errorf("status should resolve via coalesce; got %+v", e.Payload)
			}
			if e.Payload["window"] == nil {
				t.Errorf("window should resolve via coalesce; got %+v", e.Payload)
			}
		})
	}
}

// TestProfile_ClaudeCode_AssistantMultiEmit — the canonical multi-rule
// case: assistant frame with content blocks + usage produces three
// kinds at once. message_id propagates from outer scope.
func TestProfile_ClaudeCode_AssistantMultiEmit(t *testing.T) {
	frame := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":    "msg_42",
			"model": "claude-opus-4-7",
			"content": []any{
				map[string]any{"type": "text", "text": "Reading the file."},
				map[string]any{
					"type":  "tool_use",
					"id":    "toolu_1",
					"name":  "Read",
					"input": map[string]any{"file_path": "/etc/hosts"},
				},
			},
			"usage": map[string]any{
				"input_tokens":             float64(120),
				"output_tokens":            float64(40),
				"cache_read_input_tokens":  float64(9100),
			},
		},
	}
	got := ApplyProfile(frame, claudeProfile(t))
	if len(got) != 3 {
		t.Fatalf("got %d events; want 3 (text + tool_call + usage). got=%+v",
			len(got), got)
	}
	textE := pickEvent(t, got, "text")
	if textE.Payload["text"] != "Reading the file." {
		t.Errorf("text.text = %v; want %q", textE.Payload["text"], "Reading the file.")
	}
	if textE.Payload["message_id"] != "msg_42" {
		t.Errorf("text.message_id = %v; want msg_42 (lifted via $$)",
			textE.Payload["message_id"])
	}

	toolE := pickEvent(t, got, "tool_call")
	if toolE.Payload["id"] != "toolu_1" || toolE.Payload["name"] != "Read" {
		t.Errorf("tool_call wrong: %+v", toolE.Payload)
	}

	usageE := pickEvent(t, got, "usage")
	if usageE.Payload["input_tokens"] != float64(120) {
		t.Errorf("usage.input_tokens = %v", usageE.Payload["input_tokens"])
	}
	if usageE.Payload["cache_read"] != float64(9100) {
		t.Errorf("usage.cache_read = %v; coalesce should pick cache_read_input_tokens",
			usageE.Payload["cache_read"])
	}
	if usageE.Payload["model"] != "claude-opus-4-7" {
		t.Errorf("usage.model = %v; want lifted from $.message.model",
			usageE.Payload["model"])
	}
}

// TestProfile_ClaudeCode_AssistantWithoutUsage — when the SDK omits
// message.usage, when_present gates the usage emit and we get just
// the content-block events. No raw fallback (rule matched).
func TestProfile_ClaudeCode_AssistantWithoutUsage(t *testing.T) {
	frame := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id": "msg_no_usage",
			"content": []any{
				map[string]any{"type": "text", "text": "no telemetry"},
			},
		},
	}
	got := ApplyProfile(frame, claudeProfile(t))
	if len(got) != 1 {
		t.Fatalf("got %d events; want 1 (text only, usage gated). got=%+v",
			len(got), got)
	}
	if got[0].Kind != "text" {
		t.Errorf("event kind = %q; want text", got[0].Kind)
	}
}

// TestProfile_ClaudeCode_UserToolResult — user frames carry tool_result
// blocks (the agent's own view of tool outputs). Plain user-text
// frames produce nothing because there's no tool_result sub-rule for
// type=text inside user.message.content.
func TestProfile_ClaudeCode_UserToolResult(t *testing.T) {
	frame := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu_1",
					"content":     "127.0.0.1 localhost",
					"is_error":    false,
				},
			},
		},
	}
	got := ApplyProfile(frame, claudeProfile(t))
	if len(got) != 1 || got[0].Kind != "tool_result" {
		t.Fatalf("got %+v; want one tool_result", got)
	}
	if got[0].Payload["tool_use_id"] != "toolu_1" {
		t.Errorf("tool_use_id = %v", got[0].Payload["tool_use_id"])
	}
	if got[0].Payload["content"] != "127.0.0.1 localhost" {
		t.Errorf("content = %v", got[0].Payload["content"])
	}
}

// TestProfile_ClaudeCode_ResultEmitsBoth — `result` frames fire both
// turn.result (canonical) and completion (deprecated alias) per
// ADR-010 plan §4.2. Both rules tie on {type: result} specificity.
func TestProfile_ClaudeCode_ResultEmitsBoth(t *testing.T) {
	frame := map[string]any{
		"type":           "result",
		"subtype":        "success",
		"duration_ms":    float64(4500),
		"num_turns":      float64(3),
		"total_cost_usd": float64(0.0123),
	}
	got := ApplyProfile(frame, claudeProfile(t))
	if len(got) != 2 {
		t.Fatalf("got %d events; want 2 (turn.result + completion)", len(got))
	}
	turnE := pickEvent(t, got, "turn.result")
	if turnE.Payload["cost_usd"] != float64(0.0123) {
		t.Errorf("turn.result.cost_usd = %v; coalesce should pick total_cost_usd",
			turnE.Payload["cost_usd"])
	}
	if turnE.Payload["terminal_reason"] != "success" {
		t.Errorf("turn.result.terminal_reason = %v; want subtype fallback",
			turnE.Payload["terminal_reason"])
	}
	complE := pickEvent(t, got, "completion")
	if complE.Payload["subtype"] != "success" {
		t.Errorf("completion.subtype = %v; want success",
			complE.Payload["subtype"])
	}
}

// TestProfile_ClaudeCode_UnknownFalls ToRaw — a frame type we haven't
// profiled (a hypothetical future SDK shape) falls through to
// kind=raw. D5: forward-compatibility without losing transcript bytes.
func TestProfile_ClaudeCode_UnknownFallsToRaw(t *testing.T) {
	frame := map[string]any{
		"type":       "telemetry_v2",
		"data":       "future shape",
		"session_id": "sess-x",
	}
	got := ApplyProfile(frame, claudeProfile(t))
	if len(got) != 1 || got[0].Kind != "raw" {
		t.Fatalf("got %+v; want one raw fallback", got)
	}
	if !reflect.DeepEqual(got[0].Payload, frame) {
		t.Errorf("raw fallback payload should be the frame verbatim")
	}
}
