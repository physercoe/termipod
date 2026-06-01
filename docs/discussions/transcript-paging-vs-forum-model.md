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
> **Last verified vs code:** v1.0.782
>
> **Scope broadened (2026-06-01):** the director reframed the goal — this is
> not really about a navigation UI. The purpose is **a correct data model +
> tools to analyze the log so we can learn lessons and improve the system.**
> The key observation: the log has **two regimes — live SSE and idle/static
> — and the static one is far more mature with standard tooling.** §§7–11
> develop that framing; §§1–6 (the original paging-vs-forum analysis) are the
> substrate it builds on.

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

---

# Part II — the log as an analyzable dataset

## 7. The real distinction: two *lifecycles* of one log, not two UIs

The paging question (§§1–6) is a symptom. The director's reframing exposes
the cause: **an event log is consumed in two fundamentally incompatible
ways, and we have been trying to serve both from one model.**

- **As a stream** — subscribe and follow the tail, react in real time. It is
  *unbounded* and you never hold the whole thing. The right tools are
  cursors, backpressure, keyset pagination, tail-follow. This is our **live
  SSE** regime (`agent_events` + the SSE handler + `AgentFeed`).
- **As a table/dataset** — the *whole, bounded, immutable* thing, queried and
  aggregated and joined. The right tools are counts, indexes, columnar
  scans, SQL, dataframes, trace viewers. This is the **idle/static** regime
  the director points at.

