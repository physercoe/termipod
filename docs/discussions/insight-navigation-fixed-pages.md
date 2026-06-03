---
name: Insight-mode navigation — fixed pages, a server ordinal, and the minimap
description: The director reported that the Insight transcript's funnel jumps (turn / text) and the minimap still land approximately, and proposed a fixed-length-page model (fixed pages → exact jump → drag/tap-to-any-position minimap). This doc traces the current landing path to its root cause — a three-stage heuristic (anchor→nearest-visible-row, then a capped pixel binary-search over lazy variable-height rows, over a position model that assumes per-agent `seq` is the dense run ordinal) — confirms the fixed-page instinct is right, and shows the one catch grounded in the code: pages are only exact if the dense ordinal comes from the server (seq is not reliably dense and folding is client-side). Lays out three implementation options (A: server dense run-ordinal end-to-end; B: minimap drag/tap reusing the existing _jumpToOrdinal, approximate; C: full fixed-page rewrite) mapped to file:line, with a recommendation. Companion to transcript-paging-vs-forum-model.md (which establishes the data-model substrate) and the agent-run-analysis-mode plan.
---

# Insight-mode navigation — fixed pages, a server ordinal, and the minimap

> **Type:** discussion
> **Status:** Open (2026-06-02) — raised by the director after the v1.0.788
> funnel/header work: "the funnel text/turn jump still not correct … whether
> the Insight log viewport could be fixed-length so there are fixed pages and
> jump is more accurate, and the user could drag the minimap to navigate and
> tap to any position." A fair challenge — the inaccuracy is structural, not a
> stray off-by-one, and the proposed model targets the actual root cause.
> **Audience:** contributors
> **Last verified vs code:** v1.0.788

**TL;DR.** The Insight funnel/minimap jump is not one seek — it is a
**three-stage heuristic**, and two stages are intrinsically fuzzy: (1) the
anchor seq is mapped to a rendered row by "nearest visible row ≥ anchor", (2)
that row index is reached by **binary-searching the scroll offset** over
lazily-built, variable-height rows with a 14-iteration cap, (3) all of it sits
on a position model that assumes **per-agent `seq` is the dense run ordinal**,
which it is not (the rendered list is *folded*; resumed sessions span multiple
agents). The director's fixed-page proposal is the right shape — it replaces
the fuzzy stages with arithmetic — but it is only *exact* if the dense ordinal
is supplied by the **server**, because `seq` is not reliably dense and folding
is client-side. This is the same keystone that
[`transcript-paging-vs-forum-model.md`](transcript-paging-vs-forum-model.md)
§§5/8–13 already identified (a maintained count + a dense session ordinal);
this doc is its *implementation-decision* companion, scoped to the
navigation/minimap surface. §§1–6 frame the fixed-page family; **§§7–10
widen to the full design space and land the actual decision**: for
*structural* navigation (turn/error/tool jumps) the winner is **structure-first
+ window-around-anchor, landed by index not by pixel-search** (§10), with a
dense ordinal demoted to a "N of M" nicety and a time-scaled minimap for the
arbitrary-scrub case.

---

## 1. The symptom, reproduced in the code

A funnel "view in full" / ▲▼ jump to a turn or text match in Insight mode
lands near — but not always on — the intended row, and the position pill /
minimap thumb drift as more events page in. The path:

**Stage 1 — anchor → rendered row.** The turns funnel is driven by the
whole-run digest: `runTurnSeqs` = the turn rows' `start_seq`
(`lib/widgets/session_analysis_view.dart:104-110`), which the hub keeps **at
the prompt** (`hub/internal/server/digest_fold.go:222`; pinned by
`digest_fold_test.go:236`, "start_seq = 1 … the prompt, kept on adoption").
The turns lens renders `input.text` (`lib/widgets/agent_feed/feed_reducer.dart:961`),
so the anchor *usually* matches a real row exactly
(`lib/widgets/agent_feed.dart:2187`, `indexWhere`). When it does not — a
hidden marker, a folded-away row, or a sparse/multi-agent run — it falls back
to **"nearest visible row ≥ anchor"** (`agent_feed.dart:2192-2199`). On a turn
boundary that fallback can resolve to the *next* turn's prompt.

