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

// TestAppServerDriver_TurnStart_ImageBlocks pins the W4.3 wire shape:
// when payload["images"] is set, turn/start.params.input leads with
// `{type:"input_image", image_url:"data:<mime>;base64,<b64>"}` blocks
// and follows with the `{type:"text", text:body}` block. Image-only
// inputs (no body) produce a single image block. Hub-side W4.1
// validation is upstream; the driver trusts the payload shape.
func TestAppServerDriver_TurnStart_ImageBlocks(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any {
		return map[string]any{"protocolVersion": "1.0"}
	})
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{
			"thread": map[string]any{"id": "thr_img"},
		}
	})
	server.onCall("turn/start", func(_ map[string]any) any {
		return map[string]any{"turn": map[string]any{"id": "turn_img"}}
	})
	go server.run()

	drv := &AppServerDriver{
		AgentID:          "agent-img",
		Poster:           &fakePoster{},
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
	server.waitForMethod("thread/start", time.Second)

	if err := drv.Input(ctx, "text", map[string]any{
		"body": "what's in these?",
		"images": []any{
			map[string]any{"mime_type": "image/png", "data": "AAA="},
			map[string]any{"mime_type": "image/jpeg", "data": "BBB="},
		},
	}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	turnFrame := server.waitForMethod("turn/start", time.Second)
	params, _ := turnFrame["params"].(map[string]any)
	input, _ := params["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input: want 3 blocks, got %d (%+v)", len(input), input)
	}
	first, _ := input[0].(map[string]any)
	if first["type"] != "input_image" {
		t.Errorf("input[0].type = %v, want input_image", first["type"])
	}
	if got := first["image_url"]; got != "data:image/png;base64,AAA=" {
		t.Errorf("input[0].image_url = %v", got)
	}
	second, _ := input[1].(map[string]any)
	if second["type"] != "input_image" || second["image_url"] != "data:image/jpeg;base64,BBB=" {
		t.Errorf("input[1] malformed: %+v", second)
	}
	third, _ := input[2].(map[string]any)
	if third["type"] != "text" || third["text"] != "what's in these?" {
		t.Errorf("input[2] malformed: %+v", third)
	}
}

// TestAppServerDriver_Cancel_IncludesThreadID pins the cancel→
// turn/interrupt translation: codex's app-server returns
// -32600 "Invalid request: missing field `threadId`" when threadId
// is absent, which surfaced as a user-visible JSON-RPC error string
// in the mobile cancel flow. The driver must always include the
// active thread id.
func TestAppServerDriver_Cancel_IncludesThreadID(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any {
		return map[string]any{"protocolVersion": "1.0"}
	})
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{
			"thread": map[string]any{"id": "thr_cancel"},
		}
	})
	server.onCall("turn/interrupt", func(_ map[string]any) any {
		return map[string]any{}
	})
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:          "agent-cancel",
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
	server.waitForMethod("thread/start", time.Second)

	if err := drv.Input(ctx, "cancel", map[string]any{
		"reason": "user requested cancel",
	}); err != nil {
		t.Fatalf("Input(cancel): %v", err)
	}
	frame := server.waitForMethod("turn/interrupt", time.Second)
	params, _ := frame["params"].(map[string]any)
	if got, _ := params["threadId"].(string); got != "thr_cancel" {
		t.Errorf("turn/interrupt.params.threadId = %q; want thr_cancel", got)
	}
}

// fakeAttentionPoster records every PostAttention call and returns
// canned attention ids so the driver can stash them in its parked-id
// map. The slice-4 bridge depends on this surface separately from
// the agent_event poster.
type fakeAttentionPoster struct {
	mu       sync.Mutex
	posted   []AttentionIn
	nextID   int
	idPrefix string
}

func (f *fakeAttentionPoster) PostAttention(_ context.Context, in AttentionIn) (AttentionOut, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posted = append(f.posted, in)
	f.nextID++
	prefix := f.idPrefix
	if prefix == "" {
		prefix = "att_"
	}
	return AttentionOut{
		ID:        prefix + fmtIntForTest(f.nextID),
		CreatedAt: "2026-04-29T10:00:00Z",
	}, nil
}

func (f *fakeAttentionPoster) snapshot() []AttentionIn {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]AttentionIn, len(f.posted))
	copy(out, f.posted)
	return out
}

