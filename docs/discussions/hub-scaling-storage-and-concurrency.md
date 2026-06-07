---
name: Hub scaling — storage growth and write concurrency
description: A tester running a multi-agent demo (dozens-to-hundreds of agents) on a 2 GB-RAM VPS hit two scaling worries — a single demo task cost ~50 MB of hub DB, and "how many concurrent agents does this support; do we need Redis?". This doc separates the two axes. Storage growth is driven by `agent_events` storing full transcript bytes inline (including 25 MiB `attach` blobs) plus engine-record write amplification, with NO retention/prune — not by the JSONL event-log (which mirrors the `events` envelope table, not `agent_events`). Concurrency is bounded by SQLite's single global writer, and every inbound agent event fans out into several serialized writes (insert with a MAX(seq)+1 read-modify-write, digest fold, session touch, loop-progress bump); the default `database/sql` pool is uncapped so writers contend on `busy_timeout`. SSE fan-out is an in-process Go-channel bus (drop-on-overflow), single-process only. Verdict: neither an external DB service nor Redis fixes either problem at MVP scale — they relocate bytes / add an out-of-process hop while keeping the same per-event cost. The cheap in-tree levers (blob-refs not inline, event retention, batched inserts, a real per-agent seq counter, an intentional writer/reader pool split) come first; the managed-DB / pub-sub tier is the post-MVP escalation already named in hub-resilience.md option B, justified only once load is measured. No ADR locked.
---

# Hub scaling — storage growth and write concurrency

> **Type:** discussion
> **Status:** Open (2026-06-06) — **post-MVP.** Raised by a tester
> running a multi-agent demo on a small VPS. Companion to
> [`hub-resilience.md`](hub-resilience.md) (durability/HA) and
> [`transcript-source-of-truth.md`](transcript-source-of-truth.md)
> (why the hub stores transcripts at all). Decisions locked in
> [ADR-045](../decisions/045-hub-storage-scaling.md).
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.807-alpha

**TL;DR.** A tester saw a single demo task consume **~50 MB** of hub
DB and asked, for a **dozens-to-hundreds-of-agents** demo on a **2 GB
VPS**, *how many concurrent agents does the hub support, and is a
Redis-like service needed?* These are **two separate problems** and
both have **cheaper in-tree answers than "add an external service."**
Storage growth is `agent_events` holding full transcript bytes inline
— including up to **25 MiB** `attach` blobs (`handlers_blobs.go:20`) —
plus the engine-record write amplification, with **no retention sweep**.
Concurrency is bounded by SQLite's **single global writer**, and every
inbound agent event triggers **several serialized writes** (insert +
digest fold + session touch + loop-progress bump), with an **uncapped**
`database/sql` pool so writers contend on `busy_timeout(5000)`. SSE
fan-out is an **in-process Go-channel bus** (`eventbus.go`),
drop-on-overflow, single-process only. **A managed DB or Redis
relocates bytes / adds a network hop without changing the per-event
cost** — they're the post-MVP escalation (already named in
`hub-resilience.md` option B), not the first move. Measure first, then
pull the in-tree levers (§6), then escalate only if a measured
requirement demands it.

---

## 1. What the tester observed

Two data points, two different axes:

1. **Storage:** one demo *task* cost **~50 MB** of hub database. On a
   2 GB-RAM VPS that *feels* alarming, and the instinct was "move the
   DB to a service."
2. **Concurrency:** the real demo is **multi-agent — dozens to
   hundreds**. "How many concurrencies does the system support? Is a
   Redis-like service needed?"

Both instincts (managed DB, Redis) reach for an **external service**.
The analysis below argues that at the scale described, the external
service is the *wrong first lever* for both — it solves a problem the
system doesn't have yet (multi-process HA) while leaving the problem it
*does* have (per-event cost and unbounded growth) untouched.

A framing correction up front, because it changes the RAM worry:
`modernc.org/sqlite` is an **on-disk** store, and the hub sets **no
enlarged page cache** (`db.go:43` — only `journal_mode(WAL)`,
`synchronous(NORMAL)`, `busy_timeout(5000)`). A 50 MB database does
**not** cost 50 MB of RAM; only a query that pulls a whole transcript
into memory spikes. So the 2 GB VPS is threatened by unbounded **disk**
growth and by **write-throughput stalls**, not by the DB file size
sitting in RAM.

---

## 2. Storage growth — where the 50 MB lives

Per-agent transcript bytes land in:

1. **`agent_events` (SQLite) — the source of truth.** Each `text` /
   `tool_call` / `tool_result` frame is one row, and **large tool
   payloads are stored inline** in `payload_json`
   (`agent_event_insert.go:59`). The `attach` blob cap is **25 MiB
   decoded** (`handlers_blobs.go:20`), ~33 MiB as base64 — a single
   attach in a demo turn dominates the 50 MB on its own. (This is the
   same base64 that blew up the mobile transcript render in v1.0.789;
   the *render* was capped, the *storage* was not.)
2. **The engine's on-disk JSONL on the host** — the **write
   amplification** that `transcript-source-of-truth.md` §4 already
   flags as "addressable later" via event compression. Every event is
   written twice: once to the engine record, once to `agent_events`.
