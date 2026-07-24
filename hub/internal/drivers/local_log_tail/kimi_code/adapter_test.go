package kimi_code

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedEvent struct {
	Kind     string
	Producer string
	Payload  map[string]any
}

type fakePoster struct {
	mu     sync.Mutex
	events []capturedEvent
}

func (p *fakePoster) PostAgentEvent(_ context.Context, _, kind, producer string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pm, _ := payload.(map[string]any)
	p.events = append(p.events, capturedEvent{Kind: kind, Producer: producer, Payload: pm})
	return nil
}

func (p *fakePoster) snapshot() []capturedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]capturedEvent(nil), p.events...)
}

// waitFor polls the poster until pred matches or the deadline fires.
func (p *fakePoster) waitFor(t *testing.T, pred func([]capturedEvent) bool, timeout time.Duration) []capturedEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := p.snapshot()
		if pred(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for events; got %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// seedKimiStore builds a fake kimi store mapping cwd → wd_test_* with
// the session's agents/ tree created lazily by the caller.
func seedKimiStore(t *testing.T, cwd string) (storeHome, sessionDir string) {
	t.Helper()
	storeHome = t.TempDir()
	ws := fmt.Sprintf(`{"version":1,"workspaces":{"wd_test_000000000001":{"root":%q,"name":"test"}},"deleted_workspace_ids":[]}`, cwd)
	if err := os.WriteFile(filepath.Join(storeHome, "workspaces.json"), []byte(ws), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionDir = filepath.Join(storeHome, "sessions", "wd_test_000000000001", "session_test_abc")
	return storeHome, sessionDir
}

func writeWire(t *testing.T, sessionDir, agentID string, lines []string) string {
	t.Helper()
	dir := filepath.Join(sessionDir, "agents", agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wire.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func appendWire(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

const testMetadata = `{"type":"metadata","protocol_version":"1.4","created_at":1}`

// End-to-end: the adapter resolves the session for the workdir (via
// workspaces.json), tails the main wire as it grows, and posts mapped
// events — including events appended AFTER the tail started (live
// follow), which is the whole point of the mode.
func TestAdapter_StartResolvesTailsAndPosts(t *testing.T) {
	cwd := t.TempDir()
	store, sessionDir := seedKimiStore(t, cwd)

	poster := &fakePoster{}
	a, err := NewAdapter(Config{
		AgentID:   "agent-t1",
		Workdir:   cwd,
		StoreHome: store,
		Engine:    "kimi-code-ts",
		Poster:    poster,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// kimi "launches" a beat after the adapter starts: session dir +
	// main wire appear, then grow.
	time.Sleep(100 * time.Millisecond)
	wirePath := writeWire(t, sessionDir, "main", []string{
		testMetadata,
		`{"type":"turn.prompt","input":[{"type":"text","text":"hi"}],"origin":{"kind":"user"},"time":2}`,
		`{"type":"context.append_loop_event","event":{"type":"content.part","uuid":"u-1","turnId":"0","step":1,"part":{"type":"text","text":"hello back"}},"time":3}`,
	})

	got := poster.waitFor(t, func(evs []capturedEvent) bool {
		var sawInit, sawText bool
		for _, e := range evs {
			if e.Kind == "session.init" && e.Payload["session_id"] == "session_test_abc" {
				sawInit = true
			}
			if e.Kind == "text" && e.Payload["text"] == "hello back" {
				sawText = true
			}
		}
		return sawInit && sawText
	}, 3*time.Second)

	// session.init carries the launch-time engine identity fields.
	for _, e := range got {
		if e.Kind == "session.init" {
			if e.Payload["engine"] != "kimi-code-ts" || e.Payload["cwd"] != cwd {
				t.Fatalf("session.init payload = %+v", e.Payload)
			}
		}
	}

	// Live follow: lines appended after the tail is up are mapped too.
	appendWire(t, wirePath,
		`{"type":"usage.record","model":"kimi-code/k3","usage":{"inputOther":10,"output":5,"inputCacheRead":90,"inputCacheCreation":0},"usageScope":"turn","time":4}`)
	poster.waitFor(t, func(evs []capturedEvent) bool {
		for _, e := range evs {
			if e.Kind == "usage" && e.Payload["input_tokens"] == 10 {
				return true
			}
		}
		return false
	}, 3*time.Second)
}

// Subagent wire files: the adapter discovers agents/agent-N/wire.jsonl
// mid-session, reads the parent edge from state.json, and stamps every
// mapped event with the subagent provenance fields.
func TestAdapter_SubagentWireEventsAreTagged(t *testing.T) {
	cwd := t.TempDir()
	store, sessionDir := seedKimiStore(t, cwd)

	poster := &fakePoster{}
	a, err := NewAdapter(Config{
		AgentID:   "agent-t2",
		Workdir:   cwd,
		StoreHome: store,
		Poster:    poster,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	time.Sleep(100 * time.Millisecond)
	writeWire(t, sessionDir, "main", []string{testMetadata})

	// Subagent appears mid-session (state.json first this time, then
	// the wire dir — kimi's real order can be either).
	state := `{"agents":{"main":{"type":"main","parentAgentId":null},"agent-3":{"type":"sub","parentAgentId":"main"}}}`
	if err := os.WriteFile(filepath.Join(sessionDir, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWire(t, sessionDir, "agent-3", []string{
		testMetadata,
		`{"type":"context.append_loop_event","event":{"type":"tool.call","uuid":"tool_s1","toolCallId":"tool_s1","name":"Glob","args":{"pattern":"*.go"}},"time":2}`,
	})

	poster.waitFor(t, func(evs []capturedEvent) bool {
		for _, e := range evs {
			if e.Kind == "tool_call" && e.Payload["tool_use_id"] == "tool_s1" {
				return e.Payload["subagent"] == true &&
					e.Payload["kimi_agent_id"] == "agent-3" &&
					e.Payload["parent_agent_id"] == "main"
			}
		}
		return false
	}, 3*time.Second)
}

// Protocol gate at runtime: a wire whose metadata carries an
// unsupported version disables the structured tail for that agent and
// surfaces a system notice; the pane (and thus the agent) stays live.
func TestAdapter_UnsupportedProtocolDisablesTail(t *testing.T) {
	cwd := t.TempDir()
	store, sessionDir := seedKimiStore(t, cwd)

	poster := &fakePoster{}
	a, err := NewAdapter(Config{
		AgentID:   "agent-t3",
		Workdir:   cwd,
		StoreHome: store,
		Poster:    poster,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	time.Sleep(100 * time.Millisecond)
	writeWire(t, sessionDir, "main", []string{
		`{"type":"metadata","protocol_version":"9","created_at":1}`,
		`{"type":"context.append_loop_event","event":{"type":"content.part","uuid":"u-9","part":{"type":"text","text":"should never surface"}},"time":2}`,
	})

	got := poster.waitFor(t, func(evs []capturedEvent) bool {
		for _, e := range evs {
			if e.Kind == "system" && e.Payload["subtype"] == "kimi_wire_unsupported_protocol" {
				return true
			}
		}
		return false
	}, 3*time.Second)

	// No content events must have leaked past the gate.
	for _, e := range got {
		if e.Kind == "text" || e.Kind == "tool_call" || e.Kind == "usage" {
			t.Fatalf("event leaked past the protocol gate: %+v", e)
		}
	}
}

// A missing store (kimi never ran here / KIMI_CODE_HOME moved) degrades
// to the noteFailure path: a system notice, pane still live.
func TestAdapter_StoreNotResolvableNotesFailure(t *testing.T) {
	cwd := t.TempDir()
	store := t.TempDir() // empty: no workspaces.json, no sessions

	poster := &fakePoster{}
	a, err := NewAdapter(Config{
		AgentID:   "agent-t4",
		Workdir:   cwd,
		StoreHome: store,
		Poster:    poster,
	})
	if err != nil {
		t.Fatal(err)
	}
	a.SessionWaitTimeout = 300 * time.Millisecond
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	poster.waitFor(t, func(evs []capturedEvent) bool {
		for _, e := range evs {
			if e.Kind == "system" {
				text, _ := e.Payload["text"].(string)
				if strings.Contains(text, "kimi wire tail unavailable") {
					return true
				}
			}
		}
		return false
	}, 3*time.Second)
}

func TestAdapter_HandleInputRequiresPane(t *testing.T) {
	a, err := NewAdapter(Config{AgentID: "a1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": "hi"}); err == nil {
		t.Fatal("want error when PaneID unset; got nil")
	}
}

// recordingRunner captures exec calls for the send-keys tests.
type recordingRunner struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil, nil
}

func (r *recordingRunner) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

// Multi-line text input must be ONE paste (set-buffer + paste-buffer -r
// + Enter) so a multi-line envelope doesn't fragment into separate
// submissions (mirrors the antigravity adapter's regression test).
func TestAdapter_TextInputMultiLineUsesPasteBuffer(t *testing.T) {
	runner := &recordingRunner{}
	a, err := NewAdapter(Config{AgentID: "a1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%42"
	a.CmdRunner = runner

	body := "line one\nline two\nline three"
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": body}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := runner.snapshot()
	if len(calls) != 3 {
		t.Fatalf("want 3 tmux calls (set-buffer, paste-buffer, Enter); got %v", calls)
	}
	joined := strings.Join(calls[0], " ") + " | " + strings.Join(calls[1], " ") + " | " + strings.Join(calls[2], " ")
	if !strings.Contains(joined, "set-buffer") || !strings.Contains(joined, "paste-buffer") ||
		!strings.Contains(joined, "-r") || !strings.Contains(joined, "Enter") {
		t.Fatalf("unexpected tmux sequence: %s", joined)
	}
}

// Single-line short text takes the cheap send-keys -l + Enter path.
func TestAdapter_TextInputSingleLine(t *testing.T) {
	runner := &recordingRunner{}
	a, err := NewAdapter(Config{AgentID: "a1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%42"
	a.CmdRunner = runner

	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": "hello"}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := runner.snapshot()
	if len(calls) != 2 {
		t.Fatalf("want 2 tmux calls (send-keys -l, Enter); got %v", calls)
	}
	flat := strings.Join(calls[0], " ")
	if !strings.Contains(flat, "-l") || !strings.Contains(flat, "hello") {
		t.Fatalf("first call = %v", calls[0])
	}
}

// pick_option drives kimi's arrow-nav permission menu: N×Down + Enter.
func TestAdapter_PickOptionSendsDownEnter(t *testing.T) {
	runner := &recordingRunner{}
	a, err := NewAdapter(Config{AgentID: "a1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%42"
	a.CmdRunner = runner

	if err := a.HandleInput(context.Background(), "pick_option", map[string]any{"index": float64(2)}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	calls := runner.snapshot()
	if len(calls) != 3 { // Down, Down, Enter
		t.Fatalf("want 3 tmux calls; got %v", calls)
	}
	if got := calls[2][len(calls[2])-1]; got != "Enter" {
		t.Fatalf("last key = %q, want Enter", got)
	}
}

// Fixture pin: replay the sanitized real main-agent capture through a
// full adapter (resolver + tailer + mapper + poster) to prove the
// pipeline — not just the mapper — handles kimi-code 0.28.1 wire.
func TestAdapter_RealFixtureEndToEnd(t *testing.T) {
	cwd := t.TempDir()
	store, sessionDir := seedKimiStore(t, cwd)

	fixture, err := os.ReadFile("testdata/wire_main.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	// Pre-seed the session + wire BEFORE Start so the resolver finds it
	// immediately (within the launchTime skew).
	if err := os.MkdirAll(filepath.Join(sessionDir, "agents", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "agents", "main", "wire.jsonl"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	poster := &fakePoster{}
	a, err := NewAdapter(Config{
		AgentID:   "agent-t5",
		Workdir:   cwd,
		StoreHome: store,
		Engine:    "kimi-code-ts",
		Poster:    poster,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// The fixture ends with a turn-result + usage; wait for both, then
	// count the full histogram.
	got := poster.waitFor(t, func(evs []capturedEvent) bool {
		var sawTurnResult, sawUsage bool
		for _, e := range evs {
			if e.Kind == "turn.result" {
				sawTurnResult = true
			}
			if e.Kind == "usage" {
				sawUsage = true
			}
		}
		return sawTurnResult && sawUsage
	}, 3*time.Second)

	counts := map[string]int{}
	for _, e := range got {
		counts[e.Kind]++
	}
	want := map[string]int{
		"tool_call":       6,
		"tool_result":     4,
		"plan":            3,
		"usage":           5,
		"approval_result": 2,
		"text":            1,
		"thought":         1,
		"turn.result":     1,
	}
	for kind, n := range want {
		if counts[kind] != n {
			t.Errorf("kind %s = %d, want %d (all: %v)", kind, counts[kind], n, counts)
		}
	}
	// Nothing may be stamped subagent on the main wire.
	for _, e := range got {
		if _, ok := e.Payload["subagent"]; ok {
			t.Errorf("main-wire event stamped subagent: %+v", e)
		}
	}
}
