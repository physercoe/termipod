package kimi_code

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func mustMap(t *testing.T, m *Mapper, line string) []MappedEvent {
	t.Helper()
	evs, err := m.MapLine([]byte(line))
	if err != nil {
		t.Fatalf("MapLine(%s): %v", line, err)
	}
	return evs
}

// tool.call → tool_call carrying id/name/input + the kimi `display`
// hint verbatim (P2's dock consumes it later) + description.
func TestMapper_ToolCallCarriesDisplayHint(t *testing.T) {
	m := NewMapper("main", "", "kimi-code-ts")
	evs := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"tool.call","uuid":"tool_a","toolCallId":"tool_a","name":"TodoList","args":{"todos":[{"title":"echo hi","status":"in_progress"}]},"description":"Updating todo list","display":{"kind":"todo_list","items":[{"title":"echo hi","status":"in_progress"}]}},"time":1}`)
	if len(evs) != 1 {
		t.Fatalf("want 1 event; got %d", len(evs))
	}
	ev := evs[0]
	if ev.Kind != "tool_call" || ev.Producer != "agent" {
		t.Fatalf("kind/producer = %s/%s", ev.Kind, ev.Producer)
	}
	if ev.Payload["tool_use_id"] != "tool_a" || ev.Payload["name"] != "TodoList" {
		t.Fatalf("payload ids = %+v", ev.Payload)
	}
	input, _ := ev.Payload["input"].(map[string]any)
	if input["todos"] == nil {
		t.Fatalf("input not forwarded: %+v", ev.Payload["input"])
	}
	display, _ := ev.Payload["display"].(map[string]any)
	if display["kind"] != "todo_list" {
		t.Fatalf("display hint not carried: %+v", ev.Payload["display"])
	}
	if ev.Payload["description"] != "Updating todo list" {
		t.Fatalf("description missing: %+v", ev.Payload)
	}
	// Main-agent events carry NO subagent stamp.
	if _, ok := ev.Payload["subagent"]; ok {
		t.Fatalf("main-agent event stamped as subagent: %+v", ev.Payload)
	}
}

// tool.result → tool_result with tool_use_id pairing + is_error from
// the wire's camelCase isError.
func TestMapper_ToolResultPairsAndPropagatesError(t *testing.T) {
	m := NewMapper("main", "", "")
	ok := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"tool.result","parentUuid":"tool_a","toolCallId":"tool_a","result":{"output":"done"}},"time":1}`)
	if len(ok) != 1 || ok[0].Kind != "tool_result" {
		t.Fatalf("want tool_result; got %+v", ok)
	}
	if ok[0].Payload["tool_use_id"] != "tool_a" || ok[0].Payload["content"] != "done" {
		t.Fatalf("payload = %+v", ok[0].Payload)
	}
	if ok[0].Payload["is_error"] != false {
		t.Fatalf("is_error = %v, want false", ok[0].Payload["is_error"])
	}
	if _, has := ok[0].Payload["truncated"]; has {
		t.Fatalf("truncated should be omitted when false: %+v", ok[0].Payload)
	}

	bad := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"tool.result","parentUuid":"tool_b","toolCallId":"tool_b","result":{"output":"HTTP 404 Not Found","isError":true,"truncated":true}},"time":2}`)
	if bad[0].Payload["is_error"] != true {
		t.Fatalf("is_error = %v, want true", bad[0].Payload["is_error"])
	}
	if bad[0].Payload["truncated"] != true {
		t.Fatalf("truncated = %v, want true", bad[0].Payload["truncated"])
	}
}

// tools.update_store key=todo → plan: full snapshot per update, kimi
// title→content, done→completed, stable per-turn message_id +
// partial:true (the ACP driver's fold-in-place convention); a new
// turn.prompt re-arms the chain.
func TestMapper_TodoStoreMapsToPlanWithPerTurnChain(t *testing.T) {
	m := NewMapper("main", "", "")
	prompt := `{"type":"turn.prompt","input":[{"type":"text","text":"do things"}],"origin":{"kind":"user"},"time":1}`
	if evs := mustMap(t, m, prompt); len(evs) != 0 {
		t.Fatalf("turn.prompt should be dropped; got %+v", evs)
	}

	first := mustMap(t, m, `{"type":"tools.update_store","key":"todo","value":[{"title":"echo hi","status":"in_progress"},{"title":"echo bye","status":"pending"}],"time":2}`)
	if len(first) != 1 || first[0].Kind != "plan" {
		t.Fatalf("want plan; got %+v", first)
	}
	p := first[0].Payload
	if p["partial"] != true || p["sessionUpdate"] != "plan" {
		t.Fatalf("plan payload missing fold markers: %+v", p)
	}
	msgID, _ := p["message_id"].(string)
	if msgID == "" {
		t.Fatalf("plan missing message_id: %+v", p)
	}
	entries, _ := p["entries"].([]map[string]any)
	if len(entries) != 2 || entries[0]["content"] != "echo hi" || entries[0]["status"] != "in_progress" {
		t.Fatalf("entries = %+v", entries)
	}

	// Second update in the SAME turn: same message_id, done→completed.
	second := mustMap(t, m, `{"type":"tools.update_store","key":"todo","value":[{"title":"echo hi","status":"done"},{"title":"echo bye","status":"in_progress"}],"time":3}`)
	if second[0].Payload["message_id"] != msgID {
		t.Fatalf("same-turn plan message_id changed: %q → %q",
			msgID, second[0].Payload["message_id"])
	}
	entries2, _ := second[0].Payload["entries"].([]map[string]any)
	if entries2[0]["status"] != "completed" {
		t.Fatalf("kimi done should normalise to completed; got %+v", entries2[0])
	}

	// A new turn re-arms the chain.
	mustMap(t, m, prompt)
	third := mustMap(t, m, `{"type":"tools.update_store","key":"todo","value":[{"title":"fresh","status":"pending"}],"time":4}`)
	if third[0].Payload["message_id"] == msgID {
		t.Fatalf("plan message_id should rotate per turn; still %q", msgID)
	}

	// Non-todo store keys are dropped.
	if evs := mustMap(t, m, `{"type":"tools.update_store","key":"scratchpad","value":{"x":1},"time":5}`); len(evs) != 0 {
		t.Fatalf("non-todo store should be dropped; got %+v", evs)
	}
}

// usage.record → usage flattened to the canonical StdioDriver/claude-M4
// shape; scope=session tagged cumulative so mobile buckets it with
// session totals instead of clobbering the current-context chip.
func TestMapper_UsageRecordFlattening(t *testing.T) {
	m := NewMapper("main", "", "kimi-code-ts")
	turn := mustMap(t, m, `{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":1915,"output":177,"inputCacheRead":19200,"inputCacheCreation":0},"usageScope":"turn","time":1}`)
	if len(turn) != 1 || turn[0].Kind != "usage" {
		t.Fatalf("want usage; got %+v", turn)
	}
	p := turn[0].Payload
	if p["input_tokens"] != 1915 || p["output_tokens"] != 177 ||
		p["cache_read"] != 19200 || p["cache_create"] != 0 {
		t.Fatalf("flattened counts wrong: %+v", p)
	}
	if p["model"] != "kimi-code/k3" || p["engine"] != "kimi-code-ts" || p["scope"] != "turn" {
		t.Fatalf("identity fields wrong: %+v", p)
	}
	if _, has := p["cumulative"]; has {
		t.Fatalf("turn-scope usage must NOT be tagged cumulative: %+v", p)
	}

	sess := mustMap(t, m, `{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":1447,"output":2123,"inputCacheRead":179712,"inputCacheCreation":0},"usageScope":"session","time":2}`)
	if sess[0].Payload["cumulative"] != true || sess[0].Payload["scope"] != "session" {
		t.Fatalf("session-scope usage missing cumulative tag: %+v", sess[0].Payload)
	}
}

