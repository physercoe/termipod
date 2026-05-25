---
name: Codex M4 (LocalLogTail) research
description: Field-verified investigation into wiring codex as a LocalLogTailDriver engine (ADR-027 Phase 2/3) — JSONL path + schema, four envelope types × ~14 payload kinds, frame→agent_event map, resume-appends behaviour, send-keys vocabulary, deviations from claude-code (no hook gateway, no statusLine; codex carries telemetry inline). Captures the on-host probe (codex-cli 0.133.0, two probe runs) that grounded each claim and scopes a follow-up ADR + wedge plan (~1.1–1.3 kLOC + ~600 LOC of tests).
---

# Codex M4 (LocalLogTail) research

> **Type:** discussion
> **Status:** Paused (2026-05-25) — see §0 below. The JSONL schema and adapter sizing remain valid for the eventual M4 work (crash-recovery / file-on-disk resilience), but Phase 2 of ADR-027 for codex is **no longer urgent** because the upstream `openai/codex` repo is Apache-2.0 and exposes a v2 hook protocol surface — the original "M2-is-a-black-box" justification was wrong. §7 (approvals) needs re-reading with that context. Sibling research that landed as ADR-035 (`antigravity-engine-m4-locallogtail.md`) remains the structural template if/when we resume.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.712-alpha (host: codex-cli 0.133.0 on Ubuntu 22.04 / x86_64)

