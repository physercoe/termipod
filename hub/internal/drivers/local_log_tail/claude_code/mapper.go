package claudecode

import (
	"encoding/json"
	"strings"
)

// MappedEvent is one (kind, producer, payload) tuple ready to feed
// EventPoster.PostAgentEvent. The adapter posts these in order;
// downstream the hub broadcasts via SSE and mobile renders typed
// cards. The triple shape mirrors what every other driver
// (ACPDriver, StdioDriver, etc.) emits — no new AgentEvent variants
// for this mode.
type MappedEvent struct {
	Kind     string
	Producer string
	Payload  map[string]any
}

// MapLine implements plan §3: turn one JSONL line into zero or more
// MappedEvents. The returned slice may be empty for known-dropped
// types (e.g. permission-mode, file-history-snapshot). Returns an
// error only on a malformed top-level JSON — a known shape with
// unexpected content blocks degrades gracefully (drop the bad block,
// emit the rest, surface schema drift as a system event).
//
// Mapping table (plan §3.1):
//
//	assistant        → 3.2 content-block fan-out (text/thought/tool_call)
//	user             → 3.3 shape branch (user_input | tool_result[])
//	system           → emit only if subtype=compact_boundary
//	attachment       → conditional fan-out (see mapAttachment)
//	permission-mode  → drop (per-session metadata)
//	custom-title     → drop (session-header metadata; W2d applies once)
//	agent-name       → drop (session-header metadata; W2d applies once)
//	ai-title         → drop (session-header metadata; claude-generated)
//	last-prompt      → drop (internal bookkeeping)
//	file-history-snapshot → drop (internal bookkeeping)
//	queue-operation  → drop (internal bookkeeping)
//	<anything else>  → emit kind=system, subtype=unknown_type (§9 drift policy)
func MapLine(raw []byte) ([]MappedEvent, error) {
	// Skip whitespace-only lines defensively; the tailer should
	// have stripped \n already but a stray blank line from a
	// torn write isn't fatal.
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var top struct {
		Type    string          `json:"type"`
		Subtype string          `json:"subtype,omitempty"`
		Message json.RawMessage `json:"message,omitempty"`
		// attachment-specific
		Attachment json.RawMessage `json:"attachment,omitempty"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, mapErr("top-level JSON parse", err)
	}

	switch top.Type {
	case "assistant":
		return mapAssistant(top.Message)
	case "user":
		return mapUser(top.Message)
	case "system":
		return mapSystem(top.Subtype, raw)
	case "attachment":
		return mapAttachment(top.Attachment, raw)
	case "permission-mode", "custom-title", "agent-name", "ai-title",
		"last-prompt", "file-history-snapshot", "queue-operation":
		return nil, nil
	default:
		// Schema drift: surface but never fall back to xterm-VT.
		return []MappedEvent{{
			Kind:     "system",
			Producer: "system",
			Payload: map[string]any{
				"subtype": "unknown_type",
				"type":    top.Type,
			},
		}}, nil
	}
}

// --- assistant ---

func mapAssistant(msg json.RawMessage) ([]MappedEvent, error) {
	if len(msg) == 0 {
		return nil, nil
	}
	var m struct {
		Content []json.RawMessage `json:"content"`
		ID      string            `json:"id,omitempty"`
		Model   string            `json:"model,omitempty"`
		Usage   json.RawMessage   `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil, mapErr("assistant.message parse", err)
	}
	out := make([]MappedEvent, 0, len(m.Content)+1)
	for _, block := range m.Content {
		ev := mapAssistantBlock(block)
		if ev != nil {
			out = append(out, *ev)
		}
	}
	// Per-message usage event (v1.0.662). The on-disk JSONL carries
	// claude's standard usage shape on every assistant message:
	//
	//	"usage": {
	//	  "input_tokens": 6,                 (fresh tokens this call)
	//	  "cache_read_input_tokens": 15806,  (cache hits, billed at ~10%)
	//	  "cache_creation_input_tokens": 13462, (writes to the cache)
	//	  "output_tokens": 38,
	//	  ...
	//	}
	//
	// We emit each one as a `kind=usage` event so the mobile telemetry
	// strip can show a CURRENT-context number (the most recent message's
	// input + cache_read + cache_create) — matching what claude's own
	// `/context` slash command shows in the TUI. Pre-v1.0.662 the chip
	// was sourced from `turn.result.by_model` (driver_stdio M2 path),
	// which sums across every API call inside a turn — so a turn with
	// many tool-use iterations reported many-multiples of the real
	// context. M4 had no usage signal at all, so the chip was either
	// blank or stale.
	if ev := usageFromMessage(m.Model, m.Usage); ev != nil {
		out = append(out, *ev)
	}
	return out, nil
}

