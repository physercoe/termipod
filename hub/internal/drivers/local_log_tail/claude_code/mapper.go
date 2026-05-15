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
//	attachment       → emit kind=attachment
//	permission-mode  → drop (per-session metadata)
//	custom-title     → drop (session-header metadata; W2d applies once)
//	agent-name       → drop (session-header metadata; W2d applies once)
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
	case "permission-mode", "custom-title", "agent-name",
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
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil, mapErr("assistant.message parse", err)
	}
	out := make([]MappedEvent, 0, len(m.Content))
	for _, block := range m.Content {
		ev := mapAssistantBlock(block)
		if ev != nil {
			out = append(out, *ev)
		}
	}
	return out, nil
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
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, mapErr("user.content string parse", err)
	}
	return []MappedEvent{{
		Kind:     "user_input",
		Producer: "user",
		Payload:  map[string]any{"text": s},
	}}, nil
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
	default:
		// Other system subtypes (debug telemetry, env diff, etc.)
		// are noise on mobile; drop.
		return nil, nil
	}
}

func mapAttachment(_ json.RawMessage, raw json.RawMessage) ([]MappedEvent, error) {
	// W2c MVP: surface the attachment as a kind=attachment event with
	// the raw payload echoed through. Mobile's existing attachment
	// card handles the shape. A future wedge can lift specific
	// fields (path, mime, size) once the on-device cards demand them.
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
