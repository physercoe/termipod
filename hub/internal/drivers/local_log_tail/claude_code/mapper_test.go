package claudecode

import (
	"reflect"
	"strings"
	"testing"
)

func mustMap(t *testing.T, raw string) []MappedEvent {
	t.Helper()
	out, err := MapLine([]byte(raw))
	if err != nil {
		t.Fatalf("MapLine(%q): %v", raw, err)
	}
	return out
}

func TestMapLine_AssistantText(t *testing.T) {
	got := mustMap(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`)
	if len(got) != 1 || got[0].Kind != "text" || got[0].Producer != "agent" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Payload["text"] != "hello" {
		t.Errorf("payload text = %v", got[0].Payload["text"])
	}
}

func TestMapLine_AssistantThinking_MarkerOnly(t *testing.T) {
	got := mustMap(t, `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"","signature":"sig123"}]}}`)
	if len(got) != 1 || got[0].Kind != "thought" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Payload["text"] != "Thinking…" {
		t.Errorf("thought text = %v", got[0].Payload["text"])
	}
	if got[0].Payload["marker_only"] != true {
		t.Errorf("marker_only = %v", got[0].Payload["marker_only"])
	}
	if got[0].Payload["signature_present"] != true {
		t.Errorf("signature_present = %v", got[0].Payload["signature_present"])
	}
}

func TestMapLine_AssistantToolUse_PassesInput(t *testing.T) {
	got := mustMap(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}`)
	if len(got) != 1 || got[0].Kind != "tool_call" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Payload["tool_use_id"] != "toolu_1" {
		t.Errorf("tool_use_id = %v", got[0].Payload["tool_use_id"])
	}
	if got[0].Payload["name"] != "Bash" {
		t.Errorf("name = %v", got[0].Payload["name"])
	}
	input, _ := got[0].Payload["input"].(map[string]any)
	if input["command"] != "ls" {
		t.Errorf("input.command = %v", input["command"])
	}
}

func TestMapLine_AssistantMultipleBlocks_FanOut(t *testing.T) {
	got := mustMap(t, `{"type":"assistant","message":{"content":[
		{"type":"text","text":"a"},
		{"type":"tool_use","id":"t1","name":"X","input":{}},
		{"type":"text","text":"b"}
	]}}`)
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(got), got)
	}
	if got[0].Kind != "text" || got[1].Kind != "tool_call" || got[2].Kind != "text" {
		t.Errorf("kinds = %s %s %s", got[0].Kind, got[1].Kind, got[2].Kind)
	}
}

// v1.0.663 dropped the user_input emission this test used to assert
// on. The hub already stores the user's typed text as an `input.text`
// event at POST time (handlers_sessions.go); the JSONL echo carried
// the full hub-injected envelope ("[directive from the principal]\n
// <body>\n\nReply in this chat…") which mobile then rendered as a
// SECOND user-side message, looking like a duplicate. New invariant:
// a user.message with a STRING content emits zero events. Tool-result
// arrays (mapUserArray) still flow — see TestMapLine_UserArrayIsToolResults.
func TestMapLine_UserStringIsDropped(t *testing.T) {
	got := mustMap(t, `{"type":"user","message":{"content":"hello there"}}`)
	if len(got) != 0 {
		t.Errorf("user-string emitted events; v1.0.663 expects drop: %+v", got)
	}
}

// Malformed user.content (non-string, non-array) must still surface
// a parse-shape drift event, not crash. The mapper still parses the
// content to catch that case.
func TestMapLine_UserNumberContentSurfacesDrift(t *testing.T) {
	got := mustMap(t, `{"type":"user","message":{"content":42}}`)
	if len(got) != 1 || got[0].Kind != "system" {
		t.Fatalf("want 1 system{drift} event, got %+v", got)
	}
	if got[0].Payload["subtype"] != "user_content_drift" {
		t.Errorf("subtype = %v, want user_content_drift", got[0].Payload["subtype"])
	}
}

func TestMapLine_UserArrayIsToolResults(t *testing.T) {
	got := mustMap(t, `{"type":"user","message":{"content":[
		{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"},
		{"type":"tool_result","tool_use_id":"toolu_2","is_error":true,"content":"<tool_use_error>denied"}
	]}}`)
	if len(got) != 2 {
		t.Fatalf("want 2 tool_results, got %d: %+v", len(got), got)
	}
	if got[0].Kind != "tool_result" || got[1].Kind != "tool_result" {
		t.Errorf("kinds = %s %s", got[0].Kind, got[1].Kind)
	}
	if got[0].Payload["tool_use_id"] != "toolu_1" {
		t.Errorf("id 0 = %v", got[0].Payload["tool_use_id"])
	}
	if got[0].Payload["denied"] != false {
		t.Errorf("denied 0 = %v", got[0].Payload["denied"])
	}
	if got[1].Payload["is_error"] != true {
		t.Errorf("is_error 1 = %v", got[1].Payload["is_error"])
	}
	if got[1].Payload["denied"] != true {
		t.Errorf("denied 1 = %v (want true: content starts with <tool_use_error>)", got[1].Payload["denied"])
	}
}

