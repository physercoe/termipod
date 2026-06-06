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
> (why the hub stores transcripts at all). No ADR locked.
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

We can't quote a number honestly without a load test (§8). What we
*can* say: the bottleneck is **serialized writes**, and several of the
§6 levers raise that ceiling without any new infrastructure.

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
   Biggest single win for the 50 MB/task case; needs a code + migration
   look at how `tool_call`/`tool_result` payloads are written.
2. **Add event retention/compaction for `agent_events`** (mirroring
   the audit-event "older rows compactable" posture,
   `quality-attributes.md:79`) — a sweep, a per-team byte/age cap, or
   cold-storage offload of terminated-agent transcripts.
3. **Cap and split the connection pool intentionally** —
   `SetMaxOpenConns(1)` for the writer path (queue in Go, not on the
   SQLite lock) with a separate **reader** pool for `GET`/SSE backfill.
   Turns lock contention into cheap goroutine queueing.
4. **Replace the `MAX(seq)+1` read-modify-write** (§3.2) with a real
   per-agent monotonic counter (a dedicated table row or an
   autoincrement keyed by agent) so inserts stop scanning and stop
   *requiring* global serialization for correctness.
5. **Batch / coalesce event ingest** — group rapid same-agent events
   (esp. text deltas) into one transaction; fold the digest in the same
   tx instead of a second one.
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

1. **Load test.** What is the actual events/sec ceiling of the single
   writer on representative VPS hardware, and at what agent-count ×
   event-rate does p99 ingest latency or `busy_timeout` error rate
   break? Nothing is measured today; this gates every number in §4.
2. **Target scale.** Is "hundreds of concurrent agents per hub" an
   actual requirement, or is the demo really dozens? The answer decides
   whether §6 levers suffice or option B is on the critical path.
3. **Event rate per agent.** How chatty are the engines once token
   deltas / tool loops are counted — i.e. how much does lever 6 buy?
4. **Blob-ref migration cost.** Is moving inline tool payloads to
   blob-refs (lever 1) back-compatible with existing transcripts and
   the Insight read path, or does it need a read-repair?
5. **Retention policy shape.** Age-based, byte-cap, or
   terminated-agent-offload — and does any of it conflict with the
   operation-log / append-only framing (ADR-014)?
6. **When does multi-hub actually arrive?** That, not this demo, is the
   real trigger for Redis/Postgres; is it on any roadmap horizon?

## 9. Cross-links

- `hub-resilience.md` — durability + the option-A/B spectrum; the home
  of the managed-DB / multi-hub / Redis-pub-sub discussion.
- `transcript-source-of-truth.md` — why the hub stores transcripts at
  all (§4 names write amplification + event compression).
- `../reference/quality-attributes.md` — the documented scale targets
  (§3 capacity) and the unmeasured TBDs this doc leans on.
- ADR-038 — the per-run digest fold that adds a second write per event.
- ADR-002 — the single-binary, zero-dependency deployment an external
  DB/Redis would trade away.
