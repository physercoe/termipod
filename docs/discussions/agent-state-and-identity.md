# Agent identity and session lifecycle — design discussion

> **Type:** discussion
> **Status:** Resolved → `../plans/agent-state-and-identity.md`
> **Audience:** principal, contributors
> **Last verified vs code:** v1.0.319

**TL;DR.** A device walkthrough surfaced three smells in the current
session/steward design: anthropomorphic naming (`closed` reads as
"dead" for a program); affordances rendered as chrome rather than
gated on state (a "Terminate the steward" button on archived
sessions, where there is no engine to terminate); and invisible
scope (every session silently team-scoped because no UI surfaces or
sets `scope_kind`). Pulling on those threads unfolded into four
foundational questions: what is the steward's identity, what are the
state machines and their operators, how does scope work, and is
mandatory distillation load-bearing or inherited. This doc is the
deliberation log; the resolution lives in the plan linked above.

---

## 1. Trigger

Three observations from a device walkthrough on v1.0.319:

1. Tapping into a `closed` session still shows a "Terminate the
   steward" button. Tapping it would attempt to kill an engine that
   isn't attached.
2. Approval cards on the Me page show only a title with Approve /
   Deny — no way to see the action, payload, or requester chain
   before deciding.
3. There's no UI to choose or display a session's scope. The plumbing
   exists (`sessions.scope_kind`, `scope_id` columns; API params),
   but `openStewardSession` never sets them, so every steward session
   is team-scoped by default.

The first two smelled like UI bugs; the third is structural. All
three traced back to the same underlying questions about what kind
of thing the steward is, what states it can be in, and what scope a
session has.

---

## 2. The reframe — agents are programs, not people

The naming `open / interrupted / closed` reads as if the session is a
living thing that can die. But an AI agent is a program: data on
disk + a process binding. The program isn't dead when the process
exits; the data persists. Other AI tools — Claude Code, Codex,
Cursor, ChatGPT — all let users re-engage past conversations. None
have a terminal `closed` state with no path back.

The argument the user surfaced (paraphrased): *humans are processes
that can't be paused; AI agents are programs that can be paused and
resumed at will. The naming should reflect this. The current state
set is unusual — other apps don't use it.*

This reframe doesn't reject the architecture; it rejects the
**vocabulary**. The session lifecycle as designed (open → work →
distill → close) is fine as a *workflow*, but "close" implies death,
and we don't actually want death — the data stays, the audit log
stays, the artifact graph remembers.

Replacing `closed` with `archived` and treating archived as a
**resumable state** (via fork) recovers the program-shaped intuition
without breaking anything.

---

## 3. First-principles — what is the steward's identity?

Three candidates surfaced:

| Candidate | What it is | Stable across? |
|---|---|---|
| **Engine** | The running process: model + tools + connection | Process restarts: no. Model upgrade: no. |
| **Template** | The `agents` row: handle, persona, capabilities, audit attribution | Process restart: yes. Model upgrade: yes. Persona edit: yes (same row). |
| **Both** | Identity = template; engine = runtime binding | — |

The last reading wins. `agent-lifecycle.md` §103 / §388 already
commits to it: "long-lived identity; many bounded sessions." It also
matches the universal pattern across systems where identity matters
for governance:

| System | Identity anchor | Mutable contents |
|---|---|---|
| GitHub PR | repo + number | title, body, code, status |
| Linear issue | issue ID | title, status, comments |
| Slack channel | channel ID | name, members, topic |
| iOS app | bundle ID | version, code, name |
| Employee at a company | employee ID | role, name, manager |
| **termipod steward** | `agents.id` | persona, capabilities, model, sessions |

So: **steward identity = the template row**. The engine is the most
volatile property (like the running build of a service). Sessions
are bounded engagements attached to the identity. This framing
resolves the "Terminate the steward" overload — the button targets
the engine, not the identity, and so should be named "Stop session"
and gated on `session.state == active`.

---

## 4. Session state — comparison with prior art

We surveyed five comparable products to see how unusual our state
set is.

| System | States | Resume / fork? | Distillation? | First-class agent identity? |
|---|---|---|---|---|
| Claude Code CLI | running / detached | `--continue` / `--resume` | none | no |
| Codex CLI | running / saved | resume / fork | none | no |
| Cursor Agent | active / archived | open from list | none | no |
| ChatGPT / Claude.ai | open / archived / deleted | re-open | none | yes (Custom GPTs) |
| Slack bots | installed / removed | n/a (channel-scoped) | none | yes |
| **termipod (current)** | open / interrupted / closed / deleted | none from `closed` | mandatory at close | yes (steward) |

Two divergences are real and we want to keep them:

- **First-class agent identity** is shared with Slack bots and
  Custom GPTs and is required by ADR-005 (governance, audit,
  capabilities). CLI agents don't have it because they don't have
  governance; we do.
- **Mandatory distillation** is the architectural commitment to
  artifact-graph-as-memory (sessions.md §2). Without it, the
  steward becomes a transcript pile.

Two divergences are inherited and we should drop:

- **`closed` is non-resumable.** Universal-no in prior art. Replace
  with `archived` + a fork action.
- **"close" naming.** Anthropomorphic. Rename to `archive`.

One correction is straight-up a UI bug, not a design difference:

- **Affordances rendered as chrome rather than gated on state.**
  Every other product gates "stop" / "resume" / "delete" on the
  session being in the state where that action makes sense.

---

## 5. Scope — what does it mean, who picks it?

`sessions.md` §4.2 says scope determines:

| Aspect | Why it matters |
|---|---|
| Which artifacts load into the system prompt | Context budget; relevance |
| Which audit slice the model sees | Avoid leaking unrelated team context |
| Which capabilities are in scope | Tighten "review one decision" below the steward's full identity caps |
| What "done" looks like | Distillation prompt shape |

Three concrete scopes for MVP:

- **General / team** — open-ended thinking; broadest context. Default.
- **Project** — a project's plan + briefings + decisions; project-shaped distillation.
- **Attention** — one approval/decision request; tight capability set; resolution-shaped distillation.

The MVP question was: who picks the scope? Three candidates:

1. **Implicit from entry point.** Me FAB → general. Project page → project. Attention item → attention. User never thinks about scope.
2. **Explicit picker at session creation.** ChatGPT-style "pick a Project" gate. Adds one tap, makes the choice legible.
3. **Hybrid.** Implicit by default; chip in the header labels what's active; tap-to-rescope mid-session for advanced cases.

We picked (1) for MVP-demo, (3) eventually. The crucial gap today is
that (1) is *what the doc says we do* but **not what the code does** —
no entry point passes scope, so everything lands as `team`. Phase 1
wires the project entry path; the chip makes the implicit visible;
re-scoping is post-MVP.

A general-scope ("stateless-feeling") session is the right default
for "just chat with the steward" — and is in fact what's shipping
today. The work is making it *legible* (chip + label), not building
a new primitive.

---

## 6. Mandatory distillation — keep or drop?

This was the most debated decision. The argument for dropping:

- Every other AI tool lets you exit a conversation without filing
  anything. Forcing distillation is friction Claude Code users won't
  accept. It looks like bureaucracy.

The argument for keeping:

- Without distillation, sessions leak. The steward's only durable
  memory is the artifact graph. Tomorrow's session has no way to
  know today happened beyond a raw transcript dump (which doesn't
  scale, and makes the audit log a firehose). The "previously: 8
  closed sessions" cross-session memory channel (sessions.md §4.2 #6)
  evaporates.

The synthesis that won: **keep mandatory; make the escape valve
1-tap**. The current "Nothing — just close" button is the escape;
we rename it to "Nothing — just archive" and that's the entire
distillation UI for MVP. Polishing the prefilled drafts for Decision
/ Brief / Plan is post-demo work. Users who *don't* want a memory
trail can always tap "Nothing"; users who do get one tap to file a
decision.

The rename + 1-tap escape covers Claude Code's exit-without-form
pattern at equivalent UX cost.

---

## 7. The Claude Code / Codex transition

Why all of this matters: these are the products users come from.
What they expect:

| Expectation | termipod story (after this plan) |
|---|---|
| "I can resume an old conversation" | Fork from archive — pre-loads distillation + last-K transcript. One tap. |
| "Sessions survive machine restart" | ✓ already (process death ≠ session death). |
| "I can branch to explore" | Fork — same action covers branch and resume. |
| "I can list past sessions" | ✓ already, plus scope grouping in Phase 2. |
| "Closing shouldn't be a form" | "Nothing — just archive" is 1-tap. |
| "I don't want a steward — just chat" | General-scope sessions; steward identity invisible if no work assigned to it. |

If we rename, gate, wire scope, and add fork — the user who switches
from Claude Code remote finds: same primitives, plus a memory layer
when they want it. If we *don't*, they find a half-built version of
something they already have, with extra friction. Hence the
Phase-2-into-MVP retune.

---

## 8. The four questions and their resolutions

1. **What is the steward's identity?** → The template row; the
   engine is a runtime binding. (D1)
2. **What are the state sets and operators?** → Session: `active /
   paused / archived / deleted`. Steward (MVP): `active`. (D2, D3)
3. **What's missing to be competitive with prior art?** → Fork from
   archive; scope grouping in list; rename. (D4)
4. **Is mandatory distillation load-bearing?** → Yes; 1-tap escape
   preserves UX cost. (D5)

Plus: **affordances are gated on state pairs** (D6) and **scope is
implicit from entry point + visible as a chip** (D7).

These resolutions are captured as decisions D1–D7 in
`../plans/agent-state-and-identity.md` §2, with concrete gap analysis
and phased delivery.

---

## 9. Alternatives considered and rejected

For the audit trail:

- **Drop mandatory distillation entirely.** Rejected — sessions.md §2
  artifact-graph-as-memory is foundational. The 1-tap "Nothing"
  escape covers the friction concern at equivalent UX cost.
- **Keep `closed` but add a "reopen" affordance.** Rejected — `reopen`
  reuses the closed session's transcript and grows context
  monotonically. Fork-from-archive starts fresh with distillation +
  bounded transcript, which is what the artifact-graph design wants.
- **Make scope user-pickable at session creation.** Deferred —
  implicit-from-entry-point covers the common case at zero friction;
  re-scoping mid-session (Phase 3) covers the advanced case.
- **Per-conversation steward identity (no template).** Rejected —
  removes governance/audit/capabilities anchor (ADR-005). This would
  make us a Cursor-shaped product, not the principal/director-shaped
  product we're committed to.
- **Add a `disabled` steward state to MVP.** Deferred — only one
  steward per team in MVP per ADR-004; no need to disable when there's
  nothing to disable around. Surface the column in Phase 3 alongside
  per-member stewards.

---

## 10. Related

- `../plans/agent-state-and-identity.md` — resolution; concrete gaps + phases
- `../spine/sessions.md` — current canonical session doc (gets renamed in Phase 1)
- `../spine/agent-lifecycle.md` — identity = template framing
- `../decisions/004-single-steward-mvp.md` — single-steward MVP
- `../decisions/005-owner-authority-model.md` — director-vs-operator framing
- `./simple-vs-advanced-mode.md` — overlapping IA-shape discussion
