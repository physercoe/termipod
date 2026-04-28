# termipod agent harness

This document is the third leg of the design tripod, peer to
`blueprint.md` (architecture/ontology) and `information-architecture.md` (mobile IA).
Where blueprint defines *what an agent is* and IA defines *where the
steward appears in the app*, this doc defines the layer in between:
how an agent is born, what it can do alone, when it spawns workers,
how context outlives any given process, and what the user sees through
all of it.

The motivating observation is that today's docs jump straight from
"agent = LLM process with status enum" (blueprint §6.4) to "spawn
endpoint reaches host-runner which opens a tmux pane" (`../reference/hub-agents.md`).
The layer between — the *harness* — is implicit. Every feature that
touches steward behavior re-litigates the same questions: where does
the first steward come from, what does it do without workers, how does
context survive a respawn, when should it fan out. This doc settles
those.

---

## 1. Purpose & scope

**Purpose.** Provide a first-principles model of the harness so that:

1. The MVP demo can be tested in two stages: **single-agent (steward
   only)** first, then **multi-agent (steward + worker on a second
   host)**. Today there is no design that says what "single-agent"
   even means in our system, which means there is nothing to test.
2. New features touching the steward inherit a settled vocabulary
   (steward, worker, harness, context, session, memory, skill, tool
   surface) instead of inventing one per PR.
3. Comparable products (Claude Code, OpenClaw, Hermes, Cursor 2.x
   background agents, Devin 2.0, Happy) inform our defaults rather
   than being re-discovered ad hoc.

**Scope.** Everything from "the first time a user lands in a
freshly-created team" through "five workers running across three
hosts under one steward." Not in scope: the wire protocols themselves
(blueprint §5), the IA surfaces themselves (ia-redesign §6), or the
operational spawn pipeline (hub-agents.md) — those exist and are
authoritative for their layer. This doc cites them.

**Status of claims.** Each section marks claims as either:
- **Today:** what the codebase already does;
- **Target:** what this doc commits us to;
- **Gap:** the delta between Today and Target.

---

## 2. Axioms

Seven axioms, paired with blueprint's 3 (purpose/separation/data
ownership) and IA's 6 (attention, roles, surfaces). Together they
define what the harness must guarantee.

**HA-A1. The harness is a layer, not a feature.**
The harness is everything between the LLM API call and the wire
protocols (ACP/AG-UI/A2A). It owns: the agent loop, tool dispatch,
context compaction, permission gating, budget tracking, identity
persistence, return-path management. Today these pieces exist
scattered across `agent_runtime/`, `host-runner`, and the mobile chat
surface. Naming the layer means: it has its own ownership, its own
diagnostics, its own test surface.

**HA-A2. Single-agent is the base case, not a corner case.**
A team with one host, one steward, and no workers must be a complete
product — not "the spawn screen with one row in it." This is the Happy
and Claude-Code parallel: a single competent agent on a single host
is already useful. Multi-host orchestration is the *expansion*, not
the *premise*. Practical consequence: §6 below defines a complete
operating loop for steward-only.

**HA-A3. Context survives the process; sessions do not.**
Agent processes are mortal (model swaps, host reboots, network
partitions, the Devin "35-minute degradation" reality). The context
that defines what the agent is doing — goal, plan, attention queue,
memory, skills, identity — must outlive any one process. Sessions
are bounded; contexts are addressable persistent things.

**HA-A4. Fan-out is the steward's judgment, not the schema's.**
Today plans encode `phase=agent_driven` and the host-runner spawns
workers per spec. That's *plan-driven* fan-out. The harness must
also support *steward-driven* fan-out: mid-loop the steward decides
"this benefits from two workers in parallel" and asks the harness
to spawn them, without an external plan node saying so. Mirrors
Cursor's plan-mode → implement-mode split, but lifted into the
steward instead of imposed by the IDE.

**HA-A5. Every spawned agent has a named return path.**
A worker with no defined channel back to its parent is a leak.
Every spawn carries: parent agent ID, return channel (A2A endpoint
or shared project channel), deadline, and budget envelope. This is
the missing piece in the planner-worker pattern that Devin had to
retrofit; we bake it in.

**HA-A6. Identity persists; model and host don't.**
A steward has a stable identity (handle, persona/SOUL, learned
skills, history) that outlives any specific model checkpoint or
host. You can swap Claude Opus → GPT-5 → a local Hermes-Llama and
the steward "is" the same entity from the user's point of view.
Borrowed from OpenClaw's SOUL.md (persistent persona file) and
Hermes's skill folder (persistent behavior).

**HA-A7. Permission asks are first-class events.**
When the harness needs human ratification, it emits a structured
`attention_item` with a typed action — not "the agent paused and
printed a question into the channel." Validated by Happy's
push-notifications-for-permission pattern, which is the most-used
feature in mobile-CLI-agent wrappers.

