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
	"fmt"
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
	// SendKeys lets tests inject a fake for tmux send-keys; nil defaults
	// to the real tmuxSendKeys below. Used by Input (P1.8) for M4 input.
	SendKeys PaneSendKeysFunc
	// Tmux is the general tmux seam, needed for the buffer subcommands the
	// multi-line paste path uses. nil defaults to a real exec — except when
	// SendKeys is stubbed, where it refuses instead (see tmuxFn).
	Tmux PaneTmuxFunc
	// PasteSettle is the pause between paste-buffer and Enter. 0 takes
	// defaultPasteSettle; set a tiny value in tests so they don't sleep.
	PasteSettle time.Duration
	// Workdir is the agent's cwd, when the runner could derive one. A
	// pane is a terminal — there is no image channel into it at all —
	// so an annotation image is materialized here and its path named in
	// the text (desktop-ui-context D5 §3.5). Empty means "no workdir
	// derived": the image is reported as dropped rather than written
	// somewhere the agent cannot see.
	Workdir string

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	lastCap string
}

// PaneSendKeysFunc is the tmux send-keys seam. `literal` requests -l
// (literal bytes, no keyname translation); false lets tmux interpret the
// string (so "Enter"/"C-c" behave as usual).
type PaneSendKeysFunc func(ctx context.Context, paneID, text string, literal bool) error

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

// tmuxSendKeys is the default PaneSendKeysFunc. `literal` maps to -l; we
// always send to the pane target verbatim so a pane_id with special
// characters still addresses unambiguously.
func tmuxSendKeys(ctx context.Context, paneID, text string, literal bool) error {
	args := []string{"send-keys", "-t", paneID}
	if literal {
		args = append(args, "-l")
	}
	args = append(args, text)
	return exec.CommandContext(ctx, "tmux", args...).Run()
}

// PaneTmuxFunc is the general tmux seam — the buffer subcommands the paste
// path needs cannot be expressed through PaneSendKeysFunc's (text, literal)
// shape. nil defaults to a real `tmux` exec.
type PaneTmuxFunc func(ctx context.Context, args ...string) error

func tmuxRun(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, "tmux", args...).Run()
}

// paneInputInlineMax is the body size below which input takes the cheap
// send-keys path. Matches the adapters' threshold so the generic driver and
// the per-engine adapters make the same call for the same body.
const paneInputInlineMax = 512

// defaultPasteSettle is the pause between pasting a body and sending Enter,
// so the TUI has ingested the text before it is asked to submit. herdr waits
// 300 ms on its own paste path; the per-engine adapters send Enter
// immediately, which is a race we have not seen bite them but which the
// generic path — serving TUIs nobody wrote an adapter for — should not take.
const defaultPasteSettle = 300 * time.Millisecond

// tmuxFn resolves the general tmux seam.
//
// The nil-SendKeys case is the ordinary one. When a harness has stubbed
// SendKeys but NOT Tmux, we refuse rather than falling through to a real
// `tmux` exec: a test that thinks it is isolated must not reach the
// machine's tmux server, and on a developer box that server is the one the
// human is sitting in.
func (d *PaneDriver) tmuxFn() PaneTmuxFunc {
	if d.Tmux != nil {
		return d.Tmux
	}
	if d.SendKeys != nil {
		return func(_ context.Context, args ...string) error {
			return fmt.Errorf("pane driver: tmux %q needed but only the SendKeys seam was stubbed; "+
				"set PaneDriver.Tmux too", strings.Join(args, " "))
		}
	}
	return tmuxRun
}

