# LocalLogTailDriver — claude-code adapter

> **Type:** plan
> **Status:** Draft (2026-05-15) — spec frozen; hook surface empirically validated against claude-code 2.1.129; implementation not started; ADR-027 amended same day
> **Audience:** contributors
> **Last verified vs code:** claude-code 2.1.129, JSONL 200k-line live sample + 9-hook payload corpus on 2026-05-15

**TL;DR.** Replace the current "agent fallback M4" raw-PTY+xterm-VT
path with a hub-side driver that combines **three structured
signal sources**:

1. **JSONL tail** of claude-code's on-disk session log — provides
   transcript content (text / thinking / tool_use / tool_result /
   attachment).
2. **`--permission-prompt-tool` MCP path** (existing infrastructure
   in `hub/internal/server/mcp_more.go::mcpPermissionPrompt`) —
   covers ALL approval gates including `ExitPlanMode` and
   non-bypass tool-permission requests. Per-tool dispatch in the
   handler emits the right `dialog_type` (plan_approval /
   tool_permission). Same parking model the M1/M2 stewards
   already use; **no new approval-routing code**.
3. **Hook surface** (via the per-spawn host-runner UDS MCP gateway,
   `type:"mcp_tool"`) — covers TUI-interactive state events not
   served by the approval channel: idle / turn-end / subagent-stop
   / session-lifecycle (purely observational) + compaction (parked)
   + AskUserQuestion picker content (parked + send-keys for the
   actual choice).

Mobile input flows back via `tmux send-keys` for free-text + cancel
+ escape + the AskUserQuestion picker's arrow-navigation. The
driver emits `AgentEvent` shapes identical to M1/M2; mobile
surfaces (cards, approval prompt, compose box, action bar, snippet
bar) gain a few new `dialog_type` branches (plan_approval,
user_question, compaction). MVP = claude-code only; gemini / codex
/ kimi are Phase 2/3 with adapter implementations against their
own log paths.

**Capture-pane is not in the implementation.** Empirical validation
(probe 2026-05-15) established that the approval channel +
hook surface together cover every TUI-interactive signal the
adapter needs as structured payloads, with categorical
discriminators (`Notification.notification_type`, `PreToolUse.tool_name`,
`PreCompact.trigger`, MCP `tool_name`). The pre-amendment design's
regex-driven capture-pane probe is removed entirely.

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

## 5. Two-channel signal surface

Two cooperating channels carry the M4 driver's structured signals.
Both empirically validated against claude-code 2.1.129 (probes
2026-05-15) and locked by ADR-027 D-amend-3/4.

### 5.A Approval channel — `--permission-prompt-tool` MCP path

The driver spawns claude-code with
`--permission-prompt-tool mcp__{{mcp_namespace}}__permission_prompt`
**always**, regardless of `permission_mode`. The permission engine
routes every tool whose `checkPermissions()` returns
`behavior:"ask"` through this MCP tool. Coverage:

| Tool | bypass | default | acceptEdits | dialog_type emitted |
|---|---|---|---|---|
| Write / Edit / MultiEdit / NotebookEdit | allow | ask | allow | `tool_permission` |
| Bash | allow | rule-based | rule-based | `tool_permission` |
| WebFetch | allow | ask | ask | `tool_permission` |
| Agent (Task) | allow | ask | ask | `tool_permission` |
| **ExitPlanMode** | **ask** | **ask** | **ask** | **`plan_approval`** (always — independent of mode) |
| AskUserQuestion | ask | ask | ask | gate auto-allowed; **picker handled via PreToolUse hook + send-keys** (see 5.B) |
| Read / Glob / Grep / TodoWrite | allow | allow | allow | (never gated) |

Existing handler at `hub/internal/server/mcp_more.go::mcpPermissionPrompt`
adapts cleanly with a per-tool dispatcher:

- `tool_name == "ExitPlanMode"` → set `dialog_type:"plan_approval"`, extract `body` from `tool_input.plan`, options `["approve","edit","comment"]`, park as today.
- `tool_name == "AskUserQuestion"` → **auto-allow the gate immediately** (`{behavior:"allow"}`); the picker UI is handled in 5.B.
- Any other tool → existing tier-based path (`tierFor()` + attention_items{kind:"permission_prompt"}).

Mobile resolution: existing approval-card UI in
`lib/widgets/agent_feed.dart`; the `dialog_type` discriminator
selects the appropriate body+options rendering.

### 5.B Observation channel — hook surface

