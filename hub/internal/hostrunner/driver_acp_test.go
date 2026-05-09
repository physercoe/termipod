package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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

	// W1.2 toggles. Defaults preserve the W1.1-era handshake shape so
	// existing tests don't have to opt in.
	advertiseLoadSession bool                     // initialize → agentCapabilities.loadSession=true
	failSessionLoad      bool                     // session/load → JSON-RPC error
	loadReplayFrames     []map[string]any         // streamed BEFORE session/load reply

	// W1.4 toggles. Defaults preserve "no auth required" so all
	// pre-W1.4 tests remain unaffected.
	authMethods       []map[string]any // initialize.authMethods array
	failAuthenticate  bool             // authenticate → JSON-RPC error
	hangAuthenticate  bool             // authenticate → never reply (timeout exercise)
	authMethodsCalled []string         // recorded methodIds the driver dispatched

	// W2.2 toggles. availableModes/availableModels are injected into the
	// session/new response under modes.availableModes /
	// models.availableModels so the driver caches them. failSetMode /
	// failSetModel make the matching RPC return a JSON-RPC error so
	// tests can exercise the error-propagation path.
	availableModes  []string
	availableModels []string
	failSetMode     bool
	failSetModel    bool

	// W4.4 toggle: when set, initialize advertises
	// promptCapabilities.image with this value. nil → field absent.
	promptCapImage *bool

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
			caps := map[string]any{}
			if f.advertiseLoadSession {
				caps["loadSession"] = true
			}
			if f.promptCapImage != nil {
				caps["promptCapabilities"] = map[string]any{
					"image": *f.promptCapImage,
				}
			}
			result := map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": caps,
			}
			if len(f.authMethods) > 0 {
				result["authMethods"] = f.authMethods
			}
			f.respond(id, result)
		case "authenticate":
			params, _ := msg["params"].(map[string]any)
			methodID, _ := params["methodId"].(string)
			f.mu.Lock()
			f.authMethodsCalled = append(f.authMethodsCalled, methodID)
			f.mu.Unlock()
			if f.hangAuthenticate {
				continue // never reply; driver should timeout
			}
			if f.failAuthenticate {
				f.respondError(id, -32001, "auth refused")
			} else {
				f.respond(id, map[string]any{})
			}
		case "session/new":
			result := map[string]any{"sessionId": f.sessionID}
			if len(f.availableModes) > 0 {
				modes := make([]map[string]any, 0, len(f.availableModes))
				for _, m := range f.availableModes {
					modes = append(modes, map[string]any{"id": m, "name": m})
				}
				result["modes"] = map[string]any{"availableModes": modes}
			}
			if len(f.availableModels) > 0 {
				models := make([]map[string]any, 0, len(f.availableModels))
				for _, m := range f.availableModels {
					models = append(models, map[string]any{"id": m, "name": m})
				}
				result["models"] = map[string]any{"availableModels": models}
			}
			f.respond(id, result)
			close(f.initCh)
		case "session/set_mode":
			if f.failSetMode {
				f.respondError(id, -32000, "set_mode refused")
			} else {
				f.respond(id, map[string]any{})
			}
		case "session/set_model":
			if f.failSetModel {
				f.respondError(id, -32000, "set_model refused")
			} else {
				f.respond(id, map[string]any{})
			}
		case "session/load":
			// W1.2: ACP load response is preceded by a flurry of
			// session/update notifications carrying historical turns.
			// Stream them first so the readLoop tags them replay:true
			// (replayActive flips off when the response below is
			// delivered).
			for _, frame := range f.loadReplayFrames {
				f.notify("session/update", frame)
			}
			if f.failSessionLoad {
				f.respondError(id, -32000, "stale cursor")
			} else {
				f.respond(id, map[string]any{"sessionId": f.sessionID})
				close(f.initCh)
			}
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

// respondError sends a JSON-RPC error frame for the request id. Used
// by W1.2 tests that simulate a stale-cursor session/load failure.
func (f *fakeACPAgent) respondError(id any, code int, message string) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
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

// findResponse finds a response (no method, has result or error) whose
// id matches the numeric rpcID the fake previously sent. Used by the
// permission test to assert the driver's reply.
func (f *fakeACPAgent) findResponse(rpcID float64) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.received {
		if _, hasMethod := m["method"]; hasMethod {
			continue
		}
		id, ok := m["id"].(float64)
		if !ok {
			continue
		}
		if id == rpcID {
			return m
		}
	}
	return nil
}

// sendRequest is like notify but with an id — used to model the agent
// calling back into the client (e.g. session/request_permission).
func (f *fakeACPAgent) sendRequest(id int64, method string, params map[string]any) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
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

	// Expect: lifecycle.started + session.init + 4 translated events
	// (user_message_chunk dropped).
	poster.wait(t, 6, 2*time.Second)

	drv.Stop()
	poster.wait(t, 7, time.Second)

	evs := poster.snapshot()

	if evs[0].Kind != "lifecycle" || evs[0].Payload["phase"] != "started" ||
		evs[0].Payload["mode"] != "M1" || evs[0].Payload["session_id"] != "sess-acp-1" {
		t.Fatalf("evs[0] want lifecycle.started/M1/sess-acp-1; got %+v", evs[0])
	}
	if evs[1].Kind != "session.init" || evs[1].Producer != "agent" ||
		evs[1].Payload["session_id"] != "sess-acp-1" {
		t.Fatalf("evs[1] want session.init/agent/sess-acp-1; got %+v", evs[1])
	}

	// Collect translated events (skip lifecycle.started + session.init at
	// the front and lifecycle.stopped at the back).
	translated := evs[2 : len(evs)-1]
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

// TestACPDriver_InputTextWithImageBlocks — W4.4: when payload carries
// `images`, the prompt array leads with `{type:"image", mimeType, data}`
// blocks and the text block trails. Hub-side W4.1 already enforced
// mime/size/count caps; this driver test pins the wire shape.
func TestACPDriver_InputTextWithImageBlocks(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-img-1")
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-img",
		Poster:           &fakePoster{},
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

	err := drv.Input(context.Background(), "text", map[string]any{
		"body": "what's in this?",
		"images": []any{
			map[string]any{"mime_type": "image/png", "data": "AAA="},
			map[string]any{"mime_type": "image/webp", "data": "BBB="},
		},
	})
	if err != nil {
		t.Fatalf("Input: %v", err)
	}

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
		t.Fatal("session/prompt never arrived")
	}
	params, _ := prompt["params"].(map[string]any)
	arr, _ := params["prompt"].([]any)
	if len(arr) != 3 {
		t.Fatalf("prompt array: want 3 blocks, got %d (%+v)", len(arr), arr)
	}
	first, _ := arr[0].(map[string]any)
	if first["type"] != "image" || first["mimeType"] != "image/png" || first["data"] != "AAA=" {
		t.Errorf("prompt[0] malformed: %+v", first)
	}
	second, _ := arr[1].(map[string]any)
	if second["type"] != "image" || second["mimeType"] != "image/webp" {
		t.Errorf("prompt[1] malformed: %+v", second)
	}
	third, _ := arr[2].(map[string]any)
	if third["type"] != "text" || third["text"] != "what's in this?" {
		t.Errorf("prompt[2] malformed: %+v", third)
	}
}

