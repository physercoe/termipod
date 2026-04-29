package hostrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStdioDriver_TranslatesStreamJSON feeds a canned stream-json transcript
// through an io.Pipe and asserts the emitted agent_events match the
// driver's typed-event contract: session.init → text → tool_call →
// tool_result → turn.result (+ legacy completion alias), bracketed by
// lifecycle.started / .stopped.
func TestStdioDriver_TranslatesStreamJSON(t *testing.T) {
	frames := []string{
		`{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-opus-4","tools":["Read","Edit"]}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me read it."}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu-1","name":"Read","input":{"path":"/tmp/x"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-1","content":"file contents","is_error":false}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"duration_ms":123,"result":"done"}`,
	}
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{
		AgentID: "agent-m2",
		Poster:  poster,
		Stdout:  pr,
		Closer:  func() { _ = pw.Close() },
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	go func() {
		for _, f := range frames {
			_, _ = pw.Write([]byte(f + "\n"))
		}
	}()

	// Expect: lifecycle.started + 4 typed events + result-frame fanout
	// (turn.result + legacy completion alias) = 7. Stop adds one more.
	poster.wait(t, 7, 2*time.Second)
	drv.Stop()
	poster.wait(t, 8, time.Second)

	evs := poster.snapshot()

	// Event 0: lifecycle.started / system.
	if evs[0].Kind != "lifecycle" || evs[0].Producer != "system" ||
		evs[0].Payload["phase"] != "started" || evs[0].Payload["mode"] != "M2" {
		t.Fatalf("evs[0] want lifecycle.started/system/M2; got %+v", evs[0])
	}

	// Event 1: session.init with session_id + model.
	if evs[1].Kind != "session.init" || evs[1].Producer != "agent" {
		t.Fatalf("evs[1] want session.init/agent; got %+v", evs[1])
	}
	if evs[1].Payload["session_id"] != "sess-1" {
		t.Fatalf("evs[1] session_id = %v; want sess-1", evs[1].Payload["session_id"])
	}
	if evs[1].Payload["model"] != "claude-opus-4" {
		t.Fatalf("evs[1] model = %v; want claude-opus-4", evs[1].Payload["model"])
	}

	// Event 2: text.
	if evs[2].Kind != "text" || evs[2].Producer != "agent" ||
		evs[2].Payload["text"] != "Let me read it." {
		t.Fatalf("evs[2] want text=\"Let me read it.\"; got %+v", evs[2])
	}

	// Event 3: tool_call.
	if evs[3].Kind != "tool_call" || evs[3].Producer != "agent" ||
		evs[3].Payload["id"] != "tu-1" || evs[3].Payload["name"] != "Read" {
		t.Fatalf("evs[3] want tool_call tu-1/Read; got %+v", evs[3])
	}
	if inp, _ := evs[3].Payload["input"].(map[string]any); inp["path"] != "/tmp/x" {
		t.Fatalf("evs[3] input.path = %v; want /tmp/x", inp["path"])
	}

	// Event 4: tool_result.
	if evs[4].Kind != "tool_result" || evs[4].Producer != "agent" ||
		evs[4].Payload["tool_use_id"] != "tu-1" ||
		evs[4].Payload["content"] != "file contents" {
		t.Fatalf("evs[4] want tool_result tu-1; got %+v", evs[4])
	}

	// Event 5: turn.result — canonical kind, normalized payload.
	if evs[5].Kind != "turn.result" || evs[5].Producer != "agent" {
		t.Fatalf("evs[5] want turn.result/agent; got %+v", evs[5])
	}
	if evs[5].Payload["duration_ms"] != float64(123) {
		t.Fatalf("evs[5] duration_ms = %v; want 123", evs[5].Payload["duration_ms"])
	}
	if evs[5].Payload["terminal_reason"] != "success" {
		t.Fatalf("evs[5] terminal_reason = %v; want success (from subtype)", evs[5].Payload["terminal_reason"])
	}

	// Event 6: completion — legacy alias, unchanged frame passthrough.
	if evs[6].Kind != "completion" || evs[6].Producer != "agent" ||
		evs[6].Payload["subtype"] != "success" {
		t.Fatalf("evs[6] want completion/success; got %+v", evs[6])
	}

	// Event 7: lifecycle.stopped.
	if evs[7].Kind != "lifecycle" || evs[7].Payload["phase"] != "stopped" {
		t.Fatalf("evs[7] want lifecycle.stopped; got %+v", evs[7])
	}
}

// TestStdioDriver_RichSessionInit covers the expanded session.init payload —
// cwd, permission_mode, mcp_servers, slash_commands, version, etc. — with
// camelCase / snake_case tolerance because claude-code's stream-json drifts
// between releases. Mobile dispatches by field presence, so absent fields
// must come through as nil rather than missing keys.
func TestStdioDriver_RichSessionInit(t *testing.T) {
	frame := `{"type":"system","subtype":"init",` +
		`"session_id":"sess-x","model":"claude-opus-4-7","cwd":"/repo",` +
		`"permissionMode":"acceptEdits","tools":["Read","Edit"],` +
		`"mcp_servers":[{"name":"hub"}],` +
		`"slashCommands":["/help","/clear"],` +
		`"agents":["steward"],"skills":["statusline"],"plugins":[],` +
		`"claude_code_version":"2.6.0",` +
		`"outputStyle":"default",` +
		`"fastModeState":{"available":true}}`
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{AgentID: "agent-init", Poster: poster, Stdout: pr,
		Closer: func() { _ = pw.Close() }}
	_ = drv.Start(context.Background())
	go func() { _, _ = pw.Write([]byte(frame + "\n")) }()
	poster.wait(t, 2, time.Second) // started + session.init
	drv.Stop()

	evs := poster.snapshot()
	var init postedEvent
	for _, e := range evs {
		if e.Kind == "session.init" {
			init = e
			break
		}
	}
	if init.Kind == "" {
		t.Fatalf("no session.init event; got %+v", evs)
	}
	p := init.Payload
	if p["cwd"] != "/repo" {
		t.Errorf("cwd = %v; want /repo", p["cwd"])
	}
	if p["permission_mode"] != "acceptEdits" {
		t.Errorf("permission_mode = %v; want acceptEdits (from camelCase source)", p["permission_mode"])
	}
	if p["version"] != "2.6.0" {
		t.Errorf("version = %v; want 2.6.0 (from claude_code_version)", p["version"])
	}
	if p["output_style"] != "default" {
		t.Errorf("output_style = %v; want default (from camelCase outputStyle)", p["output_style"])
	}
	if _, ok := p["fast_mode_state"].(map[string]any); !ok {
		t.Errorf("fast_mode_state = %v; want map (from camelCase fastModeState)", p["fast_mode_state"])
	}
	if mcp, ok := p["mcp_servers"].([]any); !ok || len(mcp) != 1 {
		t.Errorf("mcp_servers = %v; want 1-elem slice", p["mcp_servers"])
	}
	if sc, ok := p["slash_commands"].([]any); !ok || len(sc) != 2 {
		t.Errorf("slash_commands = %v; want 2-elem slice (from camelCase)", p["slash_commands"])
	}
}

// TestStdioDriver_UsageEventFromAssistant covers the per-message usage
// extraction: when an assistant frame's message.usage is present, the
// driver emits a typed `usage` event (linked back via message_id) so the
// telemetry strip can render token counts without peeking at text payloads.
func TestStdioDriver_UsageEventFromAssistant(t *testing.T) {
	frame := `{"type":"assistant","message":{"id":"msg_42","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":120,"output_tokens":35,"cache_read_input_tokens":900,"cache_creation_input_tokens":50,"service_tier":"standard"}}}`
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{AgentID: "agent-usage", Poster: poster, Stdout: pr,
		Closer: func() { _ = pw.Close() }}
	_ = drv.Start(context.Background())
	go func() { _, _ = pw.Write([]byte(frame + "\n")) }()
	poster.wait(t, 3, time.Second) // started + text + usage
	drv.Stop()

	evs := poster.snapshot()
	var text, usage postedEvent
	for _, e := range evs {
		switch e.Kind {
		case "text":
			text = e
		case "usage":
			usage = e
		}
	}
	if text.Payload["message_id"] != "msg_42" {
		t.Errorf("text message_id = %v; want msg_42", text.Payload["message_id"])
	}
	if usage.Kind == "" {
		t.Fatalf("no usage event; got %+v", evs)
	}
	p := usage.Payload
	if p["input_tokens"] != float64(120) {
		t.Errorf("input_tokens = %v; want 120", p["input_tokens"])
	}
	if p["output_tokens"] != float64(35) {
		t.Errorf("output_tokens = %v; want 35", p["output_tokens"])
	}
	if p["cache_read"] != float64(900) {
		t.Errorf("cache_read = %v; want 900 (lifted from cache_read_input_tokens)", p["cache_read"])
	}
	if p["cache_create"] != float64(50) {
		t.Errorf("cache_create = %v; want 50 (lifted from cache_creation_input_tokens)", p["cache_create"])
	}
	if p["model"] != "claude-opus-4-7" {
		t.Errorf("model = %v; want claude-opus-4-7", p["model"])
	}
	if p["message_id"] != "msg_42" {
		t.Errorf("usage message_id = %v; want msg_42", p["message_id"])
	}
}

// TestStdioDriver_AssistantWithoutUsageDoesNotEmit guards against spurious
// usage events when the message has no usage block — drivers shouldn't
// invent telemetry the agent didn't send.
func TestStdioDriver_AssistantWithoutUsageDoesNotEmit(t *testing.T) {
	frame := `{"type":"assistant","message":{"id":"msg_x","role":"assistant","content":[{"type":"text","text":"hi"}]}}`
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{AgentID: "agent-noU", Poster: poster, Stdout: pr,
		Closer: func() { _ = pw.Close() }}
	_ = drv.Start(context.Background())
	go func() { _, _ = pw.Write([]byte(frame + "\n")) }()
	poster.wait(t, 2, time.Second) // started + text
	drv.Stop()

	for _, e := range poster.snapshot() {
		if e.Kind == "usage" {
			t.Fatalf("usage event emitted without source usage block: %+v", e)
		}
	}
}

// TestStdioDriver_RateLimitEvent normalizes the rate_limit_event frame —
// claude uses camelCase rateLimitType / resetsAt / overageDisabledReason,
// our typed shape uses snake_case. Mobile reads `overage_disabled` as a
// bool flag; the driver coerces non-nil overageDisabledReason to true.
func TestStdioDriver_RateLimitEvent(t *testing.T) {
	frame := `{"type":"rate_limit_event","rateLimitType":"5h","status":"warn","resetsAt":"2026-04-25T13:00:00Z","overageStatus":"available","overageDisabledReason":null,"isUsingOverage":false}`
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{AgentID: "agent-rl", Poster: poster, Stdout: pr,
		Closer: func() { _ = pw.Close() }}
	_ = drv.Start(context.Background())
	go func() { _, _ = pw.Write([]byte(frame + "\n")) }()
	poster.wait(t, 2, time.Second) // started + rate_limit
	drv.Stop()

	var rl postedEvent
	for _, e := range poster.snapshot() {
		if e.Kind == "rate_limit" {
			rl = e
			break
		}
	}
	if rl.Kind == "" {
		t.Fatalf("no rate_limit event; got %+v", poster.snapshot())
	}
	p := rl.Payload
	if p["window"] != "5h" {
		t.Errorf("window = %v; want 5h", p["window"])
	}
	if p["status"] != "warn" {
		t.Errorf("status = %v; want warn", p["status"])
	}
	if p["resets_at"] != "2026-04-25T13:00:00Z" {
		t.Errorf("resets_at = %v; want 2026-04-25T13:00:00Z", p["resets_at"])
	}
	if p["overage_status"] != "available" {
		t.Errorf("overage_status = %v; want available", p["overage_status"])
	}
	if p["overage_disabled"] != false {
		t.Errorf("overage_disabled = %v; want false (null reason → not disabled)", p["overage_disabled"])
	}
}

func TestStdioDriver_RateLimitDisabledFromReason(t *testing.T) {
	frame := `{"type":"rate_limit_event","rate_limit_type":"1h","status":"limited","overage_disabled_reason":"opted_out"}`
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{AgentID: "agent-rl2", Poster: poster, Stdout: pr,
		Closer: func() { _ = pw.Close() }}
	_ = drv.Start(context.Background())
	go func() { _, _ = pw.Write([]byte(frame + "\n")) }()
	poster.wait(t, 2, time.Second)
	drv.Stop()

	var rl postedEvent
	for _, e := range poster.snapshot() {
		if e.Kind == "rate_limit" {
			rl = e
			break
		}
	}
	if rl.Payload["window"] != "1h" {
		t.Errorf("window = %v; want 1h (snake_case source)", rl.Payload["window"])
	}
	if rl.Payload["overage_disabled"] != true {
		t.Errorf("overage_disabled = %v; want true (non-nil reason)", rl.Payload["overage_disabled"])
	}
	if rl.Payload["reason"] != "opted_out" {
		t.Errorf("reason = %v; want opted_out", rl.Payload["reason"])
	}
}

// Recent claude-code SDK versions wrap the rate-limit signal under
// type=system,subtype=rate_limit_event instead of emitting it as a
// bare top-level type. The driver must dispatch both shapes to the
// same translator so the mobile telemetry strip lights up regardless
// of which version the spawned agent runs.
func TestStdioDriver_RateLimitEventUnderSystemSubtype(t *testing.T) {
	frame := `{"type":"system","subtype":"rate_limit_event","rateLimitType":"5h","status":"allowed","resetsAt":"2026-04-25T13:00:00Z"}`
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{AgentID: "agent-rl3", Poster: poster, Stdout: pr,
		Closer: func() { _ = pw.Close() }}
	_ = drv.Start(context.Background())
	go func() { _, _ = pw.Write([]byte(frame + "\n")) }()
	poster.wait(t, 2, time.Second)
	drv.Stop()

	var rl postedEvent
	for _, e := range poster.snapshot() {
		if e.Kind == "rate_limit" {
			rl = e
			break
		}
	}
	if rl.Kind == "" {
		t.Fatalf("no rate_limit event from system+subtype frame; got %+v", poster.snapshot())
	}
	if rl.Payload["window"] != "5h" {
		t.Errorf("window = %v; want 5h", rl.Payload["window"])
	}
	if rl.Payload["status"] != "allowed" {
		t.Errorf("status = %v; want allowed", rl.Payload["status"])
	}
	for _, e := range poster.snapshot() {
		if e.Kind == "system" {
			t.Errorf("rate_limit_event was passed through as kind=system instead of being translated: %+v", e)
		}
	}
}

// Current claude-code (Opus 4.7-era) wraps the rate-limit fields
// under a `rate_limit_info` sub-object instead of putting them at the
// top of the frame. translateRateLimit must dig in; otherwise the
// mobile telemetry strip stays empty even when the SDK is shouting
// about quota.
func TestStdioDriver_RateLimitEventNestedInfo(t *testing.T) {
	frame := `{"type":"rate_limit_event","rate_limit_info":{` +
		`"status":"allowed","resetsAt":1777443000,` +
		`"rateLimitType":"five_hour",` +
		`"overageStatus":"rejected",` +
		`"overageDisabledReason":"org_level_disabled_until",` +
		`"isUsingOverage":false},` +
		`"uuid":"31018394-6e25-4d7a-8e2f-bf7ba4d88eff",` +
		`"session_id":"c621a4ac-cd41-4be7-9255-2b0ec79ea9e8"}`
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{AgentID: "agent-rl4", Poster: poster, Stdout: pr,
		Closer: func() { _ = pw.Close() }}
	_ = drv.Start(context.Background())
	go func() { _, _ = pw.Write([]byte(frame + "\n")) }()
	poster.wait(t, 2, time.Second)
	drv.Stop()

	var rl postedEvent
	for _, e := range poster.snapshot() {
		if e.Kind == "rate_limit" {
			rl = e
			break
		}
	}
	if rl.Kind == "" {
		t.Fatalf("no rate_limit event from nested rate_limit_info; got %+v",
			poster.snapshot())
	}
	if rl.Payload["window"] != "five_hour" {
		t.Errorf("window = %v; want five_hour", rl.Payload["window"])
	}
	if rl.Payload["status"] != "allowed" {
		t.Errorf("status = %v; want allowed", rl.Payload["status"])
	}
	if rl.Payload["overage_status"] != "rejected" {
		t.Errorf("overage_status = %v; want rejected", rl.Payload["overage_status"])
	}
	if rl.Payload["overage_disabled"] != true {
		t.Errorf("overage_disabled = %v; want true", rl.Payload["overage_disabled"])
	}
}

// TestStdioDriver_TurnResultNormalization covers normalizeTurnResult's
// modelUsage → by_model lift with camelCase inner keys, plus cost / fast
// mode passthrough. Verified through the driver path so the wiring is
// covered, not just the helper.
func TestStdioDriver_TurnResultNormalization(t *testing.T) {
	frame := `{"type":"result","subtype":"success","duration_ms":4500,"num_turns":3,` +
		`"total_cost_usd":0.0123,"permission_denials":[],` +
		`"fastModeState":{"available":true},` +
		`"modelUsage":{"claude-opus-4-7":{"inputTokens":1200,"outputTokens":340,` +
		`"cacheReadInputTokens":9100,"cacheCreationInputTokens":80,` +
		`"costUSD":0.0123,"contextWindow":200000,"maxOutputTokens":8192}}}`
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{AgentID: "agent-tr", Poster: poster, Stdout: pr,
		Closer: func() { _ = pw.Close() }}
	_ = drv.Start(context.Background())
	go func() { _, _ = pw.Write([]byte(frame + "\n")) }()
	poster.wait(t, 3, time.Second) // started + turn.result + completion
	drv.Stop()

	var tr postedEvent
	for _, e := range poster.snapshot() {
		if e.Kind == "turn.result" {
			tr = e
			break
		}
	}
	if tr.Kind == "" {
		t.Fatalf("no turn.result event; got %+v", poster.snapshot())
	}
	p := tr.Payload
	if p["cost_usd"] != 0.0123 {
		t.Errorf("cost_usd = %v; want 0.0123 (from total_cost_usd)", p["cost_usd"])
	}
	if p["num_turns"] != float64(3) {
		t.Errorf("num_turns = %v; want 3", p["num_turns"])
	}
	if p["terminal_reason"] != "success" {
		t.Errorf("terminal_reason = %v; want success", p["terminal_reason"])
	}
	byModel, ok := p["by_model"].(map[string]any)
	if !ok {
		t.Fatalf("by_model = %v; want map", p["by_model"])
	}
	model, ok := byModel["claude-opus-4-7"].(map[string]any)
	if !ok {
		t.Fatalf("by_model[claude-opus-4-7] = %v; want map", byModel["claude-opus-4-7"])
	}
	if model["input"] != float64(1200) {
		t.Errorf("by_model.input = %v; want 1200 (from inputTokens)", model["input"])
	}
	if model["output"] != float64(340) {
		t.Errorf("by_model.output = %v; want 340", model["output"])
	}
	if model["cache_read"] != float64(9100) {
		t.Errorf("by_model.cache_read = %v; want 9100", model["cache_read"])
	}
	if model["context_window"] != float64(200000) {
		t.Errorf("by_model.context_window = %v; want 200000", model["context_window"])
	}
}

// TestStdioDriver_NonJSONLineForwardedAsRaw guards the "don't silently drop
// bytes" behaviour: if the child emits a stray non-JSON line (stderr bleed,
// prompt, whatever) it should surface as a raw event, not disappear.
func TestStdioDriver_NonJSONLineForwardedAsRaw(t *testing.T) {
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{
		AgentID: "agent-m2b",
		Poster:  poster,
		Stdout:  pr,
		Closer:  func() { _ = pw.Close() },
	}
	_ = drv.Start(context.Background())
	go func() {
		_, _ = pw.Write([]byte("not json at all\n"))
	}()
	poster.wait(t, 2, time.Second) // started + raw
	drv.Stop()

	evs := poster.snapshot()
	var rawText string
	for _, e := range evs {
		if e.Kind == "raw" && e.Producer == "agent" {
			rawText, _ = e.Payload["text"].(string)
		}
	}
	if !strings.Contains(rawText, "not json") {
		t.Fatalf("expected raw event with non-JSON text; got events=%+v", evs)
	}
}

// syncBuf is a thread-safe bytes.Buffer so Input writes and test reads
// don't race the scanner goroutine.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, s.buf.Len())
	copy(out, s.buf.Bytes())
	return out
}

