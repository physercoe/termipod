# LocalLogTailDriver — claude-code adapter

> **Type:** plan
> **Status:** Draft (2026-05-15) — spec frozen; hook surface empirically validated against claude-code 2.1.129; implementation not started; ADR-027 amended same day
> **Audience:** contributors
> **Last verified vs code:** claude-code 2.1.129, JSONL 200k-line live sample + 9-hook payload corpus on 2026-05-15

**TL;DR.** Replace the current "agent fallback M4" raw-PTY+xterm-VT
path with a hub-side driver that **tails claude-code's on-disk
session JSONL for transcript content** AND **installs hooks via the
existing host-runner MCP gateway for TUI-interactive state events**
(plan-mode approval, compaction, idle, turn-end, subagent-stop).
Mobile input flows back via `tmux send-keys` for free-text + cancel
+ escape only; approve/deny decisions ride hook-return values so
they reach claude-code's permission engine directly without
keystroke synthesis. The driver emits `AgentEvent` shapes identical
to M1/M2; mobile surfaces (cards, approval prompt, compose box,
action bar, snippet bar) are unchanged. MVP = claude-code only;
gemini / codex / kimi are Phase 2/3 with adapter implementations
against their own log paths.

**Capture-pane is not in the implementation.** Empirical validation
(probe 2026-05-15) established that claude-code's hook surface
provides every TUI-interactive signal the adapter needs as
structured payloads, with categorical discriminators
(`Notification.notification_type`, `PreToolUse.tool_name`,
`PreCompact.trigger`). The pre-amendment design's regex-driven
capture-pane probe is removed entirely.

The plain-SSH terminal viewer in `lib/services/terminal/` and
`raw_pty_backend.dart` is independent of this swap and stays
untouched.

---

## 1. Vocabulary

- **JSONL session log** — claude-code's append-only transcript file
  at `~/.claude/projects/<urlencoded-cwd>/<session-uuid>.jsonl`.
  Line-buffered; one JSON event per line; written live during the
  session; never compacted or rotated. Schema is observed-not-spec.
- **Turn** — events between two consecutive user-typed messages.
  The opening `user.message.content` is a JSON string (typed
  prompt); subsequent `user.message.content` arrays contain
  `tool_result` blocks belonging to the same turn.
- **Permission rule** — a pattern in `.permissions.allow` (in
  `~/.claude/settings.json` and `<cwd>/.claude/settings.local.json`)
  that auto-allows matching tool_uses without prompting. Example:
  `Bash(git push *)`.
- **Approval prompt** — claude-code's in-TUI numbered list rendered
  when a tool_use does not match any allow rule. Three rows: row 1
  `Yes`, row 2 `Yes, and don't ask again for <pattern>` (or
  `Yes, allow all edits during this session` for Edit/Write), row 3
  `No, and tell Claude what to do differently`. Selected via arrow
  navigation + Enter, NOT digit keys.
- **AgentEvent** — the hub→mobile event shape already emitted by
  M1 (ACP) and M2 (stream-json) drivers. Reused unchanged.

---

## 2. What it replaces

| Today | After |
|---|---|
| Agent-mode M4 = xterm-VT screen-replay over SSH PTY | Agent-mode M4 = JSONL tail + tmux send-keys, for claude-code only |
| `raw_pty_backend.dart` used for both agent-fallback and plain SSH viewing | `raw_pty_backend.dart` used **only** for plain SSH viewing (unchanged) |
| Mobile renders a text dump of the alt-screen | Mobile renders the same cards M1/M2 produce |

Other engines stay on whatever M4 binding they have today until their
adapters ship.

---

## 3. Output half — JSONL → AgentEvent

### 3.1 Top-level event filter

| JSONL `type` | Action |
|---|---|
| `assistant` | Map each `message.content[]` block (see 3.2) |
| `user` | Map by `message.content` shape (see 3.3) |
| `system` | Emit if `subtype ∈ {compact_boundary}`; drop otherwise |
| `attachment` | Emit `attachment` AgentEvent |
| `permission-mode` | Drop (per-session metadata) |
| `custom-title`, `agent-name` | Apply to session header once; do not emit per occurrence |
| `last-prompt`, `file-history-snapshot`, `queue-operation` | Drop (internal bookkeeping) |
| Anything else | Treat as schema drift — emit a `system` event with `subtype=unknown_type` and the raw type name; fall back to xterm-VT path is **not** triggered (per "schema-drift policy" in §9) |

