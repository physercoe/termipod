package claudecode

import (
	"context"
	"sync"
	"testing"
)

type fsmTestPoster struct {
	mu     sync.Mutex
	events []map[string]any
}

func (p *fsmTestPoster) PostAgentEvent(_ context.Context, _, kind, producer string, payload any) error {
	pm, _ := payload.(map[string]any)
	p.mu.Lock()
	p.events = append(p.events, map[string]any{
		"_kind": kind, "_producer": producer, "payload": pm,
	})
	p.mu.Unlock()
	return nil
}

func (p *fsmTestPoster) snapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.events))
	copy(out, p.events)
	return out
}

func TestFSM_StartsIdle(t *testing.T) {
	f := NewFSM("a", &fsmTestPoster{}, nil, nil)
	if got := f.State(); got != StateIdle {
		t.Errorf("initial state = %v, want StateIdle", got)
	}
}

func TestFSM_StateString(t *testing.T) {
	for s, want := range map[State]string{
		StateIdle:             "idle",
		StateStreaming:        "streaming",
		StateAwaitingDecision: "awaiting_decision",
		State(99):             "unknown",
	} {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestFSM_TransitionPostsStateChanged(t *testing.T) {
	p := &fsmTestPoster{}
	f := NewFSM("a", p, nil, context.Background())
	f.Transition(StateStreaming, "tool_use")

	if got := f.State(); got != StateStreaming {
		t.Errorf("state = %v, want StateStreaming", got)
	}
	evs := p.snapshot()
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	if evs[0]["_kind"] != "system" {
		t.Errorf("kind = %v, want system", evs[0]["_kind"])
	}
	pl, _ := evs[0]["payload"].(map[string]any)
	if pl["subtype"] != "state_changed" {
		t.Errorf("subtype = %v, want state_changed", pl["subtype"])
	}
	if pl["from"] != "idle" || pl["to"] != "streaming" {
		t.Errorf("from/to = %v/%v, want idle/streaming", pl["from"], pl["to"])
	}
	if pl["reason"] != "tool_use" {
		t.Errorf("reason = %v, want tool_use", pl["reason"])
	}
}

func TestFSM_NoOpTransitionDoesNotPost(t *testing.T) {
	p := &fsmTestPoster{}
	f := NewFSM("a", p, nil, context.Background())
	f.Transition(StateIdle, "Stop") // already idle
	if got := p.snapshot(); len(got) != 0 {
		t.Errorf("no-op transition posted %d events, want 0", len(got))
	}
}

func TestFSM_SequenceOfTransitions(t *testing.T) {
	p := &fsmTestPoster{}
	f := NewFSM("a", p, nil, context.Background())

	f.Transition(StateStreaming, "tool_use")          // idle → streaming
	f.Transition(StateAwaitingDecision, "PreCompact") // streaming → awaiting_decision
	f.Transition(StateIdle, "Stop hook")              // awaiting_decision → idle
	f.Transition(StateIdle, "redundant")              // no-op

	got := p.snapshot()
	if len(got) != 3 {
		t.Fatalf("posts = %d, want 3 (idempotent last call drops): %+v", len(got), got)
	}
	transitions := []string{}
	for _, e := range got {
		pl, _ := e["payload"].(map[string]any)
		transitions = append(transitions, pl["from"].(string)+"->"+pl["to"].(string))
	}
	want := []string{
		"idle->streaming",
		"streaming->awaiting_decision",
		"awaiting_decision->idle",
	}
	for i := range want {
		if transitions[i] != want[i] {
			t.Errorf("transition %d = %q, want %q", i, transitions[i], want[i])
		}
	}
}

func TestFSM_TransitionOmitsEmptyReason(t *testing.T) {
	p := &fsmTestPoster{}
	f := NewFSM("a", p, nil, context.Background())
	f.Transition(StateStreaming, "")
	got := p.snapshot()
	pl, _ := got[0]["payload"].(map[string]any)
	if _, has := pl["reason"]; has {
		t.Errorf("empty reason was still emitted: %v", pl)
	}
}
