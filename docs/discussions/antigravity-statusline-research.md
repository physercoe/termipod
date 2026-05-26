---
name: Antigravity statusLine research
description: Source-grounded probe of antigravity's `statusLine` mechanism. Confirms `agy` 1.0.2 ships an opt-in command-runner with the same `~/.gemini/antigravity-cli/settings.json` install surface as claude-code's `.claude/settings.local.json`. Captures the verbatim stdin JSON schema fired on a live host (15 top-level keys after live TTY probe — including `plan_tier` + `email` not visible pre-auth — `context_window.current_usage` shape-identical to claude-code's `usage` block; pre-computed `used_percentage`; explicit `exceeds_200k_tokens` + 3-state `agent_state` lifecycle) and compares it to claude-code's ADR-036 statusLine payload to scope an M4 telemetry-parity wedge plan (G1–G6). Deferred — captured for selection later.
---

# Antigravity statusLine research

> **Type:** discussion
> **Status:** Open (2026-05-26) — first commit landed pre-auth (2-fire capture from a non-TTY context); revised the same day with the **post-auth live probe** (42 fires across a 4-min interactive TTY session driving three turns: 2 unique authenticated states, 15 unique top-level keys, full transition graph captured). Pre-auth open questions Q1–Q5 in §7 are now **RESOLVED inline**. No implementation; the six wedges in §6 (G1–G6) are sized but unselected.
> **Audience:** contributors
> **Last verified vs code:** v1.0.716 (hub) + `agy` 1.0.2 on the dev host (binary at `~/.local/bin/agy`, build 182 MB). Two-stage capture on 2026-05-26: **(1) static** — binary strings dump of `BuildStatusLineData` / `StatusLineRunner.Run` / `triggerStatusLineUpdate` symbol set + payload-field tags; `agy --help` confirms surface is data-only (no `--statusline` flag — install via settings.json + `/statusline` slash-command). **(2) live TTY probe** — instrumented script at `/tmp/agy-statusline-probe.py` captured 42 fires (~100 KB of JSONL at `~/.agy-statusline-probe.jsonl`) during a 4-min interactive `agy` session driving 3 turns (boot → 90s idle → list_files tool call → long-prose turn → quit). Conversation id `05e4c81e-e3d3-4a84-8b92-e40f1a604e5a` retained on disk under `~/.gemini/antigravity-cli/brain/<id>/`.

## TL;DR

`agy` 1.0.2 ships a statusLine command-runner **shape-for-shape parity
with claude-code's** — same install field (`statusLine` in the engine's
settings.json), same stdin-JSON contract, similar payload schema, same
auto-disable-on-failures guard rail. The current antigravity M4
adapter (`hub/internal/drivers/local_log_tail/antigravity/`) ignores it
entirely; the session.init it posts carries only `session_id`, and the
mobile session-details sheet renders no engine/model/version/cwd for
antigravity stewards (the same gap codex M2 had pre-v1.0.715).

Three concrete consequences:

1. **The claude-code statusLine wedge (ADR-036) ports to antigravity
   with minimal new code** — the per-spawn UDS gateway + `status_line`
   tool + wrap-and-passthrough installer can be reused; only the
   settings.json path + the mapper precedence rules change.
2. **Antigravity's payload is *richer* than claude-code's in three
   places** — `context_window.used_percentage` (pre-computed, no
   client-side division), explicit `exceeds_200k_tokens` (parity with
   claude-code), and `agent_state` (a lifecycle channel claude-code
   does NOT have: `authenticating` → ??? → ???).
3. **Antigravity's payload is *poorer* than claude-code's in two
   places** — no `cost` field, no `rate_limits` field (verified by
   absence in the captured fires; both are streamed through the
   internal `AgentStateUpdate` protobuf, not the statusLine).

This research scopes four wedges (§6) reproducing the ADR-036 pipeline
for antigravity. Deferred per principal direction; captured here so the
gap surface doesn't vanish.

## 1. What `agy` ships today

### 1.1 settings.json already has the field

The on-disk `~/.gemini/antigravity-cli/settings.json` ships pre-seeded
with an *empty* `statusLine` block on first install:

```json
{
  "enableTelemetry": false,
  "statusLine": {
    "type": "",
    "command": "",
    "enabled": true
  },
  "trustedWorkspaces": ["/home/ubuntu/agytest", "/home/ubuntu/hub-work/antigravity"]
}
```