Hooks installed via `<workdir>/.claude/settings.local.json` with
`type:"mcp_tool"` routing through the per-spawn host-runner UDS
MCP gateway (same transport as the approval channel). Host-runner
gains 7 new MCP tool handlers in `hub/internal/hostrunner/mcp_gateway.go`.
Approval-channel-covered hooks (PreToolUse non-AskUserQuestion,
PostToolUse) are informational only; the only hook that **parks**
is `PreCompact` (compaction isn't a tool, so the approval channel
doesn't cover it) and `PreToolUse(AskUserQuestion)` (the picker
content surface).

| Hook | Payload | AgentEvent emission | Parks? |
|---|---|---|---|
| **PreToolUse** | `tool_name, tool_input, tool_use_id, permission_mode` | If `tool_name=="AskUserQuestion"` → **park**, emit `approval_request{dialog_type:"user_question", questions: tool_input.questions, tool_use_id}` (see 5.B.1). Otherwise → informational only (mobile activity timeline). | only for AskUserQuestion |
| **PostToolUse** | `tool_name, tool_input, tool_response, duration_ms` | informational (JSONL has this too) | no |
| **Notification** | `message, notification_type` | `notification_type:"idle_prompt"` → `system{subtype:"awaiting_input"}`. `notification_type:"permission_prompt"` → **drop** (approval channel already handled this). Unknown → `system{subtype:"unknown_notification"}` | no |
| **PreCompact** | `trigger, custom_instructions` | **park**, emit `approval_request{dialog_type:"compaction", trigger, options:["compact","defer"]}`. Returns `{"decision":"block"}` to defer or `{}` to proceed | yes |
| **Stop** | `last_assistant_message, permission_mode, effort` | `system{subtype:"turn_complete", final_message}` | no |
| **SubagentStop** | `agent_id, agent_type, last_assistant_message, agent_transcript_path` | If `agent_type != ""` → `system{subtype:"subagent_complete", ...}`. If empty → drop (parent-turn duplicate) | no |
| **UserPromptSubmit** | `prompt, permission_mode` | informational (JSONL records soon after) | no |
| **SessionStart** | `source, model` | `system{subtype:"session_start", source, model}` | no |
| **SessionEnd** | `reason` | `system{subtype:"session_end", reason}` | no |

#### 5.B.1 AskUserQuestion picker — send-keys driven (Option A)

