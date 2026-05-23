package antigravity

import (
	"bytes"
	"os"
	"testing"
)

// TestMapStep_Corpus locks the mapper against a real agy 1.0.1 transcript
// (testdata/corpus.jsonl — the MCP ping round-trip, host-captured). The
// 9 lines exercise USER_INPUT/CONVERSATION_HISTORY drops, two
// PLANNER_RESPONSE shapes (tool_calls vs final text), and three
// tool-result types (LIST_DIRECTORY, VIEW_FILE, MCP_TOOL).
func TestMapStep_Corpus(t *testing.T) {
	data, err := os.ReadFile("testdata/corpus.jsonl")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	var kinds []string
	for _, raw := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		evs, err := MapStep(raw)
		if err != nil {
			t.Fatalf("MapStep error on %q: %v", raw, err)
		}
		for _, ev := range evs {
			kinds = append(kinds, ev.Kind)
			// Every event must carry the coalescing keys.
			if _, ok := ev.Payload["agy_step_index"]; !ok {
				t.Errorf("event kind=%s missing agy_step_index", ev.Kind)
			}
			if _, ok := ev.Payload["agy_status"]; !ok {
				t.Errorf("event kind=%s missing agy_status", ev.Kind)
			}
		}
	}

	// Expected fan-out for the captured 9-line conversation:
	//  USER_INPUT            → session.init (model extracted from agy's
	//                          <USER_SETTINGS_CHANGE> block — v1.0.655)
	//  CONVERSATION_HISTORY  → (drop)
	//  PLANNER_RESPONSE+calls→ tool_call (list_dir)
	//  LIST_DIRECTORY        → tool_result
	//  PLANNER_RESPONSE+calls→ tool_call (view_file)
	//  VIEW_FILE             → tool_result
	//  PLANNER_RESPONSE+calls→ tool_call (read mcp)
	//  MCP_TOOL              → tool_result
	//  PLANNER_RESPONSE+text → text + turn.result (v1.0.647 — the latter
	//                          is agy's end-of-turn marker so mobile's
	//                          _isAgentBusy() drops the cancel button)
	want := []string{
		"session.init",
		"tool_call", "tool_result",
		"tool_call", "tool_result",
		"tool_call", "tool_result",
		"text", "turn.result",
	}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v (%d); want %v (%d)", kinds, len(kinds), want, len(want))
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds[%d] = %q; want %q (full: %v)", i, kinds[i], want[i], kinds)
		}
	}
}

