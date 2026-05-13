# Project steward accountability — implementation plan

> **Type:** plan
> **Status:** Proposed (2026-05-13)
> **Audience:** contributors
> **Last verified vs code:** v1.0.556

**TL;DR.** Implementation tracker for [ADR-025](../decisions/025-project-steward-accountability.md). Workers become project-scoped first-class agents with their own session; every engaged project gets exactly one steward (lazy materialization with director consent); the general steward routes intent down to project stewards but does not spawn workers itself. Eleven wedges across v1.0.562 (foundation: schema + lazy steward + worker session + visibility) and v1.0.563 (enforcement + UI rerouting). Order is the safe-to-ship sequence; the foundation lands first and is observed for one release before the gating tightens.

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

Each wedge is one commit + one version bump. Foundation (W1-W8) ships in v1.0.562; enforcement and UI rerouting (W9-W11) follow in v1.0.563 once the foundation has soaked for a release. (v1.0.557–v1.0.561 were claimed by successive steward-overlay IME hotfixes; v1.0.561 is the ghost-FocusNode focus-bounce that automates the user's manual focus-switch workaround.)

### v1.0.562 — Foundation

**W1. Migration: `agents.project_id`.**
- Migration 0042: `ALTER TABLE agents ADD COLUMN project_id TEXT REFERENCES projects(id) ON DELETE SET NULL`. Index on the non-NULL subset.
- No backfill. Pre-ADR rows stay NULL.
- Update `agentOut` to include `project_id`; update `handleListAgents` query SELECT list; add optional `?project_id=` filter on the REST endpoint.
- Tests: round-trip insert + list + project filter.

**W2. Spawn flow plumbing.**
- `spawnModeYAML` (`spawn_mode.go`) extends to parse `project_id` from the YAML.
- `spawnIn.ProjectID` field (`handlers_agents.go`); precedence: YAML `project_id:` > spawn body field (rare). Persisted on the `agents` row insert.
- Existing spawn paths (mobile spawn sheet, MCP `agents.spawn`, internal handlers) flow through unchanged — they all build YAML with `project_id:` already (mobile) or can pass it explicitly (MCP).
- Tests: spawn with `project_id:` lands on row; spawn without leaves NULL.

**W3. `ensureProjectSteward` endpoint.**
- New helper `ensureProjectSteward(team, project, host, model, permission)` modeled on `ensureGeneralSteward`. Idempotent: returns existing steward when `agents.kind LIKE 'steward.%' AND project_id = <pid> AND status IN ('pending','running')` matches; otherwise spawns a fresh one.
- New route `POST /v1/teams/{team}/projects/{project}/steward/ensure` taking `{host_id, model, permission_mode}` body; returns the steward's `agentOut`.
- Endpoint requires director auth (not steward). The general steward cannot call it directly — it raises an attention item instead (W4).
- Tests: idempotency, no-host failure, swap when previous steward is archived.

**W4. General-steward delegation attention item.**
- New MCP tool `attention.raise_project_steward_request` (or extend `attention.create` with a new kind) for the general steward to use when the user asks it to operate in a project that has no steward.
- Payload: `{project_id, suggested_host_id, reason}`.
- Mobile attention list surfaces this kind specially — tapping it opens the host-picker sheet (W7), prefilled with the suggestion.

**W5. MCP `agents.list?project_id=` filter.**
- Add `project_id` to the existing `agents.list` MCP tool schema + call.
- Update `docs/reference/hub-mcp.md` to drop the "planned" annotation once shipped.

**W6. Worker templates + workdir convention.**
- Add `driving_mode: M2` + `fallback_modes: [M4]` to all 6 worker templates: `briefing.v1.yaml`, `coder.v1.yaml`, `critic.v1.yaml`, `lit-reviewer.v1.yaml`, `ml-worker.v1.yaml`, `paper-writer.v1.yaml`.
- Drop per-template `default_workdir` overrides in 4 of them (coder, critic, lit-reviewer, paper-writer). `briefing` and `ml-worker` already use `~/hub-work`.
- `launch_m2.go`: when `spec.Backend.DefaultWorkdir` is empty AND the spawn carries `project_id`, compute `~/hub-work/<pid[:8]>/<handle>` and use it as the workdir. Create the directory tree if missing.
- Tests: spawn with project_id → expected workdir.

**W7. Mobile: project steward materialization + host-picker sheet.**
- New widget `SpawnStewardSheet` — bottom sheet with host/model/permission pickers. Used by both engagement and delegation paths.
- Project detail Agents tab: empty state when no steward exists → CTA opens the sheet → Spawn → calls `ensureProjectSteward` → tab populates.
- Steward overlay scoped to a project: same empty state when the project has no steward.
- Attention list: tapping the W4 attention item opens the same sheet prefilled.
- Defaults ladder per ADR-025 D4: general steward's host > project template `host_hint` > sibling project hosts > first online host.

**W8. Sessions screen: scope chip + worker rows.**
- Each session row gains a `Scope` chip: `Team` (general steward) or `Project: <name>` (domain steward, worker).
- Worker session rows appear ONLY on project detail Agents tab — filtered out of the global Sessions list to keep it readable.
- Tap on worker row → `agent_feed` scoped to that worker's session_id. Steward↔worker conversation observable.

### v1.0.563 — Enforcement + UI rerouting

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
