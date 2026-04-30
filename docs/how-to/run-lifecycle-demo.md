# Run the lifecycle demo — end-to-end walkthrough + test plan

> **Type:** how-to
> **Status:** Current (2026-04-30) — staged via wedges W1–W6
> **Audience:** testers, contributors, demo reviewers
> **Last verified vs code:** v1.0.349

**TL;DR.** The canonical end-to-end walkthrough for the amended MVP
demo (the 5-phase research lifecycle locked in
[ADR-001](../decisions/001-locked-candidate-a.md) D-amend-1, design in
[`research-demo-lifecycle.md`](../discussions/research-demo-lifecycle.md)).
This doc serves three audiences:

- **Testers / reviewers** — step-by-step instructions for verifying
  each phase end-to-end, with a numbered **checkpoint** at every
  director-visible state transition.
- **Developers** — the canonical spec the wedges
  ([`research-demo-lifecycle-wedges.md`](../plans/research-demo-lifecycle-wedges.md))
  implement against. When W3's mobile review screen looks ambiguous,
  this doc names what it must show.
- **Debuggers** — checkpoint-driven scaffolding. If the demo gets
  stuck at checkpoint 2.3, the failure space is bounded to W5's
  lit-reviewer template and the corresponding agent-feed plumbing.

The walkthrough is **idealised end-state** — what the demo does once
W1–W6 are all shipped. The §Milestones section maps which wedge
unblocks which checkpoints, so a reader running the demo at any
intermediate point knows what's expected to work and what's not.

---

## How to use this doc

- **As a tester:** read §Pre-flight, then walk §Phase 0 → §Phase 4 in
  order. Tick each checkpoint as it passes. If one fails, the wedge
  responsible is named in §Milestones; report against that wedge.
- **As a developer:** when authoring a wedge's templates / prompts /
  UI, treat the per-phase "Expected backend" and "Checkpoint" boxes
  as the acceptance contract. The phase passes only when the
  director sees the named transition and the named artifact.
- **As a debugger:** failures localise to one phase + one wedge. Use
  `hub-server seed-demo --shape lifecycle` (W6) to fast-forward to
  any checkpoint without running the prior phases live.

---

## Pre-flight

**Operator-side:**

1. Hub installed and running. See
   [`install-hub-server.md`](install-hub-server.md). Hub version ≥ the
   `Last verified vs code` line above.
2. At least one host-runner installed, registered, and heartbeating.
   See [`install-host-runner.md`](install-host-runner.md).
3. The host has `claude-code` (or `codex` / `gemini-cli`) installed
   and on PATH. Run `host-runner probe` and confirm
   `agents.claude-code.installed: true`.
4. Director's bearer token issued; mobile app paired with the hub.

**Director-side (mobile):**

1. TermiPod app installed; signed in; sees the team.
2. Home tab shows the persistent **Steward** card (W3 deliverable).
   If absent, fall back to creating projects via the existing form
   path — the demo degrades but doesn't break.

**Sanity baseline (pre-W1, today):**

- Run `hub-server seed-demo --data <DataRoot>` (the existing
  single-phase harness — see [`run-the-demo.md`](run-the-demo.md)).
- Verify the seeded `ablation-sweep-demo` project renders on the
  phone with completed runs + briefing + pending review.
- This proves the underlying pipeline (hub → host-runner → mobile
  AG-UI broker) is healthy *before* introducing lifecycle complexity.

---

## Phase 0 — Bootstrap (general steward authors the project)

Goal: director gives the general steward a free-text idea; general
steward authors a 5-phase plan + a project-specific domain-steward
template + worker templates; director reviews and approves the
bundle; project enters phase 1.

### Director's actions

1. From home tab, tap the persistent **Steward** card.
2. *(First-time-on-team only)* The hub spawns
   `steward.general.v1` as a singleton for this team. Director sees
   a fresh agent feed; the general steward greets and asks for an
   idea.
3. Director types (or pastes) a research idea. Example:
   > *"I want to know whether Lion outperforms AdamW on tiny GPT
   > pretraining across model sizes. Quick study, 1000 iters, three
   > sizes."*
