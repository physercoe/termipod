# claude-code adapter — internal design (W2)

> **Type:** reference
> **Status:** Draft (2026-05-15) — load-bearing for ADR-027 W2 implementation; lives at `hub/internal/drivers/local_log_tail/claude_code/`
> **Audience:** contributors implementing W2; future readers debugging the M4 LocalLogTail path
> **Last verified vs code:** the W2 wedge is in flight; this doc precedes the code so the seams are agreed before commits land

**TL;DR.** The claude-code adapter is the per-engine plug-in
that the LocalLogTailDriver (ADR-027 W1) delegates to. It owns four
responsibilities:

1. **JSONL tail** — finds and follows the on-disk session log
   produced by claude-code, mapping each line to an `AgentEvent`.
2. **Hook routing** — implements `Adapter.OnHook` so the
   host-runner UDS gateway's 9 hook handlers (W5b) drive the state
   machine + post derived events.
3. **State machine** — three states (idle / streaming /
   awaiting_decision) cross-fed by JSONL events and hook signals
   per plan §4.
4. **Input dispatch** — implements `Adapter.HandleInput` so mobile
   text/cancel/escape/mode-cycle/AskUserQuestion-pick actions land
   as `tmux send-keys` per plan §6.

Everything that talks to claude-code's *binary* contracts (JSONL
schema per `docs/reference/claude-code-jsonl-schema.md`-pending /
the `probe-claude-jsonl` probe, hook payloads per
[`claude-code-hook-schema.md`](claude-code-hook-schema.md), tmux
send-keys vocabulary) is here. The driver above stays
engine-agnostic.

---

## Package layout

```
hub/internal/drivers/local_log_tail/claude_code/
├── adapter.go              # Adapter struct + locallogtail.Adapter implementation
├── adapter_test.go         #
├── pathresolver.go         # ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl resolution + poll-for-appearance
├── pathresolver_test.go    #
├── tailer.go               # Line-buffered append-only tail of a JSONL file
├── tailer_test.go          #
├── mapper.go               # JSONL line → AgentEvent (plan §3)
├── mapper_test.go          #
├── state.go                # FSM: idle / streaming / awaiting_decision (plan §4)
├── state_test.go           #
├── hooks.go                # OnHook dispatch (plan §5.B) — per-event handlers
├── hooks_test.go           #
├── paneresolver.go         # claude PID → tmux pane id (plan §12.5)
├── paneresolver_test.go    #
├── sendkeys.go             # HandleInput → tmux send-keys (plan §6.1)
├── sendkeys_test.go        #
├── attentionclient.go      # POST attention_items + long-poll /decide (W5e parked-hook coordination)
└── attentionclient_test.go #
```

Each file owns a narrow surface. `adapter.go` is the only one that
imports the others; everything else is leaf code.

---

## Component responsibilities

### `pathresolver.go`

Resolves the claude-code session JSONL path at spawn time. claude-code
writes the file to `~/.claude/projects/<urlencoded-cwd>/<uuid>.jsonl`
where `<uuid>` is the session id and the encoding URL-encodes path
separators (`/` → `-`). The session file doesn't exist until claude
has produced its first event, so the adapter polls the project
directory for new files until one appears or a timeout fires.

| API | Behaviour |
|---|---|
| `EncodeProjectDir(cwd string) string` | URL-encode-style path slug; `/home/user/proj` → `-home-user-proj`. |
| `WaitForSession(ctx, projectDir, pollEvery, timeout) (path, error)` | Polls `projectDir` for the newest `.jsonl` file by mtime; returns when one appears. |
| `ResolveLatest(projectDir string) (path, error)` | Pure helper used by `WaitForSession`. |

No filesystem watches (no `fsnotify` dep); `pollEvery` defaults to 250ms
which is fast enough for the human-visible boot window.

### `tailer.go`

Append-only line tail of a known-path JSONL file. Opens, seeks to a
configurable starting position (start-of-file for replay; end-of-file
for "live-only"), reads complete lines as they arrive, surfaces them
on a channel.

| API | Behaviour |
|---|---|
| `Tailer{Path, StartAt, PollEvery}` | Constructor fields. `StartAt = TailStart` / `TailEnd` / `TailReplayLast(n)`. |
| `Start(ctx) (<-chan Line, error)` | Returns a receive-only channel of `Line{Bytes, Offset}`. Closes on ctx cancel or unrecoverable read error. |
| `Stop()` | Idempotent; ctx cancellation is the primary stop. |

