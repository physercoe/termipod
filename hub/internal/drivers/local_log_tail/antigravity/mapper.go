package antigravity

import (
	"encoding/json"
	"strconv"
	"strings"
)

// MappedEvent is one (kind, producer, payload) tuple ready for
// EventPoster.PostAgentEvent — the same triple every other driver emits,
// so no new AgentEvent variants are introduced for this engine. Every
// payload carries `agy_step_index` and `agy_status` so a downstream
// consumer can coalesce the ≤2 emissions a single step produces
// (first-sight + RUNNING→DONE) by step_index.
type MappedEvent struct {
	Kind     string
	Producer string
	Payload  map[string]any
}

// transcriptLine is the full per-step envelope. content is a string for
// every observed type; tool_calls is present only on PLANNER_RESPONSE
// turns where the model decides to call tools.
type transcriptLine struct {
	StepIndex int        `json:"step_index"`
	Source    string     `json:"source"`
	Type      string     `json:"type"`
	Status    string     `json:"status"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// nonToolTypes are the transcript `type`s that are NOT tool-result lines.
// Everything else that carries content is treated as a tool result (agy's
// tool vocabulary — RUN_COMMAND, CODE_ACTION, VIEW_FILE, LIST_DIRECTORY,
// SEARCH_WEB, MCP_TOOL, EDIT_FILE, GENERIC, … — is large and grows, so
// enumerating it would silently drop new tools). A type outside this set
// WITHOUT content is surfaced as schema drift rather than guessed at.
var nonToolTypes = map[string]struct{}{
	"USER_INPUT":           {},
	"CONVERSATION_HISTORY": {},
	"PLANNER_RESPONSE":     {},
	"SYSTEM_MESSAGE":       {},
	"INVOKE_SUBAGENT":      {},
}

// MapStep turns one transcript line into zero or more MappedEvents
// (ADR-035 schema, host-verified on agy 1.0.1). Returns an error only on
// a malformed top-level JSON; a known shape with unexpected content
// degrades gracefully. Mapping:
//
//	USER_INPUT, CONVERSATION_HISTORY → drop (the hub composed the prompt)
//	PLANNER_RESPONSE  → tool_call per tool_calls[]; else text from content
//	SYSTEM_MESSAGE    → system notice (subagent correlation ignored — MVP)
//	INVOKE_SUBAGENT   → tool_call (child not tracked — D-subagents MVP)
//	<tool-result type> → tool_result{name:type, content}
//	<unknown w/o content> → system{subtype:unknown_type} (drift)
func MapStep(raw []byte) ([]MappedEvent, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var ln transcriptLine
	if err := json.Unmarshal(raw, &ln); err != nil {
		return nil, &mapError{what: "top-level JSON parse", err: err}
	}

	base := func(extra map[string]any) map[string]any {
		p := map[string]any{
			"agy_step_index": ln.StepIndex,
			"agy_status":     ln.Status,
		}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}

	switch ln.Type {
	case "USER_INPUT":
		// USER_INPUT is the prompt the hub composed and shipped in via
		// the envelope renderer — we don't surface it as an event
		// because the hub already posted input.text with the same body.
		// EXCEPTION: agy embeds a <USER_SETTINGS_CHANGE> block in step 0
		// announcing the active model (e.g. "Model Selection from None
		// to Gemini 3.5 Flash (Medium)"). agy never persists token /
		// cost / usage anywhere — that block is the ONLY on-disk signal
		// of which model is answering. Emit a synthetic session.init
		// carrying the parsed model name so mobile's AppBar
		// SessionInitChip can show "Gemini 3.5 Flash" instead of an
		// empty model pill. The convID isn't known to the mapper (lives
		// on the adapter), so we leave session_id blank and rely on the
		// mobile gate refiring when the `model` field changes.
		if model := extractAntigravityModel(ln.Content); model != "" {
			return []MappedEvent{{
				Kind:     "session.init",
				Producer: "agent",
				Payload:  base(map[string]any{"model": model}),
			}}, nil
		}
		return nil, nil
	case "CONVERSATION_HISTORY":
		return nil, nil

	case "PLANNER_RESPONSE":
		if len(ln.ToolCalls) > 0 {
			out := make([]MappedEvent, 0, len(ln.ToolCalls))
			for i, tc := range ln.ToolCalls {
				payload := base(map[string]any{
					"name":  tc.Name,
					"input": tc.Args,
					// No native call id at the planner level; the result
					// arrives as the next typed line. step_index pairs them.
					"tool_use_id": syntheticCallID(ln.StepIndex, i),
				})
				// agy includes humanised `toolAction` + `toolSummary`
				// strings in tc.Args ("Resolving revision_requested
				// attention item", "Querying matching attentions from
				// database", "Viewing handleDecideAttention input
				// struct"). They describe intent and are far more
				// informative than `grep_search({"Query":"...","SearchPath":"..."})`
				// alone. Surface them at the top level of the payload so
				// mobile's tool_call card can render them as a subtitle
				// without parsing through `input`. Absent on non-agy
				// engines, so adding the field is additive.
				if ta, ok := tc.Args["toolAction"].(string); ok && ta != "" {
					payload["tool_action"] = ta
				}
				if ts, ok := tc.Args["toolSummary"].(string); ok && ts != "" {
					payload["tool_summary"] = ts
				}
				out = append(out, MappedEvent{
					Kind:     "tool_call",
					Producer: "agent",
					Payload:  payload,
				})
			}
			return out, nil
		}
		if strings.TrimSpace(ln.Content) != "" {
			out := []MappedEvent{{
				Kind:     "text",
				Producer: "agent",
				Payload:  base(map[string]any{"text": ln.Content}),
			}}
			// A PLANNER_RESPONSE with content + no tool_calls + status=DONE
			// is agy's end-of-turn marker (host-verified on the W11 smoke:
			// the model's final text answer with no follow-up tool calls;
			// the next step is USER_INPUT). Emit `turn.result` after the
			// text so mobile's _isAgentBusy() (agent_feed.dart) sees a
			// terminal kind and drops the cancel button. Without this,
			// every turn leaves the agent "busy" forever because text and
			// tool_call are non-terminal in the busy-state ladder, while
			// other engines (claude-code, gemini-cli) emit completion or
			// turn.result naturally. Status≠DONE here is the RUNNING
			// streamer placeholder — don't terminate the turn on a partial.
			if strings.EqualFold(ln.Status, "DONE") {
				out = append(out, MappedEvent{
					Kind:     "turn.result",
					Producer: "agent",
					Payload: base(map[string]any{
						"reason": "end_of_turn",
					}),
				})
			}
			return out, nil
		}
		return nil, nil // empty planner turn (e.g. RUNNING placeholder)

	case "SYSTEM_MESSAGE":
		return []MappedEvent{{
			Kind:     "system",
			Producer: "system",
			Payload: base(map[string]any{
				"subtype": "agy_system_message",
				"text":    ln.Content,
				"src":     ln.Source,
			}),
		}}, nil

	case "INVOKE_SUBAGENT":
		// MVP ignores engine-native subagents (ADR-035 D-subagents): we
		// surface the dispatch as a tool_call so the operator sees it,
		// but do not spawn/track the child conversation.
		return []MappedEvent{{
			Kind:     "tool_call",
			Producer: "agent",
			Payload: base(map[string]any{
				"name":  "invoke_subagent",
				"input": map[string]any{"detail": ln.Content},
			}),
		}}, nil

	default:
		if _, isNonTool := nonToolTypes[ln.Type]; !isNonTool && strings.TrimSpace(ln.Content) != "" {
			// agy sets status=ERROR on tool failures (host-verified W11
			// smoke: MCP `client is closing` and `Permission denied for
			// read_file` both produced tool_results with agy_status=ERROR
			// but the mapper was hard-coding is_error=false, hiding the
			// failure from mobile's tool_result card which renders errors
			// in red and folds them into the parent tool_call). The mapper
			// now propagates: status=ERROR → is_error=true; everything
			// else → is_error=false.
			return []MappedEvent{{
				Kind:     "tool_result",
				Producer: "agent",
				Payload: base(map[string]any{
					"name":     strings.ToLower(ln.Type),
					"content":  ln.Content,
					"is_error": strings.EqualFold(ln.Status, "ERROR"),
				}),
			}}, nil
		}
		// Unknown shape with nothing to render — surface drift, never
		// silently drop (mirrors the claude-code mapper's §9 policy).
		return []MappedEvent{{
			Kind:     "system",
			Producer: "system",
			Payload: base(map[string]any{
				"subtype": "unknown_type",
				"type":    ln.Type,
			}),
		}}, nil
	}
}