4. Wait. The general steward thinks (turn-based, may be 30–90 s on
   claude-code at default settings).

### Expected backend

- `agents` row: one `steward.general.v1` instance per team, status
  `running`, role `steward` per the operation-scope manifest
  ([ADR-016](../decisions/016-subagent-scope-manifest.md)).
- General steward calls `templates.plan.create` →
  `<DataRoot>/teams/<team>/templates/plans/research-project.<id>.yaml`
  written.
- General steward calls `templates.agent.create` × 4–5 →
  `<DataRoot>/teams/<team>/templates/agents/{steward.research.<id>.yaml,
  lit-reviewer.v1.yaml, coder.v1.yaml, paper-writer.v1.yaml,
  critic.v1.yaml}` written. Optionally edits prompt files via
  `templates.prompt.create`.
- General steward calls `plan.instantiate(template_id=research-project.<id>,
  parameters={idea: "<text>"})` → new `plans` row with `status='draft'`.
- General steward calls `attention.create(kind='request_approval',
  payload=<plan_id + template_ids>)`.

### What the director sees

- Me tab gains an attention item: *"Steward proposes plan + 5
  templates for review."*
- Tap → phase-0 review surface (W3 deliverable):
  - Tabs: **Plan** | **Templates** | **Idea recap**
  - **Plan tab**: 5-phase outline with phase names, expected workers,
    expected artifacts, expected gate copy.
  - **Templates tab**: list of authored templates; tap any to open
    the raw text editor (read-only-by-default, edit toggle).
  - **Idea recap tab**: the idea the steward wrote down +
    interpretation.
  - Footer buttons: **Approve & start** | **Request revisions** | **Abort**.

### Checkpoints

| # | What to verify | Failure → wedge |
|---|---|---|
| **0.1** | Persistent steward card on home tab opens the general-steward agent feed | W3 (mobile entry) / W4 (singleton spawn) |
| **0.2** | After idea submission, attention item appears within 2 minutes | W4 (general-steward prompt) |
| **0.3** | Phase-0 review surface renders with all three tabs populated | W3 (review screen) / W4 (steward output shape) |
| **0.4** | Each template opens; raw text is valid YAML/Markdown | W2 (template-authoring MCP) / W4 (steward content) |
| **0.5** | Edit one worker template (change a comment line) → save → reopen → change persists | W2 (overlay write) / W3 (editor) |
| **0.6** | Approve → plan status flips draft→ready; attention item resolves; project's plan view shows **Phase 1 in progress** | W2 (plan promote) / W3 (review surface action) |
| **0.7** | Project's stewardship transfers from general steward to the newly-spawned domain steward (project header shows domain-steward handle) | W4 (handoff) / W5 (domain-steward seed) |
| **0.8** | General steward remains alive and reachable from home tab after handoff | W4 (persistent-concierge mode) |

---

## Phase 1 — Lit Review

Goal: domain steward spawns 1–3 `lit-reviewer.v1` workers; each
investigates a sub-area of the idea; steward aggregates findings into
one synthesis document; director reviews and approves.

### Director's actions

1. Wait. Phase runs autonomously; expect 5–20 minutes depending on
   how much the lit-reviewers fetch.
2. Optionally tap into the domain steward's agent feed to watch live
   activity (read-mostly).
3. When the gate fires, Me tab gains a new attention item.

### Expected backend

- Domain steward calls `agents.spawn(kind=lit-reviewer.v1)` × 1–3
  with sub-area assignments in the spawn task.
- Lit-reviewers use engine-native `WebSearch` + `WebFetch` against
  arxiv.org, papers-with-code, openreview, and well-known github
  repos (per safety guardrails in
  [discussion §5](../discussions/research-demo-lifecycle.md)).
- Each lit-reviewer calls `documents.create(kind=memo,
  title="Lit review: <sub-area>")` with citations.
- Each lit-reviewer calls `a2a.invoke(target=<parent steward>,
  task="lit-review-complete", ref=<doc_id>)`. The A2A target
  restriction (ADR-016 D4) ensures workers can A2A only their parent.
