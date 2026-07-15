# Product Landscape Report: Features Worth Borrowing for TermiPod Desktop

> **Type:** discussion
> **Status:** Open — for director review
> **Audience:** contributors deciding what to build / embed / integrate next
> **Last verified vs code:** v0.3.43
> **Freshness:** snapshot

**TL;DR.** An agent-authored survey of ~60 research/agent products,
organized by TermiPod's job areas (J1–J7), with every recommendation
classified per the project's own **BUILD · EMBED · INTEGRATE ·
BORROW · INTEROP** rule — starting from a top-16 ranked shortlist. A
point-in-time snapshot (2026-07 web research); copied in for review,
not yet acted on. Companion to
[desktop-design-review.md](desktop-design-review.md).

*Researched and verified July 2026 via web research across ~60 products (official docs, changelogs, repos, pricing pages). Companion to [desktop-design-review.md](desktop-design-review.md). Organized by TermiPod's job areas (J1–J7); every recommendation is classified per the project's own **BUILD · EMBED · INTEGRATE · BORROW(pattern) · INTEROP(format)** rule. Licensing is stated wherever embedding is on the table.*

---

## 0. Executive summary — the top 16 moves

Ranked by (value to the "one app for research" goal) × (feasibility given the existing codebase):

| # | Move | From | Kind | Job |
|---|------|------|------|-----|
| 1 | **Highlight → card with source deep-link** (annotation output = canvas-native card, bidirectionally linked to the PDF region) | Heptabase | BORROW | J1→J4 |
| 2 | **W3C multi-selector annotation anchoring** (quote+position+page selectors, Hypothesis-style fuzzy re-anchoring, orphans never deleted) | Hypothesis / W3C | BORROW + vendor `approx-string-match` (MIT) | J1 |
| 3 | **Coupled runs-table ↔ panel wall + baseline-run deltas + "diff only" run comparer** | W&B / ClearML | BORROW | J5 |
| 4 | **Ingest standard run formats instead of inventing one**: tfevents (Isaac Lab/SB3 default), a wandb-shim logging client, Optuna SQLite | TensorBoard/Trackio/Optuna | INTEROP | J5 |
| 5 | **Embed Rerun web viewer for robot episodes + video compare-grid with shared scrubber + STEP-style success-rate statistics** | Rerun (MIT/Apache) / W&B media panels / TRI STEP | EMBED + BORROW | J5 |
| 6 | **JSON Canvas 1.0 as the canvas file format** (with a namespaced extension for typed edges) | Obsidian ecosystem (MIT spec) | INTEROP | J4 |
| 7 | **Schema-on-tag decision capture**: `#decision`/`#finding` grafts typed fields onto any card; views are small YAML files ("rows are files, columns are frontmatter") | Tana supertags / Obsidian Bases | BORROW | J6 |
| 8 | **Stable block IDs + transclusion** (`^block-id`, `![[ref#^id]]`): agents cite/quote/embed exact paragraphs; ADR evidence stays live | Notion block model / Obsidian / Logseq | BORROW | J2/J6 |
| 9 | **Assemble an Elicit-class literature stack from open parts**: PaperQA2 (Apache-2.0) for chat-with-library, ai2-scholar-qa (Apache-2.0) for sectioned reports, SPECTER2 embeddings, S2/Asta free APIs | FutureHouse / Ai2 | EMBED + INTEGRATE | Discovery |
| 10 | **Expose TermiPod itself as a local MCP server** (library, canvas, decisions, runs) — the 2025–26 integration standard every peer adopted | Notion/Anytype/Heptabase/Elicit/Consensus/Ai2 | BUILD | all |
| 11 | **scite-style citation stances + retraction checking** in the library and a pre-submission reference check | scite / EndNote | INTEGRATE | J1/J2 |
| 12 | **Agent-interchange paste format** (`%%termipod%%` blocks: nodes, fields, tags, edges) so fleet output materializes as structured cards, not raw text | Tana Paste | BORROW | J2/J4/J6 |
| 13 | **Chat-with-library on the settled local recipe**: FTS5+sqlite-vec+RRF hybrid index with page/bbox chunk anchors → NotebookLM-grade citation-jump answers, OpenAI-compatible base-URL provider abstraction (Ollama/LM Studio/Jan presets) | NotebookLM UX / PapersGPT / Simon Willison recipe | BUILD + EMBED | J1/Discovery |
| 14 | **Emit OTel GenAI spans from the hub; self-host Langfuse (MIT) as the analytics backend**; build only the fleet honeycomb + session-replay waterfall + time-travel re-run that owning the engines uniquely enables | OTel semconv / Langfuse / AgentOps / Datadog | BUILD + INTEGRATE | J7/J3 |
| 15 | **Native Typst compile in the Tauri core** (Apache-2.0 crates, `World` trait — not the 28 MB WASM) as the PDF export engine, with a CSL-JSON→BibLaTeX shim for hayagriva | Typst 0.15 | EMBED | J2 |
| 16 | **PI-grade approval grammar**: confidence-adaptive plan gates + comment-on-plan, machine critics before human review, logically-grouped narrated diffs with risk coding and plan-step↔hunk linkage, "waiting on you?" fleet inbox with per-run cost | Devin / Jules / Ultraplan / CodeRabbit / Claude Science | BORROW + BUILD | J7/J3 |

The meta-finding across all clusters: **2025–26 converged on agentic tiers + MCP as the integration standard + "trust artifacts" (provenance, citations-that-jump-to-source, audit logs) as the differentiator.** TermiPod's architecture (local-first, hub-coordinated agents, governed actions) is well-positioned; the wins come from adopting the formats and interaction grammars below rather than inventing parallel ones. For the inverted lens — *why* each product category exists, which innovation won it, and what pain remains unsolved — see the synthesis in §14.

---

## 1. Reading & annotation (J1)

### 1.1 The pattern to steal outright: Heptabase's PDF workflow

Heptabase (closed-source, $11.99–71.99/mo) is the app PhD researchers consistently love for literature work, and the reason is one loop: drag a PDF onto a whiteboard → highlight text (7 colors) → **every highlight is automatically materialized as a first-class "highlight card"** carrying a durable deep link to the exact page/region → drag those cards onto the canvas next to your own note cards → typed connections → click any card to jump back to the passage. Since v1.74 (Sept 2025) it parses PDFs with OCR into blocks (images, equations, tables) that can be multi-selected and used as AI context; "sections" (named canvas regions) also serve as AI context scopes.

**For TermiPod:** the pieces already exist (PdfCanvas annotations, CanvasSurface reference cards, AgentCompanion context) but are not connected. Wire them: a highlight in PdfCanvas should mint a card pre-linked (`extracted-from` edge) to the reference, and canvas sections should be selectable as companion context. This is the single highest-leverage UX borrow in this report.

### 1.2 Annotation anchoring: adopt W3C selectors + Hypothesis re-anchoring

The design review flagged TermiPod's geometry-only anchors as fragile (arXiv v1→v2 breaks them). The industry-standard fix is fully specified and open:

- **W3C Web Annotation selectors** (w3.org/TR/annotation-model): store *multiple sibling selectors* per annotation — `FragmentSelector` (`page=N`, RFC 3778) refined by `TextQuoteSelector` (`exact` + ~32-char `prefix`/`suffix`), plus a document-global `TextPositionSelector` — alongside the existing Zotero-style page+rect geometry. The spec explicitly blesses redundant selectors; consumers pick the cheapest that validates.
- **Hypothesis's re-anchoring algorithm** (BSD-2 client, `src/annotator/anchoring/`): position-first with quote validation → cached quote:offset → hinted fuzzy quote search using **`approx-string-match`** (MIT, tiny, Myers bit-parallel) with scoring quote=50/prefix=20/suffix=20/proximity=2, `maxErrors=min(256, len/2)`. Failures become **orphans** (kept, retried later, never deleted). Two implementation details that matter for a pdf.js viewer: anchor against `getTextContent()` concatenated into a document-global string (not the DOM), and match with whitespace stripped + NFKD normalization — pdf.js text extraction shifts across versions and will otherwise silently break every offset. Use placeholder anchoring for unrendered pages in the virtualized viewer.
- Identify documents by `urn:x-pdf:<fingerprint>` in addition to path, so annotations follow the file.

### 1.3 Reader power features worth borrowing

- **sioyek** (GPL-3, C++, design reference only): **smart jump** — right-click "Figure 2.19"/"Eq. (4)"/"[27]" and it parses the text to find the target, showing an **overview popover**; **portals** — persistent source↔destination links rendered in a second pane that auto-follows as you scroll (the figure stays beside the paragraph discussing it); vim marks; fuzzy TOC. Smart-jump + portals are absent from every web PDF stack — a genuine differentiator that regex over the text layer can deliver.
- **Skim** (BSD, macOS): **snapshots** — floating pinned magnified mini-windows of a page region (keep the results table visible while reading the methods); notes-export templates; the AppleScript lesson → expose annotation CRUD via Tauri commands/URI scheme for automation.
- **ReadCube Papers ePDF**: inline citation popovers (hover an in-text citation → see the reference, one-click add to library), clickable figure browser, Altmetric badges. The citation-popover is buildable from the existing scrape/citation-graph data.
- **Readwise Reader** ($9.99/mo, the most open API in its class): the **keyboard-first reading grammar** (arrow-key paragraph focus; `H` highlight, `T` tag, `N` note — zero mouse), universal inbox (web/PDF/EPUB/newsletter/YouTube-transcript), and the **retention loop** (highlights feed spaced-repetition daily review; `.qa` action tags become flashcards). Borrow the keyboard grammar and consider a lightweight "resurface my highlights" habit loop; its v3 REST API is also a clean INTEGRATE source for web-clipping input.
- **pdf.js itself** (Apache-2.0): since v4–v5 it ships an `AnnotationEditorLayer` (highlight, ink, stamp, FreeText, signatures) and `saveDocument()` serializes annotations into the PDF — use it for an **"export annotated PDF"** path even while keeping the custom overlay for display. (MuPDF is the more powerful backend but AGPL; `pdfium-render` is the permissive Rust alternative.)

