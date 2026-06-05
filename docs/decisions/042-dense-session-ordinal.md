# 042. A dense per-session event ordinal (`session_ordinal`)

> **Type:** decision
> **Status:** Accepted (2026-06-04) — directed by the director, who chose
> "Option C, the long-term one … we need a clean and solid foundation" from
> [`discussions/insight-resume-seq-identity.md`](../discussions/insight-resume-seq-identity.md)
> §4. Justified to build now because there is **no real user data** to migrate
> (a dev-reset is acceptable). Realizes the dense-session-ordinal keystone
> deferred in [`discussions/transcript-paging-vs-forum-model.md`](../discussions/transcript-paging-vs-forum-model.md)
> §§5/9 and demoted in [`discussions/insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md)
> §10. Builds on [ADR-038](038-per-run-event-digest.md) (the digest/turn index)
> and the session model of [ADR-040](040-transcript-surfaces-decoupled-by-mode.md).
> Implemented by [`plans/dense-session-ordinal.md`](../plans/dense-session-ordinal.md).
> **Audience:** contributors
> **Last verified vs code:** v1.0.801-alpha

**TL;DR.** `agent_events.seq` is monotonic **per agent** (`UNIQUE(agent_id,
seq)`). A **session spans multiple agents** after a resume (resume mints a new
`agent_id`, keeps the `session_id`), so the two agents' seq ranges **overlap**.
Every session-scoped surface — the Insight transcript, the digest, the turn
index, the error samples — therefore lacks a coordinate that is unique across
the whole session, and keys its anchors on the bare `seq`, which collides. We
add **`session_ordinal`**: a dense, gap-free integer assigned to every
`agent_events` row **at insert** as `MAX(session_ordinal)+1 WHERE session_id =
?`, `UNIQUE(session_id, session_ordinal)`. It is the **canonical identity for
the session/Insight surface**; per-agent `seq` stays the live-feed cursor.
Assignment is centralized in one `insertAgentEvent` helper (the 10 existing
inline inserts collapse into it). This cures the resume/navigator wrong-row bug
at its root and makes a true `event N of M` position exact.

## Context

`agent_events` declares `UNIQUE(agent_id, seq)` and every insert assigns
`seq = COALESCE(MAX(seq),0)+1 … WHERE agent_id = ?`
(`hub/internal/server/handlers_agent_events.go:117-122`, and **9 other inline
insert sites** — none centralized). So `seq` is unique only *within an agent*;
the first event of every agent is `seq = 1`.

A **resumed session spans multiple agents**: resume mints a new `agent_id` while
preserving the `session_id` (`carryModeModelStateAcrossResume`,
`hub/internal/server/handlers_sessions.go:884`), because the session is the
primitive that survives respawn. The session-scoped event list already has to
order by `ts, agent_id, seq` and comments *"seq is per-agent and a session can
span multiple agents (resume)"* (`handlers_agent_events.go:320-325`). The
session digest **merges per-agent digests** across `GROUP BY agent_id`
(`handlers_agent_digest.go:109-111`), and the turns endpoint aggregates across
`t.agent_id IN (SELECT DISTINCT agent_id FROM agent_events WHERE session_id=?)`
(`handlers_agent_turns.go:164-170`). So both **turn anchors** (`start_seq`) and
**error samples** (`sample_seqs`) are pulled from multiple agents into one
session view — with overlapping seq values.

The mobile Insight transcript is session-scoped but identifies every anchor by
the **bare integer `seq`**: `runTurnSeqs` / `runErrorSeqs` / `runAnchorTs` are
built seq-keyed (`lib/widgets/session_analysis_view.dart:145-183`);
`_seqIsLoaded` / `_jumpToContext` match by seq
(`lib/widgets/insight_transcript.dart:827-828`). Once a session contains two
agents, an anchor at `seq=N` lands on whichever agent's `seq=N` is encountered
first — the wrong turn/error. This is the directly-reported bug: *resume an
agent, run several turns, the Insight Navigator jumps to the wrong position.*

The two companion discussions had already identified the missing piece — a
maintained count + a **dense session ordinal** — but deferred it
(`transcript-paging-vs-forum-model.md` §§5/9 "store a session-scoped ordinal";
`insight-navigation-fixed-pages.md` §10 demoted it to a "nice-to-have N of M"
once structure-first navigation landed). Two things changed: (1) structure-first
navigation still lands on a row resolved by bare `seq`, so the resume collision
survived the workbench; and (2) there is now **no production data**, so the
schema change can be made cleanly without a careful backfill. The deferral is
lifted.

### Alternatives considered

- **Compound `(agent_id, seq)` anchor identity (option A in the discussion).**
  Mobile-mostly, smaller. It fixes *identity* but not *position* — it gives no
  dense session coordinate, so the `N of M` pill and any ordinal/page math stay
  impossible, and the errors side still needs a hub field. It is a patch, not a
  foundation. Rejected in favor of the coordinate that subsumes it.
- **Seal/digest-time derivation of the ordinal.** Assign only when the digest is
  computed (idle/terminal). Lighter write path, but a *live* (unsealed) window
  has no ordinal, so the Insight view on a running session cannot land by it
  without an O(n) scan. Weaker foundation. Rejected.
- **Make `seq` itself per-session.** Re-scope the insert to `WHERE session_id=?`.
  Breaks the per-agent live-tail cursor (`before=<minSeq>` is agent-scoped) and
  the `UNIQUE(agent_id, seq)` contract, and leaves session-less system events
  without a coordinate. Rejected.

## Decision

1. **Add `session_ordinal INTEGER` to `agent_events`** — dense, gap-free,
   monotonic **per session**, assigned at insert as
   `COALESCE(MAX(session_ordinal),0)+1 … WHERE session_id = ?`. Constraint
   `UNIQUE(session_id, session_ordinal)`; index `(session_id, session_ordinal)`.
   `NULL` for events with no `session_id` (they never appear in a session view).

2. **`session_ordinal` is the canonical identity for the session/Insight
   surface.** Per-agent `seq` remains the live-feed (`LiveFeed`) cursor and the
   `(agent_id, seq)` replay key — unchanged. The two coordinates coexist: `seq`
   for the agent-scoped live tail, `session_ordinal` for the session-scoped
   analysis surface.

3. **Centralize event insertion.** The 10 inline `MAX(seq)+1` inserts collapse
   into one `insertAgentEvent(tx, …)` helper that assigns **both** `seq`
   (per-agent) and `session_ordinal` (per-session) in a single atomic statement,
   with the two `UNIQUE` constraints as the race backstop. This is a prerequisite
   for a single, correct assignment site (and retires real duplication).

4. **Express every navigation anchor in ordinal space.** The digest records turn
   `start_ordinal` and error `sample_ordinals` alongside the seqs; `agent_turns`
   gains `start_ordinal`; the session digest/turns endpoints and the event-list
   read path emit `session_ordinal`; the random-access loader keysets on
   `(session_ordinal)` for the session-scoped branches.

5. **Phased delivery, hub-first** (Go-testable locally), mobile last
   (CI-verified). See [`plans/dense-session-ordinal.md`](../plans/dense-session-ordinal.md).
   No production backfill is required; the plan includes an optional one-pass
   backfill by `(session_id, ts, agent_id, seq)` for completeness.

## Consequences

**Easier:**
- The Insight Navigator (Turns/Errors) lands on the correct row across a resume
  boundary — the reported bug is cured at its root, for every session-scoped
  surface at once.
- A true, monotonic `event N of M` position becomes exact (`session_ordinal /
  event_count`), superseding the "position is inherently approximate" caveat in
  the agent-transcript plan §8 and the §5 "maintained-total" proposal in the
  forum doc.
- Event insertion is centralized — one place owns the seq/ordinal/ts/session
  bookkeeping, so future event kinds can't reintroduce the duplication or skip a
  coordinate.

**Harder / now constrained:**
- The write path computes a second indexed `MAX()` per insert (per-session).
  Same cost class as the existing per-agent `MAX(seq)`; acceptable on the hot
  path, backstopped by `UNIQUE(session_id, session_ordinal)`.
- A schema migration + a digest schema bump (turn/error anchors gain ordinal
  fields). Old digests without ordinals degrade gracefully (the mobile path
  falls back to ts-keyed landing, as the error side already does).
- Two coordinates now exist on each event. The split is deliberate and
  documented (agent-scoped `seq` vs session-scoped `session_ordinal`); the
  glossary gains an entry so they are not conflated.
- **A new contention surface that `seq` never had.** `seq`'s `MAX(seq)+1` is
  keyed on `agent_id`, and exactly one writer ever inserts for a given agent, so
  it is structurally contention-free. `session_ordinal`'s `MAX(session_ordinal)+1`
  is keyed on `session_id`, which **multiple agents can share** (the resume
  shape). SQLite's single-writer serialization keeps the assignment *correct* as
  long as inserts are serial — and the normal resume flow is serial, because the
  prior agent is terminated/paused before the resumed one writes. The only way to
  get two *live* agents inserting into one session concurrently is an A2A or
  steward overlay that keeps both running; there, two in-flight inserts can
  resolve the same `MAX+1` and the second hits `UNIQUE(session_id,
  session_ordinal)` — which **fails the insert loudly rather than corrupting the
  coordinate**. That is the intended failure mode (a retryable conflict, not a
  silent collision); if such overlays become common, the insert should grow a
  retry-on-conflict. Recorded here because it is behavior the per-agent `seq`
  could not exhibit.

**Out of scope:** the live `LiveFeed` surface (agent-scoped, single-agent
windows) is untouched; segment-roll cadence and the Parquet/OTLP export tiers
(`transcript-paging-vs-forum-model.md` §§10/12) remain separate, later work.

## References

- Code: `hub/internal/server/handlers_agent_events.go` (inserts + read path),
  `hub/internal/server/digest_fold.go` (turn/error anchors),
  `hub/internal/server/handlers_agent_turns.go`,
  `hub/migrations/0011`/`0015`/`0026`/`0049`/`0050`,
  `lib/widgets/session_analysis_view.dart`, `lib/widgets/insight_transcript.dart`.
- Discussions: [`insight-resume-seq-identity.md`](../discussions/insight-resume-seq-identity.md)
  (the bug + options), [`transcript-paging-vs-forum-model.md`](../discussions/transcript-paging-vs-forum-model.md)
  (the dense-ordinal substrate), [`insight-navigation-fixed-pages.md`](../discussions/insight-navigation-fixed-pages.md)
  (where it was demoted).
- Related ADRs: [038](038-per-run-event-digest.md), [040](040-transcript-surfaces-decoupled-by-mode.md),
  [041](041-insight-workbench-layout.md).
- Plan: [`plans/dense-session-ordinal.md`](../plans/dense-session-ordinal.md).
