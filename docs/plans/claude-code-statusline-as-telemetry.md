# claude-code statusLine as telemetry

> **Type:** plan
> **Status:** In flight (2026-05-25) — **Phase A COMPLETE** (W1+W2+W3 shipped v1.0.696-698); **Phase A.5 COMPLETE** (W3.5 shipped v1.0.699); **Phase B W4-b + W4-a + W4-c COMPLETE** (v1.0.700 hub pricing infrastructure; v1.0.701 mobile chip pair); **W5 COMPLETE** (v1.0.702 rate_limits chip pair); W6 pending
> **Audience:** contributors
> **Last verified vs code:** v1.0.702 (hub + mobile) + claude-code 2.1.150 on host
> **Implements:** [ADR-036](../decisions/036-claude-code-statusline-telemetry.md)

**TL;DR.** Wire claude-code's statusLine JSON into M4 LocalLogTail
as an authoritative telemetry channel. Six wedges across two phases:
hub-only (W1–W3) puts the data on the wire and fixes /clear
blindness; mobile (W4–W6) renders the new chips. The load-bearing
wins are in Phase A — W1 captures the stream, W2 retires the
context-window heuristic, W3 fixes a latent /clear bug. Phase B is
pure UX polish on top.

## Phasing

Ordered so each phase is independently shippable. Phase A (hub-only)
adds the channel without changing any mobile rendering — chips stay
at pre-ADR-036 fidelity until Phase B ships. If Phase B is delayed
or descoped, Phase A still delivers W2's heuristic relegation and
W3's /clear fix on its own merit.

### Phase A — hub-side channel (~780 LOC + ~25 tests)

- **W1 — `host-runner status-fire` shim + settings.local.json install
  + gateway handler + emit `status_line` event (with 1s identical-
  payload dedupe).** ✓ Shipped v1.0.696-alpha. Pre-W3 hook
  `StatusLineSink` interface defined; nil-sink fallback ships the
  default "post-and-move-on" behaviour. Probe-time scenarios
  (long mid-turn cadence, user-global-settings-already-set) deferred
  to on-host smoke alongside W3.
  - New subcommand registered at
    `hub/cmd/host-runner/main.go:88` (mirror `hook-fire`'s case
    branch). New package `hub/internal/statusfire/run.go` (sister to
    `hub/internal/hookfire/run.go`). Reads stdin → validates JSON →
    POSTs JSON-RPC `tools/call status_line` to the per-spawn UDS
    gateway → prints one-line status to stdout.
  - **Wrap-and-passthrough for operator-set statusLine.** During
    settings.local.json merge: if existing `statusLine` block has no
    `_termipod_managed: true` marker, store its `command` as
    `_termipod_wrapped_command` in our marker block; the shim
    invokes it after our POST and uses its stdout as the rendered
    status text. If no prior block exists, our shim prints
    `termipod ✓` (or similar — pick during W1 review).
  - Extend `installClaudeHooks(...)` in
    `hub/internal/hostrunner/hooks_install.go` to also install the
    statusLine entry (rename to `installClaudeAgentConfig` if the
    second responsibility is awkward — or keep two siblings and call
    both from the launch path). The atomic-rename merge pattern is
    reusable verbatim.
  - Gateway handler in `hub/internal/hostrunner/mcp_gateway.go`
    (or sibling) registers a `status_line` tool. Body: per-spawn
    `lastSHA + lastEmitTS` mutex-guarded; if SHA(payload) ==
    lastSHA AND now - lastEmitTS < 1s, drop. Else POST
    `AgentEvent{kind:"status_line", producer:"agent",
    payload:<verbatim JSON>}` to the hub and update last*.
  - **Tests.**
    `hub/internal/statusfire/run_test.go` — shim happy path, malformed
    stdin (silent stderr + exit 0), socket-gone (silent exit 0).
    `hub/internal/hostrunner/hooks_install_test.go` — extend with two
    new cases: cold install of statusLine, wrap-existing-statusLine
    preserves operator command under `_termipod_wrapped_command`.
    Gateway handler test: 1s dedupe drops identical, lets through
    different.
  - **Probe verification.** Add a long-turn (30s+) scenario to the
    smoke and a "user-global settings.json already has statusLine"
    scenario (the two open questions §1+§2 from the ADR). Record
    findings in this plan's status block.

