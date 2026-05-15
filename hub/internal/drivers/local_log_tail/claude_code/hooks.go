package claudecode

import (
	"context"
)

// dispatchHook is called by Adapter.OnHook (W2e+f wiring lives in
// adapter.go). It owns the per-event branch: derive any AgentEvent
// the hook implies, drive the FSM transition, and return the JSON-RPC
// response body claude-code expects.
//
// Plan §5.B routing table — every row is implemented here:
//
//	Stop                        → FSM → idle ; emit system{turn_complete, final_message}
//	Notification{idle_prompt}   → FSM → idle ; emit system{awaiting_input}
//	Notification{permission_*}  → drop (approval channel owns)
//	PreToolUse(other)           → FSM → streaming ; (drop event — JSONL has tool_use)
//	PreToolUse(AskUserQuestion) → FSM → awaiting_decision ; emit approval_request{user_question}; W2i parks
//	PostToolUse                 → drop (JSONL has tool_result)
//	PreCompact                  → FSM → awaiting_decision ; emit approval_request{compaction}; W2i parks
//	SubagentStop (agent_type≠"") → emit system{subagent_complete}
//	SubagentStop (agent_type=="") → drop (parent-turn duplicate)
//	UserPromptSubmit            → drop (JSONL has it)
//	SessionStart                → emit system{session_start, source, model}
//	SessionEnd                  → emit system{session_end, reason}
//
// W2e implements all rows; the parked branches (PreCompact + AskUser-
// Question) currently return the safe default response ({} / {})
// without blocking — W2i swaps in the real attention_items + /decide
// coordination.
func (a *Adapter) dispatchHook(ctx context.Context, name string, payload map[string]any) (map[string]any, error) {
	switch name {
	case "Stop":
		return a.hookStop(ctx, payload)
	case "Notification":
		return a.hookNotification(ctx, payload)
	case "PreToolUse":
		return a.hookPreToolUse(ctx, payload)
	case "PostToolUse":
		return a.hookPostToolUse(ctx, payload)
	case "PreCompact":
		return a.hookPreCompact(ctx, payload)
	case "SubagentStop":
		return a.hookSubagentStop(ctx, payload)
	case "UserPromptSubmit":
		return a.hookUserPromptSubmit(ctx, payload)
	case "SessionStart":
		return a.hookSessionStart(ctx, payload)
	case "SessionEnd":
		return a.hookSessionEnd(ctx, payload)
	default:
		// Unknown hook event — log + return empty so claude isn't
		// blocked by our ignorance.
		a.Log.Debug("claude-code adapter: unknown hook event",
			"agent_id", a.AgentID, "name", name)
		return map[string]any{}, nil
	}
}

// --- 7 observational handlers ---

func (a *Adapter) hookStop(ctx context.Context, p map[string]any) (map[string]any, error) {
	if a.fsm != nil {
		a.fsm.Transition(StateIdle, "Stop hook")
	}
	final, _ := p["last_assistant_message"].(string)
	mode, _ := p["permission_mode"].(string)
	_ = a.post(ctx, "system", "system", map[string]any{
		"subtype":         "turn_complete",
		"final_message":   final,
		"permission_mode": mode,
	})
	return map[string]any{}, nil
}

func (a *Adapter) hookNotification(ctx context.Context, p map[string]any) (map[string]any, error) {
	nt, _ := p["notification_type"].(string)
	switch nt {
	case "idle_prompt":
		if a.fsm != nil {
			a.fsm.Transition(StateIdle, "Notification idle_prompt")
		}
		_ = a.post(ctx, "system", "system", map[string]any{
			"subtype": "awaiting_input",
		})
	case "permission_prompt":
		// Approval channel (--permission-prompt-tool) owns this UX;
		// the hook is informational only and would duplicate the
		// approval card. Drop.
	default:
		// Surface drift as a muted info card; never silently swallow.
		_ = a.post(ctx, "system", "system", map[string]any{
			"subtype":           "unknown_notification",
			"notification_type": nt,
			"message":           p["message"],
		})
	}
	return map[string]any{}, nil
}

