# 008. Adopt the SOTA orchestrator-worker pattern (6-item slice)

> **Type:** decision
> **Status:** Accepted (2026-04-27, shipped v1.0.296)
> **Audience:** contributors
> **Last verified vs code:** v1.0.310

**TL;DR.** Termipod stewards drive the production-validated
orchestrator-worker pattern (Anthropic's research system, LangGraph
Supervisor, CrewAI hierarchical): structured worker contract,
synchronous fanout/gather waves, typed worker reports, anti-pattern
guardrails. Squads / standing teams remain explicitly out of MVP.

## Context

`discussions/multi-agent-sota-gap.md` ran a survey of the five
production frameworks (Anthropic, LangGraph, CrewAI, OpenAI Agents
SDK, Devin) and identified the convergent pattern:

| Property | Consensus | Why |
|---|---|---|
| Topology | Star (orchestrator center, workers leaves) | Routing is auditable; worker↔worker rare in practice |
| Coordination | Synchronous waves (dispatch batch → wait → decide next) | Simpler than async; no race conditions |
| Worker contract | objective + output format + tools + boundaries | Vague contracts → duplicated work + scope creep |
| Memory | Each worker its own context; orchestrator its own | Free-form sharing remains unsolved |
| Cross-agent transfer | Via *artifacts* (reports, structured outputs) | Audit trail + reproducibility |
| Splitting strategy | By independent subtasks, not by task type | Type-based decomposition causes "telephone game" failures |
| Sweet spot | 3–4 workers per orchestrator | Manager routing quality declines past that |

Termipod had the schema and primitives but didn't enforce the
patterns. The doc identified 9 gaps; 6 of them
(structured-contract / fanout / gather / typed-report / synchronous-recipe /
anti-pattern-guardrail) were small-work × high-value.

## Decision

Ship the 6-item slice as one wedge. Specifically:

- **Gap 1 — structured worker contract.** Steward template's
  worker-spawn recipe always structures persona seed as
  `GOAL / OUTPUT / TOOLS / BOUNDARIES / DONE WHEN`.
- **Gap 2 — `agents.fanout` MCP tool.** Creates N agents in one
  transaction with auto-opened sessions; returns
  `agent_ids + correlation_id`.
- **Gap 3 — `agents.gather` MCP tool.** Long-polls server-side until
  all `correlation_id` workers reach a terminal state or timeout.
- **Gap 4 — `worker_report.v1` schema + `reports.post` MCP tool.**
  Typed report frame validated server-side (status / summary_md /
  output_artifacts / budget_used_usd / next_steps).
- **Gap 5 — synchronous-wave recipe in the steward prompt.**
  Concrete fanout → gather → synthesize → repeat sequence.
- **Gap 8 — anti-pattern guardrail.** Steward template explicitly
  forbids type-based decomposition ("planner agent + coder agent +
  tester agent"); requires per-independent-subtask spawning.

## Consequences

- Steward can drive an orchestrator-worker pattern that matches
  Anthropic's research-system shape on top of our existing schema +
  audit + observability.
- Defaults match SOTA discipline; less prompt-engineering pressure on
  the steward to invent the right approach turn-by-turn.
- ~350 LoC server + ~150 mobile + 1 prompt rewrite.
- Three gaps deferred:
  - Gap 6 (manager-roster cost caching) — only matters once token
    bills matter.
  - Gap 7 (failure-aware steward loop) — only matters once projects
    span multiple agents over hours/days.
  - Gap 9 (squads / standing teams) — confirmed deferred per
    `../discussions/agent-fleet.md`.

## References

- Discussion: `../discussions/multi-agent-sota-gap.md` §4 + §5
- Code: `hub/internal/server/mcp_orchestrate.go`,
  `hub/templates/prompts/worker_report.v1.md`,
  `hub/templates/prompts/steward.v1.md` (decomposition recipe)
- Migration: `hub/migrations/0029_sessions_correlation_id.up.sql`
- External: [Anthropic — How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
