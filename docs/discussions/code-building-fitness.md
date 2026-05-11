# Code-building fitness — is the chassis ready for a second vertical?

> **Type:** discussion
> **Status:** Open (Drafted 2026-05-11) — captures a fitness review; no
> implementation commitment. A future plan may extract the seed-demo
> recommendation; production support for code work is a separate later
> conversation.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.507

**TL;DR.** Termipod ships as a research-domain mobile-control plane for
agentic AI work (the MVP demo is a 5-phase research lifecycle). The
natural second vertical is **code-building** — agents writing /
reviewing / shipping code at the same arms-length the principal uses
for research today. This doc is a comprehensive + objective fitness
review of whether the architecture + UI framework can host a credible
code-building seed demo *without* MVP scope creep. **Verdict:** the
chassis is ready, the vocabulary isn't; a seed-only `code-feature.v1`
demo is one wedge of work and would prove fitness without claiming
production support. The 1-month gap between "seed shows fit" and
"production code workflow" is substantial but well-shaped — every
remaining gap is a new YAML, gate evaluator, or artifact kind, not a
chassis rewrite.

This is a sibling discussion to
[`code-as-artifact.md`](code-as-artifact.md), which focused
specifically on whether diffs / commits should be a first-class
primitive. That doc was deferred pending competitor research and
remains the right home for the diff-viewer + review-UX conversation.

---

## 1. Frame

The user (principal) asked: code-building is another main use case
besides research. Not MVP demo, but we want a seed demo proving the
architecture + UI framework is complete + fit for this type of work.
Review comprehensively and objectively.

"Fit" here means three things in descending priority:

1. **Schema fit.** Can the existing tables, artifact registry, plan /
   task / deliverable shapes hold code-domain work without bending?
2. **Mobile fit.** Can the existing chassis (Project Detail → Overview
   = header + hero + tile strip + tasks + criteria) render code work
   recognisably without inventing new screens?
3. **Demo fit.** Can a hub `seed-demo` shape produce a project that
   looks credible to a code reviewer skimming the mobile UI?

The question is *not* "can we ship a production code-workflow product"
— that's a much larger conversation and the v1.0.507 retirement of the
ablation seed shape leaves headroom for it as a future vertical, not a
near-term commitment.

---

## 2. What already maps cleanly (≈ 70%)

These primitives need **no changes** to host code work:

| Primitive | Hosts code-work how |
|---|---|
| `code-bundle` artifact kind | AFM-V1 multi-file source attached to a deliverable. Existing viewer (`flutter_highlight` syntax highlight) ships v1.0.494+. Any code agent emitting source as an artifact gets a viewer free. |
| `canvas-app` artifact kind | Sandboxed WebView for HTML/JS/CSS prototypes, dashboards, notebook-like views (v1.0.498). UI prototypes attached to a project work today. |
| `pdf` / `image` / `tabular` | Spec docs, architecture diagrams (as PNGs), CSV outputs (test coverage, perf tables) all render. |
| `plans` + `plans.steps` table | A "build feature X" plan is structurally identical to "run 3 experiments" — sequence of steps, kinds = shell / mcp_call / agent_spawn / human_decision. |
| `tasks` table + grouped-by-status mobile view (v1.0.500) | "Implement function Y, write tests, update changelog" maps directly. |
| `runs` + `run_metrics` | A CI build is a `run`; lint stats / test counts can populate `run_metrics`. The shape is flexible enough. |
| Channels + A2A | Steward → code worker → tester worker fanout reuses the existing recipe pattern. |
| `kind: commit` deliverable component | Pin a deliverable to a Git revision. Already used in the lifecycle seed (`method-doc`, `experiment-results`). One-line YAML to add. |
| Lifecycle chassis (`PhaseRibbon`, `PhaseBadge`, per-phase hero + tile swap) | Phases `[design, implementation, test, review, ship]` slot in without code change. |

The chassis being **agnostic enough** that the same building blocks host
both research and code work is the proof of architectural soundness. A
future plan should test that empirically (the seed demo recommendation
below).

---

## 3. What's underspecified (≈ 25%) — schema right, vocabulary wrong

These primitives exist but their **wire vocabulary doesn't speak code-
domain naturally**. None require chassis rewrites; each is a content
gap.

### 3.1 No project template for code work

