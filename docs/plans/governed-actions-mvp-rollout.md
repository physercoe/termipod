---
name: Governed actions MVP rollout
description: Wedge-by-wedge execution plan for ADR-030 — generic `propose` MCP verb, 4-tier authorisation ladder, per-(kind, tier) policy file, four MVP kinds (deliverable.set_state, phase.advance, task.set_status, worker_tool_call.escalate routing extension), principal-override audit, deprecated aliases for the two existing apply-on-approve paths. Single principal MVP with multi-member schema hooks. Two phases (hub then mobile); ~1300-1500 LOC code + ~750 lines prose; 11 wedges.
---

# Governed actions MVP rollout — phased

> **Type:** plan
> **Status:** Proposed (2026-05-17) — three phases, no work started;
> ADR-030 captures the locked decisions. Reissued 2026-05-20 to
> absorb the ADR-030 amendments (D-7 Option 2′ — decision stays,
> signal walks; ADR-032 envelope on fan-back; ADR-034 loop-entity
> overlap; principal ≠ owner) and fix file/line drift from
> v1.0.620-636.
> **Audience:** contributors
> **Last verified vs code:** v1.0.673-alpha
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
  The 0045 slot is currently free <!-- verify no-file hub/migrations/0045_*.up.sql -->:
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
- Add `Policy.KindFor(kind) (KindPolicy, bool)` accessor; missing
  kind → permissive default (`default_tier="principal"`, M=1,
  `override_allowed=true`, escalate-flags `false`) + a WARN log.
- Reload on mtime change reuses the existing `policyStore` reload
  loop in `policy.go:60+`. No new `policyStore`.
- The existing `tiers / approvers / quorum / escalation` keys
  remain valid in the same file — the alias-compat spawn/template-
  install dispatch consults them during the one-cycle deprecation
  window.
- Tests: missing `kinds:` block → empty map + permissive defaults
  on `KindFor`; malformed `kinds:` entry → typed error at load;
  unknown kind on `KindFor` → permissive default with WARN log;
  legacy `tiers:` rows still resolve through the existing
  `Decide()` path.

**W3. `lint-governed-actions.sh` (~50 LOC shell + 30 LOC tests).**

- `scripts/lint-governed-actions.sh` — walks the policy file
  and the dispatcher's registered kinds (from a new
  `propose_kinds.go` registry — see W4). Fails if a kind in
  the dispatcher has no policy entry, or vice versa.
- Wired into `scripts/lint-all.sh` if it exists, or runnable
  standalone.
- Catches drift between code and config (D-9 follow-up: same
  shape as `lint-glossary.sh`).
- Per the 2026-05-20 D-7 Option 2′ amendment, the linter also
  verifies that any kind with `escalate_on_timeout: true` has a
  `default_tier` strictly below `principal` (so the signal has
  somewhere to walk) and emits a warning if it does not. The flag's
  semantics in Option 2′ are "fire signal", not "move addressee",
  so a `principal`-default kind with the flag set is suspect.

**W4. `propose` MCP verb + kind registry (~180 LOC).**

- `hub/internal/server/native_tools.go` `buildNativeTools()` <!-- verify symbol hub/internal/server/native_tools.go buildNativeTools --> —
  add the `propose` entry. **This is the single declaration point
  for every native MCP tool per ADR-033** (shipped v1.0.631); the
  tool entry carries the JSON schema, audience hint, and dispatch
  arm. The ADR-030 plan was originally written against
  `hub/internal/hubmcpserver/tools.go`; that file no longer carries
  native-tool registration — `buildNativeTools()` is now the one
  table (see `native_tools.go:101` doc comment).
- `hub/internal/server/tiers.go` — add the tier entry. Suggested
  tier: **`TierSignificant`** (mirrors the existing
  `templates_propose` entry at `tiers.go:69` — propose-shaped tools
  are tier-`Significant` because they raise an attention row).
