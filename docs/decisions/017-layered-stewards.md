# 017. Layered stewards: general (frozen, persistent) + domain (overlay, project-scoped)

> **Type:** decision
> **Status:** Accepted (2026-04-30) — Amendment 1 proposed (2026-05-06): peer steward tier
> **Audience:** contributors
> **Last verified vs code:** v1.0.370-alpha

**TL;DR.** Termipod runs **two tiers of steward** instead of one. The **general steward** (`steward.general.v1`, handle `@steward`) is **frozen** in the hub binary, **persistent** (one per team, archived only by manual director action), and serves as the director's concierge — bootstraps projects, authors templates, manages schedules, debugs, free-discusses. **Domain stewards** (`steward.research.v1`, `steward.infra.v1`, …) are **overlay-authored** by the general steward, **project-scoped**, archived at project completion, and orchestrate one project's lifecycle. The general steward never does IC work (manager/IC invariant). The contract surfaces as a singleton `POST /v1/teams/{team}/steward.general/ensure` endpoint and a `@steward` handle convention distinct from the legacy plain `steward` and the domain `*-steward` suffix. This ADR consolidates the rationale that ADR-001 D-amend-2 introduced and gives it a load-bearing home.

> **Amendment 1 (proposed, 2026-05-06)** introduces a **third tier**: **peer stewards** — overlay-authored, persistent, team-scoped (not project-scoped), multi-instance (one per kind per team). Closes the "vice-steward / board member" gap between the singleton general concierge and the per-project domain orchestrator. See [§ Amendment 1](#amendment-1--peer-steward-tier-2026-05-06) at the bottom of this ADR. Implementation tracker: [`plans/team-peer-stewards.md`](../plans/team-peer-stewards.md).

---

## Context

[ADR-004](004-single-steward-mvp.md) locked "one steward per team" for MVP — the simplest model that ships. Through 2026-04 two pressures forced a refinement:

1. **Bootstrap window vs. ongoing use.** The single-agent-bootstrap-window framing (`spine/agent-lifecycle.md` §6.2) names a phase where one agent does both manager and IC work briefly, then retreats once a domain specialist takes over. In practice the bootstrapping agent — the general-purpose concierge that authors templates and the plan — kept being useful past bootstrap: cross-project debugging, schedule edits, free chat. ADR-004's "one steward per team" assumed the bootstrap agent and the project orchestrator were the same row. Director feedback on 2026-04-30: "the general steward should keep on active for helping director handle/manage other things/debug/discuss/create/edit files in the system. only director manually close its session."
2. **Lifecycle demo.** The 5-phase research lifecycle (ADR-001 D-amend-1) needs a project orchestrator that owns the plan and spawns workers across phases. That orchestrator wants to be domain-aware (research vs. infra vs. briefing), template-author-controlled (the director can edit it), and project-bound (archive at project close). A single team-scoped steward can't be all of those at once without conflating bootstrapping and orchestration.

The fix is two stewards with different lifetimes, not one steward with split mode-state.

---

## Decision

**D1. Two tiers, two lifetimes.**

| Tier | Kind | Authoring | Lifetime | Role |
|---|---|---|---|---|
| **General steward** | `steward.general.v1` | **Frozen** — bundled in hub binary via `embed.FS`; never copied to overlay; not editable by director or any agent. | **Persistent.** One per team, always-on. Archived only by manual director action. | Team-level concierge. Bootstraps new projects. Authors domain-steward + worker templates + plan. Debugs across projects. Manages schedules. Free-converses with director. |
| **Domain steward** | `steward.research.v1`, `steward.infra.v1`, `steward.briefing.v1`, … | **Overlay** at `<DataRoot>/teams/<team>/templates/agents/<name>.yaml`. Written by the general steward on first project create; editable by the director via the mobile template editor. | **Project-scoped.** Spawned at project create; archived at project close. | Project orchestrator. Owns the plan. Spawns workers. Routes per-phase results. Drives `human_gated` step approvals upward. |

Frozen vs. overlay is a structural safety boundary, not a convenience. The general steward cannot rewrite itself (D7 of ADR-016), so a misbehaving director's prompt nudge cannot cause it to silently grant itself wider scope. Domain stewards live in overlay because they are domain-specific and the director must be able to tune them per project without a hub release.

**D2. Handle convention.**

| Handle pattern | What it identifies | Examples |
|---|---|---|
| `@steward` | The team's general steward (singleton). The `@` prefix keeps it lexically distinct from project-scoped agents in lists, badges, and audit lines. | `@steward` |
| `*-steward` | Domain stewards. Bare prefix (research, infra-east, briefing) names the domain; `-steward` suffix is appended internally so a single `isStewardHandle()` predicate catches all variants. | `research-steward`, `infra-east-steward` |
| `steward` | Legacy plain steward — single-steward installs from the ADR-004 era. Still works (backwards-compatible) but new spawns use the suffixed form. | `steward` |

Predicates in `lib/services/steward_handle.dart` enforce the split: `isStewardHandle()` matches the legacy + domain shapes; `isGeneralStewardHandle()` matches only `@steward`. Code that wants "any steward" calls both; code that means "the team concierge specifically" calls only the latter.

**D3. Ensure-spawn endpoint (singleton, idempotent, race-coalescing).**

```
POST /v1/teams/{team}/steward.general/ensure
```

Contract:

- **Read-fast path.** If a `steward.general.v1` agent with status `pending` or `running` exists for the team, return its `agent_id` with `already_running: true`. No write, no spawn.
- **Spawn path.** Otherwise, spawn one on the team's first known host. The unique-handle constraint (`agents.handle` unique within team) coalesces concurrent first-callers onto a single agent row; losers see the winner's `agent_id` on retry.
- **Respawn after archive.** If the previous instance was archived, the next call spawns a fresh agent. There is no "resurrect the old agent" path — frozen template means a fresh agent gets identical behaviour.

The endpoint exists because the home-tab card (mobile) calls it on tab open. Without idempotency, opening the Me tab twice would spawn two stewards; without race coalescing, two devices opening it simultaneously would too. Implementation in `hub/internal/server/handlers_general_steward.go`.

**D4. Manager/IC invariant for the general steward.**

The general steward is a **manager**, not an IC. It does not write code, run experiments, or produce non-template artifacts. Soft-enforced by the bundled prompt (`hub/templates/prompts/steward.general.v1.md`) and structurally enforced by:

- ADR-016 D6 role middleware — general steward gets the `steward` role; the role's allowed `hub://*` tools cover authoring (`templates.*`, `schedules.*`, `projects.update`) but the IC tools (`runs.*`, `documents.*` writeside) are scoped to project context which the general steward typically doesn't have.
- ADR-016 D7 self-modification guard — the general steward cannot edit `steward.general.v1` because that's its own kind.

When the director asks the general steward to "fix this bug," the right behavior is for the steward to author a plan (or domain steward) that delegates to a worker. Doing the work itself violates the invariant.

**D5. Concierge scope (what the general steward will and won't do).**

| Will | Won't |
|---|---|
| Author/edit overlay templates and schedules (subject to D7 self-mod guard) | Edit `steward.general.v1` (its own kind) |
| Read project state across projects (status, artifacts, attention) | Modify project artifacts (`documents.*`, `runs.*` writeside) outside an authoring flow |
| Spawn domain stewards / workers via plans | Spawn arbitrary worker agents directly into a running project |
| Free-discuss with the director, summarize, advise | Run experiments, write production code, do IC research |
| Triage failures across projects ("why is project X stuck?") | Bypass the plan→agent indirection (see ADR forbidden #11 — schedules instantiate plans, not agents) |

This is the prose contract; the structural guarantees come from ADR-016 + the frozen template. If a future change relaxes the "concierge only" framing, the guarantees in ADR-016 D6/D7 must be re-evaluated first.

**D6. Project-internal singleton check.**

A project may have at most one *domain* steward at a time. The director can replace it (archive the current one, spawn a new variant), but two live domain stewards in the same project would race for plan ownership. Enforced at spawn time via the same unique-handle constraint scoped by project + a check in the spawn handler.

The team-scoped general steward and the project-scoped domain steward coexist by design — they are not duplicates, they have different scopes.

**D7. Frozen-template invariant (cross-link to ADR-016 D7).**

The bundled `steward.general.v1` template (`hub/templates/agents/steward.general.v1.yaml` + matching prompt) is **read-only at runtime**. Hub never writes to it; the director cannot edit it via the mobile editor; the general steward itself cannot edit it (ADR-016 D7). The only way to change it is a hub release. This is intentional: the general steward is the system's lowest-trust-required surface (it has the broadest scope of any agent), and the director's only assurance about its behaviour is the bundled prompt + structural gating. Allowing edits would erode that assurance to "trust whatever the overlay currently says."

---

## Consequences

**Becomes possible:**
- The director has an always-available concierge without dedicating a project to it. "Just ask `@steward`" is a coherent UX pattern for cross-project work.
- New projects bootstrap without the director needing to author templates from scratch — the general steward proposes them, the director approves the proposal.
- Domain stewards can be aggressively scoped (one project, one orchestration concern) because the always-on team work lives elsewhere.
- A future "auditor" or "reviewer" tier (post-MVP) drops in as a third row in this table without reshaping the existing two.

**Becomes harder:**
- ADR-004's "one steward per team for MVP" is no longer literal. Status of ADR-004 updated to point here.
- Code that tests "is this agent the steward?" needs to disambiguate which tier — `isStewardHandle()` vs. `isGeneralStewardHandle()` — and may now match multiple stewards per team.
- The general steward's prompt is the first prompt that can't be edited without a release. Writing it deserves more care than overlay templates do.

**Becomes forbidden:**
- Spawning more than one general steward per team (D3 idempotency + unique-handle).
- Editing `steward.general.v1` template at runtime — overlay edits are silently ignored; the bundled file wins (D7).
- The general steward doing IC work — soft-enforced by prompt, structurally enforced by ADR-016 role/tool gating (D4).
- Cross-team `@steward` references — the handle is team-scoped; logs and audit trails should always pair `@steward` with a `team_id`.

---

## Migration

This ADR consolidates a design that already shipped (W1/W2/W4/W5/W6 of `plans/research-demo-lifecycle-wedges.md`, commits `8475723`, `1d1f92f`, `eebb119`, `e687b0a`, `dd45aaf`, `f1b8340`, plus `8caff8a` for the W3 home-tab card). For new contributors:

1. **Authoring a domain steward** — write the YAML at `hub/templates/agents/steward.<domain>.v1.yaml`; the general steward's first project bootstrap copies seeds to overlay and the director can edit from there.
2. **Adding a tier** (post-MVP, e.g. "auditor") — extend `roles.yaml` (ADR-016 D6), pick a handle convention that doesn't collide (`@auditor`?), update the predicates in `steward_handle.dart`, and update this ADR's D1 table.
3. **Status of ADR-004.** Updated in this batch — header now reads "Superseded by ADR-017."

---

## References

- [ADR-001 D-amend-2](001-locked-candidate-a.md) — origin of the layered-steward design (this ADR consolidates it).
- [ADR-004](004-single-steward-mvp.md) — single-steward MVP (Superseded by this ADR).
- [ADR-016](016-subagent-scope-manifest.md) — D6 role middleware, D7 self-modification guard, D5 engine-internal subagent exemption.
- [Reference: steward templates](../reference/steward-templates.md) — template authoring contract, what overlay can/can't shadow, engine selection.
- [Discussion: research-demo-lifecycle](../discussions/research-demo-lifecycle.md) — design rationale, manager/IC invariant.
- [Plan: research-demo-lifecycle-wedges](../plans/research-demo-lifecycle-wedges.md) — W1–W6 implementation tracker.
- Code: `hub/internal/server/handlers_general_steward.go` (D3 endpoint), `lib/services/steward_handle.dart` (D2 predicates), `lib/widgets/home/persistent_steward_card.dart` (W3 home-tab entry), `hub/templates/agents/steward.general.v1.yaml` (D7 frozen template).
- Memory: `project_layered_stewards.md` (shipping summary).

---

## Amendment 1 — Peer steward tier (2026-05-06)

**Status:** Proposed. Tracker: [`plans/team-peer-stewards.md`](../plans/team-peer-stewards.md).

### Why

The two-tier model (general singleton + project-domain) leaves a **team-scoped specialist** unrepresented. Three pieces of director feedback converged:

1. The general steward is the *concierge*. As a frozen template it is intentionally generalist; it can't be the "code-review specialist" or the "ops/infra specialist" for the team without overloading its prompt and eroding the manager/IC invariant (D4).
2. Project-scoped domain stewards are the right shape for *one* project's lifecycle. They're the wrong shape for *cross-project* concerns ("how is the codebase doing across all five research projects?" or "what's our hosts' status?") because their scope is bounded to a single project.
3. Director's chairman/board metaphor: a real organisation has a chairman (general) plus VPs/department heads (peers) plus per-team-or-project leads (domain). The current ladder collapses chairman + VPs into one role.

### Decision

**D-amend-1.1. Add a third tier: peer steward.**

| Tier | Kind | Authoring | Lifetime | Multiplicity | Role |
|---|---|---|---|---|---|
| **General** | `steward.general.v1` | Frozen (bundled in hub) | Persistent — one per team | 1 per team | Team concierge (unchanged) |
| **Peer (new)** | `steward.peer.<domain>.v1`, e.g. `steward.peer.code.v1`, `steward.peer.ops.v1` | Overlay (`<DataRoot>/teams/<team>/templates/agents/`), seeded from bundled defaults; editable by director and by general steward (subject to ADR-016 D7 self-mod guard) | Persistent — archived only by manual director action | **One per (team, kind)**; many distinct peers per team | Team-level specialist. Cross-project advisory, code review, ops triage, hiring/family management, etc. |
| **Domain** | `steward.research.v1`, `steward.infra.v1`, … | Overlay (unchanged) | Project-scoped (unchanged) | 1 per project (D6, unchanged) | Project orchestrator (unchanged) |

The peer tier sits at *team scope* like the general, but is *specialised* like a domain steward. It is **not** a project-scoped agent and never owns a `project_id`.

**D-amend-1.2. Handle convention extension.**

| Handle pattern | Tier | Examples |
|---|---|---|
| `@steward` | General (singleton) | `@steward` |
| `@steward.<domain>` | **Peer (new)** | `@steward.code`, `@steward.ops`, `@steward.security` |
| `*-steward` | Domain (project) | `research-steward`, `infra-east-steward` |
| `steward` | Legacy plain | `steward` |

The `@` prefix stays the lexical mark of "team-level" — peers share it with the general so a single `isTeamLevelStewardHandle()` predicate covers both. The trailing `.<domain>` distinguishes a peer from the singleton general. Predicates in `lib/services/steward_handle.dart`:

```dart
bool isGeneralStewardHandle(String h)    => h == '@steward';
bool isPeerStewardHandle(String h)       => h.startsWith('@steward.');
bool isTeamLevelStewardHandle(String h)  =>
    isGeneralStewardHandle(h) || isPeerStewardHandle(h);
bool isStewardHandle(String h)           => /* unchanged: matches all 4 */;
```

**D-amend-1.3. Ensure-spawn endpoint, parameterised by kind.**

```
POST /v1/teams/{team}/steward.peer/ensure
Body: { "kind": "steward.peer.code.v1" }
```

Same idempotency contract as the general (D3): read-fast for an existing running instance keyed by `(team_id, kind)`; spawn-and-coalesce on race; respawn after archive. Distinct from `steward.general/ensure` because the kind is parameterised — one endpoint, many kinds. Implementation reuses the `findRunningGeneralSteward` query shape with the kind filter generalised.

The bundled hub binary ships a small set of seed peer templates (`steward.peer.code.v1.yaml`, `steward.peer.ops.v1.yaml`); the director can spawn additional ones by authoring overlay YAML directly or asking the general steward to author one.

**D-amend-1.4. Routing precedence (informational; UX details in plan).**

When a request can in principle be served by more than one tier, prefer the most-specific scope:

```
project context  → project domain steward    (most specific)
team context     → matching peer steward     (specialist)
no specialist    → general steward           (concierge fallback)
```

Concretely: a director asking from inside a Project page → domain steward; from the Me / home tab with a `@steward.code` mention → peer; from the Me tab with bare `@steward` → general. The amendment locks the precedence ordering; the picker UX (3-way picker, fallback choice, etc.) is in the wedge plan.

**D-amend-1.5. Frozen invariant scope.**

D7's frozen-template invariant applies **only** to `steward.general.v1`. Peer stewards are overlay-authored — the director and the general steward may edit them. ADR-016 D7's "no agent edits its own kind" rule still applies to peers: a peer steward cannot edit its own template (e.g. `steward.peer.code.v1` cannot edit `steward.peer.code.v1.yaml`), but it can edit a *different* peer's template if its scope grants it (subject to roles.yaml). General can edit any peer template.

**D-amend-1.6. Role manifest (cross-link to ADR-016).**

`roles.yaml` gains a `peer-steward` role bucket with permissions between `steward` (general's, broadest) and the project domain stewards (narrowest). The exact tool list is decided in the wedge plan; the principle is: peers can read across all projects (`projects.list`, `runs.list`, `documents.list`) but only write within their declared specialty (e.g. `steward.peer.code.v1` can call `templates.edit` but not `runs.start`).

### What stays the same

- D1, D2, D3, D4, D5, D6, D7 of the original ADR.
- Project-scoped domain stewards: still 1 per project, still overlay, still archived at project close.
- General steward: still 1 per team, still frozen, still archived only by manual director action.
- Spawn-time uniqueness on `(team_id, handle)` is still the structural guarantee that no two stewards share a handle.

### What becomes forbidden

- Peer stewards within a project. Peers are team-scoped; a project-bound steward must use the domain tier (D6's project-internal singleton applies).
- A peer steward editing its own kind (mirrors ADR-016 D7).
- More than one running peer steward of the same kind per team — the per-kind singleton (D-amend-1.3) is enforced via the same unique-handle constraint.

### Migration

Strictly additive — no existing data or endpoints change. New code paths:

1. New endpoint `POST /v1/teams/{team}/steward.peer/ensure?kind=...`.
2. New seed templates in `hub/templates/agents/`: `steward.peer.code.v1.yaml`, `steward.peer.ops.v1.yaml` (and any other initial kinds).
3. New role bucket in `hub/internal/server/roles.yaml`.
4. New mobile predicates + Stewards-overview section grouping.

Existing single-team-with-only-general installs see no behaviour change until the director explicitly spawns a peer.

### Open questions (answered in the wedge plan, not here)

- Default seed peer kinds shipped with the hub: just code + ops, or a wider set?
- Mobile UX for the 3-way picker when both peer and project-domain are reachable.
- A2A: can a project domain steward delegate up to a peer? (Today A2A allows worker→non-parent only via blueprint forbidden-pattern audit; peer delegation needs an explicit allowance.)