Three fields, mirroring claude-code's `.claude/settings.local.json`
shape exactly:

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Always `"command"` when populated (empty disables). |
| `command` | string | Absolute path to an executable. agy invokes it with stdin = payload JSON. |
| `enabled` | bool | Soft toggle independent of the `command` value. `false` disables even when a command is set. |

Note the **`enabled` boolean is new** — claude-code's block has no
explicit enable flag (presence of `statusLine.command` = enabled). For
us the difference is invisible (we always set `enabled: true` alongside
`command`).

### 1.2 There is a slash-command surface

From the binary strings dump (`statuslineCommand.{showHelp,setCommand,deleteCommand,setEnabled,toggle}`):

```
/statusline                 Toggle statusline on/off.
/statusline <command>       Configure a custom shell command to render the statusline.
/statusline delete          Delete the custom command and revert to the built-in default.
/statusline on | enable     Enable the statusline.
/statusline off | disable   Disable the statusline.
/statusline help            Show this help message.
```

This is operator-facing. **Operators can change the statusLine from
inside an interactive session.** That's the same wrap-and-passthrough
hazard claude-code has (`~/.claude/settings.local.json` lives outside
hub's control, so the principal can replace our entry with their
own) — the same mitigation applies: wrap-and-passthrough rather than
clobber.

### 1.3 The runtime symbol set mirrors claude-code

```
store.BuildStatusLineData                # marshals the payload
store.NewStatusLineRunner                # constructor (single instance per session)
store.(*Manager).initStatusLine          # boot-time install
store.(*Manager).triggerStatusLineUpdate # fires the runner on state change
store.(*StatusLineRunner).Run            # invokes the command
store.(*StatusLineRunner).Output         # captures stdout (rendered status line)
store.(*StatusLineRunner).ErrorHint      # human-readable error for /statusline help
store.(*StatusLineRunner).recordFailure  # accumulates strikes for auto-disable
```

Same lifecycle:

- One runner instance per agy session.
- Invoked on state changes (`triggerStatusLineUpdate` — verified from
  the strings: `addFromDiff`, `updatePendingApprovalsQueue` lead into
  triggers; we did not enumerate every trigger).
- Output captured from stdout and rendered in agy's TUI footer.
- Failures accumulate. The auto-disable message is verbatim:
  ```
  statusline: auto-disabled after %d consecutive failures. Last error: %s
  Statusline disabled after %d consecutive failures.
    Run /statusline delete to reset, or /statusline enable to retry.
  Statusline script error. Run /statusline delete to reset.
  ```

This matches claude-code's "few-strike auto-disable" model. **Our
script must succeed loudly and silently** (don't print to stderr on
benign events) to avoid the operator seeing a confusing disable
prompt.

## 2. The captured payload — verbatim from the host

Two-stage capture:

- **Stage 1 (pre-auth, 2026-05-26 early)** — 2 fires during `agy
  --prompt-interactive` boot before the TTY-init error
  terminated the run. Authentication never completed; only the
  pre-auth payload shape was visible (§2.1, §2.2).
- **Stage 2 (live TTY, 2026-05-26 later)** — 42 fires across a 4-min
  interactive session in a real TTY. Three model turns + a 90s
  idle window. **All 5 open questions from the first draft now
  RESOLVED** (§7). The post-auth payload schema (§2.3 inventory)
  is fully verified and is the version implementations should
  target.

### 2.1 First fire (pre-version-load)

```json
{
  "cwd": "/home/ubuntu/agytest",
  "session_id": "",
  "conversation_id": "",
  "transcript_path": "/home/ubuntu/.gemini/antigravity/brain/.system_generated/logs/transcript.jsonl",
  "model": null,
  "workspace": {
    "current_dir": "/home/ubuntu/agytest",
    "project_dir": "/home/ubuntu/agytest"
  },
  "version": "",
  "context_window": {
    "total_input_tokens": 0,
    "total_output_tokens": 0,
    "context_window_size": 0,
    "used_percentage": 0,
    "remaining_percentage": 0,
    "current_usage": null
  },
  "exceeds_200k_tokens": null,
  "agent_state": "authenticating",
  "sandbox": {"enabled": false},
  "terminal_width": 80
}
```

### 2.2 Second fire (~0.0s later, version + product loaded)

