// M2 (structured stdio) driver — blueprint §5.3.1.
//
// Reads a newline-delimited JSON stream from the agent's stdout (Claude
// Code's `--output-format stream-json` is the canonical producer) and
// translates each frame into one or more `agent_events`. No control-plane
// back-channel is wired here: user→agent input handling lands with P1.8's
// input route + SSE subscription; this pass is agent→hub only.
//
// Producer attribution:
//   - lifecycle events (started/stopped) are producer=system.
//   - frames originating in the model's output (text / tool calls / tool
//     results / results / errors) are producer=agent.
package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// StdioDriver implements M2. It owns a reader against the child's stdout;
// production code also holds the *exec.Cmd so Stop can signal it, but the
// driver is transport-agnostic — tests wire an io.Pipe directly.
type StdioDriver struct {
	AgentID string
	Poster  AgentEventPoster
	Stdout  io.Reader // child stdout (line-delimited stream-json)
	// Stdin, if non-nil, is the child's stdin. Input (blueprint P1.8)
	// writes a stream-json user frame here per event. Drivers wired
	// without Stdin simply reject Input calls so the router logs the
	// gap instead of silently dropping user messages.
	Stdin io.Writer
	// Closer, if non-nil, is invoked by Stop to unblock a blocked reader
	// (typically the child's stdin + a kill on the *exec.Cmd).
	Closer func()
	Log    *slog.Logger

	mu      sync.Mutex
	started bool
	stopped bool
	wg      sync.WaitGroup
	inputMu sync.Mutex // serializes concurrent Input calls
}

// Start emits lifecycle.started and launches the reader goroutine. Returns
// immediately; frame translation happens in the background.
func (d *StdioDriver) Start(parent context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	d.mu.Unlock()

	if d.Log == nil {
		d.Log = slog.Default()
	}

	_ = d.Poster.PostAgentEvent(parent, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "started", "mode": "M2"})

	d.wg.Add(1)
	go d.readLoop(parent)
	return nil
}

// Stop signals the reader to unwind (via Closer, if provided), waits for
// it to drain, then emits lifecycle.stopped. Idempotent.
func (d *StdioDriver) Stop() {
	d.mu.Lock()
	if d.stopped || !d.started {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	closer := d.Closer
	d.mu.Unlock()

	if closer != nil {
		closer()
	}
	d.wg.Wait()

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = d.Poster.PostAgentEvent(shutCtx, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "stopped", "mode": "M2"})
}

// streamJSONBufferSize is the max frame size the scanner will accept. Tool
// inputs and results can be large; 1 MiB keeps us comfortably above typical
// Claude Code output without unbounded growth on a malformed stream.
const streamJSONBufferSize = 1 << 20

func (d *StdioDriver) readLoop(ctx context.Context) {
	defer d.wg.Done()
	sc := bufio.NewScanner(d.Stdout)
	sc.Buffer(make([]byte, 64*1024), streamJSONBufferSize)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal(line, &frame); err != nil {
			// Non-JSON line: pass through as raw so the app sees *something*
			// instead of silently dropping bytes.
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent",
				map[string]any{"text": string(line)})
			continue
		}
		d.translate(ctx, frame)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		d.Log.Debug("stdio read error", "agent", d.AgentID, "err", err)
	}
}

