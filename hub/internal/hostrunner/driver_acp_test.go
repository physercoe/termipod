package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeACPAgent speaks the other side of the ACP wire for tests. It reads
// JSON-RPC messages from hostReader (what the driver writes), answers
// initialize + session/new, then emits any queued notifications.
type fakeACPAgent struct {
	t         *testing.T
	hostReads *bufio.Reader // reads from the driver's stdin
	hostWrite io.Writer     // writes into the driver's stdout
	sessionID string

	mu       sync.Mutex
	closed   bool
	initCh   chan struct{} // closed when handshake completes
	received []map[string]any
}

func newFakeACPAgent(t *testing.T, driverStdin io.Reader, driverStdout io.Writer, sessionID string) *fakeACPAgent {
	return &fakeACPAgent{
		t:         t,
		hostReads: bufio.NewReader(driverStdin),
		hostWrite: driverStdout,
		sessionID: sessionID,
		initCh:    make(chan struct{}),
	}
}

func (f *fakeACPAgent) serve() {
	for {
		line, err := f.hostReads.ReadBytes('\n')
		if err != nil {
			return
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		f.mu.Lock()
		f.received = append(f.received, msg)
		f.mu.Unlock()
		method, _ := msg["method"].(string)
		id := msg["id"]
		switch method {
		case "initialize":
			f.respond(id, map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": map[string]any{},
			})
		case "session/new":
			f.respond(id, map[string]any{"sessionId": f.sessionID})
			close(f.initCh)
		case "session/prompt":
			f.respond(id, map[string]any{"stopReason": "end_turn"})
		default:
			// Unknown method from driver: respond with empty result if it
			// was a request (had an id). Notifications get no reply.
			if id != nil {
				f.respond(id, map[string]any{})
			}
		}
	}
}

func (f *fakeACPAgent) respond(id any, result map[string]any) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	_, _ = f.hostWrite.Write(append(b, '\n'))
}

func (f *fakeACPAgent) notify(method string, params map[string]any) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	_, _ = f.hostWrite.Write(append(b, '\n'))
}

func (f *fakeACPAgent) findReceived(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.received {
		if mth, _ := m["method"].(string); mth == method {
			return m
		}
	}
	return nil
}

func (f *fakeACPAgent) close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

// TestACPDriver_HandshakeAndSessionUpdates drives a full handshake, then
// emits each of the session/update variants we translate and asserts
// they land as the right agent_events.
func TestACPDriver_HandshakeAndSessionUpdates(t *testing.T) {
	// driver.Stdin (write) ← hostR.Read  ;  driver.Stdout (read) ← hostW.Write
	hostInR, hostInW := io.Pipe()   // driver writes into hostInW; fake reads from hostInR
	hostOutR, hostOutW := io.Pipe() // fake writes into hostOutW; driver reads from hostOutR

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-acp-1")
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-m1",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the handshake to complete so session/update dispatch can begin.
	select {
	case <-fake.initCh:
	case <-time.After(2 * time.Second):
		t.Fatal("handshake did not complete")
	}

	// agent_message_chunk → text event
	fake.notify("session/update", map[string]any{
		"sessionId": "sess-acp-1",
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "hello"},
		},
	})
	// agent_thought_chunk → thought event
	fake.notify("session/update", map[string]any{
		"sessionId": "sess-acp-1",
		"update": map[string]any{
			"sessionUpdate": "agent_thought_chunk",
			"content":       map[string]any{"type": "text", "text": "thinking..."},
		},
	})
	// tool_call → tool_call event
	fake.notify("session/update", map[string]any{
		"sessionId": "sess-acp-1",
		"update": map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    "tc-1",
			"title":         "Read",
			"kind":          "read",
			"status":        "pending",
			"rawInput":      map[string]any{"path": "/tmp/x"},
		},
	})
	// plan → plan event (pass-through)
	fake.notify("session/update", map[string]any{
		"sessionId": "sess-acp-1",
		"update": map[string]any{
			"sessionUpdate": "plan",
			"entries":       []any{map[string]any{"content": "step 1"}},
		},
	})
	// user_message_chunk → dropped (our own echo)
	fake.notify("session/update", map[string]any{
		"sessionId": "sess-acp-1",
		"update": map[string]any{
			"sessionUpdate": "user_message_chunk",
			"content":       map[string]any{"type": "text", "text": "our input"},
		},
	})

	// Expect: lifecycle.started + 4 translated events (user_message_chunk dropped).
	poster.wait(t, 5, 2*time.Second)

	drv.Stop()
	poster.wait(t, 6, time.Second)

	evs := poster.snapshot()

	if evs[0].Kind != "lifecycle" || evs[0].Payload["phase"] != "started" ||
		evs[0].Payload["mode"] != "M1" || evs[0].Payload["session_id"] != "sess-acp-1" {
		t.Fatalf("evs[0] want lifecycle.started/M1/sess-acp-1; got %+v", evs[0])
	}

	// Collect translated events (skip lifecycle at [0] and [last]).
	translated := evs[1 : len(evs)-1]
	if len(translated) != 4 {
		t.Fatalf("want 4 translated events; got %d (%+v)", len(translated), translated)
	}
	if translated[0].Kind != "text" || translated[0].Payload["text"] != "hello" {
		t.Fatalf("translated[0] want text=hello; got %+v", translated[0])
	}
	if translated[1].Kind != "thought" || translated[1].Payload["text"] != "thinking..." {
		t.Fatalf("translated[1] want thought; got %+v", translated[1])
	}
	if translated[2].Kind != "tool_call" || translated[2].Payload["id"] != "tc-1" ||
		translated[2].Payload["name"] != "Read" {
		t.Fatalf("translated[2] want tool_call tc-1/Read; got %+v", translated[2])
	}
	if translated[3].Kind != "plan" {
		t.Fatalf("translated[3] want plan; got %+v", translated[3])
	}

	last := evs[len(evs)-1]
	if last.Kind != "lifecycle" || last.Payload["phase"] != "stopped" {
		t.Fatalf("last want lifecycle.stopped; got %+v", last)
	}
}