This is the well-known **stream/table duality** (Kleppmann, "Turning the
database inside out"; Kreps' Kappa architecture): the same log is both a
stream of changes and a table you can materialize. The error is asking
*table questions* — "how many", "what fraction of tool calls failed", "jump
to position X", "median turns to completion across the fleet" — of the
*streaming* model. Those are OLAP questions; the live tail is an OLTP tail.

The director's instinct that "the static log is more mature, more standard
tools" is exactly this duality: **once a log is bounded and immutable it
becomes a dataset**, and the entire off-the-shelf ecosystem (jq, DuckDB,
SQL, pandas, Parquet, trace backends) applies. The live tail has, by
comparison, bespoke tooling.

## 8. The boundary that changes everything: *sealing*

The stream→table transition happens at a concrete, observable event: the
agent reaches a **terminal/idle state** — terminated, archived, crashed, or
its session stopped/paused (the `stopSessionInternal` /
`applyAgentTerminationEffects` path, `hub/internal/server/stop_session.go:55`).
At that instant, for that agent:

- **The event range is frozen.** No further `seq` will ever be appended.
  Therefore `max(seq)` *is* the exact `event_count` — the "moving target /
  can't count cheaply" objection from §3 simply **dissolves on the cold
  side**.
- **`seq` is already a dense ordinal.** `agent_events` declares
  `UNIQUE(agent_id, seq)` with `seq` monotonic per agent (migration
  `0011_agent_events.up.sql`). For a single-agent session it already is the
  dense per-thread ordinal a forum has. For a multi-agent (resumed) session,
  seal each agent segment and concatenate by `(agent order, seq)` — now
  deterministic, because every segment is frozen.

So **everything the forum gets "for free" (count, dense ordinal, static
pages) we also get for free — but only on the cold side, at seal time.** The
mistake in §§1–6 was trying to win them on the hot side, where they are
genuinely expensive.

This is not exotic; it is how every serious log system already works —
**segment sealing**:

| System | Hot (mutable tail) | Sealed (immutable + indexed) |
|---|---|---|
| Kafka | active segment, append-only | rolled segment + `.index` (base offset, count) |
| LSM-tree | memtable | SSTable + footer (min/max key, count, bloom) |
| WAL / event store | open log | checkpoint / snapshot |
| **Transcript (proposed)** | `agent_events` live range | sealed segment + **digest** |

Hot segment: optimized for append and tail-follow. Sealed segment:
immutable, indexed, summarized. We should adopt the same split.

## 9. Proposed data model

1. **Keep `agent_events` as the hot append-only WAL, unchanged** — keyset
   cursor stays the substrate for the live tail (§3's conclusion holds for
   the hot path). Do **not** add forum pages or a live counter to it.
2. **Seal on terminal state.** Hook the termination/stop path; in one O(n)
   pass over the now-frozen range, write a **digest** (a materialized read
   model — event-sourcing's standard answer to "serve a new query shape
   without bloating the write path"). The digest row carries, per sealed
   agent (and rolled up per session):
   - `event_count`, `min_seq` / `max_seq`, `first_ts` / `last_ts`;
   - **turn index** — the seq/offset of each turn boundary (so "jump to turn
     k / position p" is a seek, not a scan);
   - **rollups for analysis** — tool-call count + failure count by tool, an
     **error taxonomy** (class → count), total + per-model cost, per-turn
     wall-clock and token timing, retries / dead-ends.
3. **Dense session ordinal** for multi-agent sessions: either store a
   session-scoped `ordinal` column populated at seal, or derive on read from
   the (now fixed) segment boundaries. Store it — cheaper at read time and
   it is what makes the mobile minimap `ordinal / count` truly **monotonic**
   for a sealed log. (For the *still-live* agent you simply lack the final
   count — show a "live" affordance, not a false percent.)

That single change resolves the recurring non-monotonic-position complaint
at its root *and* is the foundation everything in §10 builds on.

## 10. Analysis tooling — two tiers, one digest

**Tier 1 — in-system (online; serves the steward + the director's mobile).**
- `transcript.summary` MCP tool → returns the digest: counts, error
  taxonomy, cost, turn timing. A steward triaging a worker run reads *this*
  instead of paging 10k events — far cheaper on the MCP tool I/O budget than
  streaming the raw log.
- `transcript.query` → structured filter over a sealed log (by kind, tool,
  error class, turn range), backed by the existing FTS
  (`0031_agent_events_fts`) + the digest's turn index.
- Monotonic position + jump-by-ordinal in the mobile transcript — the
  original tester complaint, now backed by a real count.

**Tier 2 — export to standard analytical formats (offline; fleet-wide
"learn lessons / improve the system").**
- **Parquet / JSONL export of sealed logs → DuckDB / pandas.** This is the
  modern "analyze a structured log" path; fleet questions become plain SQL —
  "median turns to completion by engine", "top failing tools", "cost per
  task kind", "error-class frequency over the last 100 runs" — with zero
  bespoke code. The static, immutable nature of a sealed log is exactly what
  makes a columnar projection safe and cacheable.
- **OTLP trace export.** An agent run *is* a trace: **session/run = trace,
  turn = span, tool_call = child span, error = span event + error status.**
  Emit OTLP and any trace backend (Jaeger, Tempo, Honeycomb) gives
  causal/latency/failure analysis for free — and OpenTelemetry's emerging
  **GenAI semantic conventions** (`gen_ai.*` span attributes for LLM calls)
  are a real standard to align to. This is the strongest well-tested-
  practice borrow for "*why* did this run fail, where did the time and cost
  go" — the questions a lessons loop actually asks.

Both tiers are projections of the same sealed events + digest: Tier 1
computes the digest at seal; Tier 2 re-shapes the sealed range into
Parquet/OTLP on demand.

## 11. The lessons loop (the actual goal)

Sealed-log analysis feeds back two ways:

- **Per-run postmortem** — error taxonomy, retried/looping tools, dead-ends,
  cost spikes — surfaced to the director/steward as a run summary
  (Activity / Insights surface).
- **Fleet aggregates** — patterns across runs that drive *system*
  improvement. Because **behaviour is data** in this system (agent kinds,
  prompts, plans, policies are YAML templates), a lesson like "tool X fails
  40% of the time under prompt Y" is directly actionable: tune the template,
  not the code.

This closes the loop the director described: a correct data model (sealed
segment + digest) → standard analysis tools (DuckDB OLAP + OTLP traces) →
lessons → template/policy changes → better runs.

## 12. Recommendation

- **Keep keyset-streaming for the hot tail.** Do not retrofit table
  semantics (pages, live counts) onto the live model — §§1–6 stand.
- **Make sealing explicit.** At terminal state, seal the agent's range and
  compute a one-pass **digest** (counts, turn index, error/tool/cost
  rollups) + a **dense session ordinal**. This is the keystone: it fixes the
  monotonic-position complaint *and* unlocks analysis.
- **Build analysis on the sealed side, in two tiers** — Tier 1 MCP
  `summary` / `query` tools (steward + mobile); Tier 2 Parquet (DuckDB OLAP)
  + OTLP-trace (GenAI conventions) exports so mature, off-the-shelf tooling
  does the heavy lifting.
- **Likely two ADRs:** (A) *sealed-segment + digest data model* (schema +
  write-path seal hook + ordinal); (B) *analytical export surface* (MCP
  summary/query tools + Parquet/OTLP exporters). (A) is a prerequisite for
  (B) and subsumes §5's "maintained total" proposal.

**Open questions for the next pass:** where the digest lives (a new
`agent_event_digests` table vs. extending the `runs` digest the trackio work
already added); whether to seal incrementally (roll a segment every N events
for very long-running agents) rather than only at terminal state; and how
much of the OTLP mapping to standardize now vs. defer until a concrete trace
backend is in play.
