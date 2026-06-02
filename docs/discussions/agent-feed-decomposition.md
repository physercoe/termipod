---
name: agent_feed.dart audit — naming, the god-State, and a decomposition path
description: The director flagged that lib/widgets/agent_feed.dart "seems not well designed for the current codebase" and that the filename is misleading. This audit grounds both claims in the code: the file is 3108 lines, of which one _AgentFeedState carries ~40 instance fields and ~50 methods behind a single 1077-line build(), bundling six orthogonal concerns (live-tail transport, random-access windowing, the seek/converge machine, the lens system, cost polling, parent-callback forwarding) plus heavy aggregation that build() recomputes every frame. The rendering primitives were already extracted into lib/widgets/agent_feed/ (9 files, 5969 lines); what remains un-extracted is the controller logic. The widget is also no longer "a feed" — the randomAccess flag turns it into a sealed read-only analysis transcript (ADR-039's table half of the stream/table duality), so the "feed" name undersells half its job. Proposes a gated, no-regression decomposition (extract the seek machine + transport loader + telemetry aggregation as controllers, then evaluate a mode-split into LiveFeed vs AgentTranscript), and shows how the deferred point 3 (paged Text/Tools lens) and point 6 (unified lens selector) land naturally on the extracted seams. Companion to monolith-refactor.md, insight-navigation-fixed-pages.md, and ADR-039.
---

# agent_feed.dart audit — naming, the god-State, and a decomposition path

> **Type:** discussion
> **Status:** Open (2026-06-02) — raised by the director after the v1.0.790
> Insight device-test: "the point 6 just defer since agent_feed.dart needs
> review/audit, now it seems not well designed for the current codebase … also
> the file name is misleading somewhat." This audit grounds both observations in
> the code and proposes a decomposition. No code has moved yet.
> **Audience:** contributors
> **Last verified vs code:** post-`8e2e6cb` (HEAD on `main`).

**TL;DR.** `lib/widgets/agent_feed.dart` is 3108 lines. The render layer was
already split out into `lib/widgets/agent_feed/` (9 files, 5969 lines — cards,
the reducer, chrome widgets, the random-access loader). What stayed behind is a
single `_AgentFeedState` (`:204`) carrying **~40 instance fields and ~50
methods** under **one 1077-line `build()`** (`:1866`–`:2943`). That State
bundles six orthogonal concerns and, separately, `build()` recomputes heavy
aggregation (telemetry, cost, context-window math, fold maps) on every frame.
The widget also serves **two different surfaces** behind the `dense` /
`randomAccess` flags — a live-tail **stream** and a sealed read-only
**analysis transcript** — so the name "feed" describes only half of it. The fix
is not a rewrite: extract the controllers along the seams that already exist,
gated for no regression, then decide whether the two surfaces want to be two
widgets.

## What's already good (so we don't undo it)

The *rendering* decomposition is done and healthy — adding to it, not reverting
it, is the rule (memory: "Extract, don't pile on"):

| File | Lines | Role |
|---|---|---|
| `agent_feed/event_card.dart` | 1181 | per-event card dispatch |
| `agent_feed/feed_reducer.dart` | 1330 | pure folding + lens predicates |
| `agent_feed/feed_misc.dart` | 942 | chrome widgets (pills, funnel, minimap, `ErrorSummaryRow`) |
| `agent_feed/telemetry_strip.dart` | 634 | the telemetry strip widget |
| `agent_feed/interaction_cards.dart` | 676 | approval / select cards |
| `agent_feed/approval_cards.dart` | 492 | pending-permission cards |
| `agent_feed/tool_renderers.dart` | 369 | per-tool result bodies |
| `agent_feed/random_access_loader.dart` | 175 | `(ts,seq)` keyset fetch shape |
| `agent_feed/feed_render.dart` | 170 | shared render helpers |

So the problem is **not** "the cards are inline." The problem is the
**controller** — the State machine and `build()` — never got the same treatment.

## Claim 1 — the State is a god-object

`_AgentFeedState` mixes six concerns that do not share invariants and change for
different reasons. Each is a candidate to lift into its own controller/mixin:

1. **Live-tail transport.** Cold bootstrap, SSE subscribe, exponential-backoff
   reconnect, the deferred offline banner, load-older paging, snapshot/window
   ingest, and two dedup sets.
   `_bootstrap` (`:1070`), `_subscribe` (`:1185`), `_scheduleReconnect`
   (`:1284`), `_rebootstrapTail` (`:970`), `_maybeLoadOlder` (`:873`),
   `_ingestSnapshot` (`:1028`), `_maybeBackfillSessionInit` (`:829`); fields
   `_events`,`_ids`,`_replayKeys`,`_maxSeq`,`_minSeq`,`_oldestTs`,`_loadingOlder`,
   `_atHead`,`_staleSince`,`_reconnectAttempt`,`_reconnectTimer`,
   `_bannerGraceTimer`,`_sub`.
2. **Random-access windowing** (the Insight half). Window reset around an
   off-screen anchor + forward pager; the "does the window reach the tail" flag
   that gates SSE-append and the forward loader.
   `_resetWindowAround` (`:474`), `_maybeLoadNewer` (`:563`),
   `_randomAccessLoader` (`:444`), `_ingestWindow` (`:515`), `_seqIsLoaded`
   (`:437`); fields `_windowHasTail`,`_loadingNewer`,`_newestTs`,`_newestSeq`.
3. **The seek / converge machine** — the single most intricate cluster, and the
   one the director keeps hitting (landing bugs across v1.0.788–790).
   `_onSeekRequest` (`:373`), `_handleExternalSeek` (`:391`), `_seekToSeq`
   (`:1368`), `_seekToFrac` (`:1401`), `_seekToLoadedIndex` (`:1446`),
   `_convergeToIndex` (`:1491`), `_jumpToContext` (`:1656`), `_funnelRunJump`
   (`:1672`), `_funnelStep` (`:1705`), `_jumpToOrdinal` (`:1835`),
   `_openJumpSheet` (`:1765`), `_seekToLensedIndex` (`:2943`),
   `_jump/animate/releaseProgrammatic` (`:802`,`:814`,`:1577`); fields
   `_seekKey`,`_activeSeekSeq`,`_seekHighlight`,`_programmaticScrollDepth`,
   `_minBuiltIdx`,`_maxBuiltIdx`,`_lensedCount`,`_topBuiltSeq`,`_lastTopBuiltSeq`,
   `_pendingContextSeq`,`_pendingContextKeepLens`,`_funnelRunIdx`,`_viewFrac`,
   `_lastSeekGeneration`. **These index sentinels are written from inside
   `build()`/the itemBuilder and read back from async callbacks** — the coupling
   that makes the converge logic so fragile.
4. **The lens system.** `_setLens` (`:1597`), `_isErrorsSummaryLens` (`:748`),
   `_buildErrorsSummaryList` (`:1732`), `_sortedErrorSeqs` (`:755`); field
   `_lens`.
5. **Session-cost polling.** `_startSessionCostPolling` (`:613`),
   `_fetchSessionCost` (`:624`); fields `_sessionCost`,`_sessionCostTimer`. A
   self-contained 15s poll that has nothing to do with the feed list.
6. **Parent-callback forwarding** (debounced "fire once per change").
   `_maybeFireModeModelChanged` (`:3000`), `_maybeFireSessionNameHint` (`:3024`),
   `_maybeFireStatusLineChanged` (`:3046`), `_latestModeModelData` (`:2960`),
   `_modeModelSig` (`:2982`); the `_last*Sig` / `_last*Set` fields. Pure
   change-detection plumbing wrapped around `widget.on*` callbacks.

## Claim 2 — `build()` is 1077 lines and does the wrong kind of work

`build()` (`:1866`–`:2943`) is not just "a big tree." It runs **heavy
computation that does not belong in build** and recomputes it every frame:

- **Telemetry / cost / context aggregation** (~`:2100`–`:2300`): per-model
  token rollups, dominant-model selection, context-window capacity, "used"
  derivation. Pure over `_events` — belongs in `feed_reducer.dart` returning a
  struct.
- **Fold maps** (~`:1945`–`:2050`): `toolNames`, `toolResults`, `toolUpdates`,
  `resolvedApprovals`. Pure over `_events`; partially duplicates what the digest
  fold already computes server-side.
- **Lens filtering + match index + minimap marks + per-lens counts**
  (`:2326`–`:2540`): derivable; some already lives in `feed_reducer`.
- Only the **last third** (`:2609`–`:2916`) is the actual widget tree (the
  `ListView`/`_buildErrorsSummaryList`, then a `Stack` of floating chrome:
  funnel, position pill, minimap, stepper, verbose/expand, compose).

Because the aggregation and the seek-index sentinels both live in `build()`,
the frame is where transport state, derived analytics, and scroll-machine
bookkeeping all entangle — which is exactly why a "small" nav fix risks a
regression somewhere unrelated.

## Claim 3 — the name is misleading (the director is right)

`AgentFeed` began as a live append-only **stream** (SSE tail-follow + a
composer). It now also serves, behind `randomAccess: true` (set only by
`session_analysis_view.dart:180`), a **sealed, read-only, random-access
analysis transcript** — the *table* half of the stream/table duality in
[ADR-039](../decisions/039-insight-lens-as-server-query.md). In that mode it has
no live tail, no composer, no telemetry strip (as of `8e2e6cb`), and its
navigation is lens-as-query rather than scroll-the-stream. "Feed" names only the
stream half. A name like **`AgentTranscript`** (file `agent_transcript.dart`)
covers both the live and sealed readings; "feed" then survives as the *mode*,
not the whole widget. (The `dense` flag is a second, orthogonal axis —
constrained sheet vs. full screen — and is fine as a flag.)

Five call sites consume the widget — `session_analysis_view.dart`,
`screens/sessions/{sessions,transcript}_screen.dart`,
`screens/projects/{projects,archived_agents}_screen.dart` — so a rename is
mechanical but wide; it should be its own `refactor:`-prefixed commit (mirroring
the `docs:`-only-reorg convention) so the diff stays reviewable.

## Options

### Option A — extract controllers along the existing seams (recommended first)

Behavior-preserving lifts, each its own gated commit, in rough
biggest-win-first order:

1. **`FeedAggregates` (pure).** Move the telemetry/cost/context/fold-map
   computation out of `build()` into `feed_reducer.dart` (or a sibling) as a
   pure function returning a struct. Biggest single shrink of `build()`; no
   behavior change; unit-testable without a widget.
2. **`FeedSeekMachine` (controller).** Lift the seek/converge cluster (Claim 1
   §3) into one object that owns the index sentinels and exposes intent methods
   (`landOnSeq`, `stepFunnel`, `jumpToOrdinal`). The itemBuilder feeds it the
   realized-row window through a narrow callback instead of writing fields
   directly. This is where the recurring landing bugs live, so isolating it pays
   twice: testable, and the coupling to `build()` becomes explicit.
3. **`FeedTransport` (controller).** Lift live-tail load/SSE/reconnect/paging +
   the random-access window reset/forward-pager. `random_access_loader.dart`
   already started this seam; finish it so the State holds a transport, not a
   dozen cursor fields.
4. **Small mixins** for cost-polling (§5) and parent-callback forwarding (§6) —
   self-contained, low-risk, remove ~10 fields between them.

After A, `build()` should be mostly tree assembly and `_AgentFeedState` a thin
coordinator. **Gate every step on the director's device-test** (Flutter is
untestable locally); the pure extractions (1) get unit tests, mirroring
`agent_feed_random_access_loader_test.dart`.