- **W2 — adapter consumes `status_line`; retire
  `claudeModelContextWindow` to fallback-tier.** ✓ Shipped
  v1.0.697-alpha. **Plan-loose-talk reinterpretation
  ([[feedback_plan_narrative_loose_talk]]).** Plan literal said
  "the adapter's runLoop handles incoming gateway-side `status_line`
  posts the same as JSONL-mapped events (posting via
  Poster.PostAgentEvent with kind=`status_line`)". Re-posting would
  have duplicated the row W1's gateway already POSTs to
  agent_events; structural intent is "statusLine snapshots reach
  the adapter for in-process consumption". Ship: adapter implements
  `OnStatusLine` (W1's `StatusLineSink` seam), caches the latest
  payload under RWMutex, overrides session.init.version + usage.
  context_window inline before the existing post path. Mapper-side
  `MapStatusLine` deferred — the gateway already emits the
  status_line AgentEvent verbatim with no transformation; a mapper
  function would add a hop without semantic value at this layer.
  - In
    `hub/internal/drivers/local_log_tail/claude_code/mapper.go`:
    add a `MapStatusLine(raw []byte) (*MappedEvent, error)` that
    decodes the verified statusLine shape and emits a `status_line`
    MappedEvent (kind passthrough; mobile consumes the payload
    verbatim per ADR-036 D7).
  - In
    `hub/internal/drivers/local_log_tail/claude_code/adapter.go`:
    the adapter's runLoop handles incoming gateway-side
    `status_line` posts the same as JSONL-mapped events (posting via
    Poster.PostAgentEvent with kind=`status_line`).
  - In `usageFromMessage` (mapper.go:138-181): when emitting the
    usage event, attach the `context_window` field via a new helper
    `contextWindowForUsage(model, lastStatusLine)`:
    - First try `lastStatusLine.context_window.context_window_size`
      (authoritative).
    - Else fall back to `claudeModelContextWindow(model)` (today's
      heuristic — keep, don't delete).
    - Else omit (today's "blank > wrong" behaviour).
  - **Relegate, don't remove.** The
    `claudeModelContextWindow` function header gets a `Deprecated`
    comment noting it's fallback-only when statusLine hasn't fired
    yet. Tests stay green (the heuristic still works for the
    cold-open race + older claude versions).
  - **Version override.** In `maybeEmitSessionInit`
    (adapter.go:307-349): if a statusLine frame has arrived with a
    `version` field, use it instead of the literal `"claude-code"`
    in the session.init payload. (The first session.init might still
    use the literal if statusLine hasn't fired yet — that's fine,
    D5's re-emit-on-rotation gives mobile a chance to re-render
    with the real version once we have it.)
  - **Tests.** Extend `mapper_test.go` with status_line-decode cases
    (authoritative context_window override; absent block falls back
    to heuristic; unknown model with status_line still emits the
    authoritative number). Extend `adapter_integration_test.go` with
    a fixture that interleaves statusLine + usage events and asserts
    the session.init carries the statusLine-sourced version.

- **W3 — session_id rotation handler (fix /clear blindness).** ✓
  Shipped v1.0.698-alpha. Phase A complete.
  - In `claude_code/adapter.go`: maintain `currentSessionID` (init
    from `engineSessionID` at start). When a `status_line` event
    decodes with `session_id != currentSessionID`:
    1. Stop the current JSONL tailer cleanly.
    2. Update `currentSessionID = new`; update `engineSessionID = new`.
    3. Spawn a new tailer rooted at `payload.transcript_path`.
       Fall back to inferring the path via the existing pathresolver
       if the field is missing.
    4. Reset `sessionInitSent = false` so
       `maybeEmitSessionInit` fires again with the new
       session_id (and the latest model + cwd + version from the
       most recent statusLine + usage frames).
  - The session.init re-emit is the contract: mobile re-renders
    chips on session.init, and the hub's existing
    `captureEngineSessionID` handler picks up the new id from the
    payload and stamps it on `sessions.engine_session_id` (so
    `--resume <id>` on next respawn uses the post-/clear id, not the
    stale pre-/clear one).
  - **Test.** New `adapter_session_rotation_test.go`: synthesise a
    statusLine sequence with a `session_id` change, assert the
    tailer re-points + session.init re-emits + the new tailer
    starts from offset 0 of the new file.
  - **/clear safety net.** If statusLine never fires (older claude
    or operator removed the install), the adapter behaves exactly
    as today — no rotation detection, /clear silently breaks the
    tail until respawn. This is the pre-ADR-036 baseline; we don't
    regress it.

### Phase B — mobile chips (~650 LOC + ~25 tests)

- **W4 — TWO cost chips + effort / thinking / fast_mode chips.**
  Cost ships as a PAIR per ADR-036 D8 — process scope (from
  statusLine, resets on respawn) + session scope (hub-computed,
  preserved across resumes). Split into three sub-wedges so the
  pricing-table infrastructure (D10) is testable in isolation
  before the mobile chip lands.

  - **W4-a — Process cost chip (mobile).** ✓ Shipped v1.0.701-alpha
    (commit pending push). `processCostFromEvents` reducer over
    `status_line` events (latest-wins on
    `payload.cost.total_cost_usd`); new `processCostUsd` parameter
    on `_TelemetryStrip` renders `$X.XXXX · process` in green when
    non-null. Tooltip per ADR-036 D8 disclaimer + pair-aware
    cross-reference when the session chip is also visible. Self-
    gates on null (cold-open before first status_line; older
    claude). Wedge size shipped: ~75 LOC.

  - **W4-b — Hub pricing table + session-cost computation.** ✓
    Shipped v1.0.700-alpha (commit pending push). Per ADR-036 D10
    — hot-loadable YAML with embedded fallback. Shipped surface:
    - `hub/internal/pricing/types.go` — `Table` struct +
      `Rate{InputPerMillion, OutputPerMillion, CacheReadPerMillion,
      CacheWritePerMillion}` per model + `SnapshotDate` + `Origin`
      label + `TokenCounts` + `CostFromTokens` + `RateFor`.
    - `hub/internal/pricing/loader.go` — three-tier resolution
      (env-override path → default-disk path → embedded). Caches
      parsed table, invalidates on mtime change. **Warner hook**
      injected by the server (audit-shaped callback) so the
      pricing package stays free of a *server import — operator
      diagnostics surface via warn-log today; full audit-row
      integration pending a hub-global audit channel (no team
      scope fits this row).
    - `hub/internal/pricing/claude_default.yaml` — embedded via
      `//go:embed`; snapshot_date=2026-05-25; opus-4-7 + sonnet-4-6
      live; haiku-4-5 held as a commented sample until verified
      (chip degrades blank for haiku per D9, by design).
    - `hub/internal/pricing/compute.go` — `SessionCost(ctx, db,
      loader, sessionID) (Result, error)`. Result carries TotalUSD,
      per-model Breakdown, per-model Tokens, Missing list,
      SnapshotDate, Origin. Tolerant of missing-model payload
      (dropped silently — historical rows), unknown-model
      (Missing), nil-guards.
    - **Two payload sites** wire the field, both in
      `hub/internal/server/`: (1) inline scalar
      `session_cost_usd_imputed` on `GET /sessions/{id}` for fast
      first-paint; (2) new endpoint `GET /sessions/{id}/cost`
      returning the rich `sessionCostOut` shape (TotalUSD,
      Breakdown, Tokens, Missing, SnapshotDate, Origin, Imputed)
      for the chip tooltip. List-sessions NOT touched (O(N×M) cost
      would balloon).
    - Tests landed: 9 loader + 8 compute + 4 server-integration
      = 21 tests (overshot the ~12 unit + 1 integration estimate
      because the compute table-driven case + nil-guards earned
      their keep). All green.
    Wedge size shipped: ~700 LOC (vs estimate ~280) + 21 tests.
    Overshoot driven by (a) the dedicated cost endpoint instead of
    inlining the rich shape (cleaner separation), (b) the inline
    scalar on the parent GET (was a stretch; cheap enough at this
    layer to keep), and (c) the Warner-hook indirection (kept the
    pricing package import-clean for future engines).

  - **W4-c — Session cost chip (mobile).** ✓ Shipped v1.0.701-alpha
    (commit pending push). Polls `GET /sessions/{id}/cost` on a
    15s timer in `_AgentFeedState`; cached response drives the
    chip (rendered cyan to distinguish from process tile) AND its
    tooltip. `buildSessionCostTooltipFromDetail` composes the
    multi-line tooltip: disclaimer line + per-model breakdown
    (sorted, with token annotations + cache-zero suppression) +
    `snapshot_date (origin tier)` line + missing-models list +
    pair-context cross-reference. `hubClient.getSessionCost`
    swallows errors so a transient hub blip doesn't blank a
    previously-good number. `didUpdateWidget` nukes the cache on
    sessionId flip; in-flight responses dropped on flip too.
    14 widget tests across 3 groups (reducer / tooltip / pair).
    Wedge size shipped: ~115 LOC + 14 tests (vs ~140 LOC + 6 tests
    estimated — overshoot on tests because the tooltip composer
    earned its weight in pinning each rendering branch).

  - **Effort chip**: small badge ("xhigh", "high", "low"). Renders
    only when present.
  - **Thinking chip**: brain icon, present-only when
    `thinking.enabled = true`. Subtle — many users will leave it on.
  - **Fast-mode badge**: present-only when `fast_mode = true`.
    "FAST" badge in opus-orange (Opus Fast indicator).
  - All non-cost chips self-gate per
    [[feedback_self_gating_widget_pattern]].
  - **Tests.** Per-sub-wedge as above; integration test in
    `test/widgets/agent_feed_test.dart` (or sibling) — empty
    payload → no chips; full payload → 4 chips in order; cost
    transitions update both chips independently.

- **W5 — `rate_limits` surface.** ✓ Shipped v1.0.702-alpha. Three
  top-level helpers (`rateLimitsFromEvents` reducer +
  `formatRateLimitResetsAt` formatter + `rateLimitAlarmTier` color/
  severity function) plus two adjacent tiles in `_TelemetryStrip`
  (one per window, each independently tiered). Session-details-
  sheet row deferred — the agent-feed pair is the load-bearing
  surface; session-details-sheet can mirror it later if a user asks.
  Steward-overlay headroom row also deferred (separate file; not a
  blocker for the main chip-pair story). 19 widget tests (reducer
  6 + formatter 7 + alarm 6). Wedge size shipped: ~145 LOC + 19
  tests (vs ~250 LOC + ~10 tests estimated — undershot LOC because
  the existing _TelemetryStrip + _TelemetryTile infrastructure
  carried the visual weight; overshot tests because the formatter's
  branch coverage earned the extra cases).
  - Original spec (still authoritative for the deferred pieces):
    Render a row in the session details sheet AND a compact
    overview-strip entry on the agent feed:
    `5h: 24% (in 4h 38m) · 7d: 33% (resets Mon 03:00)`.
  - Format `resets_at` (Unix epoch, TZ-agnostic; Q4 resolved
    2026-05-25 — see ADR-036 D7) in **device-local TZ**:
    relative form ("in 4h 38m") for horizons under ~3h, absolute
    short form ("resets Mon 03:00") otherwise. Use
    `DateTime.fromMillisecondsSinceEpoch(resetsAt * 1000,
    isUtc: false)` — no UTC assumption baked in.
  - **Alarm tier**: when `used_percentage >= 80`, the row gets an
    amber tint; at `>= 95`, red. The steward overlay also surfaces
    a row when either window crosses 80% so a worker steward can
    pause heavy work near reset.
  - Self-gate: render nothing when `rate_limits` is absent (older
    claude versions).
  - **Tests.** Widget tests + a small formatter unit for the
    relative-time rendering.

- **W6 — `exceeds_200k_tokens` alarm + `session_name` fallback.**
  - When `exceeds_200k_tokens = true`, surface an amber pill on
    the agent feed AppBar: "200K cap exceeded — consider /clear".
    Self-gates on the field.
  - When the session has no user-set name AND `session_name` is
    non-empty in the latest status_line payload, use it as the
    sticky-header fallback (claude's own auto-derived label, e.g.
    "List directory files"). User-set names always win. Don't
    persist claude's name to the hub `sessions` table — read it
    fresh from status_line each render, so /clear's new session
    can show its new claude-derived name without state leaking.
  - **Tests.** Widget tests for both: alarm appears + disappears
    with the bool; sticky-header fallback respects user-set name.

## Effort

| Phase | Wedges | LOC (est.) | Gate |
|---|---|---|---|
| A — hub channel | W1–W3 | ~780 + ~25 tests | shim happy path; dedupe test; rotation test; CI green (✓ done v1.0.696-698) |
| A.5 — mobile filter regression fix | W3.5 | ~150 + ~10 tests | bubble-hide + busy-skip kind-list contract tests (✓ done v1.0.699) |
| B.4 — cost chips (split per ADR-036 D8 + D10) | W4-a/b/c | ~540 + ~24 tests | hub pricing table hot-reload; session-aggregation correctness; chip pair side-by-side renders (W4-b ✓ done v1.0.700; W4-a + W4-c ✓ done v1.0.701) |
| B.5+6 — remaining mobile chips | W4-effort/W5/W6 | ~510 + ~20 tests | widget tests; CI green |
| **Total** | **9** | **~1,980 + ~79 tests** | — |

Phase A is shippable alone — the hub channel + heuristic relegation
+ /clear fix earn their keep without any mobile work. Phase B's
recommended order: **W4-b first** (pricing table is the foundation
that W4-c depends on, and it's hub-only so it can ship + green-CI
before any mobile work). Then W4-a + W4-c as a pair (chip pair is
designed to be read together). Then W5 (`rate_limits`) and W6.

## Open questions inherited from ADR-036

These get resolved during wedge execution, not before. Each is
non-blocking for the wedge that touches it.

1. **statusLine cadence during long mid-turn?** → verify in W1 with
   a 30s+ tool-heavy scenario.
2. **Workdir-local vs user-global statusLine precedence?** → verify
   in W1 by adding a "global file already sets statusLine" scenario
   to the install test.

Resolved (2026-05-25, post-Phase-A smoke; see ADR-036 §Resolved):

- ~~Cost behaviour on `claude --resume <id>`?~~ → **two-scope answer.**
  statusLine's `cost.total_cost_usd` is per-process (resets on
  respawn; confirmed across 7 process boundaries + 3 within-process
  /clear captures). claude's /usage TUI command shows session-
  cumulative USD (preserves on most-recent resume; inconsistent on
  older-session resume). Both are imputed against the public API
  rate sheet. Resolution: W4 ships BOTH chips side-by-side
  (process from statusLine + session from hub-side aggregation
  against a hot-loadable pricing table — ADR-036 D8 + D10). Plan
  W4 split into W4-a/b/c above.
- ~~`rate_limits.resets_at` timezone?~~ → TZ-agnostic epoch; chip
  renders in device-local TZ (folded into D7 + W5).

## Non-goals (post-MVP if ever)

- **Per-session_id slicing of cost across /clear rotations.**
  W4-b's session aggregation keys on the agent's CURRENT session_id
  only — it does NOT diff cost across /clear-rotated session_ids
  within the same conversation. A "show me what THIS /clear
  segment cost vs the prior one" view is a separate ask; defer
  until someone wants it.
- **`lines_added / lines_removed` chip.** Probe showed both stay 0
  even on tool-using turns (suggests only Edit/Write counts).
  Narrow signal; revisit if the field's semantics widen.
- **Normalised cross-engine `rate_limits` event kind.** ADR-036 D7
  keeps it field-scoped to statusLine for now. Cross-engine
  abstraction is a separate ADR if codex / kimi / antigravity ever
  ship comparable signals.
- **statusLine for engines other than claude-code.** The
  `status-fire` shim is engine-agnostic in shape; reusing it for
  another engine is a new wedge against a new ADR. Out of scope.

## References

- [ADR-036](../decisions/036-claude-code-statusline-telemetry.md) — the
  decision + verified probe facts.
- [ADR-027](../decisions/027-local-log-tail-driver.md) — M4
  LocalLogTail skeleton that this enriches.
- [ADR-014](../decisions/014-claude-code-resume-cursor.md) —
  resume cursor semantics that W3's rotation handler preserves.
- **Code we extend.**
  `hub/cmd/host-runner/main.go:88` (subcommand dispatch),
  `hub/internal/hookfire/run.go` (shim package to mirror),
  `hub/internal/hostrunner/hooks_install.go` (settings.local.json
  merge primitive),
  `hub/internal/hostrunner/mcp_gateway.go` (per-spawn UDS gateway
  for new `status_line` handler),
  `hub/internal/drivers/local_log_tail/claude_code/adapter.go`
  (rotation handler + session.init re-emit),
  `hub/internal/drivers/local_log_tail/claude_code/mapper.go`
  (status_line decoder + heuristic relegation).
- **Mobile surface to touch.**
  `lib/widgets/agent_feed.dart` (chip strip — reducer over
  `status_line`), `lib/widgets/session_details_sheet.dart` (rate
  limits row), `lib/widgets/steward_overlay/` (rate limit headroom
  row in the steward overlay).
- **Probe artefacts** (throwaway, not committed).
  `/tmp/termipod-statusline-probe.py`,
  `/home/ubuntu/.termipod-statusline-probe.log`.