// translate maps a single stream-json frame to zero or more agent_events.
// Unknown `type` values are forwarded as producer=agent kind="raw" so the
// hub keeps a complete transcript even when the schema drifts.
func (d *StdioDriver) translate(ctx context.Context, frame map[string]any) {
	typ, _ := frame["type"].(string)
	switch typ {
	case "system":
		// system/init carries session_id + tool list + model; other system
		// subtypes are surfaced verbatim.
		sub, _ := frame["subtype"].(string)
		if sub == "init" {
			payload := map[string]any{
				"session_id": frame["session_id"],
				"model":      frame["model"],
				"tools":      frame["tools"],
			}
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "session.init", "agent", payload)
			return
		}
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent", frame)

	case "assistant":
		// Walk content blocks; each text block → text event, each tool_use
		// block → tool_call event. Other block types fall through to raw.
		msg, _ := frame["message"].(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			bt, _ := block["type"].(string)
			switch bt {
			case "text":
				text, _ := block["text"].(string)
				if text == "" {
					continue
				}
				_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "text", "agent",
					map[string]any{"text": text})
			case "tool_use":
				_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "tool_call", "agent",
					map[string]any{
						"id":    block["id"],
						"name":  block["name"],
						"input": block["input"],
					})
			default:
				_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent", block)
			}
		}

	case "user":
		// In stream-json, `user` frames carry tool_result blocks (the
		// agent's own view of tool outputs). Plain user-text frames are
		// our own input being echoed back; skip those to avoid a loop.
		msg, _ := frame["message"].(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			bt, _ := block["type"].(string)
			if bt != "tool_result" {
				continue
			}
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "tool_result", "agent",
				map[string]any{
					"tool_use_id": block["tool_use_id"],
					"content":     block["content"],
					"is_error":    block["is_error"],
				})
		}

	case "result":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "completion", "agent", frame)

	case "error":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "error", "agent", frame)

	default:
		// Unknown frame type — forward verbatim so the app can decide.
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent", frame)
	}
}

// Input implements Inputter for M2. text/cancel/approval/attach translate
// into the shapes Claude Code's stream-json reader accepts on stdin:
//   - text:     a "user" frame with content=[{type:"text", text:body}]
//   - approval: a "user" frame with content=[{type:"tool_result", …}] so
//               an approval for a pending tool_use clears through the
//               same channel the agent emitted the call on.
//   - cancel:   a "user" frame with a plain-text "cancel: <reason>" body.
//               Claude Code's CLI has no first-class cancel over stdio;
//               an explicit hub-level kill is handled by Stop() elsewhere.
//   - attach:   tool_result with a reference to the attached document id.
//
// Missing Stdin is a configuration error, not a runtime one — we surface
// it so the router can flag the agent instead of buffering forever.
func (d *StdioDriver) Input(ctx context.Context, kind string, payload map[string]any) error {
	if d.Stdin == nil {
		return fmt.Errorf("stdio driver: stdin not wired")
	}
	frame, err := buildStreamJSONInputFrame(kind, payload)
	if err != nil {
		return err
	}
	d.inputMu.Lock()
	defer d.inputMu.Unlock()
	// Context cancellation doesn't preempt the Write; a wedged child is
	// the operator's problem to kill via Stop. The lock keeps two
	// concurrent Input calls from interleaving bytes on stdin.
	_, werr := d.Stdin.Write(frame)
	return werr
}

// buildStreamJSONInputFrame produces the JSON-line bytes (with trailing
// newline) that Claude Code expects for a user-side message. Factored out
// so tests don't need a live pipe to assert on the wire shape.
func buildStreamJSONInputFrame(kind string, payload map[string]any) ([]byte, error) {
	var content []map[string]any
	switch kind {
	case "text":
		body, _ := payload["body"].(string)
		if body == "" {
			return nil, fmt.Errorf("stdio driver: text input missing body")
		}
		content = []map[string]any{{"type": "text", "text": body}}
	case "approval":
		reqID, _ := payload["request_id"].(string)
		decision, _ := payload["decision"].(string)
		if reqID == "" || decision == "" {
			return nil, fmt.Errorf("stdio driver: approval missing request_id/decision")
		}
		note, _ := payload["note"].(string)
		text := decision
		if note != "" {
			text = decision + ": " + note
		}
		content = []map[string]any{{
			"type":        "tool_result",
			"tool_use_id": reqID,
			"content":     text,
			"is_error":    decision == "deny",
		}}
	case "cancel":
		reason, _ := payload["reason"].(string)
		if reason == "" {
			reason = "user requested cancel"
		}
		content = []map[string]any{{"type": "text", "text": "cancel: " + reason}}
	case "attach":
		docID, _ := payload["document_id"].(string)
		if docID == "" {
			return nil, fmt.Errorf("stdio driver: attach missing document_id")
		}
		content = []map[string]any{{
			"type": "text",
			"text": "[attach] document_id=" + docID,
		}}
	default:
		return nil, fmt.Errorf("stdio driver: unsupported input kind %q", kind)
	}
	frame := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
	}
	b, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
