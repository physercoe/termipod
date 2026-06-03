# Decouple the transcript surfaces — phased execution

> **Type:** plan
> **Status:** Proposed (2026-06-02) — implements
> [ADR-040](../decisions/040-transcript-surfaces-decoupled-by-mode.md). Not
> started. Each phase gates on the director's device-test (Flutter is
> untestable locally); pure extractions carry unit tests.
> **Audience:** contributors
> **Last verified vs code:** post-`afb3cb9` (P3 + P4 done; awaiting device-test).

**TL;DR.** Turn the one flag-switched `AgentFeed`
(`lib/widgets/agent_feed.dart`) into two mode files — `LiveFeed` and
`InsightTranscript` — over a shared, mode-agnostic substrate under
`lib/widgets/transcript/`. Extract the substrate first (verbatim behaviour),
assemble each mode from it, migrate the five call sites, then delete the
monolith. Open/closed by file thereafter: a new mode is a new file
([ADR-040](../decisions/040-transcript-surfaces-decoupled-by-mode.md)).

## Target layout

```
lib/widgets/transcript/            (rename of agent_feed/)
  event_card.dart  feed_misc.dart  feed_reducer.dart  …   ← render primitives (exist)
  random_access_loader.dart                               ← sealed-mode loader (exists)
  fold_maps.dart         ← DONE (P1a): pure per-event fold — SHARED
  feed_telemetry.dart    ← DONE (P1b): pure telemetry/cost/context rollup — LiveFeed-only
  transcript_seek.dart   ← DONE (P2a): the landing engine (converge + sentinels + guard) — SHARED
  feed_lens.dart (or in feed_reducer): FeedLens predicate + funnel widget — SHARED (filter only)
lib/widgets/insight_transcript.dart ← P3: InsightTranscript (own buffer via RandomAccessLoader,
                                            seek orchestration, lens-as-query, minimap)
lib/widgets/live_feed.dart          ← P4: rename of agent_feed.dart (live-tail loader in its own
                                            State, composer, telemetry, declutter funnel)
```

There is **no shared `feed_transport.dart`** — the buffer + loaders bifurcate by
mode and live inside each mode's State (see the scoping correction below).

Substrate units take data + callbacks, never a `mode`/`randomAccess` flag.
`dense` is a layout parameter each mode may accept, not a mode. Per ADR-040 the
**lens-as-query engine, minimap, summary lists, and random-access seek are
Insight-only**; the **`FeedLens` predicate + funnel widget are shared** so
`LiveFeed` can client-side filter its loaded window (declutter), with no jump /
minimap / summary list.

## Phases

Order matters: extract shared pieces (no behaviour change) → build mode files on
them → migrate consumers → delete monolith. Lift behaviour **verbatim** first;
reshape only after the seam is proven.

### P0 — substrate rename + seam prep (mechanical)

- Rename `lib/widgets/agent_feed/` → `lib/widgets/transcript/`, fix imports. Own
  `refactor:` commit, no logic change. (`agent_feed.dart` stays put this phase.)
- Confirm the five consumers still build (CI analyze).

### P1 — `FoldMaps` (shared) + `FeedTelemetry` (LiveFeed-only) (pure; biggest `build()` shrink)

Per ADR-040 open-question B, the `build()` aggregation splits by ownership:

- **`fold_maps.dart` (substrate, shared).** The per-event fold —
  `toolNames`/`toolResults`/`toolUpdates`/`resolvedApprovals` (`agent_feed.dart`
  ~`:1945`–`:2050`) — pure over an event list, returning a struct. Both modes
  render cards and evaluate lens predicates from it.
- **`feed_telemetry.dart` (LiveFeed-only).** The telemetry/cost/context rollup
  (`modelTotals`, context-window capacity/used — ~`:2100`–`:2300`), pure,
  returning a struct. Insight does **not** import it (its dashboard is the
  digest `RunReportCard`).
- Unit-test both (mirror `agent_feed_random_access_loader_test.dart`) — no widget
  needed.
- **Gate:** Feed renders byte-identical telemetry + cards; Insight renders
  byte-identical cards (it never showed the telemetry) on device.

### P2 — `TranscriptSeek` (the scroll/converge machine)

