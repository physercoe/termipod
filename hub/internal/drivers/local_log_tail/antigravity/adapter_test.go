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
		// Read under a.mu — resolveAndRun writes ConversationID under the
		// same lock (adapter.go), so a bare read here races it (-race).
		a.mu.Lock()
		gotConvID := a.ConversationID
		a.mu.Unlock()
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
	//   tmux paste-buffer -b agyinput_7 -d -r -t %7
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
	// paste-buffer MUST carry `-r` so tmux doesn't translate the
	// envelope's internal LF bytes into CR (Enter) keystrokes and split
	// the paste into N separate user submissions. Post-v1.0.650 smoke
	// caught this — the 4-line ADR-032 envelope arrived as two USER_INPUT
	// events five minutes apart on the live box, and the first one (just
	// `[directive from the principal]` with no body) drove agy into a
	// 357-step self-invented work cascade.
	if !strings.Contains(fr.cmds[1], " -r ") && !strings.HasSuffix(fr.cmds[1], " -r") {
		t.Errorf("paste-buffer missing -r (LF→CR suppression): %q", fr.cmds[1])
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

// attention_reply is the hub's fan-out of `/decide` on an attention
// item the agent raised (request_approval / select / help_request).
// The W11 smoke caught it as "unsupported input kind 'attention_reply'"
// in the host-runner log — the adapter rejected it and the agent
// never saw the principal's reply. v1.0.650: handled via inputText
// after rendering the structured payload through formatAttentionReplyText.
func TestAdapter_AttentionReply_RoutesAsText(t *testing.T) {
	fr := &fakeRunner{}
	a, err := NewAdapter(Config{AgentID: "ag1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%11"
	a.CmdRunner = fr

	if err := a.HandleInput(context.Background(), "attention_reply", map[string]any{
		"kind":       "approval_request",
		"request_id": "01KS9TNGHT8GB0WDNF44MZXXYS",
		"decision":   "approve",
		"reason":     "looks good",
	}); err != nil {
		t.Fatalf("HandleInput attention_reply: %v", err)
	}
	// Expected: at least one send-keys call carrying the rendered
	// reply text ("[reply to approval_request 01KS9TNG] Approved.
	// Reason: looks good") then Enter.
	if len(fr.cmds) < 2 {
		t.Fatalf("want >=2 tmux calls; got %d (%+v)", len(fr.cmds), fr.cmds)
	}
	joined := strings.Join(fr.cmds, " | ")
	if !strings.Contains(joined, "Approved") {
		t.Errorf("rendered reply lost the decision text: %q", joined)
	}
	if !strings.Contains(joined, "01KS9TNG") {
		t.Errorf("rendered reply lost the request-id prefix: %q", joined)
	}
}

// An empty/malformed payload (no decision / no body / unknown kind)
// must surface as an error so the input router posts a system event
// rather than send-keys'ing a confusing empty line.
func TestAdapter_AttentionReply_EmptyPayloadErrors(t *testing.T) {
	a, err := NewAdapter(Config{AgentID: "ag1", Workdir: "/x", Poster: &fakePoster{}})
	if err != nil {
		t.Fatal(err)
	}
	a.PaneID = "%12"
	a.CmdRunner = &fakeRunner{}
	if err := a.HandleInput(context.Background(), "attention_reply", map[string]any{}); err == nil {
		t.Error("expected error on empty attention_reply payload")
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

// v1.0.718 (G3 — session-details parity): the launch-time session.init
// payload populates engine/version/cwd/permission_mode so the mobile
// session-details sheet doesn't render blank rows for antigravity
// stewards. Mirrors the codex v1.0.715 contract +
// AppServerDriver.emitSessionInit.
func TestAdapter_BuildLaunchTimeSessionInit_PopulatesAllFields(t *testing.T) {
	a := &Adapter{Config: Config{
		Engine:         "antigravity",
		Workdir:        "/home/op/agytest",
		PermissionMode: "dangerously-skip-permissions",
		EngineVersion:  "1.0.2",
	}}
	got := a.buildLaunchTimeSessionInit("conv-abc")

	want := map[string]any{
		"session_id":      "conv-abc",
		"engine":          "antigravity",
		"version":         "1.0.2",
		"cwd":             "/home/op/agytest",
		"permission_mode": "dangerously-skip-permissions",
	}
	if len(got) != len(want) {
		t.Fatalf("payload has %d keys; want %d (%v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("payload[%q] = %v; want %v", k, got[k], v)
		}
	}
}

// Section-gating contract: empty fields drop out so mobile's `isEmpty`
// rendering hides absent rows. session_id is always present (it's the
// resume cursor; ADR-035 D8). Mirrors the same contract on
// AppServerDriver.emitSessionInit (v1.0.715) — same upstream consumer
// shape, identical "blank > wrong" discipline.
func TestAdapter_BuildLaunchTimeSessionInit_SkipsEmptyFields(t *testing.T) {
	a := &Adapter{Config: Config{
		// All optional fields zero — only session_id should land.
		Workdir: "",
	}}
	got := a.buildLaunchTimeSessionInit("conv-only")

	if len(got) != 1 {
		t.Fatalf("expected only session_id; got %v", got)
	}
	if got["session_id"] != "conv-only" {
		t.Errorf("session_id = %v; want conv-only", got["session_id"])
	}
	for _, k := range []string{"engine", "version", "cwd", "permission_mode"} {
		if _, present := got[k]; present {
			t.Errorf("unexpected key %q in payload: %v", k, got)
		}
	}
}

// Default-mode (no --dangerously-skip-permissions) renders the
// flag-derived "interactive" string so mobile colour-maps to green
// (safest posture — every tool gate raises the arrow-nav menu the
// operator must answer). Per the antigravity statusLine research
// (docs/discussions/antigravity-statusline-research.md), the
// flag-derived string is preferred over a translated alias for grep
// affinity on the hub side.
func TestAdapter_BuildLaunchTimeSessionInit_PermissionModeInteractive(t *testing.T) {
	a := &Adapter{Config: Config{
		Engine:         "antigravity",
		Workdir:        "/tmp/x",
		PermissionMode: "interactive",
		EngineVersion:  "1.0.2",
	}}
	got := a.buildLaunchTimeSessionInit("c1")

	if got["permission_mode"] != "interactive" {
		t.Errorf("permission_mode = %v; want \"interactive\"", got["permission_mode"])
	}
}

// v1.0.719 (G1+G2 — statusLine pipeline): OnStatusLine caches the
// payload so a future in-process consumer (session.init field
// overrides, etc.) can read it. The gateway invokes the sink AFTER
// posting the verbatim AgentEvent, so mobile chips already get the
// snapshot regardless of what this sink does.
func TestAdapter_OnStatusLine_CachesLatest(t *testing.T) {
	a := &Adapter{Config: Config{AgentID: "ag1", Workdir: "/tmp/x"}}

	if got := a.LatestStatusLine(); got != nil {
		t.Errorf("pre-fire LatestStatusLine = %v; want nil", got)
	}

	first := map[string]any{"agent_state": "idle", "version": "1.0.2"}
	a.OnStatusLine(context.Background(), first)
	got := a.LatestStatusLine()
	if got == nil {
		t.Fatal("expected cached payload, got nil")
	}
	if got["agent_state"] != "idle" || got["version"] != "1.0.2" {
		t.Errorf("cached payload = %v; want {agent_state: idle, version: 1.0.2}", got)
	}

	// Subsequent fires replace (latest-wins, like the chip reducers
	// on mobile).
	second := map[string]any{"agent_state": "working", "version": "1.0.2"}
	a.OnStatusLine(context.Background(), second)
	got = a.LatestStatusLine()
	if got["agent_state"] != "working" {
		t.Errorf("cache not refreshed: %v", got)
	}
}

// A nil payload (gateway feeds {} on a malformed shim post) is stored
// as an empty map rather than ignored, so a later reader can
// distinguish "fired with no fields" from "never fired."
func TestAdapter_OnStatusLine_NilPayloadStoredAsEmpty(t *testing.T) {
	a := &Adapter{Config: Config{AgentID: "ag1", Workdir: "/tmp/x"}}
	a.OnStatusLine(context.Background(), nil)
	got := a.LatestStatusLine()
	if got == nil {
		t.Fatal("nil payload should land as empty map, not nil")
	}
	if len(got) != 0 {
		t.Errorf("empty payload expected; got %v", got)
	}
}

// The returned snapshot is a copy — mutating it doesn't poison the
// cache for the next reader. Pins the defensive-copy contract.
func TestAdapter_LatestStatusLine_ReturnsCopy(t *testing.T) {
	a := &Adapter{Config: Config{AgentID: "ag1", Workdir: "/tmp/x"}}
	a.OnStatusLine(context.Background(),
		map[string]any{"agent_state": "idle"})

	snap1 := a.LatestStatusLine()
	snap1["agent_state"] = "POISONED"

	snap2 := a.LatestStatusLine()
	if snap2["agent_state"] != "idle" {
		t.Errorf("cache poisoned: %v (snap1 mutation leaked)", snap2)
	}
}
