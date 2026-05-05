# Project detail page: lifecycle-aware architecture

> **Type:** discussion
> **Status:** Open (2026-05-05)
> **Audience:** contributors
> **Last verified vs code:** v1.0.351

**TL;DR.** The Project Detail screen is the director's primary
operating surface. Today it's *execution-biased* — the Overview
foregrounds run cadence and task progress, which is correct only for
projects already underway. A director's drill-in is **lifecycle-aware**:
they ask different questions at Idea, Initiation, Planning, Execution,
Convergence, and Closure, and each phase deserves a different chassis
view. The architecture should be **general** (any project type, any
phase model) while letting the **research-MVP demo template** declare
domain-specific specifics (5 phases, typed structured documents,
deliverables with acceptance gates per phase). This doc captures the
lifecycle frame, the gap analysis against the current page, the
chassis-vs-template seam, the steward-layering definition, and **ten
locked design decisions** (D1–D10) that follow from a director-driven
Q&A. The chassis primitive at phase boundaries is the **Deliverable**
(PMBOK term, neutral) — a bundle of typed components (documents,
artifacts, runs, commits) gated by phase-level acceptance criteria.
**Demo scope:** all wedges in (no cut), generic chassis legibility
demonstrated via diversity-within-research + YAML-in-narration (not
a second template). No implementation plan in this doc; the bridge
to a plan is a schema reference + template-YAML reference + two
viewer specs + the research-template spec + a demo script.

---

## 1. Why this discussion exists

Two prompts converged:

1. **The current Overview answers the wrong question for early-phase
   projects.** A project in Initiation has no runs, no artifacts, no
   task burn-down — yet the Overview hero foregrounds those signals.
   The director's actual question is "is the proposal solid?", which
   has no surface today.
2. **Specificity vs generality.** The MVP demo is research-domain
   (idea → lit-review → method → experiment → paper). The chassis
   should host that thoroughly *without* hard-coding it, leaving room
   for product / ops / standing-workspace templates without
   re-architecture.

The framing comes from a director-driven Q&A across three sessions in
2026-05. Ten design decisions (§5) flow from that Q&A and are
recorded here pending promotion to ADRs.

---

## 2. The lifecycle frame

### 2.1 Phase taxonomy (cross-validated)

| Tradition | Phases |
|---|---|
| **PMBOK / PMI** | Initiate · Plan · Execute · Monitor · Close |
| **Stage-Gate (Cooper)** | Discovery · Scoping · Build business case · Develop · Test · Launch · Post-launch — each a *gate* |
| **NSF / DARPA grant** | White paper → Full proposal → Review → Contract → Execution → Reporting → Closeout |
| **Academic research** | Idea → Lit review → Proposal → Method → Experiment → Analysis → Write → Submit → Review → Publish |
| **Lean / OKR** | Set objective → Execute → Review → Reset |
| **Termipod demo (locked, ADR-001)** | Idea → Lit-review → Method → Experiment → Paper *(5 phases)* |

Common shape: **bounded phases, each with one or more deliverables
and an acceptance gate**. Director's role is gate-keeper: review what
the steward produced, decide whether to advance.

### 2.2 Director question priority by phase

| Phase | Top question | Top action | Top surface |
|---|---|---|---|
| **Idea** | "Is this worth doing at all?" | Sketch with steward | Steward 1:1 |
| **Initiation** | "Is the proposal solid + defensible?" | Review/redline → ratify | Deliverable viewer (Proposal) |
| **Planning** | "Is the WBS complete? are deliverables + criteria clear?" | Approve plan + acceptance | Deliverable viewer (Plan) |
| **Execution** | "What's happening? what's blocked?" | Direct steward / clear blockers | Activity + steward strip |
| **Convergence** | "Are deliverables ready? gate met?" | Final review | Deliverable viewer (Results) |
| **Closure** | "What did we learn? archive cleanly" | Archive + retrospective | Deliverable viewer (Final report) |

The current Overview chassis is essentially the *Execution-phase view*.
The same screen at Initiation should foreground the proposal
deliverable, not run cadence. At Closure, it should foreground the
final-report deliverable.

### 2.3 Initiation as a worked example

The director's concerns at Initiation map to a **structured
deliverable** (the Proposal), not a properties form:

| Director concern | Form | Schema home |
|---|---|---|
| Project plan | Proposal § Plan | Document component § |
| Current state / SOTA | Proposal § Related work | Document component § |
| Necessity / benefit | Proposal § Motivation | Document component § |
| Key problems / challenges | Proposal § Open problems | Document component § |
| Budget / cost / risks | Proposal § Resources + `budget_cents` | Doc § + project field |
| Method | Proposal § Method | Document component § |
| Sub-tasks / work packages | Plan + sub-projects (WBS) | Plan steps + `parent_project_id` |
| Roadmap / timeline / phases | Plan + phase model | Plan + `projects.phase` |
| Per-phase deliverables | Phase-bound deliverables | `deliverables` (D8) |
| Acceptance criteria / metrics | Per-phase criterion rows | `acceptance_criteria` (D5/D9) |
| Contract / SOW | Ratified Proposal deliverable | Deliverable.ratification_state = ratified |

