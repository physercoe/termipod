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
	// Attention is the hub-side parking client (W2i). Nil disables
	// real parking; parked hooks fall back to the W2e stub of
	// returning {} immediately. The W7 launch glue wires a real
	// HubAttentionClient when constructing the adapter.
	Attention *HubAttentionClient
	// SessionCutoff is the lower bound on JSONL mtime when resolving
	// which session file to tail. Files with mtime ≤ SessionCutoff
	// are ignored — they belong to a previous `claude` session in the
	// same workdir, not this spawn. Default is the adapter's
	// construction time (set in NewAdapter). Tests pass an explicit
	// value (often time.Time{} for "no cutoff" or a fresh
	// time.Now() to verify the filter behaviour).
	SessionCutoff time.Time

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	tailer  *Tailer
	fsm     *FSM

	// pickerMu guards pickerDone. The AskUserQuestion picker uses
	// an in-process channel (not the attention_items table) for
	// hook-unblock signalling: mobile picks an option via
	// HandleInput("pick_option") which sends-keys then closes
	// pickerDone, allowing the parked PreToolUse hook to return.
	// Plan §5.B.1.
	pickerMu   sync.Mutex
	pickerDone chan struct{}

	// sessionInitMu guards sessionInitSent. The flag is checked + set
	// in runLoop so a second usage event on the same session doesn't
	// re-emit the synthetic session.init (mobile would still merge it
	// correctly, but the duplicate event is noise).
	sessionInitMu   sync.Mutex
	sessionInitSent bool
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
	// SessionCutoff defaults to "now" so stale JSONLs from a prior
	// interactive claude session in the same workdir are ignored.
	// Mtime resolution on most filesystems is at-best 1ms; we subtract
	// a small slack so a JSONL claude races us to create still
	// qualifies (rare but observed under heavy load).
	return &Adapter{
		Config:        cfg,
		SessionCutoff: time.Now().Add(-100 * time.Millisecond),
	}, nil
}

// Start composes path resolver → session-wait → tailer → mapper →
// poster. The pipeline is ASYNCHRONOUS — Start returns nil
// immediately after kicking off the resolver goroutine, mirroring
// agy's adapter pattern (which itself replaced an earlier sync Start
// at v1.0.643 after the same deadlock cascade hit it).
//
// Why async: WaitForSession blocks until claude writes its first
// JSONL line, which in turn requires claude to start a session.
// Claude doesn't start a session until it gets past the welcome
// screen (any first-run prompts: trust dialog, OAuth, "select a
// model", hook warnings) AND receives a first user message. A
// synchronous Start with a 30s deadline therefore failed every
// spawn where claude wasn't already typing — including normal cold
// starts on a fresh box. Async means HandleInput's tmux send-keys
// path (sendkeys.go — requires only PaneID, set BEFORE Start by the
// W7 launcher) starts working the instant the driver registers, so
// the user's first message flows naturally and drives claude to
// mint the JSONL. The resolver picks it up on the next poll and the
// transcript tail spins up automatically.
//
// On goroutine failure (timeout, JSONL never appears, tailer errors)
// the goroutine logs + posts a `system` notice so the activity feed
// surfaces the half-broken state instead of looking silently fine.
// The pane itself is still live; only JSONL-derived events are
// missing — mobile can still send/receive text via the send-keys
// path. Same soft-failure shape agy uses.
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
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	a.fsm = NewFSM(a.AgentID, a.Poster, a.Log, ctx)
	a.mu.Unlock()

	a.wg.Add(1)
	go a.resolveAndRun(ctx)
	return nil
}

