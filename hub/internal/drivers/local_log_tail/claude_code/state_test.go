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

// v1.0.663 dropped FSM's `system{subtype:state_changed,…}` post —
// mobile had no renderer for it, so every PreToolUse/Stop/etc dumped
// a raw JSON blob into the transcript. The state machine still drives
// internal logic (turn.result emission on Stop hook, etc.); only the
// per-transition poster call is gone. Tests below assert the new
// invariant: transitions still change `.State()` but post nothing.
func TestFSM_TransitionUpdatesStateWithoutPosting(t *testing.T) {
	p := &fsmTestPoster{}
	f := NewFSM("a", p, nil, context.Background())
	f.Transition(StateStreaming, "tool_use")

	if got := f.State(); got != StateStreaming {
		t.Errorf("state = %v, want StateStreaming", got)
	}
	if got := p.snapshot(); len(got) != 0 {
		t.Errorf("transition posted %d events; v1.0.663 expects 0: %+v", len(got), got)
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

func TestFSM_SequenceOfTransitionsLeavesStateAtTerminal(t *testing.T) {
	p := &fsmTestPoster{}
	f := NewFSM("a", p, nil, context.Background())

	f.Transition(StateStreaming, "tool_use")          // idle → streaming
	f.Transition(StateAwaitingDecision, "PreCompact") // streaming → awaiting_decision
	f.Transition(StateIdle, "Stop hook")              // awaiting_decision → idle
	f.Transition(StateIdle, "redundant")              // no-op

	if got := f.State(); got != StateIdle {
		t.Errorf("final state = %v, want StateIdle", got)
	}
	if got := p.snapshot(); len(got) != 0 {
		t.Errorf("v1.0.663 expects 0 posts across the sequence, got %d: %+v", len(got), got)
	}
}
