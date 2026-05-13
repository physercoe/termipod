# 025. Project steward accountability — workers, scope, lazy materialization, director consent

> **Type:** decision
> **Status:** Accepted (2026-05-13) — implementation lands across v1.0.563 (schema + lazy steward + worker session) and v1.0.564 (enforcement + UI rerouting). (v1.0.557–v1.0.562 were claimed by successive steward-overlay IME hotfixes.)
> **Audience:** contributors
> **Last verified vs code:** v1.0.556

**TL;DR.** Workers become first-class project-scoped agents with their own session. Every project that's *engaged with* has exactly one steward (lazy materialization); only that steward may spawn workers in the project. The general steward (ADR-017) routes and delegates but does not spawn workers itself. Every steward spawn — including the project-steward auto-create — requires explicit director consent via a host-picker sheet, regardless of host count. The director's role is principal (set intent, observe, configure, escape) rather than operator; mobile UIs that let the director operate directly should reroute through the steward by default. Refinement and extension of ADR-017; sister to ADR-016 (scope manifest).

The full exploration of alternatives is in [`discussions/project-steward-accountability.md`](../discussions/project-steward-accountability.md). This file is the decision record.

---

## Context

ADR-017 locked the two-tier steward model: a general steward at team scope and domain stewards at project scope. ADR-017 D6 says a project may have *at most one* domain steward, but does not say a project *must* have one, nor does it specify how worker agents relate to projects. ADR-016 introduced the role manifest (steward / worker), but treats worker scope as a generic "this agent has fewer privileges" attribute, not as a project-binding contract.

Three observations forced this refinement:

1. The `agents` table has no `project_id` column. Workers spawned for project Alpha and project Beta are indistinguishable in the audit log.
2. The mobile Project detail Agents tab already filters by `a['project_id']` (`project_detail_screen.dart:1098`), and the spawn sheet already seeds `project_id:` into the YAML (`spawn_agent_sheet.dart:55-60`). The frontend was prepared for project-scoped agents; the backend never finished the plumbing. **Result: every project's Agents tab is unconditionally empty for every team that's ever spawned a worker.**
3. The session backfill (`migrations/0026_sessions_create.up.sql:90`) only seeded sessions for `handle = 'steward'`. Workers are sessionless, so the mobile sessions screen never surfaces them and the director cannot debug or audit worker behavior — even though `agent_events` captures all A2A traffic and driver output.

Without these gaps closed, "workers are project-scoped" is wishful thinking. With them closed, the next architectural question becomes load-bearing: **who can spawn a worker into a project?** If both the general steward and a project steward can, accountability forks at every spawn. The team-level general steward cannot be on the project's accountability chain without also collapsing the layered-stewards work from ADR-017.

This ADR settles all of it.

---

## Decision

**D1. Workers are project-scoped first-class agents.**

`agents` gains a nullable `project_id TEXT REFERENCES projects(id) ON DELETE SET NULL`. The spawn flow parses `project_id` from `spawn_spec_yaml`, persists it on the row, and returns it in `agentOut`. Workers without `project_id` are valid (legacy rows from before this ADR; one-off team-level utility agents) but are excluded from the project Agents tab.

The migration is purely additive — no `NOT NULL` constraint, no backfill of old rows. Pre-ADR agents stay `NULL` and are visible only on the team-level Archived agents screen.

**D2. Every project that's engaged with has exactly one steward.**

A project row can exist without a `steward_agent_id` (we do not enforce `NOT NULL`). But the first time anything tries to operate within the project, the hub materializes the project's steward inline — *with director consent* (D4). "Engagement" means:

- Director opens the project's steward overlay (`mobile.navigate` to the project's steward session, or the panel's project-scoped chip).
- Director sends a message into the project from the general steward overlay ("spawn a worker for Alpha").
- A scheduled action or A2A delegation needs to operate on the project.

The lazy-materialization endpoint is `POST /v1/teams/{team}/projects/{project}/steward/ensure`. Idempotent: returns the existing steward's `agent_id` when one is running; raises an attention item with the host-picker payload when one isn't.

**D3. Only the project steward may spawn workers in its project.**

`agents.spawn` is the hub gate. When the spawn carries `project_id`:

- If `parent_agent_id` matches the project's `steward_agent_id` AND the parent's `kind` is steward-family — allow.
- If the caller is the team's general steward (`kind = 'steward.general.v1'`) — **reject with 403**: "the general steward routes intent but does not spawn workers; delegate to the project steward."
- If `parent_agent_id` is unset or unrelated — reject with 403.

Spawns without `project_id` are allowed for team-scope utility agents (rare; reserved for advanced flows). The general steward still cannot spawn them — `kind = 'steward.general.v1'` blocks `agents.spawn` outright. The director's mobile UI bypass is the only path that admits team-scope spawns, and it's flagged "advanced" (D6).

**D4. Director consent on every steward spawn.**

Project-steward creation requires explicit director approval via a host-picker sheet. Sheet content:

- **Host picker.** Defaults via this ladder:
  1. The host where the general steward currently runs (continuity bias).
  2. Project template's `host_hint` field if set (ML templates → GPU host).
  3. Sibling project stewards' hosts (cluster bias when projects share a parent).
  4. First online host.
- **Model picker.** Defaults to the user's preferred model in settings.
- **Permission mode picker.** Defaults to `skip` for the demo arc; `prompt` available for principals who want every tool call gated.
- Even with **one host**, the sheet appears with the host preselected and no dropdown. Same mental model for 1 host and N hosts.
- Failure mode: zero hosts → the sheet shows an empty host list and an inline `[Install host-runner →]` CTA deep-linking to the how-to doc.

Two trigger paths use the same sheet:

| Trigger | Initial state |
|---|---|
| Director taps a project's steward-empty surface (overlay or Agents tab CTA) | Sheet opens directly; defaults computed locally. |
| General steward needs a project steward to honor a delegation (e.g. principal asked it to "spawn a worker for project Alpha" but Alpha has no steward) | General raises an `attention_item` ("Project Alpha needs a steward to honor your spawn-worker request. Approve?"). Approve → opens the same sheet, prefilled with the general steward's recommendation. |

The attention-item path is **not** a one-tap rubber-stamp — approval opens the picker. Hosting and permission decisions are too consequential to embed silently in an approve button. Same picker, same consent moment.

**D5. Workers get sessions at spawn time.**

`agents.spawn` for a project-scoped worker atomically creates a `sessions` row alongside the agent row:

```sql
INSERT INTO sessions (id, team_id, title, scope_kind, scope_id,
                     current_agent_id, status, opened_at,
                     last_active_at, worktree_path, spawn_spec_yaml)
VALUES (?, ?, <handle>, 'project', <project_id>, <agent_id>,
        'open', NOW(), NOW(), <worktree>, <spec>);
```

- `title` is the worker's `handle` (e.g. `ml-worker.v1`). The principal can rename if needed (existing session-rename flow).
- `scope_kind = 'project'` makes the session reachable via project-scope queries; mobile's Sessions screen surfaces it with a `Project: <name>` chip.
- All `agent_events` for the worker get `session_id` stamped via the existing event-insert path (`handlers_sessions.go:602` already does the `current_agent_id → session_id` lookup). A2A inbound + driver outbound both flow into the same session, so the steward↔worker conversation is fully observable in the worker's session viewer.

Pre-ADR worker agents (rows with no session) get nothing retroactively. The schema-level upgrade is forward-only.

**D6. Director scope guardrails.**

The director is a *principal*, not an operator. Their canonical operations:

| Class | Operations |
|---|---|
| **Direct (no steward involved)** | Configure hub, install host-runner, manage SSH keys/connections, edit roles.yaml / templates / policy, create projects (intent declaration), read everything, reset/replace/fork stewards, approve attention items. |
| **Through the steward (preferred)** | Spawn workers, edit live agent config (mode/model/permissions), terminate agents, run schedules, create plans/tasks/docs, open A2A conversations. |
| **Escape valves (technically possible, flagged in UI)** | Direct-spawn workers bypassing steward, direct PATCH on live agent config, raw tmux/SSH input. Each remains available but routed through an "advanced" affordance with a friction hint. |

