# Desktop research surface

> **Type:** discussion
> **Status:** Open (2026-07-04) — raised by the director: the mobile app is the
> 24/7 glance-and-nudge cockpit; a *desktop* app is needed for focused day-work
> (designing models / algorithms). Prompted by Anthropic shipping **Claude
> Science** (2026-06-30), a local-first research desktop app whose architecture
> mirrors TermiPod's.
> **Audience:** contributors
> **Last verified vs code:** v1.0.820

**TL;DR.** A desktop research app is **not the mobile app made wide**. Its job is
to serve the work mobile *cannot* — deep paper reading, authoring
reports/slides, debugging code across diffs/logs/traces, thinking on a
graph/canvas, comparing many runs, and capturing decisions — in multi-hour
focused sessions. Derive the app from that work, not from a responsive-layout
pass. Two consequences fall out. (1) The director on desktop is a **PI directing
a lab**, not a coder: the app's centre of gravity is *direct → observe → decide
→ record*, and the agents + the breakglass SSH layer do the typing. (2) The
desktop-shaped jobs are dominated by **mature components the web ecosystem owns**
(code editors, PDF/HTML readers, rich-text/slide authoring, graph canvases,
plotting) — which are precisely Flutter's structural weak spots. That, plus
Claude Science's precedent, pulls the **delivery model** away from "one
re-flowing Flutter tree" and toward a **local-first, hub-served web-tech
workbench** for the *work* surfaces, while the *control-plane* surfaces can keep
reusing the existing app. This doc reasons the work-types, the two-halves split,
and the delivery-model options; it resolves into an ADR once the delivery fork
is chosen.

---

## 1. What prompted this, and the reframe

The mobile app is the load-bearing cockpit (`spine/blueprint.md`): a director
carries it 24/7 to glance, nudge, approve, and unblock. Its constraints are
real and permanent — one thing on screen at a time, touch + voice, seconds of
attention. Those are *virtues* for always-on supervision and *disqualifying* for
a day of focused research.

The director's instruction is the reframe: **the desktop app must serve the
functions mobile cannot do well — it is not a replica of mobile at a larger
size.** A responsive "make the phone screen wider" pass (the option
`desktop-and-web-targets.md` §4 and `research-reading-and-ideation-ui.md` §6 both
lean toward) yields a *roomy phone*. Research doesn't want a roomy phone; it
wants **simultaneous context** and **best-in-class work surfaces**. The design
must therefore be derived **from the work**, and the delivery model chosen to
serve that work — not the other way round.

Two axioms frame everything below:

- **Desktop = focus; mobile = glance.** The delta isn't just screen size. It's
  keyboard-first vs touch, hours vs seconds, *many things at once* vs one, high
  information density welcomed vs overwhelming. A desktop surface earns its
  existence only by doing what a phone structurally can't.
- **The director is a PI, not a coder.** On desktop the human is not writing
  training loops by hand — the **agents** are the grad students; the director is
  the **principal investigator** who poses hypotheses, delegates experiments,
  reads results, and decides. This kills a whole branch of design: the desktop
  app is a *PI's bench*, not an IDE. Editing code and running commands is the
  agents' job; the rare drop-to-metal is the **breakglass SSH/tmux layer** that
  survives from MuxPod (`spine/blueprint.md`). The app's spine is **direct →
  observe → decide → record**.

## 2. Claude Science: validation, and the shape of the gap

Anthropic shipped **Claude Science** on 2026-06-30 — a **local-first desktop
app that opens through a browser**, built around a **coordinating agent**
orchestrating **specialist agents**, with **compute orchestration** over local
GPU / HPC-over-SSH / Modal (it "drafts a plan, asks before reaching new
resources"), **reproducible artifacts** bundling code + environment + plain
description + full message history, data that **never leaves the machine** (only
the context each step needs is sent to Claude), and **fork-the-session** to
compare approaches.

That is TermiPod's blueprint almost line-for-line: coordinating agent =
**steward**; specialist agents = the **fleet**; compute-over-SSH/HPC =
**host-runners on NAT'd GPU boxes + the A2A tunnel**; reproducible artifacts =
**Run / Artifact / Deliverable + the transcript**; "only context is sent, data
stays put" = the **data-ownership law** (the hub owns names + events, hosts hold
bytes); fork-to-compare = session branching. The architecture is validated by a
frontier lab converging on it independently.

The convergence also **defines the gap**, because Claude Science made three
deliberate choices TermiPod does not share:

