package hostrunner

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/termipod/hub/internal/agentfamilies"
)

// fakeGeminiCmd plays a single subprocess invocation: produces canned
// stdout, blocks Wait until either the producer goroutine finishes or
// Kill is called. Tests construct one per expected turn.
type fakeGeminiCmd struct {
	args   []string
	frames []string // JSONL lines this turn emits

	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter

	startedCh chan struct{} // closed on Start
	doneCh    chan struct{} // closed when the producer goroutine exits

	mu      sync.Mutex
	killed  bool
	started bool
}

func newFakeGeminiCmd(args []string, frames []string) *fakeGeminiCmd {
	r, w := io.Pipe()
	return &fakeGeminiCmd{
		args:      args,
		frames:    frames,
		stdoutR:   r,
		stdoutW:   w,
		startedCh: make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

func (c *fakeGeminiCmd) StdoutPipe() (io.ReadCloser, error) { return c.stdoutR, nil }
func (c *fakeGeminiCmd) Args() []string                     { return c.args }

func (c *fakeGeminiCmd) Start() error {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	close(c.startedCh)
	go func() {
		defer close(c.doneCh)
		defer c.stdoutW.Close()
		for _, f := range c.frames {
			if _, err := c.stdoutW.Write([]byte(f + "\n")); err != nil {
				return
			}
			// Tiny pause so the driver's reader picks up frames in
			// order — pure cosmetic; not load-bearing.
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return nil
}

func (c *fakeGeminiCmd) Wait() error {
	<-c.doneCh
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.killed {
		return errors.New("killed")
	}
	return nil
}

func (c *fakeGeminiCmd) Kill() error {
	c.mu.Lock()
	if c.killed {
		c.mu.Unlock()
		return nil
	}
	c.killed = true
	c.mu.Unlock()
	_ = c.stdoutW.CloseWithError(errors.New("killed"))
	return nil
}

// TestExecResumeDriver_FirstTurnHasNoResume verifies the very first
// Input call spawns gemini *without* --resume — there's no captured
// session UUID yet — and that the init event latches the session_id
// for next time.
func TestExecResumeDriver_FirstTurnHasNoResume(t *testing.T) {
	fam, ok := agentfamilies.ByName("gemini-cli")
	if !ok || fam.FrameProfile == nil {
		t.Fatal("gemini-cli frame profile not embedded — run slice 2 first")
	}

	frames := []string{
		`{"type":"init","session_id":"sess-1","model":"gemini-2.5-pro","timestamp":"t"}`,
		`{"type":"message","role":"assistant","content":"hi","delta":false,"timestamp":"t"}`,
		`{"type":"result","status":"success","stats":{"input_tokens":10},"timestamp":"t"}`,
	}

	var capturedArgs []string
	var argsMu sync.Mutex
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		argsMu.Lock()
		capturedArgs = append([]string{name}, args...)
		argsMu.Unlock()
		return newFakeGeminiCmd(append([]string{name}, args...), frames)
	}

	poster := &recordingPoster{}
	drv := &ExecResumeDriver{
		AgentID:        "agt-1",
		Handle:         "@steward",
		Poster:         poster,
		Bin:            "/usr/bin/gemini",
		Workdir:        "/tmp/wt",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		Yolo:           true,
		KillGrace:      500 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text",
		map[string]any{"body": "hello"}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	argsMu.Lock()
	args := append([]string{}, capturedArgs...)
	argsMu.Unlock()
	for _, a := range args {
		if a == "--resume" {
			t.Errorf("first turn argv contains --resume; should not (no captured session yet). args=%v", args)
		}
	}
	if !containsArg(args, "--output-format", "stream-json") {
		t.Errorf("argv missing --output-format stream-json: %v", args)
	}
	if !containsArg(args, "-p", "hello") {
		t.Errorf("argv missing -p hello: %v", args)
	}
	if !containsFlag(args, "--yolo") {
		t.Errorf("argv missing --yolo (Yolo=true should add it): %v", args)
	}
	if !containsFlag(args, "--skip-trust") {
		t.Errorf("argv missing --skip-trust (gemini-cli@0.41 refuses headless launch from untrusted folder otherwise): %v", args)
	}

	if got := drv.SessionID(); got != "sess-1" {
		t.Errorf("SessionID after first turn = %q; want sess-1", got)
	}

	// Verify the init event was published as session.init.
	if !poster.has("session.init", "sess-1") {
		t.Errorf("expected session.init with sess-1 published; got %+v", poster.events)
	}
}

// TestExecResumeDriver_SecondTurnUsesResume drives two turns and
// verifies the second turn's argv carries --resume <UUID>.
func TestExecResumeDriver_SecondTurnUsesResume(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")

	frames1 := []string{
		`{"type":"init","session_id":"sess-2","model":"gemini-2.5-pro","timestamp":"t"}`,
		`{"type":"message","role":"assistant","content":"first","delta":false,"timestamp":"t"}`,
		`{"type":"result","status":"success","timestamp":"t"}`,
	}
	frames2 := []string{
		// Resumed turn — gemini still emits a fresh init in stream-json,
		// session_id stays the same.
		`{"type":"init","session_id":"sess-2","model":"gemini-2.5-pro","timestamp":"t"}`,
		`{"type":"message","role":"assistant","content":"second","delta":false,"timestamp":"t"}`,
		`{"type":"result","status":"success","timestamp":"t"}`,
	}

	turnIdx := 0
	var capturedArgs [][]string
	var argsMu sync.Mutex
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		argsMu.Lock()
		capturedArgs = append(capturedArgs, append([]string{name}, args...))
		idx := turnIdx
		turnIdx++
		argsMu.Unlock()
		var frames []string
		if idx == 0 {
			frames = frames1
		} else {
			frames = frames2
		}
		return newFakeGeminiCmd(append([]string{name}, args...), frames)
	}

	poster := &recordingPoster{}
	drv := &ExecResumeDriver{
		AgentID:        "agt-2",
		Poster:         poster,
		Bin:            "/usr/bin/gemini",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		KillGrace:      500 * time.Millisecond,
	}
	_ = drv.Start(context.Background())
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "first"}); err != nil {
		t.Fatalf("Input #1: %v", err)
	}
	if err := drv.Input(context.Background(), "text", map[string]any{"body": "second"}); err != nil {
		t.Fatalf("Input #2: %v", err)
	}

	argsMu.Lock()
	defer argsMu.Unlock()
	if len(capturedArgs) != 2 {
		t.Fatalf("expected 2 spawns, got %d", len(capturedArgs))
	}

	for _, a := range capturedArgs[0] {
		if a == "--resume" {
			t.Errorf("turn 1 argv has --resume; should not: %v", capturedArgs[0])
		}
	}

	if !containsArg(capturedArgs[1], "--resume", "sess-2") {
		t.Errorf("turn 2 argv missing --resume sess-2: %v", capturedArgs[1])
	}
	if !containsArg(capturedArgs[1], "-p", "second") {
		t.Errorf("turn 2 argv missing -p second: %v", capturedArgs[1])
	}
}

// TestExecResumeDriver_SetResumeSessionID covers the rehydration path:
// the hub reads agents.thread_id_json on restart and seeds the driver.
// The very next Input should already carry --resume.
func TestExecResumeDriver_SetResumeSessionID(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")

	frames := []string{
		`{"type":"init","session_id":"sess-pre","model":"x","timestamp":"t"}`,
		`{"type":"message","role":"assistant","content":"resumed","delta":false,"timestamp":"t"}`,
		`{"type":"result","status":"success","timestamp":"t"}`,
	}

	var captured []string
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		captured = append([]string{name}, args...)
		return newFakeGeminiCmd(captured, frames)
	}

	drv := &ExecResumeDriver{
		AgentID:        "agt-3",
		Poster:         &recordingPoster{},
		Bin:            "/usr/bin/gemini",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
	}
	_ = drv.Start(context.Background())
	defer drv.Stop()

	drv.SetResumeSessionID("sess-pre")

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	if !containsArg(captured, "--resume", "sess-pre") {
		t.Errorf("argv missing --resume sess-pre after SetResumeSessionID: %v", captured)
	}
}

