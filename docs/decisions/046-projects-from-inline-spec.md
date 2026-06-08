# 046. A project's spec is its `config_yaml`; create is a governed action

> **Type:** decision
> **Status:** Accepted (2026-06-08) — implemented across WS0–WS5 (see the
> [plan](../plans/projects-from-inline-spec.md)); director simplification during the
> code-migration lifecycle review (issues #21–#41). **Amended same day** — see
> [Amendment: `template.install` stays an agent-proposable action](#amendment-2026-06-08--templateinstall-stays-an-agent-proposable-action).
> Amends
> [044](044-adaptive-project-lifecycle.md); builds on
> [030](030-governed-actions-and-propose-verb.md) (the `propose` verb),
> [025](025-project-steward-accountability.md) (the project steward), and
> [017](017-layered-stewards.md) (general + domain stewards).
> **Audience:** contributors
> **Last verified vs code:** v1.0.808

**TL;DR.** We collapse the "project template" and "project" concepts. A
project carries its full spec **inline in its own `config_yaml`** — phases
(≥1), per-phase deliverables / criteria / **tasks** / **plan**, **typed
parameters**, and a **bound domain steward**. A steward creates a project
through **one** governed action — `propose(kind="project.create", {name,
config_yaml, parameters_json})` — whose approval **materializes** the project
from that spec (the approval *is* the install — there is no separate
`template.install`). The shipped `research` / `code-migration` YAMLs are
**reference examples** of the schema, not an installed library. The bound
steward is **not spawned** at create; an explicit **Start** spawns it after the
principal reviews the materialized project.

## Context

[ADR-044](044-adaptive-project-lifecycle.md) framed `phase_specs` as a "draft
roadmap" living in a separate, installed *template* that a project references by
id. A second round of lifecycle testing (#21–#41) showed the template/project
split is itself the source of repeated confusion and silent failures:

- `template_id` matched the YAML `name:`, not the DB-row ULID a caller naturally
  passes → silent empty project (#41).
- The template `phases:` had three divergent YAML shapes that parsed to empty
  with no error (#38).
- Demo projects (bootstrap path) and template-created projects had
  fundamentally different initialization (#30).
- Authoring a template was undiscoverable (attach → `propose(template.install)`
  → approve), and the approval showed only a blob `sha256` the principal
  couldn't review (#39, #40).
- 5 of 6 templates were empty shells (#31).

The director's resolution removes the split: **there is no separate template
thing.** The spec that defines a project *is* the project (its `config_yaml`).
Creating a project is one governed act; approving it materializes it. A
"template" is just a worked example a steward reads to learn the schema.

## Decision

1. **The spec lives in `projects.config_yaml`.** It carries phases (≥1 — a
   one-off job is simply a 1-phase project), per-phase deliverables / criteria /
   **tasks** / **plan**, **typed parameters** (`{type, required, default,
   description, min/max/enum}`), and a **bound domain steward**. Materialization
   (per the [044 amendment](044-adaptive-project-lifecycle.md#amendment-2026-06-08--early-bind--completion-gating),
   early-bind) reads the project's **own** `config_yaml` — not a named template
   file. (This makes the #38/#41 template-file-resolution class of silent
   failures irrelevant on the create path.)

2. **Create is the governed action `project.create`.** A steward
   `propose(kind="project.create", {name, config_yaml, parameters_json})`; the
   `change_spec` carries the spec **inline**, so the proposal's
   `pending_payload_json` shows the full proposed project on the approval card
   (closes #40). On approval, Apply runs the **same create + materialize path**
   as a direct create. There is **no `template.install` verb in the steward
   surface** (closes #39); `template.install` remains a principal-only direct
   action. A principal may also create a project directly (their authority needs
   no self-approval).

3. **Presets are reference examples, not a library.** `research` and
   `code-migration` ship as complete example specs a steward reads (via the
   existing project-template read endpoint) to learn the schema, then adapts to
   the actual need. They are **not** instantiated by id; principals and stewards
   compose a `config_yaml` per project. A template is **not** a recurrence
   marker — not every multi-phase project repeats.

4. **Steward bound, spawned on Start.** `config_yaml` names the project's domain
   steward (#33). Create **binds** but does not **spawn** it; the principal
   reviews/edits the materialized project and an explicit `POST …/projects/{id}/
   start` spawns the bound steward (idempotent). This is why materialization and
   spawn are separate events even though the proposal was already reviewed: the
   *spec* review (approval card) and the *materialized project* review (detail
   page) are distinct, and the director keeps the start trigger.

## Amendment (2026-06-08) — `template.install` stays an agent-proposable action

Decision §2 above (and the TL;DR) overstated the consequence as "there is **no
`template.install` verb in the steward surface**" / "`template.install` remains
a **principal-only** direct action." The director corrected this the same day:

> The principal approves the `template.install`; **agents should be able to
> propose a template.**

What ADR-046 actually changes is narrower than "remove `template.install`":

- **Project creation** no longer routes through installing-a-project-template.
  A project's spec *is* its `config_yaml`, and an agent creates a project with
  `propose(kind="project.create")`. This is the whole of #39/#40's fix — the
  reviewable surface is the inline spec, not a blob `sha256`.
- **`template.install` is unchanged** for what it is actually for — authoring
  **agent / prompt / plan** templates. It stays a normal ADR-030 governed
  action: an agent (steward) **proposes** it; the **principal approves**. It is
  *not* gated principal-only, and it is *not* the project-creation path.

So the two are orthogonal: `project.create` is how you create a *project*;
`template.install` is how you author a reusable *template* (and a project no
longer needs one — presets are reference examples). The steward prompts and
`docs/reference/hub-mcp.md §4` reflect this split; the role gate is unchanged
(no `template.install` principal-only gate was added).

## Consequences

**Easier.** One concept and one governed path; the approval card shows the real
project, not a hash; presets become documentation rather than load-bearing
infrastructure; no template-file resolution on the hot path.

**Harder / now constrained.** `project.create` joins the governance surface and
needs the three-in-lockstep catalog/dispatcher/handler + a policy tier
([033](033-tool-catalog-naming-and-registration.md)); the `config_yaml` spec
schema (typed params, tasks, plan, bound steward) must be validated on create;
mobile must render a spec-review approval card, a Start affordance, and
phase-gated completion controls.

**Out of scope.** Deliverable *content* validation (the hub holds metadata, not
bytes — A3); plan-step execution semantics (owned by the steward post-Start);
migrating already-installed template DB rows (they remain readable as
references).

## References

- Issues: #21–#41 (the lifecycle cluster). This ADR addresses #30, #31, #33,
  #36, #39, #40; the [044 amendment](044-adaptive-project-lifecycle.md#amendment-2026-06-08--early-bind--completion-gating)
  addresses #22, #23, #26.
- Code: `internal/server/handlers_projects.go` (create),
  `template_hydration.go` (materialize from `config_yaml`),
  `apply_template_install.go` (the model for `apply_project_create.go`),
  `handlers_attention.go` (approval routing), `mcp_authority_roles.go` (role
  gating), `validate_project_config.go`.
- Related ADRs: amends [044](044-adaptive-project-lifecycle.md); builds on
  [030](030-governed-actions-and-propose-verb.md),
  [025](025-project-steward-accountability.md),
  [017](017-layered-stewards.md).
- Plan: [`plans/projects-from-inline-spec.md`](../plans/projects-from-inline-spec.md)
  (the WS0–WS5 implementation program).