3. **The folded digest tables** (ADR-038) — a derived read-model;
   small relative to the raw events.

**Correction to an earlier claim:** the append-only
`event_log/<date>.jsonl` files do **not** mirror `agent_events`. They
mirror the **`events`** table — the A2A/channel *envelope* log
(`event_log.go:62` `readEventRow` selects from `events`: `channel_id`,
`parts_json`, `to_ids_json`, `task_id`) — for `hub-server
reconstruct-db`. So the JSONL is **not** doubling transcript bytes.

**No retention exists.** `db vacuum` (`handlers_admin_db.go:43`) and
`Backup` (`backup.go`, `VACUUM INTO`) only **reclaim free pages** —
neither deletes rows. `quality-attributes.md:79` allows audit_events to
be "compactable," but **`agent_events` is never pruned**. The disk
estimate there (`:82`, "~1 GB/team-year, events dominate, TBD
post-measurement") is contradicted by 50 MB/task and should be
re-derived.

---

## 3. The concurrency model — one writer, fanned-out writes

Three facts decide the answer.

**3.1 SQLite is a single global writer.** WAL mode (`db.go:43`) gives
many concurrent **readers** + exactly **one writer** at a time. Every
write across the whole hub — every agent's every event — serializes
through that one lock. A blocked writer retries up to
`busy_timeout(5000)` = 5 s, then errors.

**3.2 One inbound agent event is *several* serialized writes.**
`handlePostEvent` (`handlers_agent_events.go`) does, synchronously, per
event:

- `insertAgentEvent` — an INSERT whose `seq = MAX(seq)+1` **per agent**
  and `session_ordinal = MAX(session_ordinal)+1` **per session** are
  computed in the statement itself (`agent_event_insert.go:58-69`).
  This is a **read-modify-write**: correctness depends on the global
  write lock serializing it (two concurrent inserts for one agent would
  otherwise collide on `seq`). It's safe — but it *forces* serialization
  and adds a MAX scan per insert.
- `foldEventIntoDigest` — a **second transaction** (digest + turn index).
- `touchSession`, `captureEngineSessionID`, `captureSessionNameHint`,
  `bumpLoopProgress` — **more** writes/updates.

So "hundreds of agents each emitting N events/sec" multiplies into
*several × N × agents* write statements/sec all queued behind one lock.

**3.3 The connection pool is uncapped.** There is **no
`SetMaxOpenConns`** anywhere (`grep` clean). The default
`database/sql` pool opens connections on demand, so many goroutines
each grab a connection and pile onto the writer lock / `busy_timeout`
rather than queueing cheaply in Go. Counter-intuitively, an **uncapped**
pool is *worse* for a single-writer store than a deliberately small one.

**3.4 SSE fan-out is in-process and lossy-by-design.** `eventBus`
(`eventbus.go`) is a `map[channel]→set[chan]` of Go channels, 32-deep
per subscriber, **non-blocking** (drops on overflow; the client
backfills via `?since=`). This is cheap and correct **for one hub
process** — but it lives entirely in memory, so it does **not** fan out
across multiple hub processes.

---

## 4. "How many concurrent agents does it support?"

**Documented targets** (`quality-attributes.md:76-82`): ≤ 100 active
agents/team, ≤ 20 hosts/team, ≤ 100 projects/team; SSE concurrent
streams and disk/team-year are both **"TBD post-measurement."** So the
honest answer is: **the targets cover the tester's "dozens," reach the
low end of "hundreds," and have never been load-tested.**

The **real ceiling is write throughput**, not a connection count.
WAL + `synchronous(NORMAL)` on a decent disk sustains on the order of
thousands of small commits/sec — but §3.2 means each agent event is
*several* commits, each carrying a MAX-scan, and large `payload_json`
rows inflate every write. The practical limit is therefore
**events/sec, not agents**:

- Hundreds of agents at **low** event rates (occasional tool calls,
  coalesced text) — plausibly fine.
- Hundreds of agents **streaming** (token-delta events, chatty tool
  loops, big inline payloads) — the single writer becomes the queue;
  latency climbs and `busy_timeout` errors appear.

The bottleneck is **serialized writes**, and several of the §6 levers
raise that ceiling without any new infrastructure.

### 4.1 Measured (2026-06-06)

Run with the in-tree harness `internal/server/load_test.go`
(`TestLoad_AgentEventIngest`, env-gated `HUB_LOADTEST=1`, skipped in
CI). It seeds N agents and drives the **real** `POST .../events`
handler in-process via `s.router.ServeHTTP` — no socket, so the
HTTP-client pool can't masquerade as the bottleneck. Box: **2 vCPU**
(`GOMAXPROCS=2`) — a deliberate small-VPS proxy. 256 B payloads, agents
posting as fast as they can for 5 s each:

| Agents | Throughput (ev/s) | p50 | p99 | max | errors |
|---:|---:|---:|---:|---:|---:|
| 1 | **639** | 1.2 ms | 11 ms | 21 ms | 0 |
| 10 | 610 | 4.2 ms | 194 ms | 1.28 s | 0 |
| 50 | 537 | 11 ms | 1.42 s | 2.52 s | 0 |
| 100 | 460 | 16 ms | 2.50 s | 5.10 s | 0 |
| 200 | 332 | 42 ms | 4.51 s | 5.74 s | 0 |
| 400 | 221 | 350 ms | 5.80 s | 6.80 s | **1× `SQLITE_BUSY` 500** |
| 800 | 133 | 5.27 s | 7.74 s | 8.06 s | 0\* |

Reading the curve — it is the textbook single-writer signature:

- **Throughput does not scale with agents; it _declines_.** Peak is
  **~640 ev/s at one agent**; adding agents *reduces* aggregate
  throughput (congestion collapse: 460 @100 → 332 @200 → 221 @400 →
  133 @800). There is no concurrency win to be had from the write path
  as built — the writer is a single lane.
- **Latency degrades super-linearly.** p99 climbs from **11 ms → 4.5 s**
  between 1 and 200 agents (~400×). By 200 agents `max` (5.74 s) already
  exceeds `busy_timeout(5000)`.
- **The error cliff is ~400 saturating agents** — the first
  `500 {"error":"database is locked (5) (SQLITE_BUSY)"}` appears there
  (`busy_timeout` exhausted). (\*At 800 the run logged 0 errors only
  because requests serialize so hard that fewer reach the timeout window
  in the 5 s — p50 is already 5.3 s; the system is effectively stalled,
  not healthy.)
- **Storage amplification confirmed:** ~**1.1 KB on disk per event** for
  a 256 B payload (~4× — indexes + the ADR-038 digest fold + WAL). At a
  sustained ~600 ev/s that is ~0.65 MB/s ≈ **2.3 GB/hour** of transcript
  if agents stream continuously — the same dynamic behind the tester's
  50 MB/task.

**Caveats.** (1) 2 vCPU — a bigger box lifts the absolute ceiling
(more cores for the Go side, digest fold, SSE) but **not the
single-writer shape**; throughput-declines-under-contention persists.
(2) In-process driver excludes real network/TLS/HTTP-client cost, so
these are an **upper bound** on the server's own ingest — a networked
deployment is lower. (3) Saturating load; real agents are bursty, so a
given agent *count* tolerates more when per-agent event *rates* are low
— which is why the honest axis is **events/sec, not agents**.

**Translation for the tester's demo.** Dozens of *calm* agents (≤ ~1
event/s each) fit under the ~600 ev/s ceiling — but already with
multi-second tail latency. *Hundreds* of *streaming* agents (token
deltas, chatty tool loops) saturate the single writer and then return
`SQLITE_BUSY` 500s. A managed DB or Redis changes none of this curve;
the §6 levers (cap the writer pool so requests queue cheaply in Go;
kill the `MAX(seq)+1` read-modify-write; batch ingest; fold the digest
in the same tx) are what move the cliff.

