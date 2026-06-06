---
name: Hub store separation and fold policy
description: A deeper follow-up to hub-scaling-storage-and-concurrency.md. Two questions the tester's multi-agent demo forced open. (1) When should the digest fold run? Folding every event (sync or deferred-worker "A") makes the digest the dominant write and, under saturation, the deferred worker can never catch up — the fold debt grows. The fix is a bounded-staleness trigger: fold on turn.result OR every N pending events OR every τ ms, whichever comes first, with N tuned empirically. (2) Do we need a second database besides SQLite? The hub mixes two workloads with opposite shapes — an append-only, high-volume, time-ordered AGENT EVENT LOG (with its derived digest/turns materialised view) and a low-volume, mutable, FK-rich CONTROL PLANE (projects/teams/tasks/agents/policies/tokens). They currently share one file and one writer, so the event firehose contends with project CRUD. First-principles answer: separate the two stores by WORKLOAD before reaching for a new engine. The cheapest correct separation is two SQLite files (one writer each) — SQLite's single-writer lock is per-database, so this buys two independent writers with zero new operational surface. An external engine (Postgres for the control plane, a columnar/log store for events, Redis/NATS for SSE fan-out) is the multi-hub tier, justified by horizontal scale, not by this single-VPS demo. No ADR locked — names the decisions an ADR would need to settle.
---

# Hub store separation and fold policy

> **Type:** discussion
> **Status:** Open (2026-06-06) — **post-MVP.** Deeper follow-up to
> [`hub-scaling-storage-and-concurrency.md`](hub-scaling-storage-and-concurrency.md)
> (the levers) raised by the director after lever 7 (deferred fold)
> measured a worker that can't keep up under saturation. Companion to
> [`hub-resilience.md`](hub-resilience.md) (durability/HA) and
> [`transcript-source-of-truth.md`](transcript-source-of-truth.md)
> (why the hub stores transcripts at all). **Decisions now locked in
> [ADR-045](../decisions/045-hub-storage-scaling.md)** (D1 shipped, D2/D3
> decided not yet built); this doc remains the working design record.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.807-alpha

**TL;DR.** Two questions, one root. **(1) When should the digest fold
run?** Today it runs **per event** — on the request path (shipped) or
via a 300 ms deferred worker (lever 7 "A", built). Per-event folding
makes the **digest rewrite the dominant write** (the whole aggregate
blob + open-turn row, rewritten on every event), and under saturation
the deferred worker can never win enough writer time to drain its
backlog, so **fold debt grows without bound** (measured 99 % lag at 800
agents). The fix is not "defer the fold," it is **fold less often**: a
**bounded-staleness trigger** — fold an agent when a **turn closes**,
OR **N events** have accumulated, OR **τ ms** have elapsed, whichever
comes first; `N` chosen empirically (~20–40 to start). This cuts
digest-write volume by ~`N×` while keeping a hard freshness bound.
**(2) Do we need a second database?** The hub runs **two workloads with
opposite shapes** through **one file and one writer**: an
**append-only, high-volume, time-ordered event log** (`agent_events` +
its derived digest/turns view) and a **low-volume, mutable, FK-rich
control plane** (projects/teams/tasks/agents/policies/tokens). The
firehose and the CRUD **contend on the same write lock**. The
first-principles move is to **separate by workload**, and the **cheapest
correct separation is two SQLite files** — SQLite's single-writer lock
is **per database**, so two files = **two independent writers** with
**zero new operational surface**. That lock caps write *throughput*, not
read concurrency, and it is escapable **within SQLite** by **sharding
the event store per team** (N files = N writers, keeping FTS5/SQL)
*before* any engine swap. Beyond that, the **direction (director,
2026-06-06) is a *selectable* storage backend** — `sqlite | postgres`
*per store* — with **local/offline SQLite the zero-dependency default**
and an **external (managed/remote) Postgres an opt-in** for HA, an
off-box RAM-starved host, or high write-concurrency (Postgres' MVCC
dissolves the single-writer ceiling; **managed/remote ≠ Postgres on the
box** — it *removes* load from the VPS rather than adding it). Postgres
does **not** shrink the bytes (blob-refs still needed); Redis/NATS enter
only at multi-hub.

---

## 1. Why these are the same question

The director's two prompts —"the worker shouldn't fold every turn, it
should fold on a condition" and "do we even want one database for data
that belongs to different kinds" — are the **same observation at two
scales**:

- **Within the event pipeline:** the digest is a *derived view* over
  the event log, and we are recomputing it at the **wrong granularity**
  (per event) against the **wrong writer** (the one the firehose
  saturates).
- **Across the schema:** the *event log itself* is a different **kind**
  of data from the *control plane*, and we are storing it in the **same
  writer** so the two contend.

Both are **"match the write cadence and the store to the shape of the
data."** §3 answers the fold-cadence question; §4 answers the
store-shape question; they compose (§7).

---

## 2. Two workloads, opposite shapes

Everything in the hub's SQLite file is one of two kinds. They differ on
every axis that matters for a database.