```json
{
  "cwd": "/home/ubuntu/agytest",
  "session_id": "",
  "conversation_id": "",
  "transcript_path": "/home/ubuntu/.gemini/antigravity/brain/.system_generated/logs/transcript.jsonl",
  "model": null,
  "workspace": {"current_dir": "/home/ubuntu/agytest", "project_dir": "/home/ubuntu/agytest"},
  "version": "1.0.2",
  "context_window": {"total_input_tokens": 0, "total_output_tokens": 0, "context_window_size": 0, "used_percentage": 0, "remaining_percentage": 0, "current_usage": null},
  "exceeds_200k_tokens": null,
  "product": "antigravity",
  "agent_state": "authenticating",
  "sandbox": {"enabled": false},
  "terminal_width": 80
}
```

Note: **`version` flipped `"" → "1.0.2"` and `product: "antigravity"`
appeared between fires.** This confirms the runner re-fires as state
materialises (and not just on a fixed timer). The two fires were back-to-back at the same `ts`
because authentication had not started; in a real session each
state transition will trigger another fire.

### 2.3 Top-level key inventory (verified live, 15 keys)

After the live TTY probe, the canonical key set is **15** (the original
12 plus three that only populate post-auth). Authenticated payload shape:

| Key | Type | First seen | Notes |
|---|---|---|---|
| `cwd` | string | fire #1 | Working directory the process was launched in. |
| `session_id` | string | fire #1 (empty) → #8 (UUID) | Mints once the first user turn lands. **Identical to `conversation_id`** — aliases. |
| `conversation_id` | string | fire #1 (empty) → #8 (UUID) | agy's mutable conversation id. Identical to `session_id`. |
| `transcript_path` | string | fire #1 (placeholder) → #8 (per-conv) | **agy 1.0.2 bug: missing `-cli` segment** — see §2.5. |
| `model` | object\|null | fire #3 | Post-auth shape: `{"id": "Gemini 3.5 Flash (Medium)", "display_name": "Gemini 3.5 Flash (Medium)"}`. Tier is **embedded in the name** with a `(Medium)` suffix — same gotcha as claude-code's `model.id` `[1m]` suffix. The mapper should normalise. |
| `workspace` | object | fire #2 | `{current_dir: <path>, project_dir: <file://-URI>}`. Note the URI vs path asymmetry — `project_dir` becomes a `file://` URI post-auth, not a raw path. |
| `version` | string | fire #2 | agy binary version (`"1.0.2"`). |
| `context_window` | object | fire #1 (zeros) → #10 (numeric) | See §2.4 — richer than claude-code. |
| `exceeds_200k_tokens` | bool\|null | fire #10 (null → `false`) | Same field name as claude-code's. |
| `product` | string | fire #2 | Always `"antigravity"` once populated. Useful as a "this is really agy" defensive cross-check. |
| `agent_state` | string | fire #1 | **3-state vocabulary verified live** (`"authenticating"`, `"idle"`, `"working"`). No `tool_running`/`generating`/`streaming` even during a tool-call turn — state machine is coarser than claude-code. Binary strings hint `loading`/`error` exist; likely error-path-only. |
| `sandbox` | object | fire #1 | `{enabled: false}` because launched without `--sandbox`. |
| `terminal_width` | int | fire #1 | 80 (no TTY) / 198 (real TTY in live probe) — reflects operator's terminal. We can ignore. |
| **`email`** | string | fire #3 | ⚠️ **PII** — operator's Google account email (`"<user>@gmail.com"`). MUST be redacted before persisting or logging. Don't surface on mobile. |
| **`plan_tier`** | string | fire #5 | Subscription tier (`"Google AI Pro"` observed). **claude-code's statusLine does NOT carry this directly** — the closest analogue is the rate-limit chip. Useful for a lightweight chip on antigravity stewards' session-details sheet (see G6). |

### 2.4 The `context_window` block is richer than claude-code's

After fire #10 (first turn complete):

```json
"context_window": {
  "total_input_tokens": 147,
  "total_output_tokens": 59,
  "context_window_size": 1048576,
  "used_percentage": 0.014019012451171875,
  "remaining_percentage": 99.98598098754883,
  "current_usage": {
    "input_tokens": 17541,
    "output_tokens": 59,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 0
  }
}
```

Three observations:

