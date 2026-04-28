# Information architecture

> **Type:** axiom
> **Status:** Current (2026-04-28)
> **Audience:** contributors
> **Last verified vs code:** v1.0.314

**TL;DR.** Authoritative reference for termipod's mobile IA: the
philosophy the nav is derived from, the ontology of surfaces and
roles, the single home of every primitive, and the migration plan
from the current sprawl. Sister document to `blueprint.md` —
blueprint defines *what the system is*; this doc defines *how a
human encounters it on a phone*.

Any PR that adds a screen, a tab, or a menu entry should trace its placement
to one of the axioms or the entity matrix here. A proposal that cannot be
traced is a candidate for rejection or an explicit amendment to this document.

---

## 1. Purpose

termipod is a mobile-first control plane for a distributed fleet of AI
agents (blueprint §1). The IA determines whether that control plane feels
like a **cockpit** — glanceable, director-first, autopilot by default — or
like a sprawling admin console. Current state is the latter: 35 hub screens
plus 7 top-level sections, with primitives scattered across them
(templates in two places, runs under member settings, SSH plumbing in the
inbox). This document resets the structure.

---

## 2. Philosophy: six IA axioms

Derived from the blueprint's three system axioms (A1 attention-scarce, A2
spatially-bound, A3 stochastic-authority) but expressed in UI terms. Every
screen and tab must be traceable to one of these.

**IA-A1. The phone is opened in glances, not sessions.** Hundreds of
sub-minute interactions per day, not eight hours at a desk. Tier-1 surfaces
must answer "what needs me now?" within one tap, without a scavenger hunt.
Multi-tap drill-down is fine; multi-tab scanning for the same kind of
information is not.

**IA-A2. One entity, one home.** Every primitive (project, run, host,
template, review, agent, document, …) has exactly one canonical screen.
Every other mention is a read-only pointer that navigates there. Duplicate
access paths fragment the user's mental model and guarantee drift.

**IA-A3. The director's attention is the north star.** The steward does
the work; the director ratifies. Nav structure mirrors that priority —
*attention* and *workspace* are primary surfaces; *plumbing* and
*governance* recede until something goes wrong or is explicitly sought.

**IA-A4. Scope follows ownership.** Team-owned state lives in team
surfaces. Personal state lives in personal surfaces. Device-local state
lives in device surfaces. Mixing scopes inside one screen (today: Settings
contains audit, tokens, team management, and theme) destroys the user's
predictive model of *where* things live.

**IA-A5. Roles are affordance axes, not modes.** A director, steward,
reviewer, or observer opens the *same* screens. What changes is which
buttons render and which actions succeed. There is no mode switch and no
role-specific UI stack. Permissions, not navigation, enforce roles.

**IA-A6. SSH is a capability, not a tier.** Direct terminal access is a
button on a Host, not a sibling of the hub. The hub is the cockpit; SSH
is the maintenance hatch reachable from inside it. Terminal is always
reachable (a button on any host, plus a command-palette launch) — never
a competing top-level world.

Six axioms, three forces: **glanceability** (A1, A3), **coherence** (A2,
A4), **extensibility** (A5, A6).

---

## 3. Ontology of surfaces

Every UI element is one of these four kinds. A new surface that doesn't
fit a category is a design smell.

### 3.1 Tab (bottom nav)

The ≤5 top-level homes a user reaches from cold. Tabs represent *what I
care about*, not *what entity type exists*. A new entity type does **not**
get a new tab; it gets a home inside one.

### 3.2 Screen

A full-page view of a single entity or a list of entities. Screens are the
default destination of navigation. Every primitive has exactly one screen
it calls home (IA-A2).

### 3.3 Sheet

A modal for one focused action (create, edit, confirm). Sheets do not own
state; they mutate the entity behind them and close. A screen that is
persistently modal is a misplaced screen.

### 3.4 Capability (cross-cutting)

A feature reachable from many contexts but not a nav destination of its
own: **terminal**, **search**, **command palette**, **share**, and the
per-host **connect** action. Capabilities attach to the entity currently
in view.

### 3.5 Tiers

Surfaces are organized in five attention tiers. A tier is not a visible
label — it is a placement discipline.

