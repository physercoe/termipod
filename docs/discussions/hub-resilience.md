---
name: Hub resilience — surviving a hub failure
description: The hub is termipod's single point of authority — a deliberate consequence of blueprint axiom A3 (authority must be coherent and tamper-evident), not an oversight, so decentralizing it is off the table. This doc asks what happens when the hub fails. It distinguishes transient unavailability (crash, restart, partition) from catastrophic data loss (the VPS dies), audits what already survives an outage (the host-runner keeps agents running; the mobile is cache-first), names what is already resilient (ADR-034's deadline sweep is a crash-recoverable reconcile loop) and the gaps (no DB backup or replication — verified; the host-runner write-buffer/replay protocol is unspecified; envelope composition is hub-only so A2A cannot proceed during an outage; the orchestration contract raises hub-centrality). It recommends option A — make the single authority durable and recoverable (continuous SQLite replication, a specified degraded mode) — with a replicated-DB-tier + multi-hub setup as the escalation if uptime demands it. It rejects independent-DB failover (split-brain), decentralization (fights A3), and dropping the hub for a host-runner-shared DB (the hub is the policy gate, relay, security boundary, and broker — not a DB front-end). Post-MVP.
---

# Hub resilience — surviving a hub failure

> **Type:** discussion
> **Status:** Open (2026-05-19) — **post-MVP.** Raised by the
> principal: the hub is the centre; what if it fails? No ADR locked.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.631-alpha

**TL;DR.** The hub is termipod's single point of authority — and that
is **forced by blueprint axiom A3** (agents are stochastic and hold
authority, so policy and audit must be coherent and tamper-evident;
[`blueprint.md`](../spine/blueprint.md) §3.1 explicitly rejects
"scattered storage"). So the answer to "what if the hub fails" is
**not decentralization** — that would violate the axiom that created
the hub. It is *graceful degradation* and *durability*. A hub outage
today is already graded, not fatal: the **host-runner** is a deliberate
resilience boundary (agents keep running), and the mobile is
cache-first. The real gaps are **catastrophic data loss** — there is
**no DB backup or replication** (verified) — and an under-specified
degraded mode. This doc recommends **option A** — make the single
authority *durable and recoverable* (continuous replication) — with a
replicated-DB-tier + multi-hub setup (option B) as the escalation if
uptime ever demands it. Decentralizing the hub, or dropping it so
host-runners share the DB, stays off the table.

---

## 1. The hub is a deliberate single authority

The hub centralizes names, policy, the event log, and references.
That centralization is not an accident to be engineered away — it is
**derived from axiom A3.** Agents are stochastic executors *with
authority*; their governance (policy, audit) must be authoritative,
and [`blueprint.md`](../spine/blueprint.md) §3.1 states the reason
plainly: "scattered storage permits inconsistency and tampering."

So the framing matters: **decentralizing the hub is off the table** —
it fights the axiom. But note the precise boundary: A3 forbids a
*scattered* authority — many enforcement points that can disagree —
**not a *replicated* one.** One coherent, strongly-consistent DB
behind one *or several* hub processes is still a single authority
(§6 option B). The engineering question is therefore not "how do we
remove the single point" but "how do we make that single point
**survive a process outage** and **survive losing its disk**."

## 2. What already survives a hub outage

A hub outage today is graded, not fatal — by design:

- **Agents keep running.** The **host-runner** is a deliberate
  resilience boundary ([`blueprint.md`](../spine/blueprint.md) §3.2: it
  "must survive hub outages"). [`protocols.md`](../spine/protocols.md)
  §3: "short hub outages degrade gracefully — host-runner buffers
  writes, serves cached reads." blueprint *rejected* collapsing the
  host-runner into the hub precisely because that "loses partition
  tolerance: a network blip between hub and host kills the agent's
  pane."
- **The mobile shows cached state.** Cache-first rendering
  ([ADR-006](../decisions/006-cache-first-cold-start.md), the `sqflite`
  snapshot) means the director still sees the last-known world.
- **A2A degrades, not dies.** Cross-host *relay* through the hub
  breaks, but direct host↔host A2A still works where reachability
  allows (`protocols.md` §8).
- **Authority pauses.** New spawns, policy decisions, and event-log
  writes block or buffer until the hub returns.

