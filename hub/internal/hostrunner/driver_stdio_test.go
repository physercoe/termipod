package hostrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStdioDriver_TranslatesStreamJSON feeds a canned stream-json transcript
// through an io.Pipe and asserts the emitted agent_events match the
// blueprint's translation table: session.init → text → tool_call →
// tool_result → completion, bracketed by lifecycle.started / .stopped.
func TestStdioDriver_TranslatesStreamJSON(t *testing.T) {
	frames := []string{
		`{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-opus-4","tools":["Read","Edit"]}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me read it."}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu-1","name":"Read","input":{"path":"/tmp/x"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-1","content":"file contents","is_error":false}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"duration_ms":123,"result":"done"}`,
	}
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{
		AgentID: "agent-m2",
		Poster:  poster,
		Stdout:  pr,
		Closer:  func() { _ = pw.Close() },
	}
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	go func() {
		for _, f := range frames {
			_, _ = pw.Write([]byte(f + "\n"))
		}
	}()

	// Expect: lifecycle.started + 5 translated events = 6. Stop adds one more.
	poster.wait(t, 6, 2*time.Second)
	drv.Stop()
	poster.wait(t, 7, time.Second)

	evs := poster.snapshot()

	// Event 0: lifecycle.started / system.
	if evs[0].Kind != "lifecycle" || evs[0].Producer != "system" ||
		evs[0].Payload["phase"] != "started" || evs[0].Payload["mode"] != "M2" {
		t.Fatalf("evs[0] want lifecycle.started/system/M2; got %+v", evs[0])
	}

	// Event 1: session.init with session_id + model.
	if evs[1].Kind != "session.init" || evs[1].Producer != "agent" {
		t.Fatalf("evs[1] want session.init/agent; got %+v", evs[1])
	}
	if evs[1].Payload["session_id"] != "sess-1" {
		t.Fatalf("evs[1] session_id = %v; want sess-1", evs[1].Payload["session_id"])
	}
	if evs[1].Payload["model"] != "claude-opus-4" {
		t.Fatalf("evs[1] model = %v; want claude-opus-4", evs[1].Payload["model"])
	}

	// Event 2: text.
	if evs[2].Kind != "text" || evs[2].Producer != "agent" ||
		evs[2].Payload["text"] != "Let me read it." {
		t.Fatalf("evs[2] want text=\"Let me read it.\"; got %+v", evs[2])
	}

	// Event 3: tool_call.
	if evs[3].Kind != "tool_call" || evs[3].Producer != "agent" ||
		evs[3].Payload["id"] != "tu-1" || evs[3].Payload["name"] != "Read" {
		t.Fatalf("evs[3] want tool_call tu-1/Read; got %+v", evs[3])
	}
	if inp, _ := evs[3].Payload["input"].(map[string]any); inp["path"] != "/tmp/x" {
		t.Fatalf("evs[3] input.path = %v; want /tmp/x", inp["path"])
	}

	// Event 4: tool_result.
	if evs[4].Kind != "tool_result" || evs[4].Producer != "agent" ||
		evs[4].Payload["tool_use_id"] != "tu-1" ||
		evs[4].Payload["content"] != "file contents" {
		t.Fatalf("evs[4] want tool_result tu-1; got %+v", evs[4])
	}

	// Event 5: completion — we pass the whole frame through.
	if evs[5].Kind != "completion" || evs[5].Producer != "agent" ||
		evs[5].Payload["subtype"] != "success" {
		t.Fatalf("evs[5] want completion/success; got %+v", evs[5])
	}

	// Event 6: lifecycle.stopped.
	if evs[6].Kind != "lifecycle" || evs[6].Payload["phase"] != "stopped" {
		t.Fatalf("evs[6] want lifecycle.stopped; got %+v", evs[6])
	}
}

// TestStdioDriver_NonJSONLineForwardedAsRaw guards the "don't silently drop
// bytes" behaviour: if the child emits a stray non-JSON line (stderr bleed,
// prompt, whatever) it should surface as a raw event, not disappear.
func TestStdioDriver_NonJSONLineForwardedAsRaw(t *testing.T) {
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{
		AgentID: "agent-m2b",
		Poster:  poster,
		Stdout:  pr,
		Closer:  func() { _ = pw.Close() },
	}
	_ = drv.Start(context.Background())
	go func() {
		_, _ = pw.Write([]byte("not json at all\n"))
	}()
	poster.wait(t, 2, time.Second) // started + raw
	drv.Stop()

	evs := poster.snapshot()
	var rawText string
	for _, e := range evs {
		if e.Kind == "raw" && e.Producer == "agent" {
			rawText, _ = e.Payload["text"].(string)
		}
	}
	if !strings.Contains(rawText, "not json") {
		t.Fatalf("expected raw event with non-JSON text; got events=%+v", evs)
	}
}

