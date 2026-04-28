# Agent state and identity — naming, state machines, scope

> **Type:** plan
> **Status:** Proposed (2026-04-28; pending owner approval)
> **Audience:** contributors
> **Last verified vs code:** v1.0.319

**TL;DR.** The session/steward state machines today have anthropomorphic
naming (`closed` reads as "dead") and overloaded operators
("terminate the steward" appears in archived sessions, where there is
no engine to terminate). Scope plumbing exists in the API but no UI
exposes it, so every steward session is silently team-scoped. This
plan renames states to program-shaped vocabulary, gates affordances
on (session, steward) state pairs, surfaces scope as a chip, wires
project-page entry to actually pass project scope, and adds a
fork-from-archive action so the Claude Code / Codex resume metaphor
maps cleanly. Distillation stays as-is for MVP (the existing
"Nothing — just archive" path is sufficient placeholder).
**Phase 1 + Phase 2 = MVP.** Phase 1 is demo-blocking legibility;
Phase 2 is competitive parity with Claude Code remote / Happy
(fork action, scope-grouped session list, attention-scope entry).
Phase 3 is post-MVP identity-state expansion. Deliberation log:
`../discussions/agent-state-and-identity.md`.

---

## 1. Why this plan

Three problems surfaced from device walkthroughs and design review:

1. **Naming**. Sessions use `open / interrupted / closed / deleted`.
   "Close" implies the conversation died — but the agent is a
   program, and the data is on disk. Other AI tools (Claude Code,
   Codex, ChatGPT, Cursor) treat conversations as resumable threads;
   nothing in our architecture forces "closed" to be terminal.
2. **Overloaded operators**. The session chat shows a "Terminate the
   steward" button regardless of session state — including archived
   sessions where there is no attached engine. The button conflates
   *stop the engine*, *disable the identity*, and *delete the
   identity*, three different actions with different valid conditions.
3. **Invisible scope**. `sessions.md` §4.2 says "scope is seeded from
   the entry point". In code, `openStewardSession` posts to
   `/v1/sessions` with no `scope_kind`, so the server defaults
   everything to `team`. Project-page entry, attention-item entry,
   and Me-FAB entry all produce identical team-scoped sessions. Users
   cannot tell what scope a session is in, and the project-context
   loading promised in `sessions.md` §4.2 is silently inactive.

A separate but related issue surfaced in the same walkthrough: Me-page
attention items (approvals) show only a title with Approve/Deny
buttons and no detail link. The user cannot decide without context.
This is the same class of problem (a verb without a legible noun),
addressed in Phase 1.

---

## 2. Decisions

These are the load-bearing choices behind the rest of the plan.
Captured here rather than as a separate ADR because the rationale is
tightly coupled to the gap analysis below; if any of these flips,
half the plan changes.

