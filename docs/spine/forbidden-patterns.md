# Forbidden patterns

> **Type:** axiom
> **Status:** Current (2026-05-05)
> **Audience:** contributors, reviewers
> **Last verified vs code:** v1.0.351

**TL;DR.** Corollaries of the [`blueprint.md`](blueprint.md) axioms
and the data-ownership law. Violating any of these signals a design
regression; a PR that does so requires explicit amendment of
`blueprint.md` first. Extracted from the original `blueprint.md` §7
(P1.6 doc-uplift refactor). For mobile-IA-specific forbidden patterns
see [`information-architecture.md §8`](information-architecture.md).

---

## The list

1. **Hub stores bulk bytes.** Violates A2 + data-ownership law.

2. **Host-runner runs an LLM loop or makes stochastic decisions.**
   Violates A3 (erases the deterministic boundary).

3. **Agents open direct network connections to the hub.** Violates
   containment; breaks air-gapped operation; multiplies token surface.

4. **Policy lives on hosts and drifts from hub.** Violates A3.

5. **Agents coordinate via shared files or undocumented channels
   outside A2A + hub channels.** Destroys provenance. *Exception
   under design ([`../discussions/agent-fleet.md` §5](../discussions/agent-fleet.md)):*
   a squad's shared scratchpad lives in the existing `documents`
   table with audit semantics, so it stays on the audit trail. The
   forbidden case is the *unaudited* shared file, not "shared state
   per se." When squads land, this rule reads "no shared state
   outside A2A, hub channels, OR squad-scoped documents."

6. **App parses ANSI from the pane as the primary agent view.**
   Fights AG-UI; the default surface must be typed events, not raw
   bytes.

7. **New REST endpoint on hub that agents will call directly.** Hub
   capabilities consumed by agents must be MCP tools, accessed via
   host-runner relay.

8. **`directives` reintroduced as a separate primitive.** Already
   unified under projects; forking will fragment queries and audit.

9. **Metrics written to hub.** Metrics live on host via trackio; hub
   holds only the run's trackio URI.

10. **A2A bypassed for cross-host agent delegation.** Invents a worse
    agent-card and task model.

11. **Schedules spawning agents directly.** Schedules must
    instantiate a plan from a template. Direct
    `agent_schedule → spawn` bypasses the reviewable plan scaffold
    and loses routine-execution history.

    *Why this rule exists:* a schedule is a recurring promise to run
    *something*; the question is what. Letting cron call
    `agents.spawn` treats every recurrence as a fresh atomic action
    with no prior structure — no plan to review, no record of "this
    is the third weekly briefing run," no way for the principal to
    ratify the *category* of work versus a one-off. Instantiating a
    plan from a template gives every recurrence a structured
    scaffold (phases, `human_gated` boundaries, audit lineage),
    keeps the principal's review surface uniform across one-shots
    and recurrences, and lets the user see "this Monday's run"
    alongside "last Monday's run" as sibling plan executions
    instead of unrelated agent rows. The audit feed
    (`reference/audit-events.md`) records `schedule.run` →
    `plan.create` rather than `schedule.run` → `agent.spawn` for
    the same reason: the plan is the unit the principal cares
    about, not the agent that happens to execute it.

12. **One-shot LLM calls modeled as agents (`M3` as a "mode").** An
    invocation without a persistent session is a `llm_call` plan
    step, not an agent. Forcing it into `agents` pollutes lifecycle
    queries and audit.

13. **Plans containing loops, conditionals, or DAGs at the plan
    level.** Dynamic behavior belongs inside `agent_driven` phases
    where a steward decides, bounded by budget and policy. Plans
    stay shallow and reviewable.

14. **Host-runner inferring or probing billing context.** The user
    declares billing per agent-family per host. Host-runner probes
    binary presence and version only. Mixing the two reintroduces
    provider-specific logic into the deputy layer.

15. **Hub storing SSH credentials to help with Enter-pane.** Only
    non-secret `ssh_hint_json` (hostname, port, username) may live
    in the hub. Secrets stay in the phone's secure storage.

---

## Cross-references

- [`blueprint.md`](blueprint.md) — axioms, ontology, data-ownership
  law (the "why" each forbidden pattern follows from)
- [`protocols.md`](protocols.md) — protocol layering (rules 3, 6, 7,
  10 follow from)
- [`information-architecture.md §8`](information-architecture.md) —
  mobile-IA-specific forbidden patterns
- [`../reference/data-model.md`](../reference/data-model.md) —
  primitives (rules 8, 9, 11, 12, 13 reference)
- [`../reference/audit-events.md`](../reference/audit-events.md) —
  audit emission (rule 11 cites)
- [`../discussions/agent-fleet.md`](../discussions/agent-fleet.md) —
  squad design (rule 5 exception)
