# Demo script — research lifecycle walkthrough

> **Type:** plan
> **Status:** Draft (2026-05-05) — script not yet rehearsed; pending wedge ship. Needs a sweep through after v1.0.507's `--shape ablation` retirement: the §7.1/§7.2 ablation-grid references should be re-cast as the experiment-phase N-run sweep that `seed-demo --shape lifecycle` now produces natively. The demo arc itself doesn't change — the same multi-series chart story still plays — only the seed flow + project name.
> **Audience:** contributors · demo operators · narrators
> **Last verified vs code:** v1.0.351

**TL;DR.** Time-budgeted script for the 5-phase research-lifecycle
demo (idea → lit-review → method → experiment → paper). Targets a
**12-minute end-to-end runtime** with three YAML-in-narration
checkpoints that surface chassis-vs-template legibility per the
2026-05-05 §4.4 demo strategy. Pre-seeds the project's content up
through Lit-review's third section + Method's first three sections +
Experiment's runs and artifacts; live actions are tap-through, three
section ratifications, two steward-direct sessions (mocked for time),
three phase advances, and three YAML reveals. Hardware path: real GPU
host preferred; falls back to seed-demo + mock-trainer harness
(v1.0.169 + v1.0.170) for GPU-less environments. The demo doubles as
the dress-rehearsal target for wedges W1–W7 of the project-lifecycle
plan.

---

## 1. Why this plan exists

Wedges W1–W7 are scheduled but won't acceptance-pass until they hold
together as a single director-flow. This script is that flow — the
narrative arc reviewers see, with explicit attention to the chassis-
generality moments. Building toward this script during implementation
lets each wedge define "done" as "this beat lands" rather than "the
unit tests pass". It also surfaces spec gaps that A1–A6's static
review misses.

The script supports three uses:

1. **Demo execution** — the actual 12-min walkthrough on demo day.
2. **Dress-rehearsal target** — sanity-check after every wedge ship.
3. **Engineering anchor** — wedge acceptance ties to "this beat works
   end-to-end on a real device".

---

## 2. Audience + framing

Two simultaneous audiences (per discussion §4.4):

- **Director persona** — does the system *work*? Phone-shaped UX,
  glanceable surfaces, sensible primary actions.
- **Reviewer / evaluator** — is this just a research tool, or a
  generic platform? Chassis-vs-template seam is the answer; demo
  surfaces it without saying "we have a chassis".

Voice-over carries the second framing without breaking the first.
The director's actions are *natural* (no on-stage configuration); the
reviewer's questions are *answered* (YAML moments, primitive recap).

---

## 3. Time budget + segments

```
00:00 ─ 01:00   Setup / framing
01:00 ─ 02:00   Project creation + Idea phase           [YAML reveal #1]
02:00 ─ 04:00   Lit-review phase
04:00 ─ 07:00   Method phase                            [YAML reveal #2]
07:00 ─ 10:00   Experiment phase                        [YAML reveal #3]
10:00 ─ 11:30   Paper phase
11:30 ─ 12:00   Closing / chassis recap
```

**Stretch / contraction:** the segments compose into a 5-min "elevator"
demo (Setup + skip-to-Experiment + Closing) and a 20-min "deep-dive"
(add steward-prompt overlay + worker spawn + ratify-prompt walk-through
in Experiment). Default is 12 min.

---

## 4. Demo project's topic

Topic-agnostic at the chassis level; this script picks a concrete
topic for narrative continuity:

> **"How does dropout rate affect generalization in a 50M-parameter
> transformer language model trained on WikiText-103?"**

Rationale:
- Specific, falsifiable research question
- Clear single-metric evaluation (validation perplexity)
- Tractable ablation grid (5 dropout values: 0.0 / 0.1 / 0.2 / 0.3 /
  0.5) — fits a mock-trainer or a small real GPU run in <30 min
- Real prior work to cite (Vaswani 2017, Srivastava 2014, Raffel 2020,
  …) — Lit-review has substance
