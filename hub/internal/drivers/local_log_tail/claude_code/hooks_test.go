package claudecode

import (
	"context"
	"sync"
	"testing"
	"time"
)

// hooksTestPoster reuses the capturingPoster pattern from
// adapter_integration_test.go without colliding on the type name.
type hooksTestPoster struct {
	mu     sync.Mutex
	events []hooksEv
}

type hooksEv struct {
	kind, producer string
	payload        map[string]any
}

func (p *hooksTestPoster) PostAgentEvent(_ context.Context, _, kind, producer string, payload any) error {
	pm, _ := payload.(map[string]any)
	cp := make(map[string]any, len(pm))
	for k, v := range pm {
		cp[k] = v
	}
	p.mu.Lock()
	p.events = append(p.events, hooksEv{kind: kind, producer: producer, payload: cp})
	p.mu.Unlock()
	return nil
}

func (p *hooksTestPoster) snapshot() []hooksEv {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]hooksEv, len(p.events))
	copy(out, p.events)
	return out
}

// hooksTestAdapter returns an Adapter with FSM wired but no run
// loop / tailer — perfect for exercising the hook dispatch in
// isolation. Sets started=true so OnHook proceeds (pre-Start safety
// gate is its own test below).
func hooksTestAdapter(t *testing.T, p *hooksTestPoster) *Adapter {
	t.Helper()
	a, err := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/x", Poster: p})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.started = true
	a.fsm = NewFSM(a.AgentID, p, a.Log, context.Background())
	return a
}

func findFirstByKind(evs []hooksEv, kind string) (hooksEv, bool) {
	for _, e := range evs {
		if e.kind == kind {
			return e, true
		}
	}
	return hooksEv{}, false
}

func findBySubtype(evs []hooksEv, subtype string) (hooksEv, bool) {
	for _, e := range evs {
		if s, _ := e.payload["subtype"].(string); s == subtype {
			return e, true
		}
	}
	return hooksEv{}, false
}

func TestOnHook_StopTransitionsToIdleAndEmitsTurnComplete(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	a.fsm.Transition(StateStreaming, "seed") // start non-idle so transition is observable

	resp, err := a.OnHook(context.Background(), "Stop", map[string]any{
		"last_assistant_message": "Done.",
		"permission_mode":        "default",
	})
	if err != nil {
		t.Fatalf("OnHook: %v", err)
	}
	if resp == nil {
		t.Errorf("response is nil; want empty map")
	}
	if got := a.fsm.State(); got != StateIdle {
		t.Errorf("state = %v, want StateIdle", got)
	}
	ev, ok := findBySubtype(p.snapshot(), "turn_complete")
	if !ok {
		t.Fatalf("turn_complete not emitted: %+v", p.snapshot())
	}
	if ev.payload["final_message"] != "Done." {
		t.Errorf("final_message = %v", ev.payload["final_message"])
	}
}

func TestOnHook_NotificationIdlePrompt(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	a.fsm.Transition(StateStreaming, "seed")

	_, err := a.OnHook(context.Background(), "Notification", map[string]any{
		"notification_type": "idle_prompt",
		"message":           "what's next?",
	})
	if err != nil {
		t.Fatalf("OnHook: %v", err)
	}
	if a.fsm.State() != StateIdle {
		t.Errorf("state = %v, want StateIdle", a.fsm.State())
	}
	if _, ok := findBySubtype(p.snapshot(), "awaiting_input"); !ok {
		t.Errorf("awaiting_input not emitted: %+v", p.snapshot())
	}
}

func TestOnHook_NotificationPermissionPromptDropped(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	_, _ = a.OnHook(context.Background(), "Notification", map[string]any{
		"notification_type": "permission_prompt",
		"message":           "approve?",
	})
	// Only the FSM stays idle (no transition); no awaiting_input or
	// state_changed event should be emitted.
	for _, e := range p.snapshot() {
		if s, _ := e.payload["subtype"].(string); s == "awaiting_input" {
			t.Errorf("permission_prompt produced awaiting_input: %v", e)
		}
	}
}

func TestOnHook_NotificationUnknownTypeSurfaces(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	_, _ = a.OnHook(context.Background(), "Notification", map[string]any{
		"notification_type": "future_event",
		"message":           "x",
	})
	ev, ok := findBySubtype(p.snapshot(), "unknown_notification")
	if !ok {
		t.Fatalf("unknown_notification not emitted: %+v", p.snapshot())
	}
	if ev.payload["notification_type"] != "future_event" {
		t.Errorf("notification_type = %v", ev.payload["notification_type"])
	}
}