// resolveAndRun is the async pipeline kicked off by Start. Runs
// until either the parent context cancels or the tailer completes
// the steady-state loop and `lines` closes.
func (a *Adapter) resolveAndRun(ctx context.Context) {
	defer a.wg.Done()

	homeDir := a.HomeDir
	if homeDir == "" {
		hd, err := os.UserHomeDir()
		if err != nil {
			a.noteFailure(ctx, "resolve HOME", err)
			return
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
		// 30 min default — mirrors agy's resolver budget. claude's
		// first JSONL line lands only after the user clears any
		// first-run dialogs (trust, model picker, hook warnings)
		// AND sends a first message. 30s (the pre-v1.0.660 sync
		// default) failed every interactive smoke that wasn't
		// already typing the moment the pane appeared.
		waitTimeout = 30 * time.Minute
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, waitTimeout)
	jsonlPath, err := WaitForSessionSince(waitCtx, projectDir, 0, a.SessionCutoff)
	cancelWait()
	if err != nil {
		a.noteFailure(ctx, "wait for session jsonl in "+projectDir, err)
		return
	}

	a.tailer = &Tailer{Path: jsonlPath, Mode: a.TailMode}
	lines, err := a.tailer.Start(ctx)
	if err != nil {
		a.noteFailure(ctx, "tailer start", err)
		return
	}

	a.Log.Info("claude-code adapter started",
		"agent_id", a.AgentID,
		"workdir", a.Workdir,
		"jsonl", jsonlPath,
		"replay", a.TailMode == StartFromBeginning)

	a.runLoop(ctx, lines)
}

// maybeEmitSessionInit posts a synthetic session.init the first time
// we see a usage event carrying a model field. M4 (on-disk JSONL)
// has no equivalent of M2 stream-json's `init` frame, so mobile's
// AppBar chip stays empty for every M4 spawn without this. Idempotent
// per Adapter lifetime — the flag prevents duplicate emits when many
// usage events flow in the same session.
func (a *Adapter) maybeEmitSessionInit(ctx context.Context, ev MappedEvent) {
	if ev.Kind != "usage" {
		return
	}
	model, _ := ev.Payload["model"].(string)
	if model == "" {
		return
	}
	a.sessionInitMu.Lock()
	if a.sessionInitSent {
		a.sessionInitMu.Unlock()
		return
	}
	a.sessionInitSent = true
	a.sessionInitMu.Unlock()

	payload := map[string]any{
		"engine":  "claude-code",
		"model":   model,
		"cwd":     a.Workdir,
		"version": "claude-code", // mobile's chip shows engine alone if version absent
	}
	if err := a.Poster.PostAgentEvent(ctx, a.AgentID, "session.init", "agent", payload); err != nil {
		a.Log.Warn("claude-code adapter: session.init post failed",
			"agent_id", a.AgentID, "err", err)
	}
}

// noteFailure logs and surfaces a soft failure: the pane is still
// live, but JSONL-derived events won't flow for this session.
// Best-effort post — a hub blip shouldn't escalate this further.
// Mirrors the antigravity adapter's noteFailure (same shape).
func (a *Adapter) noteFailure(ctx context.Context, phase string, err error) {
	a.Log.Warn("claude-code adapter: async pipeline aborted",
		"agent_id", a.AgentID, "phase", phase, "err", err)
	_ = a.Poster.PostAgentEvent(ctx, a.AgentID, "system", "system",
		map[string]any{
			"text": fmt.Sprintf(
				"claude-code JSONL tail unavailable (%s: %v). "+
					"The pane is still live — type to interact via tmux.",
				phase, err),
		})
}

// runLoop pumps lines from the tailer through the mapper and posts
// the resulting events. Posting failures are logged but not fatal —
// a transient hub blip shouldn't kill the adapter; the next event
// will retry. Mapper errors (malformed JSON) log + drop the line so
// one corrupt write doesn't take down the entire transcript.
func (a *Adapter) runLoop(ctx context.Context, lines <-chan Line) {
	// wg.Done lives on resolveAndRun (the goroutine that owns this
	// run-loop call). runLoop no longer touches the WaitGroup since
	// it's invoked synchronously from resolveAndRun, not as its own
	// goroutine — touching wg here would double-Done and panic.
	//
	// v1.0.666 stopped stamping `replay: true` on every M4 event. The
	// pre-v1.0.666 W2d logic set replay = (TailMode == StartFromBeginning),
	// which was true for every fresh spawn (StartFromBeginning is the
	// default). Mobile's agent_feed.dart unconditionally drops text/thought
	// events that arrive with replay:true (the M1 ACP `session/load`
	// dedup path), so every assistant text + thought from M4 was being
	// nuked on the client. Symptom: cold-open shows lifecycle +
	// input.text + turn.result + usage but the assistant's reply
	// never appears, busy-pill never flips, the chat looks dead.
	//
	// M4 doesn't need the replay tag at all because:
	//   1. SSE delivery is seq-gated via `since=<maxSeq>` — old events
	//      that mobile already cached are not redelivered.
	//   2. ID dedup (`_ids.add(id)`) catches anything else, since hub
	//      event IDs are globally unique and stable across cold-open
	//      + live tail.
	// Net effect: just don't tag, and the W2d replay filter on mobile
	// stays useful for the M1 path it was designed for.
	// v1.0.664 diagnostic: one INFO line per agent on first line so
	// operators can see at a glance whether the tailer is producing
	// anything when the on-device transcript stays empty. Counts so
	// runaway tailers don't flood the logs — only the first line
	// gets the INFO; subsequent stay at Debug.
	var sawFirst bool
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			if !sawFirst {
				a.Log.Info("claude-code adapter: first JSONL line received",
					"agent_id", a.AgentID, "bytes", len(line.Bytes))
				sawFirst = true
			}
			events, err := MapLine(line.Bytes)
			if err != nil {
				a.Log.Warn("claude-code adapter: mapper error; dropping line",
					"agent_id", a.AgentID, "err", err)
				continue
			}
			for _, ev := range events {
				payload := ev.Payload
				// v1.0.667: synthesise a session.init event from the
				// first per-message usage frame we see. The usage
				// payload carries `model` (set by usageFromMessage),
				// and the adapter's Workdir gives `cwd`; together
				// that's enough for mobile's AppBar header chip to
				// render the engine + model + cwd row that M1/M2
				// drivers get for free from their engine-emitted
				// init frame. claude-code's on-disk JSONL has no
				// equivalent of M2 stream-json's `init` frame, so
				// the chip was empty for every M4 spawn.
				a.maybeEmitSessionInit(ctx, ev)
				// v1.0.666: no replay tagging — see runLoop header note.
				if err := a.Poster.PostAgentEvent(ctx, a.AgentID, ev.Kind, ev.Producer, payload); err != nil {
					// v1.0.664 escalated from Debug to Warn. Silent
					// post-failure left v1.0.663 on-host smokes
					// guessing why mobile saw neither agent text nor
					// turn.result (the cancel-button-stuck symptom):
					// stderr showed nothing at the default log level
					// even though every event was being dropped. A
					// failing post is operationally a real problem —
					// it means the agent is alive, the JSONL is being
					// read, but the hub never hears about the new
					// frames. Warn surfaces that on the host-runner
					// terminal without requiring a debug rebuild.
					a.Log.Warn("claude-code adapter: post failed",
						"agent_id", a.AgentID, "kind", ev.Kind, "err", err)
				} else {
					a.Log.Debug("claude-code adapter: posted",
						"agent_id", a.AgentID, "kind", ev.Kind)
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