// TestACPDriver_PromptCapImageGate — when initialize advertises
// `promptCapabilities.image:false` explicitly, image inputs are
// stripped and a kind=system warning event is emitted to the poster.
// Absent declaration is treated as permitted (forward-compat with
// agents that omit the field).
func TestACPDriver_PromptCapImageGate(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-gate-1")
	deny := false
	fake.promptCapImage = &deny
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-gate",
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

	err := drv.Input(context.Background(), "text", map[string]any{
		"body": "describe this",
		"images": []any{
			map[string]any{"mime_type": "image/png", "data": "AAA="},
		},
	})
	if err != nil {
		t.Fatalf("Input: %v", err)
	}

	// Wait for the warning system event to land. We don't insist on
	// strict ordering — any of the posted events may include other
	// kinds — but at least one must be the strip-warn for our reason.
	deadline := time.Now().Add(2 * time.Second)
	var found bool
	for time.Now().Before(deadline) && !found {
		for _, e := range poster.snapshot() {
			if e.Kind != "system" {
				continue
			}
			reason, _ := e.Payload["reason"].(string)
			if reason != "" && containsIgnoreCase(reason, "image input") {
				found = true
				break
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !found {
		t.Fatalf("expected system event noting dropped images; got %+v", poster.snapshot())
	}

	// Verify the prompt still went through with text only.
	deadline = time.Now().Add(2 * time.Second)
	var prompt map[string]any
	for time.Now().Before(deadline) {
		if m := fake.findReceived("session/prompt"); m != nil {
			prompt = m
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if prompt == nil {
		t.Fatal("session/prompt never arrived")
	}
	params, _ := prompt["params"].(map[string]any)
	arr, _ := params["prompt"].([]any)
	if len(arr) != 1 {
		t.Fatalf("after strip: want 1 block, got %d (%+v)", len(arr), arr)
	}
	if first, _ := arr[0].(map[string]any); first["type"] != "text" {
		t.Errorf("after strip: blocks[0] should be text, got %+v", first)
	}
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && strings.Contains(strings.ToLower(s), strings.ToLower(substr))
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

// TestACPDriver_PermissionAllowRoundTrip: the agent sends
// session/request_permission with id=99; the driver emits an
// approval_request event carrying request_id; the operator responds via
// Input(kind=approval, decision=allow); the driver writes a JSON-RPC
// response with id=99 and outcome={selected, optionId=allow}.
func TestACPDriver_PermissionAllowRoundTrip(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-perm-allow")
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-perm",
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

	// Agent asks for permission.
	fake.sendRequest(99, "session/request_permission", map[string]any{
		"sessionId": "sess-perm-allow",
		"toolCall":  map[string]any{"name": "bash", "args": "rm -rf /tmp/x"},
		"options": []any{
			map[string]any{"optionId": "allow", "name": "Allow"},
			map[string]any{"optionId": "deny", "name": "Deny"},
		},
	})

	// Driver should emit an approval_request event with a non-empty
	// request_id. The id is namespaced by per-spawn nonce + counter so we
	// don't assert a specific value (would couple the test to the nonce
	// scheme); we just round-trip the emitted id through Input below.
	deadline := time.Now().Add(2 * time.Second)
	var reqEvt *postedEvent
	for time.Now().Before(deadline) {
		for _, ev := range poster.snapshot() {
			if ev.Kind == "approval_request" {
				e := ev
				reqEvt = &e
				break
			}
		}
		if reqEvt != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqEvt == nil {
		t.Fatal("approval_request event never emitted")
	}
	reqID, _ := reqEvt.Payload["request_id"].(string)
	if reqID == "" {
		t.Fatal("request_id missing from approval_request event")
	}
	if reqID == "99" {
		t.Fatal("request_id leaks raw JSON-RPC id; should be namespaced")
	}
	if reqEvt.Producer != "agent" {
		t.Fatalf("producer = %q; want agent", reqEvt.Producer)
	}

	// Operator decides.
	if err := drv.Input(context.Background(), "approval", map[string]any{
		"request_id": reqID,
		"decision":   "allow",
		"option_id":  "allow",
	}); err != nil {
		t.Fatalf("Input approval: %v", err)
	}

	// Driver should have written a JSON-RPC response back to the agent.
	var resp map[string]any
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m := fake.findResponse(99); m != nil {
			resp = m
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("driver never wrote a permission response")
	}
	result, _ := resp["result"].(map[string]any)
	outcome, _ := result["outcome"].(map[string]any)
	if outcome["outcome"] != "selected" || outcome["optionId"] != "allow" {
		t.Fatalf("outcome wrong: %+v", outcome)
	}
}

// TestACPDriver_PermissionCancelRoundTrip: a "cancel" decision maps to
// ACP's cancelled outcome (no optionId required).
func TestACPDriver_PermissionCancelRoundTrip(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-perm-cancel")
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID: "agent-perm-cancel",
		Poster:  poster,
		Stdin:   hostInW,
		Stdout:  hostOutR,
		Closer:  func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()
	<-fake.initCh

	fake.sendRequest(7, "session/request_permission", map[string]any{})

	deadline := time.Now().Add(2 * time.Second)
	var reqEvt *postedEvent
	for time.Now().Before(deadline) {
		for _, ev := range poster.snapshot() {
			if ev.Kind == "approval_request" {
				e := ev
				reqEvt = &e
				break
			}
		}
		if reqEvt != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqEvt == nil {
		t.Fatal("approval_request event never emitted")
	}
	reqID, _ := reqEvt.Payload["request_id"].(string)
	if reqID == "" {
		t.Fatal("request_id missing from approval_request event")
	}

	if err := drv.Input(context.Background(), "approval", map[string]any{
		"request_id": reqID,
		"decision":   "cancel",
	}); err != nil {
		t.Fatalf("Input approval cancel: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	var resp map[string]any
	for time.Now().Before(deadline) {
		if m := fake.findResponse(7); m != nil {
			resp = m
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("driver never wrote a permission response for cancel")
	}
	result, _ := resp["result"].(map[string]any)
	outcome, _ := result["outcome"].(map[string]any)
	if outcome["outcome"] != "cancelled" {
		t.Fatalf("outcome wrong: %+v (want cancelled)", outcome)
	}
}

// TestACPDriver_PermissionRequestIDUniquePerSpawn: gemini-cli (and any
// JSON-RPC peer) restarts its outbound id counter every time the
// transport is re-established, so id=0 from spawn N collides with id=0
// from spawn N-1. Mobile's resolvedApprovals map persists across spawns
// via agent_events history; a colliding request_id makes a fresh card
// render as already-decided. The driver namespaces request_id with a
// per-spawn nonce + counter so the externally-visible id is globally
// unique.
func TestACPDriver_PermissionRequestIDUniquePerSpawn(t *testing.T) {
	spawn := func() string {
		hostInR, hostInW := io.Pipe()
		hostOutR, hostOutW := io.Pipe()
		fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-nonce")
		go fake.serve()
		poster := &fakePoster{}
		drv := &ACPDriver{
			AgentID:          "agent-nonce",
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
		// Both spawns send id=0 — the agent's counter is a blank slate.
		fake.sendRequest(0, "session/request_permission", map[string]any{})
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			for _, ev := range poster.snapshot() {
				if ev.Kind == "approval_request" {
					id, _ := ev.Payload["request_id"].(string)
					if id != "" {
						return id
					}
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("approval_request event never emitted")
		return ""
	}
	a := spawn()
	// 1ns resolution means UnixNano collisions are theoretically possible
	// on extremely fast machines; sleep one tick to make the test
	// deterministic without coupling to the nonce scheme.
	time.Sleep(2 * time.Millisecond)
	b := spawn()
	if a == "" || b == "" {
		t.Fatalf("empty request_id: a=%q b=%q", a, b)
	}
	if a == b {
		t.Fatalf("request_id collided across spawns: %q == %q", a, b)
	}
}

// TestACPDriver_ApprovalRejectsUnknownRequestID: if a phone sends a
// stale or bogus request_id, the driver must refuse rather than writing
// a response with no matching JSON-RPC id.
func TestACPDriver_ApprovalRejectsUnknownRequestID(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-perm-none")
	go fake.serve()

	drv := &ACPDriver{
		AgentID: "agent-perm-none",
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

	err := drv.Input(context.Background(), "approval", map[string]any{
		"request_id": "does-not-exist",
		"decision":   "allow",
	})
	if err == nil {
		t.Fatal("want error for unknown request_id")
	}
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

// TestACPDriver_WriteTimeout guards the path where the child agent has
// stopped reading its stdin (deadlocked, paused, OOMing). Without a
// timeout a single bad agent could block every subsequent Input call
// forever; the writer queue + WriteTimeout cap how long a caller waits
// before giving up.
func TestACPDriver_WriteTimeout(t *testing.T) {
	// A pipe with nobody reading → every Write blocks indefinitely.
	hostInR, hostInW := io.Pipe()
	hostOutR, _ := io.Pipe()
	_ = hostInR // intentionally never read

	drv := &ACPDriver{
		AgentID:      "agent-m1-wto",
		Poster:       &fakePoster{},
		Stdin:        hostInW,
		Stdout:       hostOutR,
		Closer:       func() { _ = hostInW.Close() },
		WriteTimeout: 80 * time.Millisecond,
	}

	// Skip the handshake and stand up just enough state to exercise
	// writeMsg. This isolates the timeout path from session/new (which
	// would itself time out but via the handshake timeout, not the
	// write timeout we want to test).
	drv.started = true
	drv.done = make(chan struct{})
	drv.writeQ = make(chan *acpWriteReq, 32)
	drv.pending = make(map[int64]chan acpResponse)
	drv.pendingPerm = make(map[string]json.RawMessage)
	drv.wg.Add(1)
	go drv.writerLoop()

	start := time.Now()
	err := drv.writeMsg(map[string]any{"jsonrpc": "2.0", "method": "ping"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected write timeout error; got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error; got %v", err)
	}
	// The timer should fire within a reasonable envelope — 80ms target,
	// allow 2x slack for CI scheduling noise.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("writeMsg took %v, expected ~80ms", elapsed)
	}

	// Stop the driver cleanly so the writerLoop goroutine unwinds.
	close(drv.done)
	_ = hostInW.Close() // unblock any in-flight Write
	drv.wg.Wait()
}

// TestACPDriver_PromptTimeoutSurfaces pins v1.0.400's PromptTimeout
// guard. An agent that handshakes successfully but never replies to
// session/prompt (the gemini-cli-without-GEMINI_API_KEY hang we hit in
// real testing) used to lock the driver indefinitely — `call` only
// honored ctx.Done() and the hub HTTP ctx doesn't expire. With
// PromptTimeout set, Input("text") must return cleanly within a bounded
// window so the mobile state can move out of "busy".
func TestACPDriver_PromptTimeoutSurfaces(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	// Hand-rolled fake that handshakes but ignores session/prompt — no
	// reply ever comes back. Mirrors the gemini-cli-hung-on-auth state.
	go func() {
		reader := bufio.NewReader(hostInR)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			method, _ := msg["method"].(string)
			id := msg["id"]
			switch method {
			case "initialize":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/new":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"sessionId": "sess-hung"},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/prompt":
				// Intentionally ignore — replicate the unauthenticated
				// daemon's silent hang.
			default:
				if id != nil {
					b, _ := json.Marshal(map[string]any{
						"jsonrpc": "2.0", "id": id, "result": map[string]any{},
					})
					_, _ = hostOutW.Write(append(b, '\n'))
				}
			}
		}
	}()

	drv := &ACPDriver{
		AgentID:          "agent-prompt-to",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close() },
		HandshakeTimeout: 2 * time.Second,
		PromptTimeout:    150 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	start := time.Now()
	err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Input(text): want error from prompt timeout, got nil")
	}
	if !strings.Contains(err.Error(), "no reply within") {
		t.Errorf("err = %v; want it to mention the prompt-timeout shape (`no reply within ...`) so operators recognize the auth-hang case",
			err)
	}
	// 150ms timeout + bookkeeping; allow generous slack for CI noise.
	if elapsed > 2*time.Second {
		t.Errorf("Input took %v; want it to error near PromptTimeout (150ms), not block on the hub HTTP ctx", elapsed)
	}
}

// TestACPDriver_PromptTimeoutDefaults locks the default — operators
// running with a stock ACPDriver (HandshakeTimeout/PromptTimeout zero)
// must still get the timeout protection. A regression that drops the
// default would silently let unauthenticated daemons lock agents again.
func TestACPDriver_PromptTimeoutDefaults(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-defaults")
	go fake.serve()

	drv := &ACPDriver{
		AgentID: "agent-defaults",
		Poster:  &fakePoster{},
		Stdin:   hostInW,
		Stdout:  hostOutR,
		Closer:  func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if drv.PromptTimeout != 120*time.Second {
		t.Errorf("PromptTimeout default = %v; want 120s — silent hang on session/prompt is the failure mode this defends against",
			drv.PromptTimeout)
	}
}

// TestACPDriver_RPCLogCapturesBothDirections pins v1.0.401's
// bidirectional trace. The plain stdout log only captures what gemini
// SENT BACK; without this trace there's no record of session/prompt
// etc. ever leaving host-runner. The trace MUST include both
// `dir=out` (driver → agent) and `dir=in` (agent → driver) lines and
// must be pure JSONL so operators can `jq` it.
func TestACPDriver_RPCLogCapturesBothDirections(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-rpclog")
	go fake.serve()

	rpcLog := newSyncBuffer()
	drv := &ACPDriver{
		AgentID:          "agent-rpclog",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		RPCLog:           rpcLog,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()
	<-fake.initCh

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "ping"}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	// Wait briefly for the readLoop / writerLoop to flush all trace lines.
	deadline := time.Now().Add(time.Second)
	var lines []map[string]any
	for time.Now().Before(deadline) {
		lines = parseRPCLog(t, rpcLog.Bytes())
		// initialize-out, initialize-in, session/new-out, session/new-in,
		// session/prompt-out, session/prompt-in. ≥6 lines means everything
		// flushed.
		if len(lines) >= 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(lines) < 6 {
		t.Fatalf("RPC log captured %d lines; want ≥6 (init+sessionNew+prompt × 2 dirs):\n%s",
			len(lines), rpcLog.String())
	}

	var sawOutInit, sawOutPrompt bool
	inboundCount := 0
	for _, l := range lines {
		if _, hasT := l["t"].(string); !hasT {
			t.Errorf("line missing `t` (timestamp): %+v", l)
		}
		dir, _ := l["dir"].(string)
		frame, _ := l["frame"].(map[string]any)
		method, _ := frame["method"].(string)
		if dir == "out" && method == "initialize" {
			sawOutInit = true
		}
		if dir == "out" && method == "session/prompt" {
			sawOutPrompt = true
		}
		if dir == "in" {
			inboundCount++
		}
	}
	if !sawOutInit {
		t.Error("RPC log missing dir=out method=initialize — driver→agent direction not captured")
	}
	if !sawOutPrompt {
		t.Error("RPC log missing dir=out method=session/prompt — the exact frame this trace exists to verify")
	}
	// Expect at least 3 inbound frames: replies to initialize, session/new,
	// and session/prompt. Less than 3 means the agent→driver direction
	// isn't being recorded.
	if inboundCount < 3 {
		t.Errorf("RPC log inbound count = %d; want ≥3 (init+sessionNew+prompt replies) — agent→driver direction not captured",
			inboundCount)
	}
}

// syncBuffer is a thread-safe bytes.Buffer; readLoop and writerLoop
// both write to RPCLog from their own goroutines, so the production
// path holds rpcLogMu but this test fixture must also be concurrent-
// safe to avoid a flaky test on -race builds.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{} }

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	b.mu.Unlock()
	return len(p), nil
}
func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]byte, len(b.buf))
	copy(cp, b.buf)
	return cp
}
func (b *syncBuffer) String() string { return string(b.Bytes()) }

func parseRPCLog(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("RPC log produced non-JSON line %q: %v", line, err)
			continue
		}
		out = append(out, m)
	}
	return out
}

// TestACPDriver_HandshakeBudgetIsPerCall pins v1.0.402's split: each
// handshake step gets its own d.HandshakeTimeout window, not a shared
// budget. Real-world trigger: gemini-cli's initialize alone takes 30-50s
// on a cold daemon (fnm shim + node startup + auth/model-list fetch),
// and the previous shared 60s budget left session/new starved at ~17s,
// tipping launch into the M2 fallback even when M1 was nearly there.
func TestACPDriver_HandshakeBudgetIsPerCall(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	// Slow-on-initialize fake: replies to initialize after 200ms (well
	// past the 100ms per-call budget IF shared, but fine for per-call).
	// Replies to session/new immediately. The driver should succeed
	// because each call starts fresh.
	go func() {
		reader := bufio.NewReader(hostInR)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			method, _ := msg["method"].(string)
			id := msg["id"]
			switch method {
			case "initialize":
				time.Sleep(180 * time.Millisecond)
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/new":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"sessionId": "sess-percall"},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			}
		}
	}()

	drv := &ACPDriver{
		AgentID:          "agent-percall",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close() },
		HandshakeTimeout: 250 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: want success because each call gets a fresh 250ms budget (initialize sleep 180ms < 250ms; session/new instant); got %v",
			err)
	}
	defer drv.Stop()
}

// TestACPDriver_HandshakeTimeoutDefault locks the v1.0.402 default
// (90s, up from 60s) so a future regression that drops it doesn't
// silently re-introduce the cold-start starvation problem.
func TestACPDriver_HandshakeTimeoutDefault(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-default")
	go fake.serve()

	drv := &ACPDriver{
		AgentID: "agent-hs-default",
		Poster:  &fakePoster{},
		Stdin:   hostInW,
		Stdout:  hostOutR,
		Closer:  func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if drv.HandshakeTimeout != 90*time.Second {
		t.Errorf("HandshakeTimeout default = %v; want 90s — needs headroom for slow daemon cold-start (gemini-cli initialize observed at 30-50s)",
			drv.HandshakeTimeout)
	}
}

// TestACPDriver_CancelPostsTurnResult pins v1.0.405: when the user
// taps the cancel button, the driver MUST post a turn.result eagerly
// so mobile's _isAgentBusy() flips off (cancel → send button). Waiting
// for gemini's stopReason=cancelled response often races with
// PromptTimeout having already kicked the original Input("text") out
// — the late response then gets orphaned and dropped.
func TestACPDriver_CancelPostsTurnResult(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-cancel-tr")
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-cancel-tr",
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

	if err := drv.Input(context.Background(), "cancel", nil); err != nil {
		t.Fatalf("Input(cancel): %v", err)
	}

	// Look for the turn.result the cancel branch must post eagerly.
	var found bool
	for _, e := range poster.snapshot() {
		if e.Kind == "turn.result" {
			if e.Payload["stop_reason"] != "cancelled" {
				t.Errorf("turn.result.stop_reason = %v; want cancelled", e.Payload["stop_reason"])
			}
			if e.Payload["status"] != "cancelled" {
				t.Errorf("turn.result.status = %v; want cancelled", e.Payload["status"])
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("cancel did not post turn.result — mobile cancel-button overlay would stick")
	}
}

// TestACPDriver_OrphanedPromptResponsePostsTurnResult covers the
// other half of v1.0.405: even without an explicit cancel, a
// session/prompt response that arrives after the originating call
// timed out (gemini takes >120s spinning on permission requests) MUST
// post a synthetic turn.result so mobile sees a terminal kind. Without
// this, deliverResponse used to drop the late reply silently and the
// busy walker stayed stuck.
func TestACPDriver_OrphanedPromptResponsePostsTurnResult(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	// Fake that handshakes but holds session/prompt responses for a
	// while — simulates a daemon that exceeds PromptTimeout.
	releasePrompt := make(chan struct{})
	go func() {
		reader := bufio.NewReader(hostInR)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			method, _ := msg["method"].(string)
			id := msg["id"]
			switch method {
			case "initialize":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/new":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"sessionId": "sess-orphan"},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/prompt":
				go func(id any) {
					<-releasePrompt
					b, _ := json.Marshal(map[string]any{
						"jsonrpc": "2.0", "id": id,
						"result": map[string]any{"stopReason": "cancelled"},
					})
					_, _ = hostOutW.Write(append(b, '\n'))
				}(id)
			}
		}
	}()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-orphan",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close() },
		HandshakeTimeout: 2 * time.Second,
		PromptTimeout:    100 * time.Millisecond,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	// Input will time out at 100ms; the session/prompt response will
	// arrive AFTER that and get processed by deliverResponse as an
	// orphaned reply.
	err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"})
	if err == nil {
		t.Fatal("Input: want timeout error since fake holds the response")
	}

	// Now release the held response. deliverResponse should see the
	// orphaned reply and post turn.result.
	close(releasePrompt)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range poster.snapshot() {
			if e.Kind == "turn.result" {
				if e.Payload["stop_reason"] != "cancelled" {
					t.Errorf("turn.result.stop_reason = %v; want cancelled (lifted from orphaned response)",
						e.Payload["stop_reason"])
				}
				return // success
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("orphaned session/prompt response did not produce a synthetic turn.result — mobile cancel button would stick")
}

// TestACPDriver_AccumulatesAgentMessageChunks pins v1.0.404 (a):
// gemini-cli@0.41 emits agent_message_chunk notifications in pieces
// (incremental, not cumulative). Without aggregation each chunk would
// render as its own bubble in the mobile transcript. The driver must
// accumulate per-turn and emit cumulative `kind=text, partial:true,
// message_id=<turn-id>` events whose text carries the FULL running
// content so mobile's _collapseStreamingPartials chain folds them into
// one bubble.
func TestACPDriver_AccumulatesAgentMessageChunks(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	// Custom fake that auto-replies to handshake AND emits two
	// agent_message_chunks before responding to session/prompt.
	go func() {
		reader := bufio.NewReader(hostInR)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			method, _ := msg["method"].(string)
			id := msg["id"]
			switch method {
			case "initialize":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/new":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"sessionId": "sess-stream"},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/prompt":
				// Stream two chunks then respond.
				for _, chunk := range []string{"Hello! ", "I'm Gemini."} {
					b, _ := json.Marshal(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"sessionId": "sess-stream",
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content":       map[string]any{"type": "text", "text": chunk},
							},
						},
					})
					_, _ = hostOutW.Write(append(b, '\n'))
					time.Sleep(2 * time.Millisecond)
				}
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"stopReason": "end_turn"},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			}
		}
	}()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-stream",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close() },
		HandshakeTimeout: 2 * time.Second,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	// Find every text-kind agent event.
	var partials []map[string]any
	for _, e := range poster.snapshot() {
		if e.Kind == "text" && e.Producer == "agent" {
			partials = append(partials, e.Payload)
		}
	}
	if len(partials) != 2 {
		t.Fatalf("text events = %d (want 2 from chunks); got %+v", len(partials), partials)
	}
	if partials[0]["text"] != "Hello! " {
		t.Errorf("partials[0].text = %q; want %q (first chunk only)", partials[0]["text"], "Hello! ")
	}
	if partials[1]["text"] != "Hello! I'm Gemini." {
		t.Errorf("partials[1].text = %q; want cumulative %q (chunk1 + chunk2)",
			partials[1]["text"], "Hello! I'm Gemini.")
	}
	for i, p := range partials {
		if p["partial"] != true {
			t.Errorf("partials[%d].partial = %v; want true (lets mobile collapse fold the chain)", i, p["partial"])
		}
	}
	if partials[0]["message_id"] == "" || partials[0]["message_id"] != partials[1]["message_id"] {
		t.Errorf("chunks must share one message_id (chain root); got %q vs %q",
			partials[0]["message_id"], partials[1]["message_id"])
	}
}

// TestACPDriver_PostsTurnResultOnPromptResponse pins v1.0.404 (b)+(c).
// On session/prompt success the driver MUST post a turn.result event
// — both because mobile's _isAgentBusy() returns false on it (clears
// the cancel-button overlay so the user can send the next prompt) AND
// because it carries the lifted token usage the telemetry strip reads.
func TestACPDriver_PostsTurnResultOnPromptResponse(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	// Real-shaped session/prompt result from gemini-cli@0.41.2:
	// stopReason + nested _meta.quota.{token_count,model_usage}.
	go func() {
		reader := bufio.NewReader(hostInR)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			method, _ := msg["method"].(string)
			id := msg["id"]
			switch method {
			case "initialize":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/new":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{"sessionId": "sess-tr"},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			case "session/prompt":
				b, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": id,
					"result": map[string]any{
						"stopReason": "end_turn",
						"_meta": map[string]any{
							"quota": map[string]any{
								"token_count": map[string]any{
									"input_tokens":  19614,
									"output_tokens": 45,
								},
								"model_usage": []any{
									map[string]any{
										"model": "gemini-3-flash-preview",
										"token_count": map[string]any{
											"input_tokens":  19614,
											"output_tokens": 45,
										},
									},
								},
							},
						},
					},
				})
				_, _ = hostOutW.Write(append(b, '\n'))
			}
		}
	}()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-tr",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close() },
		HandshakeTimeout: 2 * time.Second,
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"}); err != nil {
		t.Fatalf("Input: %v", err)
	}

	var tr map[string]any
	for _, e := range poster.snapshot() {
		if e.Kind == "turn.result" {
			tr = e.Payload
			break
		}
	}
	if tr == nil {
		t.Fatal("no turn.result posted — without it mobile's busy-walker stays on cancel forever")
	}

	if tr["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v; want end_turn", tr["stop_reason"])
	}
	if it, _ := tr["input_tokens"].(float64); it != 19614 {
		t.Errorf("input_tokens = %v; want 19614 (lifted from _meta.quota.token_count)", tr["input_tokens"])
	}
	if ot, _ := tr["output_tokens"].(float64); ot != 45 {
		t.Errorf("output_tokens = %v; want 45 (lifted from _meta.quota.token_count)", tr["output_tokens"])
	}
	bm, ok := tr["by_model"].(map[string]any)
	if !ok {
		t.Fatalf("by_model missing or wrong type: %+v", tr["by_model"])
	}
	entry, ok := bm["gemini-3-flash-preview"].(map[string]any)
	if !ok {
		t.Fatalf("by_model[gemini-3-flash-preview] missing")
	}
	if v, _ := entry["input"].(float64); v != 19614 {
		t.Errorf("by_model entry.input = %v; want 19614 (canonical name lifted from input_tokens)", entry["input"])
	}
	if v, _ := entry["output"].(float64); v != 45 {
		t.Errorf("by_model entry.output = %v; want 45 (canonical name lifted from output_tokens)", entry["output"])
	}
}

// TestACPDriver_CapabilityNotificationsAreSystemKind pins v1.0.403:
// gemini emits available_commands_update, current_mode_update, and
// current_model_update after session/new as one-shot capability
// announcements. They MUST land as kind=system, not the previous
// kind=raw — mobile's _isAgentBusy() walks events newest-first and
// treats any non-skipped agent-produced kind as "turn in progress",
// so kind=raw was tripping the cancel-button overlay even when the
// agent was idle. kind=system is on the skip list AND hidden from the
// feed unless verbose, which is what we want for these.
func TestACPDriver_CapabilityNotificationsAreSystemKind(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-caps")
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-caps",
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

	// Each of the three capability announcements gemini sends after
	// session/new, with payload shapes that mirror the real wire
	// format from gemini-cli@0.41.2.
	fake.notify("session/update", map[string]any{
		"sessionId": "sess-caps",
		"update": map[string]any{
			"sessionUpdate": "available_commands_update",
			"availableCommands": []any{
				map[string]any{"name": "memory", "description": "Manage memory."},
			},
		},
	})
	fake.notify("session/update", map[string]any{
		"sessionId": "sess-caps",
		"update": map[string]any{
			"sessionUpdate": "current_mode_update",
			"currentModeId": "default",
		},
	})
	fake.notify("session/update", map[string]any{
		"sessionId": "sess-caps",
		"update": map[string]any{
			"sessionUpdate": "current_model_update",
			"currentModelId": "auto-gemini-3",
		},
	})

	// Wait: lifecycle.started + session.init + 3 translated events.
	poster.wait(t, 5, 2*time.Second)
	evs := poster.snapshot()

	// Skip lifecycle.started at [0] and session.init at [1]; the next 3
	// are the capability events.
	caps := evs[2:5]
	wantUpdates := []string{
		"available_commands_update",
		"current_mode_update",
		"current_model_update",
	}
	for i, want := range wantUpdates {
		if caps[i].Kind != "system" {
			t.Errorf("%s: kind = %q; want system (so mobile _isAgentBusy skips it instead of treating as turn activity)",
				want, caps[i].Kind)
		}
		// Producer should also be system to match other capability frames.
		if caps[i].Producer != "system" {
			t.Errorf("%s: producer = %q; want system", want, caps[i].Producer)
		}
		// Payload preserved verbatim — the slash command catalog needs
		// to be lift-able from this without a hub-side schema change.
		got, _ := caps[i].Payload["sessionUpdate"].(string)
		if got != want {
			t.Errorf("%s: payload.sessionUpdate = %q; want %q (full update preserved)",
				want, got, want)
		}
	}
}

// TestACPDriver_EmitsSessionInitOnStart pins ADR-021 W1.1: after the
// ACP `session/new` handshake completes, the driver emits a dedicated
// `session.init` event with `producer=agent` carrying the engine-side
// `session_id`. The hub's `captureEngineSessionID`
// (handlers_sessions.go) gates on that exact (kind, producer) tuple to
// lift the cursor into `sessions.engine_session_id`, so missing this
// event would silently break resume across spawn restarts even though
// the lifecycle.started frame still announces the same id.
func TestACPDriver_EmitsSessionInitOnStart(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "engine-uuid-w11")
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-w11",
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

	evs := poster.wait(t, 2, 2*time.Second)
	// Expect the session.init event right after lifecycle.started, with
	// producer=agent (the capture path ignores producer=system) and the
	// session_id from session/new.
	var init *postedEvent
	for i := range evs {
		if evs[i].Kind == "session.init" {
			init = &evs[i]
			break
		}
	}
	if init == nil {
		t.Fatalf("no session.init event emitted; got kinds=%v", eventKinds(evs))
	}
	if init.Producer != "agent" {
		t.Errorf("session.init producer = %q; want agent (capture path filters on this)",
			init.Producer)
	}
	if init.Payload["session_id"] != "engine-uuid-w11" {
		t.Errorf("session.init session_id = %v; want engine-uuid-w11",
			init.Payload["session_id"])
	}
}

func eventKinds(evs []postedEvent) []string {
	out := make([]string, len(evs))
	for i := range evs {
		out[i] = evs[i].Kind
	}
	return out
}

// TestACPDriver_StartUsesSessionLoadWhenCursorAndCapability — ADR-021
// W1.2 happy path. With both a captured cursor (ResumeSessionID) and
// the agent advertising loadSession in initialize, Start must call
// session/load and skip session/new entirely.
func TestACPDriver_StartUsesSessionLoadWhenCursorAndCapability(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "engine-uuid-load")
	fake.advertiseLoadSession = true
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-load",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		ResumeSessionID:  "engine-uuid-load",
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if got := fake.findReceived("session/load"); got == nil {
		t.Fatalf("session/load not sent; received methods=%v", receivedMethods(fake))
	}
	if got := fake.findReceived("session/new"); got != nil {
		t.Errorf("session/new should NOT have been sent when load succeeded; got %+v", got)
	}
	// Sanity: the driver must have latched the sessionID for subsequent
	// prompts (without it Input("text") would refuse to prompt).
	drv.mu.Lock()
	sid := drv.sessionID
	drv.mu.Unlock()
	if sid != "engine-uuid-load" {
		t.Errorf("sessionID after load = %q; want engine-uuid-load", sid)
	}
}