func TestOnHook_PreToolUseOther_TransitionsToStreaming(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	resp, _ := a.OnHook(context.Background(), "PreToolUse", map[string]any{
		"tool_name":   "Bash",
		"tool_input":  map[string]any{"command": "ls"},
		"tool_use_id": "t1",
	})
	if resp == nil {
		t.Errorf("nil response")
	}
	if a.fsm.State() != StateStreaming {
		t.Errorf("state = %v, want StateStreaming", a.fsm.State())
	}
}

// W2i: PreToolUse(AskUserQuestion) now blocks on the picker channel.
// Verify it (a) emits the approval_request event + transitions FSM
// before blocking, (b) returns when the picker channel closes, (c)
// the picker_done channel is set so inputPickOption can find it.
func TestOnHook_PreToolUse_AskUserQuestion_ParksAndEmitsApprovalRequest(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	a.Knobs.HookParkDefaultMs = 5000 // long enough not to fire mid-test

	respCh := make(chan map[string]any, 1)
	go func() {
		resp, _ := a.OnHook(context.Background(), "PreToolUse", map[string]any{
			"tool_name": "AskUserQuestion",
			"tool_input": map[string]any{
				"questions": []map[string]any{{"question": "Color?", "options": []map[string]any{{"label": "Red"}}}},
			},
			"tool_use_id": "t9",
		})
		respCh <- resp
	}()

	// Wait for the parked hook to emit its approval_request and set
	// pickerDone.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := findFirstByKind(p.snapshot(), "approval_request"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if a.fsm.State() != StateAwaitingDecision {
		t.Errorf("state = %v, want StateAwaitingDecision", a.fsm.State())
	}
	ev, ok := findFirstByKind(p.snapshot(), "approval_request")
	if !ok {
		t.Fatalf("approval_request not emitted: %+v", p.snapshot())
	}
	if ev.payload["dialog_type"] != "user_question" {
		t.Errorf("dialog_type = %v", ev.payload["dialog_type"])
	}
	if ev.payload["tool_use_id"] != "t9" {
		t.Errorf("tool_use_id = %v", ev.payload["tool_use_id"])
	}

	// Verify pickerDone is set (so inputPickOption can find it).
	a.pickerMu.Lock()
	done := a.pickerDone
	a.pickerMu.Unlock()
	if done == nil {
		t.Fatal("pickerDone not set; AskUserQuestion didn't park")
	}
	// Closing the channel must unblock OnHook.
	close(done)
	a.pickerMu.Lock()
	a.pickerDone = nil
	a.pickerMu.Unlock()

	select {
	case <-respCh:
	case <-time.After(1 * time.Second):
		t.Fatal("OnHook did not return after pickerDone closed")
	}
	// FSM should be back to streaming.
	if a.fsm.State() != StateStreaming {
		t.Errorf("state after unpark = %v, want StateStreaming", a.fsm.State())
	}
}

// Picker times out — hook returns {}, FSM doesn't change back (timeout
// is treated as no-op so claude's TUI is left for operator-at-keyboard).
func TestOnHook_PreToolUse_AskUserQuestion_TimeoutReturnsEmpty(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	a.Knobs.HookParkDefaultMs = 50 // fire fast

	start := time.Now()
	resp, err := a.OnHook(context.Background(), "PreToolUse", map[string]any{
		"tool_name":   "AskUserQuestion",
		"tool_input":  map[string]any{},
		"tool_use_id": "t10",
	})
	if err != nil {
		t.Fatalf("OnHook: %v", err)
	}
	if resp == nil {
		t.Errorf("nil response on timeout; want empty map")
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("returned in %v before 50ms timeout", elapsed)
	}
}