| | Claude Science | TermiPod's lane |
|---|---|---|
| **Agents** | one coordinating agent + skills, Anthropic-only | a **fleet** across engines (claude-code, codex, kimi, antigravity) |
| **Machines** | your machine (+ SSH/HPC/Modal for compute) | a **multi-host fleet**, first-class, reachable 24/7 from a phone |
| **Surface** | desktop only | a **mobile ↔ desktop continuum** on one hub |

The desktop app should not try to out-notebook Claude Science. It should be what
Claude Science structurally isn't: **the command center for a lab of many agents
on many machines, that the same director also carries in their pocket.** But the
one thing Claude Science's choice *tells* us directly is about delivery — see §5.

## 3. The desktop-shaped work (derive the app from this)

The test for every surface: **does it need focus, keyboard, and simultaneity —
i.e. is it work a phone can't do well?** Walking the ML/algorithm research loop
(hypothesize → design → implement → run → observe → analyze → decide →
synthesize) as a *PI directing a fleet*, the desktop-shaped jobs are:

**J1 — Read papers/reports in depth.** Long-form reading with annotation,
side-by-side (paper ↔ notes), figure-skimming, math, precise selection. The
phone is actively hostile here (two-column PDF, no room for a second pane).
`research-reading-and-ideation-ui.md` already designed the *content* model
(question-led grounded dialogue + backlinked incubation notes); desktop is the
surface where its **dual-pane reading ↔ notes** stops being a foldable
special-case and becomes the default. *Component class:* reflowable HTML/PDF
reader + annotation.

**J2 — Author reports / slides / figures.** Writing the memo, the paper section,
the deck; editing a figure ("log-scale that axis", "drop the gridlines"). This
is sustained keyboard work with live preview, LaTeX/math, tables, and export.
The phone cannot do it at all. *Component class:* rich-text / structured-document
editor + slide layout + figure editing. (Claude Science's headline loop is
exactly "iteratively refine figures and manuscripts until publication-ready.")

**J3 — Debug code and runs.** Reading diffs, stack traces, huge logs, jumping
`file:line`, correlating a failure in the transcript against the code that
produced it. The director does this to *understand and decide*, not to hand-fix
(the agent fixes). *Component class:* code viewer with syntax highlight + diff +
fast huge-log scroll + file tree.

**J4 — Think on a graph / canvas.** Mind-maps, dependency/idea graphs, excerpt
canvases, the citation/related-notes graph that `research-reading-and-ideation-ui.md`
§6 explicitly promotes to the large screen. Incubation-by-connection is a
desktop-shaped cognitive job. *Component class:* performant 2D graph/canvas with
pan/zoom/hit-testing.

**J5 — Compare many runs.** The heart of ML research: side-by-side metric
curves, config diffs, sweep tables, live-updating training loss from several GPU
boxes at once. The run-*detail* surface shipped (trackio/wandb-grade, v1.0.797);
the run-*comparison* wall is the highest-leverage thing that does not yet exist
and is intrinsically wide-screen. *Component class:* dense, live, multi-series
plotting + comparison tables.

**J6 — Capture decisions and findings.** Research is a narrative of hypotheses
and results. TermiPod already has an unusually strong ADR/decision discipline;
desktop is where **decision + finding capture** becomes first-class rather than
an afterthought in a docs folder — linked to the runs that justify them
(provenance). *Component class:* structured authoring + linking (overlaps J2/J4).

**J7 — Fleet mission-control.** What's running where, who's stuck, cost/GPU
burn, attention items — persistent, glanceable *alongside* the work. This is the
one job the mobile app already does well; on desktop it becomes a **pinned rail**
rather than a whole screen. *Component class:* the existing hub client surfaces.

Notice the split that falls out: **J7 (and the dispatch/approve/observe control
loop) is what the current app already does well and is portable. J1–J6 are
document-, editor-, canvas-, and chart-heavy — new, and component-heavy.** That
split is the crux of the delivery decision.

## 4. The two-halves insight

The desktop app is really **two products fused**:

- a **control plane** — fleet rail, dispatch, approvals, transcript/LiveFeed,
  run list, attention. The hub already serves this over a **client-agnostic REST
  + MCP surface**; the Flutter app already renders it well (entities are plain
  JSON maps, no typed Dart classes to port). This half is *portable to any
  client stack* and *already built*.
