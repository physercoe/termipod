# Loop discipline and dreaming

> **Type:** plan
> **Status:** Proposed (2026-07-30) — for review. Derived from
> [`discussions/loop-engineering-borrows.md`](../discussions/loop-engineering-borrows.md)
> (Tier A + B1/B4); companion to
> [ADR-034](../decisions/034-orchestration-loop-closure.md) (whose
> anti-*stall* half this completes with an anti-*busy-loop* half).
> W1 is the lane opener; W4 depends on the records entity
> ([`desktop-compare-wall-and-decisions.md`](desktop-compare-wall-and-decisions.md)
> Lane B) shipping first.
> **Audience:** principal · contributors
> **Last verified vs code:** 2026.727.938 (`hub/internal/server/
> apply_task_set_status.go`, `job_sweep.go`, `digest_issues.go`,
> `hub/migrations/`)

**TL;DR.** TermiPod's loop closes on *silence* but not on *motion
without outcome*, and nothing makes a turn prove progress before the
loop funds the next one. Four additive mechanisms fix this, all
modelled as roles over existing tables in ADR-034's style — no new
runtime, no LoopX dependency: **W1** evidence-gated terminal task
transitions with an ordered receipt contract (`result < validate <
writeback`, fail closed at the hub write path); **W2** a per-turn
delivery-outcome classification plus an outcome floor that stops
funding surface-only turns after N in a row on one task; **W3** a
`should_run` decision projected by the hub and consulted before every
automatic wake — deliver / ask / wait / repair / **stay quiet**, with
quiet turns free and silent; **W4** a dreaming lane: a scheduled
read-only session over run digests and digest-issue classes whose
only write verb is `record_propose` — self-evolution as governed
proposal, never inline truth mutation. **W0** (docs-only, lands
first) adds the doc authority registry that tells a fresh fleet
session which doc is canonical per topic.

## 1. Context and grounding

- **Terminal transitions demand no evidence.** `task.set_status` is a
  governed action (`hub/internal/server/apply_task_set_status.go`)
  and spawn lifecycle auto-derives task status
  (`hub/migrations/0041_tasks_spawn_lifecycle.up.sql`), but a task
  reaches `done` on say-so: no evidence reference, no enforced order
  between "validated" and "claimed done". Our recurring review bug
  class — *a lifecycle flip must sweep every writer of the old
  status; a terminal state had no writer at all* (task-board W2/W4,
  ADR-058 heartbeat review) — is exactly what a fail-closed receipt
  contract converts from a review finding into a rejected write.
- **ADR-034 covers half the failure space.** The loop-closure runtime
  (terminal report to the issuer's inbox, per-hop deadlines,
  `job_sweep.go` as the deadline clock, stall escalation) catches an
  agent that goes *quiet*. An agent that stays *busy* — turn after
  turn of formatting, summaries, and doc nudges with no outcome —
  passes every current check and keeps drawing budget and attention.
- **Wakes are unconditional.** Scheduled directives and loop ticks
  fire when their trigger fires. There is no cheap pre-flight that
  answers "is there anything worth doing, and if not, may this turn
  stay silent and free?" [ADR-011](../decisions/011-turn-based-attention-delivery.md)
  made attention delivery turn-based; nothing yet *budgets* it.
- **The substrate for dreaming already exists in halves.**
  [ADR-038](../decisions/038-per-run-event-digest.md) digests + turn
  index give the compact evidence a background reviewer needs;
  digest-issue classes (`hub/internal/server/digest_issues.go`)
  already self-report parser drift; the records entity
  ([Lane B](desktop-compare-wall-and-decisions.md)) gives proposals a
  typed landing place with a promotion ladder
  ([ADR-030](../decisions/030-governed-actions-and-propose-verb.md)).
  What's missing is only the lane that connects them on a schedule.
- **Provenance of the borrow.** Mechanism shapes are adapted from the
  loopx kernel (turn-transaction ordering, outcome floor, should-run
  fold, dreaming contract) as catalogued in
  [`discussions/loop-engineering-borrows.md`](../discussions/loop-engineering-borrows.md)
  §2/§4. Shapes only — no code, no dependency.

## 2. Decisions

