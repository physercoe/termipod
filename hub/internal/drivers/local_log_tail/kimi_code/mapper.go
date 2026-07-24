// Package kimi_code is the LocalLogTail adapter for Kimi Code CLI's
// session wire store (docs/plans/agent-transcript-redesign.md §6 P4,
// ticket #372). kimi records every session under
//
//	<KIMI_CODE_HOME|~/.kimi-code>/sessions/<wd_*>/<session_*>/agents/<id>/wire.jsonl
//
// — a line-delimited JSON event log (protocol v1.x, gated via the
// `metadata` event's protocol_version). The adapter resolves the
// session for the spawn's workdir via workspaces.json, tails the main
// agent's wire file plus every subagent wire file (state.json maps the
// parentAgentId tree), and maps wire events onto the same AgentEvent
// triples the claude-code / antigravity adapters emit so the existing
// clients render them unchanged.
//
// Shapes in this package are pinned against kimi-code 0.28.1 captures
// (see testdata/ and the plan's Appendix B). Field names were read off
// real wire.jsonl files, not guessed.
package kimi_code

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MappedEvent is one (kind, producer, payload) tuple ready for
// EventPoster.PostAgentEvent — the same triple every other driver
// emits, so no new AgentEvent variants are introduced for this engine.
type MappedEvent struct {
	Kind     string
	Producer string
	Payload  map[string]any
}

// ErrUnsupportedProtocol is returned (wrapped) by MapLine when the
// wire file's `metadata` event carries a protocol_version this adapter
// doesn't understand. The adapter treats it as fatal for the affected
// tail (posts a system notice and stops mapping that file); the launch
// glue uses SupportedProtocolVersion for the spawn-time sniff that
// falls back to PaneDriver.
var ErrUnsupportedProtocol = errors.New("kimi-code wire: unsupported protocol version")

// ProtocolError wraps ErrUnsupportedProtocol with the offending
// version string for logs.
type ProtocolError struct{ Version string }

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("%s: %q", ErrUnsupportedProtocol, e.Version)
}
func (e *ProtocolError) Unwrap() error { return ErrUnsupportedProtocol }

// SupportedProtocolVersion reports whether a wire `metadata`
// protocol_version is one this mapper was verified against. The
// captured corpus (kimi-code 0.28.1, 2026-07) contains "1.4" (the
// version the plan's Appendix B pins) and "1.5" (newer writes on the
// same build) with byte-identical event shapes for every type we map,
// so the gate is on the MAJOR version: any 1.x is accepted, anything
// else (missing, "2.0", "9") is rejected → PaneDriver fallback.
func SupportedProtocolVersion(v string) bool {
	major, _, _ := strings.Cut(v, ".")
	return major == "1"
}

// Mapper turns wire.jsonl lines into MappedEvents. One Mapper per
// agent wire file: it is stateful (per-turn plan message_id chain,
// mirroring the ACP driver's per-turn message_id+partial convention at
// driver_acp.go's plan arm) and stamps subagent provenance onto every
// payload when the agent isn't "main".
type Mapper struct {
	// AgentID is kimi's agent id — the agents/<id> directory name
	// ("main", "agent-9", ...).
	AgentID string
	// ParentAgentID is the state.json parentAgentId ("" for main or
	// when the tree hasn't flushed yet).
	ParentAgentID string
	// Engine names the family for usage events (mirrors the claude
	// adapter's engine field). Empty defaults to "kimi-code".
	Engine string

	turnSeq   int
	planMsgID string
}

// NewMapper builds a Mapper for one agent wire file.
func NewMapper(agentID, parentAgentID, engine string) *Mapper {
	if engine == "" {
		engine = "kimi-code"
	}
	return &Mapper{AgentID: agentID, ParentAgentID: parentAgentID, Engine: engine}
}

