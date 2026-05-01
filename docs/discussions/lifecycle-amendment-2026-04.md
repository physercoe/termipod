# Lifecycle amendment — the 2026-04-30 widening

> **Type:** discussion
> **Status:** Resolved (2026-04-30) → [ADR-001 D-amend-1](../decisions/001-locked-candidate-a.md), [ADR-016](../decisions/016-subagent-scope-manifest.md), [ADR-017](../decisions/017-layered-stewards.md)
> **Audience:** reviewers · partners · contributors looking for the *why now*
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** On 2026-04-30 the MVP demo was widened in scope from a single-phase ablation sweep to a 5-phase research lifecycle (idea → lit-review → method → experiment → paper). This doc tells the *story* — what the demo looked like before, what triggered the rethink, what the decision moment looked like, and what shipped immediately afterward. The *design* of the new shape lives in [research-demo-lifecycle.md](research-demo-lifecycle.md). This doc is the narrative complement: useful when explaining the project's evolution to a reviewer, partner, or future maintainer who asks "why did this change?"

---

## 1. State at 2026-04-23 — Candidate A locked

The original demo target ([ADR-001](../decisions/001-locked-candidate-a.md)) was a single-phase experiment: nanoGPT-Shakespeare ablation sweep, 3 model sizes × 3 optimizers, run by an `ml-worker.v1` on a GPU host, orchestrated by a research steward on the VPS, with a briefing emitted at the end. Architecture-sharp: it exercised every blueprint §9 P4 primitive — multi-host A2A relay, steward orchestration, run telemetry, artifact attach, briefing review.

Research-dull: it terminated one phase early. A reviewer watching the demo would see agents run a benchmark grid and emit a digest — accurate but not what 2026 calls "research."

This was deliberate. The MVP target was the *minimum demo that exercises the architecture*, not a production-scale eval (ADR-001 §Decision). Three discussions accumulated through the week of 2026-04-23 that pushed against this scope-minimal framing.

## 2. Pressure points (2026-04-23 → 2026-04-30)

Three threads in the transcript reshaped the calculation:

**Thread 1 — competitive landscape mid-survey (2026-04-19, deepened through 2026-04-30).** The user asked for objective competitive research before committing to the hub-mvp build ("i want to be sure it is worth to be built"). The survey covered Sakana AI Scientist v2 (workshop-level papers from agents), Google PaperOrchestra (April 2026 — given raw experimental logs + an idea, writes the paper, beats AI Scientist v2 by 39–86% on overall paper quality), the IKP/01.me case (Bojie Li, 2026 — one director, multi-agent, ~4 days, real published artifact), AIDE, MLE-Bench, STORM, claudecode-remote, Happy, Codex web. Conclusion drafted at the time: termipod's differentiator is **multi-host × multi-session × multi-engine for a single director, framed as a personal lab assistant.** That conclusion is fine, but it implicitly raises the demo bar — a personal lab assistant that can't go from idea to paper is a half-product.

**Thread 2 — open-source agent integration discussion (2026-04-29 → 2026-04-30).** A separate question — "does the architecture support open-source/GUI agents like OpenClaw, Hermes, OpenClaude, Cua, OpenCUA?" — produced [`integrating-open-source-agents.md`](integrating-open-source-agents.md). Group A (CLI coding agents) fits the existing paradigm; Groups B/C/D (messaging gateways, GUI/computer-use, hybrid) do not. The discussion confirmed the existing primitives are the right building blocks but underused — the demo path was exercising one engine kind on one phase.

**Thread 3 — director's framing (2026-04-30).** The decisive turn came as an explicit director message: *"i prefer the MVP-demo should cover all phase of research from idea to paper, it is the lifecycle of a project. we can simplify the substeps but should maintain the completeness of end2end. the steward is more like an orchestrator, the specific tasks is done by subagents, such as lit-review, coding, experiment, writing."*

The framing maps cleanly onto blueprint §6.2 — plans are linear and shallow with `human_gated` phase boundaries; iteration lives inside `agent_driven` phases. No new primitives needed. The gap was templates + prompts + a few mobile affordances, not architecture.

## 3. The decision moment

Three things made the amendment cheap enough to ship same-day:

1. **No schema migration.** Plans, phases, `human_gated` steps, A2A, documents, runs, reviews, audit_events were all already in the data model. The new lifecycle is a plan template (`research-project.v1`) referencing existing primitives.
2. **No new agent kind.** The 5 phases use 4 worker templates (`lit-reviewer.v1`, `coder.v1`, `paper-writer.v1`, `critic.v1`) plus the existing `ml-worker.v1` for phase 3. Templates are content; they're a YAML + prompt edit, not a code change.
3. **No new governance primitive.** The amendment ships alongside [ADR-016 (subagent scope manifest)](../decisions/016-subagent-scope-manifest.md) and [ADR-017 (layered stewards)](../decisions/017-layered-stewards.md), which were independently motivated but compose with the lifecycle naturally — the manager/IC invariant lets the general steward bootstrap the project without doing IC work, and the operation-scope manifest gates the workers' tool surfaces uniformly.

The amendment landed as ADR-001 §Amendment — three named decisions:

- **D-amend-1.** Locked candidate is the 5-phase lifecycle plan template `research-project.v1`. The original ablation sweep is preserved as **phase 3**, same compute, same A2A path.
- **D-amend-2.** Layered stewards: general steward (frozen, persistent) + domain stewards (overlay, project-scoped). Subsequently expanded into [ADR-017](../decisions/017-layered-stewards.md).
- **D-amend-3.** Scope-not-budget governance. Engine-internal subagents out-of-scope. Self-extension safe-by-design via prompt guardrails.

## 4. What shipped immediately

Hub-side wedges (W1/W2/W4/W5/W6 of [research-demo-lifecycle-wedges](../plans/research-demo-lifecycle-wedges.md)) were authored, reviewed, and merged on 2026-04-30 — same day the framing landed. Commits:

- `8475723` + `1d1f92f` — W1: operation-scope role gate + A2A target restriction
- `eebb119` — W2: 15 `templates.*` MCP tools + self-modification guard
- `e687b0a` — W4: `steward.general.v1` frozen template + concierge prompt + ensure-spawn endpoint
- `dd45aaf` — W5: domain steward seed rewrite + 4 worker seeds + safety guardrails
- `f1b8340` — W6: `research-project.v1` plan template + `seed-demo --shape lifecycle`

Mobile W3 partial (commit `8caff8a`) shipped the persistent steward home-tab card; template editor + phase-0 review surfaces use existing screens with documented gaps.

Same-day shippability is itself evidence the architecture was right — a properly factored system absorbs a demo widening as content edits, not as new code.

## 5. What changed (and what didn't)

| Aspect | Pre-amendment (2026-04-23) | Post-amendment (2026-04-30) |
|---|---|---|
| Demo phases | 1 (experiment) | 5 (idea → lit-review → method → experiment → paper) |
| Worker templates | 2 (`ml-worker.v1`, `briefing.v1`) | 6 (added `lit-reviewer.v1`, `coder.v1`, `paper-writer.v1`, `critic.v1`) |
| Plan template | none — single direct spawn | `research-project.v1` |
| Steward tiers | 1 (project-scoped research steward) | 2 (general persistent + domain project-scoped) |
| Phase artifact | 1 (run digest + briefing) | 5 (one per phase, gated for director approval) |
| Mobile path | spawn → review one briefing | review-and-approve at each phase boundary |
| Schema | unchanged | unchanged |
| Hub MCP tool surface | unchanged | + 15 `templates.*` tools (W2) |
| Spine docs | unchanged | unchanged |

The architecture didn't change. The product did.

## 6. What this means for new contributors

If you join the project after 2026-04-30 and read [ADR-001](../decisions/001-locked-candidate-a.md), the amendment is the operative section. Read it first; the §Decision body above is the *original* candidate, preserved for archaeology. The amendment supersedes the demo *shape* but not the *primitives* — the single-host A2A relay, the steward-spawned worker pattern, the run digest model are all still load-bearing for phase 3.

If your work touches the demo path, the canonical references (in priority order) are:

1. [ADR-001 amended](../decisions/001-locked-candidate-a.md) — what we're building.
2. [Plan: research-demo-lifecycle-wedges](../plans/research-demo-lifecycle-wedges.md) — how we're building it.
3. [Discussion: research-demo-lifecycle](research-demo-lifecycle.md) — the design rationale.
4. [How-to: run-lifecycle-demo](../how-to/run-lifecycle-demo.md) — the walkthrough.
5. This doc — the *why now* story.

## 7. For demo / external storytelling

Two paragraphs to use when explaining the amendment to a reviewer or partner:

> *"In April 2026 the multi-agent research-automation field set a new bar — Sakana AI Scientist v2 ships workshop papers from agents, Google's PaperOrchestra writes papers from raw logs, and at least one solo case (IKP/01.me) had a single director shepherding multi-agent research from idea to publication in 4 days. We had been targeting a single-phase ablation sweep as the demo because that was the minimum that exercised the architecture. The 2026 bar reframed 'minimum that exercises the architecture' as a half-demo. We widened to the full 5-phase lifecycle (idea → lit-review → method → experiment → paper) — same primitives, more content."*

> *"Architecturally this was zero risk. Plans with phases, A2A relay, run telemetry, artifact attach, document review — all already in the schema. The amendment landed as templates and prompts, not code. Hub-side wedges shipped same-day; mobile work was the only multi-day follow-up. That speed was itself the demo: a properly factored multi-agent platform should absorb a demo-widening as content, not as a refactor."*

---

## References

- [ADR-001](../decisions/001-locked-candidate-a.md) — the candidate (Original §Decision + §Amendment).
- [ADR-016](../decisions/016-subagent-scope-manifest.md) — the governance manifest the amendment relies on.
- [ADR-017](../decisions/017-layered-stewards.md) — layered stewards, expanded from D-amend-2.
- [Discussion: research-demo-lifecycle](research-demo-lifecycle.md) — the *design* of the new shape (this doc is the *story*).
- [Discussion: integrating-open-source-agents](integrating-open-source-agents.md) — Thread 2 above.
- [Discussion: multi-agent-sota-gap](multi-agent-sota-gap.md) — competitive analysis behind Thread 1.
- [Plan: research-demo-lifecycle-wedges](../plans/research-demo-lifecycle-wedges.md) — what shipped.
