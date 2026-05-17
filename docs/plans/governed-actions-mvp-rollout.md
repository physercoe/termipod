---
name: Governed actions MVP rollout
description: Wedge-by-wedge execution plan for ADR-030 — generic `propose` MCP verb, 4-tier authorisation ladder, per-(kind, tier) policy file, four MVP kinds (deliverable.set_state, phase.advance, task.set_status, worker_tool_call.escalate routing extension), principal-override audit, deprecated aliases for the two existing apply-on-approve paths. Single principal MVP with multi-member schema hooks. Two phases (hub then mobile); ~1300-1500 LOC code + ~750 lines prose; 11 wedges.
---

# Governed actions MVP rollout — phased

> **Type:** plan
> **Status:** Proposed (2026-05-17) — three phases, no work started; ADR-030 captures the locked decisions
> **Audience:** contributors
> **Last verified vs code:** v1.0.619-alpha

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
| 1 | Hub: schema migration, `propose` MCP verb, kind dispatcher, 3 apply functions, permission_prompt re-addressing, override audit, alias compat, policy file reader, tests | ~1000 | — |
| 2 | Prompts: re-propose rule in 5 bundled steward templates; deprecate `request_approval+spawnIn` and `template_proposal` patterns in prompt prose; new test-steward-lifecycle scenarios | ~200 prose | Phase 1 |
| 3 | Mobile: per-kind propose card (4 cards), override affordance, decided-row visual, `governed-actions.yaml` editor (read-only viewer for MVP) | ~280 | Phase 1 |

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

- New migration `004X_attention_items_governed_actions.up.sql`:
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

**W2. Policy file reader (~80 LOC).**

- `hub/internal/policy/governed_actions.go` — new file. Reads
  `<team_data_root>/policy/governed-actions.yaml` into a typed
  `GovernedActionPolicy` struct keyed by `kind`. Each entry:
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
- mtime-cached per-team; reload on file change. Default policy
  embedded when the file is absent (the 6 kinds from ADR-030 D-6
  hardcoded as `defaultGovernedActionPolicy` fallback).
- Tests: missing file → defaults; malformed YAML → typed error;
  unknown kind → permissive default with WARN log.

**W3. `lint-governed-actions.sh` (~50 LOC shell + 30 LOC tests).**

- `scripts/lint-governed-actions.sh` — walks the policy file
  and the dispatcher's registered kinds (from a new
  `propose_kinds.go` registry — see W4). Fails if a kind in
  the dispatcher has no policy entry, or vice versa.
- Wired into `scripts/lint-all.sh` if it exists, or runnable
  standalone.
- Catches drift between code and config (D-9 follow-up: same
  shape as `lint-glossary.sh`).

**W4. `propose` MCP verb + kind registry (~180 LOC).**

- `hub/internal/hubmcpserver/tools.go` — register the new MCP
  tool `propose` with JSON schema for the six fields. Audience:
  steward + worker (steward by default; worker only when
  `roles.yaml` allows the kind).
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

- `hub/internal/server/mcp_more.go` `mcpPermissionPrompt`
  (~line 1045): after creating the attention row, check
  `agents.parent_agent_id` of the requesting agent. If the
  parent is a steward — detected via `agent.kind == 'steward.v1'
  || strings.HasPrefix(agent.kind, 'steward.')` per the
  kind-based predicate established v1.0.607, NOT a handle-based
  check (handle predicates exclude `@steward.<pid8>`) — stamp
  the row with `assigned_tier = "project-steward"` and
  `current_assignees_json = [<parent_steward_id>]`.
- `dispatchAttentionReply` is unchanged — the existing fan-back
  already addresses by `session_id`; the new addressing only
  affects which inbox surfaces the row first.
- Mobile: see W12.
- Tests:
  - Worker with steward parent: row addressed to parent.
  - Worker without steward parent (orphan): row addressed
    team-wide as today.
  - Steward decides → fan-back to engine driver (codex / claude)
    works as before.
  - Principal override after steward-approve: emits override
    audit; for codex parked-RPC, the driver's existing
    `attention_reply` handler re-runs (this is the one case
    where override is complex — needs a verification test).