- a **research workbench** — J1–J6. Dominated by **mature, hard-to-replicate
  components**: code editors (Monaco, CodeMirror), PDF/HTML readers (PDF.js,
  ar5iv-style reflow), rich-text/structured authoring (ProseMirror, Tiptap,
  Lexical), graph/whiteboard canvases (tldraw, Excalidraw, React Flow), and
  plotting (Plotly, Observable Plot). Claude Science's native renderers (3D
  protein structures, genome tracks, chemical structures) are the same story —
  **web-technology components.**

The workbench half is exactly where **Flutter is structurally weakest**:
rich-text editing / IME, native text selection + accessibility, and *embedding
third-party viewers* — `desktop-and-web-targets.md` already records that
`webview_flutter` has **no Linux/Windows** support and is iframe-only on web.
Building J1–J6 in Flutter means reimplementing, at lower quality, components the
web ecosystem has spent a decade perfecting.

**The delivery model must be chosen for the workbench half**, because that's the
half with the harder requirements and the half that embodies "what mobile
cannot do." The control-plane half is not the constraint — it's portable either
way.

## 5. Delivery model — reasoned from the work

The options, each judged against J1–J6 (the workbench) first, J7/control second:

**A. One re-flowing Flutter tree (phone → foldable → desktop).** What the sibling
docs lean toward. *Pro:* maximal reuse, one codebase, one design system, the
control plane comes free. *Con:* forces J1–J6 — the document/editor/graph/chart
work — through Flutter's weakest surfaces exactly where desktop work
concentrates. It satisfies "serve what mobile can't" at the *layout* level
(more panes) but fails it at the *component* level (worse editor/reader/graph
than any web tool the researcher already uses). **Fails the core test.**

**B. Flutter-web console served by the hub.** *Pro:* reuses Dart, gives a URL,
local-first-ish. *Con:* inherits **both** Flutter-web's text/editor/embedding
weaknesses **and** CanvasKit weight — the worst fit precisely for J1–J3 (text
selection, IME, embedding Monaco/PDF.js). Web delivery without web components.

**C. Local-first, hub-served web-tech workbench (recommended class).** A web
application (framework TBD — React/Svelte/Solid), optionally wrapped (Tauri) for
a native shell + local FS/compute access, **served by / launched from the hub**,
consuming the hub's existing REST + MCP. *Pro:* unlocks the mature components
J1–J6 demand; **matches Claude Science's proven model** (local-first desktop
that opens in a browser — chosen for exactly this reason: the work is
document/artifact/figure-render-heavy and a browser surface gives you the whole
web rendering ecosystem while data stays local); the hub is already
client-agnostic so the control plane's data is immediately consumable; can be
delivered incrementally (start with the one workbench surface that hurts most on
mobile). *Con:* a **second client stack** — new build/CI, a second design-system
implementation, and the control-plane surfaces get rebuilt or embedded rather
than reused for free.

**D. Native Flutter-desktop with heavy custom components.** *Pro:* best raw
input/canvas performance, one unified design system, offline-native. *Con:* you
reimplement editor / reader / graph / plotting from scratch — the single most
expensive path, slow to ship, duplicating what the web already perfects. Only
rational if avoiding a second stack is valued above everything.

**E. Hybrid — Flutter-desktop control-plane shell hosting embedded webviews for
J1–J6.** *Pro:* reuse for J7 + web components for the workbench. *Con:*
`webview_flutter` has no Linux/Windows (per the gap doc); two runtimes with a
fragile seam and state-sync pain across the boundary. The worst-of-both risk.

**Where the reasoning points.** The desktop *work* (J1–J6) is document-, editor-,
canvas-, and chart-shaped — the domains the **web ecosystem owns and Flutter is
weakest at** — and this is the same logic that led Claude Science to
browser-through-local. So the work pulls toward **C**. This is a genuine
reversal of the sibling docs' "one Flutter tree" lean, and it is a bigger bet (a
second client stack). The honest middle path that C enables: **keep the Flutter
app as the mobile + control-plane client, build the new workbench surfaces
web-tech, and let the two meet at the hub's API** — the split is real and can be
delivered one surface at a time, starting with whichever of J1–J6 is most
valuable and most mobile-hostile (candidates: J5 run-comparison, or J1/J2 the
reading+authoring pair `research-reading-and-ideation-ui.md` already specced).

**This fork is the load-bearing decision and belongs to the director.** It
determines whether the desktop program is a layout pass on the existing app (A)
or a new local-first web client against the same hub (C).

## 6. A proposed shape (delivery-model-independent)

Whatever the stack, the *IA* the work implies is a three-zone workbench organized
around direct/observe/decide — **not** the phone's five bottom tabs:

- **Left — Fleet & scope rail (persistent, J7):** hosts, running agents,
  attention, cost/GPU. The existing sessions rail promoted to a pinned
  first-class citizen (ADR-041 already contemplates wide-screen pinned rails).
- **Center — the Bench:** mode-switched over the same hub data —
  **Compose** (draft/review an experiment spec, approve a `project.create`,
  dispatch tasks — rides the inline-spec lifecycle plan) · **Compare** (the run
  wall, J5) · **Read/Author** (J1/J2, the `research-reading-and-ideation-ui.md`
  dual-pane) · **Analyze** (the Insight transcript/workbench, panes un-collapsed
  for width) · **Record** (decisions/findings, J6).
- **Right — Inspector (contextual):** details of the selection — a run's
  config/system/metrics, a task, a deliverable, a transcript Navigator.
- **Top — command palette:** keyboard-first dispatch, jump-to, `propose` actions.

The design borrows the *shell grammar* from the IDE workbench (rails + central
surface + palette), the *interleaved narrative* from the notebook (inverted:
agent-driven, a record of a directed session), *run comparison + lineage* from
W&B/MLflow/TensorBoard, *trace drill-down* from Grafana/Perfetto (the hub's OTLP
export already points there), and *artifact-bundling + fork-to-compare* from
Claude Science.

## 7. Open questions / forks

1. **Delivery model (§5).** A (one Flutter tree) vs **C** (local-first web-tech
   workbench, recommended) vs D/E. The director's call; gates everything else.
2. **If C: framework + shell.** Plain browser vs Tauri wrap; which web
   framework; how much design-system to re-express vs. tokens shared from the
   Flutter theme.
3. **Control-plane reuse.** If C, is the control plane (J7 + dispatch/approve)
   rebuilt web-tech, embedded from the Flutter app, or left to mobile with the
   desktop app being *workbench-only* at first?
4. **First surface.** J5 (run-comparison wall — biggest research win, doesn't
   exist) vs J1/J2 (reading+authoring — already specced, most mobile-hostile).
5. **Data path for local compute.** Claude Science's "data never leaves the
   machine; drafts a plan, asks before reaching new resources" — how much of that
   local-first compute-consent flow does the desktop app own vs. defer to the
   host-runner + governed `propose` path already in the hub.
6. **Relationship to hub-tui.** The terminal cockpit (`hub-tui/`) already covers
   the desktop-from-terminal niche; is it a third surface, or does the web
   workbench subsume the cases it serves?

## 8. Recommendation

Treat the desktop app as **derived from J1–J7**, not as a responsive pass on
mobile, and resolve the **delivery-model fork (§5.1)** first — the reasoning
points at **C, a local-first, hub-served web-tech workbench**, reusing the Go hub
(REST + MCP) unchanged as the backbone and the Flutter app as the mobile +
control-plane client, because the desktop *work* lives in the document / editor
/ graph / chart domains the web ecosystem owns and Flutter is weakest at — the
same logic that produced Claude Science. Deliver **one surface at a time**,
starting with the most valuable mobile-hostile job (§7.4). Resolve this
discussion into an **ADR** (desktop delivery model + the two-halves split) plus a
**plan** for the first surface, once the director picks the delivery fork.

## Related

- [`desktop-and-web-targets.md`](desktop-and-web-targets.md) — the *mechanical*
  cross-platform gap inventory (plugins, `dart:io`, CI, density); this doc is its
  *product/role* companion and challenges its "one adaptive Flutter tree" lean.
- [`research-reading-and-ideation-ui.md`](research-reading-and-ideation-ui.md) —
  the reading/ideation *content* model (question-led dialogue + incubation
  notes); this doc places its dual-pane surface inside the desktop cockpit and
  reconsiders its "one re-flowing widget tree" delivery assumption.
- [`positioning.md`](positioning.md) — where the Claude Science competitive
  analysis belongs (fleet / multi-engine / mobile-continuum differentiator).
- [`spine/blueprint.md`](../spine/blueprint.md) — mobile-first as the principal
  surface; breakglass SSH/tmux; the data-ownership law.
- [`decisions/041-insight-workbench-layout.md`](../decisions/041-insight-workbench-layout.md)
  — the `View ▾` / Navigator / wide-screen pinned-rail patterns the Bench reuses.
- [`plans/run-detail-ui.md`](../plans/run-detail-ui.md) — the shipped run-*detail*
  surface that the run-*comparison* wall (J5) extends.
