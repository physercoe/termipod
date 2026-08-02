package mcpbridge

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/termipod/hub/internal/mcpwire"
)

// Plan lane B2: this relay stamps the standard MCP request headers on its
// HTTP leg so the hub can classify, meter, and ring-audit a call without
// parsing the body. It stays a byte pump in every other respect — the
// frame itself is forwarded verbatim, whatever it is.

// captureHeaders runs one frame through forward() against a recording
// server and returns what arrived.
func captureHeaders(t *testing.T, frame string) http.Header {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()
	if _, err := forward(&http.Client{Timeout: 5 * time.Second}, srv.URL, []byte(frame)); err != nil {
		t.Fatalf("forward: %v", err)
	}
	return got
}

func TestForward_StampsMcpMethodHeader(t *testing.T) {
	got := captureHeaders(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if h := got.Get(mcpwire.HeaderMethod); h != "tools/list" {
		t.Errorf("%s = %q, want tools/list", mcpwire.HeaderMethod, h)
	}
	// Mcp-Name names the TOOL, so a method that calls none must not
	// invent one — a downstream classifier reading it would attribute
	// this frame to whatever tool was named last.
	if h := got.Get(mcpwire.HeaderName); h != "" {
		t.Errorf("%s = %q on a non-tools/call frame; want it absent", mcpwire.HeaderName, h)
	}
}

func TestForward_StampsMcpNameOnAToolCall(t *testing.T) {
	got := captureHeaders(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"documents_list","arguments":{}}}`)
	if h := got.Get(mcpwire.HeaderMethod); h != "tools/call" {
		t.Errorf("%s = %q", mcpwire.HeaderMethod, h)
	}
	if h := got.Get(mcpwire.HeaderName); h != "documents_list" {
		t.Errorf("%s = %q, want documents_list", mcpwire.HeaderName, h)
	}
}

// The relay is not a validator. An unparseable frame still goes to the
// hub, unstamped — the hub is the authority on what is valid JSON-RPC,
// and a relay rejecting frames on its own weaker reading would be a
// second parser in the path with its own disagreements.
func TestForward_UnparseableFrameIsForwardedUnstamped(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		gotBody = buf[:n]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`))
	}))
	defer srv.Close()

	const junk = `{not json`
	if _, err := forward(&http.Client{Timeout: 5 * time.Second}, srv.URL, []byte(junk)); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if string(gotBody) != junk {
		t.Errorf("body = %q, want the frame forwarded verbatim", gotBody)
	}
	if h := gotHeaders.Get(mcpwire.HeaderMethod); h != "" {
		t.Errorf("%s = %q; an unparsed frame must not be labelled", mcpwire.HeaderMethod, h)
	}
}

// A well-formed JSON-RPC notification (no id) is a frame like any other
// here — it is still worth labelling, because metering counts it.
func TestForward_StampsNotificationsToo(t *testing.T) {
	got := captureHeaders(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if h := got.Get(mcpwire.HeaderMethod); h != "notifications/initialized" {
		t.Errorf("%s = %q", mcpwire.HeaderMethod, h)
	}
}

// A PARSEABLE frame whose tool name cannot live in an HTTP header value
// (control bytes) must still reach the hub: Go's client rejects such a
// header at send time, so stamping it verbatim would kill the frame at
// the relay with a transport error, when the hub would have answered it
// with a proper JSON-RPC error. The method is stampable here, so the
// method still is stamped; only the hostile name is skipped.
func TestForward_HostileToolNameIsSkippedNotFatal(t *testing.T) {
	got := captureHeaders(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"evil\r\nX-Inject: 1","arguments":{}}}`)
	if h := got.Get(mcpwire.HeaderMethod); h != "tools/call" {
		t.Errorf("%s = %q; a valid method is still worth labelling", mcpwire.HeaderMethod, h)
	}
	if h := got.Get(mcpwire.HeaderName); h != "" {
		t.Errorf("%s = %q; an unstampable name must be skipped", mcpwire.HeaderName, h)
	}
}
