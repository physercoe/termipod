# 039. Insight lens as a server-side keyset query

> **Type:** decision
> **Status:** Proposed (2026-06-02) — the consumer-side complement to
> [ADR-038](038-per-run-event-digest.md) (the sealed-side data model) and the
> resolution of the open questions in
> [`discussions/transcript-paging-vs-forum-model.md`](../discussions/transcript-paging-vs-forum-model.md)
> §§7–13 and
> [`discussions/insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md).
> Implemented by [`plans/insight-lens-as-query.md`](../plans/insight-lens-as-query.md).
> **Audience:** contributors
> **Last verified vs code:** v1.0.789

**TL;DR.** The Insight transcript renders each filtered lens
(Errors / Turns / Text / Tools) as a **client-side filter over the live
Feed's tail-anchored loaded window** (`lib/widgets/agent_feed.dart:2232`),
while the funnel count and jumps come from the **whole-run digest**
(`runErrorSeqs` / `runTurnSeqs`). The two disagree: a lens is **empty whenever
its matches aren't in the loaded window** — and an empty list has no scroll
extent, so the "scroll up to load older" affordance (`agent_feed.dart:2629`,
load trigger at `:684`) can never fire; and reusing the live loader's
tail-follow (`:665–683`) plus filtered-prepend anchoring (`:908`) makes
scroll-up **jump to the end or the top**. Decide: on the **sealed/idle
(analysis) side, a lens *is* its own server-side keyset-paged query** — Errors
= the run's error list, Turns = the turn list, Text / Tools = kind-filtered
lists — **decoupled** from the live Feed loader. The hub already supports this
(`kind=` filter + the `(ts, seq)` keyset on `GET …/events`; plus `…/turns`,
`…/digest`); the work is a mobile Insight loader that pages the lens directly.
This realizes the stream/table split (live Feed keeps keyset tail-follow;
Insight is sealed-side random access) and fixes both bug classes **by
construction**.

## Context

ADR-038 established the **sealed-side data model**: a per-agent
`agent_event_digests` read model (count, turn index, error rollups) maintained
incrementally, and a queryable `agent_turns` turn index. That work shipped
(P0–P3). The recurring symptom it set out to fix — "the same run reports
different numbers in different places; a long log can't be navigated
accurately" — is now half-solved: the **data** is correct and whole-run, but
the **mobile Insight consumer never followed the data model to the cold side
for filtered views**.

The transcript-paging discussion (§§7–13) named the cause as **stream/table
duality**: one append-only log is consumed two incompatible ways —

- **As a stream** (live SSE tail-follow + keyset cursor) — the right model for
  the chat-like **Feed**, and the one ADR-038 §3 deliberately keeps.
- **As a bounded dataset** (counts, indexes, queries) — the right model for
  the **Insight / analysis** surface, which operates on a sealed (terminated /
  idle) or at-rest agent range.

The Insight surface (`SessionAnalysisView` → `AgentFeed(randomAccess: true)`)
inherited the **stream** loader and bolted a **client-side lens filter** on
top. Three grounded facts make that retrofit leak:

- **The rendered list is a filter of the loaded window, not the lens.**
  `lensed = _events.where(matchesLens)` (`agent_feed.dart:2232`). When the
  loaded ~200-event window contains none of the lens's matches, `lensed` is
  empty (`:2629` shows "No … events in the loaded transcript — scroll up to
  load older") — but an **empty `ListView` has no scroll extent**, so
  `_onScroll`'s load-older trigger (`pixels <= 120`, `:684`) never fires.
  Dead-end. Meanwhile the funnel shows `N/M` from the digest → "the funnel can
  be empty for a filtered item because its card isn't loaded" (**Issue 1**).
- **The live loader's invariants don't hold under a filter.** Tail-follow can
  re-enable on a near-bottom moment (`:665–683`) → the next event yanks to the
  **end**; a load-older prepend whose new rows are *mostly filtered out by the
  lens* yields a height delta ≈ 0, so the anchor math `priorPixels + delta`
  (`:908`) doesn't compensate → the viewport jumps to the **top**
  (**Issue 2**). The hot-path loader assumes an **unfiltered contiguous
  tail**; a filtered Insight view violates it.
- **The hub already exposes lens-as-query for kind-based lenses.**
  `GET …/events` accepts `kind=<a,b,c>` (`handlers_agent_events.go:259–342`)
  *and* the `(ts, seq)` compound keyset (`after_ts` / `before_ts` /
  `after_seq` / `before_seq`, `:225–256`). `…/turns` and `…/digest` endpoints
  exist. So "the Text / Tools / Turns lens is a keyset query over a kind set"
  needs **no new hub plumbing**. The one exception is **Errors**, a *derived*
  predicate (failed `tool_call_update` ∪ failed `turn.result`,
  `digest_fold.go`), not a single `kind`.

## Decision

### 1. Two read models for one sealed dataset

- **Live Feed** (`AgentFeed(randomAccess: false)`, `dense:*`) — **unchanged**:
  keyset tail-follow + SSE append + the existing load-older anchoring. The All
  view of the Insight surface also keeps this (it *is* the chronological
  stream).
- **Insight filtered lens** (`randomAccess: true`, lens ≠ All) — a
  **server-side keyset-paged query of the lens itself**, decoupled from the
  Feed loader. This is the new path.

### 2. A lens is its own keyset query

Each non-All lens renders a list fetched by the lens predicate **server-side**,
keyset-paged over `(ts, seq)`:

| Lens | Server query | Source |
|---|---|---|
| Text | `kind=text,thought` keyset | existing `…/events?kind=` |
| Tools | `kind=tool_call,tool_result` keyset | existing `…/events?kind=` |
| Turns | the turn index | existing `…/turns` (or `kind=turn.start` keyset) |
| Errors | the run's error events | **MVP:** the digest's whole-run error sample seqs (`runErrorSeqs`) fetched by seq; **follow-on:** a server `error=true` keyset reusing the canonical union in `digest_fold.go` |

The list is the lens. It can **never be empty-while-matches-exist**, and
scrolling pages the next slice **of that lens** — Issue 1 dissolves.

### 3. The funnel + stepper read the lens list's own index

Position is "match k of K" **within the lens list**, not "where does the
loaded window's filtered projection put it." The whole-run-vs-loaded-window
disagreement (and the dual-cursor class of bug fixed in v1.0.789's stepper
unification) is removed at the source: there is one list and one index.

### 4. Insight is static — no tail-follow, no SSE into a lens

A filtered lens does not follow the tail and does not append live SSE frames
(a sealed range is frozen; an at-rest live agent snapshots on lens entry). The
two scroll-jump conditions (§Context) cannot arise — Issue 2 dissolves. The
All view of a *live* agent still tails; **switching to a lens snapshots**.

### 5. Minimap

- **Tap-to-locate routes into the lens list:** a landmark tap is "seek to
  error #k / turn #k" *in that lens's server-backed list* — an exact,
  deterministic landing (load the lens page around index k), not an
  offset-convergence guess.
- **Monotonic tick position + drag-scrub** need the dense per-session ordinal
  / time-scale (`transcript-paging` §§4–5). This ADR does **not** require it;
  it is a **separable follow-on** (the digest already carries `event_count`
  and the ts range to power it). Listed so the refactor is not blocked on it.

## Consequences

- **Issues 1 and 2 are fixed by construction**, not patched — the empty-lens
  dead-end and the scroll-up jump both stem from filtering a live-tail window,
  which this path eliminates.
- **No throwaway code.** The targeted-patch alternative (auto-jump on lens
  entry + tail-follow suppression + anchor hardening) lives *inside* the model
  this replaces and would be deleted by it; building it first means fixing the
  same two bugs twice.
- **Hub is largely ready** — kind-based lenses reuse the existing `kind=` +
  keyset surface; only the full-fidelity Errors keyset is net-new (and the MVP
  routes around it via the digest's error seqs).
- **Scoped to avoid regression.** The live Feed and the Insight All view are
  untouched; only the filtered Insight lenses change loader. Consistent with
  the working model (director device-tests; gate-for-no-regression).
- **Open question — Errors fidelity.** Until the server `error=true` keyset
  lands, the Errors lens list is bounded by the digest's error **sample**
  completeness. Acceptable for MVP (the count stays whole-run from the digest;
  the *list* is the samples); the follow-on closes the gap.
- **Cost.** A new mobile per-lens keyset loader + per-lens fetch state; the
  All view keeps the existing buffer.

## Alternatives considered

- **A — targeted patches (rejected).** Auto-jump to the newest match on lens
  entry (fixes Issue 1's empty view) + suppress tail-follow and harden
  filtered-prepend anchoring in random-access mode (fixes Issue 2). Symptom-
  level, and discarded by this decision the moment it lands. Retained only as
  an optional *interim* relief if the refactor must wait.
- **B — forum-style numbered pages with `OFFSET` (rejected).** Regresses long
  logs (O(offset) at depth) and fights per-agent `seq`; already rejected in
  `transcript-paging` §§1–6. Keyset is the same O(log n) seek without a count
  dependency.
- **C — keep the client filter, only fix anchoring (rejected).** Fixes Issue 2
  but not Issue 1 (a lens whose matches aren't loaded stays empty).

## Related

- [ADR-038 — per-run event digest](038-per-run-event-digest.md) — the
  sealed-side data model this consumes.
- [`discussions/transcript-paging-vs-forum-model.md`](../discussions/transcript-paging-vs-forum-model.md)
  — stream/table duality; why keyset, not pages.
- [`discussions/insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md)
  — the landing-precision thread this resolves on the structural axis.
- [`plans/insight-lens-as-query.md`](../plans/insight-lens-as-query.md) — the
  phased implementation.