func fmtIntForTest(n int) string { return fmtInt(n) }

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestAppServerDriver_ApprovalBridge_RaisesAttention pins the slice-4
// happy path for codex's per-tool-call approval requests: a server-
// initiated `item/commandExecution/requestApproval` becomes an
// attention_items row (kind=permission_prompt) and the JSON-RPC
// request stays open, parked in the driver's local map keyed by
// the new attention id. No auto-decline (the slice-3 stub is gone).
func TestAppServerDriver_ApprovalBridge_RaisesAttention(t *testing.T) {
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
	att := &fakeAttentionPoster{idPrefix: "att_"}
	drv := &AppServerDriver{
		AgentID:          "agent-x",
		Handle:           "codex-steward",
		Poster:           poster,
		Attention:        att,
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
		"command": []any{"rm", "-rf", "/repo/build"},
		"reason":  "build cleanup",
	})

	// Driver should call PostAttention.
	deadline := time.Now().Add(2 * time.Second)
	var posted []AttentionIn
	for time.Now().Before(deadline) {
		posted = att.snapshot()
		if len(posted) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(posted) != 1 {
		t.Fatalf("PostAttention: want 1 call, got %d", len(posted))
	}
	in := posted[0]
	if in.Kind != "permission_prompt" {
		t.Errorf("attention.kind = %q; want permission_prompt", in.Kind)
	}
	if in.ActorHandle != "codex-steward" {
		t.Errorf("attention.actor_handle = %q; want codex-steward", in.ActorHandle)
	}
	if in.Severity != "major" {
		t.Errorf("commandExecution attention.severity = %q; want major", in.Severity)
	}
	// Pending payload carries the codex-side context and the parked
	// jsonrpc id — used by audit trail and any debugging tooling.
	var p map[string]any
	if err := json.Unmarshal(in.PendingPayload, &p); err != nil {
		t.Fatalf("decode pending_payload: %v", err)
	}
	if p["engine"] != "codex" {
		t.Errorf("pending.engine = %v; want codex", p["engine"])
	}
	if p["method"] != "item/commandExecution/requestApproval" {
		t.Errorf("pending.method = %v", p["method"])
	}
	if id, _ := p["jsonrpc_id"].(float64); int64(id) != 99 {
		t.Errorf("pending.jsonrpc_id = %v; want 99", p["jsonrpc_id"])
	}

	// Codex side must still be waiting — no response written for id=99.
	for _, f := range server.seen() {
		if f["id"] != nil {
			id, _ := f["id"].(float64)
			if int64(id) == 99 {
				if _, hasMethod := f["method"]; !hasMethod {
					t.Fatalf("driver responded to id=99; should still be parked")
				}
			}
		}
	}

	// And a system marker should record that the gate parked.
	events := poster.snapshot()
	var sawParked bool
	for _, e := range events {
		if e.Kind == "system" && e.Payload["kind"] == "appserver_request_parked" {
			sawParked = true
			break
		}
	}
	if !sawParked {
		t.Errorf("expected appserver_request_parked system event, got %+v", events)
	}
}