func TestMapLine_ToolResultContentArrayNormalized(t *testing.T) {
	got := mustMap(t, `{"type":"user","message":{"content":[
		{"type":"tool_result","tool_use_id":"t","content":[
			{"type":"text","text":"line1"},
			{"type":"text","text":"line2"}
		]}
	]}}`)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	c, _ := got[0].Payload["content"].(string)
	if !strings.Contains(c, "line1") || !strings.Contains(c, "line2") {
		t.Errorf("normalized content = %q, want both lines", c)
	}
}

func TestMapLine_SystemCompactBoundary_Surfaced(t *testing.T) {
	got := mustMap(t, `{"type":"system","subtype":"compact_boundary"}`)
	if len(got) != 1 || got[0].Kind != "system" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Payload["subtype"] != "compact_boundary" {
		t.Errorf("subtype = %v", got[0].Payload["subtype"])
	}
}

func TestMapLine_SystemOtherSubtypesDropped(t *testing.T) {
	got := mustMap(t, `{"type":"system","subtype":"debug","details":{"x":1}}`)
	if len(got) != 0 {
		t.Errorf("want drop for system subtype=debug, got %+v", got)
	}
}

// v1.0.668: turn.result must be emitted from the JSONL's own
// `system{subtype:turn_duration}` frame (the LAST frame claude
// writes for a turn), not from the Stop hook handler. The hook
// handler used to post turn.result synchronously the moment claude
// invoked it — which raced the tailer that hadn't yet posted the
// preceding assistant text frame, leaving turn.result with a lower
// seq than text. Mobile's tail-first busy walker then hit text →
// returned busy → cancel button stuck. Mapping from turn_duration
// guarantees turn.result has the highest seq of the turn.
func TestMapLine_TurnDurationSystemEmitsTurnResult(t *testing.T) {
	raw := `{"type":"system","subtype":"turn_duration","durationMs":3054,"messageCount":8}`
	got := mustMap(t, raw)
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d: %+v", len(got), got)
	}
	if got[0].Kind != "turn.result" {
		t.Errorf("kind = %q, want turn.result", got[0].Kind)
	}
	if got[0].Producer != "agent" {
		t.Errorf("producer = %q, want agent", got[0].Producer)
	}
	if got[0].Payload["reason"] != "end_of_turn" {
		t.Errorf("reason = %v, want end_of_turn", got[0].Payload["reason"])
	}
	if got[0].Payload["status"] != "success" {
		t.Errorf("status = %v, want success", got[0].Payload["status"])
	}
	if got[0].Payload["duration_ms"] != 3054 {
		t.Errorf("duration_ms = %v, want 3054", got[0].Payload["duration_ms"])
	}
	if got[0].Payload["message_count"] != 8 {
		t.Errorf("message_count = %v, want 8", got[0].Payload["message_count"])
	}
}

func TestMapLine_KnownDroppedTypes(t *testing.T) {
	for _, ty := range []string{
		"permission-mode", "custom-title", "agent-name", "ai-title",
		"last-prompt", "file-history-snapshot", "queue-operation",
	} {
		raw := `{"type":"` + ty + `"}`
		got := mustMap(t, raw)
		if len(got) != 0 {
			t.Errorf("type=%s emitted %+v; want drop", ty, got)
		}
	}
}

// v1.0.661 filter: pure telemetry / claude-internal registry sync
// attachments must NOT fan out as kind=attachment events. Mobile was
// rendering hook-fire telemetry + tool/agent/skill registry deltas as
// noisy attachment cards on every cold start. Empirical types seen
// in a real session JSONL: hook_success, hook_error,
// deferred_tools_delta, agent_listing_delta, skill_listing.
func TestMapLine_AttachmentDropsTelemetryAndRegistry(t *testing.T) {
	for _, ty := range []string{
		"hook_success", "hook_error",
		"deferred_tools_delta", "agent_listing_delta", "skill_listing",
	} {
		raw := `{"type":"attachment","attachment":{"type":"` + ty + `","payload":"x"}}`
		got := mustMap(t, raw)
		if len(got) != 0 {
			t.Errorf("attachment.type=%s emitted %+v; want drop", ty, got)
		}
	}
}