### Option B — split the widget by mode (the deeper fix, after A)

Once the seams are clean, the stream/table duality can become two widgets over
shared controllers: `LiveFeed` (stream: SSE tail-follow + composer, no random
access) and `AgentTranscript`/`InsightTranscript` (table: sealed,
random-access, lens-as-query, no composer/telemetry). Shared rendering stays in
`agent_feed/`. Higher churn (the 5 consumers, the flags), so it should follow A,
not lead — and only if A doesn't already make the flags cheap enough to keep.

### Option C — rename only

Cheapest; addresses Claim 3 but not 1–2. Worth doing regardless, as its own
commit, but it is not "the audit's fix."

## How the deferred items land on this

- **Point 6 — unified lens selector** (fold the `_TurnsDisclosure` index +
  the floating funnel into one foldable filter; see
  `session_analysis_view.dart:148` and the funnel at `agent_feed.dart:2762`).
  Lands cleanly once the **lens system** (§4) and **seek machine** (§3) are
  isolated: the selector becomes one control driving `_lens` + the seek machine,
  and Turns renders as a summary list like Errors (`_buildErrorsSummaryList`).
- **Point 3 — text-lens far jump.** Root cause (verified): Text/Tools have no
  whole-run anchor list, so a match outside the loaded window is unreachable;
  within-window landing already uses the same convergent seek as Turns/Errors.
  The fix is the **paged Text/Tools lens** ([ADR-039](../decisions/039-insight-lens-as-server-query.md)
  P1b) — a `kind=`-keyset buffer the seek machine pages. It belongs on the
  extracted `FeedTransport` + `FeedSeekMachine`, which is why it was deferred
  into this audit rather than band-aided into the current `build()`.

## Risks

- **Regression in the live Feed.** Every extraction must keep the live-tail path
  byte-identical (the gate-for-no-regression discipline that protected
  v1.0.785–790). Lift behavior verbatim first; refactor the *shape* only after.
- **Seek machine is load-bearing and subtle** (programmatic-scroll depth, the
  realized-window reset, tail-follow re-arm). Extract it with its existing
  comments intact and a device-test gate per step.
- **Rename churn.** Keep the rename in its own commit, after the logic settles,
  so a behavioral diff is never hidden inside a 5-file path change.

## Related

- [`discussions/monolith-refactor.md`](monolith-refactor.md) — the general
  extraction discipline this follows.
- [`discussions/insight-navigation-fixed-pages.md`](insight-navigation-fixed-pages.md),
  [`discussions/transcript-paging-vs-forum-model.md`](transcript-paging-vs-forum-model.md)
  — the navigation/data-model substrate the seek machine implements.
- [ADR-039](../decisions/039-insight-lens-as-server-query.md) and
  [`plans/insight-lens-as-query.md`](../plans/insight-lens-as-query.md) — the
  lens-as-query work (P1b) that rides on this decomposition.