// usageFromMessage decodes the claude `message.usage` block and emits
// a per-message `usage` event. Returns nil when the block is missing
// or contains no token counts — mobile already handles that case
// (latest-usage logic is fully nullable).
func usageFromMessage(model string, raw json.RawMessage) *MappedEvent {
	if len(raw) == 0 {
		return nil
	}
	var u struct {
		Input        int `json:"input_tokens"`
		Output       int `json:"output_tokens"`
		CacheRead    int `json:"cache_read_input_tokens"`
		CacheCreate  int `json:"cache_creation_input_tokens"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil
	}
	if u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheCreate == 0 {
		return nil
	}
	payload := map[string]any{
		"input_tokens":  u.Input,
		"output_tokens": u.Output,
		"cache_read":    u.CacheRead,
		"cache_create":  u.CacheCreate,
		"engine":        "claude-code",
		// Per-message (not session-cumulative) — the most recent
		// event wins on the mobile side. NOT tagged cumulative.
	}
	if model != "" {
		payload["model"] = model
		// v1.0.667: include context_window so mobile's telemetry
		// strip can render the context-utilisation chip. Mobile's
		// chip suppresses itself when contextWindow is zero (it
		// can't compute the pct), so without this the chip stayed
		// blank on M4 even though usage events were flowing. All
		// current claude-* models are 200K; future models would
		// extend the switch in claudeModelContextWindow.
		if cw := claudeModelContextWindow(model); cw > 0 {
			payload["context_window"] = cw
		}
	}
	return &MappedEvent{
		Kind:     "usage",
		Producer: "agent",
		Payload:  payload,
	}
}

// claudeModelContextWindow returns the context window of a claude
// model identifier in tokens, or 0 if the identifier is unrecognised
// (mobile then suppresses the chip rather than rendering a wrong %).
//
// Source: Anthropic public model docs (2026-05). claude-opus-4-*,
// claude-sonnet-4-*, and claude-haiku-4-* all ship with a 200K
// context window. claude-3-* are kept on the legacy 200K too. When a
// new model size ships with a different capacity, add the prefix
// here — keeping the map small + explicit so we don't fall back to
// a wrong default.
func claudeModelContextWindow(model string) int {
	const k200 = 200_000
	for _, prefix := range []string{
		"claude-opus-",
		"claude-sonnet-",
		"claude-haiku-",
		"claude-3-",
	} {
		if strings.HasPrefix(model, prefix) {
			return k200
		}
	}
	return 0
}

func mapAssistantBlock(raw json.RawMessage) *MappedEvent {
	var b struct {
		Type      string          `json:"type"`
		Text      string          `json:"text,omitempty"`
		Thinking  string          `json:"thinking,omitempty"`
		Signature string          `json:"signature,omitempty"`
		ID        string          `json:"id,omitempty"`
		Name      string          `json:"name,omitempty"`
		Input     json.RawMessage `json:"input,omitempty"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil
	}
	switch b.Type {
	case "text":
		return &MappedEvent{
			Kind:     "text",
			Producer: "agent",
			Payload:  map[string]any{"text": b.Text},
		}
	case "thinking":
		// Plan §3.2: on 2.1.x the `thinking` field is empty (signed for
		// API verification). Emit a marker so mobile shows a "Thinking…"
		// chip without leaking the (encrypted) content. signature_present
		// is set only when claude-code populated `.signature`.
		return &MappedEvent{
			Kind:     "thought",
			Producer: "agent",
			Payload: map[string]any{
				"text":                "Thinking…",
				"marker_only":         true,
				"signature_present":   b.Signature != "",
			},
		}
	case "tool_use":
		// Forward input as-is so mobile renders the call shape
		// without re-parsing strings.
		var inputAny any
		if len(b.Input) > 0 {
			_ = json.Unmarshal(b.Input, &inputAny)
		}
		return &MappedEvent{
			Kind:     "tool_call",
			Producer: "agent",
			Payload: map[string]any{
				"tool_use_id": b.ID,
				"name":        b.Name,
				"input":       inputAny,
			},
		}
	default:
		// Drift inside an otherwise-known shape: drop the block but
		// don't poison the turn. Mobile sees nothing for this block;
		// the surrounding text/tool_use blocks still render.
		return nil
	}
}

