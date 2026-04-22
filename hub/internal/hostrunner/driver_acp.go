// M1 (ACP) driver — blueprint §5.3.1.
//
// Zed's Agent Client Protocol is JSON-RPC 2.0 over stdio: one JSON object
// per line, roles reversed from MCP — here the *agent* is the server and
// host-runner is the client. This shim performs the minimum handshake
// (`initialize` + `session/new`), then translates `session/update`
// notifications into `agent_events`. Requests that flow the other way
// (`fs/read_text_file`, `terminal/*`, etc.) are rejected with
// method-not-found until a client capability surface lands in a later
// pass; agents are expected to tolerate that gracefully by falling back
// to internal tooling.
//
// Producer attribution matches the other drivers: lifecycle events are
// producer=system, translated agent frames are producer=agent.
package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ACPDriver implements M1. Transport is an io.Reader/Writer pair so tests
// can drive it with io.Pipe; production wires it to the child's
// stdout/stdin plus a Closer that closes stdin and kills the process.
type ACPDriver struct {
	AgentID string
	Poster  AgentEventPoster
	Stdin   io.Writer // messages TO the agent
	Stdout  io.Reader // messages FROM the agent
	Closer  func()
	Log     *slog.Logger

	// HandshakeTimeout caps initialize + session/new. 0 → 10s.
	HandshakeTimeout time.Duration

	mu        sync.Mutex
	started   bool
	stopped   bool
	writerMu  sync.Mutex // serialises stdin writes
	wg        sync.WaitGroup
	nextID    atomic.Int64
	pendingMu sync.Mutex
	pending   map[int64]chan acpResponse
	sessionID string
}

type acpResponse struct {
	result json.RawMessage
	err    *acpError
}

type acpError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// acpMessage is a union of request/response/notification; absence of a
// field disambiguates which. Using json.RawMessage for the flexible parts
// defers decoding until we know the shape we care about.
type acpMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *acpError        `json:"error,omitempty"`
}

// Start performs the handshake, emits lifecycle.started, and launches the
// reader loop. Returns an error if the handshake fails — the caller should
// log and fall back to M2/M4.
func (d *ACPDriver) Start(parent context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	d.pending = make(map[int64]chan acpResponse)
	d.mu.Unlock()

	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.HandshakeTimeout == 0 {
		d.HandshakeTimeout = 10 * time.Second
	}

	d.wg.Add(1)
	go d.readLoop(parent)

	hsCtx, cancel := context.WithTimeout(parent, d.HandshakeTimeout)
	defer cancel()

	// initialize — announce protocol version; we accept whatever the agent
	// returns (capability negotiation is out of scope for this shim).
	if _, err := d.call(hsCtx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
	}); err != nil {
		return fmt.Errorf("acp initialize: %w", err)
	}

	// session/new — capture sessionId so we can correlate updates.
	sres, err := d.call(hsCtx, "session/new", map[string]any{
		"cwd":             "",
		"mcpServers":      []any{},
		"clientMetadata":  map[string]any{"name": "termipod-hostrunner"},
	})
	if err != nil {
		return fmt.Errorf("acp session/new: %w", err)
	}
	var sr struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(sres, &sr)
	d.mu.Lock()
	d.sessionID = sr.SessionID
	d.mu.Unlock()

	_ = d.Poster.PostAgentEvent(parent, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "started", "mode": "M1", "session_id": sr.SessionID})
	return nil
}

// Stop closes the transport (which unblocks the reader), waits, and emits
// lifecycle.stopped. Idempotent.
func (d *ACPDriver) Stop() {
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

	// Drain any stragglers waiting on responses so callers don't leak.
	d.pendingMu.Lock()
	for id, ch := range d.pending {
		close(ch)
		delete(d.pending, id)
	}
	d.pendingMu.Unlock()

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = d.Poster.PostAgentEvent(shutCtx, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "stopped", "mode": "M1"})
}