| Tier | Name | What belongs here | Default visibility |
|---|---|---|---|
| 0 | Me | Attention queue, my active work, search | Default landing |
| 1 | Team workspace | Projects, Activity, Hosts | One tap |
| 2 | Team admin / Governance | Members, roles, policies, budgets, tokens (team), councils, **steward config** | Entered from top-bar **Team switcher** |
| 3 | Device Settings | Personal & device-local prefs only (theme, keyboard, API tokens on this device, notification rules on this device) | Entered from Me |
| C | Capability | Terminal, search, command palette | Cross-cutting, any screen |

**Critical distinction — Settings is two things:**

- **Device Settings** (Tier 3, the `Settings` tab): scoped to *this phone,
  this user*. Never team-wide. Stripped of everything team-scoped.
- **Team Settings** (Tier 2, entered from Team switcher): scoped to the
  team. Members, policies, budgets, steward config. Today this is the
  `team_screen.dart` "Settings" sub-tab; it must not be conflated with
  the Device Settings tab.

A setting's tier is determined by *who owns the state*, not where it
currently lives. Anything in Tier 3 today that is team-scoped is
misplaced; anything in Tier 0 today that is not attention-bearing for me
is misplaced.

---

## 4. Ontology of roles

The IA must not assume a single user. The axioms require roles to be
*affordance axes* (IA-A5), which means the role set is part of the
ontology even when only one exists in MVP.

Role set (MVP has only **Director**; the rest are reserved slots):

- **Director** — owns the goal. Ratifies autonomous decisions. Sees the
  full attention queue. Can do any steward action manually.
- **Steward** — runs operations on the director's behalf as a
  **manager / orchestrator**, not an IC. Plans, decides, spawns
  workers, arbitrates approvals, distills sessions into artifacts.
  The LLM steward is the default operator; a human steward is a role
  a member can take. Bounded by policy (blueprint A3). The steward
  *does not perform IC work directly* outside the single-agent
  bootstrap window (`agent-lifecycle.md` §6.2 / §4.9); when an IC task
  appears, the steward spawns a worker. MVP ships one steward per
  *team* (rows in `agents` are `UNIQUE(team_id, handle)` and there is
  no `users`/`team_members` table yet). The intended future shape is
  one steward per *member*, acting as that member's deputy with their
  own preferences, memory, and budget envelope — see §11 follow-up.
- **Worker** — an IC agent spawned by a steward (or another worker)
  for bounded specific work. Renders in the mobile UI as a *code
  surface* (branch + diff + file count + tests) rather than a
  *decision surface*. Lives in `agents` like a steward but with
  different tool allowlist, different transcript styling, different
  distillation outcome. See `agent-lifecycle.md` §4.9 for the full
  manager-vs-IC table.
- **Reviewer** — consulted on specific documents or decisions. Has a
  scoped inbox: reviews assigned to them, not the whole team's.
- **Member** — participates in a team; can propose, comment, execute.
  Default role on join.
- **Observer** — read-only on everything the team permits.
- **Council** — an N-of-M policy-bound approval group; not an individual
  role but a construct that generalizes single-approver reviews. A
  decision gated by council requires M of N members to approve.

Role ↔ tab visibility (MVP: everyone is Director; this is the future matrix):

| Role | Me | Projects | Activity | Hosts | Governance | Settings |
|---|---|---|---|---|---|---|
| Director | ✔ full | ✔ | ✔ | ✔ | ✔ | ✔ |
| Steward | ✔ scoped | ✔ | ✔ | ✔ ops | ─ | ✔ |
| Reviewer | ✔ their reviews | ✔ read | ✔ scoped | ─ | ─ | ✔ |
| Member | ✔ their items | ✔ | ✔ | ─ | ─ | ✔ |
| Observer | ─ | ✔ read | ✔ read | ─ | ─ | ✔ |

The same screens render for every role; non-applicable actions are hidden
or disabled (IA-A5).

---

## 5. Host unification

The current app has two parallel worlds: **Servers** (SSH bookmarks,
top-level tab) and **Hub → Hosts** (team-registered compute). These are
two views of the same physical machine. Collapsing them is the structural
change that makes the rest of the IA coherent (IA-A6).

### 5.1 One entity, two roles

A `Host` row has a **scope**:

