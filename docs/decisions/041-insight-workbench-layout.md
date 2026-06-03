# 041. The Insight transcript workbench — card-filter lens, outline navigator, session rail

> **Type:** decision
> **Status:** Accepted (2026-06-03) — directed by the director after an
> independent review of a three-pane redesign idea for the Insight transcript;
> resolves the lens/outline conflation introduced by the P5 "point 6"
> implementation. Builds on [ADR-039](039-insight-lens-as-server-query.md)
> (lens-as-query) and [ADR-040](040-transcript-surfaces-decoupled-by-mode.md)
> (one file per mode). Realizes the structure-first conclusion of
> [`discussions/insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md) §10.
> Implemented by [`plans/insight-workbench-layout.md`](../plans/insight-workbench-layout.md).
> **Audience:** contributors
> **Last verified vs code:** v1.0.792-alpha (post-`a9c2af1`; point 6 shipped — the
> placement this ADR corrects).

**TL;DR.** The Insight transcript had been conflating two different jobs:
**filtering the cards** (a *lens*) and **navigating the run's structure** (an
*outline* / TOC). Separate them. The funnel becomes a pure card-filter
(`All / Text / Tools`). Turns, Errors, and the minimap (**Map**) become **tabs in
a right "Navigator" drawer** — a structural index you jump *from*, never a filter
that swaps the stream for a list. A left **"Sessions" drawer** is a *scoped*
quick-switcher for the current agent/project's sessions. Phone-first: both rails
are overlay drawers. The bottom `‹ › ⤒` stepper and the "N of M" position pill
are **dropped** — the outline does the jumping. Insight-only;
[`LiveFeed`](040-transcript-surfaces-decoupled-by-mode.md) is untouched.

## Context

[ADR-039](039-insight-lens-as-server-query.md) framed the transcript lens as a
server **keyset query**; [ADR-040](040-transcript-surfaces-decoupled-by-mode.md)
split the live and Insight surfaces into separate files and named the deferred
work (point 6 = "one lens selector", point 3 = paged Text/Tools). During point 6
the **Turns lens was made to *replace* the transcript with a whole-run summary
list**. That conflates two concerns:

- a **lens** *narrows the cards in place* — you stay in the stream;
- an **outline** *is a list of landmarks you jump from* — you don't filter the
  stream, you navigate it.

A lens that swaps the stream for a list is an outline wearing a filter's clothes.
The repo had already reasoned to the right model:
[`discussions/insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md)
§10 concluded **"structure-first is the model"** for turn/error/tool jumps, with a
time-scaled minimap as the overview scrubber. Two visible symptoms confirm the
missing home for structural navigation: the point-6 list-swap, and the
floating right-edge **minimap overlapping the card's top-right control**.

Separately, TermiPod is a **mobile-first** control plane whose differentiator is
multi-session / multi-agent work — yet the Insight transcript has no fast way to
switch *which* session/agent it is analyzing.

## Decision

1. **Lens = card filter only.** The funnel offers `All / Text / Tools`. It
   narrows the *visible cards in place* and never replaces the stream with a
   list. (Text/Tools page the whole run via the `kind=` keyset — ADR-039 — which
   is still a *filter*, not navigation.)
2. **Outline = structural navigation, in a right "Navigator" drawer.** Tabs:
   **Turns**, **Errors**, **Map**. Each is a whole-run index of landmarks;
   tapping an entry **jumps the transcript to that seq in full context** (it does
   not filter). Turns/Errors reuse the `TurnSummaryRow` / `ErrorSummaryRow` rows
   built in point 6; **Map** is the minimap.
3. **The minimap lives in the Navigator, not as a floating overlay.** This
   removes the collision with the card's top-right control and the right-edge
   lane hack.
4. **A left "Sessions" drawer — a *scoped* quick-switcher.** It lists the current
   agent/project's sessions (and related agents); selecting one retargets the
   Insight transcript. **Scoped, not a global tree** — it is a convenience inside
   the surface, not a second top-level navigator competing with the app's
   Projects / Activity / Sessions IA.
5. **Phone-first: rails are overlay drawers.** Both sidebars open as slide-in
   overlays over the full-width transcript (edge handles / app-bar icons). Wider
   screens get more breathing room; the model is identical. **No persistent
   three-pane layout is required** — that keeps small screens whole.
