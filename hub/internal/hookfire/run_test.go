package hookfire

import (
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGateway is a stripped-down stand-in for hostrunner.McpGateway —
// it accepts a single connection on a UDS, reads ONE JSON-RPC request,
// invokes a handler, and writes the reply. Lets us exercise the shim
// without spinning up the real gateway + adapter machinery.
type fakeGateway struct {
	socket string
	resp   map[string]any // body to put inside result.content[0].text
	gotReq map[string]any // captured request for assertions
	stop   chan struct{}
}

func newFakeGateway(t *testing.T, resp map[string]any) *fakeGateway {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "fake.sock")
	g := &fakeGateway{socket: socket, resp: resp, stop: make(chan struct{})}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		<-g.stop
		_ = ln.Close()
	}()
	// Accept-loop: each connection gets its own handler goroutine so a
	// repeated dial (e.g. from RoundTrip warm-up) doesn't starve the
	// real request.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go g.handle(conn)
		}
	}()
	return g
}

func (g *fakeGateway) handle(conn net.Conn) {
	defer conn.Close()
	buf, _ := io.ReadAll(conn)
	for _, l := range strings.Split(string(buf), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var req map[string]any
		_ = json.Unmarshal([]byte(l), &req)
		if len(req) > 0 {
			g.gotReq = req
		}
		break
	}
	respText, _ := json.Marshal(g.resp)
	out := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"content": []any{map[string]any{
				"type": "text",
				"text": string(respText),
			}},
		},
	}
	b, _ := json.Marshal(out)
	_, _ = conn.Write(append(b, '\n'))
}

func (g *fakeGateway) Close() {
	close(g.stop)
}

// Happy path: shim reads stdin payload, wraps as tools/call, dials
// UDS, unwraps result.content[0].text, prints to stdout, exits 0.
func TestTransport_RoundTrip(t *testing.T) {
	g := newFakeGateway(t, map[string]any{"decision": "allow"})
	defer g.Close()

	got, err := transport(g.socket, "hook_pre_tool_use", map[string]any{
		"tool_name": "Bash",
		"args":      "ls",
	}, 2*time.Second, 5*time.Second)
	if err != nil {
		t.Fatalf("transport: %v", err)
	}

	var unwrapped map[string]any
	if err := json.Unmarshal(got, &unwrapped); err != nil {
		t.Fatalf("unmarshal response: %v (body=%s)", err, got)
	}
	if unwrapped["decision"] != "allow" {
		t.Errorf("response decision = %v, want allow", unwrapped["decision"])
	}

	// Verify the request shape the gateway saw.
	if g.gotReq == nil {
		t.Fatalf("gateway received no request")
	}
	if g.gotReq["method"] != "tools/call" {
		t.Errorf("method = %v, want tools/call", g.gotReq["method"])
	}
	params, _ := g.gotReq["params"].(map[string]any)
	if params["name"] != "hook_pre_tool_use" {
		t.Errorf("params.name = %v, want hook_pre_tool_use", params["name"])
	}
	args, _ := params["arguments"].(map[string]any)
	if args["tool_name"] != "Bash" {
		t.Errorf("params.arguments.tool_name = %v, want Bash", args["tool_name"])
	}
}

// A gateway that returns a JSON-RPC error MUST surface that as a
// transport error from the shim's perspective; the caller (claude)
// then sees stderr + an empty `{}` on stdout (handled in Run()).
func TestTransport_GatewayErrorSurfaces(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "fake.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.ReadAll(conn)
		errFrame := `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"unknown tool: hook_x"}}` + "\n"
		_, _ = conn.Write([]byte(errFrame))
	}()

	_, err = transport(socket, "hook_x", map[string]any{}, 2*time.Second, 2*time.Second)
	if err == nil {
		t.Fatal("expected gateway error to surface")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("err = %v; want mention of gateway error message", err)
	}
}

// A missing socket should produce a dial error, not crash.
func TestTransport_DialFailure(t *testing.T) {
	_, err := transport("/nonexistent/socket", "hook_stop", map[string]any{},
		200*time.Millisecond, 1*time.Second)
	if err == nil {
		t.Fatal("expected dial failure")
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Errorf("err = %v; want mention of dial", err)
	}
}

// The Event→Tool map must cover every claude-code event we install
// hooks for. A new event in hooks_install.go without a matching entry
// here is the same defect class that broke v1.0.592 → v1.0.659; lock
// the contract.
func TestEventToToolName_Complete(t *testing.T) {
	want := []string{
		"PreToolUse", "PostToolUse", "Notification",
		"PreCompact", "Stop", "SubagentStop",
		"UserPromptSubmit", "SessionStart", "SessionEnd",
	}
	if len(EventToToolName) != len(want) {
		t.Errorf("len = %d, want %d", len(EventToToolName), len(want))
	}
	for _, e := range want {
		if _, ok := EventToToolName[e]; !ok {
			t.Errorf("missing event %q", e)
		}
	}
}

// Run() with empty --socket should exit 2 (usage error). Locks the
// CLI contract.
func TestRun_RejectsMissingSocket(t *testing.T) {
	code := Run([]string{"--event", "Stop"})
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestRun_RejectsUnknownEvent(t *testing.T) {
	code := Run([]string{"--socket", "/tmp/x", "--event", "BogusEvent"})
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