This is a coherent CAP stance worth naming: **execution stays
available; authority is consistency-first and pauses-then-reconciles.**
Work continues; governance catches up. The hub is the system's CP
corner *on purpose*.

## 3. Two failure types — two different problems

"The hub failed" is two distinct failures with two distinct answers:

1. **Transient unavailability** — the hub process crashed, is
   restarting, or is network-partitioned. The hub *and its data* come
   back. The problem is graceful degradation and clean recovery.
2. **Catastrophic data loss** — the hub's disk is corrupted or the VPS
   is destroyed. The authoritative world-model is *gone*. The problem
   is durability: a recovery point to restore *to*.

Most "single point of failure" worry conflates these. They must be
designed separately.

## 4. What is already resilient

Two pieces are already sound and worth crediting:

- **ADR-034's deadline sweep is crash-recoverable by construction.**
  [ADR-034](../decisions/034-orchestration-loop-closure.md) D-3 chose a
  periodic *reconcile sweep* over per-entity timers partly *for this
  reason* — deadline state lives in persisted columns, so a hub
  restart loses nothing; the next tick re-derives. A hub down for ten
  minutes simply sees everything ten minutes staler when it returns.
- **The host-runner partition boundary** (§2) genuinely insulates
  running agents from a transient hub outage.

So transient unavailability is *partly* handled. The weak spots are
below.

## 5. The gaps

**Durability — the serious one.** Verified: there is **no hub-DB
backup, no replication, no disaster-recovery path** in the codebase.
The hub keeps the authoritative event log, tasks, attention items, and
the directive registry in a single `modernc.org/sqlite` file. The
*bytes* survive on hosts (artifacts, on-disk session JSONLs), but the
authoritative *metadata* is hub-only — and `agent_events` is the
source of truth, not the on-disk JSONL ([ADR-027](../decisions/027-local-log-tail-driver.md)).
So losing the hub disk loses the world-model, reconstructable from the
hosts only partially and lossily. `quality-attributes.md` sets latency
budgets but **no recovery-point / recovery-time target.**

**The degraded-mode protocol is under-specified.** `protocols.md` §3
asserts "host-runner buffers writes, serves cached reads" in a single
sentence — but the *protocol* is undefined: write ordering on replay,
idempotency / dedup of buffered writes, how long the buffer may grow,
what a host-runner does when the buffer overflows.

**A2A during an outage has no home.** [ADR-032](../decisions/032-message-routing-envelope.md)
D-6 composes the envelope on hub-server. With the hub down, a peer A2A
message — even one deliverable directly host↔host — cannot be
composed. The host-runner is deterministic and *could* compose a
provisional envelope (it knows `from`, `to`, `kind`), but it cannot
validate `cause` against the hub's directive registry.

**The orchestration contract raises hub-centrality.** ADR-032 / ADR-034
put envelope composition, the admission pipeline, and the loop-closure
sweep on the hub. The sweep degrades gracefully (§4); composition does
not (above). Worth tracking as the contract lands.

## 6. The option spectrum

The two natural "fix the SPOF" instincts — *run multiple hubs* or
*drop the hub and let host-runners share its DB* — collapse to one
real variable. The hub's functions (govern, relay, broker, secure)
are **irreducible**; you cannot have "no hub," only a hub backed by a
different DB. So the spectrum is really about the **DB tier** and how
many **hub processes** front it.

**A — replicate the DB, keep one hub (recommended first).** Continuous
SQLite replication — WAL shipping (litestream-style) to object storage
in a separate failure domain — turns catastrophic loss into "restore
to seconds-ago," cheaply, with **no architectural change** and no loss
of the single-binary deployment (ADR-002). Plus: specify the
host-runner buffer/replay protocol (§5) and decide the
A2A-during-outage path. Cheapest, highest-value; ship it first
regardless of what follows.

**B — a replicated DB tier + multiple hub processes.** If a measured
uptime requirement ever demands process-level HA, the sound form is
**one logically-single, strongly-consistent DB** (managed Postgres
multi-AZ, a distributed SQL, or a replicated-SQLite layer — LiteFS /
rqlite / Turso-libSQL) with **several stateless-ish hub processes** in
front of it. This is **A3-compatible**: A3 forbids a *scattered*
authority, not a *replicated* one — one coherent DB is still one
authority, just durable and HA.

