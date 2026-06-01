# Agent-run analysis mode — overview dashboard + accurate log navigation

> **Type:** plan
> **Status:** Proposed (2026-06-01) — spec for upgrading the **Insights**
> surface *into* the analysis mode (a foldable overview dashboard over a
> navigable run log — insight *is* analysis, so there is no separate page),
> plus an operator-facing **OTLP trace** export, both backed by the per-run
> digest + turn index ([ADR-038](../decisions/038-per-run-event-digest.md)).
> Not yet started.
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
- **Jump-to-context**: every dashboard entry (an error class, a tool, a turn)
  taps to seek the log to that `seq` via the convergent index seek.
- **Availability — any run.** Because the digest is maintained incrementally
  (always current), Analyze is *not* gated to idle/terminated; a live run
  shows the same surface with a subtle "as of <ts> · live" label that
  refreshes. Frozen-ness is a label, not a gate.

## Phases

### P0 — Hub: digest + turn index, maintained incrementally (ADR-038)
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
== sum of agents; insights-sum == legacy scan.* Also add an **`after_ts` forward-window param**
to the events list endpoint for **session scope** (the `(session_id, ts)`
index already supports `ts > ? ASC`; agent scope already has `since`), so
Insight-mode jumps (P2) can page forward from a mid-session anchor.

### P0b — `turn.start` event + emission (ADR-038 §3)
Add the `turn.start {turn_id, ts}` boundary event and `turn_id` on
`turn.result`, **emitted in the driver at the input/prompt-dispatch boundary
it already controls** — uniform, not per-protocol work (the ACP driver stamps
it at `session/prompt` for codex/gemini/kimi in one place; claude M4/M2 at
input-send). The driver also **stamps the active `turn_id` on tool events**
(exact OTLP parenting; ts-window grouping is the fallback). The hub
**synthesizes** turns for engines not yet emitting `turn.start`, so the index
and OTLP work everywhere from day one. Record `turn.start` + `turn_id` in
`docs/spine/protocols.md` event vocabulary. *(Drivers/protocols — sequence
with P0.)*

### P1 — Mobile: upgrade the Insights view into the analysis surface
The `View ▾ → Insights` view *becomes* the analysis surface (no new route): a
**foldable overview dashboard** (report card from the **session** digest) over
the full-screen `AgentFeed`. Wire the session `event_count` so the position
reads "N of M" (ts-rank across agents) and the minimap thumb is monotonic.
Available for **any run**, with the live/"as of <ts>" label. This replaces the
sparse Insights content directly — insight *is* analysis, so the numbers now
match the transcript (the view reads the digest).

### P2 — Structure index → jump (random-access nav)
Render the error taxonomy / tool list / **turn index (`agent_turns`)** as
tappable sections in the dashboard; each entry seeks the log to its
`start_seq`. Insight mode uses a **random-access window loader** — fetch a
window *around* the anchor seq/ts and page locally in either direction —
distinct from Feed's tail + scroll-up, so you **land anywhere without walking
from the tail**. The DB already supports this efficiently (index range scan,
no `OFFSET`): agent scope via `before` + `since`; **session scope needs the
`after_ts` forward param added in P0**. The minimap scrubber maps an arbitrary
position to the nearest anchor seq (no raw ordinal lookup). This is the
"navigate accurately" payoff — overview and log bound together.

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