| Axis | **Event log** (`agent_events` + derived `agent_event_digests`, `agent_turns`) | **Control plane** (projects, teams, tasks, agents, runs, attention, policies, tokens) |
|---|---|---|
| Mutation | **Append-only** — `agent_events` rows are never updated in production (only `handlers_insights_test.go` does, for a test). Digest/turns are derived and rewritten. | **Mutable CRUD** — status flips, assignments, budgets, edits. |
| Volume | **High** — the whole scaling worry; hundreds–thousands of rows per task. | **Low** — human/steward-paced; a task has tens of control rows. |
| Order | **Time/seq ordered**; reads are range scans (tail, since-seq, keyset `(ts,seq)`, by-turn). | **Random access by id / FK**; reads are point lookups + small joins. |
| Integrity | Weak cross-refs (an event carries `agent_id`/`session_id`; no cascade). | **Strong** — ~57 `project_id`-scoped columns, FK-rich; cascade-delete is a real (currently missing) requirement (`hub-scaling` §8-Q7). |
| Durability need | Authoritative transcript (`transcript-source-of-truth.md`) — **must not silently lose committed events** today. | System of record — must not lose a committed project mutation. |
| Retention | **Wants truncation/compaction** (the 50 MB/task problem; blob-refs). | Effectively permanent (small). |
| Sharding key | Natural per-agent / per-session. | Per-team / per-project. |

This is the textbook **append-only log vs OLTP system-of-record**
split. They share a file today only for historical reasons (one
`OpenDB`, one migration set — `db.go`), not because their access
patterns are compatible. They are **not** compatible: the high-volume
append firehose holds the **single global write lock** that the
low-volume relational CRUD also needs.

> Note one subtlety the tester's "50 MB" surfaced: the digest/turns
> tables are **derived** — a *materialised view* over the log. They are
> not a third kind of data; they belong **with** the log (they are a
> pure function of it and are discarded/rebuilt by read-repair). This
> matters in §4: when we split, the view travels **with** the events,
> not with the control plane.

---

## 3. The fold trigger — a cost model

### 3.1 What the fold actually costs

The fold turns events into `agent_event_digests` (one JSON aggregate
row per agent) + `agent_turns` (one row per turn). Per the folder
(`digest_fold.go` `step()`):

- **Per event, unconditionally:** `EventCount++`, `WatermarkSeq`,
  `DurationMs`, and — for an open turn — running token/tool counters.
  This is **cheap CPU** but it forces a **digest-row rewrite** if we
  persist after it.
- **Only at `turn.result`:** `CostUSD`, `by_model`, `TurnCount`, final
  per-turn tokens, turn `status`. The authoritative numbers land at
  **turn close**, seconds-to-minutes apart.

The expensive part is **persistence, not computation**. `saveAgentDigest`
rewrites the **whole** aggregate blob + the open-turn row every time it
runs. Call that cost `c·D` where `D` is the digest size. The
**`step()` CPU is `s` per event and is irreducible** — every event must
be visited once. So for a run of `E_total` events:

| Policy | Digest-write volume | Freshness of an open turn |
|---|---|---|
| **Per-event** (sync today, or "A" worker per event) | `c·D · E_total` | exact, every event |
| **Per-turn** ("B") | `c·D · (#turns)` | nothing until the turn closes |
| **Hybrid: turn-close OR every N events OR every τ ms** | `c·D · max(#turns, E_total/N)` | **bounded** ≤ `min(N events, τ ms)` |

`step()` CPU (`s·E_total`) is the same in all three — only the **write
amplification** and **freshness** change. And because the hub takes
**`usage` events per assistant message** plus a `tool_call`/`tool_result`
each, a single "goal mode" turn spans **many** events, so `E_total/#turns`
is large — i.e. **per-turn folding cuts digest writes by a big factor.**

### 3.2 Why lever-7 "A" still can't keep up (the key correction)

It is tempting to read A's failure as "the worker is too slow / needs
batching." It already batches: `foldDirtyAgent` drains **all** of an
agent's pending events in **one** transaction per 300 ms tick. The real
cause is different and important:

> **A deferred the fold but did not reduce it.** The worker still does
> one digest-rewrite per agent per tick, and **insert and fold share the
> one writer**. Under saturation the inserts are greedy and the worker
> starves — so fold debt grows (measured 71 % lag @100 → 99 % @800).
> Deferring work that competes for the same lane doesn't help; you have
> to make the work **smaller**.

That reframes the lever. The win isn't "background thread," it's
**fewer folds**. The hybrid trigger is what makes folds fewer.

### 3.3 The trigger: bounded-staleness coalescing

Mark an agent foldable, and fold it, when **any** of:

- **(a) a `turn.result` arrives** — the natural boundary; the only point
  where authoritative cost/by_model/status exist anyway.
- **(b) ≥ N events have accumulated** since its last fold — the
  staleness cap for a long open turn (a goal-mode turn that streams
  hundreds of events before closing).
- **(c) ≥ τ ms have elapsed** with pending events — covers a *slow* turn
  (few events, long gaps) so a quiet agent still updates.

This is the standard **debounce + max-batch + flush-on-boundary**
pattern (Kafka producer `linger.ms`/`batch.size`; group-commit; React's
`flushSync` on a boundary). (a) gives exactness where it's free; (b)
bounds amplification and worst-case staleness for a runaway turn; (c)
bounds latency for a trickle.

