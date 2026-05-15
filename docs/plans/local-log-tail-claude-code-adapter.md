# LocalLogTailDriver — claude-code adapter

> **Type:** plan
> **Status:** Draft (2026-05-15) — spec frozen, implementation not started; ADR pending
> **Audience:** contributors
> **Last verified vs code:** claude-code 2.1.129, JSONL 200k-line live sample on 2026-05-15

**TL;DR.** Replace the current "agent fallback M4" raw-PTY+xterm-VT
path with a hub-side driver that tails claude-code's on-disk session
JSONL, maps events to the same `AgentEvent` shapes M1/M2 already
emit, and routes mobile input actions via `tmux send-keys` against
the pane that owns the `claude` PID. Mobile-side surfaces (cards,
approval prompt, compose box, action bar, snippet bar) are unchanged.
MVP = claude-code only; gemini / codex / kimi are Phase 2/3 with
adapter implementations against their own log paths.

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
              (no live JSONL events for >idle_threshold)
                                 │
                                 ▼
                          ┌─────────────┐
              ┌──────────►│    idle     │◄────────────────┐
              │           └─────────────┘                  │
              │ tool_result lands                          │ tool_use lands
              │                                            │
              │                                            ▼
              │                                ┌─────────────────────┐
              │ tool_result lands              │      streaming      │
              │  within grace                  │ (assistant text or  │
              │ (auto-allow)                   │  tool_use pending)  │
              │ ────────────────────────────── └─────────────────────┘
              │                                            │
              │                                            │ grace timer fires
              │                                            │ AND tool_use unmatched by rule
              │                                            ▼
              │ tool_result lands              ┌─────────────────────┐
              │  with denial marker            │ awaiting_approval   │
              │ (user picked Deny+Reason)      │ (capture-pane probe │
              │ ──────────────────────────────►│  confirms prompt)   │
              │                                └─────────────────────┘
              │                                            │
              │                                            │ row 3 selected
              │                                            │ (Deny + Reason)
              │                                            ▼
              │                                ┌─────────────────────┐
              └────────────────────────────────│  awaiting_reason    │
                                               │ (compose box shows  │
                                               │  reason prompt;     │
                                               │  next SendInput     │
                                               │  flows as normal)   │
                                               └─────────────────────┘

  Any pane-loss event → emit system error AgentEvent, stay in idle.
