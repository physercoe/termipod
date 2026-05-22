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

// Start resolves the conversation + transcript, then spawns the run loop
// that turns each transcript Step into AgentEvents. Two synchronous
// phases (resolution can block on agy's cold start) before the loop:
//
//  1. WaitForConversation: poll agy's workspace→id cache for Workdir.
//  2. WaitForTranscript: poll for transcript_full.jsonl to appear.
//
// A failure in either returns from Start so the launch glue can fall back
// to PaneDriver M4.
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
			return fmt.Errorf("antigravity adapter: resolve HOME: %w", err)
		}
		homeDir = hd
	}

	waitTimeout := a.SessionWaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = 60 * time.Second
	}
	waitCtx, cancelWait := context.WithTimeout(parent, waitTimeout)
	defer cancelWait()

	convID := a.ConversationID
	if convID == "" {
		id, err := WaitForConversation(waitCtx, homeDir, a.Workdir, 0, a.NewestBrainFallback)
		if err != nil {
			return fmt.Errorf("antigravity adapter: resolve conversation id: %w", err)
		}
		convID = id
	}
	a.ConversationID = convID

	// Persist the resume cursor (ADR-035 D8 / ADR-014). The hub's
	// captureEngineSessionID lifts `session_id` off any agent-posted
	// session.init into sessions.engine_session_id; on respawn
	// spliceAntigravityResume threads it back as `agy --conversation
	// <id>`. Engine-neutral column, agy-shaped value (the conversationId).
	_ = a.Poster.PostAgentEvent(parent, a.AgentID, "session.init", "agent",
		map[string]any{"session_id": convID})

	transcript, err := WaitForTranscript(waitCtx, homeDir, convID, 0)
	if err != nil {
		return fmt.Errorf("antigravity adapter: wait for transcript: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel

	a.reader = &Reader{Path: transcript}
	steps, err := a.reader.Start(ctx)
	if err != nil {
		cancel()
		return fmt.Errorf("antigravity adapter: reader start: %w", err)
	}

	a.wg.Add(1)
	go a.runLoop(ctx, steps)

	a.Log.Info("antigravity adapter started",
		"agent_id", a.AgentID,
		"workdir", a.Workdir,
		"conversation_id", convID,
		"transcript", transcript)
	return nil
}

// runLoop maps each Step to AgentEvents and posts them. Post failures are
// logged, not fatal (a transient hub blip shouldn't kill the adapter);
// mapper errors (a torn JSON line) log + drop so one bad write doesn't
// take down the transcript.
func (a *Adapter) runLoop(ctx context.Context, steps <-chan Step) {
	defer a.wg.Done()
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