- `hub/internal/hubmcpserver/scaffolds_templates.go` — touch the
  steward scaffold to enumerate `propose` in the steward's tool
  list (mirrors how `templates.propose` is enumerated at
  `scaffolds_templates.go:256`).
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
- `hub/internal/server/handlers_propose.go` (new). Single
  handler:
  - Validate `kind` against the registered kinds.
  - Resolve `addressee_tier`: caller hint > policy default.
  - Compute `current_assignees_json` from tier resolution
    (steward tiers → the relevant steward agent ID; principal
    tier → `["@principal"]`).
  - Insert `attention_items` row with `kind="propose"`,
    `change_kind=<kind>`, `change_spec_json`, `target_ref_json`,
    `assigned_tier`, `session_id` (from `lookupAgentSession`
    so reply routing works), `pending_payload_json` for legacy
    code paths that still scan it.
  - If `dry_run=true`: call the apply function in *dry-run mode*
    to produce a preview diff; embed it in the
    `awaiting_response` payload.
  - Return `{request_id, status:"awaiting_response", dry_run?}`.
- `hub/internal/server/propose_kinds.go` (new). Registry:
  ```go
  type ProposeKind struct {
      Kind     string
      Apply    func(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) (executed json.RawMessage, err error)
      DryRun   func(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) (preview json.RawMessage, err error)
      Validate func(targetRef, changeSpec json.RawMessage) error
  }
  var proposeKinds = map[string]ProposeKind{}
  func RegisterProposeKind(p ProposeKind) { proposeKinds[p.Kind] = p }
  ```
- Three new apply functions (W5, W6, W7 below) register into
  this map via `func init()`. Aliases (W8) register too.

**W5. Apply function — `deliverable.set_state` (~80 LOC + 60 LOC tests).**

- `hub/internal/server/apply_deliverable_set_state.go` (new).
- Calls into existing deliverable state-transition code
  (`handlers_deliverables.go` `setDeliverableState` or
  equivalent — needs an internal-callable variant that bypasses
  HTTP authz since the call is already authorised by `/decide`).
- Records the existing
  `audit_events.action="deliverable.state_changed"` row with
  `meta.via="propose"`, `meta.by_tier=<assigned_tier>`,
  `meta.propose_id=<attention_id>`.
- DryRun: returns
  `{from_state, to_state, target_deliverable_id, target_title}`.
- Validate: checks target_ref has `deliverable_id`; checks
  `change_spec.state` is a valid value; checks transition is
  allowed (e.g. can't go `ratified → draft`; matches the same
  rules `setDeliverableState` enforces).
- Tests:
  - Happy path: propose → approve → state transitions, audit
    written with `via=propose`.
  - dry_run: returns preview, no state change.
  - Invalid transition: validate rejects at `propose` call time.
  - Reject: state unchanged, fan-back delivered.

**W6. Apply function — `phase.advance` (~70 LOC + 50 LOC tests).**

- `hub/internal/server/apply_phase_advance.go` (new).
- Calls into existing phase-set code (`handlers_projects.go`
  `setProjectPhase` or equivalent — same internal-callable
  variant pattern).
- Validates that `from_phase` matches current; advances to
  `to_phase`; records `audit_events.action="project.phase_set"`
  with `meta.via="propose"`.
- DryRun: returns `{from_phase, to_phase, project_id, project_title}`.
- Tests:
  - Happy path.
  - Stale `from_phase` (project already advanced) → reject at
    apply with descriptive error.
  - dry_run + reject paths.

**W7. Apply function — `task.set_status` (~60 LOC + 40 LOC tests).**

- `hub/internal/server/apply_task_set_status.go` (new).
- Extends ADR-029's existing `setTaskStatus` with a
  `via="propose"` audit annotation; transitions only to
  `done` or `cancelled` (auto-derive handles `in_progress` /
  `blocked` per ADR-029 D-3).
- DryRun: returns `{task_id, task_title, from_status, to_status}`.
- Tests:
  - Happy path → done with `result_summary`.
  - cancelled path.
  - Override-by-principal rolls back to prior status (W9
    covers).