### 3.2 Assistant content blocks

| Block `type` | AgentEvent emitted | Payload |
|---|---|---|
| `text` | `text` | `{text}` — no streaming-partial collapse; emit as-is |
| `thinking` | `thought` | `{text:"Thinking…", marker_only:true, signature_present:bool}` — `.thinking` is empty on 2.1.x (signed for API verification); body is a fixed marker |
| `tool_use` | `tool_call` | `{tool_use_id:id, name, input}` — input is passed through as-is |

### 3.3 User content shape branch

`user.message.content` is heterogeneous:

| Shape | Meaning | AgentEvent |
|---|---|---|
| JSON string | User-typed prompt — opens a new turn | `user_input` with `{text}` |
| JSON array of `tool_result` blocks | Tool returns for prior tool_uses | One `tool_result` per block: `{tool_use_id, is_error, content, denied:bool}` |

`tool_result.content` is also heterogeneous — either a plain string
or an array of `{type:"text", text:"…"}` blocks. Adapter normalizes
to a single string for transport; mobile renderer handles the
existing AgentEvent shape unchanged.

The `denied` flag is set when `content` begins with `<tool_use_error>`
(empirically observed denial marker). Mobile renderer can stamp
denials red without inspecting content.

---

## 4. State machine

```
              (Stop hook fires; or Notification{notification_type:"idle_prompt"})
                                 │
                                 ▼
                          ┌─────────────┐
              ┌──────────►│    idle     │◄────────────────┐
              │           └─────────────┘                  │
              │ Stop hook fires                            │ JSONL tool_use lands
              │ (or idle_prompt Notification)              │ OR PreToolUse hook fires
              │                                            │
              │                                            ▼
              │                                ┌─────────────────────┐
              │                                │      streaming      │
              │                                │ (JSONL text/thought │
              │                                │  + PreToolUse hooks │
              │                                │  flowing)           │
              │                                └─────────────────────┘
              │                                            │
              │ approval card resolved on mobile          │ Notification fires with
              │  → hook return unblocks claude            │  notification_type =
              │                                            │  "permission_prompt"
              │                                            │ OR PreToolUse(ExitPlanMode)
              │                                            │ OR PreCompact
              │                                            ▼
              │                                ┌─────────────────────┐
              │                                │ awaiting_decision   │
              └────────────────────────────────│ (parked MCP hook    │
                                               │  call waits for     │
                                               │  mobile resolution) │
                                               └─────────────────────┘

  Pane lost (PID gone, tmux pane closed) → emit system error AgentEvent.
```

### 4.1 idle_threshold knob

- **Default: 2000 ms** of no events as a safety fallback.
- Empirical: `Stop` hook + `Notification{idle_prompt}` are the
  authoritative idle signals (probe 2026-05-15 confirmed both fire
  reliably at parent turn end). The timeout is only a backstop for
  the rare case both hooks fail to fire.

### 4.2 No grace timer, no capture-pane

Removed in the empirically-validated design. Approval moments are
signalled directly by the hook surface (§5) — `Notification{
notification_type:"permission_prompt"}` is the canonical
"awaiting-decision" signal. There is no polling, no grace window,
no regex parse of the terminal screen.

---

## 5. Hook event surface (the primary structured signal)

Empirically validated against claude-code 2.1.129 (probe 2026-05-15).
Hook delivery uses `type:"mcp_tool"` routing through the existing
per-spawn host-runner UDS MCP gateway — same transport as
`mcp__termipod__permission_prompt`, not raw HTTP to hub. Host-runner
inserts the spawn id from the per-agent UDS, forwards events to hub
via the existing `agent_event_post` REST channel.

### 5.1 Installed hooks (9 events) + AgentEvent mappings

