# Research project template — content spec

> **Type:** reference
> **Status:** Draft (2026-05-06) — research.v1.yaml shipped (lifecycle-mvp W7); per-phase overview_widget swap landed 2026-05-06
> **Audience:** contributors (template authors, hub backend, mobile)
> **Last verified vs code:** v1.0.359

**TL;DR.** Concrete content of the **research project template**, the
demo's primary template. Five phases (Idea → Lit-review → Method →
Experiment → Paper) per ADR-001 (amended). Each phase declares
deliverable kinds, document section schemas, acceptance criteria, and
overview-widget bindings per the YAML schema in
[`template-yaml-schema.md`](template-yaml-schema.md). Method-phase
deliverable is the de-facto **proposal** (research question +
hypothesis + approach + experimental setup + evaluation plan +
budget); lit-review is its prerequisite; experiment + paper are its
fulfillment. Mixed-component deliverable lives in the Experiment phase
(report doc + artifacts + runs), giving the demo its "chassis range"
demonstration alongside doc-only deliverables (D8 demo strategy,
2026-05-05 §4.4). All criteria use kinds from D5/D9
(text / metric / gate); auto-advance is opt-in only — explicit
ratify-prompts otherwise (D6 + §B.5). This file is the **content**;
the YAML *schema* it conforms to is A2.

---

## 1. Why this reference / scope

A2 specifies the YAML schema; A6 (this file) specifies what the
research template's YAML actually contains. Together they define the
demo's lifecycle content. An engineer or demo-narrator reading this
doc gets the research lifecycle as a complete, reviewable artifact —
phase-by-phase deliverables, sections, criteria, transitions, prompt
overlays.

**In scope:**
- Phase set + phase-level metadata
- Per-phase deliverable kinds + components + ratification authority
- Document section schemas (lit-review, method, experiment-report,
  paper-draft)
- Per-phase acceptance criteria specs (text + metric + gate)
- Phase transitions
- Per-phase overview widget bindings + tile sets
- Steward spawn policy + worker template hints
- Per-phase steward prompt overlays (sketches)
- Demo positioning notes (chassis legibility via diversity)

**Out of scope:**
- Specific research topic (template is topic-agnostic; the project's
  `goal` field carries the topic per project)
- Steward base prompt — covered in
  [`steward-templates.md`](steward-templates.md)
- Worker templates — already exist (`lit-reviewer.v1.yaml`, `coder.v1.yaml`,
  `critic.v1.yaml`, `ml-worker.v1.yaml`, `paper-writer.v1.yaml`)
- Hub schema, API, mobile viewers — A1, A3, A4, A5

---

## 2. Template at a glance

```yaml
template: research
format_version: 1
template_version: 1
display_name: "Research project"
description: |
  AI-for-science research lifecycle. The director directs; the steward
  orchestrates a worker fleet across the lifecycle from idea to paper.
  Suitable for ML / DL ablation studies, agent-driven exploration, and
  any project producing a peer-reviewable artifact.

kind: goal

default_overview_widget: portfolio_header
on_create_steward_template: steward.research.v1

# Phases (5)
phases:                       # detailed in §3–§7
  - id: idea           ;  abbrev: Idea     ;  spawn: eager
  - id: lit-review     ;  abbrev: Lit-rev  ;  spawn: eager
  - id: method         ;  abbrev: Method   ;  spawn: eager
  - id: experiment     ;  abbrev: Exp      ;  spawn: eager
  - id: paper          ;  abbrev: Paper    ;  spawn: eager

# Section schemas (referenced by document components below)
section_schemas:              # detailed in §9
  - lit-review-sections
  - method-sections
  - experiment-report-sections
  - paper-draft-sections

# Phase transitions (10)              # detailed in §10
transitions:
  idea→lit-review            (explicit)
  idea→method                (explicit, "skip lit-review for continuation work")
  lit-review→method          (explicit)
  method→experiment          (explicit)
  experiment→paper           (explicit)
  + 5 admin-revert paths

# Steward overlay
steward_prompt_overlays:
  idea:        prompts/research.idea.md
  lit-review:  prompts/research.lit-review.md
  method:      prompts/research.method.md
  experiment:  prompts/research.experiment.md
  paper:       prompts/research.paper.md

# Worker hints (used by the steward; not chassis-enforced)
worker_hints:
  lit-review:  [lit-reviewer.v1]
  method:      [critic.v1]
  experiment:  [ml-worker.v1, coder.v1, critic.v1]
  paper:       [paper-writer.v1, critic.v1]
```