**Stage 2 — row index → pixels (the core culprit).** `_seekToLoadedIndex` →
`_convergeToIndex` (`agent_feed.dart:1408`, `1444`) **binary-searches the
scroll offset** until the target row index falls inside the realized-row
window `[_minBuiltIdx, _maxBuiltIdx]` *and* the seek-key context exists, capped
at **14 iterations** (`1451`). On a long, height-varied transcript (a one-line
text card beside a tall tool dump) it can exhaust the cap and **stop wherever
it got** (`1451`, `1460-1462`). It is frame-timed and height-sensitive — exact
landing is not guaranteed by construction.

**Stage 3 — position model assumes dense seq.** The pill and the
"jump to event N" scrubber treat **N = seq, M = `event_count`**
(`agent_feed.dart:706-708`, `1646-1652`). But `M` is the digest's **raw**
event total (`session_analysis_view.dart:69`, `digestBody['event_count']`)
while the rendered list is **folded** (tool_call+result merged into one card,
hidden kinds dropped). So the raw-event ordinal ≠ the rendered-row position,
and on a resumed multi-agent session per-agent `seq` is not even dense. The
code names this limitation itself (`agent_feed.dart:1651`, "approximate
there").

**Conclusion.** The inaccuracy is intrinsic to "binary-search pixel offsets
over lazy variable-height rows, indexed by a not-quite-dense seq." It is not a
three-line fix.

## 2. Why "fixed pages" is the right instinct

A fixed-length page model replaces the fuzzy stages with arithmetic:

- page `k` = ordinals `[k·P, (k+1)·P)`;
- jump-to-`N` = page `N ÷ P`, row `N mod P`;
- minimap fraction `f` → ordinal `round(f · M)` → page.

No nearest-visible guess, no pixel binary-search, no frame-timing. The
position pill becomes `ordinal / M` (monotonic), the minimap becomes a true
scrollbar over `[1, M]`, and "jump to any position" is well defined. This is
precisely the property [`transcript-paging-vs-forum-model.md`](transcript-paging-vs-forum-model.md)
§4 isolates ("it's the *count* + dense ordinal, not the pages").

## 3. The one catch, grounded in the code

The model is only **exact** if the dense ordinal is **server-supplied**:

1. **`seq` is not the ordinal we render against.** The loader keys on
   `(ts, seq)` (`agent_feed.dart:442-457`); `seq` is per-agent and a resumed
   session has no dense cross-agent index (only `ts` totally orders it — see
   the forum doc §3). "Page by seq" drifts exactly where the pill drifts today.
2. **Folding is client-side.** A page is a fixed slice of *raw* events but a
   *variable* count of *rendered* rows (`feed_reducer.dart` fold). Within one
   ~200-event page this is harmless — `ensureVisible` is reliable at that
   scale — but it means a page boundary is not pixel-fixed, and a "row N of
   page" count is not stable unless the fold is also server-known.

The clean fix for (1) is a **dense run-ordinal from the hub** —
`ROW_NUMBER() OVER (ORDER BY ts, seq)` projected onto the events/turns
responses (and/or paginate by an `ordinal` range instead of a `(ts, seq)`
keyset). That is Go-side and locally testable, and it is the same keystone the
forum doc §§9/12 recommends. (2) is a smaller, separable concern: either accept
within-page `ensureVisible` (fine at page scale) or move the fold server-side
later.

## 4. The minimap today — and what the proposal needs

In Insight (run-anchor) mode the minimap **cannot** yet drag or tap-to-position
— by deliberate design:

- **Tap snaps to the nearest *anchor* tick** (error preferred, else nearest
  tick), not the tapped fraction (`lib/widgets/agent_feed/feed_misc.dart:538-562`).
- **Drag is disabled** (`onScrub == null` in run-anchor mode,
  `feed_misc.dart:570-575`). It was turned off because an idle vertical-drag
  recognizer joined the gesture arena and swallowed taps — the "untappable
  minimap" regression (device-test pass 1).

Enabling tap-to-position + drag is mechanically small: route the tapped /
dragged **fraction → `_jumpToOrdinal(round(f · M))`** (already implemented,
`agent_feed.dart:1652`). Its *accuracy* is the open variable: approximate under
today's dense-seq assumption, exact once the server ordinal lands.

## 5. Options

### Option A — server dense run-ordinal (the real fix)

- **Hub (Go, locally testable):** add an `ordinal` to the events/turns
  projection (`ROW_NUMBER() OVER (ORDER BY ts, seq)` per session), and an
  ordinal-range page parameter. `M` = the maintained `event_count` (already in
  the digest). Pin with a Go test over the canonical vector.
- **Mobile (director device-tests):** page by ordinal range; jump-to-`N` =
  exact page + row; minimap fraction → ordinal (exact); enable drag +
  tap-to-position. Gated behind `randomAccess` so the live-tail Feed is
  untouched.
- **Cost:** largest. Write/read-path ordinal, a mobile loader change. **But**
  it is the keystone the forum doc already recommends, it makes *every* Insight
  navigation surface exact at once (pill, minimap, funnel, jump-sheet), and it
  is the substrate the analysis/OTLP work wants anyway.

### Option B — minimap drag/tap now, reusing `_jumpToOrdinal` (stopgap)

- **Mobile only.** In Insight mode: minimap tap → `_jumpToOrdinal(round(f·M))`;
  enable a debounced drag → `_jumpToOrdinal`. Keep the anchor ticks as visual
  guides. Re-introduce the drag recognizer carefully (the reason it was
  removed) — e.g. only treat it as a drag past a slop threshold so a tap still
  reads as a tap.
- **Cost:** small; ships the requested UX immediately. **Accuracy:** still
  approximate on sparse/multi-agent seqs (inherits Stage 3) until A lands.
  Honest as a stopgap, not a fix.

### Option C — full fixed-page rewrite (mobile)

- Replace the `(ts, seq)` window + convergent seek with a page index
  (`page = ordinal ÷ P`) and a `PageView`/segmented list. Largest mobile churn,
  re-opens the live-tail/SSE interplay the current loader carefully gates, and
  *still* needs A's server ordinal to be exact. Not recommended as a first
  move — A delivers the same exactness with far less mobile risk.

## 6. Recommendation

1. **Land Option A** — the server dense run-ordinal — as the actual fix. It is
   Go-testable locally (fits the working model where Flutter is device-tested),
   it cures the root cause (Stages 1–3 collapse to arithmetic), and it is the
   keystone already endorsed in
   [`transcript-paging-vs-forum-model.md`](transcript-paging-vs-forum-model.md)
   §12 and the [agent-run-analysis-mode plan](../plans/agent-run-analysis-mode.md).
2. **Optionally ship Option B first** as a thin stopgap so the director has
   drag/tap-to-position to live with while A is built — explicitly labelled
   approximate.
3. **Do not start Option C.** A subsumes its benefit without the live-tail
   risk.

Open question for the director: ship B as a stopgap, or go straight to A?

> **Superseded by §10 (2026-06-02).** After working the design space below
> (§§7–9), the recommendation shifted: a dense ordinal is no longer the
> load-bearing mechanism for *structural* navigation — see §10.

## 7. Alternatives beyond fixed pages (industry practice)

Fixed-page (a server ordinal) attacks only the **logical-index** axis. The
problem has a second, independent axis — **pixel landing** (getting a
virtualized, variable-height, lazily-built row onto the screen) — and most of
the industry's answers target one axis or the other:

**7a. Measured-extent virtualization (the standard fix for the *pixel* axis).**
Maintain a running prefix-sum of measured row heights (an order-statistic /
Fenwick tree): estimate an extent before a row builds, correct it once
measured, keep a cumulative-offset index. `scrollToIndex` is then O(log n)
**exact** — no offset binary-search, no frame-timing. This is what TanStack
Virtual, react-virtualized (`CellMeasurer`), VS Code's list/editor, and
Slack/Discord message lists do. It is **client-only** — no hub change. Flutter
has off-the-shelf forms: `scrollable_positioned_list`
(`ItemScrollController.scrollTo(index)` for variable heights),
`super_sliver_list` (dynamic-extent estimation + accurate scrollbar), or a
`CustomScrollView` with a `center` key + two slivers (the "anchor in the
middle" chat pattern — lays out *around* the target so it is always realized).
This directly dissolves Stage 2.

**7b. Time as the index (the observability / trace-viewer standard).**
Perfetto, Chrome DevTools Performance, Jaeger, and log tools (Kibana, Loki,
Datadog, CloudWatch) navigate by **timestamp** + a time-ruler/overview-minimap
+ zoom, not by item count; a click/drag on time seeks and reloads. Relevant
because **`ts` is the one total order we already have** across a multi-agent
session (forum doc §3). A time-proportional minimap (thumb =
`(ts − t0)/(t1 − t0)`) is exact and dense **by construction, with no ordinal
and no migration** — the trade-off is that idle gaps render as empty stretches
(arguably honest: it shows think-vs-idle time), which those tools handle with a
ruler + zoom.

**7c. Structure-first + small local window (the IDE / long-doc pattern).**
VS Code (minimap + breadcrumbs + sticky scroll), Google Docs outline, Jupyter
TOC: navigate by **structure**, then the local scroll is short. We are already
half-way — the Turns index + funnel is the outline, and `_resetWindowAround`
already resets to a small window around an anchor (`agent_feed.dart:461`).

The consistent best practice across all of these is a **separation of
authority**: the *data cursor* (ordinal or timestamp) is the source of truth
for "where am I," and the **view follows**. Within that, pixel landing uses
measured-extent or an anchored viewport — **nobody binary-searches scroll
offsets on purpose**; we only do because plain `ListView.builder` gives no
exact `scrollToIndex`.

## 8. For structural nav to a context position, structure-first wins

For the specific job of "jump to *this* turn / error / tool call and see its
context" in a **long** log, structure-first is best — and its margin *grows*
with length:

| Approach | Natural target | Precision vs. log length |
|---|---|---|
| Ordinal / fixed-page | "event N of M" | degrades (thousands of events per minimap pixel) |
| Time-as-index | "moment T" | degrades + idle gaps distort density |
| **Structure-first** | "this turn / error / tool" | **scale-invariant** |

The number of structural landmarks grows far slower than raw events (a
10k-event run might have ~50 turns, ~8 errors): you navigate the *landmark
list* (trivial, exact) and load only the **window around the chosen landmark**.
A pure ordinal/time scrubber has to resolve one target out of thousands per a
few hundred pixels — precision you cannot physically drag to. It is also the
only family that delivers **context** by construction: windowing *around* an
anchor pulls the surrounding turns; an ordinal/time seek only parks a viewport
at a coordinate. This is why IDEs navigate big files by "go to symbol /
outline," and why log/trace tools (Kibana "surrounding documents", Jaeger span
pick) show a structured entry's context rather than make you scrub for it.

## 9. Landing accuracy — window-around-anchor vs. index-based, and caching

The current jump's failure mode is specific: after resolving the anchor to a
row index, `_convergeToIndex` (`agent_feed.dart:1444`) **binary-searches the
scroll offset** until the row is realized, capped at 14 iterations, and only
then `ensureVisible`s. On a large, height-varied loaded window it can exhaust
the cap and **bail with `ctx == null`, never scrolling to the card at all**
(`1460-1462`) — i.e. "the card is not in the viewport."

**Window-around-anchor is materially more accurate** because it flips every
variable in the search's favor: small scroll extent → converges within the
cap; the anchor is centered in a fresh ~200-event window so the target row is
realized on the first probe and the existing `ensureVisible` actually fires;
the anchor is guaranteed present so the "nearest visible row ≥ anchor" fallback
rarely triggers. It makes the *existing* landing code succeed where it bails.

But it is **reliable, not exact** — it still ends in a (small) pixel search.
The exact fix is to **land by index, not by offset** (§7a): compute the
anchor's index in the list and `scrollTo(index)` via a positioned list.

**Caching reality (answers "does mobile refetch?").** The random-access loader
calls the plain `listAgentEvents` (`agents_api.dart:413`), which is **always a
network round-trip**; the cached variants (`listAgentEventsCached` /
`...CacheOnly`) only support `tail`/`since` cursors, **not** the `(ts, seq)`
keyset a window-around-anchor uses — so there is **no local cache for RA
windows**, and the offline snapshot cache covers only the cold-open tail. The
consequence is decisive for the design: an **already-loaded** anchor is in the
in-memory `_events` buffer (`_seqIsLoaded`, `agent_feed.dart:423`), so
index-based landing on it needs **zero** network; "always re-window" would
refetch two pages **even when the data is already local**, adding a round-trip
and a visible reflow on every turn-step. So index-based landing is not just
more exact — it is the one that *avoids* the refetch in the common case.

## 10. Revised recommendation (2026-06-02)

For **structural navigation** (the common case — turn/error/tool jumps),
**structure-first is the model**, landed by **index, not pixel-search**:

1. **Loaded anchor → land by index** in the in-memory window (positioned-list
   `scrollTo(index)`): exact, **no refetch**. Fixes the "card not in viewport"
   symptom for the common turn-stepping case.
2. **Unloaded anchor → `_resetWindowAround` → land by index** in the fresh
   small window: exact at any depth, one round-trip (unavoidable — the data is
   not on the device).

Both remove the offset binary-search — the actual defect — and are mobile-only,
gated behind `randomAccess`. A **time-scaled minimap** (§7b) is the cheap,
migration-free way to make the *overview scrubber* exact and draggable for the
*arbitrary* (non-landmark) case. The **server dense ordinal** (Option A,
§5/§Options-A) demotes to a *nice-to-have* for an honest "N of M" pill — no
longer the load-bearing nav mechanism. Option C (full fixed-page rewrite)
stays rejected.

This is the "option 1+2" the director approved to build after this doc.

## 11. Device-test pass 4 — anchor-near-top reset (2026-06-02)

Option 1+2 shipped (v1.0.789-alpha) hardened the offset binary-search but kept
the **centered** window: `_resetWindowAround` fetched ≈half the page *before*
the anchor and half *after*, so an **unloaded** anchor still landed **mid-list
and off-screen**, and the convergence had to scroll ~half a window down to it.
Device-testing confirmed that for context outside the loaded window the card
**still didn't land** — the offset search over a variable-height list (agent
cards span one line to multi-MB) can't reliably bracket a far target inside its
iteration budget, because `maxScrollExtent` is only an extrapolated estimate.