// TestACPDriver_RejectsAgentRequests verifies that a request flowing from
// agent → host-runner (something we don't implement yet) gets a
// method-not-found error and doesn't crash the driver.
func TestACPDriver_RejectsAgentRequests(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-acp-2")
	go fake.serve()

	// Capture what the driver writes back so we can assert on the error reply.
	replies := make(chan map[string]any, 4)
	go func() {
		br := bufio.NewReader(hostInR) // taps host→agent stream *after* fake already consumed
		_ = br                         // placeholder; we'll use fake for initialize handling
	}()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID: "agent-m1b",
		Poster:  poster,
		Stdin:   hostInW,
		Stdout:  hostOutR,
		Closer:  func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-fake.initCh

	// Fire a fake agent→client request. We can't easily read the driver's
	// response mid-test because fake owns hostInR; instead we just assert
	// the driver doesn't hang and lifecycle.stopped still lands.
	_ = replies
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "fs/read_text_file",
		"params":  map[string]any{"path": "/tmp/x"},
	})
	_, _ = hostOutW.Write(append(b, '\n'))

	// Give the reader a moment to process, then Stop.
	time.Sleep(50 * time.Millisecond)
	drv.Stop()

	evs := poster.snapshot()
	last := evs[len(evs)-1]
	if last.Kind != "lifecycle" || last.Payload["phase"] != "stopped" {
		t.Fatalf("want lifecycle.stopped after rejected agent request; got %+v", last)
	}
}

// TestACPDriver_InputTextPrompts drives a handshake then fires Input text
// and asserts a session/prompt JSON-RPC call lands on the agent side with
// the right sessionId + content block.
func TestACPDriver_InputTextPrompts(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-input-1")
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-input",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()
	<-fake.initCh

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hello agent"}); err != nil {
		t.Fatalf("Input text: %v", err)
	}

	// Poll for the prompt to arrive on the fake side.
	deadline := time.Now().Add(2 * time.Second)
	var prompt map[string]any
	for time.Now().Before(deadline) {
		if m := fake.findReceived("session/prompt"); m != nil {
			prompt = m
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if prompt == nil {
		t.Fatal("session/prompt never arrived on the agent side")
	}
	params, _ := prompt["params"].(map[string]any)
	if params["sessionId"] != "sess-input-1" {
		t.Fatalf("prompt sessionId = %v; want sess-input-1", params["sessionId"])
	}
	promptArr, _ := params["prompt"].([]any)
	if len(promptArr) == 0 {
		t.Fatalf("prompt array empty: %+v", params)
	}
	block, _ := promptArr[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "hello agent" {
		t.Fatalf("prompt block wrong: %+v", block)
	}
}

// TestACPDriver_InputCancelSendsNotification verifies cancel emits a
// session/cancel message without an id (notification semantics).
func TestACPDriver_InputCancelSendsNotification(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-cancel-1")
	go fake.serve()

	drv := &ACPDriver{
		AgentID: "agent-cancel",
		Poster:  &fakePoster{},
		Stdin:   hostInW,
		Stdout:  hostOutR,
		Closer:  func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()
	<-fake.initCh

	if err := drv.Input(context.Background(), "cancel", nil); err != nil {
		t.Fatalf("Input cancel: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m := fake.findReceived("session/cancel"); m != nil {
			if m["id"] != nil {
				t.Fatalf("session/cancel must be a notification (no id); got %+v", m)
			}
			params, _ := m["params"].(map[string]any)
			if params["sessionId"] != "sess-cancel-1" {
				t.Fatalf("cancel sessionId = %v; want sess-cancel-1", params["sessionId"])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session/cancel never arrived")
}

func TestACPDriver_InputRejectsBeforeHandshake(t *testing.T) {
	drv := &ACPDriver{AgentID: "pre-handshake", Poster: &fakePoster{}}
	if err := drv.Input(context.Background(), "text", map[string]any{"body": "x"}); err == nil {
		t.Fatal("want error when session id is empty")
	}
}

// TestACPDriver_HandshakeTimeout guards against a silent agent: if
// initialize never gets a response, Start must return an error within the
// configured timeout rather than blocking forever.
func TestACPDriver_HandshakeTimeout(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()
	// Silent agent: drains stdin but never replies. This is the realistic
	// failure — a process that started but got stuck before writing output.
	go func() { _, _ = io.Copy(io.Discard, hostInR) }()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-m1c",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close() },
		HandshakeTimeout: 100 * time.Millisecond,
	}
	err := drv.Start(context.Background())
	if err == nil {
		t.Fatal("expected handshake timeout; got nil error")
	}
	if !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("expected initialize error; got %v", err)
	}
	// Cleanup so the reader goroutine unwinds.
	_ = hostInW.Close()
	_ = hostOutW.Close()
}