The shipped templates are `research.v1` (5-phase lifecycle),
`reproduce-paper`, `write-memo`. None frame "build a feature." A new
`code-feature.v1` (or `code-fix.v1` for a smaller bug-fix shape) would
declare phases `[design, implementation, test, review, ship]` plus
deliverables `[spec-doc (typed), code-bundle, test-results, pr-link]`.

**Cost:** one YAML file (~150 lines, mirroring `research.v1.yaml`'s
shape). Zero chassis impact.

### 3.2 No hero archetype for "PR / review status"

The 8-hero set after v1.0.507 covers research surfaces. Closest fits
for a code project:

- `deliverable_focus` — works if PR description is a typed-doc.
- `recent_artifacts` — works for "binaries + logs from CI."
- `task_milestone_list` — chassis default; usable but generic.

A hypothetical `pr_acceptance` hero would mirror `paper_acceptance`:
PR description sections (summary / changes / migration / risks) +
acceptance criteria (tests / review / CI) + merge gate. **ADR-024 D2's
closed-enum rule means this is an APK change, not a config change.**
The cost is known (one Dart class + registry slot + dispatch line +
hub enum + regression test, mirroring the v1.0.504 `experiment_dash`
inline embed wedge).

For a seed-demo-only proof, `deliverable_focus` is *good enough*.

### 3.3 Acceptance-criterion taxonomy is metric-shaped

Today's criterion kinds + gate evaluators:

| Kind | Gate evaluators today |
|---|---|
| `metric` | `eval_accuracy >= threshold` etc. (numeric comparisons) |
| `gate` | `deliverable.ratified` (hub-side) |
| `text` | manual `mark-met` / `mark-failed` / `waive` |

For code, you want:
- `gate: ci.green` — "tests passed on the linked CI run"
- `gate: review.approved` — "the linked review is in approved state"
- `gate: lint.clean` — "no linter errors in the linked artifact"

**None exist today.** They'd need hub-side evaluators with hooks /
webhook intake. For a seed demo, hardcoded `state: met` with
`evidenceRef: "ci:green:run-abc"` works — the seed lies cleanly the
same way it lies about real metric results today.

### 3.4 No diff viewer

The biggest content gap for real review work. `code-bundle` shows
files as full content, not diffs. Two paths:

- **Extend `code-bundle`** with optional per-file `hunks` field
  (additions / deletions / context). Same viewer renders both shapes.
- **Add `code-diff` as the 12th closed-set artifact kind.** Mirrors
  the existing artifact-type-registry plan's expansion pattern.

Either is a separate plan. For a seed demo, attaching a `code-bundle`
of the final files (no diffs) is honest if labelled as such.

### 3.5 No build-status run-metrics shape

`run_metrics` are real-valued time series (loss, accuracy, throughput).
A CI build is discrete states `{pending, running, success, failed}`
across stages. The mobile sparkline renderer doesn't fit. Shoehorning
into `tabular` is a poor fit; a `build_stages` artifact kind (or
repurposing the dead-letter `diagram` slug, which has no viewer yet) is
the honest path. **Out of seed-demo scope.**

---

## 4. What's missing structurally (≈ 5%)

Real architectural gaps. None block a seed demo, but a production code
workflow would have to close them.

### 4.1 The hub is repo-naive

No `repos` table. No per-team Git config. `commit` is a string URL on
a deliverable component — useful as a pointer, opaque otherwise. You
can't ask "which commits target project X?" or "what's the head of the
project's repo?" without crawling deliverables.

Research projects don't need this (one repo per project, named in the
`goal` text). Code projects orbit it. The data-model honesty here is
that **code-building is fundamentally a repo-shaped vertical and the
hub today doesn't reflect that.**

### 4.2 No code-worker agent template

Agent templates today: `agents.steward`, `worker.ml.v1`, `briefing.v1`,
plus per-engine adapters. None say "decompose a code feature into N PRs
+ dispatch them." Code workers are what `claude-code` / `gemini` /
`codex` do natively when called as engines — but **the steward has no
domain prompt for code work**. Adding `worker.code.v1` is a markdown
file (the steward decomposition recipe pattern) and a YAML agent
template. Cheap.

### 4.3 Permission model is research-shaped

Today's load-bearing gates: "agent ratifies deliverable" + "director
ratifies." For code, the canonical gate is **"merge to main"** — a
destructive action on shared state outside the hub. The hub has no
concept of "approval to merge." Modelling it as a `kind: human_decision`
plan step + an out-of-band push works but is clunky.

