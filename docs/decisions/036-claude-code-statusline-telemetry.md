# 036. claude-code statusLine as authoritative M4 telemetry channel

> **Type:** decision
> **Status:** Proposed (2026-05-24)
> **Audience:** contributors
> **Last verified vs code:** v1.0.695 (hub) + claude-code 2.1.150 on host — 16-invocation probe over 159s in `~/cctest` driving idle / single-turn / tool-call / /clear / /model / process-restart scenarios; every payload field decoded; JSONL sibling `~/.termipod-statusline-probe.log` retained.

**TL;DR.** claude-code emits a periodic, structured JSON snapshot on
every status-line refresh — model, context-window size + utilisation,
cost, effort, thinking mode, fast mode, rate-limit windows,
session-id, transcript path, version, and more. The shape was probed
on-host and verified stable; cadence is calm (~10s avg, max ~50s
idle, occasional 0.3s turn-end doubles); concurrency is non-existent
(one process per fire). M4 LocalLogTail today reverse-engineers a
small subset of these from the on-disk JSONL via a fragile
prefix-family heuristic for the context-window size and a hardcoded
literal for the engine version. We adopt the statusLine stream as
the **authoritative** source for M4 claude-code telemetry: install a
host-runner-owned shim (`host-runner status-fire`) as the workdir's
statusLine command, post the JSON to the per-spawn UDS gateway as a
new `status_line` AgentEvent kind, and let the mapper prefer
statusLine-sourced fields over JSONL-derived ones. As a load-bearing
side effect, the stream gives us in-band `/clear` detection
(session-id rotation), eliminates the `claudeModelContextWindow`
heuristic, and unlocks signals we currently can't render at all
(cost, effort, thinking, rate-limit headroom).

## Context

### What we synthesise from the JSONL today (M4 claude-code)

Two synthetic events back the entire AppBar chip strip for an M4
claude-code agent:

- **`session.init`** — adapter posts one on first observed usage
  frame. Payload = `{engine, model, cwd, version: "claude-code"
  (literal — not the real binary version), session_id (basename of
  JSONL file)}`. Source:
  `hub/internal/drivers/local_log_tail/claude_code/adapter.go:307-349`
  <!-- verify symbol hub/internal/drivers/local_log_tail/claude_code/adapter.go maybeEmitSessionInit -->.
- **`usage`** — emitted per assistant message decoded from
  `message.usage` in the JSONL. Payload = `{input_tokens,
  output_tokens, cache_read, cache_create, model, context_window?}`.
  Source:
  `hub/internal/drivers/local_log_tail/claude_code/mapper.go:138-181`
  <!-- verify symbol hub/internal/drivers/local_log_tail/claude_code/mapper.go usageFromMessage -->.

The `context_window` field is the fragile piece. Because the JSONL
doesn't carry it, resolution uses a three-tier cascade
(`CLAUDE_CODE_MAX_CONTEXT_TOKENS` env override → known-legacy
overrides → prefix-family heuristic). Source:
`hub/internal/drivers/local_log_tail/claude_code/mapper.go:183-220`
<!-- verify symbol hub/internal/drivers/local_log_tail/claude_code/mapper.go claudeModelContextWindow -->.
The heuristic is documented in
`[reference_claude_code_context_window_resolution]` (memory): every
new Anthropic model ship that breaks the prefix family forces a hub
patch. v1.0.671 was the most recent such patch.

### What claude-code's statusLine actually emits (verified)

claude-code's `~/.claude/settings.json` or
`<workdir>/.claude/settings.local.json` accepts a `statusLine` block
of shape:

```json
{"statusLine": {"type": "command", "command": "<path-to-script>"}}
```

On each refresh claude pipes a structured JSON to the script's stdin
and renders the script's stdout as the status row. Verified shape
(2.1.150, 2026-05-24 probe):