- `personal` — local SSH bookmark only. Stored on this device. Terminal
  works. Steward cannot touch it.
- `team` — hub-registered. Has `host_id`, team-scoped, assignable to
  agents. Terminal still works if this device has credentials for it.
- `team+personal` — both flavors on the same row. Credentials are
  device-local; team binding is hub-global.

One row per physical machine. Promotion and demotion happen on the same
row; no duplicate listings.

### 5.2 Data ownership split

- **Hub owns:** team identity, `host_id`, non-secret SSH hints
  (hostname, port, default user), labels, assignability state. Global,
  multi-client.
- **Device owns:** SSH credentials (private keys, passwords,
  passphrases). Live in secure storage. Never leave the device.

The phone's Hosts list is `hub.team_hosts ∪ device.personal_bookmarks`,
joined on `host_id` where both sides exist. Who registered it, and from
which device, is an implementation detail — the row looks the same.

### 5.3 Lifecycle

```
                      (Register to Hub)
     personal    ─────────────────────────▶   team+personal
        ▲                                           │
        │                                           │
        │             (Unregister from Hub)         │
        └───────────────────────────────────────────┘

                      (Registered from desktop)
         ─────────────────────────────────────▶   team (no creds here)
                                                     │
                                              (Add SSH credentials)
                                                     ▼
                                                team+personal
```

### 5.4 Delete semantics (three distinct verbs)

1. **Unregister from team.** Default "hub-side delete". Row demotes to
   `personal`; terminal still works. Historical runs/artifacts referencing
   the host remain, read-only. Reversible by re-registering.
2. **Remove local bookmark.** Deletes this device's credentials for the
   host. If the host is team-registered, the row stays visible (team
   source of truth); terminal disables until creds are re-added.
3. **Hard-delete from team (admin-gated).** Tombstones the host.
   Historical references dim but survive. Not MVP.

**Invariants:**

- Hub delete never destroys SSH credentials on any device.
- Local delete never affects team state.
- `host_id` is the stable join key across renames, IP changes, credential
  rotation.

---

## 6. The five tabs

The bottom nav is the single most visible IA artifact. Five tabs, ordered
left-to-right as the director's attention naturally flows. **Me** is
centered as the default landing.

| # | Tab | Tier | Primary intent |
|---|---|---|---|
| 1 | Projects | 1 | The workspace I steer |
| 2 | Activity | 1 | What happened across the team |
| 3 | **Me** (default) | 0 | What needs me now |
| 4 | Hosts | 1 (infra) | Infra & terminal |
| 5 | Settings | 3 | My device prefs (**not** team) |

Rationale for the order: flow axis left (work I initiate) → right (tools
I fall back to). Default landing is center so the thumb arrives at "what
needs me" with zero travel.

**Persistent top-bar on every tab:**

- **Team switcher** (left): `[▸ Team Name]` — shows active team; tap
  opens team picker + **Team Settings** (governance, §6.6). This is the
  single ingress for all team-scoped admin.
- **Search** (right): capability, matches entities across all tabs.
- **Command palette** (right): capability, keyboard-driven actions.

Putting the Team switcher on every tab (rather than burying it in
Settings or giving it a tab) makes team-scope explicit: you are always
operating *inside a team*, and switching teams changes what the five
tabs show. Governance lives exactly one tap away from anywhere.

### 6.1 Me (default)

Tier-0. Three sections, scrollable:

- **Attention** — open `attention_items` assigned to or relevant to me,
  in priority order. Empty state is celebrated, not hidden.
- **My work** — projects I own or recently touched, with a condensed run
  status strip. Reviews awaiting my decision inline here.
- **Since you were last here** — digest pulled from Activity for
  projects I follow. Collapsible; dismiss marks seen.

Plus: top-bar search (capability), command palette (capability).