The Proposal *emerges from dialogue* — many turns with the steward,
but **not as a single 30-page draft**. Sections are authored
independently, ratified independently, and the deliverable advances
the phase when its acceptance criteria are met. Per ADR-009 D7 +
sessions.md §8.5, steward sessions distill into artifacts on close —
in this model, into specific *sections* of a typed document component
of a deliverable. Initiation flow:

```
director opens project (phase=Idea)
  → opens steward 1:1 ("help me scope this")
  → multi-turn refinement
  → director ratifies project scope criterion → phase advances to Initiation
  → opens section-targeted steward session (target=proposal.method)
  → multi-turn refinement
  → distill → updates Method section, state draft → in-review
  → director redlines, ratifies Method section
  → ... iterate over Motivation, SOTA, Risks, Budget ...
  → all required sections ratified
  → director ratifies deliverable
  → all phase acceptance criteria met
  → project.phase ← "Planning"
```

What's missing from the UI today:

- A phase indicator showing where the project is.
- A "Ratify deliverable → advance phase" action.
- An Initiation-phase Overview that surfaces the proposal-in-progress,
  not the (empty) run cadence.
- A **structured Deliverable viewer** (composing a structured Document
  viewer for typed-doc components) — section-aware reading + ratifying.

---

## 3. Audit of the current Project Detail page

### 3.1 What's there

```
AppBar: [Title · KindChip]  TeamSwitcher  Edit  ⋮
Pills:  Overview · Agents · Channel · Tasks · Files
─ Overview (vertical scroll) ─
  ▸ Attention banner (if open >0)
  ▸ Portfolio header card (goal, status, steward, budget, attention,
                            task progress, priority breakdown)
  ▸ Pluggable hero (template-declared overview_widget)
  ▸ 7 ShortcutTiles: Experiments · Reviews · Outputs · Documents ·
                    Schedules · Plans · Assets
  ▸ Divider
  ▸ 8 metadata rows: Name · Kind · Status · Goal · Steward template ·
                    On-create template · ID · Docs root · Created
  ▸ Archive button
```

Notable: `_ActivityView` is defined in the file but **not wired into
the pill bar** — Activity is dead code at this surface.

### 3.2 Director-priority gap analysis

| # | Gap | Severity | Director question affected |
|---|---|---|---|
| 1 | No Activity surface (class exists, not wired) | High | Q3 "what happened?" |
| 2 | Goal is buried + duplicated — 1 line in header, full text in row 4 of metadata | High | Q1 "what's it for?" |
| 3 | Steward presence is too thin — only "configured/not-configured", no liveness, no current task, no last-action timestamp | High | Q4 "what's it doing?" |
| 4 | 7 shortcut tiles is a sprawl — Reviews duplicates the attention banner; Schedules, Plans, Assets are rarely tapped by the director; Experiments is template-relevant only for ML | Med | Q8 cluttered, Q5 hidden |
| 5 | 8 metadata rows are bookkeeping noise between shortcuts and Archive — IDs, paths, template names belong in the edit sheet | Med | none — pure noise |
| 6 | Pill bar mixes views with entity types — Overview is a view, Agents/Tasks/Files are entity types, Channel is a surface kind | Med | mental-model coherence |
| 7 | No "next action" affordance — when no attention items exist, director has no clear primary action | Med | Q5/Q6 stalled empty state |
| 8 | Direct-steward-1:1 is hidden — buried inside the steward chip in the header; should be a primary action | Low-Med | Q6 "talk to my deputy" |
| 9 | Channel tab purpose is ambiguous post-wedge-C — is it team broadcast about this project, or steward direction? IA §6.7 says broadcast, but it's the most-prominent comms tab. *(Resolved per D10: demote to AppBar Discussion icon, free pill slot for Activity.)* | Low-Med | mental-model coherence |
| 10 | Sub-projects family tree visible only via parent breadcrumb + via `children_status` overview widget when template says so. Inconsistent. | Low | Q1 context |
| 11 | **No phase indicator + no phase-aware Overview content + no deliverable viewer** — the entire lifecycle frame is missing | High | All Q's, phase-dependent |

Gaps 1–10 are static (snapshot view). Gap 11 is structural — even if
1–10 are fixed, the page still answers Execution-phase questions only.

---

## 4. Generality vs specificity — the chassis-vs-template seam

### 4.1 What the chassis must do (general, frozen)