Mobile implications (delivered in v1.0.564):
- The project Agents tab `[+ Spawn Agent]` FAB routes intent through the project steward by default (intent → `a2a.invoke` → steward acts on `agents.spawn`). The advanced-bypass path moves behind a long-press or settings flag.
- The agent_config_sheet stays read-only by default; an "Ask steward to reconfigure" CTA replaces direct PATCH.
- The escape-valve flows are kept (they're load-bearing when stewards are unresponsive) but explicitly named so director and audit log both see the bypass.

**D7. General steward retains emergency-stop authority.**

The general steward CAN call `agents.terminate` on any team agent, including workers belonging to projects it didn't spawn into. This is the safety valve for unresponsive project stewards. Documented in the general steward's prompt as "use only when the project steward cannot."

`agents.spawn` is steward-role-callable in the role manifest but project-binding-gated by D3. `agents.terminate` is steward-role-callable with no further gate. The asymmetry matches their cost profile: terminate is reversible-via-respawn; spawn commits resources.

**D8. Default project-steward kind.**

The project steward auto-spawned via D4 uses `steward.general.v1` as its template by default. Project templates may override this with a `default_steward_kind` field; the director's host-picker sheet exposes the kind as an editable field (same shape as the existing "Switch engine" flow).

This is intentional minimalism. The five domain steward kinds (research, infra, codex, gemini, v1) are user-opted specializations, not defaults. Walking through 5 templates per project creation is friction; "general by default + opt into specialization later" preserves choice without imposing it.

---

## Consequences

**Becomes possible:**

- Project Agents tab actually shows the project's agents. `agents.list?project_id=...` becomes a meaningful query for the first time.
- Workers are debuggable: their session viewer shows the steward↔worker conversation in full, A2A messages on both sides, driver output, tool calls. The "what did the steward tell the worker?" question gains a UI answer.
- Audit on Activity is project-resolvable. `audit_events` joined to `agents.project_id` becomes a per-project audit timeline.
- Workers can be terminated by their project steward (default flow) or by the general steward (emergency). Either way the operation is bounded by scope.

**Becomes harder:**

- Spawning a worker now takes one extra hop in the worst case (general → project steward → worker). Justified by accountability; this is the same shape as `kubectl apply` in a delegated namespace, or a PR going through a code owner.
- Project creation gains a follow-on tap (the host-picker sheet) when the director engages with the new project. Mitigated by the empty-state CTA being unambiguous and the picker remembering defaults.
- The general steward's prompt needs to learn the routing pattern: "user asked me to do X in project Alpha → I should delegate to Alpha's steward." Currently the general steward may try to do project work itself; the prompt and the role gate together enforce delegation.

**Becomes forbidden:**

- General steward calling `agents.spawn` with `project_id` set → 403 (D3).
- Two project stewards in the same project simultaneously → blocked by the existing unique-handle constraint (ADR-017 D6 already prevented this; we keep it).
- Workers without `parent_agent_id` set to their project's steward, when spawned for a project → 403 (D3).
- Silent auto-spawn of a project steward without director consent → no API surface for it (D4).

**Schema invariants:**

- `agents.project_id` is nullable. NULL means "team-scope or legacy."
- `projects.steward_agent_id` is nullable. NULL means "no engagement yet."
- For every NON-NULL `agents.project_id`, the row must have `parent_agent_id` pointing at the project's steward at spawn time. (Parent can later be NULL'd if the steward is archived; the binding is at spawn, not eternal.)
- For every project-scoped worker, a `sessions` row with `scope_kind='project'`, `scope_id=<pid>`, `current_agent_id=<worker>` exists. Sessions outlive worker termination (closed status); the same shape stewards already use.

---

## Migration

Three pieces, landed across two releases:

**v1.0.563 — schema + lazy steward + worker session + visibility:**