**Truncation/rotation handling.** claude-code doesn't compact or
rotate session JSONL today (file is written until the session
ends). The tailer still handles truncation defensively: if file size
shrinks between polls, we re-open from the start so we don't read
stale offsets. New files in the project dir (a new session was
opened) are NOT followed — the adapter owns session selection via
the path resolver.

**Polling cadence.** 100ms when the tail is at EOF; 0ms (read until
EOF) when bytes are pending. For replay-N-turns at boot, the
adapter computes a starting offset by scanning backwards for turn
boundaries (a `user.message.content` JSON-string line) before
asking the tailer to seek there.

### `mapper.go`

Pure function: `MapLine(raw []byte) ([]AgentEvent, error)`.

Implements plan §3 verbatim. Returns a *slice* because one
JSONL line (an assistant message with multiple content blocks) can
fan out to multiple AgentEvents. Unknown top-level types emit a
single `system{subtype:"unknown_type"}` event per §9 schema-drift
policy — never panics, never falls back to xterm-VT.

Notable shape branches:

- `user.message.content` — JSON-string ⇒ `user_input`; JSON-array ⇒
  one `tool_result` per block. The `denied` flag is set when
  `content[0].text` starts with `<tool_use_error>`.
- `assistant.message.content[]` — `text` / `thinking` (marker-only,
  `signature_present` for completeness) / `tool_use`.
- `system.subtype` — only `compact_boundary` surfaces today;
  everything else is dropped (cheap chatter).

### `state.go`

Three-state FSM per plan §4. Transitions and their triggers:

```
                                  ┌──────────────┐
                          ┌──────►│     idle     │◄──────────────┐
                          │       └──────────────┘                │
                          │              │                         │
                          │              │ JSONL: tool_use         │
        Stop hook         │              │   OR PreToolUse hook    │
        OR idle_prompt    │              ▼                         │
        Notification      │       ┌──────────────┐                 │
                          │       │  streaming   │                 │
                          │       └──────────────┘                 │
                          │              │                         │
                          │              │ PreCompact hook         │ approval-card
                          │              │   OR PreToolUse(        │ resolved
                          │              │      AskUserQuestion)   │ (driver gets
                          │              │      hook               │  Input("approval"))
                          │              ▼                         │
                          │       ┌──────────────────┐             │
                          └───────│ awaiting_decision│─────────────┘
                                  └──────────────────┘
                                          ▲
                                          │ parked hook returns
                                          │ (PreCompact decision /
                                          │  AskUserQuestion picker resolution)
```

The FSM is implemented as a single goroutine reading from typed
channels (one for JSONL events, one for hook signals) so transitions
are race-free without locks. State change emits a
`system{subtype:"state_changed", from, to}` AgentEvent so mobile can
swap pills (idle pill ↔ streaming spinner ↔ "decision needed").

#### Knobs (plan §8)

| Knob | Default | Notes |
|---|---|---|
| `idle_threshold_ms` | 2000 | safety backstop for missed `Stop`/`Notification{idle_prompt}` |
| `hook_park_default_ms` | 60000 | how long parked hooks wait for a mobile decision before returning the safe-default response |
| `cancel_hard_after_ms` | 2000 | how long after `C-c` we escalate to `kill -INT <pid>` |
| `replay_turns_on_attach` | 5 | last N turns the tailer ships before live mode |

Set in a single `Knobs` struct on the `Adapter`; tunable via the
agent_families.yaml template without recompile.

### `hooks.go`

Implements `Adapter.OnHook(ctx, name, payload)`. One handler
function per claude event name; each:

1. Updates the FSM via a typed channel send.
2. Derives an `AgentEvent` per plan §5.B and posts it through
   `Config.Poster`.
3. For parked hooks (PreCompact, PreToolUse(AskUserQuestion))
   delegates to `attentionclient.go` to insert the attention item +
   long-poll resolution before returning.
4. Returns the JSON-RPC response body (`{}` /
   `{"decision":"block"}` / etc.) — driver passes it through the
   gateway to claude-code.

**Hook → state transition table.**

