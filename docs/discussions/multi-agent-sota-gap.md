# Multi-agent SOTA — gap analysis vs Termipod

Status: **research note, 2026-04-27**. Written to ground the
"what's the project-layer multi-agent design?" decision in real
field practice rather than first-principles speculation.

The user's working definition of *project-layer multi-agent* (per the
discussion that prompted this doc):

> The steward helps the user plan a project goal, then spawns agents
> within the project, manages them through execution, and brings the
> goal to completion.

This is **not** the squad / federation layer (`agent-fleet.md`). It's
the orchestrator-with-workers pattern at project granularity — a lead
agent decomposing one project's goal and coordinating workers through
the plan.

Goal of this doc: **identify what production-validated SOTA does that
we don't, decide what to copy, what to skip, and what to defer.**

---

## 1. What SOTA actually settled on (April 2026)

I read the canonical engineering posts + framework docs, not influencer
blog summaries. Five frameworks have meaningful production use:

### 1.1 Anthropic's multi-agent research system

Most credible single source — a real production system at Anthropic's
own scale, with public engineering writeup.

- **Pattern**: orchestrator-worker. Lead agent (Opus-class) plans and
  spawns subagents (Sonnet-class). Subagents return findings; lead
  synthesizes.
- **Synchronous coordination**: lead waits for each batch of subagents
  to complete before deciding next step. They explicitly call this a
  "bottleneck" but ship it because it simplifies coordination.
- **Subagent contract**: each subagent receives **objective + output
  format + tool/source guidance + clear task boundaries**. Vague
  contracts caused duplicated work and endless searching in early
  versions.
- **Memory model**: lead's research plan is saved to **external
  memory** because it can't fit in context across long sessions.
  Long-horizon contexts use intelligent compression and summary
  retrieval.
- **Failure handling**: agents notified of tool failures and adapt;
  infrastructure does retry + checkpointing so failures resume
  rather than restart.
- **Cost**: ~15× tokens vs single agent. **Justified only for
  high-value tasks with heavy parallelization.**
- **Anti-patterns they hit early**: spawning excessive subagents,
  dividing work by task-type ("planning agent + implementation
  agent + testing agent" — they call this the "telephone game"
  failure), splitting sequentially-coupled phases.

> Quote (Anthropic blog): *"A well-designed single agent with
> appropriate tools can accomplish far more than many developers
> expect."*

### 1.2 LangGraph Supervisor (LangChain)

Library shape; the SOTA reference implementation of the
orchestrator-worker pattern.

- **Supervisor-as-router**: a single supervisor node decides which
  worker to invoke next; workers don't talk to each other directly.
- **Tool-based handoff**: communication between agents uses a
  `create_handoff_tool` that passes full message history + a
  successful-handoff marker. Workers see what the supervisor saw.
- **State carried cleanly throughout execution** via the graph's
  shared state object (TypedDict-style schema).
- **Why teams pick it**: best for stateful workflows with conditional
  routing, error recovery, human-in-the-loop. Highest production
  success rate in framework benchmarks.
- **Tradeoff**: latency overhead from the routing node. The doc
  argues "routing accuracy advantage matters more than the latency
  penalty in most early deployments."

### 1.3 CrewAI hierarchical process

Higher-level abstraction over LangGraph-shaped patterns.

- **Manager + workers crew**: manager agent reads the goal, breaks
  into subtasks, delegates, validates, synthesizes.
- **Roles + memory**: each worker has a `role` and a `goal`; manager
  reads roster to pick. `memory=True` is "set in production"
  (~30% cost reduction by caching context).
- **Sweet spot**: 3–4 agents. More than that, the manager's
  delegation quality degrades.
- **Sequential vs hierarchical**: sequential (assembly line) is
  faster + cheaper for >3-agent linear pipelines; hierarchical
  earns its keep when routing logic is non-trivial.

### 1.4 OpenAI Agents SDK (replaces Swarm)

March 2025 release; April 2026 added a "harness system" matching
Codex's scaffolding.

