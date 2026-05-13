# Project steward accountability — implementation plan

> **Type:** plan
> **Status:** Foundation shipped; enforcement pending (2026-05-13)
> **Audience:** contributors
> **Last verified vs code:** v1.0.571

**TL;DR.** Implementation tracker for [ADR-025](../decisions/025-project-steward-accountability.md). Workers become project-scoped first-class agents with their own session; every engaged project gets exactly one steward (lazy materialization with director consent); the general steward routes intent down to project stewards but does not spawn workers itself. Eleven wedges across v1.0.564 (foundation: schema + lazy steward + worker session + visibility) and v1.0.565 (enforcement + UI rerouting). Order is the safe-to-ship sequence; the foundation lands first and is observed for one release before the gating tightens.

---

## 1. Goal

After this plan:

- Project Agents tab populates with the project's steward + workers — first time it's been non-empty for a project that ever spawned a worker.
- Workers debuggable via their own session in the mobile session viewer; steward↔worker A2A conversation observable in full.
- General steward routes a "spawn worker for project X" intent down to project X's steward instead of spawning the worker itself. Accountability chain is single.
- Director consents to every steward spawn via a host-picker sheet (1 host or N, same flow).
- Worker templates are M2 by default; pane lands in a project-scoped workdir; trust-folder prompt for `$HOME` is gone.

## 2. Non-goals

- **Per-member stewards** (F-1 / ADR-004). Still deferred.
- **Cross-project worker reassignment.** A worker is bound to one project at spawn; if the user wants the same worker in another project, they spawn a new one. Reassignment is a future wedge.
- **Sub-worker spawn chains.** D3 gate is "project steward spawns workers." Workers spawning workers (multi-level fanout) keeps the existing rules from ADR-008 / ADR-016 — out of scope here.
- **Backfill of pre-ADR worker rows.** Old workers without `project_id` stay legacy; no retroactive project binding.
- **Removing the escape-valve direct-spawn paths from mobile.** D6 reroutes the *default* path; the bypass stays available behind an "advanced" flag. Removing it entirely is a separate wedge.

## 3. Vocabulary

- **Project steward** — the (now exactly-one) steward owning a project's worker spawn authority. Created lazily on first engagement.
- **Worker session** — `sessions` row with `scope_kind='project'`, `scope_id=<pid>`, `current_agent_id=<worker_id>`. Materialized atomically with the worker agent row.
- **Engagement** — first interaction with a project that requires a live steward: director taps project steward overlay, sends a message into it, or a peer agent delegates a project-scoped intent.
- **Host-picker sheet** — bottom sheet exposing host / model / permission-mode pickers, prefilled per the D4 ladder. Single point of consent for both engagement-initiated and delegation-initiated spawns.

## 4. Surfaces affected

| Surface | Change | Wedge |
|---|---|---|
| `agents` table | New nullable `project_id` column + index | W1 |
| `hub/internal/server/spawn_mode.go` | Parse `project_id` from spawn YAML | W2 |
| `hub/internal/server/handlers_agents.go` | `spawnIn.ProjectID`; persist on insert; return in `agentOut`; worker session at spawn | W2, W4 |
| `hub/internal/server/handlers_general_steward.go` (pattern reuse) | New `ensureProjectSteward()` helper modeled on `ensureGeneralSteward` | W3 |
| `hub/internal/server/server.go` | New route `POST /v1/teams/{team}/projects/{project}/steward/ensure` | W3 |
| `hub/templates/agents/{briefing,coder,critic,lit-reviewer,ml-worker,paper-writer}.v1.yaml` | Add `driving_mode: M2` + `fallback_modes: [M4]`; drop per-template `default_workdir` overrides | W6 |
| `hub/internal/hostrunner/launch_m2.go` | When `spec.Backend.DefaultWorkdir` is empty AND spawn has `project_id`, derive `~/hub-work/<pid-prefix>/<handle>` | W6 |
| `hub/internal/hubmcpserver/tools.go` | `agents.list` accepts `project_id` filter | W5 |
| `lib/screens/projects/project_detail_screen.dart` | Agents tab gains empty-state CTA + project-steward materialization flow | W7 |
| `lib/widgets/steward_overlay/*` | Project-scoped overlay surfaces handle "no steward yet" empty state | W7 |
| `lib/widgets/spawn_steward_sheet.dart` (new) | Host/model/permission picker; same widget for engagement + delegation paths | W7 |
| `lib/screens/sessions/sessions_screen.dart` | Scope chip on each row (`Team` / `Project: <name>`) | W8 |
| `lib/services/hub/hub_client.dart` | `ensureProjectSteward()`, scope-aware session helpers | W7, W8 |
| `hub/internal/server/mcp_authority_roles.go` | `agents.spawn` gate: `project_id` set ⇒ caller must be that project's steward (D3) | W9 |
| `hub/templates/prompts/steward.general.v1.md` | Delegation routing pattern for "user asked me to operate in a project" | W10 |
| `lib/screens/projects/spawn_agent_sheet.dart` | FAB route changes from "direct spawn" to "intent → A2A to project steward"; advanced bypass behind toggle | W10 |
| `lib/widgets/agent_config_sheet.dart` | Read-only by default; "Ask steward to reconfigure" CTA replaces direct PATCH | W11 |