// TestAppServerDriver_ApprovalBridge_AttentionReplyAccepts drives the
// /decide → attention_reply → JSON-RPC response path end-to-end on
// the driver side. Builds on the previous test by then sending an
// attention_reply Input event with decision=approve and asserts the
// codex side now sees a {decision: accept} response on the parked id.
func TestAppServerDriver_ApprovalBridge_AttentionReplyAccepts(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr"}}
	})
	go server.run()

	poster := &fakePoster{}
	att := &fakeAttentionPoster{idPrefix: "att_"}
	drv := &AppServerDriver{
		AgentID:          "agent-y",
		Handle:           "codex-steward",
		Poster:           poster,
		Attention:        att,
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

	server.serverRequest(7, "item/commandExecution/requestApproval", map[string]any{
		"itemId": "x",
	})
	// Wait until the driver has parked.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(att.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(att.snapshot()) == 0 {
		t.Fatal("driver did not park attention within 2s")
	}
	attID := "att_1"

	// Simulate /decide approving the attention.
	if err := drv.Input(ctx, "attention_reply", map[string]any{
		"request_id": attID,
		"kind":       "permission_prompt",
		"decision":   "approve",
	}); err != nil {
		t.Fatalf("Input attention_reply: %v", err)
	}

	// Codex should now see a response for id=7 with decision=accept.
	deadline = time.Now().Add(2 * time.Second)
	var resp map[string]any
	for time.Now().Before(deadline) {
		for _, f := range server.seen() {
			if f["id"] == nil {
				continue
			}
			id, _ := f["id"].(float64)
			if int64(id) != 7 {
				continue
			}
			if _, hasMethod := f["method"]; hasMethod {
				continue
			}
			resp = f
			break
		}
		if resp != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("no JSON-RPC response written for parked id within 2s")
	}
	result, _ := resp["result"].(map[string]any)
	if got, _ := result["decision"].(string); got != "accept" {
		t.Errorf("approve → result.decision = %q; want accept", got)
	}

	// Reject path: park another, send reject, verify decline.
	server.serverRequest(8, "item/commandExecution/requestApproval", map[string]any{
		"itemId": "y",
	})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(att.snapshot()) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if err := drv.Input(ctx, "attention_reply", map[string]any{
		"request_id": "att_2",
		"kind":       "permission_prompt",
		"decision":   "reject",
	}); err != nil {
		t.Fatalf("Input attention_reply (reject): %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	var resp2 map[string]any
	for time.Now().Before(deadline) {
		for _, f := range server.seen() {
			if f["id"] == nil {
				continue
			}
			id, _ := f["id"].(float64)
			if int64(id) != 8 {
				continue
			}
			if _, hasMethod := f["method"]; hasMethod {
				continue
			}
			resp2 = f
			break
		}
		if resp2 != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resp2 == nil {
		t.Fatal("no JSON-RPC response written for second parked id")
	}
	result2, _ := resp2["result"].(map[string]any)
	if got, _ := result2["decision"].(string); got != "decline" {
		t.Errorf("reject → result.decision = %q; want decline", got)
	}
}

// TestAppServerDriver_ApprovalBridge_FallsBackWhenNoBridge keeps the
// auto-decline path covered for spawns wired without an Attention
// hook (tests, future driver-only modes). System event surfaces the
// fallback reason.
func TestAppServerDriver_ApprovalBridge_FallsBackWhenNoBridge(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr"}}
	})
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:          "agent-z",
		Poster:           poster,
		Stdout:           pipes.driverStdout,
		Stdin:            pipes.driverStdin,
		FrameProfile:     codexProfileForTest(t),
		HandshakeTimeout: 2 * time.Second,
		CallTimeout:      2 * time.Second,
		Closer:           pipes.closeFn,
		// Attention deliberately nil.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(drv.Stop)

	server.serverRequest(11, "item/commandExecution/requestApproval", map[string]any{})
	deadline := time.Now().Add(2 * time.Second)
	var resp map[string]any
	for time.Now().Before(deadline) {
		for _, f := range server.seen() {
			if f["id"] == nil {
				continue
			}
			id, _ := f["id"].(float64)
			if int64(id) == 11 {
				if _, hasMethod := f["method"]; !hasMethod {
					resp = f
					break
				}
			}
		}
		if resp != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("driver should auto-decline when no bridge is wired")
	}
	result, _ := resp["result"].(map[string]any)
	if got, _ := result["decision"].(string); got != "decline" {
		t.Errorf("auto-decline → decision = %q; want decline", got)
	}
}

// TestAppServerDriver_DropsStderrShapedLines pins the read-loop's
// non-JSON drop. RealProcSpawner merges stderr into stdout for
// captured-pane logging, which means timestamped log lines like
//
//	2026-05-06T12:58:17.190362Z ERROR codex_app_server::... missing field `action`
//
// land on the same scanner the JSON-RPC reader walks. Earlier the
// driver posted those as kind=raw, surfacing them in the transcript
// as garbled "random characters." The fix routes anything that
// doesn't start with `{` to the slog debug channel and skips the
// agent_event entirely.
func TestAppServerDriver_DropsStderrShapedLines(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_x"}}
	})
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:          "agent-stderr",
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

	// Inject a representative stderr line — the exact format codex
	// emits via tracing-subscriber. Followed by a real notification
	// to prove the loop didn't get stuck.
	_, _ = pipes.serverWrite.Write([]byte(
		"2026-05-06T12:58:17.190362Z ERROR codex_app_server::bespoke_event_handling: failed to deserialize McpServerElicitationRequestResponse: missing field `action`\n"))
	server.notify("turn/started", map[string]any{
		"turn": map[string]any{"id": "turn_001", "status": "inProgress"},
	})

	// Drain — give the read loop a beat to process both lines, then
	// assert the stderr line did not produce a kind=raw event.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		seen := poster.snapshot()
		hasSystem := false
		for _, e := range seen {
			if e.Kind == "system" {
				hasSystem = true
			}
			if e.Kind == "raw" {
				if text, _ := e.Payload["text"].(string); text != "" {
					t.Fatalf("stderr line surfaced as kind=raw: %q", text)
				}
			}
		}
		if hasSystem {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, e := range poster.snapshot() {
		if e.Kind == "raw" {
			t.Errorf("kind=raw should not be emitted for stderr lines; got payload=%v",
				e.Payload)
		}
	}
}

