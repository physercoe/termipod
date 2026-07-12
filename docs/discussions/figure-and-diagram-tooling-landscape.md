# Figure, diagram & chart tools — offline integration landscape

> **Type:** discussion
> **Status:** Open (2026-07-12) — surveys the dominant graph/diagram/figure
> tooling used in reports, papers, and slides, and proposes how to integrate
> "all the dominants" into the desktop workbench beyond the draw.io editor
> already shipped. Director ask: *"what are the main graph/diagram/figures and
> related tools/sdk/library used in report, paper, slides besides drawio — we
> need integrate all the dominants."*
> Feeds [../plans/desktop-workbench-jobs.md](../plans/desktop-workbench-jobs.md);
> extends [author-agent-assist-and-diagrams.md](author-agent-assist-and-diagrams.md)
> (which shipped offline draw.io + the agent companion); relates to
> [research-tooling-landscape.md](research-tooling-landscape.md) (embed vs build).
> **Audience:** contributors · principal
> **Last verified vs code:** desktop v0.3.28 (on main)

**TL;DR.** We already ship two points on this map: **draw.io** (an interactive
box-and-arrow editor, `DiagramEditor.tsx`) and **KaTeX** (inline/display math).
The director wants the whole map — the tools that actually produce the visuals in
technical reports, academic papers, and slide decks. This doc surveys four
families (diagram-as-code · charts/data-viz · freeform+slides · LaTeX/math),
verified against current sources (2026-07-12), filtered by one hard constraint:
**it must render client-side and offline** (no runtime cloud/server) under
permissive-enough licensing, because the desktop is offline-first.

The load-bearing finding is architectural, not a list: **nearly every dominant
tool is a pure `(spec: text|json) → SVG` function that runs in the webview.** So
the integration is *one* abstraction — a **figure-renderer registry** that
extends the existing `Doc { kind, body }` model (`state/documents.ts`) — not
twelve bespoke embeds. A figure becomes a document/block whose `body` is the
source spec and whose `kind` selects a lazily-loaded renderer. This is
**agent-native by construction**: an agent authors the spec *as data* (Mermaid
text, a Vega-Lite JSON, a DOT string) exactly the way it already authors
Markdown. Three integration *shapes* fall out — **renderer** (spec→SVG, the
majority), **editor** (interactive React component: draw.io, Excalidraw,
bpmn-js), and **artifact** (a byte deliverable produced by an agent:
matplotlib, full LaTeX, PlantUML) — the last mapping cleanly onto our
hub=metadata / hosts=bytes / agent=executor law.

---

## 1. The gap — what we have vs. what papers/reports/slides actually use

| Visual class | Dominant real-world tools | We ship |
|---|---|---|
| Box-and-arrow / architecture | draw.io, Mermaid, Graphviz, PlantUML, D2 | draw.io ✅ |
| Data charts / plots | matplotlib, Plotly, Vega/Vega-Lite, ECharts, Chart.js | — |
| Graph / dependency / FSM | Graphviz (DOT) | — |
| UML / sequence / BPMN | Mermaid, PlantUML, nomnoml, bpmn-js | partial (drawio manual) |
| C4 architecture | Structurizr, LikeC4 | — |
| Timing / hardware | WaveDrom | — |
| Freeform sketch / whiteboard | Excalidraw, tldraw | our custom Canvas ✅ (different niche) |
| Slides | PowerPoint/Keynote, Reveal.js, Marp, Slidev | — |
| Math | LaTeX math, MathJax | KaTeX ✅ |
| TikZ / scientific vector | TikZ/PGF (LaTeX) | — |
| Chemistry structures | SMILES (RDKit, SmilesDrawer) | — |

The white space is large, but most of it is reachable with one architecture.

---

## 2. The unifying architecture — a figure-renderer registry

The existing model already anticipates this:

```ts
// state/documents.ts (today)
export type DocKind = 'markdown' | 'diagram';
export interface Doc { id; kind: DocKind; title; body; filePath?; dirty?; updatedAt }
// body = markdown source, OR draw.io XML for a diagram
```

**Proposal.** Generalise `body` (already "source spec") and turn `kind` (or a new
`spec:` discriminator on a `figure` kind) into a key into a **renderer registry**:

```ts
type FigureSpec = 'mermaid' | 'graphviz' | 'vega-lite' | 'nomnoml'
                | 'wavedrom' | 'likec4' | 'echarts' | 'tikz' | ...;

interface FigureRenderer {
  spec: FigureSpec;
  label: string;
  load: () => Promise<(src: string) => Promise<string /* SVG */>>; // lazy import()
  sample: string;               // starter source (agent + human)
  schemaUrl?: string;           // JSON-schema for agent authorship (Vega-Lite, etc.)
}
```