It also **resolves the director's mental model vs the code.** The
director's model — "the digest is the last *finished* turn; a running
turn shouldn't pollute aggregates" — is **(a)**. ADR-038 §2 deliberately
keeps the open turn *running* for live UI; **(b)+(c)** preserve a
*bounded* version of that liveness without paying per-event. So the
hybrid is a **superset** of both positions: authoritative at
boundaries, bounded-fresh within a turn, and the freshness bound is a
**tuning knob**, not a fixed policy.

### 3.4 Choosing N (and τ) — empirically, but with a target

Don't pick N by feel; pick the N where the **fold stops being a
bottleneck** while staleness stays imperceptible:

- **Lower bound (amplification):** N must be large enough that
  `c·D·(E_total/N)` is **small relative to insert write volume**. Since
  one fold rewrites a multi-KB blob and one insert writes ~one row,
  `N≈20–40` already makes fold writes a **minor fraction** of total
  writes — so the worker drains comfortably *whenever the writer has
  spare capacity*. (§3.5 measures the limit of that "whenever": at full
  saturation there is no spare capacity and N stops mattering.)
- **Upper bound (freshness):** a live transcript reads as "live" at
  ≲ ~1 s. At an active streaming rate of ~5–50 events/s, `N≈20–40` and
  `τ≈300–1000 ms` keep open-turn staleness sub-second.
- **Empirical test:** re-run the load sweep (`load_test.go`) below the
  writer ceiling (the bursty regime, `HUB_LOADTEST_THINK_MS>0`) and pick
  the N that keeps **open-turn staleness sub-second without over-folding**.
  N≈20–40 holds; baked as an env-tunable constant
  (`HUB_DIGEST_FOLD_MAX_EVENTS`) with §3.5's measurement cited. (At/above
  the ceiling, N is moot — see §3.5.)

> **Failure modes to keep in mind.** N too small → back toward A's
> debt. N too large → a long goal-mode turn shows stale tool/token
> counters for many events (annoying, not incorrect — read-repair and
> `turn.result` still reconcile). Neither corrupts data: the digest is
> **derived**, and `ensureAgentDigest → backfillAgentDigest` remains the
> correctness backstop regardless of trigger.

### 3.5 Measured (2026-06-06) — the trigger matches the model; **lag tracks the writer ceiling, not the trigger**

Step 1 is implemented (`digest_worker.go`: `markDigestDirty` →
per-agent `digestPending{count, turnClosed, firstDirty}`;
`collectFoldable` folds on the §3.3 trigger; watermark-based fold, so
triggers never skip an event; N=32 / τ=750 ms / tick=100 ms,
env-tunable) and swept on the 2-vCPU harness (`load_test.go`, with a new
`HUB_LOADTEST_THINK_MS` to model bursty agents).

**Throughput rose to ~1.5–1.85× the synchronous-fold baseline** (1131
ev/s at 200 agents vs ~617; nearing the ~1330 no-fold ceiling) — the
ingest win the lever promised. **But the §3.4 hope that fold-lag would
"stay bounded at saturation" was wrong, and the measurement says why.**
At a *fixed* 200 agents, varying only think-time:

| Offered load (200 agents) | Throughput | Fold-lag |
|---|---|---|
| flat-out (think=0) | 1106 ev/s | 88.8 % |
| think=100 ms | 749 ev/s | 83.9 % |
| **think=250 ms** | **601 ev/s** | **18.5 %** |
| **think=500 ms** | 343 ev/s | **3.1 %** |

Lag does **not** depend on N/τ in this regime — it tracks **offered load
vs the single-writer ceiling (~600–650 ev/s)**. Above the ceiling
(synthetic flat-out), inserts consume ~100 % of the one writer and the
fold starves **no matter how few passes the trigger schedules** — it
defers to read-repair (correct, just lagged). Below the ceiling — the
**bursty regime real agents live in** (think → tool → wait) — the writer
has spare capacity and **the fold keeps up; lag collapses to single
digits.**

**So the corrected framing:** the bounded-staleness trigger's job is
**(1)** to match the data model (fold at turn boundaries + bounded
staleness, not per-event) and **(2)** to keep each fold cheap so the
realistic regime stays fresh — **not** to win a flat-out saturation
test. Beating saturation lag is **structural**, and belongs to §4/§5
(give the fold its own writer by splitting/sharding the store, or accept
that a genuinely writer-saturated single host folds lazily via
read-repair) — not to the fold *policy*. This is the same
"insert and fold share one writer" wall that sank "A" (§3.2),
re-confirmed from the other side: shrinking the fold raises the ceiling
but does not change that a *saturated* writer has nothing left for it.

---

## 4. Store separation — two files before two engines

### 4.1 The cheap, correct first move: split the SQLite file

SQLite's single-writer lock is **per database file**. Today both pools
(`OpenDB` reader, `OpenWriterDB` writer) open the **same** `hub.db`
(`db.go`), so the firehose and the control plane serialise through one
writer. Splitting into **two files** gives **two independent writers**
with **no new dependency, no new process, no new ops**:

