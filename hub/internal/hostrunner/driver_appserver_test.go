package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/termipod/hub/internal/agentfamilies"
)

// fakeAppServer plays the codex side of the JSON-RPC connection. It
// reads requests off the driver's stdin pipe, ships canned responses
// for the methods we care about, and lets the test inject
// notifications + server-initiated requests on demand.
type fakeAppServer struct {
	t       *testing.T
	in      io.Reader // driver-side stdin (we read what driver writes)
	out     io.Writer // driver-side stdout (we write what driver reads)
	mu      sync.Mutex
	got     []map[string]any
	respond map[string]func(req map[string]any) any
}

func newFakeAppServer(t *testing.T, in io.Reader, out io.Writer) *fakeAppServer {
	return &fakeAppServer{
		t:       t,
		in:      in,
		out:     out,
		respond: map[string]func(map[string]any) any{},
	}
}

// onCall registers a response factory for a given JSON-RPC method.
// The factory is invoked with the parsed request frame and returns
// the value that gets serialized as the `result` field. Returning
// nil sends `result: null`.
func (s *fakeAppServer) onCall(method string, fn func(map[string]any) any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond[method] = fn
}

func (s *fakeAppServer) seen() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, len(s.got))
	copy(out, s.got)
	return out
}

func (s *fakeAppServer) waitForMethod(method string, timeout time.Duration) map[string]any {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, f := range s.seen() {
			if m, _ := f["method"].(string); m == method {
				return f
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.t.Fatalf("fakeAppServer: timed out waiting for method %q (saw %v)",
		method, s.seen())
	return nil
}

// run loops over the inbound stream, recording every frame and
// dispatching to the registered response factory if the frame is a
// request. Notifications (no id) are recorded but not answered.
func (s *fakeAppServer) run() {
	sc := bufio.NewScanner(s.in)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var req map[string]any
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		s.mu.Lock()
		s.got = append(s.got, req)
		fn, hasFn := s.respond[stringField(req, "method")]
		s.mu.Unlock()
		if id, hasID := req["id"]; hasID && hasFn {
			result := fn(req)
			s.send(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  result,
			})
		}
	}
}

func (s *fakeAppServer) send(frame map[string]any) {
	b, _ := json.Marshal(frame)
	_, _ = s.out.Write(append(b, '\n'))
}