### 4.2 After the writer/reader pool split (lever 3 — SHIPPED)

Implemented the read/write pool split (§6 lever 3). **Same box, same
harness, 2 vCPU:**

| Agents | Before ev/s | After ev/s | Before p99 | After p99 | Before err | After err |
|---:|---:|---:|---:|---:|---:|---:|
| 1 | 639 | 617 | 11 ms | 12 ms | 0 | 0 |
| 100 | 460 | **650** | 2.50 s | **0.51 s** | 0 | 0 |
| 200 | 332 | **617** | 4.51 s | **1.04 s** | 0 | 0 |
| 400 | 221 | **576** | 5.80 s | **2.07 s** | 1× BUSY | **0** |
| 800 | 133 | **454** | 7.74 s | **4.51 s** | stalled | **0** |

- **Congestion collapse eliminated.** Throughput is now flat at
  ~580–650 ev/s from 100→400 agents (was 460→332→221). The writer is
  still one lane, but requests queue cheaply in Go instead of thrashing
  the SQLite lock.
- **The SQLITE_BUSY cliff is gone.** 0 errors at 400 *and* 800 agents
  (was the first `SQLITE_BUSY` 500 at ~400, stalled at 800).
- **p99 latency 2–4× better** across the contended range (400 agents:
  5.8 s → 2.1 s).

**Design note — why a *dedicated writer pool*, not just
`SetMaxOpenConns(1)` on the shared pool.** The naïve cap (one pool, one
connection) deadlocks: any goroutine holding an open `*sql.Rows` across
another query on that pool starves for the single connection — and the
codebase does this routinely (the insights aggregator's nested
per-row lookups; hundreds of test verification queries). The shipped
shape sidesteps it entirely: `s.db` stays the **uncapped reader** pool
(all `Query*`, all tests — unchanged), and a **separate `s.writeDB`**
pool capped to one connection takes **all** writes (`Exec`/`BeginTx`,
plus the write/mixed helpers `insertAgentEvent`/`ensureAgentDigest`/…).
The writer pool only ever runs `Exec`/`BeginTx` — it never holds an
open `Rows` — so the 1-connection cap cannot deadlock, and the 13
`BeginTx` blocks are tx-local (audited) so none nest-acquires. Reads
and writes are on different pools, so an open read cursor can never
block a write. Full `go test ./internal/server` stays green.

