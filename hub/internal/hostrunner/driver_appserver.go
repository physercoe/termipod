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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
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

	// turnID is captured from `turn/started` notifications and cleared
	// on `turn/completed`. Required (alongside threadId) by codex's
	// `turn/interrupt`; without it the server replies -32600
	// "missing field `turnId`" and the cancel button is a no-op.
	turnIDMu sync.RWMutex
	turnID   string

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

	// Streaming buffer for item/agentMessage/delta. Codex emits one
	// notification per ~1-5 chars while a turn is generating; posting
	// each as its own agent_event creates ~200 transcript rows for a
	// typical reply. Instead, the driver buffers per item id and
	// throttle-flushes a single `kind=text, partial: true` event
	// every StreamFlushInterval. The mobile renderer collapses the
	// chain by message_id so the user sees one row that grows in
	// place — chatbot-style streaming UX without DB-row spam.
	streamMu      sync.Mutex
	streamBuffers map[string]*streamBuffer
	// StreamFlushInterval throttles the flush cadence. Zero falls
	// back to the default (200 ms — fast enough to feel live without
	// generating a row per word). A negative value disables streaming
	// entirely (deltas dropped silently); useful for tests or for
	// hosts on slow links where DB write rate matters more than
	// streaming smoothness.
	StreamFlushInterval time.Duration
	// streamCtx is captured from Start's parent context so timer-
	// driven flushes have a long-lived context to post under. Cleared
	// in Stop.
	streamCtx context.Context
}