- **`current_usage` is shape-identical to claude-code's `usage` block.**
  Direct contract parity — mobile reducers wrote for claude-code's
  `usage.{input_tokens, output_tokens, cache_*}` can read this without
  translation.
- **`used_percentage` is a raw float, not rounded.** `0.014019...%`
  was observed at 147/1048576 tokens. Mobile chips should round
  client-side to taste.
- **Two scopes coexist** — `total_input_tokens=147` (billed-input-only
  count) vs `current_usage.input_tokens=17541` (gross including system
  context). 100× discrepancy is the expected gemini accounting; mobile
  should display `used_percentage` (which is computed from the right
  numerator) and treat `total_*` as bookkeeping.

**Cost is computable hub-side from `current_usage`.** No engine needs
to report USD; the W4-b pricing infrastructure (`hub/internal/pricing/`)
extended with a `gemini_default.yaml` covers this — see G5 in §6.

### 2.5 The `transcript_path` bug (agy 1.0.2)

Live probe verified: `statusLine.transcript_path` consistently reports
a path with a **missing `-cli` segment**:

| Source | Path |
|---|---|
| statusLine reports | `~/.gemini/antigravity/brain/<conv-id>/.system_generated/logs/transcript.jsonl` |
| Actually on disk | `~/.gemini/antigravity-cli/brain/<conv-id>/.system_generated/logs/transcript.jsonl` |

The statusLine-reported path 404s; the `-cli` path resolves. Confirmed
across all 42 post-auth fires.

**Implication for G2:** do **not** consume `statusLine.transcript_path`
as a file-path source. Use `session_id` / `conversation_id` (both
correctly populated) to drive `pathresolver.go` instead — the adapter
already does this via the workspace-cache route (ADR-035 D3). If a
future agy release fixes the bug, the field becomes consumable as a
redundancy check; until then, ignore.

### 2.6 Cadence: state-diff, not wall-clock (verified)

Across the live probe's 4-min span:

| Window | Duration | Fires observed | Verdict |
|---|---|---|---|
| Boot to first turn (`authenticating` → `idle`) | ~1.6s | 3 | State-transition + async-data fires |
| Post-turn-1 idle wait | **139s** | **0** | No metronome |
| Post-turn-2 idle wait | **54s** | **0** | No metronome |
| Same-state idle (anomalies) | 13–17s | 2 | Likely async background events (mtime polls?) |

A 10s wall-clock cadence (claude-code style) would have fired ~14 times
during the 139s gap. We observed zero. The runner fires on
`triggerStatusLineUpdate` which is invoked by state-diff + occasional
async data arrival. **Better than claude-code's periodic model** —
every fire is meaningful, no debounce needed adapter-side.

### 2.7 What's notably *absent* (and what showed up unexpectedly)

| Field | claude-code statusLine | antigravity statusLine | Source-grounded explanation |
|---|---|---|---|
| `cost` | yes — `{total_cost_usd, total_duration_ms, …}` | **no** (verified across 42 fires) | Cost lives in agy's internal `AgentStateUpdate.CostSummary` (binary symbol `(*AgentStateUpdate).GetCostSummary`). **Computable hub-side from `current_usage` — see G5.** |
| `rate_limits` | yes — `{five_hour, weekly}.{used_percent, resets_at}` | **no** (verified) | Same explanation: agy surfaces this internally via `AgentStateUpdate.CreditUsageSummary` but not via statusLine. |
| `output_style` | yes — `{name}` | **no** | claude-code-only setting. |
| `hook_event_name` | yes — names which lifecycle hook fired | **no** | agy fires on `triggerStatusLineUpdate` without naming the trigger; cadence is state-diff so the trigger is implicit. |
| `agent_state` | **no** | **yes** — verified 3-state vocab `authenticating \| idle \| working` | Unique to antigravity. A lifecycle channel claude-code lacks. |
| `terminal_width` | **no** | yes (80 default / real TTY width) | Operator-render hint; we ignore. |
| `sandbox` | **no** | yes (`{enabled: bool}`) | Reflects `--sandbox` launch flag. Useful for session-details sheet. |
| `product` | **no** | yes (`"antigravity"`) | Defensive cross-check. |
| **`plan_tier`** | **no** | **yes** — `"Google AI Pro"` observed | **Subscription tier** — closest equivalent to claude-code's rate-limit-source. New mobile chip opportunity (G6). |
| **`email`** | **no** | **yes** — operator's Google account email | ⚠️ **PII**. Adapter MUST redact before logging / persisting. Not consumed by mobile. |
| **`current_usage` (nested in `context_window`)** | implicit in `usage` block | **yes** — shape-identical to claude-code's `usage` | Direct contract parity — mobile reducers reuse. |

