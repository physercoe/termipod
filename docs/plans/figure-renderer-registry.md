# Figure-renderer registry — Phases A–C

> **Type:** plan
> **Status:** In progress (2026-07-21) — executes Phases **A–C** of
> [figure-and-diagram-tooling-landscape.md](../discussions/figure-and-diagram-tooling-landscape.md)
> §4 (the registry + the 80%, the niche renderers, the two interactive
> editors). Phases D (slides + math escalation) and E (agent-artifact
> pipeline) stay in the discussion doc until this plan ships.
> Feeds [desktop-workbench-jobs.md](desktop-workbench-jobs.md) (J2 Author);
> relates to [author-agent-assist-and-diagrams.md](../discussions/author-agent-assist-and-diagrams.md)
> (AgentCompanion, draw.io precedents).
> **Audience:** contributors · principal
> **Last verified vs code:** desktop 2026.722.818 (on main)
>
> **Progress:** **Phase A** shipped (`1f46c3b6` — Mermaid + Graphviz +
> Vega-Lite). **Phase B** shipped (`71e0e328` — nomnoml + WaveDrom + ECharts);
> the LikeC4 spike (§3) resolved it to a Phase C editor-mount candidate, **not** a
> registry row (see §3). **Phase C** in progress: **Excalidraw shipped**;
> **bpmn-js held** on the license gate (§4).

**TL;DR.** One new abstraction — a **figure-renderer registry**
(`state/figures.ts`) keyed by a `spec` discriminator on a new single `figure`
document kind — then three rounds of table rows on top of it. **Phase A** builds
the registry, a split source↔SVG-preview `FigureEditor`, SVG/PNG export, fenced
```` ```mermaid ````-block rendering in the Markdown preview, and lands
**Mermaid + Graphviz + Vega-Lite** (~80% of text-to-figure demand). **Phase B**
adds **nomnoml, WaveDrom, ECharts** (and evaluates LikeC4) as registry rows.
**Phase C** mounts the two interactive editors — **Excalidraw** and (behind its
license gate) **bpmn-js** — as new document kinds beside `diagram`/`canvas`,
following the shipped kind-per-format precedent. Every renderer is a lazy
`import()`; nothing touches app boot. `ChartView` keeps its niche (ambient run
metrics); Vega-Lite is for *authored* figures.

---

## 1. Decisions this plan locks in

The landscape doc left six open questions; this plan resolves the ones Phases
A–C need and defers the rest.

1. **`figure` kind + `spec` field (OQ 1).** Renderer-shaped tools become **one**
   new `DocKind` (`'figure'`) with a `spec: FigureSpec` field on `Doc`, *not* a
   kind per tool. Rationale: the `AuthorSurface` editor switch and tab-strip
   stay small, and adding a renderer is a registry row — the whole point.
   The kind-per-format precedent (`canvas`/`table`, 2026-07-16) still governs
   **editor-shaped** tools: Excalidraw and bpmn-js in Phase C get their own
   kinds, exactly like `diagram` and `canvas` did, because each is a distinct
   interactive surface, not a `spec → SVG` function.
2. **File round-trip per spec (OQ 1).** The registry owns the extension map
   (`ext` per spec): `.mmd` → mermaid, `.dot`/`.gv` → graphviz, `.vl.json` →
   vega-lite (Phase B: `.nomnoml`, `.wavedrom.json`, `.echarts.json`; Phase C:
   `.excalidraw`, `.bpmn`). `kindForFile`'s `.json` sniff order becomes: table
   (`isTableBody`) → vega-lite (`$schema` contains `vega-lite`) → wavedrom/
   echarts (registry sniffers) → markdown fallback, so arbitrary JSON is still
   never hijacked.
3. **Fenced blocks render in the preview only (OQ 2).** Round 1 wires the
   registry into `ui/Markdown.tsx`'s `code` component override (the plug-in
   point already exists — rehype-highlight uses the same seam), so
   ```` ```mermaid ```` / ```` ```dot ```` / ```` ```vega-lite ```` render in
   the split preview, the Read surface, and agent transcripts for free. The
   Milkdown WYSIWYG editor keeps showing the fence as a code block — acceptable
   for round 1; upgrading it is an open question (§6.1), not a blocker.