// TestAppServerDriver_DeltasNeverLeakAsRaw guards the floor
// behavior: codex's per-token streaming frames never surface as
// `kind=raw` agent_events. agentMessage deltas now route through
// the throttled streaming buffer and produce `kind=text, partial:
// true` rows (see TestAppServerDriver_StreamsAgentMessageDeltas);
// other delta methods (item/reasoning/textDelta etc.) are dropped
// outright since their content is internal-monologue debug data
// the typed event vocabulary doesn't surface.
func TestAppServerDriver_DeltasNeverLeakAsRaw(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_d"}}
	})
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:          "agent-delta",
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

	// Spam delta-shaped notifications, then send a real text/completed
	// frame to flush the read loop. Only the latter should produce an
	// agent_event.
	for i := 0; i < 5; i++ {
		server.notify("item/agentMessage/delta", map[string]any{
			"itemId": "item_msg_1", "delta": "tok",
		})
	}
	server.notify("item/reasoning/textDelta", map[string]any{
		"itemId": "item_msg_1", "delta": "thinking",
	})
	server.notify("item/completed", map[string]any{
		"item": map[string]any{
			"id":    "item_msg_1",
			"type":  "agentMessage",
			"text":  "Hi.",
			"phase": "final_answer",
		},
	})

	// Wait for the text event then check no delta-shaped raw rows
	// snuck through. lifecycle (1) + text (1) is the floor.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		seen := poster.snapshot()
		hasText := false
		for _, e := range seen {
			if e.Kind == "text" {
				hasText = true
			}
		}
		if hasText {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, e := range poster.snapshot() {
		if e.Kind == "raw" {
			method, _ := e.Payload["method"].(string)
			if strings.HasSuffix(method, "/delta") || strings.HasSuffix(method, "Delta") {
				t.Errorf("delta notification surfaced as kind=raw: method=%q", method)
			}
		}
	}
}

