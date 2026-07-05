# 050. Desktop research workbench — delivery model

> **Type:** decision
> **Status:** Accepted (2026-07-04) · **Amended (2026-07-05)** — director-directed.
> Chose option **C** over the "one adaptive Flutter tree" lean of the sibling
> discussion docs, after a full research-lifecycle × tooling landscape survey. The
> 2026-07-05 amendment resolves two open forks (cross-platform Tauri shell;
> unified web-tech client with the control plane rebuilt, not embedded) — see
> Amendment below.
> **Audience:** contributors · maintainers
> **Last verified vs code:** v1.0.820

**TL;DR.** TermiPod's **desktop** research surface is a **local-first,
web-technology application, served by / launched from the hub**, consuming the
hub's existing client-agnostic REST + MCP API — a *second client*, not a
wide-screen Flutter layout. It is derived from the **work** a director does on
desktop (deep reading, authoring, debugging, graph-thinking, multi-run
comparison, decision capture), which lives in the document / editor / canvas /
chart domains the **web ecosystem owns** and Flutter is weakest at. The app
splits in two: a **portable control plane** (fleet / dispatch / approve /
transcript — already served well over the hub API) and a **component-heavy
research workbench** (the reason for the web-tech choice). The operative rule per
capability is **build · embed · integrate · interop**, registered in
[`discussions/research-tooling-landscape.md`](../discussions/research-tooling-landscape.md).

## Context

The mobile app is the 24/7 glance-and-nudge cockpit; it is structurally wrong
for a day of focused research (one pane, touch, seconds of attention). The
director asked for a desktop app that serves **the work mobile cannot do** — not
a responsive re-flow of the phone.

Three forces set the decision (full reasoning in
[`discussions/desktop-research-surface.md`](../discussions/desktop-research-surface.md)):

1. **Desktop = focus, and the director is a PI, not a coder.** The human poses
   hypotheses, delegates experiments to the fleet, reads results, decides. The
   agents write the code; the breakglass SSH/tmux layer covers drop-to-metal.
   The app's spine is *direct → observe → decide → record*.
2. **The desktop-shaped jobs are web-ecosystem-shaped.** Deep paper reading,
   authoring reports/slides, debugging code across diffs/logs, graph/canvas
   thinking, and multi-run comparison are dominated by **mature web/JS
   components** (Monaco/CodeMirror, PDF readers, ProseMirror/Tiptap/BlockNote,
   tldraw, Plotly/Vega-Lite/ECharts) — precisely Flutter's structural weak spots
   (rich-text/IME, native selection, embedding third-party viewers;
   `webview_flutter` has no Linux/Windows).
3. **Precedent.** Anthropic's **Claude Science** (2026-06-30) is a local-first
   desktop app with a **browser-rendered UI**, chosen for exactly this reason —
   the work is document/artifact/figure-render-heavy and a browser surface
   unlocks the whole web rendering ecosystem while data stays on the machine.

The sibling docs
([`desktop-and-web-targets.md`](../discussions/desktop-and-web-targets.md),
[`research-reading-and-ideation-ui.md`](../discussions/research-reading-and-ideation-ui.md))
leaned toward one re-flowing Flutter widget tree (phone → foldable → desktop).
That satisfies the *layout* delta (more panes) but fails at the *component*
level — it forces the highest-value desktop surfaces through Flutter's weakest
path. The director chose the alternative.

## Decision