// MapLine turns one wire.jsonl line into zero or more MappedEvents.
// Returns an error only on malformed top-level JSON or an unsupported
// protocol version; known shapes with unexpected content degrade
// gracefully (drop the block, surface schema drift as a system event).
//
// Mapping table (verified against kimi-code 0.28.1 wire captures):
//
//	metadata                        → drop; gate protocol_version (ErrUnsupportedProtocol)
//	config.update                   → drop (session metadata; model arrives via usage.record)
//	tools.set_active_tools          → drop (tool catalog, no user signal)
//	turn.prompt                     → drop (hub already posted input.text; arm the per-turn plan chain)
//	context.append_message          → drop (same input.text duplication rationale as claude's mapUserString)
//	context.append_loop_event:
//	  step.begin                    → drop (internal marker)
//	  content.part part.type=text   → text     {text, message_id}
//	  content.part part.type=think  → thought  {text, message_id}
//	  tool.call                     → tool_call {tool_use_id, name, input, display?, description?}
//	  tool.result                   → tool_result {tool_use_id, content, is_error, truncated?}
//	  step.end finishReason=end_turn → turn.result {reason:end_of_turn, status:success}
//	  step.end (other reasons)      → drop (per-step bookkeeping; usage.record carries the numbers)
//	llm.tools_snapshot / llm.request → drop (request telemetry; model rides the usage event)
//	tools.update_store key=todo     → plan {entries, message_id, partial:true} (full snapshot per update)
//	tools.update_store (other keys) → drop (engine-internal stores)
//	usage.record scope=turn         → usage {input_tokens, output_tokens, cache_read, cache_create, model, engine, scope}
//	usage.record scope=session      → usage (same + cumulative:true)
//	permission.record_approval_result → approval_result {tool_use_id, name, action, decision, ...}
//	<anything else>                 → system {subtype:unknown_type} (drift policy, mirrors claude mapper §9)
func (m *Mapper) MapLine(raw []byte) ([]MappedEvent, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var top struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, mapErr("top-level JSON parse", err)
	}

	switch top.Type {
	case "metadata":
		return nil, m.mapMetadata(raw)
	case "config.update", "tools.set_active_tools", "context.append_message",
		"llm.tools_snapshot", "llm.request":
		return nil, nil
	case "turn.prompt":
		// New user turn: arm a fresh per-turn plan chain so the todo
		// snapshots that follow fold into ONE card on the clients
		// (agent-transcript-redesign §6 P1/G3). The prompt body itself
		// is dropped — the hub posts input.text the moment mobile
		// submits it, and kimi's echo carries the full envelope, so
		// re-emitting would duplicate the user bubble (the exact
		// rationale behind claude's mapUserString drop, v1.0.663).
		m.turnSeq++
		m.planMsgID = ""
		return nil, nil
	case "context.append_loop_event":
		return m.mapLoopEvent(raw)
	case "tools.update_store":
		return m.mapUpdateStore(raw)
	case "usage.record":
		return m.mapUsage(raw)
	case "permission.record_approval_result":
		return m.mapApprovalResult(raw)
	default:
		return []MappedEvent{m.event("system", "system", map[string]any{
			"subtype": "unknown_type",
			"type":    top.Type,
		})}, nil
	}
}

// event is the single construction point for MappedEvents — it stamps
// subagent provenance onto every payload a non-main agent emits so the
// clients (and the P2 state dock's background-task chips) can tell
// delegated activity apart from the main agent's own work.
func (m *Mapper) event(kind, producer string, payload map[string]any) MappedEvent {
	if m.AgentID != "" && m.AgentID != "main" {
		payload["subagent"] = true
		payload["kimi_agent_id"] = m.AgentID
		if m.ParentAgentID != "" {
			payload["parent_agent_id"] = m.ParentAgentID
		}
	}
	return MappedEvent{Kind: kind, Producer: producer, Payload: payload}
}

// mapMetadata gates the protocol version. kimi writes `metadata` as the
// first line of every wire.jsonl (main + subagents), so this check
// runs before any content flows for that agent.
func (m *Mapper) mapMetadata(raw []byte) error {
	var md struct {
		ProtocolVersion string `json:"protocol_version"`
	}
	if err := json.Unmarshal(raw, &md); err != nil {
		return mapErr("metadata parse", err)
	}
	if !SupportedProtocolVersion(md.ProtocolVersion) {
		return &ProtocolError{Version: md.ProtocolVersion}
	}
	return nil
}