// call sends a request and blocks until the matching response arrives,
// the context deadline fires, or the transport closes.
func (d *ACPDriver) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := d.nextID.Add(1)
	ch := make(chan acpResponse, 1)
	d.pendingMu.Lock()
	d.pending[id] = ch
	d.pendingMu.Unlock()

	if err := d.writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		d.pendingMu.Lock()
		delete(d.pending, id)
		d.pendingMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		d.pendingMu.Lock()
		delete(d.pending, id)
		d.pendingMu.Unlock()
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("transport closed")
		}
		if resp.err != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.err.Code, resp.err.Message)
		}
		return resp.result, nil
	}
}

func (d *ACPDriver) writeMsg(m any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	d.writerMu.Lock()
	defer d.writerMu.Unlock()
	if _, err := d.Stdin.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (d *ACPDriver) readLoop(ctx context.Context) {
	defer d.wg.Done()
	sc := bufio.NewScanner(d.Stdout)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg acpMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent",
				map[string]any{"text": string(line)})
			continue
		}
		// Response: has result or error and an id that matches a pending call.
		if msg.ID != nil && (msg.Result != nil || msg.Error != nil) && msg.Method == "" {
			d.deliverResponse(msg)
			continue
		}
		// Request from agent (has id + method): reject with method-not-found.
		if msg.ID != nil && msg.Method != "" {
			_ = d.writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(*msg.ID),
				"error": map[string]any{
					"code":    -32601,
					"message": "method not found: " + msg.Method,
				},
			})
			continue
		}
		// Notification (no id): dispatch by method.
		if msg.Method != "" {
			d.handleNotification(ctx, msg.Method, msg.Params)
			continue
		}
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		d.Log.Debug("acp read error", "agent", d.AgentID, "err", err)
	}
}

func (d *ACPDriver) deliverResponse(msg acpMessage) {
	var idNum int64
	if err := json.Unmarshal(*msg.ID, &idNum); err != nil {
		return
	}
	d.pendingMu.Lock()
	ch, ok := d.pending[idNum]
	if ok {
		delete(d.pending, idNum)
	}
	d.pendingMu.Unlock()
	if !ok {
		return
	}
	ch <- acpResponse{result: msg.Result, err: msg.Error}
}

// handleNotification is the translation hot path. session/update carries
// the structured content chunks we care about; everything else falls
// through to a raw event so no information is lost.
func (d *ACPDriver) handleNotification(ctx context.Context, method string, params json.RawMessage) {
	if method != "session/update" {
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent",
			map[string]any{"method": method, "params": params})
		return
	}
	var p struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent",
			map[string]any{"method": method, "params": params})
		return
	}
	var u map[string]any
	if err := json.Unmarshal(p.Update, &u); err != nil {
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent",
			map[string]any{"method": method, "update": string(p.Update)})
		return
	}
	kind, _ := u["sessionUpdate"].(string)
	switch kind {
	case "agent_message_chunk", "agent_thought_chunk":
		text := extractContentText(u["content"])
		if text == "" {
			return
		}
		ekind := "text"
		if kind == "agent_thought_chunk" {
			ekind = "thought"
		}
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, ekind, "agent",
			map[string]any{"text": text})
	case "tool_call":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "tool_call", "agent",
			map[string]any{
				"id":     u["toolCallId"],
				"name":   u["title"],
				"kind":   u["kind"],
				"status": u["status"],
				"input":  u["rawInput"],
			})
	case "tool_call_update":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "tool_call_update", "agent", u)
	case "plan":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "plan", "agent", u)
	case "diff":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "diff", "agent", u)
	case "user_message_chunk":
		// Our own input being echoed back — drop to avoid a loop.
		return
	default:
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent", u)
	}
}

// extractContentText pulls a text payload out of an ACP content block.
// ACP content is shaped like { type: "text", text: "..." }; we also
// tolerate the shape appearing as a list of such blocks.
func extractContentText(v any) string {
	switch c := v.(type) {
	case map[string]any:
		if t, _ := c["type"].(string); t == "text" {
			s, _ := c["text"].(string)
			return s
		}
	case []any:
		var buf []byte
		for _, item := range c {
			m, _ := item.(map[string]any)
			if t, _ := m["type"].(string); t == "text" {
				if s, _ := m["text"].(string); s != "" {
					buf = append(buf, s...)
				}
			}
		}
		return string(buf)
	}
	return ""
}
