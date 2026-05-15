# Claude-code hook schema (v2.1.129)

> **Type:** reference
> **Status:** Current (2026-05-15) — derived from on-device probe corpus `hub/cmd/probe-claude-hooks/` against claude-code 2.1.129
> **Audience:** contributors implementing the `LocalLogTailDriver` hook handlers (ADR-027 D-amend-1)
> **Last verified vs code:** claude-code 2.1.129

**TL;DR.** Authoritative schema reference for the 9 hook events
claude-code 2.1.129 emits, derived from empirical probe payloads
(not vendor docs — none published). Each section gives: when the
hook fires, payload keys + observed types + values, decision-return
contract, and notes on routing semantics for the M4 driver. Code
in `hub/internal/hostrunner/mcp_gateway.go` hook tool handlers
references this file by section anchors.

This is a *living* schema. When a claude-code release adds fields or
hooks, re-run the probe and update this doc in the same PR.

---

## How to use this doc

- **Implementing a hook handler.** Look up the hook section, copy
  the Go struct schema, route on the discriminator(s) noted in the
  "Routing semantics" subsection. Unknown fields are silently
  ignored (forward-compat); unknown enum values degrade to
  `system{subtype:"unknown_<event>"}`.
- **Adding test fixtures.** Each section's example payload is a
  real captured probe artefact, minimally redacted (`session_id`,
  `transcript_path`, `cwd`). Reuse verbatim as a unit-test golden.
- **Schema-drift checks.** Run `bash hub/cmd/probe-claude-hooks/hook-probe.sh`
  against the current installed claude-code; diff the new corpus
  against this doc.

---

## Common fields (every hook)

| Key | Type | Notes |
|---|---|---|
| `hook_event_name` | string | The hook event name, e.g. `"PreToolUse"`. Authoritative discriminator. |
| `session_id` | string (UUID) | claude-code session id. Matches the JSONL filename under `~/.claude/projects/<urlencoded-cwd>/`. |
| `transcript_path` | string (absolute path) | Path to the JSONL session log. Adapter can correlate hook events to JSONL entries by this. |
| `cwd` | string (absolute path) | Working directory at hook fire time. |

The probe script appends two metadata fields (`_event`, `_ts`) that
are NOT part of the claude-code payload — strip when consuming.

---

## PreToolUse

**Fires:** before the permission engine decides whether to allow,
prompt-for, or auto-allow a tool call. Fires for **every** tool
call regardless of permission mode (confirmed in
`--dangerously-skip-permissions` mode).

### Payload

| Key | Type | Notes |
|---|---|---|
| `tool_name` | string | The tool's wire name (`"Bash"`, `"Edit"`, `"Read"`, `"Write"`, `"Agent"` for Task, `"ExitPlanMode"`, etc.). |
| `tool_input` | object | The tool's structured input. **Shape varies per tool.** See [§Tool-input shapes](#tool-input-shapes) below for the load-bearing tools. |
| `tool_use_id` | string | Stable id correlating to JSONL's `tool_use` content block. Use this to pair PreToolUse with the eventual PostToolUse. |
| `permission_mode` | string | One of `"default"`, `"acceptEdits"`, `"bypassPermissions"`, `"plan"`. Authoritatively reflects Shift+Tab cycling. |
| `effort` | object | `{level: "low"\|"med"\|"high"\|"max"}` — model effort tier. Cosmetic for the adapter. |

