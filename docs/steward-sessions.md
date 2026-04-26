# Steward Sessions — Ontology Draft

> **Status: DRAFT for discussion.** Lots of open questions; sections marked
> "Tentative" are starting positions, not commitments. Do not implement
> from this doc yet — agree the shape first.

This is the fourth leg of the design tripod, sibling to:

- `blueprint.md` — architecture, axioms, protocol layering
- `ia-redesign.md` — mobile information architecture
- `agent-harness.md` — how an agent is born, lives, spawns, dies

…and proposes a concept the other three currently underspecify: what
**a steward session** is, why we need that as a distinct primitive, how
a steward's *memory* lives outside any one session, and what the
lifecycle of "starting a steward conversation" should look like.

---

## 1. Why we need this doc

The current implementation treats steward as **one long-lived agent
process per team**: spawn once, chat forever, terminate only on
explicit teardown. That model has worked for demo-scale work but
predictably breaks down. Three concrete pressures:

1. **Context is finite, work is not.** A steward in active use accrues
   conversation history far past any model's window. We have no story
   for "what happens when the steward fills its context", other than
   "respawn", which loses everything.

2. **The mental model leaks**. Users (rightly) treat the steward as a
   "person who remembers". When the steward respawns, they're surprised
   it doesn't recall yesterday's decision. We've built UX that promises
   continuity the architecture can't keep.

3. **We can't cleanly attribute cost or audit a "thread of thought".**
   When a single agent process handles ten unrelated questions over a
   week, there's no clear record of *which conversation* led to which
   artifact, decision, or spawn. The audit log is per-event but lacks a
   "session" frame.

The user's framing of the problem (paraphrased):

> Humans have effectively unlimited memory; AI agents have bounded
> context. The steward's "weights" can't be where its memory lives.

That's the load-bearing observation this whole doc rests on.

---

## 2. The ontology shift, in one paragraph

A **steward** is a *role*, not a process. The role has one persistent
**identity** (handle, MCP token, capability scope, audit attribution).
Each time a user "talks to the steward", they open a **session** —
a bounded, scoped, ephemeral conversation that loads relevant
**artifacts** (templates, briefings, decisions, plans, policies) into
its system prompt, does its work, and **distills** its conclusion back
into one or more new artifacts before closing. The artifact graph —
not any conversation — is the steward's durable memory.

Three primitives:

| | Persistent? | Carries memory? | Bounded by context? |
|---|---|---|---|
| **Identity** | yes (one per team) | no, only authority | no |
| **Session** | no (open → work → distill → close) | only within itself | yes |
| **Artifact** | yes (forever, audited) | yes (the only place it lives) | no |

This mirrors how a human chief-of-staff actually works: they don't
remember every email; they have memos, calendars, decision logs. When
they "remember" something, they look it up. Their value is judgment +
context-loading, not unbounded recall.

---

## 3. Identity (unchanged from today)

The steward identity is what we already have in the `agents` table for
`kind=steward`: one row per team, with `handle`, `mcp_token`,
`default_capabilities`, audit attribution. The capability set
(`spawn.descendants: 20`, `decision.vote: significant`, etc.) is a
property of the *identity*, not of any session.

What changes: identity stops being conflated with "the running
process". A steward's row can exist without any session being open;
opening a session attaches a process to the identity for as long as
the conversation lasts.

**Open question**: do we keep one steward identity per team, or per
(team, member)? Today: per team. The IA redesign §11 F-1 sketches a
per-member deputy model, post-MVP. For sessions, the per-team identity
is still the right starting point.

---

## 4. Sessions

### 4.1 What a session is

A session is one continuous conversation between a user (or another
agent) and the steward identity, with:

- An **opened-at** timestamp.
- A **scope**: a short string + a structured pointer (e.g.
  `"plan project X"` + `{kind: project, id: …}`,
  `"approve budget for run Y"` + `{kind: attention, id: …}`).
- A **system prompt** — assembled at open time from the identity's
  default persona + a curated set of artifacts relevant to the scope.
- A **transcript** — the agent_events stream, scoped to this session.
- A **closed-at** timestamp.
- One or more **distillation artifacts** — what the session produced
  that survives it (decision, brief, plan-update, template proposal,
  or explicitly "nothing").

Sessions are not free-form. Closing a session is mandatory in the
sense that the UI prompts for distillation; users can choose
"nothing" but they have to choose.