// Counterpart guard: real attachments (any other inner type) still
// surface so we don't silently swallow legitimate content claude
// produces (file refs, image attachments, anything we haven't yet
// seen). Drift must always make it to the operator.
func TestMapLine_AttachmentRealContentStillFlows(t *testing.T) {
	raw := `{"type":"attachment","attachment":{"type":"file_ref","path":"/x/y"}}`
	got := mustMap(t, raw)
	if len(got) != 1 || got[0].Kind != "attachment" {
		t.Fatalf("want 1 kind=attachment, got %+v", got)
	}
}

// v1.0.662 — every assistant message MUST also emit a kind=usage
// event with the input + cache_read + cache_create token counts the
// mobile telemetry strip uses to render the context-window chip.
// Without this, M4 spawns showed an empty/stale chip or fell back to
// driver_stdio's per-turn by_model values, which double-counted across
// tool-use iterations.
func TestMapLine_AssistantMessageEmitsUsage(t *testing.T) {
	raw := `{"type":"assistant","message":{
		"model":"claude-opus-4-7",
		"content":[{"type":"text","text":"hi"}],
		"usage":{
			"input_tokens":6,
			"output_tokens":38,
			"cache_read_input_tokens":15806,
			"cache_creation_input_tokens":13462
		}
	}}`
	got := mustMap(t, raw)
	if len(got) < 2 {
		t.Fatalf("want at least 2 events (text + usage), got %+v", got)
	}
	// usage must be present, after text.
	var usage *MappedEvent
	for i := range got {
		if got[i].Kind == "usage" {
			usage = &got[i]
		}
	}
	if usage == nil {
		t.Fatalf("usage event not emitted: %+v", got)
	}
	if usage.Producer != "agent" {
		t.Errorf("usage producer = %q, want agent", usage.Producer)
	}
	if got, want := usage.Payload["input_tokens"], 6; got != want {
		t.Errorf("input_tokens = %v, want %v", got, want)
	}
	if got, want := usage.Payload["cache_read"], 15806; got != want {
		t.Errorf("cache_read = %v, want %v", got, want)
	}
	if got, want := usage.Payload["cache_create"], 13462; got != want {
		t.Errorf("cache_create = %v, want %v", got, want)
	}
	if got, want := usage.Payload["output_tokens"], 38; got != want {
		t.Errorf("output_tokens = %v, want %v", got, want)
	}
	if got, want := usage.Payload["model"], "claude-opus-4-7"; got != want {
		t.Errorf("model = %v, want %v", got, want)
	}
	if got, want := usage.Payload["engine"], "claude-code"; got != want {
		t.Errorf("engine = %v, want %v", got, want)
	}
	// MUST NOT be flagged cumulative — mobile uses the "latest
	// snapshot wins" path for non-cumulative events. A wrong flag
	// would route through the codex-cumulative branch and overwrite
	// rather than supersede.
	if _, isCum := usage.Payload["cumulative"]; isCum {
		t.Errorf("usage payload has cumulative key; v1.0.662 expects absent")
	}
}

// Defensive: an assistant message with NO usage block (empty
// content-only frames claude sometimes writes during partial
// streaming) must not crash and must not synthesize a fake usage
// event. The chip stays at its prior snapshot.
func TestMapLine_AssistantWithoutUsageEmitsOnlyContent(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"text","text":"x"}]}}`
	got := mustMap(t, raw)
	for _, ev := range got {
		if ev.Kind == "usage" {
			t.Errorf("usage event emitted without usage block: %+v", ev)
		}
	}
}

// v1.0.667 — usage events MUST carry context_window when the model
// name resolves to a known capacity. Without it mobile's
// context-utilisation chip suppresses itself entirely (cw==0 → no
// tile). All current claude-* models are 200K.
func TestMapLine_UsageCarriesContextWindowFromModel(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-7",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
		"claude-3-5-sonnet-20240620",
	} {
		raw := `{"type":"assistant","message":{
			"model":"` + model + `",
			"content":[{"type":"text","text":"x"}],
			"usage":{"input_tokens":1,"output_tokens":1}
		}}`
		got := mustMap(t, raw)
		var usage *MappedEvent
		for i := range got {
			if got[i].Kind == "usage" {
				usage = &got[i]
			}
		}
		if usage == nil {
			t.Fatalf("%s: usage not emitted: %+v", model, got)
		}
		if cw, _ := usage.Payload["context_window"].(int); cw != 200_000 {
			t.Errorf("%s: context_window = %v, want 200000", model, usage.Payload["context_window"])
		}
	}
}

// An unrecognised model name must NOT have a context_window field —
// better blank than wrong. Mobile then suppresses the chip.
func TestMapLine_UsageOmitsContextWindowForUnknownModel(t *testing.T) {
	raw := `{"type":"assistant","message":{
		"model":"gpt-99-future",
		"content":[{"type":"text","text":"x"}],
		"usage":{"input_tokens":1,"output_tokens":1}
	}}`
	got := mustMap(t, raw)
	for _, ev := range got {
		if ev.Kind != "usage" {
			continue
		}
		if _, has := ev.Payload["context_window"]; has {
			t.Errorf("unknown model emitted context_window: %v", ev.Payload)
		}
	}
}

// An assistant message with all-zero usage fields must NOT emit a
// usage event — the chip would be no better off seeing a {0,0,0}
// snapshot than no event at all, and the zero would replace a real
// prior value.
func TestMapLine_AssistantAllZeroUsageDropped(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[],"usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`
	got := mustMap(t, raw)
	for _, ev := range got {
		if ev.Kind == "usage" {
			t.Errorf("usage event emitted for all-zero block: %+v", ev)
		}
	}
}