- **P2a — DONE (`f9677c2`, CI green).** The *landing engine* — the realized-row
  window sentinels, the programmatic-scroll guard, the seek `GlobalKey`, and the
  `convergeToIndex` algorithm — lifted verbatim into
  `transcript/transcript_seek.dart`. The host drives it (`beginFrame` /
  `recordBuiltRow` each layout) and reads back `isProgrammatic` /
  `lastTopBuiltSeq`; `_seekToLoadedIndex` delegates to `_seek.landOnIndex`. Pure
  sentinel bookkeeping unit-tested.
- **P2b — DEFERRED into P4 (director's call, 2026-06-02).** Folding the seek
  *orchestration* — the `_activeSeekSeq` anchor (the State's most cross-cutting
  field, ~13 read/write sites incl. the build matchIndex/isTarget logic, the
  lens stepper, and `_onScroll`'s tail-return clear), the funnel-run jumps, and
  the pending-context mechanism — is a ~20-site rewiring of fragile state. It is
  **Insight-specific** navigation, so rather than rewire it in-place on the
  monolith (stacking a second unverified seek change before any device-test), it
  moves into `InsightTranscript` at P4, where it's device-tested in context.
- **Gate (P2a):** all jumps (funnel, stepper, minimap, deep-link,
  jump-to-ordinal) land as before on device.

### Scoping correction — no shared `FeedTransport` (2026-06-02)

The original P3 (a shared `FeedTransport` controller) was scoped and **dropped.**
The data layer is the State's core mass and bifurcates by mode: `_events` alone
has ~30 references (FoldMaps, FeedTelemetry, the build filter, the seek's
`_seqIsLoaded`, …), plus `_followTail` (19), `_error` (16),
`_windowHasTail`/`_minSeq` (15 each), and a dozen more cursors/flags. And the two
modes don't share a loader — `LiveFeed` loads via tail-page + SSE + reconnect;
`InsightTranscript` loads via the `(ts,seq)` random-access keyset
(`RandomAccessLoader`, already extracted). A shared `FeedTransport` would be a
thin shell over two divergent flows that P4 would partly undo. So the buffer +
loaders **stay with each mode** and get their decoupled home in the mode split
below — that *is* the transport decoupling.

### P3 — build `InsightTranscript` (the sealed / random-access mode) — DONE (`ba4cbbd`, CI-green, awaiting device-test)

Extracted the Insight mode first — it had a single consumer
(`session_analysis_view.dart`) and is where Insight's loader + the deferred P2b
seek orchestration + the lens-as-query engine all got their decoupled home.

- New `lib/widgets/insight_transcript.dart` (`InsightTranscript`): its own State
  owning a random-access event buffer fed by `RandomAccessLoader`, composing
  `FoldMaps` (cards) + `TranscriptSeek` (landing) + the **seek orchestration**
  lifted from the monolith (the deferred P2b: the `_activeSeekSeq` anchor, the
  funnel-run jumps, the pending-context "view in context", `_resetWindowAround`/
  the forward pager) + the lens-as-query engine (Errors/Turns summary lists, the
  whole-run minimap, the N/M ordinal + stepper-as-cursor). The `randomAccess` /
  `dense` flags are resolved to their Insight values (always-true / always-false)
  rather than carried — the live branches don't exist in this file.
- **Sealed-dataset semantics (ADR-040 §E):** snapshot-on-entry (one read-through
  fetch, cache fallback) + manual refresh — **No** composer, **No** telemetry
  strip, **No live SSE tail**. This is the one deliberate *behaviour* change vs.
  the monolith's Insight path, which DID SSE-tail when its window reached the
  live tail: a *running* agent's Insight transcript no longer auto-updates (it
  re-snapshots on re-entry; the host's RefreshIndicator re-pulls the digest).
  Sealed / terminated runs — the dominant Insight case — are unaffected (SSE was
  silent there). **Device-test this deliberately.**
- The dashboard→transcript jump channel moved to the substrate as
  `transcript/seek_controller.dart` (`TranscriptSeekController`); `agent_feed.dart`
  keeps an `AgentFeedSeekController` typedef alias so its call sites +
  `agent_feed_seek_controller_test.dart` compile unchanged until P4.
- Migrated `session_analysis_view.dart` → `InsightTranscript` +
  `TranscriptSeekController`. `agent_feed.dart` keeps serving the four live
  consumers unchanged (never a `randomAccess`-delegating shim — open-question C).
- **Gate (pending):** the Insight surface (digest dashboard + transcript + every
  lens / funnel / stepper / minimap jump) is parity-or-better vs. the live
  `agent_feed.dart` Insight path, on device — plus the §E liveness change above.

### P4 — strip + rename the remainder to `LiveFeed` — DONE (`0e18b11` strip + `afb3cb9` rename, CI-green)

- **P4a strip (`0e18b11`, +fix `61d7e56`).** Removed `agent_feed.dart`'s
  now-dead code — but only what was **inert when `randomAccess == false`** (i.e.
  on every live consumer): the `randomAccess` / `seekController` /
  `totalEventCount` / `runErrorSeqs`/`runTurnSeqs`/… params, the `(ts,seq)`
  keyset loader + forward pager, the external-seek channel, and the digest /
  whole-run lens-as-query (Errors summary list, funnel-run jumps, whole-run
  minimap, N/M position pill, jump-to-ordinal). 894 lines removed.
- **Scoping note vs. the original §6 plan.** The **loaded-window** minimap +
  turn-stepper + funnel match-stepper are `dense`-gated, **not**
  `randomAccess`-gated, so they were active in the live full-screen feed
  (`TranscriptScreen`) and stay in `LiveFeed` — P4a kept them to guarantee
  *zero live behaviour change*. So LiveFeed's funnel is filter **+ loaded-window
  stepper** (the shipped v1.0.770 pill), and it retains a loaded-window minimap —
  a touch more than ADR-040 §6's "filter only / no minimap". Tightening LiveFeed
  to a pure declutter filter (dropping its loaded-window minimap/stepper) is a
  deliberate **follow-up** (device-tested on its own), not bundled into the
  behaviour-preserving rename.
- **P4b rename (`afb3cb9`).** `git mv agent_feed.dart → live_feed.dart`,
  `AgentFeed`→`LiveFeed`; dropped the `AgentFeedSeekController` typedef; migrated
  the 4 live consumers + the reducer/widget test imports; moved
  `agent_feed_seek_controller_test.dart` → `transcript_seek_controller_test.dart`
  (now testing `TranscriptSeekController` directly). Pure rename.
- **Result:** Insight (`InsightTranscript`) and live (`LiveFeed`) each own their
  file over the shared `transcript/` substrate. ADR-040 is structurally complete
  — a new mode is a new file.
- **Gate (pending device-test):** every live surface renders + tails +
  loads-older + composes as before; no importer of `agent_feed.dart` remains.
  CI + CodeQL green.

### P5 — land the deferred features ON the new structure (open/closed)

- **Point 6 — unified lens selector** inside `InsightTranscript`: fold the
  `_TurnsDisclosure` + funnel into one foldable selector; Turns renders as a
  summary list like Errors. No change to `LiveFeed`.
- **Point 3 — paged Text/Tools lens** (ADR-039 P1b): a `kind=`-keyset buffer the
  `RandomAccessLoader` + `TranscriptSeek` page, so far text/tools matches are
  reachable and land by index. Owned by `InsightTranscript`.
- **LiveFeed §6 tightening (optional).** Reduce `LiveFeed`'s funnel to a pure
  declutter filter and drop its loaded-window minimap + steppers (ADR-040 §6 /
  open-question A), so navigation lives only in Insight. Deferred from P4b to
  keep that a zero-behaviour-change rename; do it as its own device-tested change
  only if the director still wants the live full-screen feed's minimap/stepper
  gone.

## Risks

- **Live-feed regression** — the substrate extractions (P1, P2a) lifted
  behaviour verbatim; the P3/P4 mode split must keep each surface's loading +
  rendering parity. The live path protected the v1.0.785–790 arc.
- **The seek machine is subtle** (programmatic-scroll depth, realized-window
  reset) — P2a isolated it; the orchestration moving in P3 must preserve the
  anchor/tail-follow interplay. Device-gate the Insight surface hard.
- **Migration window** — `agent_feed.dart` keeps serving the four live consumers
  through P3 (Insight extracted first); it is renamed to `LiveFeed` in P4, never
  turned into a `randomAccess`-delegating shim (open-question C).

## Related

- [ADR-040](../decisions/040-transcript-surfaces-decoupled-by-mode.md) — the
  decision.
- [`discussions/agent-feed-decomposition.md`](../discussions/agent-feed-decomposition.md)
  — the audit.
- [ADR-039](../decisions/039-insight-lens-as-server-query.md) /
  [`plans/insight-lens-as-query.md`](../plans/insight-lens-as-query.md) — P1b,
  landed in P5.
