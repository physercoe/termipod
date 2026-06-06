# Hub storage scaling — deferred fold, store separation, selectable backend

> **Type:** plan
> **Status:** In progress (2026-06-06) — **P0 shipped** (writer/reader
> pool split, bounded-staleness fold, blob-ref externalization); **P1
> steps 1–2 shipped** (store-handle seam aliased to the control pools, all
> moving-table writes + pure reads routed, and the fold/read-repair
> decoupled to read events / write the digest through separate handles). The
> phased implementation of [ADR-045](../decisions/045-hub-storage-scaling.md)
> (D1–D3), which locks the *what/why*; this plan owns the *how/when*.
> Backed by the analysis in
> [`discussions/hub-scaling-storage-and-concurrency.md`](../discussions/hub-scaling-storage-and-concurrency.md)
> and [`discussions/hub-store-separation-and-fold-policy.md`](../discussions/hub-store-separation-and-fold-policy.md).
> **Audience:** contributors
> **Last verified vs code:** v1.0.807-alpha

**TL;DR.** A tester on a 2 GB VPS hit ~50 MB/task and asked how many
concurrent agents the hub supports. Two axes — storage growth (inline
transcript bytes) and write concurrency (one SQLite writer + a
synchronous digest fold). The fix is staged in-tree before any external
service: **P0** stop the bleeding (pool split + defer the fold + blob
refs); **P1** split the schema into three stores so the fold gets its
own writer; **P2** shard the event/digest stores per team; **P3** make
the backend selectable (SQLite default, managed Postgres opt-in). Each
phase is independently shippable and Go-testable in `hub/`.

## Why now

Storage and concurrency are both *measured* problems on a small host
(`hub-scaling` §4: ~640 ev/s single-writer ceiling, fold ~half the
per-event cost; 25 MiB inline `attach` blobs dominate the 50 MB). The
cheap in-tree levers land before the managed-DB / multi-hub tier, which
relocates bytes / adds a hop without changing per-event cost.

## Shape

Three stores by data nature (ADR-045 D2): control (`hub.db`) / event
(`events.db`) / digest (`digest.db`), one writer each; `events.db` +
`digest.db` sharded per team, `hub.db` global. The digest's own writer
is what structurally fixes fold-vs-insert contention under saturation.
External Postgres is a per-store opt-in (D3), not the default.

## Phases

### P0 — Stop the bleeding — ✅ SHIPPED

- **Writer/reader pool split** — dedicated 1-connection `writeDB` for all
  writes, uncapped `db` reader. Killed the `SQLITE_BUSY` cliff
  (`592cd49`).
- **Bounded-staleness deferred fold (D1)** — `digest_worker.go`: fold on
  turn-close OR ≥N events OR ≥τ ms, off the ingest hot path; read-repair
  backstop. ~1.5–1.85× throughput; N=32/τ=750 ms/tick=100 ms,
  env-tunable (`764b287`).
- **Blob-ref externalization (storage lever 1, cut 1)** —
  `payload_externalize.go`: oversized `payload_json` string leaves →
  `blob:sha256/<hex>` on ingest, losslessly (`ba644e5`).

### P1 — D2 step A: class split (single file per store) — 🔶 IN PROGRESS (steps 1–2 done; 3–5 next)

Build order (each Go-testable in `hub/internal/server`):

1. **Pools + routing.** Open three stores; split the reader into
   `s.db` (control) / `s.eventsDB` / `s.digestDB`, each with reader +
   1-writer; route every DB access site to the right store. Give the
   test harness matching accessors (~36 test files use `c.s.db` directly
   against the moving tables). Split into two commits:
   - **1a — seam + write routing — ✅ DONE.** Added the four store
     handles (`eventsDB`/`eventsWriteDB`/`digestDB`/`digestWriteDB`),
     **aliased to the control pools** until the physical split (step 4)
     so the change is a behaviour-preserving refactor — writes still
     serialize through the one writer, the fold stays single-file.
     Routed all 13 `insertAgentEvent` sites → `s.eventsWriteDB` and the
     standalone digest read-repair writes (`ensureAgentDigest` /
     `saveAgentDigest` in the digest/turns/finalize handlers) →
     `s.digestWriteDB`. The entangled fold/backfill/session-delete tx
     sites carry inline `ADR-045 step 2/3` markers. `Close()` dedups the
     aliased handles. Full `go test ./internal/server` green.
   - **1b — read routing — ✅ DONE.** Routed the verified *pure*
     single-store reads: event reads (`handleListAgentEvents` ×2 +
     stream backfill, `lookupRecentLifecycleReason`, the mode/model
     compose, the `worker_report` lookup, attention-context, the two
     steward-strip reads, `sessionAgentIDs`) → `s.eventsDB`; digest reads
     (`listAgentTurns`, `sumTurnActiveMs`, `deriveDigestOutcome`'s turns
     read) → `s.digestDB`. **Cross-store joins stay on `s.db`** — verified
     and *not* routed: the FTS search (joins `sessions`, filters
     `team_id`), `listSessionTurns` (`agent_turns` ⨝ `agent_events`
     subquery), the insights reads (the `scopeFilter` makes them
     *conditionally* cross-store — pure for project/agent scope, an
     `agents` subquery for team/engine/host), the `last_event_at`
     correlated subquery, and OTLP's `turns ⨝ events`. They're decomposed
     in step 3. The shared digest-helper reads (`loadAgentDigest`,
     `loadFoldEvents*`, `digestIsStale`, `resolveToolName`) take a `db`
     param shared with the fold tx, so they route in step 2. Full
     `go test ./internal/server` green.
   - **Test-harness accessors** move to **step 4** (the physical split):
     while the handles alias `s.db`, the ~36 tests that read the moving
     tables via `c.s.db` keep resolving; they only need `c.s.eventsDB` /
     `c.s.digestDB` once the files are distinct.