### 4.4 No git semantics in the wire protocol

OpenAPI surface has zero verbs for `git`. Everything's at the
file-content level via blob URIs. A real code workflow needs at
minimum:

- Push a diff (verb: post a `code-diff` artifact with parent commit).
- Read repo state (verb: list commits, resolve refs, fetch file at
  rev).
- Open a PR (verb: create a `pr-link` with provider metadata).

All are post-MVP. A seed demo seeds the *outputs* of these verbs
(static commit URLs, static diff bundles); production support needs
the verbs themselves plus a GitHub / GitLab integration plane.

---

## 5. The seed-demo recommendation (no MVP scope creep)

**Goal:** prove fitness; don't promise production.

### 5.1 What the seed produces

One additional project alongside the existing 5-project lifecycle
portfolio:

| Field | Value |
|---|---|
| Project name | `code-feature-demo` |
| Template | `code-feature.v1` (new YAML) |
| Phases | `[design, implementation, test, review, ship]` |
| Phase staged at | `review` (so most evidence is visible up-front) |
| Hero | `deliverable_focus` for design + implementation + test + review; `task_milestone_list` for ship |
| Deliverables | spec-doc (typed, ratified) · code-bundle artifact (mock files) · test-results (tabular, all-green) · pr-link (commit URL pointing at a real public PR on a sample repo, e.g. nanoGPT) |
| Criteria | spec-ratified (gate, met) · tests-pass (text, `state: met`, evidence "ci:green:run-abc") · review-approved (text, `state: met`, evidence "github:approved") · ship-merged (text, `pending` — the open decision) |
| Tasks | 4-5 tasks across `todo`/`in_progress`/`done`, mirroring the lifecycle seed's pattern |

### 5.2 What the demo proves

- **Same chassis hosts both verticals.** A second template + a second
  seed function produces a recognisable code-domain mobile UI with
  zero chassis changes.
- **Same heroes work.** `deliverable_focus` rendering a PR
  description's section list is identical to it rendering a research
  method-doc's section list.
- **Same plumbing works.** Plans, tasks, criteria, artifacts, audit
  events — all generic.

### 5.3 What the demo lies about (label clearly)

- **No real diff view.** Reviewers see whole files, not diffs.
- **No live CI evaluator.** `tests-pass` is hardcoded `state: met`.
- **No live merge gate.** "Ship" phase has a `human_decision` plan
  step pointing at an external PR URL; the hub records "shipped" but
  the merge happens elsewhere.

A short README inside the seed function comments these lies
explicitly so reviewers don't infer production-readiness.

### 5.4 Effort estimate

Roughly equivalent to the ablation-retirement wedge (W4+W5 of
multi-run-experiment-phase):

- 1 template YAML (~150 LOC)
- 1 seed function in `seed_demo_lifecycle.go` (~200 LOC)
- 1 steward decomposition recipe in `steward.v1.md` (~80 LOC)
- 3 LOC of mobile registry test (template name in the no-op
  template-resolution assertion)
- 1 changelog entry, 1 plan doc, no ADR amendments

**Zero new heroes. Zero new artifact kinds. Zero new criterion kinds.**

The seed function reuses existing helpers — `seedTypedDocument`,
`seedCodeBundleArtifact`, `seedRun`, `seedDeliverables`,
`seedCriteria`.

### 5.5 Why this is worth doing even outside MVP

Two reasons:

1. **De-risk the "domain pack" framing.** Discussions like
   [`post-mvp-domain-packs.md`](post-mvp-domain-packs.md) claim the
   system is multi-vertical by design. Until a second vertical's seed
   demo runs alongside the first, that's an assertion, not a
   demonstration.