| Hook | Notable payload fields | AgentEvent emission |
|---|---|---|
| **PreToolUse** | `tool_name, tool_input, tool_use_id` | (informational; JSONL also has it). For `tool_name=="ExitPlanMode"` → emit `approval_request{dialog_type:"plan_approval", body: tool_input.plan, options:["approve","edit","comment"]}` and **park the hook call** waiting for mobile decision |
| **PostToolUse** | `tool_name, tool_input, tool_response` | (informational; closes the parked plan_approval if user approved) |
| **Notification** | `message, notification_type` | `notification_type:"idle_prompt"` → `system{subtype:"awaiting_input"}` (clears streaming pill). `notification_type:"permission_prompt"` → either covered by PreToolUse(ExitPlanMode) park above, or for direct tool permission → `approval_request{dialog_type:"tool_permission", tool: last PreToolUse.tool_name, body: last PreToolUse.tool_input, options:["allow","deny"]}` |
| **PreCompact** | `trigger:"manual"\|"auto", custom_instructions` | `approval_request{dialog_type:"compaction", trigger, options:["compact","defer"]}`. **Park** the hook waiting for mobile decision; return `{"decision":"block"}` to defer |
| **Stop** | `last_assistant_message, permission_mode, effort` | `system{subtype:"turn_complete", final_message: last_assistant_message}` (canonical parent-turn-end signal) |
| **SubagentStop** | `agent_id, agent_type, last_assistant_message, agent_transcript_path` | If `agent_type != ""` → `system{subtype:"subagent_complete", agent_id, agent_type, final_message: last_assistant_message}`. If `agent_type == ""` → **drop** (parent-turn duplicate of Stop) |
| **UserPromptSubmit** | `prompt, permission_mode` | (informational; JSONL records as `user.message.content` shortly after) |
| **SessionStart** | `source:"startup"\|"resume"\|"clear", model` | `system{subtype:"session_start", source, model}` |
| **SessionEnd** | `reason` | `system{subtype:"session_end", reason}` |

### 5.2 Empirically locked discriminators

The probe (2026-05-15) confirmed:

- **`Notification.notification_type`** is a structured categorical
  field (NOT a free-form message string requiring regex). Observed
  values: `idle_prompt`, `permission_prompt`. Routing is a 2-row
  lookup, not a regex library.
- **`PreToolUse(ExitPlanMode).tool_input.plan`** carries the full
  plan body (markdown, 600+ chars in observed sessions) + a
  `planFilePath` field. No JSONL cross-reference needed for plan
  content.
- **`PreToolUse` fires in bypass-permissions mode** — every tool
  call, regardless of permission gate state.
- **Plan-mode approval fires even with `--dangerously-skip-permissions`**
  (Q1 confirmed). Bypass-permissions doesn't suppress plan-mode's
  separate gate.
- **`permission_mode` field on every hook payload.** Mode changes
  (Shift+Tab cycling between default/acceptEdits/bypass/plan) are
  authoritatively reflected here; no need to parse JSONL
  `permission-mode` events for current-mode tracking.
- **`SubagentStop` fires at parent turn end with `agent_type:""`.**
  Distinct from real Task subagent terminations (which have
  `agent_type` set, e.g. "Explore"). Adapter must filter.

### 5.3 Mobile-resolution → hook-return contract

For hooks that park waiting for a mobile decision (`PreToolUse(ExitPlanMode)`, `PreCompact`, and the direct `permission_prompt` Notification path), the round-trip is:

1. Hook fires → host-runner MCP tool handler parks the MCP RPC.
2. Handler inserts `attention_items{kind:"plan_approval"|"compaction"|"tool_permission"}` and posts a corresponding `approval_request` AgentEvent.
3. Mobile renders the approval card with options from the dialog_type's table.
4. User taps → mobile resolves the attention via the existing approval-flow REST endpoint.
5. Host-runner unblocks the parked MCP call, returns the appropriate hook response:
   - `PreToolUse → {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"|"deny"}}`
   - `PreCompact → {"decision":"block"}` (defer) or `{}` (allow compaction)
   - `Notification` → not a parked hook (returns immediately); approval routes through the prior `PreToolUse` park instead

This is the same parking model `mcpPermissionPrompt` already uses;
no new infrastructure.

### 5.4 Spawn-time hook installation

Host-runner writes `<workdir>/.claude/settings.local.json` at spawn
time with the hooks block. Schema (template; `{{mcp_namespace}}`
resolves to the spawn's MCP namespace, identical to existing
`--permission-prompt-tool` usage):

```jsonc
{
  "permissions": { "defaultMode": "bypassPermissions" },
  "hooks": {
    "PreToolUse":     [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_pre_tool_use",   "timeout":30 }]}],
    "PostToolUse":    [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_post_tool_use",  "timeout":5  }]}],
    "Notification":   [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_notification",   "timeout":5  }]}],
    "PreCompact":     [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_pre_compact",    "timeout":300 }]}],
    "Stop":           [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_stop",           "timeout":5  }]}],
    "SubagentStop":   [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_subagent_stop",  "timeout":5  }]}],
    "UserPromptSubmit":[{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_user_prompt",   "timeout":5  }]}],
    "SessionStart":   [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_session_start",  "timeout":5  }]}],
    "SessionEnd":     [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_session_end",    "timeout":5  }]}]
  }
}
```