```

### 4.1 grace_ms knob

- **Default: 600 ms** (post-tool_use; if `tool_result` lands inside this window, treat as auto-allowed and skip capture-pane).
- Rationale: claude-code's own allowlist match + tool execution starts within ~100–300 ms for in-policy bash. 600 ms gives comfortable headroom without making mobile feel sluggish on real approval moments.
- Tune on-device — actual prompt latency on the user's hardware overrides this default.

### 4.2 idle_threshold knob

- **Default: 2000 ms** of no events.
- Transitions `streaming → idle` so mobile's "agent is working" pill can clear.

---

## 5. Permission rule reading

On attach and on `tool_use`, adapter reads (and merges) the allowlist
from:

1. `~/.claude/settings.json` `.permissions.allow`
2. `<cwd>/.claude/settings.local.json` `.permissions.allow`

If a `tool_use` matches any pattern, **skip the approval flow** —
emit a normal `tool_call` AgentEvent and wait for the result.
If no pattern matches, **start the grace timer** and proceed to the
state machine above.

### 5.1 Match patterns supported in MVP

| Pattern form | Example | Match rule |
|---|---|---|
| `<ToolName>` bare | `Read` | matches any tool_use with `name == "Read"` |
| `Bash(<cmd-prefix> *)` | `Bash(git push *)` | matches `name == "Bash"` AND `input.command` starts with `git push ` |
| `Bash(<exact-cmd>)` | `Bash(python3)` | matches `name == "Bash"` AND `input.command` exactly equals `python3` |
| `WebFetch(domain:<host>)` | `WebFetch(domain:pub.dev)` | matches `name == "WebFetch"` AND URL host equals `pub.dev` |
| `WebSearch` | `WebSearch` | matches `name == "WebSearch"` |

Anything else — fall through to "no match," start grace timer.
Mis-parses cost at most one capture-pane probe; never block the user.

### 5.2 We do not implement

- A custom "always allow" writer. When the user picks row 2 on
  mobile, the keys flow to claude-code, which writes the new
  pattern to `settings.local.json` itself.
- Cross-pattern semantic matching (e.g. recognizing that
  `Bash(ls /tmp/foo)` is "covered" by `Bash(ls *)`). Exact glob
  prefix match only.

---

## 6. Input half — mobile action → tmux send-keys

The pane is identified by walking up from the `claude` PID
(`pstree -p <claude_pid>`) to the tmux pane that owns it.

### 6.1 Action table

| Mobile action | tmux command |
|---|---|
| Compose box → text submit (short, no newlines) | `tmux send-keys -t <pane> -l "<text>"; tmux send-keys -t <pane> Enter` |
| Compose box → text submit (long or multi-line) | `tmux load-buffer -; tmux paste-buffer -t <pane>; tmux send-keys -t <pane> Enter` |
| Approval card → Approve (row 1, default) | `tmux send-keys -t <pane> Enter` |
| Approval card → Always allow (row 2) | `tmux send-keys -t <pane> Down Enter` |
| Approval card → Deny + reason (row 3) | `tmux send-keys -t <pane> Down Down Enter` — then user types reason in compose box; flows as next `SendInput` |
| Cancel current turn (soft) | `tmux send-keys -t <pane> C-c` |
| Cancel current turn (hard fallback after 2 s) | `kill -INT <claude_pid>` |
| Escape modal / dismiss prompt | `tmux send-keys -t <pane> Escape` |
| Action bar / snippet → Up/Down/Tab | `tmux send-keys -t <pane> <name>` |

### 6.2 Highlight-position arithmetic

Approval prompts may not always pre-select row 1 (claude-code
remembers the user's last choice for similar prompts). The
capture-pane probe (see §7) returns the highlight index; the
adapter computes:

```
delta = target_row - highlighted_row
keys  = [Down × delta if delta > 0 else Up × |delta|, ..., Enter]
```

This is robust against highlight drift. Hard-coded `Enter` /
`Down Enter` / `Down Down Enter` is the default-case shortcut only.

---

## 7. Capture-pane probe

After grace expires on an unmatched `tool_use`, poll `tmux capture-pane -t <pane> -p -e` at 200 ms cadence until either:

- A `tool_result` arrives in JSONL (transitions to idle; cancel poll), or
- The pane content matches the approval-prompt regex (transition to `awaiting_approval`; emit AgentEvent; stop polling).

### 7.1 Approval-prompt regex (MVP)

Returns `ApprovalPrompt{options[], highlighted_index}`. Two patterns:

| Prompt type | Heuristic |
|---|---|
| **Numbered select** | Find lines starting with `❯ \d+\.` (highlighted row) and `  \d+\.` (other rows). Order by row number; mark `❯` row as highlighted. Capture row text after the number. |
| **Y/N inline** | Find `(y/n)` or `(Y/n)` patterns. Single-binary; no Down keys needed; Approve = `y Enter`, Deny = `n Enter`. |

Anything else (plan-mode menu, MCP tool prompt, slash menu): treat
as unrecognized — emit a `system` event with the captured screen
slice, render in mobile as a read-only block, and let the user
interact via raw key actions from the action bar. Phase 2 adds
custom regexes per prompt kind.

### 7.2 Latency budget

- 600 ms grace + (up to ~2 ticks × 200 ms) before approval card
  appears on mobile = ~1 s p99. Acceptable for a human-decision UX.
- If the agent auto-allowed, no capture-pane probe runs (skipped
  via §5 rule match) — zero added latency.

---

## 8. MVP knobs (single source of truth)

| Knob | Default | Notes |
|---|---|---|
| `grace_ms` | 600 | §4.1 |
| `idle_threshold_ms` | 2000 | §4.2 |
| `capture_pane_poll_ms` | 200 | §7 |
| `cancel_hard_after_ms` | 2000 | §6.1 |
| `replay_turns_on_attach` | 5 | Per "last N turns" decision; Phase 2 adds scroll-up pagination |
| `path_resolver` | newest mtime under `~/.claude/projects/<urlencoded-cwd>/` | Multi-project tail not in scope |

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
- **`settings.local.json` missing or malformed** → behave as if
  allow-list is empty; every tool_use goes through the grace +
  capture-pane probe path.
- **Pane lost** (PID gone, tmux pane closed) → emit `system` error
  event; transition to `idle`; do not auto-recover.

---

## 10. Out of scope (Phase 2+)

- Gemini, codex, kimi-code adapters (same shape, different paths +
  schemas). gemini-cli's `~/.gemini/tmp/<wd>/chats/...jsonl`,
  codex's `~/.codex/sessions/<date>/...jsonl`, kimi's
  `~/.kimi/sessions/<hash>/<uuid>/context.jsonl`.
- Scroll-up pagination beyond the 5-turn catchup.
- Custom regex library for plan-mode prompts, MCP-tool prompts,
  slash menus.
- Predictive permission matching for non-MVP pattern forms (regex,
  cross-pattern coverage).
- inotify-based `settings.local.json` watcher — MVP re-reads on
  each tool_use.
- xterm-VT fallback when schema drift is detected (see §9).

---

## 11. Implementation checklist

Wedge-sized; each line is its own commit / test pass:

- [ ] `hub/internal/drivers/local_log_tail/driver.go` — driver shell
  implementing the same interface as `ACPDriver`
- [ ] `hub/internal/drivers/local_log_tail/claude_code/` — adapter:
  path resolver, JSONL streamer, schema mapper, state machine,
  capture-pane probe, send-keys router, settings.local.json reader
- [ ] `hub/internal/agentfamilies/agent_families.yaml` — switch
  claude-code M4 binding to `local_log_tail`
- [ ] Tests against `/tmp/probe.events` golden fixture from probe
  wedge (`hub/cmd/probe-claude-jsonl/`)
- [ ] On-device verification: open a fresh claude-code session,
  trigger a tool_use that requires approval, verify card renders
  and tap-Approve completes the loop
- [ ] Verify settings.local.json gets the new pattern after
  tapping row 2
- [ ] Verify Deny + reason flow: row 3 → reason prompt visible →
  compose-box text flows as next turn
- [ ] On-device tune of grace_ms, capture_pane_poll_ms

---

## 12. References

- Probe wedge: `hub/cmd/probe-claude-jsonl/` (committed `48c6a93`)
- ADR for the swap: `docs/decisions/<NN>-local-log-tail.md` (pending)
- ADR-010 frame-profiles-as-data: the per-engine adapter pattern
  inside `local_log_tail/` mirrors the YAML-profile model used for
  ACP frame profiles
- Existing M1 driver: `hub/internal/hostrunner/driver_acp.go` —
  reference for AgentEvent emission shape