## 5. Wedges

Each wedge is one commit + one version bump. Foundation **shipped** at v1.0.564-v1.0.571 (W1-W8, one wedge per minor version). Enforcement and UI rerouting (W9-W11) follow at v1.0.572+ once the foundation has soaked. (v1.0.557–v1.0.563 were claimed by successive steward-overlay IME hotfixes; v1.0.561 introduced the ghost-FocusNode focus-bounce, v1.0.562 extended it to delete/replace cases, v1.0.563 pinned the input border color to eliminate visual flicker.)

### Foundation (SHIPPED v1.0.564–v1.0.571)

**W1 (v1.0.564). Migration: `agents.project_id`.** SHIPPED.
- Migration 0040 (not 0042 as initially planned — sequential numbering): `ALTER TABLE agents ADD COLUMN project_id TEXT REFERENCES projects(id) ON DELETE SET NULL`. Index on the non-NULL subset.
- No backfill. Pre-ADR rows stay NULL.
- `agentOut` carries `project_id`; `handleListAgents` + `handleGetAgent` SELECT it; `?project_id=` filter on the REST endpoint.
- Tests cover round-trip insert + list + project filter + ON DELETE SET NULL behavior.

**W2 (v1.0.565). Spawn flow plumbing.** SHIPPED.
- `spawnModeYAML` (`spawn_mode.go`) parses `project_id:` from the rendered YAML.
- `spawnIn.ProjectID` body field is the precedence-low fallback (rare); YAML wins.
- Persisted on the `agents` row insert via `NULLIF(?, '')`.
- Tests cover YAML, body, YAML-beats-body, NULL fallthrough.

**W3 (v1.0.566). `ensureProjectSteward` endpoint.** SHIPPED.
- `handleEnsureProjectSteward` modeled on the general-steward variant. Looks up `agents.kind LIKE 'steward.%' AND project_id = ? AND status NOT IN ('terminated','crashed','failed') AND archived_at IS NULL`; spawns a fresh `steward.v1` when no live match.
- Route `POST /v1/teams/{team}/projects/{project}/steward/ensure` accepts `{host_id, permission_mode, kind}`. Per-project handle `@steward.<pid[:8]>` avoids the team-singleton `@steward` collision.
- Updates `projects.steward_agent_id` on success so the existing field stays authoritative.
- Tests: first-spawn + project_id binding, idempotent repeat, archive-respawn, no-host (424), unknown project (404), cross-team isolation, pinned host honored.

