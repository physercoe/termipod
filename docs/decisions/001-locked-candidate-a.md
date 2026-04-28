# 001. Locked Candidate-A as MVP demo

> **Type:** decision
> **Status:** Accepted (2026-04-23)
> **Audience:** contributors
> **Last verified vs code:** v1.0.310

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

## References

- Discussion: `../discussions/research-demo-candidates.md`
- Plan: `../plans/research-demo-gaps.md`
- Templates: `hub/templates/agents/steward.research.v1.yaml`,
  `ml-worker.v1.yaml`, `briefing.v1.yaml`
- Memory: `project_demo_choice_locked`