- Likely produces a clear curve — Experiment's results section has
  something to say
- Familiar to ML reviewers; not so esoteric that the audience needs
  to learn the field

Other candidates (drop-in if the dropout topic is overused at the
event): pre-norm vs post-norm transformer stability; data-augmentation
ablations on small image classifiers; rope-vs-alibi positional
encoding in small LMs.

---

## 5. Pre-seed checklist

Data state before the demo starts. All seeded via the existing
seed-demo harness (extended for the lifecycle wedges).

### 5.1 Project + steward

- [ ] One existing demo project (lifecycle-shipped baseline) for the
      Me-tab cold-open
- [ ] General steward configured + spawned (already eager per
      ADR-017)

### 5.2 Idea phase

- [ ] Project created from research template (alternative cold-open:
      create live during demo — see §6.1)
- [ ] Project's `goal` field set to the topic statement (§4)
- [ ] One scope memo (free-floating Document) authored by the steward
      summarizing the Idea conversation
- [ ] `scope-ratified` criterion: state=`pending`

### 5.3 Lit-review phase

- [ ] Lit-review-doc Document created with `kind=lit-review` +
      `schema_id=research-lit-review-v1`
- [ ] Section states pre-seeded:
  - `domain-overview` → ratified, body authored, ~3 paragraphs
  - `prior-work` → ratified, body authored, 6 citations
  - `gaps` → draft, body authored, ~2 paragraphs
  - `positioning` → empty (intentional — director or steward
    completes during demo)
- [ ] Citation list backing the metric criterion — 6 entries
- [ ] `lit-review-ratified` criterion: state=`pending`
- [ ] `min-citations` criterion: state=`met` (already 6 ≥ 5)

### 5.4 Method phase

- [ ] Method-doc Document created with `kind=method` +
      `schema_id=research-method-v1`
- [ ] Section states pre-seeded:
  - `research-question` → ratified
  - `hypothesis` → ratified
  - `approach` → ratified
  - `experimental-setup` → draft, body authored
  - `evaluation-plan` → in-review (intentional — director ratifies
    during demo)
  - `risks` → empty (optional, not required)
  - `budget` → ratified
- [ ] `method-ratified` criterion: pending
- [ ] `evaluation-plan-ratified` criterion: pending
- [ ] `budget-within-cap` criterion: met (manual)

### 5.5 Experiment phase

- [ ] Experiment-results Deliverable created
- [ ] Components bound:
  - Document: `experiment-report` Document with `kind=experiment-results`
  - Artifact: `best-checkpoint` (mock or real, depending on hardware)
  - Artifact: `eval-results.json` (real JSON, mock data acceptable)
  - Run: `ablation-sweep-final` — completed; metrics populated
- [ ] Experiment-report section states:
  - `setup-recap` → ratified
  - `results` → draft, body authored with mock chart data
  - `ablations` → draft (optional)
  - `analysis` → empty (intentional — director directs steward during
    demo)
  - `limitations` → ratified (or empty if optional path)
- [ ] Criteria states:
  - `runs-completed` → met (auto)
  - `best-metric-threshold` → met (auto, current=0.87)
  - `report-results-ratified` → pending
  - `director-reviews` → pending
- [ ] One ratify-prompt attention item posted on Me tab
      ("Steward suggests ratifying — eval criterion just met")

### 5.6 Paper phase

- [ ] Paper-draft Document with `kind=paper-draft` +
      `schema_id=research-paper-draft-v1`
- [ ] Section states pre-seeded — most ratified, with intentional
      gap:
  - `abstract` → in-review (director ratifies during demo)
  - `introduction` → ratified
  - `related-work` → ratified
  - `method` → ratified (cross-ref to method-doc)
  - `experiments` → ratified
  - `results` → ratified (cross-ref to experiment-report)
  - `discussion` → ratified
  - `conclusion` → ratified
  - `references` → ratified
- [ ] `paper-sections-all-ratified` criterion: pending
- [ ] `paper-draft-ratified` criterion: pending

### 5.7 Activity feed