### Decision return

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow" | "deny" | "ask",
    "permissionDecisionReason": "<optional human-readable string>",
    "updatedInput": { ...optional mutated tool_input... }
  }
}
```

Returning empty `{}` leaves the permission engine to decide
naturally. Returning `"ask"` forces the TUI permission prompt to
appear (useful as a fallback when the adapter wants the user at the
keyboard to decide instead of the mobile user).

### Routing semantics

- For `tool_name == "ExitPlanMode"`: **park** the hook call,
  emit `approval_request{dialog_type:"plan_approval", body: tool_input.plan, options:["approve","edit","comment"]}`, await mobile decision, then return `permissionDecision`. This is the load-bearing path.
- For other `tool_name` values: store `tool_input` in a per-session map keyed by `tool_use_id`, return `{}` immediately so the permission engine proceeds. If a `Notification{notification_type:"permission_prompt"}` follows, retrieve the stored `tool_input` to populate the approval card.

### Example payload (real probe artefact, redacted)

```json
{
  "session_id": "71d311d1-7e92-450b-a9c2-a524e442e199",
  "transcript_path": "/home/wb/.claude/projects/-home-wb-cc-hook-probe-workdir/71d311d1.jsonl",
  "cwd": "/home/wb/cc-hook-probe-workdir",
  "hook_event_name": "PreToolUse",
  "tool_name": "ExitPlanMode",
  "tool_input": {
    "plan": "# Plan: Rename `foo` to `bar` in test.txt\n\n## Context\n\nThe file `test.txt` contains a single test string where the word `foo` appears at the start of line 1. ...",
    "planFilePath": "/home/wb/.claude/plans/plan-a-refactor-that-enchanted-curry.md"
  },
  "tool_use_id": "toolu_01...",
  "permission_mode": "plan",
  "effort": { "level": "max" }
}
```

---

## PostToolUse

**Fires:** after a tool call completes (either successfully or with
an error). Mirrors PreToolUse with the result attached.

### Payload

| Key | Type | Notes |
|---|---|---|
| `tool_name` | string | Same as PreToolUse. |
| `tool_input` | object | Echo of the original input. |
| `tool_use_id` | string | Pairs with the PreToolUse `tool_use_id`. |
| `tool_response` | object \| string | The tool's result. **Shape varies per tool.** Read tool: `{type:"text", file:{filePath, content}}`. Bash: `{type:"text", text:"<stdout>", stderr:"<...>", exitCode}`. ExitPlanMode: `{plan, isAgent, filePath}`. |
| `duration_ms` | number | Wall-clock duration of the tool call. |
| `permission_mode` | string | Current mode. |
| `effort` | object | As in PreToolUse. |

### Decision return

```json
{ "decision": "block", "reason": "<feeds back to model>" }
```

Returning `"block"` injects the reason into the model's next turn
input — useful for post-hoc validation gates. Empty `{}` lets the
flow proceed normally.

### Routing semantics

- Informational. Closes the stored `tool_input` map entry for this `tool_use_id`.
- If parked plan_approval for this `tool_use_id` is still open (mobile didn't resolve), unblock with `permissionDecision:"allow"` (the user must have approved in TUI directly, and PostToolUse only fires after that).

---

## Notification

**Fires:** when claude-code surfaces a UI notification — idle wait,
permission prompt, or other attention signal.

### Payload

| Key | Type | Notes |
|---|---|---|
| `message` | string | Free-form display text, e.g. `"Claude is waiting for your input"` or `"Claude Code needs your approval for the plan"`. |
| `notification_type` | string | **Structured categorical discriminator.** Observed values: `"idle_prompt"`, `"permission_prompt"`. **Route on this, not on `message`.** |

### Decision return

None — Notification is observation-only. Return `{}`.

### Routing semantics

| `notification_type` | Action |
|---|---|
| `idle_prompt` | Emit `system{subtype:"awaiting_input", message}`. Mobile clears streaming pill + focuses compose box. |
| `permission_prompt` | Retrieve the most-recent unresolved PreToolUse for this session (by `tool_use_id`). If it's `ExitPlanMode`, the plan_approval parking is already active — no-op. Otherwise, emit `approval_request{dialog_type:"tool_permission", tool:<tool_name>, body:<tool_input>, options:["allow","deny"]}` and route the mobile decision back through whatever the active permission channel is (MCP-tool path if `--permission-prompt-tool` is configured; otherwise this is an alert without a closing-loop mechanism — log + display). |
| unknown | Emit `system{subtype:"unknown_notification", notification_type, message}`. Mobile renders muted info card. |

### Example payloads

```json
{ "hook_event_name": "Notification",
  "message": "Claude is waiting for your input",
  "notification_type": "idle_prompt" }

{ "hook_event_name": "Notification",
  "message": "Claude Code needs your approval for the plan",
  "notification_type": "permission_prompt" }