## 3. Comparison to claude-code (ADR-036)

| Aspect | claude-code (ADR-036, v1.0.696–v1.0.698) | antigravity (proposed) |
|---|---|---|
| **Install path** | `<workdir>/.claude/settings.local.json` (per-workdir) | `~/.gemini/antigravity-cli/settings.json` (per-host, global to all agy processes for the operator) |
| **Wrap-and-passthrough** | Required — operators may already use statusLine | Required — operators may already use it via `/statusline <cmd>` |
| **Install scope** | Workdir-local — touches one file per spawn | **Host-global** — touches one file per host. **Tenant-isolation concern: a multi-tenant host shares the file.** ADR-035 D7 raised the same concern for MCP config and named per-spawn `HOME` isolation as the resolution. |
| **Trigger cadence** | ~10s wall-clock + on state changes; `hook_event_name` names the trigger | **State-diff only (verified live)** — 139s and 54s idle windows captured zero fires. Fires on every internal state delta (model load, plan_tier load, conversation mint, exceeds_200k determination, etc.) — no metronome, no debounce required. |
| **stdin shape** | JSON object, ~14 keys | JSON object, **15 keys** (verified live, post-auth) |
| **Stdout contract** | Renders verbatim in the TUI footer | Same |
| **Auto-disable** | "Wraps any error trigger ... silently disables" | "auto-disabled after %d consecutive failures" (binary string) — explicit strike counter |
| **Payload — `cwd`/`workspace`/`version`** | Yes | Yes; **but `workspace.project_dir` is a `file://` URI post-auth**, not a path |
| **Payload — `model`** | `{id, display_name}` | **`{id, display_name}` (verified)**. Strings identical (e.g. both `"Gemini 3.5 Flash (Medium)"`). Tier is **embedded in the name** — split on `(` to extract. |
| **Payload — `context_window`** | Raw tokens only | **Pre-computed `used_percentage` (raw float)** + nested `current_usage` shape-identical to claude-code's `usage` block. Strictly richer. |
| **Payload — `exceeds_200k_tokens`** | Yes | Yes (verified: null pre-turn → `false` post-turn) |
| **Payload — `cost`** | Yes | **No** (computable hub-side from `current_usage` — see G5) |
| **Payload — `rate_limits`** | Yes | **No** (`plan_tier` is the closest equivalent — see G6) |
| **Payload — `agent_state`** | No | Yes — 3-state vocabulary verified `authenticating \| idle \| working` |
| **Payload — `transcript_path`** | Yes — the JSONL the M4 reader tails | **Reported but wrong** — missing `-cli` segment (agy 1.0.2 bug, §2.5). Do not consume. |
| **Payload — `email`** | No | **Yes — PII; must be redacted** by adapter before logging |
| **Payload — `plan_tier`** | No | **Yes** — subscription tier string |

## 4. What the current antigravity M4 adapter does *not* know

`hub/internal/drivers/local_log_tail/antigravity/adapter.go:193` posts:

```go
_ = a.Poster.PostAgentEvent(ctx, a.AgentID, "session.init", "agent",
    map[string]any{"session_id": convID})
```

…and nothing else on session.init. Mobile's session-details sheet
(post-codex v1.0.715) reads `payload.engine`, `payload.model`,
`payload.version`, `payload.cwd`, `payload.permission_mode`, and
`payload.engine_version`. **For antigravity stewards today, those rows
are empty.**

Mobile chip surfaces that *also* read statusLine telemetry:

- **Context-fill ring** (`agent_feed.dart`) — reads `usage.context_window`
  + computes `used_percent` client-side. With antigravity's
  `context_window.used_percentage` we could **skip the client-side
  divide**.
- **200K alarm leading strip tile** (v1.0.703) — reads
  `exceeds_200k_tokens`. We have parity.
- **Cost chip + rate-limit chip pair** (v1.0.701–v1.0.703 + v1.0.713) — read
  `cost.total_cost_usd` and `rate_limits.*`. **antigravity statusLine
  carries neither.** Cost chip would silently hide; rate-limit pair
  same. Future work — see §6 W-G4.

