# Mobile UX Audit — Steward-CEO Lens (2026-04-23)

Read-only audit of every mobile screen vs the shipped MCP tool set as of
v1.0.151. Applied together: user = owner/principal (ratifies
ownership-level calls); steward = CEO-class operator (default authority
over every system surface).

A mobile CRUD screen whose primary action has **no steward-callable MCP
tool** is an **infra gap**, not an intentional boundary. This document
is the queue of infra gaps + UX restructure candidates that fall out of
the CEO principle.

## Shipped MCP tools (v1.0.151)

`projects.{list,create,get}`, `plans.{list,create,get}`,
`plans.steps.{create,list,update}`, `runs.{list,get,create}`,
`agents.spawn`, `documents.{list,create}`, `reviews.{list,create}`,
`channels.post_event`, `a2a.invoke`, `policy.read`, `audit.read`.

## 1. Full coverage (no action)

| Screen | Actions | Covered by |
|---|---|---|
| `lib/screens/hub/projects_screen.dart` | list/filter projects | `projects.list` |
| `lib/screens/hub/project_detail_screen.dart` | read-only view of channels/tasks/agents/docs | list tools |
| `lib/screens/hub/plans_screen.dart` | list plans | `plans.list`, `plans.get` |
| `lib/screens/hub/plan_viewer_screen.dart` | view plan + steps | `plans.get`, `plans.steps.list` |
| `lib/screens/hub/runs_screen.dart` | list/detail runs | `runs.list`, `runs.get` |
| `lib/screens/hub/documents_screen.dart` | list docs | `documents.list` |
| `lib/screens/hub/reviews_screen.dart` | list/filter reviews | `reviews.list` |
| `lib/screens/hub/audit_screen.dart` | list audit events | `audit.read` |
| `lib/screens/hub/templates_screen.dart` | list templates | read-only |
| `lib/screens/hub/archived_agents_screen.dart` | list archived agents | read-only |
| `lib/screens/hub/team_screen.dart` (Members) | list principals | read-only |
| `lib/screens/hub/project_create_sheet.dart` | create project | `projects.create` |
| `lib/screens/hub/plan_create_sheet.dart` | create plan | `plans.create` |
| `lib/screens/hub/plan_step_create_sheet.dart` | create plan step | `plans.steps.create` |
| `lib/screens/hub/document_create_sheet.dart` | create document | `documents.create` |
| `lib/screens/hub/run_create_sheet.dart` | create run | `runs.create` |
| Project/team channel event post | post channel message/event | `channels.post_event` |
| `schedule_create_sheet.dart` | create schedule | `schedules.create` (v1.0.153) |
| `schedule_edit_sheet.dart` | patch / delete schedule | `schedules.update`, `schedules.delete` (v1.0.153) |
| `schedules_screen.dart` | run now | `schedules.run` (v1.0.153) |
| Schedules list | list schedules | `schedules.list` (v1.0.153) |

## 2. Infra gaps — demo-critical

These mobile CRUD actions have a REST endpoint the steward cannot reach.
Each row is a future MCP wedge.

| Screen | Action | REST | Proposed tool | Inputs |
|---|---|---|---|---|
| `project_edit_sheet.dart` | patch project | `PATCH /v1/teams/{t}/projects/{id}` | `projects.update` | project_id, name?, goal?, kind?, template_id?, parameters?, budget_cents? |
| `task_detail_screen.dart` | create task | `POST /v1/teams/{t}/projects/{p}/tasks` | `tasks.create` | project_id, title, body_md?, status?, assignee_id? |
| `task_detail_screen.dart` | patch task | `PATCH /v1/teams/{t}/projects/{p}/tasks/{id}` | `tasks.update` | project_id, task_id, status?, title?, body_md? |
| `project_channel_create_sheet.dart` | create project channel | `POST /v1/teams/{t}/projects/{p}/channels` | `project_channels.create` | project_id, name |
| `team_channel_screen.dart` | create team channel | `POST /v1/teams/{t}/channels` | `team_channels.create` | name |
| `host_edit_sheet.dart` | patch host SSH hint | `PATCH /v1/teams/{t}/hosts/{id}/ssh_hint` | `hosts.update_ssh_hint` | host_id, ssh_hint_json |

## 2a. Infra gaps — nice-to-have

_(none currently — `schedules.delete` closed in v1.0.153.)_

## 3. Legitimate manual-only

These stay device-local even under the CEO model because the steward
has no business reaching the underlying state.

| Screen | Reason |
|---|---|
| `lib/screens/keys/*` | SSH private-key material; device-local crypto only |
| `lib/screens/connections/connection_form_screen.dart` | Connection secrets (password, passphrase, proxy creds) must not transit to hub |
| `lib/screens/settings/settings_screen.dart` | Terminal rendering / local prefs |
| `lib/screens/terminal/terminal_screen.dart` | Raw PTY I/O; real-time local execution |
| `lib/screens/vault/vault_screen.dart` | Biometric-gated local snippet/cred vault |
| `lib/screens/settings/action_bar_settings_screen.dart` | Device input preferences |

## 4. UX restructure candidates

Screens that technically have steward coverage but default the user
into operator mode — lots of visible "+ New" buttons, forms-first
layouts. Each row is a reframe suggestion, not a net-new wedge.

| Screen | Reframe |
|---|---|
| `projects_screen.dart` | Lead with activity feed / recent tasks; demote "+ New Project" to overflow menu |
| `plans_screen.dart` | Default to In-Progress + Pending filters; "Create Plan" as secondary |
| `schedules_screen.dart` | Summary row: enabled schedules + next-fire countdown; "+ New" in menu |
| `documents_screen.dart` | Sort: pending reviews → drafts → reports; "Create Doc" in menu |
| `team_screen.dart` Channels tab | Pre-populate #hub-meta activity; defer channel-creation UI to secondary sheet |
| `runs_screen.dart` | Sort by status (running → succeeded → failed); emphasise "Monitor active runs" mental model |

## Suggested wedge groupings

Treating one wedge per logical hub surface so the steward regains parity:
1. ~~**Scheduling surface** — `schedules.{list,create,update,delete,run}`.~~ **DONE v1.0.153.**
2. **Project tasks surface** — `tasks.{create,update}`. Steward needs this to break work down in the project view.
3. **Channel authoring** — `channels.create` (both project- and team-scope). Steward needs this before posting.
4. **Project + host patch** — `projects.update`, `hosts.update_ssh_hint`. Small but closes the ownership-level edit path.

After those four land, the mobile CRUD surface is entirely redundant
as a *default* path — each screen becomes a review/ratify surface on
top of what the steward has already authored (Section 4 reframes can
proceed from there).