### 4.2 Why scope matters

Scope is the contract between the user and the steward for the
session. It determines:

- **Which artifacts are loaded into the system prompt** (so context
  costs scale with session purpose, not team history).
- **Which audit-log slice is visible** (a project session sees that
  project's audit, not the whole team's).
- **Which capabilities are in scope** (a "review one decision" session
  shouldn't be able to spawn 20 descendants — even if the identity
  could).
- **What "done" looks like** (the distillation prompt is scoped: "what
  did you decide?" for a decision-class session, "what's the new plan?"
  for a plan-update session).

**Open question**: is scope declared by the user at open time, or
inferred from where they tapped "open session"? Tentative: both — the
UI seeds scope from the entry point (project page → project scope,
attention-item → attention scope), then lets the user edit. A
free-form "general" scope is allowed but discouraged.

### 4.3 Lifecycle

```
       open                   work                         close
  ┌──────────────┐  ┌──────────────────────┐  ┌────────────────────────────┐
  │ user picks   │  │ steward + user have  │  │ user picks distillation:   │
  │ scope, hub   │  │ a conversation;      │  │   • save as Decision       │
  │ assembles    │  │ steward may call     │  │   • save as Brief          │
  │ system       │  │ tools, propose       │  │   • update Plan            │
  │ prompt from  │  │ templates, request   │  │   • propose Template       │
  │ artifacts    │  │ approvals, spawn     │  │   • Nothing (audit only)   │
  └──────────────┘  │ workers, etc.        │  │ steward writes the         │
                    └──────────────────────┘  │ artifact + audits + closes │
                                              └────────────────────────────┘
```

Each transition is a hub event. The closed session's transcript
remains in the audit log (so we can reconstruct what was said) but
becomes inactive — no future session reads it as context.

### 4.4 Crash / resume semantics

**Open question**: if a session's process crashes mid-conversation, is
that the same session resumed (same id, same scope, same loaded
artifacts) or a new one? Tentative: same session — process death is
not session death. Session death is explicit close-or-distill.

Corollary: if a host-runner restarts, in-flight sessions reattach to
the new process with the same system prompt rebuild. We need to
re-stream the transcript so far back into the model, but that's
already implied by M2's stream-json contract.

---

## 5. Artifacts as memory

### 5.1 What counts

The artifact graph today is bigger than people realize. It already
contains most of what a steward needs to "remember":

| Artifact | What it remembers | Authority to write |
|---|---|---|
| Templates (agents/projects/policies) | "How we do recurring work" | Steward propose, user approve |
| Briefings (project documents) | "What we concluded about this run/sweep/issue" | Steward draft, anyone edit |
| Decisions log (audit_events kind=decision) | "What was decided, by whom, why" | Auto-recorded on resolve |
| Project plans (plans table) | "What's planned, what's blocked" | Steward draft, user edit |
| Team policies (templates/policies) | "What the user pre-approved" | Steward propose, user ratify |
| Attention items resolved | "What we said yes/no to" | Auto-recorded |
| Run metric digests | "What experiments showed" | Workers post |
| Member directory + handles | "Who does what" | User config |

What we're *missing*:

- **Session distillation as a first-class artifact kind.** Today the
  best a steward session can do is write a project document. We need
  a structured "session-summary" artifact with `(session_id, scope,
  summary, decisions[], next_steps[], referenced_artifacts[])`.