(The above is illustrative; the real YAML is in §3–§10 detail blocks.)

### 2.1 Mapping to generic lifecycle

For reviewers reading this through the generic lifecycle frame
(discussion §2.1):

| Generic phase | Research-template phase(s) | Notes |
|---|---|---|
| Idea | `idea` | One-to-one |
| Initiation (proposal) | `lit-review` + `method` | The Method doc IS the de-facto proposal; lit-review is its prerequisite. NSF/DARPA-style proposals carry both as sections of one document; this template splits them into two phase deliverables for incremental review. |
| Planning | (folded into `method`) | Method doc's evaluation plan + budget sections cover this |
| Execution | `experiment` | Mixed-component deliverable (doc + artifacts + runs) |
| Convergence | (folded into `experiment`'s tail) | Experiment-report ratification |
| Closure | `paper` | Paper draft as final deliverable |

This split is research-typical (academic / grant-writing convention).
A *product* template would map differently (e.g., `prd` deliverable
in the Initiation phase). The chassis is agnostic; this template
chooses.

---

## 3. Phase 1 — Idea

```yaml
- id: idea
  display_name: "Idea"
  abbrev: "Idea"
  overview_widget: idea_conversation
  tiles: []                          # no shortcut tiles; conversation-only
  steward_spawn: eager
  deliverables: []                   # no formal deliverable
  criteria:
    - id: scope-ratified
      kind: text
      body:
        text: "Director ratifies overall scope and direction."
      required: true
      ord: 0
```

**Intent.** Free-form scoping conversation with the project steward.
No formal artifact required to advance — the director ratifies the
scope statement when ready. The steward distills the conversation
into a brief scope memo (a free-floating Document, not a deliverable
component) for record-keeping.

**Director's question at this phase:** "Is this worth doing at all?"

**Steward's job at this phase:**
- Ask probing questions (motivation, novelty, feasibility, audience)
- Surface adjacent prior work informally (no deep lit-review yet)
- Sketch a hypothesis statement
- Flag obvious risks (resource cost, ethical, scope creep)
- Distill the conversation into a 1–2 paragraph scope memo on the
  director's request

**Overview widget `idea_conversation` (chassis primitive, new):**
Foregrounds the steward 1:1 entry point + a "Recent scope memos" list
of distilled conversation summaries. Minimal — most of the work
happens in the steward session, not on the project detail page.

**Mobile UX shape:** Project Detail's Overview is mostly a single
"Direct steward" CTA + the small recent-conversations list. Phase
ribbon shows Idea highlighted; tap-the-current-phase opens the
phase-summary screen (per A5 §3, N==0 case).

---

## 4. Phase 2 — Lit-review

```yaml
- id: lit-review
  display_name: "Literature review"
  abbrev: "Lit-rev"
  overview_widget: deliverable_focus
  tiles: [References, Documents]
  steward_spawn: eager
  deliverables:
    - id: lit-review-doc
      kind: lit-review
      display_name: "Literature review"
      description: |
        Survey of state-of-the-art and adjacent prior work; identifies
        gaps and positions the project's contribution. Authored
        section-by-section with the project steward; ratified as a
        whole when all required sections are ratified.
      ratification_authority: director
      required: true
      components:
        - kind: document
          ref: lit-review-sections
          required: true
  criteria:
    - id: lit-review-ratified
      kind: gate
      body:
        gate: deliverable.ratified
        params: { deliverable_id: lit-review-doc }
      deliverable_ref: lit-review-doc
      required: true
    - id: min-citations
      kind: metric
      body:
        metric: lit_review.citation_count
        operator: ">="
        threshold: 5
        evaluation: auto
      deliverable_ref: lit-review-doc
      required: false                # optional; nice signal but not blocking
```

**Intent.** Build a defensible understanding of the field. Output is
a structured Lit-review document (4 required sections — see §9.1).
Ratification of all sections gates phase advance.

**Director's question:** "Does this project know what's already been done? What gap does it fill?"

**Steward's job:**
- Spawn `lit-reviewer.v1` worker(s) to fetch + summarize prior work
- Author each section section-target via the structured doc viewer
- Produce a citation list with stable refs (DOI/arXiv ID where possible)
- Flag conflicting findings or replication crises
- Optionally compute citation_count for the metric criterion

**Mobile UX shape (active mode):**
- Phase ribbon shows Lit-rev highlighted
- Overview hero: `deliverable_focus` widget — surfaces the lit-review
  deliverable card with section completion bar
- Tiles: References (citations browser), Documents (free-floating
  notes the steward wrote)
- Tap deliverable card → A5 (Structured Deliverable Viewer)
- Tap document component → A4 (Structured Document Viewer)

**Director affordances per section:** Direct steward, Edit, Ratify
(per A4 §5.4).

---

## 5. Phase 3 — Method (the de-facto proposal)

```yaml
- id: method
  display_name: "Method"
  abbrev: "Method"
  overview_widget: deliverable_focus
  tiles: [References, Documents, Plans]
  steward_spawn: eager
  deliverables:
    - id: method-doc
      kind: method
      display_name: "Method"
      description: |
        Formal research proposal — research question, hypothesis,
        approach, experimental setup, evaluation plan, budget, risks.
        This is the artifact the director ratifies before the
        experiment phase commits compute. In academic vocabulary, this
        is the "proposal".
      ratification_authority: director
      required: true
      components:
        - kind: document
          ref: method-sections
          required: true
  criteria:
    - id: method-ratified
      kind: gate
      body:
        gate: deliverable.ratified
        params: { deliverable_id: method-doc }
      deliverable_ref: method-doc
      required: true
    - id: evaluation-plan-ratified
      kind: gate
      body:
        gate: all-sections-ratified
        params:
          document_id: method-doc.documentRef
          required_slugs: [evaluation-plan]
      deliverable_ref: method-doc
      required: true
      ord: 1
    - id: budget-within-cap
      kind: text
      body:
        text: "Budget cap declared; total estimated cost under budget."
      deliverable_ref: method-doc
      required: true
      ord: 2
```

**Intent.** Lock the technical plan. Director reviews and ratifies
the method doc as a whole; this is the **commitment gate** before
compute is spent in the Experiment phase.

**Director's question:** "Is this method sound and defensible?"

**Steward's job:**
- Author each method section, grounded in the lit-review's gaps
  section
- Spawn `critic.v1` workers to red-team the method (find weaknesses,
  propose alternatives)
- Surface trade-offs explicitly (e.g., "use X dataset for stronger
  signal vs Y for cheaper compute")
- Compute a budget estimate based on the experimental setup
- Iterate with the director until ratification

**Mobile UX shape:** identical to §4 (lit-review) — the structured
doc viewer renders the method-doc with its 7 sections (§9.2).

**Why the method doc IS the proposal.** This is the demo's chassis-
generality moment: reviewers familiar with grant proposals (NSF/NIH/
DARPA) see Method-as-proposal here, and the chassis primitives
(Document, Section, Deliverable, Criterion) demonstrate they support
the proposal pattern *without* a "Proposal" hardcoded primitive
(per D3). The same chassis would render a product PRD in a hypothetical
product template, etc.

---

## 6. Phase 4 — Experiment

```yaml
- id: experiment
  display_name: "Experiment"
  abbrev: "Exp"
  overview_widget: experiment_dash
  tiles: [Outputs, Documents, Experiments]
  steward_spawn: eager
  deliverables:
    - id: experiment-results
      kind: experiment-results
      display_name: "Experiment results"
      description: |
        Mixed-component deliverable: experiment report (doc) plus
        the artifacts and runs it summarizes. Ratified when the
        report's required sections are ratified, all runs completed,
        and the metric threshold is met.
      ratification_authority: director
      required: true
      components:
        - kind: document
          ref: experiment-report-sections
          required: true
          ord: 0
        - kind: artifact
          ref: best-checkpoint        # bound at runtime
          required: true
          ord: 1
        - kind: artifact
          ref: eval-results-json      # bound at runtime
          required: true
          ord: 2
        - kind: run
          ref: ablation-sweep-run     # bound at runtime
          required: true
          ord: 3
  criteria:
    - id: runs-completed
      kind: gate
      body:
        gate: runs.completed-without-error
        params: { deliverable_id: experiment-results }
      deliverable_ref: experiment-results
      required: true
      ord: 0
    - id: best-metric-threshold
      kind: metric
      body:
        metric: experiment.eval_accuracy
        operator: ">="
        threshold: 0.85                # template default; project may override
        evaluation: auto
        source_run_filter:
          tag: ablation-final
      deliverable_ref: experiment-results
      required: true
      ord: 1
    - id: report-results-ratified
      kind: gate
      body:
        gate: all-sections-ratified
        params:
          document_id: experiment-report.documentRef
          required_slugs: [results, analysis]
      deliverable_ref: experiment-results
      required: true
      ord: 2
    - id: director-reviews
      kind: text
      body:
        text: "Director reviews experimental outputs and signs off."
      deliverable_ref: experiment-results
      required: true
      ord: 3
```

**Intent.** Run the work, produce evidence, summarize. **Mixed-
component deliverable** — the chassis-range moment: doc + artifacts +
runs in one deliverable, all gated by criteria of three different
kinds (gate / metric / text).

**Director's question:** "What happened? Did it work? What does it mean?"

**Steward's job:**
- Spawn `ml-worker.v1` workers to execute the ablation grid declared
  in the method doc
- Spawn `coder.v1` for any pre/post-processing work
- Track run metrics, populate the artifact references as runs complete
- Author the experiment-report sections (setup-recap, results,
  ablations, analysis, limitations) section-targeted
- Surface metric → ratify-prompt attention items as criteria fire

**Demo highlights:**
- A5's deliverable viewer renders the components panel with three
  card kinds (document, artifact, run) — visually demonstrates that
  D8 isn't doc-only.
- A criterion of every kind (gate, metric, text) sits in the criteria
  panel — visually demonstrates D5/D9.
- Metric criterion fires automatically as runs complete; ratify-prompt
  attention item lands on Me; director taps to ratify (per §B.5).

**Mobile UX shape:**
- Overview hero: `experiment_dash` (new chassis widget) — sweep grid
  status, latest metrics, recent run cards
- Tiles: Outputs (artifact browser), Documents, Experiments (runs)
- Tap deliverable → A5 with all four components rendered

---

## 7. Phase 5 — Paper

```yaml
- id: paper
  display_name: "Paper"
  abbrev: "Paper"
  overview_widget: paper_acceptance
  tiles: [Outputs, Documents]
  steward_spawn: eager
  deliverables:
    - id: paper-draft
      kind: paper-draft
      display_name: "Paper draft"
      description: |
        Workshop-style paper draft synthesizing the method, experiment
        results, and discussion. Ratified by director as the closure
        artifact of the project.
      ratification_authority: director
      required: true
      components:
        - kind: document
          ref: paper-draft-sections
          required: true
  criteria:
    - id: paper-sections-all-ratified
      kind: gate
      body:
        gate: all-sections-ratified
        params:
          document_id: paper-draft.documentRef
          required_slugs:               # all required sections (§9.4)
            - abstract
            - introduction
            - related-work
            - method
            - experiments
            - results
            - discussion
            - conclusion
            - references
      deliverable_ref: paper-draft
      required: true
      ord: 0
    - id: paper-draft-ratified
      kind: gate
      body:
        gate: deliverable.ratified
        params: { deliverable_id: paper-draft }
      deliverable_ref: paper-draft
      required: true
      ord: 1
```

**Intent.** Synthesize and finalize. The Paper draft reuses ratified
content from method-doc and experiment-report (steward stitches them
into paper sections); director reviews and ratifies the whole.

**Director's question:** "Is this paper ready to submit / share?"

**Steward's job:**
- Spawn `paper-writer.v1` worker to assemble draft from existing
  ratified content (method's evaluation plan → paper's experiments
  setup; experiment-report's results → paper's results; etc.)
- Author novel paper-only sections (abstract, introduction,
  discussion, conclusion)
- Spawn `critic.v1` for review and red-teaming
- Format references via the lit-review's bibliography

**Demo highlights:** content-reuse — section bodies in the paper draft
deeplink back to source sections in method-doc / experiment-report
("derived from"). Demonstrates that the section-as-primitive design
enables traceability across deliverables.

**Mobile UX shape:**
- Overview hero: `paper_acceptance` (new chassis widget) — section
  completion progress + a "share/export" prominent CTA on ratification
- Tiles: Outputs, Documents
- A4 renders the paper draft with all 9 sections

---

## 8. Worker template hints (per phase)

The chassis does not enforce which workers a steward spawns; the
project steward chooses based on its prompt + context. This template
provides hints (preferred workers per phase) that the steward consults.

```yaml
worker_hints:
  idea: []                                  # conversation only
  lit-review:
    - lit-reviewer.v1                       # search + summarize prior work
  method:
    - critic.v1                             # red-team the method
  experiment:
    - ml-worker.v1                          # execute ablation grid
    - coder.v1                              # data prep, post-processing
    - critic.v1                             # interpret anomalies
  paper:
    - paper-writer.v1                       # assemble draft
    - critic.v1                             # review draft
```

Worker template files live at `hub/templates/agents/<id>.yaml` per
[`steward-templates.md`](steward-templates.md); not in scope for this
file.

---

## 9. Document section schemas

All four section schemas declared inline in the template's
`section_schemas:` block (per A2 §7).

### 9.1 `lit-review-sections`

```yaml
schema_id: research-lit-review-v1
sections:
  - slug: domain-overview
    title: "Domain overview"
    required: true
    guidance: |
      What domain is this and why does it matter now? 1-2 paragraphs
      situating the field for a reviewer outside it.
  - slug: prior-work
    title: "Key prior work"
    required: true
    guidance: |
      Cite-as-#N. Cluster the citations into 2-4 themes; each theme
      gets 1-3 paragraphs summarizing the contribution and limits.
  - slug: gaps
    title: "Research gaps"
    required: true
    guidance: |
      What's missing or unsolved that motivates this project? Be
      specific — "this hasn't been tried with X-class models" or
      "the only existing benchmark is in Y domain".
  - slug: positioning
    title: "Project positioning"
    required: true
    guidance: |
      How does this project relate to / advance the prior work?
      Single paragraph; commit to a contribution claim.
```

### 9.2 `method-sections`

```yaml
schema_id: research-method-v1
sections:
  - slug: research-question
    title: "Research question"
    required: true
    guidance: |
      One sentence stating the falsifiable question this project
      answers.
  - slug: hypothesis
    title: "Hypothesis"
    required: true
    guidance: |
      What do we expect to find, and why? Tie expectation to the
      gaps section of the lit-review.
  - slug: approach
    title: "Approach"
    required: true
    guidance: |
      High-level method; key technical decisions with rationale. 2-3
      paragraphs.
  - slug: experimental-setup
    title: "Experimental setup"
    required: true
    guidance: |
      Datasets, models, hardware, hyperparameters, training schedule,
      metrics. Concrete enough to reproduce.
  - slug: evaluation-plan
    title: "Evaluation plan"
    required: true
    guidance: |
      How will we measure success? Acceptance thresholds (which become
      metric criteria in the experiment phase). State the falsification
      condition explicitly.
  - slug: risks
    title: "Risks"
    required: false
    guidance: |
      What could go wrong; what's our fallback. Examples: compute
      overrun, dataset unavailable, hypothesis trivially confirmed
      and uninteresting.
  - slug: budget
    title: "Budget"
    required: true
    guidance: |
      Compute budget (GPU-hours), wall-clock time budget, headcount.
      Used by the steward to populate the project's budget_cents.
```

### 9.3 `experiment-report-sections`

```yaml
schema_id: research-experiment-report-v1
sections:
  - slug: setup-recap
    title: "Setup recap"
    required: true
    guidance: |
      Quick reminder of what was run. Reference the method-doc's
      experimental-setup section; do not duplicate.
  - slug: results
    title: "Results"
    required: true
    guidance: |
      Main findings. Charts and tables. Quantitative claims with
      confidence intervals.
  - slug: ablations
    title: "Ablations"
    required: false
    guidance: |
      Variant comparisons. Often the most informative section for
      reviewers.
  - slug: analysis
    title: "Analysis"
    required: true
    guidance: |
      Interpretation: what do the results mean? Are they consistent
      with the hypothesis? What caveats?
  - slug: limitations
    title: "Limitations"
    required: false
    guidance: |
      What we couldn't address; what would be needed for a stronger
      claim.
```

### 9.4 `paper-draft-sections`

```yaml
schema_id: research-paper-draft-v1
sections:
  - slug: abstract
    title: "Abstract"
    required: true
    guidance: |
      150-250 words. Problem, method, headline result, implication.
  - slug: introduction
    title: "Introduction"
    required: true
  - slug: related-work
    title: "Related work"
    required: true
    guidance: |
      Re-cast from the lit-review's prior-work section. Steward
      adapts tone for paper readers.
  - slug: method
    title: "Method"
    required: true
    guidance: |
      Re-cast from method-doc's approach + experimental-setup. Tighter
      than the proposal version.
  - slug: experiments
    title: "Experiments"
    required: true
    guidance: |
      Setup detail; can reference experiment-report's setup-recap.
  - slug: results
    title: "Results"
    required: true
    guidance: |
      Main quantitative results; figures from experiment-report.
  - slug: discussion
    title: "Discussion"
    required: true
    guidance: |
      Interpretation, comparison to prior work, limitations.
  - slug: conclusion
    title: "Conclusion"
    required: true
  - slug: references
    title: "References"
    required: true
    guidance: |
      Auto-populated from lit-review citation list + any added during
      experiment phase.
```

---

## 10. Phase transitions

```yaml
transitions:
  # Forward path (explicit, default)
  - { from: idea,        to: lit-review,  mode: explicit }
  - { from: idea,        to: method,      mode: explicit }   # skip lit-review
                                                              # for continuation work
                                                              # per D4 (skippable)
  - { from: lit-review,  to: method,      mode: explicit }
  - { from: method,      to: experiment,  mode: explicit }
  - { from: experiment,  to: paper,       mode: explicit }

  # Admin revert paths (rare; require admin scope)
  - { from: lit-review,  to: idea,        mode: explicit, admin_only: true }
  - { from: method,      to: lit-review,  mode: explicit, admin_only: true }
  - { from: experiment,  to: method,      mode: explicit, admin_only: true }
  - { from: paper,       to: experiment,  mode: explicit, admin_only: true }
```

All transitions are **explicit** by default per the 2026-05-05 §B.5
closure (ratify-prompt, no auto-advance). The `experiment → paper`
auto candidate is *not* enabled in MVP — preserves the director's
explicit gate-keeping role for the demo.

`admin_only` is a chassis-extension flag (proposed; not yet in A2's
schema) — captured here as a known gap for the wedge implementation.
For MVP, revert paths are accessible only via direct hub admin
endpoints; UI-level admin role gating is post-MVP.

---

## 11. Steward prompt overlays (sketches)

Per A2 §13 + steward-templates.md, each phase appends a markdown
overlay to the base steward prompt. Files live at
`hub/templates/projects/prompts/research.<phase>.md`.

Sketches (full prompts not in this spec — they ship in the markdown
files):

### `research.idea.md`

- Frame: free-form scoping; the director may not yet know what they
  want.
- Ask probing questions about motivation, novelty, feasibility, and
  expected audience.
- Surface adjacent prior work informally; do NOT do a deep lit-review
  yet (that's the next phase).
- Sketch hypothesis statements; offer 2-3 alternatives.
- Flag obvious risks early.
- On director's request, distill the conversation into a 1-2 paragraph
  scope memo (a free-floating Document, not a deliverable component).
- Do NOT spawn workers in this phase; conversation only.

### `research.lit-review.md`

- Frame: build a defensible understanding of the field; output is the
  Lit-review document with 4 required sections.
- Spawn `lit-reviewer.v1` workers with bounded tasks ("survey
  regularization techniques in transformer LMs, 2020-2025, return
  10 papers with summaries").
- Author lit-review-doc sections section-by-section. Use the schema's
  guidance text as the section's prompt scope.
- Maintain a citation list with stable refs (DOI / arXiv).
- Flag conflicting findings or replication crises explicitly.
- Compute citation_count for the optional metric criterion.

### `research.method.md`

- Frame: lock the technical plan. This is the commitment gate before
  compute is spent.
- Ground all sections in lit-review's gaps section — explicitly cite
  which gap each design decision addresses.
- Spawn `critic.v1` workers to red-team the method (find weaknesses,
  propose alternatives, stress-test the hypothesis).
- Surface trade-offs explicitly (e.g., "use X dataset for stronger
  signal vs Y for cheaper compute") and let the director choose.
- Estimate budget concretely (GPU-hours × $/GPU-hour); populate the
  project's budget_cents accordingly.
- Iterate with the director until they ratify the method-doc as a
  whole.

### `research.experiment.md`

- Frame: run the experiments declared in the method's experimental-
  setup section; produce the experiment-results deliverable.
- Spawn `ml-worker.v1` to execute the ablation grid; track each run's
  metrics and bind successful runs as deliverable components.
- Spawn `coder.v1` for data prep / post-processing.
- Bind artifact references as runs commit checkpoints / eval results.
- Author experiment-report sections (setup-recap, results, ablations,
  analysis, limitations) section-targeted.
- When the metric criterion fires, post a ratify-prompt attention
  item per §B.5 — do NOT silently advance phase.
- Spawn `critic.v1` for any anomalies or unexpected results.

### `research.paper.md`

- Frame: synthesize ratified content from method + experiment-report
  into a workshop-style paper draft.
- Spawn `paper-writer.v1` to assemble draft from existing ratified
  source. Use cross-reference deeplinks to source sections so the
  reader can verify provenance.
- Author novel paper-only sections: abstract (last; written after
  results are stable), introduction, discussion, conclusion.
- Spawn `critic.v1` for review and red-teaming the draft.
- Format references via the lit-review's bibliography section.
- Final ratification by director closes the project.

---

## 12. Demo positioning notes

This template is designed to demonstrate **chassis generality** during
the demo without a second skeleton template (per §4.4 of the
discussion: "diversity within research" + "YAML in narration"
strategy).

### 12.1 Diversity within research

Three component-kind classes appear in the same lifecycle:

- **Doc-only deliverables** — lit-review-doc, method-doc, paper-draft.
  Three different `kind` values (`lit-review`, `method`, `paper-draft`)
  with three different section schemas. Demonstrates that the chassis
  doesn't hardcode a "Proposal" type.
- **Mixed-component deliverable** — experiment-results (doc +
  artifacts + runs). Demonstrates D8's `component_kind` enum range.
- **Three criterion kinds** — text, metric, gate. All three appear in
  Experiment phase. Demonstrates D5/D9's structured-with-text-as-
  subset pattern.

### 12.2 YAML in narration

The demo voice-over surfaces the YAML at three checkpoints:

1. **At project creation** — show the template picker; reveal the
   research template's YAML; voice-over: "Here's the chassis declaring
   five phases. Each phase declares its deliverables and criteria. A
   product template would declare different ones."
2. **At Method ratification** — show the method-sections schema;
   voice-over: "These sections aren't hardcoded; they're declared in
   YAML. A PRD template would declare different sections."
3. **At Experiment phase entry** — show the criterion specs (text
   alongside metric alongside gate); voice-over: "Three criterion
   kinds, all chassis primitives. The metric here is research-specific;
   the chassis isn't."

### 12.3 Director-driven, not steward-driven

Throughout the demo, the **director** is the agent the reviewer is
following. The steward executes; the director ratifies. Every phase
advance is an explicit director ratification (per §B.5 — no
auto-advance in this template). This demonstrates the director-as-
principal mental model the IA is built on.

### 12.4 Existing demo harness

The seed-demo + mock-trainer harness (v1.0.169 + v1.0.170) covers the
GPU-less Experiment phase. For the demo, the Experiment phase can run
against either:

- A real GPU host (preferred; demonstrates the system end-to-end)
- The mock-trainer harness (backup; identical UX, simulated metrics)

Both paths exercise the same chassis primitives.

For mobile-UI dress-rehearsal *across* the lifecycle (not just the
Experiment phase), see `seed-demo --shape lifecycle`. As of v1.0.359
this seeds a five-project portfolio (one project parked at each
phase) so the W7 phase heroes, the W5a/W5b typed-document and
deliverable viewers, and the W6 acceptance-criteria pip vocabulary
can all be inspected without running phases live. The chassis honors
`phase_specs[<phase>].overview_widget` (this section's per-phase
declarations), so each seeded project picks up the phase-appropriate
hero through the project read endpoint. See
[`how-to/run-lifecycle-demo.md`](../how-to/run-lifecycle-demo.md)
§"Lifecycle UI dress-rehearsal" for the row-by-row inventory.

---

## 13. Open follow-ups

1. **`admin_only` transition flag** — proposed in §10 but not yet in
   A2's schema. Needs an A2 amendment when this template ships.
2. **Document cross-reference deeplinks** — paper-draft sections
   should deeplink to method + experiment-report source sections.
   Mobile UX for this is a small extension to A4 (link affordance in
   markdown body); spec when scheduled.
3. **Citation list as first-class entity** — the lit-review's prior-
   work section, paper-draft's references section, and the project's
   reference list overlap. A `citation` entity (or a `references`
   document kind) could unify them; deferred post-MVP.
4. **Worker hints YAML extension** — `worker_hints:` field is proposed
   in §8 but isn't yet in A2's top-level schema. Needs an A2
   amendment.
5. **Project topic vs template** — the template is topic-agnostic; the
   `project.goal` field carries the topic. The steward's first-prompt
   bootstrap reads goal + template, but goal-to-template alignment
   isn't validated. Could surface as a soft warning ("this goal looks
   product-y; consider product template?") post-MVP.
6. **Per-template metric registry** — `experiment.eval_accuracy` is
   the demo's metric path, but real research projects have different
   metrics. Templates could declare the metric path (or expose a
   metric-picker UI) as a project parameter; out of MVP scope.

---

## 14. Cross-references

- [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  — D1–D10 + research-template positioning (§4.4)
- [`reference/template-yaml-schema.md`](template-yaml-schema.md) —
  YAML schema this content conforms to (chassis layer)
- [`reference/project-phase-schema.md`](project-phase-schema.md) — DB
  schema for phases, deliverables, criteria
- [`reference/hub-api-deliverables.md`](hub-api-deliverables.md) —
  HTTP endpoints
- [`reference/structured-document-viewer.md`](structured-document-viewer.md)
  — A4 renders the four document section schemas declared here
- [`reference/structured-deliverable-viewer.md`](structured-deliverable-viewer.md)
  — A5 renders the deliverables declared here
- [`reference/steward-templates.md`](steward-templates.md) —
  steward.research.v1 base prompt + worker templates referenced in §8
- [`decisions/001-locked-candidate-a.md`](../decisions/001-locked-candidate-a.md)
  (amended) — research-demo-lifecycle ADR
- [`discussions/research-demo-lifecycle.md`](../discussions/research-demo-lifecycle.md)
  — earlier discussion that locked the 5-phase shape