## 5. What this unlocks

1. **Wire-parity session-details sheet for antigravity stewards.** Same
   intent as v1.0.715 was for codex: surface engine/model/cwd/version
   on the session-details sheet so principal can tell at a glance what
   they're talking to.
2. **Authoritative engine version (no more `"antigravity"` literal).**
   ADR-036 D6 — statusLine's `version` field replaces the legacy
   engine-name-as-version placeholder.
3. **Pre-computed context-fill percentage.** Mobile saves one division
   and gets the engine's authoritative percentage (which may differ
   from naive `input/window` if antigravity accounts for prompt
   caching or model-specific overhead).
4. **Lifecycle channel from `agent_state`.** Authenticating vs idle vs
   error is information mobile presently has no source for. Antigravity
   uniquely offers this; **claude-code does NOT have this channel** —
   so we'd be designing a new mobile contract.
5. **Sandbox-mode visibility.** `sandbox.enabled` could decorate the
   session-details sheet permission row (parallel to permission-mode
   for claude-code).
6. **`product` cross-check.** Defensive — confirm the binary at
   `bin: agy` is actually antigravity and not a renamed gemini or
   stale binary that has lost its branding.

## 6. Wedge plan (sized, unselected)

Ordered by priority. F is the rate-limit chip pair that already
shipped for codex (v1.0.713); this section uses **G-prefix** to avoid
collision with the codex audit's F-prefix.

Six wedges total (G1–G6, ~430 LOC + 22 tests aggregate). G5 + G6 were
added after the live TTY probe surfaced `current_usage` (cost gap is
closeable hub-side) and `plan_tier` (new chip opportunity).

### G1 — Reuse the ADR-036 UDS gateway + status_line tool for antigravity (~120 LOC + 6 tests)

Mirror the claude-code statusLine install for antigravity:

- New helper `installAntigravityStatusLine(home, gatewayUDSPath)` in
  `hub/internal/hostrunner/launch_m4_antigravity.go` runs at the same
  point we already install settings.json (`preTrustWorkspaceAntigravity`).
- Wraps any pre-existing operator `statusLine.command` under
  `_termipod_managed: true` + `_termipod_wrapped_command: <orig>` and
  invokes a single shim binary that POSTs payload to the per-spawn UDS
  + execs the wrapped command and merges its stdout (same wrap-and-passthrough
  pattern as ADR-036 D1).
- The shim **is the same binary** as claude-code's
  (`cmd/termipod-statusline-shim`). It already accepts `--wrap` and
  posts to a UDS. Only the install code differs.
- The per-spawn UDS gateway (`hub/internal/hostrunner/uds_gateway/`)
  gains a no-op flag — it already exposes `status_line` as a callable
  tool; route the antigravity post to the same handler.
- Hub emits a new AgentEvent `kind: status_line` with
  `producer: "agent"` and `payload: <verbatim statusLine JSON>`.
  Identical to claude-code's contract (ADR-036 D3).

**Risk:** the install target file is **global**, not per-workdir. A
second concurrent antigravity spawn would clobber the first's
statusLine block. Mitigation options:
- (a) Use per-spawn `HOME` isolation (ADR-035 D7 already calls this
  out as the preferred mode).
- (b) Compose `statusLine.command` with a shim that branches on `cwd`
  to the correct UDS path (more complex, single global install).
- (c) Single-host single-spawn assumption (ship as-is; document).

Pick during plan-stage; this research doc is neutral.

### G2 — Antigravity adapter consumes status_line + overrides session.init fields (~100 LOC + 5 tests)

Mirror `adapter.go:160-180` in claude-code. Cache `latestStatusLine` on
the antigravity adapter. Override at session.init build time:

- `payload.version` ← `statusLine.version` (replaces no-op default).
- `payload.cwd` ← `statusLine.cwd` (cross-check / supersedes launch-time).
- `payload.context_window` ← `statusLine.context_window` (pre-computed
  percentages, mobile chip consumes directly).
- `payload.exceeds_200k_tokens` ← `statusLine.exceeds_200k_tokens`.
- `payload.model` ← `statusLine.model`, **with name normalisation** — split on
  `(` to extract base model + tier suffix (e.g. `"Gemini 3.5 Flash"` +
  `"Medium"`).

