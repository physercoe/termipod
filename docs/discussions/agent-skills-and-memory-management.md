# Agent skills and memory management

> **Type:** discussion
> **Status:** Open (2026-07-05) — director directive: the app must include
> **skills** and **memory** management, since both are core elements of modern
> agent systems. Companion to [ADR-050](../decisions/050-desktop-workbench-delivery-model.md)
> and the [research-material data model](research-material-data-model.md).
> **Audience:** contributors · maintainers · principal
> **Last verified vs code:** v1.0.820

**TL;DR.** Two first-class surfaces, each grounded in something TermiPod already
has. **Skills:** a skill is structurally `{instructions + tool/MCP refs +
optional code + metadata + scope}` — which is *already* the pairing of an
**agent-family YAML** ("playbook") with the **two-registry tool catalog**
("verbs"), under the "behavior is data" axiom. So a skills surface is a **BUILD**
management/versioning/discovery layer over existing primitives, **INTEROP** on the
open **SKILL.md** standard, no new execution mechanism. **Memory:** every 2025-26
memory system converged on `{typed item + scope key + provenance + retrieval index
+ consolidation}` — structurally *identical* to the ResearchElement. So memory and
the research-material store are **one knowledge substrate with a scope column**,
differentiated by type/scope/consolidation, not a second schema; a finding an
agent remembers can **graduate** into the director's research materials without a
copy. Governance follows Zep/Graphiti's **append-with-supersession** (temporal
validity, nothing hard-deleted, provenance on every item), which fits TermiPod's
`audit_events` + propose→approve. Consolidation is a host-runner **idle-time job**
(the infra already models paused/idle agents).

---

## 1. Why these are first-class, and the two insights

In 2026 agent systems, **skills** (reusable capability packages) and **memory**
(durable, retrievable knowledge) are core, not peripheral — Claude Code, Claude
Science, Letta, and the MCP/SKILL.md standardization all treat them as primary.
A director's workbench must let the human **see and govern** what the fleet can
*do* (skills) and what it *knows* (memory). Two insights make this cheap rather
than a from-scratch build:

- **A skill is already our YAML + tool-catalog pair.** Confirmed across every
  system surveyed (Anthropic Agent Skills, Claude Code plugins/subagents, MCP,
  even Voyager's learned functions): a skill = `{instructions/prompt + tool/MCP
  refs + optional code + metadata(name/description) + scope}`. Anthropic's own
  framing — **"MCP = verbs, Skills = playbooks"** — maps one-to-one onto TermiPod:
  the two-registry catalog (`ToolSpec` + native tools) is the *verbs* layer
  (already MCP-shaped); **agent-family YAML** is the *playbook* layer. A skills
  surface adds discovery/versioning/scoping *over* that YAML — not a new runtime.
- **Memory is the same shape as a research element.** Every serious memory system
  (Letta/MemGPT, Zep/Graphiti, mem0, LangMem, Cognee) converged on the same
  ingredients as the [research-material element](research-material-data-model.md):
  typed items, an explicit **scope** key, a **provenance** pointer, a **retrieval**
  index, and a **consolidation** process. One substrate can serve both.

## 2. Skills

### Model and mapping

A skill maps onto existing primitives with no new abstraction:

| Skill part | TermiPod primitive |
|---|---|
| instructions / playbook body | **agent-family YAML** (and templates) — "behavior is data" |
| tool / MCP references | the **two-registry catalog** (`ToolSpec` + native tools), by id |
| optional bundled code | scripts alongside the family (as SKILL.md allows) |
| metadata (name/description) | frontmatter |
| scope | agent-family / project / global (already how families are scoped) |

Proposed record: `Skill { id, name, description, scope{agent-family|project|
global}, body(markdown, SKILL.md-shaped), tool_refs[catalog ids], bundled_files,
version, status(draft|approved|deprecated), created_by, approved_by, usage_stats,
source(built-in|imported|learned) }`.

### The management surface

**Author** (edit the SKILL.md-shaped body + attach tool refs) · **version**
(per-skill, file-based + commit-pin, like Claude Code marketplaces) · **scope**
(family/project/global) · **enable/disable** · **usage telemetry** (invocation
count, success rate, last-used) · **audit** (reuse propose→approve) · **curate**
(promote draft→approved) · **consolidate** (flag/merge near-duplicate skills).
The domain **connector packs** from the landscape — bio, and the embodied-AI /
Isaac Lab pack — are just skills.

### Posture

- **BUILD** the authoring/catalog/versioning UI — thin CRUD + versioning over
  agent-family YAML and existing tool refs; this *is* TermiPod's governance
  pattern already (propose→approve, `audit_events`).
- **INTEROP** on **SKILL.md** (open standard since 2025-12; agentskills.io) — store
  skill bodies in that format so they are portable and directly Claude-Code-
  consumable. Reuse existing catalog ids for tool refs (no change).
- **INTEROP/adopt** the **MCP** direction — the spec is mid-transition (RC locked
  2026-05-21, final 2026-07-28) toward a **stateless core + formal Extensions**,
  now under the Linux Foundation's Agentic AI Foundation; target the Extensions
  model, not the older spec's assumptions. MCP's tool-level governance (the
  `tools/list` surface as a security boundary, central gateway over per-agent
  allow-lists) *validates* our two-registry design.
- **Reject** external skill-marketplace INTEGRATE — none carry TermiPod's scoped
  propose→approve governance; build native browsing instead.
- **Future (not now):** **learned skills** — a steward promoting a repeatedly-
  successful ad-hoc script into a durable skill (Voyager lineage: code + embedding
  + auto-description). This remains academic (SAGE, SkillOps, SkillDAG…), no
  shipped product yet — a real direction for TermiPod, but don't build against an
  unstable pattern. The `source: learned` field reserves the seam.

## 3. Memory

### Model

The converged 2026 pattern, adopted: `Memory { id, type(fact|episode_summary|
preference|procedural_note), scope, content, embedding, provenance{source_session,
source_turn_seq, author(agent|human|system)}, valid_at, invalidated_at,
superseded_by, status, retrieval_count, last_retrieved_at, links[] }`.

Four memory types (working/short-term is the live context window, not stored):
**semantic** (facts), **episodic** (episode/session summaries), **procedural**
(how-to notes — LangMem even rewrites prompts from these), **preference**.

### Governance — append-with-supersession

Two governance families exist: contradiction-driven **invalidation with full
history retained** (Zep/Graphiti — `valid_at`/`invalidated_at`, nothing hard-
deleted, every item carries a source-episode provenance pointer) vs. LLM-mediated
CRUD with **no audit trail** (mem0 — independently found internally inconsistent).
Given TermiPod's existing `audit_events` + propose→approve, the **Graphiti
append-with-supersession** pattern is the architectural fit: editing memory
*supersedes* (never overwrites), everything is provenance-stamped and auditable,
and `status` can mute-without-delete.

### The management surface

Mostly system/agent-authored with **human override**: view what an agent/steward/
project knows · edit/merge/**quarantine** · scope · **provenance + audit** (source
turn/session, author) · usage stats (retrieval count/last-retrieved) · supersession
history · consolidate. This is the piece Claude's file-only CLAUDE.md pattern
(which TermiPod's own dev-side `MEMORY.md` mirrors) structurally *cannot* give —
a file concatenated at load has no cross-agent, cross-host, retrievable, governed
index. The hub-level memory store is worth building precisely because file-only
memory can't be retrieved or governed across the fleet.

### Posture

- **BUILD** the store + management surface, **sharing the schema with the
  research-element store** (§4) — this is the single biggest leverage point from
  the research.
- **INTEGRATE** an embedded vector index (sqlite-vec-class) — not a Neo4j-class
  graph DB; unjustified at current scale and it would break local-first.
- **No INTEROP standard exists** — memory schemas (Letta blocks / mem0 CRUD / Zep
  facts) are mutually incompatible and immature. BUILD on the converged *pattern*;
  don't adopt any vendor schema wholesale.

## 4. The unified knowledge substrate

The load-bearing architectural call: **one knowledge store, a `scope` column, two
audiences** — rather than parallel memory and research-material engines (which
would duplicate retrieval + backlink + provenance three times).

- Agent **memory** and research **materials/elements** share the record shape
  (typed item + scope + provenance + embedding + links + temporal validity) and
  the **retrieval engine** (hybrid BM25 + dense + rerank over one index, filtered
  by `type`/`scope`).
- They **differ by scope value and consolidation policy, not schema** — memory
  scope keys are agent-ish (agent/steward/session/project), material scope keys
  are durable (project/document); Zep's `user_id`/`group_id` split and Letta's
  cross-agent shared blocks are precedent for one engine serving both via a scope
  *column*.
- **The graduation path** falls out for free: a finding an agent surfaces in its
  memory can be **promoted** into the director's research materials (change scope /
  `wasDerivedFrom` link), because they are the same substrate — exactly the
  reading-surface "deposit a note-card" idea, generalized across agent and human
  authorship.

This resolves fork §9.5 of the [data-model doc](research-material-data-model.md)
toward **one table with a discriminator**, not two.

## 5. Consolidation as an idle-time job

Letta's "sleep-time compute" (a background pass reorganizing memory while the
agent is idle) is the production analog for **consolidation**. TermiPod's host-
runner **already models idle/paused agent states**, so a background hub job on
idle/pause transitions needs no new infrastructure — and the *same* job does
double duty: **memory consolidation** (dedupe, summarize episodes, supersede
stale facts) **and research-element backlink discovery** (the incubation/
resurfacing mechanism from the reading doc). One consolidation loop over the
shared substrate.

## 6. Register (skills + memory rows)

| Capability | Posture | Concretely |
|---|---|---|
| Skills authoring / catalog | **BUILD** | CRUD + versioning UI over agent-family YAML + tool refs |
| Skill format | **INTEROP** | SKILL.md (portable, Claude-Code-consumable) |
| Tool references | **INTEROP** | existing two-registry catalog ids (no change) |
| MCP direction | **INTEROP/adopt** | target the stateless-core + Extensions RC |
| Learned skills | **future (seam only)** | `source: learned`; steward promotes successful scripts |
| Memory store + surface | **BUILD** | shared substrate with the element store; supersession + audit |
| Memory vector index | **INTEGRATE** | embedded sqlite-vec-class, local-first |
| Consolidation | **BUILD** | host-runner idle-time job (memory + element backlinks) |

## 7. Open questions / forks

1. **One table or two over a shared engine** — a single `knowledge_item` with a
   `kind(memory|element)` + `scope`, vs. two tables sharing the retrieval/backlink
   layer. (Lean: one table, discriminator.)
2. **Skill vs. agent-family boundary** — is a "skill" a first-class row, or a
   view/overlay on existing agent-family YAML + tool refs? How versioning
   interacts with the family templates.
3. **Governance depth** — does memory mutation route through propose→approve
   (heavier, fully audited) or a lighter agent-writes-human-curates flow with
   audit-only? Likely tiered by scope.
4. **Retrieval unification** — the memory index and the research-element index are
   the same index; confirm one embedding space serves both audiences.
5. **Learned-skill promotion trigger** — when does a repeated ad-hoc script earn
   `source: learned` promotion (success-count threshold, steward proposal)?

## Related

- [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) — the workbench
  these surfaces live in.
- [`research-material-data-model.md`](research-material-data-model.md) — the shared
  knowledge substrate; this doc resolves its §9.5 fork toward one table.
- [`research-tooling-landscape.md`](research-tooling-landscape.md) — the master
  build/embed/integrate register.
- [`decisions/033-tool-catalog-naming-and-registration.md`](../decisions/033-tool-catalog-naming-and-registration.md)
  + [`031`](../decisions/031-agent-tool-ergonomics.md) — the two-registry tool
  catalog (the "verbs" a skill references).
- [`spine/blueprint.md`](../spine/blueprint.md) — the "behavior is data" axiom that
  makes skills editable YAML, and the `audit_events` governance memory rides on.
