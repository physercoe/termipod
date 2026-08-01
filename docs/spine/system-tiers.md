# System tiers

> **Type:** axiom
> **Status:** Current (2026-08-01)
> **Audience:** contributors · principal
> **Last verified vs code:** mobile/hub/host `2026.730.1231-alpha` · desktop `2026.730.1242`

**TL;DR.** TermiPod code runs in four places — the **hub**, the
**hosts**, the **desktop**, and the **mobile app** — and each exists for
exactly one forced reason: authority must be central, compute must be
local, deep work needs a desk, attention needs a pocket. This doc states
all four roles in one place (the three-layer story lives in
[blueprint.md](blueprint.md) §3; the client tier was scattered across
ADR-050 and the IA axioms), extends the data-ownership law with its
device column, makes one implied law explicit (**the hub commands, the
host executes — the hub never runs user or agent code**), and adds the
two things no other doc has: a **placement decision procedure** ("where
does new functionality go?") and a **degradation table** ("what still
works when a tier is gone?"). Container inventories and owns/doesn't-own
checklists stay in
[architecture-overview.md](../reference/architecture-overview.md) §3;
human/agent *actor* roles stay in [governance-roles.md](governance-roles.md).

---

## 1. The four tiers, and why each one is forced

| Tier | What it is | The one forced reason it exists | Canon |
|---|---|---|---|
| **Hub** | Go daemon on a small always-on box: name service, policy engine, event log, tool catalog | **Authority must be central.** Distributed authority is *no* authority — reconciliation across copies is a research problem, not a feature | [blueprint.md](blueprint.md) §3.1, §3.4 |
| **Host** | Host-runner (deterministic Go deputy) + the agents it spawns, on every machine with compute | **Compute and bytes must stay local** (axiom A2: a 5 GB checkpoint cannot transit a bounded VPS), and the policy-enforcer must be **deterministic** — an LLM cannot police itself | [blueprint.md](blueprint.md) §3.2, §3.4; [protocols.md](protocols.md) §3 |
| **Desktop** | Electron workbench: the control plane at full size **plus** the local-work tabs (Read, Author, Inspect, Compare, Replay, Record) | **Deep work needs a desk.** Agent-scale output demands hours of reviewing, authoring, inspecting, replaying that a phone structurally cannot serve | [ADR-050](../decisions/050-desktop-workbench-delivery-model.md); blueprint A1 amendment |
| **Mobile** | Flutter app: triage, approvals, supervision — the Me tab's "what needs me now" | **Attention lives in glances.** A1's economics: hundreds of sub-minute interactions a day; the director ratifies, the fleet works | blueprint A1; [information-architecture.md](information-architecture.md) IA-A1/IA-A3 |

Two clarifications that keep the frame clean:

- **The agent is a resident, not a tier.** Agents are the stochastic
  executors that live *on hosts* behind the deterministic deputy — the
  deterministic/stochastic boundary is also the audit boundary
  ([blueprint.md](blueprint.md) §3.4). A desktop-local agent source
  ([desktop-companion-vision-parity.md](../plans/desktop-companion-vision-parity.md)
  D-7) makes the desktop *also* act as a host for its companion — the
  role moves, the tier definitions don't.
- **"Local" is two different claims.** The *host* tier is where
  execution is local; the *clients* are additionally **local-first**
  about their own state (mobile's snapshot cache, the desktop's
  device-local library/documents/vault). Don't collapse the two: a
  hub-detached desktop still has no fleet — it has its desk.

Why not fewer tiers? Blueprint §3.4 walks the three-layer collapses
(hub↔host, host↔agent). The client-tier version: **why two clients, not
one adaptive layout?** Because the two interaction patterns are
opposites — glance-triage vs hours-deep focus — and one surface serving
both serves neither; ADR-050 chose two clients on one client-agnostic
API, and the hub cannot tell them apart. The corollary that keeps this
cheap: **neither client is ever authoritative** — every shared-state
mutation goes through the hub, so client multiplicity adds no
reconciliation problem.

## 2. What each tier owns — the ownership law, all four columns

The law ([blueprint.md](blueprint.md) §4): **"The hub stores names,
policies, events, and references. Matter stays where it was produced."**
Blueprint allocates hub vs host vs cloud; the device column existed only
for SSH credentials ([information-architecture.md](information-architecture.md)
§5.2). The full table:

| | Owns | Never holds |
|---|---|---|
| **Hub** | Identities, relationships, policies, the event log, project/run/dataset *metadata* + folded digests, small documents, review + attention state, references (URIs, SHA-256s), session transcripts | Bulk bytes (>~256 KB rule of thumb); secrets in the clear (relays ciphertext only — [ADR-056](../decisions/056-env-secret-host-envelopes.md)); UI state ([ADR-062](../decisions/062-desktop-ui-as-agent-addressable-entity.md): relays, never stores) |
| **Host** | Checkpoints, datasets, tensors, media, raw logs, metrics series, git worktrees, panes/tmux, engine state dirs, host-local secrets (`StateDir` identity), job artifacts ([ADR-058](../decisions/058-host-job-surface.md)) | Agent identity (hub rows), authoritative policy (consults hub), persistent cross-restart truth ("the hub is the source of truth — host-runner holds no persistent state across restarts") |
| **Device (desktop + mobile)** | SSH credentials + vault items (never leave the device), the desktop's local-work state (documents, library, annotations, inspect tabs, drafts), snapshot caches, UI arrangement, consent toggles + leases | Authoritative shared state — every mutation of a hub entity goes through the hub; no client-side last-write-wins merging exists by design ([cross-cutting.md](../reference/cross-cutting.md) §5) |
| **Cloud (HF/S3/…)** | Published artifacts, offsite backups | Anything the system needs to function day-to-day |

And the law this repo has enforced everywhere but stated nowhere —
explicit now:

> **The hub commands, the host executes. The hub never runs user or
> agent code.** The hub's only outbound reach is answers to pull-only
> queues (spawn requests, host commands — NAT-safe, no hub-initiated
> connection); everything that computes — engines, host jobs, exports,
> traces — runs on a host (or on the device that asked for it).

Derived from A2/A3 + [forbidden-patterns.md](forbidden-patterns.md)
1/2/14 + ADR-058's detached-job design. Its client-side mirror: **the
clients render and decide; they never execute fleet work** — the
desktop's terminal and local spawns act with the *user's* authority on
the user's machine, which is exactly why they sit outside the agent
co-working scope ([ADR-064](../decisions/064-agent-desktop-coworking-contract.md) D1).

## 3. How the tiers talk

Protocol choice is forced by relationship type
([protocols.md](protocols.md)); the edges that define the tier
topology:

- **Clients → hub, one wire.** REST + the broker's output stream; the
  hub is the sole translator from internal wire formats — "the app only
  knows AG-UI" ([protocols.md](protocols.md) §9). Desktop and mobile
  never talk to each other; they meet at the hub.
- **Agents → hub, always relayed.** Agents never open network
  connections to the hub; the host-runner's gateway forwards with
  per-agent identity preserved, one hub token per host
  ([protocols.md](protocols.md) §3).
- **Hub → host, pull only.** Hosts poll; the hub never dials in
  ([ADR-018](../decisions/018-tailnet-deployment-assumption.md) for the network
  assumption, [ADR-058](../decisions/058-host-job-surface.md) for jobs).
- **Agents → desktop, through the bridge.** The loopback MCP surface
  with bearer-derived scope, consent classes, and audit
  ([ADR-059](../decisions/059-agent-browser-bridge.md),
  [ADR-062](../decisions/062-desktop-ui-as-agent-addressable-entity.md),
  [ADR-064](../decisions/064-agent-desktop-coworking-contract.md)) —
  never CDP into the workbench.
- **Bytes bypass the hub.** Media reaches renderers via
  `termipod-media://` (local or SFTP over the user's own session,
  [ADR-060](../decisions/060-datasets-entity-and-media-posture.md)); worktrees
  move host→host on teleport; blobs ≤ 25 MiB are the hub's only
  byte-carrying concession ([ADR-061](../decisions/061-blob-lifetime.md)).

## 4. Placement: where does new functionality go?

The decision procedure. Take the first rule that matches; each rule
names its precedent.

1. **Is it bulk bytes** (media, tensors, logs, anything >~256 KB per
   primitive)? → **Host** (or the device that produced it). The hub
   gets a metadata row + digest + URI. *(Datasets: ADR-060; job
   artifacts: ADR-058; blueprint §4 rule of thumb.)*
2. **Is it shared truth** — identity, policy, provenance, review state,
   anything two devices or two people must agree on? → **Hub** entity
   + event. *(References: ADR-053 — fields on the hub, PDF bytes on the
   device; decision records: compare-wall plan lane B.)*
3. **Does it compute** — long-running, GPU, filesystem-touching, or any
   user/agent code? → **Host**, under the host-runner; detach it as a
   host job if it can exceed the synchronous window (ADR-058). The hub
   only queues and records. *(The §2 law.)*
4. **Is it a secret?** → It lives on a device or a host `StateDir` and
   moves only sealed end-to-end; the hub relays ciphertext it cannot
   open. *(ADR-056; vault: ADR-052.)*
5. **Is it something a user manipulates at a desk** — a document, a
   board, a comparison, an open tab? → A **desktop** surface, and its
   agent tool lives **where the entity lives**: desktop-owned → bridge
   surface, hub-owned → hub catalog, never both (ADR-064 D2). If the
   phone needs it too, it is shared truth — go back to rule 2.
6. **Is it attention** — an approval, a decision, a notification? →
   A **hub attention item**, delivered everywhere but *decided* on the
   device where the user is; consent (cards, leases, toggles) is always
   granted at the edge, never hub-side on the user's behalf. *(ADR-030
   ladder; ADR-064 D3; attention-delivery-surfaces.md.)*