// syncBuf is a thread-safe bytes.Buffer so Input writes and test reads
// don't race the scanner goroutine.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, s.buf.Len())
	copy(out, s.buf.Bytes())
	return out
}

// TestStdioDriver_InputFrames asserts Input translates each kind into the
// stream-json user frame shape Claude Code expects on stdin.
func TestStdioDriver_InputFrames(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		payload map[string]any
		check   func(t *testing.T, frame map[string]any)
	}{
		{
			name:    "text",
			kind:    "text",
			payload: map[string]any{"body": "hello agent"},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				content := msg["content"].([]any)
				block := content[0].(map[string]any)
				if block["type"] != "text" || block["text"] != "hello agent" {
					t.Fatalf("text block wrong: %+v", block)
				}
			},
		},
		{
			name: "approval",
			kind: "approval",
			payload: map[string]any{
				"request_id": "toolu_42",
				"decision":   "allow",
				"note":       "looks fine",
			},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if block["type"] != "tool_result" || block["tool_use_id"] != "toolu_42" ||
					block["content"] != "allow: looks fine" || block["is_error"] != false {
					t.Fatalf("approval block wrong: %+v", block)
				}
			},
		},
		{
			name: "approval deny",
			kind: "approval",
			payload: map[string]any{
				"request_id": "toolu_99",
				"decision":   "deny",
			},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if block["is_error"] != true {
					t.Fatalf("deny should set is_error=true; got %+v", block)
				}
			},
		},
		{
			name:    "cancel",
			kind:    "cancel",
			payload: map[string]any{"reason": "too slow"},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if block["type"] != "text" || !strings.Contains(block["text"].(string), "too slow") {
					t.Fatalf("cancel block wrong: %+v", block)
				}
			},
		},
		{
			name:    "attach",
			kind:    "attach",
			payload: map[string]any{"document_id": "doc-7"},
			check: func(t *testing.T, frame map[string]any) {
				msg := frame["message"].(map[string]any)
				block := msg["content"].([]any)[0].(map[string]any)
				if !strings.Contains(block["text"].(string), "doc-7") {
					t.Fatalf("attach block missing doc-7: %+v", block)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := buildStreamJSONInputFrame(c.kind, c.payload)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if len(b) == 0 || b[len(b)-1] != '\n' {
				t.Fatal("frame must end with newline")
			}
			var frame map[string]any
			if err := json.Unmarshal(b, &frame); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if frame["type"] != "user" {
				t.Fatalf("want type=user; got %v", frame["type"])
			}
			c.check(t, frame)
		})
	}
}

func TestStdioDriver_InputMissingFields(t *testing.T) {
	if _, err := buildStreamJSONInputFrame("text", map[string]any{}); err == nil {
		t.Fatal("text without body should error")
	}
	if _, err := buildStreamJSONInputFrame("approval", map[string]any{"decision": "allow"}); err == nil {
		t.Fatal("approval without request_id should error")
	}
	if _, err := buildStreamJSONInputFrame("attach", map[string]any{}); err == nil {
		t.Fatal("attach without document_id should error")
	}
	if _, err := buildStreamJSONInputFrame("bogus", map[string]any{}); err == nil {
		t.Fatal("unknown kind should error")
	}
}

func TestStdioDriver_InputWritesToStdin(t *testing.T) {
	sink := &syncBuf{}
	pr, pw := io.Pipe()
	defer pw.Close()
	drv := &StdioDriver{
		AgentID: "agent-input",
		Poster:  &fakePoster{},
		Stdout:  pr,
		Stdin:   sink,
		Closer:  func() { _ = pw.Close() },
	}
	_ = drv.Start(context.Background())
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"}); err != nil {
		t.Fatalf("Input: %v", err)
	}
	got := string(sink.Bytes())
	if !strings.Contains(got, `"type":"user"`) || !strings.Contains(got, `"text":"hi"`) {
		t.Fatalf("stdin missing expected frame; got %q", got)
	}
}

func TestStdioDriver_InputRejectsWithoutStdin(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	drv := &StdioDriver{
		AgentID: "agent-nostdin",
		Poster:  &fakePoster{},
		Stdout:  pr,
		Closer:  func() { _ = pw.Close() },
	}
	_ = drv.Start(context.Background())
	defer drv.Stop()

	if err := drv.Input(context.Background(), "text", map[string]any{"body": "hi"}); err == nil {
		t.Fatal("expected error when Stdin is nil")
	}
}

func TestStdioDriver_StopIsIdempotent(t *testing.T) {
	pr, pw := io.Pipe()
	poster := &fakePoster{}
	drv := &StdioDriver{
		AgentID: "agent-m2c",
		Poster:  poster,
		Stdout:  pr,
		Closer:  func() { _ = pw.Close() },
	}
	_ = drv.Start(context.Background())
	drv.Stop()
	drv.Stop() // must not panic or double-emit stopped
}