The *naive* form — two hubs each with an **independent** DB,
warm-standby failover — is **rejected**: it splits the brain (two hubs
writing conflicting authority — an A3 violation). The shared,
replicated-DB form has no data split-brain.

Caveats — the hub is *stateless-ish*, not stateless:
- **SSE fan-out** — a client on hub-A must see events written via
  hub-B (Postgres `LISTEN/NOTIFY`, a bus, or tailing the events table).
- **The A2A reverse-tunnel relay** — host-runners hold a persistent
  tunnel to *a* hub; multi-hub needs it routed / sticky.
- **The loop-closure sweep** ([ADR-034](../decisions/034-orchestration-loop-closure.md)
  D-3) must be a **leader-elected singleton** — N hubs must not all
  escalate the same stalled task.

B trades the embedded-SQLite, zero-dependency deployment for an
operational DB tier — a real cost for a self-hostable solo tool.
Defer it until uptime is a measured requirement; until then, A's
litestream replication is the durability subset of B with none of its
complexity. (`quality-attributes.md` already names *federation* —
multiple hubs exchanging A2A tasks — as a post-MVP scaling idea;
federation is sharding by team, not HA, and does not make any one hub
resilient.)

**C — decentralize the authority** across host-runners or peers. Off
the table — it fights A3 (§1).

**D — drop the hub; host-runners share the DB directly.** Rejected.
The premise — "the DB is the source of truth" — is true *for data*,
but the hub's job is mostly **not** storage. It is the **policy gate**
(the single deterministic chokepoint that validates an authority write
*before* it happens — the [ADR-032](../decisions/032-message-routing-envelope.md)
admission pipeline, [ADR-016](../decisions/016-subagent-scope-manifest.md)
scope checks; remove it and enforcement scatters across N host-runners
— an A3 violation), the **relay** (the agent→hub relay and the A2A
reverse-tunnel for NAT'd boxes — `protocols.md` §3, §8; a DB does not
relay live traffic), the **security boundary** (TLS, bearer auth, the
safe internet-facing API — a phone cannot hold DB credentials), and
the **broker** to AG-UI (`protocols.md` §9). **The DB stores the
truth; the hub decides, brokers, and defends it.** Delete the hub and
those four jobs have nowhere to live — re-add a component for them and
you have re-invented the hub. D is option B with the hub's irreducible
functions deleted; it does not work.

## 7. Open questions

1. **RPO / RTO targets.** What recovery-point (seconds? minutes of
   lost events?) and recovery-time is acceptable? This belongs in
   `quality-attributes.md` once decided.
2. **Where does replication ship to?** The VPS provider's object
   storage is the obvious sink; it must be a *different* failure
   domain than the hub disk.
3. **May the host-runner compose a provisional envelope** during an
   outage, reconciled (validated, `cause`-checked) when the hub
   returns? Or does A2A simply pause?
4. **How much of the world-model is reconstructable** from the hosts
   alone (host-runners know their live agents; on-disk JSONLs shadow
   transcripts) — and is partial reconstruction worth building, or is
   replication (option A) enough that it never matters?
5. **Buffer/replay correctness.** Do buffered writes need idempotency
   keys so a replay after a partial flush does not double-apply?
6. **If option B is ever taken** — which DB layer (managed Postgres vs
   a replicated-SQLite layer such as LiteFS / rqlite / Turso), and
   what leader-election mechanism for the singleton loop-closure sweep?

## 8. Recommendation

Adopt **option A first** — the hub stays the single authority A3
mandates; it is made *durable and recoverable* before redundancy is
ever reached for. Sequence, post-MVP:

1. **Continuous DB replication** (WAL shipping to a separate failure
   domain) — the cheapest, highest-value step; it alone closes the
   catastrophic-loss gap.
2. **Specify the degraded-mode protocol** — the host-runner
   write-buffer + replay (ordering, idempotency, overflow).
3. **Decide A2A-during-outage** — provisional host-composed envelopes
   with deferred validation, or an explicit pause.
4. **Add an RPO/RTO target** to `quality-attributes.md`.

**Option B** (a replicated DB tier + multiple hub processes) is the
escalation if a measured uptime requirement ever demands process-level
HA; when taken, the shared replicated-DB form — not independent-DB
failover — is the A3-compatible one. **Options C and D** (decentralize
the authority; drop the hub for a host-runner-shared DB) stay
rejected. A companion plan/ADR follows once §7 is resolved.