// streamBuffer holds in-flight delta accumulation for one streaming
// item. timer is non-nil when a flush is scheduled; flushStream
// resets it to nil after firing so the next delta can schedule again
// (throttle, not debounce — debounce would starve a continuous stream).
type streamBuffer struct {
	text  string
	timer *time.Timer
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
	d.streamBuffers = make(map[string]*streamBuffer)
	d.streamCtx = parent
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
	if d.StreamFlushInterval == 0 {
		d.StreamFlushInterval = 200 * time.Millisecond
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

	// Cancel any in-flight stream-flush timers so a late callback
	// doesn't post a partial event after the lifecycle.stopped one.
	d.streamMu.Lock()
	for id, buf := range d.streamBuffers {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		delete(d.streamBuffers, id)
	}
	d.streamMu.Unlock()

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

// TurnID returns the active turn id (empty when no turn is in
// progress). Captured from `turn/started`, cleared on
// `turn/completed`. Required by `turn/interrupt`.
func (d *AppServerDriver) TurnID() string {
	d.turnIDMu.RLock()
	defer d.turnIDMu.RUnlock()
	return d.turnID
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
		images := extractImageInputs(payload)
		if body == "" && len(images) == 0 {
			return fmt.Errorf("appserver driver: text input missing body")
		}
		return d.startTurn(ctx, body, images)
	case "attention_reply":
		// Three paths depending on attention kind:
		//  - kind=permission_prompt → we have a parked codex JSON-RPC
		//    request id; the resolution becomes a JSON-RPC response on
		//    the same stdio pipe (ADR-012 D3 — vendor-neutral
		//    equivalent of permission_prompt with no sync constraint).
		//  - kind=elicit → also a parked JSON-RPC id, but the response
		//    shape is `{action, content?}` instead of `{decision}`.
		//    The principal's free-text reply becomes the elicitation
		//    content the MCP server originally asked for.
		//  - other kinds (approval_request, select, help_request) →
		//    fresh user-text turn via turn/start, same as
		//    StdioDriver's attention_reply path. The agent's request_*
		//    tool already returned awaiting_response and ended its
		//    turn; this is what wakes it up.
		kind, _ := payload["kind"].(string)
		if kind == "permission_prompt" {
			return d.resolvePendingApproval(payload)
		}
		if kind == "elicit" {
			return d.resolvePendingElicitation(payload)
		}
		body := formatAttentionReplyText(payload)
		if body == "" {
			return fmt.Errorf("appserver driver: attention_reply produced no text")
		}
		return d.startTurn(ctx, body, nil)
	case "cancel":
		// codex requires both `threadId` and `turnId` on turn/interrupt.
		// Without either the server replies -32600 "missing field …".
		// Best-effort: if the handshake never captured a thread id
		// (very early cancel) there's nothing to interrupt anyway, so
		// report a clean error instead of dispatching a malformed call.
		tid := d.ThreadID()
		if tid == "" {
			return fmt.Errorf("appserver driver: cannot cancel — no active thread")
		}
		// Unblock any parked elicit/approval first. turn/interrupt on
		// the codex side aborts in-flight tool calls, but the parked
		// JSON-RPC ids stay open until we write a response on the
		// stdio pipe. Drain them with cancel-shaped responses so codex
		// can complete the interrupt cleanly and the next user-text
		// turn isn't stuck behind a half-closed gate.
		d.cancelPendingApprovals(ctx)
		params := map[string]any{"threadId": tid}
		if turnID := d.TurnID(); turnID != "" {
			params["turnId"] = turnID
		}
		_, err := d.Call(ctx, "turn/interrupt", params)
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
	case "mcpServer/elicitation/request":
		// MCP-tool-call approvals routed via permission_prompt arrive
		// here. The wire shape is the elicitation response — codex's
		// rmcp deserializer rejects {decision} on this method.
		//
		// content: {} — the request's `requestedSchema` is
		// `{type: object, properties: {}}` (empty-properties object).
		// A missing `content` field can leave codex's rmcp wedged on
		// the schema match; sending an empty object both satisfies the
		// schema and disambiguates the deserialization. Decline carries
		// no content (codex is supposed to discard it anyway).
		//
		// _meta.persist: "session" — codex's MCP gate offers
		// once / session / always persistence (see `_meta.persist` on
		// the request: `["session", "always"]`). Without a persist
		// hint, codex defaults to once, so every subsequent
		// request_select / request_approval / request_help in the
		// same thread re-fires the gate. "session" is the strongest
		// hint we can take on the principal's behalf without a UI
		// affordance for "always" — the principal effectively says
		// "yes, run termipod tools for the rest of this thread."
		// A "Trust always" affordance on the attention card can promote
		// this to "always" later; per-server persist UX is post-MVP.
		if codexDecision == "accept" {
			result = map[string]any{
				"action":  "accept",
				"content": map[string]any{},
				"_meta":   map[string]any{"persist": "session"},
			}
		} else {
			result = map[string]any{"action": "decline"}
		}
	default:
		// commandExecution + fileChange both take {decision}.
		result = map[string]any{"decision": codexDecision}
	}
	if err := d.writeRawResponse(pa.jsonRPCID, result); err != nil {
		return fmt.Errorf("appserver driver: write approval response: %w", err)
	}
	// One-line audit marker so the transcript records that the parked
	// gate was answered. Surfaces on the timeline alongside the
	// `appserver_request_parked` event from the bridge — useful when
	// debugging a "engine still busy after approve" report.
	_ = d.Poster.PostAgentEvent(context.Background(), d.AgentID, "system", "agent",
		map[string]any{
			"kind":         "appserver_request_resolved",
			"method":       pa.method,
			"attention_id": attID,
			"decision":     decision,
		})
	return nil
}

// resolvePendingElicitation closes a parked
// `mcpServer/elicitation/request` by writing the rmcp-shaped response
// rmcp's `McpServerElicitationRequestResponse` requires. Shape:
//
//	{action: "accept" | "decline" | "cancel", content?: object}
//
// Mapping:
//   - decision=approve → action=accept; content is the principal's
//     reply parsed as JSON if possible, else wrapped as {value: <body>}
//     so the MCP server gets a single-string field by default.
//     Schema-driven wrapping is a follow-up wedge once the mobile UI
//     can render typed inputs from `requestedSchema`.
//   - decision=reject  → action=decline; no content.
//   - missing parked id → no-op + system event (driver may have
//     restarted between bridge and reply).
func (d *AppServerDriver) resolvePendingElicitation(payload map[string]any) error {
	attID, _ := payload["request_id"].(string)
	decision, _ := payload["decision"].(string)
	body, _ := payload["body"].(string)
	d.approvalMu.Lock()
	pa, ok := d.pendingApprovals[attID]
	if ok {
		delete(d.pendingApprovals, attID)
	}
	d.approvalMu.Unlock()
	if !ok {
		_ = d.Poster.PostAgentEvent(context.Background(), d.AgentID, "system", "agent",
			map[string]any{
				"kind":         "appserver_elicitation_unmatched",
				"attention_id": attID,
				"reason":       "no parked jsonrpc id (driver may have restarted)",
			})
		return nil
	}
	var result map[string]any
	if decision == "approve" {
		result = map[string]any{
			"action":  "accept",
			"content": elicitationContentFromBody(body),
		}
	} else {
		result = map[string]any{"action": "decline"}
	}
	if err := d.writeRawResponse(pa.jsonRPCID, result); err != nil {
		return fmt.Errorf("appserver driver: write elicitation response: %w", err)
	}
	return nil
}

// cancelPendingApprovals drains every parked codex request with the
// correct cancel-shaped response, freeing the JSON-RPC id on the
// codex side. Called from the cancel path so turn/interrupt has no
// half-closed gates to fight. The hub-side attention rows stay open;
// resolving them is a separate /decide call. (Auto-resolving them
// here would create a confusing audit trail — the principal didn't
// approve OR decline, they bailed on the whole turn.)
func (d *AppServerDriver) cancelPendingApprovals(ctx context.Context) {
	d.approvalMu.Lock()
	parked := d.pendingApprovals
	d.pendingApprovals = make(map[string]pendingApproval)
	d.approvalMu.Unlock()
	if len(parked) == 0 {
		return
	}
	for attID, pa := range parked {
		var result map[string]any
		switch pa.method {
		case "mcpServer/elicitation/request":
			// MCP elicitation cancel uses a distinct action verb; rmcp
			// rejects an empty/decline shape on this method otherwise.
			result = map[string]any{"action": "cancel"}
		default:
			// Approval-shaped methods accept the decline shape.
			result = autoDeclineResultFor(pa.method)
		}
		if err := d.writeRawResponse(pa.jsonRPCID, result); err != nil {
			d.Log.Warn("appserver: cancel parked request failed",
				"attention_id", attID,
				"method", pa.method,
				"err", err)
			continue
		}
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent",
			map[string]any{
				"kind":         "appserver_request_cancelled",
				"method":       pa.method,
				"attention_id": attID,
			})
	}
}