### 4.3 Where the remaining per-event cost is (2026-06-06)

After lever 3, the writer is one lane at ~600 ev/s; the question is what
each event *does* on that lane. Two diagnostics on the same harness:

- **The insert is cheap.** Both `MAX()` lookups in the insert are
  index-backed (`idx_agent_events_agent_seq`,
  `ux_agent_events_session_ordinal`) — rightmost-entry reads, not scans.
  So lever 4 (MAX→counter) has little throughput to give.
- **The synchronous digest fold (ADR-038) is ~half the cost.** Bypassing
  the fold ~doubles ingest:

  | Agents | with fold | without fold |
  |---:|---:|---:|
  | 1 | 649 ev/s | **1202 ev/s** (+85%) |
  | 200 | 586 ev/s | **1382 ev/s** (+136%) |

  The fold runs ~5 statements/event (load digest + load open turn + save
  digest + save turn). That, not the insert, is the ceiling — which is
  why **lever 7 (defer the fold off the hot path)** is the real next
  lever, and levers 4–5 are marginal.

### 4.4 After the three-store split (ADR-045 P1 — SHIPPED, 2026-06-06)

The store separation (ADR-045 P1 / `plans/hub-storage-scaling.md`):
`agent_events` (now `events.db`) and the derived digest + turns
(`digest.db`) move to their own files, each with its **own**
single-writer pool, and the bounded-staleness fold runs in the
background worker writing to the *digest* writer — so ingest and fold no
longer share a write lock. **Same box, same harness, 2 vCPU, flat-out,
5 s, with the fold worker running** (`HUB_LOADTEST_WORKER=1`):

| Agents | Throughput (ev/s) | p50 | p99 | max | errors | fold lag |
|---:|---:|---:|---:|---:|---:|---:|
| 1 | **995** | 0.7 ms | 4.9 ms | 10 ms | 0 | 2 % |
| 100 | **1017** | 66 ms | 457 ms | 774 ms | 0 | 11 % |
| 200 | **1018** | 131 ms | 870 ms | 1.36 s | 0 | 10 % |
| 400 | 860 | 292 ms | 2.35 s | 4.16 s | 0 | 21 % |
| 800 | 764 | 740 ms | 3.98 s | 5.52 s | 0 | 24 % |
| 1000 | 846 | 864 ms | 4.05 s | 5.58 s | **0** | 34 % |

- **The error cliff is gone all the way to 1000 agents.** 0 `SQLITE_BUSY`
  at *every* level (was the first 500 at ~400 pre-everything; lever 3
  alone carried it past 800). A separate writer per store means an ingest
  write and a fold write never contend for the same lock.
- **Throughput ~1000 ev/s through 200 agents, ~760–850 under deep
  saturation** — roughly **double** the lever-3 synchronous-fold baseline
  (§4.2: 650 @100, 617 @200, 576 @400, 454 @800), *and the fold is still
  running*. The deferred fold is off the ingest hot path and on its own
  writer, so the two pipelines run in parallel across the two cores.
- **Fold freshness is the real win of the split.** The earlier deferred
  cut on the *shared* writer couldn't drain under load (lag **90 % @200,
  99 % @800** — the worker starved for the one writer). With its own
  writer the lag collapses to **2–24 % through 800 agents**, 34 % at the
  synthetic 1000-agent flat-out — and every un-folded event is
  read-repair-backed (`digestIsStale` → backfill), so a lagging digest is
  never *wrong*, only lazily recomputed on read. The residual lag at
  800–1000 is simply one fold writer not draining a 2-core box's full
  flat-out ingest flood; it falls to single digits once offered load
  drops below the ceiling — the bursty regime real agents live in.
- **Storage** held at ~0.2–0.3 KB on-disk per 256 B event summed across
  the three store files (WAL-dependent in a 5 s window; the inline-blob
  amplification the tester hit is a separate axis — lever 1).

**Caveats unchanged:** 2 vCPU upper bound, in-process driver, saturating
load — the honest axis is still events/sec, not agents. What the split
buys is **headroom (no error cliff to 1000 agents) + a fold that keeps
up**, not a different single-writer shape for *ingest* itself. Giving
ingest more than one writer is **P2** (per-team sharding → N event-store
writers), gated on a real demo actually reaching this ceiling.

### 4.5 After Tier-1 PRAGMA tuning (SHIPPED, 2026-06-06)

Connection-string pragmas only, no code-path change: `temp_store=MEMORY`
+ a 256 MiB `mmap_size` on **every** pool, plus a 64 MiB
`cache_size` on the **single-connection writer pools only** (`cache_size`
is per-connection, so a big cache on the *uncapped* reader pools would
multiply across concurrent readers and exhaust RAM on a 2 GB VPS — the
writer pools are a bounded set: one control writer + one events + one
digest writer per open team). Same box / harness / methodology as §4.4.

| Agents | Baseline (ev/s) | Tier-1 (ev/s) | Δ |
|---:|---:|---:|---:|
| 100 | 861 | 844 | wash |
| 400 | 888 | 841 | wash |
| 800 | ~690 (med of 4) | ~890 (med of 7) | **+~25 %** |
| 1000 | ~700 (med) | noisy | inconclusive |