- [ ] Audit events from a plausible 7-day project history exist —
      phase advances, section authoring, run completions. The closing
      shot scrolls through this.

### 5.8 Templates

- [ ] Research template loaded (per A2) at hub startup
- [ ] All 4 section schemas resolved + cached on the demo device

---

## 6. Demo flow (segment-by-segment)

Each segment lists timecode, on-screen actions, narrator voice-over,
and chassis primitives surfaced. **Voice-over** is verbatim suggested
text; adapt as needed.

### 6.1 Setup / framing (00:00–01:00)

**Cold-open:** Me tab visible. Steward FAB pulses (idle).

**Voice:** "Termipod is a mobile-first control plane for AI-orchestrated
research. The director directs; agents execute. We'll watch a real
research project unfold across five lifecycle phases — idea, lit-review,
method, experiment, paper — all on a phone. Twelve minutes."

**Action:** Tap general-steward FAB. A pre-canned 1-turn exchange
opens:
- Director: "Start a new project on dropout rates in transformers."
- General steward: "Got it. Research project, dropout in 50M
  transformer LMs on WikiText-103. Spawning the project steward.
  Open it?"
- Director: tap "Open it"

**On-screen:** navigation transitions to Project Detail with phase
ribbon visible at top: **Idea (current)** | Lit-rev | Method | Exp |
Paper.

**Chassis primitives surfaced:**
- Phase ribbon (W1)
- General steward / project steward distinction (§6 of discussion)
- Project Detail's lifecycle-aware Overview

### 6.2 Project creation reveal — YAML moment #1 (01:00–02:00)

**Action:** Tap the project's kind chip in AppBar; an info sheet
opens showing template metadata. Tap "View template YAML" affordance.

**On-screen:** modal sheet with the research template's YAML,
scrolled to the `phases:` block. Five phases highlighted.

**Voice:** "Here's the chassis declaring five phases. Each phase
declares its deliverables and criteria. None of this is hardcoded —
the chassis renders any template that conforms to this schema. A
product team would have a different YAML; the chassis is the same."

**Action:** dismiss sheet.

**Action:** tap Idea phase (already current) → phase-summary screen
opens (A5 §3, N==0 case). Shows the Idea-phase scope memo and the
single text criterion `scope-ratified`.

**Voice:** "Idea phase is conversation-only. The steward distilled
this scope memo from a chat the director had earlier. No formal
deliverable; just shared understanding plus one explicit criterion."

**Action:** tap "Mark scope ratified" → confirm dialog → ratify.

**On-screen:** phase ribbon updates: Idea ✓ done, Lit-rev becomes
current. A non-blocking banner: "✨ Phase ready to advance to
Lit-review." Tap "Advance phase".

**Chassis primitives surfaced:**
- Template YAML as data (A2)
- Phase-summary screen for N==0 deliverable case (A5 §3)
- Text criterion (D5)
- Explicit phase advance per §B.5 (no auto)

### 6.3 Lit-review phase (02:00–04:00)

**On-screen:** Project Detail Overview re-renders for Lit-review
phase. Hero shows the lit-review-doc deliverable card; tiles show
[References, Documents].

**Voice:** "Lit-review phase produces a structured document. Sections
are first-class — each has its own state. This is the chassis primitive
for any phase that produces text-heavy work."

**Action:** tap deliverable card → A5 deliverable viewer opens.

**On-screen:** Components panel (1 of 1 ready: lit-review document
card showing 4 sections · 2 ratified · 1 draft · 1 empty); criteria
panel (1 of 2 met: lit-review-ratified pending, min-citations met
auto).

**Action:** tap document component → A4 section index opens.

**On-screen:** 4 section rows with state pips:
- ● Domain overview · ratified · 2h ago
- ● Prior work · ratified · 2h ago
- ◐ Gaps · draft · 5m ago
- ○ Positioning · empty

**Voice:** "Director can read each section, redline it, or direct the
steward to draft. Let's complete the empty section."