6. **Drop the bottom stepper and the "N of M" pill.** With the outline doing
   landmark navigation and the Map tab doing arbitrary scrubbing, the
   `‹ › ⤒` stepper and the monotonic position pill are redundant; remove them
   (and the seek-anchor stepper math they carried). The "jump to any event"
   scrubber lives on the Map tab.
7. **Insight-only.** This redesigns the `InsightTranscript` surface. `LiveFeed`
   is unchanged — it keeps its loaded-window declutter filter (ADR-040 §6).

## Terminology (pinned in the glossary)

To stop the conflation recurring, these get canonical definitions
([`reference/glossary.md`](../reference/glossary.md) §10):

- **Lens** — a card-family *filter* over the transcript (`All / Text / Tools`).
  Narrows; never navigates.
- **Outline** (a.k.a. TOC) — the whole-run *structural index* (Turns / Errors)
  you jump *from*.
- **Navigator** — the right drawer hosting the outline tabs + the Map.
- **Map** — the minimap, as a Navigator tab.
- **Sessions rail** — the left drawer; the scoped session/agent switcher.
- **Insight transcript** — the per-run sealed analysis transcript
  (`InsightTranscript`). *Distinguish from* the **insights surface** (the
  aggregate spend/latency/errors metrics screen — ADR-022); they collide on
  "insight(s)" but are different surfaces.

## Open questions — resolved 2026-06-03 (director)

- **Form factor.** Phone-first, drawers everywhere (decision §5).
- **Left rail scope.** Scoped to the current project/agent (decision §4).
- **Lens vs outline.** Lens = `All / Text / Tools`; Turns/Errors are outline tabs
  (decision §1–2).
- **Stepper + N/M pill.** Dropped (decision §6).

## Consequences

- **Point 6 is reframed as "right widgets, wrong placement."** The
  `TurnSummaryRow` / `ErrorSummaryRow` widgets and their list builders relocate
  from "replace the transcript ListView" into the Navigator tabs; the
  transcript-replace `_summaryMode` is removed. Nothing built is wasted.
- **Point 3 (paged Text/Tools) gets *cleaner*.** With Turns/Errors out of the
  lens, the only lenses are `All / Text / Tools`, and Text/Tools are exactly the
  families that page the whole run via the `kind=` keyset. Point 3 is now
  unambiguously a *filter* concern.
- **`InsightTranscript` simplifies.** Dropping the stepper + pill removes the
  `stepAnchorIdx` / `prevStepK` / `nextStepK` math and the `_openJumpSheet`
  coupling to a floating pill; the seek engine keeps only the outline-driven and
  Map-driven jumps.
- **New surface area.** A drawer scaffold (phone overlay) is greenfield in this
  surface; the Sessions rail needs the sessions/agents list providers (which
  already back the Sessions / project-agent screens).
- **IA reconciliation.** The mobile IA spec
  ([`spine/information-architecture.md`](../spine/information-architecture.md))
  gains a "workbench-within-a-surface" pattern for analysis; the Sessions rail
  must read as a *scoped convenience*, not a competing top-level nav.

## Alternatives considered

- **Keep Turns/Errors as lenses (point-6 status quo).** Rejected — it is the very
  filter/outline conflation this resolves.
- **Persistent three-pane layout.** Rejected for phone-first; overlay drawers
  achieve the same separation without breaking small screens. Wide screens may
  later pin the rails open, but that is additive, not required.
- **Global session/agent tree in the left rail.** Rejected — duplicates and
  competes with the app's five-tab IA. A scoped switcher was chosen.
- **Fix only the minimap collision** (reposition the floating overlay).
  Insufficient — it treats the symptom, not the missing home for structural
  navigation. Subsumed by moving the minimap into the Navigator.

## Related

- [ADR-039](039-insight-lens-as-server-query.md) — lens as a keyset query
  (Text/Tools paging substrate).
- [ADR-040](040-transcript-surfaces-decoupled-by-mode.md) — one file per mode;
  `InsightTranscript` is the home for this.
- [`discussions/insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md)
  — §10 structure-first conclusion this realizes; §12 follow-up captures the UI.
- [`plans/insight-workbench-layout.md`](../plans/insight-workbench-layout.md) —
  the phased execution.
- [ADR-038](038-per-run-event-digest.md) — the digest + `agent_turns` index the
  outline reads.
