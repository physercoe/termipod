package hostrunner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

type fakeInputPoster struct {
	calls []struct {
		agentID string
		fields  map[string]any
	}
	err error
}

func (f *fakeInputPoster) PostAgentInput(ctx context.Context, agentID string, fields map[string]any) error {
	f.calls = append(f.calls, struct {
		agentID string
		fields  map[string]any
	}{agentID, fields})
	return f.err
}

func TestA2AHubDispatcher_PostsTextInput(t *testing.T) {
	p := &fakeInputPoster{}
	d := newA2AHubDispatcher(p)
	msg := a2a.Message{
		MessageID: "m-1",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"text","text":"hello"},{"kind":"text","text":"world"}]`),
	}
	if err := d.Dispatch(context.Background(), "agent-x", msg, "task-1", a2a.NewTaskStore()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("posts = %d, want 1", len(p.calls))
	}
	if p.calls[0].agentID != "agent-x" {
		t.Errorf("agent = %q, want agent-x", p.calls[0].agentID)
	}
	if p.calls[0].fields["kind"] != "text" {
		t.Errorf("kind = %v, want text", p.calls[0].fields["kind"])
	}
	if p.calls[0].fields["body"] != "hello\nworld" {
		t.Errorf("body = %v, want concat of parts", p.calls[0].fields["body"])
	}
	// Producer attribution — peer-originated input must be distinguishable
	// from phone/web input in the audit trail.
	if p.calls[0].fields["producer"] != "a2a" {
		t.Errorf("producer = %v, want a2a", p.calls[0].fields["producer"])
	}
}

func TestA2AHubDispatcher_EmptyPartsReturnsErrDispatch(t *testing.T) {
	p := &fakeInputPoster{}
	d := newA2AHubDispatcher(p)
	msg := a2a.Message{
		MessageID: "m-2",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"file","uri":"blob://x"}]`),
	}
	err := d.Dispatch(context.Background(), "agent-x", msg, "task-2", a2a.NewTaskStore())
	if err == nil {
		t.Fatal("err = nil, want ErrDispatch")
	}
	if !errors.Is(err, a2a.ErrDispatch) {
		t.Errorf("err = %v, want ErrDispatch wrap", err)
	}
	if len(p.calls) != 0 {
		t.Errorf("posted %d times, want 0 for empty text", len(p.calls))
	}
}

