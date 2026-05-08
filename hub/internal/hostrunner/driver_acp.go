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

	// ResumeSessionID is the prior engine-side cursor captured from a
	// previous spawn's session.init event (ADR-014 column reused per
	// ADR-021 W1.2). When set AND the agent advertises
	// agentCapabilities.loadSession in its initialize response, Start
	// calls `session/load` instead of `session/new` so the daemon
	// reattaches to the prior conversation. On load failure (cursor
	// stale on the agent's disk, or the agent doesn't actually
	// implement loadSession despite advertising it), Start falls back
	// to `session/new` so the user still gets a session — fresh, but
	// usable. Empty → always cold-start with `session/new`.
	ResumeSessionID string

	// AuthMethod is the resolved ACP `authenticate` methodId for this
	// spawn (ADR-021 W1.4). launch_m1 sets this from
	// SpawnSpec.AuthMethod (template override) falling back to the
	// family-level default (`agent_families.yaml`'s
	// default_auth_method). Empty is "no preference" — the driver
	// picks the first non-interactive method in `authMethods` from
	// the initialize response. Methods we never picked still satisfy
	// the agent's contract: the spec lets us NOT call authenticate
	// when authMethods is empty (zero-cost daemon, e.g. a daemon that
	// already has cached creds and treats authenticate as a no-op).
	AuthMethod string

	// AuthTimeout caps the `authenticate` RPC. 0 → 30s. Interactive
	// methods (oauth-personal without cached creds) can hang opening a
	// browser the daemon's environment can't actually reach. The
	// timeout converts a silent hang into a typed `attention_request`
	// event the principal can act on (run `gemini auth` on the host,
	// or pick a different methodId via the steward template).
	AuthTimeout time.Duration

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

	// replayMu / replayActive marks the window during which a
	// session/load call is in flight. While active, handleNotification
	// tags emitted agent_events with `replay: true` in their payload so
	// downstream caches (mobile transcript, hub-side filter) can
	// distinguish historical replay frames from live activity. ACP's
	// session/load contract: the agent emits a flurry of session/update
	// notifications carrying historical turns *before* sending the
	// session/load response. Once the response arrives, replayActive
	// flips back to false and subsequent updates are live again.
	replayMu     sync.Mutex
	replayActive bool

	// availableModes / availableModels are the id sets the agent reported
	// in its session/new (or session/load) response (ADR-021 W2.2). Set
	// once at handshake; consulted by Input("set_mode"/"set_model") to
	// reject ids the agent never advertised before we waste a round trip.
	// Empty = the agent didn't advertise modes/models in this session,
	// in which case set_mode/set_model gets a typed error explaining the
	// engine doesn't support runtime switching for this session.
	modesMu         sync.Mutex
	availableModes  map[string]struct{}
	availableModels map[string]struct{}

	// promptCapImageDecl tracks whether the agent's initialize response
	// declared promptCapabilities.image (ADR-021 W4.4). Tri-state:
	//   nil   — initialize hasn't completed, or the field was absent (we
	//           treat absent as "permitted" because some agents omit the
	//           map entirely while still accepting image blocks).
	//   *true / *false — explicit declaration. We only strip+warn when
	//                    the cached value is *false; absent or true →
	//                    forward as-is.
	promptCapMu        sync.Mutex
	promptCapImageDecl *bool

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
	if d.AuthTimeout == 0 {
		d.AuthTimeout = 30 * time.Second
	}

	d.wg.Add(2)
	go d.readLoop(parent)
	go d.writerLoop()

	// initialize — announce protocol version; we accept whatever the
	// agent returns (capability negotiation is out of scope for this
	// shim). Per-call deadline so a slow daemon startup doesn't eat
	// into the next call's budget (see HandshakeTimeout doc above).
	initCtx, cancelInit := context.WithTimeout(parent, d.HandshakeTimeout)
	initRes, err := d.call(initCtx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
	})
	cancelInit()
	if err != nil {
		return fmt.Errorf("acp initialize: %w", err)
	}
	// Cache loadSession capability so session/load is gated on what the
	// agent advertised, not on hopeful guessing. ADR-021 W1.2.
	// Also lift authMethods so W1.4 can decide whether to dispatch the
	// `authenticate` RPC and which methodId to send.
	var initParsed struct {
		AgentCapabilities struct {
			LoadSession        bool             `json:"loadSession"`
			PromptCapabilities *json.RawMessage `json:"promptCapabilities,omitempty"`
		} `json:"agentCapabilities"`
		AuthMethods []acpAuthMethod `json:"authMethods"`
	}
	_ = json.Unmarshal(initRes, &initParsed)
	canLoad := initParsed.AgentCapabilities.LoadSession

	// ADR-021 W4.4 — cache the agent's promptCapabilities.image flag
	// for content-block gating. Only the negative declaration is
	// load-bearing (strip + warn); positive or absent → forward.
	if initParsed.AgentCapabilities.PromptCapabilities != nil {
		var pc struct {
			Image *bool `json:"image"`
		}
		_ = json.Unmarshal(*initParsed.AgentCapabilities.PromptCapabilities, &pc)
		if pc.Image != nil {
			d.promptCapMu.Lock()
			d.promptCapImageDecl = pc.Image
			d.promptCapMu.Unlock()
		}
	}

	// ADR-021 W1.4 — authenticate after initialize when the agent
	// advertised any auth methods. Empty list = pre-authenticated
	// daemon (cached creds; nothing for us to do). On failure or
	// timeout we emit an attention_request event with the option set
	// the agent reported and return an error from Start so the host
	// runner can fall back to M2/M4.
	if len(initParsed.AuthMethods) > 0 {
		methodID, err := d.pickAuthMethod(initParsed.AuthMethods)
		if err != nil {
			d.emitAuthAttention(parent, initParsed.AuthMethods, err.Error())
			return fmt.Errorf("acp authenticate: %w", err)
		}
		authCtx, cancelAuth := context.WithTimeout(parent, d.AuthTimeout)
		_, authErr := d.call(authCtx, "authenticate", map[string]any{
			"methodId": methodID,
		})
		cancelAuth()
		if authErr != nil {
			d.emitAuthAttention(parent, initParsed.AuthMethods, authErr.Error())
			return fmt.Errorf("acp authenticate (method=%s): %w", methodID, authErr)
		}
	}

	// Decide between session/new (cold start) and session/load (resume).
	// Resume requires both: a captured cursor (ResumeSessionID) AND the
	// agent advertising loadSession. Either missing → cold start. Load
	// failure → fall through to session/new so the operator still gets
	// a session even if the cursor is stale on the agent's disk.
	var (
		sres        json.RawMessage
		usedLoad    bool
	)
	if d.ResumeSessionID != "" && canLoad {
		// Set replayActive BEFORE issuing the call so any session/update
		// notifications the agent streams during the load (historical
		// turn replay) get tagged in handleNotification.
		d.setReplay(true)
		nsCtx, cancelNS := context.WithTimeout(parent, d.HandshakeTimeout)
		loadRes, loadErr := d.call(nsCtx, "session/load", map[string]any{
			"sessionId":      d.ResumeSessionID,
			"cwd":            "",
			"mcpServers":     []any{},
			"clientMetadata": map[string]any{"name": "termipod-hostrunner"},
		})
		cancelNS()
		d.setReplay(false)
		if loadErr == nil {
			sres = loadRes
			usedLoad = true
		} else {
			d.Log.Warn("acp session/load failed; falling back to session/new",
				"agent", d.AgentID, "cursor", d.ResumeSessionID, "err", loadErr)
		}
	}
	if !usedLoad {
		// session/new — fresh session. Per-call deadline; same rationale
		// as initialize.
		nsCtx, cancelNS := context.WithTimeout(parent, d.HandshakeTimeout)
		newRes, newErr := d.call(nsCtx, "session/new", map[string]any{
			"cwd":            "",
			"mcpServers":     []any{},
			"clientMetadata": map[string]any{"name": "termipod-hostrunner"},
		})
		cancelNS()
		if newErr != nil {
			return fmt.Errorf("acp session/new: %w", newErr)
		}
		sres = newRes
	}
	var sr struct {
		SessionID string `json:"sessionId"`
		// ADR-021 W2.2 — cache the agent's mode/model lists at session/new
		// time so Input("set_mode"/"set_model") can validate ids without
		// burning a round trip on every keypress. Field shape is the ACP
		// spec's session/new response (gemini-cli@0.41.2 verified): each
		// list element is an object with `id`, `name`, `description`.
		Modes struct {
			AvailableModes []struct {
				ID string `json:"id"`
			} `json:"availableModes"`
		} `json:"modes"`
		Models struct {
			AvailableModels []struct {
				ID string `json:"id"`
			} `json:"availableModels"`
		} `json:"models"`
	}
	_ = json.Unmarshal(sres, &sr)
	// Some agents implement session/load by returning the same id we
	// passed in; others might omit it. Fall back to ResumeSessionID
	// when the response didn't carry one but we know the load succeeded.
	if sr.SessionID == "" && usedLoad {
		sr.SessionID = d.ResumeSessionID
	}
	d.mu.Lock()
	d.sessionID = sr.SessionID
	d.mu.Unlock()
	d.modesMu.Lock()
	d.availableModes = make(map[string]struct{}, len(sr.Modes.AvailableModes))
	for _, m := range sr.Modes.AvailableModes {
		if m.ID != "" {
			d.availableModes[m.ID] = struct{}{}
		}
	}
	d.availableModels = make(map[string]struct{}, len(sr.Models.AvailableModels))
	for _, m := range sr.Models.AvailableModels {
		if m.ID != "" {
			d.availableModels[m.ID] = struct{}{}
		}
	}
	d.modesMu.Unlock()

	_ = d.Poster.PostAgentEvent(parent, d.AgentID, "lifecycle", "system",
		map[string]any{"phase": "started", "mode": "M1", "session_id": sr.SessionID})

	// session.init carries the engine-side cursor on a `producer=agent`
	// event so handlers_sessions.captureEngineSessionID can lift it into
	// `sessions.engine_session_id` (ADR-014 + ADR-021 W1.1). The lifecycle
	// frame above is `producer=system` and intentionally excluded from
	// that capture path; this dedicated event matches the shape claude's
	// stream-json driver already emits (driver_stdio.go:309), so the
	// engine-neutral capture works for ACP without a hub-side branch.
	if sr.SessionID != "" {
		_ = d.Poster.PostAgentEvent(parent, d.AgentID, "session.init", "agent",
			map[string]any{"session_id": sr.SessionID})
	}
	return nil
}

