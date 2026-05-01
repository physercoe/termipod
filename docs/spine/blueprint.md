# termipod blueprint

> **Type:** axiom
> **Status:** Current (2026-04-30)
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** Authoritative reference for termipod's design philosophy,
component ontology, protocol layering, and primitive schema. Future
PRs should trace their design choices back to this document;
proposals that contradict the axioms or the data-ownership law
require an explicit amendment here, not a silent deviation.

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

Every inter-component edge is characterized by its relationship type. The
protocol is forced by the type, not chosen by fashion.

Relationship types:
- **C (control):** neither side is agent-shaped; imperative commands + status.
- **S (supervision):** one side owns the other's process lifetime; structured
  observation and steering of a subprocess.
- **R (RPC-with-capability):** callee exposes typed named capabilities;
  caller invokes them. Callee is a tool, not a reasoner.
- **P (peer):** both sides are autonomous reasoners coordinating via tasks
  and artifacts.
- **O (observation):** one side streams semantic events, the other renders.

### 5.1 Edge matrix

| Edge | Type | Protocol | Transport |
|---|---|---|---|
| Human ↔ Hub | C | termipod REST + SSE | HTTPS |
| Human ↔ Agent (live view) | O | **AG-UI** (hub-brokered) | SSE over HTTPS |
| Hub ↔ Host-runner | C | termipod REST + SSE | HTTPS, host-initiated |
| Host-runner ↔ Agent (local) | S | **ACP (Zed's Agent Client Protocol)** | JSON-RPC over stdio |
| Agent ↔ Hub (authority caps) | R | **MCP**, relayed via host-runner | UDS / localhost to host-runner, then relayed |
| Agent ↔ Host-runner (local caps) | R | **MCP** | UDS / localhost |
| Agent ↔ Agent (any host) | P | **A2A** | HTTPS direct, or hub reverse-tunnel relay |

### 5.2 The relay principle for agent → hub

Agents never open direct network connections to the hub. The host-runner
runs a local MCP gateway exposing `hub://` capabilities; agent calls traverse:

```
agent (MCP client) → host-runner MCP gateway → hub REST
```

Host-runner stamps the agent's identity, enforces local budget and rate
limits, reuses its single persistent hub token.

Consequences:
- One hub token per host, not per agent.
- Agent credentials never exist; nothing to leak or rotate per spawn.
- Air-gapped compute nodes (only host-runner allowed egress) work by
  construction.
- Short hub outages degrade gracefully (host-runner buffers writes, serves
  cached reads).
- Audit stays per-agent — host-runner preserves identity in the forwarded call.

### 5.3 ACP scope

ACP operates only on the host-runner ↔ agent edge on the same machine. It
does not cross the network. It runs in parallel with tmux: ACP is the
*control* channel (structured events: lifecycle, text, tool-call, diff,
progress, pause-for-approval, cancel); tmux is the *display* channel
(human-readable TTY output).

For agents that don't speak ACP, host-runner falls back to pane scraping
and synthesizes minimal events (lifecycle markers + periodic text-message
events from pane captures). AG-UI fidelity is lower but the system still
works.

**Billing caveat for Claude Code via ACP.** Zed's ACP adapter for Claude
Code (`@agentclientprotocol/claude-agent-acp`) wraps the Claude *Agent
SDK*, not the Claude Code CLI. The Agent SDK officially supports only
`ANTHROPIC_API_KEY` billing; Pro/Max subscription billing is an open
upstream issue (anthropics/claude-agent-sdk-python#559). A community
workaround — `claude setup-token` then `CLAUDE_CODE_OAUTH_TOKEN=…` — has
worked intermittently and is not officially supported. Host-runner
therefore offers three driving modes plus a one-shot plan-step path
(§5.3.1, §6.2); ACP is not the only structured way to drive Claude Code.

### 5.3.1 Agent driving modes

A **driving mode** describes how host-runner wires the stdio of a
*persistent* agent — i.e. one that has its own identity, lifecycle, and
authority. Governance (MCP gateway, audit, budget) is identical across
modes; only the control channel differs.

There are three modes:

| # | Mode | Control channel | AG-UI fidelity | Claude Code subscription-compatible | Notes |
|---|---|---|---|---|---|
| M1 | **ACP** | JSON-RPC over stdio via ACP adapter | High (native) | No (API key only via Agent SDK) | Preferred for agents with native ACP (Gemini CLI, Codex, OpenCode, Cline, …). |
| M2 | **Structured stdio** | Agent-native JSON-line protocol (e.g. `claude --input-format stream-json --output-format stream-json`) | High (agent-equivalent to ACP) | Yes (drives the CLI binary directly) | Per-agent shim. Recommended default for Claude Code under Pro/Max. |
| M4 | **Manual / pane-only** | None — host-runner only observes tmux | Low (pane scrape → lifecycle + text events) | Yes | Explicit escape hatch: user types directly into the pane from the mobile app's terminal view. |

(M3 "headless one-shot" is not a mode; it's a `llm_call` step inside a
deterministic plan phase — see §6.2. A one-shot invocation lacks a
persistent session, so it doesn't meet the agent-shape threshold.)

**Mode M4 is a first-class mode, not just a fallback.** Derivation from
axioms:

- A1 (attention scarcity) is not violated — the user only enters the
  pane when the structured UI has failed them or when they want direct
  control. For the rest of the fleet, structured modes still hold.
- A2 (work bound to compute) is satisfied — the pane is the compute's
  native interface; typing into it is the most direct form of "spatial"
  work.
- A3 (authority) still holds — the agent's outbound calls still go
  through host-runner's MCP gateway, so policy and audit are unaffected.
  Host-runner captures pane output to the audit log whether the human or
  a structured adapter is driving input.

Use cases for M4: debugging an agent that has gone sideways in M1/M2,
real-time pairing, agents that lack any structured output mode, operator
preference.

**Hooks as a side channel (orthogonal to all modes).** Claude Code and
several other agents expose hooks (`pre_tool_use`, `post_tool_use`,
`session_start`, etc.) that shell out to user scripts. Host-runner
installs hooks that POST structured events to its local MCP gateway.
This yields high-fidelity tool-call and approval events even in M4, and
augments M1/M2 with events the structured protocol doesn't carry.
Hooks are an additive event source, not a mode.

### 5.3.2 Mode resolution and host capability discovery

**Mode declaration.** Every project template declares one `driving_mode`
(single value, M1|M2|M4) and an optional ordered `fallback_modes` list
(e.g. `[M4]` to degrade to pane-only on structured-protocol failure).
A spawn request may override both.

**Billing declaration.** The user declares the billing context per agent
family per host (e.g. "Claude Code uses subscription on host X, API key
on host Y"). Host-runner does **not** infer or probe billing. If a
declared mode is known to conflict with the declared billing (e.g.
Claude Code M1 under subscription, blocked by Agent SDK), spawn fails
fast with a clear error.

**Host capability discovery.** Host-runner probes *binary presence and
version only* and reports on heartbeat:

```json
{
  "agents": {
    "claude-code": { "installed": true, "version": "…",
                     "supports": ["M1","M2","M4"] },
    "gemini-cli":  { "installed": true, "supports": ["M1","M4"] },
    "codex":       { "installed": false }
  },
  "probed_at": "…"
}
```

Hub caches this per-host as `hosts.capabilities_json` with a staleness
TTL (default 5 minutes). Mode resolution at spawn time is:

```
resolve(template.mode, spawn_override, host.capabilities, user.billing_decl)
  → concrete_mode | fail_fast(reason)
```

### 5.3.3 Enter-pane and SSH binding

Every agent detail sheet exposes an **"Enter pane"** action that drops
the user into a full-screen tmux view of that agent's pane. In M1/M2
the pane is read-mostly (display channel); in M4 it is the control
channel. A mode badge on the agent card identifies which.

The plumbing bridges three facts:

1. Hub knows `agent.host_id` + pane coords (session, window, pane).
2. Hub's `hosts` table carries a non-secret `ssh_hint_json` (hostname,
   port, username, optional jump-host hint) — set during host
   registration. Secrets never live in the hub (data-ownership law).
3. The phone keeps a local `hub_host_bindings(hub_host_id → connection_id)`
   mapping to its own SSH Connection entries (which hold the actual
   credentials in flutter_secure_storage).

Enter-pane flow:
- Binding present → phone opens SSH to the bound Connection and issues
  `tmux attach -t <s> \; select-window -t <w> \; select-pane -t <p>`.
- Binding missing but `ssh_hint_json` present → phone opens the
  Connection form pre-filled from the hint; user supplies credentials;
  binding saved.
- Neither present → action disabled with an explanation.

Constraint: **host-runner runs as the SSH user.** This guarantees the
tmux socket host-runner wrote to is the same socket the human's SSH
session attaches to. Split-user deployments (host-runner as a service
account, humans as different users) are out of MVP scope; they require
a documented shared-socket path with group perms and more involved UX.

Multi-user teams (each member bringing their own SSH credentials) are
deferred; MVP assumes solo / same user.

### 5.4 A2A topology

The host-runner is the A2A terminus. Each host-runner exposes one A2A
endpoint per live agent, with an agent-card at
`/a2a/<agent-id>/.well-known/agent.json`. Host-runner publishes agent-cards
to the hub's A2A directory.

Transport rules:
- Direct host-runner ↔ host-runner when mutual reachability allows.
- Hub reverse-tunnel relay when NAT blocks direct (hub already holds a
  persistent connection from each host-runner).

A2A payloads are small (task JSON, artifact URIs). Bulk artifact bytes are
fetched by URI from host or cloud, never through A2A task bodies. The
data-ownership law holds.

A2A supports bilateral multi-turn discussion scoped to a task — the writer
and critic agents can exchange 4 clarifying turns inside a single "review"
task, with provenance preserved. Multilateral group chat is not A2A's
concern; use hub channels via MCP post.

**A2A observability.** A2A is agent-to-agent wire, but humans still need to
watch it. When an agent invokes or responds over A2A, the host-runner emits
the call and result as events into the **calling agent's AG-UI stream** —
event kinds `a2a.invoke` (outbound task with target agent-card, capability,
task summary) and `a2a.response` (inbound result / error / turn). The
events join the same SSE feed the phone already renders for that agent, so
A2A activity appears inline in the agent's channel card alongside thoughts
and tool calls. No parallel observability channel, no extra subscription.
Bilateral multi-turn A2A discussions surface as a sequence of `a2a.invoke` /
`a2a.response` pairs on both agents' streams, keyed by the shared task id so
a future UI can collapse them into a threaded view.

### 5.5 AG-UI is the broker's output wire

The hub is the sole translator from internal protocols (ACP, A2A task
status, hub events) to AG-UI. The app only knows AG-UI. This keeps
client complexity bounded and lets us evolve internal wire formats without
breaking the phone.

AG-UI pause-for-approval is the wire format of termipod's existing
attention/approval system. The attention UI and AG-UI approval event
are the same thing — one internal model, one external standard.

---

## 6. Core primitives

### 6.1 Projects (subsume "directives")

A **project** is the unit of bounded work. Every running agent, run, task,
document, and artifact belongs to a project. Projects nest. Projects can be
templates (parameterized, reusable) or instances (bound, active).

Fields:
- `id, team_id, name, goal`
- `kind` ∈ {`goal`, `standing`} (default `goal`; see below)
- `parent_project_id` (nullable, enables nesting)
- `template_id` (nullable, references a parent template)
- `parameters_json` (bound values when instantiating a template)
- `is_template` (boolean)
- `budget_cents` (compute + API spend cap, inherited by children)
- `policy_overrides_json` (tier adjustments scoped to this project)
- `steward_agent_id` (the steward-of-record that decomposes the goal)
- `on_create_template_id` (nullable — a plan template auto-instantiated
  when the project is created; lets standing projects bootstrap channels,
  docs, and routine schedules on day one)
- `archived_at`

Templates emerge as a lab's methodology ("reproduce a paper", "ablation
sweep", "red-team an MLE model"). Instances inherit the decomposition plan
from their template, bind parameters, and start running.

**Project kinds.** Two kinds, distinguished at create time:

- `goal` — bounded, has a completion condition, closes. The default.
  Example: "reproduce the X paper," "ship feature Y." UI shows progress
  and a closable state.
- `standing` — ongoing container for routine work; never closes.
  Example: "Infra operations," "Daily briefings," "Lab triage." UI shows
  recent runs of its schedules rather than progress.

Kind affects UI presentation and default lifecycle, not the underlying
schema.

The previously proposed `directives` primitive is retired; it is subsumed
by `project.goal` + `project.template_id` + `project.parameters_json`.

### 6.2 Plans

A **plan** is an ordered, reviewable scaffold of phases that execute the
project's goal. Plans are how humans preview what a steward or the
system is about to do before it happens — A1 (attention) demands this.

A plan is **shallow by construction**: a linear list of named phases.
No loops, no conditionals, no DAGs at the plan level. Dynamic behavior
lives *inside* `agent_driven` phases, where a steward interprets the
phase goal with flexibility bounded by budget and policy. This keeps
plans reviewable (a human can read them) and keeps stochasticity
confined to the layer designed for it.

Fields:
- `id, project_id, template_id` (nullable), `version`
- `spec_json` — ordered phases (see below)
- `status` ∈ {`draft`, `ready`, `running`, `completed`, `failed`, `cancelled`}
- `created_at, started_at, completed_at`

Phase schema (inside `spec_json`):
- `name, goal, budget_cents`
- `kind` ∈ {`deterministic`, `agent_driven`, `human_gated`}
- For `deterministic`: ordered `steps` list (step kinds below).
- For `agent_driven`: a `steward` block — template ref for the steward
  agent to spawn, plus its driving mode and budget envelope.
- For `human_gated`: a `prompt` + optional `choices`; blocks until a
  human acts.

**Step kinds (deterministic-phase steps only):**

| Kind | Purpose |
|---|---|
| `agent_spawn` | Spawn an M1/M2/M4 agent, wait for termination, capture artifacts. |
| `llm_call` | One-shot inference (e.g. `claude -p … --output-format stream-json`). No persistent agent. Captures text/artifact output. |
| `shell` | Run a shell command on the host (with policy gate). |
| `mcp_call` | Invoke one named MCP tool and capture the result. |
| `human_decision` | Block for an explicit user approval/choice (equivalent to a one-step `human_gated` phase). |

Plan executor table `plan_steps`:
- `id, plan_id, phase_idx, step_idx, kind, spec_json`
- `status, started_at, completed_at`
- `input_refs_json, output_refs_json` (artifact URIs, document IDs, etc.)
- `agent_id` (nullable — set for `agent_spawn` steps)

`deterministic` phases are executed by host-runner's plan-step executor;
`agent_driven` phases are executed by the spawned steward (host-runner
spawns it, waits, reaps). `human_gated` phases are mediated by the hub.

The in-app term for a reusable plan template is **workflow**; it's a UI
label, not a schema primitive. A "workflow" is shorthand for
`template_id → plan_spec + schedule + execution history`.

### 6.3 Schedules

A **schedule** triggers a plan from a template. It generalizes and
replaces the earlier `agent_schedules` table, which spawned agents
directly — a now-forbidden shortcut (§7).

Fields:
- `id, project_id, template_id`
- `trigger_kind` ∈ {`cron`, `manual`, `on_create`}
- `cron_expr` (for `cron`)
- `parameters_json` (bound values passed to the template at instantiation)
- `enabled, next_run_at, last_run_at, last_plan_id, created_at`

MVP trigger kinds:
- `cron` — time-based (the 80% case: nightly benchmarks, daily briefings).
- `manual` — the user taps "Run now"; same code path as cron.
- `on_create` — fires once when the owning project is created; lets a
  template bootstrap a standing project.

Deferred (explicitly named but not built): event-triggered schedules
(on artifact produced, on agent completed across projects) and
conditional schedules (run only if precondition met). These require a
cross-plan event bus that MVP doesn't need.

Briefings reduce to a scheduled plan whose template is a
briefing-template (steward agent → digest document → push); no
dedicated briefings table post-migration (§6.10 historical note).

### 6.4 Agents

LLM processes with identity, lifecycle (spawned → running → paused →
terminated → archived), host assignment, project membership, spawn
authority with budget caps.

Existing schema. No change required except adding `project_id` foreign key
if not present, and ensuring `archived_at` aligns with project archival.
A new nullable `plan_step_id` links agent-spawn steps back to their
owning plan-step (populated for agents spawned via a plan).

### 6.5 Runs

Unit of single execution with reproducibility contract. Frozen config at
start; metrics time-series stored on the host via trackio and referenced
by URI on the hub.

Fields:
- `id, project_id, agent_id, config_json, seed, status`
- `started_at, finished_at`
- `trackio_host_id, trackio_run_uri` (reference, not content)
- `parent_run_id` (for sweeps)

Metrics never live on the hub. The phone fetches them from the host's
trackio via a hub-signed URL.

### 6.6 Artifacts

References to produced content. Hub stores metadata; bytes stay on host
or in cloud.

Fields:
- `id, project_id, run_id` (nullable)
- `sha256, size, uri` (host://, s3://, hf://, ...)
- `mime, producer_agent_id, created_at`
- `lineage_json` (which run/agent produced it, from which inputs)

### 6.7 Documents

Structured writeups: memos, drafts, reports, reviews. Versioned per project.
Small text stored inline; large documents stored as artifacts with a
metadata row.

Fields:
- `id, project_id, kind (memo|draft|report|review), title`
- `version, prev_version_id, content_inline` (if small)
- `artifact_id` (if large)
- `author_agent_id, created_at`

### 6.8 Reviews

Human-review queue. A review attaches to a document or artifact and has
states: pending → approved | request-changes | rejected. Visible to the
requesting agent so it can proceed or iterate.

Fields:
- `id, project_id, target_kind (document|artifact), target_id`
- `requester_agent_id, state, decided_by_user_id, decided_at, comment`

### 6.9 Channels (existing)

Ambient message streams at team or project scope. Agents post via MCP for
broadcast, group coordination, and ambient state updates that don't fit
A2A's bilateral task shape.

### 6.10 Briefings (specialization, not a primitive)

A briefing is a scheduled plan whose template's single `agent_driven`
phase spawns a briefing steward that reads recent activity and emits a
digest document + push. No dedicated `briefings` table is needed once
plans and schedules exist; a briefing is fully described by
`schedules.template_id` + the resulting plan + its output document.

### 6.11 Attention / approvals (existing)

Retained. AG-UI's pause-for-approval event type is the wire format for
these. No separate primitive needed.

### 6.12 Primitives by axis (mental-model index)

The §6 primitives factor onto orthogonal axes. Placing them on one table
removes the "is X like Y?" confusion that arises when the list reads as a
flat enumeration:

| Axis | Primitives | What the axis models |
|---|---|---|
| **Trigger** | Schedule | When work starts |
| **Procedure** | Plan, Plan step, Phase (JSON-embedded) | What will run, in what order — the reviewable recipe |
| **Execution** | Agent, Run | Living actor · ML experiment record |
| **Output** | Artifact, Document | Bytes produced · authored text |
| **Gate** | Review, Attention item | Human decisions blocking progress |
| **Work-tracking** | Task, Milestone | Kanban for work that isn't plan-driven |
| **Context** | Project, Channel, Event, Host | Container · conversation · message · machine |

**Two-lane model for human work.** Plan-driven human gates use
`plan_step(kind=human_decision)` → `attention_item` (plan pauses, director
acts on Me tab, plan resumes). Director-authored work (refactor a
trainer, triage a paper) uses `task` — independent of plans, kanban
lifecycle. Both are legitimate; they address different intents.
`task.plan_step_id` is deliberately absent — tasks are not plan outputs.

**Phase is not a primitive.** Phases exist only as JSON objects inside
`plans.spec_json` — they're typed containers (`deterministic` /
`agent_driven` / `human_gated`) that group steps and carry a budget
envelope, but have no independent state. A phase's status is derived
from its steps' statuses. `plan_steps.phase_idx` is the only place a
phase is materialised, and only for deterministic phases (the other two
kinds have no per-step rows — they produce one agent or one human
decision each).

**"Run" is a domain primitive, not a workflow-execution primitive.** It
models a single ML training/eval with frozen config + seed + trackio
metrics. The word clashes with "workflow run" in other systems, where a
Plan's execution would be called a "run". To avoid this ambiguity the
UI labels `runs` as **Experiments**; the DB name stays `runs` for
migration continuity.

### 6.13 Deferred: reusable step registry (action templates)

Plan steps today are specified inline in `plans.spec_json`. Reuse
happens at plan-template granularity (whole plan) but not at step
granularity. GitHub Actions / Airflow Operators / Temporal Activities
solve this with a step-level registry of named, parameterised
operations.

Termipod intentionally defers this. It adds real expressiveness
(marketplace-style sharing of `llm_call summarise-paper`, `shell
run-pytest`, etc.) but also real governance load (who publishes them,
how they're audited, how breaking changes roll forward). Plan templates
cover the demo and near-term goals.

When added, the shape is:

- `action_templates` table (`id, kind, spec_json, owner, version`).
- Plan step spec can reference `action_template_id` + `parameters_json`
  instead of inline `spec_json`.
- Action templates are team-scoped primitives like plan templates.

No schema break required — `plan_steps.spec_json` can continue to carry
inline specs for one-offs. Marked F-TBD in the roadmap.

---

## 7. Forbidden patterns

Corollaries of the axioms and the data-ownership law. Violating any of
these signals a design regression; a PR that does so requires explicit
amendment of this document first.

1. **Hub stores bulk bytes.** Violates A2 + data-ownership law.
2. **Host-runner runs an LLM loop or makes stochastic decisions.** Violates
   A3 (erases the deterministic boundary).
3. **Agents open direct network connections to the hub.** Violates
   containment; breaks air-gapped operation; multiplies token surface.
4. **Policy lives on hosts and drifts from hub.** Violates A3.
5. **Agents coordinate via shared files or undocumented channels outside
   A2A + hub channels.** Destroys provenance. *Exception under design
   (`../discussions/agent-fleet.md` §5):* a squad's shared scratchpad lives in
   the existing `documents` table with audit semantics, so it stays on
   the audit trail. The forbidden case is the *unaudited* shared file,
   not "shared state per se." When squads land, this rule reads "no
   shared state outside A2A, hub channels, OR squad-scoped documents."
6. **App parses ANSI from the pane as the primary agent view.** Fights
   AG-UI; the default surface must be typed events, not raw bytes.
7. **New REST endpoint on hub that agents will call directly.** Hub
   capabilities consumed by agents must be MCP tools, accessed via
   host-runner relay.
8. **`directives` reintroduced as a separate primitive.** Already unified
   under projects; forking will fragment queries and audit.
9. **Metrics written to hub.** Metrics live on host via trackio; hub
   holds only the run's trackio URI.
10. **A2A bypassed for cross-host agent delegation.** Invents a worse
    agent-card and task model.
11. **Schedules spawning agents directly.** Schedules must instantiate
    a plan from a template. Direct `agent_schedule → spawn` bypasses
    the reviewable plan scaffold and loses routine-execution history.

    *Why this rule exists:* a schedule is a recurring promise to run
    *something*; the question is what. Letting cron call `agents.spawn`
    treats every recurrence as a fresh atomic action with no prior
    structure — no plan to review, no record of "this is the third
    weekly briefing run," no way for the principal to ratify the
    *category* of work versus a one-off. Instantiating a plan from a
    template gives every recurrence a structured scaffold (phases,
    `human_gated` boundaries, audit lineage), keeps the principal's
    review surface uniform across one-shots and recurrences, and lets
    the user see "this Monday's run" alongside "last Monday's run" as
    sibling plan executions instead of unrelated agent rows. The audit
    feed (`reference/audit-events.md`) records `schedule.run` →
    `plan.create` rather than `schedule.run` → `agent.spawn` for the
    same reason: the plan is the unit the principal cares about, not
    the agent that happens to execute it.
12. **One-shot LLM calls modeled as agents (`M3` as a "mode").** An
    invocation without a persistent session is a `llm_call` plan step,
    not an agent. Forcing it into `agents` pollutes lifecycle queries
    and audit.
13. **Plans containing loops, conditionals, or DAGs at the plan level.**
    Dynamic behavior belongs inside `agent_driven` phases where a
    steward decides, bounded by budget and policy. Plans stay shallow
    and reviewable.
14. **Host-runner inferring or probing billing context.** The user
    declares billing per agent-family per host. Host-runner probes
    binary presence and version only. Mixing the two reintroduces
    provider-specific logic into the deputy layer.
15. **Hub storing SSH credentials to help with Enter-pane.** Only
    non-secret `ssh_hint_json` (hostname, port, username) may live in
    the hub. Secrets stay in the phone's secure storage.

---

## 8. Reference architecture

```
  ┌────────────────────┐
  │     Mobile App     │
  └─────┬──────────▲───┘
        │          │
        │          │ AG-UI (SSE, JSON events)
        │ REST+SSE │ hub brokers from ACP/A2A
        ▼          │
  ┌────────────────────┐                ┌────────────────────┐
  │        Hub          │◀── MCP ──────│  (any MCP agent,   │
  │                    │  via host-    │   incl. external)  │
  │  name service      │  runner relay └────────────────────┘
  │  policy engine     │
  │  event log         │
  │  A2A directory     │
  │  AG-UI broker      │
  │  artifact refs     │
  └─────┬──────────▲───┘
        │          │ (heartbeat, event post, long-poll)
        │ REST+SSE │
        ▼          │
  ┌────────────────────┐                    A2A
  │    Host-runner     │◀──── direct or ────▶ (peer host-
  │                    │      hub-relayed      runners)
  │  ACP per agent     │
  │  MCP gateway       │
  │    hub://  host:// │
  │  A2A server        │
  │  trackio server    │
  │  pane/worktree mgr │
  └─────┬──────────────┘
        │
        │ ACP (stdio) + MCP (UDS) + tmux (TTY)
        ▼
  ┌────────────────────┐
  │       Agent        │
  │  (Claude Code /    │
  │   Codex / custom)  │
  └────────────────────┘
```

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

- P1.1 ✅ Host-runner multi-mode agent driver (see §5.3.1): **M1 ACP shim**,
  **M2 structured-stdio shim** (per-agent, starting with Claude Code
  `stream-json`), **M4 manual/pane-only**. Unified `agent_events` queue
  regardless of mode. Hooks side-channel optional.
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

- P4.1 ✅ Built-in project templates: "ablation-sweep" shipped (`steward.research`,
  `ml-worker`, `briefing` templates). "reproduce paper" / "write memo" /
  "benchmark comparison" deferred — Candidate A only needs ablation-sweep.
- P4.2 ✅ Steward decomposition recipe (`hub/templates/prompts/steward.research.v1.md`)
  with the SOTA orchestrator-worker pattern from [ADR-008](../decisions/008-orchestrator-worker-slice.md).
- P4.3 🟡 End-to-end demo: backend complete; dress-rehearsal harness shipped
  (seed-demo + mock-trainer, no GPU needed). Hardware run of Candidate A
  remaining — gated on two consecutive walkthrough-clean device tests.

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