7. **Is it a capability someone else already built?** → Apply ADR-050
   D-3's ladder in order: **build · embed · integrate · interop** —
   embed vendor UIs over vendor services where they exist
   (kimi-web; companion plan D-8), build only what defines us.

Worked examples that exercise several rules at once — read these before
proposing a new flow: **teleport** ([ADR-057](../decisions/057-session-teleport.md):
the transcript never moves — it lives on the hub; only the two
byte-stores the hub does *not* own relocate host→host); **env secrets**
(ADR-056: client seals → hub relays ciphertext → host opens —
one flow touching all four tiers correctly); **the browser bridge**
(ADR-059: desktop-local capability, hub-relayed for remote agents, with
consent at the desktop).

## 5. Degradation: what still works when a tier is gone

The resilience posture is asymmetric by design — each tier degrades
along its role:

| Failure | Hub | Hosts | Desktop | Mobile |
|---|---|---|---|---|
| **Hub unreachable** | — | Agents keep running under host-runner authority; writes buffer and replay in order ([cross-cutting.md](../reference/cross-cutting.md) §5) | Local-work tabs fully functional (device-local state); standalone SSH terminal works; fleet/project views stale-serve; companion runs on a local source with hub-only capabilities absent, degraded honestly (vision-parity D-7) | Cached snapshots on every screen ([ADR-006](../decisions/006-cache-first-cold-start.md), "subway-safe"); approvals/decisions blocked until online — no offline queue of authority |
| **A host down** | Marks agents unreachable via heartbeat; queues commands (pull model) | That host's agents/jobs stop; other hosts unaffected | That host's media/SFTP unavailable; hub-side metadata still renders | Same as desktop: rows render, live surfaces show the gap |
| **Clients closed** | Fleet runs on: stewards, workers, schedules, and attention accumulate — the system is built for absent principals | Unaffected | — | — |