- Domain steward gathers via `agents.gather` (or A2A inbox), reads
  the docs via `documents.read`, writes synthesis via
  `documents.create(kind=report)`.
- Domain steward calls `attention.create(kind=request_select,
  choices=[approve, revise, abort])`.

### What the director sees

- Me tab attention item: *"Lit-review synthesis ready."*
- Tap → document viewer with:
  - Synthesis report at top
  - Per-sub-area reports as collapsed siblings
  - Footer buttons matching the choices

### Checkpoints

| # | What to verify | Failure → wedge |
|---|---|---|
| **1.1** | Domain steward's agent feed shows ≥1 `agents.spawn` call within 5 min of phase 0 approval | W5 (domain-steward prompt) |
| **1.2** | Each lit-reviewer worker shows in agent list, status `running` then `terminated` | W5 (worker template) |
| **1.3** | Each lit-reviewer's transcript shows actual `WebSearch`/`WebFetch` calls (not just text) | W5 (lit-reviewer prompt — guardrails) |
| **1.4** | Worker → domain-steward A2A succeeds; worker → other-agent A2A is rejected with role error | ADR-016 D4 (A2A target restriction; W1 follow-up) |
| **1.5** | Synthesis document renders with citations and per-sub-area sub-reports | W5 (steward aggregation prompt) |
| **1.6** | Director picks **Revise** → domain steward iterates (re-spawns workers or edits doc) → new attention item; **Approve** → phase advances | W3 (request_select choices) / W5 (revise loop) |

---

## Phase 2 — Method & Code

Goal: domain steward spawns `coder.v1`; coder writes training/eval
code in a worktree; optional `critic.v1` reviews; method spec and
code commit are presented for director approval.

### Director's actions

1. Wait. Coder iterates; critic loop may take 10–30 min.
2. Attention item appears when method+code is ready.
3. Tap → method-and-code review.

### Expected backend

- Domain steward spawns `coder.v1` with a worktree path and the
  lit-review doc as input context.
- Coder uses engine-native `Bash` / `Edit` / `Read` / `Write` /
  `Test`; installs only PyPI signed packages from well-known
  maintainers (per guardrails).
- Coder writes a method-spec document
  (`documents.create(kind=memo, title="Method")`) and commits code
  to the worktree.
- *(Optional)* Domain steward spawns `critic.v1` to review the code;
  critic returns `documents.create(kind=review)` via A2A; coder
  iterates on critic's feedback. Bounded by phase budget (soft, MVP).
- Domain steward attaches the worktree commit SHA to a `runs.register`
  preview row (no run yet) or to the method document as metadata.
- `attention.create(request_select)` for director.

### What the director sees

- Me tab: *"Method + code ready for review."*
- Tap → method-review surface:
  - Method document at top
  - Code commit reference (SHA + worktree path)
  - Critic review (if used)
  - Footer: Approve / Revise / Abort

### Checkpoints

| # | What to verify | Failure → wedge |
|---|---|---|
| **2.1** | Coder agent spawned with worktree path set | W5 (coder template) |
| **2.2** | Coder transcript shows package installs only from PyPI / apt / official releases (no `curl` from random URLs) | W5 (coder safety prompt) |
| **2.3** | Worktree commit SHA recorded on the method document | W5 / W2 (documents update) |
| **2.4** | If critic enabled: at least one critic review document exists; coder's iteration shows in transcript | W5 (critic loop) |
| **2.5** | Director **Approve** → phase 3 starts; **Revise** → coder iterates | W3 (gate UI) / W5 (revise loop) |

---

## Phase 3 — Experiment (the original Candidate A sweep)

Goal: domain steward spawns N `ml-worker.v1` workers (the existing
template); each runs one cell of the matrix on the GPU host; metrics
flow via trackio → host-runner reader → hub digests → mobile
sparklines; result-summary document is produced.

This phase reuses the existing pipeline. See
[`run-the-demo.md`](run-the-demo.md) Path B for the live-pipeline
verification of phase 3 in isolation, and
[`research-demo-gaps.md`](../plans/research-demo-gaps.md) for the
hardware-run tracker.

