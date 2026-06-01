---
name: Transcript paging vs the forum/discussion-board model
description: A tester observed that the agent transcript is an append-only growing log much like a forum/discussion-board thread, and those sites offer page numbers and direct page navigation — so why does our transcript use reverse infinite-scroll instead? This doc reconstructs the forum data model (a per-thread monotonic post number + a denormalized reply count + offset-or-keyset pagination over request/response page loads) and contrasts it with the transcript's model (SSE live tail-follow + keyset cursor pagination over per-agent `seq`, with multi-agent sessions ordered only by `ts`, and no maintained total). Concludes that the single thing forums have that we lack — a maintained total count + a dense per-thread ordinal — is also exactly what would make our minimap position indicator monotonic and enable page jumps, and sketches the cost of borrowing it (a denormalized per-session event counter + a stable session ordinal) versus keeping keyset infinite-scroll for the live-follow case. Relates to the agent-transcript plan §8.
---

# Transcript paging vs the forum/discussion-board model

> **Type:** discussion
> **Status:** Open (2026-06-01) — raised by a tester after the v1.0.775–778
> transcript-nav work: the append-only transcript "reminds me of a
> discussion board; those have pages and navigation — what's their data
> model, why not ours?". A fair challenge: the same recurring complaint
> (non-monotonic position indicator) is a symptom of the one thing the
> forum model has and ours doesn't.
> **Audience:** contributors
> **Last verified vs code:** v1.0.778

**TL;DR.** Forums *can* page a growing thread because they maintain a
**denormalized post count** and a **dense per-thread post number**, and
because a thread is **request/response** content (you load a static page),
not a live tail you follow in real time. Our transcript is a **live SSE
tail** over **per-agent `seq`** (a resumed session spans several agents, so
only `ts` totally orders it) with **no maintained count** — so page indices
would drift on every event and `OFFSET` jumps would be O(offset) on exactly
the long logs they'd serve. We deliberately chose **keyset (cursor)
pagination** to avoid needing a count. The cost of that choice is the
thing testers keep hitting: with no total, a position indicator can't be
both normalized and jump-free. The highest-value borrow from the forum
model is therefore *not* page numbers — it's a **maintained total**, which
would give a true `event N of M` position.

## 1. What a forum/discussion board actually does

The classic phpBB/Discourse-era thread model:

- **A per-thread monotonic post number.** Post #1, #2, … #N within the
  thread — a dense integer ordinal, assigned on insert. (Distinct from the
  global post id.)
- **A denormalized `reply_count` / `post_count` on the thread row.**
  Incremented on every new post in the same transaction. Reading it is
  O(1); it is the authority for "how many pages" (`ceil(count / pageSize)`)
  and for "post #X is on page ⌈X / pageSize⌉".
- **Pagination is offset *or* keyset:**
  - Numbered pages use `LIMIT pageSize OFFSET (page-1)*pageSize` — fine
    because threads are bounded (hundreds, not 100k) and pages are loaded
    one at a time, so deep offsets are rare.
  - "Jump to post #X" / permalinks use a **keyset seek** on the post
    number or id (`WHERE post_no >= X`), not offset.
- **Request/response, not a followed tail.** You load page 3; it's static
  until you refresh. New posts append at the end; you are not watching the
  thread auto-scroll as others type. Page boundaries *do* drift when posts
  are added, but because navigation is explicit page loads (and people
  read front-to-back or jump to "last page"), the drift is tolerable.

So the forum gets page numbers from three properties: **(a) a cheap
maintained count, (b) a dense ordinal, (c) static page loads.**

## 2. What the transcript does (grounded)

See [the agent-transcript plan §8](../plans/agent-transcript-debug-and-header-parity.md)
for the full picture; in short:

- **Live tail-follow over SSE.** `AgentFeed` cold-opens the newest page
  (`tail=true`, `_pageSize = 200`), then *streams* new events and
  auto-scrolls to the tail unless the user scrolled up
  (`agent_feed.dart`). This is a chat/terminal surface, not a static
  document.
