package claudecode

import (
	"context"
	"os"
	"path/filepath"
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Replay: expect the 2 seeded events with replay:true.
	got := waitForN(t, p, 2, 1*time.Second)
	if got[0].kind != "user_input" {
		t.Errorf("event 0 kind = %q, want user_input", got[0].kind)
	}
	if got[0].payload["replay"] != true {
		t.Errorf("event 0 replay = %v, want true", got[0].payload["replay"])
	}
	if got[1].kind != "text" {
		t.Errorf("event 1 kind = %q, want text", got[1].kind)
	}

	// Live: append a new line.
	appendJSONL(t, jsonl,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
	)
	got = waitForN(t, p, 3, 2*time.Second)
	if got[2].kind != "tool_call" {
		t.Errorf("live event kind = %q, want tool_call", got[2].kind)
	}
	// Replay flag should still apply (TailMode == StartFromBeginning
	// keeps it on for the whole session per W2d's simple-but-correct
	// rule; W2f's state machine refines this once a turn boundary is
	// observed.)
	if got[2].payload["replay"] != true {
		t.Errorf("live event replay = %v; (current W2d simplification: stays true)", got[2].payload["replay"])
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

	// Append a fresh line: should arrive without a replay tag.
	appendJSONL(t, jsonl,
		`{"type":"user","message":{"content":"new"}}`,
	)
	got := waitForN(t, p, 1, 2*time.Second)
	if got[0].kind != "user_input" {
		t.Errorf("kind = %q, want user_input", got[0].kind)
	}
	if got[0].payload["replay"] != nil {
		t.Errorf("StartFromEnd event has replay = %v; want unset", got[0].payload["replay"])
	}
}

func TestAdapter_Start_WaitsForSessionFile(t *testing.T) {
	cwd := "/home/test/proj3"
	homeDir, projectDir := makeFakeHome(t, cwd)

	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "ag", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir
	a.SessionWaitTimeout = 2 * time.Second

	// Drop the session file after a short delay; Start should
	// pick it up rather than failing.
	jsonl := filepath.Join(projectDir, "sess.jsonl")
	go func() {
		time.Sleep(150 * time.Millisecond)
		writeJSONL(t, jsonl,
			`{"type":"user","message":{"content":"delayed"}}`,
		)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("Start returned in %v before file appeared; expected ≥100ms wait", elapsed)
	}

	got := waitForN(t, p, 1, 2*time.Second)
	if got[0].payload["text"] != "delayed" {
		t.Errorf("text = %v, want delayed", got[0].payload["text"])
	}
}

func TestAdapter_Start_TimesOutWhenSessionNeverAppears(t *testing.T) {
	cwd := "/home/test/proj4"
	homeDir, _ := makeFakeHome(t, cwd)

	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "ag", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir
	a.SessionWaitTimeout = 100 * time.Millisecond

	err := a.Start(context.Background())
	if err == nil {
		a.Stop()
		t.Fatal("Start returned nil when session file never appeared")
	}
}

func TestAdapter_StopDrainsRunLoop(t *testing.T) {
	cwd := "/home/test/proj5"
	homeDir, projectDir := makeFakeHome(t, cwd)
	jsonl := filepath.Join(projectDir, "sess.jsonl")
	writeJSONL(t, jsonl,
		`{"type":"user","message":{"content":"one"}}`,
	)

	p := &capturingPoster{}
	a, _ := NewAdapter(Config{AgentID: "ag", Workdir: cwd, Poster: p})
	a.HomeDir = homeDir

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
	appendJSONL(t, jsonl, `{"type":"user","message":{"content":"after stop"}}`)
	time.Sleep(150 * time.Millisecond)
	if got := len(p.snapshot()); got != before {
		t.Errorf("events grew after Stop: %d → %d", before, got)
	}

	// Idempotent.
	a.Stop()
}