- **`events.db`** — `agent_events` + the derived `agent_event_digests`,
  `agent_turns` (+ optionally the FTS index and the `events` envelope
  log). Its own writer (and its own deferred-fold worker per §3). The
  firehose lives here alone; the only contention is insert-vs-fold,
  which §3 manages.
- **`hub.db`** — projects, teams, tasks, agents, runs, attention,
  policies, tokens — the **system of record**. Its own writer, full FK
  integrity, where the (still-missing, `hub-scaling` §8-Q7) cascade
  hard-delete belongs.

Cross-references stay **by id** (`agent_id`, `session_id`,
`project_id`) — exactly the weak coupling that already exists; the
Flutter app already reads everything as id-keyed JSON maps with no FK
assumptions. The control plane never had a real FK *into* `agent_events`
anyway.

What we **gain**: the event firehose can no longer block a steward's
`tasks.update` or a director's project edit, and vice versa; each store
can be tuned, checkpointed, vacuumed, and backed up on its **own**
cadence; the event store can later get its own retention/compaction
(blob-refs, pruning) without touching control-plane durability.

What we **pay**: two migration sets; two backup units; **no cross-store
transaction** — an event insert and a control-plane mutation can no
longer be one atomic tx. That last cost is **near-zero here** because
those two things are already **logically independent** (an event
arriving does not transactionally mutate a project row; the digest is
derived, not a control-plane invariant). The one place to audit is any
handler that today writes an `agent_events` row **and** a control row in
one `BeginTx`; those become two txns (and must be ordered so a crash
between them is self-healing — events first, control second, or made
idempotent).

> **Durability nuance — don't over-claim.** It is tempting to say
> "`events.db` can run relaxed durability because events are
> reconstructible from the host JSONL." **Not today.**
> `transcript-source-of-truth.md` establishes the hub's `agent_events`
> as the **authoritative** transcript (FTS5 + indexes, cache-first
> mobile UX), *not* a disposable cache. So `events.db` keeps real
> durability for now. Relaxed durability only becomes available **if**
> the hybrid in that doc's §5 is adopted (host JSONL promoted to
> source of truth) — call it out as a *future* unlock, not a
> justification for the split. The split stands on **writer isolation +
> workload shape** alone.

### 4.2 Could one engine just do both well? (DuckDB / Postgres in-process)

Worth naming so it's a considered choice, not an omission. An embedded
**columnar** engine (DuckDB) would crush event *analytics*, but the
hub's event reads are **point/range** (tail, keyset, by-turn), not
big aggregations — OLTP-shaped, which row-store indexed SQLite already
serves well; DuckDB would *regress* the control-plane CRUD. A single
**Postgres** would give one writer with real MVCC (no single-writer
lock at all) and solve concurrency outright — but it adds a **process,
a dependency, and ops** to a **2 GB single-VPS** target, which is
exactly what the tester can't afford. So at MVP: **not one fancier
engine — two right-sized files**. Postgres re-enters in §5, but as a
**selectable opt-in** (and only in its *managed/remote* form for a small
host) — not the default, and not on the box.

### 4.3 Is SQLite even *good* for an append-only event log?

A fair challenge: the event log is the workload SQLite is *least*
specialised for. Be honest about where the single writer bites and what
genuinely excels — then show why the answer is still "stay on SQLite,
but stop sharing one writer."

**The single writer limits write *throughput*, not read concurrency.**
WAL gives unlimited concurrent readers (lever 3 already put them on
their own uncapped pool), so reads never block and never block writers.
*Writes* serialise through one lock **per file** — the limit is
events/sec, not "how many agents can connect" (agents queue in Go, they
don't error). Measured on the 2-vCPU harness: **~640 ev/s** with the
synchronous fold, **~1330 ev/s** insert-only (`hub-scaling` §4.3). Real
agents are **bursty** (think → tool → wait), ~≤1–5 ev/s each, so a few
hundred agents land at a few hundred to ~1000 ev/s aggregate — **right
at that edge at peak.** So the ceiling is real and worth designing for,
but it is a *throughput* ceiling, and a row-store B-tree on
`(agent_id, seq)` (it exists, migration 0011/0015) is **adequate**, not
*specialised*, for append-only — it is not write-optimised the way a
log-structured engine is.

**The in-tree escape from the single writer is sharding, not a new
engine.** Because the write lock is *per file*, splitting the event
store across **multiple SQLite files keyed by team/agent** gives **N
writers** — the Litestream / Turso / "SQLite-per-tenant" pattern. This
is the natural extension of §4.1's split (workload first, then shard the
event store) and it **scales write concurrency without leaving SQLite or
losing FTS5.** Cost: app-level shard routing and cross-shard reads that
fan out (the reads are already per-agent/per-session, so routing is
natural).

**What genuinely excels — by tier, with the catch named.** The reason
not to jump straight to a "better" engine is that two things are
**load-bearing on SQLite specifically**: **FTS5 full-text search**
(`agent_events_fts` / `events_fts`, migration 0031, queried in
`handlers_search_sessions.go`, `handlers_search.go`, `mcp.go`) and the
**SQL keyset / by-turn reads** the Insight view depends on. Any engine
swap below the SQL line means **reimplementing search + the read
layer** — a real project, justified only past a ceiling the cheap moves
can't reach.