```json
{
  "session_id":      "<uuid>",
  "session_name":    "<auto-derived after a few turns, else empty>",
  "transcript_path": "/home/.../<session_id>.jsonl",
  "cwd":             "/home/...",
  "version":         "2.1.150",
  "fast_mode":       false,
  "exceeds_200k_tokens": false,
  "model": { "id": "claude-opus-4-7", "display_name": "..." },
  "workspace": { "current_dir", "project_dir", "added_dirs": [] },
  "effort":      { "level": "xhigh" },
  "output_style":{ "name": "default" },
  "thinking":    { "enabled": true },
  "context_window": {
    "context_window_size":  1000000,
    "total_input_tokens":   24611,
    "total_output_tokens":  15,
    "used_percentage":      2,
    "remaining_percentage": 98,
    "current_usage": { "input_tokens", "output_tokens",
                       "cache_creation_input_tokens",
                       "cache_read_input_tokens" }
  },
  "cost": { "total_cost_usd": 0.1211, "total_duration_ms": 117162,
            "total_api_duration_ms": …, "total_lines_added": 0,
            "total_lines_removed": 0 },
  "rate_limits": { "five_hour": { "used_percentage": 24,
                                  "resets_at": 1779640200 },
                   "seven_day": { "used_percentage": 33,
                                  "resets_at": 1779764400 } }
}
```

### What the probe established (host, 2026-05-24, 16 fires / 159s)

1. **Cadence is calm.** Mean ~10s; gaps in [0.3s, 50s]. The 0.3s pair
   is a turn-end double (identical payload fired twice ~0.3s apart).
   Idle fires arrive every 8–50s with no apparent driver beyond the
   TUI's own refresh tick. Not a flood; no need for aggressive
   debouncing beyond a 1s identical-payload dedupe.
2. **No concurrency.** Each fire is a fresh subshell tree (16 unique
   PIDs and 16 unique PPIDs over the probe). The shim handler
   doesn't need reentrancy protection.
3. **`session_id` ≡ `basename(transcript_path, ".jsonl")`**, always
   and exactly. This matches `engineSessionID` captured at
   `adapter.go:295`
   <!-- verify symbol hub/internal/drivers/local_log_tail/claude_code/adapter.go engineSessionID -->.
   The two streams agree; statusLine just sees rotation events
   *earlier* than the JSONL tail does.