- **Three primitives**: Agents (LLM + instructions + tools),
  Handoffs (agent-to-agent transfer), Guardrails (input/output
  validation).
- **Built-in tracing** for observability (their answer to the
  black-box-debugging problem).
- **Harness wrap**: instructions, tools, approvals, tracing,
  resume bookkeeping for long-running agents that survive
  interruptions.

### 1.5 Devin / Cognition

Production product, not framework. Notable for:

- **Each Devin runs in its own VM** — hardware-isolated.
- **Managed Devins** can decompose to a team of managed Devins,
  each in their own VM, in parallel.
- **Their public stance**: avoid splitting one task across multiple
  agents; prefer sequential decisions within one agent unless the
  parallelism is genuinely independent. (Echoes Anthropic's
  warning.)

---

## 2. What the field converged on

Reading across these five sources, the consensus pattern is
remarkably consistent. The **orchestrator-worker** with these specific
properties:

| Property | Consensus | Why |
|---|---|---|
| **Topology** | Star (orchestrator at center, workers as leaves) | Simpler to debug; routing is auditable; worker-to-worker communication is rare in practice |
| **Coordination** | Synchronous waves — orchestrator dispatches a batch, waits for all to finish, decides next step | Simpler than async; no race conditions; the latency cost is amortized over the parallelism gain |
| **Worker contract** | objective + output format + tool guidance + task boundaries | Vague contracts → duplicated work + scope creep |
| **Memory** | Each worker has its own context; orchestrator has its own (often summarized) | "Free-form context sharing remains an unsolved problem" — Cursor |
| **Cross-agent transfer** | Only via *artifacts* (reports, structured outputs) | Audit trail; reproducibility |
| **Splitting strategy** | By **independent decomposable subtasks**, not by **task type** (planner / coder / tester) | Type-based decomposition causes "telephone game" failures |
| **When to use** | Parallelization is real, task is decomposable, cost ≥3–10× single-agent | Single agent + better prompting often equivalent |
| **Sweet spot size** | 3–4 workers per orchestrator; degrades past that | Manager's routing quality declines |
| **Failure model** | Notify-and-adapt for soft failures; checkpoint + retry for hard | Restarts are expensive at multi-agent token budgets |

The interesting thing is what's **not** in the consensus:

- No standing teams (squads) — bursts dissolve when done.
- No peer-to-peer agent communication as default — it exists but
  is the exception, not the rule.
- No real consensus / voting — quorum + manager tiebreaker.
- No shared mutable state — everything goes through artifacts.

---

## 3. Where Termipod stands today vs SOTA

Mapping our existing pieces to the SOTA pattern, position-by-position:

| SOTA component | Termipod equivalent | Status |
|---|---|---|
| **Orchestrator** | Steward (or domain steward) | ✅ Shipped. Multi-steward ships post-v1.0.290. |
| **Spawn workers** | `agents.spawn` + `mcp__termipod__spawn` MCP tool | ✅ Shipped. Hub does the hard part (handle uniqueness, parent linkage, host assignment). |
| **Worker contract** | `spawn_spec_yaml` + persona seed + template overlay | ✅ Schema exists. ⚠️ Steward's prompts don't enforce "objective + output format + tool guidance + boundaries" structure today. |
| **Synchronous waves** | Steward calls `a2a.invoke` 1:1, waits | ⚠️ Possible but manual. No `fanout(N agents)` primitive. |
| **Worker → orchestrator handoff** | A2A `message/send` + agent_events stream | ✅ Shipped. The "report" structure (markdown + frontmatter) isn't enforced — workers post freeform. |
| **Plan as scaffold** | `plan_steps` table with `agent_driven` phases | ✅ Shipped. Plans are shallow + reviewable per blueprint §6.2. |
| **Project as goal container** | `projects` table with `goal_md`, `parameters_json`, `template_id`, `steward_agent_id` | ✅ Shipped. |
| **Observability / tracing** | `agent_events`, `audit_events`, AG-UI stream | ✅ Shipped. Per-event observability is *better* than most frameworks. |
| **Failure model — notify** | Driver emits `tool_call_update.status=failed` events | ✅ Partial. Mobile renders status; nothing prompts the steward to react. |
| **Failure model — retry/checkpoint** | Session resume (interrupted → resume spawns fresh agent with same worktree/spec) | ✅ Shipped post-v1.0.276. |
| **Memory: external plan storage** | Steward template + `documents` rows | ✅ Shipped. Steward's persona doc + project docs serve this role. |
| **Memory: cross-session recall** | `documents` + `audit_events` queryable via MCP | ✅ Shipped. |
| **Guardrails** | Tier-based approvals (`permission_prompt` for significant/strategic) | ✅ Shipped. More granular than most SOTA. |
| **Worker isolation** | One worktree per spawn; worker process owned by host-runner | ✅ Shipped. No microVM yet (post-MVP per `project_post_mvp_sandbox`). |

