package claudecode

import (
	"context"
	"log/slog"
	"sync"

	locallogtail "github.com/termipod/hub/internal/drivers/local_log_tail"
)

// State is one of the three FSM positions per plan §4. Transitions
// fire when either (a) the JSONL run loop observes a tool_use /
// turn boundary, or (b) a hook handler reports an idle / streaming /
// parked-decision signal. The FSM is the single source of truth for
// the "agent busy" pill mobile renders.
type State int

const (
	// StateIdle: claude is waiting for the next user prompt.
	// Entered on Stop hook, Notification{idle_prompt}, or
	// idle_threshold timeout (W2f-future).
	StateIdle State = iota
	// StateStreaming: claude is executing a turn — emitting text,
	// thoughts, tool_uses. Entered on the first JSONL tool_use of a
	// turn or on PreToolUse hook (whichever fires first).
	StateStreaming
	// StateAwaitingDecision: a parked hook (PreCompact, or PreToolUse
	// for AskUserQuestion) is blocked waiting for mobile to resolve
	// an attention item. claude itself is not producing new output;
	// the pill shows "decision needed".
	StateAwaitingDecision
)

// String returns a stable mobile-friendly label. Kept short so the
// state_changed payload doesn't bloat with prose.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateStreaming:
		return "streaming"
	case StateAwaitingDecision:
		return "awaiting_decision"
	default:
		return "unknown"
	}
}

// FSM is the mutex-protected state machine. Concurrency model is
// "synchronous": every Transition call either changes state or
// no-ops. No goroutine, no channels — keeps the contract small and
// the hook handlers + JSONL run loop trivially serializable. If the
// FSM ever becomes a hot path the implementation can swap to a
// channel-fed goroutine without changing the public surface.
//
// v1.0.663 stopped posting `system{subtype:state_changed,…}` to
// PostAgentEvent. State drives internal logic (idle/streaming gates
// the busy walker indirectly via the turn.result/Stop hook emission),
// but the per-transition system frame was noise mobile rendered as a
// raw JSON dump on every hook fire — same problem the three
// `system{subtype:…}` hook emissions hit at v1.0.661.
type FSM struct {
	poster  locallogtail.EventPoster
	agentID string
	log     *slog.Logger

	mu      sync.Mutex
	state   State
	postCtx context.Context // ctx the FSM uses when posting (zero = context.Background)
}

// NewFSM returns an FSM in StateIdle. postCtx is the context the FSM
// passes to locallogtail.EventPoster.PostAgentEvent — typically the adapter's
// long-lived context so a posted state_changed honours the same
// cancellation as the rest of the adapter.
func NewFSM(agentID string, poster locallogtail.EventPoster, log *slog.Logger, postCtx context.Context) *FSM {
	if log == nil {
		log = slog.Default()
	}
	if postCtx == nil {
		postCtx = context.Background()
	}
	return &FSM{agentID: agentID, poster: poster, log: log, postCtx: postCtx}
}

// State returns the current FSM position. Safe to call concurrently.
func (f *FSM) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// Transition moves to `to` if `to` differs from the current state.
// Pre-v1.0.663 the function also posted a `system{subtype:state_changed,
// from, to, reason}` event on every change — but mobile had no
// renderer for that subtype, so each transition (every PreToolUse,
// every Stop, etc.) put a raw JSON blob in the transcript. The
// reason+from+to are still logged at debug level for operator
// forensics; the FSM remains the canonical state source for any
// future internal consumer.
func (f *FSM) Transition(to State, reason string) {
	f.mu.Lock()
	from := f.state
	if from == to {
		f.mu.Unlock()
		return
	}
	f.state = to
	f.mu.Unlock()
	f.log.Debug("claude-code FSM transition",
		"agent_id", f.agentID, "from", from.String(),
		"to", to.String(), "reason", reason)
}