| Tier | Option | Why it fits the append-only log | What it costs here |
|---|---|---|---|
| **In-tree, escape the writer** | **SQLite sharded per team/agent** | single-writer is per *file* → N files = N writers; keeps SQL + FTS5 | shard routing; cross-shard read fan-out |
| **In-tree, write-optimised** | **Pebble / Badger** (pure-Go LSM) | LSM is built for high-volume append + seq-keyed range reads; keeps the pure-Go single binary | **loses SQL + FTS5** → reimplement read/search layer |
| **One engine, both workloads** | **Postgres** | MVCC, no single-writer lock; serves event log *and* control plane; has FTS | a server process + ops on a 2 GB VPS |
| **Scale-out analytics** | **ClickHouse / TimescaleDB** | columnar, heavy compression, millions of rows/sec ingest | server; OLAP-shaped — wasted on our point/range reads |
| **Ingest + fan-out** | **Kafka / Redpanda / NATS JetStream** | the canonical multi-producer append-only log | a **bus, not a queryable store** — still needs an indexed store behind it for "tail since seq" |

**Verdict.** SQLite is "good enough until a measured ceiling," and the
ceiling is escapable *within SQLite* (per-file sharding) long before a
different engine earns the cost of losing FTS5/SQL. So the sequence is:
**fold fix (§3) → split into control/event/digest stores (§4.4) → shard
events.db + digest.db per team (§4.5) → only then an external engine
(§5).** A write-optimised LSM or a columnar store is a *real*
answer, but for *measured* multi-thousand-ev/s sustained load on one
host, not for this demo.

### 4.4 Read/write/data inventory (verified against the schema, 2026-06-06)

Before splitting, the actual coupling was mapped against the 53
migrations and the read/write sites. **Decision (director,
2026-06-06): three stores** — control / event / digest — by data
nature. The coupling surface turned out thin, because the schema was
already built for it (denormalized scope columns, digest-merge-by-team).

**The three data classes (37 tables):**

| Class | Tables | Nature |
|---|---|---|
| **Control** (`hub.db`) | teams, agents, projects, tasks, sessions, runs + run_* (metrics/config/images/histograms/alerts/system), milestones, plans + plan_steps, deliverables + components, acceptance_criteria, documents + annotations, reviews, artifacts, attention_items, channels + members, **events + events_fts** (A2A/channel messages), hosts, host_commands, auth_tokens, agent_spawns, schedules, agent_schedules, audit_events, blobs | Mutable OLTP, FK-rich, low volume |
| **Event** (`events.db`) | **agent_events** + **agent_events_fts** | Append-only, high write volume (the firehose), inserts only |
| **Digest** (`digest.db`) | **agent_event_digests**, **agent_turns** | Materialized read-model; written only by the fold worker + read-repair; rebuildable |

**Read/write patterns:**

- `agent_events` — writes via **13 call sites, all through
  `insertAgentEvent(s.writeDB, …)`, single-statement, none inside a
  multi-table tx**. Reads: transcript paging (keyset / by-turn), FTS
  search (same-store join to `agent_events_fts`), and the fold reading
  raw events.
- `digest`/`turns` — writes only `saveAgentDigest` + turn upserts
  (`digest_store.go`), now off the insert path (§3 step 1). Reads:
  Insights (merged **by denormalized `team_id`, no join**), turn
  navigation, OTLP export.
- control — ordinary CRUD; Insights' one attention count joins
  `attention_items ⨝ sessions` — **both control, same-store**.

**Cross-store coupling surface — thin:**

- **Control↔event write transactions: zero.** Every event insert is a
  lone statement on `writeDB`; nothing wraps an `agent_events` insert and
  a control row in one tx.
- **Event↔digest write transactions: two, and they need restructuring.**
  The fold worker (`foldDirtyAgent`) and read-repair
  (`backfillAgentDigest`, `digest_store.go:353`) currently **read
  `agent_events` and write `digest`/`turns` in one `BeginTx`**. Once
  events and digest live in different files that tx can't span them.
  **Fix (the core refactor of step A): read events from the events.db
  reader → fold in memory → write digest in its own digest.db tx.** Safe
  because the digest is idempotent from the watermark (a failed digest
  write leaves the watermark; the next trigger/read-repair retries) — no
  shared transaction is required. **No `ATTACH`** (it would re-couple the
  writers and block per-team sharding); cross-store reads are app-level
  two-query, with `session_id` denormalized onto `agent_turns` to remove
  the one hot join.
- **FK cascades severed: 3** — `agent_events`,
  `agent_event_digests`, `agent_turns` each `→ agents(id) ON DELETE
  CASCADE`. **All currently dormant** (nothing hard-deletes an agent;
  retention is deferred on the missing hard-delete primitive, §8-Q7 of
  `hub-scaling`). They become an app-level cascade *when* hard-delete
  lands.
- **Cross-store trigger: 1** — `agent_events_stamp_project`
  (migration 0036) fires after insert and reads `sessions` to backfill
  `project_id`. → replace with app-level resolution at insert (the
  handler already looks up the session).