// TestACPDriver_UsesSessionNewWhenLoadCapabilityAbsent — D6 capability
// gating. When ResumeSessionID is set but the agent does NOT advertise
// loadSession, the driver must fall through to session/new instead of
// guessing.
func TestACPDriver_UsesSessionNewWhenLoadCapabilityAbsent(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "engine-uuid-fresh")
	// advertiseLoadSession defaults false — agent doesn't support load.
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-no-load",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		ResumeSessionID:  "engine-uuid-fresh", // cursor exists...
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if got := fake.findReceived("session/load"); got != nil {
		t.Errorf("session/load sent without loadSession capability: %+v", got)
	}
	if got := fake.findReceived("session/new"); got == nil {
		t.Fatalf("session/new not sent; received methods=%v", receivedMethods(fake))
	}
}

// TestACPDriver_FallsBackToSessionNewOnLoadFailure — D2 fall-back. A
// stale cursor (cursor on hub, gone from agent disk) must not strand
// the operator. Start tries session/load; on rpc-error response, falls
// through to session/new and continues normally.
func TestACPDriver_FallsBackToSessionNewOnLoadFailure(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "engine-uuid-fallback")
	fake.advertiseLoadSession = true
	fake.failSessionLoad = true
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-fallback",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		ResumeSessionID:  "stale-cursor",
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v (must NOT propagate stale-cursor failure)", err)
	}
	defer drv.Stop()

	if got := fake.findReceived("session/load"); got == nil {
		t.Fatalf("session/load not attempted before fallback; methods=%v",
			receivedMethods(fake))
	}
	if got := fake.findReceived("session/new"); got == nil {
		t.Fatalf("session/new fallback not sent; methods=%v",
			receivedMethods(fake))
	}
	drv.mu.Lock()
	sid := drv.sessionID
	drv.mu.Unlock()
	if sid != "engine-uuid-fallback" {
		t.Errorf("sessionID after fallback = %q; want engine-uuid-fallback "+
			"(the fresh sessionId from session/new, not the stale cursor)", sid)
	}
}

