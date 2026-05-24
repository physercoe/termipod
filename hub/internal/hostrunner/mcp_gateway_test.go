package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dialGateway opens a UDS connection to a running gateway and returns a
// paired reader/writer. Tests use this to speak line-delimited JSON-RPC.
func dialGateway(t *testing.T, g *McpGateway) (net.Conn, *bufio.Reader) {
	t.Helper()
	path := strings.TrimPrefix(g.Endpoint, "unix://")
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, bufio.NewReader(conn)
}

func writeJRPCLine(t *testing.T, w io.Writer, method string, id any, params any) {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		req["id"] = id
	}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readJRPCLine(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return m
}

// TestGateway_InitializeAndToolsList verifies the core handshake and that
// all four skeleton tools are reachable.
func TestGateway_InitializeAndToolsList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g, cleanup, err := StartGateway(ctx, "test-init-"+randID(t), nil)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	defer cleanup()

	conn, r := dialGateway(t, g)

	writeJRPCLine(t, conn, "initialize", 1, map[string]any{})
	resp := readJRPCLine(t, r)
	if resp["error"] != nil {
		t.Fatalf("initialize returned error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil || result["protocolVersion"] == "" {
		t.Fatalf("initialize missing protocolVersion: %v", resp)
	}

	writeJRPCLine(t, conn, "tools/list", 2, map[string]any{})
	resp = readJRPCLine(t, r)
	result, _ = resp["result"].(map[string]any)
	toolsRaw, _ := result["tools"].([]any)
	// Catalog grows additively (ADR-027 W5b added the 9 hook tools).
	// Assert presence of the 4 core ones rather than an exact length so
	// future additions don't force test churn.
	want := map[string]bool{
		"host.ping":            false,
		"hub.agent_event_post": false,
		"hub.document_create":  false,
		"hub.review_create":    false,
	}
	for _, tr := range toolsRaw {
		m, _ := tr.(map[string]any)
		name, _ := m["name"].(string)
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing tool: %s", name)
		}
	}
}

// TestGateway_ForwardStampsAgentID boots a mock hub with httptest, calls
// hub.agent_event_post through the gateway, and asserts X-Agent-Id reached
// the hub while Authorization was rewritten to the host-runner token.
func TestGateway_ForwardStampsAgentID(t *testing.T) {
	var gotAgent, gotAuth, gotPath, gotMethod atomic.Value
	gotAgent.Store("")
	gotAuth.Store("")
	gotPath.Store("")
	gotMethod.Store("")

	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAgent.Store(r.Header.Get("X-Agent-Id"))
		gotAuth.Store(r.Header.Get("Authorization"))
		gotPath.Store(r.URL.Path)
		gotMethod.Store(r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ev-123","received_ts":"now"}`))
	}))
	defer hub.Close()

	hubClient := NewClient(hub.URL, "host-runner-secret", "teamA")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentID := "agent-" + randID(t)
	g, cleanup, err := StartGateway(ctx, agentID, hubClient)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	defer cleanup()

	conn, r := dialGateway(t, g)

	writeJRPCLine(t, conn, "tools/call", 42, map[string]any{
		"name": "hub.agent_event_post",
		"arguments": map[string]any{
			"project_id": "projX",
			"channel_id": "chanY",
			"type":       "message",
			"parts": []map[string]any{
				{"kind": "text", "text": "hello"},
			},
		},
	})
	resp := readJRPCLine(t, r)
	if resp["error"] != nil {
		t.Fatalf("tools/call returned error: %v", resp["error"])
	}

	if got := gotAgent.Load().(string); got != agentID {
		t.Errorf("X-Agent-Id: got %q want %q", got, agentID)
	}
	if got := gotAuth.Load().(string); got != "Bearer host-runner-secret" {
		t.Errorf("Authorization: got %q", got)
	}
	if got := gotPath.Load().(string); got != "/v1/teams/teamA/projects/projX/channels/chanY/events" {
		t.Errorf("path: got %q", got)
	}
	if got := gotMethod.Load().(string); got != http.MethodPost {
		t.Errorf("method: got %q", got)
	}

	// Result should surface the hub's JSON (wrapped as MCP text content).
	result, _ := resp["result"].(map[string]any)
	contentArr, _ := result["content"].([]any)
	if len(contentArr) == 0 {
		t.Fatalf("no content in tool result: %v", resp)
	}
	first, _ := contentArr[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(text, "ev-123") {
		t.Errorf("tool result missing hub response body: %q", text)
	}
}

// randID returns a short unique suffix so parallel test runs don't collide
// on the UDS path. Using the test name alone isn't enough because go test
// may re-run the same test via -count.
func randID(t *testing.T) string {
	t.Helper()
	// time.Now().UnixNano() is monotonic enough for test-level uniqueness.
	return time.Now().Format("150405.000000000")
}

// --- ADR-036 W1: status_line ---

// Counting hub. Records every PostAgentEvent the gateway forwards
// (used to assert dedupe drops the second of two identical fires
// within 1s, and ALSO that two DIFFERENT fires both make it through).
type countingHub struct {
	server *httptest.Server
	posts  atomic.Int32
	bodies []string
	mu     sync.Mutex
}