4. **ChartView boundary (OQ 6).** Unchanged and explicit: `ui/ChartView.tsx`
   keeps ambient rendering of run metrics and chart-shaped agent JSON
   (J5 Compare's "no charting library" stance stands); Vega-Lite/ECharts serve
   **authored** figure documents only. Neither replaces the other.
5. **Desktop-only for now (OQ 7).** Figure docs are device-local/file-backed
   like every other Author doc; nothing syncs to mobile in A–C, so no mobile
   renderer is required yet. The mobile story stays open in the landscape doc.
6. **Weight policy.** Every renderer loads via dynamic `import()` on first use
   of its spec (same policy as the lazy `TableEditor`/`MarkdownEditor`), keeping
   the shell under its startup budget (workbench-jobs OQ 1). draw.io's
   install-on-demand path stays the escape hatch for anything heavier than a
   few MB — not needed for A–C.

---

## 2. Phase A — registry + Mermaid + Graphviz + Vega-Lite

The wedge that covers general diagrams, graphs, and paper-quality data figures.

### A1 · `state/figures.ts` — the registry

```ts
export type FigureSpec = 'mermaid' | 'graphviz' | 'vega-lite'; // grows in B
export interface FigureRenderer {
  spec: FigureSpec;
  labelKey: string;              // i18n key, en + zh
  ext: string;                   // on-disk extension (decision §1.2)
  fence: string[];               // fenced-block languages: ['mermaid'], ['dot','graphviz'], …
  sample: string;                // seed body for a new doc (human + agent starter)
  sniffJson?: (body: string) => boolean; // .json disambiguation (vega-lite)
  load: () => Promise<(src: string) => Promise<string /* SVG */>>; // lazy import()
}
export const FIGURES: FigureRenderer[] = [ /* one row per tool */ ];
```

Each `load()` wraps the library's own API into the uniform `src → SVG` shape:
`mermaid.render()`, `@hpcc-js/wasm-graphviz`'s `Graphviz.dot()`, and
`vega-embed` → `view.toSVG()`. Render errors return as a typed failure the
editor and the fenced-block renderer both surface inline (message + source
line where the tool reports one) — never a blank pane.

### A2 · `state/documents.ts` — the `figure` kind

- `DocKind` += `'figure'`; `Doc` += optional `spec?: FigureSpec` (additive —
  persisted `termipod.documents.v1` blobs are unaffected).
- `seedBody('figure')` delegates to the registry row's `sample`.
- `extForKind` grows a doc-aware variant (`extForDoc`) so a figure saves as its
  spec's extension; `kindForFile` maps registry extensions → `figure` + spec,
  with the `.json` sniff order from §1.2.

### A3 · `surfaces/FigureEditor.tsx` — the editing surface

Split source ↔ preview, mirroring the Markdown editor's shape: CodeMirror
source pane (deps already shipped) · debounced live render (reuse the #311
preview-debounce pattern) · inline error strip · toolbar with **Export SVG /
Export PNG** and a spec badge. Mounts from the `AuthorSurface` kind switch
(`AuthorSurface.tsx:429` region) exactly like `DiagramEditor`/`CanvasEditor`.
PNG export is one shared helper: SVG string → `Blob` URL → `<img>` →
`canvas.drawImage` → `toBlob('image/png')` at 2× for crispness.

### A4 · Creation + chrome

`AuthorNav` new-document menu gains one **Figure ▸** group (Mermaid · Graphviz
· Vega-Lite, driven by the registry, not hardcoded); `docKindIcon` gets a
`figure` icon; en + zh strings for every label (house rule).

### A5 · AgentCompanion wiring

For `figure` docs the companion's `context.build()` includes the spec name and
current source, and `onInsert` becomes **replace-body** (a figure body is a
single spec, unlike prose append; the existing guard that blocks append into
structured bodies at `AuthorSurface.tsx:436-440` extends naturally). The
prompt context names the spec's fenced tag so agents answer in the right
dialect; Vega-Lite's JSON-schema URL ships in the registry row for
self-validation.

### A6 · Fenced blocks in Markdown

`ui/Markdown.tsx` `code` override: when the fence language matches a registry
row's `fence` list, lazy-load that renderer and swap the highlighted block for
the SVG (with the code shown on render error). This lands figures in the
Author preview, Read surface, and transcripts in one change.

### Dependencies added (all lazy-chunked)

| Package | License | Weight (approx, gz) |
|---|---|---|
| `mermaid` | MIT | ~800 KB–1 MB chunk (2.5–3 MB raw, code-split) |
| `@hpcc-js/wasm-graphviz` | Apache-2.0 | ~1 MB WASM, async |
| `vega` + `vega-lite` + `vega-embed` | BSD-3 | ~300–400 KB |

### Acceptance criteria

- New Figure▸Mermaid doc: type a graph, see live SVG, save → `.mmd`, reopen →
  same doc kind + spec; likewise `.dot` and `.vl.json` (incl. sniff on open).
- ```` ```mermaid ```` fence in a Markdown doc renders in the split preview;
  a syntax error shows the error + source, not a blank.
- Export SVG/PNG from all three specs produces valid files.
- `vite build` chunk report shows no renderer code in the entry chunk; app
  boot time unchanged.
- Companion "insert" replaces a figure body; asking it for "a sequence diagram
  of X" round-trips into a rendering doc.

---

## 3. Phase B — niche renderers (rows on the registry)

Each item is: registry row + lazy `import()` + sample + extension/fence/sniff
+ i18n label. No new surfaces.

| Row | Package | License | Notes |
|---|---|---|---|
| `nomnoml` | `nomnoml` | MIT | tiny; quick UML classes |
| `wavedrom` | `wavedrom` | MIT | timing/bitfield; JSON body, `sniffJson` |
| `echarts` | `echarts` (tree-shaken, **SVG renderer**) | Apache-2.0 | `option` JSON body; authored dashboards only (ChartView boundary, §1.4) |
| `likec4` | *spike done → **not a row*** | MIT | **Spike result (2026-07-22):** `likec4` exports only `./react` (React-Flow components) + `./model`/`./config`; `@likec4/core` exports `./compute-view`/`./geometry`, which produce a *computed layout model*, not an SVG string. Its own CLI SVG/PNG export runs **playwright** (headless-browser screenshot) — there is **no pure `dsl → SVG` function**. So per this row's rule it is "irreducibly a React component" → it moves to **Phase C as an editor-mount candidate**, not a registry row. Deferred (Excalidraw is the Phase C wedge that ships first). |

**Acceptance:** each new spec passes the Phase A criteria list (create · render
· save/reopen · fence · export · lazy chunk); ECharts additionally verified to
emit SVG (not canvas) so export stays uniform.

---

## 4. Phase C — interactive editors (kind-per-format, like `canvas`)

These are stateful surfaces, not `src → SVG` functions — they follow the
`diagram`/`canvas`/`table` precedent: one new `DocKind` each, mounted from the
`AuthorSurface` switch, lazy-loaded like `TableEditor`.

1. **Excalidraw** — `DocKind` `'excalidraw'`, body = `.excalidraw` JSON
   (ecosystem-standard, agent-authorable), `@excalidraw/excalidraw` mounted
   lazily with **self-hosted fonts** (offline-first; no CDN fetch). Export
   SVG/PNG via its `exportToSvg`/`exportToBlob` utils, matching the Phase A
   export affordance. Complements — does not replace — the native `canvas`
   kind (Zettelkasten cards vs. freeform sketch).
   **SHIPPED (2026-07-22).** Implementation notes vs the plan:
   - Fonts are copied from the package's `dist/prod/fonts` (14 MB, gitignored)
     into `public/excalidraw-assets/fonts` at build time
     (`scripts/sync-excalidraw-assets.mjs`, wired into `npm run build`); the
     runtime `window.EXCALIDRAW_ASSET_PATH = '/excalidraw-assets/'` points the
     loader at them so it never hits the esm.sh CDN fallback. Full airplane-mode
     verification is a device-test item (fonts degrade gracefully to system fonts
     if absent — not a crash).
   - The `<Excalidraw>` component is uncontrolled after mount, so it follows the
     `key={doc.id}` remount pattern (like `DiagramEditor`): read `initialData`
     once, stream changes out via a debounced `onChange` → `serializeAsJSON`,
     skipping emits whose `getSceneVersion` is unchanged (mount + font-reflow
     re-emits must not dirty the doc). No controlled reconcile loop.
   - `.excalidraw` ⇄ file round-trip via `extForDoc`/`kindForFile`; a `.json`
     scene is sniffed by its `type: "excalidraw"` discriminator. New-doc button
     "Sketch" (`author.newExcalidraw`, en + zh) + `sketch` icon.
   - Lazy chunk verified out of the entry/App chunks (`vite build` report); E2E
     smoke pins the lazy-mount + offline-asset-path config under `app://`.
   - The AgentCompanion is read/assist-only for this kind (structured JSON body,
     no safe text insert — same as canvas/table).
2. **bpmn-js** — `DocKind` `'bpmn'`, body = `.bpmn` XML. **Gated on the §5
   license decision in the landscape doc** (the "Powered by bpmn.io" watermark
   clause is product-visible): the director accepts the watermark or we seek
   removal *before* this lands. If undecided by the time C starts, ship
   Excalidraw alone and hold bpmn-js.

**Acceptance:** both kinds create/edit/save/reopen through the same
`extForDoc`/`kindForFile` bridge; Excalidraw works fully offline (airplane-mode
smoke test — fonts included); export produces valid SVG/PNG; neither dependency
appears in the entry chunk.

---

## 5. Sequencing & risk

- **A → B → C strictly**, each an independently shippable wedge with its own
  release, matching the repo's install-feedback rhythm. B is cheap by
  construction *iff* A's registry is honest — resist special-casing any A
  renderer in the editor or Markdown paths.
- **Riskiest item:** Mermaid's bundle behaviour under Vite code-splitting
  (historically awkward dynamic-import layout). Mitigation: it is the first
  spike of Phase A; if the chunk graph is unacceptable, fall back to the
  draw.io-style install-on-demand path without changing the registry contract.
- **LikeC4 shape uncertainty** is contained by the Phase B spike (§3).
- **No hub/schema work anywhere in A–C:** figure docs are device-local/
  file-backed like all Author docs; promoting them to hub Documents rides the
  existing later-round plan, unchanged.

## 6. Open questions (not blockers)

1. **Milkdown fences.** Render figure fences inside the WYSIWYG editor too
   (Milkdown has a plugin seam), or is the split-preview enough? Decide after
   Phase A ships and usage is observable.
2. **PDF/PGF export.** SVG+PNG ship in A; PDF export (papers) pushes toward the
   Phase E artifact pipeline — revisit there.
3. **Read-surface figure files.** Should opening a `.mmd`/`.dot` from the Read
   file tree render (not edit) via the same registry? Cheap once A6 exists.

## Related

- [figure-and-diagram-tooling-landscape.md](../discussions/figure-and-diagram-tooling-landscape.md)
  — the survey, license gates (§5), and Phases D–E this plan defers.
- [desktop-workbench-jobs.md](desktop-workbench-jobs.md) — J2 Author, the
  surface this lands in; its OQ 1 (lazy bundles) is honoured by §1.6.
- [author-agent-assist-and-diagrams.md](../discussions/author-agent-assist-and-diagrams.md)
  — AgentCompanion + draw.io install-on-demand precedents.
