# 001. Locked Candidate-A as MVP demo

> **Type:** decision
> **Status:** Accepted (2026-04-23) · **Amended (2026-04-30)** — see §Amendment
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** The MVP research demo is a nanoGPT-Shakespeare optimizer ×
size sweep, with a steward on the VPS host orchestrating ml-worker
spawns on a GPU host via A2A. Candidates B and C are retired as
demo paths.

## Context

`discussions/research-demo-candidates.md` proposed three demo shapes
honoring the blueprint §9 P4 commitment. Comparing across them:

| Candidate | Shape | Why considered |
|---|---|---|
| **A** | nanoGPT-Shakespeare sweep, steward → ml-worker via A2A across hosts | Smallest path that exercises the full multi-host orchestrator loop |
| **B** | Single-host paper-reproduction agent | Easier ops, but doesn't exercise A2A relay or orchestration |
| **C** | Bigger benchmark battery | Demonstrates more, but cost and runtime out of scale for the demo |

The MVP target is the *minimum demo that exercises the architecture*,
not a production-scale eval. Candidate A is the smallest path that
hits multi-host A2A, steward decomposition, run telemetry,
artifact attach, and briefing — i.e. every primitive the blueprint
considers load-bearing for P4.

## Decision

Lock Candidate A. nanoGPT on a tiny Shakespeare corpus, sweep over
3 model sizes × 3 optimizers (= 9 runs). Steward lives on the VPS
host; ml-worker.v1 spawns on the GPU host; both reach each other via
the hub's A2A relay (`decisions/003-a2a-relay-required.md`).

Briefing.v1 writes a Goal / What ran / Plot / Takeaway / Caveats doc
that surfaces in the mobile Me tab via the existing reviews flow.

## Consequences

- All P4 wedges are scoped to Candidate A. Candidate B/C work doesn't
  count toward demo readiness.
- Templates `steward.research.v1`, `ml-worker.v1`, and `briefing.v1`
  are the demo's dependency graph. Other templates (steward.infra,
  steward.v1) remain for testing/coverage but aren't in the demo path.
- "Demo readiness" has a concrete shape we can check off
  (`plans/research-demo-gaps.md`) — we're not chasing a moving target.
- Closes a long-standing roadmap ambiguity: the demo isn't open-ended.

## Amendment (2026-04-30)

Triggered by a re-survey of the 2026 multi-agent research-automation
landscape (Sakana AI Scientist v2, Google PaperOrchestra, IKP/01.me
case study) — the original candidate covered only the *experiment*
phase of a research lifecycle, terminating at briefing. The 2026 bar
is end-to-end: idea → lit-review → method → experiment → paper, agent-
authored, director-gated. The amendment locks the *full lifecycle* as
the demo while preserving the original ablation sweep as one phase
inside it. See
[`discussions/research-demo-lifecycle.md`](../discussions/research-demo-lifecycle.md)
for the full design.

**D-amend-1. Locked candidate is the 5-phase research lifecycle.**

The demo target is now plan template `research-project.v1`:

| # | Phase | Output |
|---|---|---|
| 0 | Bootstrap (general steward authors plan + templates) | Plan proposal + draft templates |
| 1 | Lit Review (1–3 `lit-reviewer.v1` workers) | Lit-review document |
| 2 | Method & Code (`coder.v1`, optional `critic.v1` loop) | Frozen experiment spec + code commit |
| 3 | Experiment (the original Candidate A — `ml-worker.v1` × N via A2A) | Run digests + result summary |
| 4 | Paper (`paper-writer.v1`, optional `critic.v1` revise-loop) | Paper document |

Each phase boundary is a `human_gated` step — director approves on
phone before next phase begins. Iteration lives **inside**
`agent_driven` phases per blueprint §6.2; the plan stays linear and
shallow.

The original ablation sweep (3 sizes × 3 optimizers) is preserved as
phase 3 — same compute, same A2A path, same `ml-worker.v1` template.
Phases 0/1/2/4 are net-add.

**D-amend-2. Layered stewards: general (frozen, persistent) + domain
(overlay, project-scoped).**

Two steward kinds:

- **General steward** (`steward.general.v1`) — bundled in hub binary,
  frozen. **One per team, persistent, always-on.** Bootstraps new
  projects (authors domain-steward and worker templates + plan in
  phase 0), then remains available as the director's concierge for
  cross-project debugging, free discussion, template/schedule edits,
  and future-project bootstraps. Archived only by manual director
  action.
- **Domain steward** (`steward.research.v1`, `steward.infra.v1`,
  `steward.briefing.v1`, …) — overlay-authored by the general steward,
  editable by the director. Project-scoped lifetime; archived at
  project completion.

This pattern fits the existing single-agent-bootstrap-window framing
(`agent-lifecycle.md` §6.2): the general steward operationalises the
bootstrap window. It extends it: the general steward does not exit at
window's close — it delegates project orchestration to the domain
steward and stays available. Manager/IC invariant (`spine/blueprint.md`
§3.3) holds: general steward authors *infrastructure* and *advises*; IC
work is delegated to workers spawned by domain stewards.

**D-amend-3. Scope-not-budget governance for MVP; safe-by-design self-
extension; engine-internal subagents out of scope.**

Three governance commitments for MVP:

- **Scope, not budget.** Per-tool budget enforcement is deferred.
  The only governance line is the operation-scope manifest
  ([ADR-016](016-subagent-scope-manifest.md)) — which `hub://*` tools
  each role may call. Default engine tools (Bash/Edit/Read/Write/
  WebSearch/WebFetch/engine-internal `Task`) are fully open.
- **Safe-by-design self-extension.** Agents may install tools they
  need (PyPI/apt/official-releases), but only from authoritative
  sources; no API-key-bearing operations in MVP; safety is prompt-
  encoded as guardrails, not infrastructure. `attention.request_secret`
  and analogous secret-bearing flows are deferred.
- **Engine-internal subagents are not termipod-managed.** claude-code
  `Task`, codex app-server children, and analogous mechanisms share
  their parent agent's MCP client and inherit its scope by
  construction. Termipod does not enumerate, restrict, or monitor them.
  See ADR-016 D5.

**Templates as overlay, authored by the steward.** Per-team template
overlay at `<DataRoot>/teams/<team>/templates/{agents,prompts,plans}/`.
Hub binary ships seed templates (`embed.FS`); the general steward
copies seeds to overlay on first project create; hub never overwrites
team overlay after that. Director can edit overlay templates anytime
via the mobile template editor.

## References

- Discussion: `../discussions/research-demo-candidates.md` — original candidates A/B/C
- **Discussion: `../discussions/research-demo-lifecycle.md`** — amendment design
- **ADR-016: `016-subagent-scope-manifest.md`** — operation-scope governance the amendment relies on
- Plan: `../plans/research-demo-gaps.md` — original tracker (phase 3 still applies)
- **Plan: `../plans/research-demo-lifecycle-wedges.md`** — amendment implementation plan
- Templates (existing): `hub/templates/agents/steward.research.v1.yaml`,
  `ml-worker.v1.yaml`, `briefing.v1.yaml`
- Templates (forthcoming via amendment): `steward.general.v1.yaml`,
  `lit-reviewer.v1.yaml`, `coder.v1.yaml`, `paper-writer.v1.yaml`,
  `critic.v1.yaml`, plan `research-project.v1.yaml`
- Memory: `project_demo_choice_locked`