// Protocol gate: v1.4 + v1.5 accepted (both observed on kimi-code
// 0.28.1), anything else → ErrUnsupportedProtocol (the launch-time
// sniff + the runtime tail both route off this).
func TestMapper_ProtocolGate(t *testing.T) {
	for _, v := range []string{"1.4", "1.5", "1.0", "1.99"} {
		if !SupportedProtocolVersion(v) {
			t.Errorf("SupportedProtocolVersion(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "2.0", "9", "0.9", "10.1", "v1.4"} {
		if SupportedProtocolVersion(v) {
			t.Errorf("SupportedProtocolVersion(%q) = true, want false", v)
		}
	}

	m := NewMapper("main", "", "")
	if _, err := m.MapLine([]byte(`{"type":"metadata","protocol_version":"1.4","created_at":1}`)); err != nil {
		t.Fatalf("v1.4 metadata rejected: %v", err)
	}
	if _, err := m.MapLine([]byte(`{"type":"metadata","protocol_version":"1.5","created_at":1}`)); err != nil {
		t.Fatalf("v1.5 metadata rejected: %v", err)
	}
	for _, bad := range []string{
		`{"type":"metadata","protocol_version":"9","created_at":1}`,
		`{"type":"metadata","created_at":1}`,
	} {
		if _, err := m.MapLine([]byte(bad)); !errors.Is(err, ErrUnsupportedProtocol) {
			t.Fatalf("metadata %s: err = %v, want ErrUnsupportedProtocol", bad, err)
		}
	}
}

// content.part: text → text, think → thought, both with the part uuid
// as message_id; empty bodies dropped.
func TestMapper_ContentParts(t *testing.T) {
	m := NewMapper("main", "", "")
	text := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"content.part","uuid":"u-1","turnId":"0","step":5,"part":{"type":"text","text":"Done. Both commands ran."}},"time":1}`)
	if len(text) != 1 || text[0].Kind != "text" || text[0].Payload["text"] != "Done. Both commands ran." {
		t.Fatalf("text part: %+v", text)
	}
	if text[0].Payload["message_id"] != "u-1" {
		t.Fatalf("message_id = %v", text[0].Payload["message_id"])
	}
	think := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"content.part","uuid":"u-2","turnId":"0","step":1,"part":{"type":"think","think":"plan the echoes"}},"time":2}`)
	if len(think) != 1 || think[0].Kind != "thought" || think[0].Payload["text"] != "plan the echoes" {
		t.Fatalf("think part: %+v", think)
	}
	empty := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"content.part","uuid":"u-3","part":{"type":"text","text":"  "}},"time":3}`)
	if len(empty) != 0 {
		t.Fatalf("whitespace-only part should drop; got %+v", empty)
	}
}

// step.end: only finishReason=end_turn surfaces turn.result (the
// clients' busy-walker terminal marker).
func TestMapper_StepEndTurnResult(t *testing.T) {
	m := NewMapper("main", "", "")
	mid := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1","turnId":"0","step":1,"finishReason":"tool_use"},"time":1}`)
	if len(mid) != 0 {
		t.Fatalf("intermediate step.end should drop; got %+v", mid)
	}
	end := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s2","turnId":"0","step":5,"finishReason":"end_turn"},"time":2}`)
	if len(end) != 1 || end[0].Kind != "turn.result" {
		t.Fatalf("want turn.result; got %+v", end)
	}
	if end[0].Payload["reason"] != "end_of_turn" || end[0].Payload["status"] != "success" {
		t.Fatalf("turn.result payload = %+v", end[0].Payload)
	}
}