- Project + sub-projects (already)
- Documents — gain `kind`, `schema_id`, structured `body`, section state (D7)
- Tasks + milestones (mostly already)
- Plans + schedules (already)
- Agents + runs + artifacts (already)
- Channels + steward sessions (already)
- **Phases + phase transitions** (NEW — D1, D4, D6)
- **Deliverables + deliverable_components** (NEW — D8)
- **Acceptance criteria as phase-keyed structured rows** (NEW — D5, D9)
- **Structured Document Viewer** (NEW chassis primitive — D3, D7)
- **Structured Deliverable Viewer** (NEW chassis primitive — composes the document viewer)
- **Layered stewards** (already, ADR-017) — formalized boundary in §6

### 4.2 What the template / specificity layer declares

Already declarable:

- `template_id` on project
- `overview_widget` slug → registry → renders custom hero
  (`children_status`, `sweep_compare`, `recent_artifacts`,
  `task_milestone_list`, `workspace_overview`)
- `on_create_template_id` for steward bootstrap

Should extend to:

- **Phase set** — research template declares
  `[idea, lit-review, method, experiment, paper]`; product template
  declares different; standing/workspace declares no phases.
- **Phase → Overview chassis mapping** — research+initiation →
  `ProposalReviewOverview`; research+execution → `ExperimentDashOverview`;
  research+closure → `PaperAcceptanceOverview`.
- **Phase → tile set** — initiation surfaces Proposal+References+Risks;
  execution surfaces Runs+Outputs; closure surfaces DeliverableReport.
- **Phase → deliverable specs** — for each phase, declare 0..N
  deliverables with `kind`, required component types, section schemas
  for any document components, and ratification authority.
- **Phase → acceptance criteria spec** — required vs optional, kind
  (text/metric/gate), evidence linkage, auto-met conditions.
- **Phase transitions** — declare which transitions are valid + which
  require explicit director ratification vs auto-advance.
- **Steward spawn policy** — when this template's project gets its
  project steward (eager / lazy / phase-triggered).

The pattern is symmetric with how `overview_widget` already works —
extend the registry from "one widget per template" to "one widget per
(template, phase)", and add parallel registries for deliverable kinds,
section schemas, and criterion specs.

### 4.3 MVP scope discipline

For the demo:

- **Build the research template fully** — all 5 phases, with
  proposal/method/experiment-results/paper deliverables, section
  schemas, acceptance gates per phase, dialogue patterns.
- **Build the chassis to host it generically** — phase model in schema,
  deliverable primitive, two generic viewers, phase-aware overview
  registry, ratification action.
- **Do NOT build a second skeleton template** (product, ops) — chassis
  must remain agnostic, but a second template is unnecessary scope add
  for MVP.
- **Do NOT generalize phase semantics beyond what research needs** —
  leaves room for future templates to teach the chassis new tricks.

Heuristic: if a piece of behavior is "research-specific", it lives in
the template / overview widget / document section schema / criterion
spec. If it's "every project has these mechanics", it lives in the
chassis. *Phases-as-a-concept* is chassis; the research phase set is
template. *Deliverable-as-a-primitive* is chassis; "Proposal" is what
the research template's Initiation deliverable is named.

### 4.4 Demo positioning — chassis legibility

The demo serves two audiences: *director persona walkthrough* (does it
work?) and *reviewer / evaluator* (is this just a research tool, or a
generic platform?). Reviewers default to the former framing if the
demo doesn't visibly show otherwise.

**Locked demo strategy** (2026-05-05):

1. **Diversity within research.** Different deliverables in the same
   research project demonstrate the full chassis range — Proposal
   (doc-only), Method (doc-only), Experiment Results (doc + artifacts
   + runs), Paper (doc + artifact references). Same primitives, very
   different contents — chassis range becomes self-evident.
2. **YAML in narration.** During the walkthrough, surface the research
   template's YAML at the right moments to make the chassis-vs-template
   seam explicit. "Here's the template declaring the 5 phases; here's
   the deliverable spec; here's the section schema." Implementation:
   a "Template" affordance reachable from project detail (Phase 2.5
   work) or simply screenshots / voice-over at demo time.
3. **No second skeleton template.** Rejected as unnecessary MVP scope.
   Diversity within research + YAML narration are sufficient signal.

---

## 5. Locked design decisions

These nine decisions were made during framing Q&As on 2026-05-04 and
2026-05-05. They are recorded here for reference; promotion to ADRs is
a follow-up when the implementation wedge is scheduled.

### D1 — Phase as a project field

Phase lives on the project row as a nullable string (`projects.phase`).
Templates declare the allowed values. Milestones serve as gates *within*
each phase, not as the phases themselves.

**Why:** Simpler than milestones-as-phases; matches the director's
mental model ("what stage are we in?") without coupling phase
semantics to task structure. Workspaces (kind=standing) leave the field
NULL.