{ "hook_event_name": "Notification",
  "message": "Claude needs your permission to use Write",
  "notification_type": "permission_prompt" }
```

---

## UserPromptSubmit

**Fires:** when the user submits a prompt (typed in TUI or via
`send-keys`).

### Payload

| Key | Type | Notes |
|---|---|---|
| `prompt` | string | The full user prompt text. |
| `permission_mode` | string | Current mode at submit time. |

### Decision return

```json
{ "decision": "block", "reason": "<not processed>", "additionalContext": "<injected before prompt>" }
```

`"block"` rejects the prompt; `additionalContext` prepends to the
prompt for the model. Adapter normally returns `{}`.

### Routing semantics

Informational — JSONL records the prompt as a `user.message.content` string shortly after. Use this hook only for "user just submitted" UX cues (e.g. flush mobile input optimistic state).

---

## Stop

**Fires:** when the parent claude turn finishes — the agent has
emitted its final message and is now idle awaiting the next user
input.

### Payload

| Key | Type | Notes |
|---|---|---|
| `last_assistant_message` | string | The final assistant message text of the turn. **Authoritative** for parent-turn-end final-text. |
| `permission_mode` | string | Current mode. |
| `stop_hook_active` | boolean | `true` if this hook is currently parking the agent (used to prevent recursion). |
| `effort` | object | As elsewhere. |

### Decision return

```json
{ "decision": "block", "reason": "<continues the turn>" }
```

Force-continue path: returning `"block"` makes claude resume the
turn with the reason as additional context. Useful for "keep going"
loops. Adapter normally returns `{}`.

### Routing semantics

- Emit `system{subtype:"turn_complete", final_message: last_assistant_message}`. Mobile clears streaming pill.
- Don't rely on this alone for idle detection — pair with `Notification{idle_prompt}` which usually follows.

---

## SubagentStop

**Fires:** when a Task() subagent finishes **AND** at parent turn
end (the latter being a duplicate that the adapter must filter).

### Payload

| Key | Type | Notes |
|---|---|---|
| `agent_id` | string | The (sub)agent's id. |
| `agent_type` | string | **The discriminator.** Non-empty (e.g. `"Explore"`, `"general-purpose"`) → real Task subagent. Empty (`""`) → parent turn end duplicate; **drop**. |
| `last_assistant_message` | string | Subagent's final response (when `agent_type != ""`). For parent-turn duplicates this field is unreliable (sometimes contains a user prompt) — ignore. |
| `agent_transcript_path` | string | Subagent-specific JSONL file (under `~/.claude/projects/<urlencoded-cwd>/<parent-session-id>/`). |
| `permission_mode`, `effort`, `stop_hook_active` | … | As elsewhere. |

### Decision return

Same as Stop: `{"decision":"block","reason":"..."}` to force-continue. Normally `{}`.

### Routing semantics

```
if agent_type == "":
    drop  # parent-turn duplicate of Stop
else:
    emit system{subtype:"subagent_complete",
                agent_id, agent_type,
                final_message: last_assistant_message}