// --- context.append_loop_event ---

func (m *Mapper) mapLoopEvent(raw []byte) ([]MappedEvent, error) {
	var env struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, mapErr("append_loop_event parse", err)
	}
	if len(env.Event) == 0 {
		return nil, nil
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(env.Event, &head); err != nil {
		return nil, mapErr("loop event head parse", err)
	}
	switch head.Type {
	case "content.part":
		return m.mapContentPart(env.Event)
	case "tool.call":
		return m.mapToolCall(env.Event)
	case "tool.result":
		return m.mapToolResult(env.Event)
	case "step.end":
		return m.mapStepEnd(env.Event)
	case "step.begin":
		return nil, nil
	default:
		// Unknown loop-event subtype: drop quietly (the loop vocabulary
		// grows with kimi releases; the parts we don't know carry no
		// rendering obligation). Mirrors the claude mapper's block-level
		// drift policy.
		return nil, nil
	}
}

// mapContentPart surfaces the assistant's own prose. Verified on the
// 0.28.1 capture: assistant text DOES appear in wire.jsonl as
// content.part events (354 text parts + 900 think parts in the sampled
// main wire) — each part is a complete unit, not a stream delta — so
// prose is recoverable from the wire alone and the pane text is not
// needed for structure. message_id is stamped with the part's uuid so
// client-side replay dedupe / fold-by-id keeps working; no partial
// flag (parts are complete).
func (m *Mapper) mapContentPart(raw json.RawMessage) ([]MappedEvent, error) {
	var e struct {
		UUID string `json:"uuid"`
		Part struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Think string `json:"think"`
		} `json:"part"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, mapErr("content.part parse", err)
	}
	var kind, body string
	switch e.Part.Type {
	case "text":
		kind, body = "text", e.Part.Text
	case "think":
		kind, body = "thought", e.Part.Think
	default:
		return nil, nil
	}
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	payload := map[string]any{"text": body}
	if e.UUID != "" {
		payload["message_id"] = e.UUID
	}
	return []MappedEvent{m.event(kind, "agent", payload)}, nil
}

// mapToolCall maps kimi's tool.call onto the claude-M4 tool_call shape
// (tool_use_id/name/input) so the existing client cards render it, and
// carries kimi's own `display` hint verbatim into the payload — P2's
// state dock uses it to recognise background tasks / todo-list calls
// (plan §6 P2: "kimi `display` hints via P4").
func (m *Mapper) mapToolCall(raw json.RawMessage) ([]MappedEvent, error) {
	var e struct {
		ToolCallID  string          `json:"toolCallId"`
		Name        string          `json:"name"`
		Args        json.RawMessage `json:"args"`
		Description string          `json:"description"`
		Display     json.RawMessage `json:"display"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, mapErr("tool.call parse", err)
	}
	var inputAny any
	if len(e.Args) > 0 {
		_ = json.Unmarshal(e.Args, &inputAny)
	}
	payload := map[string]any{
		"tool_use_id": e.ToolCallID,
		"name":        e.Name,
		"input":       inputAny,
	}
	if len(e.Display) > 0 {
		var displayAny any
		if err := json.Unmarshal(e.Display, &displayAny); err == nil {
			payload["display"] = displayAny
		}
	}
	if e.Description != "" {
		payload["description"] = e.Description
	}
	return []MappedEvent{m.event("tool_call", "agent", payload)}, nil
}