- **Conversation-fragment quoting.** A distillation should be able to
  cite specific exchanges from its session ("I asked X, steward said
  Y") so the artifact is self-explaining.

### 5.2 Loading: what goes into a session's system prompt

Tentative algorithm at session open time, scoped to (e.g.) a project:

1. The persistent steward persona prompt (who you are, how you behave).
2. The team's policy bundle (capabilities, decision rules, governance).
3. The project's plan (current state).
4. The project's most recent N briefings (truncated by token budget).
5. The most recent decisions on this project, formatted as a log.
6. A short summary of the last K closed sessions for this scope
   ("previously: …"). This is the *only* cross-session memory channel.

Token budget is a knob. If we're using Claude with 200k window, we can
afford to be generous; we still want a budget so we don't ship the
whole team's history into every session.

**Open question**: does the user see what got loaded? Yes, probably as
a collapsed "context" pill at the top of the session ("Loaded: plan,
3 briefings, 8 decisions"). Transparency matters because it lets the
user fix a bad load by restarting with edited scope.

---

## 6. Distillation

### 6.1 Why mandatory

If sessions don't distill, they leak: tomorrow's session won't know
today happened, the audit log has events but no narrative, and the
user starts treating the steward as forgetful.

But we don't want a heavy form. Tentative: the close screen offers
**three buttons** plus a "later" punt:

- "Save as Decision" — opens a one-line + reasoning form, prefilled by
  the model.
- "Save as Brief" — opens a markdown editor, prefilled by the model.
- "Update Plan" — opens the project plan with proposed deltas inline.
- "Nothing — just close" — accepted, but the audit log notes "no
  distillation chosen" so we can tell.

Whatever the user picks, the model has already drafted the content
during the close prompt. The user is approving/editing, not authoring
from scratch.

### 6.2 Who writes the artifact

The session itself writes the artifact (via existing MCP tools the
steward already has). The user approves the model's draft. No new
backend primitive needed — distillation is just a prompted
finalization step that produces an artifact write.

### 6.3 What if the user wants to keep going?

"Later" is fine. The session stays open. The UI nags gently after some
threshold (token usage, idle time) but doesn't force.

**Open question**: is there a hard ceiling? E.g. "any session over 80%
context must distill before continuing"? Tentative: yes, but framed as
"let me summarize what we've covered and continue with a clean
context", not as a teardown. Functionally a soft restart.

---

## 7. Layer-specific session patterns

The user's intuition (paraphrased): "task → one session is OK; project
→ multi-session matters; team → ?"

Tentative answers:

### 7.1 Task layer

One session per task is the natural unit. A task is small enough that
all its context fits, and the distillation is the commit / brief that
the agent produces anyway. Today we don't actually have "task
sessions" because workers run as ephemeral spawns, not steward
sessions. **Tentative: workers don't have sessions in this sense at
all.** Sessions are a steward-side concept; workers are processes
attached to tasks.

### 7.2 Project layer

Many sessions, structured as a graph keyed to the project. Each
session's distillation is an artifact attached to the project. The
"project conversation history" doesn't exist as a thing; the project
is the *graph* of sessions + briefings + decisions.

In UI terms: the project detail page should show a "Sessions" section
listing closed and open sessions for that project, scoped tightly,
with their distillations as the thumbnails — not a single ongoing
chat panel.

### 7.3 Team layer

Sessions here are about governance, not about "talking to the
steward all day". Examples of legitimate team-scope sessions:

- Weekly council ("review the audit window 2026-04-19..26").
- Budget approval ("approve next week's compute spend").
- Policy change ("propose updating the retention policy").
- New-member onboarding ("walk through team conventions").

What about ambient orchestration? **Tentative: not a session.** If we
want the steward to watch metrics and raise attention items, that's a
*program* — a scheduled tool the steward identity can run, that
reads artifacts and writes attention. Not a chat.

---

## 8. What this changes about today's app

If we accept this ontology, several current UI patterns shift.

| Today | If we accept this doc |
|---|---|
| One persistent steward chat per team | No persistent chat. Sessions are first-class, scoped, finite |
| "Open the steward" nav verb | "Start a session" + a list of recent open/closed sessions |
| Conversation history as memory | Conversation history as *transcript*; memory is artifacts |
| Closing the chat = terminating the agent | Closing a session = distill + record; identity persists |
| Implicit model: chat duration = relationship duration | Explicit model: session is a unit of work; relationship is the identity |
| Attention items shown only on Inbox | Approval cards inline in their session's transcript |
| No session-summary artifact kind | New artifact kind (or extension of project_documents) |

These are big shifts. None of them require model changes — they're
all about how the UI scopes, opens, closes, and persists conversations.

---

## 9. Boundary: what this doc deliberately does not decide

- **Per-member stewards (deputy model).** ia-redesign §11 F-1 already
  sketches this; sessions can be per-member after we adopt it.
  Per-team for now.
- **Multi-team session sharing.** A session belongs to exactly one
  team. Cross-team coordination is via artifacts (or A2A), not
  sessions.
- **A2A worker delegation in detail.** Workers spawn from a session,
  do their thing, return artifacts. The session itself doesn't
  fan-out into a graph of sub-sessions.
- **Long-running daemons (program-style steward functions).** Real and
  needed but separately specified — they live in `scheduler` /
  council infra, not under "session".
- **Streaming model swaps within a session.** If we want
  steward-on-Sonnet for cheap turns and -on-Opus for the hard ones,
  that's a session-level config knob. Not deciding it here.

---

## 10. Open questions (unresolved, ranked by how much they block code)

1. **Schema.** New `sessions` table? Or a "session" view over existing
   audit/agent rows? A real table is cleaner; the migration cost is
   small. Tentative: real table.
2. **Session id namespace.** UUID per session, attached to every
   `agent_event` and `audit_event` produced inside it.
3. **What "scope" actually serializes as.** A `(kind, id)` tuple plus
   a freeform label. Kinds enumerated: `team | project | attention |
   member | template | run | freeform`.
4. **Default-capabilities filtering.** Does the session inherit the
   identity's full capability bundle, or a scope-narrowed subset?
   Tentative: scope-narrowed by default; user can elevate within the
   session with a confirmation.
5. **Token-budget defaults at session open.** Is 80k / 120k / 160k the
   right starting point? Likely depends on Claude variant. Probably a
   per-tier config rather than a hard literal.
6. **Distillation artifact schema.** Reuse `project_documents` with a
   `kind=session_summary` flag, or a new `session_distillations`
   table? Tentative: extend project_documents, since they already
   have the right write/read paths and audit hooks.
7. **Session resume across host-runner restart.** Same session id,
   re-stream transcript on attach. Implementation is in the host-
   runner driver's reconnect logic.
8. **What happens to currently-running long-lived stewards on
   migration.** Their existing transcripts become a single "legacy
   session" each, sealed-but-readable. We don't try to retroactively
   distill them.
9. **Cost/quota accounting.** Per-session token totals are useful for
   the user; we'd want them in the close screen. Already mostly
   captured by `usage` events; just needs aggregation.
10. **UI vocabulary.** "Session" is a programming term. Mobile users
    might prefer "conversation" or "thread". Tentative: keep
    "session" in code, expose a friendlier word on screen. Decide
    later — vocab-audit problem.

---

## 11. What we'd build, in rough order, *if* we accept this

> Not a commitment. A possible sequencing for when the ontology is
> agreed. Each step should be its own design memo.

1. **`sessions` table + open/close hub endpoints.** Backend only;
   no UI yet. New session id stamped on agent_events / audit_events.
2. **Migration shim**: every existing steward agent gets a synthetic
   "open" session covering its lifetime to date. Closes when the
   agent is next replaced.
3. **Mobile session list UI.** Per project + team-scoped lists. Tap a
   session to see its transcript; tap "new session" with seeded scope
   to start one.
4. **Artifact-loading at session open.** Templating layer that turns
   scope into a system prompt bundle. Visible to the user as a
   collapsed "context" pill.
5. **Distillation close screen.** Three buttons + draft prefill via
   the existing tool calls. Mandatory step before close.
6. **Inline approval card in the session transcript.** (This was the
   Part 1 wedge from the discussion that prompted this doc; it's
   simpler to land *after* sessions exist as an entity.)
7. **Decommission the persistent-chat metaphor.** Last step, once
   sessions cover all the use cases the persistent chat does today.

---

## 12. Things to read alongside this

- `blueprint.md` §3 (axioms) — the steward role + bounded delegation
  framing this builds on.
- `agent-harness.md` — current spawn/lifecycle, which sessions wrap
  rather than replace.
- `ia-redesign.md` §6.1 (Me tab) and §11 F-1 (per-member stewards) —
  where session UI lives in the IA.
- `ux-steward-audit.md` — current steward MCP tool gaps; sessions
  multiply some of those (we'll need open/close/distill MCP tools).

---

## 13. How to give feedback on this draft

Each open question in §10 is a candidate for a follow-up doc or a
short Slack thread. The right next step is probably:

1. **Sit with the ontology in §2** for a couple of days. Does
   "identity / session / artifact" feel right? If it doesn't, the
   rest of the doc is wasted; revise §2 first.
2. **Push on §7** (layer-specific patterns). The team layer is the
   weakest section; if you have a different mental model for
   "ambient steward", record it.
3. **Don't agree on §10 yet.** Those are the implementation knobs;
   they should be settled per-step in §11, not all at once here.

— draft 1, 2026-04-26
