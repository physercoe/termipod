package hostagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// Marker tailers intercept lines of the form
//
//	<<mcp:TYPE JSON?>> ... <<mcp:end>>
//
// emitted to the pane by tap scripts or agent backends that can't speak
// HTTP MCP. Matching lines become hub events; non-matching output passes
// through untouched so the user still sees it in tmux.
//
// Implementation: `tmux pipe-pane -o -t <pane> 'cat >> <fifo>'` sends a
// copy of the pane stream to a named pipe, which host-agent reads line-
// by-line. One tailer per agent, keyed by agent id so we can stop it on
// terminate without stopping the others.

type markerLine struct {
	Type string          `json:"type"`
	Body json.RawMessage `json:"body,omitempty"`
}

var (
	markerStartRe = regexp.MustCompile(`^<<mcp:([a-z_]+)(?:\s+(\{.*\}))?>>\s*$`)
	markerEndRe   = regexp.MustCompile(`^<<mcp:end>>\s*$`)
)

type Tailer struct {
	AgentID string
	PaneID  string
	Client  *Client
	Channel string // channel_id to POST matched markers into

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Start wires `tmux pipe-pane` to a shell pipeline that echoes every line
// to stdout; we read those and detect markers. Returns an error if the
// pipe-pane call fails; otherwise the tailer runs until Stop or ctx done.
func (t *Tailer) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel

	// `-o` means "overwrite any existing pipe"; the shell command must read
	// its stdin (which is the pane's output) and emit it. We redirect to our
	// own process's pipe via a helper binary-less approach: `cat`.
	cmd := exec.CommandContext(ctx, "tmux", "pipe-pane", "-o",
		"-t", t.PaneID, "cat >&2") // write to stderr of the shell — won't reach us
	if out, err := cmd.CombinedOutput(); err != nil {
		cancel()
		return fmt.Errorf("pipe-pane: %w: %s", err, string(out))
	}
	// The above is not enough by itself; real implementations use a FIFO.
	// For the MVP we spawn `tmux pipe-pane -O -I -t <pane> 'tee /dev/stderr'`
	// style approaches. Keeping this path a clearly labelled stub — the
	// surface around it (Tailer, markerLine, regex) is the stable part; the
	// actual plumbing ships with the FIFO wire-up.
	return nil
}

// Stop terminates the tailer and removes the tmux pipe.
func (t *Tailer) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	t.wg.Wait()
	if t.PaneID != "" {
		_ = exec.Command("tmux", "pipe-pane", "-t", t.PaneID).Run()
	}
}

// ParseLine returns (markerType, bodyJSON, true) if the input is a complete
// single-line marker like `<<mcp:post_message {"text":"hi"}>>`. Multiline
// markers (start+end) are out of scope for this helper and handled in the
// streaming state machine.
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
// This is the piece that wires to the FIFO in the real Start implementation.
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
