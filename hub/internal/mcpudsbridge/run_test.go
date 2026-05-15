package mcpudsbridge

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGateway is a minimal UDS echo server for testing pump(). Whatever
// it reads on the connection it writes back; closes when the client
// half-closes write.
type fakeGateway struct {
	t        *testing.T
	listener net.Listener
	wg       sync.WaitGroup
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	dir := t.TempDir()
	// Keep the path short — sun_path on Linux is 108 bytes.
	path := filepath.Join(dir, "g.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	g := &fakeGateway{t: t, listener: l}
	g.wg.Add(1)
	go g.accept()
	return g
}

func (g *fakeGateway) Path() string { return g.listener.Addr().String() }

func (g *fakeGateway) accept() {
	defer g.wg.Done()
	for {
		c, err := g.listener.Accept()
		if err != nil {
			return
		}
		g.wg.Add(1)
		go func(c net.Conn) {
			defer g.wg.Done()
			defer c.Close()
			_, _ = io.Copy(c, c)
		}(c)
	}
}

func (g *fakeGateway) Close() {
	_ = g.listener.Close()
	g.wg.Wait()
}

func TestPumpEchoesLinesBothDirections(t *testing.T) {
	g := newFakeGateway(t)
	defer g.Close()

	conn, err := net.Dial("unix", g.Path())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n")
	var out bytes.Buffer

	doneCh := make(chan int, 1)
	go func() {
		doneCh <- pump(in, &out, conn)
	}()

	select {
	case rc := <-doneCh:
		if rc != 0 {
			t.Errorf("pump rc = %d, want 0", rc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pump did not return within 2s")
	}

	got := out.String()
	if !strings.Contains(got, `"method":"ping"`) {
		t.Errorf("output missing echoed payload: %q", got)
	}
	if strings.Count(got, "\n") < 2 {
		t.Errorf("expected at least 2 echoed lines, got %d in %q",
			strings.Count(got, "\n"), got)
	}
}

func TestPumpExitsWhenStdinEOFs(t *testing.T) {
	g := newFakeGateway(t)
	defer g.Close()

	conn, err := net.Dial("unix", g.Path())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Empty stdin: pump should detect EOF on stdin, half-close the
	// UDS, the echo server returns, the stdout side returns, and
	// pump returns 0.
	in := strings.NewReader("")
	var out bytes.Buffer

	doneCh := make(chan int, 1)
	go func() { doneCh <- pump(in, &out, conn) }()

	select {
	case rc := <-doneCh:
		if rc != 0 {
			t.Errorf("pump rc = %d, want 0", rc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pump did not return within 2s on empty stdin")
	}
}

func TestRunReturnsErrorOnMissingSocketFlag(t *testing.T) {
	// Run reads from os.Stdin/Stderr; with no args + no env, expect rc=2.
	_ = os.Unsetenv("MCP_UDS_SOCKET")
	rc := Run([]string{})
	if rc != 2 {
		t.Errorf("Run rc = %d, want 2 on missing --socket", rc)
	}
}

func TestRunReturnsErrorOnUndialableSocket(t *testing.T) {
	// Path that definitely doesn't exist.
	dir := t.TempDir()
	bogus := filepath.Join(dir, "does-not-exist.sock")
	rc := Run([]string{"--socket", bogus, "--dial-timeout", "100ms"})
	if rc != 1 {
		t.Errorf("Run rc = %d, want 1 on undialable socket", rc)
	}
}