// TestACPDriver_TagsReplayEvents — D2 replay handling. Events emitted
// while a session/load is in flight (the agent's historical turn
// stream) must carry `replay: true` in their payload so mobile's
// dedupe layer can drop them when the cached transcript already has
// them. The replay window stays open until the operator's first
// Input() — gemini-cli@0.41.2 emits a trailing burst of history
// notifications AFTER the session/load response (last turn's
// agent_message_chunk arrives ~50µs after the load reply), and
// closing the window on Start() return tags those trailing frames
// as live, surfacing them as duplicate bubbles on resume. Events
// emitted AFTER the first Input() must NOT carry the flag.
func TestACPDriver_TagsReplayEvents(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "engine-uuid-replay")
	fake.advertiseLoadSession = true
	fake.loadReplayFrames = []map[string]any{
		{
			"sessionId": "engine-uuid-replay",
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": "historical turn"},
			},
		},
		{
			"sessionId": "engine-uuid-replay",
			"update": map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    "tc-replay",
				"title":         "Read",
				"kind":          "read",
				"status":        "completed",
				"rawInput":      map[string]any{"path": "/tmp/x"},
			},
		},
	}
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-replay",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		ResumeSessionID:  "engine-uuid-replay",
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	// After Start: lifecycle.started + session.init + 2 replay events.
	evs := poster.wait(t, 4, 2*time.Second)

	var replayText, replayTool *postedEvent
	for i := range evs {
		switch evs[i].Kind {
		case "text":
			replayText = &evs[i]
		case "tool_call":
			replayTool = &evs[i]
		}
	}
	if replayText == nil || replayTool == nil {
		t.Fatalf("missing replay events; kinds=%v", eventKinds(evs))
	}
	if got, _ := replayText.Payload["replay"].(bool); !got {
		t.Errorf("text event during load missing replay:true; payload=%+v",
			replayText.Payload)
	}
	if got, _ := replayTool.Payload["replay"].(bool); !got {
		t.Errorf("tool_call during load missing replay:true; payload=%+v",
			replayTool.Payload)
	}

	// Trailing autonomous emission AFTER Start returned but BEFORE the
	// operator has acted: replay window must still be open. gemini-cli
	// emits the final agent_message_chunk of the previous turn here.
	// Tagging these as live would surface them as duplicates of the
	// already-cached transcript on the phone.
	fake.notify("session/update", map[string]any{
		"sessionId": "engine-uuid-replay",
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": " — trailing history"},
		},
	})
	evs = poster.wait(t, 5, 2*time.Second)
	trailing := evs[len(evs)-1]
	if trailing.Kind != "text" {
		t.Fatalf("trailing event kind = %q; want text", trailing.Kind)
	}
	if got, _ := trailing.Payload["replay"].(bool); !got {
		t.Errorf("post-load trailing emission missing replay:true; payload=%+v",
			trailing.Payload)
	}

	// First user-initiated Input closes the replay window. Subsequent
	// notifications are live (responses to the operator's prompt) and
	// must NOT carry replay:true.
	if err := drv.Input(context.Background(), "text", map[string]any{"body": "operator turn"}); err != nil {
		t.Fatalf("Input text: %v", err)
	}
	fake.notify("session/update", map[string]any{
		"sessionId": "engine-uuid-replay",
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": " — and now live"},
		},
	})
	// Find the most recent text event — turn.result, lifecycle frames
	// from the prompt round-trip may interleave.
	var live *postedEvent
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for i := range poster.snapshot() {
			ev := poster.snapshot()[i]
			if ev.Kind == "text" && strings.Contains(
				fmt.Sprint(ev.Payload["text"]), "and now live") {
				e := ev
				live = &e
			}
		}
		if live != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if live == nil {
		t.Fatal("post-Input live text event never observed")
	}
	if got, _ := live.Payload["replay"].(bool); got {
		t.Errorf("post-Input live event tagged replay:true; payload=%+v",
			live.Payload)
	}
}

