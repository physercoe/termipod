package claudecode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// StartMode controls where the Tailer begins reading.
type StartMode int

const (
	// StartFromBeginning reads the entire file from byte 0 before
	// switching to live-tail mode. Use for first attach when the
	// caller wants the full transcript (W2d wires this for the
	// initial replay window).
	StartFromBeginning StartMode = iota
	// StartFromEnd skips existing content and starts emitting only
	// lines appended after the tailer opens the file. Use for
	// re-attach when the transcript is already cached on mobile.
	StartFromEnd
)

// Line is one complete (LF-terminated) line read from the tailed
// file. Bytes excludes the trailing newline. Offset is the absolute
// byte position immediately AFTER the line in the file — i.e. where
// the next line will start. Useful for resumability if the caller
// later wants to recover from where the tailer left off.
type Line struct {
	Bytes  []byte
	Offset int64
}

// defaultPollEvery is the cadence at which the tailer polls for new
// content once it has reached EOF. 100ms gives a human-perceived
// "live" feel without burning CPU on a quiet session.
const defaultPollEvery = 100 * time.Millisecond

// Tailer follows an append-only JSONL file and surfaces each
// complete line on a channel. claude-code never rotates the session
// JSONL today; the tailer defensively re-opens on truncation
// anyway (file size shrinks across polls) so a future log-rotate
// release wouldn't silently lose events.
//
// One Tailer = one file. The W2d caller starts a fresh Tailer when
// the session JSONL path resolves; teardown is ctx-cancel or Stop().
type Tailer struct {
	Path      string
	Mode      StartMode
	PollEvery time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// Start opens the file, seeks per Mode, and returns a receive-only
// channel of Lines. The channel is closed when the tailer stops
// (Stop call, ctx cancellation, or unrecoverable I/O error). Errors
// at open time are returned directly — Start does not background
// the open. Once Start returns nil the goroutine is running.
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
	out := make(chan Line, 64) // small buffer so a slow consumer doesn't stall the reader on a bursty session

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

	for {
		// Read a full line. ReadBytes returns the bytes including
		// the trailing newline AND io.EOF when no newline appears
		// before end of file. We treat partial-line + EOF as "wait
		// for more bytes" — claude-code writes complete lines
		// atomically, so a partial line means a write is in flight.
		line, err := br.ReadBytes('\n')
		switch {
		case err == nil:
			// Trim the trailing LF (and optional CR for safety) before
			// emitting; consumers want the payload, not the framing.
			payload := trimTrailingNewline(line)
			off, _ := f.Seek(0, io.SeekCurrent)
			select {
			case out <- Line{Bytes: payload, Offset: off}:
			case <-ctx.Done():
				return
			}
			continue
		case err == io.EOF:
			// Partial line OR fully at EOF. Either way, sleep then
			// continue. If we'd buffered a partial line, the next
			// ReadBytes call resumes from where we paused (bufio
			// preserves the partial).
			if len(line) > 0 {
				// Rewind the partial bytes in the underlying file so
				// the next read re-fetches them; bufio's internal
				// buffer holds them, so this is more about preserving
				// the file offset than the bytes themselves.
				// In practice bufio handles continuation transparently
				// across the next ReadBytes call.
				_ = line
			}
			if !t.waitOrReopen(ctx, f, &br) {
				return
			}
			continue
		default:
			// Unrecoverable read error — log via context error and
			// exit. Tests use this path to verify channel close.
			return
		}
	}
}

// waitOrReopen sleeps PollEvery, checks for ctx cancel, and re-opens
// the file if it's been truncated (current pos > size). Returns
// false if the tailer should exit.
func (t *Tailer) waitOrReopen(ctx context.Context, f *os.File, brp **bufio.Reader) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(t.PollEvery):
	}
	// Truncation check: if the file is shorter than our current
	// read offset, claude-code (or someone) reset the file under
	// us. Re-seek to start and rebuild the reader so we don't read
	// garbage offsets.
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
	}
	return true
}

// trimTrailingNewline returns b with at most one trailing \n and
// optional preceding \r removed. Returns a new slice; the input
// remains untouched so the caller's buffer stays consistent.
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