// TestExecResumeDriver_StopKillsInFlight verifies Stop interrupts an
// in-flight subprocess. Uses a fake whose producer never finishes
// unless killed.
func TestExecResumeDriver_StopKillsInFlight(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")

	// Build a fake whose stdout pipe is open but no frames are written —
	// Wait will block until Kill is called.
	r, w := io.Pipe()
	fc := &blockingFakeCmd{stdoutR: r, stdoutW: w, doneCh: make(chan struct{})}
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd { return fc }

	drv := &ExecResumeDriver{
		AgentID:        "agt-4",
		Poster:         &recordingPoster{},
		Bin:            "/usr/bin/gemini",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
	}
	_ = drv.Start(context.Background())

	inputDone := make(chan error, 1)
	go func() {
		inputDone <- drv.Input(context.Background(), "text", map[string]any{"body": "hi"})
	}()

	// Wait for the spawn to start before we Stop.
	select {
	case <-fc.startCh():
	case <-time.After(time.Second):
		t.Fatal("fake gemini didn't start within 1s")
	}

	drv.Stop()

	select {
	case <-inputDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Input didn't return within 2s after Stop — Kill not propagating")
	}
}

// TestExecResumeDriver_RejectsPermissionPromptReply pins ADR-013 D4:
// gemini doesn't have a permission_prompt vendor capability, so the
// driver must refuse to deliver an attention_reply with that kind
// rather than silently turning it into a user-text turn. The hub's
// dispatcher should never produce one for a gemini agent — this is
// defense-in-depth.
func TestExecResumeDriver_RejectsPermissionPromptReply(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		return newFakeGeminiCmd(append([]string{name}, args...), nil)
	}
	drv := &ExecResumeDriver{
		AgentID:        "agt-perm",
		Poster:         &recordingPoster{},
		Bin:            "/usr/bin/gemini",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
	}
	_ = drv.Start(context.Background())
	defer drv.Stop()

	err := drv.Input(context.Background(), "attention_reply", map[string]any{
		"kind":     "permission_prompt",
		"decision": "approve",
	})
	if err == nil {
		t.Fatal("permission_prompt reply should be rejected on gemini")
	}
	if !strings.Contains(err.Error(), "permission_prompt is unsupported on gemini-cli") {
		t.Errorf("error missing ADR-013 D4 attribution: %v", err)
	}
}

