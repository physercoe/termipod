---
name: Hub resilience — surviving a hub failure
description: The hub is termipod's single point of authority — a deliberate consequence of blueprint axiom A3 (authority must be coherent and tamper-evident), not an oversight, so decentralizing it is off the table. This doc asks what happens when the hub fails. It distinguishes transient unavailability (crash, restart, partition) from catastrophic data loss (the VPS dies), audits what already survives an outage (the host-runner keeps agents running; the mobile is cache-first), names what is already resilient (ADR-034's deadline sweep is a crash-recoverable reconcile loop) and the gaps (no DB backup or replication — verified; the host-runner write-buffer/replay protocol is unspecified; envelope composition is hub-only so A2A cannot proceed during an outage; the orchestration contract raises hub-centrality). It recommends option A — make the single authority durable and recoverable (continuous SQLite replication, a specified degraded mode) rather than redundant (split-brain risk) or decentralized (fights A3). Post-MVP.
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
degraded mode. This doc recommends **option A**: make the single
authority *durable and recoverable*, not redundant or decentralized.

---

## 1. The hub is a deliberate single authority

The hub centralizes names, policy, the event log, and references.
That centralization is not an accident to be engineered away — it is
**derived from axiom A3.** Agents are stochastic executors *with
authority*; their governance (policy, audit) must be authoritative,
and [`blueprint.md`](../spine/blueprint.md) §3.1 states the reason
plainly: "scattered storage permits inconsistency and tampering."

So the framing matters: **decentralizing the hub is off the table** —
it fights the axiom. The hub is *meant* to be the single source of
authority. The engineering question is therefore not "how do we remove
the single point" but "how do we make that single point **survive a
process outage** and **survive losing its disk**."

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

**A — make the single authority durable and recoverable.** Continuous
SQLite replication — WAL shipping (litestream-style) to object storage
— turns "catastrophic loss" into "restore to seconds-ago," cheaply and
with no architectural change. Plus: specify the host-runner
buffer/replay protocol (§5), and decide whether the host-runner may
compose a *provisional* envelope during an outage with hub-validation
deferred. This keeps the A3-mandated single authority and removes its
fragility.

**B — hub redundancy / failover.** A warm standby with a replicated
DB, health-checked failover. Real operational complexity, and a
**split-brain risk**: two hubs each believing they hold authority
produce conflicting authority writes — itself an A3 violation. Note
`quality-attributes.md` already names *federation* (multiple hubs
exchanging A2A tasks) as a post-MVP scaling idea — but federation is
sharding, not high-availability; it does not make any one hub
resilient. B is likely overkill for the single-team-per-hub assumption
and should wait for a concrete uptime requirement.

**C — decentralize the authority.** Off the table — it fights A3 (§1).

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

## 8. Recommendation

Adopt **option A** — the hub stays the single authority A3 mandates;
it is made *durable and recoverable*, not redundant or decentralized.
Sequence, post-MVP:

1. **Continuous DB replication** (WAL shipping to a separate failure
   domain) — the cheapest, highest-value step; it alone closes the
   catastrophic-loss gap.
2. **Specify the degraded-mode protocol** — the host-runner
   write-buffer + replay (ordering, idempotency, overflow).
3. **Decide A2A-during-outage** — provisional host-composed envelopes
   with deferred validation, or an explicit pause.
4. **Add an RPO/RTO target** to `quality-attributes.md`.

Defer **option B** (redundancy/failover) until a measured uptime
requirement justifies its complexity and split-brain risk. A companion
plan/ADR follows once §7 is resolved.