| Hook | Triggers FSM | AgentEvent emitted | Parks? |
|---|---|---|---|
| `Stop` | → idle | `system{subtype:"turn_complete", final_message}` | no |
| `Notification{idle_prompt}` | → idle (idempotent if already idle) | `system{subtype:"awaiting_input"}` | no |
| `Notification{permission_prompt}` | (no-op; approval channel owns this) | (drop) | no |
| `PreToolUse(other)` | → streaming | (informational, dropped to keep transcript clean — JSONL has the tool_use already) | no |
| `PreToolUse(AskUserQuestion)` | → awaiting_decision | `approval_request{dialog_type:"user_question", questions, current:0}` | yes |
| `PostToolUse` | (no-op; JSONL has tool_result) | (drop) | no |
| `PreCompact` | → awaiting_decision | `approval_request{dialog_type:"compaction", trigger}` | yes |
| `SubagentStop` (agent_type=="") | (no-op; parent-turn dup) | (drop) | no |
| `SubagentStop` (agent_type!="") | (no FSM change) | `system{subtype:"subagent_complete", agent_id, agent_type, last_assistant_message}` | no |
| `UserPromptSubmit` | (no FSM change) | (drop; JSONL has it) | no |
| `SessionStart` | (informational) | `system{subtype:"session_start", source, model}` | no |
| `SessionEnd` | (informational) | `system{subtype:"session_end", reason}` | no |

### `paneresolver.go`

Finds the tmux pane id from the claude PID. Walks `pgrep -af '/claude\b'`,
follows the parent chain via `ps -o ppid`, matches against
`tmux list-panes -aF '#{pane_pid} #{pane_id} ...'`. Multiple matches
disambiguated by `pane_active=1` then newest `session_activity`. See
plan §12.5 for the full procedure.

Single API: `ResolvePane(ctx, claudePID int) (paneID string, err error)`.
Tests inject fake `exec.Command` outputs via a small `Cmd` interface
so we don't shell out during unit testing.

### `sendkeys.go`

Implements `Adapter.HandleInput(ctx, kind, payload)`. Vocabulary
table verbatim from plan §6.1:

| Kind | tmux command |
|---|---|
| `text` (short, no newlines) | `send-keys -t <pane> -l "<text>"; send-keys -t <pane> Enter` |
| `text` (long or multi-line) | `load-buffer -; paste-buffer -t <pane>; send-keys -t <pane> Enter` |
| `slash_command` (snippet bar) | same as `text` |
| `cancel` | `send-keys -t <pane> C-c`; after `cancel_hard_after_ms` ⇒ `kill -INT <pid>` |
| `escape` | `send-keys -t <pane> Escape` |
| `mode_cycle` | `send-keys -t <pane> S-Tab` |
| `action_bar` (Up/Down/Tab/Fn) | `send-keys -t <pane> <name>` |
| `pick_option` (AskUserQuestion) | `send-keys -t <pane> Down × i; send-keys -t <pane> Enter` (per current question; state from prior PreToolUse hook) |

**Picker state.** The adapter stashes the most-recent
`PreToolUse(AskUserQuestion).tool_input.questions[]` plus the
current question index when it transitions to `awaiting_decision`.
On `Input("pick_option")` it consults that state to emit the right
number of `Down` keystrokes; on the last question it sends the
final Enter and clears the stash. The 200ms sleep between
sequential questions (§12.6 MVP simplification) lives here.

**Approval inputs are rejected.** `Input("approval")` returns
an error: approval routing for tool_permission / plan_approval /
compaction is the approval channel's job (`--permission-prompt-tool`
+ parked hook), not send-keys. Defensive — InputRouter shouldn't
deliver these for a LocalLogTail spawn anyway.

### `attentionclient.go` (W5e)

Hub coordination for parked hooks. Two operations:

| Operation | Implementation |
|---|---|
| `InsertAttention(ctx, payload) (id, err)` | POST `/v1/teams/<team>/attention` with the rendered approval_request payload; returns id. |
| `WaitForDecision(ctx, id, timeout) (decision, reason, err)` | Long-poll `/v1/teams/<team>/attention/<id>` until status=resolved or timeout. |

Reuses the existing pattern from `mcpPermissionPrompt` but called
from host-runner rather than the hub itself. Auth: same per-spawn
token the gateway already holds (`hubClient.Token`).

---

## Data flow (end to end)