// TestExecResumeDriver_RejectsCommandBuilderNil pins the safety check —
// missing CommandBuilder should be a Start error, not a panic at Input
// time.
func TestExecResumeDriver_RejectsCommandBuilderNil(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")
	drv := &ExecResumeDriver{
		AgentID:      "agt-5",
		Poster:       &recordingPoster{},
		Bin:          "/usr/bin/gemini",
		FrameProfile: fam.FrameProfile,
	}
	if err := drv.Start(context.Background()); err == nil {
		t.Error("Start with nil CommandBuilder should error")
	}
}

// blockingFakeCmd never produces output; Wait blocks until Kill is
// called. Used to test Stop's interrupt path.
type blockingFakeCmd struct {
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	doneCh  chan struct{}
	startCh_  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	killed  bool
}

func (c *blockingFakeCmd) startCh() chan struct{} {
	c.once.Do(func() { c.startCh_ = make(chan struct{}) })
	return c.startCh_
}

func (c *blockingFakeCmd) StdoutPipe() (io.ReadCloser, error) { return c.stdoutR, nil }
func (c *blockingFakeCmd) Start() error                       { close(c.startCh()); return nil }
func (c *blockingFakeCmd) Wait() error {
	<-c.doneCh
	return errors.New("killed")
}
func (c *blockingFakeCmd) Kill() error {
	c.mu.Lock()
	if c.killed {
		c.mu.Unlock()
		return nil
	}
	c.killed = true
	c.mu.Unlock()
	_ = c.stdoutW.CloseWithError(errors.New("killed"))
	close(c.doneCh)
	return nil
}
func (c *blockingFakeCmd) Args() []string { return nil }