// permission.record_approval_result → approval_result (NOT
// approval_request — the wire only carries the post-hoc decision, and
// the request kind would park a fake actionable card on the clients).
func TestMapper_ApprovalResult(t *testing.T) {
	m := NewMapper("main", "", "")
	evs := mustMap(t, m, `{"type":"permission.record_approval_result","turnId":0,"toolCallId":"tool_m","toolName":"Bash","action":"Running: echo hi","result":{"decision":"approved","selectedLabel":"Approve once"},"time":1}`)
	if len(evs) != 1 {
		t.Fatalf("want 1 event; got %+v", evs)
	}
	ev := evs[0]
	if ev.Kind != "approval_result" || ev.Producer != "agent" {
		t.Fatalf("kind/producer = %s/%s", ev.Kind, ev.Producer)
	}
	if ev.Kind == "approval_request" {
		t.Fatal("post-hoc records must not use the parked-request kind")
	}
	p := ev.Payload
	if p["tool_use_id"] != "tool_m" || p["name"] != "Bash" ||
		p["decision"] != "approved" || p["selected_label"] != "Approve once" ||
		p["action"] != "Running: echo hi" {
		t.Fatalf("payload = %+v", p)
	}
	text, _ := p["text"].(string)
	if !strings.Contains(text, "Bash") || !strings.Contains(text, "approved") {
		t.Fatalf("text summary = %q", text)
	}

	// Session-scoped approvals carry the scope field through.
	evs2 := mustMap(t, m, `{"type":"permission.record_approval_result","turnId":0,"toolCallId":"tool_u","toolName":"Bash","action":"Running: git clone …","sessionApprovalRule":"Bash(git clone *)","result":{"decision":"approved","scope":"session"},"time":2}`)
	if evs2[0].Payload["scope"] != "session" {
		t.Fatalf("scope not carried: %+v", evs2[0].Payload)
	}
}