// TestAppServerDriver_ElicitationAutoDeclineShape pins the
// elicitation-response shape codex's rmcp deserializer requires
// when no Attention bridge is wired. The response must use
// `{"action": "decline"}`, not the approval-shape `{"decision":
// "decline"}`. Without the right field name codex logs
//
//	failed to deserialize McpServerElicitationRequestResponse: missing field `action`
//
// and the originating MCP tool call dies with
//
//	Termipod MCP selection request was rejected by the client/tool layer.
//
// When Attention IS wired, see
// TestAppServerDriver_ElicitationBridge_RaisesAttention below.
func TestAppServerDriver_ElicitationAutoDeclineShape(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_e"}}
	})
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:          "agent-elicit",
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

	server.serverRequest(42, "mcpServer/elicitation/request", map[string]any{
		"server":  "termipod",
		"message": "pick one",
	})

	deadline := time.Now().Add(2 * time.Second)
	var resp map[string]any
	for time.Now().Before(deadline) && resp == nil {
		for _, f := range server.seen() {
			if id, ok := f["id"].(float64); ok && int64(id) == 42 {
				if _, hasMethod := f["method"]; !hasMethod {
					resp = f
					break
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("driver did not respond to mcpServer/elicitation/request")
	}
	result, _ := resp["result"].(map[string]any)
	if _, hasAction := result["action"]; !hasAction {
		t.Errorf("elicitation response missing required `action` field; result=%v", result)
	}
	if got, _ := result["action"].(string); got != "decline" {
		t.Errorf("elicitation action = %q; want decline", got)
	}
	if _, hasDecision := result["decision"]; hasDecision {
		t.Errorf("elicitation response should not carry approval-shape `decision` field; result=%v", result)
	}
}

// TestAppServerDriver_ElicitationBridge_RaisesAttention pins the
// follow-up: when Attention is wired, an mcpServer/elicitation/request
// posts an attention_items row with kind=elicit (not
// permission_prompt — the principal-side UX is a free-text reply,
// not yes/no), with the MCP server's message used as the summary.
// The JSON-RPC request stays open until the principal replies.
func TestAppServerDriver_ElicitationBridge_RaisesAttention(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_eb"}}
	})
	go server.run()

	poster := &fakePoster{}
	att := &fakeAttentionPoster{idPrefix: "att_e"}
	drv := &AppServerDriver{
		AgentID:          "agent-eb",
		Handle:           "@coder",
		Poster:           poster,
		Attention:        att,
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

	server.serverRequest(77, "mcpServer/elicitation/request", map[string]any{
		"server":  "termipod",
		"message": "Pick one of: A, B, C",
		// Non-empty requestedSchema → real form-fill elicit (not a
		// codex tool-call approval gate, which uses empty schema).
		"requestedSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"choice": map[string]any{"type": "string"},
			},
		},
	})

	// Attention should land with kind=elicit and the server's message
	// as the summary.
	deadline := time.Now().Add(2 * time.Second)
	var posted []AttentionIn
	for time.Now().Before(deadline) {
		posted = att.snapshot()
		if len(posted) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(posted) != 1 {
		t.Fatalf("expected one attention; got %d", len(posted))
	}
	if posted[0].Kind != "elicit" {
		t.Errorf("attention.kind = %q; want elicit", posted[0].Kind)
	}
	if posted[0].Summary != "Pick one of: A, B, C" {
		t.Errorf("attention.summary = %q; want server message", posted[0].Summary)
	}
	// JSON-RPC request must NOT be auto-resolved — the driver parks it
	// awaiting attention_reply.
	for _, f := range server.seen() {
		if id, ok := f["id"].(float64); ok && int64(id) == 77 {
			if _, hasMethod := f["method"]; !hasMethod {
				t.Errorf("driver responded to elicitation id=77 prematurely; should be parked")
			}
		}
	}
}

