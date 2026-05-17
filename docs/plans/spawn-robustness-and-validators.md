---
name: Spawn robustness and structural validators (v1.0.620)
description: Bundled bug-fix + defense-in-depth release closing the coder.v1 spawn incident and the seven HIGH-severity MCP-tool description gaps surfaced by the audit. Ten wedges across hub + hostrunner + prompts + CI. Implements the four-layer validation strategy from validate-at-every-boundary.md (typed handler decode, startup-time bundled audit, CI lint, description ↔ schema audit) for the 7 HIGH-severity free-form fields. Single ship.
---

# Spawn robustness and structural validators (v1.0.620)

> **Type:** plan
> **Status:** Proposed (2026-05-17) — single phase, ten wedges, no work started. Companion discussion at [`validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md); incident summary in §1 of that doc.
> **Audience:** contributors · principal · QA
> **Last verified vs code:** v1.0.619-alpha

**TL;DR.** Close the cascading spawn failure surfaced on 2026-05-17
(steward sent `spawn_spec_yaml: "template: coder.v1"` → empty
`backend.cmd` → bash placeholder → keystroke-pumped task prompt →
respawn loop until manual termination) and the seven HIGH-severity
MCP description gaps the audit found around the same shape
(under-documented free-form fields with no runtime validator).
Ten wedges, single ship as v1.0.620. Five fix the spawn failure
chain (template merge, dedup, fail-fast, hostrunner refusal,
launcher hardening); two improve the steward contract
(description rewrites, sync-wait three-state return); one fixes
the `task.notify` stuck-steward symptom from the same incident;
one adds the cross-cutting four-layer validator strategy (typed
decode + startup audit + CI lint) for all seven HIGH-severity
fields; one is the implementation-order optimisation. Approx
~840 LOC + ~580 lines prose.

---

## 0. Phase order, summarised

Single phase. Wedges by intended implementation order (earlier
wedges enable verification of later ones; W6 is last because the
descriptions describe behaviour set by earlier wedges):

| # | Wedge | Approx LOC | Depends on |
|---|---|---|---|
| 3 | `launchOne` dedup | ~10 | — |
| 4 | Hub `DoSpawn` fail-fast | ~30 | — |
| 1 | `renderSpawnSpec` template merge | ~80 | — |
| 7 | Hostrunner refuse-to-launch + status='failed' | ~50 | W1, W4 |
| 8 | Harden launcher default placeholder | ~10 | W7 |
| 9 | `agents.spawn` sync-wait three-state return | ~80 | W4 |
| 5 | `task.notify` triggers steward turn | ~50 + prompt | — |
| 10 | Typed validators + startup audit + CI lint | ~500 | W1 |
| 6 | Rewrite 7 HIGH-severity MCP descriptions | ~500 prose | W1, W9, W10 |
| 2 | Hostrunner template-index key cleanup | ~30 | — (cleanup; defused by W1) |

Implementation order is **3 → 4 → 1 → 7 → 8 → 9 → 5 → 10 → 6 → 2**.
Rationale: W3 is an isolated guard worth landing first (prevents
respawn loop even if other wedges regress); W4 catches malformed
specs at the verb boundary before W1 needs to handle them; W1
unblocks W7 (hostrunner can trust spec is well-formed); W6 lands
last so descriptions can reflect actual W1/W9/W10 behaviour.

Release: tag as **v1.0.620-alpha** after all 10 wedges land
green. No flag-gating — this is bug-fix + defense-in-depth, ships
on by default.

---

## 1. The wedges

### W3 — `launchOne` dedup against already-launched drivers

**Goal:** Prevent the respawn loop. Even if other layers regress,
this guard ensures a given `agent_id` cannot have two
simultaneous tmux panes.

**Cost:** ~10 LOC + 30 LOC tests.

**Files:**

- `hub/internal/hostrunner/runner.go` `launchOne` (`:452`):
  Add at the top, after the spec parse:
  ```go
  if _, ok := a.drivers[sp.ChildID]; ok {
      a.Log.Debug("launchOne skip: driver already registered",
          "agent", sp.ChildID, "handle", sp.Handle)
      return
  }
  ```
- `hub/internal/hostrunner/runner_test.go` (existing or new): test
  that two `launchOne(sp)` calls for the same ChildID result in
  only one driver registration.

**Acceptance:**

- Calling `launchOne` twice for the same agent ID creates exactly
  one tmux pane.
- Manual stop → tickPoll sees stale pending spawn → `launchOne`
  fires → returns immediately → no new pane.

---

### W4 — Hub `DoSpawn` fail-fast on empty rendered `backend.cmd`

**Goal:** Reject malformed spawn requests at the verb boundary with
HTTP 422 + structured error before any work hits the hostrunner.

**Cost:** ~30 LOC + 40 LOC tests.

**Files:**

- `hub/internal/server/handlers_agents.go` `DoSpawn` (`:822`):
  After `renderSpawnSpec` returns the rendered spec, decode the
  YAML into a minimal struct, check `backend.cmd != ""`. If empty:
  ```go
  return spawnOut{}, http.StatusUnprocessableEntity,
      fmt.Errorf("rendered spawn_spec_yaml has no backend.cmd; " +
          "spec must declare `backend.cmd` directly or reference a " +
          "template with backend.cmd (e.g. `template: coder.v1`)")
  ```
- Tests:
  - Empty `spawn_spec_yaml` → 422.
  - `spawn_spec_yaml: "kind: claude-code"` (no backend block) → 422.
  - `spawn_spec_yaml: "template: coder.v1"` BEFORE W1 → 422 (current
    bug surface — locked test ensures it doesn't silently regress).
  - `spawn_spec_yaml: "template: coder.v1"` AFTER W1 → 200 (template
    merge populates backend.cmd).

**Acceptance:**

- `agents.spawn` with no resolvable `backend.cmd` returns 422 with
  the structured message; agent row is NOT inserted.
- Existing fully-specified spawns continue to succeed.

---

### W1 — `renderSpawnSpec` template merge

**Goal:** Make `template: <name>` work as the steward (and the audit
report from the post-incident discussion) expects: load the named
template, merge its `backend.{cmd,kind,model,permission_modes}`,
its `prompt:` file reference, and any other fields the steward
hasn't overridden.

**Cost:** ~80 LOC + 80 LOC tests.

**Files:**

- `hub/internal/server/template.go`:
  - New function `mergeTemplateReference(spec string) (merged string, err error)`:
    1. Decode `spec` into `struct { Template string \`yaml:"template"\` }`.
    2. If `Template == ""`, return spec unchanged.
    3. Read `hub/templates/agents/<Template>.yaml` from embedded FS;
       check `team/templates/agents/<Template>.yaml` first for user
       override.
    4. Decode both into `map[string]any`. Merge: spec values override
       template values for top-level scalars; nested maps (`backend:`,
       `worktree:`) merge recursively.
    5. Re-encode and return.
  - `renderSpawnSpec` (`:164`) calls `mergeTemplateReference` first,
    then runs `expandVars` on the merged YAML.
- Tests:
  - `spawn_spec_yaml: "template: coder.v1"` → merged spec has
    `backend.cmd: "claude --model {{model}} ..."` and the steward's
    explicit fields (none in this case).
  - `spawn_spec_yaml: "template: coder.v1\nworktree:\n  branch: feat-x"` →
    merged spec has both `backend.cmd` from template and
    `worktree.branch: feat-x` from spec.
  - `spawn_spec_yaml: "template: coder.v1\nbackend:\n  model: claude-opus-4-6"` →
    merged spec has template's `backend.cmd` AND spec's `backend.model`
    override (deep merge of `backend:` map).
  - Missing template name → 422 with `"template '<name>' not found"`.
  - User override at `team/templates/agents/coder.v1.yaml` → loaded
    in preference to bundled.

**Acceptance:**

- The exact MCP call from the incident
  (`spawn_spec_yaml: "template: coder.v1"`) succeeds: agent is
  spawned, claude-code engine starts, no bash placeholder, no
  respawn loop.

---

### W7 — Hostrunner refuse-to-launch + `status='failed'`

**Goal:** Defense in depth — if a malformed spec somehow reaches
the hostrunner past W4, refuse to launch and mark the agent
failed with a structured reason. Belt-and-braces with W4.

**Cost:** ~50 LOC + 50 LOC tests.

**Files:**

- `hub/internal/hostrunner/runner.go` `launchOne` (`:579-636`):
  In the M4 fallback path, when both `spec.Backend.Cmd` and
  `templates.BackendCmd(sp.Kind)` are empty, instead of falling
  through to `a.Launcher.Launch(ctx, sp)`:
  ```go
  status := "failed"
  reason := "no backend.cmd resolved from spawn spec or template"
  a.Log.Error("launch refused", "handle", sp.Handle, "reason", reason)
  _ = a.Client.PatchAgent(ctx, sp.ChildID, AgentPatch{
      Status:        &status,
      FailureReason: &reason,
  })
  return
  ```
- Add `FailureReason` field to `AgentPatch` struct if not present;
  add `failure_reason` column to agents table if not present (check
  existing schema before assuming).
- `launch_m4_locallogtail.go`: same refusal pattern on backend.cmd
  empty — already returns error; ensure caller writes `status=failed`
  with reason (currently only logs the error per `runner.go:639-641`).

**Acceptance:**

- A spawn that reaches hostrunner with no `backend.cmd` (regression
  in W4, or direct REST bypass) → agent row marked
  `status='failed'`, `failure_reason='no backend.cmd resolved ...'`,
  NO tmux pane is created.
- Failed agents surface in `agents.get` with the reason populated.

---

### W8 — Harden launcher default placeholder

**Goal:** Make the launcher default safe even if reached by an
unanticipated code path. Currently runs an interactive bash; this
wedge makes it terminate immediately with a clear error message.

**Cost:** ~10 LOC.

**Files:**

- `hub/internal/hostrunner/tmux_launcher.go` (`:27-35`): Replace the
  default command:
  ```go
  if defaultCmd == "" {
      defaultCmd = `bash -c 'echo "[host-runner] FATAL: launcher reached without backend.cmd. This is a bug; refusing to start interactive shell. See logs."; exit 1'`
  }
  ```

**Acceptance:**

- If the default placeholder is reached (which shouldn't happen
  post-W7), the pane exits immediately with code 1; tmux reports
  the pane dead; no interactive bash session for PaneDriver to
  keystroke into.
- Hub-side reconciler observes the dead pane and patches
  `status='failed'`.

---

### W9 — `agents.spawn` sync-wait three-state return

**Goal:** Return accurate state from the spawn MCP call. Today the
response is misleadingly labelled `"spawned"`; this wedge returns
one of `{running, failed, pending}` based on real engine state,
bounded by a configurable `wait_seconds` (default 30, hard-capped
at 50s to stay under Claude Code's 60s `MCP_TOOL_TIMEOUT`).

**Cost:** ~80 LOC + 80 LOC tests.

**Files:**

- `hub/internal/hubmcpserver/tools.go` `agents.spawn` schema
  (`:411`): add `wait_seconds` (integer, optional, default 30,
  cap 50) and `wait` (boolean, optional, default true).
- `hub/internal/server/handlers_agents.go` `handleSpawn` (`:745`):
  - If `wait == false`: return immediately with `Status: "pending"`.
  - Else: after `DoSpawn` succeeds, subscribe to the agent's
    `agent_events` bus and tail for `lifecycle.started` (→ running)
    or `lifecycle.failed` (→ failed), bounded by `wait_seconds`.
  - On timeout: return `Status: "pending"` with the spawn_id so
    the steward can poll `agents.get` or (future) `agents.wait_ready`.
- `spawnOut` struct (`:728`): add `FailureReason` field (populated
  only when `Status == "failed"`).
- Tests:
  - Happy path: spawn succeeds → engine starts within wait window
    → `{Status: "running", AgentID, SpawnID}`.
  - Failure path: spawn rejected by hostrunner (W7) → engine never
    starts → `{Status: "failed", AgentID, FailureReason: "..."}`.
  - Timeout path: spawn accepted but engine cold-starts past
    wait_seconds → `{Status: "pending", AgentID, SpawnID}`.
  - `wait: false` opt-out: returns `pending` immediately.
  - `wait_seconds > 50` → silently capped at 50.

**Acceptance:**

- Steward calls `agents.spawn` and gets back `running`, `failed`, or
  `pending` — never the legacy misleading `"spawned"`.
- Total MCP call duration stays under 50s in all cases.

---

### W5 — `task.notify` triggers a steward turn

**Goal:** When a worker completes its task and the hub emits
`task.notify` to the assigner's session, the steward (assigner)
should pick up an `input.text` summary turn so it can continue
work — not just see a render-only card while its compose box
stays mysteriously busy from a stuck prior turn.

**Cost:** ~50 LOC + steward prompt addition.

**Files:**

- `hub/internal/server/task_notify.go` (`:28-107`): in addition to
  the existing `task.notify` agent_event insert, also insert an
  `input.text` agent_event (kind `input.text`, producer `system`)
  with body:
  ```
  Task <title> completed by <worker_handle>. Result: <result_summary>. Decide next step.
  ```
  This passes the `input_router.go:185-189` filter (`producer in
  {user, a2a}` AND `kind starts with input.`)? **No, system
  producer is filtered out.** Two options:
  - **Option A (simplest):** Insert with `producer = 'user'` so
    InputRouter dispatches it. Steward sees it as an external input.
    Slight semantic abuse — it's not actually a user turn — but
    works without changing the router.
  - **Option B (cleaner):** Extend `input_router.go` allowlist to
    include `producer == 'system' AND kind == 'input.task_completed'`,
    use a new kind so the router can filter selectively.

  **Recommendation: Option B.** Cleaner separation; future
  system-driven input kinds (e.g. ADR-030's `propose` fan-back) will
  benefit. ~20 LOC in input_router.
- `hub/templates/prompts/steward.v1.md` + 4 domain variants: add
  one paragraph under "Receiving worker notifications":
  > When you receive a `Task ... completed by ...` input, that's
  > the hub telling you a worker finished. Use `tasks.get` to
  > inspect the result and `request_help` to ask the principal
  > what's next, or proceed autonomously per the project plan.

**Acceptance:**

- Worker calls `tasks.complete(...)` → hub emits `task.notify` AND
  `input.task_completed` → steward's engine starts a turn on next
  pickup → compose box clears.

---

### W10 — Typed validators + startup audit + CI lint

**Goal:** Cross-cutting defense for the 7 HIGH-severity free-form
fields the audit identified. Three sub-deliverables:

**Cost:** ~500 LOC code + ~80 LOC tests + ~50 LOC shell.

**W10a — Typed Go struct validators (~350 LOC).**

For each of the 7 HIGH-severity fields, add a typed-decode +
validation function in `hub/internal/validators/` (new package):

- `validate_spawn_spec.go` — decodes spawn_spec YAML; requires
  `backend.cmd != ""`.
- `validate_plan_step_spec.go` — decodes `spec_json` per `kind`
  (agent_spawn / llm_call / shell / mcp_call / human_decision);
  each kind has its own required-field set.
- `validate_project_config.go` — decodes `config_yaml`; requires
  `phases:` array with at least one phase.
- `validate_document_body.go` — checks `body != ""` (markdown body).
- `validate_channel_event_parts.go` — checks `parts` is non-empty
  array; each element has `kind` + matching `text`/`code`/`uri`.
- `validate_artifact_lineage.go` — decodes `lineage_json`; allows
  empty (lineage is optional in MVP) but rejects malformed shapes.
- `validate_policy_overrides.go` — decodes `policy_overrides_json`
  against the policy-engine schema.

Each validator returns `(ok bool, err error)` with a structured
error message naming the missing/malformed field. Called from the
respective handler before any DB write.

**W10b — Startup-time bundled-template audit (~80 LOC).**

In `hub/cmd/hub-server/main.go` (or wherever startup wiring lives):
after loading templates, loop every bundled template at
`hub/templates/agents/*.yaml`, render with synthetic sample vars,
run `validateSpawnSpec` on the result. If any template fails,
log the error and **refuse to start** with a clear message naming
the broken template + the validation failure.

A `--skip-template-audit` flag is provided as an escape hatch for
emergency operator overrides; usage is logged as a WARN.

**W10c — CI lint `lint-templates.sh` (~50 LOC shell).**

`scripts/lint-templates.sh`: builds the hub-server binary, runs
it with `--audit-only` (new flag added in W10b), captures the
output, fails the script if any template fails validation.

Wired into `scripts/lint-all.sh` (if exists) or run directly in
the GitHub Actions workflow alongside `lint-docs.sh`.

**Tests:**

- Per validator: positive case (well-formed payload passes),
  negative cases (each required field missing fails with
  named-field error).
- Startup audit: introduce a deliberately broken
  `hub/templates/agents/broken-test.yaml` fixture; assert that
  hub-server refuses to start.
- CI lint: same fixture; assert that `lint-templates.sh` exits
  non-zero.

**Acceptance:**

- All 7 HIGH-severity tools reject malformed payloads at the
  verb boundary with structured errors that name the bad field.
- Hub-server refuses to start with a broken bundled template;
  error names the file and the validation failure.
- `lint-templates.sh` runs in CI and fails the PR if a template
  is broken.

---

### W6 — Rewrite 7 HIGH-severity MCP descriptions

**Goal:** Close the documentation gap that contributed to the
incident. Every HIGH-severity tool gets (a) field shape, (b) a
minimal worked example, (c) explicit warning about silent failure
modes.

**Cost:** ~500 lines prose.

**Files:**

- `hub/internal/hubmcpserver/tools.go` — 7 description strings:

  | Tool | Field needing documentation |
  |---|---|
  | `agents.spawn` | `spawn_spec_yaml`: shape + minimal example + `template: ...` shorthand semantics + W9 sync-wait behaviour |
  | `plans.steps.create` | `spec_json`: per-kind schema table + worked example for each kind |
  | `projects.create` | `config_yaml`: shape + minimal example with at least one phase |
  | `documents.create` | `body`: markdown dialect + warning that empty body is invalid post-W10 |
  | `channels.post_event` | `parts`: array element shape (`{kind, text}` etc.) + example |
  | `runs.attach_artifact` + `artifacts.create` | `lineage_json`: shape (`{upstream_run_ids, upstream_artifact_ids, parameters}`) + when to use |
  | `projects.update` | `policy_overrides_json`: pointer to `policy.read` for shape + example |

Each description ends with: *"Validators reject malformed
payloads at the verb boundary; expect HTTP 422 with a structured
error naming the missing field. See
`docs/discussions/validate-at-every-boundary.md` for the
underlying principle."*

**Acceptance:**

- A reader unfamiliar with the system can fill in each HIGH-severity
  field correctly from the description alone, without reading
  source.
- The descriptions reflect the actual W1/W9/W10 behaviour, not
  pre-bundle semantics.

---

### W2 — Hostrunner template-index key cleanup

**Goal:** Fix the secondary bug surfaced during diagnosis — the
hostrunner's in-memory template index is keyed by template name
(`coder.v1`) but the runtime lookup uses engine kind (`claude-code`).
The fallback at `runner.go:618` always returns "" for this reason.
Defused by W1 (which removes the dependence on the fallback), but
worth cleaning so the indirection isn't a future foot-gun.

**Cost:** ~30 LOC.

**Files:**

- `hub/internal/hostrunner/templates.go`: rename the misleading
  `byKind` field to `byTemplateName`. Add explicit comment that
  this is NOT keyed by engine kind.
- `hub/internal/hostrunner/runner.go:618`: either remove the
  fallback entirely (now dead code post-W1) or fix it to look up
  by template name if the spec carries one. Recommendation:
  **remove**, since W1 + W4 + W7 ensure `backend.cmd` is always
  populated by the time we reach this branch.

**Acceptance:**

- No remaining `byKind` references outside engine-kind contexts.
- Dead `templates.BackendCmd(sp.Kind)` fallback removed; nothing
  in the test suite depends on it.

---

## 2. Acceptance — end-to-end

After all 10 wedges land:

1. **The original incident does not reproduce.** Steward calls
   `agents.spawn` with `spawn_spec_yaml: "template: coder.v1"` →
   template merges → `backend.cmd` populated → claude-code engine
   launches → engine reaches `running` within W9's wait window →
   tool returns `{Status: "running", AgentID, SpawnID}`.

2. **The original incident's failure mode is *un*reachable.** Even
   if W1 regresses, the spec is rejected at W4 (hub fail-fast)
   with HTTP 422. If W4 regresses, the spec is rejected at W7
   (hostrunner refusal) with `status='failed'`. If W7 regresses,
   the launcher placeholder (W8) exits immediately with code 1.
   Every layer fail-fasts.

3. **Description-driven correctness.** A reader of the
   `agents.spawn` MCP description (W6) can construct a valid
   `spawn_spec_yaml` from the description alone, with a working
   minimal example to copy.

4. **Bundled templates are pre-validated.** Hub-server refuses to
   start if any bundled template is broken (W10b). CI fails the
   PR (W10c) before such a template can land.

5. **No respawn loops.** W3's dedup guard prevents the
   pending-spawn → re-launch loop independent of any other wedge.

6. **`task.notify` doesn't leave the steward stuck.** Worker calls
   `tasks.complete(...)` → steward gets an `input.task_completed`
   turn → compose box clears → steward continues work.

7. **Six other HIGH-severity tools are also hardened.** The same
   pattern applies to `plans.steps.create`, `projects.create`,
   `documents.create`, `channels.post_event`,
   `runs.attach_artifact`, `projects.update` — all reject
   malformed payloads at the verb boundary with structured
   errors.

---

## 3. Test scenarios

Append to `docs/how-to/test-steward-lifecycle.md`:

| # | Scenario | What it exercises |
|---|---|---|
| S33 | Steward spawns worker with `spawn_spec_yaml: "template: coder.v1"` | W1 template merge end-to-end (the original incident) |
| S34 | Steward spawns worker with empty `spawn_spec_yaml` | W4 fail-fast 422 |
| S35 | Steward spawns worker with `spawn_spec_yaml` missing `backend.cmd` and no `template:` | W4 fail-fast 422 |
| S36 | Manually stop a worker mid-launch; observe no new tmux windows appear | W3 dedup |
| S37 | Worker calls `tasks.complete(...)`; steward picks up and responds | W5 task.notify trigger |
| S38 | Steward spawns worker; engine takes 35s to cold-start | W9 sync-wait → `pending` return |
| S39 | Steward spawns worker; engine fails to start (corrupted binary) | W9 sync-wait → `failed` return with reason |
| S40 | Drop a deliberately broken `team/templates/agents/foo.v1.yaml` and restart hub | W10b startup audit refuses to start |
| S41 | Run `scripts/lint-templates.sh` in CI on a PR that adds a broken template | W10c CI lint fails |
| S42 | Read the rewritten `agents.spawn` description and copy the minimal example | W6 description completeness |

---

## 4. Open follow-ups (not in this plan)

- **Description ↔ schema audit (Layer 4 in `validate-at-every-boundary.md`).**
  Automated lint that parses every tool's `InputSchema` and checks
  every field named in `Description` is in the schema and vice
  versa. Deferred to a separate wedge; the audit is manual for
  v1.0.620.
- **MEDIUM-severity tool description gaps (6 tools).** `tasks.create`
  status default, `agents.spawn` project_id dual-source warning,
  `tasks.update` done-vs-cancelled coercion, `agents.list` status
  precedence, `schedules.create` conditional-required cron_expr,
  `agents.spawn` task_id+task mutex clarity. Tidy-pass wedge in
  v1.0.621.
- **LOW-severity gaps (5 tools).** policy.read stub note,
  a2a.invoke message_id collision, documents.create enum values,
  template scaffold engine default, tasks.list sort. Cosmetic;
  ship opportunistically.
- **MCP `_meta` per-call timeout override.** Not in MCP spec; not
  supported by Claude Code. If a future use case demands it, may
  require a custom progress-notification scheme. Deferred.
- **Forbidden-pattern entry.** Once v1.0.620 ships and the
  four-layer validation pattern is proven, add an entry to
  `spine/forbidden-patterns.md` codifying "free-form payload
  field accepted at a boundary without typed validation."

---

## 5. Status forward-links

- Discussion: [`validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md)
- Companion ADR (separate scope, not gated by this plan):
  [`030-governed-actions-and-propose-verb.md`](../decisions/030-governed-actions-and-propose-verb.md)
- Related references:
  [`permission-model.md`](../reference/permission-model.md),
  [`tool-call-approval-patterns.md`](../reference/tool-call-approval-patterns.md)
- Related ADRs:
  [ADR-025](../decisions/025-project-steward-accountability.md) (project-bound spawn = first kind-gated mutation),
  [ADR-027](../decisions/027-local-log-tail-driver.md) (M4 LocalLogTail — the one layer that fail-fasted in the incident),
  [ADR-029](../decisions/029-tasks-as-first-class-primitive.md) (worker delivery via task body — the prompt content that was keystroked into bash).
