package antigravity

import (
	"context"
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

// Start must resolve the conversation id via the brain-dir-since-launch
// signal, wait for the transcript, and post mapped events for each step
// — the end-to-end Adapter path against a faked agy store. As of
// v1.0.646 the legacy last_conversations.json cache is no longer
// consulted (it mis-resolved fresh spawns against stale entries from
// prior runs), so this test seeds a brain dir AFTER Start to mimic
// agy minting a conversation in response to a user message.
func TestAdapter_StartResolvesAndPosts(t *testing.T) {
	home := t.TempDir()
	workdir := "/home/ubuntu/agytest"
	convID := "conv-abc"

	poster := &fakePoster{}
	a, err := NewAdapter(Config{
		AgentID: "ag1",
		Workdir: workdir,
		HomeDir: home,
		Poster:  poster,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	// Simulate the user sending the first message and agy minting the
	// conversation: create the brain dir + transcript a beat after
	// Start so the resolver's launchTime predates the mtime.
	time.Sleep(50 * time.Millisecond)
	tdir := filepath.Dir(TranscriptPath(home, convID))
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, TranscriptPath(home, convID),
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"hi"}`+"\n"+
			`{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","content":"hello back"}`+"\n")
	// Bump the brain-dir mtime to "now" so newestBrainSince picks it up
	// even on filesystems that round mtime to the second.
	brainDir := filepath.Join(StoreDir(home), "brain", convID)
	now := time.Now()
	_ = os.Chtimes(brainDir, now, now)

	// Start is now async (v1.0.642 fix — agy mints its conversationId
	// only after the first user message, so blocking Start created an
	// unbreakable pending→running deadlock). Poll for both the
	// session.init cursor event (W8) + the text event from the
	// PLANNER_RESPONSE AND the ConversationID field — they all land via
	// the background resolveAndRun goroutine.
	deadline := time.After(2 * time.Second)
	for {
		got := poster.snapshot()
		var sawInit, sawText bool
		for _, e := range got {
			if e.Kind == "session.init" && e.Payload["session_id"] == convID {
				sawInit = true
			}
			if e.Kind == "text" && e.Payload["text"] == "hello back" {
				sawText = true
			}
		}
		gotConvID := a.ConversationID
		if sawInit && sawText && gotConvID == convID {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("want session.init(%s)+text+ConversationID=%s; got events=%+v conv=%q",
				convID, convID, got, gotConvID)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestAdapter_HandleInput_RequiresPane(t *testing.T) {
	a, err := NewAdapter(Config{AgentID: "ag1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.HandleInput(context.Background(), "text", map[string]any{"body": "hi"}); err == nil {
		t.Fatal("want error when PaneID unset; got nil")
	}
}

// Multi-line text input must be ONE paste (set-buffer + paste-buffer +
// Enter), not the per-line send-keys-Enter loop that fragmented the
// ADR-032 envelope into separate user submissions on the W11 smoke.
func TestAdapter_TextInput_MultiLineUsesPasteBuffer(t *testing.T) {
	fr := &fakeRunner{}
	a, err := NewAdapter(Config{AgentID: "ag1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%7"
	a.CmdRunner = fr

	body := "[directive from the principal]\nhi\n\nReply in this chat when you have a result."
	if err := a.HandleInput(context.Background(), "text",
		map[string]any{"body": body}); err != nil {
		t.Fatal(err)
	}
	// Expected tmux invocations (in order):
	//   tmux set-buffer  -b agyinput_7 <body>
	//   tmux paste-buffer -b agyinput_7 -d -t %7
	//   tmux send-keys -t %7 Enter
	if len(fr.cmds) != 3 {
		t.Fatalf("want 3 tmux calls; got %d (%+v)", len(fr.cmds), fr.cmds)
	}
	want := []string{"set-buffer", "paste-buffer", "send-keys"}
	for i, w := range want {
		if !strings.Contains(fr.cmds[i], w) {
			t.Errorf("call %d = %q; want substring %q", i, fr.cmds[i], w)
		}
	}
	// The body must reach set-buffer verbatim — no per-line split.
	if !strings.Contains(fr.cmds[0], body) {
		t.Errorf("body not preserved in set-buffer call: %q", fr.cmds[0])
	}
	// Only ONE Enter must be sent.
	enterCount := 0
	for _, c := range fr.cmds {
		if strings.Contains(c, "send-keys") && strings.Contains(c, "Enter") {
			enterCount++
		}
	}
	if enterCount != 1 {
		t.Errorf("want exactly 1 Enter; got %d (%+v)", enterCount, fr.cmds)
	}
}

// Single-line short text keeps the cheap send-keys -l + Enter fast path.
func TestAdapter_TextInput_SingleLineUsesSendKeys(t *testing.T) {
	fr := &fakeRunner{}
	a, err := NewAdapter(Config{AgentID: "ag1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%9"
	a.CmdRunner = fr

	if err := a.HandleInput(context.Background(), "text",
		map[string]any{"body": "hi"}); err != nil {
		t.Fatal(err)
	}
	// Two calls: send-keys -l hi, then send-keys Enter. No set-buffer.
	if len(fr.cmds) != 2 {
		t.Fatalf("want 2 calls (-l then Enter); got %d (%+v)", len(fr.cmds), fr.cmds)
	}
	for _, c := range fr.cmds {
		if strings.Contains(c, "set-buffer") || strings.Contains(c, "paste-buffer") {
			t.Errorf("single-line should not use buffer path; got %q", c)
		}
	}
}

// pick_option arrow-navigates Down×index then Enter (agy's menu UX).
func TestAdapter_PickOption_ArrowNav(t *testing.T) {
	fr := &fakeRunner{}
	a, err := NewAdapter(Config{AgentID: "ag1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%3"
	a.CmdRunner = fr
	if err := a.HandleInput(context.Background(), "pick_option", map[string]any{"index": float64(2)}); err != nil {
		t.Fatal(err)
	}
	want := []string{"Down", "Down", "Enter"}
	if len(fr.keys) != len(want) {
		t.Fatalf("keys = %v; want %v", fr.keys, want)
	}
	for i := range want {
		if fr.keys[i] != want[i] {
			t.Fatalf("keys[%d] = %q; want %q (%v)", i, fr.keys[i], want[i], fr.keys)
		}
	}
}

// fakeRunner records both the final tmux send-keys argument (`.keys`,
// used by pick_option-style tests that only care about the trailing
// key/literal) and the full argv of every invocation (`.cmds`, used by
// the multi-line input tests that need to verify the set-buffer +
// paste-buffer + Enter sequence end to end).
type fakeRunner struct {
	keys []string // trailing arg of each `tmux send-keys` call
	cmds []string // full "name arg arg arg" of every Run
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
	if name == "tmux" && len(args) >= 1 && args[0] == "send-keys" {
		f.keys = append(f.keys, args[len(args)-1])
	}
	return nil, nil
}

func TestCapturePane(t *testing.T) {
	fr := &captureRunner{out: "  > 1. Allow\n    2. Deny\n"}
	got, err := CapturePane(context.Background(), "%4", fr)
	if err != nil {
		t.Fatal(err)
	}
	if got != fr.out {
		t.Fatalf("got %q; want %q", got, fr.out)
	}
	if fr.lastArgs != "capture-pane -p -t %4" {
		t.Fatalf("args = %q; want capture-pane -p -t %%4", fr.lastArgs)
	}
	if _, err := CapturePane(context.Background(), "", fr); err == nil {
		t.Fatal("want error for empty pane id")
	}
}

type captureRunner struct {
	out      string
	lastArgs string
}

func (c *captureRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	c.lastArgs = ""
	for i, a := range args {
		if i > 0 {
			c.lastArgs += " "
		}
		c.lastArgs += a
	}
	return []byte(c.out), nil
}