// mapToolResult pairs back to the tool_call via tool_use_id (the
// clients' FoldMaps join key). isError on the wire is camelCase —
// verified: 35 of 959 sampled results carry {"isError": true}.
func (m *Mapper) mapToolResult(raw json.RawMessage) ([]MappedEvent, error) {
	var e struct {
		ToolCallID string `json:"toolCallId"`
		Result     struct {
			Output    string `json:"output"`
			IsError   bool   `json:"isError"`
			Truncated bool   `json:"truncated"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, mapErr("tool.result parse", err)
	}
	payload := map[string]any{
		"tool_use_id": e.ToolCallID,
		"content":     e.Result.Output,
		"is_error":    e.Result.IsError,
	}
	if e.Result.Truncated {
		payload["truncated"] = true
	}
	return []MappedEvent{m.event("tool_result", "agent", payload)}, nil
}

// mapStepEnd emits turn.result only on the terminal step of a turn
// (finishReason "end_turn"; intermediate steps end "tool_use" —
// verified 71 end_turn vs 1086 tool_use in the sampled capture).
// Mobile's busy-walker scans tail-first for turn.result/completion, so
// this is what drops the cancel button when a kimi turn completes.
//
// Subagent tails emit turn.result too (marked subagent:true like every
// event from a non-main agent). A subagent's terminal event can
// therefore briefly flip the session busy-walker to idle while the
// main agent is between wire events — accepted deliberately: (1) the
// client's busy inference is default-idle by design (feed_reducer.dart
// v1.0.721 — transient wrong-idle self-heals on the next turn-active
// event), and (2) kimi's main agent blocks inside the delegating Agent
// tool.call while a subagent runs, so the next main event lands
// immediately after the subagent drains. Payload-level busy refinement
// (ignoring subagent-flagged terminal kinds) is client-side P2 work.
func (m *Mapper) mapStepEnd(raw json.RawMessage) ([]MappedEvent, error) {
	var e struct {
		FinishReason string `json:"finishReason"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, mapErr("step.end parse", err)
	}
	if e.FinishReason != "end_turn" {
		return nil, nil
	}
	return []MappedEvent{m.event("turn.result", "agent", map[string]any{
		"reason": "end_of_turn",
		"status": "success",
	})}, nil
}

// --- tools.update_store ---

// mapUpdateStore maps the authoritative todo store onto a `plan`
// event. kimi writes the FULL list on every change (verified:
// key="todo" is the only store key in the sampled corpus), which is
// exactly the ACP plan-update shape — so we mirror the ACP driver's
// per-turn message_id + partial:true convention (driver_acp.go plan
// arm): the clients' fold-in-place reducer collapses the chain into
// one checklist card that updates instead of N snapshot cards.
//
// Entry field mapping: kimi todos carry {title, status}; ACP plan
// entries carry {content, status}. Status vocabulary differs by one
// word — kimi "done" vs ACP "completed" (see the plan's Appendix A
// probe capture) — so "done" is normalised to "completed"; the rest of
// the vocabulary (pending / in_progress) already matches.
func (m *Mapper) mapUpdateStore(raw []byte) ([]MappedEvent, error) {
	var u struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, mapErr("update_store parse", err)
	}
	if u.Key != "todo" {
		return nil, nil
	}
	var items []struct {
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(u.Value, &items); err != nil {
		return nil, mapErr("todo store value parse", err)
	}
	entries := make([]map[string]any, 0, len(items))
	for _, it := range items {
		status := it.Status
		if status == "done" {
			status = "completed"
		}
		entries = append(entries, map[string]any{
			"content": it.Title,
			"status":  status,
		})
	}
	if m.planMsgID == "" {
		// The agent id is part of the chain id: mappers are per wire file
		// but all post into ONE termipod transcript, and subagent files
		// have no turn.prompt (turnSeq stays 0) — without the namespace,
		// two subagents' todo chains would share "…-t0" and the clients'
		// fold-by-message_id would collapse them into one card.
		m.planMsgID = "kimi-plan-" + m.AgentID + "-t" + strconv.Itoa(m.turnSeq)
	}
	return []MappedEvent{m.event("plan", "agent", map[string]any{
		"sessionUpdate": "plan",
		"entries":       entries,
		"message_id":    m.planMsgID,
		"partial":       true,
	})}, nil
}

// --- usage.record ---

// mapUsage flattens kimi's usage block onto the canonical per-message
// usage shape the StdioDriver + claude M4 mapper emit (mobile's
// telemetry strip reads exactly these keys):
//
//	kimi inputOther           → input_tokens  (fresh, non-cache input)
//	kimi output               → output_tokens
//	kimi inputCacheRead       → cache_read
//	kimi inputCacheCreation   → cache_create
//
// usageScope "turn" (per-turn, most-recent-wins) ships untagged like
// claude's per-message events; usageScope "session" is tagged
// cumulative:true so mobile's rollup buckets it with codex's
// session-total events instead of letting it clobber the
// current-context chip (feed_telemetry.dart's cumulative branch).
func (m *Mapper) mapUsage(raw []byte) ([]MappedEvent, error) {
	var u struct {
		Model string `json:"model"`
		Usage struct {
			InputOther         int `json:"inputOther"`
			Output             int `json:"output"`
			InputCacheRead     int `json:"inputCacheRead"`
			InputCacheCreation int `json:"inputCacheCreation"`
		} `json:"usage"`
		UsageScope string `json:"usageScope"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, mapErr("usage.record parse", err)
	}
	payload := map[string]any{
		"input_tokens":  u.Usage.InputOther,
		"output_tokens": u.Usage.Output,
		"cache_read":    u.Usage.InputCacheRead,
		"cache_create":  u.Usage.InputCacheCreation,
		"engine":        m.Engine,
	}
	if u.Model != "" {
		payload["model"] = u.Model
	}
	if u.UsageScope != "" {
		payload["scope"] = u.UsageScope
	}
	if u.UsageScope == "session" {
		payload["cumulative"] = true
	}
	return []MappedEvent{m.event("usage", "agent", payload)}, nil
}

// --- permission.record_approval_result ---

// mapApprovalResult surfaces kimi's approval audit record. The wire
// only carries the POST-HOC result (the user already decided in the
// TUI — there is no pending request for the phone to answer), so this
// deliberately does NOT emit kind=approval_request: that kind feeds
// the clients' attention pipeline and would park a fake actionable
// card + notification. Instead we emit kind=approval_result with the
// request-identifying fields the approval_request shape uses
// (request_id/tool_use_id + tool name + action) plus the decision, and
// a `text` summary so the default event-card arm renders a readable
// line today. (Deviation from a literal approval_request mapping —
// documented in the launch header + P4 wedge notes.)
func (m *Mapper) mapApprovalResult(raw []byte) ([]MappedEvent, error) {
	var a struct {
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Action     string `json:"action"`
		Result     struct {
			Decision      string `json:"decision"`
			SelectedLabel string `json:"selectedLabel"`
			Scope         string `json:"scope"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, mapErr("approval_result parse", err)
	}
	payload := map[string]any{
		"tool_use_id": a.ToolCallID,
		"name":        a.ToolName,
		"action":      a.Action,
		"decision":    a.Result.Decision,
	}
	if a.Result.SelectedLabel != "" {
		payload["selected_label"] = a.Result.SelectedLabel
	}
	if a.Result.Scope != "" {
		payload["scope"] = a.Result.Scope
	}
	// Human-readable summary for the default card renderer. Reads e.g.
	// "Bash approved (Approve once): Running: echo hi".
	var sb strings.Builder
	sb.WriteString(a.ToolName)
	sb.WriteString(" ")
	sb.WriteString(a.Result.Decision)
	if a.Result.SelectedLabel != "" {
		sb.WriteString(" (")
		sb.WriteString(a.Result.SelectedLabel)
		sb.WriteString(")")
	}
	if a.Action != "" {
		sb.WriteString(": ")
		sb.WriteString(a.Action)
	}
	payload["text"] = sb.String()
	return []MappedEvent{m.event("approval_result", "agent", payload)}, nil
}

type mapError struct {
	what string
	err  error
}

func (e *mapError) Error() string { return "kimi-code wire mapper: " + e.what + ": " + e.err.Error() }
func (e *mapError) Unwrap() error { return e.err }

func mapErr(what string, err error) error { return &mapError{what: what, err: err} }