// TestAppServerDriver_ElicitationBridge_ReplyAccepts drives the full
// happy path: server sends elicitation/request → driver bridges to
// attention → principal replies via attention_reply (decision=approve,
// body="hello") → driver writes the parked JSON-RPC response with
// shape `{action: accept, content: {value: "hello"}}`. A schema-driven
// content wrap is a follow-up wedge once the mobile UI surfaces typed
// inputs — for now the body wraps as `{value: <text>}`.
func TestAppServerDriver_ElicitationBridge_ReplyAccepts(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_er"}}
	})
	go server.run()

	poster := &fakePoster{}
	att := &fakeAttentionPoster{idPrefix: "att_r"}
	drv := &AppServerDriver{
		AgentID:          "agent-er",
		Handle:           "@coder",
		Poster:           poster,
		Attention:        att,
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

	server.serverRequest(88, "mcpServer/elicitation/request", map[string]any{
		"server":  "termipod",
		"message": "What's your favorite color?",
		"requestedSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
	})

	// Wait for the bridge to post the attention.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(att.snapshot()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	posted := att.snapshot()
	if len(posted) != 1 {
		t.Fatalf("expected one attention; got %d", len(posted))
	}

	// Simulate /decide → attention_reply Input event.
	if err := drv.Input(ctx, "attention_reply", map[string]any{
		"kind":       "elicit",
		"request_id": "att_r1",
		"decision":   "approve",
		"body":       "blue",
	}); err != nil {
		t.Fatalf("attention_reply: %v", err)
	}

	// Server should now see a JSON-RPC response on id=88 with the
	// elicitation shape.
	deadline = time.Now().Add(2 * time.Second)
	var resp map[string]any
	for time.Now().Before(deadline) && resp == nil {
		for _, f := range server.seen() {
			if id, ok := f["id"].(float64); ok && int64(id) == 88 {
				if _, hasMethod := f["method"]; !hasMethod {
					resp = f
					break
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("driver did not respond to parked elicitation after attention_reply")
	}
	result, _ := resp["result"].(map[string]any)
	if got, _ := result["action"].(string); got != "accept" {
		t.Errorf("elicitation response action = %q; want accept", got)
	}
	content, _ := result["content"].(map[string]any)
	if got, _ := content["value"].(string); got != "blue" {
		t.Errorf("elicitation content.value = %q; want blue", got)
	}
}

// TestAppServerDriver_StreamsAgentMessageDeltas pins the
// chatbot-style streaming UX. A burst of item/agentMessage/delta
// notifications produces one or more `kind=text, partial: true`
// events whose `text` field grows monotonically — *not* one event
// per delta, *not* zero events. The trailing item/completed
// finalizes the chain by canceling the timer; the profile-emitted
// final text event (kind=text, no partial flag) supersedes
// whatever partial the mobile renderer last collapsed.
//
// We use a short StreamFlushInterval (10ms) so the throttle fires
// within the test's deadline; production runs at 200ms which is
// fine for live UX but too slow for a hermetic test.
func TestAppServerDriver_StreamsAgentMessageDeltas(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_s"}}
	})
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:             "agent-stream",
		Poster:              poster,
		Stdout:              pipes.driverStdout,
		Stdin:               pipes.driverStdin,
		FrameProfile:        codexProfileForTest(t),
		HandshakeTimeout:    2 * time.Second,
		CallTimeout:         2 * time.Second,
		StreamFlushInterval: 10 * time.Millisecond,
		Closer:              pipes.closeFn,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(drv.Stop)

	// Burst 5 deltas, sleep past the throttle so each batch flushes,
	// repeat. Then send the final completion frame.
	for i := 0; i < 3; i++ {
		for j := 0; j < 5; j++ {
			server.notify("item/agentMessage/delta", map[string]any{
				"itemId": "item_msg_s",
				"delta":  "ab",
			})
		}
		time.Sleep(20 * time.Millisecond)
	}
	server.notify("item/completed", map[string]any{
		"item": map[string]any{
			"id":    "item_msg_s",
			"type":  "agentMessage",
			"text":  "ababababababababababababababab",
			"phase": "final_answer",
		},
	})

	// Wait until the final text (non-partial) shows up.
	deadline := time.Now().Add(2 * time.Second)
	var partials []postedEvent
	var finalText *postedEvent
	for time.Now().Before(deadline) && finalText == nil {
		partials = nil
		for _, e := range poster.snapshot() {
			if e.Kind != "text" {
				continue
			}
			mid, _ := e.Payload["message_id"].(string)
			if mid != "item_msg_s" {
				continue
			}
			if e.Payload["partial"] == true {
				partials = append(partials, e)
			} else {
				ev := e
				finalText = &ev
			}
		}
		if finalText != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if finalText == nil {
		t.Fatal("never saw final text event for streamed message")
	}
	if len(partials) == 0 {
		t.Fatal("expected one or more partial text events, got 0 — streaming didn't fire")
	}
	if len(partials) >= 15 {
		// Floor sanity: 15 is the total delta count; we should see far
		// fewer flushes than deltas.
		t.Errorf("partial count %d ≈ delta count: throttle didn't coalesce", len(partials))
	}
	// Each partial's text must be a prefix of (or equal to) the final
	// text — the throttled flushes accumulate; they don't reset.
	want := "ababababababababababababababab"
	if got, _ := finalText.Payload["text"].(string); got != want {
		t.Errorf("final text = %q; want %q", got, want)
	}
	for i, p := range partials {
		got, _ := p.Payload["text"].(string)
		if got == "" {
			t.Errorf("partial[%d] empty text", i)
		}
		if len(got) > len(want) || want[:len(got)] != got {
			t.Errorf("partial[%d] text %q is not a prefix of final %q", i, got, want)
		}
		if i > 0 {
			prev, _ := partials[i-1].Payload["text"].(string)
			if len(got) < len(prev) {
				t.Errorf("partial[%d] text %q shorter than partial[%d] %q — chain went backwards",
					i, got, i-1, prev)
			}
		}
	}
}

// TestElicitationContentFromBody covers the three wrap behaviors:
// JSON object verbatim, free text → {value}, empty → {}. Single-
// purpose unit test so the wrap stays predictable as we extend the
// schema-driven content shape later.
func TestElicitationContentFromBody(t *testing.T) {
	cases := []struct {
		body string
		want map[string]any
	}{
		{"", map[string]any{}},
		{"   ", map[string]any{}},
		{"hello", map[string]any{"value": "hello"}},
		{`{"name":"x"}`, map[string]any{"name": "x"}},
		{`{"broken json`, map[string]any{"value": `{"broken json`}}, // falls back to wrap
	}
	for _, tc := range cases {
		got := elicitationContentFromBody(tc.body)
		if len(got) != len(tc.want) {
			t.Errorf("body=%q: got %v; want %v", tc.body, got, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("body=%q: got[%q]=%v; want %v", tc.body, k, got[k], v)
			}
		}
	}
}

// TestAppServerDriver_Cancel_IncludesTurnID pins the bug fix where
// turn/interrupt was sent with only threadId and codex replied
// -32600 "Invalid request: missing field `turnId`". Once turn/started
// has fired, the cancel path must include both ids.
func TestAppServerDriver_Cancel_IncludesTurnID(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_t"}}
	})
	server.onCall("turn/start", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("turn/interrupt", func(_ map[string]any) any { return map[string]any{} })
	go server.run()

	poster := &fakePoster{}
	drv := &AppServerDriver{
		AgentID:          "agent-tcancel",
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

	if err := drv.Input(ctx, "text", map[string]any{"body": "hi"}); err != nil {
		t.Fatalf("Input(text): %v", err)
	}
	server.waitForMethod("turn/start", time.Second)
	server.notify("turn/started", map[string]any{
		"turn": map[string]any{"id": "turn_42", "status": "inProgress"},
	})

	// Wait for the driver to absorb the notification.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && drv.TurnID() == "" {
		time.Sleep(5 * time.Millisecond)
	}
	if drv.TurnID() != "turn_42" {
		t.Fatalf("driver did not capture turn id from turn/started; got %q", drv.TurnID())
	}

	if err := drv.Input(ctx, "cancel", map[string]any{}); err != nil {
		t.Fatalf("Input(cancel): %v", err)
	}
	frame := server.waitForMethod("turn/interrupt", time.Second)
	params, _ := frame["params"].(map[string]any)
	if got, _ := params["threadId"].(string); got != "thr_t" {
		t.Errorf("turn/interrupt.params.threadId = %q; want thr_t", got)
	}
	if got, _ := params["turnId"].(string); got != "turn_42" {
		t.Errorf("turn/interrupt.params.turnId = %q; want turn_42 (rmcp rejects without it)", got)
	}
}

// TestAppServerDriver_ToolCallApprovalRoutesAsPermissionPrompt pins
// the bug fix where codex's MCP-tool-call approval (wire-level shape:
// `mcpServer/elicitation/request` with `_meta.codex_approval_kind ==
// "mcp_tool_call"` and an empty `requestedSchema.properties`) was
// being routed as a free-text elicit. The principal would type "yes"
// and codex's rmcp couldn't deserialize {value: "yes"} against the
// empty schema, leaving the turn stuck. The driver must route this
// shape as kind=permission_prompt and respond with `{action: accept}`
// (no content) on approve.
func TestAppServerDriver_ToolCallApprovalRoutesAsPermissionPrompt(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_tc"}}
	})
	go server.run()

	poster := &fakePoster{}
	att := &fakeAttentionPoster{idPrefix: "att_tc"}
	drv := &AppServerDriver{
		AgentID:          "agent-tc",
		Handle:           "@coder",
		Poster:           poster,
		Attention:        att,
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

	server.serverRequest(7, "mcpServer/elicitation/request", map[string]any{
		"serverName": "termipod",
		"mode":       "form",
		"_meta": map[string]any{
			"codex_approval_kind": "mcp_tool_call",
			"persist":             []any{"session", "always"},
		},
		"message":         "Allow the termipod MCP server to run tool \"request_select\"?",
		"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(att.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	posted := att.snapshot()
	if len(posted) != 1 {
		t.Fatalf("expected one attention; got %d", len(posted))
	}
	if posted[0].Kind != "permission_prompt" {
		t.Errorf("attention.kind = %q; want permission_prompt (tool-call approvals must not be free-text elicits)", posted[0].Kind)
	}

	if err := drv.Input(ctx, "attention_reply", map[string]any{
		"kind":       "permission_prompt",
		"request_id": "att_tc1",
		"decision":   "approve",
	}); err != nil {
		t.Fatalf("attention_reply: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	var resp map[string]any
	for time.Now().Before(deadline) && resp == nil {
		for _, f := range server.seen() {
			if id, ok := f["id"].(float64); ok && int64(id) == 7 {
				if _, hasMethod := f["method"]; !hasMethod {
					resp = f
					break
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("driver did not respond to parked tool-call approval after attention_reply")
	}
	result, _ := resp["result"].(map[string]any)
	if got, _ := result["action"].(string); got != "accept" {
		t.Errorf("result.action = %q; want accept (rmcp rejects {decision} on this method)", got)
	}
	// content: {} — empty object satisfies the request's
	// `requestedSchema: {type: object, properties: {}}`. Missing the
	// field entirely leaves codex's deserializer ambiguous and the
	// turn stuck in waitingOnApproval.
	content, hasContent := result["content"].(map[string]any)
	if !hasContent {
		t.Errorf("result.content missing; want empty object {} to satisfy the empty-properties schema; result=%v", result)
	} else if len(content) != 0 {
		t.Errorf("result.content = %v; want empty {}", content)
	}
	// _meta.persist: "session" — pre-authorize subsequent termipod
	// tool calls in the same thread so the principal isn't gated
	// every single MCP call.
	meta, _ := result["_meta"].(map[string]any)
	if got, _ := meta["persist"].(string); got != "session" {
		t.Errorf("result._meta.persist = %q; want session (skip-future-gates hint)", got)
	}
}

// TestAppServerDriver_Cancel_DrainsParkedRequests pins the bug where
// canceling a turn left parked elicit/approval JSON-RPC ids open on
// the codex side. turn/interrupt aborts in-flight tool calls, but
// the wire response on the parked id still has to be written or
// codex's rmcp keeps the request alive. Cancel must write
// cancel-shaped responses to every parked id.
func TestAppServerDriver_Cancel_DrainsParkedRequests(t *testing.T) {
	pipes := newPipePair()
	t.Cleanup(pipes.closeFn)

	server := newFakeAppServer(t, pipes.serverRead, pipes.serverWrite)
	server.onCall("initialize", func(_ map[string]any) any { return map[string]any{} })
	server.onCall("thread/start", func(_ map[string]any) any {
		return map[string]any{"thread": map[string]any{"id": "thr_drain"}}
	})
	server.onCall("turn/interrupt", func(_ map[string]any) any { return map[string]any{} })
	go server.run()

	poster := &fakePoster{}
	att := &fakeAttentionPoster{idPrefix: "att_d"}
	drv := &AppServerDriver{
		AgentID:          "agent-drain",
		Handle:           "@coder",
		Poster:           poster,
		Attention:        att,
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

	// Park a tool-call approval (elicitation method, empty schema).
	server.serverRequest(101, "mcpServer/elicitation/request", map[string]any{
		"serverName": "termipod",
		"_meta": map[string]any{
			"codex_approval_kind": "mcp_tool_call",
		},
		"message":         "Allow tool call?",
		"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	})
	// Wait for it to be parked.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(att.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(att.snapshot()) != 1 {
		t.Fatalf("expected parked attention; got %d", len(att.snapshot()))
	}

	if err := drv.Input(ctx, "cancel", map[string]any{}); err != nil {
		t.Fatalf("Input(cancel): %v", err)
	}

	// turn/interrupt is one signal; the cancel-shaped response on
	// id=101 is the other. Both must arrive.
	server.waitForMethod("turn/interrupt", time.Second)
	deadline = time.Now().Add(time.Second)
	var resp map[string]any
	for time.Now().Before(deadline) && resp == nil {
		for _, f := range server.seen() {
			if id, ok := f["id"].(float64); ok && int64(id) == 101 {
				if _, hasMethod := f["method"]; !hasMethod {
					resp = f
					break
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("cancel did not write a response on the parked elicitation id")
	}
	result, _ := resp["result"].(map[string]any)
	if got, _ := result["action"].(string); got != "cancel" {
		t.Errorf("parked elicit cancel.action = %q; want cancel", got)
	}
}
