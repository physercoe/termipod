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
navigation/minimap surface, with three concrete options.

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

## Related

- [`discussions/transcript-paging-vs-forum-model.md`](transcript-paging-vs-forum-model.md)
  — the data-model substrate (maintained count + dense session ordinal); this
  doc is its navigation/minimap implementation companion.
- [`plans/agent-run-analysis-mode.md`](../plans/agent-run-analysis-mode.md) —
  the Insight surface this navigates; the ordinal is shared infrastructure.
- [`decisions/038-per-run-event-digest.md`](../decisions/038-per-run-event-digest.md)
  — the digest that supplies `event_count` (M) and the turn/error anchors.