**Honest reading: we have the architecture; we don't yet enforce the
patterns.**

The schema and primitives needed to *do* SOTA orchestration are all
in place. What's missing is the *discipline at the steward prompt
layer* and a couple of *helpers* that make the recommended patterns
the easy path instead of "the steward could do this manually if it
remembers to."

---

## 4. Concrete gaps

Listing only the gaps that would actually change behavior, in priority
order:

### Gap 1: No structured worker contract

**SOTA**: every spawn carries (objective, output format, tools, boundaries).

**Termipod today**: `spawn_spec_yaml` + `persona_seed` + template — but
nothing in the steward's prompt or the spawn schema enforces the
objective/output-format/boundaries structure. Workers receive a
template + a free-text persona seed, then figure out their job from
the conversation.

**Symptom**: workers will ask the steward "what do you want me to do?"
mid-flight, or post freeform results that the steward then has to
re-parse.

**Fix shape**: extend the steward prompt's worker-spawn recipe to
**always** structure the persona seed as:

```
GOAL: <one sentence>
OUTPUT: <format the worker produces>
TOOLS: <subset the worker is licensed to call>
BOUNDARIES: <what's out of scope>
DONE WHEN: <termination condition>
```

Plus a `spawn.persona_seed_template` field on the steward template
that drops into this shape on every spawn. ~30 LoC + a steward prompt
edit. **High value, tiny work.**

### Gap 2: No `fanout` helper

**SOTA**: orchestrator-worker patterns lean on parallel sub-agent
launches. Anthropic's research system gets 90% latency reduction
from parallel calls.

**Termipod today**: steward calls `a2a.invoke(handle, text)` one
worker at a time. Each spawn requires a fresh template lookup +
agent insert + session open. Nothing batches them.

**Symptom**: steward serializes work that should run in parallel.

**Fix shape**: an MCP tool `agents.fanout(spawns: [{handle, kind, spawn_spec, persona_seed}])`
that creates N agents inside one transaction, each with auto-opened
sessions, and returns the list of agent_ids + correlation_id. Steward
then `a2a.invoke`'s each in parallel. ~150 LoC server + steward prompt
edit. **High value, modest work.**

### Gap 3: No `gather` helper

**SOTA**: orchestrator dispatches a batch, **waits for all to
complete**, then decides next step.

**Termipod today**: steward dispatches via A2A; A2A returns a task_id;
steward must poll `tasks/get` per task. There's no "wait for these N
correlation_ids to all reach `completed`" primitive.

**Symptom**: steward either polls in a loop (burns context) or moves
on prematurely (gets partial results).

**Fix shape**: MCP tool `agents.gather(correlation_id, timeout_s)`
that long-polls server-side, returning when all spawned-in-the-fanout
tasks reach a terminal state (completed | failed | cancelled), or
when the timeout fires. ~100 LoC server. **High value, small work.**

### Gap 4: Worker output format isn't a typed artifact

**SOTA**: workers return structured output (JSON, markdown with
frontmatter); orchestrators parse it programmatically.

**Termipod today**: workers post freeform text via channels or A2A
responses. Steward reads the prose and infers.

**Symptom**: parsing errors, hallucinated synthesis, missing key
fields.

