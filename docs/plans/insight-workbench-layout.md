# Insight transcript workbench — phased execution

> **Type:** plan
> **Status:** In progress (2026-06-03) — implements
> [ADR-041](../decisions/041-insight-workbench-layout.md). **R1 coded (awaiting
> the director's device-test); R2–R4 not started.** Each phase gates on the
> director's device-test (Flutter is untestable locally); pure extractions carry
> unit tests.
> **Audience:** contributors
> **Last verified vs code:** v1.0.792-alpha (R1 coded post-tag; see the R1
> phase note below).

**TL;DR.** Reshape the `InsightTranscript` surface per
[ADR-041](../decisions/041-insight-workbench-layout.md): the funnel becomes a
pure card-filter (`All / Text / Tools`); Turns, Errors, and the minimap (Map)
move into a right **Navigator** drawer as an outline you jump *from*; a left
**Sessions** drawer scoped-switches the analyzed session/agent. Phone-first —
both rails are overlay drawers. Drop the bottom stepper and the N/M pill. Each
phase is a separate, device-testable commit; Insight-only (`LiveFeed`
untouched).

## Starting point

After ADR-040 P5 point 6 (`a9c2af1`), `InsightTranscript`
(`lib/widgets/insight_transcript.dart`) renders Turns/Errors as **summary lists
that replace the transcript** (`_summaryMode` / `_buildTurnsSummaryList` /
`_buildErrorsSummaryList`), and the minimap floats over the card stack
(colliding with the card's top-right control). The substrate is ready: the
`kind=` keyset query exists end-to-end (hub `handlers_agent_events.go:264`,
client `agents_api.dart` `kinds:`, reducer `feedLensKinds`), and
`TurnSummaryRow` / `ErrorSummaryRow` are substrate widgets in
`transcript/feed_misc.dart`.

## Target shape

```
InsightTranscript (phone)
  ┌─ app-bar: [☰ Sessions]            [Navigator ▤]
  │  ┌──────────────────────────────────────────┐
  │  │  transcript (cards, always)               │   ← center
  │  │  funnel: All · Text · Tools  (filter)     │
  │  └──────────────────────────────────────────┘
  │  left drawer  (overlay): Sessions rail — scoped switcher
  │  right drawer (overlay): Navigator — [Turns][Errors][Map] tabs
```

The Navigator's Turns/Errors tabs are the relocated summary-row lists (tap →
jump the transcript, full context). The Map tab is the minimap (whole-run ticks
+ viewport indicator + drag-scrub + the "jump to any event" scrubber). No
floating minimap, no bottom stepper, no N/M pill.

## Phases

Order: fix the conceptual model first (lens vs outline), then relocate the
minimap, then add the session switcher, then the paged filter. Reuse the point-6
widgets; remove the transcript-replace mode.

### R1 — Navigator drawer (Turns/Errors outline) + lens revert

- Funnel lens set → `All / Text / Tools` only. Turns/Errors leave the funnel UI
  (the `FeedLens` enum keeps the values for the predicate/anchor lists, but they
  are not selectable lenses).
- Add a right **Navigator** drawer (phone overlay; `Scaffold.endDrawer` or an
  in-widget overlay since `InsightTranscript` is embedded, not a route) with a
  tab bar **Turns | Errors**. Move `_buildTurnsSummaryList` /
  `_buildErrorsSummaryList` into the tabs; a row tap jumps the transcript
  (`_handleExternalSeek`) and closes the drawer.
- Remove `_summaryMode` and the transcript→list swap: the center ListView always
  renders cards.
- **Gate:** the transcript always shows cards; the funnel filters in place;
  opening the Navigator shows the Turns/Errors outline; a tap lands the
  transcript on the right turn/error in context.
- **Status: coded (awaiting device-test).** `FeedFilterControl` gained an
  optional `selectableLenses` (Insight passes `All / Text / Tools`).
  `InsightTranscript` dropped the `_summaryMode` transcript→list swap (and the
  `_isErrorsSummaryLens` / `_isTurnsSummaryLens` / `_runAnchorListFor` helpers);
  the centre `ListView` always renders cards. The point-6 row builders became
  `_buildNavTurnsList` / `_buildNavErrorsList` (own scroll controllers; a tap
  calls `_jumpFromOutline` → close drawer + `_handleExternalSeek`), hosted in a
  phone-overlay `_buildNavigatorOverlay` (scrim + right panel + `Turns | Errors`
  `TabBar`), opened by the top-right `_NavigatorHandle`. Stepper + N/M pill +
  floating minimap stay for now (R2 removes them).

### R2 — Map tab (minimap into the Navigator)

- Move `FeedMinimap` from the floating right-edge `Positioned` into a **Map** tab
  in the Navigator. The 28px right-edge lane and the card top-right collision are
  gone.
- Fold the "jump to any event" scrubber (`_openJumpSheet`) onto the Map tab (a
  control there), then **delete the N/M position pill and the bottom
  `TurnStepperPill`** (ADR-041 §6) and the `stepAnchorIdx` / `prevStepK` /
  `nextStepK` build math.
- **Gate:** the minimap scrubs/jumps from the Map tab; nothing floats over the
  cards; the stepper + pill are gone with no lost capability.

### R3 — Sessions rail (left drawer, scoped switcher)

- Add a left **Sessions** drawer (phone overlay) listing the current
  project/agent's sessions (+ related agents), sourced from the existing
  sessions/agents providers. Selecting one **retargets** the Insight transcript
  (re-key `InsightTranscript` on the new `agentId`/`sessionId`, or hoist a small
  controller in the host so the digest/turns providers re-resolve too).
- Reconcile with the IA spec so the rail reads as a scoped convenience, not a
  competing top-level nav.
- **Gate:** the rail lists the in-scope sessions/agents; selecting one swaps the
  analyzed run (transcript + dashboard + outline all retarget); the app's
  top-level navigation is unaffected.

### R4 — paged Text/Tools filter (ADR-039 point 3)

- The Text/Tools lens pages the whole run via a `kind=`-filtered keyset buffer
  (a second buffer in `InsightTranscript`, distinct from the main `_events`
  window): on entering Text/Tools, fetch `feedLensKinds(lens)` keyset-newest,
  build `FoldMaps` over it, filter `isHiddenInFeed` + `agentEventMatchesLens`
  (the kind set is a SUPERSET — re-check the predicate) + `collapseStreamingPartials`,
  render as cards; scroll-up pages older matches; a card tap → "view in context"
  jumps back into the main `_events` window at that seq.
- **Gate:** a text/tool match anywhere in the run is reachable from the
  Text/Tools filter (fixes "the text jump still incorrect"); folding + result
  pairing render correctly (Tools set includes `tool_result` / `tool_call_update`).

### Polish

- Pin the glossary terms (lens / outline / Navigator / Map / Sessions rail /
  Insight transcript-vs-insights-surface) — done in the ADR-041 docs commit.
- Wide-screen affordance (optional, additive): pin the rails open instead of
  overlaying when horizontal space allows. Not required by ADR-041 §5.

## Risks

- **Phone real estate.** Two overlay drawers + a transcript must stay legible on
  a phone; the drawers overlay (not split) the transcript, and only one opens at
  a time.
- **Retarget correctness (R3).** Switching the analyzed run must re-resolve the
  digest + turns providers *and* the transcript buffer together, or the
  dashboard/outline desync from the cards. Re-keying the subtree is the safe
  default.
- **Second buffer (R4).** The paged filter buffer coexists with the main window;
  lens-switch lifecycle (build/discard), folding, and "view in context" hand-off
  are the subtle parts (see ADR-040 §C-style discipline: lift, gate, device-test).
- **No local Flutter** — every phase gates on the director's device-test; pure
  units (row mappers, kind-set parity) carry unit tests.

## Related

- [ADR-041](../decisions/041-insight-workbench-layout.md) — the decision.
- [ADR-039](../decisions/039-insight-lens-as-server-query.md) — the `kind=`
  keyset substrate R4 rides.
- [ADR-040](../decisions/040-transcript-surfaces-decoupled-by-mode.md) /
  [`plans/transcript-surface-decoupling.md`](transcript-surface-decoupling.md) —
  the mode split this builds on.
- [`discussions/insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md)
  — §10 structure-first, §12 the workbench follow-up.