**Implication:** Phase history could be a small JSON column
(`projects.phase_history`) if we want transition timestamps; otherwise
it's derivable from `audit_events` filtered by `kind='project.phase_*'`.

### D2 — Sub-projects = WBS, not sub-experiments

The parent-child relationship represents work-breakdown decomposition
(an Initiation-phase concern). Sub-experiments within a single research
project are modeled as Tasks/Plans/Runs, not as sub-projects.

**Why:** Keeps sub-projects semantically meaningful at the planning
level; avoids over-using the parent-child mechanism for runtime entity
nesting.

**Open question:** Current depth cap is 2 (parent → child, no
grandchildren). For research, a paper-project's experiment-children
might want their own sub-experiments-as-Tasks. Either lift depth cap
or treat experiments inside a child as Tasks. Resolution pending.

### D3 — Chassis has no name for the phase deliverable

The chassis primitive at the phase boundary is the **Deliverable**
(D8), which can contain typed Documents (D7) among other components.
The chassis does *not* hardcode the term "Proposal" — that's what the
research template names its Initiation deliverable. Other templates
will name it Charter / PRD / Design-doc / SOW / etc. `Document.kind`
and `Deliverable.kind` are freeform strings declared by templates.

**Why:** "Proposal" is research-tinged. Cross-domain survey shows the
same artifact is called Charter (PMBOK), PRD (product), RFC / Design
doc (engineering), SOW (consulting), CONOPS (defense), etc. The
*structure* is invariant (motivation, scope, method, constraints,
deliverables, acceptance); the *name* is not. Naming a chassis
primitive after one domain locks others out.

### D4 — Phases skippable; ordered but non-linear

Templates declare phases as an ordered list, but transitions are not
required to follow the order. A research project that skips lit-review
(continuation work) or a product project that skips planning (rapid
iteration) is valid.

**Why:** Real lifecycles aren't linear. Forcing strict order fights the
user.

**Implication:** Template YAML declares phases as a graph or as
ordered-list-with-skips, not a strict sequence.

### D5 — Acceptance criteria: structured, with free-text as a subset

Acceptance criteria are a structured type:
`{ kind: 'text' | 'metric' | 'gate', body }`. Free-text criteria are
the `kind=text` case. Structured criteria (metric thresholds,
automated gates) are added on top.

**Why:** Lets steward author free-text criteria during Initiation, then
structure them later as the project tightens. The schema must accept
both *from day one* — adding structure later via migration is painful.

### D6 — Phase transitions: explicit by default, configurable

Phase advancement is gated on explicit director ratification by default.
Templates can opt specific transitions into auto-advance (e.g., when
all phase criteria are met automatically). Configuration is
per-transition, not per-project.

**Why:** Explicit forces the director to actually look at what changed.
Auto-advance is appropriate for low-risk transitions but should be opt-in.

**Smallest representation:** template YAML declares
`transitions: [{ from, to, mode: 'explicit' | 'auto', auto_when? }]`.

### D7 — Documents have first-class sections (typed, structured)

Typed structured documents have a section schema declared by template.
Each section has its own state (`empty | draft | in-review | ratified`)
and its own authoring lineage. Steward sessions are
**section-targeted** (`target = (document_id, section_slug)`),
distilling into one section, leaving siblings untouched. Plain
markdown documents (kind=NULL, no schema) keep current rendering as
fallback.

**Why:** LLMs cannot reliably draft a 30-page proposal in one shot;
2026 practice (AI Scientist v2, IKP, PaperOrchestra) is section-by-
section pipelines. Independently, directors on phones want to read +
ratify one section at a time. Both forces converge on sectioned-as-
primary.

**Schema:** `documents.body` accepts JSON for structured docs:
`{ sections: [{ slug, title, body, status, last_authored_at, ... }] }`.
Plain markdown docs keep TEXT body. `documents.kind` and
`documents.schema_id` are nullable; templates declare both for typed
docs. Section state lives inline in the JSON for MVP simplicity (no
separate `document_sections` table); migration path to a dedicated
table is preserved if scale forces it.

### D8 — Deliverable as chassis primitive (PMBOK term)

A **Deliverable** is bound to a phase and is the unit of acceptance.
Schema:

```
deliverables:
  id, project_id, phase, kind (template-declared string),
  ratification_state ('draft' | 'in-review' | 'ratified'),
  ratified_at, ratified_by_actor,
  required bool, order

deliverable_components:
  deliverable_id, kind ('document' | 'artifact' | 'run' | 'commit'),
  ref_id (FK depending on kind),
  required bool, order
```

**Decisions:**
- **Cardinality:** 0..N deliverables per phase, **declared by template**.
  Idea phase typically has 0; Initiation has 1 (Proposal); Convergence
  may have 2 (Results doc + Final paper). Templates pick.
