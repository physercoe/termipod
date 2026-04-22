// M4 (manual / pane-only) driver — blueprint §5.3.1.
//
// There is no structured control channel here; host-runner periodically
// captures the pane and emits the newly-appended text as an `agent.text`
// event. Fidelity is low by design — the user is free to type directly in
// the pane, and the app still sees the output. M4 is the fallback for
// agents with no structured stdio protocol, and the explicit escape hatch
// when M1/M2 goes sideways.
//
// Producer attribution:
//   - lifecycle events (started/stopped) are producer=system — they're
//     synthesized by host-runner, not emitted by the agent.
//   - text captures are producer=agent — the bytes originated in the
//     agent's stdout (or the user's keystrokes, indistinguishable at the
//     pane level; callers should not rely on the distinction for M4).
package hostrunner

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// defaultPaneCaptureInterval is the scrape cadence. Tight enough that the
// UI feels live, loose enough that the hub isn't flooded on a chatty pane.
const defaultPaneCaptureInterval = 2 * time.Second

// PaneCaptureFunc runs `tmux capture-pane` (or an injected equivalent for
// tests) and returns the current pane contents. An error stops the driver;
// transient tmux failures should be swallowed and returned as empty.
type PaneCaptureFunc func(ctx context.Context, paneID string) (string, error)

// PaneDriver implements M4. It owns a ticker, a capture func, and a
// running-diff cursor; no FIFO, no pipe-pane — so it composes with the
// existing marker Tailer without fighting for the single pipe-pane slot.
type PaneDriver struct {
	AgentID  string
	PaneID   string
	Poster   AgentEventPoster
	Capture  PaneCaptureFunc // nil → tmuxCapturePane
	Interval time.Duration   // 0 → defaultPaneCaptureInterval
	Log      *slog.Logger

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	lastCap string
}

// Start emits a lifecycle.started event and launches the capture loop.
// It returns immediately; capture happens in a background goroutine so a
// slow hub doesn't stall the spawn flow.
func (d *PaneDriver) Start(parent context.Context) error {
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
	if d.Interval == 0 {
		d.Interval = defaultPaneCaptureInterval
	}
	if d.Capture == nil {
		d.Capture = tmuxCapturePane
	}

	_ = d.Poster.PostAgentEvent(parent, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "started", "mode": "M4", "pane": d.PaneID})

	ctx, cancel := context.WithCancel(parent)
	d.cancel = cancel
	d.wg.Add(1)
	go d.loop(ctx)
	return nil
}

// Stop cancels the capture loop, waits for it to drain, and emits
// lifecycle.stopped. Safe to call more than once.
func (d *PaneDriver) Stop() {
	d.mu.Lock()
	if d.stopped || !d.started {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	cancel := d.cancel
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	d.wg.Wait()

	// Fire-and-forget on a fresh context: parent ctx is likely cancelled
	// by the time Stop is called, but the hub should still record the
	// stop. A 3s budget is plenty and bounds shutdown latency.
	shutCtx, cancelShut := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelShut()
	_ = d.Poster.PostAgentEvent(shutCtx, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "stopped", "mode": "M4"})
}

func (d *PaneDriver) loop(ctx context.Context) {
	defer d.wg.Done()
	t := time.NewTicker(d.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.tick(ctx)
		}
	}
}

func (d *PaneDriver) tick(ctx context.Context) {
	cap, err := d.Capture(ctx, d.PaneID)
	if err != nil {
		// Transient tmux errors (pane gone, server restarted) shouldn't
		// kill the driver; the reconcile loop will stop us if the agent
		// has genuinely exited.
		d.Log.Debug("pane capture failed", "agent", d.AgentID, "err", err)
		return
	}
	delta := diffAppend(d.lastCap, cap)
	d.lastCap = cap
	if delta == "" {
		return
	}
	if err := d.Poster.PostAgentEvent(ctx, d.AgentID, "text", "agent",
		map[string]any{"text": delta}); err != nil {
		d.Log.Debug("post agent event failed", "agent", d.AgentID, "err", err)
	}
}

// diffAppend returns the new suffix of `next` that isn't already in `prev`.
// The common case is strict append (prev is a prefix of next); we also
// handle scrollback trimming by falling back to the longest-suffix-of-prev
// that is a prefix of next. A full redraw or unrelated capture means no
// overlap — we emit the full `next` so the app at least sees *something*.
func diffAppend(prev, next string) string {
	if prev == "" {
		return next
	}
	if strings.HasPrefix(next, prev) {
		return next[len(prev):]
	}
	// Find the longest suffix of prev that is a prefix of next. Scrollback
	// dropped the early lines of prev, so only the tail still matches.
	// Start from a reasonable cap — scanning the whole buffer every tick
	// is O(N²) worst-case; 8 KiB covers one terminal-screen of history.
	start := 0
	if len(prev) > 8192 {
		start = len(prev) - 8192
	}
	for i := start; i < len(prev); i++ {
		tail := prev[i:]
		if strings.HasPrefix(next, tail) {
			return next[len(tail):]
		}
	}
	return next
}

func tmuxCapturePane(ctx context.Context, paneID string) (string, error) {
	// -p writes to stdout; -J joins wrapped lines; -S - -E - would grab
	// full scrollback but we want the screen only for diffing liveness.
	out, err := exec.CommandContext(ctx, "tmux",
		"capture-pane", "-p", "-J", "-t", paneID).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