// sendBody delivers one body to the pane and submits it.
//
// Short single-line bodies keep the original two-call shape
// (`send-keys -l <body>`, `send-keys Enter`) — unchanged, and what the
// existing tests pin.
//
// Anything multi-line or long goes through tmux's named-buffer paste, the
// path the claude-code / kimi / antigravity adapters already use:
//
//	tmux set-buffer   -b <name> <body>
//	tmux paste-buffer -b <name> -d -r -p -t <pane>
//	tmux send-keys              -t <pane> Enter
//
// Before this, the generic path sent a multi-line body with a single
// `send-keys -l`, so the pane's line discipline submitted at the FIRST
// newline and the remaining lines landed as separate turns — the same defect
// the adapters were fixed for, still live for every engine without one.
//
// `-r` is load-bearing: without it tmux rewrites each LF in the buffer to a
// CR keystroke, which is exactly the multiple-submissions bug in another
// costume. `-d` drops the buffer so concurrent inputs don't stack. `-p`
// brackets the paste **only if the application asked for bracketed paste**
// (tmux(1): "paste bracket control codes are inserted around the buffer if
// the application has requested bracketed paste mode") — so a TUI that
// requested it stops line-editing the pasted body, and one that did not is
// unaffected.
func (d *PaneDriver) sendBody(ctx context.Context, send PaneSendKeysFunc, body string) error {
	if len(body) <= paneInputInlineMax && !strings.ContainsAny(body, "\n\r") {
		if err := send(ctx, d.PaneID, body, true); err != nil {
			return err
		}
		return send(ctx, d.PaneID, "Enter", false)
	}

	run := d.tmuxFn()
	// Buffer names are [A-Za-z0-9_-]; pane ids arrive as `%NN`.
	bufName := "paneinput_" + strings.NewReplacer("%", "", ":", "_", ".", "_").Replace(d.PaneID)
	if err := run(ctx, "set-buffer", "-b", bufName, body); err != nil {
		return fmt.Errorf("pane driver: set-buffer: %w", err)
	}
	if err := run(ctx, "paste-buffer", "-b", bufName, "-d", "-r", "-p", "-t", d.PaneID); err != nil {
		// Best-effort cleanup so a failed paste doesn't leave a buffer for
		// the next call to inherit.
		_ = run(ctx, "delete-buffer", "-b", bufName)
		return fmt.Errorf("pane driver: paste-buffer: %w", err)
	}
	if settle := d.pasteSettle(); settle > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(settle):
		}
	}
	return send(ctx, d.PaneID, "Enter", false)
}

func (d *PaneDriver) pasteSettle() time.Duration {
	if d.PasteSettle != 0 {
		return d.PasteSettle
	}
	return defaultPasteSettle
}

// Input implements Inputter for M4. Translations:
//   - text:     send-keys -l <body>; send-keys Enter
//   - cancel:   send-keys C-c  (tmux interprets the keyname)
//   - approval: send-keys -l <decision[: note]>; send-keys Enter
//     (M4 has no way to correlate a request_id back to the pane; the
//     operator is expected to use approvals only while the agent is
//     prompting for one)
//   - attach:   not meaningful for a pane — surfaced as text marker so
//     the user sees that an attachment was requested but has to act on
//     it manually.
func (d *PaneDriver) Input(ctx context.Context, kind string, payload map[string]any) error {
	if d.PaneID == "" {
		return fmt.Errorf("pane driver: no pane wired")
	}
	send := d.SendKeys
	if send == nil {
		send = tmuxSendKeys
	}
	switch kind {
	case "text":
		body, _ := payload["body"].(string)
		// D5 §3.5: the pane cannot receive an image (the kimi TUI's
		// clipboard paste is a LOCAL feature, not something send-keys
		// can drive), so an annotation crop is written into the agent's
		// workdir and the path rides the text it CAN read.
		if images := extractImageInputs(payload); len(images) > 0 {
			paths, merr := materializeImageInputs(d.Workdir, images, time.Now())
			body = annotationNote(body, paths)
			if merr != nil || len(paths) != len(images) {
				_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent",
					map[string]any{
						"reason":   "a tmux pane has no image input channel and the workdir fallback failed — attached images dropped",
						"dropped":  len(images) - len(paths),
						"engine":   "pane",
						"fallback": fallbackReason(merr),
					})
			}
		}
		if body == "" {
			return fmt.Errorf("pane driver: text input missing body")
		}
		return d.sendBody(ctx, send, body)
	case "cancel":
		return send(ctx, d.PaneID, "C-c", false)
	case "approval":
		decision, _ := payload["decision"].(string)
		note, _ := payload["note"].(string)
		if decision == "" {
			return fmt.Errorf("pane driver: approval missing decision")
		}
		body := decision
		if note != "" {
			body = decision + ": " + note
		}
		// An approval note is operator-authored free text and can carry a
		// newline as easily as a prompt can, so it takes the same path.
		return d.sendBody(ctx, send, body)
	case "attach":
		docID, _ := payload["document_id"].(string)
		if docID == "" {
			return fmt.Errorf("pane driver: attach missing document_id")
		}
		// Leave the operator a visible marker instead of silently
		// no-oping; they can paste the referenced content themselves.
		text := "# attach requested: document_id=" + docID
		return d.sendBody(ctx, send, text)
	default:
		return fmt.Errorf("pane driver: unsupported input kind %q", kind)
	}
}