// recordingPoster captures PostAgentEvent calls for assertion.
type recordingPoster struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Kind     string
	Producer string
	Payload  map[string]any
}

func (p *recordingPoster) PostAgentEvent(_ context.Context, _ string, kind, producer string, payload any) error {
	pl, _ := payload.(map[string]any)
	p.mu.Lock()
	p.events = append(p.events, recordedEvent{Kind: kind, Producer: producer, Payload: pl})
	p.mu.Unlock()
	return nil
}

// has returns true if a session.init event with session_id=sid was posted.
func (p *recordingPoster) has(kind, sessionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.events {
		if e.Kind == kind {
			if sid, _ := e.Payload["session_id"].(string); sid == sessionID {
				return true
			}
		}
	}
	return false
}

// TestExecResumeDriver_AccumulatesAssistantDeltas pins the per-turn
// streaming-text accumulator. gemini-cli@0.41 emits assistant content
// only as delta=true chunks (incremental, not cumulative). The driver
// must accumulate them and emit `kind=text, partial=true,
// message_id=<turn-local id>` events whose `text` carries the FULL
// running content — that's the shape the mobile transcript's
// _collapseStreamingPartials chain expects to fold into one bubble.
//
// Two turns in this test: each turn must get a different message_id so
// turn N's chunks don't merge into turn N-1's bubble.
func TestExecResumeDriver_AccumulatesAssistantDeltas(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")

	turn1Frames := []string{
		`{"type":"init","session_id":"sess-acc","model":"m","timestamp":"t1"}`,
		`{"type":"message","role":"user","content":"hi","timestamp":"t1"}`,
		`{"type":"message","role":"assistant","content":"Hello","delta":true,"timestamp":"t2"}`,
		`{"type":"message","role":"assistant","content":", world","delta":true,"timestamp":"t3"}`,
		`{"type":"result","status":"success","stats":{"input_tokens":1},"timestamp":"t4"}`,
	}
	turn2Frames := []string{
		`{"type":"init","session_id":"sess-acc","model":"m","timestamp":"t5"}`,
		`{"type":"message","role":"assistant","content":"Bye","delta":true,"timestamp":"t6"}`,
		`{"type":"result","status":"success","stats":{"input_tokens":1},"timestamp":"t7"}`,
	}

	turn := 0
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		defer func() { turn++ }()
		if turn == 0 {
			return newFakeGeminiCmd(append([]string{name}, args...), turn1Frames)
		}
		return newFakeGeminiCmd(append([]string{name}, args...), turn2Frames)
	}

	poster := &recordingPoster{}
	drv := &ExecResumeDriver{
		AgentID:        "agt-acc",
		Handle:         "@s",
		Poster:         poster,
		Bin:            "/usr/bin/gemini",
		Workdir:        "/tmp/wt",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		Yolo:           true,
		KillGrace:      500 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hello"}); err != nil {
		t.Fatalf("Input turn 1: %v", err)
	}
	if err := drv.Input(context.Background(), "text", map[string]any{"body": "again"}); err != nil {
		t.Fatalf("Input turn 2: %v", err)
	}

	// Collect every text/agent partial event in order.
	type partial struct {
		text  string
		mid   string
		isPar bool
	}
	var partials []partial
	poster.mu.Lock()
	for _, e := range poster.events {
		if e.Kind != "text" || e.Producer != "agent" {
			continue
		}
		txt, _ := e.Payload["text"].(string)
		mid, _ := e.Payload["message_id"].(string)
		par, _ := e.Payload["partial"].(bool)
		partials = append(partials, partial{text: txt, mid: mid, isPar: par})
	}
	poster.mu.Unlock()

	// Turn 1: 2 chunks → 2 partials with cumulative text "Hello" and
	// "Hello, world". Turn 2: 1 chunk → 1 partial with "Bye". So 3
	// total partial text events.
	if len(partials) != 3 {
		t.Fatalf("partial count = %d (want 3 — 2 from turn1 + 1 from turn2); got %+v", len(partials), partials)
	}
	if partials[0].text != "Hello" {
		t.Errorf("turn1 chunk1.text = %q; want %q (single chunk so far)", partials[0].text, "Hello")
	}
	if partials[1].text != "Hello, world" {
		t.Errorf("turn1 chunk2.text = %q; want cumulative %q", partials[1].text, "Hello, world")
	}
	if partials[2].text != "Bye" {
		t.Errorf("turn2 chunk1.text = %q; want %q (fresh buffer for new turn)", partials[2].text, "Bye")
	}

	// All 3 partials must be flagged partial:true.
	for i, p := range partials {
		if !p.isPar {
			t.Errorf("partial[%d].partial = false; want true (lets mobile collapse fold the chain)", i)
		}
	}

	// Turn 1's chunks share a message_id. Turn 2's chunk uses a
	// DIFFERENT message_id — otherwise the mobile collapse would merge
	// the new turn's bubble into the previous turn's bubble.
	if partials[0].mid == "" {
		t.Error("turn1 message_id is empty; collapse needs a stable id per turn")
	}
	if partials[0].mid != partials[1].mid {
		t.Errorf("turn1 chunks have different message_ids (%q vs %q); chain must share one id within a turn",
			partials[0].mid, partials[1].mid)
	}
	if partials[2].mid == partials[0].mid {
		t.Errorf("turn2 message_id %q == turn1 message_id; new turns MUST start a fresh chain or bubbles merge across turns",
			partials[2].mid)
	}
}

