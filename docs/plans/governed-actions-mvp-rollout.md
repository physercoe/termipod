---
name: Governed actions MVP rollout
description: Wedge-by-wedge execution plan for ADR-030 — generic `propose` MCP verb, 4-tier authorisation ladder, per-(kind, tier) policy file, four MVP kinds (deliverable.set_state, phase.advance, task.set_status, worker_tool_call.escalate routing extension), principal-override audit, deprecated aliases for the two existing apply-on-approve paths. Single principal MVP with multi-member schema hooks. Two phases (hub then mobile); ~1300-1500 LOC code + ~750 lines prose; 11 wedges.
---

# Governed actions MVP rollout — phased

> **Type:** plan
> **Status:** ALL PHASES COMPLETE (2026-05-24, v1.0.674-695).
> Phase 1: 11 hub wedges at v1.0.674-685. Phase 2: W12-W14 at
> v1.0.686. Phase 3: W19.6 hub + W19.5 mobile at v1.0.687; W15 at
> v1.0.688; W16 at v1.0.689; W17 at v1.0.690; W18 at v1.0.691;
> W21 at v1.0.692; W20 at v1.0.693; W19.6-mobile at v1.0.694;
> W19 (steward-side inbox) at v1.0.695. Open follow-ups: W20-resolved
> (override on resolved rows), deferred propose kinds (criterion /
> agent.terminate / etc — plan §5).
> **Audience:** contributors
> **Last verified vs code:** v1.0.804-alpha (the W6 `phase.advance` kind was **retired** by [ADR-044](../decisions/044-adaptive-project-lifecycle.md) P3 — phase advance is now AC-driven; the registry now carries `deliverable.set_state` + `task.set_status` + `agent.spawn` + `template.install` + the four ADR-044 lifecycle kinds (`deliverable.create`, `criteria.create/update/delete`). The rollout mechanics — propose verb, 4-tier ladder, policy, override — are unchanged; the W6/W16 `phase.advance` wedges below are historical.)
> **Freshness:** contract

**TL;DR.** Close the "approve isn't load-bearing enough" gap by
generalising apply-on-approve into a single MCP verb. Phase 1 is
hub-side (schema, verb, dispatcher, 3 propose apply functions,
permission_prompt re-addressing, override audit, alias compat).
Phase 2 is steward prompt + bundled-templates work (re-propose
rule). Phase 3 is mobile (per-kind propose cards, override
affordance). Each phase is independently shippable; Phase 1 is
the load-bearing structural change. Locked decisions in
[decisions/030-governed-actions-and-propose-verb.md](../decisions/030-governed-actions-and-propose-verb.md);
motivation + prior-art audit in
[discussions/governed-actions-and-propose-verb.md](../discussions/governed-actions-and-propose-verb.md).

---

## 1. Phase order, summarised

| Phase | Ship | Approx LOC | Depends on |
|---|---|---|---|
| 1 | Hub: schema migration (0045), `propose` MCP verb (with cross-project authz check), kind dispatcher, 3 apply functions, permission_prompt strict-parent re-addressing, override audit, alias compat, `kinds:` extension to existing `policy.yaml`, **loop-sweep escalation signal (audit-row only)**, tests | ~1080 | — |
| 2 | Prompts: re-propose rule in **9** bundled steward templates (was 8 before antigravity at v1.0.641); deprecate `request_approval+spawnIn` and `template_proposal` patterns in prompt prose; new test-steward-lifecycle scenarios | ~270 prose | Phase 1 |
| 3 | Mobile: per-kind propose card (4 cards, **each with stalled variant**), override affordance, decided-row visual, **`_filterForAttention('propose')` mapping**, **top-of-Me stalled-decisions digest card**, read-only viewer for the `kinds:` block of `policy.yaml` | ~400 | Phase 1 |

Phase 1 closes the structural gap and unblocks every load-bearing
state change to flow through one verb. Phase 2 teaches the
stewards to use it. Phase 3 makes the authoriser surface match
the new verb's per-kind shape.

---

## 2. Phase 1 — hub-side

### 2.1 Goal

Any agent (steward or worker, gated by `roles.yaml`) can call
`propose(kind, target_ref, change_spec, reason, addressee_tier?,
dry_run?)`. The hub raises an `attention_items` row stamped with
`change_kind`, `assigned_tier`, and the per-(kind, tier) policy
addressing. On `/decide(approve)` the dispatcher reads
`change_kind` and calls the registered apply function. On
`/decide(reject)` the existing fan-back delivers the rejection
to the requester. Principal can override any lower-tier decision
on kinds with `override_allowed: true`; override emits an audit
row. The two existing apply-on-approve branches (`approval_request
+ spawnIn`, `template_proposal`) work unchanged through alias
dispatch. Worker `permission_prompt` rows raised under a steward
parent re-address to the parent steward.

### 2.2 Wedges

**W1. Schema migration — `attention_items.change_kind` + `assigned_tier` + `change_spec_json` + `target_ref_json` (~60 LOC).**

> **Migration overlap with ADR-034 (migration 0042).** Migration
> 0042 (shipped v1.0.632) already added `inactivity_deadline`,
> `last_progress_at`, `opened_at`, `absolute_cap`,
> `escalation_state`, `terminal_reason`, and `cause` to
> `attention_items`. The W1 columns are disjoint — no literal
> collision — but two conceptual overlaps must be respected by the
> propose handler and the sweep, per the 2026-05-20 ADR-030
> amendment:
>
> 1. **`cause` (lineage, ADR-034 D-8) vs `target_ref_json`
>    (mutation target, ADR-030 D-1).** For `task.set_status` the
>    two often hold the same `task_id`; the propose handler MUST
>    populate both. `cause` is read by the directive trace;
>    `target_ref_json` is read by the apply function.
> 2. **`assigned_tier` (decision authority, ADR-030 D-3) vs
>    `escalation_state` (signal state, ADR-034 D-4).** Per the
>    Option 2′ rewrite of D-7, `assigned_tier` is immutable;
>    `escalation_state` is the signal walker. The sweep MUST NOT
>    re-emit `escalation_advanced` for the same `escalation_state`
>    value across ticks — the column is also the dedup key.

- New migration `0045_attention_items_governed_actions.up.sql`
  (renumbered from the originally-planned 0044; the 0044 slot was
  taken by the post-v1.0.636 handle-normalization migration shipped
  first <!-- verify file hub/migrations/0044_strip_handle_at_prefix.up.sql --> — see migration 0044 + glossary "handle").
  The 0045 slot is now occupied by this wedge's
  migration <!-- verify file hub/migrations/0045_attention_items_governed_actions.up.sql --> (shipped v1.0.674):
  ```sql
  ALTER TABLE attention_items
    ADD COLUMN change_kind TEXT;          -- e.g. "deliverable.set_state"
  ALTER TABLE attention_items
    ADD COLUMN assigned_tier TEXT;        -- 'worker'|'project-steward'|'general-steward'|'principal'
  ALTER TABLE attention_items
    ADD COLUMN change_spec_json TEXT;     -- per-kind payload, applied on approve
  ALTER TABLE attention_items
    ADD COLUMN target_ref_json TEXT;      -- {project_id, deliverable_id?, ...}
  ALTER TABLE attention_items
    ADD COLUMN executed_json TEXT;        -- mirror of attentionDecideOut.Executed
  CREATE INDEX idx_attention_change_kind
    ON attention_items(change_kind)
    WHERE change_kind IS NOT NULL;
  CREATE INDEX idx_attention_assigned_tier
    ON attention_items(assigned_tier, status)
    WHERE assigned_tier IS NOT NULL;
  ```
- Down migration drops the columns + indexes.
- `hub/internal/storage/attention.go` — extend the row struct
  with the four new fields; nullable on read for backward
  compatibility with pre-migration rows.

**W2. Policy file extension (~80 LOC).**

> Per the 2026-05-20 pre-W1 decision #1: extend the **existing**
> `<dataRoot>/team/policy.yaml` (read by `hub/internal/server/policy.go`)
> with a new top-level `kinds:` block. One file, one reader. No new
> file at `<dataRoot>/policy/governed-actions.yaml`.

- `hub/internal/server/policy.go` — extend the existing `Policy`
  struct with a `Kinds map[string]KindPolicy` field:
  ```go
  type KindPolicy struct {
      DefaultTier       string                  // 'principal'|'project-steward'|...
      Quorum            map[string]QuorumPolicy // per-tier {M: int}
      Commits           bool
      OverrideAllowed   bool
      EscalateOnReject  bool   // MVP: always false
      EscalateOnTimeout bool   // MVP: always false
  }
  type QuorumPolicy struct{ M int }
  ```
- Add `Policy.KindFor(kind) (KindPolicy, bool)` accessor <!-- verify symbol hub/internal/server/policy.go KindFor -->;
  missing kind → permissive default (`default_tier="principal"`,
  M=1, `override_allowed=true`, escalate-flags `false`) + a WARN log.
- Reload on mtime change reuses the existing `policyStore` reload
  loop in `policy.go`. No new `policyStore`. Shipped v1.0.675 via
  `newPolicyStoreWithLogger` so the WARN lands on the daemon's
  structured log.
- The existing `tiers / approvers / quorum / escalation` keys
  remain valid in the same file — the alias-compat spawn/template-
  install dispatch consults them during the one-cycle deprecation
  window.
- Tests: missing `kinds:` block → empty map + permissive defaults
  on `KindFor`; malformed `kinds:` entry → typed error at load;
  unknown kind on `KindFor` → permissive default with WARN log;
  legacy `tiers:` rows still resolve through the existing
  `Decide()` path.

