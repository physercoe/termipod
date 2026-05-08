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
	"errors"
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
	// WriteTimeout caps the time any single outbound frame may spend
	// blocked — both in the queue (backpressure) and in the final
	// Stdin.Write (child stuck not reading). 0 → 5s. On timeout the
	// caller gets an error immediately; any orphaned blocked Write
	// unwinds when Stop closes the transport.
	WriteTimeout time.Duration
	// PromptTimeout caps how long a single session/prompt call may wait
	// for the agent's reply. 0 → 120s. The mobile UI's "agent busy"
	// state is bounded by this — without it, an unauthenticated daemon
	// (gemini-cli without GEMINI_API_KEY) silently hangs forever, and
	// the mobile cancel button is the only way out. On timeout the
	// caller gets context.DeadlineExceeded; the agent record stays in
	// place so the operator can retry after fixing creds.
	PromptTimeout time.Duration

	// RPCLog, when non-nil, receives a JSONL trace of every JSON-RPC
	// frame in both directions: each line is a wrapping object with
	// `t` (UTC timestamp), `dir` (`out` = driver→agent, `in` =
	// agent→driver), and `frame` (the raw RPC message). M1 launch wires
	// this to a sibling `*-rpc.jsonl` file so operators can replay the
	// exact wire conversation when diagnosing hangs — the existing
	// stdout log only shows what the agent *sent back*, not what the
	// driver wrote. nil disables logging.
	RPCLog io.Writer
	// rpcLogMu serializes RPCLog writes — readLoop and writerLoop both
	// log frames concurrently and io.Writer is not generally
	// thread-safe.
	rpcLogMu sync.Mutex

	mu      sync.Mutex
	started bool
	stopped bool
	wg      sync.WaitGroup
	nextID  atomic.Int64

	// writeQ + done decouple callers from the actual stdin write. A
	// single writerLoop goroutine drains writeQ, which means we never
	// fan out goroutines blocked on a stuck pipe — callers just see a
	// backpressure error and stop calling. done is a one-shot tombstone
	// closed by Stop so every select in writeMsg unblocks.
	writeQ chan *acpWriteReq
	done   chan struct{}

	pendingMu sync.Mutex
	pending   map[int64]chan acpResponse
	// promptIDs tracks JSON-RPC ids that were issued for session/prompt
	// requests. deliverResponse uses this to recognize an orphaned
	// session/prompt response (one whose call already timed out or
	// was abandoned) and post a synthetic turn.result so mobile's
	// busy-walker still sees a terminal kind. Cleared when the call
	// returns OR when the orphaned-response path consumes the id.
	promptIDs map[int64]struct{}
	// permMu protects pendingPerm; we need a separate lock because the
	// reader (recording a request_id) and Input (resolving it) can race.
	permMu      sync.Mutex
	pendingPerm map[string]json.RawMessage // request_id → original JSON-RPC id
	sessionID   string

	// Per-turn streaming aggregator state. gemini-cli emits
	// `agent_message_chunk` and `agent_thought_chunk` notifications
	// during a session/prompt — each chunk is incremental, not
	// cumulative. Without aggregation each chunk would render as a
	// separate bubble in the mobile transcript. We accumulate per-turn
	// and emit cumulative `kind=text, partial:true, message_id=<id>`
	// events so the existing mobile `_collapseStreamingPartials` chain
	// folds them into one bubble that grows. message_ids are turn-
	// local: regenerated at every Input("text"/"attach") entry so
	// turn N+1's chunks don't merge into turn N's bubble.
	turnMu           sync.Mutex
	turnTextBuf      []byte
	turnTextMsgID    string
	turnThoughtBuf   []byte
	turnThoughtMsgID string
}