// PLANNER_RESPONSE with content + no tool_calls + status=DONE is agy's
// end-of-turn marker. The mapper must emit `text` THEN `turn.result` so
// mobile's _isAgentBusy() drops the cancel button. v1.0.647.
func TestMapStep_PlannerFinalText_EmitsTurnResult(t *testing.T) {
	raw := []byte(`{"step_index":147,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Yes, I copy you loud and clear."}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 events (text + turn.result); got %d (%+v)", len(evs), evs)
	}
	if evs[0].Kind != "text" || evs[0].Payload["text"] != "Yes, I copy you loud and clear." {
		t.Errorf("event 0 = %+v; want text with content", evs[0])
	}
	if evs[1].Kind != "turn.result" {
		t.Errorf("event 1 kind = %q; want turn.result", evs[1].Kind)
	}
	if evs[1].Producer != "agent" {
		t.Errorf("turn.result producer = %q; want agent", evs[1].Producer)
	}
	if evs[1].Payload["reason"] != "end_of_turn" {
		t.Errorf("turn.result reason = %v; want end_of_turn", evs[1].Payload["reason"])
	}
	// Coalescing keys still required on the synthetic event.
	if _, ok := evs[1].Payload["agy_step_index"]; !ok {
		t.Errorf("turn.result missing agy_step_index")
	}
}

// A PLANNER_RESPONSE with content but status=RUNNING (streaming
// placeholder, not yet finalised) must NOT emit turn.result — only the
// text event. Locks the contract that turn.result lands exactly once
// per real turn end, on the DONE finalisation.
func TestMapStep_PlannerStreaming_NoTurnResult(t *testing.T) {
	raw := []byte(`{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","status":"RUNNING","content":"Yes, I cop"}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("want 1 event (text only on RUNNING); got %d (%+v)", len(evs), evs)
	}
	if evs[0].Kind != "text" {
		t.Errorf("got kind %q; want text", evs[0].Kind)
	}
}

func TestMapStep_PlannerToolCallShape(t *testing.T) {
	raw := []byte(`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"list_dir","args":{"DirectoryPath":"/x"}}]}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != "tool_call" {
		t.Fatalf("want one tool_call; got %+v", evs)
	}
	if evs[0].Payload["name"] != "list_dir" {
		t.Fatalf("name = %v; want list_dir", evs[0].Payload["name"])
	}
	if evs[0].Payload["tool_use_id"] != "agy-2-0" {
		t.Fatalf("tool_use_id = %v; want agy-2-0", evs[0].Payload["tool_use_id"])
	}
}

// Tool failures (agy sets status=ERROR) must surface as is_error=true
// on the tool_result event so mobile's tool_result card renders in
// red and folds correctly into the parent tool_call. v1.0.649 — the
// W11 smoke saw MCP failures + Permission-denied responses arrive as
// is_error=false, hiding them from the principal.
func TestMapStep_ErrorStatusPropagatesIsError(t *testing.T) {
	raw := []byte(`{"step_index":6,"source":"MODEL","type":"MCP_TOOL","status":"ERROR","content":"connection closed: invalid request"}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != "tool_result" {
		t.Fatalf("want one tool_result; got %+v", evs)
	}
	if evs[0].Payload["is_error"] != true {
		t.Errorf("agy_status=ERROR must produce is_error=true; got %v",
			evs[0].Payload["is_error"])
	}
}

// agy puts humanised intent strings on every tool_call's args
// (`toolAction` + `toolSummary`). Mobile renders them as the card's
// subtitle so the principal sees "Querying matching attentions from
// database" rather than just `grep_search(args:...)`. v1.0.650.
func TestMapStep_PlannerToolCall_SurfacesAgyActionStrings(t *testing.T) {
	raw := []byte(`{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"grep_search","args":{"Query":"foo","SearchPath":"/x","toolAction":"Querying matching attentions from database","toolSummary":"Grep search"}}]}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != "tool_call" {
		t.Fatalf("want one tool_call; got %+v", evs)
	}
	if evs[0].Payload["tool_action"] != "Querying matching attentions from database" {
		t.Errorf("tool_action not surfaced: %v", evs[0].Payload["tool_action"])
	}
	if evs[0].Payload["tool_summary"] != "Grep search" {
		t.Errorf("tool_summary not surfaced: %v", evs[0].Payload["tool_summary"])
	}
}

// A tool_call without toolAction/toolSummary on its args (a non-agy
// engine that later adopts the same shape) must not crash and must
// not synthesise empty fields.
func TestMapStep_PlannerToolCall_NoActionStringsAbsent(t *testing.T) {
	raw := []byte(`{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"/x"}}]}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := evs[0].Payload["tool_action"]; ok {
		t.Errorf("tool_action should be absent when args lacks it; got %v", evs[0].Payload)
	}
}