**W3. `lint-governed-actions.sh` + propose-kind registry skeleton (~50 LOC shell + ~95 LOC Go + ~220 LOC tests).**

- `hub/internal/server/propose_kinds.go` <!-- verify symbol hub/internal/server/propose_kinds.go ListProposeKinds -->
  — registry skeleton (shipped at W3 because the linter reads it):
  `ProposeKind` type, `proposeKinds` global map under sync.RWMutex,
  `RegisterProposeKind` / `LookupProposeKind` / `ListProposeKinds`
  / `resetProposeKindsForTest`. Empty registry at W3 ship; W5/W6/W7
  fill it via `func init()`. The linter's static-grep contract is
  documented on `RegisterProposeKind` — only literal `Kind: "..."`
  registrations are discoverable.
- `scripts/lint-governed-actions.sh` <!-- verify file scripts/lint-governed-actions.sh -->
  — three checks: (1) kind-shape (snake_case-with-dots), (2)
  bidirectional registry⇄policy consistency (FAIL on either-side
  mismatch when a policy file is found; WARN-on-empty-policy when
  registry is non-empty), (3) escalate-on-timeout sanity. Discovers
  policy files via repo glob; `--policy <path>` pins explicit.
  Tested via Go (`lint_governed_actions_test.go`, 8 script cases
  + 2 registry cases) that shell out to the script with crafted
  fixtures — keeps the bash contract authoritative without
  re-implementing it in Go.
- CI wired in `.github/workflows/ci.yml` after `lint-doc-anchors`.
- Per the 2026-05-20 D-7 Option 2′ amendment, the linter verifies
  that any kind with `escalate_on_timeout: true` has a
  `default_tier` strictly below `principal` (so the signal has
  somewhere to walk) and emits a warning if it does not. The flag's
  semantics in Option 2′ are "fire signal", not "move addressee",
  so a `principal`-default kind with the flag set is suspect.

**W4. `propose` MCP verb (~180 LOC + ~380 LOC tests). Shipped v1.0.677-alpha.**

- `hub/internal/server/handlers_propose.go` <!-- verify symbol hub/internal/server/handlers_propose.go mcpPropose -->
  — single handler; full propose pipeline (validate → scope check →
  tier resolution → assignees → dry_run branch → row insert + audit).
  Uses the W3 registry (`LookupProposeKind` / `ListProposeKinds`) +
  W2 policy (`KindFor`) + W1 schema columns.
- `hub/internal/server/native_tools.go` `buildNativeTools()` <!-- verify symbol hub/internal/server/native_tools.go buildNativeTools --> —
  `propose` entry shipped with full JSON schema (kind / target_ref /
  change_spec required; reason / addressee_tier / dry_run optional).
  Audience: steward + worker via `WorkerEligible: true`. **This is
  the single declaration point for every native MCP tool per
  ADR-033** (shipped v1.0.631).
- `hub/internal/server/tiers.go` — `propose` registered as
  `TierSignificant` (mirrors `templates_propose`; propose raises an
  attention row).
- `nativeToolMeta` overlay — `propose` carries the ADR-031 D-1
  SeeAlso list: `[request_approval, tasks_update, templates_propose]`.
- (Deferred to a post-MVP polish wedge: per-engine prompt-scaffold
  enumeration — `scaffolds_templates.go` doesn't carry a propose
  reference. The agents that need propose will reach it via the
  `tools/list` MCP catalog plus the `tools_get propose` lookup; the
  scaffold-time tool listing is decorative.)
- Audience: steward + worker (steward by default; worker only when
  `roles.yaml` allows the kind).