- **Component kinds:** **closed enum for MVP** —
  `document | artifact | run | commit`. Open extensibility deferred
  until a concrete need exists.
- **Component requirements:** **template declares** which components
  are required vs optional per deliverable kind.
- **Nesting:** **no** — deliverables are flat; sub-projects handle
  decomposition.
- **Ratification authority:** **template declares** — director-only,
  council (when implemented), or auto-when-criteria-met.

**Why:** Phase outputs aren't all documents. Experiment phase produces
artifacts + runs + a summary report; software-build phase produces
code (commits) + a launch report. The chassis primitive must absorb
both text-heavy and artifact-heavy outputs. PMBOK's Deliverable term
is neutral and well-grounded.

**Migration path for existing data:** today's typed documents
(proposal, method, paper) become Deliverable components when their
template declares the binding (D8 confirmation §6, response 7). Plain
markdown documents stay free-floating, attached to the project but
not bound to a specific phase.

### D9 — Acceptance criteria attach to phase, with optional deliverable_id

Phases have first-class criteria. Deliverable ratification is one
*possible* criterion among others. A phase advances when all required
criteria are met.

```
acceptance_criteria:
  id, project_id, phase, deliverable_id (nullable),
  kind ('text' | 'metric' | 'gate') -- per D5,
  body (kind-dependent),
  state ('pending' | 'met' | 'failed' | 'waived'),
  met_at, met_by_actor, evidence_ref,
  required bool, order
```

**Why:** Lightweight phases without deliverables (Idea) can still be
gated. Heavy phases can have criteria that span beyond the deliverable
("at least 3 sibling-team reviewers signed off" — not a deliverable
property). Auto-advance criteria (D6) can reference any signal:
deliverable state, run state, metric value, manual ratify.

### D10 — Channel tab demoted to AppBar Discussion icon

The project channel (multi-party broadcast surface, distinct from
director↔steward 1:1 per forbidden #15) is **demoted** from the pill
bar to a chat icon in the project detail AppBar. Renamed
**"Discussion"** for clarity. The freed pill slot goes to **Activity**
(wedge W2). The channel data primitive is unchanged — only its entry
point moves.

**Why:** Three converging forces:

1. **Pill slot economics** — adding Activity (highest-priority gap)
   needs a slot; the lowest-traffic existing tab is Channel in a
   single-director MVP.
2. **Single-user usage** — most director comms is steward 1:1; the
   project channel has no unique function in MVP beyond "project-root
   discussion catch-all", which the engineering tradition (Linear,
   GitHub, Notion, Jira) handles via per-entity threading + de-emphasis
   of channel-as-tab.
3. **Multi-user reversibility** — when F-1 (per-member stewards +
   multi-user teams) lands, Discussion can re-promote to a pill tab
   with no schema change. The data primitive is preserved.

**Rejected alternatives:**
- *Talk tab merging channel + steward 1:1 with mode toggle* — violates
  forbidden #15; re-introduces the conflation pattern.
- *Remove channel entirely* — too aggressive; loses the
  project-root-discussion affordance with no replacement.
- *Keep channel as-is* — duplicates Activity for steward broadcasts in
  single-user MVP; weakest tab in slot competition.

**Section affordance:** chat icon next to Edit in AppBar, opens a
sheet/screen showing the existing project_channel content.

---

## 6. Steward layering: general vs project

Per ADR-017, layered stewards are already shipped at the architectural
level. This section formalizes the operational boundary.

### 6.1 Two functional axes

Stewards split along two orthogonal dimensions:

**Axis 1 — scope of context:**
- *Team-scoped*: knows about all projects, the director's calendar,
  team policies, cross-project priorities
- *Project-scoped*: knows about one project's goal, history, codebase,
  agents, current phase, deliverables

**Axis 2 — operational mode:**
- *Concierge / dispatcher*: routing, scheduling, digesting,
  bootstrapping new work
- *Manager / executor*: supervising workers, making in-scope decisions,
  authoring artifacts

| | Concierge mode | Manager mode |
|---|---|---|
| **Team-scoped** | **General steward** ✓ | (rare; cross-project execution) |
| **Project-scoped** | (rare; project-level routing) | **Project steward** ✓ |

The other two cells could exist (cross-project executor; project
concierge) but neither is MVP.

### 6.2 General steward — definition

**Owns:**
- Director profile, preferences, working style
- Cross-project visibility (project list, phases, attention queue,
  recent activity)