### Director's actions

1. Wait. Phase 3 runtime is set by the experiment matrix — for
   Candidate A's 6 runs of nanoGPT-Shakespeare 1000 iters, ~12–15 min
   on a real GPU, or seconds with `mock-trainer`.
2. Optionally tap into a worker's agent feed to watch live training
   logs.
3. Sparklines populate on the project's run views as digests land.
4. Attention item fires when all runs complete + summary written.

### Expected backend

- Domain steward calls `agents.spawn(kind=ml-worker.v1)` × N (per
  the frozen experiment matrix from phase 2).
- Each ml-worker runs nanoGPT in its worktree; writes metrics to
  trackio; calls `runs.register` + `runs.complete` +
  `runs.attach_metric_uri`.
- Host-runner's trackio reader poll-loop reads SQLite, downsamples,
  PUTs digests to `/v1/teams/{team}/runs/{run}/metrics`.
- Domain steward gathers, writes summary doc, fires attention item.

### Checkpoints

| # | What to verify | Failure → wedge |
|---|---|---|
| **3.1** | N ml-worker agents spawned with distinct worktrees | (existing infrastructure) |
| **3.2** | Sparklines populate on run cards within 1 minute of each run starting | (P3.1 trackio path — existing) |
| **3.3** | Each `runs` row has `trackio_run_uri` + non-empty digest | (existing) |
| **3.4** | Result-summary document renders with per-run table + comparison | W5 (steward summary prompt) |
| **3.5** | Director **Approve** → phase 4; **Iterate** → steward spawns more workers (parameter-extend); **Abort** → project closes failed | W3 / W5 |

---

## Phase 4 — Paper

Goal: domain steward spawns `paper-writer.v1`; paper-writer reads
phases 1–3 outputs (lit-review, method, runs/digests, summary) and
writes a 6-section paper-shaped document; optional `critic.v1`
peer-review revise-loop; director approves; project closes
successfully.

### Director's actions

1. Wait. Paper writing typically 5–15 min; with critic loop, longer.
2. Attention item fires when paper is ready (after final critic-loop
   pass if enabled).
3. Tap → paper view (rendered as a long document with section
   anchors).

### Expected backend

- Domain steward spawns `paper-writer.v1` with read access to all
  prior-phase documents + run digests.
- Paper-writer calls `documents.read` / `runs.list` /
  `run.metrics.read` to gather inputs.
- Paper-writer produces a `documents.create(kind=report,
  title="<paper title>")` with sections: Abstract, Introduction,
  Method, Results, Discussion, Limitations, References.
- *(Optional)* Domain steward spawns `critic.v1`; revise-loop until
  critic accepts or max-iterations reached.
- Domain steward calls `attention.create(request_select,
  choices=[approve, revise, abort])`.

### What the director sees

- Me tab: *"Paper ready for review."*
- Tap → paper viewer (markdown render with section headers).
- Footer: Approve & close project / Revise / Abort.

### Checkpoints

| # | What to verify | Failure → wedge |
|---|---|---|
| **4.1** | Paper-writer agent spawned with read-side scope only (cannot spawn agents, cannot edit templates) | W5 (paper-writer template) + ADR-016 (operation-scope) |
| **4.2** | Paper document renders with all 6 sections; references cite the lit-review document and run digests (not made-up sources) | W5 (paper-writer prompt — no novelty claims) |
| **4.3** | If critic enabled: at least one revise-loop iteration; final paper score recorded | W5 (critic loop) |
| **4.4** | Director **Approve** → project status flips to `closed`; domain steward auto-archives; general steward remains alive | W3 / W4 (lifetime semantics) |

---

## Cross-cutting tests (run any time)