Consequences that make this the right shape:

- **One code path, N tools.** Adding WaveDrom or nomnoml after the registry
  exists is a table row + a lazy `import()`, not a new surface. (Matches
  "behaviour is data" — the same principle that makes a new engine a YAML file.)
- **Lazy-loaded weight.** Mermaid (~2.5–3 MB), Graphviz-WASM (~1–3 MB), Vega
  (~1 MB) never touch app boot; each renderer `import()`s on first use of its
  `spec`. No regression to the shell's startup budget.
- **Agent-native authorship.** The figure *is* its source text/JSON — an agent
  writes a Mermaid block or a Vega-Lite spec the same way it writes prose, and
  the AgentCompanion can `onInsert` a figure block into a document. Ship the
  JSON-schema (Vega-Lite/ECharts have them) so agents self-validate.
- **Export for free-ish.** Every renderer already emits **SVG**; PNG is a
  rasterise-the-SVG helper. That is exactly what a report/paper figure needs
  (crisp vector), and it is uniform across tools.
- **Round-trips to the hub as data.** A figure document is small text — it lives
  happily as a hub Document/Reference blob (metadata), not bytes; rendering stays
  on the client. Only the *rasterised* export is bytes.

### Three integration shapes (pick per tool, not per family)

1. **Renderer** — `spec → SVG` pure function, lazy-loaded into the registry.
   *The default and the majority.* Mermaid, Graphviz, Vega-Lite, ECharts,
   nomnoml, WaveDrom, LikeC4, (TikZ via a worker).
2. **Editor** — a stateful interactive React component / embedded app, like the
   draw.io editor we already ship. Excalidraw and bpmn-js are editors, not
   renderers — they get a `DiagramEditor`-shaped mount, not a registry row.
3. **Artifact** — the tool cannot run in a webview (needs a JVM, CPython, or a
   full TeX Live). An **agent produces the figure on a host** and returns
   SVG/PNG/PDF bytes we embed. matplotlib, full LaTeX→PDF, PlantUML. This is not
   a compromise — it is the correct architecture for tools whose value is a
   large, evolving external dependency closure.

---

## 3. The four families (verified 2026-07-12)

Legend: **Shape** = Renderer / Editor / Artifact. License flags called out.

### 3.1 Diagram-as-code (text → diagram)

| Tool | Niche | Offline | License | Shape | Weight |
|---|---|---|---|---|---|
| **Mermaid** | 20+ types; **native GitHub/GitLab rendering**; the default | ✅ pure JS | MIT | Renderer | Heavy ~2.5–3 MB, split |
| **Graphviz** `@hpcc-js/wasm-graphviz` | DOT graphs: deps/call-graphs/FSM/trees | ✅ WASM | Apache-2.0 | Renderer | ~1–3 MB async |
| **nomnoml** | Lightweight UML class diagrams | ✅ pure JS | MIT | Renderer | Tiny (~tens KB) |
| **WaveDrom** | Digital timing / bitfield (only tool in niche) | ✅ pure JS | MIT | Renderer | Light ~100–250 KB |
| **LikeC4** | C4 architecture (**pure-JS Structurizr replacement**) | ✅ pure JS (React Flow) | MIT | Editor/Renderer | Few 100 KB–low MB |
| **bpmn-js** | BPMN 2.0 process diagrams (standard) | ✅ pure JS | MIT ⚠️ *watermark clause* | Editor | ~300–500 KB |
| **D2** | Modern architecture DSL | ✅ WASM-in-worker (dagre/ELK; TALA is paid) | ⚠️ **MPL-2.0** | Renderer | Heaviest, multi-MB |
| **PlantUML** | UML DSL, huge install base | ❌ **Java engine**; only browser path (CheerpJ) is proprietary/CDN/paid; permissive TeaVM port discontinued | LGPL-3.0 engine | **Artifact** | — |
| **Structurizr** | Canonical C4-as-code | ❌ DSL parser + Lite are **Java/Spring** | Apache-2.0 | **Artifact** / use LikeC4 | — |

*Coverage:* **Mermaid + Graphviz alone cover ~80%** of general text-to-diagram
demand. The rest fill specific niches (timing, BPMN, C4, quick UML).

### 3.2 Charts / data-visualisation