- **Keyset cursor pagination, never offset.** Scrolling near the top pages
  older events with a cursor — `before=<minSeq>` (agent-scoped) or
  `before_ts=<oldestTs>` (session-scoped) — server precedence
  `before_ts > before > tail > since` (`hub/internal/server/handlers_agent_events.go`).
  The handler only ever runs `LIMIT`; it **never runs `COUNT`** and exposes
  **no offset**.
- **Per-agent `seq`, multi-agent sessions.** `seq` is monotonic *per
  agent*. A resumed session spans multiple agents, so there is **no dense
  global ordinal** across the session — only `ts` totally orders it. You
  cannot compute "page 7" from a `ts` without an `OFFSET` scan.
- **High volume / churn.** A long-running agent is 10k–100k+ events at a
  high write rate.

## 3. Why the forum's three enablers don't hold here

| Forum enabler | Transcript reality |
|---|---|
| Cheap maintained count | None. Counting is O(n) and a *moving target* while live. |
| Dense per-thread ordinal | `seq` is per-agent; a session has no dense global index (only `ts`). |
| Static page loads | Live SSE tail the user follows; page indices would shift on every frame. |
| Bounded volume | 100k+ events; `OFFSET` at depth is O(offset) — regresses the long logs. |

This is why the nav work landed on **keyset infinite-scroll + relative
navigation** (jump-to-latest, turn stepper, minimap scrub) rather than
numbered pages. Keyset is O(log n) on the `seq`/`ts` index and needs no
count; it is the model that stays correct under a live append-only tail.

## 4. The real insight: it's the *count*, not the pages

The recurring tester complaint isn't "I want page numbers" — it's that the
**minimap position indicator isn't monotonic**: over a lazily-loaded window
with no known total, loading an older page above the viewport re-scales any
normalized percent/thumb (numerator and denominator both grow). A position
bar can't be both normalized-to-the-loaded-window and jump-free.

A forum doesn't have this problem **because it knows the total.** With a
maintained `post_count`, "post #1240 of 5000" is an absolute, monotonic
position — independent of which pages are currently loaded.

So the one forum property worth borrowing is the **maintained total**, not
the page UI.

## 5. If we wanted to borrow it

To get a true, monotonic position (and, if desired, page jumps) we would
need:

1. **A denormalized per-session (and/or per-agent) event count** — a
   counter incremented on append, so the total is O(1) to read. The
   moving-target-while-live concern is acceptable for a count (it only
   grows at the tail; a row already loaded keeps its absolute ordinal).
2. **A dense per-session ordinal.** Single-agent sessions can use `seq`
   directly. Multi-agent (resumed) sessions need an ordinal assigned across
   the agent boundary — either a session-scoped sequence column populated
   on insert, or an accepted approximation (rank by `ts`).

With (1)+(2), the minimap thumb becomes `ordinal / total` (monotonic), and
"jump to ~position" / coarse page jumps become expressible without an
`OFFSET` scan (seek by ordinal, like a forum's "jump to post #X").

**Costs / open questions:**

- Write-path bookkeeping (a counter + an ordinal column) on a hot path.
- Backfill for existing events.
- Live append still shifts the *last* page boundary (same as forums) — fine
  for a position indicator, a wrinkle for fixed page numbers.
- Multi-agent ordinal assignment (the genuinely novel bit vs a forum's
  single post sequence).

## 6. Recommendation (for discussion)

- **Keep keyset infinite-scroll** as the substrate — it's the only model
  correct for a live-followed tail, and it's what the nav affordances
  (jump-to-latest, turn stepper, minimap scrub/tap) are built on.
- **Do not adopt forum-style fixed page numbers** — they fight live append
  and the per-agent `seq`, and `OFFSET` regresses long logs.
- **Consider the one high-value borrow: a maintained per-session event
  count + a dense ordinal**, purely to power a *true* position indicator
  (`event N of M`) and optional seek-by-position — resolving the recurring
  non-monotonic-position complaint at its root rather than papering over it.

If that lands, it would likely become an ADR (schema change + write-path
bookkeeping) and supersede the "position is inherently approximate" caveat
recorded in the plan §8.
