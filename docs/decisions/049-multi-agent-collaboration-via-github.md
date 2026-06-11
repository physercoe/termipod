# 049. Multi-agent collaboration via GitHub

> **Type:** decision
> **Status:** Accepted (2026-06-11) — director-directed; proven by the i18n
> Hosts pilot (4 delegated tickets, all first-pass CI-green, reviewed +
> merged). Formalizes the protocol shipped in PRs #197 and #201.
> **Audience:** contributors · maintainers · builder agents
> **Last verified vs code:** v1.0.817

**TL;DR.** Development work on this repo is delegated across **heterogeneous
AI coding agents on different hosts**, coordinated **only through GitHub**, so
the expensive maintainer's tokens are spent on decomposition, review, and
judgment rather than mechanical implementation. The model is **general** — it
governs any delegatable work (refactors, test backfill, dependency bumps,
mechanical migrations, doc sweeps, codegen), not one task type. The i18n/ARB
sweep was the proving pilot, not the scope. The protocol is **vendor-agnostic**:
agents are identified by a `git config` handle, never by model or CLI.

---

## Context

The maintainer agent (Opus / Claude Code) is rate-limited; per-ticket
implementation tokens dominate its budget. Cheaper agents — Codex,
Antigravity, open-source models such as DeepSeek, and whatever ships next —
can absorb mechanical work, but they run on **different hosts** with no shared
filesystem, and the vendor landscape moves fast. The coordination layer must
therefore be (a) a channel every agent already reaches, (b) durable and
auditable, and (c) free of any per-vendor coupling.

GitHub is that channel. (TermiPod is itself an agent-fleet coordinator, but
the hub is the *product runtime*; coordinating this repo's *development* uses
GitHub, per the director.)

## Decision

- **D-1 — Two roles, workload-agnostic.** A single **maintainer** decomposes
  work into specced tickets, reviews PRs, merges, handles judgment calls, and
  unblocks. Any number of **builders** (any model/vendor) claim and implement
  tickets; a builder never merges and never decides policy.

- **D-2 — GitHub is the substrate.** Issues = work queue, labels = state
  machine, branches/PRs = work units, CI = gate, committed docs = the protocol
  itself. No out-of-band channel; everything is auditable in the repo.

- **D-3 — The work unit is a "ticket."** A ticket is a GitHub issue specced
  for delegation. The term is deliberately *not* "task" — that collides with
  the product **Task** primitive ([ADR-029]; see the
  [glossary](../reference/glossary.md)).

- **D-4 — Lifecycle is a label state machine.** `ticket:ready → claimed →
  in-review → (changes) → merged`, plus `ticket:blocked`. Capability tiers
  `tier:mechanical | medium | judgment` describe the **work**, not the agent;
  an operator clears its builder for the tiers it may take.

- **D-5 — Identity is two axes.** *Attribution* ("which agent wrote this") is a
  `git config` **handle** + `Co-Authored-By` trailer — free, no account, scales
  to any number of agents. *Acting account* ("who pushes / opens PRs /
  approves") is the auth token; builders **may share one account**, so account
  count is **constant**, not per-agent. The claim source-of-truth is the
  **claim comment handle + branch name** (`agent/<handle>/<N>-…`), not the
  GitHub assignee (which can't distinguish shared-account agents).

- **D-6 — Hot resources serialize behind a baton.** Any file or resource that
  every ticket of a workload mutates (so parallel PRs would conflict) gets a
  general **`holds:<resource>`** label; exactly one in-flight ticket holds it,
  released on merge. `holds:arb` (the shared `lib/l10n/*.arb` files) is the
  first instance; a future workload's hot resource — a migration sequence, a
  generated file, a lockfile — gets its own baton by the same rule. Tickets
  that don't touch the resource parallelize freely.

- **D-7 — Verify before merge; maintainer-only merge.** The builder
  self-verifies the gate its ticket names **and** that every CI check is green
  (re-reading `gh pr checks` rows, never trusting `--watch`); the maintainer
  re-verifies and reviews before merging. Under a single shared account this is
  a **convention** backed by CI + review; it becomes an **enforced** gate with
  a *distinct* builder account + branch protection. Token permission scope does
  not create the gate — only distinct accounts do.

- **D-8 — Escalate, don't guess.** Ambiguity or any judgment call →
  `ticket:blocked` + a specific comment; the builder stops. The tier system
  exists to keep judgment work away from cheap agents.

- **D-9 — Vendor-agnostic.** No model or CLI name appears anywhere in the repo.
  A new agent joins by configuring a handle and reading
  [`AGENTS.md`](../../AGENTS.md). Builders may run autonomously (a host-side
  poller that claims a `ticket:ready`, hands the standing prompt to the agent,
  and loops), with a one-in-flight guard and the baton preventing collisions.

- **D-10 — General scope.** This protocol governs *any* delegatable dev work.
  The **ticket spec** carries the workload-specific recipe (files, reference
  PR, rules, the gate to run); the **protocol** carries only the coordination.
  i18n is one workload, not the definition.

## Consequences

- The maintainer's tokens are reserved for decomposition, review, and
  judgment; implementation tokens move to cheaper agents. Per-ticket maintainer
  cost ≈ spec + diff review + merge.
- Throughput scales with the number of builders; account maintenance stays
  constant (≤ two accounts: maintainer + one optional shared builder).
- **Quality risk** (cheap models) is contained by mandatory CI + maintainer
  review + tier gating, with repeated `ticket:changes` bounces escalating a
  ticket to a higher tier or the maintainer.
- **Runaway risk** (autonomous builders) is contained by the one-in-flight
  guard, a narrow tier clearance, and `ticket:blocked` acting as a natural
  kill-signal.
- The protocol's living spec is the docs/scripts
  ([`AGENTS.md`](../../AGENTS.md),
  [the how-to](../how-to/agent-collaboration.md), the agent-task issue
  template, `scripts/setup-agent-labels.sh`); this ADR is the rationale.

## Alternatives considered

- **Use TermiPod's own hub / A2A for dev coordination** — rejected: the hub is
  the product runtime, not a development tool; GitHub is the substrate every
  agent already reaches.
- **One GitHub account per agent** — rejected as the primary model: maintenance
  grows with agent count. Replaced by handle-based attribution + an optional
  single shared builder account (D-5).
- **Fork-based PRs** instead of same-repo collaborator branches — a viable
  fallback when collaborator access isn't granted; same-repo branches chosen
  for simplicity.
- **Enforced approval gate from day one** — deferred: convention + CI + review
  sufficed for the pilot; the enforced gate is a constant-cost add later
  (distinct builder account + branch protection), not a per-agent burden.

## Pilot evidence

The i18n Hosts cluster ran end-to-end on this protocol: tickets #198/#200/#203/#205
(specs) → PRs #199/#202/#204/#206 (Codex) — **four delegated tickets, every one
first-pass CI-green, each reviewed and merged** by the maintainer at low token
cost. The pilot surfaced one finding (D-5): a builder and maintainer sharing
one GitHub account make the approval step a convention rather than an enforced
gate — acceptable, and fixable with a distinct account when wanted.

## Follow-ups

- Commit a reference autonomous poller as `scripts/agent-poller.sh` once the
  operator confirms the headless invocation for their agent.
- Enable the enforced approval gate (distinct builder account + branch
  protection) when more than one builder runs in parallel.
- Revisit a per-team or per-workload routing channel if the single ready-queue
  develops contention.