`PreToolUse(AskUserQuestion).tool_input.questions[]` carries the
structured payload: `{question, options:[{label}], isMultiSelect}`.
The adapter renders this on mobile as an N-choice picker (1-4
questions, per the tool's schema).

Resolution flow:

1. Hook parks, emits the `approval_request{dialog_type:"user_question"}` AgentEvent.
2. Mobile renders the question(s) + options as a card (existing approval-card widget, new dialog_type branch).
3. User selects option index `i` for each question.
4. Adapter unblocks the hook with `{}` (no decision override — the gate was already auto-allowed in 5.A).
5. claude-code's TUI renders the picker for the actual user-input step.
6. Adapter sends `tmux send-keys` arrow navigation + Enter for each question — `Down × i + Enter`. With multiple questions, repeat per question.

**MVP scope:** single-select only (`isMultiSelect:false`). Multi-select rejected with a `system{subtype:"multi_select_unsupported"}` event and the user handles in the TUI. Phase 2 adds toggle (Space) + submit navigation.

**Latency:** ~50-100 ms per send-keys round-trip; acceptable for click-then-wait UX.

#### 5.B.2 Empirically locked discriminators

The probe (2026-05-15) confirmed:

- `Notification.notification_type` is a structured categorical field. Observed values: `idle_prompt`, `permission_prompt`. Routing is a 2-row lookup, not a regex.
- `PreToolUse(ExitPlanMode).tool_input.plan` carries the full plan body — but **read via the approval channel (5.A), not the hook channel**, since `--permission-prompt-tool` receives the same `tool_input` payload.
- `PreToolUse(AskUserQuestion).tool_input.questions[]` carries the structured questionnaire — read via the hook channel since the approval channel auto-allows the gate.
- Every hook payload includes `permission_mode`. Mode changes (Shift+Tab) are reflected without JSONL parsing.
- `SubagentStop` fires twice per Task call: once with `agent_type` set (real subagent), once at parent turn end with empty `agent_type` (drop).
- `Stop.last_assistant_message` is the canonical parent-turn final-message field.

### 5.C Spawn-time setup

Two things written at spawn:

1. **Cmd flag** in `backend.cmd`:
   ```
   claude --model {{model}} <perm-mode-flag> --permission-prompt-tool mcp__{{mcp_namespace}}__permission_prompt
   ```
   `<perm-mode-flag>` is `--dangerously-skip-permissions` (M4 default), or empty (default mode), or `--permission-mode acceptEdits`, depending on the template's intent.
2. **Hook config** merged into `<workdir>/.claude/settings.local.json`:
   ```jsonc
   { "hooks": {
       "Notification":     [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_notification",   "timeout":5  }]}],
       "PreToolUse":       [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_pre_tool_use",   "timeout":30 }]}],
       "PostToolUse":      [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_post_tool_use",  "timeout":5  }]}],
       "PreCompact":       [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_pre_compact",    "timeout":300 }]}],
       "Stop":             [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_stop",           "timeout":5  }]}],
       "SubagentStop":     [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_subagent_stop",  "timeout":5  }]}],
       "UserPromptSubmit": [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_user_prompt",    "timeout":5  }]}],
       "SessionStart":     [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_session_start",  "timeout":5  }]}],
       "SessionEnd":       [{ "matcher":"*", "hooks":[{ "type":"mcp_tool", "tool":"mcp__{{mcp_namespace}}__hook_session_end",    "timeout":5  }]}]
   } }
   ```

If the workdir already has a `settings.local.json` (user's
`.permissions.allow` rules etc.), host-runner **merges** the hooks
block in rather than overwrite — preserves user configuration.

---

## 6. Input half — mobile action → tmux send-keys

The pane is identified by walking up from the `claude` PID
(`pstree -p <claude_pid>`) to the tmux pane that owns it.

`tmux send-keys` is **output-only direction** (mobile → CLI). Approve
/ deny / always-allow decisions for `tool_permission`, `plan_approval`,
and `compaction` do NOT route through here — they unblock parked
MCP / hook calls (§5.A and §5.B). The send-keys path covers
free-text input, control keys, and the **AskUserQuestion picker**
(the one tool whose internal UI requires arrow-key navigation per
§5.B.1, per ADR-027 D-amend-4).

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
| **AskUserQuestion: pick option `i` for question** (single-select MVP) | `tmux send-keys -t <pane> Down × i; tmux send-keys -t <pane> Enter` — per question; repeat sequentially for multi-question payloads |

The action table is **strictly smaller** than the pre-amendment
draft. No `Down Enter` / `Down Down Enter` rows for tool-permission
or plan-mode approvals — those collapse to the `--permission-prompt-tool`
MCP path. AskUserQuestion's arrow-key row remains because its
picker UI lives inside the tool body, not the permission engine.

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
  (including AskUserQuestion picker navigation per §6.1)
- [ ] `hub/internal/server/mcp_more.go::mcpPermissionPrompt` — extend
  with per-tool dispatch: `ExitPlanMode → dialog_type:"plan_approval"
  + body: tool_input.plan`; `AskUserQuestion → auto-allow gate`;
  default → existing tier-based path
- [ ] `hub/internal/hostrunner/mcp_gateway.go` — 9 new MCP tool
  handlers (`hook_pre_tool_use` parks only for AskUserQuestion,
  `hook_pre_compact` parks always; the other 7 are purely
  observational). Reuse the parking pattern from
  `mcpPermissionPrompt`.
- [ ] `hub/internal/hostrunner/hooks_install.go` — spawn-time writer
  that merges the hooks block into `<workdir>/.claude/settings.local.json`
- [ ] `hub/internal/agentfamilies/agent_families.yaml` — switch
  claude-code M4 binding to `local_log_tail`
- [ ] `lib/widgets/agent_feed.dart` — add new `dialog_type` branches
  in the approval-card widget: `plan_approval` (markdown body),
  `user_question` (multi-choice picker), `compaction` (simple
  yes/no). Existing `tool_permission` path unchanged.
- [ ] Tests against `/tmp/probe.events` golden fixture from the JSONL
  probe (`hub/cmd/probe-claude-jsonl/`) AND the hook probe corpus
  from `hub/cmd/probe-claude-hooks/` (sample tarballs to commit
  alongside)
- [ ] On-device verification: open a fresh claude-code session
  with `--dangerously-skip-permissions --permission-prompt-tool
  mcp__termipod__permission_prompt`, enter plan mode, trigger
  `ExitPlanMode`, verify the plan-approval card renders on mobile
  with `tool_input.plan` as the body and resolves correctly when
  tapped
- [ ] On-device verification: trigger AskUserQuestion, verify mobile
  picker renders, tap option → arrow-keys send-keys navigate TUI
  → tool returns the chosen option
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
