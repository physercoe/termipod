package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// capturingPoster records every (kind, producer, payload) the
// adapter emits. Tests assert against snapshot().
type capturingPoster struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	kind, producer string
	payload        map[string]any
}

func (p *capturingPoster) PostAgentEvent(_ context.Context, _, kind, producer string, payload any) error {
	pm, _ := payload.(map[string]any)
	cp := make(map[string]any, len(pm))
	for k, v := range pm {
		cp[k] = v
	}
	p.mu.Lock()
	p.events = append(p.events, capturedEvent{kind: kind, producer: producer, payload: cp})
	p.mu.Unlock()
	return nil
}

func (p *capturingPoster) snapshot() []capturedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]capturedEvent, len(p.events))
	copy(out, p.events)
	return out
}

// makeFakeHome creates a temp home dir with a .claude/projects/<slug>
// subtree mirroring the on-disk layout the adapter expects. Returns
// the home dir + the per-cwd project dir for direct file writes.
func makeFakeHome(t *testing.T, cwd string) (homeDir, projectDir string) {
	t.Helper()
	homeDir = t.TempDir()
	projectDir = ProjectDirFor(homeDir, cwd)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return
}

func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	var body string
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func appendJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
}

func waitForN(t *testing.T, p *capturingPoster, n int, timeout time.Duration) []capturedEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := p.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited %v for %d events; got %d", timeout, n, len(p.snapshot()))
	return nil
}

func TestAdapter_Start_ReplaysExistingThenLive(t *testing.T) {
	cwd := "/home/test/proj"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess-abc.jsonl")
	// First user line is a string — v1.0.663 drops it (the hub's
	// `input.text` event is the canonical user-text record).
	// Assistant text is the first surviving event of the replay.
	writeJSONL(t, jsonl,
		`{"type":"user","message":{"content":"hi"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello back"}]}}`,
	)

	p := &capturingPoster{}
	a, err := NewAdapter(Config{
		AgentID: "agent-1",
		Workdir: cwd,
		Poster:  p,
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.HomeDir = homeDir
	// Tests need to see the seeded JSONL (mtime is older than the
	// `time.Now()` cutoff NewAdapter installs). Zero = no cutoff.
	a.SessionCutoff = time.Time{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Replay: first surviving event is the assistant text. (The
	// user-string line is dropped per v1.0.663.)
	got := waitForN(t, p, 1, 1*time.Second)
	if got[0].kind != "text" {
		t.Errorf("event 0 kind = %q, want text", got[0].kind)
	}
	// v1.0.666: no replay tag is added by the M4 adapter. The pre-
	// v1.0.666 W2d rule stamped replay:true on every StartFromBeginning
	// event, which mobile's text/thought replay filter then nuked. M4
	// doesn't need the tag (SSE seq-gating + id-dedup already prevent
	// double-rendering across cold-open + live), so the assertion is
	// inverted: replay MUST be absent.
	if _, has := got[0].payload["replay"]; has {
		t.Errorf("event 0 carries replay tag; v1.0.666 expects absent: %v", got[0].payload)
	}

	// Live: append a new line.
	appendJSONL(t, jsonl,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
	)
	got = waitForN(t, p, 2, 2*time.Second)
	if got[1].kind != "tool_call" {
		t.Errorf("live event kind = %q, want tool_call", got[1].kind)
	}
	if _, has := got[1].payload["replay"]; has {
		t.Errorf("live event carries replay tag; v1.0.666 expects absent: %v", got[1].payload)
	}
}

func TestAdapter_Start_StartFromEndSkipsExisting(t *testing.T) {
	cwd := "/home/test/proj2"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess.jsonl")
	writeJSONL(t, jsonl,
		`{"type":"user","message":{"content":"old"}}`,
	)

	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "ag", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir
	a.TailMode = StartFromEnd
	a.SessionCutoff = time.Time{} // see v1.0.661 cutoff

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Small grace window for the goroutine to settle at EOF.
	time.Sleep(150 * time.Millisecond)
	if got := p.snapshot(); len(got) != 0 {
		t.Errorf("StartFromEnd posted %d events for prior content; want 0: %+v", len(got), got)
	}

	// Append a fresh line that DOES survive the v1.0.663 user-string
	// drop: an assistant text. Must arrive without a replay tag.
	appendJSONL(t, jsonl,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"new"}]}}`,
	)
	got := waitForN(t, p, 1, 2*time.Second)
	if got[0].kind != "text" {
		t.Errorf("kind = %q, want text", got[0].kind)
	}
	if got[0].payload["replay"] != nil {
		t.Errorf("StartFromEnd event has replay = %v; want unset", got[0].payload["replay"])
	}
}

// Start MUST return immediately (the resolver runs in a background
// goroutine — v1.0.660 async refactor). The session file appearing
// later must still be picked up by the goroutine and produce events.
func TestAdapter_Start_AsyncWaitsForSessionFile(t *testing.T) {
	cwd := "/home/test/proj3"
	homeDir, projectDir := makeFakeHome(t, cwd)

	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "ag", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir
	a.SessionWaitTimeout = 2 * time.Second

	// Drop the session file after a short delay; the resolver
	// goroutine should pick it up and emit the event. Assistant text
	// is the smallest line that produces a surfaced event after the
	// v1.0.663 user-string drop.
	jsonl := filepath.Join(projectDir, "sess.jsonl")
	go func() {
		time.Sleep(150 * time.Millisecond)
		writeJSONL(t, jsonl,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"delayed"}]}}`,
		)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	// Start is async post-v1.0.660 — it must return promptly
	// (well under the 150ms file-write delay) so the host-runner
	// launch path doesn't block waiting for claude to produce a
	// session.
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("Start blocked for %v; expected immediate return (async resolver)", elapsed)
	}

	got := waitForN(t, p, 1, 2*time.Second)
	if got[0].payload["text"] != "delayed" {
		t.Errorf("text = %v, want delayed", got[0].payload["text"])
	}
}