Timeout 30s on PreToolUse + 300s on PreCompact are headroom for
mobile-decision parks; the host-runner side enforces a real
deadline (configurable) and falls back to a default decision if
the user doesn't respond.

If the workdir already has a `settings.local.json` (user's
`.permissions.allow` rules etc.), host-runner **merges** the
hooks block in rather than overwrite — preserves user
configuration.

---

## 6. Input half — mobile action → tmux send-keys

The pane is identified by walking up from the `claude` PID
(`pstree -p <claude_pid>`) to the tmux pane that owns it.

`tmux send-keys` is **output-only direction** (mobile → CLI). Approve
/ deny / always-allow decisions for the dialog_types in §5 do NOT
route through here — they unblock parked hook calls (§5.3) so
claude-code receives the decision via its own permission engine.
The send-keys path covers only the surfaces hooks can't reach.

### 6.1 Action table

| Mobile action | tmux command |
|---|---|
| Compose box → text submit (short, no newlines) | `tmux send-keys -t <pane> -l "<text>"; tmux send-keys -t <pane> Enter` |
| Compose box → text submit (long or multi-line) | `tmux load-buffer -; tmux paste-buffer -t <pane>; tmux send-keys -t <pane> Enter` |
| Slash command from snippet bar | `tmux send-keys -t <pane> "/clear" Enter` (or similar) — same path as text submit |
| Cancel current turn (soft) | `tmux send-keys -t <pane> C-c` |
| Cancel current turn (hard fallback after 2 s) | `kill -INT <claude_pid>` |
| Escape modal / dismiss prompt | `tmux send-keys -t <pane> Escape` |
| Mode cycle (Shift+Tab) | `tmux send-keys -t <pane> S-Tab` — note: usually user-initiated in TUI; mobile equivalent is optional |
| Action bar → Up/Down/Tab/F-keys | `tmux send-keys -t <pane> <name>` |

The action table is **strictly smaller** than the pre-amendment
draft. No `Down Enter` / `Down Down Enter` rows for approvals; no
highlight-position arithmetic; no Y/N letter shortcuts. All of
those collapse to the parked-hook decision path.

---

## 7. (removed)

Section §7 in the pre-amendment draft was the capture-pane probe.
Removed entirely — the hook surface (§5) replaces every signal
capture-pane was designed to produce. Capture-pane is not in the
implementation.

---

## 8. MVP knobs (single source of truth)

| Knob | Default | Notes |
|---|---|---|
| `idle_threshold_ms` | 2000 | §4.1 — safety backstop; primary idle signal is `Stop` + `Notification{idle_prompt}` |
| `hook_park_default_ms` | 60_000 (1 min) for `PreToolUse` & `PreCompact`; 0 for non-decision hooks | Real parking deadline enforced host-runner-side; hooks' settings.local.json timeout is just a transport upper bound |
| `cancel_hard_after_ms` | 2000 | §6.1 — `C-c` then `kill -INT` ladder |
| `replay_turns_on_attach` | 5 | "Last N turns" decision; Phase 2 adds scroll-up pagination |
| `path_resolver` | newest mtime under `~/.claude/projects/<urlencoded-cwd>/` | Multi-project tail not in scope |

Removed: `grace_ms`, `capture_pane_poll_ms` (capture-pane removed
entirely).

All knobs live in one struct on the `ClaudeCodeAdapter`; tunable via
hub config without recompile.

---

## 9. Schema-drift policy

- **Unknown top-level `type`** → emit a `system` event with
  `subtype=unknown_type` and the type name; render as a muted info
  card. Do NOT fall back to xterm-VT — that would defeat the
  benefit of having the structured stream.
- **Known type with unexpected content block** → drop the
  problematic block, emit the rest, log to hub-side metrics.
- **`message.content` neither string nor array** → drop the event,
  log; should be unreachable in observed schemas.