- **D-1. Evidence is a typed reference, not prose.** A terminal
  transition (`done`, and `cancelled` with cause) carries
  `evidence_json`: a small array of `{kind, ref}` where `kind ∈
  {commit, ci_run, artifact, test_output, report, waiver}` and `ref`
  is resolvable (sha, run id, blob id per
  [ADR-061](../decisions/061-blob-lifetime.md), digest anchor).
  `waiver` is legal but explicit — the absence of evidence is always
  a stated decision, never a default. Additive column on `tasks`; no
  new table (ADR-034 precedent: roles over existing tables).
- **D-2. Receipts are ordered and fail closed at the hub.** The write
  path rejects a terminal transition whose receipt violates the
  prefix order `result < validate < writeback`. Enforcement lives in
  the single governed write path (`apply_task_set_status.go` + the
  spawn-lifecycle deriver), not in client convention — every writer
  of a terminal status goes through the same gate, by construction
  (the writer-sweep lesson, made structural).
- **D-3. Outcome classification is claimed, then checked.** Each
  agent-attributed turn on a task may claim `outcome ∈ {surface_only,
  outcome_progress, primary_outcome}`; a claim above `surface_only`
  requires an evidence ref, else the hub records `surface_only`.
  Absent claim = `surface_only`. The floor: after `N=3` (config)
  consecutive `surface_only` turns on one task, automatic wakes for
  that task are refused until either an evidence-backed turn or a
  blocker/finding record exists. Manual (human-initiated) turns are
  never floored.
- **D-4. `should_run` is a hub projection, not a scheduler.** One
  read-only endpoint folds: open work for the target, gate state
  (pending attention items), floor state (D-3), health (ADR-034
  deadline state), and budget hints into `{decision: deliver | ask |
  wait | repair | quiet, reason}`. Cron/loop/heartbeat callers
  consult it before spawning; a `quiet` outcome spawns nothing, emits
  no attention item, and is visible only in the run-history ledger.
  Existing schedulers keep their cadence; the decision only gates the
  tick's *effect*.
- **D-5. Dreaming is an ordinary session with an extraordinary
  contract.** A scheduled agent session, read-only scope (digests,
  transcripts, records, docs), whose only write verb is
  `record_propose` (kind `finding` or `decision`, status `proposed`).
  It cannot set task status, spawn, or touch delivery budget — the
  contract is scope + tool allowlist, not a new authority tier;
  promotion of its proposals rides the ADR-030 ladder unchanged.
  Harness self-modification (skills, protocols, CLAUDE.md) enters
  the same way: proposed, empirically gated (CI + review), then
  promoted — the Darwin-Gödel-Machine lesson in our idiom.
- **D-6. Quota counts verified transitions.** Where budgets/rollups
  exist ([ADR-036](../decisions/036-claude-code-statusline-telemetry.md),
  ADR-038 rollups), the unit of "the loop did something" is a
  terminal or evidence-backed transition, not a turn. Monitor-only
  turns are free and silent (D-4 `quiet`).