---

## 3. Reference products & what they settled

Each subsection pulls one or two patterns we adopt and one we
reject.

### 3.1 Claude Code (Anthropic)

**Pattern adopted: persistent project memory file (`CLAUDE.md`) +
slash commands as namespaced capabilities.** A markdown file in the
project root becomes the durable instruction layer; `/commands` are
named verbs the user can invoke. We already have a project-channel
analog; we should have an equivalent of `CLAUDE.md` per project (a
*goal/persona document*, see §8) and slash commands surfaced in the
mobile composer.

**Pattern adopted: permission gate as a structured event** ("Allow
edit to file X?" with allow / allow-once / deny choices). Maps to
our `attention_items` of `kind=approval_request`.

### 3.2 Codex CLI (OpenAI)

**Pattern adopted: explicit tool-budget framing.** Each agent run
declares a token / tool-call budget; harness tracks consumption and
preempts at threshold. Today we have policy budgets (cents); this
extends them to per-loop tool-call ceilings.

### 3.3 OpenClaw (`openclaw.ai`, ~100k★)

**Patterns adopted:**
- **`SOUL.md`-style identity file:** a persistent persona document
  injected as system prompt every session. Five canonical sections
  (Identity / Communication style / Values / Boundaries / Goals).
  Our analog: `steward.persona.md` per team, plus per-project
  goal/scope overlay. *Today's gap:* persona is implicit in the
  spawn YAML; needs to be a persistent, editable document.
- **Skill loading hierarchy** (5 tiers: workspace → project agent
  scope → personal scope → machine-shared → bundled). We adopt a
  3-tier version: per-team → per-host → bundled. Per-agent skills
  are an anti-pattern at our scale (skills duplicate per worker).

**Pattern rejected: skill marketplace without vetting.** Cisco
research found OpenClaw third-party skills performing data
exfiltration. Our policy/approval layer (blueprint §6.11) is the
correct gate; skill imports MUST flow through it.

### 3.4 Hermes Agent (Nous Research, ~103k★)

**Pattern adopted: GEPA-style auto-generated skills.** After a
complex task (Hermes uses ≥5 tool calls as the threshold), the
agent writes a markdown skill file capturing: procedure, known
pitfalls, verification steps. GEPA (ICLR 2026 Oral) reads execution
traces — error messages, profiler output, reasoning logs — and
mutates the skill toward a Pareto front of improvements. Reported
result: 40% faster on repeated tasks for agents with 20+ skills,
and gpt-oss-120b beating frontier models on enterprise tasks at
20–90× lower cost.

For us this is *post-MVP*, but the design must not foreclose it.
Concrete implication for §8 (memory): the persistent memory tier
must be append-only, structured, and addressable — not just "chat
history blob." Skills are documents in our `documents` primitive
(blueprint §6.7) with a specific subtype.

**Pattern rejected: single gateway process serving N messengers
(Telegram/Discord/Slack/Signal/CLI).** We don't need it; our hub
is the gateway, and channels are the multiplex. Messenger-bridges
are a post-MVP integration, not a primitive.

### 3.5 Cursor 2.x background agents

**Patterns adopted:**
- **Auto-managed git worktree per parallel agent.** We already do
  this in `host-runner` for spawned agents. Cursor validates the
  pattern at scale (up to 8 parallel agents per problem).
- **Plan-mode + implement-mode split.** One agent designs in
  plan-mode while another implements in parallel. We lift this into
  the steward-driven fan-out of HA-A4: steward in "design" loop
  spawns a worker in "implement" loop, then reads back.

**Pattern noted but not adopted: ensemble (run N agents on the
same task, pick best).** Useful but expensive; defer to post-MVP.
Cursor's own writing notes "context sharing between parallel
agents remains an unsolved problem across the entire category."
We don't pretend to solve it; §8 below makes the boundaries
explicit.

### 3.6 Devin 2.0 (Cognition)

**Patterns adopted:**
- **Planner-worker as the dominant long-running architecture.** Our
  steward = planner; spawned agents = workers. We already match
  this; the doc names it.
- **The "effective performance window" reality.** Long tasks
  degrade past ~35 minutes of in-context work. Implication: the
  harness must auto-checkpoint context to persistent memory at
  defined boundaries (turn count, time, tool-call count) so the
  next session can resume. This is the bridge HA-A3 demands.

### 3.7 Aider, Goose, OpenInterpreter

Single-agent flavors. Aider's notable contribution: per-file diff
review as the unit of approval (not whole-task approval). Goose's:
named recipes (we already have templates). OpenInterpreter's: the
"local-first, no-cloud" stance which we share via host-runner.

### 3.8 Happy (`happy.engineering`, `slopus/happy`) — closest UX prior art

This is the product we are most often compared to (or will be).
What Happy does, in one paragraph: install `happy-coder` as an npm
companion on your laptop; the mobile app E2E-pairs to it and gives
you Claude Code (or Codex) on your phone, with file mentions, slash
commands, custom agents, voice input, and push notifications when
the CLI needs permission or hits an error.

**Pattern adopted: push notifications as the approval transport.**
The CLI hits a permission gate → companion forwards via E2E to phone
→ phone surfaces a push → user taps allow/deny → answer relays back.
This is exactly our `attention_items` flow, validated.

**Pattern adopted: device-pair quickstart.** Install companion → scan
QR / paste pair token → done. Our equivalent must hit the same
sub-2-minute target on the single-host case (§6.4).

**The differentiator we need to be able to articulate.** Happy is
"Claude Code on your phone" — one user, one host, one CLI. We are
"a steward that runs N CLIs on N hosts under one director." The
single-host case looks a lot like Happy; the value-add only shows
up when you have a second host or want delegated/scheduled work.
This doc must give the single-host case enough standalone value
that a user who never adds a second host still wins, otherwise we
lose to Happy on simplicity.

### 3.9 Distillation — patterns we adopt vs. reject

Adopt:
1. Persistent identity file (SOUL.md analog) — OpenClaw.
2. Skill = addressable markdown document — Hermes / OpenClaw.
3. Skill hierarchy (3 tiers) — adapted from OpenClaw's 5.
4. Permission gate as structured event with push transport —
   Claude Code / Happy.
5. Tool-call budget per loop — Codex.
6. Planner-worker as the multi-agent topology — Devin.
7. Per-worker git worktree — Cursor.
8. Plan-mode + implement-mode lift into steward-driven fan-out —
   Cursor.
9. Auto-checkpoint to persistent memory at boundaries — Devin's
   degradation-window finding.
10. Named return path on every spawn (HA-A5) — our own; addresses
    the leak Devin retrofitted.

Reject (or defer post-MVP):
- Skill marketplace without per-team vetting (OpenClaw lesson).
- Ensemble multi-agent ("run 8, pick best") — too expensive.
- Single-process gateway to N messengers (Hermes) — channels do this.
- GEPA self-improving skills loop — defer to post-MVP wedge.

---

## 4. Ontology

Eight named entities. Each has a definition, a present location in
the codebase or schema, and a target invariant.

### 4.1 Steward

The team's resident **manager / orchestrator / head**. Near-singleton
(one per team in MVP; ia-redesign §11 F-1 documents the per-member-
deputy variant for post-MVP). Owns: planning, decomposition, worker
spawning, decision arbitration, attention escalation, audit
narration, distillation of sessions into artifacts.

**The steward does not perform IC work directly.** It plans and
spawns; workers perform. The single explicit exception is the
single-agent bootstrap window described in §6.2 — when there are
no workers yet, the steward shells the host directly to demo
something useful. As soon as workers exist (or once the demo loop
ends), the steward retreats to the manager role.

Mental model: a chief of staff, not a coder. A director, not a
performer. A head, not a hand.

**Today:** an `agents` row with a special role flag (set by the spawn
template). **Target:** a first-class entity with its own table or a
clearly-flagged subset of `agents`, persistent identity (HA-A6)
carried across model swaps.

### 4.2 Worker

The team's **IC / performer / hand**. An agent spawned for bounded,
specific work — coding, experiments, analysis, writing. Has a parent
(steward or another worker), a return path (HA-A5), a deadline, a
budget, a worktree. Workers do the things; stewards decide what
things and when.

**Today:** a regular `agents` row spawned via `agents.spawn`; parent
edge lives in `agent_spawns`. **Target:** all four invariants
enforced at the schema level.

### 4.3 Harness

The layer between the LLM and the protocol. Owns: the agent loop
(read context → choose action → dispatch tool → observe → repeat),
tool dispatch with permission checks, budget tracking and preemption,
context compaction, identity loading (read SOUL/persona at session
start), checkpoint to persistent memory. **Today:** distributed
across `agent_runtime/` (loop), `host-runner` (process mgmt), MCP
bridge (tool dispatch). **Target:** named, with one entry point and
one diagnostics surface.

### 4.4 Context

The addressable bag of state the harness reads on session start and
mutates during operation. Composition:
- *Identity layer* (persona/SOUL — stable across sessions).
- *Goal layer* (project goal, current plan, current task —
  per-project/per-task).
- *Attention queue* (open approvals/decisions).
- *Working memory* (recent N turns, compaction-bounded).
- *Skill index* (pointers to skill documents the agent may use).

Contexts are *named* and *addressable* — the harness can ask "load
context `team:acme`" or "load context `project:p_xyz`" and the
correct stack assembles. **Today:** ad hoc; the spawn YAML hardcodes
some of this. **Target:** explicit addressable context type.

### 4.5 Session

One process lifetime of an agent. Bounded by either: voluntary
exit, timeout, eviction, or model upgrade. **Sessions ≠ contexts**
(HA-A3): a context outlives many sessions. **Today:** implicit;
the tmux pane lifetime is the de facto session. **Target:** session
records in DB, with start/end/checkpoint events.

### 4.6 Memory

Persistent state that survives session boundaries. Three tiers
(detailed in §8):
- *Ephemeral* — current session only (the LLM context window).
- *Persistent* — survives sessions, owned by one agent (skills,
  notes, persona).
- *Shared* — visible to multiple agents (channels, documents,
  artifacts; already in blueprint §6).

### 4.7 Tool surface

The allowlist of tools an agent may call this session. Composed
from: bundled tools (shell, file, MCP-via-hub), per-team policy
overrides, per-agent role narrowing, per-task context narrowing.
**Today:** spawn YAML lists `tools:`; runtime honors it. **Target:**
hierarchical composition, with policy as the hard ceiling.

### 4.8 Skill

A memorialized procedure the agent has learned (or been taught).
A skill is a `documents` row of subtype `skill`, with frontmatter
declaring trigger keywords, required tools, expected inputs/outputs.
The harness consults the skill index when planning a turn. **Today:**
none. **Target:** post-MVP wedge; design the schema now so skill
generation can land later (HA Hermes/GEPA inspiration).

### 4.9 Manager vs. IC — the layer split

Steward and worker are at **different layers**. They are not two
flavors of the same thing. The harness must enforce, render, and
reason about them as distinct surfaces.

| | Steward (manager / head) | Worker (IC / hand) |
|---|---|---|
| **Primary verbs** | plan, decide, spawn, arbitrate, distill, narrate | read, write, edit, run, test, commit, post-result |
| **Lifetime** | Long-lived identity; many bounded sessions | One spawn = one task; ends when task completes (or fails) |
| **Worktree** | Usually none; reads artifacts | One per spawn; writes code |
| **Tool surface** | Governance tools (audit, attention, decision_request, template propose, schedules, channels, plan ops) | Code/IC tools (read, write, edit, bash, test runners, metric posts) |
| **Approval surface** | Receives the user's decisions; arbitrates | Subject to the steward's policy; asks for permission via the harness |
| **Conversation content** | Questions, decisions, plans, briefs | Tool calls, file diffs, test outputs, branch state |
| **State strip in UI** | Loaded artifacts + scope label + token budget | Branch · file count · +N/-M · token budget *(Happy-style)* |
| **Distillation** | Decision / Brief / Plan-update artifact (`sessions.md` §6) | Task summary + code-change artifact (deferred per `../discussions/code-as-artifact.md`) |
| **Default model class** | High-capability (decisions are higher stakes) | Cost-efficient where the task allows |
| **Plurality** | One per team (MVP); per-member post-MVP | Many per project, many per task |
| **Mental model** | Chief of staff / director / lead | Engineer / analyst / writer doing the actual work |

**Two rules follow from this split:**

1. **The steward UI must not look like a coding agent.** Specifically:
   no branch/diff strip on steward chat; no file-card tool calls; no
   commit-status indicators. Those belong on worker UI. The steward
   chat is a *decision surface*, not a *code surface*. (Detail and
   precedent: `../discussions/transcript-ux-comparison.md` §7.4.)

2. **The steward should not be the IC.** When a steward starts
   reaching for code tools (read, edit, bash, commit), that is a
   signal to spawn a worker. The single-agent bootstrap mode in §6.2
   is the explicit, time-bounded exception — not the operating
   model. As soon as a host has been registered with the team and
   workers can spawn, the steward retreats from direct shell access
   except for governance-shell concerns (e.g. running a hub-side
   diagnostic).

**Why this matters now.** Comparable single-engine clients
(Happy, CCUI) collapse manager and IC into one chat because they
have one role per app. Our positioning (multi-host, team
governance) only pays off if the layer split is honored — otherwise
we're just a thinner client doing the same thing at higher cost.

**Anti-pattern:** a "general-purpose steward" that answers research
questions, edits files, runs tests, AND handles approvals. That's
one agent doing three layers' worth of work badly. Spawn workers;
keep the steward at the layer it's good at.

---

## 5. Lifecycle

### 5.1 Genesis — how a steward is born

Today's gap: nothing in the codebase explains where the first
steward comes from. The spawn endpoint requires a host; team
creation does not require a host; therefore a freshly-created team
has no steward and no obvious way to get one. This is the single
biggest gap blocking the single-agent demo.

**Target flow.** When the user creates a team:

1. Team row inserted; user is owner.
2. App detects "team has no steward" and opens the **Steward
   bootstrap** sheet:
   - Choose a host (with a "register a host now" inline path if none
     exists; reuses `install-host-runner.md` flow).
   - Choose a backend (claude-code / codex / local-llm); reasonable
     default per detected capability.
   - Confirm autonomy + budget defaults (autonomy=balanced,
     budget=$5/day) — editable later in Team Settings → Steward.
   - Optional: persona seed ("what is this team for?"). Becomes the
     first lines of the steward's persona document.
3. Hub spawns the steward via the existing `agents.spawn` pipeline,
   tagged `role=steward`.
4. Mobile auto-navigates to the team channel; steward greets the
   user. Done.

Time budget: ≤ 2 minutes from team creation to steward greeting,
matching Happy's pair flow. This is the single-host quickstart's
acceptance test (§6.4).

### 5.2 Operating states

Five states, all today-implicit-target-explicit:

| State | Meaning | Today | Target |
|---|---|---|---|
| `running` | Loop is active | `agents.status='running'` | same |
| `idle` | Loop is paused, awaiting input | conflated with `running` | new state, distinguishes "alive but waiting" |
| `waiting_attention` | Blocked on `attention_items` row | implicit | explicit; UI shows the blocking item inline |
| `paused` | Manually paused by user | `pause_state='paused'` | same |
| `archived` | Retired; context preserved | `status='archived'` | same |

### 5.3 Suspension & respawn

Three triggers for suspension:
1. User pause (manual).
2. Budget exhaustion.
3. Model swap (e.g., user changes the steward's backend).

On suspension, the harness writes a *checkpoint* to persistent
memory: current goal, current plan position, attention queue
snapshot, working-memory summary. On respawn, the new session loads
its addressable context (§4.4) and resumes — possibly under a
different model. This is HA-A6 + HA-A3 in operation.

Respawn must be safe under model swap: the checkpoint format is
prose + structured frontmatter (markdown), not model-specific
embeddings.

### 5.4 Retirement & archival

Stewards retire only when the team is dissolved or the user
explicitly archives. Workers retire when their task completes or
their deadline passes. Retirement preserves: the agent row, the
audit trail, all spawned-by edges, all output artifacts. The agent
process is killed; the persistent memory document is archived with
the agent.

---

## 6. Single-agent mode (the Claude-Code base case)

This section defines what the steward does when there are zero
workers. It is the base case (HA-A2) and the demo prerequisite.

### 6.1 The loop

Standard tool-use loop, with explicit budget and gating:

```
while not done:
    context = load_context(steward.id)
    next_action = LLM(context)
    if next_action.is_tool_call:
        if requires_approval(next_action):
            emit attention_item(approval_request)
            yield  # loop pauses; resumes on decide
        observe = dispatch_tool(next_action)
        context.append(observe)
        budget.consume(next_action.cost)
        if budget.exhausted: suspend_with_checkpoint(); return
        if turn_count >= checkpoint_threshold: checkpoint(); continue
    elif next_action.is_message_to_principal:
        post_to_team_channel(next_action.text)
    elif next_action.is_done:
        commit_task_complete()
        done = True
```

Concrete numbers (target defaults, not yet implemented):
- `checkpoint_threshold` = 30 turns OR 20 minutes wall-clock,
  whichever first (Devin degradation finding).
- `requires_approval` = budget tier ≥ `significant`, per existing
  policy (blueprint §6.11).
- `budget.cost` = token-based + per-tool fixed cost (tool call to
  shell ≠ tool call to MCP read).

### 6.2 What the steward does without workers (bootstrap mode only)

> **Important framing.** This section describes the **bootstrap
> window**: a fresh team, no host or one host, no workers spawned
> yet, the user wants useful behavior in the first 2.5 minutes. In
> this window the steward acts as both manager *and* IC because
> there's nobody else. As soon as a worker can be spawned (or the
> user explicitly directs the steward back to manager work), the
> steward should hand off and retreat to the role described in
> §4.1 / §4.9. This is an exception, not the operating model.

A complete useful steward-only operating set, **for bootstrap**:

1. **Direct host operation.** The steward has shell access on its
   host (the host it was spawned on). It can run commands, edit
   files, manage processes. This *is* Claude Code, exposed through
   our channel UI. Bootstrap-only — once workers exist, the steward
   delegates.
2. **Project authoring.** The steward creates projects, plans, tasks,
   schedules, channels via MCP (existing tools, see
   ux-steward-audit.md §1). This stays manager-class even after
   workers exist.
3. **Document authoring.** The steward writes briefs, reports,
   notes — first-class `documents` rows. Manager-class; stays.
4. **Scheduled work.** Cron-style schedules can fire steward
   actions (already shipped per blueprint §6.3). Stays.
5. **Attention narration.** Every meaningful action posts a
   structured event to the team channel; Activity tab surfaces it.
   Stays.
6. **Self-bootstrap of additional capability.** If the user wants
   more (a second host, a worker), the steward can guide them
   through it — including running the host-registration commands.
   This *is* the bootstrap path; once it succeeds, item 1 above
   becomes worker work.

This is enough to ship as v1.0 of the single-host demo, well before
multi-agent lands. **But:** the demo loop must also surface "spawn
a worker for this" as soon as the user asks for any non-trivial code
work, so the bootstrap exception doesn't silently calcify into a
permanent operating mode.

### 6.2.1 When does the steward hand off (the retreat trigger)

Concrete signals that bootstrap mode should end and the steward
should spawn a worker instead:

- The user asks for a multi-step coding task ("implement X", "fix
  the bug in Y") that lasts more than 2–3 turns of tool use.
- The current task touches code in a worktree the steward doesn't
  already own.
- A test run is needed.
- Parallelism would help (more than one independent sub-task).
- The user explicitly says "spawn a worker for this".

In all of these, the steward proposes a worker spawn (with the
current task as the worker's goal), the user approves, and the
steward steps back to plan/decide/distill mode for the rest of the
work. The worker reports back; the steward narrates.

### 6.3 Escape hatches & approval gates

Three gates the user can intervene at:

- **Per-tool-call approval** (the Happy push pattern): when policy
  says `significant` or `critical`, the loop yields and the user
  must explicitly approve.
- **Pause** (manual, immediate): freezes the loop; checkpoint is
  written; no further turns until resume.
- **Hard stop** (cancel the current task): clears current goal,
  steward returns to idle, persistent memory preserves the partial
  state under the cancelled task's ID.

Escape hatches must be reachable in ≤ 2 taps from any screen
(Activity, channel, Me/Attention).

### 6.4 UX: device-as-shell quickstart

The acceptance test for the single-agent demo:

1. Fresh install. → Onboard, create team. (≤ 30s)
2. Bootstrap sheet appears (§5.1). Register host, choose backend,
   confirm defaults. (≤ 60s)
3. Team channel opens with steward greeting + 3 suggested first
   actions ("show me what's on this host", "summarize my git repos
   here", "set up a daily brief"). (≤ 30s)
4. User taps a suggestion or types freely. Steward executes. First
   meaningful response on screen. (≤ 30s)

Total: ≤ 2.5 minutes from launch to first useful steward output.
This is the Happy benchmark; we must match or beat it.

---

## 7. Multi-agent mode

> **Scope of this section.** This section covers **bursts** — a
> steward spawning workers on demand for one decomposable task, then
> dissolving when the task is done. That's the model the codebase
> implements today.
>
> What it does **not** cover: standing teams of agents (squads),
> peer coordination between stewards (federation), shared mutable
> state across agents, or hierarchical org structures. Those layer
> *above* this section and live in `../discussions/agent-fleet.md` as a
> design memo (not yet started).
>
> The split is deliberate: bursts answer "how does one steward
> decompose a task?" — squads answer "how does a persistent group
> of agents organize?". Different problems; different time-scales.

This section defines what changes when there are workers.

### 7.1 When does the steward fan out

Three triggers:

1. **Plan-driven** (today): a plan phase declared `agent_driven`
   spawns workers per spec. Schema-mandated.
2. **Steward-driven** (HA-A4, target): mid-loop, the steward judges
   "this benefits from parallel work" and asks the harness to spawn.
   Heuristics the steward should use (codified in its persona, not
   the schema):
   - Task is decomposable into ≥2 independent sub-tasks.
   - Each sub-task fits in one context window.
   - Wall-clock parallelism would beat sequential by ≥2×.
   - Per-team `parallelism_cap` (policy) is not exceeded.
3. **User-directed**: the user says "do these two things in
   parallel"; steward fans out.

### 7.2 Worker spawn (today's pipeline, slightly extended)

`agents.spawn` already exists and works. Extensions needed:
- **Parent linkage in spawn payload** (today partly via
  `agent_spawns`; promote to first-class field on `agents`).
- **Return-path field** (HA-A5): explicit channel ID + A2A endpoint
  the worker reports back through.
- **Budget envelope inheritance**: worker's budget cannot exceed
  parent's remaining budget; harness enforces.

### 7.3 Worker → steward report-back

A2A relay (already required per `project_a2a_relay_required.md`
memory) is the transport. The protocol:

1. Worker on task completion: emits a structured *report* (markdown
   + frontmatter: outputs, artifacts produced, approvals consumed,
   budget remaining).
2. Hub relays report to steward's queue (steward may be on a
   different host).
3. Steward consumes report on its next loop turn; reconciles into
   its context.

### 7.4 Context reconciliation

The honest acknowledgment, per Cursor's note: "context sharing
between parallel agents remains an unsolved problem." We don't
solve it; we bound it.

Our model:
- Worker contexts are *not* shared with the steward in real time.
- Worker emits a *report* on completion (or scheduled checkpoint);
  the report is what the steward sees.
- Shared *via primitives* (channels, documents, artifacts) only —
  not via raw memory.
- This is a choice: it forces communication to flow through the
  audit trail, which is the single source of truth.

### 7.5 Parallelism caps & budget

Two caps:
- **Per-team `parallelism_cap`** (policy): max simultaneous workers.
  Default 4; configurable in Team Settings → Steward.
- **Per-spawn `budget_envelope`**: child cannot exceed parent's
  remaining budget (recursive).

### 7.6 UX: multi-agent inspector

When there are 2+ live agents, the user needs:
- A *roster* view — who's running, on what host, doing what, how
  long, how much budget consumed. (Today: `agent_feed.dart` —
  partial; needs the budget/host columns.)
- A *spawn tree* view — parent → children edges, per task. (Today:
  none; spawn edges are in DB but not surfaced.)
- An *attention badge* per agent — which ones need approval.
  (Today: global Attention; needs per-agent.)

This is post-bootstrap UX; lands after §5.1 and §6.4.

---

## 8. Context & memory architecture

### 8.1 Three tiers

| Tier | Lifetime | Owner | Persistence | Primary use |
|---|---|---|---|---|
| Ephemeral | one session | one agent | LLM context window only | turn-by-turn reasoning |
| Persistent | many sessions, one agent | one agent | DB (documents) | persona, skills, notes, checkpoint |
| Shared | many sessions, many agents | team / project | DB primitives (channels, documents, artifacts) | audit trail, deliverables, principal direction |

The boundary that matters most: *worker findings cross from
ephemeral → shared* (via report) before they can influence the
steward. They never cross worker-ephemeral → steward-ephemeral
directly. This is the Cursor lesson, made architectural.

### 8.2 Compaction & summarization

When ephemeral approaches budget:
1. Harness summarizes oldest N turns into one structured note.
2. Note is appended to persistent memory (the agent's working
   document).
3. Summarized turns are dropped from ephemeral.

This is the bridge HA-A3 demands, executed in-loop instead of only
at session boundaries.

### 8.3 Cross-agent transfer

Three legal paths:
1. *Report* (worker → steward, via A2A).
2. *Channel post* (any → any subscribed agent).
3. *Document/artifact reference* (any → any with read access).

No other paths. In particular: no shared memory blob, no peeking
into another agent's ephemeral, no copy-context-by-id between
agents. (If we need this later, it's a designed feature, not an
implicit capability.)

---

## 9. Tool & MCP surface per role

| Role | Default tool surface | Notes |
|---|---|---|
| Steward | All bundled tools + MCP-via-hub (full set per ux-steward-audit) + shell on its host | The CEO-class operator |
| Worker (general) | Inherited from steward, narrowed by spawn YAML | Often shell + a focused MCP subset |
| Worker (sandboxed) | Read-only MCP + scoped shell | For untrusted/exploratory work |
| Reviewer | Read-only MCP + post-event | Decision-making, not mutation |

The MCP surface itself (tool catalog) is governed by
`../discussions/ux-steward-audit.md` and is closed for MVP-critical actions.

---

## 10. Failure modes & escape hatches

Failure modes the harness must handle:

| Mode | Detection | Response |
|---|---|---|
| Loop wedged (no progress N turns) | turn count w/o action | suspend + alert user via attention |
| Tool call timeout | per-tool deadline | retry once, then alert |
| Budget exhausted | budget tracker | suspend with checkpoint |
| Model unavailable / quota | LLM API error | mark `waiting_attention`, surface choose-other-model action |
| Host unreachable | host-runner heartbeat | mark agent `unreachable`; preserve context |
| Parent steward retired with live workers | retirement guard | block retirement until workers drain or user force-archives |
| Attention queue grows past N | queue depth | escalate with a digest item to principal |

Escape hatches (already covered in §6.3): per-tool-call approval,
pause, hard stop. Plus: **kill (last-resort)** — terminates the
process without a checkpoint; reserved for stuck states.

---

## 11. Roadmap — what's needed before the MVP demo

This section is the actionable output of the doc.

### 11.1 Demo-blocking (single-agent)

Ship before the single-host demo:
- **B1. Steward bootstrap flow** (§5.1). Today's gap. ≈ 2-day
  wedge: bootstrap sheet, default-spawn YAML, auto-greet in team
  channel.
- **B2. Steward persona document** (§4.7, §6.2). Editable file in
  Team Settings → Steward; injected at session start. ≈ 1-day wedge.
- **B3. Loop with checkpoint + budget enforcement** (§6.1). Today
  the loop runs but doesn't checkpoint or preempt. ≈ 3-day wedge.
- **B4. Single-host quickstart UX** (§6.4) — measured against the
  ≤ 2.5-minute target. ≈ 2-day wedge (mostly polish on B1).
- **B5. Operating-state separation** (§5.2): split `running` vs
  `idle` vs `waiting_attention`. ≈ 1-day wedge.

Total: ~9 person-days for a complete single-agent demo path.

### 11.2 Demo-blocking (multi-agent)

Ship before the multi-host demo:
- **B6. Named return path on spawn** (§7.2, HA-A5). ≈ 1-day wedge.
- **B7. Worker report-back protocol** (§7.3). ≈ 2-day wedge (A2A
  relay already exists per memory).
- **B8. Multi-agent roster view** (§7.6). ≈ 2-day wedge on top of
  existing `agent_feed.dart`.
- **B9. Parallelism cap policy** (§7.5). ≈ 1-day wedge.

Total: ~6 person-days on top of B1–B5.

### 11.3 Post-MVP

- Skill primitive (§4.8) — schema design now, generator later.
- GEPA-style auto-skill-generation (§3.4).
- Spawn tree view (§7.6).
- Per-member steward (ia-redesign §11 F-1).
- Skill marketplace with vetting (§3.3).
- **Squads / fleet layer** — standing teams of agents organized
  around a goal, with roles, shared scratchpad, group fan-out and
  group decisions. Design memo at `../discussions/agent-fleet.md`. Layers
  above §7's burst model; not started.

---

## 12. Open questions

Not blocking; flagging for review.

1. **Should the steward be in `agents` with a flag, or its own
   `stewards` table?** Today it's a flagged `agents` row. A separate
   table makes invariants (one-per-team, no parent, no deadline)
   schema-enforced, but doubles the join surface. Lean: keep flagged
   row + a CHECK constraint until we have per-member stewards, then
   re-evaluate.

2. **Where does the persona document physically live?** Options:
   `documents` row of subtype `persona` (uniform with the rest), or
   a dedicated `agent_personas` table. Lean: documents row.

3. **Checkpoint cadence: turn-count, time, or both?** Devin's
   observation suggests both (§6.1). Open: are 30 turns / 20
   minutes the right defaults for our LLM mix?

4. **Approval-gate UX on worker-initiated tool calls.** Does the
   approval go to the steward, the user, or both? Lean: steward
   first (because the steward owns the worker), escalate to user
   only on `critical` tier.

5. **Multi-team users and steward identity.** When a user is in
   teams A and B, do they see two stewards? Yes — stewards are
   team-scoped (HA-A6 says identity persists *within a team*).
   Confirmed by ia-redesign §6.

6. **Local-LLM steward viability.** With a $5-VPS Hermes-style
   setup, is the loop + tool-budget framing strict enough to keep a
   small model on-rails? Open; informs §11.3 priorities.

7. **Skill scope at fan-out.** When a steward spawns a worker, does
   the worker inherit the steward's skill index? Lean: yes by
   default, narrowable by spawn YAML.

---

## Appendix A — terms cross-reference

| This doc | blueprint.md | information-architecture.md | hub-agents.md |
|---|---|---|---|
| Steward (§4.1) | §3.3 (agent), §6.4 | §4 (role), §6.7 (capability) | spawn target |
| Worker (§4.2) | §3.3 | §4 | spawn target |
| Harness (§4.3) | (implicit) | (implicit) | (implicit) |
| Context (§4.4) | (implicit) | — | — |
| Session (§4.5) | (implicit) | — | tmux pane |
| Memory (§4.6) | §6.7 (documents) | — | — |
| Tool surface (§4.7) | §5.3 | — | spawn YAML `tools:` |
| Skill (§4.8) | §6.7 (documents subtype, target) | — | — |

## Appendix B — sources for §3

External products and the artifacts informing our patterns:

- Claude Code: Anthropic's docs; `CLAUDE.md` and `/commands` are public conventions.
- Codex CLI: OpenAI docs.
- OpenClaw: openclaw.ai, github.com/openclaw/openclaw, AgentSkills + SOUL.md docs.
- Hermes Agent: hermes-agent.nousresearch.com, NousResearch/hermes-agent, GEPA paper (ICLR 2026 Oral).
- Cursor: docs.cursor.com/en/background-agent, cursor.com/blog/agent-best-practices.
- Devin: docs.devin.ai, Cognition AI public materials.
- Happy: happy.engineering, github.com/slopus/happy.