type acpWriteReq struct {
	frame []byte
	// done is buffered (cap 1) — the writer does a non-blocking send so
	// a caller that timed out doesn't strand the writerLoop.
	done chan error
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
	d.promptIDs = make(map[int64]struct{})
	d.pendingPerm = make(map[string]json.RawMessage)
	d.writeQ = make(chan *acpWriteReq, 32)
	d.done = make(chan struct{})
	d.mu.Unlock()

	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.HandshakeTimeout == 0 {
		// Per-call budget for each handshake step (initialize and
		// session/new each get their own d.HandshakeTimeout window —
		// not a shared budget). 90s is the right floor for engines
		// that fold real work into one of the calls. Observed in
		// production: gemini-cli's `initialize` on a cold daemon
		// takes 30-50s on its own (fnm shim → node startup with a
		// large heap → model-list fetch → OAuth refresh on the same
		// path) before responding; sharing a 60s budget left
		// session/new starved at ~17s and tipped the launch into
		// the M2 fallback even though M1 was minutes away from
		// succeeding. Engines that trigger a *full* interactive
		// OAuth flow still trip this — that's a setup bug we want
		// surfaced, not papered over.
		d.HandshakeTimeout = 90 * time.Second
	}
	if d.WriteTimeout == 0 {
		d.WriteTimeout = 5 * time.Second
	}
	if d.PromptTimeout == 0 {
		d.PromptTimeout = 120 * time.Second
	}

	d.wg.Add(2)
	go d.readLoop(parent)
	go d.writerLoop()

	// initialize — announce protocol version; we accept whatever the
	// agent returns (capability negotiation is out of scope for this
	// shim). Per-call deadline so a slow daemon startup doesn't eat
	// into the next call's budget (see HandshakeTimeout doc above).
	initCtx, cancelInit := context.WithTimeout(parent, d.HandshakeTimeout)
	if _, err := d.call(initCtx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
	}); err != nil {
		cancelInit()
		return fmt.Errorf("acp initialize: %w", err)
	}
	cancelInit()

	// session/new — capture sessionId so we can correlate updates.
	// Fresh per-call deadline; same rationale as initialize.
	nsCtx, cancelNS := context.WithTimeout(parent, d.HandshakeTimeout)
	defer cancelNS()
	sres, err := d.call(nsCtx, "session/new", map[string]any{
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
	// Close done under the lock so callers blocked in writeMsg wake up
	// before Stop returns. Closer() below unblocks the actual Write in
	// writerLoop so wg can complete.
	close(d.done)
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
	// Abandoned permission requests get dropped — the agent will unblock
	// when we close stdin, treating it as a cancellation.
	d.permMu.Lock()
	d.pendingPerm = nil
	d.permMu.Unlock()

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
	// Track session/prompt ids so deliverResponse can recognize an
	// orphaned response (one whose call timed out and is no longer
	// listening) and post a synthetic turn.result. Without this,
	// gemini's late stopReason=cancelled reply gets dropped and mobile
	// stays stuck on the cancel button.
	if method == "session/prompt" {
		d.promptIDs[id] = struct{}{}
	}
	d.pendingMu.Unlock()

	if err := d.writeMsg(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		d.pendingMu.Lock()
		delete(d.pending, id)
		delete(d.promptIDs, id)
		d.pendingMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		// Leave promptIDs[id] set on timeout/cancel — the orphaned
		// session/prompt response is exactly the case the deliverResponse
		// path needs to recognize. Only the pending channel is removed.
		d.pendingMu.Lock()
		delete(d.pending, id)
		d.pendingMu.Unlock()
		return nil, ctx.Err()
	case resp, ok := <-ch:
		// Successful (or rpc-errored) response — call is no longer
		// orphaned, drop the prompt-id tracker.
		d.pendingMu.Lock()
		delete(d.promptIDs, id)
		d.pendingMu.Unlock()
		if !ok {
			return nil, fmt.Errorf("transport closed")
		}
		if resp.err != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.err.Code, resp.err.Message)
		}
		return resp.result, nil
	}
}

// resetTurn clears the per-turn streaming aggregator state so chunks
// from the next session/prompt don't merge into the previous turn's
// bubble. Called from Input("text"/"attach") entry under turnMu so a
// concurrent handleNotification (very unlikely — the steward
// serializes turns — but cheap to defend) sees consistent state.
func (d *ACPDriver) resetTurn() {
	d.turnMu.Lock()
	d.turnTextBuf = nil
	d.turnTextMsgID = ""
	d.turnThoughtBuf = nil
	d.turnThoughtMsgID = ""
	d.turnMu.Unlock()
}

