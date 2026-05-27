package hostrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

// a2aInputPoster is the narrow contract a2aHubDispatcher needs from the
// hub client. The real *Client satisfies this; tests can fake it without
// standing up a hub.
type a2aInputPoster interface {
	PostAgentInput(ctx context.Context, agentID string, fields map[string]any) error
}

// a2aHubDispatcher is the concrete a2a.Dispatcher the host-runner wires
// in when its A2A server is enabled. Incoming message/send calls arrive
// already routed to an agent_id; the dispatcher extracts text parts and
// POSTs them to the hub's /input endpoint for that agent, stamped
// producer="a2a" so peer-originated input is distinguishable in the
// audit trail from phone/web input.
//
// Going through the hub (rather than calling the driver directly) keeps
// peer input on the same audit path as phone/web input and means no new
// cross-cutting state is introduced for A2A.
//
// Response harvesting: Dispatch registers a correlation
// (agentID, taskID, store). When a driver emits producer="agent" output
// events, a2aHubDispatcher.OnAgentEvent routes the text into the
// correlated task's history and advances state submitted → working. On
// a lifecycle "stopped" phase the task flips to completed. Only one
// live task per agent is tracked — a second message/send arriving
// before the first finishes marks the prior one canceled (terminal)
// and supersedes the correlation, mirroring the single-turn shape of
// today's drivers.
type a2aHubDispatcher struct {
	poster a2aInputPoster

	mu sync.Mutex
	// open holds at most one live task per agent. Tasks move out of
	// this map as soon as they transition to a terminal state (either
	// via driver output reaching the completed flip, or being
	// superseded by a fresh message/send).
	open map[string]*a2aOpenTask
}

// a2aOpenTask is the minimum correlation state needed to route driver
// output back into a2a task history. Treated as immutable once
// registered — mutations (state/history) go through the TaskStore,
// which is lock-protected.
type a2aOpenTask struct {
	taskID string
	store  *a2a.TaskStore
}

func newA2AHubDispatcher(c a2aInputPoster) *a2aHubDispatcher {
	return &a2aHubDispatcher{
		poster: c,
		open:   map[string]*a2aOpenTask{},
	}
}

// Dispatch renders msg.Parts into the agent's prompt as kind=text input
// with producer="a2a". Text parts pass through verbatim; file parts
// become "[file: <uri> (...)]" lines the receiver can resolve via
// `blob_get`; data parts become "[data: ...]" lines. A message that
// renders to an empty string yields an error so the message/send
// handler can mark the task failed rather than silently dropping the
// submission.
//
// On success the dispatcher records (agentID, taskID, store) so driver
// output for this agent can be appended back to the task history. A
// pre-existing open task for the same agent is marked canceled before
// the new one takes its slot.
func (d *a2aHubDispatcher) Dispatch(ctx context.Context, agentID string, msg a2a.Message,
	taskID string, store *a2a.TaskStore) error {
	text, err := renderInboundParts(msg.Parts)
	if err != nil {
		return fmt.Errorf("%w: %v", a2a.ErrDispatch, err)
	}
	if text == "" {
		return fmt.Errorf("%w: message has no renderable parts", a2a.ErrDispatch)
	}
	fields := map[string]any{
		"kind":     "text",
		"body":     text,
		"producer": "a2a",
	}
	// ADR-032: the hub relay stamped the orchestration-envelope provenance
	// into message.metadata.termipod; forward it as input fields so the
	// hub composes the envelope (handlePostAgentInput).
	if tp := parseTermipodMeta(msg.Metadata); tp != nil {
		if v, _ := tp["from_role"].(string); v != "" {
			fields["from_role"] = v
		}
		if v, _ := tp["from_handle"].(string); v != "" {
			fields["from_handle"] = v
		}
		if v, _ := tp["kind"].(string); v != "" {
			fields["a2a_kind"] = v
		}
		if v, _ := tp["cause"].(string); v != "" {
			fields["cause"] = v
		}
	}
	if err := d.poster.PostAgentInput(ctx, agentID, fields); err != nil {
		return fmt.Errorf("%w: post input: %v", a2a.ErrDispatch, err)
	}
	d.registerTask(agentID, taskID, store)
	return nil
}

// registerTask claims the agent's single open-task slot for taskID.
// Any pre-existing open task is moved to the canceled terminal state so
// late output can't leak into the wrong history.
func (d *a2aHubDispatcher) registerTask(agentID, taskID string, store *a2a.TaskStore) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.open[agentID]; ok && prev.store != nil {
		prev.store.Update(agentID, prev.taskID,
			a2a.TaskStatus{State: a2a.TaskStateCanceled}, nil)
	}
	d.open[agentID] = &a2aOpenTask{taskID: taskID, store: store}
}