// containsArg returns true iff args contains `flag value` as adjacent tokens.
func containsArg(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// containsFlag returns true iff args contains the standalone flag.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// TestExecResumeDriver_NormalizesByModelTokens pins the gemini→hub key
// rewrite the driver applies after the frame profile lifts
// `stats.models` verbatim. gemini-cli@0.41 emits per-model entries with
// gemini-native names (input_tokens / output_tokens / cached) that the
// mobile telemetry aggregator (_ModelTokens.add in agent_feed.dart)
// doesn't read — it expects input / output / cache_read. Without this
// rewrite, the mobile token-usage tile reads 0 for every gemini turn
// even though the JSON has real numbers (caught in v1.0.399).
func TestExecResumeDriver_NormalizesByModelTokens(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")

	// Real-shaped result frame from gemini-cli@0.41.2: nested stats with
	// per-model entries using input_tokens / output_tokens / cached.
	frames := []string{
		`{"type":"init","session_id":"sess-norm","model":"m","timestamp":"t1"}`,
		`{"type":"result","status":"success","stats":{"total_tokens":20476,"input_tokens":19820,"output_tokens":40,"cached":0,"input":19820,"duration_ms":40219,"tool_calls":0,"models":{"gemini-2.5-flash-lite":{"total_tokens":881,"input_tokens":787,"output_tokens":28,"cached":0,"input":787},"gemini-3-flash-preview":{"total_tokens":19595,"input_tokens":19033,"output_tokens":12,"cached":0,"input":19033}}},"timestamp":"t2"}`,
	}
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		return newFakeGeminiCmd(append([]string{name}, args...), frames)
	}

	poster := &recordingPoster{}
	drv := &ExecResumeDriver{
		AgentID:        "agt-norm",
		Handle:         "@s",
		Poster:         poster,
		Bin:            "/usr/bin/gemini",
		Workdir:        "/tmp/wt",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		Yolo:           true,
		KillGrace:      500 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	var got map[string]any
	poster.mu.Lock()
	for _, e := range poster.events {
		if e.Kind == "turn.result" {
			got = e.Payload
			break
		}
	}
	poster.mu.Unlock()
	if got == nil {
		t.Fatal("no turn.result event posted")
	}

	bm, ok := got["by_model"].(map[string]any)
	if !ok {
		t.Fatalf("turn.result.by_model missing or wrong type: %+v", got["by_model"])
	}

	for _, name := range []string{"gemini-2.5-flash-lite", "gemini-3-flash-preview"} {
		entry, ok := bm[name].(map[string]any)
		if !ok {
			t.Errorf("by_model[%q] missing", name)
			continue
		}
		// Canonical keys must be present so _ModelTokens.add picks them up.
		if _, has := entry["input"]; !has {
			t.Errorf("by_model[%q].input missing — mobile aggregator reads `input`, not `input_tokens`", name)
		}
		if _, has := entry["output"]; !has {
			t.Errorf("by_model[%q].output missing — mobile aggregator reads `output`, not `output_tokens`", name)
		}
		if _, has := entry["cache_read"]; !has {
			t.Errorf("by_model[%q].cache_read missing — mobile aggregator reads `cache_read`, not `cached`", name)
		}
	}

	// Pin specific values for the lite model so a regression that
	// silently zeroes out output gets caught.
	lite := bm["gemini-2.5-flash-lite"].(map[string]any)
	if v, _ := lite["output"].(float64); v != 28 {
		t.Errorf("by_model[lite].output = %v; want 28 (lifted from output_tokens)", lite["output"])
	}
	if v, _ := lite["input"].(float64); v != 787 {
		t.Errorf("by_model[lite].input = %v; want 787", lite["input"])
	}

	// Original gemini-named fields must still be present so the raw
	// `stats` block under turn.result.stats stays lossless.
	if _, has := lite["input_tokens"]; !has {
		t.Errorf("by_model[lite].input_tokens removed — original fields must be preserved alongside canonical ones")
	}
}

// TestExecResumeDriver_NextTurnModelArgvSplice — W2.4: Input("set_model")
// stashes the override, the next runTurn argv carries `--model X`, and
// the slot is consumed (subsequent turns omit the flag unless the
// picker fires again — sticky behavior is a follow-up).
func TestExecResumeDriver_NextTurnModelArgvSplice(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")
	frames := []string{
		`{"type":"init","session_id":"s","model":"gemini-2.5-pro","timestamp":"t"}`,
		`{"type":"result","status":"success","stats":{},"timestamp":"t"}`,
	}
	var capturedArgs [][]string
	var argsMu sync.Mutex
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		full := append([]string{name}, args...)
		argsMu.Lock()
		capturedArgs = append(capturedArgs, full)
		argsMu.Unlock()
		return newFakeGeminiCmd(full, frames)
	}
	drv := &ExecResumeDriver{
		AgentID:        "agt-set-model",
		Handle:         "@steward",
		Poster:         &recordingPoster{},
		Bin:            "/usr/bin/gemini",
		Workdir:        "/tmp/wt",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		KillGrace:      500 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	// Stash the override.
	if err := drv.Input(context.Background(), "set_model",
		map[string]any{"model_id": "gemini-2.5-flash"}); err != nil {
		t.Fatalf("Input set_model: %v", err)
	}
	// Drive turn 1 — must include --model gemini-2.5-flash.
	if err := drv.Input(context.Background(), "text",
		map[string]any{"body": "hello"}); err != nil {
		t.Fatalf("Input text 1: %v", err)
	}
	// Drive turn 2 — must NOT carry --model (slot consumed; sticky is
	// a follow-up).
	if err := drv.Input(context.Background(), "text",
		map[string]any{"body": "again"}); err != nil {
		t.Fatalf("Input text 2: %v", err)
	}

	argsMu.Lock()
	turn1 := append([]string{}, capturedArgs[0]...)
	turn2 := append([]string{}, capturedArgs[1]...)
	argsMu.Unlock()
	if !containsArg(turn1, "--model", "gemini-2.5-flash") {
		t.Errorf("turn1 argv missing --model gemini-2.5-flash: %v", turn1)
	}
	if containsFlag(turn2, "--model") {
		t.Errorf("turn2 argv still carries --model after one-shot consume: %v", turn2)
	}
}

// TestExecResumeDriver_NextTurnModeArgvSplice — W2.4: same shape as
// model, but the override maps to gemini's `--approval-mode <id>`.
// When the picker explicitly chooses a mode for this turn, the legacy
// `--yolo` flag is suppressed so --approval-mode wins.
func TestExecResumeDriver_NextTurnModeArgvSplice(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")
	frames := []string{
		`{"type":"init","session_id":"s","model":"gemini-2.5-pro","timestamp":"t"}`,
		`{"type":"result","status":"success","stats":{},"timestamp":"t"}`,
	}
	var capturedArgs [][]string
	var argsMu sync.Mutex
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		full := append([]string{name}, args...)
		argsMu.Lock()
		capturedArgs = append(capturedArgs, full)
		argsMu.Unlock()
		return newFakeGeminiCmd(full, frames)
	}
	drv := &ExecResumeDriver{
		AgentID:        "agt-set-mode",
		Handle:         "@steward",
		Poster:         &recordingPoster{},
		Bin:            "/usr/bin/gemini",
		Workdir:        "/tmp/wt",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		Yolo:           true, // baseline state; override should suppress --yolo
		KillGrace:      500 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if err := drv.Input(context.Background(), "set_mode",
		map[string]any{"mode_id": "auto-edit"}); err != nil {
		t.Fatalf("Input set_mode: %v", err)
	}
	if err := drv.Input(context.Background(), "text",
		map[string]any{"body": "hello"}); err != nil {
		t.Fatalf("Input text 1: %v", err)
	}
	if err := drv.Input(context.Background(), "text",
		map[string]any{"body": "again"}); err != nil {
		t.Fatalf("Input text 2: %v", err)
	}

	argsMu.Lock()
	turn1 := append([]string{}, capturedArgs[0]...)
	turn2 := append([]string{}, capturedArgs[1]...)
	argsMu.Unlock()
	if !containsArg(turn1, "--approval-mode", "auto-edit") {
		t.Errorf("turn1 argv missing --approval-mode auto-edit: %v", turn1)
	}
	if containsFlag(turn1, "--yolo") {
		t.Errorf("turn1 argv still has --yolo despite explicit --approval-mode override: %v", turn1)
	}
	// Turn 2 reverts: no override, Yolo=true → --yolo back, no --approval-mode.
	if !containsFlag(turn2, "--yolo") {
		t.Errorf("turn2 argv missing --yolo (should revert after one-shot consume): %v", turn2)
	}
	if containsFlag(turn2, "--approval-mode") {
		t.Errorf("turn2 argv still carries --approval-mode: %v", turn2)
	}
}

// TestExecResumeDriver_TextStripsImagesAndWarns — W4.5: gemini's
// exec-per-turn argv has no inline-image affordance, so when a hub
// input carries `images`, the driver:
//   - emits a kind=system event noting the strip + the upgrade
//     path (switch to gemini --acp / M1 for multimodal turns),
//   - lets the text portion proceed normally as gemini -p <body>.
// Hub-side W4.1 validation has already enforced caps; this is the
// last-mile drop.
func TestExecResumeDriver_TextStripsImagesAndWarns(t *testing.T) {
	fam, ok := agentfamilies.ByName("gemini-cli")
	if !ok {
		t.Fatal("gemini-cli family missing")
	}
	frames := []string{
		`{"type":"init","session_id":"sess-w45","model":"gemini-2.5-pro","timestamp":"t"}`,
		`{"type":"message","role":"assistant","content":"ok","delta":false,"timestamp":"t"}`,
		`{"type":"result","status":"success","timestamp":"t"}`,
	}
	var capturedArgs []string
	var argsMu sync.Mutex
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		argsMu.Lock()
		capturedArgs = append([]string{name}, args...)
		argsMu.Unlock()
		return newFakeGeminiCmd(append([]string{name}, args...), frames)
	}
	poster := &recordingPoster{}
	drv := &ExecResumeDriver{
		AgentID:        "agt-w45",
		Handle:         "@steward",
		Poster:         poster,
		Bin:            "/usr/bin/gemini",
		Workdir:        "/tmp/wt",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		KillGrace:      500 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{
		"body": "describe this",
		"images": []any{
			map[string]any{"mime_type": "image/png", "data": "AAA="},
		},
	}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	// argv must carry the body, not an image flag (gemini-exec has
	// no such flag — but check anyway for forward-compat regression
	// guard if someone adds one to gemini and we forget to rewire).
	argsMu.Lock()
	args := append([]string{}, capturedArgs...)
	argsMu.Unlock()
	if !containsArg(args, "-p", "describe this") {
		t.Errorf("argv missing -p body: %v", args)
	}
	for _, a := range args {
		if a == "AAA=" {
			t.Errorf("argv leaked image data: %v", args)
		}
	}

	// Find the system warn event.
	poster.mu.Lock()
	defer poster.mu.Unlock()
	var found bool
	for _, e := range poster.events {
		if e.Kind != "system" {
			continue
		}
		reason, _ := e.Payload["reason"].(string)
		engine, _ := e.Payload["engine"].(string)
		if engine == "gemini-exec" && reason != "" && strings.Contains(strings.ToLower(reason), "no inline multimodal support") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected gemini-exec strip warning; got events %+v", poster.events)
	}
}

// TestExecResumeDriver_TextImagesOnlyStillRejected — gemini-exec
// can't carry images at all; an image-only input (no body) is still
// invalid because the strip leaves nothing to send. The text-input
// missing-body error is the right user-visible signal.
func TestExecResumeDriver_TextImagesOnlyStillRejected(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		t.Fatalf("CommandBuilder must not be invoked when body is empty")
		return nil
	}
	poster := &recordingPoster{}
	drv := &ExecResumeDriver{
		AgentID:        "agt-w45b",
		Handle:         "@steward",
		Poster:         poster,
		Bin:            "/usr/bin/gemini",
		Workdir:        "/tmp/wt",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		KillGrace:      500 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	err := drv.Input(context.Background(), "text", map[string]any{
		"images": []any{
			map[string]any{"mime_type": "image/png", "data": "AAA="},
		},
	})
	if err == nil {
		t.Fatal("expected error for image-only input on gemini-exec")
	}
	// Still warn — the principal needs to know images were dropped
	// before the error message about missing body.
	poster.mu.Lock()
	defer poster.mu.Unlock()
	var sawWarn bool
	for _, e := range poster.events {
		if e.Kind == "system" {
			engine, _ := e.Payload["engine"].(string)
			if engine == "gemini-exec" {
				sawWarn = true
				break
			}
		}
	}
	if !sawWarn {
		t.Errorf("expected gemini-exec strip warning before missing-body error; events=%+v", poster.events)
	}
}

// TestExecResumeDriver_SetModelMissingID — empty model_id returns a
// typed error without touching the override slot.
func TestExecResumeDriver_SetModelMissingID(t *testing.T) {
	fam, _ := agentfamilies.ByName("gemini-cli")
	cb := func(ctx context.Context, name string, args ...string) GeminiCmd {
		t.Fatalf("CommandBuilder must not be invoked on validation failure")
		return nil
	}
	drv := &ExecResumeDriver{
		AgentID:        "agt-bad",
		Handle:         "@steward",
		Poster:         &recordingPoster{},
		Bin:            "/usr/bin/gemini",
		Workdir:        "/tmp/wt",
		FrameProfile:   fam.FrameProfile,
		CommandBuilder: cb,
		KillGrace:      500 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if err := drv.Input(context.Background(), "set_model",
		map[string]any{}); err == nil {
		t.Fatalf("expected error for missing model_id, got nil")
	}
}