**Action:** tap Positioning (empty) → section detail screen. Empty-
state card shows "[Direct steward to draft]" + "[Write manually]".
Tap "Direct steward".

**On-screen:** section-targeted steward session opens. Pre-canned
2-turn exchange:
- Director: "Position this project against Raffel 2020's findings."
- Steward: drafts a paragraph; "Saved as draft. Review when ready."

**Action:** back to section detail. Body now populated; state = draft.

**Voice:** "Drafted. Director ratifies."

**Action:** tap Ratify → confirm → state = ratified.

**Action:** back to A4 index → 3 ratified, 1 draft (Gaps). Tap Gaps
→ tap Ratify → state = ratified.

**On-screen:** A4 index shows all 4 sections ratified. Back to A5.

**On-screen:** A5 components panel updates ("4 of 4 ratified");
criteria panel: lit-review-ratified now auto-met via gate; both
criteria met. Action bar's "Ratify deliverable" button is now
enabled (previously disabled with "1 required criterion still
pending" hint).

**Action:** tap Ratify deliverable → confirm.

**On-screen:** deliverable state → ratified. Phase advance banner
appears: "✨ Phase ready to advance to Method." Tap.

**Chassis primitives surfaced:**
- Document with section schema (D7, A4)
- 3-state pip (empty / draft / ratified)
- Section-targeted steward session (A3 §7.4)
- Gate criterion (D5/D9)
- Deliverable viewer (A5)
- Automatic gate criterion firing on deliverable ratification
- Explicit phase advance after deliverable ratification

### 6.4 Method phase — YAML moment #2 (04:00–07:00)

**On-screen:** Project Detail re-renders for Method phase. Hero shows
method-doc deliverable card.

**Voice:** "The Method document IS the proposal. Same chassis primitives
— Document, Sections, Deliverable, Criteria. The template just names
them 'Method' instead of 'Proposal'. A grant proposal would use this
exact same chassis."

**Action:** tap an info icon next to "Method" in the deliverable card
→ schema reveal sheet.

**On-screen:** YAML showing `method-sections` schema (research-
method-v1) with 7 sections.

**Voice:** "These sections — research-question, hypothesis, approach,
experimental-setup, evaluation-plan, risks, budget — are template-
declared. The chassis just renders sections with their states. The
chassis doesn't know what 'method' means; the template does."

**Action:** dismiss sheet. Tap deliverable → A5.

**On-screen:** Components: method-doc (1 of 1, 5 ratified · 1 in-
review · 1 empty optional). Criteria: 1 of 3 met (budget-within-cap
met manual; method-ratified + evaluation-plan-ratified pending).

**Action:** tap document → A4 index.

**On-screen:** 7 section rows, evaluation-plan in-review.

**Action:** tap evaluation-plan → section detail. Body shows the
threshold prose: "We claim a successful method if validation
perplexity decreases by ≥5% relative to the dropout=0.0 baseline."

**Voice:** "Notice: this evaluation criterion will become a metric
criterion in the next phase. Continuity across phases is built into
the chassis."

**Action:** Director reads briefly, taps Ratify (since they're
satisfied with the threshold).

**On-screen:** state → ratified. evaluation-plan-ratified gate
criterion auto-fires (uses `all-sections-ratified` gate kind).

**Action:** back to A5. 5 of 5 required sections now ratified.
"Ratify deliverable" button enabled.

**Action:** tap Ratify deliverable → confirm.

**On-screen:** deliverable ratified. method-ratified gate fires. All
3 criteria met. Phase advance banner. Tap.

**Chassis primitives surfaced:**
- Section schema (D7) declaring method-doc's 7 sections
- All-sections-ratified gate (D5/D9, A2 §8.3 gate library)
- Cascade: section ratify → gate criterion auto-met → deliverable
  ratifiable
- "Method as proposal" chassis-generality argument

### 6.5 Experiment phase — YAML moment #3 (07:00–10:00)

**On-screen:** Project Detail re-renders for Experiment phase. Hero
shows the experiment-results deliverable card with metric strip:
"validation perplexity 0.87 (≥ 0.85 threshold)".

**Voice:** "Now the chassis range shows up. Experiment-results is
ONE deliverable with FOUR components — a document, two artifacts,
and a run. Three different criterion kinds — gate, metric, text.
All in one phase."

**Action:** tap deliverable card → A5.

**On-screen:** Components panel:
- 📄 Experiment Report — 5 sections · 3 ratified · 1 draft · 1 empty
- 📦 best-checkpoint — model · 245 MB · committed
- 📦 eval-results.json — 12 KB · committed
- 🏃 ablation-sweep-final — completed · 27m · perplexity=0.87

**Action:** tap criteria panel header → schema reveal sheet.

**On-screen:** YAML showing the four criterion specs side by side:
- gate (`runs-completed`)
- metric (`best-metric-threshold` with operator+threshold+evaluation)
- gate (`report-results-ratified` with all-sections-ratified)
- text (`director-reviews`)

**Voice:** "Three criterion kinds — gate, metric, text. The chassis
defines the kinds; the template populates them. The metric here is
research-specific — perplexity threshold — but the chassis isn't.
A product launch would use the same kinds for different metrics."

**Action:** dismiss sheet.

**On-screen:** criteria panel:
- ✓ Runs completed without error · auto · met
- ✓ Best perplexity ≥ 0.85 · auto · current 0.87 · met
- ✗ Results section ratified · pending
- ✗ Director reviews · pending

**Voice:** "The metric criterion fired automatically when the run
completed — see the green pip. But the chassis didn't auto-advance
the phase. It posted a ratify-prompt attention item to the director's
Me tab. Director still gates the phase explicitly."

**Action:** tap document component → A4 index.

**Action:** tap Analysis (empty) → section detail. Tap "Direct
steward". Pre-canned exchange:
- Director: "Interpret the results."
- Steward: "Drafts a 3-paragraph analysis comparing the dropout
  values, noting the curve's inflection at p=0.2."

**Action:** back. Section now draft. Tap Ratify → ratified.

**Action:** back to A4 index. Tap Results (draft) → section detail.
Tap Ratify (already authored). State → ratified.

**Action:** back to A5. report-results-ratified gate auto-fires;
3 of 4 criteria met. Tap director-reviews "[mark met]" → confirm.

**On-screen:** all 4 criteria met. Action bar's Ratify deliverable
enabled.

**Action:** tap Ratify deliverable → confirm.

**On-screen:** deliverable ratified. Phase advance banner. Tap.

**Chassis primitives surfaced:**
- Mixed-component Deliverable (D8) — doc + 2 artifacts + 1 run
- All 3 criterion kinds in one panel — chassis range moment
- Metric criterion → ratify-prompt attention item (no auto-advance,
  per §B.5)
- Section-targeted distillation in a different deliverable kind

### 6.6 Paper phase (10:00–11:30)

**On-screen:** Project Detail re-renders for Paper phase. Hero shows
paper-draft deliverable card.

**Voice:** "Paper draft. Sections reuse content from earlier phases
— the section primitive enables traceability. Method section deeplinks
to the method-doc; results section deeplinks to the experiment-report.
You can verify provenance from the paper draft itself."

**Action:** tap deliverable → A5 → tap document → A4 index.

**On-screen:** 9 section rows, 8 ratified, 1 in-review (abstract).

**Action:** tap abstract → section detail. Show body — a 200-word
abstract.

**Voice:** "Abstract was authored last, after results stabilized.
Director reviews and ratifies."

**Action:** tap Ratify → ratified.

**Action:** back to A5. paper-sections-all-ratified gate auto-fires.
Both criteria met. Tap Ratify deliverable → confirm.

**On-screen:** deliverable ratified. Project complete. A "Project
complete" banner offers Archive / Export options.

**Voice:** "Project complete. Twelve minutes from idea to ratified
paper draft, on a phone, with the director's hands always on the
gate."

**Chassis primitives surfaced:**
- Cross-deliverable section reuse (paper.method ↔ method-doc.method)
- Final-deliverable ratification triggers project closure flow

### 6.7 Closing / chassis recap (11:30–12:00)

**Action:** swipe to Activity tab. Show the project's audit feed
scrolled to highlights — phase advances, section authoring,
deliverable ratifications.

**Voice:** "Five phases. One chassis. Four document section schemas,
each declared in YAML. Three criterion kinds: gate, metric, text.
Two viewer primitives: the Structured Document Viewer and the
Structured Deliverable Viewer. One director, one project steward.
The template carries the research-specific content; the chassis
hosts any project lifecycle."

**Action:** tap to return to Me tab — the demo's closing frame.

**Voice (optional close):** "Termipod. The director's surface for
agent-orchestrated work."

---

## 7. Hardware setup

### 7.1 Preferred: real GPU host

- Hub-registered GPU host (existing host-runner)
- Mock or real ablation-sweep configured to complete in ~25 min
  (can run during the pre-demo soak; demo opens with run completed)
- Real metrics in `eval-results.json`
- Demonstrates the full system end-to-end

### 7.2 Fallback: mock-trainer harness

Per memory: v1.0.169 seed-demo + v1.0.170 mock-trainer cover the
GPU-less path.

- Mock-trainer simulates the ablation grid; emits realistic
  `eval-results.json` shape
- Same UX, simulated metrics
- Indistinguishable from real on the phone (mobile doesn't render
  GPU-side details)

### 7.3 Setup checklist

- [ ] Phone charged ≥ 80%
- [ ] Phone on a known-good Wi-Fi (demo venue's wifi tested)
- [ ] Hub running (real or mocked)
- [ ] All pre-seed data loaded (§5)
- [ ] One backup phone with same state (in case primary phone
      misbehaves)
- [ ] Backup laptop with screen-mirror in case of phone failure

---

## 8. Failure modes + contingency

### 8.1 Steward session fails to open

- **Cause:** hub-side spawn timeout, network drop
- **Contingency:** the demo's two steward-direct moments (§6.3, §6.5)
  are pre-canned exchanges; if the live session fails, narrator
  describes what would happen and skips ahead. Pre-seeded section
  bodies cover the case.

### 8.2 Phase advance rejected

- **Cause:** a criterion the demo expected met is actually pending
  (pre-seed bug)
- **Contingency:** the action-bar disabled state + hint message ("1
  required criterion still pending") becomes a teaching moment —
  narrator explains the criterion, taps "[mark met]" or ratifies the
  missing piece, then advances.

### 8.3 Network drop mid-demo

- **Cause:** wifi flakes
- **Contingency:** cache-first per ADR-006 means the phone keeps
  rendering. Mutating actions (ratify, advance) queue per A4 §10.3 /
  A5 §12.3. Narrator points out the "queued, will sync" indicator —
  another chassis-legibility moment.

### 8.4 Phone unresponsive

- **Cause:** OS-level issue
- **Contingency:** swap to backup phone (§7.3); the seeded state is
  identical.

### 8.5 YAML reveal sheet missing

- **Cause:** the affordance for "View template YAML" / "View schema"
  isn't shipped (a wedge slipped)
- **Contingency:** narrator shows the YAML on a side screen (laptop
  with the spec docs open) and continues. Less polished but still
  conveys the chassis-vs-template seam.

---

## 9. Dress rehearsal cadence

Per the existing dress-rehearsal harness pattern (memory: "Demo
dress-rehearsal harness"):

- **After every wedge ship**, run the segments that wedge touches
  end-to-end. Mark beat passes/fails.
- **Once W1–W6 ship**, full 12-min run-through. Time it.
- **Once W7 ships**, second full run-through with all 5 phases'
  content rendered correctly.
- **Demo-day -7**: full run-through on the actual demo phone +
  network.
- **Demo-day -1**: full run-through with the audience-side projector
  / screen-mirror confirmed.

Each rehearsal logs to `docs/plans/demo-rehearsal-log.md` (TBD) with
date, environment, beat-by-beat pass/fail, and follow-ups.

---

## 10. Success criteria (input to C3)

For the demo to "land" with both audiences, every item below must be
true:

### 10.1 Director-persona checks

- [ ] Cold open to first phase advance happens in ≤ 90s
- [ ] Phase advance UX feels intentional, not magical (every advance
      is explicit director ratification per §B.5)
- [ ] Section state pips are recognizable at glance (no narration
      needed for what they mean)
- [ ] Steward direct sessions feel responsive (mocked turns return in
      ≤ 2s)
- [ ] No on-stage configuration / settings modals
- [ ] Total runtime within ±15s of 12 min

### 10.2 Reviewer-persona checks

- [ ] All 3 YAML moments fire on schedule (§6.2, §6.4, §6.5)
- [ ] Each YAML reveal makes the chassis-vs-template seam visible
      without verbose narration
- [ ] All 4 component kinds appear (document, artifact, run; commit
      kind absent in research demo — narrator can mention if asked)
- [ ] All 3 criterion kinds appear (gate, metric, text)
- [ ] Both viewer primitives are exercised (A4 in 4 phases; A5 in 4
      phases)
- [ ] Director's gate-keeping role is demonstrated explicitly
      (ratify-prompt + explicit advance per §B.5)
- [ ] Reviewer can answer "what would change for a different domain?"
      with "the YAML" without being prompted

### 10.3 Recovery checks

- [ ] If Steward session fails, demo continues without breaking
      narrative
- [ ] If network flakes, demo continues with cached state
- [ ] If a beat slips by >10s, narrator can compensate by trimming a
      later beat (which beats are trimmable: §6.3 fourth section
      ratify, §6.5 second section ratify)

---

## 11. Open prep items

Tracked here so they don't fall through the cracks during wedge
implementation:

1. **YAML reveal affordance** — the "View template YAML" / "View
   schema" button is not in any wedge spec. Add to W3 (steward de-
   buryal) since it's a general info-surface affordance, or split as
   a tiny W3.5 wedge.
2. **Empty-state "Direct steward to draft"** card content (A4 §5.3) —
   referenced in §6.3 but the exact copy + button labels haven't
   been pinned. Pin during W5a build.
3. **Phase advance banner copy** — referenced as "✨ Phase ready to
   advance to Method." across this script but not formally pinned in
   A5. Pin during W1 build.
4. **Mock steward exchanges** — pre-canned content for §6.3, §6.5,
   §6.6. Author + dry-run by demo day -7. Lives in the seed harness.
5. **Demo phone screen recording** — backup against live failure.
   Record once dress-rehearsal passes.
6. **Audience-facing screen mirror** — confirm the mirror tool
   (whatever the venue uses) doesn't strip key animations (state pip
   transitions, action bar enable/disable, banner reveal).
7. **One real device test on Pixel + iPhone** — the demo runs on one
   device but covering both platforms during dress rehearsal catches
   platform-specific glitches.
8. **Backup laptop with spec docs open** — for §8.5 contingency.

---

## 12. Cross-references

- [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  §4.4 — demo positioning strategy
- [`reference/research-template-spec.md`](../reference/research-template-spec.md)
  — content this demo renders (§12 of A6 covers demo positioning)
- [`reference/template-yaml-schema.md`](../reference/template-yaml-schema.md)
  — YAML revealed in §6.2, §6.4, §6.5
- [`reference/structured-document-viewer.md`](../reference/structured-document-viewer.md)
  — A4 used throughout for section authoring
- [`reference/structured-deliverable-viewer.md`](../reference/structured-deliverable-viewer.md)
  — A5 used at every phase boundary
- [`reference/hub-api-deliverables.md`](../reference/hub-api-deliverables.md)
  — endpoints exercised by every action
- [`plans/research-demo-gaps.md`](research-demo-gaps.md) — older demo
  tracker; supersede or fold in once this plan stabilizes
- [`decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md)
  — cache-first behavior surfaced in §8.3
