# Decouple the transcript surfaces — phased execution

> **Type:** plan
> **Status:** Proposed (2026-06-02) — implements
> [ADR-040](../decisions/040-transcript-surfaces-decoupled-by-mode.md). Not
> started. Each phase gates on the director's device-test (Flutter is
> untestable locally); pure extractions carry unit tests.
> **Audience:** contributors
> **Last verified vs code:** post-`8e2e6cb`.

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
  fold_maps.dart         ← NEW: pure per-event fold (tool names/results/updates/approvals) — SHARED
  feed_telemetry.dart    ← NEW: pure telemetry/cost/context rollup — LiveFeed-only
  transcript_seek.dart   ← NEW: the scroll/converge/seek machine (controller) — Insight-only
  feed_transport.dart    ← NEW: live load / SSE / reconnect / load-older (controller) — LiveFeed
  feed_lens.dart (or in feed_reducer): FeedLens predicate + funnel widget — SHARED (filter only)
lib/widgets/live_feed.dart          ← NEW: LiveFeed (stream + composer + telemetry + declutter funnel)
lib/widgets/insight_transcript.dart ← NEW: InsightTranscript (sealed, random-access, lens-as-query, minimap)
lib/widgets/agent_feed.dart         ← deleted in P5 (coexists during migration; never a flag-delegating shim)
```

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

### Sequencing note (2026-06-02)

P3 (transport) runs **before** the P2b orchestration fold. P3 is behaviourally
independent of the seek changes (loading ≠ landing), so a device-test can
isolate a regression to one phase; stacking P2b on the not-yet-device-tested
P2a would entangle the two. The seek orchestration lands in P4 with the mode
split.

### P3 — `FeedTransport` (live-tail loader)

- Lift bootstrap/SSE/reconnect/banner/load-older/ingest/dedup into
  `transcript/feed_transport.dart`. `random_access_loader.dart` already owns the
  sealed loader; this completes the pair.
- **Gate:** live tail-follow, reconnect, offline banner, load-older unchanged.

### P4 — split into mode files

- `live_feed.dart` (`LiveFeed`): composes `FeedTransport` + `FoldMaps` +
  `FeedTelemetry` + cards + composer + telemetry strip + the shared declutter
  funnel (client-side filter over the loaded window). No random access, no
  lens-as-query, no minimap, no summary lists.
- `insight_transcript.dart` (`InsightTranscript`): composes
  `RandomAccessLoader` + `TranscriptSeek` + `FoldMaps` + cards + the lens-as-query
  engine (Errors/Turns summary lists, minimap, N/M + stepper). No composer, no
  telemetry strip.
- `agent_feed.dart` **coexists** during migration — it is NOT rewritten to
  delegate by `randomAccess` (open-question C). Consumers move per-mode in P5.
- **Gate:** both surfaces render through the new files; device-test parity.

### P5 — migrate consumers + delete the monolith

- Insight host (`session_analysis_view.dart`) → `InsightTranscript`; the live
  hosts (`sessions_screen`, `transcript_screen`, `projects_screen`,
  `archived_agents_screen`) → `LiveFeed`.
- Delete `agent_feed.dart` once nothing imports it.
- **Gate:** full device-test of every surface; CI + CodeQL green.

### P6 — land the deferred features ON the new structure (open/closed)

- **Point 6 — unified lens selector** inside `InsightTranscript`: fold the
  `_TurnsDisclosure` + funnel into one foldable selector; Turns renders as a
  summary list like Errors. No change to `LiveFeed`.
- **Point 3 — paged Text/Tools lens** (ADR-039 P1b): a `kind=`-keyset buffer the
  `RandomAccessLoader` + `TranscriptSeek` page, so far text/tools matches are
  reachable and land by index. Owned by `InsightTranscript`.

## Risks

- **Live-feed regression** — every extraction lifts behaviour verbatim; reshape
  later. The live path protected the v1.0.785–790 arc; keep it byte-identical
  through P0–P3.
- **The seek machine is subtle** (programmatic-scroll depth, realized-window
  reset). Extract with comments intact; device-gate P2 hard.
- **Migration window** — `agent_feed.dart` coexists with the two mode files
  while consumers move per-mode; don't delete it until grep shows no importers.
  It is never turned into a `randomAccess`-delegating shim (open-question C).

## Related

- [ADR-040](../decisions/040-transcript-surfaces-decoupled-by-mode.md) — the
  decision.
- [`discussions/agent-feed-decomposition.md`](../discussions/agent-feed-decomposition.md)
  — the audit.
- [ADR-039](../decisions/039-insight-lens-as-server-query.md) /
  [`plans/insight-lens-as-query.md`](../plans/insight-lens-as-query.md) — P1b,
  landed in P6.
