# 045. Hub storage scaling: deferred fold, store separation, selectable backend

> **Type:** decision
> **Status:** Accepted (2026-06-06) — director-locked after a tester
> running a multi-agent demo on a 2 GB VPS hit two scaling worries
> (~50 MB hub DB per task; "how many concurrent agents, is Redis
> needed?"). Distils the analysis in
> [`discussions/hub-scaling-storage-and-concurrency.md`](../discussions/hub-scaling-storage-and-concurrency.md)
> and [`discussions/hub-store-separation-and-fold-policy.md`](../discussions/hub-store-separation-and-fold-policy.md).
> **Amends [ADR-038](038-per-run-event-digest.md) §2** (synchronous
> digest fold → bounded-staleness deferred fold). D1 + D2 + D4 are
> shipped (D2 via the P1 class split and the P2 per-team shard — see the
> [plan](../plans/hub-storage-scaling.md); D4 the storage-maintenance
> loop); D3 is decided, not yet built.
> **Audience:** contributors
> **Last verified vs code:** v1.0.807-alpha

**TL;DR.** Three decisions for scaling the hub's storage and write
concurrency on a single small VPS, without adding an external service at
MVP. **D1 — the digest fold runs on a bounded-staleness trigger**
(turn-close OR N events OR τ ms), off the ingest hot path, amending
ADR-038's synchronous fold. **D2 — the schema splits into three stores
by data nature** — control (`hub.db`) / event (`events.db`) / digest
(`digest.db`) — with `events.db` + `digest.db` **sharded per team** and
`hub.db` global. **D3 — the storage backend is selectable per store**
(`sqlite | postgres`): local/offline SQLite is the zero-dependency
default, an external **managed/remote** Postgres is the opt-in for HA,
an off-box RAM-starved host, or high write-concurrency. The shipped
prerequisites — a writer/reader pool split and automatic blob-ref
externalization of oversized payloads — are recorded in the discussion
docs; this ADR locks the three forward decisions.

## Context

The hub stores the agent transcript inline in `agent_events.payload_json`
and maintains a per-agent digest (ADR-038). Two independent axes bit a
tester (`hub-scaling` §1):

- **Storage.** A single `attach` tool call's multi-MB base64, stored
  inline with no retention, dominated a ~50 MB/task DB.
- **Concurrency.** SQLite has one global writer **per file**; each
  inbound event fanned out into several serialized writes, and ADR-038's
  **synchronous** digest fold was ~half the per-event cost
  (`hub-scaling` §4.3). Measured single-writer ceiling: ~640 ev/s with
  the fold, ~1330 insert-only.

Two prerequisites already shipped (recorded in the discussions, not
re-litigated here): the **writer/reader pool split** (dedicated
1-connection writer; killed the `SQLITE_BUSY` cliff) and **blob-ref
externalization** (oversized `payload_json` string leaves rewritten to
`blob:sha256/<hex>` refs in the existing content-addressed store, on
ingest, losslessly). What remained were three system-wide decisions that
change load-bearing invariants — hence this ADR.

A managed DB or Redis **relocates bytes / adds a network hop without
changing the per-event cost**; it is the multi-hub escalation
(`hub-resilience.md` option B), not the first move. Measure, pull the
in-tree levers, then escalate only on a measured requirement.

## Decision

### D1 — the digest fold is deferred and bounded-staleness (amends ADR-038 §2)

ADR-038 §2 maintained the digest **synchronously, in the same
transaction as the `agent_events` insert**. We move it off the hot path
and change *when* it runs. The ingest handler only records cheap
in-memory trigger accounting; a background worker folds an agent when
**any** of:

- **(a)** a turn closes (`turn.result`) — the authoritative boundary;
- **(b)** ≥ **N** events have accumulated since its last fold;
- **(c)** ≥ **τ** ms have elapsed with pending events.

The fold stays watermark-based, so the count/turn flags are *triggers
only* — no event is skipped. Read-repair (`ensureAgentDigest` →
`backfillAgentDigest`) remains the correctness backstop, so a
deferred/lagged/post-restart digest is **never observable as wrong
data** — it is recomputed on read. Defaults **N = 32, τ = 750 ms,
tick = 100 ms**, env-overridable (`HUB_DIGEST_FOLD_*`).

This makes the digest **eventually consistent** (bounded by the
trigger), trading ADR-038 §2's per-event liveness for ~1.5–1.85×
throughput. Measured caveat (`hub-store-separation` §3.5): under flat-out
saturation the fold defers to read-repair (lag tracks the single-writer
ceiling, not the trigger); below the ceiling — the bursty regime real
agents live in — it keeps up. The structural fix for the saturated case
is D2's separate digest writer.

### D2 — three stores by data nature, sharded per team

The schema splits into three SQLite stores, **one writer each**:

- **`hub.db` (control)** — teams, agents, projects, tasks, sessions,
  runs + `run_*`, attention, documents, the `events` envelope
  (A2A/channel) log, hosts, tokens, … Mutable OLTP, FK-rich, low volume.
- **`events.db` (event log)** — `agent_events` + `agent_events_fts`.
  Append-only firehose, inserts only.
- **`digest.db` (derived)** — `agent_event_digests` + `agent_turns`.
  Written only by the fold worker + read-repair; rebuildable.

**Three, not two:** the fold **reads `events.db` and writes `digest.db`**
while inserts **write `events.db`** → different writers → the saturated
fold-lag of D1/§3.5 is **structurally fixed**, not merely bounded.
(Folding the digest into `events.db` keeps the contention; putting it in
`hub.db` mixes durable control with rebuildable data and routes fold
writes into the system of record — both rejected.)

**Sharding:** `events.db` + `digest.db` are **sharded per team**
(the route key is already `/v1/teams/{team}/…`); `hub.db` is **global**.
N teams → N event/digest writers, plus O(1) per-team retention / delete /
backup (a team's transcript is self-contained files — the cleanest
answer to the missing hard-delete primitive). Control stays one shared
DB because it is low-volume and has inherently cross-team concerns (the
`teams` registry, `auth_tokens`, `hosts`). Per-team sharding scales
across **teams**, not within one hot team; per-**session** is the next
granularity if a single team is measured to saturate one writer.

The coupling is thin (verified vs 53 migrations, `hub-store-separation`
§4.4): **zero cross-store write transactions** (all 13
`insertAgentEvent` sites are lone `writeDB` statements); three
`→ agents(id) ON DELETE CASCADE` edges, all dormant (nothing
hard-deletes an agent today); one cross-store trigger
(`agent_events_stamp_project`); one cross-store read join (OTLP
`turns ⨝ events`). Soft-ref scope columns (`session_id`, `project_id`,
`team_id`) are already denormalized with no FK.

### D3 — the storage backend is selectable per store

`storage_backend = sqlite | postgres`, **per store**:

- **SQLite is the default** — zero dependency, offline/airgapped, the
  single-binary deployment (ADR-002).
- **External (managed/remote) Postgres is the opt-in** — for HA, an
  off-box RAM-starved host, or high write-concurrency (MVCC dissolves
  the single-writer ceiling; declarative partitioning gives O(1)
  retention). **Managed/remote ≠ Postgres on the box**: a remote
  provider *removes* load from a small VPS; self-hosting on the box adds
  it (only the remote form is recommended for a constrained host).

This is a **port, not a flag** (`hub-store-separation` §5.3): a parallel
migration set, an FTS5→`tsvector` rewrite, and the loss of the offline
single-binary property when chosen. It does **not** shrink bytes
(blob-refs still needed). Redis/NATS enter only at 2+ hub processes,
independent of the DB choice. The D2 split is what makes D3 affordable —
the backend choice is per-store, so a deployment can move one store
(e.g. control → Postgres) without touching the others.

### D4 — automated WAL-checkpoint + incremental reclamation (amends D2)

The D2 split leaves two storage-hygiene gaps that bit an operator (#79):
**WAL files grow unbounded** and **freed pages are never returned to the
OS**. They are *different* mechanisms and are addressed separately — a
common confusion that conflates checkpointing with `VACUUM`.

- **WAL growth is a reader-pinning problem.** SQLite's auto-checkpoint
  (default 1000 pages) can only reset the WAL up to the *oldest live
  reader's snapshot*. The hub holds long-lived **SSE readers**
  (Activity / transcript streams), so a continuous reader + continuous
  firehose write keeps the checkpoint from ever reaching the WAL head —
  it grows without bound. The fix is a periodic
  `PRAGMA wal_checkpoint(TRUNCATE)` run by the hub, not a vacuum.

- **Reclamation uses `auto_vacuum=INCREMENTAL`, not full `VACUUM`.** A
  full `VACUUM` rewrites the whole file: it needs ~2× the DB size in
  free disk (an ENOSPC risk on a 2 GB VPS), takes a global write lock
  for an O(DB-size) duration (stop-the-world, *per shard*), and fights
  the very SSE readers above. Incremental auto-vacuum instead returns
  free pages to the OS in **bounded chunks**, each a short transaction
  that interleaves with readers — the same pattern always-on embedded
  SQLite uses (Chromium, Firefox, Android). Its two costs are immaterial
  here: the per-commit pointer-map overhead is negligible against the
  measured fold cost, and its no-defragment limitation barely bites an
  **append-mostly** firehose (near-sequential inserts ⇒ low
  fragmentation). Blob-ref externalization (the D2 prerequisite) already
  removes the large transient payloads that would otherwise strand free
  pages, so the residual reclamation need is modest — incremental is
  cheap insurance, not a hot path.

**The policy:**

1. **New event/digest shards are created `auto_vacuum=INCREMENTAL`** (set
   on the schema-creating writer connection at first open — it must
   precede the first table). `hub.db` (control) keeps freelist reuse: low
   delete volume, and converting an existing file needs a full VACUUM.
2. **A background maintenance loop** (same ctx lifetime as the other
   sweeps) runs every `HUB_STORE_MAINTENANCE_INTERVAL` (default 5 m). Per
   currently-open shard writer (`hub.db` + each open team's events/digest
   writer) it: (a) `wal_checkpoint(TRUNCATE)`; (b) a bounded
   `incremental_vacuum` **with hysteresis** — only when the freelist is
   ≥25 % of the file *and* above an absolute floor, reclaiming down to a
   watermark (not to zero) and capped per pass, so a still-active
   firehose can't thrash returning pages it immediately re-allocates.
   `incremental_vacuum` is a no-op where `auto_vacuum≠INCREMENTAL`, so it
   is safe on `hub.db` and pre-D4 shards. Disable with
   `HUB_STORE_MAINTENANCE_DISABLE`. Evicted teams are checkpointed on
   pool close (SQLite checkpoints when the last connection closes), so a
   cold team needs no loop coverage.
3. **Full `VACUUM` stays operator-only and offline** — the existing
   `hub-server db vacuum` (now also sets `auto_vacuum=INCREMENTAL` before
   the rebuild, so it doubles as the one-time pre-D4-shard converter).
   It is never run automatically.

## Consequences

- **Digest freshness becomes visible by design** (D1): a long open turn
  can lag up to N events / τ ms. Acceptable (derived, reconciled at
  turn close and by read-repair) but a change from ADR-038 §2's
  per-event liveness.
- **Many DB files** (D2): three migration sets, and with per-team
  sharding a handle per `(team, store)` — needs an **LRU-capped
  connection registry** to bound file descriptors at hundreds of teams,
  with per-file migrations on first open.
- **No cross-store atomicity.** Control↔event writes never share a tx.
  Event↔digest: the fold + read-repair are restructured from one tx to
  read-then-write-own-tx (safe — digest idempotent from the watermark);
  **no `ATTACH`**. Residual trigger + read-join have named fixes
  (handler-side `project_id`; denormalize `session_id` onto turns).
- **Hard-delete cascade becomes app-level** (D2) — the three dormant FK
  cascades must be reproduced in code when the hard-delete primitive
  lands; per-team file deletion makes a team-wide purge trivial.
- **Backend abstraction cost** (D3) — a dialect/migration/FTS seam the
  codebase does not have today; deferred until a measured multi-hub /
  off-box requirement.

## Implementation

The phased, Go-testable rollout (P0 shipped → P1 class split → P2
per-team sharding → P3 selectable backend), the build order, and the
resolved mechanics (the fold/backfill tx restructure, the one-shot
`db split`, no `ATTACH`, backup/restore) live in the plan,
[`plans/hub-storage-scaling.md`](../plans/hub-storage-scaling.md) — not
here. This ADR records the *decisions*; the plan owns the *how/when* and
tracks status.

## Alternatives considered

- **Per-event deferred fold ("A")** — deferred but did not shrink the
  fold; under saturation the worker starved (fold debt grew). Superseded
  by D1's bounded-staleness trigger (`hub-store-separation` §3.2).
- **Two stores (events incl. digest)** — keeps fold-vs-insert contention
  on one writer; D2's third store is the fix.
- **Digest in `hub.db`** — same-store Insights attention join, but mixes
  durable control with rebuildable data; rejected.
- **One fancier embedded engine (DuckDB)** — OLAP-shaped; regresses the
  OLTP control CRUD and the point/range event reads.
- **External DB / Redis first** — relocates bytes / adds a hop without
  changing per-event cost; the multi-hub tier, not the MVP move.

## Open questions

- **Real event rate.** Does a representative hundreds-of-agents demo
  reach the ~600–650 ev/s ceiling? Parked for a future demo;
  `scripts/measure-event-rate.sh` runs the measurement against any
  hub DB. The answer sets the urgency of D2 step B.
- **D1 constants.** N/τ defaults (32 / 750 ms) are tunable; confirm
  against a bursty trace and device-test that Insights feels live.
- **Blob-ref backfill.** Cut 1 externalizes new events only; a one-time
  sweep to reclaim existing inline payloads is a follow-up.
- **Hard-delete primitive.** D2's per-team files make purge trivial, but
  the cross-store cascade contract for a *partial* (sub-team) delete is
  still unspecified (`hub-scaling` §8-Q7).

## References

- [`plans/hub-storage-scaling.md`](../plans/hub-storage-scaling.md) —
  the phased implementation of these decisions (status, build order).
- [`discussions/hub-scaling-storage-and-concurrency.md`](../discussions/hub-scaling-storage-and-concurrency.md)
  — the two axes, measured numbers, the in-tree levers.
- [`discussions/hub-store-separation-and-fold-policy.md`](../discussions/hub-store-separation-and-fold-policy.md)
  — the fold cost model (§3), the store inventory + boundary (§4), the
  selectable backend (§5).
- [ADR-038](038-per-run-event-digest.md) — the digest/turns this amends.
- [`hub-resilience.md`](../discussions/hub-resilience.md) — the
  durability / multi-hub tier D3 defers to.
- [`transcript-source-of-truth.md`](../discussions/transcript-source-of-truth.md)
  — why `agent_events` is authoritative (bounds D2/D3 durability).
