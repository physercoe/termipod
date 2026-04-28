# 004. One steward per team for MVP; per-member deferred

> **Type:** decision
> **Status:** Accepted (2026-04-23)
> **Audience:** contributors
> **Last verified vs code:** v1.0.310

**TL;DR.** MVP ships with one steward agent per team. Per-member
stewards (a "deputy" for each principal) are documented as F-1 in
information-architecture.md §11 and deferred until there's a second
user.

## Context

The blueprint and IA both anticipate multi-user teams where each
member could plausibly want their own steward (different working
style, different memory, different delegations). But the MVP target
is the single-principal research demo — one user, one phone, one
hub, one team.

Building per-member stewards now would require:
- A `member_id` join on every agent/session/attention row
- Per-member memory partitioning
- Steward-of-stewards logic (which deputy handles which request)
- Multi-user permission model (whose decisions can override whose)

None of that advances the demo. The complexity is real but the
payoff is for users we don't have yet.

## Decision

MVP: one steward per team, scoped by team_id alone. Member-level
identity is not modeled in the steward path. Multi-steward
*specialization* (steward.research, steward.infra) is supported via
template variants — that's distinct from per-member instances.

The wedge for adding domain-specialized stewards (research/infra
templates) shipped as wedges 1+2 of `plans/multi-steward.md`.
Wedge 3 (per-member) is the boundary — explicitly deferred.

## Consequences

- Steward sessions are team-scoped, not member-scoped. Two members
  on the same team share the steward's history.
- Audit trail is sufficient for single-user testing; multi-user
  attribution is a Phase-5 concern.
- `information-architecture.md` §11 F-1 thread documents the
  per-member design but stays a roadmap entry, not a build target.
- If/when a second user shows up, the deferred wedge plan in
  `plans/multi-steward.md` §3 (per-member section) is the starting
  point.

## References

- Discussion: `../plans/multi-steward.md`
- IA: `../spine/information-architecture.md` §11 F-1
- Memory: `project_multi_user_stewards`