**W4 (v1.0.567). General-steward delegation attention item.** SHIPPED.
- New MCP tool `request_project_steward` (sibling of `request_help` / `request_select`; the plan's `attention.raise_project_steward_request` was renamed to follow the `request_*` convention).
- Args: `{project_id, reason, suggested_host_id}`. Validates project belongs to the caller's team.
- Creates an attention_items row with `kind='project_steward_request'`, `scope_kind='project'`, severity `major`. Steward role's `allow_all: true` grants the tool; workers remain denied.
- Tests cover happy-path persistence, required-field validation, unknown-project rejection, cross-team isolation.

**W5 (v1.0.568). MCP `agents.list?project_id=` filter.** SHIPPED.
- `agents.list` MCP tool schema and call gain `project_id`. Threads through to the W1 REST filter.
- `docs/reference/hub-mcp.md` annotation updated; planned-then-shipped markers cleared.

**W6 (v1.0.569). Worker templates + workdir convention.** SHIPPED.
- All 6 worker templates carry `driving_mode: M2` + `fallback_modes: [M4]`.
- coder / critic / lit-reviewer / paper-writer dropped their per-handle `default_workdir`. briefing + ml-worker kept their team-shared `~/hub-work`.
- `launch_m2.go` derives `~/hub-work/<pid[:8]>/<handle>` when DefaultWorkdir is empty AND the spawn carries `project_id`; creates the directory tree.
- Tests cover the derivation path and the explicit-default-wins fallback.

**W7 (v1.0.570). Mobile: project steward materialization + host-picker sheet.** SHIPPED.
- New widget `showSpawnProjectStewardSheet` (`lib/widgets/spawn_project_steward_sheet.dart`). Host picker + permission-mode chips + Spawn button. POSTs the W3 endpoint, refreshes the hub snapshot on success.
- `HubClient.ensureProjectSteward(...)` + `listAgents({projectId})` (with cached variant) added.
- Project detail Agents tab empty state replaces the bare placeholder with a `Spawn project steward` CTA.
- Default host ladder per ADR-025 D4: suggested > first online > first available. (Sibling-project + project-template hints will layer on once `projects.host_hint` exists — see open question §6.)
- Deferred: steward-overlay empty-state mirror and attention-tap deep link (those land alongside W10/W11 rerouting).

**W8 (v1.0.571). Worker auto-session + sessions screen filter.** SHIPPED.
- DoSpawn auto-opens a `scope_kind='project'` sessions row whenever `in.ProjectID` resolves non-empty (was: only when `AutoOpenSession=true`). Realizes ADR-025 D5 ("worker is born with its session").
- Mobile sessions screen builds a `workerSessionAgentIDs` skip-set and filters worker sessions out of the "Detached sessions" bucket — they live on the project detail Agents tab instead.
- The per-row "Scope chip" the plan called for is already provided by the existing group-header label (`General` / `Project: <name>`); adding a literal chip per row would visually duplicate the header.
- Tests cover the auto-open path and confirm the swap branch doesn't double-create.

### v1.0.572+ — Enforcement + UI rerouting (PENDING)

**W9. Hub role gate: `agents.spawn` project-binding check.**
- New gate in `mcp_authority_roles.go` (or a sibling file): when `agents.spawn` request has `project_id` set, the caller's `parent_agent_id` must match `projects.steward_agent_id` for that project. Otherwise 403.
- General steward (`kind = 'steward.general.v1'`) is blocked from `agents.spawn` outright — falls through to delegation (raises the W4 attention item).
- Tests: project steward can spawn into its project; general cannot; worker cannot spawn workers.

**W10. UI rerouting: spawn-agent FAB routes through steward.**
- Mobile `[+ Spawn Agent]` FAB on project Agents tab: default path becomes "draft an intent message to the project steward" — opens the steward overlay scoped to that project with a prefilled chat ("Spawn a [template] with params: …").
- Direct-spawn (bypass) moves behind an "Advanced" toggle in settings or a long-press gesture.
- General steward prompt: add a paragraph teaching the routing pattern. "If the principal asks you to operate in project X and project X has a steward, send your suggestion as an A2A to that steward. If it doesn't, raise an attention item."

**W11. `agent_config_sheet` read-only + steward-mediated edits.**
- Existing PATCH controls hidden by default.
- Read-only viewer (already mostly there in v1.0.554) becomes the default.
- New CTA "Ask steward to reconfigure" → opens the project's steward overlay with a prefilled "Please reconfigure agent <handle>: …" message.
- Direct PATCH still reachable via the "Advanced" toggle from W10.

## 6. Open questions tracked here, not in the ADR

- **Worker-session naming when the principal hasn't typed a title.** Default to `<handle>` (e.g. `ml-worker.v1`). Renaming uses the existing session-rename flow. (Settled in discussion; recording for traceability.)
- **What to show in the host-picker for a beefy host that's already running 5 agents?** A "current load" hint next to the host name. Implementation: query `agents` count per host, render `gpu-01 · 4 agents running`.
- **`projects.host_hint` template field.** Not yet a real field; W7's default ladder skips it gracefully if absent. A future template-schema wedge adds it.
- **Delegation message UX when general steward raises the W4 attention item.** Free text or structured fields? Suggest structured: `{project_id, intent, suggested_template, suggested_host_id}`. Lets the sheet prefill cleanly.

## 7. Test plan

Per wedge:

- W1-W6 (server): Go unit tests for handlers + schema migrations; existing `hub` test suite runs as part of CI.
- W7-W8 (mobile): manual test on physical device — spawn flow end-to-end, empty state, attention-item flow, scope chip render, worker session viewer. Flutter test coverage where unit-testable (the host-picker sheet's default ladder is a pure function and gets a test).
- W9 (gate): Go unit tests for each of the four cases — project-steward-allowed, general-blocked, unrelated-parent-blocked, no-project_id-fallthrough.
- W10-W11 (UI rerouting): manual test on physical device.

Risk hotspots:

- The host-picker sheet is new UI; the v1.0.552-554 IME bug is fresh. Use existing `agent_compose.dart` patterns (no `_ctrl.addListener` listener storms; no conditional Row children without keys) when building it.
- The `ensureProjectSteward` race: two simultaneous engagement triggers could both try to spawn. Use the same unique-handle coalescing pattern as `ensureGeneralSteward`.

## 8. Out of scope (for this plan, not for the project)

- Per-template `host_hint` field (deferred to a future template-schema wedge).
- Worker-spawning-worker chains beyond what ADR-016 / ADR-008 already permit.
- Cross-project worker reassignment.
- Removing the escape-valve direct-spawn paths entirely (they stay as advanced bypasses).
- Project deletion / archival lifecycle for project stewards (no change — existing archive flow handles it).

## 9. References

- [ADR-025](../decisions/025-project-steward-accountability.md) — the decision this plan implements.
- [Discussion: project-steward-accountability](../discussions/project-steward-accountability.md) — the why.
- [ADR-017](../decisions/017-layered-stewards.md) D6 — original "at most one domain steward" rule that ADR-025 tightens.
- [ADR-016](../decisions/016-subagent-scope-manifest.md) — the role manifest the W9 gate extends.
- [ADR-023](../decisions/023-agent-driven-mobile-ui.md) — director-as-principal framing W10/W11 finishes.
- [Plan: team-peer-stewards](team-peer-stewards.md) — sister plan for the peer-steward amendment; same `ensureSteward` helper pattern.