func newCountingHub(t *testing.T) *countingHub {
	t.Helper()
	ch := &countingHub{}
	ch.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ch.posts.Add(1)
		ch.mu.Lock()
		ch.bodies = append(ch.bodies, string(body))
		ch.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ev-1"}`))
	}))
	t.Cleanup(ch.server.Close)
	return ch
}

// Both: the status_line tool catalog entry is present in tools/list,
// AND consecutive identical fires within 1s are deduped by the
// gateway (only the first forwards to the hub).
func TestGateway_StatusLine_DedupeIdentical(t *testing.T) {
	hub := newCountingHub(t)
	hubClient := NewClient(hub.server.URL, "host-runner-secret", "teamA")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g, cleanup, err := StartGateway(ctx, "agent-"+randID(t), hubClient)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	defer cleanup()

	// 1. catalog presence.
	conn, r := dialGateway(t, g)
	writeJRPCLine(t, conn, "tools/list", 1, map[string]any{})
	resp := readJRPCLine(t, r)
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	var sawStatus bool
	for _, tr := range tools {
		m, _ := tr.(map[string]any)
		if name, _ := m["name"].(string); name == "status_line" {
			sawStatus = true
		}
	}
	if !sawStatus {
		t.Fatal("status_line tool missing from tools/list catalog")
	}

	// 2. fire twice with identical payload within 1s.
	payload := map[string]any{
		"session_id":     "abc",
		"model":          map[string]any{"id": "claude-opus-4-7"},
		"context_window": map[string]any{"context_window_size": 1_000_000},
	}
	conn2, r2 := dialGateway(t, g)
	writeJRPCLine(t, conn2, "tools/call", 2, map[string]any{
		"name": "status_line", "arguments": payload,
	})
	_ = readJRPCLine(t, r2)
	_ = conn2.Close()

	conn3, r3 := dialGateway(t, g)
	writeJRPCLine(t, conn3, "tools/call", 3, map[string]any{
		"name": "status_line", "arguments": payload,
	})
	resp2 := readJRPCLine(t, r3)
	_ = conn3.Close()

	// Brief wait — the hub POST is best-effort and happens after the
	// MCP reply; give it a slice to land.
	time.Sleep(50 * time.Millisecond)

	if got := hub.posts.Load(); got != 1 {
		t.Errorf("posts forwarded = %d, want 1 (second identical fire should dedupe)", got)
	}
	// The deduped response should advertise itself for diagnostic
	// purposes (lets the shim's stderr log distinguish dedupe from
	// transport failure if we ever flip to non-best-effort).
	result2, _ := resp2["result"].(map[string]any)
	contentArr2, _ := result2["content"].([]any)
	first2, _ := contentArr2[0].(map[string]any)
	if text, _ := first2["text"].(string); !strings.Contains(text, "deduped") {
		t.Errorf("deduped response missing flag: %q", text)
	}

	// 3. fire a DIFFERENT payload — must pass.
	conn4, r4 := dialGateway(t, g)
	writeJRPCLine(t, conn4, "tools/call", 4, map[string]any{
		"name": "status_line",
		"arguments": map[string]any{
			"session_id": "new-session-after-clear",
			"model":      map[string]any{"id": "claude-sonnet-4-6"},
		},
	})
	_ = readJRPCLine(t, r4)
	_ = conn4.Close()
	time.Sleep(50 * time.Millisecond)

	if got := hub.posts.Load(); got != 2 {
		t.Errorf("after differing fire, posts = %d, want 2", got)
	}
}

// The optional StatusLineSink is invoked with the decoded payload
// (W3 will register the LocalLogTail adapter as the sink for
// session_id rotation handling).
func TestGateway_StatusLine_SinkReceivesPayload(t *testing.T) {
	hub := newCountingHub(t)
	hubClient := NewClient(hub.server.URL, "host-runner-secret", "teamA")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g, cleanup, err := StartGateway(ctx, "agent-"+randID(t), hubClient)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	defer cleanup()

	var got map[string]any
	var mu sync.Mutex
	g.StatusLineSink = statusLineSinkFunc(func(_ context.Context, p map[string]any) {
		mu.Lock()
		got = p
		mu.Unlock()
	})

	conn, r := dialGateway(t, g)
	writeJRPCLine(t, conn, "tools/call", 1, map[string]any{
		"name": "status_line",
		"arguments": map[string]any{
			"session_id":      "ses-1",
			"transcript_path": "/path/to/ses-1.jsonl",
		},
	})
	_ = readJRPCLine(t, r)
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("sink not invoked")
	}
	if sid, _ := got["session_id"].(string); sid != "ses-1" {
		t.Errorf("sink got wrong session_id: %v", got)
	}
}

// statusLineSinkFunc adapts a closure to the StatusLineSink interface
// for test wiring without a real adapter.
type statusLineSinkFunc func(context.Context, map[string]any)

func (f statusLineSinkFunc) OnStatusLine(ctx context.Context, p map[string]any) {
	f(ctx, p)
}
