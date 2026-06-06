# Hub storage scaling — deferred fold, store separation, selectable backend

> **Type:** plan
> **Status:** In progress (2026-06-06) — **P0 shipped** (writer/reader
> pool split, bounded-staleness fold, blob-ref externalization). The
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

### P1 — D2 step A: class split (single file per store) — ⬜ NEXT

Build order (each Go-testable in `hub/internal/server`):

1. **Pools + routing.** Open three stores; split the reader into
   `s.db` (control) / `s.eventsDB` / `s.digestDB`, each with reader +
   1-writer; route every DB access site (~20 server files) to the right
   store. Give the test harness matching accessors (~36 test files use
   `c.s.db` directly against the moving tables).
2. **Restructure the fold + read-repair** (the correctness-critical
   change). `foldDirtyAgent` and `backfillAgentDigest`
   (`digest_store.go:353`) today read `agent_events` and write
   digest/turns in **one tx** — split into *read from the events.db
   reader → fold in memory → write digest in its own digest.db tx*.
   Safe: the digest is idempotent from the watermark. **No `ATTACH`.**
3. **Sever the remaining cross-store edges.** Replace the
   `agent_events_stamp_project` trigger with handler-side `project_id`
   resolution; **denormalize `session_id` onto `agent_turns`** and drop
   the OTLP `turns ⨝ events` join; add an app-level cascade hook for the
   3 dormant `→ agents ON DELETE CASCADE` edges; `agent_events_fts`
   moves with `agent_events`.
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
  (digest idempotent from the watermark). Corrects the earlier "zero
  cross-store tx" framing (control↔event is zero; event↔digest was two).
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