// Stop closes the transport (which unblocks the reader), waits, and emits
// lifecycle.stopped. Idempotent.
// promptCapImage reports whether the agent's initialize response
// allows image content blocks. Returns true when the declaration was
// absent (forward-compat with agents that omit promptCapabilities) or
// explicitly true; only an explicit false strips images. ADR-021 W4.4.
func (d *ACPDriver) promptCapImage() bool {
	d.promptCapMu.Lock()
	defer d.promptCapMu.Unlock()
	if d.promptCapImageDecl == nil {
		return true
	}
	return *d.promptCapImageDecl
}

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

// acpAuthMethod is one entry from initialize.authMethods. ACP doesn't
// pin the field set tightly — we read what we need (id, label,
// description, optional `interactive` flag) and tolerate extras.
type acpAuthMethod struct {
	ID          string `json:"id"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Interactive is set true when invoking this method requires a
	// human present at the host (e.g. opening an OAuth URL in a
	// browser). Daemons spawned in non-desktop environments can't
	// satisfy interactive flows; the picker prefers non-interactive
	// methods when no explicit AuthMethod is set.
	Interactive bool `json:"interactive,omitempty"`
}

// pickAuthMethod resolves which ACP authentication methodId to use,
// per ADR-021 D3 precedence:
//
//  1. Explicit override from steward template / family default
//     (d.AuthMethod). Returns it as-is provided the agent advertises
//     a method with that id.
//  2. First non-interactive method in `methods`. Targets daemons that
//     can self-auth from cached creds without opening a browser.
//  3. Fallback: error — there is no method we can pick without human
//     intervention; the caller emits an attention_request so the
//     principal can resolve out-of-band (run `gemini auth`, set
//     GEMINI_API_KEY, or override via the steward template).
func (d *ACPDriver) pickAuthMethod(methods []acpAuthMethod) (string, error) {
	// (1) explicit preference. Validate against the advertised set so
	// a typo'd template fails loudly rather than passing a meaningless
	// id to the agent.
	if d.AuthMethod != "" {
		for _, m := range methods {
			if m.ID == d.AuthMethod {
				return d.AuthMethod, nil
			}
		}
		return "", fmt.Errorf(
			"configured auth_method %q not in agent's advertised authMethods",
			d.AuthMethod,
		)
	}
	// (2) first non-interactive method.
	for _, m := range methods {
		if !m.Interactive {
			return m.ID, nil
		}
	}
	// (3) only interactive methods left — daemon needs out-of-band
	// human attention to authenticate.
	return "", fmt.Errorf(
		"only interactive auth methods available; cached creds required",
	)
}

// emitAuthAttention surfaces an authentication failure as a typed
// `attention_request` agent_event so mobile can render the option set
// to the principal. ADR-021 W1.4. The payload matches the same broad
// shape mobile already renders for `approval_request` events (request
// id + agent-supplied options) so the renderer code path can be
// shared on the Phase 1.4 mobile work — for now it lands as a typed
// kind even if the renderer treats it as a generic "needs your
// attention" surface.
func (d *ACPDriver) emitAuthAttention(
	ctx context.Context, methods []acpAuthMethod, reason string,
) {
	options := make([]map[string]any, 0, len(methods))
	for _, m := range methods {
		options = append(options, map[string]any{
			"id":          m.ID,
			"label":       m.Label,
			"description": m.Description,
			"interactive": m.Interactive,
		})
	}
	_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "attention_request", "agent",
		map[string]any{
			"kind":               "auth_required",
			"reason":             reason,
			"configured_method":  d.AuthMethod,
			"available_methods":  options,
			// Hint surfaces the most common operator fix verbatim so
			// mobile doesn't need its own copy of the playbook.
			"remediation": "Run `gemini auth` on the host (oauth-personal) " +
				"OR set GEMINI_API_KEY in the daemon's environment " +
				"OR override `auth_method:` in the steward template.",
		})
}

// setReplay flips the replay tag window. handleNotification reads the
// flag while building each event payload so historical turn frames
// streamed by the agent in response to session/load come through with
// `replay: true` set.
func (d *ACPDriver) setReplay(v bool) {
	d.replayMu.Lock()
	d.replayActive = v
	d.replayMu.Unlock()
}

// tagIfReplay annotates a payload with `replay: true` when a
// session/load is currently streaming history. Caller-supplied payloads
// are kept untouched outside the replay window so the live wire shape
// is byte-identical to today's traffic.
func (d *ACPDriver) tagIfReplay(payload map[string]any) map[string]any {
	d.replayMu.Lock()
	active := d.replayActive
	d.replayMu.Unlock()
	if !active || payload == nil {
		return payload
	}
	payload["replay"] = true
	return payload
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
			d.tagIfReplay(map[string]any{
				"text":       cumulative,
				"message_id": msgID,
				"partial":    true,
			}))
	case "tool_call":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "tool_call", "agent",
			d.tagIfReplay(map[string]any{
				"id":     u["toolCallId"],
				"name":   u["title"],
				"kind":   u["kind"],
				"status": u["status"],
				"input":  u["rawInput"],
			}))
	case "tool_call_update":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "tool_call_update", "agent", d.tagIfReplay(u))
	case "plan":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "plan", "agent", d.tagIfReplay(u))
	case "diff":
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "diff", "agent", d.tagIfReplay(u))
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
		_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "raw", "agent", d.tagIfReplay(u))
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
		images := extractImageInputs(payload)
		if body == "" && len(images) == 0 {
			return fmt.Errorf("acp driver: text input missing body")
		}
		// ADR-021 W4.4 — image content blocks lower to ACP shape
		// `{type:"image", mimeType, data}` and lead the prompt array;
		// the text block (if any) trails so the model reads imagery
		// before the question. Hub-side W4.1 already enforced
		// mime/size/count caps. promptCapabilities.image gating is
		// best-effort: if the cached capabilities flag is explicitly
		// false we drop images and emit a kind=system warning so the
		// principal sees why their attachment didn't reach the agent;
		// otherwise we forward as-is.
		if len(images) > 0 && !d.promptCapImage() {
			_ = d.Poster.PostAgentEvent(ctx, d.AgentID, "system", "agent",
				map[string]any{
					"reason":       "agent did not advertise image input support — attached images dropped",
					"dropped":      len(images),
					"engine":       "acp",
					"capability":   "promptCapabilities.image",
				})
			images = nil
			if body == "" {
				return fmt.Errorf("acp driver: text input has no body and image attachments were dropped (agent rejected promptCapabilities.image)")
			}
		}
		prompt := make([]map[string]any, 0, len(images)+1)
		for _, img := range images {
			prompt = append(prompt, map[string]any{
				"type":     "image",
				"mimeType": img.mime,
				"data":     img.data,
			})
		}
		if body != "" {
			prompt = append(prompt, map[string]any{"type": "text", "text": body})
		}
		d.resetTurn()
		promptCtx, cancel := context.WithTimeout(ctx, d.PromptTimeout)
		defer cancel()
		res, err := d.call(promptCtx, "session/prompt", map[string]any{
			"sessionId": sid,
			"prompt":    prompt,
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
	case "set_mode":
		// ADR-021 W2.2 — runtime mode switch. Validated against the cached
		// availableModes from session/new so a typo doesn't burn a round
		// trip. ACP RPC: session/set_mode { sessionId, modeId }.
		modeID, _ := payload["mode_id"].(string)
		if modeID == "" {
			return fmt.Errorf("acp driver: set_mode missing mode_id")
		}
		d.modesMu.Lock()
		_, ok := d.availableModes[modeID]
		hasList := len(d.availableModes) > 0
		d.modesMu.Unlock()
		if !hasList {
			return fmt.Errorf("acp driver: set_mode unsupported (agent did not advertise modes)")
		}
		if !ok {
			return fmt.Errorf("acp driver: set_mode unknown mode_id %q", modeID)
		}
		callCtx, cancel := context.WithTimeout(ctx, d.HandshakeTimeout)
		defer cancel()
		_, err := d.call(callCtx, "session/set_mode", map[string]any{
			"sessionId": sid,
			"modeId":    modeID,
		})
		return err
	case "set_model":
		// ADR-021 W2.2 — runtime model switch. Same shape as set_mode.
		modelID, _ := payload["model_id"].(string)
		if modelID == "" {
			return fmt.Errorf("acp driver: set_model missing model_id")
		}
		d.modesMu.Lock()
		_, ok := d.availableModels[modelID]
		hasList := len(d.availableModels) > 0
		d.modesMu.Unlock()
		if !hasList {
			return fmt.Errorf("acp driver: set_model unsupported (agent did not advertise models)")
		}
		if !ok {
			return fmt.Errorf("acp driver: set_model unknown model_id %q", modelID)
		}
		callCtx, cancel := context.WithTimeout(ctx, d.HandshakeTimeout)
		defer cancel()
		_, err := d.call(callCtx, "session/set_model", map[string]any{
			"sessionId": sid,
			"modelId":   modelID,
		})
		return err
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
