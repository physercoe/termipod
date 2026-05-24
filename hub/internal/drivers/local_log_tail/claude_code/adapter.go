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
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

	// engineSessionID is the claude-code session UUID — the basename
	// (sans `.jsonl`) of the file we're tailing. Captured in
	// resolveAndRun once WaitForSessionSince picks the live JSONL, and
	// emitted on the synthetic session.init payload so the hub's
	// captureEngineSessionID handler can stamp it onto
	// `sessions.engine_session_id`. That column drives the resume path:
	// handleResumeSession reads it and spliceClaudeResume threads
	// `--resume <uuid>` into the respawn cmd so claude reattaches to
	// the prior conversation instead of cold-starting. Without this
	// field, every M4 claude-code resume opened a fresh session.
	// v1.0.672 — see ADR-014.
	engineSessionID string

	// latestStatusLine is the most recent claude-code statusLine
	// snapshot the per-spawn UDS gateway has handed us through
	// OnStatusLine (ADR-036 W2). When non-nil, its fields override
	// JSONL-derived values on the events the adapter emits:
	//
	//   - session.init.version ← statusLine.version (replaces the
	//     hardcoded "claude-code" literal at maybeEmitSessionInit's
	//     payload assembly).
	//   - usage.context_window ← statusLine.context_window.context_window_size
	//     (replaces the prefix-family heuristic in
	//     claudeModelContextWindow when the authoritative number is
	//     available).
	//
	// We DON'T overwrite the usage event's `model` field. The
	// statusLine `model.id` sometimes carries a `[1m]` tier suffix
	// (host-verified: present on sonnet-4-6, absent on opus-4-7, even
	// though both are 1M-windowed). Different from the bare model name
	// the JSONL `message.model` carries, which mobile groups usage by
	// — splitting that string into `<bare>[<tier>]` would create
	// orphaned per-tier rollups on the mobile side.
	//
	// Mutex-guarded because the gateway's OnStatusLine can fire
	// concurrent with the runLoop's read; ADR-036 D3's 1s dedupe
	// reduces but doesn't eliminate that.
	latestStatusLineMu sync.RWMutex
	latestStatusLine   map[string]any

	// pendingRotation, when non-empty, signals that OnStatusLine has
	// detected a session_id rotation (ADR-036 W3 — /clear within a
	// running claude process mints a new session_id + a new JSONL
	// file; today's adapter keeps tailing the OLD one until respawn).
	// OnStatusLine stamps the new transcript_path here AND calls
	// tailer.Stop(), which closes the lines channel; resolveAndRun's
	// outer loop then re-points the tailer at this path and starts a
	// fresh runLoop. Field is consumed at the top of the next loop
	// iteration and cleared. pendingMu guards both fields.
	pendingMu             sync.Mutex
	pendingTranscriptPath string
	pendingSessionID      string
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

