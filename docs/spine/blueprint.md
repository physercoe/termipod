# termipod blueprint

> **Type:** axiom
> **Status:** Current (2026-05-05)
> **Audience:** contributors
> **Last verified vs code:** v1.0.351

**TL;DR.** Authoritative reference for termipod's design philosophy,
component ontology, and the data-ownership law. Future PRs should
trace their design choices back to this document; proposals that
contradict the axioms or the data-ownership law require an explicit
amendment here, not a silent deviation.

**Refactor note (2026-05-05).** The original blueprint covered
protocols (§5), primitives (§6), and forbidden patterns (§7) inline.
Per the doc-uplift plan
([`../plans/doc-uplift.md` P1.6](../plans/doc-uplift.md)) those
moved to focused sibling docs:
- §5 → [`protocols.md`](protocols.md)
- §6 → [`../reference/data-model.md`](../reference/data-model.md)
- §7 → [`forbidden-patterns.md`](forbidden-patterns.md)

This file kept its numbering (§5/§6/§7 stubs forward to the new
homes) so existing cross-references like "blueprint.md §5.3" still
land here and find a pointer.

---

## 1. Purpose

termipod is a mobile-first control plane for a fleet of AI agents distributed
across multiple machines. It exists so a single human, acting as director,
can coordinate 24/7 agents doing real work — coding, experiments, analysis,
writing — across heterogeneous compute, with governance, audit, and
reviewability, primarily from a phone.

termipod is not a coding assistant. The agents are. termipod is the layer
that dispatches, supervises, bounds, records, and surfaces their work.

Target MVP cases: AI/CS research and software building. Primary user archetype:
researcher or small team acting as *principal* to a fleet of agent *ICs*.

---

## 2. Design philosophy: three axioms

Every element of the system is derived from these. A design proposal that
cannot be traced to one of them is a candidate for rejection.

**A1. Human attention ≪ agent output.** Agents can produce orders of magnitude
more output than a human can review. Filtering and summarization are
primitives, not features. UX optimizes for the 100-glances-per-day phone
interaction pattern, not the 8-hours-at-a-desk pattern.

**A2. Work is spatially bound to compute and data.** Training touches large
datasets on specific GPUs; analysis reads large files on specific hosts.
Work executes *where the matter is*. The system is distributed by physics,
not preference.

**A3. Agents are stochastic executors with authority.** Unlike deterministic
programs, agents have a distribution over behaviors. They can hallucinate,
loop, over-spend, touch files they shouldn't. Unattended losses grow with
autonomy. Every autonomous action must be bounded by a rule that existed
*before the action happened*.

Three axioms, three forces: **filtering** (A1), **distribution** (A2),
**governance** (A3).

---

## 3. Ontology

Every component is derived by asking which axiom forces it to exist. If no
axiom forces it, it shouldn't be in the system.

### 3.1 Hub

The **authority layer**: a name service, policy engine, event log.

Forced by:
- A1: one coherent world-model the human can consult from a phone.
- A3: policy and audit must be authoritative; scattered storage permits
  inconsistency and tampering.

Constrained by:
- A2: bulk data cannot flow through the hub. The hub runs on a bounded VPS;
  making it a data path for compute it doesn't own turns it into a
  bandwidth and storage bottleneck.

The hub stores: identities, relationships, policies, events, references to
content. It never stores bulk content itself.

### 3.2 Host-runner

The **deterministic local deputy**: a persistent, non-LLM process on each
machine that holds delegated authority across the lifetimes of many agents.

Forced by:
- A2: physical machines need a resident process that can accept spawns.
- Agent-ephemerality: agents start, work, exit. Between agents, there must
  be someone to spawn the next one, reap zombies, enforce limits.
- A3: the boundary between stochastic planner (agent) and deterministic
  executor (host-runner) is the auditability boundary. Collapsing it loses
  the ability to ground-truth what happened.

The host-runner owns machine-local resources: panes, worktrees, filesystem
paths, local secrets, local metric storage, ACP sessions, A2A endpoints. It
must survive hub outages.

### 3.3 Agent

The **stochastic executor**: an LLM-driven, ephemeral process that produces
the actual work.

Forced by A1: the human cannot produce enough. Something must.

Agents come in two role-distinguished classes (`agent-lifecycle.md` §4.9
is canonical):