- Calendar / scheduling / reminders
- Routing dispatch ("this question concerns project X; opening project
  X's steward")
- Project bootstrap (Idea-phase scoping; transitions a project to
  Initiation when ready)
- Cross-project synthesis ("what's blocked? what should you focus on?")
- Memory: team-wide, slow-changing, persistent across projects

**Does not own:**
- In-project decisions (phase advancement, worker spawning, run
  authoring)
- Project artifact authoring (proposal sections, code, papers)
- Project-specific tool execution (running an experiment, editing a
  repo)

**Lives at:** Me tab → steward FAB / steward card. Single instance
per team.

### 6.3 Project steward — definition

**Owns:**
- Single project's goal, phase state, history
- Project-specific knowledge (codebase, dataset, prior runs)
- Project artifact authoring via section-targeted sessions (D7)
- Worker spawning within project scope
- Phase-specific behavior (Initiation = drafting; Execution = running;
  Closure = wrapping)
- Acceptance gate authoring + monitoring
- Memory: project-scoped, persistent

**Does not own:**
- Other projects (must hand off via A2A)
- Team-wide policy (consults general steward if needed)
- Schedule / calendar

**Lives at:** Project detail → steward strip / steward chip. One per
project.

### 6.4 Handoff protocol

Director-to-steward routing:

- **Inline handoff** (rejected for MVP): general steward A2A's project
  steward, returns answer in current chat.
- **Navigation handoff** (chosen for MVP): general steward says
  "opening Project Y" and the UI navigates to Project Detail →
  steward 1:1. Clearer to the director, matches IA-A2 (one entity,
  one home).

Steward-to-steward routing (internal, director doesn't see):

- Project steward needs team-wide info → A2A general steward
  (hub-mediated per ADR-003).
- General steward needs project-specific info → A2A that project's
  steward.
- Project A's steward needs Project B's data → goes via general
  steward (general arbitrates; preserves the manager/IC + scope
  boundaries).

### 6.5 Spawn policy (template-declared)

Templates declare when a project gets its project steward:

- **Eager**: spawn at project creation. Default for research template
  — director needs steward help during Idea/Initiation.
- **Lazy**: spawn on first Initiation interaction. Cheap; first
  interaction slower.
- **Phase-triggered**: spawn at a specific phase entry (e.g.,
  Initiation entry).

Default is eager for MVP. Lazy / phase-triggered are template knobs.

### 6.6 Steward chip → strip with rich states

Today the chip says `configured / not-configured`. Real states the
strip should reflect:

| State | Meaning | Affordance |
|---|---|---|
| `not-spawned` | Template hasn't created project steward yet | "Start steward" |
| `idle` | Exists, no active session | "Direct" |
| `active-session` | Director is in 1:1 with it | "Resume" |
| `working` | Autonomously processing (drafting, supervising) | "View work" |
| `worker-dispatched` | Has spawned ICs, supervising | "View workers" |
| `awaiting-director` | Has posted attention items | "Respond" |
| `error` | Stalled / paused | "Inspect" |

The strip should display the current state with a glanceable indicator
+ contextual primary affordance. Ties to gap #3 / wedge W3 (steward
de-burial).

---

## 7. What this means for the page

### 7.1 The page shape

The four incremental gap-fixes from §3.2 (Activity tab, goal
de-burial, steward strip, tile cut from 7→3) are still right *for the
execution phase*, but they're insufficient because:

1. The page needs a **phase indicator** at the top (5-step horizontal
   stepper or breadcrumb). Without it, the director can't see where
   the project is in its life.
2. The Overview's hero must become **phase-driven**, not just
   template-driven. Initiation's hero is the deliverable viewer, not
   run cadence.
3. The shortcut tile set must be **phase-filtered**. Showing "Outputs"
   during Initiation is misleading (there are none yet); showing
   "Proposal" during Execution is past-tense.
4. The "next action" the director needs is phase-dependent — at
   Initiation it's "review proposal sections"; at Convergence it's
   "ratify final deliverable".

### 7.2 Suggested overview chassis (phase-aware)

```
[Title · KindChip]   TeamSwitcher · Edit · ⋮

[Phase ribbon]  Idea ─ Lit-review ─ Method ─ ●Experiment ─ Paper

Goal (2-line, prominent, non-duplicated)

Vital strip:  ● status  ▰▰▰▱ 60%  $42/$200  🤖 steward live  ⚠ 2

[Next-action card]  (phase-driven primary CTA)

[Phase-driven hero region]  (registry: (template, phase) → widget)

[Phase-filtered shortcut tiles]  (template declares which apply)

[Activity snippet]  (last 5 events; "View all" → Activity tab)

─ tabs ─
Overview · Activity · Talk · Plan · Library · Agents
```

The **phase ribbon is interactive**: tapping the current phase opens
the deliverable viewer for that phase; tapping a past phase opens its
ratified deliverable read-only; tapping a future phase shows a "Not
yet started" placeholder. Phase ribbon = navigation spine of the
project; deliverable viewer = its content.

**Phase ribbon visual (locked 2026-05-05):** horizontal Material
stepper. Circle nodes for each phase, abbreviated labels beneath
(e.g., "Idea / Lit-rev / Method / Exp / Paper"), current phase
filled with primary color, completed phases muted-primary, future
phases muted-grey. Familiar pattern, fits 5 short labels on phone
width, clear active/done/pending states.

### 7.3 Two generic viewers, composed

```
Structured Deliverable Viewer (chassis primitive — D8)
├─ Header: phase, kind, ratification state, ratify action
├─ Components panel
│   ├─ Document component → Structured Document Viewer (D7)
│   │     sections list, per-section state, section-targeted sessions
│   ├─ Artifact component → Artifact reference card
│   ├─ Run component → Run reference card
│   └─ Commit component → Commit reference card
├─ Acceptance Criteria panel (D9)
│   └─ Criterion list with state pips, evidence refs
└─ Action panel: Direct steward, Ratify deliverable, Advance phase
```

Both viewers are generic chassis primitives. Templates configure
section schemas + component requirements + criterion specs. Same
viewers serve Proposal in research, PRD in product, design-doc in
engineering, paper-draft in research closure.

### 7.4 Tab restructure (refined)

Three options span the disruption axis:

- **Option A (work-centric, biggest move):** Now · Talk · Plan ·
  Library · Agents. Maps cleanly to director questions. Disruptive.
- **Option B (incremental, recommended):** demote Channel → AppBar
  Discussion icon (D10), promote Activity into the freed pill slot,
  cut shortcut tiles 7 → 3 (template-driven), collapse metadata rows
  behind expander.
- **Option C (Notion-like):** single scrollable page, no tabs. Most
  context-preserving but unfamiliar; defer.

Recommendation: **Option B for the first wedge** (smallest move that
fixes gaps 1–5 and resolves D10), then Option A's tab restructure
after the phase model + deliverable primitive land and the chassis-vs-
template seam stabilizes.

**Pill bar after Option B:** Overview · **Activity** · Agents · Tasks ·
Library *(was Files; renamed when tile-cut consolidates docs/outputs
under Library)*. Discussion accessible via AppBar chat icon.

---

## 8. Schema additions (small, foundational)

Relative to today:

- `projects.phase TEXT NULL` — nullable, template-declared values
- `projects.phase_history JSON NULL` — optional transition log
- `documents.kind TEXT NULL` — freeform, template-declared
- `documents.schema_id TEXT NULL` — references template's section schema
- `documents.body` — accepts JSON for structured docs (with sections);
  TEXT for plain markdown (existing behavior)
- **NEW** `deliverables` table (per D8)
- **NEW** `deliverable_components` table (per D8)
- **NEW** `acceptance_criteria` table (per D5/D9, phase-keyed,
  optional deliverable_id reference)
- Templates gain a `phases:` block declaring the phase set,
  per-phase overview widget, per-phase tile set, per-phase deliverable
  specs (kinds, components, ratification authority), per-phase
  criterion specs, transition rules, and steward spawn policy.

No table additions for the chassis primitive *Document* itself
(sections live inline in body JSON). Three new tables for deliverables
+ components + criteria. All chassis-side; no research-specific
schema.

---

## 9. Open questions / follow-ups

Resolved by the 2026-05-04 / 2026-05-05 Q&As (§5):

- ~~Phase placement (D1)~~ — resolved: project field
- ~~Sub-projects = WBS (D2)~~ — resolved: yes
- ~~Proposal authoring model (D3, D7)~~ — resolved: section-targeted
  sessions, distillation per section, deliverable wraps
- ~~Phase skippability (D4)~~ — resolved: ordered but skippable
- ~~Acceptance criteria shape (D5)~~ — resolved: structured with
  free-text subset
- ~~Phase transitions (D6)~~ — resolved: explicit default, configurable
- ~~Naming "Proposal" generically (D3)~~ — resolved: chassis has no name
- ~~Sections as primitive (D7)~~ — resolved: yes, inline in body JSON
- ~~Deliverable primitive name (D8)~~ — resolved: "Deliverable"
- ~~Deliverable cardinality (D8)~~ — resolved: 0..N, template-declared
- ~~Deliverable component enum (D8)~~ — resolved: closed for MVP
- ~~Component requirements (D8)~~ — resolved: template-declared
- ~~Deliverable nesting (D8)~~ — resolved: no
- ~~Ratification authority (D8)~~ — resolved: template-declared
- ~~Acceptance criteria placement (D9)~~ — resolved: phase-level with
  optional deliverable_id
- ~~Doc-vs-deliverable migration (D8)~~ — resolved: typed docs become
  Deliverable components when bound; plain markdown stays free-floating

Resolved by the 2026-05-05 Q&A:

- ~~Sub-project depth cap~~ — keep at depth 2 for MVP (D2 scope);
  revisit if WBS for nested research demands it.
- ~~Section state granularity~~ — **3 states**: `empty / draft /
  ratified` (drop the intermediate `in-review` to reduce UI complexity
  and audit-event types).
- ~~Phase indicator visual~~ — **horizontal Material stepper** with
  abbreviated labels beneath circle nodes (locked, §7.2).
- ~~Acceptance gate automation~~ — **ratify-prompt**, not auto-advance.
  When a metric criterion fires, steward posts an attention item; the
  director ratifies to advance.
- ~~Channel tab fate~~ — **D10**: demote to AppBar Discussion icon,
  free pill slot for Activity.
- ~~A2A handoff UX~~ — **visible indicator**. When the project steward
  routes via the general steward (or vice-versa), the director sees a
  brief "Asking general steward…" / "Handing off to project steward…"
  cue. Avoids the impression that the steward already knows what it's
  about to look up.

Still open:

1. **Backwards compatibility.** Existing projects (no phase) must keep
   working. Default phase = NULL → fallback to current execution-style
   Overview. Template migration becomes opt-in. Resolution: plan-grain
   detail in `plans/project-lifecycle-mvp.md` (TBD).
2. **Document versioning.** Per-section state covers small redlines;
   what about radical restructure (steward proposes a new section
   layout)? Defer to a post-MVP schema-evolution discussion.
3. **Deliverable history.** When a deliverable is ratified, then later
   re-opened (revoked ratification — does this exist?), how is the
   prior ratified state preserved? Audit trail vs versioned rows.
   Defer to post-MVP.

---

## 10. Suggested wedges (no plan; sketches only)

If/when this becomes work, a sensible wedge order:

| Wedge | Scope |
|---|---|
| **W1 — Phase chassis** | Add `projects.phase` + phase ribbon widget + ratify action. Default phase=NULL, page falls back to today's Overview. |
| **W2 — Activity surface + Channel demote** | Wire `_ActivityView` into the pill bar in the slot freed by demoting Channel to an AppBar Discussion icon (D10). Add Activity-snippet to Overview. (Gaps 1, 9.) |
| **W3 — Goal + steward strip** | Promote goal to 2-line block; expand steward chip → strip with rich states (§6.6). Cut metadata rows behind expander. (Gaps 2, 3, 5.) |
| **W4 — Tile cut + template-driven** | 7 → 3 default tiles; template declares phase-filtered set. (Gap 4.) |
| **W5a — Structured Document Viewer** | New chassis primitive: section-aware viewer with state pips, section-targeted steward sessions, plain-markdown fallback. Used by typed docs across all templates. (D3, D7.) |
| **W5b — Deliverables schema + Structured Deliverable Viewer** | Add `deliverables`, `deliverable_components` tables. Wrap viewer composes W5a's doc viewer + artifact / run / commit references. Phase ribbon → deliverable navigation. (D8.) |
| **W6 — Acceptance criteria** | Add `acceptance_criteria` table. UI: free-text criteria first (D5 text subset); structured (metric/gate) layered on. Phase-keyed (D9). |
| **W7 — Research template content** | All 5 phases declared with overview widgets, deliverable kinds, document section schemas, acceptance gates with auto-advance rules where appropriate, transitions, steward spawn policy, prompt variants per phase. Specific content, not chassis. |
| **W8 — Tab restructure (Option A)** | Optional. Now · Talk · Plan · Library · Agents. Schedule after the phase model + deliverable primitive stabilize. |

Wedges 1–6 are general (chassis); wedge 7 is the demo content; wedge
8 is a UX restructure that should follow real usage.

**Demo scope (locked 2026-05-05):** all wedges W1–W7 in scope; no cut.
The demo doubles as a chassis-generality showcase for reviewers, so
all phases of research must be exercised end-to-end with the full
deliverable + criterion machinery visible. W8 (tab restructure to
Option A) remains optional — schedulable after the phase model + two
viewers stabilize.

**Steward layering (§6) does not need a separate wedge** — the
architecture already shipped (ADR-017). The clarifications surface as:
richer chip states in W3, spawn policy declaration in W7, handoff
indicator in W3 (D-bucket §B.6 resolution), prompt variants in W7.

---

## 11. References

- `spine/information-architecture.md` §6.2 — Project Detail IA
- `spine/blueprint.md` §6.5 — runs and templates
- `spine/sessions.md` §8.5 — director ↔ steward 1:1 sessions
- `decisions/001-locked-candidate-a.md` (amended) — research demo lifecycle
- `decisions/009-agent-state-and-identity.md` D7 — steward session scope routing
- `decisions/017-layered-stewards.md` — general / project steward architecture (§6)
- `discussions/research-demo-lifecycle.md` — sister discussion (research-specific)
- `discussions/project-list-director-review.md` — sister discussion (list page; deferred)