// TestStdioDriver_InputFrames asserts Input translates each kind into the
// stream-json user frame shape Claude Code expects on stdin.
func TestStdioDriver_InputFrames(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		payload map[string]any
		check   func(t *testing.T, frame map[string]any)
	}{
		{
			name:    "text",
			kind:    "text",
			payload: map[string]any{"body": "hello agent"},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				content := msg["content"].([]any)
				block := content[0].(map[string]any)
				if block["type"] != "text" || block["text"] != "hello agent" {
					t.Fatalf("text block wrong: %+v", block)
				}
			},
		},
		{
			name: "approval",
			kind: "approval",
			payload: map[string]any{
				"request_id": "toolu_42",
				"decision":   "allow",
				"note":       "looks fine",
			},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if block["type"] != "tool_result" || block["tool_use_id"] != "toolu_42" ||
					block["content"] != "allow: looks fine" || block["is_error"] != false {
					t.Fatalf("approval block wrong: %+v", block)
				}
			},
		},
		{
			name: "approval deny",
			kind: "approval",
			payload: map[string]any{
				"request_id": "toolu_99",
				"decision":   "deny",
			},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if block["is_error"] != true {
					t.Fatalf("deny should set is_error=true; got %+v", block)
				}
			},
		},
		{
			name: "answer",
			kind: "answer",
			payload: map[string]any{
				"request_id": "toolu_AskUserQuestion_42",
				"body":       "Red",
			},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if block["type"] != "tool_result" {
					t.Fatalf("answer block type = %v; want tool_result", block["type"])
				}
				if block["tool_use_id"] != "toolu_AskUserQuestion_42" {
					t.Fatalf("answer tool_use_id wrong: %+v", block)
				}
				if block["content"] != "Red" {
					t.Fatalf("answer content = %v; want Red (no decision prefix)", block["content"])
				}
				if block["is_error"] != false {
					t.Fatalf("answer is_error = %v; want false", block["is_error"])
				}
			},
		},
		{
			name:    "cancel",
			kind:    "cancel",
			payload: map[string]any{"reason": "too slow"},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if block["type"] != "text" || !strings.Contains(block["text"].(string), "too slow") {
					t.Fatalf("cancel block wrong: %+v", block)
				}
			},
		},
		{
			name:    "attach",
			kind:    "attach",
			payload: map[string]any{"document_id": "doc-7"},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if !strings.Contains(block["text"].(string), "doc-7") {
					t.Fatalf("attach block missing doc-7: %+v", block)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := buildStreamJSONInputFrame(c.kind, c.payload)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if len(b) == 0 || b[len(b)-1] != '\n' {
				t.Fatal("frame must end with newline")
			}
			var frame map[string]any
			if err := json.Unmarshal(b, &frame); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if frame["type"] != "user" {
				t.Fatalf("want type=user; got %v", frame["type"])
			}
			c.check(t, frame)
		})
	}
}

