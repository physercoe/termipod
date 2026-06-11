# Multi-agent development collaboration — the design space

> **Type:** discussion
> **Status:** Open (2026-06-11) — companion research note to
> [ADR-049](../decisions/049-multi-agent-collaboration-via-github.md). The ADR
> records *the decision* (coordinate this repo's development through GitHub);
> this doc surveys the *design space* that decision sits in, so the choice is
> legible and revisitable.
> **Audience:** contributors · maintainers · builder agents
> **Last verified vs code:** v1.0.817
> **Freshness:** snapshot (refresh when a coordination substrate, protocol, or
> run mode materially shifts — e.g. a protocol merges, a new harness crosses
> ~10k stars, or our own contention model changes)

**TL;DR.** We delegate this repo's *development* across heterogeneous AI coding
agents, coordinated only through GitHub ([ADR-049](../decisions/049-multi-agent-collaboration-via-github.md)).
That is one point in a large, fast-moving design space. This note maps the
space along five axes — **(1) when multi-agent is worth it at all**, **(2) the
SOTA collaboration patterns**, **(3) the coordination substrate** (GitHub is
one of ~six), **(4) the inter-agent protocols**, and **(5) how an agent stays
running** (poller / TUI / daemon / cloud / app) — and says, for each, what it
costs, when it fits, and why TermiPod's dev workflow landed where it did. The
through-line: for **write-heavy** work (editing one shared codebase), the
expensive thing is not parallelism, it is **keeping the implicit decisions in
edits from diverging** — so the design centres on a durable shared substrate, a
single merge authority, and serialization of hot resources, not on clever
agent-to-agent chatter.

> **Scope.** This is about coordinating *the people-and-agents who build
> TermiPod*. It is **not** about TermiPod's product runtime, which is itself a
> multi-agent coordinator (hub / host-runner / A2A). The two rhyme and this doc
> cross-links them, but they are different problems with different substrates:
> the product coordinates *agents doing the director's work* over the hub; this
> coordinates *agents doing our work* over GitHub. Keep them distinct — see
> [`multi-agent-sota-gap.md`](multi-agent-sota-gap.md) and
> [`orchestration-contract.md`](orchestration-contract.md) for the product side.

---

## 1. Why — and when — multi-agent at all?

The first question is not "which pattern" but "should there be more than one
agent." In 2026 this is an open, well-argued debate with two named poles.

### The two poles

- **Cognition (Devin) — "Don't Build Multi-Agents."** Their position: naïve
  multi-agent setups fail because **sub-agents don't share each other's
  context**, and because **every action carries an implicit decision** (an edit
  encodes judgments on style, naming, edge cases). When two agents write in
  parallel from premises that were never reconciled, those judgments split and
  the merged result is fragile. Their two load-bearing principles: *share
  context as fully as possible*, and treat actions as decisions. The practical
  takeaway is the **single-writer / single-threaded-with-context** default, and
  a *splitting → parallel-execution → integration* shape only when you must.
- **Anthropic — "How we built our multi-agent research system."** Their
  position: an **orchestrator-worker** system with parallel sub-agents beat a
  single agent by ~90% on a breadth-first *research* task — at **~15× the token
  cost** of a chat turn. It wins where the work is **parallelizable and
  read-heavy** (fan out to explore many sources, stitch findings back), and it
  needs careful delegation prompts or sub-agents duplicate work (their canonical
  bug: three sub-agents redundantly investigating overlapping supply-chain
  questions).

### The reconciliation (and what it means for *coding*)

The two are not actually contradictory; they partition by task shape:

| | Read-heavy / breadth-first | Write-heavy / one artifact |
|---|---|---|
| **Examples** | research, codebase Q&A, audit, triage | editing a shared codebase to ship a change |
| **Parallelism** | embarrassingly parallel (each agent reads its own slice) | contended (every agent edits the same tree) |
| **Dominant cost** | tokens / breadth | **keeping implicit decisions coherent on merge** |
| **Verdict** | multi-agent pays (Anthropic) | single-writer-per-unit; coordinate the writes (Cognition) |

**Coding is mostly the right column.** Reading/exploring the repo fans out
cheaply; *writing* it does not, because merges re-collide all the implicit
decisions. So the useful form of "multi-agent coding" is **not** many agents
co-editing one change — it is **many agents each owning a disjoint unit of
write**, with a coordination layer that (a) keeps units disjoint, (b)
serializes the genuinely shared resources, and (c) puts a single authority on
integration. That is exactly the shape ADR-049 picks: one **maintainer** owns
decomposition + merge; **builders** each own one ticket on one branch; the
**ARB baton** serializes the one file every ticket touches.

**When to stay single-agent.** If the work is one coherent change whose parts
share a lot of context, a single agent with good **context engineering** (the
discipline Cognition elevates) is simpler, cheaper, and more consistent. Reach
for multiple agents only when the work **decomposes into units that barely
share context** *and* the per-unit cost (tokens, wall-clock, or rate limits) is
high enough that the coordination overhead pays for itself. For us the trigger
was concrete: the maintainer (Opus) is **rate-limited**, and mechanical work
(an i18n sweep across dozens of files) is exactly "many low-context units" — so
moving implementation to cheaper builders pays. See
[`intra-vs-inter-engine-delegation.md`](intra-vs-inter-engine-delegation.md)
for the sibling question *inside* the product: when a steward should fan out
in-engine vs spawn workers.

> **Rule of thumb.** Multi-agent is a **throughput-and-cost** lever, not a
> capability lever, for write-heavy work. It buys parallelism and lets you
> spend cheap tokens on mechanical units; it costs coordination, and it can
> *lose* on quality if the units aren't truly disjoint. Default to one agent;
> add agents when units are disjoint and per-unit cost is high.

---

## 2. The SOTA collaboration patterns

Five-or-so named patterns dominate 2026 production write-ups. They are not
mutually exclusive — real systems compose them — but each has a centre of
gravity. Below, each is described, then scored for **coding** specifically.

### 2.1 Single agent + context engineering (the baseline)

One agent, one long-running context, sub-tasks handled sequentially (often with
in-engine sub-agents that *return text*, not co-writers). The "#1 job" becomes
curating what's in the context window.

- **Pros:** maximal context sharing → maximal decision coherence; simplest to
  reason about and debug; cheapest for small/coherent work; no merge problem.
- **Cons:** no parallelism; bounded by one context window and one rate limit;
  long autonomous runs drift without compaction/memory discipline.
- **Fits:** a single coherent feature/fix; anything where the parts share heavy
  context; the default until proven otherwise.

### 2.2 Orchestrator–worker (manager / children / integrate)

A lead agent decomposes the goal, dispatches disjoint sub-tasks to workers,
then integrates results. Anthropic's research system and Cognition's
"splitting → parallel → integration" are both this. **~70% of production
multi-agent deployments** are some form of this pattern.

- **Pros:** clear ownership; parallel where units are disjoint; single
  integration point keeps decisions coherent; matches how humans run a team.
- **Cons:** orchestrator is a bottleneck and a single point of failure;
  delegation-prompt quality is load-bearing (bad task boundaries → duplicated
  work); integration is where write-conflicts surface.
- **Fits:** **the canonical coding pattern.** ADR-049 *is* an orchestrator-worker
  with a human-speed, durable orchestrator (the maintainer) and GitHub as the
  message bus. The product's own slice (ADR-008) is the same shape.

### 2.3 Pipeline (sequential stages)

Work flows through ordered stages, each a specialized agent (e.g.
plan → implement → test → review), output of one feeding the next.

- **Pros:** each stage is simple and independently improvable; natural fit for
  a build→verify→merge flow; easy to insert a gate between stages.
- **Cons:** latency is the sum of stages; a weak stage bottlenecks the whole;
  little parallelism within a unit.
- **Fits:** the *lifecycle of a single ticket* (claim → implement → CI → review
  → merge) is a pipeline; we run many such pipelines concurrently, one per
  builder.

### 2.4 Swarm / decentralized peer-to-peer

No central controller; agents coordinate by local rules and emergent behavior
(e.g. reported runs of hundreds of sub-agents over thousands of steps).

- **Pros:** scales past any single orchestrator's attention; resilient to
  individual failures; high parallelism.
- **Cons:** emergent = hard to predict, audit, and stop; coordination cost
  shows up as conflicting writes and wasted work; weakest on the
  "implicit-decision coherence" criterion.
- **Fits:** read-heavy exploration; *not* a fit for write-heavy shared-codebase
  work, where emergent co-editing is precisely what fragments the artifact.

### 2.5 Debate / verifier (review as a role)

One or more agents produce, another independently critiques or verifies before
the result is accepted. The "independent verifier" overlay.

- **Pros:** catches errors a producer is blind to; cheap quality lever; maps
  directly onto code review and CI.
- **Cons:** extra tokens/latency; a verifier no smarter than the producer adds
  little; can stall on disagreement.
- **Fits:** **already core to our model** — CI is a mechanical verifier, the
  maintainer is a judgment verifier, and `ticket:changes` is the
  bounce-back-for-revision edge. See
  [`coordination-basis-and-decision-classification.md`](coordination-basis-and-decision-classification.md)
  on the verifier overlay as a first-class coordination force.

### 2.6 Hierarchical / mesh (structural variants)

**Hierarchical** = orchestrator-worker nested into a tree (managers of
managers); fits very large org-shaped work, costs depth-of-delegation latency
and context loss across levels. **Mesh** = every agent can talk to every other
directly; maximal flexibility, but O(n²) communication and the hardest to audit
— rarely the right call for coding.

### Pattern picker (for coding)

| Pattern | Parallel? | Decision coherence | Auditable | Best for |
|---|---|---|---|---|
| Single + context-eng | No | **Highest** | High | one coherent change |
| **Orchestrator-worker** | Yes (disjoint units) | High (single integrate) | High | **delegated dev work** |
| Pipeline | Within stage: no | High | High | one ticket's lifecycle |
| Swarm | Yes (high) | **Lowest** | Low | read-heavy exploration |
| Debate / verifier | n/a (overlay) | Raises it | High | review + CI gate |
| Hierarchical / mesh | Yes | Drops with depth/edges | Low–med | very large orgs (rarely) |

---

## 3. The coordination substrate — GitHub is one of ~six

Whatever the pattern, agents need a **shared medium** to coordinate through:
where work is queued, who owns what, how handoffs and results are recorded.
This is the most consequential and least-discussed choice. Six families:

### 3.1 Issue tracker / VCS forge (GitHub — our choice)

Issues = queue, labels = state machine, branches/PRs = work units, CI = gate,
committed docs = the protocol itself. A clear 2026 trend names issue trackers
(GitHub, Jira, Linear) the "default substrate for enterprise AI automation"
because they already encode **persistent state, ownership, permissions, and
queryable history** — tickets as shared state, status transitions as handoffs,
comments as the inter-agent message bus.

- **Pros:** **durable + auditable by construction** (every action is a
  commit/comment/label — replayable, attributable); a channel every agent
  already reaches with no new infra; native gate (CI) and native review;
  permissioned; works across hosts with no shared filesystem.
- **Cons:** coordination latency is poll-bound (seconds, not millis); label
  index can lag right after a write; not designed for high-frequency chatter;
  one shared account blurs attribution unless you add the handle convention.
- **Fits:** **cross-host, heterogeneous-vendor, write-heavy** delegation where
  auditability and a durable trail matter more than millisecond coordination —
  i.e. exactly our case. ADR-049 §D-2.

### 3.2 Shared task file / blackboard (markdown, doc, or shared memory)

A single shared document (a `TASKS.md`, a Notion page, or a true blackboard
data structure) that all agents read/write; each claims a task, marks
in-progress, marks done. The "shared task document" pattern is widely
recommended for worktree fleets; the formal version is the **blackboard
architecture** (specialists communicate *indirectly* through shared state).

- **Pros:** dead simple; decouples agents (they needn't know each other); single
  source of truth prevents state divergence; trivially auditable if versioned.
- **Cons:** needs a control mechanism for *who acts when* (the blackboard's
  classic weakness); conflicting writes if not serialized; degrades with many
  agents; usually assumes a **shared filesystem** (single host).
- **Fits:** single-host worktree fleets; a lightweight intra-project todo. It is
  essentially "GitHub-lite without the forge" — we get its benefits *and*
  durability/permissions by using issues+labels instead.

### 3.3 Message / event bus (queue, pub-sub, Redis streams)

Agents publish/subscribe over a broker; work is a message, handoffs are events.

- **Pros:** low-latency, high-throughput, decoupled; natural backpressure and
  fan-out; mature ops tooling.
- **Cons:** **ephemeral by default** — you must add a store for the audit trail
  the forge gives free; another piece of infra to run and secure; ordering /
  exactly-once semantics are real work; overkill for human-speed dev cadence.
- **Fits:** high-frequency production agent meshes (customer support, doc
  pipelines), *not* a few builders shipping PRs. This is closer to what
  TermiPod's **product** hub does internally (events + A2A relay) — see
  [`orchestration-contract.md`](orchestration-contract.md).

### 3.4 Direct agent-to-agent messaging (mesh / A2A)

Agents address each other directly over a protocol (see §4).

- **Pros:** lowest-latency coordination; expressive; no central bottleneck.
- **Cons:** O(n²) edges; hardest to audit and to stop; couples agents to each
  other's availability; you re-implement queueing/state on top anyway.
- **Fits:** cross-organizational delegation where there's no shared forge; live
  negotiation. For us, rejected for dev coordination (ADR-049 alternatives):
  the hub/A2A is the *product*, not a dev tool.

### 3.5 Shared filesystem + worktree isolation

Not a message substrate but the **isolation** substrate that the others assume.
Each agent gets its own git worktree (own working dir, shared `.git`), so
file-level edits don't collide; coordination still rides on one of the above.
Worktrees became "load-bearing for AI coding" in Q1 2026 and are now native to
Claude Code, Codex, and Cursor; tools like Claude Squad, Conductor, and Vibe
Kanban wrap them.

- **Pros:** eliminates file-level conflict without full clones; cheap; native.
- **Cons:** **single-host** (shared `.git`); still needs a coordination layer on
  top to assign/claim/merge; doesn't solve the *semantic* merge problem.
- **Fits:** one beefy host running several agents. **Orthogonal to ADR-049** —
  our builders are on *different* hosts with no shared FS, so branches+PRs are
  the isolation unit; a single-host builder could still use worktrees locally
  under the same protocol.

### 3.6 In-process orchestrator framework (shared memory)

LangGraph / CrewAI / AutoGen / OpenAI Agents SDK: agents are objects in one
process sharing memory; the framework owns routing and state. Every major
framework now ships multi-agent primitives as first-class.

- **Pros:** richest context sharing (shared memory = Cognition's principle #1
  for free); fast; great for one-process orchestrator-worker.
- **Cons:** **single process, single host, usually single vendor**; not a
  cross-host or cross-vendor substrate; the agents must be the framework's
  agents.
- **Fits:** building *one* multi-agent application; **not** coordinating
  independent CLIs from different vendors across machines — which is the
  vendor-agnostic constraint ADR-049 (§D-9) is built around.

### Substrate picker

| Substrate | Durable/auditable | Cross-host | Cross-vendor | Latency | Best for |
|---|---|---|---|---|---|
| **Issue tracker / forge** | **Yes (native)** | **Yes** | **Yes** | sec | **delegated dev (ours)** |
| Shared task file / blackboard | If versioned | No (shared FS) | Yes | sub-sec | single-host todo |
| Message / event bus | No (add store) | Yes | Yes | ms | high-freq prod mesh |
| Direct A2A / mesh | No (add store) | Yes | Yes (w/ protocol) | ms | cross-org live deleg. |
| Worktree (isolation) | via git | No | Yes | n/a | single-host fleet |
| In-process framework | In-mem only | No | No | µs | one app, one host |

**Why GitHub wins for us:** it is the only row that is durable+auditable,
cross-host, *and* cross-vendor with **zero new infrastructure** — the exact
three constraints in ADR-049's Context. The forge gives for free what every
other substrate makes you build: a permissioned, replayable, attributable trail
plus a native gate (CI) and a native review surface.

---

## 4. The protocols

"Protocol" operates at two altitudes here, and it's worth separating them.

### 4.1 Wire protocols (industry, cross-agent)

The 2026 interop stack has converged toward a **two-layer reference model**:

- **MCP (Model Context Protocol, Anthropic)** — agent ↔ **tools/context**. The
  de-facto standard for giving an agent tools. TermiPod's hub already speaks
  MCP (`hub/internal/hubmcpserver`); engines connect over it.
- **A2A (Agent-to-Agent, Google)** — agent ↔ **agent** coordination/delegation,
  esp. across org boundaries. TermiPod's product uses an A2A relay through the
  hub.
- **ACP (Agent Communication Protocol, IBM/AGNTCY)** — a REST-native A2A
  alternative; **announced (Sept 2025) to merge with A2A** under the Linux
  Foundation, which now governs MCP, A2A, and ACP — the most important
  structural fact of the year (consolidation, not proliferation).
- **OASF / AGNTCY (Cisco-initiated)** — an Open Agent Schema Framework for
  **identity and discovery** ("which agent is this, what can it do").

Mental model: **MCP for tools, A2A/ACP for agent-to-agent, OASF for
identity/discovery.** Note these are for *runtime* agent meshes. (TermiPod also
tracks **ACPs/AIP**, a *different* China-standard with a colliding acronym —
see [`aip-acps-china-standard.md`](aip-acps-china-standard.md) — don't conflate.)

### 4.2 The coordination protocol (ours, application-level)

ADR-049's protocol is **not** a wire protocol — it is a **convention encoded in
GitHub primitives**, which is the point: the substrate *is* the protocol, so
there's nothing extra to run.

- **State machine** = labels: `ticket:ready → claimed → in-review → (changes) →
  merged`, plus `ticket:blocked`; capability tiers
  `tier:mechanical|medium|judgment`.
- **Identity** = two axes: *attribution* (a `git config` handle + `Co-Authored-By`,
  free, unlimited) vs *acting account* (the auth token; builders may share one).
  Claim source-of-truth = **claim comment handle + branch name**
  (`agent/<handle>/<N>-…`), never the GitHub assignee.
- **Mutual exclusion** = the **baton**: `holds:<resource>` serializes any hot
  file every ticket of a workload touches (`holds:arb` for `lib/l10n/*.arb`).
- **Gate** = verify-before-merge (CI green, re-read `gh pr checks`, never trust
  `--watch`) + maintainer-only merge.
- **Failure handling** = escalate-don't-guess (`ticket:blocked` + a specific
  comment), the natural kill-signal for an autonomous builder.

This is deliberately the **minimum protocol** that makes orchestrator-worker
safe over an issue tracker. It maps onto §4.1 only loosely: a builder *could* be
reached over A2A, but for dev coordination we'd be adding a wire protocol where
labels already suffice. The wire protocols matter for the **product**; the
**convention** matters for **dev**.

---

## 5. How an agent keeps running (the run mode)

The last axis: once an agent has a ticket, *what process model keeps it
working*? Five modes, increasingly autonomous, each a different point on the
control ↔ autonomy trade-off.

### 5.1 Human-in-the-loop interactive (the TUI)

A person drives the agent's interactive TUI, approving steps. Multi-agent TUIs
(Claude Squad — Go, tmux + worktrees, terminal-only; Conductor; Vibe Kanban)
let one human watch/steer several agents.

- **Pros:** maximal control and visibility; catch a wrong turn immediately;
  best for judgment work and debugging the *process*.
- **Cons:** human is the bottleneck; doesn't scale past a handful; no overnight
  progress.
- **Fits:** risky/novel tickets, first runs of a new builder, anything where
  you want to intervene. Our poller's **`--interactive` take-over mode** is
  exactly this: it seeds the ticket prompt into the runtime's TUI inside a tmux
  session and blocks, so the operator can `tmux attach`, watch, type, and let it
  finish.

### 5.2 Headless one-shot

Run the agent non-interactively on one prompt to completion (Claude Code
`--print`/headless; `codex exec`), then exit. The unit of the poller loop.

- **Pros:** scriptable; deterministic lifecycle; composes into a loop; no UI
  overhead.
- **Cons:** no mid-run steering; you only see the result; needs an
  auto-approve/sandbox-bypass posture to run unattended.
- **Fits:** one mechanical ticket. It's the inner call of §5.3.

### 5.3 Host-side poller / loop (our default)

A shell loop on the builder's host finds a `ticket:ready`, claims it, hands the
standing prompt to the agent (headless, §5.2), waits, repeats — **one-in-flight
by construction** (foreground), with the baton preventing collisions. The
general "poller → dispatcher → reconciler" shape recurs in 2026 orchestrators.
Ours is [`scripts/agent-poller.sh`](../../scripts/agent-poller.sh).

- **Pros:** runs unattended for hours; vendor-agnostic (the runtime is named
  only in the operator's `$AGENT_CMD`, never in the repo); cheap; the
  one-in-flight + tier-clearance + `ticket:blocked` give natural runaway
  containment; easy to add a supervised confirm or interval cadence.
- **Cons:** dies when its host/shell dies (not a managed service); coarse
  observability (logs, not a dashboard); doesn't yet auto-handle
  `ticket:changes` rounds (open follow-up); needs a trusted host because
  unattended edits + network require bypassing per-command sandboxes.
- **Fits:** **the autonomous builder.** ADR-049 §D-9 + the
  [how-to §11](../how-to/agent-collaboration.md).

### 5.4 Persistent local daemon

A long-lived background process on the dev machine that watches for triggers and
acts continuously — the rumored Claude Code "Chyros" codename describes exactly
this (always-on local daemon vs today's session-based CLI; runs in your real
environment, push-notifies on completion).

- **Pros:** always-on without a babysat loop; reacts to events (not just polls);
  lives in your real toolchain/credentials.
- **Cons:** a daemon is an ops surface (crash, restart, resource use);
  observability and kill-switch must be designed in; not yet a shipped, stable
  thing to depend on.
- **Fits:** a future evolution of the poller — event-driven instead of
  poll-driven — once the trigger surface and safety rails are worth it. Today
  the poll loop is simpler and sufficient at dev cadence.

### 5.5 Cloud / managed background agent

A hosted agent runs in a cloud (or self-hosted) sandbox, kicked off by an event
(an assigned issue, a comment), opening a PR when done — Claude **Managed
Agents** (cloud or self-hosted sandbox), GitHub-style "assign an issue to an
agent."

- **Pros:** no host to babysit; scales horizontally; isolated sandbox;
  integrates natively with the forge (which *is* our substrate).
- **Cons:** runs in a *replica* environment, not your machine (env drift); cost
  and vendor lock-in; less direct visibility; sandbox may lack the network/tool
  access an unattended builder needs.
- **Fits:** scaling builders past local hosts; the cleanest fit *because our
  substrate is already GitHub* — a managed agent that claims a `ticket:ready`
  and opens a PR drops into ADR-049 with **zero protocol change** (it's just
  another builder identity). A strong candidate when one shared builder host
  stops being enough.

### 5.6 App / control-plane (the director's cockpit)

A first-class application surface for spawning, watching, and steering a fleet —
which is literally **what TermiPod's product is** (the Flutter cockpit + hub).

- **Pros:** richest observability and control; mobile/remote; the product we're
  building.
- **Cons:** heaviest to build; it's the product runtime, not a dev tool —
  using it to coordinate *our own repo* is the rejected alternative in ADR-049.
- **Fits:** coordinating the *director's* agent fleet (the product). For *our
  dev*, GitHub + a poller is right-sized.

### Run-mode picker

| Mode | Autonomy | Observability | Ops weight | Best for |
|---|---|---|---|---|
| Interactive TUI | Low | **Highest** | Low | risky/novel tickets, steering |
| Headless one-shot | Med | Low | None | one mechanical ticket |
| **Poller loop (ours)** | High | Med (logs) | Low | **unattended builder** |
| Local daemon | High | Med | Med | event-driven future poller |
| Cloud / managed | High | Med–high | Low (hosted) | scaling past local hosts |
| App / control-plane | High | **Highest** | **Highest** | the product fleet |

---

## 6. What TermiPod's dev workflow chose, and why

Reading the five axes together, ADR-049 is a coherent point, not an arbitrary
one:

- **When (Axis 1):** write-heavy, low-context-per-unit, high per-unit cost
  (maintainer rate limits) → multi-agent pays, in the *disjoint-units* form.
- **Pattern (Axis 2):** **orchestrator-worker** (durable human-speed
  orchestrator = maintainer) with a **pipeline** per ticket and a **verifier**
  overlay (CI + review). No swarm, no mesh — they lose exactly the
  decision-coherence that write-heavy work needs.
- **Substrate (Axis 3):** **GitHub forge** — the only durable+auditable,
  cross-host, cross-vendor, zero-new-infra option.
- **Protocol (Axis 4):** an **application-level convention in labels + branches
  + a baton**, not a wire protocol — the substrate is the protocol. Wire
  protocols (MCP/A2A) stay where they belong: the product.
- **Run mode (Axis 5):** a **host-side poller** with an interactive take-over
  escape hatch, designed so a **cloud/managed builder** can slot in unchanged
  when we outgrow local hosts.

The single thread tying all five: for write-heavy work the scarce resource is
**coherence of the implicit decisions in edits**, so every choice favours a
durable shared record + a single integration authority + serialized hot
resources over fast-but-fragile agent-to-agent autonomy.

---

## 7. Open questions / when to revisit

- **`ticket:changes` autonomy.** The poller claims `ticket:ready` but doesn't
  yet re-engage a bounced PR. Full autonomy wants a builder that also services
  its own revision rounds. (Tracked as a poller follow-up.)
- **Event-driven vs poll.** If dev cadence rises, move §5.3 toward §5.4/§5.5
  (a daemon or managed agent reacting to issue/PR webhooks) — the substrate
  already emits the events.
- **Enforced gate.** Today maintainer-only merge is a *convention* under one
  shared account; a distinct builder account + branch protection makes it an
  *enforced* gate (ADR-049 follow-up) — do it when >1 builder runs in parallel.
- **Contention.** One ready-queue + one ARB baton suffices now. If a second hot
  resource or a second workload causes queue contention, add a
  `holds:<resource>` baton and/or a per-workload routing label before reaching
  for a richer substrate.
- **Protocol convergence.** If A2A/ACP consolidation produces a clean
  cross-vendor *delegation* standard, re-evaluate whether a builder should be
  reachable over it — but only if it buys something labels don't.

This doc **stays Open** as a living map; it resolves only if the design space
itself stabilizes. The *decision* it accompanies is settled in
[ADR-049](../decisions/049-multi-agent-collaboration-via-github.md).

---

## See also

- [ADR-049 — Multi-agent collaboration via GitHub](../decisions/049-multi-agent-collaboration-via-github.md) — the decision this surveys the space around.
- [How-to: Coordinate agents through GitHub](../how-to/agent-collaboration.md) — the operational protocol.
- [`multi-agent-sota-gap.md`](multi-agent-sota-gap.md) · [`multi-agent-harness-landscape.md`](multi-agent-harness-landscape.md) — the **product**-side multi-agent landscape.
- [`orchestration-contract.md`](orchestration-contract.md) · [`coordination-basis-and-decision-classification.md`](coordination-basis-and-decision-classification.md) — coordination as a typed contract / decision-classification basis.
- [`intra-vs-inter-engine-delegation.md`](intra-vs-inter-engine-delegation.md) — fan-out-in-engine vs spawn-a-worker, the product analogue of Axis 1.
- [`integrating-open-source-agents.md`](integrating-open-source-agents.md) — the vendor landscape a builder can be drawn from.

## Sources (web research, 2026-06-11)

Patterns & the debate:
- Anthropic — [How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
- Cognition — [Don't Build Multi-Agents](https://cognition.ai/blog/dont-build-multi-agents) · [Multi-Agents: What's Actually Working](https://cognition.ai/blog/multi-agents-working)
- LangChain — [How and when to build multi-agent systems](https://blog.langchain.com/how-and-when-to-build-multi-agent-systems/)
- [Multi-Agent Orchestration: 5 Patterns That Work in 2026](https://www.digitalapplied.com/blog/multi-agent-orchestration-5-patterns-that-work) · [Swarm vs Mesh vs Hierarchical](https://gurusup.com/blog/agent-orchestration-patterns)

Substrates:
- MindStudio — [Issue Trackers as AI Agent Infrastructure](https://www.mindstudio.ai/blog/issue-trackers-ai-agent-infrastructure-jira-linear) · [Git Worktrees for parallel AI coding](https://www.mindstudio.ai/blog/git-worktrees-parallel-ai-coding-agents)
- CallSphere — [Blackboard Architecture for Multi-Agent Systems](https://callsphere.ai/blog/blackboard-architecture-multi-agent-systems-shared-knowledge-spaces)
- [Best Git Worktree Tools for AI Coding 2026](https://nimbalyst.com/blog/best-git-worktree-tools-ai-coding-2026/)

Protocols:
- Zylos — [Agent Interoperability Protocols 2026: MCP, A2A, ACP convergence](https://zylos.ai/research/2026-03-26-agent-interoperability-protocols-mcp-a2a-acp-convergence/)
- [MCP vs A2A vs ACP: The 2026 Guide](https://optinampout.com/blogs/mcp-vs-a2a-vs-acp-agent-protocols-2026) · [AI Agent Protocol Ecosystem Map 2026](https://www.digitalapplied.com/blog/ai-agent-protocol-ecosystem-map-2026-mcp-a2a-acp-ucp)

Run modes:
- [Claude Managed Agents overview](https://platform.claude.com/docs/en/managed-agents/overview)
- MindStudio — [Claude Code "Chyros" background daemon](https://www.mindstudio.ai/blog/what-is-claude-code-chyros-background-daemon)
- amux — [Best Multi-Agent Coding Orchestrators in 2026 (Claude Squad, Conductor, Codex)](https://amux.io/blog/best-multi-agent-orchestrators-2026/)
- SitePoint — [Claude Code as an Autonomous Agent: Advanced Workflows (2026)](https://www.sitepoint.com/claude-code-as-an-autonomous-agent-advanced-workflows-2026/)
