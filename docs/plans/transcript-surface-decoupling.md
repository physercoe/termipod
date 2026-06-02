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
  feed_aggregates.dart   ← NEW: pure telemetry/cost/fold over an event list
  transcript_seek.dart   ← NEW: the scroll/converge/seek machine (controller)
  feed_transport.dart    ← NEW: live load / SSE / reconnect / load-older (controller)
lib/widgets/live_feed.dart          ← NEW: LiveFeed (stream + composer + telemetry)
lib/widgets/insight_transcript.dart ← NEW: InsightTranscript (sealed, random-access, lens-as-query)
lib/widgets/agent_feed.dart         ← deleted at the end (or a thin re-export during migration)
```

Substrate units take data + callbacks, never a `mode`/`randomAccess` flag.
`dense` is a layout parameter each mode may accept, not a mode.

## Phases

Order matters: extract shared pieces (no behaviour change) → build mode files on
them → migrate consumers → delete monolith. Lift behaviour **verbatim** first;
reshape only after the seam is proven.

### P0 — substrate rename + seam prep (mechanical)

- Rename `lib/widgets/agent_feed/` → `lib/widgets/transcript/`, fix imports. Own
  `refactor:` commit, no logic change. (`agent_feed.dart` stays put this phase.)
- Confirm the five consumers still build (CI analyze).

### P1 — `FeedAggregates` (pure; biggest `build()` shrink)

- Move the telemetry/cost/context/fold-map computation out of `build()`
  (`agent_feed.dart` ~`:1945`–`:2300`) into `transcript/feed_aggregates.dart` as
  a pure function returning a struct consumed by `build()`.
- Unit-test it (mirrors `agent_feed_random_access_loader_test.dart`) — no widget
  needed.
- **Gate:** Feed + Insight render byte-identical telemetry on device.

### P2 — `TranscriptSeek` (the scroll/converge machine)

- Lift the seek cluster (the converge/jump/funnel methods + their index
  sentinels — audit §3) into `transcript/transcript_seek.dart` as a controller
  bound to a `ScrollController` + a realized-window feedback callback. The
  itemBuilder reports its realized rows through that callback instead of writing
  State fields directly.
- Keep the existing comments (programmatic-scroll depth, window reset, tail-
  follow re-arm) intact — load-bearing.
- **Gate:** all jumps (funnel, stepper, minimap, deep-link, jump-to-ordinal)
  land as before on device.

### P3 — `FeedTransport` (live-tail loader)

- Lift bootstrap/SSE/reconnect/banner/load-older/ingest/dedup into
  `transcript/feed_transport.dart`. `random_access_loader.dart` already owns the
  sealed loader; this completes the pair.
- **Gate:** live tail-follow, reconnect, offline banner, load-older unchanged.

### P4 — split into mode files

- `live_feed.dart` (`LiveFeed`): composes `FeedTransport` + `FeedAggregates` +
  cards + composer + telemetry strip. No random access, no lens-as-query table.
- `insight_transcript.dart` (`InsightTranscript`): composes
  `RandomAccessLoader` + `TranscriptSeek` + `FeedAggregates` + cards + the lens
  system (Errors/Turns summary lists, minimap); no composer/telemetry.
- During migration `agent_feed.dart` may become a thin re-export so consumers
  move one at a time.
- **Gate:** both surfaces render through the new files; device-test parity.

### P5 — migrate consumers + delete the monolith

- Insight host (`session_analysis_view.dart`) → `InsightTranscript`; the live
  hosts (`sessions_screen`, `transcript_screen`, `projects_screen`,
  `archived_agents_screen`) → `LiveFeed`.
- Delete `agent_feed.dart` (and the re-export) once nothing imports it.
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
- **Migration window** — the thin re-export keeps the tree green while consumers
  move; don't delete `agent_feed.dart` until grep shows no importers.

## Related

- [ADR-040](../decisions/040-transcript-surfaces-decoupled-by-mode.md) — the
  decision.
- [`discussions/agent-feed-decomposition.md`](../discussions/agent-feed-decomposition.md)
  — the audit.
- [ADR-039](../decisions/039-insight-lens-as-server-query.md) /
  [`plans/insight-lens-as-query.md`](../plans/insight-lens-as-query.md) — P1b,
  landed in P6.