**Source-handling rules verified by the live probe:**

- **Drop `statusLine.transcript_path` entirely** — agy 1.0.2 reports a
  path missing the `-cli` segment (§2.5). Adapter keeps its
  workspace-cache resolution (ADR-035 D3).
- **`workspace.project_dir` is a `file://` URI** post-auth — strip the
  prefix before exposing on session.init.
- **Redact `payload.email`** — antigravity statusLine carries the
  operator's Google account email (§2.3). Adapter must NOT include
  this in the AgentEvent payload. Optional: emit a one-time
  `audit_events` row with the hash for forensic correlation.

Precedence rule (same as ADR-036 D6): statusLine-sourced fields
override transcript-derived ones when a statusLine frame has arrived
within the last ~60s; fall back to transcript-derived or launch-time
values otherwise. **Blank > wrong** — never *replace* a known good
value with an empty statusLine field (ADR-036 D9 "status_line is NOT
load-bearing").

### G3 — Session-details sheet parity (~60 LOC + 3 tests; codex-v1.0.715 sibling) (hub-only)

Independent of G1/G2. Same as v1.0.715 was for codex: at adapter init
time, post `session.init` with `engine/workdir/permission_mode/engine_version`
populated from launch-time fields. No statusLine dependency — just
parity with the codex AppServerDriver session.init shape.

- `Engine` ← `"antigravity"` (the family name).
- `Workdir` ← the resolved spawn workdir (already known).
- `PermissionMode` ← `"dangerously-skip-permissions"` or
  `"interactive"` (mode flag at launch).
- `EngineVersion` ← `runVersion(agy, "--version")` (already 1.0.2 today).

Mobile `_permModeColor` may need an additional vocabulary case
(`dangerously-skip-permissions` → red, `interactive` → green) — verify
against `session_details_sheet.dart` before committing to the colour
mapping.

### G4 — agent_state lifecycle chip (~40 LOC + 2 tests; new mobile contract)

Status-bar mini-chip rendering `agent_state` value. New on mobile;
gates off `payload.status_line.agent_state` so only antigravity
spawns show it (claude-code doesn't have this field, so the chip
is naturally engine-agnostic by absence).

**Live-probe-verified vocabulary** is 3 states only:
`authenticating | idle | working`. Binary strings hint `loading` /
`error` may exist on error paths; design the mobile reducer with
**enum + unknown-fallback** (degrade to no-chip on unknown values
rather than rendering raw string).

Most useful diagnostic case: spotting `agent_state: authenticating`
that doesn't transition (auth wedged, no model loaded — operator
can resolve by quitting + relaunching).

### G5 — Hub-computed cost chip from `current_usage` (~100 LOC + 4 tests)

**Closes the cost-chip parity gap without engine support.** Antigravity
statusLine doesn't carry cost (§2.7), but `context_window.current_usage`
is shape-identical to claude-code's `usage` block (§2.4). The hub
pricing infrastructure (`hub/internal/pricing/`) already aggregates
`agent_events.usage` by session_id for claude-code (ADR-036 D8, W4-b
shipped v1.0.700) — extending to antigravity is a YAML + dispatch
addition:

- Add `hub/internal/pricing/gemini_default.yaml` with the gemini-1.5/2.5
  rate card (input / output / cache-creation / cache-read $ per token).
- Hot-loadable per the 3-tier convention (operator override at
  `<dataRoot>/pricing/gemini.yaml` → `//go:embed` default → degrade
  per-key).
- Extend the engine-dispatch in `pricing/compute.go` to detect
  antigravity sessions (`agent.kind == "antigravity"`) and route to
  the gemini table.
- The `/sessions/{id}/cost` endpoint inherits the same shape
  automatically.

Mobile cost chip already engine-agnostic — reads
`payload.session_cost_usd_imputed` for both claude-code and antigravity
once the hub field populates.

### G6 — `plan_tier` chip on session-details sheet (~30 LOC + 2 tests)

Lightweight chip on the session-details sheet rendering
`statusLine.plan_tier` (e.g. `"Google AI Pro"`). Closest antigravity
equivalent to claude-code's rate-limit source. No mobile reducer
work — direct passthrough. Gates off
`payload.status_line.plan_tier != null` so claude-code stewards
don't see an empty row.

## 7. Open questions (verify before / while implementing) — Q1–Q5 RESOLVED 2026-05-26