| Tool | Niche | Spec-as-data? | Offline | License | Weight (gz) |
|---|---|---|---|---|---|
| **Vega-Lite** (+vega+vega-embed) | Statistical/publication figures, grammar-of-graphics | ★★★ **pure JSON doc** + JSON-schema | ✅ pure JS | BSD-3 | ~300–400 KB |
| **Apache ECharts** | Interactive dashboards, network/geo/large-data | ★★★ `option` object | ✅ pure JS, tree-shake | Apache-2.0 | ~360 KB full |
| **Plotly.js** | **3D / scientific / geo** (matplotlib-adjacent breadth) | ★★★ trace+layout JSON | ✅ pure JS | MIT | ⚠️ 1.33 MB (use partial dist) |
| Chart.js | Simple dashboard charts | ★★ config | ✅ | MIT | 67 KB — but **Canvas-only, no SVG export** |
| D3 / Observable Plot / Recharts / Nivo | bespoke / React charts | ✗ imperative JS/JSX | ✅ | MIT/ISC | — (not "figure as data") |
| **matplotlib** | **the actual source of most paper figures** | ✗ Python | ❌ client-side (Pyodide = ~25–30 MB) | — | **Artifact** |

*Pick:* **Vega-Lite** is the "paper figure authored as a declarative document"
winner (one JSON → interactive on screen **and** static SVG/PNG export).
**ECharts** for interactive fleet/dashboards. **Plotly (partial dist)** only when
3D/geo is needed. The "figure-as-data" filter eliminates D3/Recharts/Nivo (they
need the agent to emit *code*, not a storable spec). matplotlib parity is the
**artifact** path (§3.4 / §4).

### 3.3 Freeform sketch + slides

| Tool | Role | Offline | License | Shape |
|---|---|---|---|---|
| **Excalidraw** `@excalidraw/excalidraw` | Hand-drawn sketch/whiteboard; `.excalidraw` JSON is agent-authorable & an ecosystem standard | ✅ React component (self-host fonts) | **MIT** | **Editor** — top freeform pick |
| **tldraw** | Infinite-canvas SDK | ✅ | ❌ **proprietary SDK license**: watermark on free tier, ~$6k/yr commercial, refuses prod without key (SDK 4.0) | **Reject** |
| Konva / Fabric.js | canvas infra (what a custom canvas is built on) | ✅ | MIT | infra only |
| **Marp** `@marp-team/marp-core` | **Markdown → slides** — deck *is a `.md` document* | ✅ pure JS render fn | MIT | Renderer (doc type) |
| Reveal.js | HTML deck runtime (offline, zero-dep) | ✅ | MIT | pairs w/ Marp |
| Spectacle | React (JSX) slides — code, not data | ✅ | MIT | weaker doc semantics |
| Slidev | Most-starred, but **Vue + full app toolchain** | ⚠️ | MIT | poor fit for React/Vite webview |

*Pick:* **Excalidraw** complements our custom Canvas + draw.io with the
sketchy/conceptual niche (MIT, zero license risk — unlike tldraw). **Marp
Markdown** is the natural "slides as a document type" (author-as-data), rendered
offline; Reveal.js is the presentation runtime if we want present-mode.

### 3.4 Math · LaTeX · scientific

| Tool | Role | Offline | License | Verdict |
|---|---|---|---|---|
| **KaTeX** (have) | Fast LaTeX math, HTML | ✅ | MIT | **Keep as default** |
| **MathJax v4** | LaTeX+MathML+AsciiMath → **SVG** math, `physics` pkg, a11y/speech, auto line-break | ✅ ES6 modules | Apache-2.0 | **Add opt-in** for SVG export / `physics` / coverage KaTeX lacks (mhchem is on *both* — not a reason) |
| **node-tikzjax** | Real TeX→SVG for **TikZ/PGF** snippets; preloads CircuiTikZ/chemfig/pgfplots/tikz-cd | ✅ WASM | LPPL-1.3c | **Add lazy + Web-Worker + serialise renders** (multi-MB first load, single global instance) |
| **SmilesDrawer** | SMILES → chemistry structures | ✅ pure JS | MIT | **Conditional** — tiny; add iff chemistry is a target |
| RDKit-JS | accurate cheminformatics | ✅ WASM | BSD | only if real chem needed (multi-MB) |
| **SwiftLaTeX / full LaTeX→PDF** | compile whole figures/docs | ⚠️ engine works but **TeX Live closure breaks offline** (remote file fetch or huge bundle) + **AGPL-family risk** | mixed | **Artifact** (agent compiles on host) |
| **matplotlib / seaborn** | most paper figures | ❌ (Pyodide ~25–30 MB, lazy fallback only) | — | **Artifact** (agent runs Python → SVG/PNG/PGF) |

*Note:* PlantUML, Structurizr, full LaTeX, and matplotlib share one property —
they need a **large external runtime/dependency closure** (JVM, Spring, TeX Live,
CPython) that is hostile to a bundled offline webview. All four are the
**artifact** shape: the agent produces the figure on a host, we embed the bytes.

---

## 4. Ranked master plan — phased rollout on one registry