```
on disk:    ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl  ← claude-code writes
              │
              │  pathresolver finds at boot;
              │  tailer.Start streams Lines
              ▼
         mapper.MapLine ── one Line → 1..N AgentEvents
              │
              │  adapter posts each via Config.Poster (→ hub /v1/.../agent_events → mobile SSE)
              │
              ▼
mobile transcript renders typed cards (text/thinking/tool_use/tool_result/approval/etc.)


parallel inbound from claude-code:
   hook MCP call (mcp__termipod-host__hook_*)
              │  via UDS gateway → HookSink.OnHook
              │
              ▼
    hooks.go per-event handler
       ├─ FSM transition (state.go)
       ├─ AgentEvent → Config.Poster
       └─ if parked:
             attentionclient.InsertAttention
             attentionclient.WaitForDecision (blocks)
             return decision response → gateway → claude-code


parallel outbound to claude-code:
   mobile action → hub /v1/.../input → InputRouter → driver.Input → adapter.HandleInput
              │
              ▼
   sendkeys.go → tmux send-keys (or kill -INT for hard cancel)
```

---

## Tests + fixtures

| Fixture | Source | Used by |
|---|---|---|
| `testdata/probe.events.jsonl` | `hub/cmd/probe-claude-jsonl/` golden output (commit `48c6a93`) | `mapper_test.go` |
| `testdata/hooks/*.json` | `hub/cmd/probe-claude-hooks/` payload corpus (commit `a45d24f`) | `hooks_test.go` |
| Synthetic JSONL streams | inline in tests | `tailer_test.go` |
| Faked `exec.Command` outputs | inline | `paneresolver_test.go`, `sendkeys_test.go` |

The probe wedges already wrote the golden corpora — W2 consumes
them rather than re-deriving from scratch.

---

## Wedge decomposition

W2 is decomposed so each piece compiles + tests on its own; later
wedges glue them together.

| # | Subject | What lands | Depends on |
|---|---|---|---|
| **W2a** | scaffolding + path resolver | `claude_code/` package skeleton, `adapter.go` stub satisfying `Adapter`, `pathresolver.go` + tests | — |
| **W2b** | JSONL tailer | `tailer.go` + tests (polling tail, truncation, EOF live mode, replay-from-offset) | W2a |
| **W2c** | schema mapper | `mapper.go` + tests against `probe.events.jsonl` golden | W2a |
| **W2d** | wire Start: resolver → tailer → mapper → poster | `adapter.Start` end-to-end; integration test with a synthetic JSONL file | W2a/b/c |
| **W2e** | hooks dispatch (observational only) | `hooks.go` + the 7 observational handlers; FSM stub (`state.go` minimal) | W2a |
| **W2f** | state machine | `state.go` full FSM; integrated into Start; state-change AgentEvents | W2d/e |
| **W2g** | pane resolver | `paneresolver.go` + tests with fake exec | — |
| **W2h** | send-keys router | `sendkeys.go` + tests; consumes pane id from W2g; AskUserQuestion picker state lives here | W2e/g |
| **W2i** | attention client + parked hooks | `attentionclient.go` + the 2 parked handlers (PreCompact, AskUserQuestion) wired in `hooks.go` | W2e/f, hub `attention_items` API |

Order on the wire: W2a → W2b/c (parallel) → W2d → W2e → W2f/g (parallel) → W2h → W2i. Each
wedge is its own commit; the W7 launch glue wedge composes after
W2i lands and W5a (gateway wiring in `runner.go`) is in.

---

## References

- [ADR-027](../decisions/027-local-log-tail-driver.md) — Accepted 2026-05-15; D-amend-6 sets the host-runner UDS gateway as the hook surface
- [Plan / frozen contract](../plans/local-log-tail-claude-code-adapter.md) — the §3/4/5/6/8/9/12 sections this doc materializes
- [claude-code-hook-schema.md](claude-code-hook-schema.md) — authoritative hook payload fields
- `hub/cmd/probe-claude-jsonl/main.go` — JSONL schema validator + reference mapper
- `hub/cmd/probe-claude-hooks/` — hook payload corpus
- `hub/internal/drivers/local_log_tail/driver.go` — `Adapter` interface this design plugs into
- `hub/internal/hostrunner/mcp_gateway.go` — `HookSink` seam this adapter satisfies