func (a *Adapter) hookPreToolUse(ctx context.Context, p map[string]any) (map[string]any, error) {
	tool, _ := p["tool_name"].(string)
	if tool == "AskUserQuestion" {
		// W2e stub for parking — return {} immediately to allow the
		// picker to render. W2i replaces this with: emit
		// approval_request{user_question}, park on attention_items,
		// return after mobile decides + send-keys nav fires.
		if a.fsm != nil {
			a.fsm.Transition(StateAwaitingDecision, "PreToolUse(AskUserQuestion)")
		}
		_ = a.post(ctx, "approval_request", "agent", map[string]any{
			"dialog_type": "user_question",
			"questions":   p["tool_input"],
			"tool_use_id": p["tool_use_id"],
		})
		return map[string]any{}, nil
	}
	if a.fsm != nil {
		a.fsm.Transition(StateStreaming, "PreToolUse")
	}
	return map[string]any{}, nil
}

func (a *Adapter) hookPostToolUse(_ context.Context, _ map[string]any) (map[string]any, error) {
	// JSONL has the tool_result; don't duplicate as an event.
	return map[string]any{}, nil
}

func (a *Adapter) hookPreCompact(ctx context.Context, p map[string]any) (map[string]any, error) {
	if a.fsm != nil {
		a.fsm.Transition(StateAwaitingDecision, "PreCompact hook")
	}
	trigger, _ := p["trigger"].(string)
	custom, _ := p["custom_instructions"].(string)
	_ = a.post(ctx, "approval_request", "agent", map[string]any{
		"dialog_type":         "compaction",
		"trigger":             trigger,
		"custom_instructions": custom,
		"options":             []string{"compact", "defer"},
	})
	// W2e stub: return {} (allow compaction) immediately. W2i swaps
	// this for the real attention_items + /decide block-or-allow.
	return map[string]any{}, nil
}

func (a *Adapter) hookSubagentStop(ctx context.Context, p map[string]any) (map[string]any, error) {
	at, _ := p["agent_type"].(string)
	if at == "" {
		// SubagentStop fires twice per Task call: once with
		// agent_type set (the real subagent), once at parent turn
		// end with empty agent_type. The latter is a duplicate of
		// Stop in practice — drop to keep transcripts clean.
		return map[string]any{}, nil
	}
	aid, _ := p["agent_id"].(string)
	final, _ := p["last_assistant_message"].(string)
	tp, _ := p["agent_transcript_path"].(string)
	_ = a.post(ctx, "system", "system", map[string]any{
		"subtype":               "subagent_complete",
		"agent_id":              aid,
		"agent_type":            at,
		"last_assistant_message": final,
		"agent_transcript_path": tp,
	})
	return map[string]any{}, nil
}

func (a *Adapter) hookUserPromptSubmit(_ context.Context, _ map[string]any) (map[string]any, error) {
	// JSONL records the user prompt as a `user.message.content`
	// string within a beat; the hook is purely informational.
	return map[string]any{}, nil
}

func (a *Adapter) hookSessionStart(ctx context.Context, p map[string]any) (map[string]any, error) {
	src, _ := p["source"].(string)
	model, _ := p["model"].(string)
	_ = a.post(ctx, "system", "system", map[string]any{
		"subtype": "session_start",
		"source":  src,
		"model":   model,
	})
	return map[string]any{}, nil
}

func (a *Adapter) hookSessionEnd(ctx context.Context, p map[string]any) (map[string]any, error) {
	reason, _ := p["reason"].(string)
	_ = a.post(ctx, "system", "system", map[string]any{
		"subtype": "session_end",
		"reason":  reason,
	})
	return map[string]any{}, nil
}

// post is a tiny convenience wrapper that lets the handlers ignore
// Poster errors uniformly (W2d's runLoop log + drop pattern). Errors
// here are also debug-logged; a missing event is annoying but not
// fatal — the next event arrives fine.
func (a *Adapter) post(ctx context.Context, kind, producer string, payload map[string]any) error {
	if err := a.Poster.PostAgentEvent(ctx, a.AgentID, kind, producer, payload); err != nil {
		a.Log.Debug("claude-code adapter: post failed",
			"agent_id", a.AgentID, "kind", kind, "err", err)
		return err
	}
	return nil
}