Not in Me: any team-wide feed (that's Activity), any settings, any SSH
bookmarks, any templates.

### 6.2 Projects

Tier-1. List of projects, filterable by status, kind, role. Each project
row opens the Project Detail screen (unchanged as a concept, improved in
density). Project Detail nests, not sprawls:

- Overview (goal, status, recent runs, attention summary)
- Runs → run detail → metrics, artifacts, logs
- Reviews → review detail
- Documents → doc viewer
- Agents (scoped to this project)
- Channel (scoped to this project)
- Schedules / plans / tasks (scoped to this project)
- Templates link (read-only pointer to the one home under Projects)

A single **Templates / Blueprints** screen lives here too — one home.
Any other "templates" surface in the app is a pointer into it.

### 6.3 Activity

Tier-1. The team's mutation feed, backed by `audit_events`. Chronological,
filterable (project, entity type, actor, time, "affects me"). See §9 for
what is and isn't an Activity event.

Top of this tab has a **digest card** mirroring the Me tab's "since you
were last here" — identical data, but Activity is the firehose and Me is
the summary.

### 6.4 Hosts

Tier-2. Unified list of team + personal hosts (§5). Each row:

- Name, hints, scope badge (`team`, `personal`, or `team+personal`)
- Terminal button (enabled iff device has creds)
- Assign-work affordance (enabled iff scope is `team`)

Host detail exposes: SSH hints, agent assignments, recent runs,
credential status, attach/rotate keys. **Keys, credentials, and SSH
identities attach to hosts here** — there is no separate top-level Keys
or Vault tab. Snippets (a separate concept, attached to terminal
sessions) live under a Snippets drawer entered from terminal screens.

Bootstrap / debug affordances (host-runner install, SSH reachability
test, health ping) live on host detail, entered only when something
needs diagnosis.

### 6.5 Device Settings (the `Settings` tab)

Tier-3. **This phone, this user** only. Scope is non-negotiable.

In:
- Theme, font, keyboard (custom keyboard toggle), language
- Export / Import backup (device-local data)
- Notification rules **on this device** (which team events ping this phone)
- API tokens **this device** uses to reach the hub
- Action bar profiles (device-local UI prefs)
- About / licenses

Not in Device Settings:
- Members, roles, team policy, team tokens, team budget → **Team Settings** (§6.6)
- Steward config → Team Settings → Steward (§6.7)
- Audit log → Activity tab (§6.3)
- SSH credentials, snippets → Host detail / Terminal drawer

The Device Settings tab is small and predictable. If a user has to think
about whether a toggle is team-wide or device-wide, it's in the wrong tab.

### 6.6 Team Settings / Governance (entered from the Team switcher)

**Not a tab.** A screen reached from the **Team switcher** in the top bar,
present on every tab. The switcher shows the active team name and, on
tap, reveals:

- Team switcher list (teams I belong to — MVP has one)
- **Team Settings** (this screen)
- New team / join team

Team Settings is Tier-2 and contains everything team-scoped that isn't a
primary workspace (Projects / Activity / Hosts already cover those):

- **Members** — who is in the team, what role they hold
- **Roles** — role definitions and what each role can do (future)
- **Policies** — the team's policy.yaml viewer/editor
- **Budgets** — team spend caps, per-project allowances
- **Tokens** — team-scoped API tokens (distinct from device tokens)
- **Councils** — N-of-M approval groups (future)
- **Audit filters** — team-level filters applied to Activity
- **Steward** — §6.7
- **Team channel** — link to the team-wide channel

This is the single governance entry point (IA-A5: roles are affordance
axes, not a separate nav tier — so governance slots under Team, not as a
sixth tab). Non-director roles see a read-only or scoped subset of this
screen; permissions gate buttons (IA-A5).

### 6.7 Steward

The steward (blueprint §3) is the primary operator — the LLM that runs
plans, schedules, and decisions on the team's behalf. It is not a person
or a tab; it is an **actor** that authors runs, reviews, documents, and
attention items like any other member. Accordingly, there is **no
Steward tab**. Instead, there are four distinct access points for
distinct intents:

| Intent | Where | How it works |
|---|---|---|
| **Direct the steward (project-scoped)** | Project detail → Channel | Steward is a participant in every project channel. Director types; steward responds. Scoped to that project's goal. |
| **Direct the steward (team-wide)** | Team switcher → Team channel | Same pattern at team scope (cross-project direction, policy clarification). |
| **Observe what the steward did** | Activity tab (filter: actor=steward) | Every steward-authored mutation lands in Activity. Filter for audit. |
| **Be notified when steward needs you** | Me tab → Attention | Steward escalates via `attention_items`; they land in Me. |
| **Configure the steward** | Team switcher → Team Settings → Steward | Autonomy level, budget caps, per-project scope, policy overrides, model & provider selection |

**What the Steward Settings screen holds (Tier-2):**

- Autonomy level (what the steward may do without human ratification)
- Budget caps (team-wide, per-project)
- Scope allowlist (which projects/hosts the steward may touch)
- Policy overrides (which policies gate vs. advise)
- Model / provider selection
- Councils and escalation paths (future)

**Why no Steward tab:** giving the steward a tab would frame it as a
separate app inside the app. In reality the steward's output is the
team's output — runs, reviews, documents, attention items — and those
already have their homes. Surfacing the steward in every project channel
makes it feel like a team member, which matches what it is.

### 6.8 Deferred: a sixth tab

MVP is five tabs. Candidates for a future sixth tab (and why we're
*not* adding them now):

- **Team tab** — rejected: governance is entered from the Team switcher;
  daily team workspace is already Projects + Activity.
- **Steward tab** — rejected: §6.7.
- **Reviews tab** — rejected: reviews I owe are in Me; reviews I
  authored live in Project detail → Reviews.
- **Search tab** — rejected: search is a capability (top bar), not a
  destination.

---

## 7. Entity × surface matrix

Single source of truth for *where every primitive lives*. Each row is one
entity. "Home" = canonical screen. "Referenced from" = read-only pointers
that must navigate back to Home, never duplicate the data.

| Entity | Scope | Home | Referenced from |
|---|---|---|---|
| Attention item | team/me | Me tab → item detail | Project overview, Activity, item source |
| Project | team | Projects tab → Project detail | Me (my work), Activity |
| Run | team (project-scoped) | Project detail → Runs → Run detail | Me, Activity, Host detail |
| Review | team | Project detail → Reviews → Review detail | Me (when assigned), Activity |
| Document | team (project-scoped) | Project detail → Documents → Doc viewer | Review detail, Activity |
| Agent | team (project-scoped) | Project detail → Agents | Host detail (assignment), Activity |
| Host | team and/or device | Hosts tab → Host detail | Project detail (assignments), Run detail (where it ran) |
| SSH credential | device | Host detail → Credentials | — (device-local, no remote refs) |
| SSH key | device | Host detail → Keys | — |
| Vault / secrets | device-per-host | Host detail → Secrets | Terminal sheet |
| Template / blueprint | team | Projects tab → Templates | Project create sheet, Plan create sheet |
| Plan | team (project-scoped) | Project detail → Plans → Plan viewer | Schedule detail, Template detail |
| Task | team (project-scoped) | Project detail → Tasks → Task detail | Plan viewer, Me (when assigned) |
| Schedule | team (project-scoped) | Project detail → Schedules | Activity (firings) |
| Audit event | team | Activity tab | Project detail (filtered), Run detail (filtered) |
| Channel | team or project | Project detail → Channel (project) · Activity → Team Channel (team) | Attention item source |
| Snippet | device | Terminal → Snippets drawer | Custom keyboard, action bar |
| Command history | device | Terminal → History sheet | — |
| Budget | team | Team Settings → Budgets | Project detail (badge) |
| Token (team) | team | Team Settings → Tokens | — |
| Token (device) | device | Device Settings → API tokens | — |
| Member | team | Team Settings → Members | Project detail (members chip) |
| Role | team | Team Settings → Roles | Member detail |
| Policy | team | Team Settings → Policies | Project detail (badge) |
| Council | team | Team Settings → Councils (future) | Review detail |
| Steward config | team | Team Settings → Steward | Project detail (steward status chip) |
| Steward channel surface | team or project | Team channel · Project → Channel | Me (attention from steward) |
| Team | team | Team switcher → Team Settings (governance) | Top bar on every tab |
| Notification rule | device | Device Settings → Notifications | Attention item source (historical) |
| Connection (legacy) | — | **Removed**; merged into Host | — |

Any screen that surfaces an entity and is **not** on the "Home" row of
this table is either a pointer or a misplacement. The migration table in
§10 enumerates every current screen's status against this matrix.

---

## 8. Forbidden IA patterns

Corollaries of the axioms and the entity matrix. Violation signals a
regression and requires explicit amendment of this document first.

1. **Two tabs rendering the same entity list.** Violates IA-A2.
   (Example: templates appearing under both Hub and Settings today.)
2. **Personal data under team surfaces, or team data under Settings.**
   Violates IA-A4. (Example: tokens, team management, and audit
   currently under Settings.)
3. **Tier-1 surface that is not attention-bearing or workspace.**
   Violates IA-A1 and IA-A3. (Example: SSH connection list as a
   top-level tab.)
4. **SSH or terminal as a parallel top-level tier alongside the hub.**
   Violates IA-A6. Terminal is a capability on hosts.
5. **Role-specific UI stack or mode switch.** Violates IA-A5. Roles
   gate affordances; they do not fork navigation.
6. **Settings screen that cannot be fully summarized as "device/personal
   prefs only".** Violates IA-A4.
7. **Duplicate create sheets for the same primitive reachable from
   multiple tabs.** A primitive has one Home (§7); create affordance
   lives there and is linked in from others.
8. **Tier-0 surface wider than one-screen scroll of "what needs me
   now" + my active work + digest.** Tier-0 is not a dashboard; it is
   a triage surface (IA-A1).
9. **Device-scoped state (credentials, snippets, theme) synced to hub.**
   Violates data ownership (§5.2) and blueprint forbidden pattern #15.
10. **A tab whose primary content is empty for role R.** The tab shape
    must hold for every role defined in §4. If it's empty for a role,
    that role shouldn't see it (role→tab matrix in §4).
11. **Conflating Device Settings with Team Settings.** They are two
    different entities with two different scopes (§3.5, §6.5 vs §6.6).
    A toggle whose state lives in SharedPreferences belongs in Device
    Settings; a toggle whose state lives in the hub belongs in Team
    Settings. Never mix in one screen.
12. **Giving the steward a tab.** Violates IA-A5 and §6.7. The steward
    is an actor, not a navigation destination. It is directed via
    channels, observed via Activity, notified via Me, configured via
    Team Settings → Steward.
13. **Governance as a sixth tab.** Violates IA-A3 (governance recedes)
    and §6.6. Team Settings is entered from the Team switcher in the
    top bar, not by adding a tab.
14. **Rendering steward chat as a coding agent.** Violates the
    manager / IC split (`agent-lifecycle.md` §4.9). Steward sessions
    are decision surfaces, not code surfaces — no branch/diff strip
    below the input, no Happy-style file-card tool calls in the
    transcript, no commit-status indicators. Those affordances belong
    to *worker* sessions, where there is a worktree to render. The
    only steward exception is the single-agent bootstrap window
    (`agent-lifecycle.md` §6.2), which is time-bounded and ends when
    workers can spawn.
15. **Conflating director ↔ steward 1:1 with the team `hub-meta`
    channel.** Violates `sessions.md` §8.5. The team channel
    is multi-party broadcast; a steward session is bounded
    director-only conversation that distills into an artifact on
    close. Pollution of the team channel with director musings is a
    leak, not a feature. Once the sessions wedge ships, the Me
    "Direct" FAB stops opening hub-meta and instead opens
    "Start session".

---

## 9. What is and isn't an Activity event

Activity is defined by the `audit_events` table. To keep the feed
load-bearing rather than noisy:

**In Activity (mutations of team-owned state):**

- Project / run / review / document / agent / host / attention /
  schedule / task / channel / budget / token / member / role state
  changes
- Steward decisions taken on the team's behalf
- Policy changes

**Not in Activity:**

- SSH terminal sessions (ephemeral, device-local)
- Personal settings changes (device-local)
- Read / view events (not state changes)
- Channel message contents (threads render themselves; Activity notes
  only that a channel was created or a significant policy change
  happened)
- Run metrics arrival past initial "run started" (too noisy; metrics
  live on run detail)

---

## 10. Migration table

Every current surface, with its target under the new IA. A "move"
preserves the screen; a "merge" collapses it into another; "delete"
removes it.

### 10.1 Top-level tabs

| Current | Action | Destination |
|---|---|---|
| Servers | merge | Hosts (unified with hub-side hosts) |
| Vault | split | Keys/credentials → Host detail; Snippets → Terminal drawer |
| Inbox | rename + scope | **Me** (strip SSH plumbing; add My Work + digest) |
| Hub | split | Projects tab · Activity tab · (Hosts tab absorbs hub hosts) |
| Settings | shrink | Personal prefs only; team/tokens/audit move out |

### 10.2 Hub sub-screens

| Current screen | Action | Destination |
|---|---|---|
| `hub_screen.dart` | split | Top tabs Projects / Activity / (Hosts extracted) |
| `hub_bootstrap_screen.dart` | move | Hosts tab → first-run flow |
| `project_detail_screen.dart` | keep | Projects tab → Project detail (unchanged) |
| `runs_screen.dart` | keep | Project detail → Runs |
| `reviews_screen.dart` | keep | Project detail → Reviews |
| `documents_screen.dart` | keep | Project detail → Documents |
| `doc_viewer_screen.dart` | keep | Project detail → Documents → viewer |
| `plans_screen.dart` | keep | Project detail → Plans |
| `plan_viewer_screen.dart` | keep | Project detail → Plans → viewer |
| `schedules_screen.dart` | keep | Project detail → Schedules |
| `task_detail_screen.dart` | keep | Project detail → Tasks |
| `templates_screen.dart` | promote | Projects tab → Templates (single home) |
| `workflows_screen.dart` | evaluate | Merge into Templates if overlap, else Project detail → Workflows |
| `audit_screen.dart` | promote | Activity tab |
| `search_screen.dart` | promote | Capability (top bar on every tab) |
| `search_event_sheet.dart` | keep | Capability sheet |
| `archived_agents_screen.dart` | merge | Project detail → Agents → Archive filter |
| `budget_screen.dart` | move | **Team Settings → Budgets** (entered from Team switcher) |
| `tokens_screen.dart` | split | Team-scoped tokens → **Team Settings → Tokens**; device tokens → **Device Settings → API tokens** |
| `team_screen.dart` | split + move | Becomes **Team Settings** screen reached from the top-bar Team switcher. Its current sub-tabs map as: Members → Team Settings → Members · Policies → Team Settings → Policies · Channels → split (team channel → Team switcher → Team channel; project channels → Project detail → Channel) · Settings → Team Settings root page |
| `team_channel_screen.dart` | move | Team switcher → Team channel (single team-wide channel surface) |
| `project_channel_screen.dart` | keep | Project detail → Channel (steward-facing surface; §6.7) |
| `host_edit_sheet.dart` | keep | Hosts tab → Host detail sheet |
| `blobs_section.dart` | keep | Referenced from Run detail & Host detail |
| Create sheets (project/plan/run/task/schedule/channel/…) | keep | Launched from the primitive's Home |
| (new) Steward Settings screen | **add** | Team Settings → Steward (§6.7) — autonomy, budget caps, scope, model |
| (new) Team switcher top-bar widget | **add** | Top bar of every tab — active team badge + dropdown; single governance ingress |

### 10.3 Non-hub top-level

| Current | Action | Destination |
|---|---|---|
| `connections/` | delete as top-level | Hosts absorbs; connection = host with `personal` scope |
| `keys/` | delete as top-level | Host detail → Credentials |
| `vault/` | split | Keys → Host detail; Snippets → Terminal drawer |
| `notifications/` | move | Settings → Notifications (device rules) |
| `inbox/` | rename | Me |
| `terminal/` | keep | Launched from Host detail (capability) |
| `settings/` | shrink to device-only | Keeps: theme, font, keyboard, language, export/import, notification rules (device), API tokens (device), action bar profiles, about. Moves out: any team-scoped setting, tokens-to-the-team, audit, team management, members, policies, budgets, steward config — all to **Team Settings** (reached from Team switcher). |
| `settings/action_bar_settings_screen.dart` | keep | Device Settings → Action bar (device-local UI pref) |
| `settings/file_browser_screen.dart` | keep | Device Settings → Files (device-local) |
| `settings/licenses_screen.dart` | keep | Device Settings → About → Licenses |

---

## 11. Execution plan

The redesign shipped as 7 wedges across v1.0.175–v1.0.182. Listed
here for archaeology — what landed and in what order. Order reflects
decreasing structural leverage — the earliest wedges unblocked the
later ones.

**Wedge 1 ✅ — Nav skeleton.** Rename Inbox→Me, Hub→Projects, add Activity
tab, replace Servers with Hosts (stub; still lists current SSH
bookmarks). Settings shrunk to prefs-only, with orphaned sub-pages
linked from their new homes. No entity moves yet.

**Wedge 2 ✅ — Host unification.** Hub's hosts and SSH bookmarks join on
`host_id`. Introduce scope field. Terminal button on host detail. Keys
& credentials attach to host. Delete `connections/`, `keys/`, `vault/`
as top-level.

**Wedge 3 ✅ — Me tab shape.** Attention + My Work + digest. Pull reviews
and run-follows into Me. Strip non-attention noise.

**Wedge 4 ✅ — Projects tab shape.** Consolidate templates to one home.
Project detail density pass: nested sub-tabs instead of sprawl.

**Wedge 5 ✅ — Activity tab shape.** Promote audit_screen to top tab.
Filters, digest card mirror to Me.

**Wedge 6 ✅ — Team switcher + Team Settings scaffold.** Add persistent
top-bar Team switcher to every tab (MVP shows one team). Tapping it
opens Team Settings: Members, Policies, Budgets, Tokens (team),
Councils (stub), Steward config (stub). Migrate `team_screen.dart` +
`tokens_screen.dart` + `budget_screen.dart` in here. Device Settings
shrinks to device-only in the same wedge (the move-out and move-in are
paired).

**Wedge 7 ✅ — Steward surface.** Formalize the steward as a first-class
actor: render steward messages in every project channel and in a new
team channel; surface steward-initiated attention items on Me; add
filter `actor=steward` to Activity; build the Team Settings → Steward
config screen (autonomy, budget caps, scope allowlist, model
selection). No new tab.

All 7 wedges shipped. Subsequent steward UX work (v1.0.281–v1.0.300)
followed in `../plans/steward-ux-fixes.md`.

### Follow-ups (unscheduled)

These are known gaps in the as-shipped IA. They are not wedged onto
the roadmap until a concrete use case forces them, but they are listed
here so future work does not have to re-derive them.

**F-1. Per-member stewards (multi-user teams).** Today the steward is
team-scoped: one `agents` row with `handle='steward'`, referenced by
`projects.steward_agent_id` and stamped on `attention_items` /
`audit_events` via the new `actor_kind='agent'` + `actor_handle` columns
(migration 0016). Prompts read `{{principal.handle}}` as a single
per-team value. This is correct for the single-director MVP.

When a team onboards a second member, the intended model is **one
steward per member, acting as that member's deputy** with their own
preferences, memory, budget envelope, and policy overrides. The
changes are additive and mostly schema-level:

- Add `users` and `team_members(user_id, team_id, role)` tables. Today
  the only non-agent principal is the team's "owner" auth_token; this
  promotes the concept to a row.
- Add nullable `agents.owner_user_id` FK. NULL preserves today's
  team-owned behavior (e.g. shared briefing stewards); non-NULL marks
  a per-member deputy.
- Relax `UNIQUE(team_id, handle)` to `UNIQUE(team_id, owner_user_id,
  handle)` so every member can have their own `steward`.
- Steward resolution becomes user-scoped: "find steward where team=X
  and owner_user=me." Projects either store `steward_user_id` directly
  or resolve via the project creator's membership.
- Per-user memory needs no new storage — each steward agent already
  has its own `journal_path`, budget, and policy overrides.
- StewardBadge matcher is unaffected: it already matches on `handle`,
  which stays `steward` across all deputies.
- Prompts template `{{principal.handle}}` per-call instead of per-team.

No IA axiom changes. Role ontology (§4) already calls Director and
Steward *roles*, not identities, so per-member deputies are compatible
with the intent. Ship when the first second-member story lands.

---

## 12. Amendment process

This document is the authority on IA. Changes to it require:

1. A proposal describing the new axiom, entity placement, or surface
   change, referencing which existing axiom it amends or extends.
2. An update to the entity × surface matrix (§7) and the forbidden
   patterns (§8) to reflect the change.
3. A migration note if the change moves an existing primitive.

PRs that add tabs, screens, or top-level menu items without citing a
clause here are candidates for rejection. Silent drift is the failure
mode this document exists to prevent.