- **Cross-project target check** (per 2026-05-20 pre-W1 decision #4).
  After `kind` validation and before policy lookup, the handler runs
  a target-scope check against the caller:
  - Worker caller: `target_ref.project_id` MUST equal
    `caller.project_id`; mismatch → 403 with
    `{code: "out_of_scope", message: "<kind> targets project P2 but caller is bound to project P1"}`.
  - Project steward / domain steward / general steward callers: may
    cross projects (a steward coordinating across owned projects, or
    the general steward routing across the team).
  - The check skips when `target_ref` carries no `project_id`
    (kind-specific shapes that operate above the project scope —
    `template.install`, future `agent.archive`).
  - The caller's `kind` is read from `agents.kind`; the steward
    predicate is `kind == 'steward.v1' || strings.HasPrefix(kind, 'steward.')`
    per the v1.0.607 kind-based detection rule.
  - Tests: worker proposing against own project (200); worker
    proposing against another project (403 out_of_scope); domain
    steward proposing across projects (200); general steward
    proposing across projects (200); template.install with no
    `project_id` in target_ref (skip check, 200).
- The handler order shipped at W4:
  1. JSON-decode + presence check on `kind`.
  2. `LookupProposeKind` against the registry; unknown → -32602
     with the registered set echoed in the message.
  3. Per-kind `Validate` hook (if registered).
  4. `checkProposeScope` per pre-W1 decision #4 (see below).
  5. Tier resolution: caller hint > `KindFor(...).DefaultTier` >
     `GovTierPrincipal` permissive fallback. Unknown tiers
     rejected with the valid set echoed.
  6. `resolveAssigneesForTier` → current_assignees_json. For
     `project-steward` tier with a live `findRunningProjectSteward`
     hit, the live agent's handle lands; otherwise a symbolic
     `@steward.project` / `@steward.general` / `@principal`
     placeholder so the mobile card always has a legible
     addressee.
  7. `dry_run=true` → call registered `DryRun`, return
     `{status:"dry_run", preview, would_address}` without insert.
  8. INSERT into `attention_items` with the W1 columns
     (change_kind, assigned_tier, change_spec_json,
     target_ref_json) plus the legacy mirror in
     `pending_payload_json`. `project_id` extracted from
     `target_ref.project_id` so the project queue surfaces the row.
  9. Audit row `propose.raised`.
  10. Return `{request_id, kind:"propose", change_kind,
      assigned_tier, status:"awaiting_response"}`.
- W5/W6/W7 register their `Apply` functions into the registry via
  `func init()`. W8 wires the /decide path to dispatch through the
  registry to the per-kind Apply function on approve.

**Cross-project target check** (per 2026-05-20 pre-W1 decision #4)
shipped as `handlers_propose.go::checkProposeScope`:

- Worker caller: `target_ref.project_id` MUST equal
  `caller.project_id`; mismatch → -32602 with message
  `"propose: out_of_scope — <kind> targets project P2 but caller is
  bound to project P1"`.
- Steward callers (kind matches `steward.v1` or `strings.HasPrefix
  "steward."`) may cross projects.
- The check skips when `target_ref` carries no `project_id`
  (kind-specific shapes that operate above the project scope —
  `template.install`, future `agent.archive`).

**W5. Apply function — `deliverable.set_state` (~220 LOC + ~310 LOC tests). Shipped v1.0.678-alpha.**

- `hub/internal/server/apply_deliverable_set_state.go` <!-- verify symbol hub/internal/server/apply_deliverable_set_state.go applyDeliverableSetState --> —
  registers `deliverable.set_state` via `init()` with Validate /
  DryRun / Apply.
- **Audit-action choice.** The plan's original "use a unified
  `deliverable.state_changed` action" was reconsidered at ship time:
  the legacy REST paths emit `deliverable.{ratified,unratified,updated}`
  per transition direction, and activity-feed renderers already
  consume those names. Using a fresh unified action would create a
  parallel feed event. Instead the apply function emits the SAME
  action the legacy path emits (`deliverable.ratified` for X →
  ratified, `deliverable.unratified` for ratified → draft, otherwise
  `deliverable.updated`) and stamps the propose lineage on the audit
  meta (`via:"propose"`, `by_tier:<tier>`, `propose_id:<att_id>`).
  Mobile renderers stay unchanged; propose-routed and direct-REST
  changes look identical in the feed but the audit-row meta is the
  discriminator.
- **ProposeKind.Apply signature change** (landed this wedge). The
  Apply hook now receives a `ProposeApplyContext { AttentionID,
  Team, AssignedTier, DeciderHandle }` so the audit-meta plumbing
  works without re-querying. This is a pre-W4-consumer change so
  cost is zero; W6/W7/W8 register against the new shape from day
  one. (DryRun + Validate signatures are unchanged.)
- Validate: target_ref has `project_id` + `deliverable_id`;
  change_spec.state in `{draft, in-review, ratified}`. Transition
  validity (e.g. `ratified → in-review`) is enforced at Apply time
  under the row read, not pre-flight, so concurrent state changes
  fail visibly.
- DryRun: reads the current state + kind and returns
  `{from_state, to_state, target_deliverable_id, target_kind, no_op}`.
- Apply: three transition branches mirror the legacy endpoints —
  `→ ratified` writes the `ratified_at`/`ratified_by_actor` stamps
  (using `ac.DeciderHandle`, falling back to `"propose"` when
  blank); `ratified → draft` clears them; everything else is a
  plain state UPDATE.
- No-op (from == to) returns `executed.no_op = true` without
  touching the row OR emitting an audit — symmetric with the
  "already ratified" 409 the legacy `/ratify` returns, surfaced
  through `executed_json` rather than failing the apply.
- Tests (8): registered-at-init, Validate happy + 4 reject paths,
  DryRun preview shape + read-only, Apply in-review→ratified
  (stamps + audit meta lineage), Apply ratified→draft (clears
  stamps), Apply draft→in-review (no stamps), Apply no-op (no
  row change + no audit), end-to-end propose-then-manual-Apply
  with the propose row's request_id linked back from the audit
  meta.
- **Lint hardening shipped alongside.** `scripts/lint-governed-actions.sh`
  now strips Go comments before its static-grep, so doc-comment
  example registrations (`// RegisterProposeKind(ProposeKind{...})`)
  no longer pollute the registry count.

**W6. Apply function — `phase.advance` (~220 LOC + ~250 LOC tests). Shipped v1.0.679-alpha.**

- `hub/internal/server/apply_phase_advance.go` <!-- verify symbol hub/internal/server/apply_phase_advance.go applyPhaseAdvance --> —
  registers `phase.advance` via `init()` with Validate / DryRun /
  Apply.
- **Two design calls at ship time.**
  1. **Audit action: `project.phase_advanced`, not `phase_set`.**
     The legacy endpoint emits `phase_set` for NULL → first-phase
     hydration and `phase_advanced` for gate-cleared advances; only
     the second is reachable via propose (initial hydration goes
     through project-create, not propose). Stay strict on
     `project.phase_advanced` so the activity feed reads
     consistently.
  2. **Acceptance-criteria gating not enforced at Apply.** The
     legacy endpoint 409s when required criteria are pending; here
     the approver IS the gate — if the principal approves a phase
     advance, they're explicitly overriding criteria. The propose
     `reason` field should make that clear; the audit row carries
     the same `via="propose"` stamp the W5 deliverable apply uses.
- Validate: target_ref has `project_id`; change_spec has `to_phase`;
  `from_phase` is optional.
- DryRun: reads current phase + template phases and returns
  `{project_id, from_phase, to_phase, no_op, to_phase_not_in_template,
  from_phase_expected?, from_phase_drifted?}`. Two flags help the
  proposer notice "you're about to walk off the template" /
  "the project has advanced since you staked your propose".
- Apply: re-reads under the row lock; rejects on stale `from_phase`
  mismatch with `phase.advance: stale from_phase — proposed %q but
  project is now at %q` (proposer can re-propose against the new
  current); appends a `phaseTransition` to `phase_history`
  (by_actor = decider handle, falling back to `"propose"`);
  short-circuits no-op without row touch or audit emission.
- **DryRun reads cross-team.** A new helper
  `loadProjectPhaseRowAnyTeam` falls back when the apply context
  doesn't yet know the team — Apply itself ALWAYS uses the scoped
  `loadProjectPhaseRow` since `ac.Team` is the authoritative scope.
- Tests (9): registered-at-init; Validate (5 sub-cases — happy /
  happy-with-from / missing project_id / missing to_phase / empty
  spec); DryRun preview shape; DryRun flags from_phase drift; Apply
  happy path (history append + audit lineage); stale from_phase
  rejection + unchanged-state invariant; no-op short-circuit (no
  row mutation, no audit); empty from_phase skips optimistic check;
  wrong-team not-found.

**W7. Apply function — `task.set_status` (~210 LOC + ~330 LOC tests). Shipped v1.0.680-alpha.**

- `hub/internal/server/apply_task_set_status.go` <!-- verify symbol hub/internal/server/apply_task_set_status.go applyTaskSetStatus --> —
  registers `task.set_status` via `init()`.
- **Narrowed status set.** Per ADR-029 D-3, propose only permits
  `done` and `cancelled`. `in_progress` and `blocked` are
  auto-derived by `deriveTaskStatusFromAgent` (spawn-lifecycle
  watchers); proposing them would race the auto-derive and confuse
  the audit timeline. The `todo` initial state is set at
  task-create time and never re-entered. The Validate rejection
  message names the forbidden status + the allowed set + the
  ADR-029 D-3 reason so the agent re-proposes correctly.
- Apply mirrors `handlePatchTask`'s status-flip branch: UPDATE
  status + completed_at (terminal stamp, mandatory for both
  done/cancelled) + result_summary (when provided; NULLIF semantics
  preserve prior value when caller omits). Emits `task.status`
  audit with from→to summary, then fires `notifyTaskAssigner` so
  the steward who delegated the work sees the system message
  inline (ADR-029 W2.9 up-edge). Both audit emission and assigner
  notification mirror the legacy REST path exactly; the
  discriminator is `meta.via="propose"`.
- No-op (from == to) short-circuits without row touch or audit —
  same pattern as W5/W6.
- Tests (8): registered-at-init; Validate (9 sub-cases — 2 happy
  + 3 missing-field + 4 reject-status); DryRun preview shape +
  read-only; Apply done-with-result-summary (audit lineage,
  completed_at stamp, result_summary persisted); Apply cancelled
  (audit + stamp); Apply no-op (no row mutation, no audit); Apply
  not-found; end-to-end propose-then-manual-Apply with request_id
  round-trip into the audit meta and tier resolution through W4.
- (W9 + override-rollback test deferred to W9 itself, per plan.)

**W8. Deprecated propose-aliases for `agent.spawn` and `template.install` + decide-handler dispatcher refactor (~480 LOC + ~700 LOC tests). Shipped v1.0.681-alpha.**

- `hub/internal/server/apply_agent_spawn.go` <!-- verify symbol hub/internal/server/apply_agent_spawn.go applyAgentSpawn --> —
  wraps `DoSpawn`. change_spec IS the spawnIn JSON shape (same
  shape the legacy `pending_payload_json` carried), so the two
  dispatch paths share the same unmarshal. target_ref is cosmetic
  (spawn details live in change_spec). Validate / DryRun / Apply
  all registered.
- `hub/internal/server/apply_template_install.go` <!-- verify symbol hub/internal/server/apply_template_install.go applyTemplateInstall --> —
  wraps `installProposedTemplate`. change_spec is the
  {category, name, blob_sha256, rationale?, proposed_by?} payload
  the installer already understands. DryRun stat's the blob so
  the preview can show the body size + presence; missing-blob is a
  soft signal, not an error.
- `ProposeApplyContext.Via` field (added this wedge). Default ""
  → "propose" via `ViaOrDefault()`; the W8 legacy-alias dispatch
  sets "alias_legacy" so audit-meta consumers can distinguish
  the two dispatch shapes without parsing the kind name. W5/W6/W7
  apply functions now read `ac.ViaOrDefault()` instead of the
  hard-coded "propose" literal.
- **Decide-handler refactor** at `handlers_attention.go::handleDecideAttention`.
  SELECT widened for the 4 ADR-030 W1 columns (change_kind,
  assigned_tier, change_spec_json, target_ref_json). The two
  prior `if` blocks (lines 378-414 in v1.0.677) collapse into a
  single dispatcher arm with three input shapes:
  1. `kind="propose"` + change_kind → ADR-030 path, Via="propose"
  2. `kind="approval_request"` + spawnIn pending_payload →
     alias_legacy path, dispatches to `agent.spawn` registry
     entry with Via="alias_legacy"
  3. `kind="template_proposal"` + install pending_payload →
     alias_legacy path, dispatches to `template.install` with
     Via="alias_legacy"
  All three converge on `LookupProposeKind(...).Apply(...)`. The
  ADR-030 W1 `executed_json` column is now populated from the
  return value (best-effort UPDATE; log on failure).
- Tests (16 across 3 files):
  - `apply_agent_spawn_test.go` (6) — registered, Validate (4
    cases), DryRun, Apply happy path (audit lineage), Apply
    alias_legacy (via tag), Apply missing-team.
  - `apply_template_install_test.go` (7) — registered, Validate
    (5 cases), DryRun present-blob, DryRun missing-blob, Apply
    happy path (file written + audit lineage + rationale/
    proposed_by carry-through), Apply alias_legacy (via tag).
  - `handlers_propose_dispatch_test.go` (5) — propose+task.set_status
    end-to-end (status flip + audit + executed_json mirror);
    propose+agent.spawn end-to-end; legacy approval_request+spawnIn
    via=alias_legacy + propose_id link; legacy template_proposal
    via=alias_legacy; reject-skips-apply regression (no audit
    fires, status unchanged, audit count only +1 for the
    attention.decide row).
- **No backward-compat regression** in the existing legacy paths —
  the full hub test suite (107s) is green, including every
  pre-W8 test that exercises `approval_request + spawnIn` and
  `template_proposal`.

**W9. Principal override of lower-tier decisions (~470 LOC + ~430 LOC tests). Shipped v1.0.682-alpha.**

- `ProposeKind.Rollback` field <!-- verify symbol hub/internal/server/propose_kinds.go Rollback -->
  added to the registry shape. Optional — kinds without one
  explicitly refuse override (422 with hint).
- `handlers_attention.go::handleAttentionOverride` <!-- verify symbol hub/internal/server/handlers_attention.go handleAttentionOverride -->
  is the new branch the decide handler routes to when
  `status != "open"` AND `in.Override == true`. Guard order:
  1. **Principal-tier check** — MVP: `in.By == "@principal"`.
     Returns 403 if not (token-identity tier check is a
     follow-up wedge).
  2. **Status check** — only `status == "resolved"` rows
     overrideable. Other terminal statuses (e.g. "expired")
     return 409.
  3. **Propose-ladder check** — `change_kind == ""` means a
     legacy non-propose row; 422 (override is undefined for
     them).
  4. **Policy check** — `policy.KindFor(change_kind).OverrideAllowed`
     must be true; 400 with `Hint{SeeTool: policy_read}` if not.
  5. **Rollback presence** — `LookupProposeKind(change_kind).Rollback`
     must be non-nil; 422 with "no Rollback registered" otherwise.
  6. **Original-apply prerequisite** — row must have
     non-empty `executed_json`; 422 if not (e.g. the prior
     decision was reject, so nothing landed to roll back).
  7. **Double-override block** — if the last decision in
     `decisions_json` is already "override", returns 409.
- Wire shape: `POST /decide` body adds `override: true`.
  `decision` may be `"approve" | "reject" | "override"`; the
  validation gate accepts `"override"` only when paired with
  `override: true`.
- Audit shape: emits `attention.override` with meta
  `{change_kind, by, by_tier, original_decision, reason,
  rollback_executed}` — the rollback's executed_json rides
  inline so consumer queries don't need to join audit_events
  to the row.
- Per-kind Rollback semantics shipped:
  - `deliverable.set_state` → re-calls Apply with the
    recorded `from_state` as the new `change_spec.state`.
    All transition-direction logic (clears stamps on
    ratified→draft, etc.) runs unchanged.
  - `phase.advance` → re-calls Apply with `from_phase` and
    `to_phase` swapped. Optimistic-concurrency check still
    fires — if another actor moved the phase since the
    original Apply, rollback refuses.
  - `task.set_status` → BYPASSES Apply's
    propose-permitted-status check because the rollback
    target may be `in_progress` / `blocked` / `todo` (not
    propose-permitted). Writes the UPDATE directly + emits
    `task.status` audit with `rollback: true`. Clears
    `completed_at` when restoring a non-terminal status.
  - `agent.spawn` → emits `agent.spawn.rollback_todo`
    audit pointing the principal at the spawned agent_id
    with hint "manually terminate via DELETE …".
    Per-plan: terminate-via-propose is post-MVP.
  - `template.install` → deletes the installed file +
    emits `template.uninstall` audit with `rollback: true`.
    Does NOT restore a prior version (plan §5 follow-up;
    apply path doesn't capture the prior body).
- Tests (8): override task.set_status reverts (status revert
  + audit chain); override template.install deletes file;
  override_allowed=false → 400 with hint; no override flag
  preserves 409; non-principal caller → 403; double-override
  → 409; kind without Rollback → 422; still-open row stays
  unaffected (open-row + decision=override fails the
  validation gate before reaching override).

**Decision-validation gate extension** (landed this wedge).
The `decision` field gained `"override"` as a third legal
value, accepted only when `override: true`. The override
handler always appends `"decision": "override"` to
decisions_json regardless of the incoming Decision —
callers may pass `decision="approve"` + `override=true`
just as legibly as `decision="override"` + `override=true`.

**W10. `worker_tool_call.escalate` — re-address `permission_prompt` rows raised by steward-parented workers (~70 LOC + ~190 LOC tests). Shipped v1.0.683-alpha.**

- `hub/internal/server/mcp_more.go` `mcpPermissionPrompt` <!-- verify symbol hub/internal/server/mcp_more.go mcpPermissionPrompt -->
  — extended to call the new `permissionPromptAddressee` helper
  <!-- verify symbol hub/internal/server/mcp_more.go permissionPromptAddressee -->
  before the row INSERT. When the helper returns a non-empty
  steward_id, the INSERT writes
  `current_assignees_json = [<steward_id>]` AND
  `assigned_tier = 'project-steward'`. Otherwise the row stays
  team-wide-addressed (`assignees='[]'`, `assigned_tier=NULL`).
- **Strict same-project parent-steward predicate** shipped as
  one SQL JOIN with five conjuncts (per 2026-05-20 pre-W1
  decision #3):
  ```sql
  SELECT p.id
  FROM agents w
  JOIN agents p ON p.id = w.parent_agent_id
  WHERE w.team_id = ?
    AND w.id = ?
    AND w.parent_agent_id IS NOT NULL
    AND p.kind LIKE 'steward.%'
    AND p.project_id IS NOT NULL
    AND w.project_id IS NOT NULL
    AND p.project_id = w.project_id
  ```
  The two `IS NOT NULL` guards on `project_id` defend against
  SQL's `NULL = NULL → NULL` semantics — without them, two
  unbound rows would accidentally match. The third clause
  (`p.project_id = w.project_id`) is the binding-drift guard
  (v1.0.605-class bug — parent-id pointer survives but
  project binding has drifted).
- Best-effort: any non-`ErrNoRows` DB error logs a warn +
  returns "" so a transient DB issue degrades to safe
  (team-wide).
- `dispatchAttentionReply` is unchanged — the existing fan-back
  already addresses by `session_id`; the new addressing only
  affects which inbox surfaces the row first.
- Tests (6): same-project steward parent addresses row;
  cross-project steward parent (binding drift) stays
  team-wide; non-steward parent stays team-wide; orphan
  worker (no parent) stays team-wide; both-sides-NULL
  project_id stays team-wide (the NULL=NULL guard); direct
  helper test against ghost worker returns "".
- (Steward-decides → fan-back test deferred — covered by
  existing `TestDecide_PermissionPromptFansOutAttentionReply`
  which lives in `handlers_attention_permission_prompt_test.go`
  and still passes under the W10 changes since `dispatchAttentionReply`
  is untouched.)
- (Principal-override-of-codex-parked-RPC complex case
  deferred — W9 ships the override path against
  propose-kind rows; permission_prompt isn't a propose
  kind, so override on it would require widening
  `handleAttentionOverride`. Tracked separately.)

**W11. `dispatchAttentionReply` allowlist + fan-back payload + ADR-032 envelope (~140 LOC + ~310 LOC tests). Shipped v1.0.684-alpha.**

- `handlers_attention.go::dispatchAttentionReply` <!-- verify symbol hub/internal/server/handlers_attention.go dispatchAttentionReply -->
  signature extended to take an `attentionReplyExtras { ChangeKind,
  Executed }` so propose-specific fields ride alongside the
  existing per-attention-kind fields. SELECT also widened to fetch
  `actor_handle` + `cause` (the ADR-034 lineage pointer) for the
  envelope.
- Allowlist at `handleDecideAttention` gains `propose` — the
  fan-back fires on approve / reject / override of any propose
  row that carries a session_id.
- Fan-back payload shape (flat top level):
  `{request_id, kind:"propose", change_kind, decision, reason?,
  executed?, envelope:{...}}` — `executed` populated on approve
  AND on override (carries the rollback's executed payload so the
  agent sees the state reverted).
- **ADR-032 envelope composition** — nested under `payload.envelope`
  rather than flattened at the top level, because the payload's
  top-level `kind` field already holds the attention kind
  ("propose", "approval_request", …) and would collide with the
  envelope's `kind`. Envelope-aware consumers read
  `payload["envelope"]["from"|"to"|"kind"|"text"|"cause"|"thread"]`.
- **Envelope.kind = `KindReport`**, NOT `"attention_reply"`. The
  plan W11 narrative referenced `"attention_reply"`, but that's the
  `agent_events.kind` value (an MCP-level event kind), not an
  ADR-032 envelope kind. ADR-032 D-2 defines a closed four-value
  enum {directive, question, report, notification}; a
  propose-decision CLOSES the loop the propose opened, so `report`
  is the structurally correct mapping. The `agent_event` row itself
  still uses `kind="input.attention_reply"` — only the envelope's
  inner kind value changes.
- Envelope fields populated:
  - `from`: `{role: principal, handle: in.By}` (defensive default
    `@principal` when blank)
  - `to`: `{role: peer_worker, handle: requester_handle,
    agent_id: current_agent_id}`
  - `kind`: `KindReport`
  - `text`: short human-readable summary
    `"<decision> <attention_kind>: <change_kind?> — <reason?>"`
    so engines surfacing the envelope inline don't have to parse
    the payload's other fields
  - `cause`: round-tripped from the attention row's `cause` column
    (the ADR-034 lineage pointer to the enclosing task)
  - `thread`: `{transport: TransportAttention, id: attention_id}`
- **Override path also fan-backs** (W9 inter-op). The override
  handler calls `dispatchAttentionReply` after the rollback so the
  requester learns the state reverted; they'd otherwise be stuck
  on "approved + applied" without seeing the inverse.
- Tests (6): approve fan-back shape (envelope + executed +
  thread/from/to assertions); reject fan-back omits executed,
  envelope text describes reject; dry_run produces zero fan-back
  events; envelope cause round-trips from the row's `cause`
  column; override produces a second fan-back with the rollback
  payload; legacy approval_request still fans back without
  change_kind (regression — empty extras must not break existing
  attention kinds).

**W11.5. Loop-closure signal — sweep emits audit row on `escalation_state` transition (~110 LOC + ~220 LOC tests). Shipped v1.0.685-alpha — Phase 1 COMPLETE.**

> Lands the 2026-05-20 D-7 Option 2′ amendment on the hub.
> **Per pre-W1 decision #5: audit-row-only — no separate push
> infra in MVP** (repo has no FCM/APNS/firebase). Mobile visibility
> = Activity-tab `agent_events` stream consumes the audit row +
> Me-page query widening (W19.6) surfaces the source row on next
> foreground/pull.

- `hub/internal/server/loop_sweep.go::escalateStall` <!-- verify symbol hub/internal/server/loop_sweep.go escalation_state -->
  already advances `attention_items.escalation_state` and emits the
  generic `loop.stall_escalated` audit. W11.5 adds a SECOND audit
  row scoped to propose semantics via a new
  `emitProposeEscalationAudit` <!-- verify symbol hub/internal/server/loop_sweep.go emitProposeEscalationAudit -->
  helper:
  - `action = "attention.escalation_advanced"`
  - `meta = {attention_id, change_kind, from_state, to_state,
     original_assigned_tier, project_id, change_spec_preview}`
  - `change_spec_preview` = first 200 bytes of
    `attention_items.change_spec_json` with `…` suffix when
    truncated (single-pass computation at emit time, no
    per-render JSON re-parsing).
- `truncateChangeSpecPreview` <!-- verify symbol hub/internal/server/loop_sweep.go truncateChangeSpecPreview -->
  helper unit-tested in isolation.
- **`questionAttentionKinds` extension** (landed this wedge).
  The sweep filters open attention rows by this set; without
  `propose` in it, propose rows would never be picked up as
  loop-entities and W11.5's audit could never fire. Comment
  block documents the rationale (propose is loop-bearing by
  construction — agent calls propose, ends turn, awaits
  fan-back).
- Dedup: the `escalation_state` column itself is the dedup key.
  `escalateStall` only fires when `inactivity_deadline` is past;
  the UPDATE pushes the deadline forward a budget so the next
  tick doesn't re-fire until the next window. Test 3
  (`NoDuplicateOnReTick`) is the regression.
- **No push infrastructure built in MVP.** No FCM/APNS adapter,
  no device-token table, no notifications service. The principal
  learns about escalation on next foreground/pull. Real-time push
  is a clean post-MVP add and is out of scope here.
- The 24h re-push backoff from earlier drafts is **dropped from
  MVP** — without a real-time push channel, repeated re-pushes
  are not a concern.
- Tests (5 in `loop_sweep_propose_audit_test.go`):
  - One sweep tick on a stale propose row emits exactly one
    `attention.escalation_advanced` (alongside the legacy
    `loop.stall_escalated`).
  - Audit meta carries the W11.5-spec shape (attention_id,
    change_kind, from/to_state, original_assigned_tier,
    project_id, change_spec_preview).
  - Two ticks across the same row at the same state emit ONE
    transition, not two (dedup regression).
  - Non-propose attention row (legacy `approval_request`) does
    NOT emit the propose audit — only `loop.stall_escalated`.
  - `truncateChangeSpecPreview` 4-case table test.

### 2.3 Acceptance

- `propose(kind="deliverable.set_state", ...)` → principal
  approves → deliverable state changes → audit row
  `deliverable.state_changed` with `meta.via="propose"` is
  visible in the activity feed.
- `propose(kind="task.set_status", target_ref={task_id}, change_spec={status:"done", result_summary:"..."})` →
  project steward approves → task row updates →
  `audit_events.action="task.status_changed"` written with
  `meta.via="propose"`.
- Worker writes a file in a steward-parented project →
  `permission_prompt` row addressed to the parent steward, NOT
  the team inbox.
- Principal taps "Override" on a resolved steward-approve →
  state reverts, override audit row is written and shows in
  activity feed.
- `approval_request + spawnIn` MCP call from a worker → still
  resolves through the alias dispatcher; new and old shapes
  coexist.
- `scripts/lint-governed-actions.sh` passes; no kind drift
  between policy file and registered dispatchers.

---

## 3. Phase 2 — prompts + test scenarios

### 3.1 Goal

Bundled stewards (general + 4 domain variants) know to use
`propose` for governed actions and follow the re-propose
convention. Test-steward-lifecycle gains scenarios that
exercise each MVP kind end-to-end.

### 3.2 Wedges

**W12. Re-propose rule in 9 bundled steward templates (~30 LOC prose × 9 files). Shipped v1.0.686-alpha.**

- Each of the nine bundled steward templates gained a
  `### Governed actions — use the `propose` verb (ADR-030)`
  subsection under Authority. The full set as of v1.0.686
  (verified against `hub/templates/prompts/` <!-- verify glob hub/templates/prompts/steward.*.md 9 -->):
  `steward.v1.md`, `steward.general.v1.md`,
  `steward.claude-m4.v1.md`, `steward.codex.v1.md`,
  `steward.gemini.v1.md`, `steward.kimi.v1.md`,
  `steward.research.v1.md`, `steward.infra.v1.md`,
  `steward.antigravity.v1.md` (added at v1.0.641 by ADR-035).

  Each gets the same canonical three-paragraph block (the
  governed-actions gate, `dry_run`, the re-propose discipline)
  with two domain tailorings noted:
  - **`steward.infra.v1.md`** adds: "For infra-heavy actions
    (deploy/rollback, config change), this means routing the
    state change through `propose` even when you have shell
    access to do it directly."
  - **`steward.antigravity.v1.md`** adds: "This applies
    REGARDLESS of agy's local file tools — even if you can edit
    a deliverable file directly, route state transitions through
    `propose` so the system records the audit lineage and the
    principal can override."
  - **`steward.research.v1.md`** adds a parenthetical pointing
    at "your phase 3 review-cycle outcomes" and "your
    `plan.advance` calls" so the research steward sees its
    domain verbs in the propose set.
  - **`steward.claude-m4.v1.md`** is a test-only template with
    no Authority section; it received a one-paragraph pointer
    under Concierge mode that defers to the general steward
    template for the full convention.

  Canonical block <!-- verify file hub/templates/prompts/steward.v1.md -->:

  > **Governed actions are gated.** For load-bearing state
  > changes — deliverable state transitions, project-phase
  > advances, task close-out, agent spawn, template install —
  > use the `propose(kind, target_ref, change_spec, reason)`
  > verb. The system applies the change on approve; do not
  > attempt the mutation directly via REST or by editing files
  > yourself. The five MVP kinds are `deliverable.set_state`,
  > `phase.advance`, `task.set_status`, `agent.spawn`, and
  > `template.install`.
  >
  > **`dry_run: true`** lets you preview the diff before the
  > authoriser sees it. Use it when you're uncertain whether
  > the change_spec is well-formed — the preview returns
  > `{from, to, target_label, no_op}` so you can self-correct
  > before raising the attention row.
  >
  > **If a propose is rejected, do not immediately re-propose
  > to a higher tier.** Re-examine the reason in the fan-back
  > envelope. Re-propose ONLY if you have new information that
  > addresses the rejection — fresh evidence, a smaller scope,
  > or a different `target_ref`. Repeated propose-then-reject
  > loops are themselves a signal to escalate to the principal
  > via `request_help` instead.

**W13. Test scenarios in test-steward-lifecycle.md (~410 LOC prose). Shipped v1.0.686-alpha.**

- Ten new scenarios appended to
  `docs/how-to/test-steward-lifecycle.md` <!-- verify file docs/how-to/test-steward-lifecycle.md -->,
  numbered from 33 (current highest is S32, ADR-034 stuck-task
  recovery — verified against v1.0.636). All ten sit under a
  shared H1 banner with the two shared pre-conditions (hub build
  ≥ v1.0.685-alpha; live worker + project steward triad) and the
  shared diagnostic ladder (audit_events oracle):
  - **S33**: `deliverable.set_state.ratified` propose →
    approve → audit row visible.
  - **S34**: `deliverable.set_state.ratified` propose →
    reject → fan-back received → steward re-examines.
  - **S35**: `phase.advance` propose → approve → phase
    advances → activity feed updated.
  - **S36**: `task.set_status.done` propose by worker →
    project steward approves → task row updates.
  - **S37**: `worker_tool_call.escalate` (Write call by
    worker) → project steward sees row → approves → file
    written.
  - **S38**: Principal override of steward-approve →
    override audit row + state revert.
  - **S39**: Alias compat — old `approval_request + spawnIn`
    call resolves through dispatcher.
  - **S40**: `dry_run` preview surfaces in awaiting_response
    payload; agent decides to abort or proceed.
  - **S41** *(new — D-7 Option 2′ signal walk)*: `task.set_status`
    propose to project-steward sits past `inactivity_deadline` →
    sweep emits `attention.escalation_advanced` audit row + push
    to principal → principal's Me-page shows the row with the
    stalled pill (`⏱ Stuck Nh — addressed to @steward.proj-X`) and
    `Override` action → principal taps Override → D-8 override
    audit row written → task status updates → original steward
    can no longer decide (row resolved). Verify the push fires
    once per transition, not per sweep tick.
  - **S42** *(new — late-but-valid decision)*: Same setup as S41,
    but the project steward decides between the sweep tick that
    fired the push and the principal opening Me. Verify the row
    resolves via the steward's `attention.decide` (normal path);
    `assigned_tier` stays `'project-steward'` throughout; no
    override audit fires; the principal's Me-page list refreshes
    to drop the stalled card.

**W14. Seed-demo annotation (~13 LOC prose comment). Shipped v1.0.686-alpha.**

- Added a comment block in
  `hub/internal/server/seed_demo_lifecycle.go`
  <!-- verify file hub/internal/server/seed_demo_lifecycle.go -->
  immediately before the `INSERT INTO attention_items` call:

  ```
  // NOTE: seeded lifecycle attentions are UI-only. They carry no
  // session_id, so dispatchAttentionReply short-circuits at the
  // missing-session guard in handlers_attention.go. Tap-approve from
  // the principal's Me-tab demonstrates the queue surface; no
  // system-side state change fires (no propose-kind Apply runs,
  // nothing mutates beyond the attention row itself). When ADR-030
  // Phase 1 wired the propose dispatcher, these rows were left
  // session_id-less ON PURPOSE so the demo doesn't accidentally
  // flip a real deliverable / phase / task as a side-effect of the
  // principal exploring the queue. See:
  // docs/discussions/governed-actions-and-propose-verb.md §9.
  ```
- Per the principal's instruction: **do not modify the seed
  itself**. The W11 missing-session guard reference is to
  `handlers_attention.go::dispatchAttentionReply` lines 985-987
  (the `if !sessionID.Valid || sessionID.String == "" { return nil }`
  block); the comment intentionally omits the line number since
  the guard's existence is the contract, not its address
  <!-- verify symbol hub/internal/server/handlers_attention.go dispatchAttentionReply -->.

### 3.3 Acceptance

- All nine bundled steward templates carry the canonical
  `Governed actions — use the `propose` verb` block under
  Authority / Concierge mode <!-- verify glob hub/templates/prompts/steward.*.md 9 -->.
- `docs/how-to/test-steward-lifecycle.md` lists Scenarios 33-42
  with reproducible steps + diagnostic ladders + failure modes.
- Seed-demo comment is in place; no behavioural change to the
  seed itself.

---

## 4. Phase 3 — mobile

### 4.1 Goal

The Me-page queue renders `propose` rows with per-kind cards that
surface enough context to decide. **Per the 2026-05-20 D-7 Option 2′
amendment, stalled propose rows (those a viewer is not the addressee
for but `escalation_state` has surfaced to their tier) render the
same per-kind cards in a "stalled" variant** — same body, top pill
showing `⏱ Stuck Nh — addressed to @<addressee>`, action buttons
flip to `Override` / `View source` (instead of `Approve` / `Reject`).
A top-of-Me digest card summarises the stalled count. Resolved rows
expose an "Override" menu when the policy allows. The
`governed-actions.yaml` policy file is readable (not editable) under
Settings.

### 4.2 Me-page IA fit (no redesign)

The current `lib/screens/me/me_screen.dart` is a single scrolling
page with a four-chip filter bar — *All · Requests · Agents ·
Messages* — keyed by attention `kind`. Plan §4 absorbs `propose`
into the existing IA without rename or chip add:

- **`propose` is a Request by kind.** `_filterForAttention('propose')`
  → `_Filter.approvals` (one-line addition at `me_screen.dart:349`
  switch). Stalled propose rows surface to the principal still in
  the Requests bucket — they're requests-the-system-is-asking;
  stalled is a *state*, not a kind, and gets a card decoration not
  a filter chip.
- **No bucket rename.** Messages stays the catch-all "every other
  attention kind"; renaming it would erase the catch-all semantic.
- **The "stalled" axis** becomes the per-card variant from W15-W18
  plus the new digest card from W19.6 below.

### 4.3 Wedges

**W15. Per-kind propose card — `deliverable.set_state` (~270 LOC card + actions + router + 175 LOC tests). Shipped v1.0.688-alpha.**

- `lib/screens/me/widgets/propose_card_deliverable.dart` (new)
  <!-- verify file lib/screens/me/widgets/propose_card_deliverable.dart -->
  — body: state-transition chips (from_state → to_state, with the
  `to_state` chip in green/emphasis), summary text, deliverable
  id in monospace, and either primary or stalled actions.
- `lib/screens/me/widgets/propose_card_actions.dart` (new)
  <!-- verify file lib/screens/me/widgets/propose_card_actions.dart -->
  — shared between W15-W18: `PrimaryProposeActions` (Approve /
  Reject) and `StalledProposeActions` (Override / View source).
  The Override flow opens an inline AlertDialog asking for a
  required reason then POSTs decide with `decision='override'`
  + `override=true` (W20 will replace this with the D-8
  confirmation sheet).
- `lib/screens/me/widgets/propose_card_router.dart` (new) — single
  dispatch widget that picks the per-kind card by `change_kind`
  (W16-W18 register here as they ship); unrecognised change_kinds
  fall back to `InlineApprovalActions`. Keeps me_screen.dart's new
  branch to one line.
- `lib/screens/me/me_screen.dart` — new `else if (item.kind ==
  'propose')` branch routes through `ProposeCardRouter` BEFORE the
  legacy `InlineApprovalActions` fallback. Tier hardcoded to
  `'principal'` in MVP; the W19 steward-side inbox will pass its
  own tier.
- `lib/services/hub/hub_client.dart::decideAttention` +
  `lib/providers/hub_provider.dart::decide` gained `override: bool`
  named param. Forwarded to wire as `{override: true}` only when
  set — existing call sites (Approve/Reject throughout the app)
  unaffected.
- `test/screens/me/propose_card_deliverable_test.dart` (new) — 10
  widget test cases: primary variant shows Approve/Reject + state
  chips + summary + deliverable id + no Stuck pill; stalled
  variant shows Override + View deliverable + Stuck pill with
  addressee name + body unchanged; addressed-AND-stalled →
  primary (the addressee predicate wins); legacy row (no
  assigned_tier) → primary fallback; change_spec parses from
  raw JSON string too.
- **`dry_run` preview** intentionally not rendered yet — the
  ProposeKind.DryRun path returns the preview synchronously to
  the proposing agent (no attention row created), so there's
  no propose attention with a `dry_run` payload for the card
  to render. If a future ProposeKind variant stores the preview
  on the row, this card adds a `_DryRunDiff` block above the
  actions; not on the W15 critical path.

**W16. Per-kind propose card — `phase.advance` + shared visuals refactor (~120 LOC card + 250 LOC visuals + 100 LOC test). Shipped v1.0.689-alpha.**

- `lib/screens/me/widgets/propose_card_phase.dart` (new)
  <!-- verify file lib/screens/me/widgets/propose_card_phase.dart -->
  — body: `from_phase → to_phase` transition with the project id and
  summary. The `from_phase` may be absent on the wire (phase.advance
  optimistic-concurrency check is opt-in); the [TransitionFrame]
  renders `→ to_phase` without a from-side chip when so.
- `lib/screens/me/widgets/propose_card_visuals.dart` (new) —
  shared visual + parsing helpers extracted once W16 made the
  duplication concrete. Exports `decodeJsonObject` (defensive
  JSON-or-Map decoder), `StalledPill` (top pill for stalled
  variant), `TransitionChip` + `TransitionFrame` (the from→to
  pattern), `TransitionChipFamily` enum (green = state, indigo =
  phase, slate = status) — different colour families per kind so
  the user can tell propose-rows apart at a glance even with the
  same body shape. W15 (deliverable) + W16 (phase) + the
  forthcoming W17 (task) all consume these.
- `lib/screens/me/widgets/propose_card_deliverable.dart` refactored
  to consume the shared visuals (-130 LOC of local primitives).
  Behaviour unchanged; test suite passes unchanged.
- `lib/screens/me/widgets/propose_card_router.dart` — phase.advance
  registered alongside deliverable.set_state.
- **W15-lint-warning fix.** v1.0.688's
  `propose_card_deliverable.dart` carried an unused
  `hub_provider.dart` import (left over from the original draft
  before actions were extracted); the visuals refactor removed it
  along with the local widgets. CI green this commit.
- `test/screens/me/propose_card_phase_test.dart` (new) — 7 widget
  test cases: primary variant shows Approve/Reject + phase chips
  + project id; stalled variant shows Override/View project + Stuck
  pill; from_phase omitted renders `→ to_phase` only (the forced-
  advance case).

**W17. Per-kind propose card — `task.set_status` (~125 LOC + 135 LOC test). Shipped v1.0.690-alpha.**

- `lib/screens/me/widgets/propose_card_task.dart` (new)
  <!-- verify file lib/screens/me/widgets/propose_card_task.dart -->
  — body: `→ status` (no from-side chip; task.set_status's
  change_spec has no `from_status` field — Apply compares the row's
  current status at runtime), result_summary as a wrapped
  quote-block when present (recommended for `done`, allowed-but-
  pointless for `cancelled`), task + project ids.
- Registered in `propose_card_router.dart` under
  `case 'task.set_status'`.
- `test/screens/me/propose_card_task_test.dart` (new) — 7 widget
  cases: primary variant shows Approve/Reject + status chip
  (no from-side) + result_summary block + task/project ids; absent
  result_summary stays hidden; stalled variant shows Override /
  View task + Stuck pill.
- **W16-lint-error fix.** v1.0.689's
  `propose_card_visuals.dart` placed the `library;` directive
  AFTER the imports (it was nested inside the file's docstring
  so I'd expected it to count as a leading comment); flutter
  analyze flagged it as `library_directive_not_first` (error
  level — fatal), failing CI on the v1.0.689 push. Fixed by
  moving `library;` to position 19 (right after the docstring,
  before the first import). Sibling note: `propose_addressee.dart`
  had the same `library;` placement but no imports follow, so it
  was already lint-clean.

**W18. Per-kind cards — `agent.spawn` + `template.install` (~280 LOC + 165 LOC test). Shipped v1.0.691-alpha. PLAN-LITERAL REINTERPRETATION.**

> Plan literal named this wedge `worker_tool_call.escalate` — that's
> not a Phase 1 propose kind (Phase 1 shipped 5 kinds:
> deliverable.set_state, phase.advance, task.set_status,
> agent.spawn, template.install per plan §3.2 W8). Shipped the
> structural intent: per-kind cards for the two ALIAS kinds
> (agent.spawn + template.install — the legacy approval_request /
> template_proposal flows that W8 re-routed through propose). The
> worker-tool-escalation flow is realised as `permission_prompt`
> after W10's re-addressing; its card is the existing
> `InlineApprovalActions` fallback in the router. See
> [[feedback_plan_narrative_loose_talk]] for the pattern.

- `lib/screens/me/widgets/propose_card_agent_spawn.dart` (new)
  <!-- verify file lib/screens/me/widgets/propose_card_agent_spawn.dart -->.
  Compact body: child_handle (bold mono) + engine kind chip (deep
  purple), reason, host (when pinned), project (when bound).
  Punts full spawn_spec_yaml to the Details affordance.
- `lib/screens/me/widgets/propose_card_template_install.dart` (new)
  <!-- verify file lib/screens/me/widgets/propose_card_template_install.dart -->.
  Compact body: `<category>/<name>` path (bold mono with file icon),
  rationale, proposed_by handle, blob sha256 12-char prefix.
  Punts full template body to the Details affordance (the legacy
  v1.0.602 template-proposal preview block already renders the
  full YAML there).
- `propose_card_router.dart` — both kinds registered. All 5 MVP
  propose kinds now covered; unknown change_kinds fall through to
  the legacy `InlineApprovalActions`.
- `test/screens/me/propose_card_alias_test.dart` (new) — 7 widget
  cases across both cards: primary variants with full body
  rendering; stalled variants with Override + tailored View label
  ("View spawn detail" / "View template body"); edge cases
  (missing handle → "(no handle)"; missing category/name →
  "(unknown)"; missing rationale → block omitted).

**W19. Steward-side propose inbox (~245 LOC widget+screen + 145 LOC test). Shipped v1.0.695-alpha.**

- `lib/screens/sessions/widgets/steward_propose_inbox.dart` (new)
  <!-- verify file lib/screens/sessions/widgets/steward_propose_inbox.dart -->
  — exports two widgets + a public predicate:
  - `StewardProposeInboxPill` — AppBar icon button (inbox icon +
    amber count badge) with self-gating visibility: hidden unless
    `agentKind.startsWith('steward.')` AND `projectId.isNotEmpty`
    AND at least one matching row. Drops cleanly into every
    session AppBar's actions; non-steward sessions see nothing.
  - `StewardProposeInboxScreen` — list view pushed when the pill
    is tapped. Each matching row renders via `ProposeCardRouter`
    with `myTier: 'project-steward'` so the per-kind cards
    (W15-W18) show their PRIMARY variant (Approve/Reject) for
    the addressee. Empty-state explains the surface ("workers
    will route load-bearing state changes here via propose").
  - `stewardProposeInboxRows(attention, projectId)` — the
    4-clause predicate exposed publicly so tests can verify it
    without instantiating widgets. The clauses: `kind=propose`
    AND `assigned_tier=project-steward` AND `status=open` AND
    `project_id == <steward's project>`.
- `lib/screens/sessions/sessions_screen.dart` — `SessionChatScreen`
  build path adds an `agentProjectId` local (looked up from the
  agent row) and threads it + `_agentKind()` into the new pill at
  the start of the AppBar `actions:` list. Self-gating means
  worker / general-steward / team-only sessions stay unchanged.
- `test/screens/sessions/steward_propose_inbox_test.dart` (new) —
  8 cases:
  - 5 predicate tests: empty list; 4-clause filter exact match;
    multi-match order preservation; empty projectId → zero
    matches; legacy row without project_id → never matches.
  - 3 widget gating tests: hidden when agentKind is not a
    steward; hidden when projectId is empty; hidden when no
    matching rows.

**W19.5. `propose` kind → Requests filter mapping + Me-page query widen (~55 LOC + ~85 LOC tests). Shipped v1.0.687-alpha.**

> Lands the Option 2′ IA fit on the mobile side. Pairs with the
> hub-side widening described under W19.6 below — both halves
> shipped together at v1.0.687.

- `lib/screens/me/me_screen.dart` — added `case 'propose': return
  _Filter.approvals;` to `_filterForAttention`
  <!-- verify symbol lib/screens/me/me_screen.dart _filterForAttention -->.
  Comment block names the W15-W18 per-kind card consumers + the
  stalled-variant decoration.
- `lib/services/hub/hub_client.dart` `listAttention(...)` +
  `listAttentionCached(...)` — gained `includeEscalated: bool` named
  parameter that forwards to the `include_escalated` query param.
  `_attentionQuery(status, includeEscalated)` helper composes the
  query map, kept consistent across the cached + uncached variants.
- `lib/providers/hub_provider.dart` — both call sites
  (`_resolveCached` for the Me-page primary fetch + `_reloadAttention`
  for the post-decide refresh) pass `includeEscalated: true`
  unconditionally so the contract is locked in.
- `lib/screens/me/widgets/propose_addressee.dart` (new) —
  `isAddresseeOfPropose(attention, myTier)` (primary vs stalled
  variant selector); `isStalledPropose(attention)` (true when
  `escalation_state != 'none'`); `stalledPillLabel(attention)`
  (returns `'Stuck'` for the top pill). Top-level functions in a
  shared utility file so [[W15-W18]] cards + the [[W19 steward
  inbox]] consume one predicate without duplication.
- `test/screens/me/propose_addressee_test.dart` (new) — 11
  test cases covering both predicates' happy paths + the legacy
  empty-tier / cross-tier / empty-viewer-tier edge cases.

**W19.6. Hub-side Me-page query widen (hub half ~85 LOC + 210 LOC tests) + top-of-Me digest card (mobile half — pending W19.6-mobile, deferred). Hub half shipped v1.0.687-alpha.**

- **Hub side.** `hub/internal/server/handlers_attention.go`
  <!-- verify symbol hub/internal/server/handlers_attention.go handleListAttention -->.
  `handleListAttention` + `handleGetAttention` gained 6 new fields
  on `attentionOut`: `ChangeKind`, `AssignedTier` (string), plus
  `ChangeSpec`, `TargetRef`, `Executed` (json.RawMessage with
  omitempty) — the 5 ADR-030 W1 columns from migration 0045 — and
  `EscalationState` (string, from migration 0042's
  `attention_items.escalation_state`). All 6 fields ride the
  same SELECT widening; the JSON tags match the snake_case lib/
  consumers expect.

  `include_escalated` query param: parsed but unused in MVP — the
  baseline already returns every open row regardless of tier, so
  widening has nothing to widen against until `?tier=<t>` lands.
  Captured as a forward-compat hook so Phase 3 mobile (W19.5)
  can pass it unconditionally and the contract is locked in
  before any tier-narrowing arrives. PLAN-LITERAL REINTERPRETATION:
  the plan's `WHERE assigned_tier = caller_tier` baseline doesn't
  exist on the current handler (the existing endpoint returns all
  rows); shipping the literal widening would have changed visibility
  for every existing API caller. Shipped the forward-compat hook
  instead — see [[feedback_plan_narrative_loose_talk]] for the
  pattern. **Test coverage** in
  `handlers_attention_adr030_fields_test.go` (6 cases): propose-shaped
  row exposes all 5 ADR-030 fields on list + get; legacy row omits
  them via omitempty; escalated row exposes `escalation_state` AND
  preserves `assigned_tier` (the D-7 Option 2′ "decision stays"
  contract); `include_escalated` query param parses with any value
  including bogus.
- **Mobile side — top-of-Me digest card** (deferred to a follow-up
  Phase 3 wedge — tracked as **W19.6-mobile**).
  `lib/screens/me/widgets/stalled_decisions_digest.dart` (new).
  Renders at the top of Me-page (sibling-above the
  `_FilterBar`, parallel to the existing bottom-of-page "Since
  you were last here" digest at `me_screen.dart` §"Wedge 5"). Hidden
  when the stalled-row count is 0. Shape:
  ```
  ┌──────────────────────────────────────────────┐
  │ ⏱  Stalled decisions                    [3]  │
  │  3 stalled at stewards · 1 stalled >24h with │
  │  you. Tap to review.                         │
  └──────────────────────────────────────────────┘
  ```
  Tap → applies an in-list filter narrowing to
  `escalation_state != 'none'` rows (preserves the active
  chip-filter, AND-combined). ~40 LOC.
- **Mobile side — top-of-Me digest card.**
  `lib/screens/me/widgets/stalled_decisions_digest.dart` (new).
  Renders at the top of Me-page (sibling-above the
  `_FilterBar`, parallel to the existing bottom-of-page "Since
  you were last here" digest at `me_screen.dart` §"Wedge 5"). Hidden
  when the stalled-row count is 0. Shape:
  ```
  ┌──────────────────────────────────────────────┐
  │ ⏱  Stalled decisions                    [3]  │
  │  3 stalled at stewards · 1 stalled >24h with │
  │  you. Tap to review.                         │
  └──────────────────────────────────────────────┘
  ```
  Tap → applies an in-list filter narrowing to
  `escalation_state != 'none'` rows (preserves the active
  chip-filter, AND-combined). ~40 LOC.
- **Pull-only in MVP** (per pre-W1 decision #5). The mobile app
  has no real-time push channel. Visibility is via: (a) the
  Activity tab, which consumes `agent_events` and renders the
  `attention.escalation_advanced` audit row when the principal
  opens it; (b) this Me-page digest card, which appears on the
  same screen the principal already opens for decisions. The URI
  intent `termipod://me?stalled=1` is still registered for
  post-MVP push-channel use, but in MVP nothing fires it; the
  digest card is the affordance.

**W19.6-mobile. Top-of-Me stalled-decisions digest card (~160 LOC widget + helpers + 130 LOC test). Shipped v1.0.694-alpha.**

- `lib/screens/me/widgets/stalled_decisions_digest.dart` (new)
  <!-- verify file lib/screens/me/widgets/stalled_decisions_digest.dart -->
  — amber-bordered card with `Icons.schedule`, "Stalled decisions"
  header, count badge, subtitle that splits "N stalled at
  stewards" vs "N stalled with you" (rows where
  `escalation_state == 'escalated_principal'`). Hidden when total
  stalled count = 0 (renders `SizedBox.shrink()`).
- Tap toggles `stalledFilterProvider` (a NotifierProvider<bool>).
  When ON, the Me-page item list filters further to rows whose
  `escalation_state != 'none'` AND-combined with the active
  chip-filter. Header copy flips "Stalled decisions" →
  "Showing stalled decisions" and the border/background go
  full-saturation amber so the active state is obvious.
- Helpers exported alongside the widget for reuse in tests +
  steward inbox (W19): `hasStalledDecisions(items)`,
  `stalledDecisionsCount(items)`, `stalledOverDayDecisionsCount(items)`
  — the last counts only `escalated_principal` rows so the digest
  distinguishes "with stewards still" from "with you now".
- `lib/screens/me/me_screen.dart` — digest sliver inserted ABOVE
  `_SectionLabel` per plan §4.3 W19.6 layout. Filter narrowing
  applied in the `_buildItems → items.where(...).where(stalled)`
  pipeline.
- `test/screens/me/stalled_decisions_digest_test.dart` (new) — 9
  cases: 5 pure-function counter tests (empty / no-stalled / one
  stalled / mixed-state counts / escalated_principal-only count);
  4 widget tests (renders nothing when count=0; renders badge +
  header when count>0; subtitle splits at-stewards vs with-you;
  tap toggles stalledFilterProvider + flips active header copy).

**W20. Override affordance — confirmation sheet (~245 LOC sheet + 120 LOC test + 5 caller updates). Shipped v1.0.693-alpha. SCOPE NARROWED.**

> **Scope-narrowing note.** Plan literal said "Resolved rows
> (status=`resolved`) gain a menu (… overflow) with 'Override
> decision'…". The current Me-page only renders OPEN rows
> (listAttention filters status='open'); supporting override on
> resolved rows requires a separate resolved-rows toggle/filter,
> which is itself a feature, not a small UI affordance. Shipped
> the visible MVP improvement: the modal bottom sheet that
> replaces v1.0.688's inline AlertDialog placeholder. Override on
> resolved rows is deferred to a Phase 3+ follow-up
> (**W20-resolved**) — the override path itself works fine via
> direct REST today; the gap is just the Me-page surface for
> resolved rows.

- `lib/screens/me/widgets/override_sheet.dart` (new)
  <!-- verify file lib/screens/me/widgets/override_sheet.dart -->
  — modal bottom sheet with:
  - drag handle + "Override decision" header with gavel icon
  - explanation paragraph naming the addressee + linking to the
    ADR-030 W9 Rollback semantic
  - context block in muted background showing `change_kind`,
    one-line `change_spec` preview (max 3 lines, ellipsised),
    one-line `target_ref` preview — so the principal sees what
    they're overriding before they type
  - autofocus reason TextField (required); inline error if
    submitted empty; clears on next keystroke
  - Submit button shows CircularProgressIndicator during the
    decide round-trip; Cancel disabled mid-submit
  - errors surface inline (no snack) so the principal can read +
    retry without dismissing
  - returns `bool` — `true` on successful decide, `false` on
    cancel; callers decide what to do with onResolved
- `lib/screens/me/widgets/propose_card_actions.dart` —
  `StalledProposeActions` API changed: takes the full `attention`
  Map<String, dynamic> instead of just `id`, so the sheet can
  render the change_kind + change_spec context without a second
  fetch. All 5 propose cards updated (W15-W18).
- `test/screens/me/override_sheet_test.dart` — 4 widget cases:
  shows change_kind / addressee / reason field / Override button;
  Cancel returns false + closes sheet; empty reason → inline
  error + stays open; change_spec preview includes from_state +
  to_state.

**W21. Read-only policy viewer (~265 LOC mobile + 50 LOC hub endpoint + 145 LOC test). Shipped v1.0.692-alpha. RE-HOMED OUT OF MOBILE SETTINGS.**

> **Re-homing note** (principal-directed, 2026-05-24): the plan
> originally said "Settings > Advanced > Governed action policy".
> The mobile Settings page is for MOBILE preferences (theme, voice,
> etc); hub/team-scoped settings belong on the team-settings
> surface reachable via the team switcher. Shipped under
> **Team switcher → Team settings → Governance → Governed action
> policy** instead. The clarification stands as the design
> principle for future Phase-3 surfaces (any policy that lives in
> `<dataRoot>/team/`, NOT in `~/.shared_preferences`, belongs on
> the team-settings surface).

- **Hub side.** `hub/internal/server/handlers_policy.go::handleGetPolicyKinds`
  <!-- verify symbol hub/internal/server/handlers_policy.go handleGetPolicyKinds -->
  — new `GET /v1/teams/{t}/policy/kinds` endpoint returns the
  parsed `kinds:` block as JSON so the Flutter binary doesn't
  need a YAML parser. Empty file → `{"kinds": {}}` (empty-state
  in mobile). Legacy file with no `kinds:` block → same shape.
  Malformed YAML → 500. Read-only; the canonical edit path
  stays `PUT /policy`. `KindPolicy` + `QuorumPolicy` structs
  gained `json:` tags alongside the existing `yaml:` tags so
  serialization round-trips per ADR-030 spec.
- `hub/internal/server/handlers_policy_kinds_test.go` — 4 tests:
  missing-file → empty map, full-policy → every-field round-trip,
  legacy-policy-without-kinds → empty map, malformed-yaml → 500.
- **Mobile side.** `lib/services/hub/hub_client.dart::getPolicyKinds`
  — calls the new endpoint and returns
  `Map<String, dynamic>`.
- `lib/screens/team/governed_actions_policy_screen.dart` (new)
  <!-- verify file lib/screens/team/governed_actions_policy_screen.dart -->
  — table-style read-only view with 4 columns (kind, default tier,
  commits, override). Sorted by kind name for stable rendering.
  Empty-state explains where to edit the underlying file (Policies
  tab sibling). Footnote spells out the permissive fallback (no
  kinds: block → principal / quorum=1 / override allowed).
  Reload button in the AppBar.
- `lib/screens/team/team_screen.dart` — new ListTile under the
  Governance tab (the existing `_SettingsView`): "Governed
  action policy · Read-only view of policy.yaml `kinds:`".
  Icon: `Icons.gavel_outlined`. Sibling to Budgets / Auth /
  Councils / Steward tiles.

### 4.4 Acceptance

- Principal sees `deliverable.set_state` propose → taps approve
  → mobile shows pending → result returned with `executed`
  payload → row marked resolved.
- Project steward sees `task.set_status` propose in its
  session-side inbox → approves → worker's session receives
  fan-back on next turn.
- Principal can override a steward-approve via the "…" menu
  on a resolved row → override audit appears in activity feed.
- Settings > Advanced > Governed action policy renders the
  six MVP+alias kinds with their default tier and override
  setting.
- **Stalled signal — Option 2′ acceptance.** A `task.set_status`
  propose to project-steward sits past `inactivity_deadline` →
  the principal receives a push → opening Me-page shows the
  top digest card *"1 decision stalled at stewards"* and the
  Requests filter shows the row with the stalled pill +
  `Override` action button → tap Override → D-8 confirmation
  sheet → row resolves; original addressee can no longer
  decide; the audit feed shows `attention.escalation_advanced`
  + `attention.override`.

---

## 5. Open follow-ups (not in this plan)

- **Multi-member + escalation ADR.** Auto-escalation on
  reject and timeout, M>1 quorum for commits, override
  windows, carbon-copy mode. Sequenced after this plan ships
  and the alias deprecation window closes.
- **Deferred propose kinds.** `criterion.set_state`,
  `agent.terminate`, `agent.archive`,
  `permission_policy.change`, `artifact.publish`,
  `project.archive`, `project.update_metadata`. Each is a
  one-wedge addition to the registry.
- **Policy file editor in mobile.** Currently read-only (W21).
  Future wedge: schema-aware editor with validation.
- **Glossary backfill.** New entries: "governed action",
  "commit (action sense)", "propose (verb)". Should land in
  the same PR as ADR-030 or immediately after.
- **Remove the two aliases.** Removal lands in the follow-up
  ADR; existing clients have one cycle to migrate.

---

## 6. Status forward-links

- ADR: [decisions/030-governed-actions-and-propose-verb.md](../decisions/030-governed-actions-and-propose-verb.md)
- Discussion: [discussions/governed-actions-and-propose-verb.md](../discussions/governed-actions-and-propose-verb.md)
- Subsumed discussion: [discussions/worker-permission-routing-to-steward.md](../discussions/worker-permission-routing-to-steward.md)
- Tool-call-approval-patterns reference (extended): [reference/tool-call-approval-patterns.md](../reference/tool-call-approval-patterns.md)
- Related ADRs: ADR-020 (director-side counterpart), ADR-025
  (D3 precedent), ADR-029 (override pattern), ADR-016
  (role gates), **ADR-032 (envelope on fan-back, see W11)**,
  **ADR-034 (loop-entity columns + sweep, see W1 overlap notes
  and W11.5)**, **ADR-033 (tool catalog — W4 registers via
  `native_tools.go`)**, **ADR-028 (owner-kind gate vs principal
  tier — distinct concerns, see ADR-030 §Amendments)**.