**TL;DR.** Codex writes a comprehensive per-session JSONL transcript
at `~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<ISO>-<UUID>.jsonl`
covering every mode it ships (`codex` TUI, `codex exec`, `codex
resume`, `codex review`) — confirmed on host with three live
runs. Resume **appends** to the existing file (no rotation, no
sibling file). Frames are `{timestamp, type, payload}` with four
envelope types (`session_meta`, `event_msg`, `response_item`,
`turn_context`) and ~14 discriminated `payload.type` kinds; every
kind maps cleanly to an existing termipod `agent_events.kind`.
Telemetry already carries the per-turn / cumulative split (the
v1.0.712 fix) **and** the `model_context_window` — no statusLine
shim needed (M4-claude-code had to install one in v1.0.696-698;
codex doesn't). Codex's per-tool-call approval gate flows through
the same `mcpServer/elicitation/request` rmcp shape app-server
uses, but the JSONL records the **outcomes** (a `function_call` +
`function_call_output` pair); approvals would still need an
out-of-band route — provisionally via the same UDS gateway pattern
the claude-code adapter uses for hooks, OR by spawning codex with
`--dangerously-bypass-approvals-and-sandbox` + relying on
hub-side trust. The clear shape is ~1.1–1.3 kLOC of new code
plus ~600 LOC of tests, mirroring the structure of
`hub/internal/drivers/local_log_tail/claude_code/`. Phase 2 of
ADR-027 is unblocked.

---

## 0. Paused — codex is open source; M4 urgency drops materially

Same-day amendment (2026-05-25 PM). After this research landed,
we checked the upstream provenance: codex is
[`openai/codex`](https://github.com/openai/codex) on GitHub,
**Apache-2.0**, 85k stars, last push within the hour. The
binary's npm `package.json` advertises the repo directly:
`"repository": "git+https://github.com/openai/codex.git",
"directory": "codex-cli"`.

That moves two load-bearing premises:

1. **The "M2 protocol is rmcp-private" framing in §1 was wrong.**
   The protocol crate
   (`codex-rs/app-server-protocol/src/protocol/v2/`) is plain
   Rust source; every shape we speak in `AppServerDriver` is
   declared there. We don't need M4 just to read codex's mind.

2. **§7 (approvals) Path-B-impossible claim is wrong.** Codex
   ships an explicit hook surface in
   `codex-rs/app-server-protocol/src/protocol/v2/hook.rs` —
   `HookEventName::{PreToolUse, PermissionRequest, PostToolUse,
   PreCompact, PostCompact, SessionStart, UserPromptSubmit,
   SubagentStart, SubagentStop, Stop}`. Nearly 1:1 with
   claude-code's hook contract. The v1.0.712
   `AutoAcceptMCPToolCalls` global bypass was the right interim
   fix for the symptom; the structural fix is to wire those
   hooks through the existing AppServerDriver attention bridge,
   not to build M4.

**What stays valid in this document:**

- §2 (JSONL on-disk layout) — host-verified, doesn't go stale.
- §3 (frame schema) — same.
- §4 (frame → agent_event map) — useful reference if/when we
  pursue M4 for crash-recovery resilience, independent of the
  approvals question.
- §5 (send-keys vocabulary) — same.
- §6 (telemetry already complete) — same.
- §8 (resume = append) — host-verified.
- §9 (code sizing) — still a fair estimate.

**What's now wrong / needs re-reading:**

- §1 (Why now) — the M2-is-a-black-box premise.
- §7 (Approvals — the structural decision) — Path B is
  reachable via the v2 hook surface; reasoning about codex
  needing upstream changes was incorrect.
- §11 (Recommended next step) — ADR-036 + 8-wedge plan is
  paused.

**Next session is M2-deepening**, not M4-implementation:

1. Read `codex-rs/app-server-protocol/src/protocol/v2/{hook,
   permissions,turn,thread,mcp}.rs` end-to-end.
2. Wire `PreToolUse` / `PermissionRequest` through the existing
   `AppServerDriver` attention bridge — same plumbing as the
   `mcpServer/elicitation/request` path the v1.0.711-712 wedges
   shipped. Replace the v1.0.712 global bypass with per-hook
   routing: bypass for trusted MCP servers (termipod), bridge
   for shell/file (`PreToolUse` on `exec_command` /
   `apply_patch`).
3. Audit our M2 driver against the v2 protocol crate for
   notifications + methods we're not yet exposing.

The rest of this document is the original research — preserved
unchanged because the schema work doesn't go stale and is the
foundation for the M4 wedge if we ever need it. Read on for
context if you're the one resuming this.

---

## 1. Why now

ADR-027 D9 ("Phase 2/3 engines") parks codex (and gemini-cli /
kimi-code) on the legacy `PaneDriver` M4 path because the JSONL
adapter wasn't built. With the codex M2 polish in v1.0.706–v1.0.712
the operator now has a viable M2 codex experience, but:

- **M2 is exec-server-bound.** Every spawn keeps an
  `app-server --listen stdio://` child alive on the host;
  long-lived state lives in codex's in-process thread store.
  Hosts that crash lose the running turn (resume rebuilds via
  `thread/resume`, but a turn-mid-flight is sunk).
- **M2's bridge surfaces are codex-private (rmcp JSON-RPC).** The
  attention bridge, the `mcpServer/elicitation/request` codex
  uses for tool-call gates, the `app-server` token-usage frame —
  all of these are codex-app-server-private and require the
  AppServerDriver to be running to observe.
- **PaneDriver is screen-scraping the TUI.** The current legacy
  fallback for non-claude engines reads raw bytes off the tmux
  pane. Cards (text, tool_use, tool_result, usage) are derived
  via regexes that drift on every codex TUI redesign. The smoke
  failures around v1.0.643 (agy dual-pane / wrong-cwd cascade)
  came out of this exact class.

M4-LocalLogTail in the claude-code shape — JSONL tail + send-keys
input + UDS gateway for hooks — fixes both: the tail survives
host-runner restarts (file-on-disk is the recovery point), the
JSONL is codex's own structured event log (zero scraping), and
the principal gets a live tmux pane for the breakglass case.

## 2. On-disk layout (verified on host)

Single canonical path:

```
~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<ISO>-<UUID>.jsonl
```

Example from the verified probe:

```
~/.codex/sessions/2026/05/25/rollout-2026-05-25T15-02-39-019e5fa8-cf7f-7113-8caa-2d369c3bddf1.jsonl
```

Distinguishers vs claude-code's `~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`:

- **Not keyed by cwd.** Codex shards by date, claude-code shards by
  cwd. The cwd is recorded *inside* the JSONL (`session_meta.payload.cwd`)
  but not in the filename. → Our path resolver needs to scan the
  date directory and filter by `cwd` from line 1.
- **Time-shaped filename, not UUID-only.** The ISO prefix is a
  natural sort key for mtime-equivalent ordering — useful when
  picking "newest matching" without stat-ing every entry.
- **No mid-session rotation.** Verified: a 40-line session
  remained a single file. `codex resume <SID>` **appends** to the
  same file (29585 → 31812 bytes on a single resume turn — no new
  sibling). → Our tailer can track a single inode + file offset
  across host-runner restarts.

Sidecars also under `~/.codex/`:

- `auth.json` — OAuth tokens (PRIVATE, never read).
- `config.toml` — operator-global config; we override per-spawn via
  `CODEX_HOME=.codex codex …` to point at a project-local
  `<workdir>/.codex/config.toml` (already done by `launch_m2.go`).
- `history.jsonl` — terse cross-session command history (one row
  per user prompt, all sessions combined). Not useful for the
  adapter.
- `log/codex-tui.log` — internal OTEL tracing (`tracing-subscriber`).
  Not useful for the adapter; JSONL is the source of truth.
- `state_*.sqlite`, `logs_*.sqlite` — internal state stores. Not
  parsed.
- `sessions/`, `memories/`, `skills/`, `shell_snapshots/` — codex's
  own per-session artefacts.

## 3. Frame schema

Every line is:

```jsonc
{ "timestamp": "<RFC3339>", "type": "<envelope>", "payload": { … } }
```

Four envelope types, ~14 discriminated `payload.type` kinds across
the four probed sessions (TUI + exec + exec resume). The
distribution from a 138-frame corpus:

| envelope.type   | count | discriminator |
| --------------- | ----- | ------------- |
| `response_item` | 57    | `payload.type` |
| `event_msg`     | 53    | `payload.type` |
| `turn_context`  | 6     | (always one shape) |
| `session_meta`  | 4     | (always one shape; one per session, line 1) |

Discriminated by `(envelope.type, payload.type)`:

| envelope | payload.type | count | meaning |
| --- | --- | --- | --- |
| `session_meta` | — | 4 | session open: id, cwd, originator, cli_version, model_provider, base_instructions |
| `turn_context` | — | 6 | per-turn settings: approval_policy, sandbox_policy, permission_profile, model, reasoning_effort |
| `event_msg` | `task_started` | 6 | turn boundary opens: turn_id, model_context_window, collaboration_mode_kind |
| `event_msg` | `task_complete` | 6 | turn boundary closes: turn_id, last_agent_message, duration_ms, time_to_first_token_ms |
| `event_msg` | `token_count` | 13 | telemetry: `info.{total,last}_token_usage.*` + `info.model_context_window` (same shape as M2's `thread/tokenUsage/updated`) |
| `event_msg` | `user_message` | 6 | principal turn echo (text + images list) |
| `event_msg` | `agent_message` | 11 | assistant prose (incremental, may fire multiple times per turn) |
| `event_msg` | `exec_command_end` | 7 | rich shell-command result: parsed_cmd, exit_code, stdout/stderr/aggregated_output, duration |
| `event_msg` | `web_search_end` | 4 | web-search outcome: queries[] |
| `response_item` | `message` | 26 | model output: role ∈ {developer, user, assistant}; content list |
| `response_item` | `reasoning` | 11 | chain-of-thought: summary[] + encrypted_content blob |
| `response_item` | `function_call` | 8 | tool invocation: name, arguments (JSON string), call_id |
| `response_item` | `function_call_output` | 8 | tool result: call_id, output text |
| `response_item` | `web_search_call` | 4 | web-search invocation: action.query, action.queries |

Observations:

- **No frame is unrecognised.** Every payload.type maps to an
  existing termipod `agent_events.kind`. No new kinds needed.
- **`event_msg` vs `response_item` is the doubled-channel pattern.**
  Codex emits each interaction twice — once as a `response_item`
  (model-protocol-level), once as an `event_msg` (presentation-
  level). The TUI consumes `event_msg`; we should too. The
  `response_item` track is useful for resume-replay and audit but
  produces duplicates if both are mapped.
- **`agent_message` is incremental.** Multiple `agent_message`
  frames per turn while the model streams its reply (similar to
  claude-code's streaming-text deltas). The adapter must coalesce
  by `turn_id` (carried on the surrounding `task_started`).
- **`reasoning` is mostly opaque.** The `encrypted_content` field is
  a blob we can't decode. The `summary[]` array is sometimes
  populated with short rationale snippets and is the right thing
  to show on a `thought` card (claude-code parity).

## 4. Frame → `agent_events` map (proposed)

| codex frame | hub event | notes |
| --- | --- | --- |
| `session_meta` | `session.init` | extract `cwd`, `cli_version`, `model_provider`, `originator`, **embed compact derived `version` = "codex-cli/<v>" so mobile's session card reads consistently** |
| `task_started` | `turn.start` | turn_id; emit `model_context_window` here so the context-fill strip has a value before `token_count` lands |
| `task_complete` | `turn.result` | turn_id, last_agent_message, duration_ms, time_to_first_token_ms → cost line. Cost itself is **not in the JSONL**; derived from `token_count` cumulative deltas (see §6) |
| `token_count` | `usage` (cumulative=true) | reuse the M2 profile rule (v1.0.712 — `total.*` + `last.*` + `model_context_window`) — same payload, different source |
| `user_message` | `input.text` (echo) | producer=`agent` (the engine echoing what it received); mobile already has the real input event with producer=`user`/`a2a` — dedupe by `(content, time-window)` to avoid double-card OR suppress on the adapter side |
| `agent_message` | `text` (streaming) | coalesce per turn_id; emit on each delta + once at `task_complete` with the final consolidated text |
| `response_item.message` role=assistant | (skip) | duplicate of `agent_message`; the `event_msg` track is canonical |
| `response_item.message` role=developer | `system` | one-shot sandbox/policy preamble — surface but render quietly (claude-code's session.init equivalent does this too) |
| `response_item.message` role=user | (skip) | duplicate of `user_message` |
| `response_item.reasoning` | `thought` | emit when `summary` is non-empty; skip when only `encrypted_content` (no human-readable content to surface) |
| `response_item.function_call` | `tool_call` | name=`exec_command`/`apply_patch`/etc; `arguments` is a JSON string — parse and embed shape-by-name |
| `response_item.function_call_output` | `tool_result` | pair by `call_id` |
| `event_msg.exec_command_end` | `tool_result` (enrich) | strictly richer than `function_call_output` — has exit_code, parsed_cmd, duration, separate stdout/stderr. Prefer this over the response_item version when both fire for the same call_id |
| `response_item.web_search_call` | `tool_call` (web search) | name=`web_search` |
| `event_msg.web_search_end` | `tool_result` | pair by call_id; surface `queries[]` |
| `turn_context` | (cache, no event) | adapter caches `approval_policy`, `sandbox_policy`, `model` per turn_id; emit at most one `system` event per session if values change across turns (rare) |

Coalescing rules (per turn):

- **`agent_message` deltas** → one `text` card per turn, growing.
  Mirrors claude-code adapter's streaming-message coalescer.
- **`function_call` / `exec_command_end` / `web_search_end`** →
  each tool gets one card. The pair is keyed by `call_id`. If
  both `function_call_output` and `exec_command_end` arrive for
  the same call_id (the doubled-channel rule), `exec_command_end`
  wins because it carries `exit_code` + duration + parsed_cmd.
- **`reasoning`** → one `thought` card per `summary` non-empty
  frame. (Many `reasoning` frames are summary-empty + encrypted-
  only and surface no useful text — skip those.)

## 5. Input routing — send-keys vocabulary

Codex ships three launch modes. M4 must use the **TUI** mode
(`codex` with no subcommand) because it's the only one that stays
alive across turns and accepts new prompts via stdin. `codex exec`
exits after one turn (good for one-shots, wrong for chat). `codex
app-server` is M2.

The TUI is a ratatui-based app with these probable bindings
(host-verify before implementation):

- **Type text + Enter** — submits the prompt.
- **Ctrl-C** — interrupts the current turn (codex's
  `turn/interrupt`-equivalent).
- **Ctrl-D** — exits codex.
- **`/slash` commands** — `/clear`, `/quit`, `/model`, `/mode`, etc.
  (raw passthrough, codex consumes).

The send-keys vocabulary mirrors claude-code's exactly. Map the
hub's `input.*` events to tmux send-keys actions:

| hub input.kind | codex tmux action |
| --- | --- |
| `text` | `tmux load-buffer` + `tmux paste-buffer -p` (bracketed paste, LF→CR conversion guarded) then `tmux send-keys Enter` |
| `cancel` | `tmux send-keys C-c` |
| `attention_reply` (approval) | (n/a in M4 — see §7) |
| `set_mode` / `set_model` | `tmux send-keys -l '/mode <id>'` Enter (slash command via paste-buffer) |

The v1.0.652 lesson holds: paste-buffer LF→CR splitting needs the
same `-r` flag the claude-code adapter uses. Carry the
`sendkeys.go` helper over rather than reinventing.

## 6. Telemetry — already complete

Three reasons codex M4 telemetry is **simpler** than claude-code M4:

1. **`token_count` carries everything.** The same shape the
   v1.0.712 profile rule already lifts from
   `thread/tokenUsage/updated`:
   `info.total_token_usage.*` (cumulative), `info.last_token_usage.*`
   (per-turn), `info.model_context_window`. The adapter reuses
   the profile JSON-path rule unchanged — just sourcing from the
   JSONL `event_msg.token_count` frame instead of the rmcp
   notification.
2. **`model_context_window` is on `task_started` AND `token_count`.**
   So context-fill is known before the first usage frame lands.
   No need for the claude-code statusLine shim (v1.0.696-698) that
   workaround a similar gap.
3. **Cost is derivable.** Codex doesn't emit a USD cost line in
   the JSONL, but the model name + token counts are enough to
   compute via the existing pricing table (`hub/internal/pricing/`).
   Treat the codex usage frame the same way we treat claude-code's
   M4 usage path — derive `cost_usd_imputed` server-side.

The only new field worth lifting is `task_complete.time_to_first_token_ms`
— codex makes it explicit; claude-code derives it from message
deltas. Useful telemetry. Cheap to add.

## 7. Approvals — the structural decision

This is where codex M4 deviates from claude-code M4 the most.

Codex's per-tool-call approval gate flows through
`mcpServer/elicitation/request` (the rmcp shape v1.0.711/v1.0.712
operate on). That's a **server-initiated JSON-RPC request** —
codex (the engine, in the role of MCP **client**) blocks on a
response from its **host** (us). In M2 this works because the
AppServerDriver owns the rmcp pipe and can write the response on
the parked request id.

**In M4, the rmcp pipe doesn't exist.** Codex's TUI handles
approvals via its own modal prompt — we have no protocol-level
way to inject a decision. Two viable paths:

### Path A — bypass-mode only (recommended for the MVP)

Spawn codex with `--dangerously-bypass-approvals-and-sandbox` (or
equivalent: `-a never -s danger-full-access`). Codex skips its own
gates entirely. The hub remains the trust boundary — same posture
as v1.0.712's `AutoAcceptMCPToolCalls` for M2. Pros: zero new
plumbing, zero hook gateway. Cons: principal gives up the
breakglass "I see the gate appear and can decline" affordance
codex's TUI provides on its own. (They get the JSONL outcome
either way; what they lose is *pre-execution intervention* on
non-bypass spawns.)

### Path B — UDS gateway mirroring the claude-code adapter

Same shape as `hub/internal/hostrunner/mcp_gateway.go` already
exposes for claude-code's hook tools. Codex would need a config
that points at the gateway's MCP server, and codex's
`mcpServer/*` requests would route through it. Pros: full
parity with M2's attention-bridge UX. Cons: codex doesn't have a
`PreToolUse` hook (it uses `mcpServer/elicitation/request` only
for its own gates, which is what the MCP protocol already
forwards). We can't intercept tool-call decisions out-of-band
the way claude-code's `hook_PreToolUse` mechanism allows.
**Probably not implementable without upstream codex changes.**

### Provisional choice

**Path A.** Steward templates ship with `approval_policy = "never"`
+ `--dangerously-bypass-approvals-and-sandbox`; this is the same
trust posture v1.0.712 already enforces. Path B is a follow-up if
codex upstream adds a hook surface comparable to claude-code's.

## 8. Resume

Verified on host: `codex exec resume <SID> --skip-git-repo-check
<prompt>` appends to the existing `rollout-*.jsonl` (29585 → 31812
bytes, no new file). The appended timeline:

```
12  event_msg/task_started
13  turn_context/-
14  response_item/message
15  event_msg/user_message
16  event_msg/agent_message
17  response_item/message
18  event_msg/token_count
19  event_msg/task_complete
```

So the tailer can:

- Track `(inode, offset)` across host-runner restarts.
- After restart, `lseek` to the saved offset and continue reading
  — no new frame parser needed.
- The session row's `engine_session_id` is `session_meta.payload.id`
  (line 1, already on disk before any post-restart work).

The TUI resume path (`codex` without `exec`, with `--resume <SID>`
or via the `resume` picker) needs verifying — likely identical
behaviour. Open in §10.

## 9. Code shape (sizing)

Mirrors `hub/internal/drivers/local_log_tail/claude_code/`:

| file | est LOC | what it does |
| --- | --- | --- |
| `pathresolver.go` | ~120 | scan `~/.codex/sessions/YYYY/MM/DD/`, filter by `session_meta.payload.cwd`, return the freshest matching path; `WaitForSession(ctx)` polling helper |
| `tailer.go` | ~150 | line-buffered JSONL reader with offset tracking + EOF-poll loop; rotation isn't a thing for codex (verified above) but keep the contract symmetric |
| `mapper.go` | ~400 | the §4 frame→event table; coalescer for `agent_message` deltas; tool-call pair matching by `call_id` |
| `adapter.go` | ~350 | `Adapter` interface impl; `Start` (path-resolve + tail spawn + run loop); `Stop` (drain); `HandleInput` (send-keys); per-turn state (turn_id, model_context_window cache) |
| `sendkeys.go` | ~80 | text/cancel/slash vocabularies; LF→CR guard ported from claude-code adapter |
| `state.go` | ~60 | turn tracking, latest token_count snapshot |
| `launch_m4_codex.go` (new) | ~150 | mirror of `launch_m4_locallogtail.go` but no hook-gateway wiring; reuses `writeCodexMCPConfig` from `launch_m2.go` for the per-spawn `.codex/config.toml` |
| **subtotal** | **~1.3 kLOC** | |
| `*_test.go` per file | ~600 LOC | corpus fixture under `testdata/codex_jsonl/` capturing each frame type once |

Plus a 1-line runner.go dispatch: codex Kind → `launchM4Codex` when
mode resolves to M4.

## 10. Open questions (host-verify before/during implementation)

1. **TUI resume path.** `codex --last` (no `exec`) — does it
   append to the existing JSONL the same way `exec resume` does?
   Probable yes; verify with a 30s probe.
2. **MCP tool calls in JSONL.** When codex invokes a `termipod`
   MCP tool (e.g. `documents.get`), does the `function_call` frame
   carry the MCP-namespaced name (`mcp__termipod__documents.get`)
   or just the short name? The adapter's tool-card mapper needs
   to recognise both.
3. **Cancel semantics under M4.** `tmux send-keys C-c` aborts the
   current turn at the TUI level. Does codex write a
   `task_complete` with a `cancelled` status or just stop the
   stream silently? If the latter, the adapter needs a watchdog to
   synthesise a turn-end event — same class as
   `LocalLogTailDriver`'s claude-code adapter `WriteCancel`.
4. **Path-resolver narrowness.** Date sharding means a busy host
   can have many JSONLs on the same day. The (cwd, mtime≥
   launch-time) filter should be unique per spawn — but the
   adapter still needs the cwd → date directory traversal not to
   blow up on a host that's accumulated 1000s of sessions over
   months. Cap to "today + yesterday's directories" as a first
   pass.
5. **Bypass flag wording.** Confirm `--dangerously-bypass-
   approvals-and-sandbox` doesn't disable the JSONL emission
   (some flags can elide telemetry — verify).

These questions inform the ADR; the adapter itself can be drafted
against the §3/§4 schema without waiting.

## 11. Recommended next step

1. **One-screen ADR** (proposed `036-codex-engine-m4-locallogtail.md`)
   that locks D1 (adopt LocalLogTail for codex M4), D2 (event-
   mapping table = §4), D3 (bypass-mode only for v1; Path A),
   D4 (resume via inode+offset), D5 (input via send-keys per §5),
   D6 (skip hook gateway), with the §10 questions in the open-
   questions section.
2. **Wedge sequence** (each one CI-green and small):
   - W1: `pathresolver.go` + tests against a recorded JSONL corpus.
   - W2: `tailer.go` + tests.
   - W3: `mapper.go` happy-path (text + tool_call + tool_result + usage); leave reasoning + system + cancel synth for W6.
   - W4: `adapter.go` (Start/Stop/HandleInput basics) + minimal `launch_m4_codex.go`; wire into runner.go behind a feature flag.
   - W5: send-keys vocabulary (text, cancel, slash) + LF→CR guard.
   - W6: thought/system surfacing, cancel-watchdog synthesis.
   - W7: resume cursor round-trip (host-runner restart → reattach
     to same JSONL).
   - W8: integration smoke + flip the steward.codex template's
     `driving_mode: M2` → `M4` (with explicit M2 fallback in
     `fallback_modes`).

## 12. Out of scope for this round

- **MCP elicitation bridge in M4.** Path B from §7. Probably
  never; the protocol gap is real and the v1.0.712 bypass-mode
  semantics cover the same ground.
- **Subagent transcripts.** Per ADR-035 D-subagents, MVP ignores
  engine-native subagents across engines. Codex's
  `event_msg.subagent_*` shapes (if any — none observed in the
  138-frame corpus) stay legacy-track.
- **Mobile UX changes.** Codex M4 inherits the existing M4 UX
  (agent feed cards, status strip). No mobile work required for
  parity.

## 13. References

- ADR-027 — `decisions/027-local-log-tail-driver.md`. The original
  design + D9 deferral of codex/gemini/kimi.
- ADR-035 — `decisions/035-antigravity-engine-m4-locallogtail.md`.
  Structural template for the codex ADR; reuse the format wholesale.
- ADR-012 — `decisions/012-codex-app-server-integration.md`. The
  M2 + appserver design; the JSONL is referenced there but the
  M4-LocalLogTail path isn't.
- Reference impl — `hub/internal/drivers/local_log_tail/claude_code/`.
  Every file in the codex adapter has a claude-code sibling worth
  reading before implementing.
- v1.0.712 entry in `docs/changelog.md` — the codex
  `token_count` shape (cumulative + per-turn) we already lift
  from app-server; M4 reuses the rule against the same source
  in the JSONL.