// syntheticCallID derives a stable id for a planner tool_call so mobile's
// tool_call card has a key. agy emits no native call id at this layer;
// the format is agy-<step>-<idx>.
func syntheticCallID(step, idx int) string {
	return "agy-" + strconv.Itoa(step) + "-" + strconv.Itoa(idx)
}

// extractAntigravityModel parses the model name out of agy's
// <USER_SETTINGS_CHANGE> block, which lands on USER_INPUT step 0 and
// reads roughly:
//
//	<USER_SETTINGS_CHANGE>
//	The user changed setting `Model Selection` from None to Gemini 3.5 Flash (Medium). …
//	</USER_SETTINGS_CHANGE>
//
// The "X to Y" sentence is host-verified. Returns the captured model
// name with any trailing period / whitespace trimmed; empty when the
// content doesn't carry the block (e.g. resumed sessions where the
// setting hasn't been touched, or any non-step-0 USER_INPUT).
func extractAntigravityModel(content string) string {
	if !strings.Contains(content, "<USER_SETTINGS_CHANGE>") {
		return ""
	}
	if !strings.Contains(content, "Model Selection") {
		return ""
	}
	// Slice off everything before "Model Selection" + " to "; the
	// model name is what follows, terminated by "." or "\n".
	const marker = "Model Selection` from "
	i := strings.Index(content, marker)
	if i < 0 {
		// Fallback for a slightly different agy phrasing without the
		// backtick. Cheap defense against minor wording drift.
		i = strings.Index(content, "Model Selection from ")
		if i < 0 {
			return ""
		}
		i += len("Model Selection from ")
	} else {
		i += len(marker)
	}
	rest := content[i:]
	// "from <prev> to <model>." — skip past " to ".
	j := strings.Index(rest, " to ")
	if j < 0 {
		return ""
	}
	rest = rest[j+len(" to "):]
	// Terminate at the first newline; everything up to it is the
	// "<model>. <trailing-sentence>" run. Splitting on "." would slice
	// "Gemini 3.5 Flash" mid-name on the "3.5" decimal.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	// Drop the trailing sentence(s) — agy follows the model with
	// ". <hint about reporting>" on a single line in the host-verified
	// samples. The model name + parenthesized tier is the first
	// sentence; anything after ". " is editorial.
	if idx := strings.Index(rest, ". "); idx >= 0 {
		rest = rest[:idx]
	}
	return strings.TrimSpace(strings.TrimRight(rest, "."))
}

type mapError struct {
	what string
	err  error
}

func (e *mapError) Error() string { return "antigravity mapper: " + e.what + ": " + e.err.Error() }
func (e *mapError) Unwrap() error { return e.err }