- **Cross-store query JOIN: 1** — OTLP's
  `sessionsWithClosedTurnsSince` joins `agent_turns ⨝ agent_events` on
  `agent_id`. → eliminate by **denormalizing `session_id` onto
  `agent_turns`** (it already denormalizes `agent_id` + `team_id`).
- **Denormalized soft-refs (no FK), already split-ready:**
  `agent_events.session_id` / `.project_id`, `digest`/`turns.team_id`.

**Why three, not two — the payoff that closes §3.5.** The fold **reads
`events.db` and writes `digest.db`** while event inserts **write
`events.db`**. Different writers → the fold's writes no longer contend
with event inserts → the saturation fold-lag of §3.5 is **structurally
fixed**, not merely bounded. Folding the digest into `events.db` would
keep that contention; the third store is what gives the fold its own
writer. (Alternative C — digest in `hub.db` — makes Insights' attention
count same-store but mixes durable control with rebuildable derived data
and routes fold writes into the system-of-record; rejected for A.)

**Work items when we build:** (1) open 3 pools per file, route by store;
(2) replace the `agent_events_stamp_project` trigger with handler-side
`project_id` resolution; (3) add `session_id` to `agent_turns`, drop the
OTLP cross-store join; (4) app-level cascade hook for the 3 severed FK
edges (wired into the future hard-delete); (5) `agent_events_fts` moves
with `agent_events`; parameterize the migration runner per DB file.
`runs`/`run_*` stay in control for v1 (project-scoped, FK-rich, lower
volume than the transcript firehose); the `events` envelope (A2A/channel
log, FK-coupled to channels) stays in control too — it is not the
firehose.

### 4.5 Sharding granularity — events + digest per team, control global

Splitting by *class* (§4.4) gives one writer per workload; splitting by
*tenant* gives N writers per workload. **Decision (director,
2026-06-06): shard `events.db` and `digest.db` per team; keep
`hub.db` (control) global.**

- **`events.db` per team** — the firehose is the write bottleneck and is
  naturally team-isolated; the ingest route is already
  `/v1/teams/{team}/agents/{agent}/events`, so the shard key is in hand
  at insert time (and the file *is* the team scope, so a per-team file
  needs no `team_id` column). N teams → N independent event writers, and
  a team's transcript becomes self-contained files (O(1) per-team
  retention / delete / backup — the cleanest answer yet to the
  hard-delete gap).
- **`digest.db` per team** — pairs with events: team T's fold reads
  `events.db[T]` and writes `digest.db[T]`, fully per-team isolated;
  `digest`/`turns` already carry `team_id`, and Insights already merges
  by team.
- **`hub.db` global (not sharded)** — control is low-volume (sharding
  buys no concurrency) and has **inherently cross-team / global**
  concerns: the `teams` registry itself, `auth_tokens`, the `hosts`
  registry. One shared control DB keeps those simple; the single writer
  is not the bottleneck there.

**Honest limit — what per-team sharding does and doesn't buy.** It scales
writers across *teams*. A single hot team with hundreds of agents still
has one `events.db` writer for all of them, so per-team sharding does
**not** raise that team's ceiling — for that case the win is the
class-split + the bounded fold (§3), and the *next* granularity (per
**session**, since `agent_events` already carries `session_id`) is the
lever, at the cost of many more open files. So: shard per team now (the
right multi-tenant shape, and the O(1)-retention win); reach for
per-session only if a *single* team is measured to saturate one writer.

**New cost this adds: a per-(team,store) connection registry** — open
DB handles lazily on first use, cache them (LRU-capped to bound file
descriptors at hundreds of teams), and run that file's migrations on
first open. The fold worker's dirty-set already keys by agent+team, so
routing a fold to `events.db[T]` + `digest.db[T]` is a lookup.

---

## 5. The external tier — a *selectable* Postgres backend

The split in §4 is in-process. An external service earns its keep when a
requirement crosses a process/host boundary — HA, horizontal scale, or a
RAM-starved host that wants the DB *off the box*. **Direction
(director, 2026-06-06): the hub should offer the storage backend as an
option** — **local/offline SQLite is the default**, **external Postgres
is an opt-in** — rather than picking one for everyone. The two serve
different deployments, not different quality levels.

### 5.1 Managed/remote Postgres ≠ Postgres-on-the-box

An earlier draft dismissed Postgres as "adds a process + ops to a 2 GB
VPS." That is true for **self-hosting Postgres on the same box** and
false for a **managed/remote provider** — the distinction matters for
exactly the RAM-constrained tester who raised this:

| | **Self-hosted on the VPS** | **Managed/remote provider** (Neon, Supabase, RDS, Cloud SQL) |
|---|---|---|
| Effect on the box | **Adds** a memory-hungry process to the constrained host — worse | **Removes** the DB from the host; bytes + write load move off-box — better |
| Cost it trades in | Ops + RAM on the same machine | A **network hop per query** (vs SQLite's in-process µs reads) + a **hard dependency** on a reachable service |
| Offline / airgapped | Possible | **No** — breaks the zero-dependency single binary (ADR-002) |

So "use Postgres" is a *legitimate* answer to "my VPS is too small" —
**but only the managed/remote form**, and only by accepting the network
hop and the external dependency. Self-hosting it on the 2 GB box is
still the wrong move.

### 5.2 Why Postgres fits the event log (not just the control plane)

It is genuinely strong where SQLite is weak, so the opt-in covers
*both* stores, not only `hub.db`:

- **MVCC → no single global write lock.** Concurrent writers via
  row-level locking + WAL — **dissolves the single-writer ceiling**
  outright, without per-file sharding (§4.3). (Caveat: single-threaded
  per-row insert is *slower* than local SQLite; Postgres wins on
  *concurrent* writes and ops features — batch with `COPY` for ingest.)
- **Declarative partitioning** of `agent_events` by time/agent —
  **dropping an old partition is O(1)**, which answers retention/
  compaction (lever 2) and the missing hard-delete (`hub-scaling`
  §8-Q7) far more cleanly than `DELETE`.
- **BRIN indexes** are tailor-made for append-only, naturally-ordered
  `seq`/`ts` — tiny index, cheap range scans.
- **TimescaleDB** (if the provider offers it) adds hypertables,
  compression, and **continuous aggregates that could subsume the
  digest fold** (§3) into a DB-maintained rollup.

**What Postgres does *not* fix:** the **bytes** are unchanged — the
50 MB/task is inline 25 MiB `attach` blobs, which Postgres also stores
(TOAST, out-of-line, but still stored). **Blob-refs (lever 1) is still
needed**; Postgres relocates and concurrency-fixes, it does not
compress. Nor is it as specialised as ClickHouse/Kafka at
millions-of-events analytics — it is the **"one engine, both
workloads"** sweet spot for the hundreds-of-agents range, not the
extreme-scale tier.

### 5.3 Cost of making it selectable, and what stays local-only

A pluggable backend is a real port, not a driver flag — `database/sql`
abstracts the *driver*, not the *dialect*:

- **Two migration sets** — the 53 migrations are SQLite-flavoured (the
  `db.go` table-recreate ALTER pattern, `PRAGMA foreign_keys` handling,
  dynamic typing); Postgres needs a parallel set (real `ALTER`, strict
  types, `IDENTITY`, `$1` placeholders).
- **FTS5 → `tsvector`/GIN** — search (`handlers_search_sessions.go`,
  `mcp.go`, migration 0031) is a rewrite; `MATCH`/`snippet()` are
  SQLite-only.
- **DSN/pragmas** — the `_pragma=…` knobs are SQLite-only; Postgres
  wants connection pooling at scale.
- **Redis/NATS for SSE** — only once there are **2+ hub processes**;
  the in-process `eventbus.go` is fine for a single hub *even with an
  external Postgres* (one hub, remote DB ≠ multi-hub). Not a fix for the
  single-writer or storage axis. (`hub-scaling` §5.)

The §4 split is what makes the option **affordable**: because
`events.db` and `hub.db` are separate stores with id-only coupling, a
deployment can put **one** of them (e.g. the control plane) on Postgres
and keep the other on SQLite, or move both — the backend choice is
per-store, and the seam already exists.

> **Implication for §7:** "external engine" stops being a far-off
> escalation and becomes a **first-class configuration axis** the hub
> exposes — `storage_backend = sqlite | postgres` (per store) — with
> SQLite the zero-dependency default and Postgres the opt-in for HA /
> off-box / high-concurrency deployments.

---

## 6. Which well-tested pattern this is

So the recommendation isn't ad hoc — it maps cleanly onto named,
load-bearing practice:

- **CQRS / event-store + read-model.** `agent_events` is the event log;
  digest/turns is the read model; the fold is the projector. The
  bounded-staleness trigger is just an **async projector with a
  coalescing/debounce policy** — standard for read-model rebuilds.
- **System-of-record vs derived/time-series sidecar.** Splitting the
  OLTP control plane from the append-only telemetry log is the
  conventional "don't run your firehose through your system-of-record's
  writer" rule (app DB + a metrics/TSDB sidecar).
- **Group commit / linger batching.** (a)/(b)/(c) is the same
  flush-on-`max(size, time, boundary)` policy databases and message
  producers already use to amortise commits.
- **Per-shard single writer.** SQLite-per-workload is the embedded
  version of giving each shard its own writer — Litestream/LiteFS and
  the "SQLite-per-tenant" pattern lean on exactly the per-file writer
  isolation we're exploiting.

---

## 7. Recommendation, staged

Three independent, individually-shippable steps, smallest blast radius
first:

1. **Fold trigger → bounded-staleness (do first; smallest, highest
   value).** Rework lever 7 from "A" (per-event deferred) to the hybrid
   §3.3: mark-dirty on `turn.result`, on ≥N pending, or on ≥τ ms; keep
   the 300 ms worker as the drain and `ensureAgentDigest` as the
   backstop. Tune N (~20–40) and τ (~300–1000 ms) by re-running the
   sweep until fold-lag stops climbing with agent count. **No schema
   change**; reversible; this alone is expected to capture lever 7's
   throughput win **without** A's debt.
2. **Split into three stores — control / event / digest (do next;
   bigger, reversible).** `hub.db` (control) + `events.db`
   (`agent_events` + `agent_events_fts`) + `digest.db`
   (`agent_event_digests` + `agent_turns`), one writer each. The third
   store gives the fold its own writer → structurally fixes §3.5's
   saturation lag. Work items in §4.4: replace the
   `agent_events_stamp_project` trigger with handler-side resolution;
   denormalize `session_id` onto `agent_turns` to drop the one
   cross-store join; app-level cascade hook for the 3 (dormant) FK
   edges. Coupling is thin — **zero cross-store write transactions**.
3. **Shard `events.db` + `digest.db` per team; keep `hub.db` global
   (§4.5).** N teams → N event/digest writers + O(1) per-team
   retention/delete/backup; control stays one shared low-volume DB. Adds
   a per-(team,store) connection registry. Per-**session** is the next
   granularity if a single hot team saturates one writer — heavier, only
   on measurement.
4. **Selectable Postgres backend — a first-class config axis, not a
   far-off escalation (§5).** `storage_backend = sqlite | postgres`
   *per store*: **SQLite is the zero-dependency, offline default;
   external (managed/remote) Postgres is the opt-in** for HA, off-box
   on a RAM-starved host, or high write-concurrency. Postgres' MVCC
   dissolves the single-writer ceiling and its partitioning answers
   retention; the §4 split makes the choice per-store and affordable.
   Cost: a parallel migration set + FTS5→`tsvector` rewrite (§5.3); it
   does **not** shrink bytes (blob-refs still needed). Redis/NATS enter
   only at **2+ hub processes**, independent of the DB choice.

**Decisions — now locked in [ADR-045](../decisions/045-hub-storage-scaling.md):**

- **D1 — fold trigger policy and its constants.** turn-close + N + τ;
  the tuned N/τ with the measurement cited. (Supersedes the lever-7 A/B
  framing in `hub-scaling`.)
- **D2 — the store split (LOCKED by director, 2026-06-06; §4.4–4.5).**
  **Three stores** — control (`hub.db`) / event (`events.db`) / digest
  (`digest.db`), by data nature (the digest's own writer is what fixes
  §3.5). `agent_events_fts` travels with `agent_events`; the `events`
  envelope log + `runs`/`run_*` stay in control for v1. **Shard
  `events.db` + `digest.db` per team; `hub.db` global.** Remaining for
  the ADR to pin: the migration sequencing, the per-(team,store)
  connection-registry shape, and the app-level cascade contract for the
  3 dormant FK edges.
- **D3 — the storage-backend abstraction.** Make backend **selectable
  per store** (`sqlite | postgres`), with SQLite the offline default —
  not a one-way migration. Settles: the dialect/migration split, the
  FTS abstraction (FTS5 vs `tsvector`), and which deployments the
  managed-Postgres option targets. (Director-set direction,
  2026-06-06.)

These three change a system-wide invariant (one store, one writer,
synchronous projection, one engine) — locked in
[ADR-045](../decisions/045-hub-storage-scaling.md).

---

## 8. Costs and risks (honest)

- **Three stores, per-team-sharded = many DB files.** Three migration
  sets, and with per-team sharding (§4.5) a handle per (team, store) —
  needs an LRU-capped connection registry to bound file descriptors at
  hundreds of teams. The migration runner already exists and would be
  parameterised per file.
- **No cross-store atomicity.** Control↔event write tx: zero (all 13
  `insertAgentEvent` sites are lone `writeDB` statements). Event↔digest:
  the fold + read-repair read events and write digest in one tx today —
  restructured to read-then-write-own-tx (safe: digest is idempotent
  from the watermark), **no `ATTACH`**. Residual: 1 cross-store trigger
  and 1 read join, both with named fixes (handler-side `project_id`;
  denormalize `session_id` onto turns).
- **Fold staleness is now visible by design.** A long open turn can show
  counters up to N events / τ ms behind. Acceptable (derived data,
  reconciled at close + by read-repair) but it **is** a UX-visible
  change from today's per-event freshness — worth a director sign-off,
  since ADR-038 §2 originally chose per-event liveness on purpose.
- **Premature split risk.** If the bounded-staleness fold (step 1)
  alone drops write pressure enough for the tester's real (bursty)
  workload, the file split (step 2) can wait. Sequence matters: **fold
  fix first, measure, then decide on the split.**

---

## 9. Cross-links

- [`hub-scaling-storage-and-concurrency.md`](hub-scaling-storage-and-concurrency.md)
  — the parent: the two axes, the measured numbers, levers 1–7. This
  doc deepens its §6 (lever 7) and §8 (open questions).
- [`hub-resilience.md`](hub-resilience.md) — durability/HA; the
  external-tier (option B) this doc defers to.
- [`transcript-source-of-truth.md`](transcript-source-of-truth.md) — why
  `agent_events` is authoritative (bounds the §4.1 durability claim) and
  the §5 hybrid that would later unlock relaxed event durability.
- ADR-038 (agent-run analysis mode) — defines the digest/turns the fold
  produces and the §2 "stays fresh" choice this doc revisits.