// resolveAndRun is the async pipeline kicked off by Start. Runs as
// a session-loop: resolve the JSONL → tail → mapper → poster, then
// if runLoop returned because of a pending rotation (W3), pick up
// the new transcript path and re-enter. Outer loop exits when the
// parent context cancels or the tailer terminates without a pending
// rotation.
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

	// First session: wait for the JSONL to appear, then tail it. The
	// tail mode for the first session honours TailMode (resume spawns
	// pass StartFromEnd to skip pre-existing transcript bytes).
	waitCtx, cancelWait := context.WithTimeout(ctx, waitTimeout)
	jsonlPath, err := WaitForSessionSince(waitCtx, projectDir, 0, a.SessionCutoff)
	cancelWait()
	if err != nil {
		a.noteFailure(ctx, "wait for session jsonl in "+projectDir, err)
		return
	}
	tailMode := a.TailMode

	// Session-loop. Each iteration tails one JSONL file from start to
	// stop (or until OnStatusLine signals a rotation by Stop'ing the
	// tailer + writing pendingTranscriptPath). Post-W3 the loop
	// re-enters for every /clear; pre-W3 it runs exactly once and
	// exits when the tailer closes its lines channel naturally.
	for {
		a.tailer = &Tailer{Path: jsonlPath, Mode: tailMode}
		lines, err := a.tailer.Start(ctx)
		if err != nil {
			a.noteFailure(ctx, "tailer start", err)
			return
		}

		// Capture the engine session UUID from the JSONL filename
		// (`<uuid>.jsonl`). claude-code itself uses this same id as the
		// argument to its `--resume` flag (verified by sampling a fresh
		// JSONL: each line carries a `sessionId` field that matches the
		// basename). Stashing it here makes it available to
		// maybeEmitSessionInit so the synthetic session.init payload
		// carries `session_id`, which the hub's captureEngineSessionID
		// then stamps onto `sessions.engine_session_id` for the resume
		// path to consume. v1.0.672.
		a.engineSessionID = strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")

		a.Log.Info("claude-code adapter started",
			"agent_id", a.AgentID,
			"workdir", a.Workdir,
			"jsonl", jsonlPath,
			"engine_session_id", a.engineSessionID,
			"replay", tailMode == StartFromBeginning)

		a.runLoop(ctx, lines)

		// runLoop has returned. Either:
		//   1. ctx was cancelled (Stop or parent cancel) → exit cleanly.
		//   2. lines channel closed naturally → exit unless rotation
		//      is pending.
		if ctx.Err() != nil {
			return
		}

		// Check for a pending W3 rotation. If OnStatusLine fired
		// with a new session_id + transcript_path, it stamped them
		// here and Stop'd the tailer; pick them up and re-loop.
		a.pendingMu.Lock()
		nextPath := a.pendingTranscriptPath
		a.pendingTranscriptPath = ""
		a.pendingSessionID = ""
		a.pendingMu.Unlock()
		if nextPath == "" {
			return // natural EOF without rotation — done.
		}

		jsonlPath = nextPath
		// New conversation post-/clear: tail from beginning so we
		// catch any content claude wrote between minting the file
		// and our rotation handler running.
		tailMode = StartFromBeginning
		// Reset the session.init guard so the next usage event
		// re-emits a fresh session.init carrying the new session_id.
		// Mobile re-renders chips on session.init; rotation is a
		// real lifecycle event the principal needs to see.
		a.sessionInitMu.Lock()
		a.sessionInitSent = false
		a.sessionInitMu.Unlock()
		a.Log.Info("claude-code adapter: rotating to new session (post-/clear)",
			"agent_id", a.AgentID,
			"new_jsonl", jsonlPath)
	}
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
		"version": "claude-code", // fallback; the statusLine override below replaces this with the real binary version when known
	}
	// ADR-036 D6: prefer the statusLine-sourced `version` (e.g.
	// "2.1.150") over the hardcoded literal when we've already seen a
	// statusLine frame. If status_line hasn't fired yet (race with
	// first usage), the literal stays — the next session_id rotation
	// in W3 will re-emit with the authoritative value.
	if v := a.statusLineVersion(); v != "" {
		payload["version"] = v
	}
	// session_id is the claude-code session UUID — the basename (sans
	// `.jsonl`) of the file we're tailing. The hub's
	// captureEngineSessionID handler reads this field off
	// session.init payloads and stamps it onto
	// `sessions.engine_session_id`, which the resume path then threads
	// back into the respawn cmd as `--resume <uuid>`. Without this
	// field every M4 claude-code resume cold-started. v1.0.672.
	if a.engineSessionID != "" {
		payload["session_id"] = a.engineSessionID
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
				// ADR-036 D6: prefer the statusLine-sourced
				// context_window_size over the prefix-family heuristic
				// when a statusLine frame has arrived. usageFromMessage
				// has already stamped its best-guess value (or omitted
				// the field for unrecognised models); this override
				// replaces it with the authoritative number once we
				// have one. No-op when no statusLine has fired yet OR
				// the event isn't a usage event.
				if ev.Kind == "usage" {
					if cw := a.statusLineContextWindow(); cw > 0 {
						if payload == nil {
							payload = map[string]any{}
						}
						payload["context_window"] = cw
					}
				}
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

// OnStatusLine is the gateway-side seam for claude-code statusLine
// frames (ADR-036 W2 + W3). The per-spawn UDS gateway invokes this
// SYNCHRONOUSLY after posting the status_line AgentEvent to the hub.
// Two responsibilities:
//
//   1. Cache the snapshot (W2) so subsequent JSONL-derived events
//      can override their JSONL-heuristic fields with the
//      authoritative values.
//
//   2. Detect session_id rotation (W3) — when /clear within a
//      running claude process mints a new session_id + JSONL file,
//      stamp the new path under pendingTranscriptPath and Stop the
//      current tailer. resolveAndRun's outer loop picks up the new
//      path and starts a fresh runLoop.
//
// Rotation is gated on three preconditions: (a) we already know an
// engineSessionID (adapter has fully resolved its first session;
// pre-resolution races are ignored), (b) the statusLine carries a
// non-empty session_id that differs from the current one, and (c)
// the statusLine carries a non-empty transcript_path so we have
// somewhere to point the new tailer.
func (a *Adapter) OnStatusLine(_ context.Context, payload map[string]any) {
	if payload == nil {
		return
	}
	a.latestStatusLineMu.Lock()
	a.latestStatusLine = payload
	a.latestStatusLineMu.Unlock()

	// W3: rotation detection. Read fresh from `payload` since
	// the cached snapshot we just stored is the same object.
	newSID, _ := payload["session_id"].(string)
	newPath, _ := payload["transcript_path"].(string)
	if newSID == "" || newPath == "" {
		return
	}
	curSID := a.engineSessionID
	if curSID == "" || newSID == curSID {
		return
	}

	// Concurrency note: by the time we get here, the gateway is in
	// the middle of its tools/call handler. Tailer.Stop() blocks
	// until the tailer loop exits AND its lines channel closes; the
	// runLoop reading from lines will see !ok and return. The
	// session-loop in resolveAndRun then reads pendingTranscriptPath
	// (under pendingMu) and re-enters with the new file. This is
	// synchronous from the gateway's perspective (~100ms typical),
	// which is fine because the shim's exit-0 contract masks any
	// latency at the claude TUI layer.
	a.pendingMu.Lock()
	a.pendingTranscriptPath = newPath
	a.pendingSessionID = newSID
	a.pendingMu.Unlock()

	a.Log.Info("claude-code adapter: statusLine reports session_id rotation",
		"agent_id", a.AgentID,
		"prev_session_id", curSID,
		"new_session_id", newSID,
		"new_transcript_path", newPath)

	a.mu.Lock()
	tailer := a.tailer
	a.mu.Unlock()
	if tailer != nil {
		tailer.Stop()
	}
}

// statusLineVersion returns the statusLine-sourced binary version
// string (e.g. "2.1.150"), or "" if no statusLine frame has been
// received yet OR the field is absent / wrong type. Used by
// maybeEmitSessionInit to replace the hardcoded "claude-code" literal.
func (a *Adapter) statusLineVersion() string {
	a.latestStatusLineMu.RLock()
	defer a.latestStatusLineMu.RUnlock()
	if a.latestStatusLine == nil {
		return ""
	}
	s, _ := a.latestStatusLine["version"].(string)
	return s
}

// statusLineContextWindow returns the statusLine-sourced authoritative
// context-window size (in tokens), or 0 if no statusLine frame has
// been received yet OR the nested context_window.context_window_size
// field is absent / wrong type. Used by the runLoop to override the
// per-message usage event's prefix-family heuristic with the
// authoritative value.
func (a *Adapter) statusLineContextWindow() int {
	a.latestStatusLineMu.RLock()
	defer a.latestStatusLineMu.RUnlock()
	if a.latestStatusLine == nil {
		return 0
	}
	cw, ok := a.latestStatusLine["context_window"].(map[string]any)
	if !ok {
		return 0
	}
	// JSON decode lands numeric fields as float64; accept either to
	// stay robust against any future re-marshalling on the way in.
	switch v := cw["context_window_size"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	}
	return 0
}

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