func TestA2AHubDispatcher_PostErrorWrappedAsErrDispatch(t *testing.T) {
	p := &fakeInputPoster{err: errors.New("hub down")}
	d := newA2AHubDispatcher(p)
	msg := a2a.Message{
		MessageID: "m-3",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"text","text":"hi"}]`),
	}
	err := d.Dispatch(context.Background(), "agent-x", msg, "task-3", a2a.NewTaskStore())
	if err == nil || !errors.Is(err, a2a.ErrDispatch) {
		t.Errorf("err = %v, want wrapped ErrDispatch", err)
	}
}

// TestA2AHubDispatcher_HarvestsAgentOutput covers the main response-loop
// wedge: once Dispatch registers a task, driver-emitted agent events for
// the same agent must land in the task's history and advance the state
// out of "submitted". A lifecycle.stopped event flips the task to
// "completed" and releases the correlation slot.
func TestA2AHubDispatcher_HarvestsAgentOutput(t *testing.T) {
	p := &fakeInputPoster{}
	d := newA2AHubDispatcher(p)
	store := a2a.NewTaskStore()

	// Seed the task the way the JSON-RPC handler would before invoking
	// Dispatch, so the store has a row Update can target.
	store.Create("agent-x", "task-h1", a2a.Message{
		MessageID: "m-in",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"text","text":"run"}]`),
	})

	msg := a2a.Message{
		MessageID: "m-in",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"text","text":"run"}]`),
	}
	if err := d.Dispatch(context.Background(), "agent-x", msg, "task-h1", store); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// First agent event: text reply. State flips submitted -> working and
	// history grows by one role="agent" message.
	d.OnAgentEvent("agent-x", "text", "agent", map[string]any{"text": "working on it"})

	got, ok := store.Get("agent-x", "task-h1")
	if !ok {
		t.Fatalf("task missing after first event")
	}
	if got.Status.State != a2a.TaskStateWorking {
		t.Errorf("state = %q, want working", got.Status.State)
	}
	// History: initial user message + 1 agent reply = 2.
	if len(got.History) != 2 {
		t.Fatalf("history len = %d, want 2 (user + agent); history=%+v",
			len(got.History), got.History)
	}
	if got.History[1].Role != "agent" {
		t.Errorf("history[1].role = %q, want agent", got.History[1].Role)
	}
	var parts []map[string]any
	if err := json.Unmarshal(got.History[1].Parts, &parts); err != nil {
		t.Fatalf("decode parts: %v", err)
	}
	if len(parts) != 1 || parts[0]["text"] != "working on it" {
		t.Errorf("parts = %+v, want single text 'working on it'", parts)
	}

	// Non-agent-text events that aren't lifecycle.stopped are ignored.
	d.OnAgentEvent("agent-x", "text", "system", map[string]any{"text": "x"})
	d.OnAgentEvent("agent-x", "lifecycle", "system", map[string]any{"phase": "started"})
	got, _ = store.Get("agent-x", "task-h1")
	if got.Status.State != a2a.TaskStateWorking {
		t.Errorf("state after noise = %q, want working", got.Status.State)
	}

	// Second text reply: appended, still working.
	d.OnAgentEvent("agent-x", "text", "agent", map[string]any{"text": "halfway"})
	got, _ = store.Get("agent-x", "task-h1")
	if len(got.History) != 3 {
		t.Errorf("history len = %d, want 3 (user + two agent)", len(got.History))
	}

	// lifecycle.stopped -> completed.
	d.OnAgentEvent("agent-x", "lifecycle", "system", map[string]any{"phase": "stopped"})
	got, _ = store.Get("agent-x", "task-h1")
	if got.Status.State != a2a.TaskStateCompleted {
		t.Errorf("state = %q, want completed", got.Status.State)
	}

	// Once completed the slot is released: subsequent agent events for the
	// same agent must not re-append to the finished task.
	d.OnAgentEvent("agent-x", "text", "agent", map[string]any{"text": "late"})
	got, _ = store.Get("agent-x", "task-h1")
	if len(got.History) != 3 {
		t.Errorf("history grew after completion: len=%d", len(got.History))
	}
}

// TestA2AHubDispatcher_SupersedesPriorTask: sending a new message/send
// while a prior task for the same agent is still open must cancel the
// prior task (terminal) and start fresh. Mirrors the single-turn shape
// of the current drivers — if two peers race, the second wins.
func TestA2AHubDispatcher_SupersedesPriorTask(t *testing.T) {
	p := &fakeInputPoster{}
	d := newA2AHubDispatcher(p)
	store := a2a.NewTaskStore()

	seed := func(taskID string) {
		store.Create("agent-y", taskID, a2a.Message{
			MessageID: "u-" + taskID,
			Role:      "user",
			Parts:     json.RawMessage(`[{"kind":"text","text":"go"}]`),
		})
	}
	seed("t1")
	seed("t2")

	msg := a2a.Message{
		MessageID: "u1",
		Role:      "user",
		Parts:     json.RawMessage(`[{"kind":"text","text":"first"}]`),
	}
	if err := d.Dispatch(context.Background(), "agent-y", msg, "t1", store); err != nil {
		t.Fatalf("dispatch t1: %v", err)
	}
	msg.MessageID = "u2"
	if err := d.Dispatch(context.Background(), "agent-y", msg, "t2", store); err != nil {
		t.Fatalf("dispatch t2: %v", err)
	}

	t1, _ := store.Get("agent-y", "t1")
	if t1.Status.State != a2a.TaskStateCanceled {
		t.Errorf("t1 state = %q, want canceled (superseded)", t1.Status.State)
	}

	// Events now belong to t2 only.
	d.OnAgentEvent("agent-y", "text", "agent", map[string]any{"text": "reply"})
	t2, _ := store.Get("agent-y", "t2")
	if t2.Status.State != a2a.TaskStateWorking {
		t.Errorf("t2 state = %q, want working", t2.Status.State)
	}
	// t1 must still be canceled — terminal freeze in TaskStore.Update
	// guarantees the store won't accept further changes.
	t1, _ = store.Get("agent-y", "t1")
	if t1.Status.State != a2a.TaskStateCanceled {
		t.Errorf("t1 state drifted to %q after t2 events", t1.Status.State)
	}
}

// TestA2APosterTap_ForwardsAndFeedsDispatcher ensures the wrapper around
// AgentEventPoster both posts to the hub (so existing event consumers
// keep working) and fans the event out to the dispatcher's correlator.
func TestA2APosterTap_ForwardsAndFeedsDispatcher(t *testing.T) {
	p := &fakeInputPoster{}
	d := newA2AHubDispatcher(p)
	store := a2a.NewTaskStore()
	store.Create("agent-z", "task-tap", a2a.Message{
		MessageID: "u", Role: "user",
		Parts: json.RawMessage(`[{"kind":"text","text":"x"}]`),
	})
	_ = d.Dispatch(context.Background(), "agent-z",
		a2a.Message{
			MessageID: "u", Role: "user",
			Parts: json.RawMessage(`[{"kind":"text","text":"x"}]`),
		},
		"task-tap", store)

	inner := &a2aTapInnerPoster{}
	tap := newA2APosterTap(inner, d)
	if err := tap.PostAgentEvent(context.Background(),
		"agent-z", "text", "agent",
		map[string]any{"text": "tapped"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if len(inner.calls) != 1 {
		t.Fatalf("inner got %d calls, want 1", len(inner.calls))
	}
	got, _ := store.Get("agent-z", "task-tap")
	if got.Status.State != a2a.TaskStateWorking {
		t.Errorf("state = %q, want working", got.Status.State)
	}
	if len(got.History) != 2 {
		t.Errorf("history len = %d, want 2", len(got.History))
	}
}

// a2aTapInnerPoster is the minimal AgentEventPoster test double for a2a
// tap assertions. Named distinctly from fakePoster (driver_pane_test.go)
// to avoid colliding with that type in the same package.
type a2aTapInnerPoster struct {
	calls []struct {
		agentID, kind, producer string
		payload                 any
	}
}

func (f *a2aTapInnerPoster) PostAgentEvent(_ context.Context, agentID, kind, producer string, payload any) error {
	f.calls = append(f.calls, struct {
		agentID, kind, producer string
		payload                 any
	}{agentID, kind, producer, payload})
	return nil
}