Rather than swap the list engine (`scrollable_positioned_list` would give exact
`scrollTo(index)` but drops the `ScrollController`, forcing a rewrite of the
load-older/newer, tail-follow, minimap, and position machinery stabilised
across passes 1–3 — the largest regression surface, unverifiable without a
device), the fix makes the **reset asymmetric**: fetch a small backward lead
(`kDefaultAnchorLead = 12`) and the rest of the page *after* the anchor, so the
target renders among the **first** rows of the fresh window. It is then
realised at scroll offset 0 and `ensureVisible` (via the existing convergence,
now a *small near-top* scroll where height-variance error is negligible) lands
it directly. Scrolling up reloads older context through the normal load-older
pager (`_maybeLoadOlder`, keyset-anchored — works in windowed mode). This keeps
**every** piece of the stabilised machinery untouched and adds no dependency;
the only trade-off is the anchor lands near the top, not centred. The
`scrollable_positioned_list` swap (§7/§10) remains the documented next lever if
even a small near-top scroll proves unreliable on-device.

## 12. The workbench — structure-first as UI chrome (2026-06-03, → ADR-041)

§10 concluded *structure-first* is the model but left it as a navigation
*mechanism*. The director's redesign gives it a *home*, and in doing so corrects
a category error that crept into the P5 "point 6" build: the Turns lens had been
made to **replace the transcript with a summary list**, which conflates two
distinct jobs —

