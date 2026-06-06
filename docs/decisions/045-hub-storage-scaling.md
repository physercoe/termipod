# 045. Hub storage scaling: deferred fold, store separation, selectable backend

> **Type:** decision
> **Status:** Accepted (2026-06-06) — director-locked after a tester
> running a multi-agent demo on a 2 GB VPS hit two scaling worries
> (~50 MB hub DB per task; "how many concurrent agents, is Redis
> needed?"). Distils the analysis in
> [`discussions/hub-scaling-storage-and-concurrency.md`](../discussions/hub-scaling-storage-and-concurrency.md)
> and [`discussions/hub-store-separation-and-fold-policy.md`](../discussions/hub-store-separation-and-fold-policy.md).
> **Amends [ADR-038](038-per-run-event-digest.md) §2** (synchronous
> digest fold → bounded-staleness deferred fold). D1 is shipped; D2/D3
> are decided, not yet built.
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

## Consequences

- **Digest freshness becomes visible by design** (D1): a long open turn
  can lag up to N events / τ ms. Acceptable (derived, reconciled at
  turn close and by read-repair) but a change from ADR-038 §2's
  per-event liveness.
- **Many DB files** (D2): three migration sets, and with per-team
  sharding a handle per `(team, store)` — needs an **LRU-capped
  connection registry** to bound file descriptors at hundreds of teams,
  with per-file migrations on first open.
- **No cross-store atomicity** — audited to be a non-issue for writes;
  the residual trigger and read-join have named fixes (handler-side
  `project_id`; denormalize `session_id` onto `agent_turns`).
- **Hard-delete cascade becomes app-level** (D2) — the three dormant FK
  cascades must be reproduced in code when the hard-delete primitive
  lands; per-team file deletion makes a team-wide purge trivial.
- **Backend abstraction cost** (D3) — a dialect/migration/FTS seam the
  codebase does not have today; deferred until a measured multi-hub /
  off-box requirement.

## Implementation surface (phased, hub-first, Go-testable)

1. **D1 — SHIPPED** (`digest_worker.go`, `payload_externalize.go`
   prerequisite): bounded-staleness fold + lossless blob-ref ingest;
   full `go test ./internal/server` green.
2. **D2 step A — class split** (single file per store): three pools;
   replace `agent_events_stamp_project` with handler-side resolution;
   add `session_id` to `agent_turns` and drop the OTLP join; app-level
   cascade hook; `agent_events_fts` travels with `agent_events`;
   parameterize the migration runner per file.
3. **D2 step B — per-team sharding**: a per-`(team,store)` connection
   registry (lazy open, LRU cap, first-open migration); route the fold
   worker per team.
4. **D3 — selectable backend**: gated on a measured multi-hub / off-box
   requirement; not before.

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
