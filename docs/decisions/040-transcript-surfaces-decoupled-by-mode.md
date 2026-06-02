# 040. Decouple the transcript surfaces — one file per mode, additive by file

> **Type:** decision
> **Status:** Accepted (2026-06-02) — directed by the director after the
> [`agent_feed.dart` audit](../discussions/agent-feed-decomposition.md):
> "the refactor needs fully decoupled and modular design; the live feed and the
> insight mode should fully decouple, each their own file; once a new
> function/feature/mode is added, it is added as a new file, not by patching the
> old file." Supersedes the audit's "extract-then-maybe-split" ordering with a
> committed end state. Implemented by
> [`plans/transcript-surface-decoupling.md`](../plans/transcript-surface-decoupling.md).
> **Audience:** contributors
> **Last verified vs code:** post-`8e2e6cb`.

**TL;DR.** Today one widget — `AgentFeed` in `lib/widgets/agent_feed.dart`
(3108 lines, a ~40-field / ~50-method `_AgentFeedState` under a 1077-line
`build()`) — serves **two genuinely different surfaces** behind a `randomAccess`
flag: a live append-only **stream** (SSE tail-follow + composer) and a sealed,
read-only **analysis transcript** (random-access, lens-as-query). We split them
into **separate files, each owning its own widget + State**, over a **shared,
mode-agnostic substrate**. The rule going forward is **open/closed by file**: a
new mode or feature is a *new file* composing the substrate, never a new flag or
branch inside an existing mode's file.

## Context

`AgentFeed` began as the live feed and accreted the Insight analysis surface as
a second personality switched by `randomAccess: true` (set only at
`session_analysis_view.dart:180`) and a `dense` layout flag. The two surfaces
share rendering but not invariants: the stream tails a live window and accepts
input; the table is sealed, never tails, has no composer/telemetry, and
navigates by server-keyset lens queries. Bundling them produced the entanglement
the audit documents — transport state, per-frame analytics, and the
scroll/seek machine all resolving in one `build()`, so a localized nav fix risks
an unrelated regression (the v1.0.788–790 landing-bug arc). The director's
direction makes the separation a hard architectural boundary, not a cleanup
preference.

## Decision

1. **One file per mode.** The live stream and the Insight transcript are
   separate widgets in separate files — `live_feed.dart` (`LiveFeed`) and
   `insight_transcript.dart` (`InsightTranscript`). Neither imports the other.
   No `randomAccess`-style flag selects mode-behaviour inside a shared State.
2. **A shared, mode-agnostic substrate.** Everything both modes need lives in
   reusable, mode-ignorant units under `lib/widgets/transcript/` (the rename of
   `agent_feed/`): the render primitives (already extracted) plus pure
   controllers — `FeedAggregates` (telemetry/cost/fold, pure over an event
   list), `TranscriptSeek` (the scroll/converge machine), `FeedTransport` (live
   load/SSE/reconnect/paging), and the existing `RandomAccessLoader` (the sealed
   loader). Substrate units know nothing about which mode hosts them.
3. **Open/closed by file.** Adding a mode (e.g. a search view, a diff view) or a
   mode-specific feature means **a new file** that composes substrate units —
   not a branch, flag, or `if (mode == …)` inside an existing mode file. Shared
   behaviour that two modes genuinely need is promoted *into the substrate*, not
   copied or special-cased.
4. **`dense` stays a layout parameter, not a mode.** Constrained-sheet vs.
   full-screen is an orthogonal layout axis each mode may accept; it is not a
   second surface.
5. **The name follows the role.** "Feed" names the live stream only; the sealed
   surface is a transcript. The directory and shared types use *transcript* as
   the umbrella term; `LiveFeed` keeps "feed" for the streaming mode.

## Consequences

- **Enables the deferred work cleanly.** Point 6 (one lens selector) is owned by
  `InsightTranscript`; point 3 (paged Text/Tools lens, ADR-039 P1b) rides on the
  substrate loader + `TranscriptSeek` — neither touches `LiveFeed`.
- **No-regression discipline still governs the transition.** The substrate is
  extracted by lifting current behaviour verbatim (unit-tested where pure); the
  mode files are assembled from it; the monolith is deleted only once both modes
  render through the new files and the director's device-test passes. Flutter is
  untestable locally, so each phase gates on that device-test.
- **Five call sites migrate** (`session_analysis_view.dart`,
  `screens/sessions/{sessions,transcript}_screen.dart`,
  `screens/projects/{projects,archived_agents}_screen.dart`): Insight hosts swap
  to `InsightTranscript`, the rest to `LiveFeed`.
- **Cost:** more files and some duplication of trivial glue between the two
  modes — accepted deliberately, because the alternative (shared State with mode
  flags) is the very coupling being removed.

## Alternatives considered

- **Extract controllers but keep one widget** (the audit's Option A as an end
  state). Rejected per direction: it leaves the two surfaces sharing a State and
  a `build()`, so mode behaviour still interleaves and the open/closed rule
  can't hold.
- **Rename only.** Addresses the name, not the coupling. Done anyway as part of
  the substrate rename.

## Related

- [`discussions/agent-feed-decomposition.md`](../discussions/agent-feed-decomposition.md)
  — the audit this decides on.
- [ADR-039](039-insight-lens-as-server-query.md) /
  [`plans/insight-lens-as-query.md`](../plans/insight-lens-as-query.md) — the
  lens-as-query work that lands on the decoupled substrate.
- [`discussions/monolith-refactor.md`](../discussions/monolith-refactor.md) — the
  extraction discipline.
- [`plans/transcript-surface-decoupling.md`](../plans/transcript-surface-decoupling.md)
  — the phased execution.
