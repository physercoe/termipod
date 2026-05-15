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
	"os"
	"sync"
	"time"

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

// Adapter implements locallogtail.Adapter for claude-code. W2a-c
// laid down the leaves (path resolver, tailer, schema mapper); W2d
// (this file's Start wiring) composes them into a goroutine that
// turns the on-disk JSONL into MappedEvents posted via Config.Poster.
// Hooks + send-keys + state machine + parked attentions are still
// stubs (W2e-i to fill).
type Adapter struct {
	Config

	// HomeDir overrides $HOME when resolving the claude project
	// directory. Zero value = use os.UserHomeDir(); set by tests.
	HomeDir string
	// SessionWaitTimeout caps how long Start will poll for the
	// session JSONL to appear before failing. 0 → 30s. claude-code
	// typically writes the first event within ~500ms of process
	// start; 30s gives generous headroom for a slow cold-start.
	SessionWaitTimeout time.Duration
	// TailMode overrides the tailer's start mode. Zero value
	// (StartFromBeginning) replays the existing transcript before
	// switching to live tail.
	TailMode StartMode
	// PaneID is the tmux pane id the claude process is running in
	// (resolved by W7's launch glue via ResolvePane). Required for
	// any tmux-driven input kind; empty means HandleInput will
	// return an error rather than send-keys to a wrong target.
	PaneID string
	// CmdRunner overrides the default exec-backed CmdRunner. Tests
	// inject a fake so the test binary doesn't need real tmux/ps on
	// PATH. Production leaves this nil.
	CmdRunner CmdRunner

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	tailer  *Tailer
	fsm     *FSM
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

// Start composes path resolver → session-wait → tailer → mapper →
// poster. Two phases:
//
//  1. Synchronously locate the on-disk session JSONL under
//     <homeDir>/.claude/projects/<encoded-workdir>/. The directory
//     may not exist yet when Start is called (host-runner spawned
//     claude-code only moments earlier), so we mkdir-p the parent
//     and WaitForSession until the first .jsonl appears or the
//     SessionWaitTimeout fires. A failure here returns from Start
//     so the W7 caller can fall back to PaneDriver.
//  2. Spawn the run loop: read MappedEvents from the tailer's
//     channel via MapLine, post each via Config.Poster. The loop
//     exits on ctx cancel / Stop / tailer channel close.
//
// Replay tagging: every event posted before the tailer first
// reaches EOF carries `replay:true` in its payload so downstream
// caches can distinguish historical from live. (Plan §4.2: same
// shape ACPDriver already uses for session/load replay frames.)
// The W2d implementation stamps this only when TailMode ==
// StartFromBeginning; in StartFromEnd mode there is no replay.
func (a *Adapter) Start(parent context.Context) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil
	}
	a.started = true
	a.mu.Unlock()

	homeDir := a.HomeDir
	if homeDir == "" {
		hd, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("claude-code adapter: resolve HOME: %w", err)
		}
		homeDir = hd
	}
	projectDir := ProjectDirFor(homeDir, a.Workdir)
	// Best-effort mkdir so WaitForSession's first poll can see
	// the directory; claude-code itself will create it on first
	// write if missing, but creating it eagerly also covers the
	// case where host-runner is testing the path before claude
	// has produced anything.
	_ = os.MkdirAll(projectDir, 0o755)

	waitTimeout := a.SessionWaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = 30 * time.Second
	}
	waitCtx, cancelWait := context.WithTimeout(parent, waitTimeout)
	jsonlPath, err := WaitForSession(waitCtx, projectDir, 0)
	cancelWait()
	if err != nil {
		return fmt.Errorf("claude-code adapter: wait for session jsonl in %s: %w",
			projectDir, err)
	}

	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	a.fsm = NewFSM(a.AgentID, a.Poster, a.Log, ctx)

	a.tailer = &Tailer{Path: jsonlPath, Mode: a.TailMode}
	lines, err := a.tailer.Start(ctx)
	if err != nil {
		cancel()
		return fmt.Errorf("claude-code adapter: tailer start: %w", err)
	}

	a.wg.Add(1)
	go a.runLoop(ctx, lines)

	a.Log.Info("claude-code adapter started",
		"agent_id", a.AgentID,
		"workdir", a.Workdir,
		"jsonl", jsonlPath,
		"replay", a.TailMode == StartFromBeginning)
	return nil
}

// runLoop pumps lines from the tailer through the mapper and posts
// the resulting events. Posting failures are logged but not fatal —
// a transient hub blip shouldn't kill the adapter; the next event
// will retry. Mapper errors (malformed JSON) log + drop the line so
// one corrupt write doesn't take down the entire transcript.
func (a *Adapter) runLoop(ctx context.Context, lines <-chan Line) {
	defer a.wg.Done()
	replay := a.TailMode == StartFromBeginning
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			events, err := MapLine(line.Bytes)
			if err != nil {
				a.Log.Warn("claude-code adapter: mapper error; dropping line",
					"agent_id", a.AgentID, "err", err)
				continue
			}
			for _, ev := range events {
				payload := ev.Payload
				if replay {
					// Don't mutate the mapper's map: copy so a future
					// reuse of the same map by the mapper doesn't pick
					// up our tagging.
					tagged := make(map[string]any, len(payload)+1)
					for k, v := range payload {
						tagged[k] = v
					}
					tagged["replay"] = true
					payload = tagged
				}
				if err := a.Poster.PostAgentEvent(ctx, a.AgentID, ev.Kind, ev.Producer, payload); err != nil {
					a.Log.Debug("claude-code adapter: post failed",
						"agent_id", a.AgentID, "kind", ev.Kind, "err", err)
				}
				// JSONL-driven FSM transitions: a tool_call means
				// claude is actively producing output — promote to
				// streaming so the busy pill renders. Plan §4 second
				// trigger arrow.
				if a.fsm != nil && ev.Kind == "tool_call" {
					a.fsm.Transition(StateStreaming, "JSONL tool_use")
				}
			}
		}
	}
}

// Stop tears down the adapter. Idempotent. Waits for the run loop
// to drain before returning so the caller can rely on no further
// PostAgentEvent calls firing after Stop.
func (a *Adapter) Stop() {
	a.mu.Lock()
	if a.stopped || !a.started {
		a.mu.Unlock()
		return
	}
	a.stopped = true
	cancel := a.cancel
	tailer := a.tailer
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if tailer != nil {
		tailer.Stop()
	}
	a.wg.Wait()
}

// HandleInput is now implemented in sendkeys.go (W2h).

// OnHook routes a hook MCP call from the host-runner gateway to the
// per-event handler in hooks.go. Each handler updates the FSM and
// emits any derived AgentEvent. W2e wires the 7 observational hooks
// + stub responses for the 2 parked ones (PreCompact, AskUserQuestion);
// W2i fills in real parking via attention_items + /decide.
//
// Pre-Start safety: OnHook may legitimately fire BEFORE Start
// completes — the gateway's accept loop and our Start are racing.
// Return a benign {} in that window so claude isn't blocked on us.
func (a *Adapter) OnHook(ctx context.Context, name string, payload map[string]any) (map[string]any, error) {
	a.mu.Lock()
	started := a.started
	a.mu.Unlock()
	if !started {
		return map[string]any{}, nil
	}
	return a.dispatchHook(ctx, name, payload)
}

// Compile-time assertion: *Adapter satisfies locallogtail.Adapter
// (Start, Stop, HandleInput, OnHook).
var _ locallogtail.Adapter = (*Adapter)(nil)