func TestStdioDriver_InputMissingFields(t *testing.T) {
	if _, err := buildStreamJSONInputFrame("text", map[string]any{}); err == nil {
		t.Fatal("text without body should error")
	}
	if _, err := buildStreamJSONInputFrame("approval", map[string]any{"decision": "allow"}); err == nil {
		t.Fatal("approval without request_id should error")
	}
	if _, err := buildStreamJSONInputFrame("answer", map[string]any{"body": "x"}); err == nil {
		t.Fatal("answer without request_id should error")
	}
	if _, err := buildStreamJSONInputFrame("answer", map[string]any{"request_id": "r"}); err == nil {
		t.Fatal("answer without body should error")
	}
	if _, err := buildStreamJSONInputFrame("attach", map[string]any{}); err == nil {
		t.Fatal("attach without document_id should error")
	}
	if _, err := buildStreamJSONInputFrame("bogus", map[string]any{}); err == nil {
		t.Fatal("unknown kind should error")
	}
}

func TestStdioDriver_InputWritesToStdin(t *testing.T) {
	sink := &syncBuf{}
	pr, pw := io.Pipe()
	defer pw.Close()
	drv := &StdioDriver{
		AgentID: "agent-input",
		Poster:  &fakePoster{},
		Stdout:  pr,
		Stdin:   sink,
		Closer:  func() { _ = pw.Close() },
	}
	_ = drv.Start(context.Background())
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	got := string(sink.Bytes())
	if !strings.Contains(got, `"type":"user"`) || !strings.Contains(got, `"text":"hi"`) {
		t.Fatalf("stdin missing expected frame; got %q", got)
	}
}

func TestStdioDriver_InputRejectsWithoutStdin(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	drv := &StdioDriver{
		AgentID: "agent-nostdin",
		Poster:  &fakePoster{},
		Stdout:  pr,
		Closer:  func() { _ = pw.Close() },
	}
	_ = drv.Start(context.Background())
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"}); err == nil {
		t.Fatal("expected error when Stdin is nil")
	}
}

func TestStdioDriver_StopIsIdempotent(t *testing.T) {
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{
		AgentID: "agent-m2c",
		Poster:  poster,
		Stdout:  pr,
		Closer:  func() { _ = pw.Close() },
	}
	_ = drv.Start(context.Background())
	drv.Stop()
	drv.Stop() // must not panic or double-emit stopped
}
