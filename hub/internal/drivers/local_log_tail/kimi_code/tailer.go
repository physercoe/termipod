package kimi_code

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// StartMode controls where the Tailer begins reading. Mirrors the
// claude-code tailer's contract — kimi's wire.jsonl is append-only in
// the same way (one JSON object per line, LF-terminated).
type StartMode int

const (
	// StartFromBeginning reads the entire file from byte 0 before
	// switching to live-tail mode. Fresh-spawn attach uses this: the
	// wire file is created after the spawn, so byte 0 IS the start of
	// this agent's session.
	StartFromBeginning StartMode = iota
	// StartFromEnd skips existing content and starts emitting only
	// lines appended after the tailer opens the file.
	StartFromEnd
)

// Line is one complete (LF-terminated) line read from the tailed
// file. Bytes excludes the trailing newline. Offset is the underlying
// file's read position when the line was emitted — bufio's readahead
// means it can run past the line's end; informational only (the
// adapter doesn't consume it).
type Line struct {
	Bytes  []byte
	Offset int64
}

// defaultPollEvery is the cadence at which the tailer polls for new
// content once it has reached EOF. Matches the claude tailer (100ms).
const defaultPollEvery = 100 * time.Millisecond

// Tailer follows an append-only JSONL file and surfaces each complete
// line on a channel. A partial trailing line (kimi mid-write — the
// append/flush cadence of wire.jsonl is unverified, per the P4 wedge)
// is held by bufio until its newline arrives, so a torn write never
// reaches the mapper. Truncation (file size shrinks across polls) is
// defensively re-opened from byte 0, mirroring the claude tailer.
//
// One Tailer = one wire file. The adapter starts one per agent
// (main + each subagent).
type Tailer struct {
	Path      string
	Mode      StartMode
	PollEvery time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// Start opens the file, seeks per Mode, and returns a receive-only
// channel of Lines. The channel is closed when the tailer stops
// (Stop call, ctx cancellation, or unrecoverable I/O error).
func (t *Tailer) Start(parent context.Context) (<-chan Line, error) {
	if t.PollEvery <= 0 {
		t.PollEvery = defaultPollEvery
	}
	f, err := os.Open(t.Path)
	if err != nil {
		return nil, fmt.Errorf("tailer open %s: %w", t.Path, err)
	}
	switch t.Mode {
	case StartFromBeginning:
		// Already at offset 0 after Open.
	case StartFromEnd:
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("tailer seek-end %s: %w", t.Path, err)
		}
	default:
		_ = f.Close()
		return nil, fmt.Errorf("tailer: unknown StartMode %d", t.Mode)
	}

	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel
	t.done = make(chan struct{})
	out := make(chan Line, 64)

	go t.loop(ctx, f, out)
	return out, nil
}

// Stop cancels the tailer and waits for the goroutine to exit. Safe
// to call multiple times. The Lines channel returned by Start is
// closed before Stop returns.
func (t *Tailer) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	if t.done != nil {
		<-t.done
	}
}

// loop owns the file handle and drives the read/poll cycle.
func (t *Tailer) loop(ctx context.Context, f *os.File, out chan<- Line) {
	defer close(out)
	defer close(t.done)
	defer f.Close()

	br := bufio.NewReader(f)

	// pending holds the unconsumed bytes of a partial trailing line
	// (kimi mid-write). bufio.ReadBytes CONSUMES the partial bytes it
	// returns alongside io.EOF, so the partial must be stashed here and
	// prepended when the rest of the line arrives — otherwise the
	// mapper would receive a headless fragment. (wire.jsonl's
	// append/flush cadence is unverified per the P4 wedge, so this
	// path is load-bearing, not hypothetical.)
	var pending []byte

	for {
		line, err := br.ReadBytes('\n')
		switch {
		case err == nil:
			if len(pending) > 0 {
				line = append(pending, line...)
				pending = nil
			}
			payload := trimTrailingNewline(line)
			off, _ := f.Seek(0, io.SeekCurrent)
			select {
			case out <- Line{Bytes: payload, Offset: off}:
			case <-ctx.Done():
				return
			}
			continue
		case err == io.EOF:
			// Partial line OR fully at EOF. Stash any partial bytes —
			// ReadBytes has CONSUMED them from the reader (the returned
			// slice is a copy, but the next read continues after them),
			// so they must be prepended when the rest of the line
			// arrives. Sleep, then continue; the completed line is
			// emitted whole once its newline lands.
			if len(line) > 0 {
				pending = append(pending, line...)
			}
			if !t.waitOrReopen(ctx, f, &br, &pending) {
				return
			}
			continue
		default:
			// Unrecoverable read error — exit (channel close signals
			// the adapter's run loop).
			return
		}
	}
}

// waitOrReopen sleeps PollEvery, checks for ctx cancel, and re-opens
// the file if it's been truncated (current pos > size). Returns
// false if the tailer should exit. A truncation invalidates any stashed
// partial (the bytes it belonged to may be gone), so pending is reset.
func (t *Tailer) waitOrReopen(ctx context.Context, f *os.File, brp **bufio.Reader, pending *[]byte) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(t.PollEvery):
	}
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	if st.Size() < pos {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return false
		}
		*brp = bufio.NewReader(f)
		*pending = nil
	}
	return true
}

// trimTrailingNewline returns b with at most one trailing \n and
// optional preceding \r removed.
func trimTrailingNewline(b []byte) []byte {
	n := len(b)
	if n > 0 && b[n-1] == '\n' {
		n--
		if n > 0 && b[n-1] == '\r' {
			n--
		}
	}
	out := make([]byte, n)
	copy(out, b[:n])
	return out
}