**W8. Deprecated propose-aliases for `agent.spawn` and `template.install` (~80 LOC + 60 LOC tests).**

- Register `propose(kind="agent.spawn", ...)` →
  `apply_agent_spawn.go`: wraps existing `DoSpawn`.
- Register `propose(kind="template.install", ...)` →
  `apply_template_install.go`: wraps existing
  `installProposedTemplate`.
- The decide handler at `handlers_attention.go:378-414` keeps
  the existing `approval_request + spawnIn` and
  `template_proposal` branches but is refactored to call the
  apply functions through the dispatcher; the kind-specific
  payload extraction stays in place so old MCP calls still
  resolve.
- Tests:
  - Old-shape spawn call via `approval_request + spawnIn` →
    dispatcher routes to `agent.spawn` apply function, audit
    records `via="alias_legacy"`.
  - New-shape via `propose(kind="agent.spawn")` → dispatcher
    routes to same apply function, audit records
    `via="propose"`.
  - Likewise for template install.

**W9. Principal override of lower-tier decisions (~120 LOC + 80 LOC tests).**

- `handlers_attention.go` decide handler detects override:
  - Row already resolved.
  - Override caller is principal tier.
  - Policy has `override_allowed: true` for the change_kind.
- Append new entry to `decisions_json`; mark
  `decisions_json` schema as supporting "override" entry kind.
- Emit `audit_events.action="attention.override"` row per
  ADR-030 D-8 shape.
- Per-kind rollback semantics:
  - `agent.spawn`: emit follow-up `agent.terminate` governed
    action; do NOT auto-execute the terminate (it's a separate
    governed action) — instead, write an attention row with
    `kind="propose", change_kind="agent.terminate"` and address
    it to the project steward; principal can self-approve in a
    follow-up tap. (MVP: terminate is post-MVP propose kind,
    so for now: emit a manual TODO audit row pointing the
    principal at the terminate REST.)
  - `template.install`: call rollback function that deletes
    the installed file and (if a prior version existed in the
    blob store) restores it.
  - `deliverable.set_state` / `phase.advance` /
    `task.set_status`: revert via the apply function with
    the prior `state` / `phase` / `status` as
    `change_spec`; audit row records the revert.
- Tests:
  - Override after steward-approve task.set_status → status
    reverts, audit records both rows.
  - Override after principal-approve template.install →
    file removed.
  - Override on a kind with `override_allowed: false` →
    400 with descriptive error.

**W10. `worker_tool_call.escalate` — re-address `permission_prompt` rows raised by steward-parented workers (~90 LOC + 70 LOC tests).**

- `hub/internal/server/mcp_more.go` `mcpPermissionPrompt` <!-- verify symbol hub/internal/server/mcp_more.go mcpPermissionPrompt -->
  (~line 687, function declared at `mcp_more.go:687` as of v1.0.636):
  after creating the attention row, check
  `agents.parent_agent_id` of the requesting agent.