1. ~~**`transcript_path` resolution timing.**~~ **RESOLVED
   (2026-05-26):** the field *does* rewrite to per-conversation form
   after the first prompt (fire #8) — but the **path is wrong by
   exactly the `-cli` segment** (agy 1.0.2 bug, §2.5). Adapter must
   NOT consume this field. Workspace-cache resolution from ADR-035
   D3 remains the authoritative path source.

2. ~~**`agent_state` enum vocabulary.**~~ **RESOLVED (2026-05-26):**
   only 3 states observed across 4 min and 3 turns:
   `authenticating | idle | working`. Coarser than claude-code's
   state machine. G4 designs for 3-state minimum + unknown-fallback;
   `loading`/`error` (binary strings) remain unverified, treat as
   error-path-only.

3. ~~**Trigger cadence.**~~ **RESOLVED (2026-05-26):** state-diff
   only — 139s and 54s idle windows captured **zero** fires. Two
   same-state re-fires at ~13–17s gaps (likely async-event triggers,
   not metronome). G2 needs no debounce.

4. ~~**`model` payload shape post-auth.**~~ **RESOLVED (2026-05-26):**
   `{"id": "<name> (<tier>)", "display_name": "<name> (<tier>)"}` —
   both fields identical, tier embedded in name with `(...)` suffix.
   G2 mapper splits on `(` to extract base name + tier.

5. ~~**Concurrency / tenant isolation.**~~ Still open. The live
   probe used a single agy process; the multi-spawn-shares-settings.json
   risk remains uninspected. Resolution paths (a/b/c in §3) unchanged;
   pick during the rollout plan.

6. **Cost / rate-limits via the AgentStateUpdate protobuf?** Binary
   has `(*AgentStateUpdate).{GetCostSummary, GetCreditUsageSummary}`
   — antigravity *has* this data internally; it just isn't piped
   to statusLine. Whether we can access it via a separate channel
   (e.g. a different gRPC stream agy exposes locally) is unknown.
   Out of scope for this research; relevant if we want full chip
   parity later.

## 8. Side-band notes

- **agy binary scope cross-check.** `which agy` →
  `/home/ubuntu/.local/bin/agy`; `agy --version` → `1.0.2`. The
  binary is a Go-built monolith (~182 MB). Symbol prefix
  `google3/third_party/jetski/cli/...` confirms it's the same product
  ADR-035 references; "jetski" is the internal codename.

- **Probe artefacts retained.**
  `/tmp/agy-statusline-probe.py` (probe v2, captures ts_wall +
  ts_mono_ns + payload sha256), `/tmp/agy-statusline-test.sh` (helper
  with install/restore/summarize/tail subcommands), and
  `~/.agy-statusline-probe.jsonl` (42 fires, ~100 KB JSONL). Settings.json
  restored from `/tmp/agy-settings-backup.json` post-probe; verified
  clean. Conversation `05e4c81e-e3d3-4a84-8b92-e40f1a604e5a` retained
  on disk for any future replay needs.

- **Re-running the probe.** `/tmp/agy-statusline-test.sh install` then
  open agy in a real TTY, drive the 3-turn recipe in §0 of this
  doc's predecessor message, then `/tmp/agy-statusline-test.sh
  restore && /tmp/agy-statusline-test.sh summarize`.

- **No memory links here.** Per the project memory note on doc-spec
  enforcement, body files live at `~/.claude/...` which isn't in the
  repo checkout; inline historical context instead.

## 9. References

- Code: `hub/internal/drivers/local_log_tail/antigravity/` (current
  adapter); `hub/internal/hostrunner/launch_m4_antigravity.go` (W7
  install glue — extension point for G1).
- Related ADRs: [027](../decisions/027-local-log-tail-driver.md) (M4
  LocalLogTail);
  [035](../decisions/035-antigravity-engine-m4-locallogtail.md)
  (antigravity engine M4 wiring; D7 per-spawn HOME isolation);
  [036](../decisions/036-claude-code-statusline-telemetry.md)
  (claude-code statusLine — the template this research mirrors).
- Plans: [claude-code statusLine as telemetry](../plans/claude-code-statusline-as-telemetry.md)
  (Phase A + B; the wedge plan G1/G2 is the antigravity reflection).
- On-host: `agy` 1.0.2; `~/.gemini/antigravity-cli/settings.json`.