| # | Scenario | Expected | Failure → wedge |
|---|---|---|---|
| **X.1** | Director archives general steward from agent menu, then taps Steward card from home | New general-steward instance spawns; prior conversation is in archived sessions | W4 (singleton respawn) |
| **X.2** | While phase 1 is running, edit `lit-reviewer.v1` worker template via mobile editor | Save succeeds; running lit-reviewers keep their version; future spawns use new content | W2 (overlay versioning) / W3 (editor) |
| **X.3** | Concurrent edit: domain steward writes a template via MCP at the same time the director edits in mobile | Last-write-wins; no crash; both edits visible in subsequent reads (later one persists) | W2 (overlay write semantics) |
| **X.4** | A worker invokes its engine's `Task` tool to fan out internally | Engine-internal subagents do not appear as separate `agents` rows; their actions surface in the parent's transcript only | ADR-016 D5 (engine-internal exemption) |
| **X.5** | Worker attempts `agents.spawn` directly via MCP | Returns role-denial error | ADR-016 D2 (W1) |
| **X.6** | Worker attempts `a2a.invoke` to a non-parent agent | Returns role-denial error | ADR-016 D4 (W1 follow-up) |
| **X.7** | Director rejects at any phase gate with "Revise" | Domain steward enters intra-phase iteration loop; new artifact replaces old; new attention item fires | W5 (revise loops) |
| **X.8** | Manager/IC invariant — director asks general steward to write code | General steward declines or delegates to a worker (does not edit files itself) | W4 (concierge prompt) |
| **X.9** | Manager/IC invariant — domain steward attempts to write code directly | Domain steward delegates to coder (does not perform IC inline) | W5 (domain-steward prompt) |

---

## Wedge → checkpoint mapping (debugging triage)

When a checkpoint fails, this table localises the responsible wedge.
Cross-reference with
[`research-demo-lifecycle-wedges.md`](../plans/research-demo-lifecycle-wedges.md).

| Wedge | Unblocks checkpoints |
|---|---|
| **W1** (operation-scope middleware) | X.5, X.6 |
| **W2** (template-authoring MCP + team overlay loader) | 0.4, 0.5, 0.6, X.2, X.3 |
| **W3** (mobile template editor + phase-0 review + persistent-steward entry) | 0.1, 0.3, 0.5, 0.6, 1.6, 2.5, 3.5, 4.4 |
| **W4** (`steward.general.v1` template + bootstrap+concierge prompt) | 0.1, 0.2, 0.7, 0.8, X.1, X.8 |
| **W5** (domain steward seed + worker seeds + safety guardrails) | 0.4, 0.7, 1.1–1.6, 2.1–2.5, 3.4, 3.5, 4.1–4.3, X.7, X.9 |
| **W6** (`research-project.v1` plan + `seed-demo --shape lifecycle`) | 0.6, fast-forward access to all phase checkpoints via harness |

Phase 3 checkpoints (3.1–3.3) ride on existing infrastructure shipped
through P3 + P4.1–4.4 in
[`research-demo-gaps.md`](../plans/research-demo-gaps.md).

---

## Milestones

Roadmap of when each lifecycle capability becomes verifiable.

### M0 — Pre-W1 baseline (today)

**What works:** original Candidate A demo (single-phase ablation
sweep). Run via `seed-demo` per [`run-the-demo.md`](run-the-demo.md).

**Lifecycle status:** none. The 5 phases are not yet wired.

### M1 — W1 shipped (operation-scope middleware)

**What works:** role-gating at the hub MCP boundary. Workers cannot
call steward-only tools.

**Verifiable:** X.5 (worker → `agents.spawn` denied). X.6 (worker →
non-parent A2A denied) blocked on the W1 follow-up commit.

**Director-visible UX:** none. Pure backend.

### M2 — W2 + W4 shipped (template authoring + general steward)

**What works:** general steward can author template overlay files
via MCP; respawns on demand; runs in concierge mode after bootstrap.
No mobile UI yet, so the director can't see this directly — verified
via API calls or hub logs.

**Verifiable:** 0.7 (general steward authors a domain-steward
template), 0.8 (general-steward persistence), X.1 (singleton
respawn), X.8 (manager/IC invariant via prompt).

**Director-visible UX:** chat with general steward via the existing
agent feed (no persistent home-tab card yet).

### M3 — M2 + W3 (mobile template editor + phase-0 review surface)

**What works:** director can see and approve plans + templates on
phone; phase-0 review surface renders.

