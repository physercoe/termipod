package kimi_code

import (
	"errors"
	"fmt"
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

	// The chain id is namespaced by agent: subagent wire files carry no
	// turn.prompt (turnSeq stays 0 in every subagent mapper), and all
	// mappers post into ONE termipod transcript — two agents sharing a
	// message_id would fold into a single client card.
	todo := `{"type":"tools.update_store","key":"todo","value":[{"title":"sub","status":"pending"}],"time":6}`
	subA := mustMap(t, NewMapper("agent-1", "main", ""), todo)
	subB := mustMap(t, NewMapper("agent-2", "main", ""), todo)
	if subA[0].Payload["message_id"] == subB[0].Payload["message_id"] {
		t.Fatalf("plan message_id must differ across agents; both %q", subA[0].Payload["message_id"])
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

// A subagent's end_turn must NOT surface turn.result (#374): every
// consumer of that kind assumes the session's own turns — the hub
// digest closes the single open turn on it (shutting the MAIN turn
// early), mobile's turns chip counted it, and the busy-walker
// flickered idle mid-main-turn. The main agent's own end_turn still
// lands when its delegating Agent tool.call completes.
func TestMapper_StepEndTurnResult_SubagentDrops(t *testing.T) {
	m := NewMapper("agent-9", "main", "")
	end := mustMap(t, m, `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s2","turnId":"0","step":5,"finishReason":"end_turn"},"time":2}`)
	if len(end) != 0 {
		t.Fatalf("subagent end_turn should drop; got %+v", end)
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
		// 0.31: the compaction bookends bracket the apply event that
		// carries the signal; mcp.tools_discovered is tool-catalog noise.
		`{"type":"full_compaction.begin","source":"manual","time":1}`,
		`{"type":"full_compaction.complete","time":2}`,
		`{"type":"mcp.tools_discovered","serverName":"s","enabledNames":["a"],"hash":"h","tools":[],"time":3}`,
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

// --- kimi-code 0.31.0 wire vocabulary ---

// turn.cancel → turn.result with EXACTLY the ACP driver's eager-cancel
// vocabulary (driver_acp.go Input("cancel")): the busy-walker
// terminates on the kind, the digest reads stop_reason ("cancelled" is
// a normal stop — an intentional end, not a finding), and status
// matches what ACP engines post on a user cancel. This is THE 0.31
// correctness fix: kimi writes no step.end end_turn for the
// interrupted turn, so without it the session stayed busy forever.
func TestMapper_TurnCancelMapsToCancelledTurnResult(t *testing.T) {
	m := NewMapper("main", "", "")
	evs := mustMap(t, m, `{"type":"turn.cancel","time":1784620213235}`)
	if len(evs) != 1 || evs[0].Kind != "turn.result" {
		t.Fatalf("want one turn.result; got %+v", evs)
	}
	ev := evs[0]
	if ev.Producer != "agent" {
		t.Fatalf("producer = %q, want agent", ev.Producer)
	}
	if ev.Payload["status"] != "cancelled" || ev.Payload["stop_reason"] != "cancelled" {
		t.Fatalf("cancel vocabulary wrong: %+v", ev.Payload)
	}
	// No reason:end_of_turn — that spelling means a clean finish.
	if _, has := ev.Payload["reason"]; has {
		t.Fatalf("cancelled turn.result must not carry the end_of_turn reason key: %+v", ev.Payload)
	}
}

// A subagent's turn.cancel describes its inner loop, not the session's
// turn — same drop guard as mapStepEnd (#374).
func TestMapper_TurnCancel_SubagentDrops(t *testing.T) {
	m := NewMapper("agent-9", "main", "")
	if evs := mustMap(t, m, `{"type":"turn.cancel","time":1}`); len(evs) != 0 {
		t.Fatalf("subagent turn.cancel should drop; got %+v", evs)
	}
}

// turn.steer drops EVERY origin explicitly (no unknown_type drift
// noise): background_task is the engine's own task-notification
// envelope (129/133 steers in the 0.31 capture), user steers duplicate
// the hub's input.text (same rationale as turn.prompt), unknown
// origins drop quietly rather than guess.
func TestMapper_TurnSteerDropsAllOrigins(t *testing.T) {
	m := NewMapper("main", "", "")
	for _, line := range []string{
		`{"type":"turn.steer","input":[{"type":"text","text":"x"}],"origin":{"kind":"background_task","taskId":"bash-0576ywug","status":"completed","notificationId":"task:bash-0576ywug:completed"},"time":1}`,
		`{"type":"turn.steer","input":[{"type":"text","text":"x"}],"origin":{"kind":"user"},"time":2}`,
		`{"type":"turn.steer","input":[{"type":"text","text":"x"}],"origin":{"kind":"some_future_origin"},"time":3}`,
		`{"type":"turn.steer","input":[{"type":"text","text":"x"}],"time":4}`,
	} {
		if evs := mustMap(t, m, line); len(evs) != 0 {
			t.Errorf("steer should drop silently (no events, no unknown_type): %s → %+v", line, evs)
		}
	}
}

// turn.prompt origin.kind=task (0.31 goal/cron engine tasks +
// background-task notices) drops like any prompt but STILL re-arms the
// per-turn plan chain — a task prompt opens a real engine turn.
func TestMapper_TurnPromptTaskOriginDropsAndRearms(t *testing.T) {
	m := NewMapper("main", "", "")
	mustMap(t, m, `{"type":"turn.prompt","input":[{"type":"text","text":"do things"}],"origin":{"kind":"user"},"time":1}`)
	first := mustMap(t, m, `{"type":"tools.update_store","key":"todo","value":[{"title":"a","status":"pending"}],"time":2}`)
	idA := first[0].Payload["message_id"]

	taskPrompt := `{"type":"turn.prompt","input":[{"type":"text","text":"<notification redacted>"}],"origin":{"kind":"task","taskId":"bash-kl8rz8bn","status":"completed","notificationId":"task:bash-kl8rz8bn:completed"},"time":3}`
	if evs := mustMap(t, m, taskPrompt); len(evs) != 0 {
		t.Fatalf("task-origin prompt should drop; got %+v", evs)
	}
	second := mustMap(t, m, `{"type":"tools.update_store","key":"todo","value":[{"title":"b","status":"pending"}],"time":4}`)
	if second[0].Payload["message_id"] == idA {
		t.Fatalf("task prompt should re-arm the plan chain; still %q", idA)
	}
}

// context.apply_compaction → ONE system event (subtype compaction)
// with the summary (bounded), the token delta and the compacted count;
// a text line feeds the default card renderer. begin/complete and
// mcp.tools_discovered are dropped in TestMapper_DropsKnownNoise.
func TestMapper_ApplyCompactionMapsToSystemEvent(t *testing.T) {
	m := NewMapper("main", "", "")
	evs := mustMap(t, m, `{"type":"context.apply_compaction","summary":"working brief","contextSummary":"boilerplate","compactedCount":135,"tokensBefore":150972,"tokensAfter":1585,"keptUserMessageCount":7,"time":1}`)
	if len(evs) != 1 || evs[0].Kind != "system" || evs[0].Producer != "system" {
		t.Fatalf("want one system event; got %+v", evs)
	}
	p := evs[0].Payload
	if p["subtype"] != "compaction" {
		t.Fatalf("subtype = %v", p["subtype"])
	}
	if p["summary"] != "working brief" {
		t.Fatalf("summary = %v", p["summary"])
	}
	if p["compacted_count"] != 135 || p["tokens_before"] != 150972 || p["tokens_after"] != 1585 {
		t.Fatalf("counts wrong: %+v", p)
	}
	text, _ := p["text"].(string)
	if !strings.Contains(text, "135") || !strings.Contains(text, "150972") || !strings.Contains(text, "1585") {
		t.Fatalf("text summary = %q", text)
	}
	// contextSummary is engine-facing boilerplate, not carried.
	if _, has := p["contextSummary"]; has {
		t.Fatalf("contextSummary should not be forwarded: %+v", p)
	}

	// The summary is bounded so one event can't dominate the transcript.
	long := strings.Repeat("a", 600)
	evs2 := mustMap(t, m, `{"type":"context.apply_compaction","summary":"`+long+`","compactedCount":1,"tokensBefore":2,"tokensAfter":1,"time":2}`)
	got, _ := evs2[0].Payload["summary"].(string)
	if r := []rune(got); len(r) != 501 || !strings.HasSuffix(got, "…") {
		t.Fatalf("summary not truncated to the bound: %d runes", len(r))
	}
}

// plan_mode / permission.set_mode / swarm_mode (0.31) → producer=system
// cards carrying the phase/mode/trigger. producer=system keeps them off
// mobile's busy-inference path (same treatment as the ACP driver's
// current_mode_update forward).
func TestMapper_ModeEvents(t *testing.T) {
	m := NewMapper("main", "", "")

	enter := mustMap(t, m, `{"type":"plan_mode.enter","id":"lockjaw-iron-fist-adam-warlock","time":1}`)
	p := enter[0].Payload
	if enter[0].Kind != "system" || enter[0].Producer != "system" ||
		p["subtype"] != "plan_mode" || p["phase"] != "enter" || p["id"] != "lockjaw-iron-fist-adam-warlock" {
		t.Fatalf("plan_mode.enter payload = %+v", p)
	}
	if text, _ := p["text"].(string); !strings.Contains(text, "enter") || !strings.Contains(text, "lockjaw") {
		t.Fatalf("plan_mode.enter text = %q", text)
	}

	for _, line := range []string{
		`{"type":"plan_mode.cancel","time":2}`,
		`{"type":"plan_mode.exit","time":3}`,
	} {
		evs := mustMap(t, m, line)
		if len(evs) != 1 || evs[0].Payload["subtype"] != "plan_mode" {
			t.Fatalf("plan_mode arm produced %+v", evs)
		}
		if _, has := evs[0].Payload["id"]; has {
			t.Fatalf("non-enter phases carry no id: %+v", evs[0].Payload)
		}
	}

	perm := mustMap(t, m, `{"type":"permission.set_mode","mode":"yolo","time":4}`)
	if perm[0].Payload["subtype"] != "permission_mode" || perm[0].Payload["mode"] != "yolo" {
		t.Fatalf("permission.set_mode payload = %+v", perm[0].Payload)
	}

	swarmIn := mustMap(t, m, `{"type":"swarm_mode.enter","trigger":"tool","time":5}`)
	if swarmIn[0].Payload["subtype"] != "swarm_mode" || swarmIn[0].Payload["phase"] != "enter" ||
		swarmIn[0].Payload["trigger"] != "tool" {
		t.Fatalf("swarm_mode.enter payload = %+v", swarmIn[0].Payload)
	}
	swarmOut := mustMap(t, m, `{"type":"swarm_mode.exit","time":6}`)
	if swarmOut[0].Payload["phase"] != "exit" {
		t.Fatalf("swarm_mode.exit payload = %+v", swarmOut[0].Payload)
	}
	if _, has := swarmOut[0].Payload["trigger"]; has {
		t.Fatalf("exit carries no trigger: %+v", swarmOut[0].Payload)
	}
}

// The 0.31 event types must not disturb the per-turn plan chain
// (turnSeq / planMsgID): steers, mode events, compaction and
// turn.cancel all leave the chain intact; only turn.prompt re-arms.
func TestMapper_PlanChainUnaffectedBy031Events(t *testing.T) {
	m := NewMapper("main", "", "")
	todo := `{"type":"tools.update_store","key":"todo","value":[{"title":"x","status":"pending"}],"time":%d}`

	mustMap(t, m, `{"type":"turn.prompt","input":[{"type":"text","text":"go"}],"origin":{"kind":"user"},"time":1}`)
	first := mustMap(t, m, fmt.Sprintf(todo, 2))
	idA := first[0].Payload["message_id"]

	for i, line := range []string{
		`{"type":"turn.steer","input":[{"type":"text","text":"x"}],"origin":{"kind":"user"},"time":3}`,
		`{"type":"plan_mode.enter","id":"p","time":4}`,
		`{"type":"plan_mode.cancel","time":5}`,
		`{"type":"permission.set_mode","mode":"yolo","time":6}`,
		`{"type":"swarm_mode.enter","trigger":"tool","time":7}`,
		`{"type":"swarm_mode.exit","time":8}`,
		`{"type":"context.apply_compaction","summary":"s","compactedCount":1,"tokensBefore":9,"tokensAfter":2,"time":9}`,
		`{"type":"turn.cancel","time":10}`,
	} {
		mustMap(t, m, line)
		again := mustMap(t, m, fmt.Sprintf(todo, 20+i))
		if again[0].Payload["message_id"] != idA {
			t.Fatalf("after %s the chain moved: %q → %q", line, idA, again[0].Payload["message_id"])
		}
	}

	// And turn.prompt still re-arms after all of the above.
	mustMap(t, m, `{"type":"turn.prompt","input":[{"type":"text","text":"next"}],"origin":{"kind":"user"},"time":100}`)
	third := mustMap(t, m, fmt.Sprintf(todo, 101))
	if third[0].Payload["message_id"] == idA {
		t.Fatalf("turn.prompt should re-arm; still %q", idA)
	}
}

// Fixture replay: the sanitized real 0.31.0 capture maps end-to-end —
// every new event type exercises its arm, explicit drops emit nothing,
// and the kind histogram + plan-chain rotation + cancel vocabulary are
// pinned (the drift alarm for 0.31 shapes, alongside the 0.28.1 pin in
// TestMapper_RealFixtureReplay).
func TestMapper_RealFixtureReplay_0_31(t *testing.T) {
	data, err := os.ReadFile("testdata/wire_main_0_31.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	m := NewMapper("main", "", "kimi-code-ts")
	counts := map[string]int{}
	var plans []map[string]any
	var turnResults []map[string]any
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		evs, err := m.MapLine([]byte(line))
		if err != nil {
			t.Fatalf("fixture line %d (%s): %v", i, line[:60], err)
		}
		for _, ev := range evs {
			counts[ev.Kind]++
			switch ev.Kind {
			case "plan":
				plans = append(plans, ev.Payload)
			case "turn.result":
				turnResults = append(turnResults, ev.Payload)
			case "system":
				if ev.Payload["subtype"] == "unknown_type" {
					t.Errorf("line %d fell to unknown_type — a 0.31 type lost its arm: %s", i, line[:80])
				}
			}
		}
	}
	// The fixture carries: 2 todo updates (one per turn), 1 text part,
	// 1 usage, 1 turn.cancel, and the mode/compaction system events
	// (2 plan_mode + 1 permission_mode + 2 swarm_mode + 1 compaction).
	want := map[string]int{
		"plan":        2,
		"system":      6,
		"text":        1,
		"usage":       1,
		"turn.result": 1,
	}
	for kind, n := range want {
		if counts[kind] != n {
			t.Errorf("kind %s = %d, want %d (all: %v)", kind, counts[kind], n, counts)
		}
	}
	// The task-origin turn.prompt between the two todo updates re-armed
	// the chain.
	if len(plans) == 2 && plans[0]["message_id"] == plans[1]["message_id"] {
		t.Errorf("plan chain should rotate across turns; both %q", plans[0]["message_id"])
	}
	// The single turn.result is the cancel terminal, in the ACP
	// eager-cancel vocabulary.
	if len(turnResults) == 1 &&
		(turnResults[0]["status"] != "cancelled" || turnResults[0]["stop_reason"] != "cancelled") {
		t.Errorf("turn.result payload = %+v, want cancelled/cancelled", turnResults[0])
	}
}
