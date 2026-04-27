# Agent fleet / multi-agent orchestration

Status: **draft, not started**. Discussion-first. Companion to
`docs/agent-harness.md` §7 (which scopes "multi-agent mode" as
*one steward spawning workers on demand*) — this doc is the layer above
that: standing teams of agents, hierarchies, peer coordination, and
fleet-level operations.

> The current architecture supports independent agents (single steward,
> spawned workers, multiple stewards via the wedges shipped through
> v1.0.293). There is no first-class concept of an *agent fleet*: a
> persistent group of agents organized around a goal, with shared
> state, group operations, and an org structure. This doc is where we
> design that layer.

---

## 1. What we have today (honest baseline)

| Surface | State |
|---|---|
| **Single steward + spawned workers** | Shipped. Steward decomposes a goal, spawns workers via `agents.spawn`, workers report back via A2A. Each worker is one task, one process. |
| **Multiple stewards** | Shipped (v1.0.290–293). Each is independent; no formal coordination between them. The merged Sessions page lists them side-by-side; that's the only shared surface. |
| **A2A peer messaging** | Shipped. Agents address each other by `handle` via `a2a.invoke(handle, text)`. Strictly 1:1. |
| **Channels + documents + artifacts** | Shipped. Shared *content*, not shared *state*. Agents read/write through these primitives, but there's no notion of "the squad's current scratchpad" or "the team's working hypothesis." |
| **`agent_spawns` parent→child edges** | Shipped. Records who spawned whom, but isn't surfaced as a tree, isn't queried for rollup, doesn't influence routing. |
| **Forbidden patterns** (`blueprint.md` §7) | "Free-form agent-to-agent broadcast" is explicitly listed as forbidden. So is "shared mutable agent memory." Both close some doors that a fleet design wants opened — those rules need revisiting. |

What's **not** there:
- Standing teams of agents (a "research squad" that persists across runs).
- Role-bound agents within a team (lead / reviewer / fact-checker).
- Group operations (broadcast to a squad, vote across N agents, rollup status).
- Shared mutable state (a working hypothesis, a partial answer being refined).
- Hierarchies (steward of stewards, escalation chains).

---

## 2. What "fleet" means in our context

The word covers three distinct things that get conflated. Naming them
separately to keep the design sharp:

**(a) Squad** — a *standing* set of agents that work together on a
coherent goal over time. Members have roles ("lead", "reviewer",
"writer"). Lifetime: weeks to months. Example: the team's
**research squad** that ablates models, summarizes results, drafts
papers — three agents that come and go but the squad persists as an
addressable entity.

**(b) Burst** — an *ad-hoc* set of agents spawned for one decomposable
task, dissolved when done. Lifetime: hours. Example: a steward fans out
3 workers to evaluate 3 model variants in parallel; once the verdicts
are in, the burst is over. **This is what we have today.** Workers
report back, then terminate.

**(c) Federation** — *peer-level* coordination between agents that
don't have a shared parent. Multiple stewards (research-steward +
infra-steward) cooperating on a cross-domain task. Lifetime: indefinite.
Example: research-steward needs a deploy environment provisioned;
infra-steward owns that. They negotiate as peers, not parent/child.

**Today: bursts work, federation is informal (just A2A messaging
between stewards), squads don't exist.**

---

## 3. The minimum primitive: Squad

If we add **one** thing, it's the squad. Federation is "two squads
talking" and bursts are "a squad's lead hiring temp workers" — both
fall out of the squad primitive plus existing tools.

Proposed shape:

```
squad {
  id, team_id, name, kind ('research' | 'infra' | 'release' | …)
  goal_md            // persistent description; what this squad is for
  status             // 'active' | 'paused' | 'archived'
  created_at, archived_at?

  members[]:         // role-bound; agents can be members of multiple squads
    role ('lead' | 'reviewer' | 'writer' | …)
    agent_id         // FK
    joined_at, left_at?

  scratchpad         // shared mutable document (one document row,
                     // type='squad_scratchpad') — read+write by every
                     // member, audit-logged like any other doc
  channels[]:        // squad_channel rows: name, kind ('decisions',
                     // 'work', 'notify')
}
```

**Why each piece:**

- `kind` is the squad type — drives default channel set, default member
  roles, default tool subset. Templates ship with `squad_kinds.yaml`.
- `goal_md` is what makes a squad addressable as an entity. The lead's
  system prompt opens with it; new members read it on join.
