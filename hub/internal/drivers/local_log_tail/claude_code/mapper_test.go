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

func TestMapLine_UserStringIsUserInput(t *testing.T) {
	got := mustMap(t, `{"type":"user","message":{"content":"hello there"}}`)
	if len(got) != 1 || got[0].Kind != "user_input" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Producer != "user" {
		t.Errorf("producer = %q, want user", got[0].Producer)
	}
	if got[0].Payload["text"] != "hello there" {
		t.Errorf("text = %v", got[0].Payload["text"])
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

func TestMapLine_KnownDroppedTypes(t *testing.T) {
	for _, ty := range []string{
		"permission-mode", "custom-title", "agent-name",
		"last-prompt", "file-history-snapshot", "queue-operation",
	} {
		raw := `{"type":"` + ty + `"}`
		got := mustMap(t, raw)
		if len(got) != 0 {
			t.Errorf("type=%s emitted %+v; want drop", ty, got)
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
func TestMapLine_MultiLineSessionOrdering(t *testing.T) {
	lines := []string{
		`{"type":"user","message":{"content":"list files"}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"file1\nfile2"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}`,
	}
	wantKinds := []string{"user_input", "tool_call", "tool_result", "text"}
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
