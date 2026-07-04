---
name: Research reading & ideation UI — question-led, agent-discussed, incubation-friendly
description: The director observed that with the run-detail surface now industry-grade (trackio/wandb/TF practice), the *other* core of research work — surveying and reading the literature — has no tuned UI: papers render as generic markdown or a fixed PDF, neither optimised for ideation. This doc reframes the job from "read a paper top-to-bottom" to *question-led* reading (the human arrives with questions — what is the SOTA of X? why does it work? what is the core idea? how does X differ from Y? what is the implication?) answered by an agent that has read the corpus and *discusses* it, not just summarises. It surveys the well-tested UIs per research job (read / survey / ideate) — NotebookLM, Elicit, Consensus, Semantic Scholar Reader for grounded question-answering; LiquidText, MarginNote, Obsidian/Zettelkasten for synthesis — and argues the right model for TermiPod is a *grounded research dialogue* (NotebookLM/Elicit lineage) whose answers deposit durable, backlinked note-cards that *incubate* via the Zettelkasten/evergreen-notes mechanism (bidirectional links + resurfacing, rendered phone-first as related-card lists rather than a force-graph). It places the surface inside the existing **workspace** (project `kind: standing`) container, notes the project-detail chassis needs a research-workspace redesign, and treats the layout as **foldable/tablet-first** (single-pane chat + Navigator on a phone; dual-pane reading↔notes / canvas on a foldable), reusing the References-tile (artifact-kind `citation`), the `paper`/`lit-review` document & deliverable kinds, and the Insight `View ▾`/Navigator/excerpt-to-context patterns. Concludes with a recommended first slice (corpus-scoped grounded Q&A dialogue) and the open forks.
---

# Research reading & ideation UI — question-led, agent-discussed, incubation-friendly

> **Type:** discussion
> **Status:** Open (2026-06-03) — raised by the director after the run-detail
> UI shipped (v1.0.797-alpha). "The run has a full-fledged, industry-tested
> view now; but paper survey and reading is the *other* core of research work,
> and the mobile app is just markdown or PDF — not optimised for ideation."
> **Audience:** contributors
> **Last verified vs code:** v1.0.797-alpha