1. **Migration 0042** — `ALTER TABLE agents ADD COLUMN project_id TEXT REFERENCES projects(id) ON DELETE SET NULL; CREATE INDEX idx_agents_project ON agents(project_id) WHERE project_id IS NOT NULL;`. Forward-only; no backfill.
2. **`spawn_mode.go`** — extend `spawnModeYAML` to parse `project_id`; thread through `resolveSpawnMode` so it lands on the agent row.
3. **`handlers_agents.go`** — `spawnIn.ProjectID` field; persist on insert; return in `agentOut`. Worker spawn (any spawn with `project_id` set) atomically creates a `sessions` row per D5.
4. **`ensure_project_steward`** — new helper + endpoint `POST /v1/teams/{team}/projects/{project}/steward/ensure`. Idempotent. Returns existing steward when running; otherwise returns the attention-item payload the mobile uses to render the host-picker sheet.
5. **Mobile** — empty-state on project Agents tab + steward overlay surfaces. Bottom sheet with host/model/permission picker. Sessions screen gains a scope chip on each row. Worker row tap → agent_feed scoped to its session.
6. **Worker templates** — add `driving_mode: M2` + `fallback_modes: [M4]` to all 6 worker templates (briefing, coder, critic, lit-reviewer, ml-worker, paper-writer). Drop per-template `default_workdir` overrides in favor of a derived `~/hub-work/<project-id-prefix>/<handle>` convention computed at spawn time.

**v1.0.564 — enforcement + UI rerouting:**

7. **Role gate** — `agents.spawn` with `project_id` requires `parent_agent_id` to be the project's current steward (D3). General steward (`kind = 'steward.general.v1'`) cannot call `agents.spawn`; falls through to delegation.
8. **Mobile** — `[+ Spawn Agent]` FAB on project Agents tab routes through the project steward by default. Direct-bypass moved behind an advanced toggle (D6).
9. **agent_config_sheet** — read-only by default; "Ask steward to reconfigure" CTA replaces direct PATCH (D6).
10. **General steward prompt** — adds the delegation routing pattern.

**Pre-ADR data:**

- Existing `agents` rows: `project_id = NULL`. Visible only on team-level Archived agents and on team-wide queries. Not surfaced on any project page.
- Existing `projects` rows: `steward_agent_id = NULL` unless one was already set. First engagement triggers the consent flow.
- Existing worker agents (pre-ADR): sessionless. Stay sessionless; we do not retroactively create sessions because we cannot know which project they "belonged" to. New worker spawns get sessions; old worker spawns are observable only via the agent-detail sheet (limited transcript reachable through agent_events queries by `agent_id`).

---

## Relation to existing ADRs

- **ADR-004 (single-steward MVP)** — already superseded by ADR-017. No further change.
- **ADR-016 (subagent scope manifest)** — the role gate that enforces D3 is implemented as an extension of the existing `mcp_authority` middleware. The `worker` role is unchanged; the project-binding check is a separate gate that fires before the role check on `agents.spawn`.
- **ADR-017 (layered stewards)** — D1's domain-steward tier becomes the canonical project-steward role. D6's "at most one domain steward per project" becomes "exactly one steward per engaged project" — a tighter invariant that subsumes the original. ADR-017 D-amend-1 (peer stewards) is unaffected: peers are team-scope by design and never own a `project_id`.
- **ADR-020 (director-action surface)** — unaffected. The seven director-on-doc moves are about deliberation primitives, orthogonal to spawn accountability.
- **ADR-023 (agent-driven mobile UI)** — D6's "director scope guardrails" formalizes the principal/operator boundary that ADR-023 has been operating under since v1.0.464. Mobile reroute (v1.0.564) is the wedge that finishes the work ADR-023 started.

---

## References

- [`discussions/project-steward-accountability.md`](../discussions/project-steward-accountability.md) — the exploration this ADR resolves.
- [ADR-016](016-subagent-scope-manifest.md), [ADR-017](017-layered-stewards.md), [ADR-023](023-agent-driven-mobile-ui.md) — adjacent decisions.
- [`spine/agent-lifecycle.md`](../spine/agent-lifecycle.md) — updated in this batch to reflect project-scoped workers + lazy stewards.
- [`reference/hub-mcp.md`](../reference/hub-mcp.md) — updated to document `project_id` on `agents.spawn` + the project-steward ensure endpoint.
- Code (post-implementation): `hub/internal/server/handlers_agents.go` (D1 + D3 + D5), `hub/internal/server/handlers_projects.go` (D2 ensure), `hub/internal/server/mcp_authority_roles.go` (D3 gate), `lib/screens/projects/project_detail_screen.dart` (D5 + D6 surfaces), `lib/widgets/spawn_steward_sheet.dart` (D4 picker).