// OnAgentEvent is the tap point for driver output. It is safe to call
// for any (agentID, kind, producer, payload) tuple — unrelated events
// are silently ignored. Invoke this from a wrapper around
// AgentEventPoster.PostAgentEvent so every driver mode feeds the same
// correlator.
//
// State handling:
//   - producer="agent" with a "text" field on the payload: first such
//     event flips state submitted → working and appends the text as a
//     role="agent" history message. Subsequent text events append more
//     messages but stay in working (we have no reliable turn-complete
//     signal across all drivers).
//   - producer="system" + kind="lifecycle" + phase="stopped": flip to
//     completed and release the slot. Without a driver-side idle
//     signal this is the only unambiguous terminal event we can watch.
//   - everything else is a no-op.
func (d *a2aHubDispatcher) OnAgentEvent(agentID, kind, producer string, payload any) {
	d.mu.Lock()
	t, ok := d.open[agentID]
	d.mu.Unlock()
	if !ok {
		return
	}

	switch {
	case producer == "agent" && hasText(payload):
		text := extractPayloadText(payload)
		if text == "" {
			return
		}
		parts, err := json.Marshal([]map[string]any{{"kind": "text", "text": text}})
		if err != nil {
			return
		}
		msg := &a2a.Message{
			MessageID: t.taskID + ".agent." + nextAgentMsgSuffix(),
			Role:      "agent",
			Parts:     parts,
		}
		// submitted → working on first reply; working stays working on
		// subsequent chunks. No completed flip here: most drivers do
		// not emit a turn-complete signal, so we defer that to the
		// lifecycle.stopped path below.
		t.store.Update(agentID, t.taskID,
			a2a.TaskStatus{State: a2a.TaskStateWorking}, msg)
	case producer == "system" && kind == "lifecycle":
		phase := extractLifecyclePhase(payload)
		if phase != "stopped" {
			return
		}
		t.store.Update(agentID, t.taskID,
			a2a.TaskStatus{State: a2a.TaskStateCompleted}, nil)
		d.mu.Lock()
		// Only release the slot if it's still ours — a concurrent
		// Dispatch may have already superseded us.
		if cur, ok := d.open[agentID]; ok && cur == t {
			delete(d.open, agentID)
		}
		d.mu.Unlock()
	}
}

// nextAgentMsgSuffix returns a process-unique discriminator for the
// next agent message id. TaskStore.Update never enforces uniqueness
// on MessageID; we just need something distinct within a task's
// history so tools consuming tasks/get don't collapse duplicates.
var (
	agentMsgCounterMu sync.Mutex
	agentMsgCounter   uint64
)

func nextAgentMsgSuffix() string {
	agentMsgCounterMu.Lock()
	agentMsgCounter++
	n := agentMsgCounter
	agentMsgCounterMu.Unlock()
	return fmt.Sprintf("%d", n)
}

// hasText reports whether payload is a map carrying a non-empty "text"
// field. Kept separate so extractPayloadText can be called
// unconditionally without repeating the type switch.
func hasText(payload any) bool {
	m, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	s, _ := m["text"].(string)
	return s != ""
}

func extractPayloadText(payload any) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m["text"].(string)
	return s
}

func extractLifecyclePhase(payload any) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m["phase"].(string)
	return s
}