- **Steward** — manager / orchestrator / head. Plans, decides, spawns,
  arbitrates, distills. *Does not perform IC work directly* outside
  the explicit single-agent bootstrap window (`agent-lifecycle.md` §6.2).
- **Worker** — IC / performer / hand. Bounded, specific work in a
  worktree. Spawned by a steward (or another worker) for one task.

Both are agents (rows in `agents`); the role is what separates them.
The split is load-bearing because it determines: which tools the agent
gets via the operation-scope manifest (`decisions/016-subagent-scope-manifest.md`),
what its conversation looks like in the mobile
UI (steward = decision surface, worker = code surface), what it
distills on session close (steward → decisions/briefs/plans;
worker → code change + task summary), and what model class it
defaults to (steward = high-capability for stakes; worker =
cost-efficient where the task allows).

**Steward tiers.** The steward role itself has two tiers, distinguished
by template provenance and lifetime:

- **General steward** (`steward.general.v1`) — bundled in the hub
  binary, **frozen template**, **persistent (one per team, always-on)**.
  Bootstraps new projects (authors domain-steward + worker templates +
  plan in phase 0), then remains available as the director's concierge
  for cross-project debugging, free discussion, template/schedule
  edits, and future-project bootstraps. Archived only by manual
  director action. See `decisions/001-locked-candidate-a.md` D-amend-2.
- **Domain steward** (`steward.research.v1`, `steward.infra.v1`,
  `steward.briefing.v1`, …) — **overlay-authored** by the general
  steward, editable by the director. **Project-scoped** lifetime;
  archived at project completion.

The general steward is the operationalisation of the single-agent
bootstrap window (`agent-lifecycle.md` §6.2), extended to remain alive
after the window closes — it does not perform IC work outside that
window either; it delegates project orchestration to the domain
steward and acts as concierge for everything else. The
manager/IC invariant holds across both tiers. The general steward is
**not** §3.4's "general-purpose steward" anti-pattern — it is general
in the sense of *team-scoped and project-agnostic*, not in the sense
of *manager + IC collapsed* (see `reference/glossary.md` §3 for the
distinction).

**Engine-internal subagents are not termipod agents.** When a
termipod-managed agent invokes its engine's internal subagent
mechanism (claude-code's `Task` tool, codex app-server child sessions,
gemini-cli subagent invocations, or analogous mechanisms in other
engines), those subagents are **not** rows in `agents`. They share
the parent's process, MCP client, tmux pane, and host-runner
supervision. They inherit the parent's operation scope by
construction, and termipod does not enumerate, restrict, or monitor
them beyond what the parent's frame profile surfaces in the
transcript. The *agent* primitive in this document refers to a
termipod-managed process; engine-internal subagents are an engine
concern. See `decisions/016-subagent-scope-manifest.md` D5 for the
formal exemption.

### 3.4 Why the three-layer separation is load-bearing

The repeated question: *why three layers? Why not collapse host-runner into hub (one less moving piece) or agent into host-runner (one less process)?* The answer comes from three distinct concerns each layer addresses; collapsing any pair re-introduces a failure mode the split was designed to prevent.

The three concerns:

1. **Authority** (hub). The single coherent view: identities, policies, audit. The principal's mental model lives here. There is exactly one. Authority must be central — distributed authority is *no* authority because reconciliation across copies is a research problem, not a feature.
2. **Locality of compute** (host-runner). Bytes (model weights, datasets, intermediate artifacts) live where compute is. The host-runner is the *deterministic deputy* on each host: it owns the local processes, the panes, the SSH lifetime, the local resource budgets. It must be local because moving the bytes for every operation is impossible (axiom A2). It must be deterministic because anything stochastic (LLMs) cannot be the policy-enforcer for itself.
3. **Stochastic execution** (agent). The agent is the LLM-driven actor. It is necessarily stochastic; it produces text + tool calls + opinions. It has to be a separate identity — *not* code in the host-runner — because the deterministic/stochastic boundary is also the *audit* boundary: every claim about "what the agent did" is anchored on the boundary between the deputy that recorded the input and the LLM that produced the output.

The three concerns map to three layers. Each boundary is irreducible because collapsing it merges concerns that have to stay separate:

- **Collapse hub into host-runner** ("just put authority on each host"). Authority becomes per-machine. Two hosts disagree about policy; reconciling needs a coordination layer that *is* the hub. The collapse re-creates the hub at the storage layer with worse semantics.
- **Collapse host-runner into hub** ("just have agents talk to the hub directly"). Bytes have to flow through the hub (axiom A2 violation: a 5GB model weight cannot transit a $5 VPS). And: the hub becomes the supervisor for every host's processes, which means the hub has to know each host's filesystem layout, available memory, GPU topology — concerns local to a host. The collapse loses partition tolerance: a network blip between hub and host kills the agent's pane.
- **Collapse host-runner into agent** ("the agent supervises itself"). The agent is stochastic; it cannot reliably enforce its own policies. Bounded policy-enforcer becoming the bounded thing is a contradiction. Practically: zombie processes accumulate, runaway agents have no kill switch.
- **Collapse agent into host-runner** ("just embed the LLM in the deputy"). The audit boundary is gone. Every claim about "what the agent did" now goes through the same code path as "what the deputy enforced," and there's no reliable way to separate the two for review. Termipod's principal-direction model depends on the audit boundary holding.

So the layers are forced by the concerns, not chosen for elegance. New contributors who see the three layers and ask "why not fewer" should walk this list before proposing a collapse.

The **steward / worker layer split** within the agent layer (§3.3) is
similarly irreducible. Collapsing them — letting one agent be both
manager and IC for sustained work — produces a "general-purpose
steward" that answers questions, edits files, runs tests, AND
arbitrates approvals. That's one agent doing three layers' work
badly: token budget bloats with code context, decision audit drowns
in tool noise, the principal can't tell governance signal from
execution signal. Single-engine clients (Happy, CCUI) collapse the
two because they have one role per app; our positioning depends on
keeping them separate. See `agent-lifecycle.md` §4.9 for the rule and
§6.2.1 for the retreat triggers that force handoff back to a worker
once bootstrap mode ends.

---

## 4. Data ownership law

> **The hub stores names, policies, events, and references. Matter stays
> where it was produced.**

Concrete allocation:

**Hub** (authoritative, small, bounded, backed up):
- identities (agents, hosts, users, tokens)
- relationships (spawn edges, project membership, ownership)
- policies (tiers, approvers, budgets, overrides)
- event log (audit, attention queue)
- projects, runs (metadata only), documents (small text only)
- review state
- channel messages under a size threshold (~256 KB)
- references: artifact URIs, metric endpoint URIs, SHA-256s

**Host** (where compute and data live):
- checkpoints, datasets, tensors, figures, raw experiment logs
- pane buffers, tmux session state
- metrics time-series (via trackio SQLite)
- git worktrees, local repos
- local secrets, per-host config

**Cloud / Hugging Face Spaces / S3**:
- published artifacts, shared datasets, papers
- offsite backups of host trackio data

Rule of thumb: when in doubt, store a reference. If a proposed endpoint would
have the hub holding more than ~256 KB of content for a single primitive,
split into a small metadata row + an artifact URI pointing at the host.

---

## 5. Protocol layering

> **Moved.** Protocol layering — the relationship-type taxonomy, the
> seven-edge matrix, ACP scope and driving modes (M1/M2/M4), the
> agent-→-hub relay principle, A2A topology, AG-UI as the broker's
> output wire — now lives in
> [`protocols.md`](protocols.md). This stub stays so older
> cross-references like "blueprint.md §5.3.1" resolve to a forward
> pointer.

Read [`protocols.md`](protocols.md) directly when designing or
reviewing anything that touches an inter-component edge.

---

## 6. Core primitives

> **Moved.** The conceptual data model — Projects, Plans, Schedules,
> Agents, Runs, Artifacts, Documents, Reviews, Channels, Briefings,
> Attention, the primitives-by-axis index — now lives in
> [`../reference/data-model.md`](../reference/data-model.md). The
> *physical* schema (tables, columns, indexes) lives in
> [`../reference/database-schema.md`](../reference/database-schema.md).

This stub stays so older cross-references like "blueprint.md §6.5"
resolve to a forward pointer.

---

## 7. Forbidden patterns

> **Moved.** The 15 corollary rules that follow from the axioms +
> data-ownership law are in
> [`forbidden-patterns.md`](forbidden-patterns.md). Mobile-IA-specific
> forbidden patterns remain in
> [`information-architecture.md §8`](information-architecture.md).