2. **Restructure the fold + read-repair — ✅ DONE.** The
   correctness-critical change. `foldEventIncremental` now threads two
   handles — an **event reader** (`eventsR`) for the rare in-fold event
   reads (`loadFoldEventsBefore`, `resolveToolName`) and a **digest tx**
   (`digestTx`) for all digest/turn writes — instead of one shared tx.
   `foldDirtyAgent` reads the watermark from `s.digestDB` + the events
   from `s.eventsDB`, then folds into an `s.digestWriteDB` tx.
   `ensureAgentDigest` / `backfillAgentDigest` became `*Server` methods
   that reach each store directly (digest read from `s.digestDB`, the
   staleness probe + full event log from `s.eventsDB`, `team_id` from
   `s.db` control, writes in an `s.digestWriteDB` tx). `foldEventIntoDigest`
   (now test-only — production ingest defers to the worker) + the OTLP
   `loadFoldEvents` follow suit. **No `ATTACH`**; the event reads see
   already-committed rows and the digest is idempotent from the watermark,
   so the two stores need no shared transaction. Behaviour-preserving
   while the handles alias one file; correct across files post-split. Full
   `go test ./internal/server` green.
3. **Sever the remaining cross-store edges — 🔶 IN PROGRESS.** The
   2026-06-06 implementation audit found the documented coupling surface
   **understated in two ways** (the earlier "zero cross-store write tx +
   one OTLP read join" framing was wrong). Each edge becomes an app-level
   two-query / two-statement form (resolve the id set from `hub.db` /
   `events.db` first, then query the other store by `… IN (?,?,…)`), no
   `ATTACH`, behaviour-preserving while the handles still alias one file.
   - **Cross-store *read* joins (more than the OTLP join):**
     - ✅ `handlers_agents.go` `last_event_at` — dropped the correlated
       subquery; `lastEventAtForAgents` post-fetches `MAX(ts)` from the
       event store over the (chunked) page of agent ids. (`handleListAgents`
       + `handleGetAgent`.)
     - ✅ `handlers_agent_turns.go` `listSessionTurns` — resolves the
       session's agents via `sessionAgentIDs` (event store), then reads
       `agent_turns` from the digest store by `agent_id IN (…)`.
     - ✅ `insights_scope.go` / `handlers_insights.go` — `materializeInsightsScope`
       resolves the agent-id set from control (`s.db`) once and rewrites
       `EventsClause` to a concrete `agent_id IN (…)` list (empty → `0`), so
       the agent_events reads stay pure-event. `buildInsightsResponse` + the
       five `readInsights*` helpers became `*Server` methods routing event
       reads → `s.eventsDB`, control reads (agents / sessions /
       attention_items / projects / deliverables / criteria) → `s.db`.
       `SessionsClause` is sessions⨝agents (both control) and stays as-is.
       The IN-list is a single statement (SQLite's bound-var cap is ~32k,
       far beyond pre-P2 scale; P2 sharding drops the filter entirely). All
       24 insights tests pass — incl. team/engine/host/team_stewards.
     - ⬜ `handlers_search_sessions.go` FTS — joins `agent_events_fts` +
       `agent_events` (event) with `sessions` (control, `team_id`). Needs a
       cross-store filter-pushdown: FTS MATCH in the event store, then the
       team/`status!='deleted'` filter from control. (Watch the LIMIT — the
       team filter must apply before truncation.)
     - ⬜ `otlp_export.go` — the `turns ⨝ events` join; fix by denormalizing
       `session_id` onto `agent_turns` (needs a migration + populate).
     - (`backfillAgentDigest` `agents`-read + `deriveDigestOutcome`
       `tasks`+`agent_turns` were already split into separate queries in
       step 2 / 1b — not joins.)
   - ✅ **The cross-store *write* tx.** `handleDeleteSession` — the
     `agent_events` session_id clear is now `clearSessionFromEvents`
     (`s.eventsWriteDB`), run *after* the control tx commits + on the
     already-deleted idempotent path, so a crash between stores self-heals
     on retry.
   - ⬜ **Structural edges.** Replace the `agent_events_stamp_project`
     trigger with handler-side `project_id` resolution (drop trigger =
     migration); the 3 dormant `→ agents ON DELETE CASCADE` edges become an
     app-level cascade hook *when hard-delete lands* (no-op today — there is
     no agent hard-delete); `agent_events_fts` moves with `agent_events`
     (step-4 migration).
4. **Existing-DB migration: a one-shot `hub-server db split` command.**
   Copies the moving tables into the new files and drops them from
   `hub.db`; each file gets its own `schema_migrations`. The server
   **refuses to serve a not-yet-split legacy DB** so a pre-split boot
   can't mis-route writes. Parameterize the migration runner per file.
5. **Backup/restore + tooling.** `backup.go` (today snapshots only
   `hub.db` via `VACUUM INTO`) and `db_cmd.go`/`doctor.go` must cover
   all three files.

Independent of P2 and of the real-rate measurement — worth building now
(delivers control-isolation + the fold's own writer).

### P2 — D2 step B: per-team sharding — ⬜ Later (gated on measured rate)

`events.db` + `digest.db` become per-team files
(`dataRoot/teams/<team>/…`); `hub.db` stays global. Add a per-(team,
store) **connection registry** — lazy open, LRU-capped to bound file
descriptors, first-open migration. Route the fold worker per team (the
dirty-set already keys by agent+team). N teams → N writers + O(1)
per-team retention/delete/backup. Per-**session** is the next
granularity only if a single hot team is measured to saturate one
writer.

### P3 — D3: selectable Postgres backend — ⬜ Later (gated on multi-hub / off-box need)

`storage_backend = sqlite | postgres` per store; SQLite default,
managed/remote Postgres opt-in. A port, not a flag: a parallel migration
set, FTS5 → `tsvector`, and the loss of the offline single binary when
chosen. Does not shrink bytes. Not before a measured requirement.

### Later (post-MVP)

- **Blob-ref backfill sweep** — cut 1 externalizes new events only; a
  one-time sweep reclaims existing inline payloads (`hub-scaling`
  lever 1 follow-up).
- **Mobile feed-card render** of an externalized ref (chip + size vs raw
  `blob:sha256/…` string) — device-tested Flutter change.
- **Retention / hard-delete cascade** — per-team files make a team purge
  trivial; the cross-store cascade for a *partial* (sub-team) delete is
  still unspecified (`hub-scaling` §8-Q7).

## Resolved (was open)

- **Fold/backfill cross-store tx** → read-then-write-own-tx, no `ATTACH`
  (digest idempotent from the watermark). The event↔digest write tx (two
  sites: `foldDirtyAgent`, `backfillAgentDigest`). **Correction
  (2026-06-06):** control↔event is **not** zero either — `handleDeleteSession`
  is one cross-store write tx (see step 3); and the cross-store *read*
  surface is wider than the single OTLP join (step 3 enumerates it). The
  fix pattern (app-level multi-query / split tx, no `ATTACH`) is unchanged;
  the worklist is larger.
- **Existing-DB migration** → one-shot `hub-server db split` + a
  serve-guard against an un-split legacy DB (director, 2026-06-06).
- **Cross-store reads** → app-level two-query, no `ATTACH`, with
  `session_id` denormalized onto turns for the one hot join.
- **Store boundary + sharding key** → three stores; events+digest per
  team, control global (ADR-045 D2).

## Open questions

- **Real event rate.** Does a representative hundreds-of-agents demo
  reach the ~600–650 ev/s ceiling? `scripts/measure-event-rate.sh` runs
  it against any hub DB; parked for a future demo. Sets P2's urgency.
- **D1 constants.** N/τ defaults (32 / 750 ms) — confirm against a
  bursty trace and device-test that Insights feels live.
- **Partial hard-delete cascade** — the sub-team delete contract (above).

## References

- [ADR-045](../decisions/045-hub-storage-scaling.md) — the locked
  decisions (D1–D3) this plan implements.
- [ADR-038](../decisions/038-per-run-event-digest.md) — the digest/turns
  D1 amends.
- [`discussions/hub-scaling-storage-and-concurrency.md`](../discussions/hub-scaling-storage-and-concurrency.md),
  [`discussions/hub-store-separation-and-fold-policy.md`](../discussions/hub-store-separation-and-fold-policy.md)
  — the analysis behind the phases.