- **Strict same-project parent-steward predicate** (per
  2026-05-20 pre-W1 decision #3). All three clauses must hold or
  the row stays team-wide-addressed:
  ```
  worker.parent_agent_id IS NOT NULL
  AND parent_agent.kind LIKE 'steward.%'        (kind-based — v1.0.607)
  AND parent_agent.project_id = worker.project_id
  ```
  The third clause (`project_id` match) avoids a v1.0.605-class
  bug where the parent-id pointer survives but the project binding
  has drifted. When the strict predicate holds, stamp the row with
  `assigned_tier = "project-steward"` and `current_assignees_json
  = [<parent_steward_id>]`. Otherwise leave the row team-wide-
  addressed as today.
- `dispatchAttentionReply` is unchanged — the existing fan-back
  already addresses by `session_id`; the new addressing only
  affects which inbox surfaces the row first.
- Mobile: see W12.
- Tests:
  - Worker with same-project steward parent: row addressed to
    parent.
  - Worker with cross-project steward parent (binding drift):
    row stays team-wide-addressed (third clause fails).
  - Worker with non-steward parent: row stays team-wide-addressed
    (second clause fails).
  - Worker without parent (orphan): row stays team-wide-addressed
    (first clause fails).
  - Steward decides → fan-back to engine driver (codex / claude)
    works as before.
  - Principal override after steward-approve: emits override
    audit; for codex parked-RPC, the driver's existing
    `attention_reply` handler re-runs (this is the one case
    where override is complex — needs a verification test).

**W11. `dispatchAttentionReply` allowlist + fan-back payload + ADR-032 envelope (~40 LOC + 30 LOC tests).**

- `handlers_attention.go:442` allowlist gains `propose` so
  the requester's session receives `input.attention_reply` on
  decide.
- Fan-back payload shape:
  `{request_id, kind:"propose", change_kind, decision, reason?,
  executed?}` — `executed` populated on approve so the agent
  knows the system applied the change.
- **ADR-032 envelope composition.** Per the 2026-05-20 ADR-030
  amendment, the hub-side `dispatchAttentionReply` site populates
  the ADR-032 envelope fields `{from, to, kind, text, cause,
  thread}` alongside the propose-specific payload so downstream
  lineage queries (directive trace) resolve a propose-decision edge
  uniformly with other directed-input edges. `from` =
  authoriser-handle, `to` = requester-handle, `kind` =
  `"attention_reply"`, `cause` = the source attention row's `cause`
  column passed through, `thread` = the requester's session id.
- Tests:
  - Approve → fan-back with `decision:"approve", executed:{…}` +
    envelope.
  - Reject → fan-back with `decision:"reject", reason` + envelope.
  - Dry-run preview is NOT fanned back (preview is part of
    the awaiting_response payload, not the fan-back).
  - Envelope `cause` round-trip: source row's `cause` ↦ fan-back
    envelope's `cause` (preserves lineage through the propose hop).

**W11.5. Loop-closure signal — sweep emits audit row on `escalation_state` transition (~30 LOC + 30 LOC tests).**

> Lands the 2026-05-20 D-7 Option 2′ amendment on the hub.
> **Per pre-W1 decision #5: audit-row-only — no separate push
> infra in MVP** (repo has no FCM/APNS/firebase). Mobile visibility
> = Activity-tab `agent_events` stream consumes the audit row +
> Me-page query widening (W19.6) surfaces the source row on next
> foreground/pull.

- `hub/internal/server/loop_sweep.go` already advances
  `attention_items.escalation_state` (`loop_sweep.go:212` <!-- verify symbol hub/internal/server/loop_sweep.go escalation_state -->).
  On every transition (`none → escalated_steward`, `escalated_steward
  → escalated_principal`), the sweep additionally calls
  `s.recordAudit` with:
  - `action = "attention.escalation_advanced"`
  - `meta = {attention_id, change_kind, from_state, to_state,
     original_assigned_tier, project_id, change_spec_preview}`.
  The preview is the same truncated 200-char summary the mobile
  cards render, computed once at audit-emit time so the Activity
  feed has enough context to navigate without a follow-up fetch.
- Dedup: the `escalation_state` column itself is the dedup key.
  The sweep emits an audit row only when `state != prev_state` on
  the UPDATE, so no re-emission across ticks for the same value.
- **No push infrastructure built in MVP.** No FCM/APNS adapter,
  no device-token table, no notifications service. The principal
  learns about escalation on next foreground/pull. Real-time push
  is a clean post-MVP add and is out of scope here.
- The 24h re-push backoff from earlier drafts is **dropped from
  MVP** — without a real-time push channel, repeated re-pushes
  are not a concern.
- Tests:
  - One sweep tick that flips a row to `escalated_steward` emits
    exactly one audit row.
  - Two sweep ticks across the same row (same state) emit one
    transition, not two — verified by an audit-row count.
  - `meta.change_spec_preview` matches the mobile card's preview
    text for the same row (string equality, not just shape).

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

**W12. Re-propose rule in 9 bundled steward templates (~30 LOC prose × 9 files).**

- Each of the nine bundled steward templates gains a short
  section under BOUNDARIES / Authority. The full set as of
  v1.0.673 (verified against `hub/templates/prompts/` <!-- verify glob hub/templates/prompts/steward.*.md 9 -->):
  `steward.v1.md`, `steward.general.v1.md`,
  `steward.claude-m4.v1.md`, `steward.codex.v1.md`,
  `steward.gemini.v1.md`, `steward.kimi.v1.md`,
  `steward.research.v1.md`, `steward.infra.v1.md`,
  `steward.antigravity.v1.md` (added at v1.0.641 by ADR-035).

  > **Governed actions are gated.** For load-bearing state
  > changes — deliverable state transitions, project-phase
  > advances, task close-out, agent spawn, template install —
  > use the `propose(kind, target_ref, change_spec, reason)`
  > verb. The system applies the change on approve; do not
  > attempt the mutation directly via REST or by editing files
  > yourself.
  >
  > **If a propose is rejected, do not immediately re-propose
  > to a higher tier.** Re-examine the reason in the
  > fan-back. Only re-propose if you have new information
  > that addresses the rejection.
  >
  > **`dry_run: true`** lets you preview the diff before the
  > authoriser sees it. Use it when you're uncertain whether
  > the change_spec is well-formed.

**W13. Test scenarios in test-steward-lifecycle.md (~200 LOC prose).**

- Ten new scenarios appended to
  `docs/how-to/test-steward-lifecycle.md`, numbered from 33
  (current highest is S32, ADR-034 stuck-task recovery —
  verified against v1.0.636):
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

**W14. Seed-demo annotation (~10 LOC prose comment).**

- Add a comment block in
  `hub/internal/server/seed_demo_lifecycle.go` near the
  `INSERT INTO attention_items` call (~line 1454):
  ```go
  // NOTE: seeded lifecycle attentions are UI-only.
  // They carry no session_id, so dispatchAttentionReply
  // short-circuits at handlers_attention.go:661. Tap-approve
  // demonstrates the Me-tab queue surface; no system-side
  // state change fires. See:
  // docs/discussions/governed-actions-and-propose-verb.md §9.
  ```
- Per the principal's instruction: **do not modify the seed
  itself**.

### 3.3 Acceptance

- All five steward templates carry the re-propose rule and
  the `propose` verb description.
- `docs/how-to/test-steward-lifecycle.md` lists scenarios 33-40
  with reproducible steps.
- Seed-demo comment is in place; no behavioural change.

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

**W15. Per-kind propose card — `deliverable.set_state` (~60 LOC).**

- `lib/screens/me/widgets/propose_card_deliverable.dart` (new).
- Renders: title, requester avatar + handle, reason text,
  target deliverable title + current state → proposed state
  arrow, "View deliverable" link → existing deliverable
  viewer with annotations open.
- Approve/Reject buttons → existing decide endpoint with
  `change_kind` in body.
- `dry_run` payload (when present) renders inline diff above
  the buttons.
- **Stalled variant (Option 2′).** When the viewer's tier ≠
  `assignedTier` and `escalationState` puts the row on their
  surface: a top pill (`⏱ Stuck Nh — addressed to @<addressee>`,
  duration from `escalated_at` or computed from the latest
  `attention.escalation_advanced` audit row); buttons flip to
  `Override` (opens the D-8 confirmation sheet from W20) and
  `View source` (navigate to the source deliverable). Body
  rendering is unchanged. A single predicate function decides
  which variant to render: `isAddressee = item.assignedTier ==
  myTier`.

**W16. Per-kind propose card — `phase.advance` (~50 LOC).**

- `lib/screens/me/widgets/propose_card_phase.dart` (new).
- Renders: project title + phase ribbon highlighting the
  transition, reason text, "View project" link.
- **Stalled variant** as per W15 (top pill, `Override` /
  `View source` buttons when viewer ≠ addressee).

**W17. Per-kind propose card — `task.set_status` (~60 LOC).**

- `lib/screens/me/widgets/propose_card_task.dart` (new).
- Renders: task title + body preview, current status → proposed
  status, result_summary text, "View task" link → task detail
  screen.
- **Stalled variant** as per W15.

**W18. Per-kind card — `worker_tool_call.escalate` (~40 LOC).**

- `lib/screens/me/widgets/propose_card_worker_tool.dart` (new).
- Renders: worker handle + project, tool name + input preview
  (truncated 200 chars), parent steward avatar, "View
  worker session" link.
- Note: this card is also shown to the project steward in its
  own session inbox (not just Me-tab); separate placement is
  W19.
- **Stalled variant** as per W15 — particularly relevant for
  this kind, since it is the path where a worker's blocked tool
  call sits while the project steward is unresponsive; the
  principal sees the stalled card and overrides if appropriate.

**W19. Steward-side propose inbox (~60 LOC).**

- Project-steward sessions get a "Pending decisions" pill
  surface (analogous to attention badge) showing rows where
  `assigned_tier == "project-steward"` and the steward is the
  addressee.
- Tap → list view → per-kind card from W15-W18.
- Decide → existing decide endpoint; fan-back delivers to the
  proposing worker's session as before.

**W19.5. `propose` kind → Requests filter mapping + Me-page query widen (~40 LOC + 30 LOC tests).**

> Lands the Option 2′ IA fit on the mobile side. Pairs with the
> hub-side widening described under W19.6 below — both halves are
> needed before stalled rows surface to the principal's Me-page.

- `lib/screens/me/me_screen.dart:349` — add `case 'propose': return
  _Filter.approvals;` to `_filterForAttention`. Single-line
  addition; verifies via the existing
  `_filterForAttention_unit_test.dart` pattern (extend the table
  test).
- `lib/services/hub/hub_client.dart` `listAttention(...)` — pass
  through the new `include_escalated: true` query parameter when
  fetching for the Me-page so the hub widens the result set per
  W19.6.
- `lib/providers/attention_provider.dart` (or equivalent) — add an
  `_isAddressee(AttentionItem item, String myTier)` predicate:
  `item.assignedTier == myTier`. Used by W15-W18 card builders to
  pick between primary and stalled variants.

**W19.6. Hub-side Me-page query widen + top-of-Me digest card (~80 LOC + 50 LOC tests).**

- **Hub side.** `hub/internal/server/handlers_attention.go`
  `handleListAttention` (or the equivalent Me-page query) gains an
  optional `include_escalated` query parameter. When true, the
  WHERE clause widens from
  `WHERE assigned_tier = caller_tier`
  to
  `WHERE (assigned_tier = caller_tier
         OR escalation_state = 'escalated_' || caller_tier)
     AND status = 'open'`.
  Default `include_escalated=false` preserves existing API
  callers; the mobile Me-page sends `true`. Tests: caller-tier
  rows return as today; tier-below rows with
  `escalation_state='escalated_<caller>'` appear iff
  `include_escalated=true`; orphan rows (no tier match either
  way) stay invisible.
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

**W20. Override affordance (~40 LOC).**

- Resolved rows (status=`resolved`) gain a menu (… overflow)
  with "Override decision" when policy has
  `override_allowed: true` and the current user tier is higher
  than the resolver's tier.
- Tap → confirmation sheet with reason field (required) →
  POST decide with `override=true` flag.
- Display the override visibly in the row's history: original
  decision + override decision + override reason, color-coded
  to distinguish.

**W21. Read-only policy viewer (~30 LOC).**

- Settings > Advanced > Governed action policy: renders the
  `kinds:` block from `<dataRoot>/team/policy.yaml` (per pre-W1
  decision #1 — same file as the legacy `tiers/approvers/quorum`)
  as a table (kind / default tier / commits / override allowed).
  No editor in MVP — file is team-scoped and authored by-hand for
  now. The legacy `tiers:` block is not rendered here; the
  attention-policy surface elsewhere already covers it.

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