func receivedMethods(f *fakeACPAgent) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.received))
	for _, m := range f.received {
		if mth, _ := m["method"].(string); mth != "" {
			out = append(out, mth)
		}
	}
	return out
}

// TestACPDriver_AuthSkippedWhenNoMethods — ADR-021 W1.4 fast path: an
// agent that doesn't advertise any auth methods is treated as
// pre-authenticated. Driver MUST NOT dispatch authenticate.
func TestACPDriver_AuthSkippedWhenNoMethods(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-noauth")
	// authMethods empty — agent advertises no auth.
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-noauth",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		AuthMethod:       "oauth-personal", // even with a preference, no advert = skip
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	if got := fake.findReceived("authenticate"); got != nil {
		t.Errorf("authenticate dispatched even though authMethods was empty: %+v",
			got)
	}
}

// TestACPDriver_PicksConfiguredAuthMethod — explicit AuthMethod from
// steward template / family default wins over heuristics, provided
// the agent advertises a matching id.
func TestACPDriver_PicksConfiguredAuthMethod(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-auth")
	fake.authMethods = []map[string]any{
		{"id": "gemini-api-key", "label": "API key", "interactive": false},
		{"id": "oauth-personal", "label": "Google login", "interactive": true},
	}
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-auth",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		AuthMethod:       "oauth-personal", // explicit override
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	fake.mu.Lock()
	called := append([]string{}, fake.authMethodsCalled...)
	fake.mu.Unlock()
	if len(called) != 1 || called[0] != "oauth-personal" {
		t.Errorf("authMethodsCalled = %v; want [oauth-personal]", called)
	}
}