func TestMapStep_DoneStatusKeepsIsErrorFalse(t *testing.T) {
	raw := []byte(`{"step_index":7,"source":"MODEL","type":"VIEW_FILE","status":"DONE","content":"file contents..."}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Payload["is_error"] != false {
		t.Errorf("agy_status=DONE must produce is_error=false; got %v",
			evs[0].Payload["is_error"])
	}
}

// A type agy adds tomorrow that carries content renders as a tool_result
// (named after the type) rather than being dropped.
func TestMapStep_UnknownContentTypeIsToolResult(t *testing.T) {
	raw := []byte(`{"step_index":4,"source":"MODEL","type":"BROWSER_NAVIGATE","status":"DONE","content":"navigated to https://x"}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != "tool_result" {
		t.Fatalf("want one tool_result; got %+v", evs)
	}
	if evs[0].Payload["name"] != "browser_navigate" {
		t.Fatalf("name = %v; want browser_navigate", evs[0].Payload["name"])
	}
}

// An unknown type with no content surfaces as drift, never silently dropped.
func TestMapStep_UnknownEmptyTypeIsDrift(t *testing.T) {
	raw := []byte(`{"step_index":4,"source":"MODEL","type":"MYSTERY","status":"DONE"}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != "system" || evs[0].Payload["subtype"] != "unknown_type" {
		t.Fatalf("want one system/unknown_type; got %+v", evs)
	}
}

// agy embeds its active model in the <USER_SETTINGS_CHANGE> block on
// step 0 — the only on-disk signal of which model is answering (token
// counts + cost stay in-memory only). The mapper extracts that string
// into a synthetic session.init so mobile's AppBar SessionInitChip can
// show "Gemini 3.5 Flash (Medium)" instead of an empty model pill. The
// adapter later stamps `session_id` on the same payload so the mobile
// merge in _latestSessionInitPayload pairs it with the adapter's
// earlier session.init.
func TestMapStep_UserInput_EmitsSessionInitWithModel(t *testing.T) {
	raw := []byte(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>\n<USER_SETTINGS_CHANGE>\nThe user changed setting ` + "`Model Selection`" + ` from None to Gemini 3.5 Flash (Medium). No need to comment.\n</USER_SETTINGS_CHANGE>"}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != "session.init" {
		t.Fatalf("want one session.init; got %+v", evs)
	}
	if evs[0].Payload["model"] != "Gemini 3.5 Flash (Medium)" {
		t.Errorf("model = %v; want %q",
			evs[0].Payload["model"], "Gemini 3.5 Flash (Medium)")
	}
	// session_id is stamped by the adapter, not the mapper — must be
	// absent at this layer so the adapter knows to fill it.
	if _, ok := evs[0].Payload["session_id"]; ok {
		t.Error("mapper-emitted session.init should not carry session_id")
	}
}

// A USER_INPUT without the <USER_SETTINGS_CHANGE> block must NOT emit
// any event (resume / follow-up turns don't carry the model
// announcement; we don't want a stream of empty session.init events).
func TestMapStep_UserInput_NoSettingsChangeIsDropped(t *testing.T) {
	raw := []byte(`{"step_index":3,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nfollow up\n</USER_REQUEST>"}`)
	evs, err := MapStep(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Errorf("USER_INPUT without USER_SETTINGS_CHANGE should be dropped; got %+v", evs)
	}
}

func TestExtractAntigravityModel(t *testing.T) {
	cases := map[string]string{
		// Host-verified shape from agy 1.0.1.
		"<USER_SETTINGS_CHANGE>\nThe user changed setting `Model Selection` from None to Gemini 3.5 Flash (Medium). No need to comment.\n</USER_SETTINGS_CHANGE>": "Gemini 3.5 Flash (Medium)",
		// Same shape with a different model name.
		"<USER_SETTINGS_CHANGE>\nThe user changed setting `Model Selection` from Gemini 3.5 Flash to Gemini 2.5 Pro (Reasoning). Continue.\n</USER_SETTINGS_CHANGE>": "Gemini 2.5 Pro (Reasoning)",
		// No settings change block → empty.
		"<USER_REQUEST>\nhello\n</USER_REQUEST>": "",
		// Settings change but unrelated setting → empty.
		"<USER_SETTINGS_CHANGE>\nThe user changed setting `Theme` from Light to Dark.\n</USER_SETTINGS_CHANGE>": "",
		// Empty content.
		"": "",
	}
	for in, want := range cases {
		got := extractAntigravityModel(in)
		if got != want {
			t.Errorf("extractAntigravityModel(%q) = %q; want %q", in[:min(len(in), 60)], got, want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestMapStep_MalformedTopLevelErrors(t *testing.T) {
	if _, err := MapStep([]byte(`{not json`)); err == nil {
		t.Fatal("want error on malformed JSON; got nil")
	}
}