```

---

## PreCompact

**Fires:** before context compaction starts — either user-initiated
(`/compact`) or auto-triggered (context-full).

### Payload

| Key | Type | Notes |
|---|---|---|
| `trigger` | string | `"manual"` or `"auto"`. |
| `custom_instructions` | string \| null | User-provided compaction instructions if any. |

### Decision return

```json
{ "decision": "block", "reason": "..." }
```

`"block"` defers compaction. Empty `{}` proceeds.

### Routing semantics

- **Park** the hook, emit `approval_request{dialog_type:"compaction", trigger, options:["compact","defer"]}`.
- Mobile tap → unblock with `{}` (compact) or `{"decision":"block"}` (defer).

---

## SessionStart

**Fires:** when a claude session begins — fresh start or via
`claude --resume`.

### Payload

| Key | Type | Notes |
|---|---|---|
| `source` | string | Observed: `"startup"`. Docs mention `"resume"`, `"clear"`; not yet observed (probe v2 follow-up). |
| `model` | string | The configured model name, e.g. `"deepseek-v4-pro[1m]"`, `"claude-opus-4-7"`. |

### Decision return

```json
{ "hookSpecificOutput": { "additionalContext": "<text>" } }
```

Adds context to the system prompt at session start. Adapter
normally returns `{}`.

### Routing semantics

Emit `system{subtype:"session_start", source, model}`.

---

## SessionEnd

**Fires:** when the claude session terminates.

### Payload

| Key | Type | Notes |
|---|---|---|
| `reason` | string | Termination reason. (Not yet observed in probe corpus — `/exit` and Ctrl+D both bypass the hook in v2.1.129; this section needs probe v2.) |

### Decision return

None — observation only. Return `{}`.

### Routing semantics

Emit `system{subtype:"session_end", reason}`. Used for adapter
teardown.

---

## Tool-input shapes

The `tool_input` field is per-tool. Load-bearing examples from the
probe corpus:

### ExitPlanMode

```json
{
  "plan": "# Plan: ...\n\n## Context\n\n... (markdown body)",
  "planFilePath": "/home/wb/.claude/plans/plan-<slug>.md"
}
```

### Bash

```json
{
  "command": "ls /tmp",
  "description": "List /tmp contents",
  "run_in_background": false
}
```

### Read

```json
{
  "file_path": "/home/wb/.bashrc",
  "limit": 100,
  "offset": 0
}
```

### Write

```json
{
  "file_path": "/home/wb/test.txt",
  "content": "..."
}
```

### Edit

```json
{
  "file_path": "/home/wb/test.txt",
  "old_string": "foo",
  "new_string": "bar",
  "replace_all": false
}
```

### Agent (Task)

```json
{
  "description": "Short task description",
  "prompt": "Full instructions for the subagent...",
  "subagent_type": "Explore"
}
```

Note: claude-code's wire name for the Task tool is `"Agent"`. The
`subagent_type` field maps to `SubagentStop.agent_type` on
completion.

---

## Tool-response shapes (PostToolUse)

### Read

```json
{
  "type": "text",
  "file": {
    "filePath": "/home/wb/.bashrc",
    "content": "<file contents>",
    "numLines": 138
  }
}
```

### Bash

```json
{
  "type": "text",
  "text": "<stdout>",
  "stderr": "",
  "exitCode": 0,
  "interrupted": false
}
```

(Probe corpus didn't capture every tool's response shape; extend
this section as new shapes are observed.)

### ExitPlanMode

```json
{
  "plan": "<echo of the plan>",
  "isAgent": false,
  "filePath": "/home/wb/.claude/plans/plan-<slug>.md"
}
```

---

## What this doc does NOT cover

- The **JSONL transcript schema** — that's `hub/cmd/probe-claude-jsonl/main.go`'s mapping table, validated separately. The hook surface and the JSONL transcript are **complementary** (see ADR-027 D-amend-1 + plan §5): hooks carry the TUI-interactive state; JSONL carries the streaming transcript content (text/thinking blocks between tool calls).
- **Hook events claude-code docs mention but the 2.1.129 binary doesn't emit**: `WorktreeCreate`, `Elicitation`, `PermissionRequest`, `PermissionDenied`, `Setup`. Treat as aspirational; do not depend on them.
- **`mcp_tool`-type hook transport details** — that lives in the host-runner side, not the schema. See `hub/internal/hostrunner/mcp_gateway.go` for the existing parking-MCP pattern this driver's hook handlers will reuse.

---

## References

- [decisions/027-local-log-tail-driver.md](../decisions/027-local-log-tail-driver.md) — ADR + D-amend-1 hook surface
- [plans/local-log-tail-claude-code-adapter.md](../plans/local-log-tail-claude-code-adapter.md) — implementation contract (§5 hook event surface)
- [discussions/local-log-tail-m4-replacement.md](../discussions/local-log-tail-m4-replacement.md) — design rationale + probe analysis
- `hub/cmd/probe-claude-hooks/` — probe script + test plan + this doc's source of truth
- claude-code public hook docs (formatting hints): `code.claude.com/docs/en/hooks`