### 1.4 Reference-manager features (Paperpile, ReadCube, EndNote)

- **EndNote 2025**: the two features worth copying are **Retraction Watch integration** (flags retracted papers in-library *and at citation time*) and **Manuscript Matcher** (journal recommendation from title/abstract). Retraction checking is available free via Crossref/Retraction Watch data — add it to the library enrichment pass.
- **Paperpile**: the **continuous BibTeX auto-export** (folder/label → `.bib` in Drive/GitHub/URL, feeding Overleaf) is the right shape for TermiPod's citation bridge output; its **bring-your-own-AI** strategy (send PDFs+prompts to the user's own ChatGPT/Claude/NotebookLM) validates TermiPod's agent-companion approach; a **Citation Checker** (screen a .bib for hallucinated/erroneous entries) is a natural agent task.
- All four managers cluster at $5–14/mo subscriptions with 40–50% academic discounts; none except Readwise has a usable public API — reinforcing that TermiPod's open, agent-accessible library is a real differentiator.

---

## 2. Discovery & AI literature tools

### 2.1 The open-source stack that replicates the commercial tier

The commercial agentic tools (Elicit Research Agents, Undermind, SciSpace Deep Review, Consensus Deep Search) all converged on: multi-round agentic search → screen/classify → extract to tables → synthesize with citations. The remarkable fact of 2025–26: **the entire stack is now assemblable from Apache-2.0 parts + free APIs** —

- **PaperQA2** (FutureHouse, Apache-2.0, `pip install paper-qa`): agentic RAG over the user's own PDF library — retrieval → parallel LLM re-rank+contextual-summarize → cited answers; metadata via Crossref/S2; runs against local models (Ollama) for fully-private library chat. *The cleanest EMBED for a "chat with my library" feature — run it host-side as an agent tool.*
- **ai2-scholar-qa** (Apache-2.0, pip/Docker): sectioned literature reports over Semantic Scholar full-text (query preprocess → retrieve → rerank → quote extraction → plan/cluster → per-section synthesis with literature tables); pluggable retriever/reranker abstractions. *The reference architecture for a "generate literature review" agent task.*
- **OpenScholar** (Ai2/UW, Apache-2.0 code): self-feedback synthesis pipeline + open 8B model + SPECTER2 embeddings (open on HF) for similarity/recommendations.
- **Free APIs**: Semantic Scholar Graph API (214M papers; TLDR, embeddings, recommendations endpoints; 1 rps with free key) and the new **Asta Scientific Corpus Tool** — Ai2's **MCP endpoint** (`asta-tools.allen.ai/mcp/v1`) over the S2 corpus, launched Aug 2025. *Agents in the fleet should get this MCP server registered by default.*

### 2.2 Features worth borrowing from the commercial tools

- **Elicit**: **extraction tables** (papers × custom columns, every cell backed by a quote + page provenance) — this is the single most-loved AI-lit-review UX and maps directly onto a TermiPod agent task rendering into a Bases-style table view; PRISMA-style screening workflow for systematic rigor. (API: OAuth, Pro+; also ships an MCP server.)
- **Undermind**: **statistical coverage estimation** — it tracks classification decisions and reports "~90% of relevant literature found, search converged." Borrowable as a concept for agent search tasks: make the agent report coverage confidence, not just results. (No public API.)
- **scite** ($12–20/mo; partial free API — `/tallies/{doi}` uncapped): **Smart Citations** — 1.2B citation statements classified supporting/contrasting/mentioning with the surrounding snippet. INTEGRATE the free tallies into the reference inspector ("3 contrasting citations" is a stronger signal than a citation count), and consider their **Reference Check** pattern (flag retracted/contrasted references in a draft) as an agent task on Author documents.
- **Consensus**: the **Consensus Meter** (yes/no/possibly distribution across findings) — a good visualization pattern for agent-synthesized answers over the library.

### 2.3 Cross-source discovery (fixing TermiPod's single-source gap)

The design review already recommends search-time fan-out + strong-key merge. The research adds: prefer-richest-field merge (S2 for TLDR, OpenAlex for topics/concepts, Crossref for authoritative metadata, Unpaywall for OA PDF), and note that **MCP is now the standard interface** — Elicit, Consensus, and Ai2 all shipped MCP servers in 2025, so TermiPod's discovery layer should be consumable *by agents* through the same registry the hub already has.