This stub stays so older cross-references like "blueprint.md §7"
resolve to a forward pointer.

---

## 8. Reference architecture

The full C4 view (Level 1 system context + Level 2 containers + per-
container summary + tech stack + deployment topology) lives in
[`../reference/architecture-overview.md`](../reference/architecture-overview.md).
That doc is the cold-start onboarding read; this section forwards to
it so older cross-references still resolve.

---

## 9. Roadmap

Phased PR plan. Each phase is independently demoable; phase 2 completes
the research MVP pitch.

**Status as of v1.0.314.** Phases P0–P3 shipped; P4 backend is
feature-complete. The remaining demo work is reliability hardening
from device walkthroughs and the actual hardware run of Candidate A.
Per-bullet status below; current Now/Next/Later view is in
[`../roadmap.md`](../roadmap.md).

### Phase 0 — primitives (hub schema) ✅ shipped

- P0.1 ✅ `projects` evolution migration: add `goal`, `kind` (goal|standing),
  `parent_project_id`, `template_id`, `parameters_json`, `is_template`,
  `budget_cents`, `policy_overrides_json`, `steward_agent_id`,
  `on_create_template_id`. Retire separate directive concept.
- P0.2 ✅ `plans` + `plan_steps` tables + MCP tools `plan.instantiate`,
  `plan.advance`, `plan.step.complete`, `plan.get`.
- P0.3 ✅ `schedules` table (generalizes and replaces `agent_schedules`):
  `trigger_kind ∈ {cron, manual, on_create}`; scheduler refactored to
  instantiate plans instead of spawning agents. Migration ports any
  existing `agent_schedules` rows to synthetic single-step templates.
- P0.4 ✅ `runs` table + MCP tools `run.register`, `run.complete`,
  `run.attach_metric_uri`, `run.attach_artifact`.
- P0.5 ✅ `documents` + `reviews` tables + MCP tools.
- P0.6 ✅ `hosts.ssh_hint_json` + `hosts.capabilities_json` columns.

### Phase 1 — structured wire (protocols) ✅ shipped

- P1.1 ✅ Host-runner multi-mode agent driver (see [`protocols.md` §5](protocols.md)): **M1 ACP shim**,
  **M2 structured-stdio shim** (per-agent, starting with Claude Code
  `stream-json`), **M4 per-engine local-stream tap** (amended by
  [ADR-027](../decisions/027-local-log-tail-driver.md): JSONL-tail
  adapter for claude-code; legacy pane-PTY for the rest until their
  adapters ship). Unified `agent_events` queue regardless of mode.
  Hooks side-channel optional.
- P1.2 ✅ Host-runner **plan-step executor** (deterministic phases):
  executes `llm_call`, `shell`, `mcp_call`, `human_decision` steps
  without spawning a supervised agent. `agent_spawn` steps delegate to
  P1.1.
- P1.3 ✅ Host-runner **capability probe** (binary presence + version) on
  boot and heartbeat; reports to `hosts.capabilities_json`.
- P1.4 ✅ Hub **mode resolver**: given template mode + spawn override +
  host capabilities + declared billing → concrete mode, or fail-fast
  with reason.
- P1.5 ✅ Hub MCP server exposing authority capabilities (projects, plans,
  runs, documents, reviews, policy, audit read). Consolidated to a
  single service in v1.0.298 — see [ADR-002](../decisions/002-mcp-consolidation.md).
- P1.6 ✅ Host-runner MCP gateway with `hub://` and `host://` mounts;
  credential injection on forward.
- P1.7 ✅ `agent_events` store + hub AG-UI broker + `GET /v1/teams/{team}/
  agents/{agent}/stream` SSE endpoint.
- P1.8 ✅ Structured input endpoint `POST /v1/teams/{team}/agents/{agent}/
  input`.

### Phase 2 — app UI ✅ shipped

- P2.1 ✅ `AgentFeed` widget (AG-UI card renderer); card library for each
  event type. Move pane view to "Raw" tab; add mode badge on agent card.
- P2.2 ✅ Structured input wire-up: approve / reject / redirect / cancel as
  AG-UI input events.
- P2.3 ✅ Custom event renderers: run sparkline card, artifact thumbnail
  card, document review card.
- P2.4 ✅ **Plan viewer screen**: phases, step status, outputs, audit trail
  of runs of that plan.
