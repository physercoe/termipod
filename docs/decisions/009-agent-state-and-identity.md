# 009. Agent identity and session lifecycle

> **Type:** decision
> **Status:** Accepted (2026-04-28)
> **Audience:** contributors
> **Last verified vs code:** v1.0.319

**TL;DR.** Steward identity = the template row, not the engine.
Sessions use program-shaped vocabulary (`active / paused / archived
/ deleted`), and `archived` is resumable via a fork action.
Affordances are gated on the `(session.state, steward.state)` pair.
Scope is implicit from entry point and visible as a chip. Mandatory
distillation stays — it's load-bearing for artifact-graph-as-memory —
but the existing 1-tap "Nothing — just archive" escape preserves
Claude-Code-like UX cost. Captured here so future contributors
don't relitigate; the deliberation log lives in
`../discussions/agent-state-and-identity.md` and the work itself in
`../plans/agent-state-and-identity.md`.

## Context

Three smells surfaced from a v1.0.319 device walkthrough:

1. The session chat shows "Terminate the steward" on archived
   (`closed`) sessions, where there is no engine to terminate.
2. Approval cards on Me page show only a title + Approve/Deny — no
   detail link. Users can't decide without context.
3. No UI surfaces or sets `scope_kind`. The plumbing exists in
   `sessions` columns and the API; every steward session is silently
   `team`-scoped because no entry point passes the field.

Pulling on these threads exposed four foundational questions:
*what is the steward's identity*, *what state machines bound it*,
*how does scope work*, *is mandatory distillation load-bearing*.
The discussion doc records the deliberation, including alternatives
considered and rejected; this ADR captures only the resolutions.

## Decision

Seven decisions, bundled because they are tightly coupled — flipping
any one of them changes the others.

**D1. Steward identity = the template row.** The `agents` row
(kind=steward) carries handle, persona, capabilities, audit
attribution. The engine (model + process) is a runtime binding to
that identity. Process death is not identity death; engine swap is
not identity change. This matches `agent-lifecycle.md` §103 / §388
("long-lived identity; many bounded sessions") and the universal
identity-anchor pattern across Slack bots, ChatGPT GPTs, Linear
issues, GitHub PRs.

**D2. Session state set: `active / paused / archived / deleted`.**
Replaces `open / interrupted / closed / deleted`. The mapping is
1:1; only the names change. Anthropomorphic vocabulary
(open/closed) is replaced with program-shaped vocabulary
(active/archived) because an AI agent is data-on-disk + a process
binding, not a living thing.

**D3. Steward identity state set (MVP): `active` only.**
Post-MVP adds `disabled` (template exists, new sessions blocked) and
`deleted` (soft-deleted). MVP UI gates "open new session" on
`state == active` so the post-MVP transition is mechanical.

**D4. `fork` is a first-class operator on archived sessions.**
`POST /v1/sessions/:id/fork` creates a new `active` session
pre-loaded with the source's distillation artifact + last-K
transcript events. This is the Claude Code `--resume` / Codex
`continue` metaphor in our artifact-graph world, and is the line
between "broken-feeling" and "complete-feeling" parity with prior
art. Required for MVP, not deferred.

**D5. Mandatory distillation stays. 1-tap escape preserves UX cost.**
The artifact graph is the steward's only durable memory
(`sessions.md` §2). Without distillation, sessions leak: tomorrow's
session has no narrative summary of today, and the cross-session
memory channel evaporates. The "Nothing — just archive" button (1
tap, no form) is the escape valve for users who want Claude-Code-style
exit-without-form. For MVP, this stays as-is — polishing the
prefilled Decision/Brief/Plan drafts is post-demo work.

**D6. UI affordances are gated on `(session.state, steward.state)`,
not rendered as chrome.** The "Terminate the steward" button is the
canonical example. Same discipline applies to "Stop session,"
"Archive," "Fork," "Delete." Each has a state precondition; rendering
the button when the action is invalid is a bug, not a design choice.

**D7. Scope is implicit from entry point and visible as a chip.**
Me-FAB → general/team. Project page → project. Attention item →
attention. A scope chip in the session header surfaces what's
loaded so users aren't guessing. Re-scoping mid-session is post-MVP;
explicit scope picker on the open-from-list path is MVP.

## Consequences

- **Vocabulary churn across server + app + docs.** Plan Phase 1
  renames the enum, alias `/close` → `/archive` for one release,
  walks all UI strings. SQL migration is a single `UPDATE` over
  existing rows; no external API consumers depend on the old names.
- **Sessions become resumable.** Fork-from-archive removes the
  "closed = dead" trap. Cost: a new endpoint and a button. Benefit:
  Claude Code remote / Codex / Happy users find their muscle memory
  works.
- **Affordance gating discipline.** Every session-actions affordance
  is audited against the state-pair gating rule. This is a small
  ongoing tax on UI work; in exchange, walkthroughs stop surfacing
  "this button does nothing here" smells.
- **Scope becomes legible.** Users can tell whether they're in a
  general session, a project session, or an attention session.
  Project-page steward sessions actually load the project's plan
  and briefings into the system prompt (which sessions.md §4.2
  promised but the code did not deliver).
- **No identity-state expansion in MVP.** One steward per team per
  ADR-004; no need for `disabled` until per-member stewards land.
- **Distillation stays mandatory.** This is the load-bearing
  divergence from Claude Code / Codex / Cursor / ChatGPT.
  Justified by artifact-graph-as-memory; mitigated by the 1-tap
  "Nothing" escape.

## Alternatives considered

Brief enumeration; full reasoning in the discussion doc §9:

- **Drop mandatory distillation entirely.** Rejected — would lose
  cross-session memory.
- **Keep `closed` but add a "reopen" affordance.** Rejected — reopen
  reuses the closed transcript and grows context monotonically;
  fork starts fresh with bounded transcript.
- **User-pickable scope at every session creation.** Deferred —
  implicit-from-entry-point covers the common case at zero friction.
- **Per-conversation steward identity (no template).** Rejected —
  removes governance/audit/capabilities anchor (ADR-005).
- **Add `disabled` steward state to MVP.** Deferred to Phase 3.

## References

- Discussion: `../discussions/agent-state-and-identity.md` —
  deliberation log, prior-art comparison, alternatives
- Plan: `../plans/agent-state-and-identity.md` — gap analysis +
  phased delivery
- Spine: `../spine/sessions.md` (renamed in Phase 1),
  `../spine/agent-lifecycle.md` (D1 framing already aligned)
- Related ADRs: 004 (single-steward MVP — gates D3),
  005 (director/operator — frames identity-as-template)