*(NotebookLM's grounded-chat UX and the chat-with-library architecture: §8. Graph-based discovery — ResearchRabbit, Connected Papers, Litmaps, Inciteful — and Zotero live-sync and the digest pattern: §11.)*

---

## 3. Authoring (J2)

### 3.1 The block-addressing lesson (Notion, Obsidian, Logseq, Roam)

The deepest cross-product lesson for a markdown-file-centric app comes from Notion's block model (everything is a UUID-addressed block; transclusion = rendering the same block in many places; permissions/provenance flow through the *home* parent, not the render location) and its file-based translations (Obsidian `^block-id` + `![[Note#^id]]`; Logseq `((uuid))`). Concretely for TermiPod:

1. **Stable IDs written into the markdown** (`^id` anchors) so they survive git, rename, reorder — human-legible (Roam's opaque-UUID export mess is the anti-pattern).
2. **Transclusion as render-time inclusion**: `![[ref#^id]]` in a draft renders the referenced block live. An ADR's evidence field transcludes the exact paragraph of a paper note and stays current.
3. **A SQLite block index** (uuid → file/offset/type/outbound refs) maintained by a file watcher — files stay canonical, lookups and backlinks become O(1). (Obsidian's on-demand backlink inversion is a known pain point; Logseq's 3-year rewrite to SQLite-canonical validates the hybrid: **files as source of truth + DB as index** — exactly what TermiPod should do, avoiding Logseq's mistake of abandoning files.)
4. **Show blast radius**: Notion's "halo" — when a user *or agent* edits a transcluded block, surface everywhere it appears before the edit lands.

### 3.2 Editor components (2025–26 state)

- Keep **CodeMirror 6** as the primary editor (markdown-file fidelity is the point). Add: slash-command insertion menu (`/ref`, `/adr`, `/diagram`, `/agent` — trivial via autocomplete), wikilink completion, and `y-codemirror.next` if collaborative/agent-concurrent editing is wanted.
- **BlockNote** (MPL-2.0 core; XL packages GPL-3.0-or-commercial — see the §9.4 correction; React, on ProseMirror/Tiptap) remains the best embeddable Notion-style block editor if a WYSIWYG surface is ever wanted — but its officially-lossy markdown round-trip and missing math block make it unsuitable as the *primary* editor here; the block-ID architecture (§3.1) delivers most of the value without the editor swap.
- **BlockSuite** (MPL-2.0, from AFFiNE): the only open production-grade "same blocks in page mode and on infinite canvas" engine — see §4.
- **CRDT layer**: **Yjs** (MIT) in the webview + **yrs/y-octo** (MIT, Rust, binary-compatible) in the Tauri core is the right concurrency substrate — it also gives agents a principled way to edit drafts concurrently (CRDT updates instead of racing whole-file writes).

### 3.3 The agent-interchange format (Tana's best idea)

**Tana Paste**: plain text beginning `%%tana%%` encoding nodes, typed fields (`Field::value`), tags, dates — designed precisely for LLM output → structured graph. TermiPod should define a `%%termipod%%` paste/insert protocol so fleet agents emit structured cards/fields/edges rather than raw prose; the companion's "insert reply" then materializes typed objects. (Tana's cautionary side: its write-only, 1-req/s API frustrates users — TermiPod's full read/write local access is the counter-position.)

### 3.4 Capture

Fork or mine the **Obsidian Web Clipper** (MIT): the **defuddle** extraction library + template engine (OpenGraph/Schema.org/CSS-selector variables, per-site trigger rules, in-page highlighting) pointed at TermiPod's library via a local endpoint. Web capture with structured metadata is the #1 library feeder and is essentially free to reuse.

*(The Typst-centered export pipeline and the hayagriva CSL-JSON caveat: §9. Quarto/Overleaf/MyST and slides/diagrams/grammar tooling: §12. The design review's recommendation — CSL-JSON internal format + citeproc-js + `[@ref]` pandoc keys + .bib export — stands, with one amendment from §9.2: the Typst export path needs a CSL-JSON→BibLaTeX converter because hayagriva does not read CSL-JSON.)*

---

## 4. Canvas thinking (J4)

### 4.1 Adopt JSON Canvas as the file format (INTEROP)

**JSON Canvas 1.0** (jsoncanvas.org, MIT, from Obsidian): 4 node types (`text` markdown / `file`+`subpath` / `link` / `group` with label+background) + edges (`fromSide/toSide`, ends, color, `label`), 6 preset colors + hex. Adopted by Obsidian, Kinopio, Flowchart Fun; TS/React/Rust libraries exist (Rust lib usable in the Tauri core). TermiPod should persist canvases in it (or export losslessly), storing typed edges as `label` + a namespaced `"x-termipod": {edgeType}` extension so exports degrade gracefully. The `file`-node `subpath` (`#heading` / `#^block-id`) pattern is what makes cards *views over notes* rather than copies — wire it to the block IDs of §3.1.

### 4.2 Patterns from the canvas leaders

- **Heptabase**: **sections** (named colored regions; usable as agent context — "summarize section 'Threats to validity'"), **mindmap toggle** for a card cluster, same-card-on-many-whiteboards with propagated edits.
- **AFFiNE/BlockSuite** (MIT / MPL-2.0 — *the one ecosystem in this space that is legally embeddable*): the **Page↔Edgeless duality** — the same blocks render as a linear doc or exploded on an infinite canvas. Even a read-only "explode draft onto canvas" mode captures most of the value. BlockSuite's `EdgelessEditor` is embeddable-with-elbow-grease (web components; last standalone release July 2025, active development inside the AFFiNE monorepo — expect churn); the safer extraction is `@blocksuite/store` (Yjs doc model) under the existing canvas.
- **Logseq** (AGPL — ideas only): **live queries as canvas citizens** — a query ("all `#finding` cards referencing reference X since the last decision") rendered as a virtual, self-updating card. With SQLite underneath, TermiPod can do this with SQL instead of Datalog.
- **Anytype** (MIT protocol / source-available app): **relations as first-class typed objects** — `supersedes`, `evidenced-by`, `blocks` defined as schema entities with their own metadata, making the decision graph queryable. Also the **local REST API + headless CLI** shape (§7).

---

## 5. Run comparison & experiment tracking (J5 — the moat surface)

This is TermiPod's declared "headline BUILD," and the research yields a precise feature bar plus a data-strategy correction.

### 5.1 Data strategy: ingest, don't invent (INTEROP)

- **TensorBoard event files are the lingua franca** of the user's field: Isaac Lab's RL wrappers default to tfevents; SB3/RSL-RL/rl_games write them; even W&B's SB3 integration scrapes them. The hub must ingest tfevents (Go parsers exist) so existing training code appears on the wall with zero changes. (TB itself is in maintenance mode and slow beyond ~dozens of runs — its weakness is the wall's opportunity.)
- **A wandb-shim client is the adoption cheat code** (Trackio's move: `import trackio as wandb`): a Python package exposing `init/log/finish` that writes to the hub covers LeRobot and most robot-learning repos unchanged.
- **Runs-as-directories on hosts, hub as index** (Guild AI's zero-server lesson + the data-ownership law): metrics/videos stay in per-run directories on GPU hosts (rsync-able, survivable), the hub indexes; **MLflow's REST shapes** (`runs/search` filter DSL, `metrics/get-history` pagination, log-batch limits ≤1000 metrics/1MB) are a proven contract to mirror so export/import to the ecosystem stays trivial. Optionally accept `MLFLOW_TRACKING_URI` read-only ingestion (MLflow is Apache-2.0 and SQLite-default since 3.7 — the one tracker legally embeddable wholesale).
- **Server-side downsampling in the Go hub** (LTTB) — Neptune's scalability lesson; the difference between a toy and a wall when overlaying 20 × 1M-step runs.

### 5.2 The comparison-wall feature bar (BORROW — composite of best-in-class)

| Feature | Source of the pattern |
|---|---|
| One visible-runs/filter/group state driving **all** panels (eye-toggles, regex filter) | W&B workspace + TensorBoard run selector |
| **Baseline run**: pin one run; Δ-vs-baseline columns in the table; distinct curve styling | W&B v0.76+ (their biggest 2025–26 comparison investment) |
| **Run Comparer with "diff only"**: side-by-side config/hparams/summary, identical rows hidden, next-diff navigation | W&B + ClearML |
| **"What changed?" triad on run detail**: config diff + git/uncommitted-code diff + package diff | Comet / ClearML — *the single highest-value run-detail idea; the hub already knows git state and env of agent-launched runs* |
| Query → **group-by hparam → color/facet/line-style + seed aggregation (mean ± band)** | Aim (Apache-2.0 — its React chart components are legally minable) |
| Smoothing ghost-line (EMA/gaussian) + x-axis switching (step/wall/relative) | TensorBoard/W&B muscle memory |
| Scalar **extremes table** (last/min/max per run, best-per-row highlighted) | ClearML |
| **Parallel coordinates + fANOVA param importances** for sweeps | W&B sweeps / **Optuna Dashboard WASM** — MIT, runs fully client-side from a study SQLite file: bundle it and the deferred "optuna embed" is solved with no server and no Python |
| **Fork lineage**: `parent_run + fork_step` rendered as one continuous curve with a fork marker | Neptune (checkpoint-restart workflows) |
| **URL-addressable comparison state** (run set + metrics + smoothing deep-linkable) | ClearML — cheap in React, huge for sharing with collaborators *and agents* |
| **Reports**: prose + live panel grids bound to saved run sets | W&B Reports — maps onto the hub's existing digests |

### 5.3 Robotics-specific visualization (the field-specific edge)

- **Rerun** (MIT/Apache-2.0; v0.34, July 2026; "data layer for physical AI"): EMBED `@rerun-io/web-viewer-react` as the 3D/episode tile on run detail — `.rrd` files per eval episode; native LeRobot loader; Isaac Lab's Newton branch ships a Rerun backend; the **blueprint system** (layout-as-versioned-data, reused across recordings) is both usable directly and the right model for TermiPod's own wall-config; **Viewer MCP** (0.34) lets agents drive the viewer — watch this. Caveats: pin viewer version to SDK version; ~2 GiB WASM memory cap → keep `.rrd`s episode-sized.
- **Video compare-grid**: reproduce W&B's media panel Compare mode locally — a `<video>` grid with a **shared scrubber**, populated by globbing LeRobot (`outputs/eval/.../videos/`), robomimic (`videos/`), and Isaac Lab RecordVideo conventions. Known gap to exploit: gymnasium ≥1.0 RecordVideo videos currently fail to auto-upload to W&B — a filesystem-watching local tool just wins.
- **Statistics on success rates** (differentiator nobody pairs with videos): TRI/Princeton **STEP** (open code, non-commercial license — borrow the *methods*): binomial CIs, Bayesian beta-posterior violins, sequential testing, corrected pairwise comparisons. Robot eval has small n; raw success-rate curves mislead; the wall should show CIs by default.
- **Live 3D from hosts**: **Viser** (Apache-2.0, v1.0, works over SSH) / **mjviser** (Apache-2.0, MuJoCo viewer with rollout callbacks) as agent-launchable sidecars that TermiPod iframes. **MuJoCo WASM** (first-party, in-tree) enables client-side MJCF + qpos-trajectory replay with no video files at all. **Foxglove** is closed since 2.0 — use only as optional integration under its free Academic plan, or iframe the MPL-2.0 fork **Lichtblick** (BMW); standardize logs on **MCAP** either way.

---

## 6. Decision capture (J6 — the other moat surface)

Composite recommendation from Tana + Obsidian Bases + Anytype + Notion:

1. **Schema-on-tag** (Tana supertags; Obsidian 1.9 "Bases"; Logseq DB "NewTags"): a card becomes a decision by tagging `#decision`, which grafts typed fields (status, options-considered, consequence, supersedes, confidence, agents-involved) rendered as a form strip. Retroactive, low-ceremony — beats a dedicated ADR editor. Types registered per-name in one registry file (Obsidian's `types.json` pattern), stored as YAML frontmatter so files stay greppable and agent-legible.
2. **Views as small YAML files** (Obsidian Bases): "rows are files, columns are frontmatter"; global filters + per-view filters + named formulas + *editable* cells that write YAML back (editability was Bases' decisive win over Dataview). `file.hasLink(this.file)` as a filter primitive gives "all decisions linking to this note" panels for free. Copy the **view SPI** shape (`registerBasesView(type, {factory, options})` — five community kanban views appeared within months) if extensibility is ever exposed.
3. **Commands on tags** (Tana): a `#finding` tag exposes buttons — "have agent verify," "supersede" (sets status, creates successor stub, updates canvas edges). Notion's automation triad (trigger → action, buttons, Dec-2025 webhook actions) confirms even closed platforms reach out to arbitrary services — TermiPod automations should invoke agents.
4. **Provenance is the differentiator**: an ADR records *which agent run* proposed it (trigger, reasoning, diffs — Notion 3.1 logs every agent run's trigger/actions/reasoning and makes all edits reversible); evidence fields **transclude** source blocks (stay live, §3.1); findings deep-link to the exact block/figure supporting them (Notion AI Meeting Notes' "takeaway links to the exact transcript moment" pattern). No competitor links decisions to *runs* — TermiPod's hub already has the data.

---

## 7. Cross-cutting: the local API/MCP surface, sync, and licensing

### 7.1 Expose TermiPod as a platform (BUILD)

Every serious peer shipped a programmatic surface in 2025–26: Notion (hosted MCP + REST + webhooks), Anytype (local REST API + headless CLI + MCP), Heptabase (MCP + CLI), Obsidian (CLI, Feb 2026), Elicit/Consensus/Ai2 (MCP). TermiPod already has the hub's MCP for its own agents; the missing piece is a **local MCP/REST surface over the workbench itself** — library, annotations, canvas graph, decisions, runs — so *any* agent (a Claude Code session on a GPU host, the user's own scripts) can operate on the workbench with purpose-built, token-efficient tools (Notion measured 91% token savings vs raw CRUD — purpose-built tools beat generic ones). This also implements the review's "SurfaceContext protocol" from the outside in. Copy Obsidian's 2026 safety pattern: automation actions surface as confirmable intents (the approvals dock already exists for exactly this).

### 7.2 Sync and concurrency

- **Yjs (MIT) + yrs/y-octo (MIT, Rust, binary-compatible)**: CRDT docs in the webview persisted by the Tauri core into SQLite with deterministic markdown export — the architecture AFFiNE proves and Logseq's painful rewrite validates. Agents apply CRDT updates instead of racing file writes.
- **any-sync** (MIT, Go) is the reference if multi-user E2EE spaces are ever needed; over-engineered for now.
- Anti-models: Heptabase (vendor-cloud, not E2E, not self-hostable) and Notion offline (per-page opt-in, 50-row caps) — TermiPod's local-first files are strictly stronger; say so in positioning.

### 7.3 Licensing quick reference

| Safe to embed/vendor | License |
|---|---|
| JSON Canvas spec/libs, Yjs, yrs/y-octo, approx-string-match, Obsidian Web Clipper (defuddle), AFFiNE CE, BlockNote core (MPL-2.0 — *not* the XL packages), Rerun, Viser/mjviser, Optuna Dashboard (incl. WASM), Trackio, MLflow, Aim, PaperQA2, ai2-scholar-qa, OpenScholar code, pdf.js, Annotorious, recogito text-annotator, DataScript | MIT / Apache-2.0 / BSD / MPL-2.0 / EPL |
| **Also safe (added by §§8–12)**: Typst compiler crates + typst.ts + hayagriva, sqlite-vec, LanceDB, fastembed-rs + nomic/bge/arctic embedding weights, Docling, GROBID, Ollama, llama.cpp, lmstudio-js/py SDKs, Jan, AnythingLLM, zotero-mcp (reference), Better BibTeX, Langfuse core (MIT — avoid `/ee`), OpenLLMetry, Helicone, OTel Go SDK, marp-core, touying/polylux, reveal.js, Mermaid, Excalidraw, @viz-js/viz, harper-core, Vale, mystmd (CLI), @xterm/xterm 6 + addons, portable-pty, creack/pty, **codex CLI** (Apache-2.0 — the one embeddable engine), Wave Terminal, VibeTunnel, Omnara, Claude Agent SDK (Python, MIT) | MIT / Apache-2.0 / BSD |
| **Traps** | Logseq & MuPDF (AGPL-3.0), ClearML server (SSPL), Anytype apps (source-available, non-commercial), W&B server (1-seat personal license only), Foxglove core (closed since 2.0 — use Lichtblick MPL-2.0), TRI STEP (non-commercial — borrow methods, not code), **BlockNote XL** (GPL-3.0 or $195/mo), **Marker** (GPL-3.0 code + OpenRAIL-M weights, <$2M-revenue cap), **Arize Phoenix** (ELv2 — self-host yes, redistribute no), **Zotero AI plugins** Aria/PapersGPT/zotero-gpt/Khoj (AGPL — study behavior only), cetz (LGPL — fine as compile-time markup), **tldraw** (non-OSS license keys, ~$6k/yr since SDK 4.0 — use Excalidraw), **D2 TALA layout** (proprietary + watermark; D2 itself MPL-2.0 is fine), **PlantUML** (GPL + JVM — integrate, never bundle), **Quarto distribution** (MIT CLI but bundles GPL-2 Pandoc + Posit trademark — PATH-detect, never bundle), Overleaf CE (AGPL), **Warp client** (AGPL since Apr 2026 open-sourcing), **claude-code CLI** (proprietary — drive via SDK, don't embed; no Pro/Max auth piggybacking, no "Claude Code" branding), claude-squad & Coder reconnecting-PTY (AGPL — pattern only) |
| **Closed — design references only** | Obsidian app, Heptabase, Tana, Roam, Notion, Elicit/Undermind/SciSpace/Consensus apps, ReadCube, EndNote, Paperpile, NotebookLM, LangSmith, AgentOps backend, Braintrust, typst.app web app, LM Studio app (free incl. commercial use, closed source) |

---

## 8. Chat with library & grounded answers (the NotebookLM-class surface)

The gap analysis first, because it's striking: **no product in 2026 combines local-first + your own curated library + citation-jump RAG + bring-your-own-local-LLM + MCP exposure in a modern desktop app.** PapersGPT is closest but is a plugin bolted onto Zotero's aging plugin platform with a proprietary core and $29–59/mo pricing for what is largely local compute; NotebookLM has the best UX but is cloud-only and API-less; Elicit/Undermind are discovery tools, not library tools. This surface is TermiPod's for the taking.

### 8.1 What NotebookLM teaches (the UX bar)

NotebookLM (free → Google AI Ultra $99.99–200/mo; ~1M-token context; hallucination rate measured at ~13% vs ~40% for raw Gemini/ChatGPT, 95% citation accuracy in a radiology eval) is the mainstream benchmark, and its lessons are precise:

1. **Citation-first answers are the product.** Every generated sentence carries a numbered chip; clicking opens the source and jumps to the highlighted passage. But its citations are *passage pointers, not bibliographic references* ("Source 3"), and copying an answer loses the links. TermiPod owns the PDF viewer, so it can beat this: resolve citations to real page coordinates **and** emit proper author-year/BibTeX cites.
2. **Scoped, user-visible context.** A notebook is an explicit source set with per-source checkboxes. Give TermiPod a collection-scope picker with per-paper toggles, not always-whole-library RAG.
3. **Suggested questions on ingest** (3–5 per source + a one-line summary) — the on-ramp that makes an empty chat box usable.
4. **Answers as durable artifacts**: "Save to note" + "convert note to source" makes synthesis compounding — TermiPod equivalent: save answers as library notes with live citation links, re-indexed for future chats.
5. **Refusal over invention**: it explicitly answers "the sources don't contain this." Enforce the same contract; it is why users trust it.
6. **Chat config is cheap and heavily used**: persona presets + custom instructions (Google expanded these to 10,000 chars) + a Shorter/Default/Longer toggle.

For a **weekly digest** feature, borrow the Audio/Report format menu shape (Deep Dive / **Brief** = what's-new / **Critique** = adversarial read of a new paper against the library / Debate) with a free-text customization prompt, and the **Discover Sources / Deep Research** pattern: a weekly agentic pass proposing new arXiv/web sources for one-click import.

**Integration reality:** consumer NotebookLM has *no public API* (the Enterprise API is Google-Cloud-org-gated alpha; unofficial SDKs are ToS-gray). The sanctioned DIY path is the **Gemini API File Search tool** — managed RAG with automatic passage-level citations incl. page numbers, ~$0.15/1M-token one-time indexing — viable as a hosted mode; the private mode needs the local stack below. NotebookLM's exploitable weaknesses map exactly onto TermiPod strengths: no reference-manager integration (the community built NZBridge and Chrome extensions just to bridge Zotero), no live file sync (uploads are snapshots), notebook-scoped silos, no citation-formatted export, cloud-only.

### 8.2 The feature bar set by PapersGPT (closest competitor)

PapersGPT-for-Zotero combines: a model buffet (GPT-5.x/Claude/Gemini/DeepSeek/Kimi + bundled local HF models + any Ollama model + custom OpenAI-compatible endpoint, marketed as "zero-byte data leakage"), local structure-aware indexing (proprietary C++ core, claims 1,000+ PDFs near-instant) with **verifiable citations back into documents**, and a **built-in MCP server** exposing the library to Claude Desktop/Code, Cursor, etc. All three in one product is the bar. Its history lesson: A.R.I.A., the first Zotero AI plugin, died from hardcoding GPT-4-only; zotero-gpt (7.3k stars, alive) survived on one feature — a **configurable API base URL**. Abstract the model endpoint from day one.

### 8.3 The settled local-RAG recipe (EMBED)

- **Index**: one SQLite database with **FTS5 (BM25) + sqlite-vec (MIT/Apache dual)**, fused with **Reciprocal Rank Fusion** (`score = Σ 1/(60+rank)` — rank-based, no score calibration). BM25 catches exact terms/author names/gene IDs (critical in science); vectors catch paraphrase. Production examples run 18–49 ms warm queries on 2k–19k docs, CPU-only. sqlite-vec is brute-force KNN — fine to ~100k–1M chunks; LanceDB (Apache-2.0, Rust-native, what AnythingLLM embeds) is the graduate-to option.
- **Chunks carry anchors**: `item_id, attachment_id, page, char/bbox range, section heading`. Answers cite chunk IDs → the UI opens the PDF at the page and highlights the rect. This is the NotebookLM-grade citation-jump no local tool does well, and it is trivial when you control the pdf.js viewer.
- **Embeddings**: bundle a small ONNX embedder via **fastembed-rs** (default **nomic-embed-text v1.5** — 768-dim, 8k context, ~5× faster than 1,024-dim peers on CPU; offer **bge-m3**, MIT, for multilingual). Record the model name per index; re-embed on change. Settings override to any `/v1/embeddings` or Ollama `/api/embed` endpoint.
- **Chat providers**: the whole integration is *OpenAI-compatible base URL + API key + model list from `GET /v1/models`*, with presets: Ollama `:11434/v1`, LM Studio `:1234/v1` (closed but free for commercial use since July 2025; MIT SDKs), Jan `:1337/v1` (Apache-2.0, 43.6k stars, **built on Tauri — the best architectural comp for TermiPod**), llama.cpp/llamafile `:8080/v1`, plus cloud keys.
- **PDF parsing**: fast text-layer extraction (pdfium/MuPDF-via-Rust) with coordinates preserved; GROBID (Apache-2.0, ~120 pages/s, but a heavy Java service) for scholarly structure; **Docling** (IBM, MIT) as an opt-in high-fidelity re-parse. **Avoid Marker**: code GPL-3.0 and model weights under a modified OpenRAIL-M (free only under $2M revenue — a commercial trap).

### 8.4 MCP is the moat multiplier (BUILD)

ChatGPT Deep Research gained MCP-client connectivity (Feb 2026) and Gemini Deep Research is API-exposed with MCP support — **frontier deep-research agents now consume MCP sources.** If TermiPod exposes its library over MCP (`search_hybrid`, `get_item`, `get_fulltext_page`, `get_annotations`, `add_by_doi`, `cite` — model the tool surface on MIT-licensed `54yyyu/zotero-mcp`, 4.3k stars; stdio for Claude Desktop/Code + streamable HTTP/OAuth 2.1 for remote), then Claude/ChatGPT/Gemini deep-research runs ground themselves in the user's own library. TermiPod doesn't have to build a deep-research agent to benefit from all of them. Note the licensing hygiene: the Zotero AI plugins (Aria, PapersGPT, zotero-gpt, Khoj) are AGPL — study behavior only, copy no code.

---

## 9. Publishing pipeline: Typst as the export engine (J2)

### 9.1 Typst state (0.15.0, June 2026) — ready for the PDF path

Typst's paged/PDF pipeline is now production-grade and *ahead of most LaTeX toolchains* on standards: 0.14 (Oct 2025) shipped tagged PDF by default, PDF/UA-1, and all four PDF/A parts; 0.15 (June 2026) added variable fonts, MathML in HTML export, bundle export (one project → PDF + HTML + PNG/SVG), and multiple bibliographies. HTML export remains experimental/feature-gated. The Universe ecosystem is at ~1,436 packages incl. the academic essentials (cetz drawing, touying Beamer-class slides, lilaq plotting, physica, IEEE/NeurIPS/ACM lookalike templates).

**How to embed it (the key finding):** Typst *is* a Rust library — a Tauri app should **compile natively on the Rust side and skip WASM entirely**. All crates (`typst`, `typst-kit`, `typst-pdf`, `typst-svg`, `typst-html`) are Apache-2.0 at 0.15.0; you implement the `World` trait (or use the `typst-as-lib` wrapper) and invoke over Tauri IPC. For live preview, either stream SVG pages from the Rust side or ship only typst.ts's 1 MB renderer WASM consuming vector artifacts with incremental rendering — do **not** ship the 28 MB web-compiler WASM when you already have native Rust. Caveat: the crate API breaks every minor release (0.13→0.14→0.15 each broke embedders); isolate it behind one module.

### 9.2 The bibliography catch: hayagriva does not read CSL-JSON

Typst's citation engine **hayagriva** (MIT/Apache-2.0) consumes real `.csl` styles (all 2,600+ in the CSL repo) but its inputs are its own YAML format and BibTeX/BibLaTeX — **CSL-JSON input is unsupported** (long-open issues #32/#132). So the report's recommended CSL-JSON-internal citation bridge needs one extra converter: CSL-JSON → BibLaTeX (Zotero's Better-BibTeX-shaped export works today) or → hayagriva YAML on the Typst export path. hayagriva is also not a certified citeproc (ibid/position-test deviations, no CSL-M) — mainstream author-date/numeric styles are fine; legal/humanities note styles are the weak spot; keep citeproc-js for the markdown/HTML path.

### 9.3 Venue reality (advice for the PI persona)

arXiv still has **no native Typst support** (PDF-only submission works; source support tentatively tied to "Submission 2.0" in 2026 with no timeline; an arXiv engineer flagged package-churn and perpetual-recompilation blockers). Only small journals (IJIMAI, JUTI) accept `.typ` source; IEEE/ACM/Elsevier/Springer do not. But PDF-only venues — **OpenReview conferences (NeurIPS/ICLR/ICML), bioRxiv, most grant systems — work fine today** with Universe templates. Pandoc ≥3.2 has native Typst writer *and* reader (pandoc→typst PDF benchmarked ~27× faster than xelatex), and Quarto uses Typst as a first-class PDF engine. **Strategy: draft in TermiPod markdown → default export via native Typst compile; keep the pandoc escape hatch (typst↔latex) for LaTeX-source-mandating venues.**

### 9.4 BlockNote correction (updates §3.2)

Deeper verification revises the §3.2 note: BlockNote (v0.51.4, June 2026) core is **MPL-2.0, not MIT** (fine to embed unmodified), but the XL packages — **AI, PDF/DOCX/ODT export, multi-column** — are **GPL-3.0-or-commercial at $195/mo per application**. Markdown interop is *officially lossy by design* (`blocksToMarkdownLossy()` targets a CommonMark+GFM subset; the v0.51.0 parser rewrite improved round-tripping but the contract stands), there is **no built-in math/KaTeX block** (five community PRs closed unmerged), and it is pre-1.0 with regular breaking changes. Adoption is real (~434k weekly downloads; the Franco-German-Dutch government "Docs" suite; Twenty CRM). **Verdict: this strengthens the report's CodeMirror-first recommendation** — for a markdown-file-canonical app with heavy math, BlockNote's lossy markdown and missing KaTeX make it a poor fit as the primary editor; reconsider only for a WYSIWYG-lite surface, avoiding XL packages.

---

## 10. Fleet observability (J7/J3 — the control-plane half)

TermiPod's position here is unique and worth stating first: **no observability platform combines execution control (start/stop/steer agents on your machines) with observation — they all watch apps instrumented elsewhere.** TermiPod is the cockpit *and* the flight recorder. The strategy that follows: emit the standard, integrate the backends, and build only the fleet UX that owning the engines uniquely enables.

### 10.1 Emit the standard: OTel GenAI semantic conventions (BUILD, small)

OpenTelemetry's GenAI semantic conventions define agent-lifecycle spans (`create_agent`/`invoke_agent`), `execute_tool` spans, inference spans, `gen_ai.usage.input_tokens`/`output_tokens` attributes, token-usage metrics, and even MCP conventions. Status: still "Development" stability as of semconv 1.40/1.41 (names can change), **but adoption is ahead of stability** — Langfuse, Phoenix, Grafana, and Datadog all ingest it today. The Go hub should emit OTLP spans following these conventions (one span per agent run / tool call / model call), keep the attribute mapping in one Go module so a spec rename is a one-file change, and thereby make every backend below a zero-effort optional export target instead of a build.

### 10.2 Integrate: Langfuse as the anchor reference

**Langfuse** — MIT core (only `/ee` dirs are commercial: SCIM, extended audit, retention policies), acquired by ClickHouse Jan 2026 with explicit continued-OSS commitment, self-hosted via `docker compose` (Postgres + ClickHouse + Redis + S3) — is the closest feature match: sessions ("simple session replay of the entire interaction" — maps 1:1 to a TermiPod run), an **agent graph with an aggregated-topology vs expanded-as-it-ran DAG toggle** (the best loop/retry-debugging pattern found), per-observation cost from a model-price table rolled up to trace/session/user/tag, and a **Metrics API v2** exposing the same aggregates over REST — meaning TermiPod can self-host Langfuse for storage/analytics and still render its *own* native fleet dashboard from Langfuse's API. Offer "deploy Langfuse alongside the hub" as a one-click option. Secondary lightweight option: Arize Phoenix (single container — but **ELv2 source-available: self-host fine, never embed/redistribute its code**).

### 10.3 Borrow: the fleet-UX pattern table

| Pattern | Source | Concrete spec for TermiPod |
|---|---|---|
| **Session replay waterfall** | AgentOps, Langfuse | Per run: horizontal timeline of steps (LLM call / tool call / file edit / MCP call), scrubber, click any step for full input/output; failed step red with error payload. *The* failed-run debug surface. |
| **Time-travel re-run** | AgentOps | Rewind to step N and re-launch from checkpoint. AgentOps can only replay; TermiPod controls the engines and can do it for real. |
| **Trace tree with cost rollups** | LangSmith | Collapsible tree; every node shows tokens + $ + latency; parents aggregate, so "which phase burned the budget" is one glance. |
| **Session → turn → step vocabulary** | W&B Weave 2026 | Use these words in the UI instead of span/trace — right for a non-coding PI. |
| **Fleet honeycomb / host map** | Datadog hostmap | One tile per GPU machine colored by health/GPU-util; nested tiles per agent colored by state (running/waiting/failed/idle); click-through to session replay. The single best "PI glances at screen" pattern. |
| **RED + USE dashboard split** | Grafana canon | Top row (agents): runs/hr, failure %, p50/p95 duration per engine. Bottom row (machines): GPU util, queue depth, host errors. |
| **Failure-mode clustering digest** | LangSmith Insights Agent | Nightly job clustering failed runs ("12 failures: kimi rate-limit") — the ideal PI-facing summary. |
| **Natural-language run querying** | Braintrust Loop, LangSmith Polly | "Why did last night's runs fail?" answered by an LLM over run history — TermiPod already has the engines to power it. |
| **Budget/threshold alerts** | LangSmith, Grafana Alerting | Rule builder: metric + threshold + window → notification; include "project X > $50/day." |

Pricing context that validates the self-host strategy: SaaS observability meters aggressively (LangSmith $39/seat + $2.50–5/1k traces; W&B Weave $0.10/MB overage — a chatty agent fleet blows through 1.5 GB fast, though its **academic tier gives 25 GB/mo free**, relevant to this user; Datadog ~$160/mo per 100k LLM-spans with bill-shock complaints), while Langfuse/Phoenix/Helicone self-hosted are free and unlimited.

---

## 11. Graph discovery, Zotero live-sync & the personalized digest

### 11.1 The graph-discovery incumbents: closed, consolidated, and copyable

The market consolidated in May 2025 — **Litmaps acquired ResearchRabbit** — and monetized alerts (both gate them at ~$10/mo). None of the big three (ResearchRabbit, Connected Papers, Litmaps) is open source or has a public API; Inciteful pivoted to a medical product in 2025 and its academic tool looks maintenance-mode. So there is nothing to EMBED or INTEGRATE here — but everything to BORROW, because TermiPod already has the two inputs they sell access to: a citation-graph dataset and a canvas.

- **Connected Papers' signature is a *similarity* graph, not a citation graph**: from one seed it scores candidates by **co-citation + bibliographic coupling** (overlap of citing papers; overlap of reference lists — the latter works for brand-new papers with no citations yet), then force-lays-out the top ~40 with node color = recency, size = citation count, edge = similarity. Both metrics are trivial sparse-matrix ops (A·Aᵀ and Aᵀ·A over the adjacency matrix) — computable locally, offline, no embeddings. Its "Prior works"/"Derivative works" (common ancestors/descendants) panels fall out of the same matrices.
- **Litmaps' signature is the timeline map**: X = publication date, Y = citation count (log), edges = citations within the mapped set. Field genealogy reads left-to-right — the most PI-legible view for writing related-work sections, and a one-day layout toggle on an existing canvas.
- **Inciteful's signature is the transparent multi-seed ranking table**: sortable candidates with visible similarity/PageRank/citation scores, add-as-seed iteration. The open-source **PURE suggest** (U. Bamberg) and Citation Gecko are readable references for the scoring.
- **ResearchRabbit's signature is collections as living seed sets**: every collection is a standing query powering both hop-pivots (Similar/Earlier/Later work) and weekly email digests. TermiPod collections should work the same way.
- **Data substrate note**: OpenAlex (CC0 works metadata) moved to usage-based API pricing — keys mandatory since Feb 13, 2026, $1/day free credit — but the **full snapshot remains free** and is the right bulk path for the Go hub; Semantic Scholar's free API (1 rps with key) remains the second source.

### 11.2 Zotero live-sync: the read path is finally solid; writes still go through the cloud

Zotero went rapid-release (Zotero 8 Jan 2026, Zotero 9 Apr 2026, ~6–10-week cadence). The integration-relevant facts:

- **Local HTTP API (`localhost:23119/api/users/0/...`)** mirrors Web API v3 and is the primary read path: items, collections, saved-search execution, `?since=<version>` change polling, `/items/<key>/file` returning a 302 to the local `file://` PDF path, and — **stable since Zotero 8** — `/fulltext` endpoints. It is unauthenticated loopback and **read-only** (writes "in a future version," still true at Zotero 9).
- **Better BibTeX's JSON-RPC** (`:23119/better-bibtex/json-rpc`, MIT) supplies citation keys (`item.citationkey`), search, per-translator export, and `autoexport.add` — and its auto-export machinery (collection → CSL-JSON/.bib file on change, optional git push) doubles as a push-style change signal via file watching.
- **Writes**: the only sanctioned path is the **zotero.org Web API v3** (versioned CRUD with `If-Unmodified-Since-Version`); Zotero's own sync brings changes back down. Even ResearchRabbit — a funded integrator — chose cloud-API writes. **Never write `zotero.sqlite`** (exclusive lock; Zotero's caching layer breaks normal SQLite locking; corruption); copy-then-read is the emergency fallback (ZotLit's approach).
- Pitfalls: Zotero 8 continuously auto-renames attachment files (path caches break — always re-resolve via `/file`); port 23119 collisions; quarterly release churn.

**TermiPod recipe (INTEGRATE)**: read live via local-API polling + BBT JSON-RPC + watched auto-export; write via web API v3 with the user's key; PDFs via the `/file` redirect; full text via local `/fulltext`.

### 11.3 The personalized weekly digest (BUILD — the second moat surface)

The state of the art is **Scholar Inbox** (arXiv 2504.08385, ACL 2025 demo — effectively a public design document): embeddings over every new arXiv/bioRxiv/medRxiv submission, a per-user lightweight classifier trained on thumbs up/down over paper embeddings, active-learning cold start, tunable-threshold daily/weekly ranked digests. Combined with §8.1's NotebookLM digest-format lessons, the local-first recipe:

1. **Seed** = the user's library, per-collection (ResearchRabbit's living-seed-set pattern).
2. **Candidates** = Semantic Scholar **Recommendations API** (POST positives → relevance-ranked papers from the past ~60 days; free) ∪ arXiv RSS categories inferred from the library (re-implemented 2024, daily, free) ∪ OpenAlex queries within credit budget.
3. **Local re-rank** = embed candidates (SPECTER2 embeddings come free with the S2 API) against a library centroid, then a Scholar-Inbox-style classifier trained on digest thumbs up/down.
4. **Render** with PaperWeaver-style context lines ("relates to [your paper X]"), dedup against the library by DOI/arXiv ID, one-click add-to-collection (Zotero web-API write).

Avoid Google Scholar alerts programmatically (no API; scraping is paid + ToS-fragile). The positioning: RR/Litmaps gate alerts behind $10/mo cloud accounts; a local-first, library-seeded digest with private feedback data is unmatched.

---

## 12. The rest of the publishing pipeline (J2: computational docs, collaboration, slides, diagrams, grammar)

The recurring integration pattern across the healthiest ecosystems here (Quarto's VS Code extension, Harper-in-Obsidian, Vale's LSP) is **"permissive core embedded + heavyweight tool detected on PATH"** — TermiPod should standardize exactly that two-tier pattern. Two strategic tailwinds also validate the Typst bet of §9: Quarto 1.9 shipped Typst book projects and PDF/UA, and **Quarto 2 is being rewritten in Rust** (announced Apr 2026); mystmd has had first-class Typst export since 2023.

### 12.1 Quarto — optional INTEGRATE for computational documents

Quarto (v1.9 stable, Mar 2026) covers what a markdown→Typst pipeline misses: executable code cells (Jupyter/knitr engines with freeze/cache), uniform cross-references across formats, the **manuscripts project type** (article website + PDF/Word/**MECA-JATS submission bundle** with linked notebooks), and maintained journal templates. Its `format: typst` bundles the Typst CLI with a CSS→Typst translation layer — for *static* documents TermiPod's native compile is already ahead (no Pandoc round-trip). Licensing verdict: quarto-cli is MIT (v1.4+) but the distribution **bundles GPL-2 Pandoc** and is ~500 MB, and "Quarto" is a Posit trademark — **never bundle; detect on PATH** (Posit's own extension does exactly this; `quarto inspect` emits JSON explicitly for downstream tools, `quarto render --to typst|pdf` is the stable entry point). Watch Quarto 2: a Rust core could become a crate dependency later.

### 12.2 Overleaf — INTEROP only; there is no API

Overleaf still has **no public REST API** (the only sanctioned endpoint is create-only "Open in Overleaf" via `overleaf.com/docs?snip_uri=`). Its git bridge, GitHub sync, and Dropbox sync are all premium-gated (paid project owner required; single branch, no LFS, pushes can clobber track-changes); the free tier is down to 1 collaborator and ~10-second compiles; AI (Writefull/TeXGPT) was folded into all plans July 2026. Realistic collaboration patterns, in fidelity order: git-bridge round-trip → GitHub-as-hub (TermiPod user never touches Overleaf) → zip round-trip (free but re-upload creates a new project) → outbound "Open in Overleaf" link on a pandoc LaTeX export. Ship the LaTeX export + documented recipes; promise no programmatic sync. (Overleaf CE is AGPL and lacks git bridge/track changes/sandboxed compiles — not a component, at most a self-host suggestion for labs.)

### 12.3 MyST — borrow the dialect, don't embed the runtime

mystmd (MIT, a Project Jupyter subproject; **Jupyter Book 2 rebuilt on it became the default Nov 2025** — the momentum signal) offers the best structured-scientific-markdown semantics: roles/directives, `(label)=` targets with automatic numbering, scholarly frontmatter (ORCID/ROR/CRediT/funding), DOI-auto-resolving citations using **the same pandoc `[@key]` syntax TermiPod already chose**, and exports to 400+ journal LaTeX templates, JATS XML, and Typst. But the engine exists only in Node — no Rust parser, no CodeMirror 6 language package. **Verdict: BORROW the syntax subset** (fenced `{directive}` blocks, `{role}` spans, labels — they're shallow additions a CM6 extension can support, as jupyterlab-myst proves) and offer the mystmd CLI as an optional PATH-detected INTEROP for JATS/journal export. Curvenote itself pivoted B2B (journal SCMS, quote-based pricing) — no longer a consumer competitor.

### 12.4 Slides, diagrams, grammar — the EMBED shortlist

- **Slides**: **marp-core** (MIT) is the only true library-shaped option — a pure synchronous converter (`render(md)` → `{html, css}`) trivially embedded in React via Shadow DOM; PDF via detected local Chrome or a marp-cli sidecar. Pair with **touying** (MIT, a plain Typst package) for dependency-free PDF decks through the §9 native compiler — `#pause` subslides, speaker notes, pages renderable in-app as SVG. reveal.js 6 (MIT, official React wrapper since Mar 2026) only if in-app *presenting* becomes a feature; Slidev is Vue+Vite+Playwright-shaped — external tool, INTEROP at most.
- **Diagrams**: **Mermaid 11** (MIT, ~157 kB gzip entry, lazy chunks) as the default fenced-block renderer; **Excalidraw** (MIT, drop-in React component, open JSON format, official mermaid-to-excalidraw bridge) as the whiteboard. **Skip tldraw** — non-OSS license-key regime since SDK 3/4, watermark, ~$6k/yr commercial. **@viz-js/viz** (MIT wrapper, 537 kB) for optional ```dot``` support; **D2** (MPL-2.0) as an optional PATH-detected CLI (official WASM is 0.x and ~60 MB; TALA layout is proprietary+watermarked); **PlantUML** GPL+JVM — integrate against a user's server or skip.
- **Grammar**: **harper-core** (Apache-2.0, Rust, Automattic; ~10 ms/check vs LanguageTool's ~650 ms at <1/50th the memory) embedded **in-process in the Tauri Rust backend**, feeding `@codemirror/lint` diagnostics — the harper-obsidian-plugin is working CM6 prior art. Its gap: English-only, shallower rules. So: optional INTEGRATE of **LanguageTool** (user-provided self-host URL or Premium key — never bundle: JVM + up to 15 GB n-gram data) for depth/multilingual, and **Vale** (MIT, single Go binary) to encode an academic house style.

---

## 13. Agent mission control: HITL, diff review & terminal surfaces (J7/J3)

This completes the control-plane half started in §10. The category is crowding from three directions — Anthropic itself (Claude Science + a research-preview "agent view" listing live sessions with a **"waiting on you?"** flag), the IDE giants (Cursor 3.x parallel agents over SSH; the Codex desktop app; GitHub Agent HQ's multi-vendor Mission Control), and Devin Desktop (ex-Windsurf) adopting the Agent Client Protocol as a "cockpit for every agent." TermiPod's defensible intersection remains unoccupied: **multi-engine + multi-machine self-hosted GPU fleet + a non-coder PI's decision workflow.** The 2026 casualty list (Terragon defunct, Bloop shut down) also argues for staying local-first with no hosted-relay dependency.

### 13.1 Claude Science — the closest precedent, verified

Launched June 30, 2026 (desktop beta, macOS + Linux, drives remote compute over SSH/HPC — i.e., TermiPod's exact topology). What to borrow: **consent-gated compute** (drafts a plan, asks before reaching new resources, decisions reviewable/revocable pre-execution — this legitimizes TermiPod's approval grammar as the *expected* research UX); **provenance-carrying artifacts** (every figure/structure bundles code + environment + message history); session forking for compare-approaches; and a built-in **reviewer agent** flagging wrong citations, untraceable numbers, and figure/code mismatches. Its gaps are TermiPod's position: single-engine, life-sciences-slanted, no fleet dispatch, no heterogeneous engines, no decision ledger.

### 13.2 The engine adapters (INTEGRATE; one is EMBED-capable)

- **claude-code**: the Agent SDK / `claude -p --bare --output-format stream-json` (NDJSON with per-invocation `total_cost_usd`; `--bare` is the documented mode for scripted driving; a `capabilities` array replaces version-sniffing). The approval plumbing is first-class: `canUseTool` callbacks, `--permission-prompt-tool <mcp_tool>` (the hub's MCP tool becomes Claude Code's permission prompter), and **`PermissionRequest` hooks that support `defer`** — the process exits and the decision resumes later from the persisted session, enabling an asynchronous approval queue across the hub; hooks also come in an `http` flavor (POST to the hub — a centralized policy service is a supported pattern). Constraints: the CLI is proprietary (drive, don't embed), **API-key auth only — no piggybacking on users' Pro/Max logins in a distributed product**, no "Claude Code" branding.
- **codex CLI**: **Apache-2.0, ~95% Rust** — the one major engine TermiPod can legally embed or fork. Its two-axis model is the cleanest permission formalization in the market: sandbox mode (`read-only`/`workspace-write`/`danger-full-access`) × approval policy (`untrusted`/`on-request`/`never`), with escalate-and-explain when the sandbox blocks something; best-of-N is a product parameter (`--attempts 3`) — approval as *selection*.
- **Cross-engine formats**: **Agent Skills** became an open standard (agentskills.io, Dec 2025; 32 adopters incl. OpenAI, Gemini CLI, Cursor by Mar 2026) — write lab procedures once, run on all four engines. MCP is the hub↔engine control seam (tools can be marked `requiresUserInteraction` to force prompts even when allowlisted); watch the **Agent Client Protocol** (Zed + Devin Desktop) as adapters mature.

### 13.3 The approval grammar (BORROW — grounded in fatigue evidence)

The design-critical evidence: Anthropic measured that **users approve ~93% of permission prompts** (per-action prompting decays into rubber-stamping), and a June 2026 paper ("Oversight Has a Capacity," arXiv 2606.08919) shows safety vs. escalation rate is an **inverted U** — more prompts can make the system *less* safe once fatigue sets in. Design position for the PI: **approve plans and outcomes, not tool calls**; step-level safety comes from sandboxes + org-level deny rules + machine critics, with human escalation reserved for consequential or low-confidence actions.

The market converged on **two orthogonal dials**: approval *frequency* (allow/ask/deny grammars, with org-level deny rules that autonomy modes cannot override — Devin, Claude managed settings) × *blast radius* (network-off sandboxes, branch jail, escalate-on-exception — GitHub Copilot's containment triad: agent pushes only to its own `copilot/*` branch, CI won't run until a human clicks approve, default-on egress firewall, agent can't approve its own PR). Cursor's cautionary tale: its auto-run *denylist* was deprecated after trivial bypasses — denylists don't work; sandboxes do.

**Plan gates**: Devin's is the model for a PI — plan with code citations, a **30-second soft gate** (auto-proceeds unless "wait for my approval" is clicked) that becomes a **hard gate when the agent self-assesses low confidence**. Jules adds a **Planning Critic** — an adversarial agent reviewing auto-approved plans (−9.5% task-failure rate) plus a code-level critic reviewing the diff before the human sees it. Claude Code's **Ultraplan** (browser plan review with inline comments on sections, then "approve and start"; approval simultaneously sets the downstream permission mode) is the best existing model for PI-style plan review. TermiPod should compose all three: soft gate + confidence adaptation + machine critic + comment-on-plan.

### 13.4 Non-coder diff review (BUILD — the differentiating surface)

The feature bar, composed from best-in-class: **Devin Review** reorganizes diffs *logically instead of alphabetically*, narrates each hunk in plain language, color-codes findings red=bug/yellow=warning/gray=info, and shows a "lines left to review" counter — the most non-coder-friendly diff on the market. **CodeRabbit walkthroughs** add a plain-language grouped-file table, Mermaid sequence diagrams of affected flows, a review-effort score (1–5), and a linked-issue assessment (does the diff satisfy the stated objective?). GitHub contributes the right dirty-state primitive: **"viewed" checkboxes that auto-reset when a file changes after viewing**. The open market gap: **nobody ships true plan-step↔diff-hunk linkage** ("this hunk ← plan step 3") — Kiro's tasks-back-reference-requirements and Devin's timeline come closest. TermiPod's hub knows both the approved plan and the resulting diff; wiring them is the J3 differentiator.

**Notifications**: best-practice payload = the question verbatim + one-tap approve + deep link. Claude Code's `Notification` hook emits typed payloads (`permission_prompt`, `agent_needs_input`, `agent_completed`) the hub can relay to push; Omnara (Apache-2.0, YC S25 — mobile mission control built on the Agent SDK) is borrowable reference code. The fleet board's primary sort should be the **"waiting on you?" inbox** (Claude agent view, Omnara), with per-agent status dots, session folders/trees, and a **per-run cost meter** — after every vendor's 2025–26 move to token-metered billing (Cursor, Devin, Codex, Copilot), cost visibility is table stakes.

### 13.5 Terminal/PTY stack (EMBED + BORROW)

- **`@xterm/xterm` 6.0** (MIT; canvas renderer removed — WebGL2 + DOM only; DEC-mode-2026 synchronized output kills TUI flicker, exactly what claude-code emits) with addons: `webgl`, `fit`, `search`, `unicode11`, `serialize` (buffer→ANSI — the reattach primitive), `image`, and `progress` (OSC 9;4 — free agent-task progress bars). **Tauri/Linux caveat**: WebKitGTK WebGL is flaky on NVIDIA — ship WebGL with `onContextLoss` fallback to the DOM renderer, and consider DOM-by-default on Linux. No credible webview alternative exists (alacritty_terminal/libghostty are VT models needing native GPU renderers; **Warp open-sourced its client Apr 2026 but AGPL** — read, don't embed).
- **PTY plumbing**: `portable-pty` (Rust, MIT) locally; `creack/pty` (Go, MIT) on GPU hosts; emit **VS Code's OSC 633/133 shell-integration markers** from the remote wrapper to get block-segmented agent output (command/output units, exit-code decorations) client-side for free.
- **Reattach**: Coder's "reconnecting PTY" pattern — a raw-ANSI ring buffer (size 1–4 MiB for verbose agents) replayed to every attaching WebSocket; ~200 lines in the Go hub (Coder's code is AGPL — borrow the pattern, not the code). Upgrade path: `@xterm/headless` + serialize snapshots (VS Code's persistent-sessions pattern). **Wave Terminal** (Apache-2.0 — legally code-liftable) and VibeTunnel (MIT, a PTY→WebSocket→xterm.js relay built precisely for driving claude-code remotely) are the open blueprints.

---

## 14. Why these products won — pain points, innovations, core value, and what's still unmet

The preceding sections are organized around what TermiPod can borrow. This section inverts the lens: per cluster, *what pain drove the category into existence, which innovation won, what evidence shows users actually value it, and what remains unsolved* — including needs TermiPod would share with the incumbents rather than exploit.

### 14.1 The synthesis table

| Cluster (exemplars) | Pain point the category solved | The core innovation that won | Evidence of value | Still unmet |
|---|---|---|---|---|
| **Reading & annotation** (Heptabase, Hypothesis, Readwise, sioyek) | Reading produces insight that dies in the PDF — highlights are write-only; re-finding a passage weeks later is manual archaeology | Heptabase: highlight auto-materializes as a first-class card you *think with* on a canvas, deep-linked back to the source. Hypothesis: anchors that survive document edits (multi-selector + fuzzy re-anchor, orphans never deleted). Readwise: highlights resurface on a spaced-repetition schedule | Heptabase sustains $11.99–72/mo from PhD students — a price-sensitive segment paying for *one loop*; Hypothesis's anchoring model became the W3C standard | Cross-device/cross-version annotation portability is still fragile everywhere; nobody closes the loop from annotation → citation in the eventual manuscript |
| **Reference managers** (Zotero, Paperpile, EndNote, ReadCube) | Bibliographic chaos: metadata entry, citation formatting, PDF hoarding | Zotero: one-click capture + community translator ecosystem + free/open. Paperpile: continuous `.bib` auto-export into the writing tool. EndNote: retraction alerts at citation time | Zotero's dominance despite zero marketing budget; every competitor clusters at $5–14/mo and still can't displace it | All are *libraries*, not *reading environments* — the read/annotate/cite/discover loop spans 3–4 apps; none has a usable API except via plugins (the seam §8/§11 exploit) |
| **AI literature discovery** (Elicit, Undermind, scite, Consensus) | Keyword search misses paraphrase; systematic reviews take months; citation counts say *how much*, not *whether it held up* | Elicit: extraction tables where every cell carries a quote + page provenance. Undermind: agentic multi-round search that *reports its own coverage estimate*. scite: citations classified supporting/contrasting | Elicit reached a $12–169/mo tier ladder and shipped an API by demand; scite's tallies became a badge embedded across publisher sites | Provenance ends at the paper boundary — no tool tracks a claim *through* your own notes/drafts/decisions; coverage estimation (Undermind) remains proprietary and unreplicated in OSS |
| **Graph discovery** (Connected Papers, Litmaps, ResearchRabbit) | "What am I missing?" — keyword search can't find the adjacent literature you don't know the words for | Co-citation + bibliographic coupling rendered legibly (CP's similarity graph; Litmaps' time×citation axes); collections as living seed sets feeding alerts (RR) | Free tools with millions of users; consolidation (Litmaps bought RR, May 2025) and $10/mo alert paywalls show where the willingness-to-pay actually is: *monitoring*, not one-shot maps | All cloud-only, none touches your actual library deeply; graph views remain read-only endpoints — you can't think *on* them (annotate, connect, decide) |
| **Block-based authoring** (Notion, Obsidian, Logseq, Tana) | Documents are silos; the same fact lives in five places and drifts | The UUID-addressed block + transclusion (Notion); files-as-truth + links/blocks grafted on (Obsidian); schema-on-tag — structure added *after* capture, not before (Tana supertags, Obsidian Bases) | Notion's enterprise growth; Obsidian's community of thousands of plugins on a closed core; Bases' instant adoption validating "rows are files" | Sync vs. files-as-truth is still a pick-one (Logseq's 3-year rewrite is the cautionary tale); agent-written content has no provenance conventions in any of them |
| **Canvas thinking** (Heptabase, AFFiNE, Miro-class) | Linear documents can't hold a mental model — spatial arrangement *is* the thinking | Same block in page mode and on infinite canvas (AFFiNE's Page↔Edgeless duality); cards as live views over notes, not copies (subpath embeds) | JSON Canvas becoming a multi-app interchange standard within a year of release; Heptabase's retention among researchers | Canvases are still endpoints — no tool treats the canvas as a *queryable* substrate (live queries as canvas citizens exist only in AGPL Logseq); no agent co-thinks on a canvas yet |
| **Experiment tracking** (W&B, MLflow, TensorBoard, Neptune) | "Which run was that?" — results scattered across terminals, tfevents dirs, and memory; comparison by squinting at two browser tabs | One URL per run, forever (W&B); the runs-table ↔ panel-wall coupling where one filter state drives everything; log-once-render-anywhere via a 4-line SDK shim | W&B's $60/mo+ pricing and CoreWeave's $1.7B acquisition; MLflow as the default OSS answer; tfevents as the field's de-facto interchange format | Robot-learning evals (videos + small-n success rates + 3D episodes) are second-class everywhere — the §5 gap TermiPod targets; cross-org run sharing without cloud upload remains unsolved |
| **Decision capture** (ADRs, Tana commands, Notion automations) | Teams re-litigate decided questions because the *why* evaporated; docs record conclusions, not evidence | Schema-on-tag ADRs (a card *becomes* a decision retroactively); evidence that stays live via transclusion instead of copy-paste | ADR practice spreading from software into research groups; Tana's supertag model driving its entire product identity | The weakest cluster in the market — no product links decisions to the *runs/experiments* that motivated them (TermiPod's hub uniquely has this data); decision *review* (was it right in hindsight?) is nowhere |
| **Chat-with-library** (NotebookLM, PapersGPT, Elicit) | "I've read this somewhere in my 400 PDFs" — retrieval by memory doesn't scale; LLM answers without sources can't be trusted in research | Source-grounded generation with citation chips that jump to the passage — trust made clickable; refusal over invention ("the sources don't contain this") | NotebookLM's measured 13% vs 40% hallucination advantage and its viral growth; PapersGPT sustaining $29–59/mo *for local compute* | The §8 five-way combination (local-first + own library + citation-jump + local LLM + MCP) is claimed by no one; answers still don't compound into durable, versioned knowledge |
| **Publishing pipeline** (LaTeX/Overleaf, Typst, Quarto, MyST) | LaTeX's compile-debug loop and collaboration friction tax every manuscript; Word can't do science | Typst: incremental compilation with human-readable errors — the *feedback loop* is the product. Overleaf: collaboration without installation. Quarto: prose and computation in one source of truth | Overleaf's 20M users; Typst's trajectory from 0.1 to journal acceptance in three years; Jupyter Book rebasing onto MyST | Venue lock-in unresolved (arXiv/IEEE still demand LaTeX source — every Typst author keeps an escape hatch); reviewer-facing collaboration (response letters, tracked revisions across versions) is served by none |
| **Coding-agent mission control** (Claude Code, Devin, Cursor, Codex, Copilot) | One agent needs a babysitter; ten agents need an *air-traffic controller*; per-action approval collapses into rubber-stamping (93% approve rate) | Plan-level gating with confidence adaptation (Devin); two-dial permissions — frequency × blast radius (Codex); machine critics before human eyes (Jules, −9.5% failures); diff narration for non-readers-of-code (Devin Review) | Every major vendor shipped a fleet surface within 12 months (Agent HQ, agent view, Codex app, Devin Desktop); billing converged on metered tokens, making cost a first-class UX concern | Plan-step↔diff-hunk linkage shipped by nobody; the *non-coder* principal is served by nobody (all assume the reviewer reads code); multi-engine + self-hosted fleet + decision ledger is TermiPod's unoccupied intersection |
| **Agent observability** (Langfuse, LangSmith, AgentOps) | "It failed last night" — agent runs are black boxes; costs surprise at invoice time | The session-replay waterfall (scrub through an agent's steps); cost rollups per subtree; OTel GenAI as the emit-once-view-anywhere standard | Langfuse's 6M+ Docker pulls and ClickHouse acquisition; LangSmith building a custom database (SmithDB) just to make trace trees load fast | Observation without *control* — none can pause/steer/re-run what they watch (TermiPod's structural edge); failure-mode clustering is nascent everywhere |
| **Terminals** (Warp, Wave, xterm.js) | The terminal is where agents actually run, but it forgets everything: no structure, no reattach, no shared view | Blocks (command+output as addressable units — Warp/Wave); durable sessions that survive disconnects; shell-integration escape codes (OSC 633) giving structure without replacing the shell | Warp's $18–200/mo tiers on a *terminal*; xterm.js as the unchallenged substrate of every webview terminal | Remote-fleet terminal multiplexing with approval-aware rendering (which command is the agent *asking* to run?) exists nowhere outside bespoke internal tools |

### 14.2 The cross-cutting reading

Three pain-point patterns recur across all thirteen clusters:

1. **The trust gap is the deepest current.** The winning innovation in every AI-touching cluster is a *trust artifact*: citation chips that jump to the passage (NotebookLM), cells backed by quotes (Elicit), supporting/contrasting classifications (scite), provenance-carrying figures (Claude Science), narrated risk-coded diffs (Devin Review), session replays (Langfuse). Users don't pay for generation — generation is commoditized. They pay for *verifiability*. Every TermiPod surface should treat "click to see why" as a requirement, not a feature.

2. **Structure-after-capture beats structure-before-capture.** Tana's supertags, Obsidian's Bases, Heptabase's highlight-cards, and schema-on-tag decisions all won against tools demanding upfront schemas. Researchers won't fill forms while thinking; the tools that won let structure be grafted onto material retroactively. This validates TermiPod's markdown-files-plus-index architecture and warns against any workflow that begins with a template.

3. **The seams between categories are where the unmet needs live.** Each cluster is internally well-served; what no one solves is the *handoffs*: annotation → citation in the manuscript; discovery graph → your own library; experiment run → the decision it motivated; agent diff → the plan step it implements; chat answer → durable knowledge. Every incumbent stops at its category boundary because crossing it requires owning both sides. TermiPod's entire premise — one app, one hub, all surfaces — is precisely a bet on these seams, and the research confirms the seams are real, painful, and unoccupied.

And one shared unmet need TermiPod inherits rather than exploits: **compounding.** No tool in any cluster makes knowledge measurably *accumulate* — answers evaporate, decisions aren't revisited, digests aren't remembered. The mechanisms exist in fragments (NotebookLM's note-to-source conversion, Readwise's spaced repetition, decision supersession edges), but nobody has composed them into a system that demonstrably gets smarter about *your* research over months. If TermiPod solves the seams first, this is the harder second act.

---

*Sources: each cluster's findings were compiled from official product documentation, changelogs, GitHub repositories, and pricing pages current as of July 2026; representative URLs are retained in the underlying research notes. Fast-moving facts (prices, versions, licenses) should be re-verified before commitments.*