// elicitationContentFromBody best-effort-parses the principal's
// reply into the `content` map an MCP server expects back. The
// elicitation's requestedSchema describes what the server wants,
// but the mobile UI doesn't render schema-typed inputs yet — the
// principal types free text. Two fallback shapes:
//
//   - If body parses as a JSON object, use it verbatim. Power users
//     can hand-type structured replies.
//   - Otherwise wrap as {value: <body>}. Single-string-field
//     elicitations (the common case) get a sensible default;
//     servers that asked for richer shapes will fail validation
//     and the agent surfaces the error — which at least makes the
//     mismatch visible instead of silently degrading.
func elicitationContentFromBody(body string) map[string]any {
	body = strings.TrimSpace(body)
	if body == "" {
		return map[string]any{}
	}
	if body[0] == '{' {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(body), &parsed); err == nil && parsed != nil {
			return parsed
		}
	}
	return map[string]any{"value": body}
}

// startTurn calls turn/start with the user's content blocks. Returns
// after the server acknowledges the call; turn output flows back
// through notifications (item/*, turn/started, turn/completed)
// translated by the frame profile.
//
// ADR-021 W4.3 — image content blocks lower to OpenAI responses-API
// shape `{type:"input_image", image_url:"data:<mime>;base64,<b64>"}`
// and lead the input array; the text block (if any) comes last so
// the model sees the imagery before the question. Image-only inputs
// (no body text) are accepted at this layer.
func (d *AppServerDriver) startTurn(ctx context.Context, text string, images []imageInput) error {
	tid := d.ThreadID()
	if tid == "" {
		return fmt.Errorf("appserver driver: no active thread (handshake didn't complete?)")
	}
	input := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		input = append(input, map[string]any{
			"type":      "input_image",
			"image_url": "data:" + img.mime + ";base64," + img.data,
		})
	}
	if text != "" {
		input = append(input, map[string]any{"type": "text", "text": text})
	}
	_, err := d.Call(ctx, "turn/start", map[string]any{
		"threadId": tid,
		"input":    input,
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
		// codex's stderr is merged into stdout (RealProcSpawner sets
		// cmd.Stderr = cmd.Stdout) so log lines like
		// `2026-05-06T12:58:17.190362Z ERROR codex_app_server::...`
		// land in the same scanner. Drop anything that doesn't look
		// like a JSON-RPC frame — keep the capture-file copy above
		// for forensics, but don't post it as an agent_event where
		// it shows up as garbled "random characters" in the
		// transcript. A real malformed JSON-RPC frame (starts with
		// `{` but won't unmarshal) still posts as kind=raw below so
		// protocol issues stay visible.
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 || trimmed[0] != '{' {
			d.Log.Debug("appserver: dropping non-JSON line",
				"agent", d.AgentID, "text", string(line))
			continue
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Malformed JSON — emit raw so debugging is possible
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

	// Three classes of server-initiated request bridge to attention:
	//   - Approval-shaped (item/*/requestApproval) → kind=permission_prompt,
	//     responds with {decision} once the principal decides.
	//   - Elicitation (mcpServer/elicitation/request) → kind=elicit,
	//     responds with {action, content?} carrying the principal's
	//     filled-in form.
	// Anything else we can't usefully bridge in this slice — log it
	// and auto-decline (with the right per-method shape) so codex
	// doesn't stall. Adding more methods is a question of building
	// the response-shape factory, not the bridge plumbing.
	bridged := d.Attention != nil && (isApprovalMethod(method) || isElicitationMethod(method))
	if !bridged {
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent",
			map[string]any{
				"kind":   "appserver_request_auto_declined",
				"method": method,
				"reason": map[bool]string{true: "no attention bridge wired", false: "method not bridged"}[d.Attention == nil],
				"raw":    string(raw),
			})
		_ = d.writeRawResponse(id, autoDeclineResultFor(method))
		return
	}

	params := decodeParams(resp.Params)
	attentionKind := "permission_prompt"
	if isElicitationMethod(method) {
		// codex re-uses MCP elicitation as the wire-level shape for two
		// distinct UX flows. Distinguish by the codex-private meta
		// hint (`_meta.codex_approval_kind`):
		//   - "mcp_tool_call" → permission gate (yes/no buttons),
		//     response is `{action: accept|decline}` with no content.
		//   - anything else → real form fill, response carries the
		//     principal's typed reply as `content`.
		// Falling back to "elicit" for a tool-call gate routes the
		// principal into a free-text input that codex then can't
		// deserialize against the empty schema, leaving the turn
		// stuck in waitingOnApproval.
		if isToolCallApprovalElicitation(params) {
			attentionKind = "permission_prompt"
		} else {
			attentionKind = "elicit"
		}
	}
	att, err := d.Attention.PostAttention(ctx, AttentionIn{
		ScopeKind:      "team",
		Kind:           attentionKind,
		Summary:        attentionSummary(method, params),
		Severity:       attentionSeverity(method),
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
		_ = d.writeRawResponse(id, autoDeclineResultFor(method))
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
			"kind":         "appserver_request_parked",
			"method":       method,
			"attention":    attentionKind,
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

// isElicitationMethod flags the MCP elicitation request — codex
// forwards an MCP server's elicitation/create call to its client
// (host-runner) under this method name. Distinct from approvals
// because the response shape (`action`+`content`) and the principal-
// side UX (free-text reply rather than yes/no) both differ.
func isElicitationMethod(method string) bool {
	return method == "mcpServer/elicitation/request"
}

// isToolCallApprovalElicitation returns true when an
// `mcpServer/elicitation/request` is actually codex asking the
// principal to permit an MCP tool call (vs. forwarding a real form
// fill from the MCP server). codex tags this case with
// `_meta.codex_approval_kind: "mcp_tool_call"` and ships an empty
// `requestedSchema.properties` because no input is being collected.
//
// Detection rules (any one suffices):
//   - `_meta.codex_approval_kind == "mcp_tool_call"` — codex's
//     explicit signal.
//   - `requestedSchema` missing or has no properties — protocol-level
//     fingerprint that no input is being collected; an "elicit" UX
//     would just dump the principal into a useless free-text box.
//
// A real form-fill elicitation always carries a non-empty
// `requestedSchema.properties` describing the field shapes.
func isToolCallApprovalElicitation(params map[string]any) bool {
	if meta, ok := params["_meta"].(map[string]any); ok {
		if k, _ := meta["codex_approval_kind"].(string); k == "mcp_tool_call" {
			return true
		}
	}
	rs, hasSchema := params["requestedSchema"].(map[string]any)
	if !hasSchema {
		return true
	}
	props, hasProps := rs["properties"].(map[string]any)
	if !hasProps || len(props) == 0 {
		return true
	}
	return false
}

// autoDeclineResultFor builds the per-method decline-shape for any
// server-initiated request the driver can't bridge. Each codex
// method has its own deserializer in rmcp — sending the wrong shape
// trips a `missing field` error on the codex side that propagates
// back to the agent's MCP tool call as a flat rejection.
//
//   - mcpServer/elicitation/request → {action: decline}
//     (rmcp's McpServerElicitationRequestResponse requires `action`.)
//   - approval-shaped methods       → {decision: decline}
//     (matches the slice-3 shape resolvePendingApproval sends.)
//   - anything else                 → empty object; codex will treat
//     it as best-effort and the system event in the transcript
//     records what got bypassed.
func autoDeclineResultFor(method string) map[string]any {
	switch method {
	case "mcpServer/elicitation/request":
		return map[string]any{"action": "decline"}
	case "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/permissions/requestApproval":
		return map[string]any{"decision": "decline"}
	}
	return map[string]any{}
}

// attentionSummary produces a human-readable one-liner for the
// attention card. Mirrors what the principal would see in codex's
// own TUI prompt — command, file path, or summary phrase — so the
// gate reads consistently across surfaces.
func attentionSummary(method string, params map[string]any) string {
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
	case "mcpServer/elicitation/request":
		// Elicitation params: rmcp wraps the MCP server's request as
		// `{server, params: {message, requestedSchema}}`. Surface the
		// MCP-server message verbatim — that's the human-readable ask
		// the server author wrote.
		if msg := elicitationMessage(params); msg != "" {
			return msg
		}
		if srv := stringPath(params, "server"); srv != "" {
			return "Input requested by " + srv
		}
		return "Input requested (codex)"
	}
	return method
}

// attentionSeverity maps method → tier. commandExecution defaults to
// `major` because shell commands are blast-radius decisions; the
// others land at `minor` until we accumulate enough real traffic to
// know whether they merit promotion. Elicitation is `minor` — it's
// a form fill, not a permission grant.
func attentionSeverity(method string) string {
	if method == "item/commandExecution/requestApproval" {
		return "major"
	}
	return "minor"
}

// elicitationMessage extracts the MCP-server-supplied prompt text
// from a `mcpServer/elicitation/request` params map. The path varies
// across rmcp builds — some put `message` at the top level of params,
// some nest it under `params.params.message` (the inner `params`
// being the MCP elicitation/create parameters). Try both before
// falling back to the empty string.
func elicitationMessage(params map[string]any) string {
	if s := stringPath(params, "message"); s != "" {
		return s
	}
	if inner, ok := params["params"].(map[string]any); ok {
		if s := stringPath(inner, "message"); s != "" {
			return s
		}
	}
	return ""
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

// isDeltaNotification flags codex notifications that stream partial
// output for an in-progress item. Each delta carries a few characters
// of an agentMessage / reasoning / agentReasoningRawContent block; in
// aggregate they reproduce what arrives complete on item/completed.
// Posting them as agent_events would multiply transcript-row volume
// 50–200× per turn for content the renderer doesn't show outside
// debug mode anyway. The driver collapses them upstream; a future
// streaming-UI wedge can opt back in by routing deltas through a
// separate channel.
func isDeltaNotification(method string) bool {
	if method == "" {
		return false
	}
	if strings.HasSuffix(method, "/delta") {
		return true
	}
	// camelCase variants observed in real codex traffic:
	//   item/reasoning/textDelta
	//   item/agentReasoningRawContentDelta
	//   item/agentReasoningRawContent/Delta
	if strings.HasSuffix(method, "Delta") {
		return true
	}
	return false
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
	method, _ := frame["method"].(string)
	// Streaming deltas don't traverse the profile — they're routed
	// through the per-item buffer + throttle so the mobile renderer
	// sees a single row that grows in place. Other delta methods
	// (reasoning textDelta, etc.) are dropped: their content is
	// internal-monologue debug data the agent_event vocabulary
	// doesn't surface.
	if isDeltaNotification(method) {
		if method == "item/agentMessage/delta" {
			d.handleAgentMessageDelta(frame)
		}
		return
	}
	// item/completed for an agentMessage finalizes any in-flight
	// stream chain — cancel the timer + free the buffer so the
	// final text event the profile is about to post becomes the
	// authoritative row. Mobile-side coalescing replaces the last
	// partial with this final.
	if method == "item/completed" {
		if params, ok := frame["params"].(map[string]any); ok {
			if item, ok := params["item"].(map[string]any); ok {
				if t, _ := item["type"].(string); t == "agentMessage" {
					if itemID, _ := item["id"].(string); itemID != "" {
						d.finalizeStream(itemID)
					}
				}
			}
		}
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
	// Track the active turn id so cancel can populate
	// turn/interrupt.turnId. turn/started carries it on
	// `params.turn.id`; turn/completed clears it. Any in-between
	// notification (item/started, etc.) also carries `params.turnId`
	// — we use those as a fallback in case turn/started was missed.
	if m, _ := frame["method"].(string); m != "" {
		if params, ok := frame["params"].(map[string]any); ok {
			switch m {
			case "turn/started":
				if turn, ok := params["turn"].(map[string]any); ok {
					if id, ok := turn["id"].(string); ok && id != "" {
						d.turnIDMu.Lock()
						d.turnID = id
						d.turnIDMu.Unlock()
					}
				}
			case "turn/completed", "turn/failed":
				d.turnIDMu.Lock()
				d.turnID = ""
				d.turnIDMu.Unlock()
			default:
				if id, ok := params["turnId"].(string); ok && id != "" {
					d.turnIDMu.Lock()
					if d.turnID == "" {
						d.turnID = id
					}
					d.turnIDMu.Unlock()
				}
			}
		}
	}
	evts := ApplyProfile(frame, d.FrameProfile)
	for _, e := range evts {
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, e.Kind, e.Producer, e.Payload)
	}
}

// handleAgentMessageDelta accumulates one streaming chunk and ensures
// a flush is scheduled. Throttle (not debounce) — the timer fires
// after StreamFlushInterval regardless of arrival cadence; once it
// fires, the next delta starts a fresh window. Steady-state for a
// 1000-token reply: ~5 flushes/sec, ~5 DB rows total instead of
// hundreds, with the user seeing the message grow live on each.
func (d *AppServerDriver) handleAgentMessageDelta(frame map[string]any) {
	if d.StreamFlushInterval < 0 {
		return // streaming explicitly disabled
	}
	params, _ := frame["params"].(map[string]any)
	if params == nil {
		return
	}
	itemID, _ := params["itemId"].(string)
	delta, _ := params["delta"].(string)
	if itemID == "" || delta == "" {
		return
	}
	d.streamMu.Lock()
	defer d.streamMu.Unlock()
	if d.streamBuffers == nil {
		// Driver stopped — buffer was nilled in Stop(). Drop silently.
		return
	}
	buf, ok := d.streamBuffers[itemID]
	if !ok {
		buf = &streamBuffer{}
		d.streamBuffers[itemID] = buf
	}
	buf.text += delta
	if buf.timer == nil {
		// Capture itemID by value so the closure flushes the right
		// buffer even after the map mutates.
		id := itemID
		buf.timer = time.AfterFunc(d.StreamFlushInterval, func() {
			d.flushStream(id)
		})
	}
}

// flushStream posts the accumulated buffer as a single partial text
// event and clears the timer slot so the next delta can schedule a
// fresh flush. Called from time.AfterFunc on a separate goroutine —
// holds streamMu for the read+reset, then drops the lock before the
// PostAgentEvent call (which can block on network).
func (d *AppServerDriver) flushStream(itemID string) {
	d.streamMu.Lock()
	if d.streamBuffers == nil {
		d.streamMu.Unlock()
		return
	}
	buf, ok := d.streamBuffers[itemID]
	if !ok {
		d.streamMu.Unlock()
		return
	}
	text := buf.text
	buf.timer = nil // free the slot for the next delta to re-schedule
	ctx := d.streamCtx
	d.streamMu.Unlock()
	if text == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "text", "agent", map[string]any{
		"text":       text,
		"message_id": itemID,
		"partial":    true,
	})
}

// finalizeStream cancels any pending flush and removes the buffer for
// the given item id. Called from translateNotification when
// item/completed for an agentMessage arrives — the profile-emitted
// final text event will follow this call and supersede whatever the
// mobile collapse currently shows for the message_id.
func (d *AppServerDriver) finalizeStream(itemID string) {
	d.streamMu.Lock()
	defer d.streamMu.Unlock()
	if d.streamBuffers == nil {
		return
	}
	buf, ok := d.streamBuffers[itemID]
	if !ok {
		return
	}
	if buf.timer != nil {
		buf.timer.Stop()
	}
	delete(d.streamBuffers, itemID)
}