func TestMapLine_UnknownTypeSurfacesAsDrift(t *testing.T) {
	got := mustMap(t, `{"type":"futuristic-event-2030"}`)
	if len(got) != 1 || got[0].Kind != "system" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Payload["subtype"] != "unknown_type" {
		t.Errorf("subtype = %v, want unknown_type", got[0].Payload["subtype"])
	}
	if got[0].Payload["type"] != "futuristic-event-2030" {
		t.Errorf("type field = %v", got[0].Payload["type"])
	}
}

func TestMapLine_UnknownAssistantBlockTypeDropped(t *testing.T) {
	got := mustMap(t, `{"type":"assistant","message":{"content":[
		{"type":"text","text":"keep"},
		{"type":"unknown_block","data":"???"},
		{"type":"text","text":"also"}
	]}}`)
	if len(got) != 2 {
		t.Fatalf("want 2 (unknown block dropped), got %d: %+v", len(got), got)
	}
	if got[0].Payload["text"] != "keep" || got[1].Payload["text"] != "also" {
		t.Errorf("kept blocks = %v", got)
	}
}

func TestMapLine_MalformedTopLevelIsError(t *testing.T) {
	_, err := MapLine([]byte(`{ not json }`))
	if err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestMapLine_WhitespaceOnlyIsNoop(t *testing.T) {
	got, err := MapLine([]byte("   \t  "))
	if err != nil {
		t.Errorf("err = %v on whitespace-only", err)
	}
	if len(got) != 0 {
		t.Errorf("want no events, got %+v", got)
	}
}

func TestMapLine_AttachmentSurfaced(t *testing.T) {
	got := mustMap(t, `{"type":"attachment","attachment":{"path":"/tmp/x.png","mime":"image/png"}}`)
	if len(got) != 1 || got[0].Kind != "attachment" {
		t.Fatalf("got %+v", got)
	}
}

func TestMapLine_UserContentDriftSurfaced(t *testing.T) {
	got := mustMap(t, `{"type":"user","message":{"content":42}}`)
	if len(got) != 1 || got[0].Kind != "system" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Payload["subtype"] != "user_content_drift" {
		t.Errorf("subtype = %v, want user_content_drift", got[0].Payload["subtype"])
	}
}

// Round-trip a tiny representative session through the mapper to
// make sure the per-block / per-line ordering survives the multi-line
// drive path. Mirrors the structure of a real claude-code session
// (user prompt, assistant tool_use, user tool_result, assistant text).
//
// v1.0.663: user.message-with-string is now dropped (see
// TestMapLine_UserStringIsDropped), so the expected sequence omits
// that first `user_input` and the assistant frames carry their
// matching `usage` events.
func TestMapLine_MultiLineSessionOrdering(t *testing.T) {
	lines := []string{
		`{"type":"user","message":{"content":"list files"}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"file1\nfile2"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}`,
	}
	wantKinds := []string{"tool_call", "tool_result", "text"}
	var got []string
	for _, l := range lines {
		evs := mustMap(t, l)
		for _, e := range evs {
			got = append(got, e.Kind)
		}
	}
	if !reflect.DeepEqual(got, wantKinds) {
		t.Errorf("kind sequence = %v, want %v", got, wantKinds)
	}
}