**Verifiable:** 0.1, 0.3, 0.4, 0.5, 0.6.

**Director-visible UX:** persistent steward card on home tab; full
phase-0 round-trip end-to-end on phone.

### M4 — M3 + W5 (worker seeds + safety guardrails)

**What works:** lit-reviewer / coder / paper-writer / critic workers
spawn and produce phase artifacts. Phases 1, 2, 4 work end-to-end.

**Verifiable:** all phase 1, 2, 4 checkpoints (1.1–1.6, 2.1–2.5,
4.1–4.4). Phase 3 was already working pre-W5.

**Director-visible UX:** full lifecycle minus phase 3's hardware run
— a reviewer can take a project from idea to paper-draft on a no-GPU
laptop.

### M5 — M4 + W6 (`research-project.v1` plan + seed-demo --shape lifecycle)

**What works:** plan template that ties phases 0–4 together. The
`seed-demo --shape lifecycle` harness stages a multi-phase project
in any state for fast checkpoint verification without running prior
phases live.

**Verifiable:** all checkpoints; full lifecycle as a single
end-to-end flow.

**Director-visible UX:** **the demo as designed.** Director gives
idea → 5 phases run → paper produced. Phase 3 runs on real GPU
hardware (per `research-demo-gaps.md`'s remaining hardware-run gate)
or on `mock-trainer` for laptop-only review.

### Post-MVP — deferred capability gates

These exist as design points but don't block the lifecycle MVP:

- **Multi-day autonomy.** Today's lifecycle expects to complete in
  one human-day with attention-blocking. Multi-day backgrounded
  agents are a post-MVP autonomy wedge.
- **Real lit-review with API-keyed search.**
  `attention.request_secret` is deferred ([discussion D5
  / OQ-3](../discussions/research-demo-lifecycle.md)). The MVP
  lit-review uses non-key-bearing search only.
- **Cross-project memory for general steward.** The general steward
  doesn't yet remember last week's work on a different project when
  helping today. Engine-resume gives intra-conversation continuity;
  cross-project synthesis is OQ-4.
- **Hard manager/IC enforcement.** X.8 / X.9 are prompt-soft for
  MVP. A classifier-based hard gate is OQ-5.
- **Paper review by external reviewer.** `critic.v1` is an
  AI-on-AI review for MVP. Real human peer review (export → arxiv
  → reviews back into hub) is post-MVP.

---

## Hooks for development workflow

This doc is the **acceptance contract** for the lifecycle MVP.

- **PR review checklist:** when a wedge PR claims to close a
  checkpoint, the reviewer runs the checkpoint flow against the PR
  branch.
- **CI integration test:** the cross-cutting tests (X.1–X.9) and a
  subset of phase checkpoints (anything that doesn't need a real
  GPU or real WebSearch) are candidates for CI smoke. Backed by the
  `seed-demo --shape lifecycle` harness once W6 lands.
- **Bug reports:** when a tester files a bug from running this doc,
  they cite the checkpoint number — e.g. *"Checkpoint 0.6 fails:
  approve button doesn't flip plan status."* The bug is then routed
  to the wedge owner per §Wedge→checkpoint mapping.

---

## References

- [ADR-001 (amended)](../decisions/001-locked-candidate-a.md) — locked candidate is the lifecycle
- [ADR-016](../decisions/016-subagent-scope-manifest.md) — operation-scope manifest
- [Discussion: research-demo-lifecycle](../discussions/research-demo-lifecycle.md) — full design + open questions
- [Plan: research-demo-lifecycle wedges](../plans/research-demo-lifecycle-wedges.md) — implementation breakdown
- [Plan: research-demo-gaps](../plans/research-demo-gaps.md) — phase 3's hardware-run tracker
- [How-to: run-the-demo](run-the-demo.md) — original single-phase dress rehearsal (still valid for phase 3 in isolation)
- [How-to: install-hub-server](install-hub-server.md), [install-host-runner](install-host-runner.md) — pre-flight
- [Glossary](../reference/glossary.md) — canonical terms (general steward, domain steward, operation scope, …)
