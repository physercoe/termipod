# Intra- vs inter-engine delegation

> **Type:** discussion
> **Status:** Resolved (2026-06-07) → [ADR-016 Amendment](../decisions/016-subagent-scope-manifest.md#amendment-2026-06-07) + the `steward.v1` prompt change in the same commit
> **Audience:** contributors, reviewers
> **Last verified vs code:** v1.0.808

**TL;DR.** A claude-code [steward](../reference/glossary.md#steward)
spawned dozens of hub [workers](../reference/glossary.md#worker) for
small tasks instead of doing the cheap work itself or fanning out
*inside its own engine*. Root cause: the steward prompt offers only a
binary — do-it-inline vs spawn-a-hub-worker — and routes all
parallelism to hub spawn. The fix is a **three-tier delegation
ladder** with an explicit promotion test. The governing principle: the
inter-engine boundary is the unit of **director attention and
governance**, not the unit of compute.

---

## 1. The trigger

A tester ran a multi-agent demo. The general steward (a `claude-code`
[agent](../reference/glossary.md#agent)) decomposed the goal and
spawned **dozens of hub workers** to do many small tasks — while never
using claude-code's own internal `Task` fan-out to do anything itself.
Every micro-step became a full, separate engine process with its own
[hub session](../reference/glossary.md#hub-session), tmux pane, audit
row, and budget envelope.

The tester's question is the right one: claude-code can already spawn
many cheap agents *inside itself* to finish a goal — so where is the
boundary between an **intra-engine agent** (an engine-internal
subagent) and an **inter-engine agent** (a hub-managed worker), and
what is the right model?

## 2. The two primitives (already named in the system)

| | Intra-engine agent | Inter-engine agent |
|---|---|---|
| What it is | engine-internal subagent — claude-code `Task`, codex app-server child | a hub worker spawned via `agents_spawn` / `agents_fanout` |
| Process | shares the parent engine process | its own OS process + engine instance |
| Hub identity | none — invisible as topology | a row in `agents`; its own [hub session](../reference/glossary.md#hub-session) + pane |
| Governance | inherits the parent's [operation scope](../reference/glossary.md#operation-scope) by construction (ADR-016 D5) | own role, budget/policy envelope, audit trail |
| Lifetime | ephemeral — dies with the parent's turn | durable — survives respawn; resumable; a first-class [task](../reference/glossary.md#task) assignee |
| Reach | same engine, same host | any engine, any host (A2A across NAT) |
| Marginal cost | tokens (a separate context window) | a process + session + cold-start context + RAM + **a slot of director attention** |

The concept is not new. [ADR-016 D5](../decisions/016-subagent-scope-manifest.md)
already rules engine-internal subagents *out of scope* for hub
governance — they "inherit the parent's operation scope by
construction and are not separately monitored." What ADR-016 did not
say is *when the steward should prefer one over the other*. That gap is
what this discussion closes.

## 3. Root cause — a missing tier, not a misbehaving steward

Read `hub/templates/prompts/steward.v1.md`. Its entire delegation
vocabulary is hub-spawn: an "Orchestrator-worker pattern (PREFERRED)"
section, `agents_fanout`, and the rule "one worker per independent
subtask." It even carries cost discipline ("fanout costs ~3–10× tokens
… sweet spot 3–4 workers"). But there is **no middle tier**. The model
it hands the steward is binary:

> *do it inline myself* — or — *spawn a hub worker.*

So when a goal genuinely has 30 small independent subtasks, the rule
"one worker per independent subtask" plus "parallelism is real" routes
**all** of that parallelism to the only parallel primitive the prompt
names: hub spawn. You get dozens of processes for trivial work. The
steward followed its instructions. The instructions are missing a
rung.

## 4. First principles — a cost hierarchy, not a switch

Delegation is a ladder of escalating cost, each rung roughly an order
of magnitude dearer than the one below:

| Tier | Primitive | Marginal cost | Buys you |
|---|---|---|---|
| 1 | **Inline** (the steward's own turn) | ~0 | sequential, cheapest, full shared context |
| 2 | **Intra-engine subagent** (`Task`) | tokens only | parallelism + context isolation, same engine/host, ephemeral |
| 3 | **Inter-engine hub worker** (`agents_spawn`) | process + session + cold context + RAM + an attention slot | distribution, heterogeneity, durability, governance, failure isolation |

The classic engineering error is using a high rung where a low one
fits: forking a process per function call when a thread or coroutine
exists; standing up a network service per module — the **nanoservice /
distributed-monolith** anti-pattern. Spawning a hub engine per
ten-second edit is the same mistake one layer up. The boundary — here,
the process + session + hub registration — is never free: it adds
latency, failure modes, memory, and observability load. *Don't
distribute until you must.*

## 5. Well-tested practice points the same way

- **Anthropic's multi-agent research system** (2025): subagents shine
  for breadth-first parallel exploration with separate context
  windows — but cost ~**15× the tokens** of a single chat. Even tier-2
  fan-out is justified only when the task value is high and the work
  is genuinely parallel. The prompt's "3–4 per fanout, ≥2× wall-clock
  win" instinct is right; it was just aimed at the wrong tier.
- **Cognition, "Don't Build Multi-Agent Systems"** (2025): the
  dominant failure of eager fan-out is **context fragmentation** —
  subagents make locally-plausible, globally-conflicting decisions and
  reintegration is where it breaks. This cost is paid by *both* tier 2
  and tier 3; tier 3 only adds process/governance overhead on top. So
  the bar for tier 3 should be *strictly higher* than for tier 2 — not
  the default.
- **Span of control / Conway's law**: a good manager does cheap things
  in-head and onboards a contractor only for a substantial, separable
  deliverable. A manager who hires a fresh contractor (full onboarding
  each time) for every five-minute task is the steward we observed.

## 6. The right model

**Default to the lowest rung; promote up only on a trigger.** The
steward does work inline, reaches for an intra-engine subagent when
there is *real* parallelism or context-isolation value, and reifies an
inter-engine hub worker **only** when at least one **promotion
trigger** holds:

1. **Crosses a host** — must run where different compute/data lives.
   Tier 2 cannot leave the process.
2. **Crosses an engine** — needs codex / kimi specifically, not
   claude-code.
3. **Is a durable, director-visible deliverable** — a tracked
   [task](../reference/glossary.md#task) the director would ratify,
   budget, audit, or resume.
4. **Outlives the parent's turn** — a long/overnight job that must
   survive the steward respawning and be independently resumable.
5. **Needs its own governance envelope** — a separate budget cap,
   policy scope, or permission mode.
6. **Needs a hard failure boundary** — if it loops or burns tokens it
   must not take the steward down.
7. **Is large enough to amortize spawn overhead** — seconds of
   cold-start + RAM + an attention slot are a real fixed cost.

If none hold — same engine, same host, small, ephemeral, no separate
deliverable — the unit stays tier 1 or 2. "Dozens of workers for small
same-host tasks" fails every trigger. That is the tell.

**The governing principle** that makes the boundary crisp:

> The inter-engine boundary is the unit of **director attention and
> governance** — *not* the unit of compute.

Decompose *compute* as cheaply as possible (inline → subagent). Reify
a hub worker only for what the director would actually want to see,
ratify, budget, or independently control — which is exactly the
[task](../reference/glossary.md#task) primitive (ADR-029) and the
steward-accountability model (ADR-025: the steward owns its internal
decomposition and is answerable for the whole). This is also forced by
IA axiom A1 — *human attention ≪ agent output*: forty hub rows for one
goal **is** the attention-flooding failure the product exists to
prevent. And it is forced by economics — forty heavyweight engine
processes is precisely what would blow the 2 GB VPS the
[ADR-045](../decisions/045-hub-storage-scaling.md) scaling work braces
([hub-scaling-storage-and-concurrency.md](hub-scaling-storage-and-concurrency.md)).

## 7. The governance objection answers itself

*"Isn't invisible intra-engine fan-out a governance hole?"* No. Per
[ADR-016 D5](../decisions/016-subagent-scope-manifest.md), an
engine-internal subagent inherits the parent's
[operation scope](../reference/glossary.md#operation-scope) and runs
its `hub://*` MCP calls through the parent's session. The *topology* is
invisible, but every *consequential action* still crosses the hub MCP
boundary under the steward's identity and is audited there. You govern
**actions and deliverables**, not compute decomposition. So the
steward may fan out internally as much as it likes; the moment it does
something director-meaningful, governance bites at the right layer.

## 8. What changes

This is a prompt/policy gap, not a mechanism gap — the mechanism
(engine-internal subagents) already exists and is already scoped
correctly by ADR-016 D5. Landed in this commit:

1. **ADR-016 amendment (D-amend-1)** — promotes D5 from "we don't
   restrict engine-internal subagents" to "and the steward *should
   prefer* them for cheap parallelism; the inter-engine boundary is
   the unit of director attention and governance." See
   [ADR-016 Amendment (2026-06-07)](../decisions/016-subagent-scope-manifest.md#amendment-2026-06-07).
2. **`steward.v1` prompt** — a new "Delegation ladder" section ahead of
   the orchestrator-worker pattern, with the three rungs and the
   promotion-trigger test; the orchestrator-worker section is reframed
   as tier-3 guidance ("once the work warrants inter-engine workers"),
   not the default for any decomposable goal.

## 9. Open follow-ups (not blocking)

- **Make spawn cost legible.** The steward over-spawns partly because a
  spawn *feels* free. A spawn-rate signal, or a soft policy that
  surfaces "you've spawned N workers for this goal — are these
  director-meaningful deliverables?", converts the abstract cost into
  feedback.
- **Over-decomposition guardrail.** A policy that flags many workers
  each completing in seconds with trivial output, since `agents.spawn`
  already passes through the governed-action path.
- ~~**Generalize to the other-engine steward prompts.**~~ **Done in the
  same arc** — and "check before writing" mattered twice. The codebase
  alone was *misleading*: the per-engine prompts didn't mention a native
  subagent, which first looked like the engines lacked one. A web check
  of the engines' own docs (2026-06) corrected that — the tier-2
  mechanism is real but **differs per engine**, so each got an
  engine-correct pass, never a copy of the claude-code wording.
  - **claude-code** — `Task` tool. Affirmed tier-2 (inherits the
    parent's MCP scope — ADR-016 D5, blueprint §3.3,
    `reference/hub-mcp.md`).
  - **codex** — native **parallel subagents**, invoked in plain language
    ("spawn one agent per point, wait, summarize"); ephemeral,
    codex-orchestrated, `/agent` to inspect
    ([developers.openai.com/codex/subagents](https://developers.openai.com/codex/subagents)).
    Named in the prompt as tier-2.
  - **kimi-code** — the **`Agent` tool** with built-in
    `explore`/`plan`/`coder` subagents; isolated context, ephemeral
    ([moonshotai.github.io/kimi-cli](https://moonshotai.github.io/kimi-cli/en/customization/agents.html)).
    Named in the prompt as tier-2.
  - **gemini-cli** — deprecated (retires 2026-06-18), not re-verified;
    left with the universal guard only, no tier-2 tool named.
  - **antigravity** — `agy invoke_subagent` exists but runs on agy's
    private bus the hub can't see; its prompt **bans** it. Kept and
    harmonized — the guard routes parallel exploration inline instead.
  - Governance caveat: the codex/kimi docs do *not* confirm whether
    their subagents share the parent's MCP connection, so the prompts
    stay conservative — use native subagents for *ephemeral* compute
    only, never as a substitute for a governed hub worker on dispatched
    or durable work.

## 10. References

- [ADR-016](../decisions/016-subagent-scope-manifest.md) — subagent
  operation-scope manifest; D5 + the 2026-06-07 amendment.
- [ADR-029](../decisions/029-tasks-as-first-class-primitive.md) — task
  as the first-class unit of steward-dispatched work.
- [ADR-025](../decisions/025-project-steward-accountability.md) —
  steward accountability for its own decomposition.
- [ADR-045](../decisions/045-hub-storage-scaling.md) /
  [hub-scaling-storage-and-concurrency.md](hub-scaling-storage-and-concurrency.md)
  — why heavyweight over-spawn is also a systems cost.
- Anthropic, "How we built our multi-agent research system" (2025).
- Cognition, "Don't Build Multi-Agent Systems" (2025).