The one deliberate single point of authority is the hub itself — forced
by A3, defended in
[hub-resilience.md](../discussions/hub-resilience.md); resilience means
*degrading around* the hub, never *replicating* it.

## 6. Tier anti-patterns

The full list is [forbidden-patterns.md](forbidden-patterns.md); the
ones that are specifically tier confusions: hub stores bulk bytes (#1);
host-runner runs an LLM loop (#2); agents call the hub directly (relay
principle); metrics written to hub; a client holding authoritative
state or merging conflicts locally; consent decided anywhere but the
user's device; an agent operating a client by synthetic input instead
of through entities (ADR-062/064); designing the *work* the way the
*software* is tiered ([orchestration-layer.md](orchestration-layer.md)
— the operating basis is not the implementation basis).

## 7. What this doc is not

Not the container inventory (per-container owns/doesn't-own, ports,
diagrams: [architecture-overview.md](../reference/architecture-overview.md)
§3). Not the actor ontology (principal/director/steward/worker/operator:
[governance-roles.md](governance-roles.md)). Not the wire spec
([protocols.md](protocols.md)). It is the answer to four questions per
tier — *what* it is, *why* it must exist, *when* functionality belongs
there, *how* it reaches its neighbors — in one place, so placement
arguments cite a rule instead of re-deriving one.