**Fix shape**: convention + schema. Define `worker_report.v1.md`
template with required frontmatter (`status`, `output_uri`,
`artifacts`, `budget_used`, `next_steps`). Steward prompt instructs
workers to post reports in this form. Add an MCP `reports.post(task_id,
status, frontmatter, body)` tool that validates the shape server-side.
~80 LoC + a markdown template. **Medium value, small work.**

### Gap 5: No "synchronous wave" pattern in the steward prompt

**SOTA**: orchestrators dispatch a wave, wait, decide the next wave —
not a continuous async stream of decisions.

**Termipod today**: steward decides loosely. Sometimes it dispatches
sequentially, sometimes in parallel, sometimes mixes.

**Symptom**: hard to predict what the steward will do; debugging
multi-step decompositions is messy.

**Fix shape**: pure prompt change. Steward template gains a
"Decomposition recipe" subsection: "1. Plan all the sub-tasks. 2.
`agents.fanout` for the parallel ones. 3. `agents.gather` to wait. 4.
Synthesize, decide next wave. 5. Repeat until plan is done." Already
partially shipped in `prompts/steward.v1.md` per
`research-demo-gaps.md` P4.2. Strengthen with concrete fanout/gather
calls now that the helpers exist. ~prompt edit.

### Gap 6: No "manager re-reads roster" cost optimization

**SOTA** (CrewAI): `memory=True` caches the roster / past delegations
to reduce manager cost ~30%.

**Termipod today**: each MCP call re-fetches `list_agents`. Steward's
context window holds the roster only as long as the model decides
to keep it.

**Symptom**: token burn during long projects with many agent lookups.

**Fix shape**: cache `list_agents(scope=project)` server-side (already
have `listAgentsCached` mobile-side; backend can do the same).
~50 LoC. **Low value at current scale; revisit when token bills
matter.**

### Gap 7: No worker-failure-aware steward loop

**SOTA**: orchestrator notified of tool failures; adapts (retries,
escalates, drops).

**Termipod today**: when a worker fails, mobile shows it as `failed`
status. Steward isn't proactively notified — it has to notice on
the next `list_agents` call.

**Symptom**: stalled projects where a worker died silently.

**Fix shape**: agent_events stream includes lifecycle events. Steward's
prompt + tool surface should include "subscribe to my child agents'
lifecycles; if any flips to failed/crashed, get notified on next
turn." Concrete: `agents.children_status_since(seq)` MCP tool that
returns recent state changes. ~80 LoC. **Medium-high value once
projects span multiple agents over time.**

### Gap 8: Anti-pattern guardrail — type-based decomposition

**SOTA anti-pattern**: dividing work as "planner agent + coder agent +
tester agent" causes telephone-game failures. Decompose by
**independent subtasks**, not by **task type**.

**Termipod today**: nothing prevents the steward from making this
mistake. The current prompt encourages "spawn a worker per phase"
which is *exactly the wrong shape*.

**Symptom**: workers built from type templates ("the testing agent")
will under-perform a single agent given the same prompts.

**Fix shape**: re-write the steward's decomposition recipe to
explicitly forbid type-based decomposition. Replace with: "spawn one
worker per *independent subtask*, with that subtask's full context."
The recipe in `prompts/steward.v1.md` already does this approximately
(per P4.2); make it explicit + add an example anti-pattern.

### Gap 9: No standing roster of named workers

**Anthropic-style**: the same orchestrator spawns fresh subagents per
turn; subagents are ephemeral.

**CrewAI-style**: workers are named members of a crew that persists.