// notify sends a server-side notification (no id).
func (s *fakeAppServer) notify(method string, params any) {
	s.send(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

// serverRequest sends a server-initiated request — emulates the
// approval-request shape codex uses for item/*/requestApproval.
// Returns the id so the test can verify the driver responds.
func (s *fakeAppServer) serverRequest(id int64, method string, params any) {
	s.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
}

func stringField(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

// pipePair wires two io.Pipes into a single (driver-stdin, driver-stdout)
// pair: the driver writes to stdinW (server reads stdinR), and the
// server writes to stdoutW (driver reads stdoutR). Both pipes are
// closed via the returned Close func.
type pipePair struct {
	driverStdout *io.PipeReader
	serverWrite  *io.PipeWriter
	driverStdin  *io.PipeWriter
	serverRead   *io.PipeReader
	closeFn      func()
}

func newPipePair() pipePair {
	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()
	return pipePair{
		driverStdout: stdoutR,
		serverWrite:  stdoutW,
		driverStdin:  stdinW,
		serverRead:   stdinR,
		closeFn: func() {
			_ = stdoutR.Close()
			_ = stdoutW.Close()
			_ = stdinR.Close()
			_ = stdinW.Close()
		},
	}
}

// codexProfileForTest pulls the embedded codex profile so the driver
// translates notifications the same way production does. If the
// profile fails to load the test fails immediately — this is the
// load-bearing dependency from slice 2.
func codexProfileForTest(t *testing.T) *agentfamilies.FrameProfile {
	t.Helper()
	f, ok := agentfamilies.ByName("codex")
	if !ok || f.FrameProfile == nil {
		t.Fatal("codex frame_profile not embedded")
	}
	return f.FrameProfile
}

// TestAppServerDriver_HandshakeAndTurn pins the slice-3 happy path:
// initialize → initialized → thread/start → turn/start, with a
// matching set of notifications flowing back as agent_events. The
// production driver doesn't care about the exact wire bytes (the
// frame profile owns that); this test asserts the *protocol-level*
// shape that's the slice-3 contract.
func TestAppServerDriver_HandshakeAndTurn(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any {
		return map[string]any{"protocolVersion": "1.0"}
	})
	server.onCall("thread/start", func(_ map[string]any) any {
		// thread/started notification rides the same response — many
		// real servers send it as a separate notification but the
		// driver tolerates both, so test the more conservative path.
		return map[string]any{
			"thread": map[string]any{
				"id":            "thr_test",
				"createdAt":     "2026-04-29T10:00:00Z",
				"modelProvider": "gpt-5.4",
			},
		}
	})
	server.onCall("turn/start", func(_ map[string]any) any {
		return map[string]any{"turn": map[string]any{"id": "turn_001"}}
	})
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:          "agent-test",
		Poster:           poster,
		Stdout:           pipes.driverStdout,
		Stdin:            pipes.driverStdin,
		FrameProfile:     codexProfileForTest(t),
		HandshakeTimeout: 2 * time.Second,
		CallTimeout:      2 * time.Second,
		Closer:           pipes.closeFn,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(drv.Stop)

	// Handshake must have hit initialize, initialized (notification),
	// thread/start in order. The fake server records every frame
	// regardless of whether it's a request or a notification.
	server.waitForMethod("initialize", time.Second)
	server.waitForMethod("initialized", time.Second)
	server.waitForMethod("thread/start", time.Second)
	if got := drv.ThreadID(); got != "thr_test" {
		t.Errorf("ThreadID after handshake = %q; want thr_test", got)
	}

	// Send an Input → driver issues turn/start.
	if err := drv.Input(ctx, "text", map[string]any{"body": "hello"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	turnFrame := server.waitForMethod("turn/start", time.Second)
	params, _ := turnFrame["params"].(map[string]any)
	if got, _ := params["threadId"].(string); got != "thr_test" {
		t.Errorf("turn/start.params.threadId = %q; want thr_test", got)
	}
	input, _ := params["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("turn/start.params.input: want 1 item, got %d", len(input))
	}
	first, _ := input[0].(map[string]any)
	if got, _ := first["text"].(string); got != "hello" {
		t.Errorf("turn/start.input[0].text = %q; want hello", got)
	}

	// Push a notification — the driver should translate it and post.
	server.notify("turn/started", map[string]any{
		"turn": map[string]any{"id": "turn_001", "status": "inProgress"},
	})
	server.notify("item/completed", map[string]any{
		"item": map[string]any{
			"id":    "item_msg_1",
			"type":  "agentMessage",
			"text":  "Hi.",
			"phase": "final_answer",
		},
	})
	server.notify("turn/completed", map[string]any{
		"turn": map[string]any{"id": "turn_001", "status": "completed"},
	})

	// poster sees: lifecycle.started + (system turn/started) +
	// (text) + (turn.result). The lifecycle event is posted before
	// handshake, so it's already there.
	events := poster.wait(t, 4, 2*time.Second)
	wantKinds := map[string]bool{
		"lifecycle":   false,
		"system":      false,
		"text":        false,
		"turn.result": false,
	}
	for _, e := range events {
		if _, ok := wantKinds[e.Kind]; ok {
			wantKinds[e.Kind] = true
		}
	}
	for k, seen := range wantKinds {
		if !seen {
			t.Errorf("missing event kind %q in posted set %+v", k, events)
		}
	}
}

// TestAppServerDriver_ServerRequestAutoDeclines pins the slice-3
// stub for server-initiated approval requests: the driver auto-
// declines so the agent doesn't hang on its own permission gate
// while slice 4 is in flight. Slice 4 will replace this with a
// /decide-mediated answer.
func TestAppServerDriver_ServerRequestAutoDeclines(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any {
		return map[string]any{"protocolVersion": "1.0"}
	})
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_x"}}
	})
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:          "agent-x",
		Poster:           poster,
		Stdout:           pipes.driverStdout,
		Stdin:            pipes.driverStdin,
		FrameProfile:     codexProfileForTest(t),
		HandshakeTimeout: 2 * time.Second,
		CallTimeout:      2 * time.Second,
		Closer:           pipes.closeFn,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(drv.Stop)

	// Server fires a per-tool-call approval request.
	server.serverRequest(99, "item/commandExecution/requestApproval", map[string]any{
		"itemId":  "item_cmd_1",
		"command": []string{"rm", "-rf", "/"},
		"reason":  "destructive",
	})

	// Driver should respond with a decline. The fake server records
	// every frame it reads, including responses; look for an entry
	// with id=99 and result.decision=decline.
	deadline := time.Now().Add(2 * time.Second)
	var found map[string]any
	for time.Now().Before(deadline) {
		for _, f := range server.seen() {
			if f["id"] == nil {
				continue
			}
			// id may decode as float64.
			id, _ := f["id"].(float64)
			if int64(id) != 99 {
				continue
			}
			if _, hasMethod := f["method"]; hasMethod {
				continue // request, not response
			}
			found = f
			break
		}
		if found != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if found == nil {
		t.Fatal("driver did not respond to server-initiated request within 2s")
	}
	result, _ := found["result"].(map[string]any)
	if got, _ := result["decision"].(string); got != "decline" {
		t.Errorf("server response decision = %q; want decline (slice-4 will replace this)",
			got)
	}

	// Also: the system event surfacing the unhandled method should
	// have been posted. This is what slice-4 will use to drive the
	// attention bridge.
	events := poster.snapshot()
	var sawPending bool
	for _, e := range events {
		if e.Kind == "system" && e.Payload["kind"] == "appserver_request_pending_bridge" {
			sawPending = true
			break
		}
	}
	if !sawPending {
		t.Errorf("expected system event flagging unhandled server request, got %+v", events)
	}
}
