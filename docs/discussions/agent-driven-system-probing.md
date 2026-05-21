# Agent-driven system probing — agents as autonomous integration testers

> **Type:** discussion
> **Status:** Open (no code yet; first wedge proposed in §11)
> **Audience:** contributors
> **Last verified vs code:** v1.0.640

**TL;DR.** Today the full-stack behaviour of the system —
spawn-a-steward through approve-a-permission-gate through
loop-closes-back — is verified only by a human driving a phone (see
[`../plans/single-agent-demo-test.md`](../plans/single-agent-demo-test.md)).
That's the slow, un-repeatable layer. A spawned agent has both halves
of the loop the test needs: it can **act** (the ~72-tool MCP surface)
and **observe** (`audit.read`, `*.get`, channel SSE). So a prober
that **acts exactly as a normal agent** can drive the system
end-to-end and assert against the system's own black-box recorder.
Findings flow director → steward → prober → reviewer → propose →
approve (§3), so a fix lands only after a code-grounded review and the
director's ratify.

**Scope** is the backend operating runtime — every tool call,
loop-closure enforcement, governance, role and permission rules — and
explicitly *not* mobile rendering (an agent can't see the phone) and
*not* admin/fleet operations (shutdown, upgrade: a normal agent never
performs those, so neither does the prober). The discipline: **every
scenario carries a deterministic acceptance criterion taken from the
design** — the prober verifies what the system *should* do, not what
it happened to output. This doc reasons about how to do that without
inheriting the LLM's stochasticity as test flakiness, and proposes an
LLM-free L1 contract prober as the first rung.

---

## 1. The gap this fills

We have testing at two extremes and nothing in the middle:

| Layer | Exists today | Driver | Cost |
|---|---|---|---|
| Seam unit tests | 181 Go `_test.go` files | CI | seconds, free |
| Static health | `hub-server doctor`, `probe-claude-hooks`, `probe-claude-jsonl` | CLI / you | seconds |
| Deterministic fixtures | `seed-demo`, `mock-trainer` | CLI / you | seconds |
| **Full-stack behaviour** | [`single-agent-demo-test.md`](../plans/single-agent-demo-test.md) AC1–AC5 | **a human on a phone** | **half a session, each time** |

The bottom row is where the interesting bugs live — the v1.0.620–637
boundary-validator cascade
([`validate-at-every-boundary.md`](validate-at-every-boundary.md)),
the [`031`](../decisions/031-agent-tool-ergonomics.md) tool-contract
drift, the [`032`](../decisions/032-message-routing-envelope.md)/[`034`](../decisions/034-orchestration-loop-closure.md)
orchestration paths — and it is the *only* row a machine doesn't
already exercise. The reason it has stayed manual is an assumption
worth challenging: that full-stack verification needs eyes. Most of
it doesn't. It needs an actor that can call the same MCP tools an
agent calls and read back what the system recorded.

## 2. The core insight — `audit_events` is the test oracle

The usual reason agent-driven testing is flaky is that people let the
LLM be the *judge* ("does this output look right?"). Don't. The
determinism has to live somewhere stable, and the system already has
that place: **every governed action lands an
[`audit_events`](../reference/glossary.md#audit-event) row with
attribution.** That is a black-box flight recorder. So the contract
splits cleanly:

- **The agent sequences and acts** — LLM judgment decides *what* to
  do and *in what order*, and (the genuinely valuable part) *how to
  probe deeper when something is wrong*.
- **The assertions read structured state** — `audit.read` rows in
  expected order with expected attribution, `agents.get` status
  transitions, `tasks.get` / `runs.get` records. **Never** the
  wording of a chat bubble.

This is the load-bearing design choice. The stochastic component
never touches the pass/fail decision; it only drives the inputs and,
on failure, the diagnosis. A static script says "AC5 failed." An
agent prober says: *"AC5 deny-path — claude retried with `Write`
after `Bash` was denied; audit row 4412 shows the second call
executed with no `approval_request` row preceding it. The gate keys
on tool name, not on the turn, so any tool the model substitutes
slips through."* That second output is the convention
([fix the class, not the instance](../../CLAUDE.md)) made executable.

### Every scenario carries its acceptance criterion

The corollary, binding from the **first** phase: a scenario is not
"do some things and see if they look fine." Each one declares, up
front, the deterministic outcome the design dictates — *this tool
call must return 422 with a `Hint`*; *this worker→non-parent A2A must
be denied*; *this completed task must close the loop back to the
principal within the deadline*. That expected outcome is read
straight from the authoritative design (the ADR, the protocol spec,
the schema), so the prober is checking the system against its own
contract, not against a guess. A scenario with no falsifiable AC is
not a scenario — it's exploration, and belongs in the separate
exploratory mode (§8), never in the regression gate.

### The finding report contract

When the prober finds a bug or gap it must report it in a form that
is **(a) re-checkable** — a human can independently verify every fact
without trusting the agent — and **(b) reliably producible by an
agent**, i.e. it only asks for things an agent actually has in hand.
The governing rule: **facts are quarantined from the agent's
reasoning.** `expected` / `observed` / `evidence` are facts, copied
verbatim from what the prober received or read; `diagnosis` is the
one bucket where the agent is allowed to reason, and it is labelled as
hypothesis. If a claim isn't backed by an evidence entry, it doesn't
go in the facts. This report is the input the reviewer (§3) consumes;
its `code_guess: UNVERIFIED` field is the deliberate handoff point.

A run emits one **report envelope** plus a flat list of **findings**:

```jsonc
// run envelope — identifies the run so any finding is reproducible
{
  "version": "1.0.640-alpha",        // buildinfo of the hub under test
  "engine":  "claude-code/opus",     // driver for LLM rungs (L0/L1: "none")
  "fixture": "seed-demo@<git-sha>",   // deterministic starting state
  "started": "2026-05-21T...Z",
  "tally":   { "passed": 41, "failed": 2, "errored": 1, "skipped": 0 },
  "findings": [ /* see below */ ]
}
```

```jsonc
// one finding — the unit you triage and fix
{
  "id":       "PRB-2026-05-21-014",          // run-scoped, not global
  "scenario": "L4.governance.worker_a2a_non_parent_denied",
  "result":   "fail",                         // fail = AC violated (a finding)
                                              // error = prober couldn't run it
                                              //         (harness/flake, NOT a
                                              //          system bug — triaged apart)
  "kind":     "bug",                          // bug = system did the wrong thing
                                              // gap = design has no rule / no AC
                                              //       was checkable → needs a
                                              //       decision, not a code fix
  "class":    "governance-breach",            // fixed enum (below), not a vibe score
  "ac": {
    "statement": "A worker's a2a.invoke to an agent outside its parent steward must be denied.",
    "source":    "decisions/016-subagent-scope-manifest.md §3"   // where the AC comes from
  },
  "expected": "403 + Hint envelope; NO a2a.message_sent audit row.",
  "observed": "200 OK; audit row 8841 a2a.message_sent was written.",
  "evidence": [                               // verbatim, re-queryable — never paraphrased
    { "type": "request",  "tool": "a2a.invoke", "args": { /* exact */ } },
    { "type": "response", "raw":  { /* exact body + status */ } },
    { "type": "audit",    "id": 8841, "row": { /* exact audit_events row */ } },
    { "type": "status",   "of": "agent:worker-x", "snapshot": { /* agents.get */ } }
  ],
  "repro": [                                  // ordered, deterministic, runnable by hand
    "seed-demo@<sha>; spawn worker A under steward S",
    "a2a.invoke from A targeting B (B is under a different steward)"
  ],
  "diagnosis": {                              // the ONLY place the agent reasons
    "hypothesis": "scope check validates project membership but not the parent-steward edge",
    "bug_class":  "missing-boundary-validation",   // ties to validate-at-every-boundary
    "confidence": "medium",                         // low | medium | high
    "code_guess": "hub a2a invoke authz path — UNVERIFIED"  // the reviewer's job to confirm/locate
  }
}
```

`class` is a small fixed enum so it's machine-groupable and not a
subjective severity score: `contract-violation` (schema/Hint),
`governance-breach`, `permission-bypass`, `loop-not-closed`,
`wrong-state-transition`, `crash-or-5xx`, `undocumented-behaviour`.

**Why each required field is agent-doable** — and what is
deliberately *not* required:

- **Required, because the agent already holds it:** the request it
  sent, the response it got, the `audit.read` rows and `*.get`
  snapshots it queried. Copying these verbatim is the anti-
  hallucination control — an agent is reliable at *quoting* what it
  received, unreliable at *summarising* it.
- **`result: fail` vs `error`** keeps a flaky prober run from masquerading
  as a system bug — the agent can always tell whether *it* failed to
  execute or the *system* violated the AC, and that split is what
  makes the report trustworthy enough to gate on.
- **Not required:** a numeric severity (agents assign it by vibe →
  use the objective `class` enum instead); a file-and-line of the
  defect (the prober is a behavioural agent, it doesn't read Go — so
  code location is at most a `code_guess`, always flagged
  `UNVERIFIED`, and the reviewer resolves it); free prose as the
  primary record (structured fields only; prose lives solely in
  `diagnosis.hypothesis`).

The agent emits the JSON; a human reads the markdown rendering the
harness derives from it (a findings table + one detail block each).
**Transport:** L0/L1 (LLM-free harness) writes the envelope to stdout
+ a JSON file and exits non-zero on any `fail`. L2+ (a spawned prober
agent) emits each finding through `artifacts.create` and posts the
summary to a dedicated probe channel via `channels.post_event`, and
the harness collates — the prober uses only normal-agent tools (§6),
never a privileged reporting path.

## 3. The probe workflow — detect, review, propose, approve

A probe run is not a one-shot script; it is a governed loop the
director kicks off and the director closes. The shape:

```
director ──"probe the governance rules"──▶ steward
                                             │  plan + tasks (ADR-029)
                                             ▼
                                          prober (probe.v1)
                                             │  drives the system as a normal agent;
                                             │  emits findings (report contract, §2)
                                             ▼
                                          reviewer (reviewer.v1)
                                             │  reads the CURRENT codebase + design;
                                             │  confirms bug vs gap, locates it,
                                             │  resolves the UNVERIFIED code_guess,
                                             │  drafts a fix or a decision
                                             ▼
                                          steward ──propose (ADR-030)──▶ director
                                                                          │ ratify / reject
                                                                          ▼
                                                              (downstream: a coder worker
                                                               or a human implements)
```

Every arrow is an existing primitive — task dispatch
([`029`](../decisions/029-tasks-as-first-class-primitive.md)), A2A
through the relay
([`032`](../decisions/032-message-routing-envelope.md)), loop-closure
back to the principal
([`034`](../decisions/034-orchestration-loop-closure.md)), the propose
verb and attention-item ratify
([`030`](../decisions/030-governed-actions-and-propose-verb.md)). The
workflow is the harness *using* the orchestration it also tests.

### Why a separate reviewer

The prober is blind to code by design (§5, §6): it proves *the system
behaves wrong*, with verbatim runtime evidence and an `UNVERIFIED`
`code_guess`. It cannot, on its own, tell a real bug from a misread of
the design. The reviewer's whole job is the other half — read the
**current** codebase plus the authoritative design, and decide:

- **Confirmed bug** — behaviour contradicts the code's own intent →
  resolve `code_guess` to a real location, draft the fix, propose it.
- **Gap** (`kind: gap`) — the design has no rule, or the prober's AC
  was itself wrong → nothing to fix in code; propose a *decision* (a
  discussion / ADR) for the director instead.
- **Not-a-finding** — the system is correct and the prober misread →
  close it, and tighten the scenario's AC so it doesn't re-fire.

This is the separation of concerns that makes the report contract's
`UNVERIFIED` handoff load-bearing: **prober scope = live MCP/runtime,
no repo read; reviewer scope = repo read, no driving the live
system.** Two minimal blast radii, with the finding report as the
clean interface between them.

### Bootstrap ordering — don't let the loop test itself

This workflow is itself an L3 construct: it depends on task dispatch,
A2A, loop-closure, and propose all working. So it **cannot be what
validates those primitives** — that would be circular (§9 self-test
paradox). The order is: prove L1/L2/L3 through the out-of-band CLI
harness (§7) first; only once the primitives are trusted does the
agent-orchestrated workflow become a reliable *delivery* mechanism for
findings. Until then the CLI path is the source of truth and the
workflow is a convenience layered on top.

## 4. The probe ladder

Layered like the M1 ACP debug ladder — each rung trusts the one
below it, so you build and stabilise bottom-up.

- **L0 — health.** Already shipped: `hub-server doctor`. Static,
  no agent.
- **L1 — contract conformance.** Verify every tool's contract holds:
  catalog↔dispatcher↔handler lockstep, `required[]` enforcement,
  `SeeAlso` targets resolve, and the `Hint` envelope on 4xx. **Most of
  the static half already exists** — `tool_registry_test.go` and
  `native_tools_meta_test.go` already CI-lock the lockstep, alias,
  tier, and registration invariants (the trio CLAUDE.md warns about is
  *already guarded*: `TestEveryAuthorityToolRegistered`,
  `TestToolRegistry_BackendsResolve`,
  `TestToolRegistry_CatalogIsConsistent`). The genuine gaps are a
  catalog-wide `required[]`-rejection sweep and a `SeeAlso`-target
  resolution check — both ~20-line, LLM-free extensions to that
  existing meta-test suite — plus the checks that need a *live* hub:
  the `Hint`-coverage sweep, and the schema↔handler field agreement
  that is the open
  [`031` §7 Decision D](../decisions/031-agent-tool-ergonomics.md)
  `list_channels` mismatch. **That mismatch is *not* caught by the
  static lockstep checks** — `list_channels` registers, resolves a
  tier, and has a handler; the defect is the declared schema
  disagreeing with what the handler reads — so it needs the tool
  *exercised*, not introspected.
- **L2 — single-agent behaviour.** The agent-driven half of
  [`single-agent-demo-test.md`](../plans/single-agent-demo-test.md)
  AC1–AC4: spawn a steward, send a message, drive a tool call, then
  assert the file exists on the host **and** the matching audit row
  appears. Replaces the keyboard-and-SSH part of the manual walk.
- **L3 — orchestration.** The paths never auto-tested: a steward
  decomposes a goal into a [plan](../reference/glossary.md#plan) and
  [tasks](../reference/glossary.md#task)
  ([`029`](../decisions/029-tasks-as-first-class-primitive.md)),
  spawns workers, A2A round-trips through the relay
  ([`032`](../decisions/032-message-routing-envelope.md) envelope),
  and the loop closes back to the principal
  ([`034`](../decisions/034-orchestration-loop-closure.md)). Assert
  on the envelope metadata and the close-out audit rows.
- **L4 — governance.** Acting as a normal agent, the prober verifies
  the guardrails hold *around* it: propose/ratify is required where
  the design says so
  ([`030`](../decisions/030-governed-actions-and-propose-verb.md)),
  the permission gate fires on approve/deny, the role allow-set is
  enforced, and the **negative** cases refuse — a worker→non-parent
  A2A must be *denied*
  ([`016`](../decisions/016-subagent-scope-manifest.md)), an invalid
  spawn must fail-fast, and an **admin/fleet verb must be refused** to
  a non-admin identity. The prober never *performs* an admin
  operation; verifying it is *blocked* is a governance rule and stays
  in scope. Negative assertions are where the boundary-cascade class
  hides; they matter most.

## 5. The honest boundary — the prober is blind to the phone

An agent can emit `mobile.navigate` and (if the write-intent surface
ever lands) other intents, but it **cannot see the rendered Flutter
UI** — there is no return channel from pixels to the agent. So this
harness covers hub + host-runner + engine + MCP contract + governance
+ A2A + loop-closure: the *backend half* of every acceptance
criterion. The pixel-rendering half stays manual, or moves to the
complementary "eyes" track in
[`screenshot-automation.md`](screenshot-automation.md).

The win is still large and concrete. Instead of running AC1–AC5 by
hand, you run the harness; it greens the backend autonomously; your
manual job shrinks to *"confirm the green paths render correctly, and
look hard at the one red the harness already root-caused."* The
harness turns a half-session of driving into a few minutes of
focused looking.

## 6. The actors are normal agents — exactly that, no more

The prober's whole purpose is to exercise the rules a *normal* agent
is subject to, so its identity must be a normal-agent one: a scoped
`probe.v1` [agent kind](../reference/glossary.md#agent-kind) of
worker or steward class (YAML template,
[behaviour-is-data](../spine/blueprint.md), not Go), with a role
allow-set no wider than the agents it stands in for. Under
[blueprint A3](../spine/blueprint.md) every action it takes carries
its `agent_id` and lands in `audit_events` — convenient, because
those rows are partly what L3/L4 assert on.

The **reviewer** (§3) is the prober's mirror image — also a scoped
normal agent, not a privileged one: it may read the repository and
the design docs but holds **no** runtime-driving verbs. It never
spawns, never invokes A2A, never touches the live system it reasons
about. Same governed discipline, opposite half — and the two scopes
never overlap, so neither agent can both perturb the system and judge
the code at once.

**Admin and fleet operations are out of both actors' action scope,
permanently.** Shutdown, upgrade, host control
([`028`](../decisions/028-host-control-via-tunnel-and-cli.md)) are
operator verbs; a normal agent never performs them, so neither does
the prober or reviewer. The most they do with them is the negative
test of §4 L4 — confirm they are *refused* to a non-admin identity.
The one place admin verbs are used is the **harness**, out of band:
the seed/teardown CLI (§7) runs `init` / `seed-demo` / `shutdown-all`
to stand up and tear down the ephemeral hub. That is the test rig,
not an agent — keep the two cleanly separated.

## 7. Fixture lifecycle — deterministic in, swept out

A probe run needs a known starting state and must not pollute real
teams:

1. **Seed.** `hub-server init` + `seed-demo` into a throwaway
   `--data` root gives the known 5-phase lifecycle fixture;
   `mock-trainer` supplies run metrics with no GPU (the same
   fixtures [`screenshot-automation.md`](screenshot-automation.md)
   and [`research-demo-gaps.md`](../plans/research-demo-gaps.md)
   already lean on).
2. **Run** the prober against that ephemeral hub.
3. **Assert** on structured reads.
4. **Sweep.** `shutdown-all` + drop the throwaway data root, so
   `probe_`-namespaced projects/tasks/audit rows never reach a real
   team.

## 8. Two operating modes

- **Regression.** Fixed scenario spec, deterministic assertions,
  pass/fail. Cheap and repeatable; a candidate to gate releases once
  trusted.
- **Exploratory.** "Exercise the task lifecycle in unusual orders;
  report anything that violates the documented invariants." This is
  where agent capability earns its cost — it finds the *next*
  boundary-cascade bug instead of re-confirming known-good paths.
  Non-deterministic by design; run on demand, not in a gate.

## 9. Risks and how the design answers them

1. **Self-test paradox.** If the harness leans on the MCP layer to
   test the MCP layer, a broken layer can hide its own breakage. →
   L1 uses the lowest-level client (direct REST / raw MCP), so trust
   is built bottom-up before any rung depends on the abstraction; and
   the §3 workflow is layered on only *after* its primitives are
   proven by the CLI path.
2. **Cost and flake.** Don't burn an LLM to assert a schema. The LLM
   appears only from L3 up; L1/L2 assertions are structured-state
   reads; pin model + low temperature; settle async with
   retry-and-backoff, never with sleeps.
3. **Stochastic driver → flaky green.** Assert on `audit_events` /
   status, never on phrasing. The LLM is the *sequencer and
   diagnostician*, never the *oracle* (§2).
4. **Test pollution.** Ephemeral data root + `probe_` namespace +
   `shutdown-all` teardown (§7).
5. **Governance of the actors.** Scoped `probe.v1` / `reviewer.v1`
   roles, audited, neither admin-by-default, scopes non-overlapping
   (§6).

## 10. Engine choice for the LLM rungs

L3/L4 drive with the **same engine the real stewards use**
(claude-code / opus) so the prober exercises the exact production
path, not a cheaper proxy that might mask an engine-specific frame or
timing bug. The token cost is accepted as the price of
production-fidelity verification; the cheap rungs (L0/L1, and most of
L2's assertions) carry no LLM cost at all, so the spend is
concentrated where fidelity actually matters.

## 11. Recommendation and first wedge

Build **L1 — contract conformance** first, scoped to its *real* gaps —
the static lockstep half is already CI-locked by `tool_registry_test.go`
(§4), so don't rebuild it. Start with the two cheap, LLM-free
extensions to that existing meta-test suite:

1. a **catalog-wide `required[]`-rejection sweep** — for every tool
   whose schema declares required fields, assert `ValidateArgs` rejects
   an empty argument object, so the contract every agent reads is a
   real boundary for *all* tools, not just the spot-checked ones;
2. a **`SeeAlso`-target resolution check** — every `SeeAlso` an agent
   might follow names a tool that actually exists (aliases and
   `Backend`s are already locked; `SeeAlso` was the gap).

Both are concrete, immediately useful (a dangling `SeeAlso` or an
unenforced `required[]` trips CI), and need no new harness. The *live*
half — a `hub-server probe contract` subcommand (sibling to `doctor`)
that exercises each tool against a seeded hub to measure `Hint`
coverage and catch the schema↔handler Decision D mismatch — follows,
because those checks need the tool *run*, not just the registry read.

L2 follows once L1 is green; the full §3 workflow (prober + reviewer +
propose) is L3-class and gated on the `probe.v1` / `reviewer.v1` roles
plus the open questions below.

## 12. Open questions

- **Naming.** "probe" already means low-level introspection in the
  CLI (`probe-claude-hooks`, `probe-claude-jsonl`) and in
  `single-agent-demo-test.md` §6. An autonomous *agent* tester is a
  different thing. Is it a **conformance probe** (L1, mechanical), a
  **probe agent** (L2+, spawned), or do we need a distinct word
  before any of this reaches the glossary? Resolve before code so
  the term doesn't collide ([glossary §7 contract](../doc-spec.md)).
- **Reviewer role and host.** Is the reviewer a new `reviewer.v1`
  kind or an existing coder worker handed a review task? It needs a
  host with the repo checked out to read current code — which host,
  and does it read the same git SHA the fixture was built from so
  behaviour and code line up?
- **Where do scenario specs live?** YAML next to the prompt
  templates (behaviour-is-data), or Go test tables? The L1 rung
  argues for Go; L3 exploratory argues for YAML the agent reads.
- **Gate or on-demand?** Does the regression mode (§8) eventually
  block a release tag, or stay a manual `gh workflow run`? Tied to
  how flaky L2+ proves in practice.
- **Mobile assertion.** Is the phone-blind boundary (§5) permanent,
  or does a future mobile→hub "I rendered X" report channel let the
  prober assert one rung higher? Out of scope here; flagged for
  [`screenshot-automation.md`](screenshot-automation.md) and the
  agent-driven-mobile-ui write-intent work to weigh in.

## 13. Related

- [`../plans/single-agent-demo-test.md`](../plans/single-agent-demo-test.md)
  — the manual walkthrough L2 automates.
- [`validate-at-every-boundary.md`](validate-at-every-boundary.md)
  — the bug class L4's negative tests target.
- [`screenshot-automation.md`](screenshot-automation.md) — the
  complementary "eyes on the phone" track; shares the `seed-demo`
  fixture.
- [`auto-notification-coverage.md`](auto-notification-coverage.md)
  — loop-closure visibility, which L3 verifies.
- ADRs whose rules the prober verifies (acting as a normal agent):
  [`029`](../decisions/029-tasks-as-first-class-primitive.md),
  [`030`](../decisions/030-governed-actions-and-propose-verb.md),
  [`031`](../decisions/031-agent-tool-ergonomics.md),
  [`032`](../decisions/032-message-routing-envelope.md),
  [`034`](../decisions/034-orchestration-loop-closure.md),
  [`016`](../decisions/016-subagent-scope-manifest.md).
  [`028`](../decisions/028-host-control-via-tunnel-and-cli.md) is
  *out of action scope* — only its denial-to-non-admins is checked.