**Phase A — the registry + the 80% (highest ROI).** Build the figure-renderer
registry (extend `Doc`/`DocKind`, lazy `import()` per spec, SVG→PNG export
helper, an "insert figure" affordance the AgentCompanion can drive). Land the
three that cover the most ground: **Mermaid** (general diagrams), **Graphviz**
(graphs), **Vega-Lite** (data figures as JSON). All pure client-side, permissive,
agent-authorable.

**Phase B — fill the niches (cheap on the registry).** **nomnoml** (quick UML),
**WaveDrom** (timing), **LikeC4** (C4), **ECharts** (interactive dashboards). Each
is one registry row + a lazy import.

**Phase C — interactive editors (draw.io-shaped).** **Excalidraw** (freeform
sketch; MIT) and **bpmn-js** (BPMN; mind the watermark clause). These mount like
the existing `DiagramEditor`, not as registry renderers.

**Phase D — slides + math escalation.** **Marp** Markdown as a slide document
type (+ optional Reveal.js present-mode); **MathJax v4** opt-in renderer for
SVG-export / `physics`; **node-tikzjax** (lazy, worker) for TikZ snippets. Add
**SmilesDrawer** iff chemistry becomes a target surface.

**Phase E — the agent-artifact pipeline.** A "produce figure" path where an agent
runs on a host and returns SVG/PNG/PDF: **matplotlib/seaborn**, **full LaTeX→PDF**,
**PlantUML** (JVM). This unifies with the AgentCompanion + host-runner work and
honours hub=metadata / hosts=bytes. The figure lands as a Deliverable/Artifact,
not a client render.

---

## 5. License gates (decisions the director owns)

- **D2 — MPL-2.0.** Weak file-level copyleft; fails a strict MIT/Apache/BSD gate
  though it does not infect our TS. Its niche overlaps Mermaid + LikeC4 (both
  cleaner). *Recommendation:* **defer** unless MPL-2.0 is explicitly accepted.
- **bpmn-js — MIT with a "Powered by bpmn.io" watermark/attribution clause.**
  Product-visible; removal needs permission. *Recommendation:* accept the
  watermark for Phase C, or seek removal.
- **tldraw — proprietary SDK license (watermark / ~$6k-yr / prod-key).**
  *Recommendation:* **reject** — confirms our existing not-tldraw stance; use
  Excalidraw.
- **Vega-Lite — BSD-3-Clause.** Fine to bundle; note attribution.
- **SwiftLaTeX / full-LaTeX — AGPL-family risk** on parts of the stack. Verify
  before ever shipping client-side; the artifact path sidesteps it.
- **PlantUML offline — no permissive path exists** in mid-2026 (CheerpJ is
  proprietary/paid; the permissive TeaVM+Viz.js port is discontinued). Artifact
  path (JVM sidecar on a host) only.

---

## 6. Open questions

1. **`kind` vs `spec` discriminator.** Do figures become new `DocKind`s
   (`figure-mermaid`, …) or a single `figure` kind with a `spec:` field? A single
   kind + `spec` keeps the tab/editor switch small and the registry central —
   leaning that way.
2. **Where do figures live in the UI?** As their own Author documents (like
   `diagram`), *and/or* as inline fenced blocks inside a Markdown document
   (```` ```mermaid ````), rendered in the split preview? The fenced-block path
   is the most paper/report-native and the most agent-native (GitHub-identical),
   but needs a Markdown-renderer plug-in point. Likely **both**: fenced blocks in
   Markdown + standalone figure docs sharing the same registry.
3. **Export targets.** SVG is free everywhere; is PNG rasterisation enough, or do
   we need PDF/PGF (which pushes toward the artifact pipeline)?
4. **Scope of Phase A.** Confirm Mermaid + Graphviz + Vega-Lite as the first three
   (recommended), vs. leading with a different mix.
5. **Artifact pipeline dependency.** Phase E needs the host-runner + a
   "figure job" contract; it is gated on the Windows/host-runner decisions in
   [author-agent-assist-and-diagrams.md](author-agent-assist-and-diagrams.md).

---

## 7. Recommendation

Adopt the **figure-renderer registry** as the spine, and integrate in the phased
order above. It turns "integrate all the dominants" from a dozen one-off embeds
into one agent-native abstraction where a figure is a document whose body is its
source spec. Start with **Phase A (registry + Mermaid + Graphviz + Vega-Lite)** —
that single wedge covers general diagrams, graphs, and paper-quality data figures,
all authored as data, all rendered offline, all exportable to SVG. The remaining
phases are additive rows and two editor mounts; the genuinely server-bound tools
(PlantUML, matplotlib, full LaTeX) are deferred to the agent-artifact path where
they architecturally belong.
