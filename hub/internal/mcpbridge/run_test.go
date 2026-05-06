package mcpbridge

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestForward_RoundTrip exercises the one-shot path: a request line goes in,
// the hub returns a JSON-RPC response, and forward() hands that body back
// untouched so main() can write it to stdout.
func TestForward_RoundTrip(t *testing.T) {
	var gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	defer srv.Close()

	endpoint := srv.URL + "/mcp/tok_xyz"
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := forward(client, endpoint, append(req, '\n'))
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if gotPath != "/mcp/tok_xyz" {
		t.Errorf("path = %q, want /mcp/tok_xyz", gotPath)
	}
	if !strings.Contains(string(gotBody), `"tools/list"`) {
		t.Errorf("hub never saw method: body=%s", gotBody)
	}
	var out map[string]any
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("response not valid json: %v", err)
	}
	if res, ok := out["result"].(map[string]any); !ok || res["ok"] != true {
		t.Errorf("unexpected result: %v", out)
	}
}

// TestForward_NotificationNoBody: when the hub replies with an empty body
// (notification semantics), forward must return (nil, nil) so main() skips
// the stdout write — writing an empty line would confuse clients that
// parse one JSON per line.
func TestForward_NotificationNoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := forward(client, srv.URL+"/mcp/tok", []byte(`{"jsonrpc":"2.0","method":"noop"}`))
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response for notification, got %s", resp)
	}
}

// TestMakeTransportError: when the HTTP call fails we must still return a
// well-formed JSON-RPC error keyed to the original request id so the MCP
// client can correlate it with its in-flight call.
func TestMakeTransportError(t *testing.T) {
	reqLine := []byte(`{"jsonrpc":"2.0","id":42,"method":"tools/call"}`)
	frame := makeTransportError(reqLine, errString("connection refused"))
	var parsed map[string]any
	if err := json.Unmarshal(frame, &parsed); err != nil {
		t.Fatalf("not json: %v (%s)", err, frame)
	}
	if parsed["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v", parsed["jsonrpc"])
	}
	// id can deserialize as float64 (JSON numbers) — compare via type-agnostic cast.
	if id, _ := parsed["id"].(float64); id != 42 {
		t.Errorf("id = %v, want 42", parsed["id"])
	}
	errObj, _ := parsed["error"].(map[string]any)
	if errObj["code"].(float64) != -32000 {
		t.Errorf("code = %v, want -32000", errObj["code"])
	}
	if !strings.Contains(errObj["data"].(string), "connection refused") {
		t.Errorf("data missing cause: %v", errObj["data"])
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestRunLoop_NormalizesTrailingNewline pins the framing contract that
// codex's rmcp transport requires: each stdout line must be a single
// complete JSON value followed by exactly one "\n", with no empty
// trailing line. The hub already terminates its body with "\n", so the
// previous "Write(body); WriteByte('\n')" path produced "...}}\n\n" and
// rmcp aborted the transport with "EOF while parsing a value at line 1
// column 0".
func TestRunLoop_NormalizesTrailingNewline(t *testing.T) {
	stdin := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")
	var stdout bytes.Buffer
	logger := log.New(io.Discard, "", 0)

	fwd := func(_ []byte) ([]byte, error) {
		// Mimic the hub: response body already ends with "\n".
		return []byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}` + "\n"), nil
	}

	rc := runLoop(stdin, &stdout, fwd, logger)
	if rc != 0 {
		t.Fatalf("runLoop rc = %d, want 0", rc)
	}

	out := stdout.Bytes()
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Fatalf("output must end with \\n: %q", out)
	}
	if bytes.HasSuffix(out, []byte("\n\n")) {
		t.Fatalf("output must not end with empty line: %q", out)
	}
	// Every line must be valid JSON — empty lines are fatal to rmcp.
	for i, line := range bytes.Split(bytes.TrimRight(out, "\n"), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			t.Errorf("line %d is empty (rmcp would abort)", i)
			continue
		}
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Errorf("line %d not valid json: %v (%q)", i, err, line)
		}
	}
}
