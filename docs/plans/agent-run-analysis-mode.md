# Agent-run analysis mode — overview dashboard + accurate log navigation

> **Type:** plan
> **Status:** Proposed (2026-06-01) — spec for a dedicated **Analyze**
> screen: a foldable overview dashboard over a navigable run log. Backed by
> the per-run event digest ([ADR-038](../decisions/038-per-run-event-digest.md)).
> Replaces today's sparse per-agent Insights view. Not yet started.
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
true "event N of M" position.

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

### P0 — Hub: per-run event digest (ADR-038)
The data substrate. New `agent_event_digests` table; one-pass compute at the
idle (`onPreAgentIdle`) and terminal (`stopSessionInternal`) watermarks;
canonical error = the transcript-lens union (Go is source of truth);
`GET /v1/teams/{team}/agents/{agent}/digest`. Refactor `/v1/insights?agent_id`
to read the digest. *Tests: digest counts == a brute-force scan; error union
matches a shared vector; idle/terminal recompute.*

### P1 — Mobile: Analyze screen shell
New full-screen route (`screens/.../analyze_screen.dart`) = foldable overview
dashboard (report card from the digest) above the full-screen `AgentFeed`.
Wire `event_count` into the feed so the position reads "N of M" and the
minimap thumb is monotonic. Reachable from the per-agent Insights entry, the
run/session card, and the transcript overflow.

### P2 — Structure index → jump
Render the digest's error taxonomy / tool list / turn list as tappable
sections; each entry seeks the log to its `seq` (reuse the convergent seek).
This is the "navigate accurately" payoff — overview and log bound together.

### P3 — Replace the sparse Insights view
Fold the per-agent Insights surface into (or redirect it to) the Analyze
screen, so "Insights" for one run *is* the rich dashboard. Reconcile the
numbers (insights now reads the digest, so they match the transcript).

### Later (post-MVP)
- Per-session rollup + dense cross-agent ordinal (multi-agent resumed
  sessions) — ADR-038 open question.
- Incremental digest maintenance (vs. wholesale recompute).
- Tier-2 export: Parquet (DuckDB OLAP) + OTLP trace (OTel GenAI conventions)
  + MCP `transcript.summary` / `transcript.query` — see the discussion doc
  Part II and a future ADR-B.

## Open questions

- **Launch points**: is Analyze its own route everywhere, or the content of
  the existing `View ▾ → Insights` tab promoted to full-screen? (Leaning: a
  route, linked from Insights + the run card.)
- **Digest staleness UI**: how prominent the "as of <ts>" label + a manual
  refresh is for an idle run that may resume.
- **Live runs**: do we offer a read-only "snapshot now" analyze for a running
  agent (watermark = current max seq), or gate Analyze to idle/terminal only
  for v1? (Leaning: idle/terminal only for v1.)