**TL;DR.** Stop thinking of literature work as *reading a paper top-to-bottom*.
Researchers arrive **with questions** ("what's the SOTA of X? why? what's the
core idea? how does X differ from Y? what's the implication?") and want the
**relevant key info** surfaced as an affordance. The best-tested UI for that is
a **grounded research dialogue** — an agent that has read the project's corpus
**answers and discusses**, with every claim **citing back** to a source
(NotebookLM / Elicit / Consensus / Semantic Scholar lineage). Each answer should
**deposit durable, backlinked note-cards** that **incubate** over time via the
Zettelkasten/evergreen-notes mechanism (bidirectional links + resurfacing) —
the only one of the three ideation modalities actually suited to *incubation*.
This lives in the **workspace** project kind (`standing`), needs a
research-workspace redesign of the project-detail chassis, and is designed
**foldable/tablet-first**: one pane (chat + Navigator) on a phone, dual-pane
(dialogue ↔ source/notes, or reading ↔ excerpt canvas) on a foldable. It reuses
primitives we already have — the References tile, the `paper`/`lit-review`
document & deliverable kinds, and the Insight `View ▾`/Navigator/excerpt-to-
context patterns.

---

## 1. What prompted this

The run-detail surface (`docs/plans/run-detail-ui.md`, Done) gives experiments a
view grounded in adopted practice (trackio / wandb / TensorBoard). Research has a
second core loop the app does **not** serve well: **finding, reading, and
synthesising the literature**. Today a paper is either:

- a **document** (`flutter_markdown` + bundled KaTeX, `flutter_math_fork`) —
  great for *authored* prose, wrong for an imported paper; or
- an **artifact** behind the `pdfrx` PDF viewer — a fixed, usually two-column
  layout that is actively hostile on a phone and offers no path to ideation.

Neither is "question-led", and neither helps a half-formed idea mature. The
director's framing is the key unlock: **the reading is question-lead.** The
human has questions in mind; the UI's job is to provide the affordance — the
relevant info that answers them — and the agent's job is not just to *read* but
to **discuss**.

## 2. The three research jobs and their well-tested UIs

Literature work decomposes into three jobs with *different* gold-standard UIs.
Conflating them is why a generic renderer satisfies none.

**Job A — read/understand (depth).** The well-tested move on a small screen is
**reflow over fixed layout** (arXiv HTML / ar5iv reflow LaTeX to one resizable
column; never two-column PDF on a phone), plus **structure-aware navigation**
(section outline, figure strip — researchers skim by figures), **inline citation
hover/tap-cards** (Semantic Scholar Reader), and **selection actions** (define /
explain / ask / save). Distill.pub is the quality bar for inline figures and
margin notes.

**Job B — survey (breadth).** The most-praised UI is **Elicit's literature
matrix**: rows = papers, columns = auto-extracted attributes (method, dataset,
metric, finding). Citation/similarity graphs (Connected Papers, ResearchRabbit,
Litmaps) help *discovery* but are desktop-shaped; on a phone a ranked "related"
list beats a force-graph. Zotero/Mendeley supply the unglamorous library +
collections + reading-status layer.

**Job C — ideate/synthesise (connection).** LiquidText is the canonical tool —
excerpt passages onto a freeform workspace, link them, pinch to gather — but it
shines on iPad and struggles on a phone. MarginNote turns highlights into an
outline/mind-map (phone-friendlier). Obsidian/Roam and the Zettelkasten /
evergreen-notes tradition (Andy Matuschak) give bidirectional links and
transclusion — networked thought.

## 3. The reframe: a grounded research dialogue

The director's two constraints — *question-led* and *the agent discusses* —
point at a specific, well-tested model that cuts across Jobs A and B: **grounded
question-answering over a fixed source set, in conversation, with citations.**
The exemplars:

- **NotebookLM (Google)** — the closest match: hand it sources, it **grounds a
  chat** in them with inline **citations** and auto-generates briefing docs /
  FAQs. "Agent reads *and* discusses" is literally its design.
- **Elicit** — ask a *research question* → extracted, cited answers *across*
  papers (the "what's the SOTA of X?" affordance, at corpus scale).
- **Consensus / Perplexity / scite assistant** — NL question → synthesised
  **cited** answer.
- **Keshav's three-pass method** ("How to Read a Paper", 2007) — the principled
  decomposition of a paper into exactly the director's question-set: *category,
  contributions, correctness, and the **key insight***. This is the source for a
  fixed **palette of question-affordances** rather than a blank prompt box.

So the surface is **chat grounded in the project's corpus**, fronted by a
**question palette** — SOTA · Why · Core idea · Diff vs … · Implication · (more)
— and answers rendered as **claim + citation tap-card** back to the source. The
fit with what we already ship is unusually clean:

| Need | Existing primitive |
|---|---|
| "Agent discusses" | a **session** with a research/domain steward scoped to the corpus — reuses the LiveFeed / transcript stack |
| Question palette | prompt-chips on that session |
| "Cite back" | tap-card to the **References tile** (artifact-kind `citation`, `glossary.md` §10c) |
| Spin into a writeup | **document** `kind: paper` / **deliverable** `kind: lit-review` (`glossary.md`) — closing research → directed work |
| Outline / figure / citation jumps while reading | the Insight **`View ▾` + Navigator** + excerpt-to-context patterns ([ADR-041](../decisions/041-insight-workbench-layout.md)) |

## 4. Incubation: why backlinked notes, and how it survives a phone

The director asked which ideation modality is **fit for incubating**. Incubation
is a specific cognitive job — half-formed ideas maturing through **connection
over time** and **serendipitous resurfacing**, not deliberate arrangement.
Judged against that:

- **Outline of cards** presupposes you already know the structure — it
  *organises what you understand*, it doesn't incubate what you don't.
- **Freeform canvas** is session-bound and high-maintenance (and phone-hostile)
  — it's for an active synthesis *sprint*, not letting ideas sit and connect.
- **Networked notes / backlinks** is the modality built *for* incubation:
  Zettelkasten, Matuschak's evergreen notes, and Roam/Obsidian all rest on the
  claim that connections should **emerge over time** rather than be filed up
  front. This is the right answer.

**The phone reconciliation** (the load-bearing insight): incubation needs the
**mechanism** — bidirectional links + "here is your prior thinking related to
what you're looking at now" — *not* the force-directed graph *visualisation*,
which is the genuinely phone-hostile part. That resurfacing mechanism is exactly
the pattern we already ship repeatedly (run→agent links; the Insight Navigator's
related lists). So we get the incubation modality cheaply by rendering backlinks
as **related-card lists**, and defer the graph view to the large screen (§6).

## 5. Where it lives: the workspace project, and a chassis redesign

The director placed this in "the **workspace** kind of project." In the schema
that is project `kind: standing` — the mobile IA renders it as the **Workspaces**
section, the *ongoing container* mental model as opposed to a bounded **goal**
(`lib/screens/projects/projects_screen.dart:545`; blueprint §6.1). Literature
work is open-ended and accretive, so the workspace container is the right home.

But the **project-detail chassis** ([reference/project-detail-chassis.md](../reference/project-detail-chassis.md))
— today a strip of tiles (References / Documents / Runs / …) — is tuned for the
goal/phase lifecycle ([research-template-spec.md](../reference/research-template-spec.md)),
not for a standing reading workspace. A research workspace wants a *different
first screen*: the **grounded dialogue** front-and-centre, the **corpus**
(References) and **notes** as its working set, and the lit-review **deliverable**
as the output. So part of this work is a **workspace chassis** variant, not just
a new tile. That keeps the goal/phase chassis intact while giving standing
research projects a surface built for the read→discuss→incubate loop.

## 6. Foldable / tablet-first layout

The director's bet: **foldables will dominate the research community**, so design
for them rather than retrofitting. This *inverts* a constraint — the modalities
flagged "phone-hostile" above (LiquidText-style dual-pane, even a modest graph
view) become **viable on the unfolded screen**. The design should therefore be
**adaptive**, not phone-only:

- **Phone (folded):** single pane — grounded **chat + question palette**, with
  the **Navigator** drawer for outline/citations/related-notes and tap-sheets
  for sources. Exactly the lineage we built for Insight.
- **Foldable / tablet (unfolded):** **dual-pane** — dialogue on one side, the
  **source/excerpt or notes** on the other (the LiquidText reading↔workspace
  split, which is *good* on a large screen), and a related-notes / light graph
  affordance for incubation.

There is precedent: the Insight workbench already contemplates wide-screen
pinned rails ([ADR-041](../decisions/041-insight-workbench-layout.md)), and the
app already branches on width via `LayoutBuilder` in several widgets
(`insight_transcript.dart`, `sessions_rail.dart`). The discipline is a single
set of breakpoints and **one widget tree that re-flows** (phone single-pane →
foldable dual-pane), not two forked screens — see also
[desktop-and-web-targets.md](desktop-and-web-targets.md).

## 7. The crystallised model + data shape

Putting it together: a **grounded research dialogue** whose answers **deposit
durable, backlinked note-cards**; the notes **incubate** via backlinks +
resurfacing; a cluster of notes **spins into** the lit-review deliverable or a
task.

The one new primitive is a lightweight **note / excerpt** entity:

- created two ways — the agent *deposits* it from an answer (a claim + its cited
  source), or the human *captures* a selection while reading;
- carries **backlinks** to the paper(s) it cites and to other notes;
- **resurfaces** wherever its links point (open a paper → its notes; ask a
  related question → prior notes);
- **transcludes** into a `document(kind: lit-review)` when a cluster matures.

Whether this is a new table or a specialisation of `documents` (sections already
exist) / annotations is an open question (§8). Either way the agent grounding —
how the steward retrieves over the References corpus to answer with citations —
is the substrate that makes the whole thing real (RAG over the citation
artifacts, exposed as an MCP tool to the research steward).

## 8. Open questions / forks

1. **First slice.** Recommended: the **corpus-scoped grounded Q&A dialogue**
   (question palette + cited answers) — the spine the other layers hang off.
   Alternatives: a per-paper question-digest card; or the notes/incubation layer
   (but it needs answers/excerpts to exist first).
2. **The note entity.** New `notes` table vs. extend `documents`/sections vs.
   reuse annotations. Backlinks need a join table either way.
3. **Workspace chassis.** How much of the project-detail chassis to fork for
   `kind: standing` research workspaces vs. add as tiles to the existing one.
4. **Grounding mechanism.** RAG over the References corpus as an MCP tool for the
   research steward — index granularity (paper / section / chunk), and whether
   the agent ingests PDFs to HTML/text up front.
5. **Foldable breakpoints.** One canonical breakpoint set + a posture signal
   (folded/unfolded) — define once, app-wide.
6. **Reading substrate.** Reflow HTML-first (best read UX, needs a source — arXiv
   HTML or agent conversion) vs. annotate-on-`pdfrx` vs. agent-digest-first.

## 9. Alternatives considered (and why deferred)

- **PDF-annotator-first** (LiquidText/MarginNote clone on `pdfrx`). Familiar, but
  keeps the two-column mobile problem, is human-labour-heavy, and ignores the
  "agent discusses" reframe. The annotation *primitive* (excerpt→note) is kept;
  the PDF-canvas *centre of gravity* is not.
- **Pure reflow reader** (no dialogue). Fixes reading, but the director's job is
  *question-led* — a reader still makes you hunt. Dialogue subsumes it.
- **Canvas-first ideation** (Muse/Heptabase). Powerful for a synthesis sprint,
  but not for *incubation*, and weakest exactly where we're strongest-targeted
  (phone). Promoted to the foldable's dual-pane, not the foundation.

## 10. Recommendation

Build, in order: (1) the **grounded Q&A dialogue** on the **workspace** chassis,
reusing the session/transcript stack, with cited answers back to the References
tile and a Keshav-derived question palette; (2) the **note/excerpt** primitive
with backlinks + resurfacing (incubation); (3) **spin-into** lit-review /task.
Design every step **adaptive** (phone single-pane ↔ foldable dual-pane) from the
start, since retrofitting the large screen later is the expensive path. Resolve
this discussion into a plan (and an ADR for the note primitive + workspace
chassis) once the first-slice fork (§8.1) is chosen.

## Related

- [`reference/project-detail-chassis.md`](../reference/project-detail-chassis.md)
  — the chassis a research workspace would vary.
- [`reference/research-template-spec.md`](../reference/research-template-spec.md)
  — the goal/phase research template (paper / lit-review live here).
- [`reference/glossary.md`](../reference/glossary.md) §10c — document-kind /
  artifact-kind / tile (References = artifact-kind `citation`).
- [`spine/information-architecture.md`](../spine/information-architecture.md) —
  the entity × surface matrix this surface must slot into.
- [`decisions/041-insight-workbench-layout.md`](../decisions/041-insight-workbench-layout.md)
  — the `View ▾` / Navigator / wide-screen patterns reused here.
- [`discussions/desktop-and-web-targets.md`](desktop-and-web-targets.md) —
  large-screen / adaptive-layout direction.
- [`discussions/desktop-research-surface.md`](desktop-research-surface.md) —
  places this reading/ideation surface inside the desktop cockpit and
  reconsiders the "one re-flowing widget tree" delivery assumption (the
  desktop *work* may justify a distinct web-tech workbench).
- [`discussions/tasks-as-first-class-primitive.md`](tasks-as-first-class-primitive.md)
  — the "spin a cluster into directed work" target.
- [`discussions/positioning.md`](positioning.md) — why agent-mediated reading is
  a differentiator, not a me-too reader.