- P2.5 ✅ **Workflows tab** (UI-only aggregation over `schedules` +
  templates + history); "Run now" for manual trigger.
- P2.6 ✅ Director home screen ("Triage"): pending reviews, stuck agents,
  overnight briefings. Top-level tab, aggregates across projects.
- P2.7 ✅ Project template picker + instantiation flow; `kind`-aware
  (goal projects show progress, standing projects show recent runs).
- P2.8 ✅ **Enter-pane action**: phone `hub_host_bindings` local table +
  SSH-hint-pre-filled Connection form + `tmux attach` navigation.

### Phase 3 — integrations ✅ shipped

- P3.1 ✅ Host-runner reads the run's metrics over a wandb/trackio-
  compatible HTTP endpoint. For MVP, trackio is assumed installed and
  self-hosted on the host (operator installs `pip install trackio` and
  runs it as a local service); the host-runner does not implement a
  native Go endpoint — it only consumes the existing HTTP contract.
  Verify `import trackio as wandb` in user code works end-to-end.
- P3.2 ✅ A2A server on host-runner; publish agent-cards to hub directory.
- P3.3 ✅ Hub A2A directory + reverse-tunnel relay. Required because GPU
  hosts are typically NAT'd — see [ADR-003](../decisions/003-a2a-relay-required.md).
- P3.4 ✅ Cross-host A2A smoke test: two host-runners under the same hub
  route an A2A task through the hub's directory/relay (agent on host A
  invokes a capability exposed by an agent on host B). This exercises
  P3.2 + P3.3 on the realistic MVP deployment (one hub, many hosts).
  Cross-hub federation (multiple hubs exchanging A2A tasks) is out of
  MVP scope.

### Phase 4 — research demo 🟡 backend feature-complete; hardware run pending

Locked to Candidate A (nanoGPT-Shakespeare optimizer × size sweep) per
[ADR-001](../decisions/001-locked-candidate-a.md). Detail tracker:
[`../plans/research-demo-gaps.md`](../plans/research-demo-gaps.md).

- P4.1 ✅ Built-in project templates: `research.v1` (5-phase research
  lifecycle) is the canonical demo template; `reproduce-paper` and
  `write-memo` ship as supporting examples. The single-phase
  `ablation-sweep` + `benchmark-comparison` templates were retired
  in v1.0.507 — `research.v1`'s experiment phase encodes the N-run
  sweep shape natively (one `experiment-results` deliverable with
  per-run runs + aggregate metric-chart). See
  [`../plans/multi-run-experiment-phase.md`](../plans/multi-run-experiment-phase.md).
- P4.2 ✅ Steward decomposition recipe (`hub/templates/prompts/steward.research.v1.md`)
  with the SOTA orchestrator-worker pattern from [ADR-008](../decisions/008-orchestrator-worker-slice.md).
- P4.3 🟡 End-to-end demo: backend complete; dress-rehearsal harness shipped
  (`seed-demo --shape lifecycle` + optional mock-trainer, no GPU needed).
  Hardware run of Candidate A remaining — gated on two consecutive
  walkthrough-clean device tests.

---

## 10. Non-goals

Explicit rejections to keep scope coherent.

- **Competing with Claude Code or Codex on single-agent UX.** They are the
  agents; we are the control plane. Our job is to make them work better
  *together* and *under governance*, not to build a better agent loop.
- **Competing with W&B on plotting breadth.** Trackio provides the
  experiment-tracking layer; we mount it. We don't build a new metrics DB.
- **Supporting general chat-style LLM use.** termipod is for bounded agent
  *work* with provenance, not for open-ended conversation.
- **IDE integration.** The IDE is the agent's concern, not the director's.
  The phone is the director's surface.
- **Hosting inference or embedding models.** Agents bring their own
  providers; termipod never proxies model calls.
- **Being a solo-developer's local tool.** Termipod's distinguishing
  features (governance, audit, multi-host, multi-vendor) have cost; users
  who don't need them should use Claude Code Remote or a single-host
  orchestrator instead.

---

## 11. Amendment process

This document is authoritative. PRs that contradict it require one of:

1. An amendment commit to this file in the same PR, with rationale.
2. An ADR (architecture decision record) in `docs/adr/` referenced from a
   new subsection here.

Drift is not allowed. If reality diverges, the document changes to match
the new reality and the change is reviewed explicitly.