- **D-7. W0 doc registry is data, linted, small.** One YAML file
  (`docs/doc-registry.yaml`): topic → canonical doc, freshness rule,
  conflict rule ("ADR beats discussion; newer Accepted ADR beats
  older"), plus the default entry docs for a fresh fleet session.
  `lint-docs` verifies every referenced path exists; CLAUDE.md points
  at it. It does not attempt to describe every doc — canonical
  owners and entry points only.

## 3. Design notes

**W1 receipt shape.** The receipt is not a new table either: it is
`evidence_json` (D-1) plus a `receipt_phases` string column recording
the ordered prefix actually completed (e.g. `result,validate,
writeback`). The hub validates prefix order and the D-1/D-2 rules in
one place; the desktop task detail renders evidence refs as links
(commit → forge, ci_run → checks, blob → viewer). The A2A/MCP surface
gains optional `evidence` on the existing task-completion paths —
additive, engines that never send it fall under D-1's explicit-waiver
rule via a steward-visible `unattested` badge rather than a hard
reject for M1-driven engines that cannot be taught (parity with the
[driving-mode](../reference/glossary.md#driving-mode) split).

**W2 floor bookkeeping.** Two columns on `tasks`
(`surface_only_streak`, `floored_at`), maintained by the same write
path that records outcomes; `job_sweep.go` remains the clock only for
ADR-034 deadlines — the floor needs no sweeper because it is
*computed* state checked at wake time (loopx lease lesson: computed
expiry beats reapers).

**W3 decision fold.** Pure function over queryable state; exposed as
one handler + one MCP tool so agents can also self-check ("should I
keep going or end my turn"). The fold's order is fixed and documented:
safety/health → operator gates → evidence readiness → floor → budget
— matching the gate-order doctrine in the discussion doc §4.

**W4 dreaming loop.** Cron-shaped trigger (existing scheduled
directive machinery) → spawn with the D-5 contract → session reads
the last window of digests + open digest-issue classes + recent
records → emits 0..k proposals with typed links (`caused`,
`supersedes`, `depends_on` — the Tier C vocabulary) → terminal report
per ADR-034. The steward reviews proposals in the records surface;
nothing else changes. First scheduled dream targets the highest-value
corpus we already have: digest-issue classes and review bug-class
recurrences.

## 4. Wedges

- **W0 — doc authority registry** (docs-only, LANDS FIRST).
  `docs/doc-registry.yaml` + lint + CLAUDE.md pointer. *Accept:* lint
  fails on a dangling registry path; a fresh session prompt names the
  registry as first read; registry names ADR-conflict rule.
- **W1 — evidence-gated terminal transitions + receipts.** Migration
  (two columns), hub-side validation in the governed write path,
  MCP/A2A additive `evidence`, desktop task-detail rendering,
  `unattested` badge path. *Accept:* a `done` write without evidence
  or waiver is rejected on evidence-capable paths and badged on
  M1 paths; a receipt with `writeback` before `validate` is rejected;
  every existing writer of terminal status flows through the gate
  (test enumerates writers — the sweep test, made permanent).
- **W2 — outcome classification + floor.** Outcome on turn records,
  streak columns, wake-time floor check, floor visible on the task
  card with its reason. *Accept:* three evidence-free turns floor the
  task's auto-wakes; an evidence-backed turn or proposed blocker
  record lifts it; manual turns unaffected; floor state survives hub
  restart (computed, not swept).
- **W3 — `should_run` projection.** Handler + MCP tool + adoption in
  the scheduled-directive and loop-tick callers; `quiet` outcome
  ledger row. *Accept:* a tick with no eligible work spawns nothing
  and emits no attention item; each non-`deliver` decision carries a
  concrete reason; decision fold order matches §3 and is asserted in
  a table test.
- **W4 — dreaming lane** (gated on Lane B records shipping).
  Scheduled read-only session, `record_propose`-only toolset, first
  dream over digest-issue classes. *Accept:* the session cannot
  perform any governed action except `record_propose` (enforced, not
  prompted); proposals land as `proposed` records with typed links;
  a rejected proposal leaves no state beyond its record; the dream
  session itself produces an ADR-034 terminal report.

## 5. Non-goals

No LoopX dependency or file-based state kernel; no graph
orchestration engine; no new authority tier (ADR-030's ladder is
enough); no automatic acceptance of any dreamed proposal; no
lane-level reward review yet (Tier B5 — needs W1–W3's data first);
no write-scope leases yet (Tier B2 — revisit when parallel fleet
collisions on one task actually occur, not before).

## 6. Verification

Hub: table tests per wedge acceptance above, including the
writer-enumeration sweep test (W1) and the fold-order table test
(W3); `job_sweep` tests extended for floor non-interaction. Desktop:
state tests for task-detail evidence rendering (CI does not run
desktop state tests — run `node --test src/state/*.test.ts`
manually). Docs: `lint-docs` + the new registry lint (W0). Each wedge
lands with its own review pass per repo cadence.

## 7. Open questions

- **Q1.** Should W2's `N=3` floor be per-task, per-lane, or
  per-engine? Start per-task; revisit with W2 telemetry.
- **Q2.** Does W3 subsume the ADR-058 host-job heartbeat gating, or
  stay strictly above it? (Host jobs have their own liveness
  semantics; proposal: `should_run` consults, never replaces.)
- **Q3.** Which corpus does the second dream target — transcripts, or
  the review-fix history? Decide after the first dream's proposal
  quality is reviewed.
- **Q4.** Self-checking projection hashes for ADR-062 snapshots and
  ADR-057 teleport envelopes (Tier B3) — fold into W1 as a shared
  helper, or a separate micro-wedge? Left out of scope here; cheap
  either way.