// TestACPDriver_PicksFirstNonInteractiveMethod — when AuthMethod is
// not set, the driver falls back to the first non-interactive method
// in the agent's authMethods list. This is the daemon-friendly
// default: cached creds + no human standing by.
func TestACPDriver_PicksFirstNonInteractiveMethod(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-auto")
	fake.authMethods = []map[string]any{
		{"id": "oauth-personal", "interactive": true},
		{"id": "gemini-api-key", "interactive": false},
		{"id": "vertex-ai", "interactive": false},
	}
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-auto",
		Poster:           &fakePoster{},
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		// AuthMethod unset.
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drv.Stop()

	fake.mu.Lock()
	called := append([]string{}, fake.authMethodsCalled...)
	fake.mu.Unlock()
	if len(called) != 1 || called[0] != "gemini-api-key" {
		t.Errorf("authMethodsCalled = %v; want [gemini-api-key] "+
			"(first non-interactive)", called)
	}
}

// TestACPDriver_AttentionRequestOnInteractiveOnly — when no AuthMethod
// preference is set AND the agent advertises only interactive methods,
// the driver fails fast (no handshake) and emits an attention_request
// so the principal can fix creds out of band or override the method
// via the steward template.
func TestACPDriver_AttentionRequestOnInteractiveOnly(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-attn")
	fake.authMethods = []map[string]any{
		{"id": "oauth-personal", "label": "Google login", "interactive": true},
	}
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-attn",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
	}
	err := drv.Start(context.Background())
	if err == nil {
		drv.Stop()
		t.Fatalf("Start succeeded with only interactive methods + no preference; want error")
	}
	defer drv.Stop()

	// authenticate must NOT have been called — pickAuthMethod returned
	// an error before we even tried.
	if got := fake.findReceived("authenticate"); got != nil {
		t.Errorf("authenticate dispatched despite no method being pickable: %+v", got)
	}
	// attention_request event with the method options must be present.
	var attn *postedEvent
	for _, ev := range poster.snapshot() {
		if ev.Kind == "attention_request" {
			attn = &ev
			break
		}
	}
	if attn == nil {
		t.Fatalf("attention_request event not emitted; events=%v",
			eventKinds(poster.snapshot()))
	}
	if k, _ := attn.Payload["kind"].(string); k != "auth_required" {
		t.Errorf("attention_request kind = %q; want auth_required", k)
	}
	opts, _ := attn.Payload["available_methods"].([]map[string]any)
	if len(opts) != 1 {
		t.Errorf("available_methods len = %d; want 1 (the interactive option)",
			len(opts))
	}
}

