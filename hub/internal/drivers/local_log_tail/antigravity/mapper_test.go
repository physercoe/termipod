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
	//  USER_INPUT            → (drop)
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

func TestMapStep_MalformedTopLevelErrors(t *testing.T) {
	if _, err := MapStep([]byte(`{not json`)); err == nil {
		t.Fatal("want error on malformed JSON; got nil")
	}
}
