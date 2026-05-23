package antigravity

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
// launch glue. Held read-only after Start.
type Config struct {
	// AgentID namespaces posted events.
	AgentID string
	// Workdir is the directory `agy` was launched in — the key into
	// agy's workspace→conversationId cache (pathresolver).
	Workdir string
	// HomeDir overrides $HOME when resolving agy's store. Zero =
	// os.UserHomeDir(). The W7 launch glue may set a per-spawn HOME for
	// MCP-config isolation on shared hosts (ADR-035 D7).
	HomeDir string
	// Poster publishes AgentEvents to the hub.
	Poster locallogtail.EventPoster
	// Log is optional; defaults to slog.Default().
	Log *slog.Logger
}

// Adapter implements locallogtail.Adapter for Antigravity (`agy`). It
// composes: pathresolver (find the conversationId + transcript) →
// watch-and-diff Reader (snapshot, not tail) → mapper → Poster. Input is
// routed via tmux send-keys (sendkeys.go). agy has no host-runner hook
// surface (no permission-prompt-tool), so OnHook is a benign no-op.
type Adapter struct {
	Config

	// PaneID is the tmux pane id agy runs in (resolved by the W7 launch
	// glue). Required before HandleInput can send-keys.
	PaneID string
	// CmdRunner overrides the exec-backed runner in tests.
	CmdRunner CmdRunner
	// ConversationID is resolved at Start and equals engine_session_id
	// (ADR-035 D8 resume cursor). Exposed so the launch glue can persist
	// it for an interactive `agy --conversation <id>` respawn.
	ConversationID string
	// NewestBrainFallback opts into the racy newest-brain-dir resolver
	// when the workspace→id cache hasn't flushed yet. Safe only under
	// per-spawn HOME isolation (ADR-035 D7).
	NewestBrainFallback bool
	// SessionWaitTimeout caps how long Start polls for the conversation
	// id + transcript to appear. 0 → 60s (agy's first model turn can
	// take several seconds before it mints the conversation).
	SessionWaitTimeout time.Duration

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	reader  *Reader
}

// NewAdapter validates mandatory config so the W7 launch glue can fall
// back to PaneDriver without leaking a half-built struct.
func NewAdapter(cfg Config) (*Adapter, error) {
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("antigravity adapter: AgentID required")
	}
	if cfg.Workdir == "" {
		return nil, fmt.Errorf("antigravity adapter: Workdir required")
	}
	if cfg.Poster == nil {
		return nil, fmt.Errorf("antigravity adapter: Poster required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Adapter{Config: cfg}, nil
}

// Start launches the asynchronous resolver+reader pipeline and returns
// immediately. The pipeline:
//
//  1. WaitForConversation: poll agy's workspace→id cache for Workdir.
//  2. WaitForTranscript: poll for transcript_full.jsonl to appear.
//  3. Reader.Start: open the watch-and-diff snapshot reader.
//  4. runLoop: map each Step to AgentEvents.
//
// Synchronous Start used to block on (1)+(2), but agy only mints its
// conversationId *after* the first user message — and the user can't send
// that message from mobile until the agent leaves `pending`. The driver
// flips the agent to `running` once Start returns, so blocking here
// created an unbreakable deadlock: no events without a conversationId, no
// conversationId without user input, no user input without `running`.
// See the v1.0.642 on-host smoke incident: 60s timeout → adapter error →
// runner fell back to PaneDriver → second pane with the wrong cwd, mobile
// session stuck "busy", input gated.
//
// Async means HandleInput's tmux send-keys path (sendkeys.go — requires
// only PaneID, not ConversationID) starts working the instant the
// driver registers, so the user's first message flows naturally and
// drives agy to mint the conversation. The resolver picks it up on the
// next poll and the transcript tail spins up automatically.
//
// On goroutine failure (timeout, transcript never appears, reader
// errors) we log + post a `system` notice so the activity feed surfaces
// the half-broken state instead of looking silently fine. The pane
// itself is still live; only the transcript-derived events are missing.
func (a *Adapter) Start(parent context.Context) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil
	}
	a.started = true
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	a.mu.Unlock()

	a.wg.Add(1)
	go a.resolveAndRun(ctx)
	return nil
}

