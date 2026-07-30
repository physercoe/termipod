# Loop-engineering borrows

> **Type:** discussion
> **Status:** Open (2026-07-30) — landscape capture + code-read of
> huangruiteng/loopx; borrow catalogue ranked; concrete wedges live in
> [`plans/loop-discipline-and-dreaming.md`](../plans/loop-discipline-and-dreaming.md)
> **Audience:** contributors
> **Last verified vs code:** 2026.727.938
> **Freshness:** snapshot (refresh when a loop-engineering kernel crosses
> ~10k stars, or when the referenced plan ships its first wedge)

**TL;DR.** Mid-2026's "loop engineering" wave (Osmani/Cherny, June;
the Linear-vs-LangGraph graphs-vs-loops debate and Microsoft's Agent
Framework "Harness", July) is converging on one claim: the scarce
skill is no longer prompting an agent but **designing the system that
prompts it** — durable goals, verification, evidence, wake schedules,
and attention budgets. The most instructive open-source exemplar is
[huangruiteng/loopx](https://github.com/huangruiteng/loopx) (~156★,
MIT, Python, 2026-05-31): small in traction but unusually disciplined
in its kernel — a *state kernel outside the agent runtime* whose
doctrine is **"an observation is not a transition; a summary is not
writeback; feedback is not permission; quota protects attention, not
just compute."** A full code-read (kernel verified mechanism by
mechanism, not README-trusted) finds a genuinely strong core wrapped
in an unlintable periphery — borrow the ideas, never the dependency.
TermiPod already has the *closure* half of loop engineering
([ADR-034](../decisions/034-orchestration-loop-closure.md): terminal
reports, per-hop deadlines, stall escalation) and the *authority*
half ([ADR-030](../decisions/030-governed-actions-and-propose-verb.md):
the propose verb). What we lack is the **evidence half**: nothing
makes a turn *prove* progress before the loop funds the next one.
Tier A borrows (build soon): (A1) evidence-gated completion with
ordered turn receipts, (A2) the outcome floor — stop funding
surface-only turns, (A3) a `should-run` decision before every
auto-wake. Tier B (adapt): (B1) a dreaming lane emitting
`record_propose` proposals, (B2) write-scope leases for parallel
fleet work, (B3) self-checking lossy context projections, (B4) a doc
authority registry. Tier C is lenses and declines. The self-evolving
frontier (Darwin Gödel Machine, EvoAgentX) reduces, for a system like
ours, to the same governed shape: agents may evolve the harness only
through empirically validated proposals — which is Tier B1.

Companion to
[`multi-agent-harness-landscape.md`](multi-agent-harness-landscape.md)
(who else builds harnesses) and
[`reasonix-loop-borrows.md`](reasonix-loop-borrows.md) (loop-design
borrows from a single engine); this doc is about the *control plane
above* engines and harness both.

---

## 1. The wave, briefly

Four signals define the moment:

1. **"Loop engineering" named.** Coined June 2026 (Addy Osmani,
   crystallizing Boris Cherny's *"I don't prompt Claude anymore. I
   have loops that are running"*). The canonical decomposition:
   every loop needs a **trigger**, a **verifiable goal**, **actions**,
   **verification/stop conditions**, and **memory**. Prompt
   engineering asks "what should I say"; loop engineering asks "what
   system finds the work, does it, verifies it, and remembers it."
2. **Graphs vs loops.** July 2026, triggered by Linear's Loops launch.
   The consensus landing zone is unexciting and correct: match the
   orchestration to the workflow shape — bounded loops for
   well-scoped tasks, explicit graphs when branching across
   specialized agents makes implicit control flow unmaintainable,
   event triggers for scheduled background work. State machines,
   repackaged.
3. **Harness packaging.** Microsoft's Agent Framework "Harness"
   bundles loop, planning, memory, context management, and safety
   controls into a product. Validation of TermiPod's thesis — the
   harness *is* the product — and of the
   [`multi-agent-harness-landscape.md`](multi-agent-harness-landscape.md)
   finding that "harness on top of a stochastic executor" is the
   winning shape.
4. **Self-evolving agents.** The
   [Darwin Gödel Machine](https://arxiv.org/abs/2505.22954) (ICLR
   '26) grows an archive of self-modifying coding agents where every
   self-modification must pass **empirical validation** (SWE-bench
   20%→50%); the
   [EvoAgentX survey](https://github.com/EvoAgentX/Awesome-Self-Evolving-Agents)
   taxonomizes what actually evolves in practice: prompts, memory,
   tools, and workflows — almost never weights. Anthropic's Managed
   Agents ships "dreaming" — scheduled review of past sessions that
   curates memory between sessions. The governed common shape:
   **self-evolution = background proposal generation + empirical
   gates + human promotion.** No credible system lets the delivery
   agent rewrite its own truth inline.

## 2. LoopX anatomy — what a code-read actually finds

LoopX is a Python CLI + local state kernel that deliberately does
**not** own the run loop: Claude Code's `/loop` or a Codex heartbeat
drives ticks, and LoopX guards what a tick may do and what it must
prove afterwards. Registry → goal state → append-only event ledgers →
run history → status/attention queue → compute quota, with a
four-responsibility runtime split (Agent plans; Provider calls out;
Capability validates and proposes typed transitions; Kernel accepts
writeback). Mechanisms worth naming precisely, with where they live:

- **Ordered turn transaction.** `TRANSACTION_PHASES = (host_execute,
  typed_result, validation, durable_writeback, quota_spend,
  scheduler_apply, scheduler_ack)` with the commit policy
  `result<validate<writeback<spend`. A turn receipt must be an
  **ordered prefix** of the phases; a material result without
  `validation` is rejected; a no-spend result with `quota_spend` is
  rejected; the receipt is bound to the plan by a content hash
  (`control_plane/turn_driver/transaction.py`).
- **`quota should-run`.** Before an automatic turn runs, a decision
  folds slot arithmetic, gate state, health, scheduler hints, and
  repair detectors into one of: run / observe / safe-bypass recovery
  / self-repair / operator-gate notify / **stay quiet**. Monitor-only
  turns spend nothing.
- **The outcome floor.** Delivery outcomes are classified
  (`surface_only | outcome_gap | outcome_progress |
  primary_goal_outcome`); after N consecutive surface-only turns the
  kernel **refuses to fund more** of them — only an outcome-scale
  attempt or a written blocker is eligible, and even that narrow
  bypass is withdrawn once the blocker exists (`loopx/quota.py`,
  `quota_with_handoff_outcome_floor`).
- **Append-only evidence, three idempotency strategies.**
  Content-addressed event ids (same body → reuse, same id different
  body → conflict), business-identity dedupe, and turn-instance
  replay — all under file locks; mutation of a prior event is a
  validation error, not a convention (`event_sourced_state.py`,
  `rollout_event_log.py`).
- **Per-todo leases, write-scope disjointness.** Contention unit is
  `(goal_id, todo_id)`; leases carry owner, TTL (expiry *computed*,
  no reaper), optimistic version CAS, and declared `write_scopes[]` —
  two peers may work the same goal concurrently **iff their write
  scopes don't overlap**, with four typed conflict codes
  (`control_plane/work_items/task_lease.py`).
- **Self-checking lossy projection.** The 8 KB turn envelope hashes
  the same 13 decision fields on both sides of compaction and ships
  `action_signature.matches`; dropped detail is named in a
  `detail_ref` cold path with the exact commands to re-fetch it
  (`control_plane/quota/turn_envelope.py`).
- **Builder-as-schema-authority.** The explore-graph validator
  re-runs the event *builder* on parsed input and requires
  byte-equality — one schema authority, no validator drift
  (`capabilities/explore/result_log.py`).
- **Structural privacy.** Event builders reject forbidden text
  markers, key names that *smell* raw (`*log*`, `*path*`,
  `*transcript*`…), and absolute paths at construction time — the
  public/private boundary is enforced where events are built, not
  audited later (`rollout_event_log.py`).
- **Dreaming lane (design, partially built).** A background lane
  reads compact run history and emits refactor warnings, memory
  consolidations, and option comparisons as **proposals** with
  `advisory: true, execution_allowed: false,
  delivery_spend_allowed: false`; promotion goes through the normal
  operator gate + quota path.

**Honest quality verdict.** Two repos share the checkout. The kernel
(event store, leases, transaction, envelope, explore log) is
fail-closed, small-moduled, and architecture-tested (import-boundary
tests, an AST-based maintainability ratchet). The periphery is sprawl:
~605k LOC, `mypy --strict` on 15 of 1,529 files, coverage gate at
19.6%, an 858-line should-run function, ~1,100 versioned schema
literals, and MCP adapter bugs (a `list_todos` tool that sends
`should_run` arguments; a quota spend gated on substring-sniffing
output). It reads as agent-authored under a rigid house style — the
repo dogfooding its own loops. The traction (~156★) doesn't support
adopting it as a dependency, and its Markdown-as-database substrate
is the opposite of our hub. **Ideas, not code.**

## 3. Where TermiPod already stands

The borrow analysis only makes sense against what exists. Mapping
LoopX's seven layers onto ours:

| LoopX concern | TermiPod today |
| --- | --- |
| Durable goals/todos | [ADR-029](../decisions/029-tasks-as-first-class-primitive.md) tasks + [`plans/project-task-board-redesign.md`](../plans/project-task-board-redesign.md) (shipped): canonical `tasks` table, spawn linkage, lifecycle auto-derivation |
| Loop closure | [ADR-034](../decisions/034-orchestration-loop-closure.md) (shipped): a directive is not done until a terminal report reaches the issuer's inbox; per-hop deadlines; `job_sweep.go` as the deadline clock; stall escalation |
| Human gates / authority | [ADR-030](../decisions/030-governed-actions-and-propose-verb.md) propose verb + 4-tier ladder; [ADR-011](../decisions/011-turn-based-attention-delivery.md) turn-based attention |
| Evidence trail | [ADR-038](../decisions/038-per-run-event-digest.md) per-run digests + turn index; digest issue classes self-report parser drift (`hub/internal/server/digest_issues.go`) |
| Host execution boundary | [ADR-058](../decisions/058-host-job-surface.md) host jobs: allowlisted kinds, detached exec, heartbeats, stale sweep |
| Safety boundary | [ADR-059](../decisions/059-agent-browser-bridge.md) consent gates + ring-audited reads; [ADR-056](../decisions/056-env-secret-host-envelopes.md) sealed secrets |
| Agent-visible state | [ADR-062](../decisions/062-desktop-ui-as-agent-addressable-entity.md) focus snapshots with allowlist projection + byte cap |
| Records of judgment | [`plans/desktop-compare-wall-and-decisions.md`](../plans/desktop-compare-wall-and-decisions.md) Lane B (planned): decision\|finding records, proposed\|accepted\|superseded, typed links, `record_propose` |

So: we are **not** missing loops, closure, or authority. The gap is
precise — three absences, all on the evidence axis:

1. **Nothing makes a turn prove progress.** A task completes when an
   agent lifecycle ends or a status write lands
   (`hub/internal/server/apply_task_set_status.go`); no evidence
   reference is demanded, no ordering is enforced between "validated"
   and "claimed done". Our recurring bug class — *a lifecycle flip
   must sweep every writer of the old status; a state had no writer
   at all* — is exactly the class an ordered, fail-closed receipt
   contract turns from a review finding into a rejected write.
2. **Nothing stops a busy loop.** ADR-034 catches *silence* (stalls,
   missed deadlines); nothing catches *motion without outcome* — an
   agent looping on cosmetic progress passes every current check.
3. **Nothing decides whether waking is worth it.** Spawns and
   directives fire when asked; there is no deliver / ask / wait /
   repair / **stay-quiet** decision, and no notion that a
   monitor-only turn should be free and silent. ADR-011 delivers
   attention efficiently; nothing yet *budgets* it.

## 4. Borrow catalogue

### Tier A — build soon (the evidence axis)

- **A1. Evidence-gated completion + ordered turn receipts.** Terminal
  task transitions carry an `evidence` reference (commit, CI run,
  artifact, test output — or an explicit waiver); receipts honor
  `result < validate < writeback` as an ordered prefix, fail-closed.
  Direct counter to the writer-sweep bug class; makes ADR-034's
  terminal reports *load-bearing* instead of narrative.
- **A2. Outcome floor.** Classify per-turn delivery outcome
  (`surface_only | outcome_progress | …`); after N consecutive
  surface-only turns on one task, the loop refuses to fund another
  until an outcome-scale attempt or a blocker record exists. The
  anti-busy-loop half that ADR-034's anti-stall half never covered.
- **A3. `should-run` before auto-wake.** Every automatic wake
  (scheduled directive, loop tick, heartbeat) passes one decision:
  deliver / ask / wait-for-evidence / self-repair / stay-quiet.
  Quiet outcomes are free and unlogged-to-humans. Quota counts
  **verified transitions**, not turns.

### Tier B — borrow with adaptation

- **B1. Dreaming lane.** A scheduled background session reads run
  digests, transcripts, and digest-issue classes and emits
  **proposals only** — via `record_propose` into the Lane B records
  entity (kind `finding`, status `proposed`). Never mutates truth,
  never spends delivery budget. This is the governed form of
  "self-evolving": DGM's lesson (empirical gates on every
  self-modification) arrives here as CI + review on proposed harness
  changes. Depends on Lane B shipping.
- **B2. Write-scope leases.** Parallel fleet work is currently
  deconflicted socially (and we have the scars: colliding ADR
  numbers, sessions overlapping in one worktree). Borrow the
  primitive: a claim on a task declares write scopes; overlap is
  checked at claim time; versions are CAS'd. Fits the existing tasks
  table as columns, not a new system.
- **B3. Self-checking projections.** ADR-062 snapshots and ADR-057
  teleport envelopes are lossy projections that cannot detect when
  compaction changed meaning. Hash the load-bearing field set on
  both sides, ship `matches`, name what was dropped. ~30 lines per
  surface.
- **B4. Doc authority registry.** A machine-readable registry naming
  the canonical doc per topic, freshness, and conflict rules — what
  a fresh fleet session reads *first*. Would have prevented the
  ADR-numbering collision outright. Cheap, docs-only.
- **B5. Lane-level reward review.** Quantity / quality / token cost /
  **user attention cost** as separate coarse labels per agent lane
  over a window — "which lanes deserve more trust", distinct from
  ML-run compare. Builds on ADR-036 telemetry + ADR-038 rollups.

### Tier C — lenses and declines

Lenses (adopt the idea opportunistically): builder-as-schema-authority
validation for hub JSONL builders; structural privacy checks at event
build time (we enforce at egress today); typed relation vocabulary
(`supersedes | depends_on | caused | implements`) for Lane B record
links; LoopX's gate-order doctrine (safety → operator → evidence →
quota → execution) as a review checklist for future gate work.

Declines: LoopX as a dependency; Markdown-as-database (our hub is the
opposite bet, correctly); per-concept schema-version proliferation;
the speculative explore-harness runtime (bandit-routed exploration
lanes — largest and least load-bearing code in the repo); graph
orchestration frameworks (per §1.2, our workflow shape is loops +
event triggers; ADR-034's directive-trace already gives us the
inspectable graph *view* without a graph *engine*).

## 5. Sources

- [huangruiteng/loopx](https://github.com/huangruiteng/loopx) — repo;
  code-read 2026-07-30 at the then-current default branch
- [What is loop engineering](https://explainx.ai/blog/what-is-loop-engineering-ai-agents-2026);
  [Graphs vs loops](https://explainx.ai/blog/graphs-vs-loops-agentic-ai-debate-linear-andrew-ng-2026);
  [Graph engineering guide](https://flowtivity.ai/blog/graph-engineering-2026-guide-openclaw-codex/)
- [Microsoft Agent Framework Harness](https://visualstudiomagazine.com/articles/2026/07/22/microsoft-agent-framework-makeover-claws-loops-and-harnesses.aspx)
- [Darwin Gödel Machine](https://arxiv.org/abs/2505.22954)
  ([Sakana overview](https://sakana.ai/dgm/));
  [Awesome-Self-Evolving-Agents](https://github.com/EvoAgentX/Awesome-Self-Evolving-Agents)