// parseTermipodMeta extracts the `termipod` orchestration-envelope bag
// from an A2A message's metadata — the hub relay stamps it with kind,
// cause, from_role, from_handle (ADR-032). Returns nil when absent or
// unparseable, so a legacy peer's message still dispatches.
func parseTermipodMeta(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var meta struct {
		Termipod map[string]any `json:"termipod"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil
	}
	return meta.Termipod
}

// renderInboundParts folds A2A Parts into a single string the receiving
// agent can read. A2A v0.3 allows three part kinds (text/file/data);
// we concat with newlines so multi-part messages arrive intact.
//
// Pre-v1.0.723 this function consumed only text parts and dropped
// file/data on the floor — the "MVP: text only" gate. That made
// cross-host file transfer half-implemented (host A could `attach` and
// the bytes reached the hub, but host B's agent never saw the URI in
// its prompt and so had no way to call `blob_get` to fetch them).
//
// We now render non-text parts as text lines so the receiving agent
// has the URI in its prompt:
//
//   - file part   → "[file: <uri> (<mime>, <size> bytes)]"
//   - data part   → "[data: <json>]" (compact JSON, truncated to ~512 chars)
//
// Bytes-inline file parts (`file.bytes` carrying base64 directly) are
// summarized — we don't inject base64 into the prompt because (a) it
// pollutes context and (b) peers should `attach` first then reference
// the sha if they want the receiver to act on the bytes.
//
// Unknown kinds are still skipped (not errors) so peers can ship
// richer messages without breaking simple agents.
func renderInboundParts(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var parts []inboundPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		line := renderOnePart(p)
		if line == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String(), nil
}

// inboundPart accepts both the A2A v0.3 canonical FilePart shape
// (`file: {uri | bytes, mimeType, name}`) and the looser flat form
// some peers and tests use (`uri` directly at the part level).
// Either shape resolves cleanly via the helper getters below.
type inboundPart struct {
	Kind string          `json:"kind"`
	Text string          `json:"text,omitempty"`
	File *inboundFileRef `json:"file,omitempty"`

	// Flat-form fallbacks used by older test fixtures and some A2A
	// peers that didn't follow the nested FilePart shape.
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Name     string `json:"name,omitempty"`
	Size     int64  `json:"size,omitempty"`

	// Data part — passthrough as compact JSON.
	Data json.RawMessage `json:"data,omitempty"`
}

type inboundFileRef struct {
	URI      string `json:"uri,omitempty"`
	Bytes    string `json:"bytes,omitempty"` // base64; summarized, not inlined
	MimeType string `json:"mimeType,omitempty"`
	Name     string `json:"name,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

// renderOnePart returns the prompt-line shape for one part, or ""
// if there's nothing usable to render. Caller joins non-empty
// returns with newlines.
func renderOnePart(p inboundPart) string {
	switch p.Kind {
	case "text":
		return p.Text
	case "file":
		uri, mime, name, size := fileFields(p)
		var hasInlineBytes bool
		if p.File != nil {
			hasInlineBytes = p.File.Bytes != ""
		}
		// Pick the most agent-actionable piece (URI is what blob_get
		// consumes); fall back to the bytes-inline summary if the
		// peer chose to inline base64 instead of pre-attaching.
		switch {
		case uri != "":
			return "[file: " + uri + fileSuffix(mime, size) + "]"
		case hasInlineBytes:
			label := name
			if label == "" {
				label = "(inline)"
			}
			return "[file: " + label + fileSuffix(mime, size) + " — inline bytes; ask peer to attach + reference by sha to fetch]"
		}
		return ""
	case "data":
		if len(p.Data) == 0 {
			return ""
		}
		// Compact and truncate so a peer can't blow out context with
		// a multi-MB data part.
		raw := strings.Join(strings.Fields(string(p.Data)), " ")
		const dataTruncate = 512
		if len(raw) > dataTruncate {
			raw = raw[:dataTruncate] + "…"
		}
		return "[data: " + raw + "]"
	}
	return ""
}

// fileFields resolves uri/mime/name/size across the two valid shapes.
// The nested `file:` form wins when present (canonical A2A v0.3
// FilePart); the flat form fills in otherwise.
func fileFields(p inboundPart) (uri, mime, name string, size int64) {
	if p.File != nil {
		uri = p.File.URI
		mime = p.File.MimeType
		name = p.File.Name
		size = p.File.Size
	}
	if uri == "" {
		uri = p.URI
	}
	if mime == "" {
		mime = p.MimeType
	}
	if name == "" {
		name = p.Name
	}
	if size == 0 {
		size = p.Size
	}
	return uri, mime, name, size
}

// fileSuffix renders the " (mime, N bytes)" tail when any metadata
// is known. Returns "" so the inner bracket form stays compact when
// the peer sent only a bare URI.
func fileSuffix(mime string, size int64) string {
	var parts []string
	if mime != "" {
		parts = append(parts, mime)
	}
	if size > 0 {
		parts = append(parts, fmt.Sprintf("%d bytes", size))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// a2aPosterTap wraps an AgentEventPoster so every driver-emitted event
// is both forwarded to the hub (the existing contract) and mirrored to
// the A2A correlator. The correlator fans out to open tasks by
// agentID; agents with no live task see zero overhead beyond a mutex
// miss.
type a2aPosterTap struct {
	inner AgentEventPoster
	disp  *a2aHubDispatcher
}

func newA2APosterTap(inner AgentEventPoster, disp *a2aHubDispatcher) *a2aPosterTap {
	return &a2aPosterTap{inner: inner, disp: disp}
}

func (p *a2aPosterTap) PostAgentEvent(ctx context.Context, agentID, kind, producer string, payload any) error {
	if p.disp != nil {
		p.disp.OnAgentEvent(agentID, kind, producer, payload)
	}
	return p.inner.PostAgentEvent(ctx, agentID, kind, producer, payload)
}

// PostAttention delegates to the inner client's AttentionPoster
// surface (production *Client implements both interfaces). Without
// this, the type assertion `cfg.Client.(AttentionPoster)` at
// launch_m2.go:405 silently fails whenever the agentPoster is wrapped
// in a tap (which it always is when A2AAddr is set — the production
// default), and the codex AppServerDriver runs with Attention=nil.
// Every server-initiated approval / MCP-tool-call elicitation then
// auto-declines, surfacing on the codex side as "user rejected MCP
// tool call" — even though the principal never saw a gate. This is
// the v1.0.711 fix for that smoke regression.
//
// Inner-doesn't-implement is treated as a programming error
// (host-runner main has wired this for years); we surface it as a
// non-fatal error rather than panicking so the driver's
// `appserver_attention_post_failed` audit path still records the
// miss and codex still gets a clean decline rather than a stalled
// JSON-RPC request.
func (p *a2aPosterTap) PostAttention(ctx context.Context, in AttentionIn) (AttentionOut, error) {
	ap, ok := p.inner.(AttentionPoster)
	if !ok {
		return AttentionOut{}, fmt.Errorf(
			"a2aPosterTap: inner %T does not implement AttentionPoster", p.inner,
		)
	}
	return ap.PostAttention(ctx, in)
}