// resolveAndRun is the async pipeline kicked off by Start. Runs until
// either the parent context cancels or the resolver/reader completes
// the steady-state loop and `steps` closes.
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

	waitTimeout := a.SessionWaitTimeout
	if waitTimeout <= 0 {
		// 30 min default: the user may sit on the workspace dialog or
		// walk away before sending the first message that mints the
		// conversation. 60s (the pre-fix value) failed every interactive
		// smoke that wasn't already typing.
		waitTimeout = 30 * time.Minute
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, waitTimeout)
	defer cancelWait()

	convID := a.ConversationID
	if convID == "" {
		id, err := WaitForConversation(waitCtx, homeDir, a.Workdir, 0, a.NewestBrainFallback)
		if err != nil {
			a.noteFailure(ctx, "resolve conversation id", err)
			return
		}
		convID = id
	}
	a.mu.Lock()
	a.ConversationID = convID
	a.mu.Unlock()

	// Persist the resume cursor (ADR-035 D8 / ADR-014). The hub's
	// captureEngineSessionID lifts `session_id` off any agent-posted
	// session.init into sessions.engine_session_id; on respawn
	// spliceAntigravityResume threads it back as `agy --conversation
	// <id>`. Engine-neutral column, agy-shaped value (the conversationId).
	_ = a.Poster.PostAgentEvent(ctx, a.AgentID, "session.init", "agent",
		map[string]any{"session_id": convID})

	transcript, err := WaitForTranscript(waitCtx, homeDir, convID, 0)
	if err != nil {
		a.noteFailure(ctx, "wait for transcript", err)
		return
	}

	reader := &Reader{Path: transcript}
	steps, err := reader.Start(ctx)
	if err != nil {
		a.noteFailure(ctx, "reader start", err)
		return
	}
	a.mu.Lock()
	a.reader = reader
	a.mu.Unlock()

	a.Log.Info("antigravity adapter started",
		"agent_id", a.AgentID,
		"workdir", a.Workdir,
		"conversation_id", convID,
		"transcript", transcript)

	a.runLoop(ctx, steps)
}

// noteFailure logs and surfaces a soft failure: the pane is still live,
// but transcript-derived events won't flow for this session. Best-effort
// post — a hub blip shouldn't escalate this further.
func (a *Adapter) noteFailure(ctx context.Context, phase string, err error) {
	a.Log.Warn("antigravity adapter: async pipeline aborted",
		"agent_id", a.AgentID, "phase", phase, "err", err)
	_ = a.Poster.PostAgentEvent(ctx, a.AgentID, "system", "system",
		map[string]any{
			"text": fmt.Sprintf(
				"antigravity transcript tail unavailable (%s: %v). "+
					"The pane is still live — type to interact via tmux.",
				phase, err),
		})
}

// runLoop maps each Step to AgentEvents and posts them. Post failures are
// logged, not fatal (a transient hub blip shouldn't kill the adapter);
// mapper errors (a torn JSON line) log + drop so one bad write doesn't
// take down the transcript. WaitGroup ownership lives in the caller
// (resolveAndRun) — runLoop returns when steps closes or ctx fires.
func (a *Adapter) runLoop(ctx context.Context, steps <-chan Step) {
	for {
		select {
		case <-ctx.Done():
			return
		case step, ok := <-steps:
			if !ok {
				return
			}
			events, err := MapStep(step.Bytes)
			if err != nil {
				a.Log.Warn("antigravity adapter: mapper error; dropping step",
					"agent_id", a.AgentID, "step", step.Index, "err", err)
				continue
			}
			for _, ev := range events {
				if err := a.Poster.PostAgentEvent(ctx, a.AgentID, ev.Kind, ev.Producer, ev.Payload); err != nil {
					a.Log.Debug("antigravity adapter: post failed",
						"agent_id", a.AgentID, "kind", ev.Kind, "err", err)
				}
			}
		}
	}
}

// Stop tears down the adapter. Idempotent; waits for the run loop to
// drain so no PostAgentEvent fires after Stop returns.
func (a *Adapter) Stop() {
	a.mu.Lock()
	if a.stopped || !a.started {
		a.mu.Unlock()
		return
	}
	a.stopped = true
	cancel := a.cancel
	reader := a.reader
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if reader != nil {
		reader.Stop()
	}
	a.wg.Wait()
}

// OnHook is a no-op for agy: it has no host-runner hook surface (no
// permission-prompt-tool, no mcp__termipod-host__hook_* tools — that is
// claude-code-specific, ADR-027 W5b). Returning {} keeps the
// locallogtail.Adapter contract satisfied.
func (a *Adapter) OnHook(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

// Compile-time assertion: *Adapter satisfies locallogtail.Adapter.
var _ locallogtail.Adapter = (*Adapter)(nil)
