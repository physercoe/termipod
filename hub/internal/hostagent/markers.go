package hostagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

// Marker tailers intercept lines of the form
//
//	<<mcp:TYPE JSON?>>
//
// emitted to the pane by tap scripts or agent backends that can't speak
// HTTP MCP. Matching lines become hub events; non-matching output passes
// through untouched so the user still sees it in tmux.
//
// Implementation: per-agent FIFO under /tmp, `tmux pipe-pane -o -t <pane>
// 'cat >> <fifo>'` sends a copy of the pane stream into it, and a goroutine
// reads the FIFO line-by-line with Scan. One tailer per agent, keyed by
// agent id so Stop can close just that pane's pipe without disturbing
// others.

var (
	markerStartRe = regexp.MustCompile(`^<<mcp:([a-z_]+)(?:\s+(\{.*\}))?>>\s*$`)
	markerEndRe   = regexp.MustCompile(`^<<mcp:end>>\s*$`)
)

// Tailer watches one pane's output for `<<mcp:…>>` markers and forwards
// matched ones as hub events. It owns the named FIFO under /tmp and the
// goroutine reading from it; Stop cleans both up.
type Tailer struct {
	AgentID   string
	PaneID    string
	ProjectID string
	ChannelID string
	Client    *Client
	Log       *slog.Logger

	fifoPath string
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// Start creates the FIFO, tells tmux to pipe pane output into it, and spawns
// a goroutine that scans for markers. Returns on success once both the FIFO
// and the tmux pipe are live. If any step fails the FIFO (if created) is
// removed and any pipe-pane is cancelled.
func (t *Tailer) Start(parent context.Context) error {
	if t.PaneID == "" || t.ChannelID == "" || t.ProjectID == "" {
		// Nothing to wire — treat as a no-op so callers can construct a
		// Tailer for every spawn and only those with channel binding do work.
		return nil
	}
	if t.Log == nil {
		t.Log = slog.Default()
	}

	t.fifoPath = filepath.Join(os.TempDir(),
		fmt.Sprintf("hub-marker-%s.fifo", t.AgentID))
	// Remove any stale FIFO from a previous process crash; ignore errors
	// (ENOENT is fine; anything else surfaces on Mkfifo below).
	_ = os.Remove(t.fifoPath)
	if err := syscall.Mkfifo(t.fifoPath, 0o600); err != nil {
		return fmt.Errorf("mkfifo %s: %w", t.fifoPath, err)
	}

	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel

	// Ask tmux to tee the pane into our FIFO. `-o` replaces any existing
	// pipe; the shell command inherits pane output on stdin.
	pipeCmd := fmt.Sprintf("cat >> %s", shellQuote(t.fifoPath))
	if out, err := exec.CommandContext(ctx, "tmux", "pipe-pane", "-o",
		"-t", t.PaneID, pipeCmd).CombinedOutput(); err != nil {
		cancel()
		_ = os.Remove(t.fifoPath)
		return fmt.Errorf("pipe-pane: %w: %s", err, strings.TrimSpace(string(out)))
	}

	t.wg.Add(1)
	go t.readLoop(ctx)
	t.Log.Info("marker tailer started",
		"agent", t.AgentID, "pane", t.PaneID, "fifo", t.fifoPath)
	return nil
}

// readLoop opens the FIFO for reading (this blocks until tmux opens the
// write end) and scans line-by-line until ctx is cancelled or EOF.
// We reopen on EOF because `tmux pipe-pane` can briefly close the write
// end and reopen it when the pane reattaches; losing the reader would
// drop markers emitted after reattach.
func (t *Tailer) readLoop(ctx context.Context) {
	defer t.wg.Done()
	for ctx.Err() == nil {
		// O_RDONLY on a FIFO blocks until a writer opens it; that's fine —
		// ctx cancellation interrupts by closing the file via the defer below.
		f, err := os.OpenFile(t.fifoPath, os.O_RDONLY, 0)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			t.Log.Warn("open fifo failed", "agent", t.AgentID, "err", err)
			return
		}

		// Bridge context cancel → close(f) so a blocked Scan returns.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = f.Close()
			case <-done:
			}
		}()

		if err := Scan(f, t.handleMarker); err != nil && ctx.Err() == nil {
			t.Log.Debug("scan err", "agent", t.AgentID, "err", err)
		}
		close(done)
		_ = f.Close()
	}
}

