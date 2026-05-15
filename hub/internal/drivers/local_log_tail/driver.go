// LocalLogTailDriver — agent-mode M4 successor for engines that ship a
// structured on-disk session log (ADR-027). The driver owns lifecycle +
// state machine + input dispatch; per-engine specifics (path resolver,
// JSONL parser, hook payload mapping, send-keys vocabulary) plug in
// behind the Adapter interface. MVP wires only the claude-code adapter
// (sibling package claude_code); gemini / codex / kimi adapters land in
// Phase 2/3.
//
// Structural typing keeps this package free of any hub/internal/hostrunner
// import: Driver satisfies hostrunner.Driver + hostrunner.Inputter
// implicitly, and EventPoster matches hostrunner.AgentEventPoster
// signature-for-signature, so the runner can pass its *hostrunner.Client
// in unmodified.
package locallogtail

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// EventPoster is the minimal hub-side dependency the driver needs. The
// signature matches hostrunner.AgentEventPoster exactly so a
// *hostrunner.Client passed at construction satisfies this interface by
// structural typing — no import cycle.
type EventPoster interface {
	PostAgentEvent(ctx context.Context, agentID, kind, producer string, payload any) error
}

// Adapter is the per-engine plug-in. The driver delegates everything
// engine-specific to it:
//   - Start spins up the JSONL tailer + hook state sink (W2/W5b) and
//     returns when the adapter is wired. The adapter posts AgentEvents
//     via the EventPoster supplied in Config.
//   - Stop drains background work. Must be idempotent — host-runner's
//     reconcile loop and context cancellation can both invoke it.
//   - HandleInput translates a mobile input (text/cancel/approval/etc.)
//     into engine-side action — tmux send-keys for M4 LocalLogTail.
//     Unknown kinds should return an error so the InputRouter logs the
//     drop instead of silently swallowing.
type Adapter interface {
	Start(ctx context.Context) error
	Stop()
	HandleInput(ctx context.Context, kind string, payload map[string]any) error
}

// Config bundles the runtime dependencies the driver hands to its
// adapter. Keeping these as a struct rather than a long constructor
// argument list makes the wiring readable from runner.go where the
// Driver is built alongside the other per-mode drivers.
type Config struct {
	AgentID string
	PaneID  string
	Poster  EventPoster
	Log     *slog.Logger
}

// Driver is the lifecycle shell for the local-log-tail mode. It is
// deliberately small: lifecycle.started/stopped emission, Start/Stop
// idempotency, and Input passthrough to the adapter. State-machine
// transitions and JSONL parsing live in the adapter (W2 onward).
type Driver struct {
	Config
	Adapter Adapter

	mu      sync.Mutex
	started bool
	stopped bool
}

// Start emits lifecycle.started and starts the adapter. On adapter
// failure it emits lifecycle.stopped before returning so downstream
// caches don't leave the agent looking live.
func (d *Driver) Start(parent context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	d.mu.Unlock()

	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Adapter == nil {
		return fmt.Errorf("local_log_tail: nil Adapter")
	}
	if d.Poster == nil {
		return fmt.Errorf("local_log_tail: nil Poster")
	}
	if d.AgentID == "" {
		return fmt.Errorf("local_log_tail: empty AgentID")
	}

	_ = d.Poster.PostAgentEvent(parent, d.AgentID, "lifecycle", "system",
		map[string]any{
			"phase": "started",
			"mode":  "M4-local-log-tail",
			"pane":  d.PaneID,
		})

	if err := d.Adapter.Start(parent); err != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.Poster.PostAgentEvent(shutCtx, d.AgentID, "lifecycle", "system",
			map[string]any{
				"phase": "stopped",
				"mode":  "M4-local-log-tail",
				"err":   err.Error(),
			})
		return fmt.Errorf("local_log_tail adapter start: %w", err)
	}
	return nil
}

// Stop drains the adapter and emits lifecycle.stopped. Safe to call
// more than once — reconcile and ctx cancel can both fire it for the
// same agent during shutdown (matches the contract of the other M1/M2/M4
// drivers in hub/internal/hostrunner).
func (d *Driver) Stop() {
	d.mu.Lock()
	if d.stopped || !d.started {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	d.mu.Unlock()

	if d.Adapter != nil {
		d.Adapter.Stop()
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = d.Poster.PostAgentEvent(shutCtx, d.AgentID, "lifecycle", "system",
		map[string]any{
			"phase": "stopped",
			"mode":  "M4-local-log-tail",
		})
}

// Input forwards a mobile input event to the adapter for engine-side
// dispatch. Implements hostrunner.Inputter via structural typing.
func (d *Driver) Input(ctx context.Context, kind string, payload map[string]any) error {
	if d.Adapter == nil {
		return fmt.Errorf("local_log_tail: nil Adapter")
	}
	return d.Adapter.HandleInput(ctx, kind, payload)
}