// --- user ---

func mapUser(msg json.RawMessage) ([]MappedEvent, error) {
	if len(msg) == 0 {
		return nil, nil
	}
	// `content` is a discriminated union: JSON string for typed
	// prompts; JSON array of tool_result blocks otherwise.
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil, mapErr("user.message parse", err)
	}
	if len(m.Content) == 0 {
		return nil, nil
	}
	// Discriminator: leading byte is `"` for a string, `[` for an array.
	first := skipWS(m.Content)
	if first == '"' {
		return mapUserString(m.Content)
	}
	if first == '[' {
		return mapUserArray(m.Content)
	}
	// Unknown shape — surface drift instead of guessing.
	return []MappedEvent{{
		Kind:     "system",
		Producer: "system",
		Payload: map[string]any{
			"subtype":      "user_content_drift",
			"first_char":   string(first),
		},
	}}, nil
}

func mapUserString(raw json.RawMessage) ([]MappedEvent, error) {
	// v1.0.663 dropped the `kind=user_input` emission this branch
	// previously produced. Every user-typed message is already on
	// the wire as an `input.text` event the hub inserts the moment
	// mobile POSTs to `/v1/agents/<id>/input` (handlers_sessions.go
	// `input.text` insertion) — that record is the canonical, mobile-
	// owned source. The JSONL echo carries the FULL hub-injected
	// envelope ("[directive from the principal]\n<body>\n\nReply in
	// this chat…") which mobile then rendered as a SECOND user-side
	// message, looking like a duplicate of what the operator typed.
	// Dropping the emission removes the dup. Tool-result blocks
	// (handled by the sibling mapUserArray) are unrelated and still
	// flow. We still parse the content here to surface a parse-error
	// for malformed JSONL, but emit nothing.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, mapErr("user.content string parse", err)
	}
	_ = s
	return nil, nil
}

func mapUserArray(raw json.RawMessage) ([]MappedEvent, error) {
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, mapErr("user.content array parse", err)
	}
	out := make([]MappedEvent, 0, len(blocks))
	for _, blk := range blocks {
		ev := mapToolResultBlock(blk)
		if ev != nil {
			out = append(out, *ev)
		}
	}
	return out, nil
}

func mapToolResultBlock(raw json.RawMessage) *MappedEvent {
	var b struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error,omitempty"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil
	}
	if b.Type != "tool_result" {
		return nil
	}
	// content is heterogeneous: plain string OR array of
	// {type:"text", text:"…"} blocks. Normalize to one string for
	// transport; mobile's existing tool_result renderer handles the
	// AgentEvent shape unchanged.
	contentStr := normalizeToolResultContent(b.Content)
	denied := strings.HasPrefix(contentStr, "<tool_use_error>")
	return &MappedEvent{
		Kind:     "tool_result",
		Producer: "agent",
		Payload: map[string]any{
			"tool_use_id": b.ToolUseID,
			"is_error":    b.IsError,
			"content":     contentStr,
			"denied":      denied,
		},
	}
}

func normalizeToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	first := skipWS(raw)
	if first == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return string(raw)
	}
	if first == '[' {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &parts); err != nil {
			return string(raw)
		}
		var sb strings.Builder
		for i, p := range parts {
			if p.Type != "text" {
				continue
			}
			if i > 0 && sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(p.Text)
		}
		return sb.String()
	}
	// Unknown shape — pass through raw bytes so the operator can
	// see what claude actually emitted.
	return string(raw)
}

// --- system / attachment ---