// handleMarker dispatches one parsed marker. Only kinds we know how to
// translate get forwarded; unknown kinds are logged-and-dropped so noisy
// future tap scripts can't flood the event feed.
func (t *Tailer) handleMarker(kind string, body []byte) {
	switch kind {
	case "post_message":
		var p struct {
			ChannelID string `json:"channel_id"`
			Text      string `json:"text"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			t.Log.Debug("post_message marker: bad json", "err", err)
			return
		}
		// Marker may omit channel_id to mean "the agent's bound channel".
		ch := p.ChannelID
		if ch == "" {
			ch = t.ChannelID
		}
		if err := t.Client.PostEvent(context.Background(), t.ProjectID, ch, EventIn{
			Type:   "message",
			FromID: t.AgentID,
			Parts:  []EventInPart{{Kind: "text", Text: p.Text}},
		}); err != nil {
			t.Log.Warn("forward post_message failed", "err", err)
		}
	case "ping":
		// Lightweight liveness: emit a zero-payload event. Useful for tap
		// scripts that want to prove the pipeline is alive without a body.
		if err := t.Client.PostEvent(context.Background(), t.ProjectID, t.ChannelID, EventIn{
			Type:   "ping",
			FromID: t.AgentID,
		}); err != nil {
			t.Log.Debug("forward ping failed", "err", err)
		}
	case "attach":
		t.handleAttach(body)
	default:
		t.Log.Debug("unhandled marker kind", "kind", kind)
	}
}

// handleAttach reads a file from the host's filesystem, uploads it to the
// hub as a content-addressed blob, and emits an `attach` event whose single
// part carries a BlobRef. Large files are rejected upstream (25 MiB cap in
// handleUploadBlob); we surface the error as a log line so the tap script
// author can see it without the marker stream going silent.
//
// The file path MUST be absolute (or relative to the tmux pane's cwd, which
// is not deterministic from here) — resolution happens in the pane's shell
// where the marker was emitted; we just read what the tap scripts tells us.
func (t *Tailer) handleAttach(body []byte) {
	var p struct {
		Path      string `json:"path"`
		Mime      string `json:"mime"`
		ChannelID string `json:"channel_id"`
		Note      string `json:"note"` // optional text part alongside the file
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Path == "" {
		t.Log.Debug("attach marker: bad json", "err", err)
		return
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		t.Log.Warn("attach: read failed", "path", p.Path, "err", err)
		return
	}
	out, err := t.Client.UploadBlob(context.Background(), data, p.Mime)
	if err != nil {
		t.Log.Warn("attach: upload failed", "path", p.Path, "err", err)
		return
	}
	ch := p.ChannelID
	if ch == "" {
		ch = t.ChannelID
	}
	parts := []EventInPart{{
		Kind: "file",
		File: &BlobRefWire{
			URI:  "hub-blob://" + out.SHA256,
			Mime: out.Mime,
			Size: out.Size,
		},
	}}
	if p.Note != "" {
		parts = append(parts, EventInPart{Kind: "text", Text: p.Note})
	}
	if err := t.Client.PostEvent(context.Background(), t.ProjectID, ch, EventIn{
		Type:   "attach",
		FromID: t.AgentID,
		Parts:  parts,
	}); err != nil {
		t.Log.Warn("attach: post event failed", "err", err)
	}
}

// Stop cancels the read loop, removes the tmux pipe, and unlinks the FIFO.
// Safe to call more than once.
func (t *Tailer) Stop() {
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	t.wg.Wait()
	if t.PaneID != "" {
		// Unpipe: `pipe-pane` with no command toggles off.
		_ = exec.Command("tmux", "pipe-pane", "-t", t.PaneID).Run()
	}
	if t.fifoPath != "" {
		_ = os.Remove(t.fifoPath)
		t.fifoPath = ""
	}
}

// ParseLine returns (markerType, bodyJSON, true) if the input is a complete
// single-line marker like `<<mcp:post_message {"text":"hi"}>>`. Multiline
// markers (start+end) are out of scope here.
func ParseLine(line string) (string, []byte, bool) {
	line = strings.TrimRight(line, "\r\n")
	m := markerStartRe.FindStringSubmatch(line)
	if m == nil {
		return "", nil, false
	}
	return m[1], []byte(m[2]), true
}

// Scan consumes r line-by-line and invokes onMarker for every matched line.
// Returns when r is exhausted or an unrecoverable read error occurs.
func Scan(r io.Reader, onMarker func(kind string, body []byte)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if kind, body, ok := ParseLine(sc.Text()); ok {
			onMarker(kind, body)
		}
	}
	return sc.Err()
}

// shellQuote produces a single-quoted POSIX shell literal; any embedded
// single quotes are escaped the standard way. Used to build the pipe-pane
// command that hands a FIFO path to `cat`.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// markerEndRe is reserved for a future multi-line marker state machine and
// kept exported in case external tooling wants to detect block terminators.
var _ = markerEndRe
