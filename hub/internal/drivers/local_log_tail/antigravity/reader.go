package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Step is one transcript entry surfaced by the Reader: its stable
// step_index key, its lifecycle status (RUNNING|DONE), and the full raw
// JSON line so the mapper can parse the type-specific shape. Bytes
// excludes the trailing newline.
type Step struct {
	Index  int
	Status string
	Bytes  []byte
}

// stepHead is the minimal envelope the Reader parses to drive its
// diffing; the mapper re-parses Bytes for the type-specific payload.
type stepHead struct {
	StepIndex int    `json:"step_index"`
	Status    string `json:"status"`
}

// Reader follows agy's transcript_full.jsonl, which — unlike claude-code's
// append-only session JSONL — is a *rewritten snapshot*: every poll may
// rewrite existing lines in place as their step transitions RUNNING→DONE,
// and step_index (not byte offset) is the stable identity. So the shared
// tail-from-offset Tailer cannot be used; this reader re-reads the whole
// file on change and diffs by step_index (last-writer-wins), emitting a
// Step on first sight and again when a step's status changes (the
// RUNNING→DONE finalisation). That bounds emissions to ≤2 per step.
//
// Torn reads (a partial line caught mid-rewrite) parse-fail for that line
// and are simply skipped that round; the next poll re-reads the now-whole
// line. One Reader = one transcript file; teardown is ctx-cancel or Stop().
type Reader struct {
	Path      string
	PollEvery time.Duration

	// SkipExisting drains the current file contents into the `emitted`
	// map without sending them downstream, so only steps that appear
	// after Start are emitted. The adapter sets this on resume paths
	// (a re-spawn that landed in an existing transcript via the
	// engine_session_id resume cursor) to avoid re-emitting the full
	// historical conversation as if it were live — that was the W11
	// v1.0.645 smoke incident's surface effect (mobile feed replayed
	// every prior step on a fresh spawn that mis-resolved to an old
	// brain dir via the lazy `last_conversations.json` cache).
	//
	// Off by default: fresh spawns want every step emitted, starting
	// from step 0 (the user's first USER_INPUT).
	SkipExisting bool

	cancel context.CancelFunc
	done   chan struct{}
}

const defaultPollEvery = 150 * time.Millisecond

// Start opens nothing eagerly (the file is re-opened each poll) and
// returns a receive-only channel of Steps. The channel closes on Stop,
// ctx cancellation, or an unrecoverable error. Unlike the claude-code
// Tailer there is no seek mode — a snapshot has no "tail from end".
func (r *Reader) Start(parent context.Context) (<-chan Step, error) {
	if r.Path == "" {
		return nil, fmt.Errorf("antigravity reader: empty Path")
	}
	if r.PollEvery <= 0 {
		r.PollEvery = defaultPollEvery
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.done = make(chan struct{})
	out := make(chan Step, 64)
	go r.loop(ctx, out)
	return out, nil
}

// Stop cancels the reader and waits for the goroutine to exit. Safe to
// call multiple times. The Steps channel is closed before Stop returns.
func (r *Reader) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.done != nil {
		<-r.done
	}
}

func (r *Reader) loop(ctx context.Context, out chan<- Step) {
	defer close(out)
	defer close(r.done)

	// emitted maps step_index → last-emitted status. A step is emitted
	// on first sight and re-emitted whenever its status differs from the
	// last emit (RUNNING→DONE). Bounded re-emits; superseding is left to
	// downstream (events carry step_index so a consumer can coalesce).
	emitted := make(map[int]string)
	var lastMod time.Time
	var lastSize int64

	// On resume, mark every currently-present step as already-emitted
	// without sending anything downstream — incremental polling then
	// only picks up steps that appear after Start.
	if r.SkipExisting {
		r.prime(emitted, &lastMod, &lastSize)
	}

	// Read once immediately so a transcript that already exists doesn't
	// wait a full poll interval before its first emit.
	r.scan(ctx, out, emitted, &lastMod, &lastSize)

	t := time.NewTicker(r.PollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !r.scan(ctx, out, emitted, &lastMod, &lastSize) {
				return
			}
		}
	}
}

// prime drains the currently-present file into `emitted` without
// emitting anything downstream. Used by SkipExisting to silence the
// historical replay on resume. lastMod/lastSize are seeded so a
// subsequent unchanged scan is a fast no-op.
func (r *Reader) prime(emitted map[int]string, lastMod *time.Time, lastSize *int64) {
	st, err := os.Stat(r.Path)
	if err != nil {
		return
	}
	*lastMod = st.ModTime()
	*lastSize = st.Size()
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return
	}
	for _, raw := range bytes.Split(data, []byte("\n")) {
		raw = bytes.TrimRight(raw, "\r")
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var head stepHead
		if err := json.Unmarshal(raw, &head); err != nil {
			continue
		}
		emitted[head.StepIndex] = head.Status
	}
}

// scan re-reads the file if it changed since the last read and emits any
// new-or-status-changed steps. Returns false only when the caller should
// stop (ctx cancelled mid-emit). A missing file or read error is
// transient (agy may be rewriting) → returns true so the loop keeps
// polling.
func (r *Reader) scan(ctx context.Context, out chan<- Step, emitted map[int]string, lastMod *time.Time, lastSize *int64) bool {
	st, err := os.Stat(r.Path)
	if err != nil {
		return true // file briefly absent during rewrite; retry next tick
	}
	if st.ModTime().Equal(*lastMod) && st.Size() == *lastSize {
		return true // unchanged since last scan
	}
	*lastMod = st.ModTime()
	*lastSize = st.Size()

	data, err := os.ReadFile(r.Path)
	if err != nil {
		return true
	}
	for _, raw := range bytes.Split(data, []byte("\n")) {
		raw = bytes.TrimRight(raw, "\r")
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var head stepHead
		if err := json.Unmarshal(raw, &head); err != nil {
			// Torn snapshot line — skip; the next scan re-reads it whole.
			continue
		}
		prev, seen := emitted[head.StepIndex]
		if seen && prev == head.Status {
			continue
		}
		// Copy the slice: bytes.Split aliases the backing array, which
		// the next ReadFile reuses.
		b := make([]byte, len(raw))
		copy(b, raw)
		select {
		case out <- Step{Index: head.StepIndex, Status: head.Status, Bytes: b}:
			emitted[head.StepIndex] = head.Status
		case <-ctx.Done():
			return false
		}
	}
	return true
}