- `members` with `role` is the org structure. A squad member is an
  agent; an agent can be in multiple squads with different roles.
- `scratchpad` is the contentious one — see §5.
- `channels` give the squad its own conversational surfaces (separate
  from the team's hub-meta and from individual agent transcripts).

**What stays unchanged:** A2A messaging between agents; sessions table;
agent lifecycle; templates. Squads layer on top, they don't replace
anything.

---

## 4. Three coordination patterns the squad needs

### 4.1 Hierarchical delegation (lead → member)

Already supported via `agents.spawn` + A2A. Squad just adds:
- Lead can spawn a temp member into the squad (auto-leaves on task
  completion).
- The squad's `goal_md` is injected into the new member's prompt.

No new primitives. ~50 LoC of glue once squads exist.

### 4.2 Peer fan-out (lead → N members in parallel)

Today: lead calls `a2a.invoke` once per worker, manually tracks
correlation. Squad version:

```
squad.fanout(squad_id, message, role_filter='member')
  → N independent A2A invocations in parallel
  → returns a correlation_id; results land on a "fanout-results"
    channel as they complete
```

Needed: server-side fanout helper that knows the squad's roster, plus a
result-aggregation surface. ~200 LoC + UI.

### 4.3 Group decision (vote / consensus)

The hard one. Examples:
- "All three reviewers must approve before merge."
- "Majority of squad picks one of these 3 model configs."

Honest design choice: **start with simple quorum, defer real
consensus.** Quorum = "N out of M members vote yes within T minutes,
otherwise the lead breaks the tie." This matches what
`policy.QuorumFor(tier)` already does for human approvals; reuse the
machinery.

```
squad.poll(squad_id, question_md, options[], quorum, timeout)
  → opens a per-member attention_item (reuses the kind='select' work)
  → tally votes as members decide
  → on quorum-met: emit decision event
  → on timeout: lead decides
```

This is a real coordination primitive but builds on existing pieces
(attention_items, the select kind, the quorum machinery).

---

## 5. Shared state — the contentious piece

Three options for "what does the squad know that no individual member
holds":

**Option A — `scratchpad` document (recommended).** One markdown doc
per squad, living in the existing `documents` table with type
`squad_scratchpad`. Every member can read and patch (via existing
`documents.update` MCP tool). Audit-logged. Conflicts resolved
last-write-wins with the audit trail showing who overwrote what.

- Pros: zero new schema; reuses the document primitive; the squad's
  state is browsable from mobile alongside other docs; conflict
  semantics are familiar.
- Cons: not real-time; concurrent edits don't merge intelligently.
  Probably fine — agents take turns, not concurrent like humans.

**Option B — append-only event log per squad.** New table
`squad_events`; members append, anyone reads. Mental model: a Slack
channel where messages can't be edited.

- Pros: durable, ordered, no merge conflicts.
- Cons: a separate primitive that overlaps `agent_events` and
  `channel_events`. Risk of "which log do I post to?" confusion.

**Option C — KV bag.** New table `squad_state` with JSON values keyed
by string. Members `get`/`set`/`delete`.

- Pros: cheap.
- Cons: schema-free state usually rots. Documents at least have
  authoring conventions.

**Lean: A.** It's the smallest viable change and reuses the conflict
story we already have for human-edited docs.

---

## 6. How squads compose into bigger structures

**Nested squads.** A squad's `lead` can itself be a squad — but for
MVP we'd say no, leads are single agents. Otherwise the org chart
becomes recursive and the rendering UX gets gnarly. Revisit when a
real use case shows up.

**Cross-squad federation.** A squad can post to another squad's
channel (with permission). A squad's lead can `a2a.invoke` another
squad's lead. We don't need a new primitive for federation — channels
+ A2A do it.

**Steward as squad-lead.** Each domain steward (research-steward,
infra-steward) becomes the lead of a squad with the same name.
"Talking to research-steward" is talking to the lead of the research
squad — the steward's UI surface stays the same; under the hood, it
has members it can fan out to. This is the cleanest migration path.

---

## 7. UI surfaces (rough)

These are sketches, not final. Let the design doc settle first.

- **Team Settings → Squads**: list, create, archive. Per-squad: roster,
  scratchpad link, channel list.
- **Sessions page (the merged stewards page)**: each steward section
  optionally shows a "Squad" pill that opens the squad detail.
- **Squad detail screen**: roster (with status pills), scratchpad
  preview, recent decisions, recent fan-outs.
- **Agent feed**: when a member is mid-fan-out or mid-poll, surface the
  group context inline ("waiting for 2/3 votes on [topic]").

---

## 8. Forbidden patterns (revisit blueprint §7)

`blueprint.md` §7 lists "free-form agent-to-agent broadcast" and
"shared mutable agent memory" as forbidden. The squad design needs
both, in bounded forms:

- **Broadcast within a squad** is fine. Broadcast across a team is
  still forbidden (use channels + subscriptions).
- **Mutable shared memory within a squad** (the scratchpad) is fine.
  Mutable shared memory across squads is still forbidden (use
  documents + read access).

So the rule becomes: *within a squad, these are first-class; outside a
squad, they're forbidden*. Update §7 accordingly.

---

## 9. What this doesn't try to do

- **Not solving real consensus** (Paxos, Raft). Quorum + timeout is
  enough for "did we agree" semantics. Real consensus is overkill.
- **Not building a workflow engine.** Plans (`plan_steps`) are the
  workflow engine; squads are the *who*, plans are the *what-then-what*.
- **Not auto-organizing.** A human (or steward) explicitly creates the
  squad, names members, sets the goal. We're not learning org structure
  from execution traces.
- **Not federation by default.** Squads are team-scoped. Cross-team
  squads are post-post-MVP.

---

## 10. Wedge plan (sketch)

If we ship squads, this is roughly the order:

1. **Schema + CRUD.** `squads` table + `squad_members` table + REST
   endpoints. ~300 LoC server, ~150 mobile (Squads list under Team
   Settings).
2. **Squad scratchpad** as a document subtype. ~50 LoC.
3. **`squad.fanout` MCP tool.** Steward calls it; server handles the
   parallel A2A invocations + result channel. ~250 LoC.
4. **`squad.poll` MCP tool** + reuse of `kind='select'` attention
   per-member. ~150 LoC.
5. **Mobile UI**: Squad detail screen, roster pills on the steward
   chip. ~400 LoC.
6. **Steward template wiring.** Each domain steward becomes the lead
   of a same-named squad on first spawn; updates the prompt to mention
   the squad. ~50 LoC.

Total: ~5–7 wedges. Roughly the same shape as the multi-steward
sequence (handful of small commits, each shippable).

---

## 11. The single decision before code

**Do we ship squads at all, or stay with bursts + multi-steward?**

The question is whether the user's actual use case justifies a standing
team primitive. If the demo flow ("research-steward decomposes →
spawns 3 workers → workers report → briefing summarizes") works fine
with bursts and multi-steward, squads are over-engineering for the MVP.

Concrete signals that would say "yes, squads":
- The user wants persistent reviewer agents that critique every
  research run, not one-shot reviewers spawned per run.
- Multi-step research goals where the same agents iterate over weeks.
- Cross-domain projects where research-steward and infra-steward must
  routinely coordinate (more than once per project).

Concrete signals that would say "no, stay with bursts":
- The user's workflow is "fire and forget" — spawn workers, get
  results, throw them away.
- The principal prefers explicit hierarchy (one steward, talks to
  everyone).
- Adding squad UX would clutter the merged Sessions page.

Discuss before committing.

---

## 12. Things to read alongside this

- `agent-harness.md` §4.9 (manager/IC layer split) and §7 (multi-agent
  mode) — the foundation this layers on top of.
- `blueprint.md` §3.3 (agent ontology) and §7 (forbidden patterns —
  the ones that need revisiting).
- `wedges/multi-steward.md` — the sibling design that just shipped.
  Squads are the next layer above multi-steward.
- `docs/research-demo-candidates.md` §… on LangGraph Supervisor,
  CrewAI etc. — prior art we should learn from before designing
  this in detail.

---

## 13. Open questions

1. **Squad vs project.** A `project` has agents working on it; is a
   squad just "the agents currently assigned to a project"? Or are
   they orthogonal (agents can be in multiple projects via squad
   membership)? Lean: orthogonal — projects are *what*, squads are
   *who*.
2. **Squad templates.** Should `squad_kinds.yaml` define default
   roster shape (one lead + 2 reviewers + 1 writer)? Or always
   user-authored?
3. **Steward identity.** When research-steward is the lead of the
   research squad, is that one entity or two? Lean: one — the
   steward IS the squad lead, no separate "lead" agent.
4. **Cross-team squads.** Out of scope for now; flag for later.
5. **External (human) members.** Can a user be a "member" of a squad?
   The user is already the principal — they have authority over
   everything. Probably not a squad member; squads are for agents.
