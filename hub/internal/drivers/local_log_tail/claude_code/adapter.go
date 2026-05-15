// Package claudecode is the claude-code-specific plug-in for the
// LocalLogTailDriver (ADR-027 W2). Implements
// locallogtail.Adapter by composing four leaf concerns:
//
//   - pathresolver: locate the on-disk session JSONL once it appears
//     under ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
//   - tailer:       follow that JSONL as claude-code appends to it
//   - mapper:       turn each JSONL line into 1..N AgentEvents
//   - hooks:        translate hook MCP calls from the host-runner
//                   gateway into FSM transitions + AgentEvents
//
// Each leaf lives in its own file; this one is the integration
// surface. See docs/reference/claude-code-adapter-design.md for the
// component diagram and the W2a-i wedge decomposition.
package claudecode

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	locallogtail "github.com/termipod/hub/internal/drivers/local_log_tail"
)

// Config carries the runtime dependencies the adapter needs from the
// driver. Embedded in NewAdapter; held read-only after Start.
type Config struct {
	// AgentID is the hub-side agent identifier used to namespace
	// posted events.
	AgentID string
	// Workdir is the project root claude-code is running in. The
	// session JSONL lives under ~/.claude/projects/<encoded-workdir>/.
	Workdir string
	// ClaudePID is the OS pid of the claude-code process — used by
	// the pane resolver (W2g) and the hard-cancel ladder (W2h).
	// Zero is allowed at construction; pane lookups defer until set.
	ClaudePID int
	// Poster is how the adapter publishes AgentEvents to the hub.
	Poster locallogtail.EventPoster
	// Log is optional; defaults to slog.Default().
	Log *slog.Logger
	// Knobs holds the MVP-tunable timings (plan §8). Zero values
	// trigger sensible defaults at Start.
	Knobs Knobs
}

// Knobs are the MVP-tunable timings (plan §8). All zero values
// resolve to sensible defaults at Start time so callers can pass an
// empty Knobs{} and get the documented behaviour.
type Knobs struct {
	IdleThresholdMs    int // default: 2000
	HookParkDefaultMs  int // default: 60000
	CancelHardAfterMs  int // default: 2000
	ReplayTurnsOnAttach int // default: 5
}

func (k Knobs) withDefaults() Knobs {
	if k.IdleThresholdMs == 0 {
		k.IdleThresholdMs = 2000
	}
	if k.HookParkDefaultMs == 0 {
		k.HookParkDefaultMs = 60_000
	}
	if k.CancelHardAfterMs == 0 {
		k.CancelHardAfterMs = 2000
	}
	if k.ReplayTurnsOnAttach == 0 {
		k.ReplayTurnsOnAttach = 5
	}
	return k
}

// Adapter implements locallogtail.Adapter for claude-code. Each W2
// sub-wedge fills in one leaf concern; W2a (this file) is the
// scaffolding: methods are present and satisfy the interface, but
// most are no-ops until later wedges land.
type Adapter struct {
	Config

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
}

// NewAdapter constructs a claude-code Adapter. Returns an error early
// if mandatory config is missing, so the caller (W7 launch glue) can
// fall back to PaneDriver without leaking a half-built struct.
func NewAdapter(cfg Config) (*Adapter, error) {
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("claude-code adapter: AgentID required")
	}
	if cfg.Workdir == "" {
		return nil, fmt.Errorf("claude-code adapter: Workdir required")
	}
	if cfg.Poster == nil {
		return nil, fmt.Errorf("claude-code adapter: Poster required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	cfg.Knobs = cfg.Knobs.withDefaults()
	return &Adapter{Config: cfg}, nil
}

// Start spawns the adapter's background work (JSONL tailer, FSM
// runner, etc.). W2a stub: only marks the started flag; later
// wedges plug in the real pipelines.
func (a *Adapter) Start(parent context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return nil
	}
	a.started = true
	_, a.cancel = context.WithCancel(parent)
	return nil
}

// Stop tears down the adapter. Idempotent.
func (a *Adapter) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped || !a.started {
		return
	}
	a.stopped = true
	if a.cancel != nil {
		a.cancel()
	}
}

// HandleInput translates a mobile input event into an engine-side
// action. W2a stub: returns a not-implemented error so the test in
// W1 documenting that Input() flows here doesn't silently succeed
// for kinds the real adapter (W2h) hasn't wired yet.
func (a *Adapter) HandleInput(_ context.Context, kind string, _ map[string]any) error {
	return fmt.Errorf("claude-code adapter: input kind %q not yet wired (W2h pending)", kind)
}

// OnHook routes a hook MCP call from the host-runner gateway to the
// per-event handler. W2a stub: returns an empty response so the
// gateway can complete the call; later wedges (W2e/i) translate
// payloads into FSM transitions + AgentEvents + parked-attention
// coordination.
func (a *Adapter) OnHook(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

// Compile-time assertion: *Adapter satisfies locallogtail.Adapter
// (Start, Stop, HandleInput, OnHook).
var _ locallogtail.Adapter = (*Adapter)(nil)