// Subagent wire events are stamped with the subagent flag + the
// parent edge from state.json.
func TestMapper_SubagentTagging(t *testing.T) {
	m := NewMapper("agent-9", "main", "")
	evs := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"content.part","uuid":"u","part":{"type":"text","text":"sub report"}},"time":1}`)
	p := evs[0].Payload
	if p["subagent"] != true || p["kimi_agent_id"] != "agent-9" || p["parent_agent_id"] != "main" {
		t.Fatalf("subagent stamp = %+v", p)
	}

	// A subagent with an unknown parent still stamps the flag + id.
	m2 := NewMapper("agent-12", "", "")
	p2 := mustMap(t, m2, `{"type":"usage.record","model":"k3","usage":{"inputOther":1,"output":2,"inputCacheRead":3,"inputCacheCreation":0},"usageScope":"turn","time":1}`)[0].Payload
	if p2["subagent"] != true || p2["kimi_agent_id"] != "agent-12" {
		t.Fatalf("orphan subagent stamp = %+v", p2)
	}
	if _, has := p2["parent_agent_id"]; has {
		t.Fatalf("unknown parent should be omitted, not blank: %+v", p2)
	}
}

// Known-noise lines produce nothing.
func TestMapper_DropsKnownNoise(t *testing.T) {
	m := NewMapper("main", "", "")
	for _, line := range []string{
		`{"type":"config.update","cwd":"/tmp/x","modelAlias":"k3","time":1}`,
		`{"type":"tools.set_active_tools","names":["Bash"],"time":1}`,
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"hi"}]},"time":1}`,
		`{"type":"llm.tools_snapshot","hash":"abc","tools":[]}`,
		`{"type":"llm.request","kind":"loop","model":"k3","time":1}`,
		`{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s","turnId":"0","step":1},"time":1}`,
	} {
		if evs := mustMap(t, m, line); len(evs) != 0 {
			t.Errorf("noise line produced events: %s → %+v", line, evs)
		}
	}
}

// Unknown top-level types surface as drift system events (mirrors the
// claude mapper's §9 policy).
func TestMapper_UnknownTypeSurfacesDrift(t *testing.T) {
	m := NewMapper("main", "", "")
	evs := mustMap(t, m, `{"type":"cron.notice","text":"x","time":1}`)
	if len(evs) != 1 || evs[0].Kind != "system" || evs[0].Producer != "system" {
		t.Fatalf("want system drift event; got %+v", evs)
	}
	if evs[0].Payload["subtype"] != "unknown_type" || evs[0].Payload["type"] != "cron.notice" {
		t.Fatalf("drift payload = %+v", evs[0].Payload)
	}
}

// Malformed JSON returns an error (the run loop logs + drops the line).
func TestMapper_MalformedLineErrors(t *testing.T) {
	m := NewMapper("main", "", "")
	if _, err := m.MapLine([]byte(`{"type":"usage.record","usage":{`)); err == nil {
		t.Fatal("want parse error on torn line")
	}
	if evs, err := m.MapLine([]byte("   ")); err != nil || evs != nil {
		t.Fatalf("blank line should be a quiet no-op; got %v %v", evs, err)
	}
}

// Fixture replay: the sanitized real wire capture maps end-to-end with
// no errors and yields the expected kind histogram (this is the shape
// pin against kimi-code 0.28.1 — if a future kimi build drifts, this
// test is the alarm).
func TestMapper_RealFixtureReplay(t *testing.T) {
	data, err := os.ReadFile("testdata/wire_main.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	m := NewMapper("main", "", "kimi-code-ts")
	counts := map[string]int{}
	var plans []map[string]any
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		evs, err := m.MapLine([]byte(line))
		if err != nil {
			t.Fatalf("fixture line %d (%s): %v", i, line[:60], err)
		}
		for _, ev := range evs {
			counts[ev.Kind]++
			if ev.Kind == "plan" {
				plans = append(plans, ev.Payload)
			}
		}
	}
	// The fixture carries: 3 TodoList + 2 Bash + 1 Agent tool calls,
	// 4 results (incl. 1 isError), 3 todo updates, 4 turn-scope +
	// 1 session-scope usage, 2 approvals, 1 text + 1 think part,
	// 1 end_turn step.end.
	want := map[string]int{
		"tool_call":       6,
		"tool_result":     4,
		"plan":            3,
		"usage":           5,
		"approval_result": 2,
		"text":            1,
		"thought":         1,
		"turn.result":     1,
	}
	for kind, n := range want {
		if counts[kind] != n {
			t.Errorf("kind %s = %d, want %d (all: %v)", kind, counts[kind], n, counts)
		}
	}
	// All three plan updates share one per-turn message_id chain.
	if len(plans) == 3 &&
		(plans[0]["message_id"] != plans[1]["message_id"] ||
			plans[1]["message_id"] != plans[2]["message_id"]) {
		t.Errorf("fixture plan updates didn't share the chain: %q %q %q",
			plans[0]["message_id"], plans[1]["message_id"], plans[2]["message_id"])
	}
}
