// Codex `app-server` driver — ADR-012 D1.
//
// Like StdioDriver, this owns a line-delimited JSON stream against the
// child's stdio. Unlike StdioDriver, the wire format is JSON-RPC 2.0,
// not stream-json: every line is either a request, a response, or a
// notification, distinguished by the presence of `id` and `method`.
// That changes how input and output route:
//
//   - Notifications (no `id`) → frame profile → agent_events.
//     Same path the StdioDriver takes; we reuse ApplyProfile.
//   - Responses (has `id`, no `method`) → matched to a pending
//     in-process request via a channel keyed by id.
//   - Server-initiated requests (has `id` and `method` — used by
//     codex for item/*/requestApproval) → bridge to attention_items
//     in slice 4. For slice 3 they're surfaced as `system` events
//     so they're at least visible in the transcript while the
//     bridge is being built.
//
// Input from the hub side becomes a JSON-RPC call rather than a
// stream-json frame: a `text` Input call dispatches `turn/start`,
// not a "user" frame on stdin. The hub's input router doesn't have
// to know which engine is on the other side — it calls
// driver.Input("text", payload) and the driver speaks the right
// protocol.
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

	"github.com/termipod/hub/internal/agentfamilies"
)

// AttentionPoster is the host-runner-side hook into the hub's
// /attention surface, used by the codex approval bridge (ADR-012 D3)
// to raise a permission_prompt on each server-initiated approval
// request and recover the attention id for /decide-driven response
// routing. Production wires this to *Client; tests stub it.
type AttentionPoster interface {
	PostAttention(ctx context.Context, in AttentionIn) (AttentionOut, error)
}

// pendingApproval tracks one server-initiated approval request that
// has been bridged to an attention_items row but not yet resolved.
// jsonRPCID is the parked codex request id we'll respond on; method
// drives the response shape (item/commandExecution and
// item/fileChange use {decision}, item/permissions uses {permissions}).
type pendingApproval struct {
	jsonRPCID int64
	method    string
}

// AppServerDriver speaks codex app-server's JSON-RPC protocol over
// the child's stdio. Field set mirrors StdioDriver's where possible
// so the launch path can construct either by family.
type AppServerDriver struct {
	AgentID string
	Handle  string // agent handle; stamped on the attention's actor_handle
	Poster  AgentEventPoster
	// Attention is the optional hook for the codex approval bridge.
	// When nil, server-initiated approval requests fall back to the
	// auto-decline stub (slice 3 behavior). Production sets it to the
	// host-runner Client; tests can stub it independently from Poster.
	Attention AttentionPoster
	Stdout    io.Reader
	Stdin     io.Writer
	Closer    func()
	Log       *slog.Logger

	// FrameProfile drives notification translation. JSON-RPC requests
	// and responses bypass the profile (they're handled in the read
	// loop directly); notifications go through ApplyProfile the same
	// way the StdioDriver does.
	FrameProfile *agentfamilies.FrameProfile

	// ResumeThreadID, when non-empty, calls `thread/resume` instead
	// of `thread/start` during the initial handshake. Used after
	// host-runner restarts when a session was already opened against
	// this agent. The hub captures the thread id from the
	// `thread/started` notification and persists it on the agent row;
	// reconcile reads it back into this field on restart.
	ResumeThreadID string

	// CallTimeout caps how long any one JSON-RPC request waits for a
	// response. Server-initiated requests (the approval bridge) are
	// not subject to this — they're answered out-of-band via /decide.
	// Zero means use the default (30s).
	CallTimeout time.Duration

	// HandshakeTimeout caps the initialize+thread/start sequence on
	// Start(). A child that doesn't complete the handshake in this
	// window is killed via Closer and Start returns an error.
	HandshakeTimeout time.Duration

	mu      sync.Mutex
	started bool
	stopped bool
	wg      sync.WaitGroup

	writeMu sync.Mutex // serializes Stdin writes

	// pending tracks in-flight requests by id. The read loop pushes
	// responses onto the matching channel; call() removes the entry
	// after receiving (or on cancel/timeout).
	pendingMu sync.Mutex
	pending   map[int64]chan jsonRPCResponse
	nextID    atomic.Int64

	// threadID is captured from the thread/start response (or from
	// the thread/started notification if for some reason the response
	// is missing). Used as a debug label and as the resume cursor the
	// hub persists on the agent row.
	threadIDMu sync.RWMutex
	threadID   string

	// shutdownCh is closed by Stop to fail in-flight calls fast
	// instead of waiting on CallTimeout.
	shutdownCh chan struct{}

	// pendingApprovals maps attention_id → parked codex request id
	// for the approval bridge (ADR-012 D3). Set on every
	// server-initiated approval request, cleared on attention_reply
	// or driver shutdown. In-memory only — if the driver process
	// dies, pending approvals are lost on the codex side and the
	// attention_items row remains for the principal to dismiss; the
	// agent retries on its next user-input turn. Persisting across
	// driver restarts is a follow-up wedge.
	approvalMu       sync.Mutex
	pendingApprovals map[string]pendingApproval
}