2. **Surface the gaps from §3 + §4 concretely.** Building the seed
   forces every fudge ("no diff view" / "fake CI evaluator" / "no
   merge gate") into a code comment. Each comment is a candidate
   future plan, written exactly when fresh in someone's mind.

---

## 6. Beyond the seed: what production code support would need

A separate, later conversation. Listed here so the gap is visible, not
because any of it is in scope.

| Gap | Shape of the fix |
|---|---|
| §3.4 no diff viewer | Extend `code-bundle` or add `code-diff` kind |
| §3.3 no CI evaluator | Hub-side `ci.green` gate + webhook intake (GitHub Actions / GitLab CI) |
| §3.3 no review evaluator | `review.approved` gate + GitHub / GitLab API poller |
| §3.5 no build status shape | `build_stages` artifact kind or `diagram` repurpose |
| §4.1 repo-naive hub | `repos` table; Git config per team; ref resolution endpoint |
| §4.2 no code worker template | `worker.code.v1` agent template + steward recipe |
| §4.3 merge gate | Decide: hub-mediated merge (high blast radius) vs. `human_decision` pointer (current path) |
| §4.4 no git verbs | OpenAPI surface for commits / refs / diffs; provider integrations |
| §3.2 PR-shaped hero | `pr_acceptance` hero (closed-enum APK change, well-bounded) |

Rough sequencing if these became real work:

1. `code-feature.v1` seed (this doc's recommendation) — proves fit.
2. `pr_acceptance` hero + `code-diff` artifact — closes the "show the
   actual change" demo gap.
3. `ci.green` + `review.approved` evaluators + first provider
   integration — closes the "real-world" demo gap.
4. `repos` table + git verbs — closes the "production" gap.

Each step is independently shippable. None requires the next.

---

## 7. Risks of doing the seed demo

- **Mistaken-for-production.** If the seed lands without prominent
  caveats, a reviewer skimming might infer code work is supported.
  Mitigated by §5.3's explicit lie-labelling and a changelog entry
  that says "seed only; production support is a separate plan."
- **Scope creep into a half-built product.** The seed surfaces the
  §3.4 + §4.1 gaps vividly; the pressure to "just add a diff viewer
  while we're here" is real. The mitigation is this doc — explicit
  statement that the seed is fitness-only, and the production fixes
  are separately scoped.
- **Doc rot.** A second seed shape introduces a second set of mobile
  walkthrough docs (how-to/test-code-feature.md ?). One more thing to
  keep current. Mitigated by keeping it minimal at first — no
  dedicated walkthrough until the seed has been used in anger.

---

## 8. Open questions

- **OQ-1.** Should the seed live alongside lifecycle in `seed-demo
  --shape lifecycle`, or as a new `seed-demo --shape code` flag? Splitting
  shapes by demo-domain mirrors the v1.0.507 design (one shape per
  domain); bundling under lifecycle mirrors the "portfolio of demo
  projects" framing. **Recommendation:** new `--shape code` flag — it
  composes naturally with the existing dispatcher in `runSeedDemo` and
  keeps the two verticals' seeds independently testable.
- **OQ-2.** Does the `code-feature.v1` template's `ship` phase need a
  hero, or is `task_milestone_list` (chassis default) enough? **Lean:**
  default is enough for the seed. The `ship` phase is mostly a
  human_decision plan step; the tile strip + tasks tab carry the
  weight.
- **OQ-3.** Do we want the seed's PR-link to point at a real public
  GitHub URL (e.g. a known nanoGPT PR) or a synthetic
  `https://example.com/pr/123`? **Lean:** real public URL —
  reviewers can tap-through to a real GitHub PR and immediately
  understand the artifact's intent. Privacy / link-rot are minimal
  for well-known public repos.
- **OQ-4.** Should `pr_acceptance` be added as a new hero in the same
  wedge as the seed, or deferred? **Lean:** deferred. The seed's job
  is to prove the existing heroes are general enough; adding a new
  one *in the same wedge* would undermine the point. Save it for a
  separate plan if a real user complains that `deliverable_focus`
  isn't enough.
- **OQ-5.** Memory pattern around per-vertical seed-demo: the project
  memory's "active state" rotates fast. Should each vertical demo
  shape become its own steward recipe + steward.v1.md section, or do
  we keep one steward template and let it pivot via the project
  template's `goal` text? **Lean:** the latter — one steward, many
  recipes. Matches today's design (steward has separate sections for
  write-memo / reproduce-paper / etc.).

---

## 9. Verdict + next move

**Verdict.** The architecture + UI framework is fit for code-building
at the *seed-demo level*. The chassis is repo-naive and diff-naive at
the *production level*; closing those gaps is a separate, well-shaped
sequence of post-MVP plans (§6).

**Next move (when prioritised).** Lift §5 into its own plan doc
(`docs/plans/code-feature-seed-demo.md`) with a 1-wedge breakdown
mirroring the multi-run-experiment-phase plan's structure. Implement
when the principal greenlights it as a fit-proof investment outside
the MVP critical path.

No commitment from this discussion — the doc captures the review and
the recommendation so the conversation doesn't have to be rebuilt
from scratch next time.