0 `SQLITE_BUSY` at every level, both configs. **The win is real but
narrow: ~20–29 % at writer saturation (≥800 agents), a wash below it.**
Mechanism: under sustained saturation the WAL grows and
checkpoints/page-faults stall the writer; the bigger writer cache + mmap +
in-RAM temp cut that I/O. Below the ceiling the working set already fits
the default ~2 MB cache, so there's nothing to save — the bottleneck there
is SQL work + writer serialization, the **same finding as the rejected
batched-insert lever** (§6 #5). The 1000-agent column is dominated by
run-to-run variance on a 2-core box under full flood and isn't a reliable
signal either way. **Verdict: a free, zero-complexity headroom buffer for
the saturated tail — but the real bursty agent workload lives in the
"wash" regime, so this is not the lever that moves the common case.**

**Cache-size sweep (n=800, the saturated regime; medians):** the writer
cache is operator-tunable via `HUB_SQLITE_WRITER_CACHE_KB` (default 64 MiB):

| Writer cache | Throughput (ev/s) |
|---:|---:|
| 2 MiB (sqlite default) | ~647 |
| 16 MiB | ~790 |
| 64 MiB (default) | ~878 |
| 256 MiB | ~905 |

Monotonic with diminishing returns: the knee is ~16–64 MiB; 256 MiB edges
higher but at 4× the per-writer RAM. 64 MiB is a sound default, not
over-tuned. (Caveat: a 5 s run's DB is only ~3–4 MiB, so this micro-
benchmark can't fully resolve the cache-vs-DB-size curve — a definitive
value wants a realistically-sized store; the env knob exists to tune it
per VPS. `cache_size` is a *cap*, not an allocation — an append-mostly
writer only fills the pages it touches, so a hot team's real writer RSS is
usually a few MiB, well under the 64 MiB cap.)

### 4.6 The VPS ceiling — per-team sharding raises it to the core count (2026-06-06)

Fixing the offered load at 800 agents and fanning them across N teams
(`HUB_LOADTEST_TEAMS`) — each team its own `events.db`/`digest.db` writer
(ADR-045 P2) — measures whether sharding lifts the **aggregate** ingest
ceiling on this 2-vCPU box. Flat-out, 5 s, fold worker on:

| Teams | Agents/team | Aggregate (ev/s) | Fold lag |
|---:|---:|---:|---:|
| 1 | 800 | ~810 | ~30 % |
| 2 | 400 | **~1080** | ~58 % |
| 4 | 200 | ~1075 | ~71 % |
| 8 | 100 | ~775 | ~91 % |

0 `SQLITE_BUSY` at every point. **Sharding raises the ceiling ~+33 % (one
writer → two), then plateaus at ~1100 ev/s by 2–4 teams (≈ the 2 cores),
then *regresses* at 8.** So the ceiling on a 2-vCPU box is **CPU-bound at
~1100 ev/s flat-out**, not writer- or RAM-bound, once sharded to ≈ core
count. Past the core count, extra teams buy **isolation** (a busy team no
longer lock-contends its neighbours) but no throughput. **Practical rule:
teams ≈ core count for throughput; more only for isolation; the ~1100 ev/s
figure scales with cores, and real bursty agents (think/tool/wait between
events) sit far below it** — at ~0.5 ev/s/agent the ingest ceiling is
~2000+ concurrently active agents, well beyond any single demo.

**Follow-up — the per-team fold worker (SHIPPED).** The table above ran
with the *single* global fold worker, and it was the next bottleneck the
sharding exposed: one goroutine drained every team's dirty agents serially,
so fold lag climbed *with team count* — 30 % @1 → 71 % @4 → **91 % @8**
(correctness preserved throughout by read-repair; only freshness suffered).
`foldDueByTeam` (digest_worker.go) now fans each tick's due agents out
**per team** — one goroutine per team with work, parallel across the
independent per-team digest writers and cores, serial within a team (which
shares one writer). The single-team common case still folds inline.
Re-measured: lag is roughly **halved and no longer grows with team count**
— **71 % → ~45 % @4, 91 % → ~48 % @8** (now flat across team count, bounded
by cores rather than teams). Ingest throughput is unchanged — the fan-out
buys digest *freshness* under many teams, not a higher ingest ceiling
(that's still CPU-bound). Under realistic bursty load (cores with slack)
the parallel drain does even better.

### 4.7 Read-path, SSE and memory probes — the other three axes (2026-06-07)

The load test above measures only the **ingest write path**. Three
companion probes (`internal/server/scaling_probe_test.go`, gated behind
`HUB_SCALEPROBE=1`, CI-skipped, with a live `MemAvailable` floor guard so
they're safe on a shared box) cover the axes it doesn't: read latency under
write load, SSE fan-out, and process memory. Run on the same 2-vCPU / ~2 GB
box.

**A — read latency under write contention.** 4 team shards, 40 writers
flat-out, 6 readers hammering three shapes. Two findings, one a real bug:

- The `/v1/insights` **response cache was defeated**. Its key is
  `(scope, since, until)`, and an absent `until` defaulted to
  `time.Now()` formatted to the nanosecond — so every param-less request
  (the common mobile case: Project Detail refresh sends no window) had a
  unique key → **0 % hit rate**, re-scanning `agent_events` every call
  (engine-scope p50 **135 ms**). Fix: quantize the default `until` to the
  30 s TTL boundary so repeated param-less reads share a key. Measured
  after: engine-scope p50 **135 ms → ~50 µs** (cache now fires).
- The keyset event-page reader is healthy under load (sub-ms p50; p99
  spikes are writer-lock contention).

A concurrent per-shard **insights fan-out** was implemented and tested
here too, and **rejected**: on a CPU-bound 2-vCPU box, parallelism adds no
throughput (no spare cores; modernc SQLite is pure-Go CPU work) and
measured a *worse* engine-scope p50 (231 ms parallel vs 135 ms serial)
plus goroutine/lock overhead. The fan-out stays serial; the cache fix is
the real win. (Revisit only for a many-core deployment that also routinely
cache-misses on wide engine scope — then per-shard partials with a
lock-free merge, not a mutex-guarded accumulator, is the shape to measure.)

**B — SSE eventBus fan-out + drop.** The bus is a 32-deep per-subscriber
buffer that drops on overflow. Paced at a realistic 1000 ev/s on one
channel (thundering-herd: many directors tailing one busy run):

| Subscribers | Publish p50 | p99 | Fast-drainer drop | Slow-drainer drop (500 ev/s) |
|---:|---:|---:|---:|---:|
| 50 | 9 µs | 27 µs | 0 % | 55 % |
| 200 | 33 µs | 100 µs | 0 % | 58 % |
| 800 | 127 µs | 342 µs | 0 % | 61 % |

A subscriber that keeps up loses nothing; only one draining slower than
the publish rate sheds the excess, then recovers via `?since=` backfill
(every drop converts into read load — back to axis A). Fan-out is **O(N)
under the bus lock**: ~127 µs/publish at 800 subscribers ≈ 13 % of one
core at the 1000 ev/s ceiling, negligible at ≤ 200. No change needed at
MVP scale; the number to watch is concurrent live-tail count, not agents.

**C — process RSS vs. open per-team store count.** Opening team shards one
at a time and sampling RSS: fixed overhead is **~0.75 MiB per open store**
(~123 MiB projected at the 128-store LRU cap) — trivial on 2 GB. But the
real lever is the **writer cache**: the prior design gave every per-team
writer pool a flat 64 MiB, and with 2 pools/team (events + digest) × up to
128 open teams that product was **unbounded** (a 2 GB-box hazard). Fix:
`perTeamWriterCachePragma` (db.go) divides a single budget
(`HUB_SQLITE_WRITER_CACHE_BUDGET_MB`, default 256) across `2 × maxOpen`
pools, clamped to `[1 MiB, HUB_SQLITE_WRITER_CACHE_KB]`, so the aggregate
per-team writer cache stays ≈ the budget regardless of team count (fewer
teams ⇒ a bigger cache per pool, same total). The global `hub.db` control
writer keeps its full cache (it's a single pool). A/B load test confirmed
**no ingest regression** from the smaller default per-pool cache (878 vs
894 ev/s, within noise) — the write path is CPU-bound with a small working
set, so cache size barely moves it.

**Net:** two shipped fixes (insights cache key; bounded per-team writer
cache) and one measured-and-rejected (parallel fan-out). The recurring
lesson, consistent with §4.6: on a small CPU-bound box, the wins are
**doing less work** (cache hits, bounded memory), not **more parallelism**.

---

## 5. "Is a Redis-like service needed?"

**For a single hub process: no.** Redis is (a) an in-memory store, (b)
a pub/sub bus, (c) a cache/rate-limiter. The hub already has an
in-process pub/sub bus (`eventBus`); routing it through Redis adds a
network hop and a serialization round-trip for **zero** benefit while
one process owns all state. Redis is **not** a substitute for the
authority store either — the event log, tasks, policy, and attention
items need durability + transactional consistency, which is SQLite's
job, not Redis's (`hub-resilience.md` §6-D: "the DB stores the truth;
the hub decides, brokers, and defends it").

**Where a Redis-like component *would* earn its place — all post-MVP:**

- **Multi-hub SSE fan-out.** The moment there is **more than one hub
  process** (the `hub-resilience.md` §6 **option B** world — managed
  Postgres / Turso / rqlite behind several hubs), the in-process bus
  (§3.4) breaks: a client on hub-A must see events written via hub-B.
  *Then* you need cross-process pub/sub — **Redis pub/sub, Postgres
  `LISTEN/NOTIFY`, or a bus** (named verbatim in `hub-resilience.md`
  §6 caveats). This is HA infrastructure, not a single-VPS demo need.
- **A durable write buffer / queue** in front of the writer to absorb
  bursts — but an **in-process bounded channel + batched inserts**
  (§6) achieves the same without an external dependency or a second
  failure domain.
- **A shared cache / rate-limiter** across hub processes — again only
  relevant once there's more than one process.

So Redis is an **option-B escalation**, coupled to multi-hub HA, not a
fix for "dozens-to-hundreds of agents on one VPS." Reaching for it now
trades away the single-binary, zero-dependency deployment (ADR-002)
that `hub-resilience.md` is careful to preserve.

---

## 6. The in-tree levers (do these before any external tier)

Ordered cheapest-highest-value first:

1. **Stop storing large blobs inline in `agent_events`.** Persist a
   `blob:sha256/<hex>` reference (the bytes already live in the host
   blob store, 25 MiB cap) instead of inline base64 in `payload_json`.
   Biggest single win for the 50 MB/task case.
   **CUT 1 SHIPPED (2026-06-06):** `payload_externalize.go` — on POST
   `/events` ingest the hub walks `payload_json` and rewrites any string
   leaf over 64 KiB (e.g. an `attach` tool_call's multi-MB base64) into
   a `blob:sha256/<hex>` ref via the existing `storeBlob`
   (`handlers_blobs.go`, content-addressed + deduped). Lossless
   (`json.Decoder.UseNumber` so re-marshal can't corrupt a large int;
   sha reconstructs the bytes), no-op for normal small events (single
   length check), and it doesn't disturb the digest (its `json_extract`
   paths are small scalars). Reads stay lazy — consumers fetch via
   `GET /v1/blobs/<sha>` (mobile already resolves `blob:sha256/…` →
   `/v1/blobs/…`). **FOLLOW-UPS:** (a) a one-time backfill sweep to
   reclaim the existing inline 50 MB (cut 1 only stops new bleeding);
   (b) a nicer mobile feed-card render of an externalized ref
   (chip/size instead of the raw ref string) — device-tested.
2. **Add event retention/compaction for `agent_events`** (mirroring
   the audit-event "older rows compactable" posture,
   `quality-attributes.md:79`) — a sweep, a per-team byte/age cap, or
   cold-storage offload of terminated-agent transcripts.
   **DEFERRED (director, 2026-06-06)** — blocked on a real hard-delete
   primitive. Today every entity "delete" is a **soft archive**
   (`handleArchiveProject`, `handleArchiveAgent` flip status; the only
   `DELETE FROM projects` is demo-seed cleanup), and there is **no
   cascade purge** of a project's rows across the ~57 `project_id`-scoped
   columns. A retention sweep that actually reclaims disk needs that
   delete primitive first — otherwise pruning `agent_events` alone
   orphans digests/turns/runs/attention and never frees project-scoped
   data. Build the cascade hard-delete (see §8-Q7) before this lever.
3. **Cap and split the connection pool intentionally — SHIPPED (§4.2).**
   A dedicated single-connection **writer** pool (`s.writeDB`) takes all
   writes so they queue in Go instead of thrashing the SQLite lock; the
   uncapped **reader** pool (`s.db`) serves all `Query*` + tests. Turned
   the congestion-collapse + `SQLITE_BUSY` cliff into flat throughput
   with 0 errors at 800 agents. (Crucially a *separate* writer pool, not
   `SetMaxOpenConns(1)` on the shared pool — see §4.2 design note for the
   deadlock that avoids.)
4. **Replace the `MAX(seq)+1` read-modify-write** (§3.2) with a real
   per-agent monotonic counter. **LOW VALUE — superseded by lever 7.**
   Verified the two aggregate lookups are already **index-backed**
   (`idx_agent_events_agent_seq(agent_id, seq)` and the partial unique
   `ux_agent_events_session_ordinal(session_id, session_ordinal)`), so
   each is a rightmost-entry index read (O(log n)), *not* a scan — there
   is no scan to remove. A counter would still let us drop the
   correctness-dependency on global serialization (a prerequisite for
   >1 writer) but buys little throughput today. Diagnostic (§4.3) shows
   the insert is cheap; the cost is the fold.

7. **Move the per-event digest fold OFF the ingest hot path (lever 7,
   the real one).** §4.3 measured the synchronous ADR-038 fold at
   **~half the per-event cost** (bypassing it ~doubles throughput:
   649→1202 ev/s at 1 agent, 586→1382 at 200). Fold in a **background
   worker** instead — ingest marks the agent dirty (cheap, in-memory)
   and returns; a debounced goroutine folds new events incrementally
   from `watermark_seq`, with the existing read-repair (`digestIsStale`
   → backfill) as the crash/lag backstop. **Changes ADR-038's
   synchronous-fold decision to eventually-consistent (bounded by the
   worker tick), so it needs an ADR amendment + director sign-off**
   before building — the live Insight view would see aggregates lag by
   one tick. Biggest remaining throughput lever by far. **Update
   (2026-06-06):** a first cut ("A", per-event deferred worker) measured
   ~1.5–1.75× but the worker **can't drain under saturation** (fold debt
   grows). The fix — fold on **turn-close OR every N events OR every τ
   ms** (bounded-staleness), plus the deeper question of splitting the
   event log off the control plane's writer entirely — is worked through
   in
   [`hub-store-separation-and-fold-policy.md`](hub-store-separation-and-fold-policy.md),
   which supersedes this lever's A/B framing.
5. **Batch / coalesce event ingest** — group rapid same-agent events
   (esp. text deltas) into one transaction; fold the digest in the same
   tx instead of a second one. **TRIED, NOT SHIPPED (2026-06-06).**
   Folding insert + session touches + digest into one tx (with a
   SAVEPOINT to keep the fold best-effort) measured a config-dependent
   *wash*: **−20% at 1 agent (682→543 ev/s), +10% at 200 agents
   (517→571).** Root cause: the hub runs SQLite at `synchronous=NORMAL`
   + WAL, where commits **don't fsync** (only checkpoints do) — so
   consolidating commits saves almost nothing while the explicit
   `BeginTx`/`SAVEPOINT`/`RELEASE` *adds* statements, regressing the
   common low-concurrency case. The +10% under contention came from
   fewer writer-connection *acquisitions*, not fewer commits. Lesson:
   **the per-event bottleneck is SQL *work*, not commit count** —
   pursue lever 4 (less work per insert), not commit batching, unless
   `synchronous` is ever raised to FULL.
6. **Reduce event volume at the source** — coalesce token-delta events
   in the driver before they ever hit `POST /events`.

Levers 1-2 attack **storage**; 3-6 attack **concurrency**. None adds a
service or a second failure domain, and all keep the single-binary
deployment.

---

## 7. Relationship to the resilience discussion

This doc is the **"optimize the single binary"** companion to
`hub-resilience.md`'s **"make the single authority durable / HA"**:

- `hub-resilience.md` **option A** (litestream-style WAL replication)
  is about *not losing* the DB — orthogonal to size/throughput, ship it
  regardless.
- `hub-resilience.md` **option B** (managed Postgres / distributed SQL
  / replicated-SQLite behind several hubs, with Redis/`LISTEN-NOTIFY`
  for cross-process SSE) is the **only** place an external DB *or*
  Redis becomes architecturally necessary — and it's gated on a
  **measured uptime requirement**, not a demo's agent count.

The sequencing: **measure → §6 in-tree levers → option-A durability →
option-B external tier only if HA is a hard requirement.** Jumping
straight to "managed DB + Redis" skips the cheap wins and takes on the
operational tier `hub-resilience.md` explicitly defers.

---

## 8. Open questions

1. **Load test.** *Partially answered (§4.1, 2026-06-06):* on a 2-vCPU
   box the single-writer ceiling is ~640 ev/s, throughput declines under
   contention, and `SQLITE_BUSY` 500s begin ~400 saturating agents. Open:
   re-run on representative production hardware, and re-measure after the
   §6 levers land to quantify each one's lift. Harness:
   `internal/server/load_test.go`.
2. **Target scale.** Is "hundreds of concurrent agents per hub" an
   actual requirement, or is the demo really dozens? The answer decides
   whether §6 levers suffice or option B is on the critical path.
3. **Event rate per agent.** How chatty are the engines once token
   deltas / tool loops are counted — i.e. how much does lever 6 buy?
4. **Blob-ref migration cost.** *Mostly answered (lever 1 cut 1,
   2026-06-06):* new events externalize losslessly on ingest with no
   schema change, and the read path is unaffected (lazy `blob:sha256/…`
   refs the mobile already resolves). Still open: the **backfill** of
   pre-existing inline payloads (a one-time sweep), and whether the
   mobile **feed card** should render an externalized ref as a chip
   rather than the raw ref string.
5. **Retention policy shape.** Age-based, byte-cap, or
   terminated-agent-offload — and does any of it conflict with the
   operation-log / append-only framing (ADR-014)?
6. **When does multi-hub actually arrive?** That, not this demo, is the
   real trigger for Redis/Postgres; is it on any roadmap horizon?
7. **The missing hard-delete primitive (blocks lever 2).** Every entity
   "delete" today is a soft archive — `handleArchiveProject` /
   `handleArchiveAgent` flip status, no row is removed, and there is no
   cascade purge of a project's data across the ~57 `project_id`-scoped
   columns (digests, turns, runs, attention, deliverables, criteria,
   documents, metrics, images, histograms, …). Some FK `ON DELETE
   CASCADE` constraints exist (42 in migrations) but nothing *triggers*
   a hard project delete. A real retention/compaction story — and
   GDPR-style "purge this project" — needs that primitive first.
   This is a system-wide gap, not specific to scaling; it likely
   deserves its own discussion/ADR (soft-vs-hard delete semantics,
   what cascades, audit trail of a purge, append-only-log tension with
   ADR-014). Director deferred lever 2 on it (2026-06-06).

## 9. Cross-links

- `hub-resilience.md` — durability + the option-A/B spectrum; the home
  of the managed-DB / multi-hub / Redis-pub-sub discussion.
- `transcript-source-of-truth.md` — why the hub stores transcripts at
  all (§4 names write amplification + event compression).
- `../reference/quality-attributes.md` — the documented scale targets
  (§3 capacity) and the unmeasured TBDs this doc leans on.
- `hub-store-separation-and-fold-policy.md` — the deeper follow-up: the
  bounded-staleness fold trigger (supersedes lever 7's A/B) and whether
  to split the append-only event log onto its own SQLite writer,
  separate from the control-plane CRUD.
- ADR-038 — the per-run digest fold that adds a second write per event.
- ADR-002 — the single-binary, zero-dependency deployment an external
  DB/Redis would trade away.