**Termipod today**: leans Anthropic — workers are spawned per task,
terminated when done. Project-scoped persistent workers (e.g., "the
project's resident reviewer") aren't a primitive.

**Symptom**: every invocation spawns a fresh agent; no cumulative
expertise.

**Fix shape**: this is the **squad** primitive from `agent-fleet.md`.
Confirmed correctly deferred per the user's "squads are far
post-MVP" decision.

---

## 5. The recommended slice (single wedge, ~1 week)

If we ship just the gaps that are *high-value × small-work* and that
follow SOTA discipline:

1. **Gap 1**: structured worker contract (prompt template).
2. **Gap 2**: `agents.fanout` MCP tool (server + prompt).
3. **Gap 3**: `agents.gather` MCP tool (server + prompt).
4. **Gap 4**: `worker_report.v1.md` schema + `reports.post` MCP tool.
5. **Gap 5**: rewrite steward decomposition recipe to use fanout +
   gather + report.
6. **Gap 8**: forbid type-based decomposition in the prompt with a
   worked anti-pattern example.

That's roughly **350 LoC server + 150 mobile + 1 prompt rewrite**.
After this, the steward can drive an orchestrator-worker pattern
that matches Anthropic's research-system shape, with our existing
schema + audit + observability behind it.

Defer (in order):

- Gap 6 (caching) — only matters once token bills matter.
- Gap 7 (failure subscription) — only matters once projects span
  multiple agents over hours/days.
- Gap 9 (squads) — confirmed deferred.

---

## 6. What we have that SOTA doesn't

Worth flagging — Termipod has some things the field doesn't:

- **Multi-host orchestration**. Most frameworks assume one box. Our
  hub + host-runner + A2A relay routes across hosts as a first-class
  case.
- **Mobile-first principal UI**. Anthropic's system is API-first;
  CrewAI is Python-CLI-first. Our chat surface is the principal's
  primary surface, not an afterthought.
- **Full audit trail** at the event level (agent_events, audit_events).
  CrewAI/LangGraph's tracing is opt-in observability; ours is the
  source of truth.
- **Tier-based approvals** (`permission_prompt` for ≥significant
  tool calls). More granular than the binary guardrails most
  frameworks ship.
- **Session-as-conversation primitive**. SOTA frameworks treat
  sessions as glue around the agent process; we treat them as the
  durable conversational surface that survives respawn.

These are differentiators worth preserving — the question is what to
adopt from SOTA without losing them.

---

## 7. The decision before any code

Two real questions:

1. **Adopt the SOTA orchestrator-worker discipline (the 6-item slice
   in §5) or stay with today's looser pattern?** The slice is small;
   the value is "your steward will actually execute the demo path
   reliably instead of dispatching ad-hoc."
2. **If we adopt it: do we ship as one wedge or split it?** The 6
   items are tightly coupled — fanout without gather is awkward;
   reports without a recipe are prose; the recipe needs all of the
   above. Single wedge probably right.

My read: **ship the slice as one wedge**. It's small, it makes the
research demo more reliable (which is what the MVP is targeting), and
it doesn't commit us to anything (squads, fleet ops, anything bigger).
Defer everything else.

---

## 8. Sources

External, ranked by signal density:

1. [Anthropic — How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
   — single most useful source. Production system, real numbers.
2. [Anthropic / Claude — When to use multi-agent systems (and when not to)](https://claude.com/blog/building-multi-agent-systems-when-and-how-to-use-them)
   — the negative case. Quote: *"start with a single agent."*
3. [LangGraph Supervisor docs](https://reference.langchain.com/python/langgraph-supervisor)
   — reference implementation of orchestrator-worker.
4. [CrewAI Hierarchical Process docs](https://docs.crewai.com/en/learn/hierarchical-process)
   — production lessons (memory=True, 3-4 agent sweet spot).
5. [OpenAI Agents SDK](https://openai.github.io/openai-agents-python/)
   — the Swarm replacement; primitives + handoff + guardrails +
   tracing.
6. [Devin / Cognition](https://cognition.ai/blog/devin-2)
   — managed-Devins-of-Devins; per-agent VM isolation.
7. [Multi-agent orchestration framework comparison (Adopt.ai 2026)](https://www.adopt.ai/blog/multi-agent-frameworks)
   — survey of which frameworks production teams are picking.
8. [Composio agent-orchestrator](https://github.com/ComposioHQ/agent-orchestrator)
   — open-source instance of the parallel-coding-agents pattern.
