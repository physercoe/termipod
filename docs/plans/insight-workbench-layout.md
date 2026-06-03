# Insight transcript workbench — phased execution

> **Type:** plan
> **Status:** In progress (2026-06-03) — implements
> [ADR-041](../decisions/041-insight-workbench-layout.md). **R1 + R2 tagged
> (v1.0.793-alpha); R3 tagged (v1.0.794-alpha); R4 coded (awaiting the
> director's device-test).** All four phases are now implemented. Each phase
> gates on the director's device-test (Flutter is untestable locally); pure
> extractions carry unit tests.
> **Audience:** contributors
> **Last verified vs code:** v1.0.794-alpha (R4 coded post-tag; see the per-phase
> status notes below).

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
- **Status: coded (awaiting device-test).** The Navigator gained a third **Map**
  tab (`_buildNavMap`): a full-height `FeedMinimap` (tap a tick → close drawer +
  land) plus a "Jump to event N / M" button that opens `_openJumpSheet`. The
  floating right-edge `FeedMinimap`, the centred `FeedPositionPill`, and the
  bottom `TurnStepperPill` are deleted, along with the `stepAnchorIdx` /
  `stepSeqs` / `stepUnit` / `prevStepK` / `nextStepK` math. Removing the stepper
  cascaded out the now-dead funnel-run-jump machinery: `_funnelRunJump`,
  `_funnelRunIdx`, `funnelUsesRunList`, `_seekToLensedIndex`,
  `_jumpToOldestLoaded`, and the `keepLens` path (`_landOnSeqKeepLens`,
  `_resetWindowAround`/`_jumpToContext` `keepLens` param, `_pendingContextKeepLens`)
  — all gone, since with Turns/Errors out of the lens the funnel only steps
  Text/Tools loaded-window matches (`_funnelStep` simplified). The `_NavigatorHandle`
  moved to the clean top-right corner (`right:6`); arbitrary-position navigation
  is preserved by the Map tab's slider (more precise than a hidden drag-strip).
  Stepper/pill widgets stay in `feed_misc.dart` (still used by `LiveFeed`).

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
- **Status: coded (awaiting device-test).** Source = **both** (director's call):
  a `SessionsRail` (`lib/widgets/sessions_rail.dart`) lists two groups — **Agents
  · <project>** (the project's siblings via `listAgentsCached(projectId,
  includeTerminated, includeArchived)`, project resolved from the current agent's
  row) and **This agent** (its sessions via `listSessionsCached()` filtered on
  `current_agent_id`). Picking an agent resolves its session (newest event
  `session_id`, like the archived-agent screen) and picking a session uses its
  `current_agent_id`. The host (`SessionAnalysisView`) now holds the active
  `(agentId, sessionId, live)` in state (seeded from the widget, re-synced in
  `didUpdateWidget` so external nav still wins), exposes `_retarget`, and re-keys
  `InsightTranscript` on `'$agentId/$sessionId'` so the buffer rebuilds while the
  digest/turns providers re-resolve on the new session id. Phone overlay: a slim
  left-edge pull handle (`_SessionsRailHandle`) opens a scrim + left panel.
  **Device-test pass 1 (R3):** the rail awaited three sequential fetches on open
  (`getAgentCached` + `listAgentsCached` + `listSessionsCached`) → visible
  latency; the Project-detail Agents tab is instant because it reads the *warm*
  `hubProvider.value.agents` snapshot synchronously. Rewrote the rail to read
  that same warm snapshot — the roster paints on open, no spinner. **Dropped the
  "This agent" sessions group** (the latency source *and* the respawn-history
  limit): the rail is now a single context-scoped roster — a project agent
  (`project_id` set) shows the **project's agents** (the Agents-tab list), a
  team-level steward (no `project_id`) shows the **team steward roster**
  (`isStewardAgent`). Picking an agent resolves its session from the warm
  sessions snapshot first (instant), falling back to the newest-event fetch only
  when cold; the rare archived-agent case (absent from the snapshot) does one
  `getAgentCached` to learn its project. **Persistence + mutual exclusion
  (ADR-041 §4/§5):** picking a row no longer closes the rail — it stays open
  until the user closes it; and the rail's open state is hoisted beside the
  Navigator's into the host so only one drawer shows at a time (opening either
  closes the other; the rail handle hides while the Navigator is open).

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
- **Status: coded (awaiting device-test).** `InsightTranscript` gained a second
  buffer (`_lensEvents` + `_lensScroll` + cursors) fed by a `kind=`-bound
  `RandomAccessLoader` (`_lensLoader`): `_loadLensBuffer` tail-fetches the
  newest page on entering Text/Tools, `_loadOlderLens` pages older via
  `fetchOlder` on scroll-up. Build branches its source — `isLensView ? _lensEvents
  : _events` — then folds / hides / re-applies `agentEventMatchesLens` /
  collapses over it; the centre `ListView` uses `_lensScroll` in lens view. A
  card's "view in context" → `_viewInContext` clears the buffer + lens and
  `_handleExternalSeek`s the main window onto the seq (resetting it around the
  anchor if off-window — the real fix for "the text jump is wrong"). The funnel
  pill is now count-only in lens view (`FeedFilterControl.showStepper=false`,
  new flag; LiveFeed keeps its stepper), and the in-lens prev/next stepper
  (`_funnelStep` / `matchSeqs` / `matchIndex`) is removed. **Also done:** the
  R1-leftover over-indented centre `ListView.separated` is re-flowed.
  **Device-test pass 1 (Navigator persistence):** the Navigator's open state was
  a private `_navigatorOpen` field inside `InsightTranscript`, so it couldn't be
  coordinated with the left rail (both could show at once) and a Turns / Errors /
  Map row tap closed it. Lifted it to the host as a controlled prop
  (`navigatorOpen` + `onNavigatorOpenChanged`); the host owns both drawers and
  keeps them mutually exclusive (opening either closes the other). Outline /
  minimap row taps no longer close the Navigator — only the scrim and the close
  button do (ADR-041 §4/§5).

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