- **D-1 — Web-tech, local-first, hub-served.** The desktop surface is a **web
  application** (framework TBD; optionally a native shell such as Tauri),
  **local-first** (runs on the director's machine, pointed at their hub), and
  **served by / launched from the hub**. It is **not** Flutter-desktop, **not**
  Flutter-web (which inherits Flutter's editor/embedding weaknesses without the
  reuse), and **not** a native from-scratch rebuild.

- **D-2 — The two-halves split is the design frame.** The app is a **control
  plane** (fleet rail, dispatch, approvals, transcript/LiveFeed, run list —
  portable, already well-served by the hub API) fused with a **research
  workbench** (deep reading, authoring, debugging, graph-thinking, run
  comparison, decision capture — the component-heavy half). The delivery model is
  chosen *for the workbench half*; the control-plane half is portable either way.

- **D-3 — The operative rule is build · embed · integrate · interop.** Per
  capability: **EMBED** a mature web/JS component where one exists; **INTEGRATE**
  an external service via API where the capability is a whole product; **BUILD**
  only the *fleet-native* surfaces the hub's own data uniquely enables; **INTEROP**
  (import/export) everything else. The default is *not* to build. The
  per-capability register is
  [`discussions/research-tooling-landscape.md`](../discussions/research-tooling-landscape.md);
  its headline BUILD is the **multi-run comparison wall** (no embeddable OSS
  component exists, and the data already lives in the hub's digest + `agent_turns`).

- **D-4 — The hub is the unchanged backbone.** The Go hub's REST + MCP surface is
  client-agnostic; the desktop client consumes it without hub changes beyond
  additive endpoints. The **data-ownership law** holds — bytes stay on hosts,
  only the context each step needs reaches a model. Governed **compute-consent**
  (a `compute_plan` proposed by a steward, approved by the director before spend)
  becomes a first-class hub primitive, matching the plan-then-ask UX now common
  to Claude Science / SkyPilot / Modal.

- **D-5 — Two clients, one API.** The Flutter app remains the **mobile +
  control-plane** client; the web app is the **desktop workbench**; they meet at
  the hub API. Deliver **one desktop surface at a time**, starting with the most
  valuable mobile-hostile job. This is the accepted cost of the decision (see
  Consequences).

## Consequences

**Easier / unlocked:**
- Best-in-class work surfaces (code/diff, PDF/HTML reading, rich-text authoring,
  graph canvas, dense live charts) by embedding proven web components instead of
  reimplementing them at lower quality in Flutter.
- The multi-run comparison wall — the highest-leverage missing research surface —
  built directly on the hub's digest/`agent_turns`/OTLP data, local-first and
  air-gappable.
- The differentiators hold: multi-engine, multi-host, self-hosted, data-on-hosts,
  governance, and the mobile↔desktop continuum on one hub — none of which Claude
  Science offers.

**Harder / cost:**
- A **second client stack** — a new build/CI pipeline and a **second expression
  of the design system**. Design-system divergence between the Flutter and web
  clients is the principal risk; mitigate by sharing design tokens (ADR-047) as
  the single source of truth across both.
- Two clients to maintain. Accepted deliberately: the control-plane half can lag
  on the desktop client (or be embedded/deferred to mobile) while the workbench
  surfaces ship first.

**Unaffected:**
- `hub-tui/` (the terminal cockpit) is orthogonal and continues to serve the
  desktop-from-terminal niche.
- The mobile app and its IA are unchanged by this decision.

## Amendment (2026-07-05) — cross-platform shell + unified client

The director resolved the two delivery forks left open in
[`research-tooling-landscape.md`](../discussions/research-tooling-landscape.md) §6:

- **A-1 — Cross-platform native shell (Tauri).** The web app is packaged as a
  portable desktop app for **Windows, macOS, and Linux** via a **Tauri** shell
  (Rust core + the OS-native webview): one web codebase → three native installers,
  small footprint, and the local filesystem + OS integration the local-first
  requirements need (data-on-host access, local compute-consent). The identical
  build also runs in a plain browser against a remote hub (the no-install path).
  Electron is the fallback only if a Tauri capability gap appears.
- **A-2 — Unified web-tech client (control plane rebuilt, not embedded).** The
  desktop app is a **single web-tech client covering both halves** — the control
  plane (fleet / dispatch / approve / transcript) is **rebuilt in web-tech**, not
  embedded from Flutter and not deferred to mobile. The two-halves split (D-2)
  stays the *design* frame; there is one runtime and one design-system expression.
  Consequence: the design-system-divergence risk now spans the whole desktop
  surface, so **shared design tokens ([ADR-047](047-design-system-enforcement.md))
  as the single source of truth across Flutter + web is now load-bearing**, not
  optional. Flutter remains the mobile client; the hub API is the meeting point.
  The concrete stack (Tauri v2 + React + TypeScript, and the DTCG token pipeline
  that carries ADR-047 to the web client) is decided in
  [ADR-051](051-desktop-client-stack.md).

Two director directives that extend *beyond* the delivery model open their own
companion discussions (not part of this ADR's decision, cross-linked here):
- **Embodied-AI / simulator pilot** — the first research field is embodied AI /
  robotics; the workbench must interoperate with Isaac Lab and other simulators.
  See [`discussions/embodied-ai-research-workbench.md`](../discussions/embodied-ai-research-workbench.md).
- **Composable research-material data model** — all materials retrievable across
  machines, and any paper/report/digest decomposable into reusable elements
  (figure / table / chart / quote / …) for recomposition. See
  [`discussions/research-material-data-model.md`](../discussions/research-material-data-model.md).
- **Skills & memory management** — first-class surfaces for what the fleet can *do*
  (skills) and *knows* (memory); memory shares the research-material knowledge
  substrate. See [`discussions/agent-skills-and-memory-management.md`](../discussions/agent-skills-and-memory-management.md).

## References

- Discussions: [`desktop-research-surface.md`](../discussions/desktop-research-surface.md)
  (the role/work derivation), [`research-tooling-landscape.md`](../discussions/research-tooling-landscape.md)
  (the per-capability build/embed/integrate register + competitive survey),
  [`desktop-and-web-targets.md`](../discussions/desktop-and-web-targets.md)
  (the mechanical cross-platform gap),
  [`positioning.md`](../discussions/positioning.md) §3 (Claude Science axis).
- Related ADRs: [038](038-per-run-event-digest.md) + [045](045-hub-storage-scaling.md)
  (the digest / `agent_turns` / OTLP substrate the comparison wall builds on);
  [041](041-insight-workbench-layout.md) (workbench-layout patterns reused);
  [047](047-design-system-enforcement.md) (shared design tokens — the divergence
  mitigation).
- Axiom: [`spine/blueprint.md`](../spine/blueprint.md) — the data-ownership law
  and mobile-first-cockpit framing this decision extends to a second surface.