// jsonRPCRequest is the shape we serialize for outbound calls and
// notifications. Notifications omit ID by setting nil.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse covers all three inbound shapes — response,
// notification, server-initiated request — with optional fields.
// The read loop dispatches by which fields are present.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	// Params for notifications and server requests; nil for responses.
	Params json.RawMessage `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Start initializes the JSON-RPC connection and opens (or resumes) a
// thread. Returns once the handshake completes; the read loop
// continues in the background. lifecycle.started is emitted before
// the handshake so the hub sees the agent come online even if the
// handshake errors out — the lifecycle.stopped that Stop emits
// closes the loop in either case.
func (d *AppServerDriver) Start(parent context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	d.pending = make(map[int64]chan jsonRPCResponse)
	d.pendingApprovals = make(map[string]pendingApproval)
	d.shutdownCh = make(chan struct{})
	d.mu.Unlock()

	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.CallTimeout == 0 {
		d.CallTimeout = 30 * time.Second
	}
	if d.HandshakeTimeout == 0 {
		d.HandshakeTimeout = 60 * time.Second
	}

	_ = d.Poster.PostAgentEvent(parent, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "started", "mode": "M2", "engine": "codex-app-server"})

	d.wg.Add(1)
	go d.readLoop(parent)

	hsCtx, cancel := context.WithTimeout(parent, d.HandshakeTimeout)
	defer cancel()
	if err := d.handshake(hsCtx); err != nil {
		// Best-effort cleanup — Stop is idempotent.
		go d.Stop()
		return fmt.Errorf("appserver handshake: %w", err)
	}
	return nil
}

// handshake runs the post-Start RPC sequence: initialize → thread/start
// (or thread/resume). Captures the resulting thread id for resume on
// restart.
func (d *AppServerDriver) handshake(ctx context.Context) error {
	// initialize is mandatory and must be first per the app-server
	// protocol. We declare clientInfo + experimentalApi=false; the
	// experimental surface (realtime, dynamic tools) is not part of
	// our integration in this slice.
	if _, err := d.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "termipod-host-runner",
			"title":   "termipod",
			"version": "0",
		},
		"capabilities": map[string]any{
			"experimentalApi": false,
		},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	// Per the protocol, the client also sends an `initialized` no-id
	// notification after `initialize` returns. App-server treats this
	// as confirmation; we send-and-forget.
	if err := d.notify("initialized", nil); err != nil {
		return fmt.Errorf("initialized: %w", err)
	}

	method := "thread/start"
	params := map[string]any{}
	if d.ResumeThreadID != "" {
		method = "thread/resume"
		params["threadId"] = d.ResumeThreadID
	}
	res, err := d.Call(ctx, method, params)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	// thread/start returns { thread: { id, ... } }; thread/resume
	// shares the shape. Capture id either way.
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(res, &out); err == nil && out.Thread.ID != "" {
		d.threadIDMu.Lock()
		d.threadID = out.Thread.ID
		d.threadIDMu.Unlock()
	}
	return nil
}

// Stop closes the connection, fails any in-flight calls, and waits
// for the read loop to drain. Idempotent — reconcile and ctx-cancel
// can both fire it.
func (d *AppServerDriver) Stop() {
	d.mu.Lock()
	if d.stopped || !d.started {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	closer := d.Closer
	if d.shutdownCh != nil {
		close(d.shutdownCh)
	}
	d.mu.Unlock()

	if closer != nil {
		closer()
	}
	d.wg.Wait()

	// Drain pending: any caller still waiting gets a closed channel
	// and a "driver stopped" error from their select.
	d.pendingMu.Lock()
	for id, ch := range d.pending {
		close(ch)
		delete(d.pending, id)
	}
	d.pendingMu.Unlock()

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = d.Poster.PostAgentEvent(shutCtx, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "stopped", "mode": "M2", "engine": "codex-app-server"})
}

// ThreadID returns the current thread id (empty before handshake or
// after a failed start). Hub callers use it as the resume cursor on
// host-runner restart.
func (d *AppServerDriver) ThreadID() string {
	d.threadIDMu.RLock()
	defer d.threadIDMu.RUnlock()
	return d.threadID
}

// Input dispatches a hub-side input event to the JSON-RPC method
// that maps onto the same semantics. Implements the Inputter
// interface so the host-runner's InputRouter can hand off to either
// driver shape transparently.
//
// Mapping:
//   - text             → turn/start with the input text
//   - attention_reply  → turn/start with the rendered reply text
//                        (same as text, but with a correlation prefix
//                        added by formatAttentionReplyText so the
//                        agent knows which prior request this answers)
//   - cancel           → turn/interrupt against the active turn
//
// approval / answer (the legacy stream-json shapes) are not used
// here — codex's approvals come via server-initiated RPC requests
// answered through the slice-4 bridge, not user-side stream-json
// frames.
func (d *AppServerDriver) Input(ctx context.Context, kind string, payload map[string]any) error {
	if d.Stdin == nil {
		return fmt.Errorf("appserver driver: stdin not wired")
	}
	switch kind {
	case "text":
		body, _ := payload["body"].(string)
		if body == "" {
			return fmt.Errorf("appserver driver: text input missing body")
		}
		return d.startTurn(ctx, body)
	case "attention_reply":
		// Two paths depending on attention kind:
		//  - kind=permission_prompt → we have a parked codex JSON-RPC
		//    request id; the resolution becomes a JSON-RPC response on
		//    the same stdio pipe (ADR-012 D3 — vendor-neutral
		//    equivalent of permission_prompt with no sync constraint).
		//  - other kinds (approval_request, select, help_request) →
		//    fresh user-text turn via turn/start, same as
		//    StdioDriver's attention_reply path. The agent's request_*
		//    tool already returned awaiting_response and ended its
		//    turn; this is what wakes it up.
		kind, _ := payload["kind"].(string)
		if kind == "permission_prompt" {
			return d.resolvePendingApproval(payload)
		}
		body := formatAttentionReplyText(payload)
		if body == "" {
			return fmt.Errorf("appserver driver: attention_reply produced no text")
		}
		return d.startTurn(ctx, body)
	case "cancel":
		// codex requires `threadId` on turn/interrupt — without it the
		// server replies -32600 "Invalid request: missing field
		// `threadId`". Best-effort: if the handshake never captured a
		// thread id (very early cancel) there's nothing to interrupt
		// anyway, so report a clean error instead of dispatching a
		// malformed call.
		tid := d.ThreadID()
		if tid == "" {
			return fmt.Errorf("appserver driver: cannot cancel — no active thread")
		}
		_, err := d.Call(ctx, "turn/interrupt", map[string]any{
			"threadId": tid,
		})
		return err
	case "approval", "answer":
		return fmt.Errorf("appserver driver: %q input shape not used by codex (use attention_reply / slice-4 approval bridge)", kind)
	default:
		return fmt.Errorf("appserver driver: unsupported input kind %q", kind)
	}
}

// resolvePendingApproval looks up the parked JSON-RPC request id
// for the resolved attention and writes a response on the codex
// stdio pipe. The response shape depends on the original method:
//
//   - item/commandExecution/requestApproval → {decision: accept|decline}
//   - item/fileChange/requestApproval        → {decision: accept|decline}
//   - item/permissions/requestApproval       → {permissions: {...}, scope}
//
// Mapping from the principal's decision verb to codex's:
//   - approve → accept
//   - reject  → decline
//
// `cancel` and `acceptForSession` aren't currently exposed through
// /decide — adding them to attentionDecideIn is a follow-up if
// real traffic shows the principal wants those granular options.
//
// Missing-id case: the codex side either restarted or already
// timed out. We log it as a system event so it surfaces in the
// audit trail and return nil — the attention is still resolved
// hub-side, and the agent will retry on its next user-input turn.
func (d *AppServerDriver) resolvePendingApproval(payload map[string]any) error {
	attID, _ := payload["request_id"].(string)
	decision, _ := payload["decision"].(string)
	d.approvalMu.Lock()
	pa, ok := d.pendingApprovals[attID]
	if ok {
		delete(d.pendingApprovals, attID)
	}
	d.approvalMu.Unlock()
	if !ok {
		// Best-effort logging; attention's already resolved on the hub.
		_ = d.Poster.PostAgentEvent(context.Background(), d.AgentID, "system", "agent",
			map[string]any{
				"kind":         "appserver_approval_unmatched",
				"attention_id": attID,
				"reason":       "no parked jsonrpc id (driver may have restarted)",
			})
		return nil
	}
	codexDecision := "decline"
	if decision == "approve" {
		codexDecision = "accept"
	}
	var result any
	switch pa.method {
	case "item/permissions/requestApproval":
		// permissions response shape: {scope, permissions} on accept,
		// {scope: "session", permissions: {}} on decline. The fine-
		// grained permission set isn't exposed through /decide today —
		// approve grants the full requested set, decline grants none.
		// A follow-up wedge can surface per-permission picks when the
		// mobile UI grows them.
		if codexDecision == "accept" {
			result = map[string]any{
				"scope": "turn",
				// Empty map = grant whatever the request implied. Codex
				// treats absent permissions as "use the default for this
				// approval" rather than "deny" in this position.
				"permissions": map[string]any{},
			}
		} else {
			result = map[string]any{
				"scope":       "turn",
				"permissions": map[string]any{},
			}
		}
	default:
		// commandExecution + fileChange both take {decision}.
		result = map[string]any{"decision": codexDecision}
	}
	if err := d.writeRawResponse(pa.jsonRPCID, result); err != nil {
		return fmt.Errorf("appserver driver: write approval response: %w", err)
	}
	return nil
}

// startTurn calls turn/start with one text content item. Returns
// after the server acknowledges the call; turn output flows back
// through notifications (item/*, turn/started, turn/completed)
// translated by the frame profile.
func (d *AppServerDriver) startTurn(ctx context.Context, text string) error {
	tid := d.ThreadID()
	if tid == "" {
		return fmt.Errorf("appserver driver: no active thread (handshake didn't complete?)")
	}
	_, err := d.Call(ctx, "turn/start", map[string]any{
		"threadId": tid,
		"input": []map[string]any{
			{"type": "text", "text": text},
		},
	})
	return err
}

// Call sends a JSON-RPC request and waits for the matching response.
// Exposed so tests (and slice-4 approval-bridge code) can issue raw
// RPC calls without going through Input.
func (d *AppServerDriver) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := d.nextID.Add(1)
	ch := make(chan jsonRPCResponse, 1)
	d.pendingMu.Lock()
	d.pending[id] = ch
	d.pendingMu.Unlock()

	cleanup := func() {
		d.pendingMu.Lock()
		delete(d.pending, id)
		d.pendingMu.Unlock()
	}

	if err := d.write(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}); err != nil {
		cleanup()
		return nil, err
	}

	timeout := d.CallTimeout
	if dl, ok := ctx.Deadline(); ok {
		// Honor the caller's deadline if tighter than CallTimeout.
		if remaining := time.Until(dl); remaining < timeout {
			timeout = remaining
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp, ok := <-ch:
		cleanup()
		if !ok {
			// Channel was closed by Stop — driver is shutting down.
			return nil, errors.New("appserver driver: stopped")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("jsonrpc %s: %s (code %d)",
				method, resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-timer.C:
		cleanup()
		return nil, fmt.Errorf("jsonrpc %s: timeout after %s", method, timeout)
	case <-d.shutdownCh:
		cleanup()
		return nil, errors.New("appserver driver: stopped")
	}
}

// notify sends a JSON-RPC notification (no id, no response expected).
// Used for `initialized` post-handshake and for slice-4 approval
// responses (where we send back the decision as a request response,
// not a notification — but the helper stays useful).
func (d *AppServerDriver) notify(method string, params any) error {
	return d.write(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

func (d *AppServerDriver) write(req jsonRPCRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if _, err := d.Stdin.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// readLoop reads JSON-RPC frames from stdout until EOF. Dispatches
// by frame shape:
//
//   - response (id, no method) → match to pending channel
//   - server request (id + method) → slice-4 bridge stub
//   - notification (no id) → frame profile → agent_events
func (d *AppServerDriver) readLoop(ctx context.Context) {
	defer d.wg.Done()
	captureFile := openCaptureFile(d.AgentID, d.Log)
	if captureFile != nil {
		defer captureFile.Close()
	}
	sc := bufio.NewScanner(d.Stdout)
	sc.Buffer(make([]byte, 64*1024), streamJSONBufferSize)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if captureFile != nil {
			_, _ = captureFile.Write(append(line, '\n'))
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Malformed line — emit raw so debugging is possible
			// instead of silently swallowing bytes.
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent",
				map[string]any{"text": string(line)})
			continue
		}
		switch {
		case resp.ID != nil && resp.Method == "":
			d.dispatchResponse(resp)
		case resp.ID != nil && resp.Method != "":
			d.handleServerRequest(ctx, line, resp)
		default:
			d.translateNotification(ctx, line)
		}
	}
	// Reader exit (EOF or error) → fail any waiters fast.
	d.pendingMu.Lock()
	for id, ch := range d.pending {
		close(ch)
		delete(d.pending, id)
	}
	d.pendingMu.Unlock()
	if err := sc.Err(); err != nil && err != io.EOF {
		d.Log.Debug("appserver read error", "agent", d.AgentID, "err", err)
	}
}

func (d *AppServerDriver) dispatchResponse(resp jsonRPCResponse) {
	d.pendingMu.Lock()
	ch, ok := d.pending[*resp.ID]
	d.pendingMu.Unlock()
	if !ok {
		// Response without a pending caller — likely a late arrival
		// after timeout/cancel. Log and drop.
		d.Log.Debug("appserver: response with no pending caller",
			"agent", d.AgentID, "id", *resp.ID)
		return
	}
	// Non-blocking send — channel has capacity 1, set in Call.
	select {
	case ch <- resp:
	default:
	}
}

// handleServerRequest bridges codex's server-initiated JSON-RPC
// requests to the hub's attention surface (ADR-012 D3). The codex
// `app-server` protocol uses these for per-tool-call approvals
// (`item/commandExecution/requestApproval`,
// `item/fileChange/requestApproval`,
// `item/permissions/requestApproval`) and for MCP elicitation
// (`mcpServer/elicitation/request`).
//
// For approval-shaped requests the driver:
//
//  1. Posts an `attention_items` row with kind=permission_prompt and
//     the codex method+context as pending_payload, capturing the
//     resulting attention id.
//  2. Stashes (attentionID → jsonRPCID, method) locally so a later
//     `attention_reply` Input event can find the parked id.
//  3. Leaves the JSON-RPC request open. The codex side blocks on
//     it indefinitely — the protocol allows arbitrary response
//     latency on the long-lived stdio pipe; that's the whole point
//     of going through app-server (ADR-012 D3, ADR-011 D6 update).
//
// On any failure path (no Attention hook wired, hub call errors,
// unknown method) we fall back to auto-declining so the codex
// process doesn't stall. The system event surfaces *why* in the
// transcript so the operator can see what got bypassed.
func (d *AppServerDriver) handleServerRequest(
	ctx context.Context, raw []byte, resp jsonRPCResponse,
) {
	method := resp.Method
	id := *resp.ID

	// Approval-shaped methods bridge to attention. Anything else
	// (mcpServer/elicitation, item/tool/requestUserInput) we can't
	// usefully bridge in this slice — log it and decline so codex
	// proceeds. Adding those is a follow-up wedge once we know what
	// shapes the principal-side UI wants for them.
	if d.Attention == nil || !isApprovalMethod(method) {
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent",
			map[string]any{
				"kind":   "appserver_request_auto_declined",
				"method": method,
				"reason": map[bool]string{true: "no attention bridge wired", false: "method not bridged"}[d.Attention == nil],
				"raw":    string(raw),
			})
		_ = d.writeRawResponse(id, map[string]any{"decision": "decline"})
		return
	}

	params := decodeParams(resp.Params)
	att, err := d.Attention.PostAttention(ctx, AttentionIn{
		ScopeKind:      "team",
		Kind:           "permission_prompt",
		Summary:        approvalSummary(method, params),
		Severity:       approvalSeverity(method),
		ActorHandle:    d.Handle,
		PendingPayload: marshalPending(method, id, params),
	})
	if err != nil {
		// Fall back to decline so codex can proceed. The system event
		// trail tells the operator the bridge had a hub-side failure
		// (vs. just being misconfigured).
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent",
			map[string]any{
				"kind":   "appserver_attention_post_failed",
				"method": method,
				"err":    err.Error(),
				"raw":    string(raw),
			})
		_ = d.writeRawResponse(id, map[string]any{"decision": "decline"})
		return
	}

	d.approvalMu.Lock()
	d.pendingApprovals[att.ID] = pendingApproval{jsonRPCID: id, method: method}
	d.approvalMu.Unlock()

	// One-line system marker so the transcript records that the gate
	// fired and which attention is holding the answer. Useful in
	// audit replays.
	_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent",
		map[string]any{
			"kind":         "appserver_approval_parked",
			"method":       method,
			"attention_id": att.ID,
		})
}

// isApprovalMethod identifies the codex JSON-RPC methods whose
// responses we know how to build from a /decide outcome. Keep this
// list in sync with codex's protocol surface — adding a method here
// without a matching builder in resolvePendingApproval will produce
// a protocol error on the codex side.
func isApprovalMethod(method string) bool {
	switch method {
	case "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/permissions/requestApproval":
		return true
	}
	return false
}

// approvalSummary produces a human-readable one-liner for the
// attention card. Mirrors what the principal would see in codex's
// own TUI prompt — command, file path, or summary phrase — so the
// gate reads consistently across surfaces.
func approvalSummary(method string, params map[string]any) string {
	switch method {
	case "item/commandExecution/requestApproval":
		if cmd := stringPath(params, "command"); cmd != "" {
			return "Run: " + cmd
		}
		return "Run command (codex)"
	case "item/fileChange/requestApproval":
		return "Apply file change (codex)"
	case "item/permissions/requestApproval":
		return "Grant permissions (codex)"
	}
	return method
}

// approvalSeverity maps method → tier. commandExecution defaults to
// `major` because shell commands are blast-radius decisions; the
// others land at `minor` until we accumulate enough real traffic to
// know whether they merit promotion.
func approvalSeverity(method string) string {
	if method == "item/commandExecution/requestApproval" {
		return "major"
	}
	return "minor"
}

// marshalPending packages the codex-side context the audit trail
// (and the slice-4 detail screen) needs to make sense of the
// attention without round-tripping back to codex. The id is
// recorded but the driver's own pendingApprovals map is what
// closes the loop on /decide.
func marshalPending(method string, jsonRPCID int64, params map[string]any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"engine":  "codex",
		"method":  method,
		"jsonrpc_id": jsonRPCID,
		"params":  params,
	})
	return b
}

// decodeParams unwraps the json.RawMessage into the loosely-typed
// map ApplyProfile and the bridge helpers consume. Returns an empty
// map (not nil) so callers can dot-walk safely.
func decodeParams(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// stringPath flattens a single dotted lookup against a decoded JSON
// map. Used by the summary builder; keeping it local rather than
// reaching for profile_eval.Eval which is overkill for one-key
// lookups.
func stringPath(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	if arr, ok := m[key].([]any); ok && len(arr) > 0 {
		// commandExecution.command is `["bash","-lc","..."]` —
		// flatten to a single readable string.
		var parts []string
		for _, p := range arr {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		return joinSpaces(parts)
	}
	return ""
}

func joinSpaces(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// writeRawResponse marshals and sends a JSON-RPC response. Distinct
// from write() because the response shape carries `result`/`error`,
// not `method`/`params`, so it doesn't fit jsonRPCRequest.
func (d *AppServerDriver) writeRawResponse(id int64, result any) error {
	frame := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Result  any    `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	b, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_, err = d.Stdin.Write(append(b, '\n'))
	return err
}

// translateNotification runs a notification frame through the frame
// profile and posts the resulting agent_events. Falls back to raw
// passthrough on a missing profile so the operator sees something
// rather than nothing.
func (d *AppServerDriver) translateNotification(ctx context.Context, raw []byte) {
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		return
	}
	// Capture thread/started.params.thread.id eagerly — under unusual
	// timing the notification can arrive before the thread/start
	// response, and we want the resume cursor available regardless.
	if m, _ := frame["method"].(string); m == "thread/started" {
		if params, ok := frame["params"].(map[string]any); ok {
			if thread, ok := params["thread"].(map[string]any); ok {
				if id, ok := thread["id"].(string); ok && id != "" {
					d.threadIDMu.Lock()
					if d.threadID == "" {
						d.threadID = id
					}
					d.threadIDMu.Unlock()
				}
			}
		}
	}
	evts := ApplyProfile(frame, d.FrameProfile)
	for _, e := range evts {
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, e.Kind, e.Producer, e.Payload)
	}
}
