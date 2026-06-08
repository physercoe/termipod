# Projects from an inline spec — rollout

> **Type:** plan
> **Status:** In flight (2026-06-08) — WS0 (ADRs) shipped in
> [#44](https://github.com/physercoe/termipod/pull/44); WS1–WS5 to come. The
> phased implementation of
> [ADR-046](../decisions/046-projects-from-inline-spec.md) (a project's spec is
> its `config_yaml`; create is governed) and the
> [ADR-044 early-bind amendment](../decisions/044-adaptive-project-lifecycle.md#amendment-2026-06-08--early-bind--completion-gating).
> The ADRs hold the *what/why*; this plan holds the *how* and the status.
> **Audience:** contributors
> **Last verified vs code:** v1.0.808

**TL;DR.** Collapse the "project template" and "project" concepts: a project
carries its full spec inline in its own `config_yaml` (phases ≥1, per-phase
deliverables/criteria/**tasks**/**plan**, typed parameters, bound domain
steward). A steward creates a project through one governed action —
`propose(kind="project.create", {name, config_yaml, parameters_json})` — whose
approval **materializes** it (the approval *is* the install; no
`template.install`). All phases **early-bind** at create and stay editable;
**completion is phase-gated** (ratify/mark-met only in the active phase). Presets
(`research`, `code-migration`) are reference examples, not a library. The bound
steward is spawned on an explicit **Start**. Shipped as ~6 PRs, hub-first and
Go-testable; mobile follows the device-test model.

## Context

Triage of the tester's 19 project-lifecycle issues
([#21](https://github.com/physercoe/termipod/issues/21)–[#41](https://github.com/physercoe/termipod/issues/41))
left a set of decided design/content/governance work. Four root-cause fixes
(#38/#41/#21/#27/#29) shipped first in
[#43](https://github.com/physercoe/termipod/pull/43). Through discussion the
director simplified the rest into one unified model:

- **A project's spec _is_ the project** — it lives in the project's own
  `config_yaml`; materialization reads that, not a named template file.
- **Create is a single governed action** — `propose(project.create, …)`;
  approval materializes the project. No separate `template.install`.
- **Presets are reference examples**, not an installed library; a steward reads
  one to learn the schema, then composes a spec for the need. A template is
  **not** a recurrence marker — a one-off is just a 1-phase project.
- **Early-bind + completion-gated** — all phases materialize at create and stay
  editable (the plan adapts); you may not **ratify** a deliverable or **mark an
  AC met** for a phase you are not currently in.
- **Start** — the bound steward is not spawned at create; the principal
  reviews/edits the materialized project, then an explicit Start spawns it.

> The merged #38/#41 fixes (tolerant phases parse, ULID resolution) stay valid —
> preset specs remain loadable as references via `loadProjectTemplates` /
> `readProjectTemplateYAML`.

## Sequence

WS0 → WS2 → WS1 → WS3 → WS4 → WS5. Each is its own CI-gated PR, branched off
`main`. (WS2 defines the spec shape WS1 materializes; otherwise as listed.)

## WS0 — ADRs ✓ (shipped, [#44](https://github.com/physercoe/termipod/pull/44))

- [ADR-044 amendment (2026-06-08)](../decisions/044-adaptive-project-lifecycle.md#amendment-2026-06-08--early-bind--completion-gating):
  early-bind + completion-gating.
- New [ADR-046](../decisions/046-projects-from-inline-spec.md): a project's spec
  is its `config_yaml`; create is a governed `project.create` whose approval
  materializes it; presets are reference examples; steward bound, spawned on
  Start.
- Index + roadmap updated. Glossary term updates deferred to the WS that ships
  each behaviour (the glossary tracks shipped code).

## WS2 — Project-spec schema + validation (Go)

- **Typed parameters (#32).** New `validate_project_params.go`:
  `paramSpec{Type,Required,Default,Description,Min,Max,Enum}`; parse the spec's
  `parameters:` block (extend the `config_yaml` parse); validate
  `parameters_json` on create/update (`handlers_projects.go`); expose the schema
  on project read for mobile form rendering. Back-compat: a bare `key: value`
  block ⇒ untyped default.
- **Per-phase `tasks:` + `plan:` in `phase_specs`** (`template_hydration.go:31`):
  `tasks: [{title, ord, …}]` (materialized by WS1) and `plan:` (ordered steps →
  seed a draft `plans` + `plan_steps` row at create; tables exist,
  `0009_plans`). Reuse the `phaseDeliverableSpec` pattern.
- **Bound steward field** in the spec (`on_create_template_id` already stores
  it, `init.go:119`) — WS4 Start spawns it.
- **`validateProjectConfigYAML`** extended to validate the richer spec when
  present (phases + phase_specs shape + params), still lenient on extras.
- Tests: param validation (types/required/min-max/enum + untyped back-compat);
  tasks/plan parse + materialize; plan seeded once at create.

## WS1 — Materialize from config_yaml: early-bind + completion gating (Go)

- **Read the spec from the project's own `config_yaml`.** Refactor the hydrators
  (`template_hydration.go`) to parse `phase_specs` from the project's
  `config_yaml` rather than resolving a template file. `readProjectTemplateYAML`
  stays for preset-reference reads.
- **Materialize all phases at create.** Project create
  (`handlers_projects.go:~300`) loops every phase → `hydratePhase`. Hydrators are
  idempotent; the #21 gate-ref rewrite resolves per (phase, kind) — all
  deliverables exist at create, so every phase's gates resolve.
- **Tasks gain a phase + materialize.** Migration: `tasks.phase` (#22). New
  `hydratePhaseTasks` mirroring `hydratePhaseDeliverables`; call from
  `hydratePhase` (`:309`).
- **Completion gating** (#23): in `apply_deliverable_set_state.go` reject
  `→ratified` when `deliverable.phase != current_phase`, route `ratified→draft`
  via `/unratify` (`server.go:599`) + re-pend the gate it fired; in
  `handleMarkCriterion` (`handlers_criteria.go`) reject mark-met off the active
  phase. *Definition edits stay ungated* (`projects_update`, `criteria.*`,
  `deliverable.create`/components, task edits).
- **Lists stay full + optional `phase=`** (#22) — future phases are
  intentionally visible; the filter is a mobile-grouping convenience.
- Tests: all phases hydrate at create; cross-phase ratify/mark-met rejected;
  future-phase definition edit allowed; un-ratify re-pends a gate; current-phase
  ratify fires the gate + auto-advances.

## WS3 — Preset reference specs (mostly YAML)

- **Author `hub/templates/projects/code-migration.v1.yaml`** in full: 5 phases
  (env-setup→port→integrate→experiment→deliver) with per-phase deliverables +
  criteria (text/gate/metric) + **tasks** + **plan** + `transitions:` + typed
  `parameters:` + bound `agents.steward.code-migration`. Model on
  `research.v1.yaml`.
- **Upgrade `research.v1.yaml`**: typed params + per-phase `tasks:` + `plan:` +
  bound `agents.steward.research`.
- **Remove** `write-memo.yaml`, `reproduce-paper.yaml` + a one-line cleanup of
  their seeded rows. **Domain stewards** under `hub/templates/agents/`:
  `steward.research.v1` (exists) + new `steward.code-migration.v1`.
- Tests: both presets parse + validate as complete specs; a `*_meta_test.go`
  invariant that every shipped preset is structurally complete.

## WS4 — Governed project.create + Start (Go + Flutter)

- **New propose kind `project.create`** (`apply_project_create.go`, modeled on
  `apply_template_install.go`): `change_spec` carries `{name, config_yaml,
  parameters_json}` **inline** → proposal `pending_payload_json` shows the spec
  for review (#39/#40); Apply runs the same create+materialize path as direct
  create (steward bound, not spawned); Rollback archives the project. Approval
  routes through it (`handlers_attention.go:547`).
- **`POST …/projects/{id}/start`**: spawns the bound steward via the existing
  spawn path (`apply_agent_spawn.go`); idempotent (409 if already running);
  emits `project.started` audit. Route in `server.go`.
- **Project read** exposes a derived `steward_started` (bound + no running
  project steward ⇒ false).
- **Mobile** (project detail): render the proposed-spec review on the approval
  card; a "Not started — review & Start" affordance + **Start** button;
  all-phase structure grouped by phase with ratify/mark-met **disabled outside
  the active phase** (server enforces, WS1). Gated; CI builds; director
  device-tests.
- Tests (Go): propose→approve materializes; start spawns + idempotent; read
  exposes `steward_started`.

## WS5 — Steward prompt + remove template.install + docs (Go/prompt/docs)

- **Remove `template.install` from stewards (#39/#40):** gate it principal-only
  in `mcp_authority_roles.go` (steward path `:335`); stewards get
  `project.create` instead.
- **Steward prompt:** add a single **"Creating a project"** section to
  `steward.general.v1.yaml` (+ `steward.v1.yaml`): *compose a project spec
  (`config_yaml`) — phases, per-phase deliverables/criteria/tasks/plan, typed
  params, bound steward — using the shipped presets as a schema reference; then
  `propose(project.create, …)`. A one-off is a 1-phase project.* Remove any
  attach→`template.install` guidance.
- Update `docs/spine/protocols.md` and the hub-MCP reference (new
  `project.create` kind + two-family naming, ADR-033 D-1). Tests:
  `tool_registry_test.go` / `tool_contract_sweep_test.go` for the new kind; a
  role-gate test that a steward token cannot call `template.install`.

## Verification

- **Per PR:** from `hub/`, `PATH=/usr/local/go/bin:$PATH go build ./... &&
  go test ./... && go vet ./...`; `-race` on packages touching hydration /
  spawn; gofmt clean; CI + CodeQL green before merge.
- **MCP invariants (WS4/WS5):** catalog + dispatcher + handler in lockstep; pass
  `tool_registry_test.go` + `native_tools_meta_test.go` +
  `tool_contract_sweep_test.go`.
- **Mobile (WS4):** gated Dart, CI build, director device-test.
- **End-to-end:** steward `propose(project.create, {config_yaml=code-migration
  example, params})` → principal reviews the inline spec → approve → all 5
  phases' criteria/deliverables/tasks materialize + draft plan seeded, no steward
  running; edit a `deliver`-phase AC definition (allowed); ratify a
  `deliver`-phase deliverable while in env-setup (rejected); ratify the
  env-setup deliverable (allowed) → gate fires → auto-advance; `POST /start` →
  steward spawns.

## Closes / advances

#26, #22, #23 (WS0/WS1) · #31, #32, #33, #34, #35, #36, #30 (WS2/WS3) · #39, #40
(WS4/WS5). Leaves #24/#25 (narrowed: run auto-timestamps + metrics attach;
structured audit old/new + per-project read) as a small follow-up.

## References

- ADRs: [046](../decisions/046-projects-from-inline-spec.md) (the model),
  [044](../decisions/044-adaptive-project-lifecycle.md) (+ its 2026-06-08
  amendment), [030](../decisions/030-governed-actions-and-propose-verb.md) (the
  `propose` verb), [033](../decisions/033-tool-catalog-naming-and-registration.md)
  (tool registration).
- Predecessor: PRs [#43](https://github.com/physercoe/termipod/pull/43)
  (root-cause fixes), [#44](https://github.com/physercoe/termipod/pull/44)
  (WS0 ADRs).