**D1. Steward identity = the template row, not the engine.**
The `agents` table row (kind=steward) carries handle, persona,
capabilities, and audit attribution. The engine (model + process) is
a runtime binding to that identity. Process death is not identity
death; engine swap (Sonnet 4.6 → Opus 4.7) is not identity change.
This matches `agent-lifecycle.md` §103, §388 ("long-lived identity;
many bounded sessions") and the universal pattern across Slack bots,
ChatGPT GPTs, Linear issues, GitHub PRs (identity = stable handle;
contents mutable).

**D2. Session state set: `active / paused / archived / deleted`.**
Replaces `open / interrupted / closed / deleted`. The mapping is
1:1; only the names change. `archived` is reachable from `active`
via explicit user action with mandatory distillation choice (see D5).
`paused` is reached automatically when the host runner detaches and
reattached automatically on reconnect — same semantics as today's
`interrupted`, just with a less alarming name.

**D3. Steward identity state set (MVP): `active` only.**
Post-MVP adds `disabled` (template exists, new sessions blocked) and
`deleted` (soft-deleted). The MVP UI must already gate "open new
session" on `state == active` so the post-MVP transition is mechanical.

**D4. Add a `fork` operator.** From an `archived` session the user
can `fork` into a new `active` session pre-loaded with the source
session's distillation artifact + the last-K transcript turns. This
is the Claude Code "resume" / Codex "continue" metaphor in our
artifact-graph world. Closes the divergence-from-common-practice gap
without breaking the artifact-as-memory invariant.

**D5. Distillation stays mandatory in design; placeholder for demo.**
The "Nothing — just archive" button (currently labeled "Nothing —
just close") is sufficient as the 1-tap escape valve for the MVP
demo. Polishing distillation prefill and the Decision/Brief/Plan
shapes is post-demo work tracked separately.

**D6. UI affordances are gated on `(session.state, steward.state)`,
not rendered as chrome.** The "Terminate the steward" button is the
canonical example today; the same discipline applies to "Stop
session", "Archive", "Fork", "Delete". Each has a state precondition.

**D7. Scope is implicit from entry point + visible as a chip.** The
Me-FAB → general/team. Project page → project. Attention item →
attention. Re-scoping mid-session is post-MVP. For MVP, the
**chip just labels** what's in effect; users don't pick scope
themselves at session creation.

---

## 3. State tables

### 3.1 Session — current → proposed

| Current name | Proposed name | Meaning | Reachable via |
|---|---|---|---|
| `open` | `active` | Engine attached, conversation live | `POST /v1/sessions` |
| `interrupted` | `paused` | Engine detached (host offline / app killed); auto-resumes when reachable | host-runner heartbeat loss |
| `closed` | `archived` | Distillation filed; conversation done; data preserved | `POST /v1/sessions/:id/archive` (was `/close`) |
| `deleted` | `deleted` | Soft-deleted | `DELETE /v1/sessions/:id` |

### 3.2 Session — operators × states

| Operator | Valid in states | Notes |
|---|---|---|
| `open` (create) | n/a | Always allowed against an `active` steward |
| `send` (post event) | `active` | 409 in any other state |
| `pause` (auto) | `active` → `paused` | Triggered by host detach |
| `resume` (auto on reconnect) | `paused` → `active` | Re-streams transcript into engine |
| `archive` | `active` → `archived` | Requires distillation choice (Decision / Brief / Plan / Nothing) |
| `fork` *(new)* | `archived` → spawn new `active` | Pre-loads distillation + last-K transcript |
| `delete` | `active` (after archive) / `archived` → `deleted` | Soft delete |

### 3.3 Steward identity — state × operators

| State | MVP? | Operators | Notes |
|---|---|---|---|
| `active` | ✅ | open new session, edit persona, edit capabilities | The only MVP state |
| `disabled` | post-MVP | enable, edit metadata, delete | New sessions blocked; existing sessions read-only |
| `deleted` | post-MVP | (none — soft-deleted; visible in audit) | Tombstone for audit chain |

---

## 4. Comparison with common practice

| Tool | Session states | Resume / fork? | Distillation? | First-class agent identity? |
|---|---|---|---|---|
| Claude Code CLI | running / detached | `--continue` / `--resume` | none | no |
| Codex CLI | running / saved | resume / fork | none | no |
| Cursor Agent | active / archived | open from list | none | no |
| ChatGPT / Claude.ai | open / archived / deleted | re-open | none | yes (Custom GPTs) |
| Slack bots | installed / removed | n/a (channel-scoped) | none | yes |
| **termipod (proposed)** | active / paused / archived / deleted | fork from archive | mandatory at archive (1-tap "Nothing" allowed) | yes (steward template) |

**Where we differ — and whether the reason holds.**

| Difference | Load-bearing? | Reason |
|---|---|---|
| First-class steward identity | ✅ | Governance, audit, capabilities (ADR-005). Slack bots / GPTs share this pattern. |
| Sessions are scoped | ✅ | Context budget + scoped capabilities (sessions.md §4.2). |
| Mandatory distillation at archive | ✅ | Artifact-graph-as-memory (sessions.md §2). 1-tap "Nothing" escape preserves Claude-Code-like UX. |
| `closed` is non-resumable | ❌ | Inherited assumption. **Replaced** by archive + fork. |
| "close" naming | ❌ | Anthropomorphic. **Renamed** to `archive`. |
| Affordances not gated on state pair | ❌ | Bug, not design. **Fixed**. |

---

## 5. Gap analysis — current vs proposed

### 5.1 Schema / API

| Surface | Current | Proposed | Gap |
|---|---|---|---|
| `sessions.status` enum | `open / interrupted / closed / deleted` | `active / paused / archived / deleted` | Migration: rename 4 values; update all reads/writes. |
| `POST /v1/sessions/:id/close` | exists | `POST /v1/sessions/:id/archive` | Rename endpoint; keep `/close` as alias for one release for app forward-compat. |
| `POST /v1/sessions/:id/fork` | does not exist | new endpoint | Body: source session ID, optional new title. Server creates new `active` session, copies scope, pre-loads distillation + last-K events into system prompt. |
| `agents.state` (steward) | implicit (always exists) | explicit `active` (MVP); `disabled / deleted` (post-MVP) | Add column post-MVP; MVP no-op. |

### 5.2 App UI

| Surface | Current | Proposed | Gap |
|---|---|---|---|
| Session list status pill | shows `open / interrupted / closed` | shows `active / paused / archived` | Rename in `sessions_screen.dart`. |
| Session header | shows title + agent handle | + scope chip ("General · team" / "Project: Foo" / "Approving: Bar") | New widget; reads `scope_kind` + `scope_id` from session row, resolves name. |
| Session chat actions menu | "Close", "Delete" | "Archive", "Fork from archive" *(archived only)*, "Delete" | Rename + add fork action. |
| "Terminate the steward" button | always rendered in chat | only when `session.state == active` AND steward owned by current team | Rename to "Stop session" (or "Detach engine"); gate visibility. |
| Project-page steward chip | calls `openStewardSession` → team-scoped session | calls a project-scoped open path (`scope_kind=project, scope_id=<id>`) | Wire scope through `open_steward_session.dart`; signature change. |
| Attention-item card → steward | no entry path | tap "Discuss with steward" → opens attention-scoped session | New affordance; new route. |
| Approval card on Me page | title + Approve/Deny only | title + chevron → detail page (action / payload / requester / chain / audit) | New screen + route; minimal data fetch (already in `/v1/attention`). |

### 5.3 Docs

| Doc | Change |
|---|---|
| `docs/spine/sessions.md` | Rename `open / interrupted / closed` throughout; add §X "Fork"; update §4.3 lifecycle diagram. |
| `docs/spine/agent-lifecycle.md` | Cross-link to D1 (identity = template). |
| `docs/spine/information-architecture.md` | Update §6.x where session states are referenced; note scope-chip pattern. |
| `docs/discussions/code-as-artifact.md` | No change. |
| `docs/changelog.md` | New entry per phase. |

---

## 6. Phases

### Phase 1 — Demo legibility (MVP-demo blocker)

Goal: nothing in the demo screen reads as broken or contradictory.
No new behavior; rename + gate + label. Lands first because the
demo is the visible milestone and these items are pure rename/gate.

1. **Schema migration** — rename status enum values; update Go server reads/writes; keep `/close` as alias of `/archive` for one release.
2. **App rename** — `closed → archived`, `open → active`, `interrupted → paused` in all UI strings, status pills, list filters.
3. **Button gating** — "Terminate the steward" → "Stop session"; hide in `paused / archived / deleted`. Same gating audit on every other session-actions affordance.
4. **Scope chip** — add to session header. Display only; reads existing `scope_kind` / `scope_id`.
5. **Project-page wiring** — `openStewardSession` accepts optional scope, project chip passes `(project, projectId)`. Me-FAB unchanged (defaults team).
6. **Approval detail route** — new screen showing action/payload/requester/chain/audit; chevron on Me-page approval card opens it.

Deliverable check: walkthrough shows no "terminate" button on archived
sessions; project-page steward sessions show "Project: Foo" chip;
approval cards have a tappable detail.

### Phase 2 — MVP completion (parity with Claude Code remote / Happy)

Goal: a user switching from Claude Code remote / Codex / Happy finds
their resume + fork muscle memory works, and the session list groups
sessions by scope so navigation isn't a flat firehose. **Phase 1 + 2 =
MVP**; this is not optional polish.

1. **`POST /v1/sessions/:id/fork`** — server endpoint; new session is `active`, copies scope, pre-loads distillation artifact + last-K transcript events into system prompt.
2. **Fork action in archived-session view** — "Fork from archive" button; pushes new active session.
3. **Attention-scope entry** — "Discuss with steward" action on attention items → opens `(attention, itemId)`-scoped session. Pairs with the Phase 1 approval detail screen.
4. **Sessions list grouping by scope** — section headers in `sessions_screen.dart`: General / Project: Foo / Approving: Bar / Archived. Per-steward grouping deferred to Phase 3 (single steward in MVP).
5. **Default scope picker on the open-from-list path** — when a user starts a session from the sessions screen (no entry-point context), let them pick General / a specific Project / a specific Attention item. Implicit-from-entry-point still covers Me-FAB and project-page paths.

Deliverable check: archived session → Fork → new active session opens
with previous distillation + recent transcript visible as initial
context; sessions screen shows section headers; a Claude Code remote
user can do "resume yesterday's session" in two taps.

### Phase 3 — Identity state expansion (deferred, post-MVP)

Goal: steward identity gets full lifecycle; per-member stewards.

1. `agents.state` column for stewards; `disabled` / `deleted` states.
2. UI: enable/disable from steward config screen; gate "open new session" on state.
3. Re-scope mid-session (`POST /v1/sessions/:id/rescope`).
4. Per-member stewards (ADR-004 F-1) — sessions group by `(steward, scope)`.
5. Distillation prefill polish (model-drafted Decision / Brief / Plan templates).

---

## 7. Verification

For each phase, before declaring done:

**Phase 1**
- [ ] Walk through every session state transition in the app; status text matches new vocabulary.
- [ ] In an archived session: "Stop session" button is absent.
- [ ] In a paused session: "Stop session" is absent (no engine to stop).
- [ ] Open a session from a project page → header chip reads "Project: <name>".
- [ ] Open a session from Me FAB → header chip reads "General".
- [ ] Tap an approval card → detail screen renders with payload + requester chain.
- [ ] DB migration is forward + backward compatible: rolling restart of hub + app does not 500.

**Phase 2**
- [ ] Archived session → tap "Fork" → new active session opens with distillation + last-K transcript visible as initial context.
- [ ] Forked session has new session id, same scope, fresh transcript.
- [ ] Attention item → "Discuss with steward" → new session with `scope_kind=attention`.

**Phase 3** — separate plan doc when triggered.

---

## 8. Migration notes

- The status enum rename is a server + app coordinated cutover. No
  external API consumers today; no compatibility shim beyond the
  one-release `/close` → `/archive` alias.
- All existing sessions on the user's hub are renamed in place via a
  single SQL migration: `UPDATE sessions SET status = CASE status
  WHEN 'open' THEN 'active' WHEN 'interrupted' THEN 'paused' WHEN
  'closed' THEN 'archived' ELSE status END`.
- App release that ships Phase 1 must be installed before the hub
  rolls the migration, or the app will display the new strings
  against old enum values and not match. Sequence: ship app build →
  user updates → bump hub → migrate.

---

## 9. Related

- `../discussions/agent-state-and-identity.md` — deliberation log (alternatives, first-principles, prior-art comparison)
- `../spine/sessions.md` — current canonical doc; gets renamed in Phase 1
- `../spine/agent-lifecycle.md` — identity-as-template framing (D1)
- `../decisions/004-single-steward-mvp.md` — single-steward MVP; per-member stewards in Phase 3
- `../decisions/005-owner-authority-model.md` — director-vs-operator framing; steward as CEO-class operator
- `../discussions/code-as-artifact.md` — separate artifact-class discussion
- `../discussions/simple-vs-advanced-mode.md` — overlapping IA-shape discussion (post-demo)
- `./research-demo-gaps.md` — MVP demo tracker; Phase 1 here is the demo-blocker subset, Phase 2 lands as part of MVP completion