- a **lens** *filters the cards in place* (you stay in the stream), versus
- an **outline** *is a list of landmarks you jump from* (you navigate the
  stream, you don't filter it).

The resolution ([ADR-041](../decisions/041-insight-workbench-layout.md)): the
funnel reverts to a pure card-filter (`All / Text / Tools`); **Turns, Errors, and
the minimap (Map) become tabs in a right "Navigator" drawer** — the structure-
first index of §10, now a panel you open and jump from, never a filter. This
also dissolves a standing symptom: the minimap stops floating over the card
stack (the top-right-control collision) because it lives in the Navigator. A left
**"Sessions" drawer** adds a *scoped* session/agent switcher (the multi-session
differentiator the surface lacked). Phone-first: both rails are overlay drawers,
not a persistent three-pane split. The bottom `‹ › ⤒` stepper and the "N of M"
pill are dropped — the outline does landmark navigation and the Map tab does
arbitrary scrubbing, so they are redundant.

So the §10 levers map onto chrome: **outline tabs** = the loaded/unloaded
land-by-index jumps (1+2); **Map tab** = the time-scaled minimap (§7b) for the
arbitrary case; the **dense ordinal** stays the demoted "N of M" nice-to-have —
now without even a pill to host it. The point-3 paged Text/Tools work survives,
clarified as a *filter* (lens) concern. Execution:
[`plans/insight-workbench-layout.md`](../plans/insight-workbench-layout.md).

## Related

- [`discussions/transcript-paging-vs-forum-model.md`](transcript-paging-vs-forum-model.md)
  — the data-model substrate (maintained count + dense session ordinal); this
  doc is its navigation/minimap implementation companion.
- [`plans/agent-run-analysis-mode.md`](../plans/agent-run-analysis-mode.md) —
  the Insight surface this navigates; the ordinal is shared infrastructure.
- [`decisions/038-per-run-event-digest.md`](../decisions/038-per-run-event-digest.md)
  — the digest that supplies `event_count` (M) and the turn/error anchors.
