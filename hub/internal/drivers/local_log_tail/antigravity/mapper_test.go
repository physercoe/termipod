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
	//  PLANNER_RESPONSE+text → text (the PONG token)
	want := []string{
		"tool_call", "tool_result",
		"tool_call", "tool_result",
		"tool_call", "tool_result",
		"text",
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