// postTurnResult emits a turn.result agent_event on session/prompt
// success. Two things ride on this:
//   (a) Mobile's _isAgentBusy() returns false on turn.result, which
//       clears the cancel-button overlay so the user can send the next
//       prompt. Without it the composer sticks in cancel-state forever
//       (the streaming text events are agent-produced and tip the
//       busy walker the wrong way).
//   (b) The mobile telemetry strip reads turnCount + by_model + tokens
//       from this event — same canonical hub shape the other drivers
//       (StdioDriver, AppServerDriver, ExecResumeDriver) emit so one
//       renderer code path lights up for every engine.
//
// gemini-cli@0.41 surfaces token usage on the session/prompt result's
// `_meta.quota` block: token_count for whole-turn totals plus a
// model_usage list for per-model breakdown. We flatten that into the
// canonical {by_model: {<model>: {input, output, cache_read}}} shape
// so the mobile aggregator's _ModelTokens.add picks the values up
// without needing to know about gemini's nesting. Engines that don't
// ship tokens (claude-code SDK ACP, etc.) will just get an empty
// turn.result with stop_reason — still load-bearing for (a).
func (d *ACPDriver) postTurnResult(ctx context.Context, raw json.RawMessage) {
	var r struct {
		StopReason string                 `json:"stopReason"`
		Meta       map[string]any         `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		// Even if parsing fails we still want a turn.result so the
		// busy walker sees a terminal kind. Empty payload is fine.
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "turn.result", "agent",
			map[string]any{"status": "success"})
		return
	}
	payload := map[string]any{
		"status": "success",
	}
	if r.StopReason != "" {
		payload["stop_reason"] = r.StopReason
	}
	if quota, ok := r.Meta["quota"].(map[string]any); ok {
		payload["quota"] = quota
		if tc, ok := quota["token_count"].(map[string]any); ok {
			if v, ok := tc["input_tokens"].(float64); ok {
				payload["input_tokens"] = v
			}
			if v, ok := tc["output_tokens"].(float64); ok {
				payload["output_tokens"] = v
			}
			if it, _ := payload["input_tokens"].(float64); it > 0 {
				if ot, _ := payload["output_tokens"].(float64); ot > 0 {
					payload["total_tokens"] = it + ot
				}
			}
		}
		if mu, ok := quota["model_usage"].([]any); ok {
			byModel := map[string]any{}
			for _, item := range mu {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				name, _ := m["model"].(string)
				if name == "" {
					continue
				}
				entry := map[string]any{}
				if tc, ok := m["token_count"].(map[string]any); ok {
					if v, ok := tc["input_tokens"].(float64); ok {
						entry["input"] = v
					}
					if v, ok := tc["output_tokens"].(float64); ok {
						entry["output"] = v
					}
				}
				byModel[name] = entry
			}
			if len(byModel) > 0 {
				payload["by_model"] = byModel
			}
		}
	}
	_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "turn.result", "agent", payload)
}

// logRPCFrame appends a JSONL trace line to d.RPCLog if configured.
// Failure to log is intentionally silent — the trace is a debug aid,
// not a transport guarantee. Marshalling the wrapper itself can't fail
// for our shapes (timestamp string + direction string + already-valid
// JSON bytes), but if the underlying writer errors (full disk, fd
// closed) we drop the line rather than tear down the wire.
func (d *ACPDriver) logRPCFrame(dir string, frame []byte) {
	if d.RPCLog == nil {
		return
	}
	wrap := map[string]any{
		"t":     time.Now().UTC().Format(time.RFC3339Nano),
		"dir":   dir,
		"frame": json.RawMessage(frame),
	}
	b, err := json.Marshal(wrap)
	if err != nil {
		return
	}
	d.rpcLogMu.Lock()
	_, _ = d.RPCLog.Write(append(b, '\n'))
	d.rpcLogMu.Unlock()
}

func (d *ACPDriver) writeMsg(m any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	d.logRPCFrame("out", b)
	req := &acpWriteReq{
		frame: append(b, '\n'),
		done:  make(chan error, 1),
	}
	timer := time.NewTimer(d.WriteTimeout)
	defer timer.Stop()
	// Phase 1: get onto the queue. If writerLoop is stuck on a prior
	// Write the queue fills and this select times out instead of
	// spawning another blocked goroutine.
	select {
	case d.writeQ <- req:
	case <-d.done:
		return fmt.Errorf("acp driver: stopped")
	case <-timer.C:
		return fmt.Errorf("acp driver: stdin queue timeout after %s", d.WriteTimeout)
	}
	// Phase 2: wait for the write result. Stop draining may happen here;
	// done closing unblocks us immediately even if the pipe is wedged.
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d.WriteTimeout)
	select {
	case err := <-req.done:
		return err
	case <-d.done:
		return fmt.Errorf("acp driver: stopped")
	case <-timer.C:
		return fmt.Errorf("acp driver: stdin write timeout after %s", d.WriteTimeout)
	}
}

// writerLoop is the single goroutine that touches d.Stdin. Serialising
// all writes through one goroutine means we never need a writerMu and
// callers never fan out goroutines on a stuck pipe — backpressure is
// encoded in the bounded writeQ.
func (d *ACPDriver) writerLoop() {
	defer d.wg.Done()
	for {
		select {
		case req := <-d.writeQ:
			_, err := d.Stdin.Write(req.frame)
			// Non-blocking: if the caller timed out and left, drop.
			select {
			case req.done <- err:
			default:
			}
		case <-d.done:
			return
		}
	}
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
		d.logRPCFrame("in", line)
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
		// Request from agent (has id + method).
		if msg.ID != nil && msg.Method != "" {
			if msg.Method == "session/request_permission" {
				d.handlePermissionRequest(ctx, *msg.ID, msg.Params)
				continue
			}
			// Everything else: reject with method-not-found. Agents are
			// expected to fall back to internal tooling.
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
	_, isPrompt := d.promptIDs[idNum]
	if isPrompt {
		delete(d.promptIDs, idNum)
	}
	d.pendingMu.Unlock()
	if ok {
		ch <- acpResponse{result: msg.Result, err: msg.Error}
		return
	}
	// Orphaned response — call timed out or was abandoned but the
	// agent eventually replied. For session/prompt this is the path
	// that fires when gemini sends stopReason=cancelled after our
	// PromptTimeout already kicked us out of Input("text"). Post a
	// synthetic turn.result so mobile's busy walker still sees a
	// terminal kind and clears the cancel button. We use a fresh
	// background context — the original Input ctx is gone.
	if isPrompt && msg.Result != nil {
		d.postTurnResult(context.Background(), msg.Result)
	}
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
		isThought := kind == "agent_thought_chunk"
		if isThought {
			ekind = "thought"
		}
		// Per-turn accumulator: append the incremental chunk to the
		// turn-local buffer and emit the running cumulative text with a
		// stable message_id + partial:true. mobile's collapse chain
		// then folds this stream into a single bubble.
		d.turnMu.Lock()
		var cumulative string
		var msgID string
		if isThought {
			d.turnThoughtBuf = append(d.turnThoughtBuf, text...)
			if d.turnThoughtMsgID == "" {
				d.turnThoughtMsgID = newMessageID()
			}
			cumulative = string(d.turnThoughtBuf)
			msgID = d.turnThoughtMsgID
		} else {
			d.turnTextBuf = append(d.turnTextBuf, text...)
			if d.turnTextMsgID == "" {
				d.turnTextMsgID = newMessageID()
			}
			cumulative = string(d.turnTextBuf)
			msgID = d.turnTextMsgID
		}
		d.turnMu.Unlock()
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, ekind, "agent",
			map[string]any{
				"text":       cumulative,
				"message_id": msgID,
				"partial":    true,
			})
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
	case "available_commands_update", "current_mode_update", "current_model_update":
		// Capability-state announcements gemini emits after session/new
		// (slash-command catalog, current approval mode, current model).
		// They're informational, not turn activity — mobile's
		// _isAgentBusy() walks events newest-first and treats anything
		// from producer=agent that isn't on its skip list as
		// "turn in progress", which trips the cancel-button overlay
		// even though the agent is idle waiting for the user's prompt.
		// Tagging these as kind=system + producer=system folds them
		// into the same skip path mobile already has for lifecycle and
		// pings, AND keeps them hidden from the feed unless verbose.
		// Payload is preserved verbatim so a future slash-command
		// picker / mode pill on mobile can lift fields from it without
		// a hub-side schema change.
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "system", u)
	default:
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent", u)
	}
}

// Input implements Inputter for M1 (ACP). Translations:
//   - text:     session/prompt with a text content block against d.sessionID.
//   - cancel:   session/cancel notification against d.sessionID.
//   - approval: resolves a pending session/request_permission call that
//               the agent initiated; request_id must match the one the
//               driver emitted in its approval_request event. decision
//               of "cancel" maps to ACP's "cancelled" outcome, any other
//               value to "selected" with optionId taken from payload
//               (defaults to the decision string itself).
//   - attach:   surfaced as a text prompt with a document_id marker; the
//               agent has no fs capability from this client yet.
//
// Missing sessionID means Start never succeeded; treat as a config error.
func (d *ACPDriver) Input(ctx context.Context, kind string, payload map[string]any) error {
	d.mu.Lock()
	sid := d.sessionID
	d.mu.Unlock()
	if sid == "" {
		return fmt.Errorf("acp driver: no session (handshake incomplete)")
	}
	switch kind {
	case "text":
		body, _ := payload["body"].(string)
		if body == "" {
			return fmt.Errorf("acp driver: text input missing body")
		}
		d.resetTurn()
		promptCtx, cancel := context.WithTimeout(ctx, d.PromptTimeout)
		defer cancel()
		res, err := d.call(promptCtx, "session/prompt", map[string]any{
			"sessionId": sid,
			"prompt":    []map[string]any{{"type": "text", "text": body}},
		})
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("acp session/prompt: no reply within %s — agent likely stuck on auth (set GEMINI_API_KEY for gemini-cli, or check ~/.gemini/oauth_creds.json reachability): %w", d.PromptTimeout, err)
		}
		if err != nil {
			return err
		}
		d.postTurnResult(ctx, res)
		return nil
	case "cancel":
		// session/cancel is a notification (no id) per the ACP spec.
		err := d.writeMsg(map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/cancel",
			"params":  map[string]any{"sessionId": sid},
		})
		// Eagerly post a turn.result so mobile's _isAgentBusy()
		// flips off immediately — the cancel-button → send-button
		// transition can't wait for the agent's stopReason=cancelled
		// response to come back. In practice the response often gets
		// orphaned (the originating session/prompt's call may have
		// already returned from PromptTimeout, and deliverResponse
		// then drops the late arrival). If a real response does
		// arrive afterwards, the driver posts a second turn.result —
		// harmless; mobile aggregator just counts both for the same
		// logical turn.
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "turn.result", "agent",
			map[string]any{
				"status":      "cancelled",
				"stop_reason": "cancelled",
			})
		return err
	case "attach":
		docID, _ := payload["document_id"].(string)
		if docID == "" {
			return fmt.Errorf("acp driver: attach missing document_id")
		}
		d.resetTurn()
		promptCtx, cancel := context.WithTimeout(ctx, d.PromptTimeout)
		defer cancel()
		res, err := d.call(promptCtx, "session/prompt", map[string]any{
			"sessionId": sid,
			"prompt": []map[string]any{{
				"type": "text",
				"text": "[attach] document_id=" + docID,
			}},
		})
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("acp session/prompt (attach): no reply within %s: %w", d.PromptTimeout, err)
		}
		if err != nil {
			return err
		}
		d.postTurnResult(ctx, res)
		return nil
	case "approval":
		reqID, _ := payload["request_id"].(string)
		if reqID == "" {
			return fmt.Errorf("acp driver: approval missing request_id")
		}
		decision, _ := payload["decision"].(string)
		d.permMu.Lock()
		rpcID, ok := d.pendingPerm[reqID]
		if ok {
			delete(d.pendingPerm, reqID)
		}
		d.permMu.Unlock()
		if !ok {
			return fmt.Errorf("acp driver: no pending permission request %q", reqID)
		}
		// ACP permission outcome shape: a "selected" option by id, or
		// "cancelled". Hub accepts approve|allow|deny|cancel; any non-
		// cancel value maps to "selected" with optionId. Prefer an
		// explicit option_id from the caller (matches the option list
		// the agent sent); fall back to the decision string so agents
		// that ignore optionId still get meaningful intent.
		optionID, _ := payload["option_id"].(string)
		if optionID == "" {
			optionID = decision
			// Normalize "approve" to "allow" when synthesizing an option
			// id — ACP-native agents (Claude Code, Zed) expose "allow"
			// in their options list, not "approve".
			if optionID == "approve" {
				optionID = "allow"
			}
		}
		var outcome map[string]any
		switch decision {
		case "cancel":
			outcome = map[string]any{"outcome": "cancelled"}
		default:
			outcome = map[string]any{
				"outcome":  "selected",
				"optionId": optionID,
			}
		}
		return d.writeMsg(map[string]any{
			"jsonrpc": "2.0",
			"id":      rpcID,
			"result":  map[string]any{"outcome": outcome},
		})
	default:
		return fmt.Errorf("acp driver: unsupported input kind %q", kind)
	}
}

// handlePermissionRequest captures an agent's session/request_permission
// RPC, emits a matching approval_request agent_event, and parks the
// JSON-RPC id until a user input.approval resolves it. If the phone
// never responds, the agent's call blocks until the driver is stopped —
// Stop closes stdin which the agent should treat as a cancellation.
func (d *ACPDriver) handlePermissionRequest(ctx context.Context, rpcID json.RawMessage, params json.RawMessage) {
	// request_id visible to the phone: stringified JSON of the rpc id so
	// it round-trips without parsing (the id may be numeric or string).
	reqID := string(rpcID)
	d.permMu.Lock()
	d.pendingPerm[reqID] = append(json.RawMessage(nil), rpcID...)
	d.permMu.Unlock()

	// Decode the params loosely so the event payload carries whatever the
	// agent sent (toolCall summary, option list, sessionId). We don't
	// validate the shape — the phone gets the raw structure and can
	// render it however it wants.
	var p map[string]any
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	payload := map[string]any{
		"request_id": reqID,
		"params":     p,
	}
	_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "approval_request", "agent", payload)
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
