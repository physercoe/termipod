# Multi-steward — design proposal

Status: **draft, not started**. Discussion-first per the user's "think
comprehensively before code" direction.

Asks the user explicitly raised that this design has to answer:

1. Stewards management (overview screen — what's running, who is who).
2. Templates management (already exists; this design only asks how
   stewards relate to templates).
3. View / edit current running stewards and their sessions.

What this design intentionally **doesn't** cover (per user direction):

- Per-project stewards (#2 in the user's three options).
- Per-member stewards (the F-1 thread in `../spine/information-architecture.md` §11).

---

## 1. What we already have

| Surface | State | Role in multi-steward |
|---|---|---|
| `templates/agents/steward.v1.yaml` (built-in) | Shipped | The default steward template. New domain templates would live alongside it. |
| `team/templates/agents/*.yaml` (user files) | Shipped — `TemplatesScreen` does CRUD | Where domain templates would be stored (`research-steward.v1.yaml`, etc.). |
| `_SpawnStewardSheet` | Shipped | Lets the user spawn a steward from a template + host. Currently picks template from a list, hardcodes handle to `steward`. |
| `agents` table, `(team_id, handle, status='live')` unique index | Shipped | Already supports multiple agents with distinct handles per team. Schema-level support for multi-steward is free. |
| `sessions` table with `current_agent_id`, `status`, `title` | Shipped | Sessions naturally group by agent. |
| Mobile recognition of "is this a steward?" | Hardcoded `handle == 'steward'` in 9 call sites | Single failure point for moving to multi-steward. |
| Steward chip on Projects, Me FAB, `_newSession`, `StewardBadge`, `steward_liveness` | Shipped | All assume one steward per team. |

So: hub + DB + templates already support N stewards; the gap is purely
in mobile UX + a couple of conventions.

---

## 2. Vocabulary

- **Steward** — an agent whose role is "talk to the principal, hold
  conversation context, route tasks". Distinguished by handle
  convention rather than a new schema field (no new column required).
- **Steward template** — a YAML file under `team/templates/agents/`
  whose `template:` key starts with `agents.steward.` (or equals
  `agents.steward` for the legacy default). The category prefix is the
  matcher; no schema change.
- **Steward instance** — a live agent record with handle matching the
  steward convention (see §3).

---

## 3. Identity convention (one schema-free decision to make)

Three options for telling stewards apart from workers:

**Option A — handle suffix.** Stewards have handle ending in `steward`:
`steward` (legacy default), `research-steward`, `infra-steward`. The
matcher is `handle == 'steward' || handle.endsWith('-steward')`.

- Pros: zero schema change, `_isSteward(h)` is one line, existing
  spawn code paths just work.
- Cons: a worker named `the-steward` would be misclassified. Easy to
  forbid in the spawn sheet's validator.

**Option B — handle prefix.** `steward`, `steward-research`,
`steward-infra`. Same shape, different end of the string.

- Pros: same as A.
- Cons: same.

**Option C — template-derived.** Read each agent's spawn_spec_yaml to
find which template it came from; classify as steward iff the template
is under `agents.steward.*`.

- Pros: the source-of-truth is the template, not the handle.
- Cons: every "is this a steward?" check now does a YAML parse or a
  template lookup. Ugly.

**Recommendation: Option A.** Cheapest, zero migration, the existing
9 hardcoded sites become 9 one-line `_isSteward(h)` calls.

---

## 4. UX surfaces

### 4.1 Stewards overview screen — NEW

Reachable from:

- **Me tab AppBar** — gain a "Stewards…" entry in the tab's overflow
  menu. The FAB stays for the common case.
- **Team menu / settings** — a Stewards row alongside Templates / Hosts.

Content (one row per live steward):

```
┌───────────────────────────────────────────┐
│ research-steward                          │
│ claude · opus 4.7 · host=gpu-1            │
│ 2 active sessions · last_active 4m ago    │
│                       [▶ open] [⋯]        │
├───────────────────────────────────────────┤
│ infra-steward                             │
│ codex · gpt-5 · host=vps                  │
│ 1 active session · last_active 2h ago     │
│                       [▶ open] [⋯]        │
└───────────────────────────────────────────┘
                              [+ new steward]
```

Row actions (kebab):

- Rename (steward handle, with collision check)
- Replace (existing _confirmAndRecreateSteward flow)
- Terminate (kills the agent; sessions auto-interrupt)
- View sessions (push SessionsScreen filtered by this steward)
- View template (push TemplateEditorScreen on the source template)

`+ new steward` opens an extended `_SpawnStewardSheet` (see §4.4).

### 4.2 Sessions list grouping

Two small additions to the existing SessionsScreen:

- **Filter chip strip at the top**: `All | research | infra | …`,
  sourced from distinct steward handles in the live agent set. "All"
  is the default. Active steward filter persists per-tab via
  Riverpod.
- **Subtitle adds steward handle**: each session row already shows
  `status · scope_kind · worktree`; prepend `<handle> · `.

The "+ new session" flow:

- One steward live → current behavior.
- Multiple stewards live → bottom sheet picker: "Which steward?"
  Selecting one runs the existing close-and-reopen flow against that
  steward.

### 4.3 Me FAB + Projects steward chip

- Single steward → unchanged.
- Multiple stewards → tap opens a bottom sheet with one row per
  steward + "Stewards…" link to the overview. Long-press still
  triggers Recreate (now scoped to the most-recently-used steward).

### 4.4 Spawn-steward sheet — extended

Today it: picks template, picks host, optional model override, spawn.
Add **one** field above the template picker:

- **Handle**: text input. Default is `steward` if no live steward
  exists; otherwise prompts the user to type a unique handle (e.g.
  `research-steward`). Validator: must match
  `^[a-z][a-z0-9-]*-?steward$` or be exactly `steward`. Refuses any
  handle already in use by a live agent.

The template picker stays as-is; users who have new domain templates
under `team/templates/agents/agents.steward.<name>.yaml` see them in
the list.

### 4.5 Templates screen

No code change. Already supports CRUD on any template. To make domain
stewards discoverable, we'd add a sub-filter on the existing
"Templates" tab: `agents · stewards`, `agents · workers`, `prompts`,
`policies`. That's one screen, ~50 LoC, optional.

---

## 5. Server-side

Almost nothing. Concrete needs:

- **Validator on `handleSpawn`**: reject a spawn whose `child_handle`
  matches the steward convention but whose template doesn't (and
  vice versa). One regex check, one template-prefix lookup. Prevents
  accidental misclassification.
- **No new endpoints**. Templates, agents, sessions all already
  expose what the new screen needs.
- **`/v1/teams/{team}/agents?role=steward`**: optional convenience
  filter so mobile doesn't have to walk the agent list and apply the
  handle regex client-side. ~10 LoC.

No migration required.

---

## 6. Wedge breakdown (for execution if you say go)

Each line is a self-contained wedge that lands as one commit + version
bump. Order is the safe-to-ship sequence:

1. **`_isSteward(handle)` helper + replace 9 call sites.** Pure refactor;
   single-steward behavior identical because the matcher returns true
   for `handle == 'steward'`.
2. **Spawn-sheet handle field.** Adds the input + validator. Existing
   spawn flow unchanged when only one steward exists.
3. **Stewards overview screen.** New screen + entry from Me overflow.
   No filter changes to Sessions yet.
4. **Sessions list filter chip strip + per-row handle subtitle.**
5. **Me FAB / Projects chip multi-steward picker.** Bottom sheet for
   the n>1 case; single-steward path unchanged.
6. **(Optional) Templates screen sub-filter.** Cosmetic.
7. **(Optional) Server-side `?role=steward` filter.** Performance.

Total: ~5 user-facing wedges, ~2 days of work, no schema migration,
no wire-format break.

---

## 7. What this leaves unsolved (call-outs for later)

- **Agent personas vs templates**: today a template is a YAML file the
  user owns. There's no "library" of curated steward personas
  (research-steward with a known prompt, infra-steward with another)
  that a user can pick from. Building a community-contributed
  persona library is its own design, deferred.
- **Routing across stewards** (e.g., "research-steward delegates an
  infra task to infra-steward"): A2A already supports this; nothing
  in this wedge improves it.
- **Stewards managing each other**: a parent steward spawning a
  domain steward as a child is supported by `mcp__termipod__spawn_*`
  but the UX is "the steward types a tool call". A discoverable button
  would be a follow-up.
- **Per-member stewards** (F-1): still deferred per
  `project_multi_user_stewards.md`.
- **Per-project stewards**: explicitly out of scope per user direction.

---

## 8. Risks

- **Broken steward chip / FAB on existing single-steward installs.**
  Mitigation: every wedge falls back to current behavior when only
  one steward is live.
- **Handle collisions from poorly-validated input.** Mitigation: the
  regex validator + the unique-handle DB index.
- **Templates editor ergonomics.** Editing raw YAML on a phone is
  rough. Worth it for power users; we shouldn't make it worse for
  the common case.
- **"Which steward did I just talk to?" memory load.** Active steward
  needs to be visible in every chat AppBar. The existing
  SessionInitChip already does this once we add `engine` (just
  shipped) and a `handle` pill.

---

## 9. Decisions taken

- **Identity convention** (§3): **A — handle suffix.** `_isSteward(h) =
  h == 'steward' || h.endsWith('-steward')`.
- **Sessions/Stewards page**: **merged.** Each steward is a section
  with its current session inline + collapsible "previous" subsection.
  Me FAB and Projects steward chip both route here.
- **"Reset" naming**: **`Reset (new conversation)`** — the close-and-
  reopen action that keeps the same agent alive and starts a fresh
  transcript. Distinct from Terminate (kills the agent) and from
  Replace (engine swap inside the existing session).
- **Template CRUD location**: **stewards-page level**, not per-steward.
  Stewards page AppBar `⋮` carries `Templates` (pushes the existing
  TemplatesScreen) + `Engines` (AgentFamiliesScreen). Per-steward
  kebab no longer offers `Edit template` because changes affect every
  future spawn, not the current instance.
- **Spawn-creates-session contract**: **server-side**, via a new
  `auto_open_session: true` flag on the spawn request. When set and
  `SessionID` is empty, `DoSpawn` opens a session inside the same tx
  so a freshly-spawned steward is never agent-without-session.
- **Workdir per template**: domain templates ship with distinct
  `default_workdir` values (`~/hub-work/research`, `~/hub-work/infra`,
  …) so two stewards on the same host don't collide on
  `~/hub-work`. The base `agents.steward` keeps `~/hub-work` for
  back-compat.

## 10. Per-steward menu (final shape)

Per-steward kebab on the merged page — only "this instance" actions:

| Action | What it does |
|---|---|
| Reset (new conversation) | Closes current session, opens a fresh one against the same agent. Engine + memory preserved at the agent level; transcript starts empty. |
| Replace steward | Swap engine/model inside the existing session (existing flow). |
| Terminate steward | Kills the agent process. Session auto-flips to interrupted. |
| Rename | Rename the steward's handle (collision check). |

Stewards page AppBar `⋮` — global / type-system actions:

| Action | What it does |
|---|---|
| Templates | Push TemplatesScreen (full CRUD on `team/templates/agents/*.yaml`). |
| Engines | Push AgentFamiliesScreen. |
| Refresh | Re-pull stewards + sessions from hub. |

## 10.5. Session state machine — full audit

Added 2026-04-27 after a device walkthrough surfaced "session is in
state X, what operations apply?" ambiguity. This is the canonical
table; the per-steward kebab and per-row session menus must match it.

### States

| State | Meaning | How a session reaches it |
|---|---|---|
| `open` | Conversation active. Agent is alive (`agents.status` in `running`/`pending`/`paused`) and pointed at by `current_agent_id`. | (a) `openSession` called explicitly. (b) `DoSpawn` with `auto_open_session=true`. (c) `handleResumeSession` flips an interrupted session back. |
| `interrupted` | Session preserved; the agent it was pointing at died unexpectedly. Transcript intact, can be resumed. | `handlePatchAgent` sees the agent flip to `crashed` or `failed` and updates its open sessions to `interrupted` in the same handler. |
| `closed` | Conversation ended explicitly. Transcript stays for reference. No actor. | `handleCloseSession`. Today the only path is the per-steward Reset (which closes the prior session before opening a new one). The standalone Close action was removed in v1.0.294. |
| `deleted` | Soft-deleted. Excluded from default lists. Transcript-link cleared from `agent_events`/`audit_events`/`attention_items`. | `handleDeleteSession`. Refuses if the session is still `open` or `interrupted`. |

### Transitions

```
       openSession / DoSpawn(auto_open_session)
                 │
                 ▼
              [open] ──── Reset (per-steward kebab) ─── [closed]
                 │            │
       agent → crashed/failed │
                 │            ▼
                 ▼          (delete)
          [interrupted] ──── Resume ─── [open]
                 │
                 │
                 ▼
            (no direct path; close-via-Reset isn't applicable)
```

What this enforces:
- A live steward (`agents.status` in `running`/`pending`/`paused`) is
  *expected* to have exactly one session in `open` or `interrupted`.
  The "every live steward has a session" invariant.
- `closed` is only reachable from `open`, and only via Reset.
- `deleted` is the final disposition; only reachable from `closed`
  (or `interrupted` if you close it first).

### Operations per state

| Operation | open | interrupted | closed | deleted |
|---|---|---|---|---|
| **Tap session row** | → live chat | → chat (read of past + Resume button) | → chat (read-only of transcript) | hidden |
| **Reset (new conversation)** | ✅ closes current, opens new | ❌ no agent to reset against | ❌ no agent | ❌ |
| **Replace steward** | ✅ swap agent inside the session | ✅ swap agent inside the session, status → open | ❌ closed is final | ❌ |
| **Terminate steward** | ✅ kills agent, session → interrupted | ❌ no agent to kill | ❌ no agent | ❌ |
| **Resume** | ❌ already open | ✅ spawn fresh agent → open | ❌ (read `Reopen` discussion below) | ❌ |
| **Rename session title** | ✅ | ✅ | ✅ | ❌ |
| **Start session** (new!) | n/a | n/a | n/a | n/a |
| **Delete session** | ❌ close first | ❌ close first | ✅ | ✅ idempotent |

> **Start session** is a new affordance shipped 2026-04-27: it appears
> on the steward section header when a *live steward has no active
> session* (an edge case that can happen with pre-v1.0.290 spawns or a
> Reset whose openSession failed silently). Single tap calls
> `openSession(agentId)`. Not a normal flow — when it appears, the user
> is in a recoverable but-unexpected state.

### Why no `Reopen` for closed sessions

A closed session's transcript is preserved; the user can tap it and
read what was said. But there's no way to "unclose" it back to a live
state. Two reasons:

1. The agent the closed session was pointing at may be terminated by
   now. Reopening would need to spawn a new agent inside the old
   session — exactly what Resume does. But resume is for *interrupted*,
   meaning "I expect to come back". *Closed* meant "I'm done with
   this." Letting the user undo closed felt like papering over an
   intentional choice.
2. The same effect is achievable via per-steward Reset (which opens a
   fresh session against the same steward) or Replace (which keeps
   conversational continuity by inheriting the prior session's ID).

If a Reopen affordance turns out to be needed, it's a small wedge —
just a server endpoint + a per-row menu entry. Held.

### What "tap" does per state (subtle but matters)

| State | Tap behavior today | UX gap if any |
|---|---|---|
| `open` | Push SessionChatScreen → live AgentFeed + compose box | None |
| `interrupted` | Push SessionChatScreen; AgentFeed shows the historical transcript; the per-row Resume button is on the Sessions page (not the chat AppBar) | The chat itself doesn't make read-only-ness obvious; compose box still appears but sends will queue against a dead agent |
| `closed` | Push SessionChatScreen; transcript appears; compose still sends but goes nowhere useful | Same as interrupted — no clear "this is read-only" banner |
| `deleted` | Hidden by default | n/a |

The "compose box still appears on dead/closed sessions" is a known
polish gap. Filed as a follow-up.

## 11. Wedge plan (final)

Three wedges:

1. **`_isSteward(handle)` refactor + spawn-sheet handle field +
   DoSpawn `auto_open_session`.** Zero UX change for single-steward
   installs. Adds the foundation for wedge 2.
   - Server: `auto_open_session bool` on `spawnIn`; when true + no
     `SessionID`, `DoSpawn` opens a session inside the same tx.
     Round-trip test.
   - Templates: ship `agents.steward.research.v1.yaml` and
     `agents.steward.infra.v1.yaml` with distinct workdirs and
     light persona prompts.
   - Mobile: `_isSteward(h)` helper; replace 9 call sites; spawn
     sheet gains a handle field with regex validator + collision
     check; template picker filters to `agents.steward*`.

2. **Merged Sessions/Stewards page**: replace `SessionsScreen` with
   the section-per-steward layout. Reset (new conversation),
   Replace, Terminate, Rename in per-steward kebab; Templates,
   Engines, Refresh in AppBar `⋮`.

3. **Me FAB / Projects chip routing** to the merged page, scrolled to
   the most-recent steward.