**W11. `dispatchAttentionReply` allowlist + fan-back payload (~30 LOC + 20 LOC tests).**

- `handlers_attention.go:436-444` allowlist gains `propose` so
  the requester's session receives `input.attention_reply` on
  decide.
- Fan-back payload shape:
  `{request_id, kind:"propose", change_kind, decision, reason?,
  executed?}` — `executed` populated on approve so the agent
  knows the system applied the change.
- Tests:
  - Approve → fan-back with `decision:"approve", executed:{…}`.
  - Reject → fan-back with `decision:"reject", reason`.
  - Dry-run preview is NOT fanned back (preview is part of
    the awaiting_response payload, not the fan-back).

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

**W12. Re-propose rule in 5 steward templates (~30 LOC prose × 5 files).**

- Each of `hub/templates/prompts/steward.v1.md`,
  `steward.research.v1.md`, `steward.codex.v1.md`,
  `steward.gemini.v1.md`, `steward.kimi.v1.md` gains a short
  section under BOUNDARIES / Authority:

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

**W13. Test scenarios in test-steward-lifecycle.md (~150 LOC prose).**

- Eight new scenarios appended to
  `docs/how-to/test-steward-lifecycle.md`, numbered from 33:
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

**W14. Seed-demo annotation (~10 LOC prose comment).**

- Add a comment block in
  `hub/internal/server/seed_demo_lifecycle.go` near the
  `INSERT INTO attention_items` call (~line 1444):
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

The Me-tab queue renders `propose` rows with per-kind cards
that surface enough context to decide. Resolved rows expose an
"Override" menu when the policy allows. The `governed-actions.yaml`
policy file is readable (not editable) under Settings.

### 4.2 Wedges

**W15. Per-kind propose card — `deliverable.set_state` (~50 LOC).**

- `lib/screens/me/widgets/propose_card_deliverable.dart` (new).
- Renders: title, requester avatar + handle, reason text,
  target deliverable title + current state → proposed state
  arrow, "View deliverable" link → existing deliverable
  viewer with annotations open.
- Approve/Reject buttons → existing decide endpoint with
  `change_kind` in body.
- `dry_run` payload (when present) renders inline diff above
  the buttons.

**W16. Per-kind propose card — `phase.advance` (~40 LOC).**

- `lib/screens/me/widgets/propose_card_phase.dart` (new).
- Renders: project title + phase ribbon highlighting the
  transition, reason text, "View project" link.

**W17. Per-kind propose card — `task.set_status` (~50 LOC).**

- `lib/screens/me/widgets/propose_card_task.dart` (new).
- Renders: task title + body preview, current status → proposed
  status, result_summary text, "View task" link → task detail
  screen.

**W18. Per-kind card — `worker_tool_call.escalate` (~30 LOC).**

- `lib/screens/me/widgets/propose_card_worker_tool.dart` (new).
- Renders: worker handle + project, tool name + input preview
  (truncated 200 chars), parent steward avatar, "View
  worker session" link.
- Note: this card is also shown to the project steward in its
  own session inbox (not just Me-tab); separate placement is
  W19.

**W19. Steward-side propose inbox (~60 LOC).**

- Project-steward sessions get a "Pending decisions" pill
  surface (analogous to attention badge) showing rows where
  `assigned_tier == "project-steward"` and the steward is the
  addressee.
- Tap → list view → per-kind card from W15-W18.
- Decide → existing decide endpoint; fan-back delivers to the
  proposing worker's session as before.

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

- Settings > Advanced > Governed action policy: renders
  `governed-actions.yaml` as a table (kind / default tier /
  commits / override allowed). No editor in MVP — file is
  team-scoped and authored by-hand for now.

### 4.3 Acceptance

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
  (role gates).