// inputPickOption closes pickerDone after send-keys — verifies the
// hook → send-keys → unblock loop completes.
func TestPickOption_ClosesPickerDone(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	a.Knobs.HookParkDefaultMs = 5000
	a.PaneID = "%42"
	r := &recordingRunner{}
	a.CmdRunner = r

	respCh := make(chan map[string]any, 1)
	go func() {
		resp, _ := a.OnHook(context.Background(), "PreToolUse", map[string]any{
			"tool_name":   "AskUserQuestion",
			"tool_input":  map[string]any{},
			"tool_use_id": "t11",
		})
		respCh <- resp
	}()
	// Wait for pickerDone to be set.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		a.pickerMu.Lock()
		set := a.pickerDone != nil
		a.pickerMu.Unlock()
		if set {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Send pick_option index=1: Down + Enter, then close pickerDone.
	if err := a.HandleInput(context.Background(), "pick_option", map[string]any{"index": float64(1)}); err != nil {
		t.Fatalf("HandleInput pick_option: %v", err)
	}
	select {
	case <-respCh:
	case <-time.After(1 * time.Second):
		t.Fatal("OnHook did not return after pick_option")
	}
	calls := r.snapshot()
	if len(calls) != 2 {
		t.Fatalf("send-keys calls = %d, want 2 (Down + Enter)", len(calls))
	}
}

// W2i: PreCompact with no Attention client auto-allows (returns {}
// immediately so the hook contract is satisfied) but still emits the
// approval_request agent_event so mobile can render an info card.
// FSM ends up at streaming after the fallthrough.
func TestOnHook_PreCompact_NoAttentionAutoAllows(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	resp, err := a.OnHook(context.Background(), "PreCompact", map[string]any{
		"trigger":             "manual",
		"custom_instructions": "be brief",
	})
	if err != nil {
		t.Fatalf("OnHook: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("resp = %v, want {} (auto-allow without Attention)", resp)
	}
	ev, ok := findFirstByKind(p.snapshot(), "approval_request")
	if !ok {
		t.Fatalf("approval_request not emitted: %+v", p.snapshot())
	}
	if ev.payload["dialog_type"] != "compaction" {
		t.Errorf("dialog_type = %v", ev.payload["dialog_type"])
	}
	if ev.payload["trigger"] != "manual" {
		t.Errorf("trigger = %v", ev.payload["trigger"])
	}
	if a.fsm.State() != StateStreaming {
		t.Errorf("state = %v, want StateStreaming (no-Attention fallthrough)", a.fsm.State())
	}
}

// W2i: PreCompact with a real Attention client parks until mobile
// decides. Approve → resp empty (compaction proceeds). Reject →
// resp carries decision:block.
func TestOnHook_PreCompact_ParkedApprovePath(t *testing.T) {
	c, hub, cleanup := newTestClient(t)
	defer cleanup()
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	a.Attention = c
	a.Knobs.HookParkDefaultMs = 3000

	respCh := make(chan map[string]any, 1)
	go func() {
		resp, _ := a.OnHook(context.Background(), "PreCompact", map[string]any{
			"trigger": "manual",
		})
		respCh <- resp
	}()
	// Wait for the row to land, then resolve.
	deadline := time.Now().Add(2 * time.Second)
	var id string
	for time.Now().Before(deadline) {
		if id = hub.lastID(); id != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("attention row never landed on mock hub")
	}
	hub.resolve(id, "approve", "ok")
	select {
	case resp := <-respCh:
		if len(resp) != 0 {
			t.Errorf("approve resp = %v, want {}", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnHook did not return after approve")
	}
}

func TestOnHook_PreCompact_ParkedRejectPath(t *testing.T) {
	c, hub, cleanup := newTestClient(t)
	defer cleanup()
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	a.Attention = c
	a.Knobs.HookParkDefaultMs = 3000

	respCh := make(chan map[string]any, 1)
	go func() {
		resp, _ := a.OnHook(context.Background(), "PreCompact", map[string]any{
			"trigger": "auto",
		})
		respCh <- resp
	}()
	deadline := time.Now().Add(2 * time.Second)
	var id string
	for time.Now().Before(deadline) {
		if id = hub.lastID(); id != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	hub.resolve(id, "reject", "defer for now")
	select {
	case resp := <-respCh:
		if resp["decision"] != "block" {
			t.Errorf("reject resp = %v, want decision:block", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnHook did not return after reject")
	}
}

func TestOnHook_PreCompact_TimeoutBlocks(t *testing.T) {
	c, _, cleanup := newTestClient(t)
	defer cleanup()
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	a.Attention = c
	a.Knobs.HookParkDefaultMs = 100

	resp, err := a.OnHook(context.Background(), "PreCompact", map[string]any{"trigger": "auto"})
	if err != nil {
		t.Fatalf("OnHook: %v", err)
	}
	if resp["decision"] != "block" {
		t.Errorf("timeout resp = %v, want decision:block (fail closed)", resp)
	}
}

func TestOnHook_SubagentStop_DropsParentTurnDuplicate(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	_, _ = a.OnHook(context.Background(), "SubagentStop", map[string]any{
		"agent_type":             "",
		"agent_id":               "irrelevant",
		"last_assistant_message": "x",
	})
	// Empty agent_type = parent-turn dup; no event should fire.
	for _, e := range p.snapshot() {
		if s, _ := e.payload["subtype"].(string); s == "subagent_complete" {
			t.Errorf("parent-turn dup emitted: %v", e)
		}
	}
}

func TestOnHook_SubagentStop_RealSubagent(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	_, _ = a.OnHook(context.Background(), "SubagentStop", map[string]any{
		"agent_type":             "Task",
		"agent_id":               "sub-1",
		"last_assistant_message": "Subagent done.",
		"agent_transcript_path":  "/tmp/sub.jsonl",
	})
	ev, ok := findBySubtype(p.snapshot(), "subagent_complete")
	if !ok {
		t.Fatalf("subagent_complete not emitted: %+v", p.snapshot())
	}
	if ev.payload["agent_type"] != "Task" {
		t.Errorf("agent_type = %v", ev.payload["agent_type"])
	}
}

func TestOnHook_SessionStartEmits(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	_, _ = a.OnHook(context.Background(), "SessionStart", map[string]any{
		"source": "startup",
		"model":  "claude-sonnet-4-5",
	})
	ev, ok := findBySubtype(p.snapshot(), "session_start")
	if !ok {
		t.Fatalf("session_start not emitted")
	}
	if ev.payload["source"] != "startup" || ev.payload["model"] != "claude-sonnet-4-5" {
		t.Errorf("payload = %v", ev.payload)
	}
}

func TestOnHook_SessionEndEmits(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	_, _ = a.OnHook(context.Background(), "SessionEnd", map[string]any{
		"reason": "user_exit",
	})
	ev, ok := findBySubtype(p.snapshot(), "session_end")
	if !ok {
		t.Fatalf("session_end not emitted")
	}
	if ev.payload["reason"] != "user_exit" {
		t.Errorf("reason = %v", ev.payload["reason"])
	}
}

func TestOnHook_PostToolUseAndUserPromptSubmitAreNoops(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	_, _ = a.OnHook(context.Background(), "PostToolUse", map[string]any{
		"tool_name": "Bash", "tool_response": map[string]any{},
	})
	_, _ = a.OnHook(context.Background(), "UserPromptSubmit", map[string]any{
		"prompt": "hi",
	})
	// Neither should emit anything (JSONL has the truth).
	for _, e := range p.snapshot() {
		if s, _ := e.payload["subtype"].(string); s == "user_prompt" || s == "tool_result" {
			t.Errorf("unexpected event: %v", e)
		}
	}
}

func TestOnHook_PreStartReturnsBenignEmpty(t *testing.T) {
	p := &hooksTestPoster{}
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: "/tmp/x", Poster: p})
	// Do NOT set started=true; the gateway may call OnHook before
	// Start completes.
	resp, err := a.OnHook(context.Background(), "Stop", map[string]any{})
	if err != nil {
		t.Errorf("pre-start OnHook error: %v", err)
	}
	if resp == nil {
		t.Errorf("pre-start resp nil; want empty map")
	}
	if len(p.snapshot()) != 0 {
		t.Errorf("pre-start OnHook emitted %d events; want 0", len(p.snapshot()))
	}
}

func TestOnHook_UnknownEventReturnsEmpty(t *testing.T) {
	p := &hooksTestPoster{}
	a := hooksTestAdapter(t, p)
	resp, err := a.OnHook(context.Background(), "FutureHook2030", map[string]any{})
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if resp == nil {
		t.Errorf("resp nil for unknown event")
	}
}

// Verify the FSM gets driven from JSONL events too: a tool_call line
// in the run loop transitions to streaming.
func TestAdapter_RunLoopJSONLToolCallDrivesFSMStreaming(t *testing.T) {
	cwd := "/home/test/hookfsm"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := projectDir + "/sess.jsonl"
	writeJSONL(t, jsonl,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
	)
	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "a", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	// Wait for the tool_call event to land...
	_ = waitForN(t, p, 1, 1*time.Second)
	// ...then verify the FSM transitioned to streaming.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if a.fsm != nil && a.fsm.State() == StateStreaming {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if a.fsm == nil {
		t.Fatal("fsm never initialized")
	}
	t.Errorf("FSM state = %v, want StateStreaming after JSONL tool_use", a.fsm.State())
}
