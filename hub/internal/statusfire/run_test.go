package statusfire

import (
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGateway accepts one MCP tools/call on a UDS, captures the
// arguments, and replies with an empty success body. Modelled on
// hookfire/run_test.go's fakeGateway (same wire shape).
type fakeGateway struct {
	socket  string
	gotReq  map[string]any
	stop    chan struct{}
	delayMS int // optional artificial latency before replying
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "fake.sock")
	g := &fakeGateway{socket: socket, stop: make(chan struct{})}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		<-g.stop
		_ = ln.Close()
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
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
	if g.delayMS > 0 {
		time.Sleep(time.Duration(g.delayMS) * time.Millisecond)
	}
	out := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"content": []any{map[string]any{
				"type": "text",
				"text": "{\"ok\":true}",
			}},
		},
	}
	b, _ := json.Marshal(out)
	_, _ = conn.Write(append(b, '\n'))
}

func (g *fakeGateway) Close() { close(g.stop) }

// transport delivers a status_line tools/call carrying the payload, and
// the gateway captures it. The shim's job is just to POST — the
// caller verifies via the gateway capture.
func TestTransport_RoundTrip(t *testing.T) {
	g := newFakeGateway(t)
	defer g.Close()

	err := transport(g.socket, map[string]any{
		"session_id":     "abc",
		"model":          map[string]any{"id": "claude-opus-4-7"},
		"context_window": map[string]any{"context_window_size": 1_000_000},
	}, 2*time.Second, 5*time.Second)
	if err != nil {
		t.Fatalf("transport: %v", err)
	}

	if g.gotReq == nil {
		t.Fatal("gateway captured no request")
	}
	method, _ := g.gotReq["method"].(string)
	if method != "tools/call" {
		t.Errorf("method = %q, want tools/call", method)
	}
	params, _ := g.gotReq["params"].(map[string]any)
	name, _ := params["name"].(string)
	if name != Tool {
		t.Errorf("tool name = %q, want %q", name, Tool)
	}
	args, _ := params["arguments"].(map[string]any)
	if got, _ := args["session_id"].(string); got != "abc" {
		t.Errorf("payload not preserved: session_id = %q", got)
	}
}

// Dial timeout: shim's transport returns an error within the dial
// budget when the socket doesn't exist. The Run() wrapper turns this
// into a quiet exit-0; transport itself surfaces the error.
func TestTransport_SocketGone(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.sock")
	err := transport(missing, map[string]any{}, 100*time.Millisecond, 1*time.Second)
	if err == nil {
		t.Fatal("expected error for missing socket")
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Errorf("error not from dial: %v", err)
	}
}