4. **`context_window.context_window_size` is always present and
   authoritative.** 1,000,000 in every probe row (both for
   `claude-opus-4-7` and `claude-sonnet-4-6[1m]`). The `[1m]` tier
   suffix on `model.id` is NOT a reliable tier signal — it only
   appears for some models (sonnet-4-6 shows it, opus-4-7 doesn't)
   despite both having a 1M window. **Use `context_window_size`
   directly; ignore the suffix.**
5. **`version: "2.1.150"`** is real and stable across the run — the
   replacement for our hardcoded `"claude-code"` literal at
   `adapter.go:333`.
6. **`/clear` rotates `session_id` within the same process, no cost
   reset.** Process-cumulative meter for `cost.total_cost_usd` and
   `cost.total_duration_ms`; only a fresh claude process resets
   them. Implication: **`/clear` is invisible to today's JSONL-tail
   adapter until the next event lands on the new file**; statusLine
   tells us instantly.
7. **`/model` does NOT rotate `session_id`.** Model swap mid-session
   shows up as a `model.id` change in the next statusLine frame
   (~5–15s latency from the swap itself).
8. **`effort.level` is model-owned, not user-owned.** xhigh on
   opus-4-7, high on sonnet-4-6 with no operator input — claude
   chooses a default per model.
9. **`rate_limits` is a brand-new signal** we didn't anticipate.
   Two windows (5-hour rolling, 7-day rolling) with `used_percentage`
   and `resets_at` (epoch seconds). Anthropic's actual plan limits,
   exposed authoritatively. Completely unavailable today — the
   highest-value new field for steward reasoning ("batch now or wait
   for reset?").
10. **`lines_added / lines_removed` were 0 throughout the probe**
    despite a tool-using turn. Suggests they count Edit/Write deltas
    only, not Read — narrow signal, MVP can drop.

### What `~/.claude/settings.json` vs `.claude/settings.local.json` give us

The probe used `~/cctest/.claude/settings.local.json` and claude
honored it (fires landed). This matches our existing assumption in
`hub/internal/hostrunner/hooks_install.go:22`
<!-- verify file hub/internal/hostrunner/hooks_install.go -->
("Other top-level keys (permissions, model, statusLine, …) are not
touched" — the merge-don't-clobber rule). So the per-workdir
`.claude/settings.local.json` we already own as part of the ADR-027
hooks install is the right home for our statusLine entry — no need
to touch user-global settings.

## Decision

### D1. Install the statusLine command in `<workdir>/.claude/settings.local.json` on M4 claude-code spawn

Reuse the merge-don't-clobber primitive at
`installClaudeHooks(workdir, hookFireExe, udsSocket)`
<!-- verify symbol hub/internal/hostrunner/hooks_install.go installClaudeHooks -->;
extend it to also seed a `statusLine` block. The block is marked
with the same `_termipod_managed: true` marker the hook blocks use,
so a future teardown drops only what we own. **Operator pre-existing
`statusLine` is preserved by wrap-and-passthrough**: if a
`statusLine` is already present and not marked `_termipod_managed`,
we record the operator's command and the shim invokes it after
posting to our gateway, using the operator's stdout as the rendered
status text. (Pure write-skip would silently disable telemetry; pure
clobber would surprise operators. Wrap is the only honest option.)

### D2. The command is a new host-runner shim — `host-runner status-fire`

Mirrors the existing `hook-fire` subcommand pattern at
`hub/cmd/host-runner/main.go:88`
<!-- verify symbol hub/cmd/host-runner/main.go hook-fire -->.
New subcommand `status-fire`, new package
`hub/internal/statusfire/run.go` (sister to
`hub/internal/hookfire/run.go`
<!-- verify file hub/internal/hookfire/run.go -->).

Per-invocation: reads stdin → validates JSON → POSTs to the
per-spawn UDS gateway as a `status_line` tools/call → prints a
single line to stdout (e.g. `termipod ✓`, or the wrapped operator
script's output per D1). Flags: `--socket` (UDS path, baked into
the settings.local.json command line at spawn time). On error
(socket gone, payload malformed) the shim degrades silently —
status-line failures must not break claude's UI.

### D3. The gateway emits a new AgentEvent kind: `status_line`

The per-spawn UDS gateway gains a `status_line` handler that posts a
single `AgentEvent{kind: "status_line", producer: "agent", payload: <verbatim
JSON>}` to the hub. **Per-spawn 1s identical-payload dedupe** in
the handler — keyed by SHA(payload) of the previous emit — drops
the turn-end double (verified pattern: identical payloads ~0.3s
apart at every turn boundary). The dedupe MUST NOT swallow frames
where `session_id` changed even if the rest matches; in practice
session_id change implies many other fields change too, so a SHA
match across a session_id rotation is unobservable, but make the
dedupe explicit-tolerant.

### D4. `status_line` is a periodic-snapshot event, not a lifecycle event

session.init stays the once-per-conversation lifecycle event
(emitted at adapter start AND on session_id rotation — see D5).
`status_line` is the periodic-snapshot channel: chips on mobile read
their per-field values from the *latest* `status_line` payload, a
reducer-over-events shape. This keeps lifecycle consumers (resume,
audit, session table) clean and avoids fake session.init re-emits
on every model swap or effort tick.

### D5. session.init also fires on `session_id` rotation; the tailer re-points

The current adapter captures `engineSessionID` once at start
(`adapter.go:295`) and tails one JSONL file for the spawn lifetime.
`/clear` within the running claude process mints a new
`session_id` + a new JSONL file; today's adapter keeps tailing the
old one until respawn. **statusLine gives us the rotation signal in
the same frame as the new `transcript_path`.** On detected
rotation:

1. Re-emit `session.init` with the new `session_id`. Mobile already
   re-renders chips on session.init; new conversation is the right
   semantic.
2. Cancel the old JSONL tailer; start a new one at the path from
   `payload.transcript_path`.
3. The hub's existing `captureEngineSessionID` handler picks up the
   new session_id from the re-emitted session.init and stamps it on
   `sessions.engine_session_id`, so the resume path threads the
   correct cursor on respawn.

This fixes a latent bug that exists today regardless of whether the
chips ever change. ADR-014 (resume cursor) and ADR-027 §D9 are both
satisfied — we're widening session-rotation observability, not
changing the contract.

### D6. Mapper precedence: statusLine-sourced fields override JSONL-derived ones

Three-tier cascade for each chip-bearing field:

1. **statusLine-sourced** (latest `status_line` payload within last
   N seconds) — authoritative.
2. **JSONL-derived** (today's `usage` event from per-message
   `message.usage`, today's adapter `cwd`/`session_id` capture) —
   first-fallback.
3. **Heuristic** (existing `claudeModelContextWindow`, hardcoded
   `version: "claude-code"`) — last-fallback, for the cold-open
   race before the first statusLine fire and for older claude
   versions that don't ship statusLine.

The mapper does not delete the heuristic — the JSONL-derived path
stays the floor. We relegate, not remove. This matches the
verify-don't-guess discipline: a future claude that stops emitting
statusLine fields silently must not blank out the chips.

### D7. `rate_limits` rides on `status_line` (not its own event kind)

`rate_limits` is a *field* of the statusLine payload, not a separate
event from claude's perspective. Mobile reads it from the latest
`status_line` payload's `rate_limits` block, same reducer as the
other chip fields. (Open: a future cross-engine rate-limit
abstraction might justify a normalised event kind. Out of scope for
this ADR.)

**`resets_at` is timezone-agnostic Unix-epoch seconds.** No TZ anchor
need (or can) be encoded in the protocol: the wire field is an
int64, and the wall-clock-rendering TZ is a chip-side concern.
Mobile MUST render in the device's local TZ (with relative form
"in 4h 38m" for short horizons, absolute "resets Mon 03:00" for
horizons past a few hours). Probe samples show the 5h window
is *rolling* (quantized to 10-min boundaries, not aligned to any
TZ midnight: observed 16:30 UTC and 06:40 UTC); the 7d window
*did* land on a clean UTC hour boundary (03:00 UTC) in the single
captured sample but one sample can't distinguish "Anthropic anchors
7d to clean hours" from "user's first request happened to fall on
a clean hour". Either way the chip renders the same way.

### D8. Two cost scopes ship side-by-side: process (from statusLine) AND session (hub-computed)

statusLine's `cost.total_cost_usd` is **process-cumulative** (probe
verified across 7 process boundaries on 2026-05-25): it grows
across `/clears` and `/model` swaps within the same claude process
and resets to 0 on every respawn (resume / restart / fresh). Claude
maintains no persistent USD ledger across processes — the value is
recomputed live in-process from token counts × current rates and
discarded at process exit.

claude's own `/usage` command, by contrast, displays a
**session-cumulative** USD (preserved on most-recent resume,
inconsistent on older-session resume — see §Resolved Q3 below). It
also imputed: subscription users see USD in `/usage` even though
their account isn't billed per-token, because /usage applies the
public API rate table to local JSONL token counts.

**Both numbers are useful and they aren't redundant.** Mobile
ships **both chips side-by-side**:

| Chip | Source | Scope | Resets when |
|---|---|---|---|
| **Process: $X** | latest `status_line.cost.total_cost_usd` per agent | This claude process only | Process respawn |
| **Session: $Y** | Hub-side aggregation: sum of `agent_events.kind='usage'` token counts for this session_id × an in-hub pricing table | This whole conversation regardless of resumes | Never (our session is more consistent than /usage's — every resume preserves) |

Reading both at once tells the director what mode they're in:
- $X = $Y → fresh cold session, no resumes
- $X < $Y → this process is resuming something with prior cost
- $X = 0 with $Y > 0 → just resumed, no turn yet
- $X > $Y → bug (shouldn't happen; investigate)

Both chips MUST carry the "imputed" disclaimer in their tooltip
(subscription users aren't actually paying these dollars; the
numbers are estimates against the public API rate sheet).

Per-session cost is computed hub-side — see D10 for the pricing
table policy.

### D9. status_line is NOT load-bearing for any existing chip

The contract: removing the statusLine install (e.g. by an operator)
must leave today's chip set working at the pre-ADR-036 fidelity.
Concretely: model + context_window pct fall back to the JSONL
`usage` path with the prefix heuristic; version chip reverts to the
literal `"claude-code"`. Only NEW chips (cost, effort, thinking,
fast_mode, rate_limits, exceeds_200k_tokens, session_name) hide
when no statusLine frame has been seen. This is the "blank > wrong"
discipline applied to the new fields.

### D10. Pricing table is hot-loadable config with an embedded fallback

Computing session-cumulative cost (D8 chip 2) requires a model →
rate mapping the hub applies to aggregated `agent_events.usage`
token counts. The table MUST NOT be hardcoded Go constants — when
Anthropic ships new rates, operators must be able to update without
rebuilding or restarting.

**Layout (three-tier resolution, first hit wins):**

1. **On-disk operator override.** Default path:
   `<hub-data-dir>/pricing/claude.yaml` (override via env
   `TERMIPOD_PRICING_CLAUDE`). Hot-reloaded on access via mtime
   check (cheaper than fsnotify; cadence is fine because cost
   chips render at most every few seconds and an mtime stat is
   sub-millisecond). On parse error, fall through to tier 2 and
   emit a hub-level warning audit row (operator misconfigured the
   file — don't silently use defaults; warn AND keep serving).
2. **Embedded default.** `hub/internal/pricing/claude_default.yaml`
   compiled in via `//go:embed`. Ships with rates current as of
   the release date; the file header carries the snapshot date so
   operators can spot drift.
3. **Per-model fallback.** Unknown model in BOTH tiers → degrade
   the chip pair: hide the session-cost chip ("blank > wrong" per
   D9 discipline); the process-cost chip still works because
   statusLine ships USD verbatim. Hub emits one warning audit row
   per unknown-model-first-sighting per spawn.

**Schema (YAML; one block per model):**

```yaml
# Anthropic Claude API pricing — USD per 1M tokens
# Source: https://www.anthropic.com/pricing (snapshot as of date below)
version: 1
snapshot_date: 2026-05-25
models:
  claude-opus-4-7:
    input_per_million: 15.00
    output_per_million: 75.00
    cache_read_per_million: 1.50
    cache_write_per_million: 18.75
  claude-sonnet-4-6:
    input_per_million: 3.00
    output_per_million: 15.00
    cache_read_per_million: 0.30
    cache_write_per_million: 3.75
  # …
```

The session-cost chip's tooltip MUST display the active table's
`snapshot_date` ("rates as of 2026-05-25") so the user can spot
when their config is stale.

**What this is and isn't.** This is a `behaviour-is-data` slot
([blueprint A6](../spine/blueprint.md)) for pricing specifically —
it does NOT make the cost computation operator-defined. The CODE
that walks `agent_events.usage` and multiplies tokens × rates is
still in `hub/internal/pricing/compute.go`. Only the RATES are
swappable. An operator overriding to malformed-but-parseable rates
(e.g. negative numbers) gets garbage on the chip but no security
risk — same trust model as any host-local config.

## Consequences

**Positive.**

- **Eliminates the `claudeModelContextWindow` heuristic** as the
  primary signal. Anthropic ships a new opus or sonnet variant on a
  non-prefix-family default? No hub patch needed; the chip lights up
  on first turn.
- **Fixes /clear blindness** in the M4 adapter — a real bug present
  today regardless of any chip work.
- **Real version string** in `session.init` (replaces the literal
  `"claude-code"`).
- **Unlocks five new chip fields** (cost, effort, thinking,
  fast_mode, rate_limits, exceeds_200k_tokens, session_name) that
  the JSONL doesn't carry at all.
- **Operationally cheap** — reuses the settings.local.json merge
  primitive and the UDS gateway pattern we already own for hooks;
  the shim is sister-code to `hook-fire`.

**Negative.**

- **claude-version coupling.** statusLine's payload shape is not a
  documented contract; Anthropic can rename / drop fields without
  notice. Mitigated by the three-tier cascade (D6) and "blank >
  wrong" (D9) — silent fallback if a field disappears, never a
  wrong value. Cost: chips silently revert to the heuristic when
  Anthropic moves things around.
- **Operator surprise on settings.local.json** — we now write a
  `statusLine` block alongside `hooks`. Wrap-and-passthrough (D1)
  contains the surprise but doesn't eliminate it. Documented in the
  hooks-install module comment.
- **One more host-runner subcommand** to maintain (`status-fire`),
  one more shim binary call per claude status refresh (~10s
  cadence). Trivial overhead but real.
- **Cost chip needs honest naming.** "Agent cost" not "session cost"
  is the price of telling the truth about claude's process-cumulative
  semantics. A user running claude for 8 hours through 5 /clears
  sees one growing meter; UI must NOT pretend it's per-conversation.

**Now possible / forbidden.**

- The next M4 engine that ships a status-line equivalent (codex,
  kimi, future antigravity revisions) can reuse the `status-fire`
  shim shape — engine-agnostic plumbing.
- ADR-027 §D9 stays correct; this is additive (a new event kind, a
  new shim) not a replacement.
- The mapper's heuristic block at `mapper.go:183-220` is now
  fallback-tier code, not primary. It MUST NOT be deleted until
  every fielded claude-code version reliably ships statusLine
  (today: 2.1.150+ confirmed; older versions unknown).

## Open questions (resolve during plan execution)

1. **statusLine fires during a long mid-turn?** The probe used only
   short turns. If statusLine fires during a 30s+ tool-heavy turn,
   the cost / context-window chip updates in flight feel livelier.
   If it only fires at turn boundaries, chips feel snapshotty.
   Affects W5 only (mobile chip aesthetics); not a blocker.
   **Verify in W1 by adding a long-turn scenario to the smoke.**
2. **Does claude install statusLine if `~/.claude/settings.json`
   has its own block?** The merge order matters when both files set
   `statusLine`. Our test had only the workdir-local file; need to
   verify that workdir-local wins (or merges) when user-global also
   defines it. **Verify in W1 by adding a global-file-already-set
   scenario.**

## Resolved (2026-05-25, post-Phase-A smoke)

- **Q3 — Cost on `claude --resume`?** Resolved: **two scopes, both
  correct, both useful.** Three independent within-process `/clear`
  captures (rows 11→12, 40→41, 63→64) confirm statusLine's
  `cost.total_cost_usd` is **process-cumulative** and is NOT touched
  by `/clear` or `/model` — it only resets when the claude PROCESS
  restarts (resume / cold / crash). All seven captured process
  boundaries showed cost reset to 0.

  But claude's own `/usage` TUI command shows a **session-cumulative**
  USD that DOES preserve across most-recent-session resume: probe row
  53 captured $0.064365 in a fresh process; `/usage` screenshot taken
  later in that session showed $0.0640 (same value, rounded display).
  Both numbers are imputed against the public API rate sheet
  (subscription users aren't actually billed per-token, but /usage
  displays the imputed USD anyway). The /usage source is the JSONL
  transcript itself (no USD field anywhere in JSONLs — 391 lines
  swept, zero USD keys — so /usage walks tokens × current rates on
  every render).

  One subtle case left open: resuming an OLDER (not most-recent)
  session shows /usage cost = 0, contradicting the preserved-on-
  most-recent-resume behaviour. We don't have visibility into
  claude's /usage source to know why. Our hub-side session
  aggregation (D10) walks `agent_events.usage` directly and is
  deterministic across ANY resume — arguably more consistent than
  /usage's behaviour.

  Phase B W4 ships BOTH chips side-by-side (D8 chip pair) so the
  director can cross-check. Pricing table is hot-loadable per D10.
- **Q4 — `rate_limits.resets_at` timezone?** Resolved:
  **TZ-agnostic Unix epoch; render in device-local TZ.** Decoding
  the captured epochs: 5h-window samples landed at 16:30 UTC and
  06:40 UTC (10-min-quantized rolling window, not anchored to any
  TZ midnight); the single 7d sample landed at 03:00 UTC (clean
  hour, but one sample doesn't distinguish "Anthropic anchors 7d
  to clean hours" from "user's first request happened to fall on a
  clean hour"). Either way the chip renders in device-local TZ.
  Folded into D7.

## References

- **Probe artefacts.** `/tmp/termipod-statusline-probe.py` (the
  script) + `/home/ubuntu/.termipod-statusline-probe.log` (16-row
  capture). Both throwaway; not committed.
- **Code we extend.**
  `hub/internal/hostrunner/hooks_install.go` (settings.local.json
  merge primitive),
  `hub/internal/drivers/local_log_tail/claude_code/adapter.go`
  (session.init, engineSessionID),
  `hub/internal/drivers/local_log_tail/claude_code/mapper.go`
  (usage, claudeModelContextWindow),
  `hub/cmd/host-runner/main.go` (subcommand dispatch),
  `hub/internal/hookfire/run.go` (shim package to mirror).
- **Related ADRs.**
  [027](027-local-log-tail-driver.md) (M4 LocalLogTail — the
  channel we enrich),
  [014](014-claude-code-resume-cursor.md) (engine_session_id
  semantics — unchanged; we now detect rotation in-band),
  [010](010-frame-profiles-as-data.md) (behaviour-is-data
  precedent — statusLine is a code-side channel, not a data-side
  frame profile, because the payload schema isn't an Anthropic-
  published contract we'd want to codify as YAML).
- **Memory.**
  `reference_claude_code_context_window_resolution` (the
  maintenance burden we're relegating).