func mapSystem(subtype string, raw json.RawMessage) ([]MappedEvent, error) {
	switch subtype {
	case "compact_boundary":
		var s struct {
			Subtype string `json:"subtype"`
		}
		_ = json.Unmarshal(raw, &s)
		return []MappedEvent{{
			Kind:     "system",
			Producer: "system",
			Payload:  map[string]any{"subtype": "compact_boundary"},
		}}, nil
	case "turn_duration":
		// v1.0.668: claude's own end-of-turn marker. It's the LAST
		// frame claude writes for a turn (after assistant text +
		// stop_hook_summary), so emitting turn.result here guarantees
		// it lands on the hub with a higher seq than the preceding
		// text + usage. Mobile's busy-walker scans tail-first and
		// stops at the first turn.result/completion it finds, so
		// having turn.result be the LATEST event in the session is
		// what flips the cancel button off.
		//
		// Pre-v1.0.668 the only source of turn.result was the Stop
		// hook handler (hookStop posted immediately on hook fire),
		// which raced the tailer: hookStop's POST landed BEFORE the
		// tailer caught up to the assistant text frame, so the wire
		// order was turn.result → text → usage and the walker
		// returned busy. Caught on v1.0.667 dev-box smoke when the
		// cancel button stayed on after a multi-tool-use MCP turn.
		var s struct {
			Subtype     string `json:"subtype"`
			DurationMs  int    `json:"durationMs"`
			MessageCount int   `json:"messageCount"`
		}
		_ = json.Unmarshal(raw, &s)
		return []MappedEvent{{
			Kind:     "turn.result",
			Producer: "agent",
			Payload: map[string]any{
				"reason":        "end_of_turn",
				"status":        "success",
				"duration_ms":   s.DurationMs,
				"message_count": s.MessageCount,
			},
		}}, nil
	default:
		// Other system subtypes (debug telemetry, env diff, etc.)
		// are noise on mobile; drop.
		return nil, nil
	}
}

// attachmentDropTypes is the set of inner attachment.type values that
// are pure claude-internal bookkeeping — surfacing them as cards on
// mobile turns the transcript into a debug dump. The set was derived
// empirically from a real m4-test session JSONL (v1.0.660 smoke):
//
//   - hook_success / hook_error  — telemetry that our own hook-fire
//     shim ran (and returned what). The event the hook handler
//     produced is already on the wire via OnHook → adapter.post.
//   - deferred_tools_delta       — claude's deferred-tools registry
//     sync (catalog of MCP tools loaded lazily). No user signal.
//   - agent_listing_delta        — claude's subagent registry sync.
//     No user signal.
//   - skill_listing              — claude's skill catalog sync. No
//     user signal.
//
// Anything else (real file attachments, image references, future
// shapes we haven't seen) still fans out as kind=attachment so we
// don't silently swallow legitimate content.
var attachmentDropTypes = map[string]bool{
	"hook_success":         true,
	"hook_error":           true,
	"deferred_tools_delta": true,
	"agent_listing_delta":  true,
	"skill_listing":        true,
}

func mapAttachment(att json.RawMessage, raw json.RawMessage) ([]MappedEvent, error) {
	// Peek the inner attachment.type so we can drop telemetry/registry
	// frames without paying for the full top-level unmarshal twice.
	if len(att) > 0 {
		var inner struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(att, &inner); err == nil {
			if attachmentDropTypes[inner.Type] {
				return nil, nil
			}
		}
	}
	// Surface anything we don't recognize as a kind=attachment event
	// with the raw payload echoed through. Mobile's existing
	// attachment card handles the shape; a future wedge can lift
	// specific fields (path, mime, size) once the on-device cards
	// demand them.
	var anyPayload map[string]any
	if err := json.Unmarshal(raw, &anyPayload); err != nil {
		return nil, mapErr("attachment parse", err)
	}
	return []MappedEvent{{
		Kind:     "attachment",
		Producer: "agent",
		Payload:  anyPayload,
	}}, nil
}

// skipWS returns the first non-whitespace byte of raw (or 0 if the
// slice is whitespace-only). Used to discriminate JSON string-vs-array
// without unmarshaling twice.
func skipWS(raw []byte) byte {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b
		}
	}
	return 0
}

type mapError struct {
	what string
	err  error
}

func (e *mapError) Error() string { return "claude-code mapper: " + e.what + ": " + e.err.Error() }
func (e *mapError) Unwrap() error { return e.err }

func mapErr(what string, err error) error { return &mapError{what: what, err: err} }