// Pre-v1.0.660 Start returned an error when WaitForSession timed
// out, which blocked the W7 launch path and marked the agent as
// failed even though the tmux pane was perfectly healthy (claude
// was just sitting on its welcome screen waiting for input). The
// async refactor turns the failure into a SOFT event: Start still
// returns nil, but the resolver goroutine posts a `system` notice
// with "tail unavailable" text so mobile shows the half-broken
// state.
func TestAdapter_Start_TimesOutPostsSoftFailureEvent(t *testing.T) {
	cwd := "/home/test/proj4"
	homeDir, _ := makeFakeHome(t, cwd)

	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "ag", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir
	a.SessionWaitTimeout = 50 * time.Millisecond

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error on async-mode timeout: %v", err)
	}
	defer a.Stop()

	// The goroutine's noteFailure should land within a couple
	// timeout multiples.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range p.snapshot() {
			if ev.kind != "system" {
				continue
			}
			text, _ := ev.payload["text"].(string)
			if strings.Contains(text, "tail unavailable") {
				return // pass
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("noteFailure event never posted; saw events = %+v", p.snapshot())
}

func TestAdapter_StopDrainsRunLoop(t *testing.T) {
	cwd := "/home/test/proj5"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess.jsonl")
	// Use assistant text — user-string is dropped post-v1.0.663
	// (the hub's input.text is the canonical user-text source).
	writeJSONL(t, jsonl,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"one"}]}}`,
	)

	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "ag", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir
	a.SessionCutoff = time.Time{} // see seeded JSONL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = waitForN(t, p, 1, 1*time.Second)

	stopped := make(chan struct{})
	go func() {
		a.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s")
	}

	// After Stop, further appends should NOT produce events.
	before := len(p.snapshot())
	appendJSONL(t, jsonl,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"after stop"}]}}`)
	time.Sleep(150 * time.Millisecond)
	if got := len(p.snapshot()); got != before {
		t.Errorf("events grew after Stop: %d → %d", before, got)
	}

	// Idempotent.
	a.Stop()
}

// v1.0.667 — the M4 adapter synthesises a session.init event on the
// first usage frame carrying a model, since on-disk JSONL has no
// equivalent of M2 stream-json's `init` frame. Without this mobile's
// AppBar chip stays blank for every M4 spawn. Asserts:
//   - session.init lands BEFORE the corresponding usage event so
//     the chip can render in the same build pass
//   - payload carries engine="claude-code", the model from the
//     assistant frame, and the workdir
//   - subsequent usage frames in the same session DO NOT re-emit
//     session.init (would be benign but adds noise)
func TestAdapter_SynthesisesSessionInitFromFirstUsage(t *testing.T) {
	cwd := "/home/test/proj-sinit"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess.jsonl")
	writeJSONL(t, jsonl,
		`{"type":"assistant","message":{"model":"claude-opus-4-7","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"cache_read_input_tokens":100}}}`,
		`{"type":"assistant","message":{"model":"claude-opus-4-7","content":[{"type":"text","text":"more"}],"usage":{"input_tokens":2,"cache_read_input_tokens":200}}}`,
	)

	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "ag", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir
	a.SessionCutoff = time.Time{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Both assistant frames produce text + usage. With one
	// synthetic session.init, that's 5 events total.
	got := waitForN(t, p, 5, 2*time.Second)

	// Find the session.init.
	var initIdx = -1
	initCount := 0
	for i, ev := range got {
		if ev.kind == "session.init" {
			initCount++
			if initIdx < 0 {
				initIdx = i
			}
		}
	}
	if initCount != 1 {
		t.Fatalf("want exactly 1 session.init, got %d in %+v", initCount, got)
	}
	init := got[initIdx]
	if init.payload["engine"] != "claude-code" {
		t.Errorf("engine = %v, want claude-code", init.payload["engine"])
	}
	if init.payload["model"] != "claude-opus-4-7" {
		t.Errorf("model = %v, want claude-opus-4-7", init.payload["model"])
	}
	if init.payload["cwd"] != cwd {
		t.Errorf("cwd = %v, want %s", init.payload["cwd"], cwd)
	}
	// session.init must precede the FIRST usage event so mobile's
	// build-pass picks it up alongside the chip-driving values.
	for i := 0; i < initIdx; i++ {
		if got[i].kind == "usage" {
			t.Errorf("usage at index %d landed BEFORE session.init at %d: %+v",
				i, initIdx, got)
		}
	}
}
