# Agent-run analysis mode — overview dashboard + accurate log navigation

> **Type:** plan
> **Status:** Proposed (2026-06-01) — spec for a dedicated **Analyze**
> screen (foldable overview dashboard over a navigable run log) plus an
> operator-facing **OTLP trace** export, both backed by the per-run digest +
> turn index ([ADR-038](../decisions/038-per-run-event-digest.md)). Replaces
> today's sparse per-agent Insights view. Not yet started.
> **Audience:** contributors
> **Last verified vs code:** v1.0.783

**TL;DR.** A director needs, for a finished or idle run, both an **overview**
("did it work, how many turns, what did it cost, what failed") and the
ability to **navigate the log accurately** to the moments that matter. Today
neither is solid: per-run insight is inconsistent and undercounted
(discussion §14), and the per-agent Insights view is sparse. This plan adds a
**dedicated Analyze screen** — a foldable overview dashboard on top, the
navigable transcript below — driven by one canonical [per-run event
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
- The user chose a **dedicated Analyze screen** (foldable overview dashboard
  + navigable log) as the home for this, and as the upgrade path for the
  currently sparse Insights surface.

## Shape

```
[Analyze ⤢]  (full-screen route; offered when the run is idle/terminated)
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
- **Navigable log**: the existing full-screen `AgentFeed` (`dense: false`),
  now with a **monotonic** position ("event N of M" from `event_count`) and a
  true minimap position indicator.
- **Jump-to-context**: every dashboard entry (an error class, a tool, a turn)
  taps to seek the log to that `seq` via the convergent index seek.
- **Availability**: offered for **idle / terminated** runs (the frozen
  dataset). A live run stays in follow mode; when it goes idle the Analyze
  affordance lights up. The dashboard is labelled "as of <watermark ts>".

## Phases

### P0 — Hub: digest + turn index, maintained incrementally (ADR-038)
The data substrate. New `agent_event_digests` (per-agent scalar rollups +
canonical-error count + a mergeable latency histogram) **and** `agent_turns`
(the turn index — one row per turn). **Maintained incrementally** by folding
each event into the digest + open/close turn rows in the same transaction as
the `agent_events` insert; the idle (`onPreAgentIdle`) / terminal
(`stopSessionInternal`) hooks reconcile + finalize outcome. Canonical error =
the transcript-lens union (Go = source of truth). Reads:
`GET …/agents/{agent}/digest` and `…/sessions/{session}/digest` (the
ts-ordered rollup of the session's agents). Refactor `/v1/insights` to **sum
the in-scope digests** (percentiles from merging the latency histograms).
*Tests: incremental digest == a brute-force scan at every watermark; error
union matches a shared Go/Dart vector; session rollup == sum of agents;
insights-sum == legacy scan.*

### P0b — `turn.start` event + emission (ADR-038 §3)
Add the `turn.start {turn_id, ts}` boundary event and `turn_id` on
`turn.result`; emit it from the claude-code M4 driver first (others follow).
The hub **synthesizes** turns for engines not yet emitting it, so the index
and OTLP work everywhere from day one; native `turn.start` refines the log
anchor + tool grouping. *(Drivers/protocols change — sequence with P0.)*

### P1 — Mobile: Analyze screen shell (session-scoped)
New full-screen route (`screens/.../analyze_screen.dart`) = foldable overview
dashboard (report card from the **session** digest) above the full-screen
`AgentFeed`. Wire the session `event_count` so the position reads "N of M"
(ts-rank across agents) and the minimap thumb is monotonic. Reachable from
the per-agent/session Insights entry, the run/session card, and the
transcript overflow.

### P2 — Structure index → jump
Render the error taxonomy / tool list / **turn index (`agent_turns`)** as
tappable sections; each entry seeks the log to its `start_seq` (reuse the
convergent seek). This is the "navigate accurately" payoff — overview and log
bound together, at session granularity.

### P3 — Replace the sparse Insights view
Fold the per-agent/session Insights surface into (or redirect it to) the
Analyze screen, so "Insights" for one run *is* the rich dashboard. The
numbers now match the transcript (insights reads the digest).

### P4 — Operator OTLP export (ADR-038 §4)
The hub's optional OTLP exporter (`--otlp-endpoint`, off by default) projects
`agent_turns` → spans: **trace = session, span = turn, child span = tool
call**, OTel GenAI attributes, deterministic span IDs. Operator points it at
Phoenix / Jaeger. Direct projection — built on the same turn index P2 uses.

### Later (post-MVP)
- Per-session **dense ordinal** materialized (vs. the ts-rank used here) if a
  stored ordinal becomes necessary.
- Tier-2 OLAP: Parquet export → DuckDB (see discussion doc Part II) + MCP
  `transcript.summary` / `transcript.query` — a future ADR-B.
- Per-turn **live** OTLP streaming (vs. terminal-only export).

## Open questions

- **Launch points**: is Analyze its own route everywhere, or the content of
  the existing `View ▾ → Insights` tab promoted to full-screen? (Leaning: a
  route, linked from Insights + the run card.)
- **Digest staleness UI**: how prominent the "as of <ts>" label + a manual
  refresh is for an idle run that may resume.
- **Live runs**: do we offer a read-only "snapshot now" analyze for a running
  agent (watermark = current max seq), or gate Analyze to idle/terminal only
  for v1? (Leaning: idle/terminal only for v1.)
- **Latency histogram shape** (fixed buckets vs. t-digest) and **`turn.start`
  rollout order** — tracked in ADR-038's open questions.
