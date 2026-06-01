# Agent-run analysis mode — overview dashboard + accurate log navigation

> **Type:** plan
> **Status:** In progress (2026-06-01) — **P0 shipped** (digest + turn index,
> incremental fold, canonical-error reconciliation, digest read endpoints,
> `after_ts`/`kind` params). Spec for upgrading the **Insights** surface *into*
> the analysis mode (a foldable overview dashboard over a navigable run log —
> insight *is* analysis, so there is no separate page), plus an operator-facing
> **OTLP trace** export, both backed by the per-run digest + turn index
> ([ADR-038](../decisions/038-per-run-event-digest.md)). P0b–P3 not yet started.
> **Audience:** contributors
> **Last verified vs code:** v1.0.783

**TL;DR.** A director needs, for a finished or idle run, both an **overview**
("did it work, how many turns, what did it cost, what failed") and the
ability to **navigate the log accurately** to the moments that matter. Today
neither is solid: per-run insight is inconsistent and undercounted
(discussion §14), and the per-agent Insights view is sparse. This plan
**upgrades the Insights view into the analysis surface** — a foldable
overview dashboard on top, the navigable transcript below (insight *is*
analysis; no second page) — driven by one canonical [per-run event
digest](../decisions/038-per-run-event-digest.md). The digest supplies the
overview stats *and* the navigation anchors (error/tool/turn `seq`s + total
count), so tapping a stat jumps to that moment in the log and the log shows a
true "event N of M" position. The same first-class turn index also projects
directly to **OTLP** (trace = session, span = turn, child span = tool call),
so an operator can point the hub at a trace backend (Phoenix / Jaeger) and
get waterfalls, latency, and failure analysis for free.

## Why now

- **Per-run insight is wrong, not just thin** (discussion §14): disjoint
  "error" definitions across surfaces, telemetry counted over the loaded
  window only, engine-dependent zeros. A canonical digest is the fix.
- **The navigation primitives already exist and are now correct**: the lens
  filters, the right-edge minimap, the turn stepper, and the convergent
  index seek (v1.0.782–783) that lands exactly on a row. What is missing is a
  *count* and a *structure index* to drive them — which the digest provides.
- **Insight *is* analysis** — rather than a second page that conveys the same
  meaning, the existing (sparse) **Insights** surface becomes the analysis
  mode: a foldable overview dashboard over the navigable log.

## Shape

**Insight *is* analysis — there is no separate "Analyze" page.** The existing
`View ▾ → Insights` surface (today sparse) *becomes* the analysis surface: a
foldable overview dashboard over the navigable log. We do not add a second
route that conveys the same meaning.

```
View ▾ → Insights   (the analysis surface; available for any run)
┌─ Run report ▴ (foldable) ───────────────┐
│ ✓ done · 47 turns · 6m12s · $0.12        │
│ ⚠ 3 errors · tools 18/20 · model breakdown│
│ Errors ▾   Tools ▾   Turns ▾   (tap=jump)│
├──────────────────────────────────────────┤
│ event 1240 / 5000                  ▕│▏    │  ← monotonic position + minimap
│ … transcript log (lens, stepper) … ▕│▏    │
│                                    ▕▮▏    │
└──────────────────────────────────────────┘
```

- **Foldable overview dashboard** (collapses to a one-line summary so the log
  gets full height): the digest as a report card + the structure index
  (error taxonomy, tool success/fail, turn list).
- **Navigable log**: the same full-screen `AgentFeed` (`dense: false`), now
  with a **monotonic** position ("event N of M" from `event_count`) and a true
  minimap position indicator.
- **Inherits Feed's lenses + context-jump, completed by the digest.** Insight
  carries the same lens filters (All / Text / Turns / Tools / Errors), stepper,
  and "view in full transcript" context-jump. The difference is *data scope*:
  Feed filters the loaded window (fine for live follow); **Insight's filters /
  structure index are digest-backed (full-run, complete)** — "Errors" lists
  *every* error in the run (not just loaded ones), and each jumps via the
  random-access loader. So the filters and the context-jump aren't just
  supported — the digest makes them complete and accurate across the whole run.
- **Jump-to-context**: every dashboard entry (an error class, a tool, a turn)
  taps to seek the log to that `seq` via the convergent index seek.
- **Availability — any run.** Because the digest is maintained incrementally
  (always current), Analyze is *not* gated to idle/terminated; a live run
  shows the same surface with a subtle "as of <ts> · live" label that
  refreshes. Frozen-ness is a label, not a gate.

### Loading model (Insight never loads the whole run)

Two windowed models, neither loading the full log:

- **All view — a bounded sliding window.** The `ListView` renders only the
  currently-loaded window (a few hundred events). **Position comes from the
  digest** (`event N of M`, minimap) — *not* the list extent. Scrolling an
  edge pages the adjacent block (keyset `before` / `after_ts`); **a jump
  *resets* the window** to a fresh block fetched *around* the target seq/ts
  (random-access), rather than growing the list by walking from the tail
  (which is Feed's prepend-only model). Memory stays bounded; a jump relocates
  the window in O(log n).
- **Filtered view — a server/digest-backed keyset *listing* of just the
  matches**, *not* a client filter of the loaded window (which only sees
  loaded events and can't be full-run). By lens: **Turns** paginate
  `agent_turns`; **Errors** read the digest's complete (sparse) error list;
  **Tools / Text** use a **`kind`-filtered keyset listing** on the events
  endpoint (a *new* filter param — none exists today; the index supports it).
  Each filtered row taps "view in full" → switches to All and resets its
  window around that `seq` for context. This makes filters **full-run
  complete** (matching the digest), fixing today's loaded-window-only filter.

## Phases

### P0 — Hub: digest + turn index, maintained incrementally (ADR-038) — ✅ SHIPPED
**Done (v1.0.783 tree):** migrations `0049_agent_event_digests` +
`0050_agent_turns`; the shared `digestFolder` (`digest_fold.go`) used by both
the brute-force backfill and the incremental POST fold (`digest_store.go`),
pinned to a shared Go/Dart vector (`testdata/digest_canonical_vector.json`);
the fold runs best-effort in its own transaction after the insert with
read-path staleness repair (`digestIsStale`); the canonical-error union as both
Go (`canonicalErrorClass`) and SQL (`canonicalErrorSQLPredicate`); digest read
endpoints `GET …/agents/{agent}/digest` + `…/sessions/{session}/digest`
(rollup); `after_ts` + `kind` params on the events list; the terminal-hook
`outcome` finalization; and `/v1/insights` adopting the canonical-union
`total_errors` so insights, the transcript lens, and the digest reconcile
(rather than a whole-run digest-sum, which would regress the windowed axis —
see ADR-038 §5). Tests: brute==incremental, lazy backfill, endpoint backfill,
session rollup, kind/after_ts params, insights↔digest reconciliation. The
original spec follows.

The data substrate. New `agent_event_digests` (per-agent scalar rollups +
canonical-error count + a mergeable latency histogram) **and** `agent_turns`
(the turn index — one row per turn). **Maintained incrementally** by folding
each event into the digest + open/close turn rows in the same transaction as
the `agent_events` insert; the idle (`onPreAgentIdle`) / terminal
(`stopSessionInternal`) hooks reconcile + finalize outcome. Canonical error =
the transcript-lens union (Go = source of truth). The latency histogram is **fixed log-scale (exponential) buckets** (OTel
exponential-histogram aligned, pure-Go, mergeable by summing counts), fed from
the **computed wall-clock turn duration** (`agent_turns.end_ts − start_ts`),
which exists for every engine — not the engine-reported `turn.result.
duration_ms` (claude-only). Reads: `GET …/agents/{agent}/digest` and
`…/sessions/{session}/digest` (the ts-ordered rollup of the session's agents).
Refactor `/v1/insights` to **sum the in-scope digests** (percentiles from
merging the histograms). *Tests: incremental digest == a brute-force scan at
every watermark; error union matches a shared Go/Dart vector; session rollup
== sum of agents; insights-sum == legacy scan.* Two small events-endpoint additions for the
loading model: an **`after_ts` forward-window param** (session scope — the
`(session_id, ts)` index already supports `ts > ? ASC`; agent scope already
has `since`), and a **`kind`/lens filter param** (keyset-paged) so a filtered
Insight view is a full-run server-side listing, not a client filter of the
loaded window. (No `kind` filter exists today — verified; the index supports
it.)

### P0b — `turn.start` event + emission (ADR-038 §3) — 🟡 ACP shipped
The `turn.start {turn_id, ts}` boundary event + `turn_id` on `turn.result`,
**emitted in the driver at the input/prompt-dispatch boundary it already
controls**, with the active `turn_id` **stamped on tool events** (exact OTLP
parenting; ts-window grouping is the synthesis fallback). The hub
**synthesizes** turns for engines not yet emitting `turn.start`, so the index
and OTLP work everywhere from day one. **Done:** the ACP driver (M1) emits it
at `session/prompt` — the single clean injection point covering codex / gemini
/ kimi — and stamps `turn_id` on its `tool_call` / `tool_result` /
`tool_call_update` and `turn.result` events (`driver_acp.go` `beginTurn` /
`stampTurnID` / `endTurn`; test `TestACPDriver_EmitsTurnStartAndStampsTurnID`);
the contract is recorded in `docs/spine/protocols.md` §5. **Remaining:** the
claude M2/M4 drivers emit at input-send (synthesis covers them meanwhile —
accurate for claude, which emits one `turn.result` per prompt).

### P1 — Mobile: upgrade the Insights view into the analysis surface — 🟡 session surface shipped
The `View ▾ → Insights` view *becomes* the analysis surface (no new route): a
**foldable overview dashboard** (report card from the **session** digest) over
the full-screen `AgentFeed`. Available for **any run**, with the live/"as of
<ts>" label. This replaces the sparse Insights content directly — insight *is*
analysis, so the numbers now match the transcript (the view reads the digest).
**Done:** `getSessionDigest`(+cached) hub client + `sessionDigestProvider`;
`RunReportCard` (foldable digest dashboard — outcome / turns / duration / cost /
errors / tool success / models / latency, error-stat → `onJumpToSeq`);
`SessionAnalysisView` (card over `AgentFeed(dense:false)` + pull-to-refresh),
wired into `SessionChatScreen`'s Insights tab; widget tests
(`run_report_card_test.dart`). **Remaining:** the project-agent sheet's Insights
tab is agent-scoped (no session) and still uses the sparse `InsightsPanel` — an
agent-digest `RunReportCard` variant gives it parity (follow-up; `GET
agents/{agent}/digest` already exists). Wiring the digest `event_count` into the
`AgentFeed` position ("N of M") + monotonic minimap is folded into **P2** with
the random-access loader (the position bar and the loader share the count).

### P2 — Structure index + filtered views → jump (random-access nav)
Implement the two-model loading (see *Loading model* above). **All view** = the
bounded sliding window with the **random-access loader** (reset-around-anchor
via `before` + `since` / `after_ts`); the minimap scrubber maps a position to
the nearest anchor seq (no raw ordinal lookup). **Filtered views** = keyset
listings of just the matches (Turns ← `agent_turns`; Errors ← digest; Tools /
Text ← the `kind`-filtered endpoint), each row "view in full" → reset the All
window around its `seq`. Render the digest's error taxonomy / tool list / turn
index as the tappable dashboard sections. This is the "navigate accurately"
payoff — overview and log bound together, full-run complete.

### P3 — Operator OTLP export (ADR-038 §4)
The hub's optional OTLP exporter (`--otlp-endpoint`, off by default) projects
`agent_turns` → spans: **trace = session, span = turn, child span = tool
call**, OTel GenAI attributes, deterministic span IDs. **Batch export at idle
*and* terminal** (both watermark points) — idempotent via the deterministic
IDs, so a long-running agent still exports without live streaming. Operator
points it at Phoenix / Jaeger.

### Later (post-MVP)
- Per-session **dense ordinal** materialized (vs. the ts-rank used here) if a
  stored ordinal becomes necessary.
- Tier-2 OLAP: Parquet export → DuckDB (see discussion doc Part II) + MCP
  `transcript.summary` / `transcript.query` — a future ADR-B.
- Per-turn **live** OTLP streaming (vs. the idle+terminal batch export).

## Resolved (was open)

- **Launch points** → **no separate route: the `Insights` view *is* the
  analysis surface** (insight = analysis). Reachable wherever Insights already
  is (the `View ▾` switcher; an "Insights/Analyze" action on the run/session
  card can deep-link to it).
- **Live runs** → **available for any run** (live label), not gated to
  idle/terminal — a free consequence of incremental maintenance.
- **Digest staleness UI** → a subtle "as of <ts> · live" label only while the
  run is live/idle (none for terminated); pull-to-refresh. Near-real-time
  because the digest is incremental, so the label reads "ongoing", not "stale".
- **Latency histogram / `turn.start` rollout / OTLP cadence / tool→turn** —
  resolved in ADR-038 (fixed log buckets from computed duration; driver-emitted
  `turn.start` + tool `turn_id` stamping; idle+terminal batch export).

- **Feed vs. Insights are distinct access patterns, not redundant.** **Feed**
  = live SSE follow (tail + scroll-up lazy-load, newest-anchored). **Insight**
  = static **random-access** — land at any anchor and page locally either way,
  no walk-from-tail. The DB supports any-jump efficiently (index range scans on
  `(agent_id, seq)` / `(session_id, ts)`, no `OFFSET`); the only addition is
  the `after_ts` session param (P0) + the window-around-anchor loader (P2).

## Open questions

- *(none blocking P0)* — exact histogram bucket boundaries and `turn.start`
  rollout order are tracked in ADR-038; both are tuning/sequencing, not forks.