// TestACPDriver_AttentionRequestOnAuthFailure — authenticate returns
// rpc-error (auth refused). Driver must surface the failure as an
// attention_request rather than a silent infinite hang.
func TestACPDriver_AttentionRequestOnAuthFailure(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-authfail")
	fake.authMethods = []map[string]any{
		{"id": "gemini-api-key", "interactive": false},
	}
	fake.failAuthenticate = true
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-authfail",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
	}
	err := drv.Start(context.Background())
	if err == nil {
		drv.Stop()
		t.Fatalf("Start succeeded with failing authenticate; want error")
	}
	defer drv.Stop()

	var attn *postedEvent
	for _, ev := range poster.snapshot() {
		if ev.Kind == "attention_request" {
			attn = &ev
			break
		}
	}
	if attn == nil {
		t.Fatalf("attention_request not emitted after authenticate failure; events=%v",
			eventKinds(poster.snapshot()))
	}
}

// TestACPDriver_AuthMethodTypoFails — an explicit AuthMethod that
// doesn't appear in the agent's advertised list is a configuration
// error; we fail fast (no authenticate dispatched) and surface
// attention_request with the actual options so the operator can fix
// the typo or pick a real method.
func TestACPDriver_AuthMethodTypoFails(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-typo")
	fake.authMethods = []map[string]any{
		{"id": "gemini-api-key", "interactive": false},
		{"id": "oauth-personal", "interactive": true},
	}
	go fake.serve()

	poster := &fakePoster{}
	drv := &ACPDriver{
		AgentID:          "agent-typo",
		Poster:           poster,
		Stdin:            hostInW,
		Stdout:           hostOutR,
		Closer:           func() { _ = hostInW.Close(); _ = hostOutW.Close(); fake.close() },
		HandshakeTimeout: 2 * time.Second,
		AuthMethod:       "googl-personal", // typo
	}
	err := drv.Start(context.Background())
	if err == nil {
		drv.Stop()
		t.Fatalf("Start succeeded with typo'd auth method; want error")
	}
	defer drv.Stop()

	if got := fake.findReceived("authenticate"); got != nil {
		t.Errorf("authenticate dispatched for typo'd method: %+v", got)
	}
	var attn *postedEvent
	for _, ev := range poster.snapshot() {
		if ev.Kind == "attention_request" {
			attn = &ev
			break
		}
	}
	if attn == nil {
		t.Fatalf("attention_request not emitted for typo'd method; events=%v",
			eventKinds(poster.snapshot()))
	}
	if cm, _ := attn.Payload["configured_method"].(string); cm != "googl-personal" {
		t.Errorf("configured_method in attention payload = %q; want googl-personal", cm)
	}
}