- **Unknown `Notification.notification_type`** → emit as `system{subtype:"unknown_notification", notification_type, message}`; mobile renders a muted info card.
- **Unknown hook event name** → log + drop. Should be unreachable for the 9 events we install; new claude-code versions may add more.
- **Pane lost** (PID gone, tmux pane closed) → emit `system` error event; transition to `idle`; do not auto-recover.
- **Hook park times out** (mobile doesn't resolve within `hook_park_default_ms`) → return the configured default decision (e.g. for `PreToolUse(ExitPlanMode)`: `{"permissionDecision":"ask"}` so the TUI still shows its prompt and the human at the keyboard can decide). Never silently allow.

---

## 10. Out of scope (Phase 2+)

- Gemini, codex, kimi-code adapters (same shape, different paths +
  schemas). gemini-cli's `~/.gemini/tmp/<wd>/chats/...jsonl`,
  codex's `~/.codex/sessions/<date>/...jsonl`, kimi's
  `~/.kimi/sessions/<hash>/<uuid>/context.jsonl`.
- Scroll-up pagination beyond the 5-turn catchup.
- Q3 + Q5 from ADR-027 — `mcp_tool` long-park behavior and `PreToolUse permissionDecision:"allow"` overriding plan-mode prompt. Tested separately when hub-side hook handlers exist.
- New hook events beyond the 9 listed in §5.1 (e.g. `WorktreeCreate`, `Elicitation` — documented but not in 2.1.129).
- Auto-recovery when the agent's pane is closed.
- xterm-VT fallback when JSONL or hook schema drifts (see §9).

---

## 11. Implementation checklist

Wedge-sized; each line is its own commit / test pass:

- [ ] `hub/internal/drivers/local_log_tail/driver.go` — driver shell
  implementing the same interface as `ACPDriver`
- [ ] `hub/internal/drivers/local_log_tail/claude_code/` — adapter:
  path resolver, JSONL streamer + schema mapper, send-keys router
- [ ] `hub/internal/hostrunner/mcp_gateway.go` — 9 new MCP tool
  handlers (`hook_pre_tool_use`, `hook_post_tool_use`,
  `hook_notification`, `hook_pre_compact`, `hook_stop`,
  `hook_subagent_stop`, `hook_user_prompt_submit`,
  `hook_session_start`, `hook_session_end`); reuse the parking
  pattern from `mcpPermissionPrompt`
- [ ] `hub/internal/hostrunner/hooks_install.go` — spawn-time writer
  that merges the hooks block into `<workdir>/.claude/settings.local.json`
- [ ] `hub/internal/agentfamilies/agent_families.yaml` — switch
  claude-code M4 binding to `local_log_tail`
- [ ] Tests against `/tmp/probe.events` golden fixture from the JSONL
  probe (`hub/cmd/probe-claude-jsonl/`) AND the hook probe corpus
  from `hub/cmd/probe-claude-hooks/` (sample tarballs to commit
  alongside)
- [ ] On-device verification: open a fresh claude-code session,
  enter plan mode, trigger `ExitPlanMode`, verify the plan-approval
  card renders on mobile with `tool_input.plan` as the body and
  resolves correctly when tapped
- [ ] On-device verification: `/compact`, observe PreCompact-driven
  approval card; defer once + compact once
- [ ] On-device verification: idle/streaming pill clears on `Stop` +
  `Notification{idle_prompt}` (post-turn)
- [ ] On-device tune of `hook_park_default_ms` and `idle_threshold_ms`

---

## 12. References

- JSONL probe wedge: `hub/cmd/probe-claude-jsonl/` (committed `48c6a93`) — validated the transcript-event schema
- Hook probe wedge: `hub/cmd/probe-claude-hooks/` (committed `a45d24f`) — captured the 9-hook payload corpus from claude-code 2.1.129 on 2026-05-15
- ADR for the swap: [`docs/decisions/027-local-log-tail-driver.md`](../decisions/027-local-log-tail-driver.md) (Accepted 2026-05-15, amended same day)
- Existing parking model: `hub/internal/server/mcp_more.go::mcpPermissionPrompt` — the parked-MCP-call pattern this driver's hook handlers reuse verbatim
- ADR-010 frame-profiles-as-data: the per-engine adapter pattern inside `local_log_tail/` mirrors the YAML-profile model used for ACP frame profiles
- Existing M1 driver: `hub/internal/hostrunner/driver_acp.go` — reference for AgentEvent emission shape
- Hub-side MCP gateway: `hub/internal/hostrunner/mcp_gateway.go` — the per-spawn UDS transport that hook MCP tools route through
