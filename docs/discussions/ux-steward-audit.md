# Mobile UX audit — steward-CEO lens

> **Type:** discussion
> **Status:** Resolved (audit completed 2026-04-23; queue of identified gaps closed across v1.0.151–v1.0.156, see ADR-005)
> **Audience:** contributors
> **Last verified vs code:** v1.0.312

**TL;DR.** Read-only audit of every mobile screen vs the shipped MCP
tool set as of v1.0.151. Lens: user = owner/principal (ratifies
ownership-level calls); steward = CEO-class operator (default
authority over every system surface). The audit produced ADR-005
(`../decisions/005-owner-authority-model.md`) and a queue of MCP
gaps that closed v1.0.151–v1.0.156.

A mobile CRUD screen whose primary action has **no steward-callable
MCP tool** is an **infra gap**, not an intentional boundary.

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
| `lib/screens/projects/projects_screen.dart` | list/filter projects | `projects.list` |
| `lib/screens/projects/project_detail_screen.dart` | read-only view of channels/tasks/agents/docs | list tools |
| `lib/screens/projects/plans_screen.dart` | list plans | `plans.list`, `plans.get` |
| `lib/screens/projects/plan_viewer_screen.dart` | view plan + steps | `plans.get`, `plans.steps.list` |
| `lib/screens/projects/runs_screen.dart` | list/detail runs | `runs.list`, `runs.get` |
| `lib/screens/projects/documents_screen.dart` | list docs | `documents.list` |
| `lib/screens/projects/reviews_screen.dart` | list/filter reviews | `reviews.list` |
| `lib/screens/team/audit_screen.dart` | list audit events | `audit.read` |
| `lib/screens/team/templates_screen.dart` | list templates | read-only |
| `lib/screens/projects/archived_agents_screen.dart` | list archived agents | read-only |
| `lib/screens/team/team_screen.dart` (Members) | list principals | read-only |
| `lib/screens/projects/project_create_sheet.dart` | create project | `projects.create` |
| `lib/screens/projects/plan_create_sheet.dart` | create plan | `plans.create` |
| `lib/screens/projects/plan_step_create_sheet.dart` | create plan step | `plans.steps.create` |
| `lib/screens/projects/document_create_sheet.dart` | create document | `documents.create` |
| `lib/screens/projects/run_create_sheet.dart` | create run | `runs.create` |
| Project/team channel event post | post channel message/event | `channels.post_event` |
| `schedule_create_sheet.dart` | create schedule | `schedules.create` (v1.0.153) |
| `schedule_edit_sheet.dart` | patch / delete schedule | `schedules.update`, `schedules.delete` (v1.0.153) |
| `schedules_screen.dart` | run now | `schedules.run` (v1.0.153) |
| Schedules list | list schedules | `schedules.list` (v1.0.153) |
| `task_detail_screen.dart` | create / patch task | `tasks.create`, `tasks.update` (v1.0.154) |
| Project tasks list | list tasks | `tasks.list` (v1.0.154) |
| `project_channel_create_sheet.dart` | create project channel | `project_channels.create` (v1.0.155) |
| `team_channel_screen.dart` | create team channel | `team_channels.create` (v1.0.155) |
| `project_edit_sheet.dart` | patch project | `projects.update` (v1.0.156) |
| `host_edit_sheet.dart` | patch host SSH hint | `hosts.update_ssh_hint` (v1.0.156) |

## 2. Infra gaps — demo-critical

These mobile CRUD actions have a REST endpoint the steward cannot reach.
Each row is a future MCP wedge.

| Screen | Action | REST | Proposed tool | Inputs |
|---|---|---|---|---|

_(all demo-critical rows closed as of v1.0.156)_

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

All rows below landed in **v1.0.160** as a conservative pass: FABs
demoted into AppBar overflow menus (where a screen had its own bar),
sort order reshuffled to lead with in-flight work, and #hub-meta
pinned on the team Channels tab. Activity-feed / recent-tasks
surfacing in the projects tab is deferred — it requires new data
wiring on top of the existing inventory view.

| Screen | Reframe | Status |
|---|---|---|
| `projects_screen.dart` (in `hub_screen.dart`) | Lead with activity feed / recent tasks; demote "+ New Project" to overflow menu | Partial v1.0.160 (FAB shrunk to `.small`, empty-state copy updated; activity-feed surfacing deferred — needs new wiring) |
| `plans_screen.dart` | Default to In-Progress + Pending filters; "Create Plan" as secondary | DONE v1.0.160 (synthetic `active` filter as default, FAB → AppBar menu) |
| `schedules_screen.dart` | Summary row: enabled schedules + next-fire countdown; "+ New" in menu | DONE v1.0.160 |
| `documents_screen.dart` | Sort: pending reviews → drafts → reports; "Create Doc" in menu | DONE v1.0.160 |
| `team_screen.dart` Channels tab | Pre-populate #hub-meta activity; defer channel-creation UI to secondary sheet | Partial v1.0.160 (hub-meta pinned first; activity pre-population deferred) |
| `runs_screen.dart` | Sort by status (running → succeeded → failed); emphasise "Monitor active runs" mental model | DONE v1.0.160 |

## Suggested wedge groupings

Treating one wedge per logical hub surface so the steward regains parity:
1. ~~**Scheduling surface** — `schedules.{list,create,update,delete,run}`.~~ **DONE v1.0.153.**
2. ~~**Project tasks surface** — `tasks.{list,create,update}`.~~ **DONE v1.0.154.**
3. ~~**Channel authoring** — `project_channels.create` + `team_channels.create`.~~ **DONE v1.0.155.**
4. ~~**Project + host patch** — `projects.update`, `hosts.update_ssh_hint`.~~ **DONE v1.0.156.**

All four wedges have landed. The mobile CRUD surface is now entirely
redundant as a *default* path — each screen becomes a review/ratify
surface on top of what the steward has already authored. Section 4
reframes (demote "+ New" across hub screens, lead with activity /
status feeds) can now proceed as pure UX work without leaving the
steward behind.