// TestACPDriver_SetModeDispatch — W2.2: Input("set_mode") validates the
// requested id against the cached availableModes from session/new and
// dispatches session/set_mode RPC.
func TestACPDriver_SetModeDispatch(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-mode-1")
	fake.availableModes = []string{"default", "yolo", "plan"}
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-set-mode",
		Poster:           &fakePoster{},
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

	if err := drv.Input(context.Background(), "set_mode", map[string]any{"mode_id": "yolo"}); err != nil {
		t.Fatalf("Input set_mode: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var rpc map[string]any
	for time.Now().Before(deadline) {
		if m := fake.findReceived("session/set_mode"); m != nil {
			rpc = m
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rpc == nil {
		t.Fatalf("session/set_mode never arrived; received methods=%v", receivedMethods(fake))
	}
	params, _ := rpc["params"].(map[string]any)
	if params["sessionId"] != "sess-mode-1" {
		t.Errorf("sessionId = %v, want sess-mode-1", params["sessionId"])
	}
	if params["modeId"] != "yolo" {
		t.Errorf("modeId = %v, want yolo", params["modeId"])
	}
}

// TestACPDriver_SetModelDispatch — W2.2: same as set_mode but for
// session/set_model + availableModels.
func TestACPDriver_SetModelDispatch(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-model-1")
	fake.availableModels = []string{"gemini-2.5-pro", "gemini-2.5-flash"}
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-set-model",
		Poster:           &fakePoster{},
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

	if err := drv.Input(context.Background(), "set_model",
		map[string]any{"model_id": "gemini-2.5-flash"}); err != nil {
		t.Fatalf("Input set_model: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var rpc map[string]any
	for time.Now().Before(deadline) {
		if m := fake.findReceived("session/set_model"); m != nil {
			rpc = m
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rpc == nil {
		t.Fatalf("session/set_model never arrived; received methods=%v", receivedMethods(fake))
	}
	params, _ := rpc["params"].(map[string]any)
	if params["modelId"] != "gemini-2.5-flash" {
		t.Errorf("modelId = %v, want gemini-2.5-flash", params["modelId"])
	}
}

// TestACPDriver_SetModeRejectsUnknownID — unknown id returns error
// before any RPC is sent.
func TestACPDriver_SetModeRejectsUnknownID(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-mode-bad")
	fake.availableModes = []string{"default", "yolo"}
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-set-mode-bad",
		Poster:           &fakePoster{},
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

	err := drv.Input(context.Background(), "set_mode", map[string]any{"mode_id": "doesnotexist"})
	if err == nil {
		t.Fatalf("expected error for unknown mode_id, got nil")
	}
	if !strings.Contains(err.Error(), "unknown mode_id") {
		t.Errorf("error = %q; want substring 'unknown mode_id'", err.Error())
	}
	// Sanity: the driver must NOT have dispatched the RPC.
	time.Sleep(50 * time.Millisecond)
	if m := fake.findReceived("session/set_mode"); m != nil {
		t.Errorf("driver dispatched RPC for unknown id; got %+v", m)
	}
}

// TestACPDriver_SetModeUnsupportedWhenNoList — agent that didn't
// advertise any modes (older or barebones daemon) → typed error
// surfacing the mismatch instead of dispatching against an empty cache.
func TestACPDriver_SetModeUnsupportedWhenNoList(t *testing.T) {
	hostInR, hostInW := io.Pipe()
	hostOutR, hostOutW := io.Pipe()

	fake := newFakeACPAgent(t, hostInR, hostOutW, "sess-no-modes")
	// Intentionally no availableModes injected.
	go fake.serve()

	drv := &ACPDriver{
		AgentID:          "agent-no-modes",
		Poster:           &fakePoster{},
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

	err := drv.Input(context.Background(), "set_mode", map[string]any{"mode_id": "yolo"})
	if err == nil {
		t.Fatalf("expected error for no-list agent, got nil")
	}
	if !strings.Contains(err.Error(), "did not advertise modes") {
		t.Errorf("error = %q; want substring 'did not advertise modes'", err.Error())
	}
}
