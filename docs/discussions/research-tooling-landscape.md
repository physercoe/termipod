# Research tooling landscape

> **Type:** discussion
> **Status:** Open (2026-07-04) — feeds [ADR-050](../decisions/050-desktop-workbench-delivery-model.md)
> (desktop delivery model = local-first web-tech workbench). This doc is the
> per-capability register that ADR-050 D-3 refers to. Resolves as each capability
> is scheduled into a plan.
> **Audience:** contributors · maintainers · principal
> **Last verified vs code:** v1.0.820

**TL;DR.** A comprehensive survey (current mid-2026) of the tooling across the
**entire ML/algorithm research lifecycle** — discover · read · ideate · design ·
implement · data · run · track · compare · debug · visualize · write · review ·
reproduce · direct — and, for each capability, whether TermiPod's desktop
workbench should **BUILD** it (a fleet-native surface only our hub's data
enables), **EMBED** a mature web/JS component, **INTEGRATE** an external service
by API, or **INTEROP** (import/export only). The synthesized rule: **the web
ecosystem has already solved almost every *work* surface** — readers, editors,
canvases, plotting, trace UIs — so we EMBED those and INTEGRATE the discovery /
compute / tracking services; we BUILD only what is *fleet-native*, above all the
**multi-run comparison wall** (no embeddable open component exists and the data
already lives in our hub). The competitive read: **Claude Science** sets the
single-lab ceiling; TermiPod's differentiation is fleet · multi-engine ·
multi-host · governance · mobile↔desktop continuum.

---

## 1. Purpose and method

[ADR-050](../decisions/050-desktop-workbench-delivery-model.md) fixed the desktop
delivery model (local-first, hub-served, web-tech). This doc answers the next
question in full: **across the whole research lifecycle, what do we build vs.
reuse?** It is deliberately exhaustive — the director asked to "cover all
elements of the research lifecycle and compare all relevant tools/products."

Method: six parallel landscape surveys (literature/reading/ideation; code &
notebooks; experiment tracking; compute orchestration; observability/viz/writing;
autonomous-science & workbench rivals), each current to mid-2026, distilled here
into one register. Per-category sources are in the appendix.

**The four postures (ADR-050 D-3):**

- **BUILD** — a *fleet-native* surface the hub's own data uniquely enables;
  building elsewhere is the trap.
- **EMBED** — a mature web/JS component drops into the web workbench.
- **INTEGRATE** — a whole external product consumed by API / as an MCP tool.
- **INTEROP** — import/export only; borrow the UX idea, don't couple.

---

## 2. The research lifecycle — all stages, mapped

The full loop, each stage tagged with the desktop **job** it maps to (J1–J7 from
[`desktop-research-surface.md`](desktop-research-surface.md) §3), who owns it
today, and our default posture. Stages are simultaneous in practice, not linear.

| # | Lifecycle stage | Desktop job | Owned today by | Default posture |
|---|---|---|---|---|
| 1 | **Discover / survey** literature | J1 | Semantic Scholar, Elicit, Undermind, Perplexity | INTEGRATE (APIs) |
| 2 | **Read / understand** papers, code, math | J1, J3 | Semantic Reader, ar5iv, PDF.js | EMBED |
| 3 | **Ideate / connect** (notes, graphs) | J4 | Obsidian, Heptabase, tldraw | BUILD (on tldraw) |
| 4 | **Design / spec** experiments | J6 → dispatch | (bespoke) + `project.create` | BUILD (fleet-native) |
| 5 | **Implement** model/train/eval code | agents; J3 to read | Claude Code / Codex / Aider (agents) | INTEROP (MCP) |
| 6 | **Data** acquire/version/explore | J5-adjacent | DVC, HF Datasets, LakeFS | INTEROP / INTEGRATE |
| 7 | **Run / compute** training + eval | dispatch | Slurm, Ray, dstack, Modal, SkyPilot | INTEGRATE (adapters) |
| 8 | **Track / observe** metrics live | J5 | W&B, MLflow, Aim, TensorBoard | BUILD (on hub data) |
| 9 | **Compare** runs / sweeps | J5 | W&B comparison view, optuna-dashboard | **BUILD** (+ EMBED sweep panel) |
| 10 | **Debug** code + models (NaNs, spikes) | J3 | Monaco/CodeMirror, Perfetto | EMBED |
| 11 | **Visualize** figures / dashboards | J5, J2 | Plotly, Vega-Lite, ECharts, Observable | EMBED |
| 12 | **Trace / audit** agent sessions | J7 | Phoenix, Langfuse, Grafana/Tempo (OTLP) | INTEGRATE + BUILD |
| 13 | **Write / synthesize** reports, slides | J2 | Tiptap/BlockNote, Quarto, Typst, Slidev | EMBED + INTEGRATE |
| 14 | **Review / collaborate** | J6 | GitHub, Deepnote, Overleaf | INTEROP |
| 15 | **Reproduce / archive** provenance | J6 | marimo, DVC, artifact stores | EMBED + BUILD |
| 16 | **Direct the fleet** (mission-control) | J7 | *(TermiPod is the only one)* | BUILD (have it) |

The shape of the answer is already visible: **EMBED/INTEGRATE dominates the
*work* stages; BUILD concentrates on the *fleet-native* stages** (design→dispatch,
track/compare on our own data, audit, mission-control) — exactly the half no
competitor can copy cheaply.

---

## 3. The landscape, by capability

Distilled comparisons. "Emb?" = embeddable in a web app as a component (✓), an
integrate-by-API service (API), or iframe-only (iframe).

### 3.1 Literature — discover · read · reference · ideate (J1, J4)

| Tool | OSS/self-host | Emb? | Owns | Posture |
|---|---|---|---|---|
| **Semantic Scholar** (graph + API) | free API | API | discovery backbone (200M papers, TLDRs, citations) | **INTEGRATE** |
| **Semantic Reader / PaperCraft** (React SDK, MIT) | yes | ✓ | augmented PDF reading | **EMBED** |
| **Undermind** | no | API | exhaustive recursive recall | INTEGRATE |
| **Elicit** | no | API | structured extraction tables | INTEGRATE |
| **Perplexity Deep Research** | no | API | fast landscape reports | INTEGRATE |
| **scite.ai** | no | API | supporting/contradicting citations | INTEGRATE |
| **NotebookLM** | no | (thin) | grounded doc-set Q&A (pattern) | BUILD the pattern over our agents |
| **Zotero** (GPL, REST API v3) | yes | API | reference store / interop standard | **INTEGRATE** |
| **ar5iv / arXiv HTML** | ar5iv OSS | iframe/parse | machine-readable paper source | INTEROP |
| **Connected Papers / ResearchRabbit / Litmaps** | no | — | citation-graph discovery | INTEROP |
| **Obsidian / Logseq** | partial | — | PKM (markdown vault) | INTEROP (vault format) |
| **tldraw** (Apache-2, React SDK) | yes | ✓ | infinite canvas primitive | **EMBED** |
| **Heptabase / LiquidText / Muse** | no | — | spatial card ideation (pattern) | INTEROP (borrow UX) |

**Findings.** (a) Semantic Scholar's open graph is the discovery substrate every
other tool sits on — integrate it, free at our scale. (b) PDF reading is a solved
open problem — **embed** Semantic Reader's PaperCraft; do not build a reader.
(c) Discovery splits by *recall* (Undermind), *speed* (Perplexity), *structure*
(Elicit) — each is an agent sub-tool. (d) The
[research-reading-and-ideation-ui.md](research-reading-and-ideation-ui.md)
"grounded dialogue + backlinked incubation notes" model is the right frame; on
desktop its dual-pane reading↔notes is the default, and the spatial ideation
canvas (no embeddable product exists) is **built on tldraw**.

### 3.2 Code, notebooks, AI-coding (J3, session-record)

| Component | OSS | Emb? | Use | Posture |
|---|---|---|---|---|
| **Monaco** (MIT) | yes | ✓ | primary code + diff pane (`MonacoDiffEditor` free) | **EMBED** |
| **CodeMirror 6 + Lezer** (MIT) | yes | ✓ | lightweight read-only highlight, log annotation (~50 kB) | **EMBED** |
| **git-diff-view** (MIT) | yes | ✓ | GitHub-style patch rendering, off-thread for big diffs | **EMBED** |
| **xterm.js** (MIT) | yes | ✓ | terminal / log stream (already in stack) | **EMBED** |
| **marimo** (Apache-2) | yes | ✓ (WASM export) | reactive `.py` notebook; session-replay artifact | **EMBED/INTEROP** |
| **Observable Framework** (ISC) | yes | ✓ (static) | build-time metric/trace dashboards | EMBED |
| **JupyterLab / JupyterLite** | yes | iframe | live kernels on GPU hosts; in-browser Pyodide | INTEROP (kernel protocol) |
| **Cursor / Windsurf / Zed / Copilot app** | mixed | — | agent IDEs | INTEROP (MCP/CLI) |
| **Claude Code / Aider / Cline / Continue** | mixed | — | coding agents | INTEROP (MCP/spawn) — already in use |
| **code-server / VS Code Web** | FOSS | iframe | breakglass editor tab | INTEROP |

**Findings.** (a) **Monaco + CodeMirror is the decisive pairing** — Monaco for
the primary code/diff pane (IntelliSense, built-in diff), CodeMirror6 for
lightweight read surfaces (log lines, event-card snippets, mobile-safe). (b)
Diff, terminal, and huge-log rendering are all solved components — embed, don't
build. (c) The **reactive-notebook shift** (marimo DAG, Observable) is the right
model for the "record of a directed session": emit a marimo `.py` per closed run
→ render as a self-contained WASM bundle → link from the run record; a
reproducible replay with no server. (d) All coding agents are INTEROP-only via
MCP — unchanged; Claude Code is already our engine. (e) code-server is a full app
behind a hostile CSP — an iframe escape hatch, not an embed.

### 3.3 Experiment tracking + the comparison wall (J5) — **the headline BUILD**

| Tool | OSS/self-host | Reusable UI? | Note | Posture |
|---|---|---|---|---|
| **Weights & Biases** | SaaS (server ~$$$) | no (iframe) | *gold-standard* comparison UX | INTEROP (borrow UX) |
| **MLflow 3.x** | yes (Apache-2) | monolith, no lib | parallel-coords regressed 3.3.1; slow >500 runs | INTEROP (data-model ref) |
| **Aim** (MIT) | yes | no lib | best *OSS* comparison UX; clean API | INTEROP (design ref) |
| **ClearML** (Apache-2) | yes | plots iframe-able | full MLOps suite | INTEROP |
| **TensorBoard** | yes | iframe | scalars only, maintenance mode | EMBED (iframe) if needed |
| **optuna-dashboard** (MIT) | yes | **✓ React lib + WASM** | sweep/HPO: param importance, contour, Pareto | **EMBED** (sweep panel) |
| **Ray Tune / Optuna / Hydra** | yes | — | sweep *engines* | INTEROP |
| **trackio** (HF, MIT) | yes | no | pre-release, schema unstable | watch |
| **Neptune.ai** | — | — | **shut down 2026-03** | skip |

**Finding + decision — BUILD the comparison wall on our own data.** No open tool
exports a reusable run-comparison component; W&B/MLflow/Aim/ClearML are each a
full-stack app. The one narrow embeddable is **optuna-dashboard** (React lib) for
the sweep/HPO sub-panel. Meanwhile our data is already in the hub — the run
digest + `agent_turns` (cost/duration/errors) + config + OTLP spans
([ADR-038](../decisions/038-per-run-event-digest.md)/[045](../decisions/045-hub-storage-scaling.md))
map cleanly onto the standard `(run, params, {metric,step,value})` model with a
projection layer, **no schema surgery**. The W&B gold-standard UX — runs table
with sparklines → parallel-coordinates panel → per-metric overlays → config diff
— is four composable panels over MIT charting libs (§3.6). Building it keeps the
wall local-first and air-gappable (NAT'd GPU data never leaves the host), which
any SaaS integration would undermine. **Borrow** Aim's UX + MLflow's schema as
references; **embed** optuna-dashboard for sweeps.

### 3.4 Compute orchestration (stage 7 — dispatch)

| Backend | OSS/self-host | Integration | Note | Posture |
|---|---|---|---|---|
| **Slurm** (+ `slurmrestd`) | yes | REST+JWT | ubiquitous in academic/HPC | **INTEGRATE** (adapter) |
| **dstack** (MPL-2) | yes (1 container) | REST + SDK | best fit for **NAT'd GPU boxes**, no K8s | **INTEGRATE** (adapter) |
| **SkyPilot** (Apache-2) | yes | SDK + **Agent Skill** | multi-cloud burst; GPU Compass cost view | INTEGRATE + borrow Agent-Skill spec |
| **Ray Jobs** (Apache-2) | yes | REST | when researcher uses Ray Train/Tune | INTEGRATE (adapter) |
| **Modal** | SaaS | Python SDK | ephemeral GPU; **Claude Science's backend** | INTEROP (target) |
| **Kubeflow Trainer v2 / Flyte / Metaflow / Determined** | yes | CRD/SDK | K8s / typed workflows | INTEROP (submit-and-monitor) |
| **Runhouse** (Apache-2) | yes | `fn.to(cluster)` | *mental model to borrow* | borrow pattern |

**Findings.** (a) **Host-runners already exist** — build a *thin governed layer*,
not a scheduler. (b) The convergent UX is **plan-then-ask**: agent proposes a
`compute_plan` (GPU, hours, cost estimate, backend), director approves, *then*
spend flows — shipped by Claude Science, SkyPilot, Modal alike. Encode it as a
hub primitive on the governed-`propose` ladder
([ADR-030](../decisions/030-governed-actions-and-propose-verb.md)). (c) Borrow
**Runhouse's `fn.to(host)`** mental model for the agent-facing MCP tool
(`run_on(code, {gpu,hours}, backend="auto")`), not raw `sky launch` flags. (d)
Priorities: Slurm + dstack adapters first (HPC and NAT'd boxes — our topology),
SkyPilot/Ray next, Modal/Flyte/Metaflow as submit-and-monitor interop. (e) A
unified **cost/GPU panel** across backends (à la SkyPilot GPU Compass) is a
director-cockpit differentiator.

### 3.5 Observability + trace/audit (stage 12, J7)

| Tool | OSS/self-host | Emb? | Note | Posture |
|---|---|---|---|---|
| **OTel GenAI semconv** | spec | — | the `gen_ai.*` standard; hub already exports OTLP | **ADOPT** |
| **Arize Phoenix** (Apache-2) | yes | sidecar/iframe | OTLP-native LLM-aware trace UI | **INTEGRATE** (self-host sidecar) |
| **Langfuse** (MIT) | yes | API | traces + prompt/dataset evals | INTEROP |
| **Grafana + Tempo** (AGPL) | yes | **iframe panels** | infra traces/metrics | EMBED (iframe) |
| **Perfetto** (Apache-2) | yes | — | best dense-timeline idiom | BUILD-borrow (visual idiom) |
| **W&B Weave / LangSmith / Braintrust** | SaaS | API | eval-centric | INTEGRATE (optional) |

**Findings.** Hub OTLP export is already on the right rail — **adopt** GenAI
semconv, **integrate** a self-hosted **Phoenix** sidecar for LLM-aware agent-run
traces (zero lock-in, local), **embed** Grafana panels via iframe for infra. Do
not self-build a trace viewer; borrow Perfetto's flame-chart idiom for dense
`agent_turns` timelines only.

### 3.6 Visualization (stage 11, J5/J2)

| Library | License | Emb? | Best fit | Posture |
|---|---|---|---|---|
| **Plotly.js** | MIT | ✓ | scientific/statistical/3D | **EMBED** |
| **Vega-Lite** | BSD-3 | ✓ (JSON spec) | **agent-generated** plots | **EMBED** |
| **Apache ECharts 6** | Apache-2 | ✓ | streaming high-frequency dashboards | EMBED |
| **Observable Plot** | ISC | ✓ | quick exploratory marks | EMBED |
| **D3** | ISC | ✓ | bespoke escape hatch | build-on |

**Finding.** Standardize on **Plotly (scientific/3D) + ECharts (streaming
metrics) + Vega-Lite (agent-emitted specs)**, with Observable Plot for
exploration and D3 as the escape hatch. Vega-Lite is special: because agents emit
JSON, its declarative spec is the natural *agent→plot* interface — the workbench
renders an agent's plot with no code.

### 3.7 Writing · slides · publishing (stage 13, J2)

| Tool | License | Emb? | Use | Posture |
|---|---|---|---|---|
| **BlockNote** (MPL-2, on Tiptap) | yes | ✓ | Notion-style note/deliverable editor + Yjs collab | **EMBED** |
| **Tiptap / ProseMirror / Lexical** | MIT | ✓ | rich-text base (BlockNote's foundation) | EMBED (base) |
| **KaTeX** (+ MathJax fallback) | MIT | ✓ | inline math | **EMBED** |
| **Quarto** | MIT | CLI | `.qmd` → HTML/PDF/reveal.js reproducible report | INTEGRATE (build step) |
| **Typst** | Apache-2 | CLI | millisecond PDF (Quarto's fast path) | INTEGRATE |
| **Slidev** (Vue) / reveal.js / Marp | MIT | ✓/iframe | research decks (code-exec) | EMBED (Slidev) |
| **Overleaf / Curvenote / Google Docs** | mixed | link | external final-publishing | INTEROP |

**Findings.** **Embed BlockNote** (block UX, collab, Tiptap/ProseMirror base) +
**KaTeX** for the in-app authoring/decision-capture surface. **Integrate Quarto**
(agents write `.qmd`; hub runs `quarto render`) with Typst as the fast PDF path
for the reproducible-report pipeline. **Embed Slidev** for decks. Borrow Distill's
*executable-inline-figure-with-provenance* idea as an artifact association on the
hub's `Deliverable` — not a third-party platform.

### 3.8 Competitive — autonomous-science + workbench rivals

**Claude Science (Anthropic, 2026-06-30) — the closest mirror, and the single-lab
ceiling.** A coordinating agent + specialist sub-agents + a **reviewer agent**
(checks citations/calcs, ties every figure to the code that made it); runs
locally on macOS/Linux or over SSH to HPC/Slurm; **data stays on lab infra, only
step-context crosses to Anthropic** (inference is *not* local); artifacts bundle
code+env+message-history; fork-to-compare; 60+ database connectors; native
rich-artifact rendering. Layered onto Claude Pro/Max/Team/Enterprise (no separate
SKU) + a $30K-credits grant program. **Precision note:** it is a *local desktop
app with a browser-rendered UI* — not a URL you visit, and not local inference.
**Deliberate limits:** single-vendor (Claude only), single-PI (no fleet
governance, no multi-team routing, no audit/policy layer), desktop-only (no
mobile continuum).

| Platform | Human-in-loop? | Agents | Hosts | Open? | Coverage |
|---|---|---|---|---|---|
| **Claude Science** | PI approves plan steps | multi (coord+specialist+reviewer) | multi via SSH/Slurm | closed (Claude-only) | hypothesis→figure→manuscript; no fleet/governance |
| **Sakana AI Scientist v2** | fully autonomous | multi (tree-search) | single | OSS | hypothesis→paper; ~$50–200/cycle |
| **Google Co-Scientist** | reviews ranked hypotheses | multi ("tournament of ideas") | single/cloud | closed (Gemini) | hypothesis + lit only; no code exec |
| **FutureHouse Robin / PaperQA2** | autonomous (ran 2.5 mo) | multi | single/cloud | mixed | biology discovery; PaperQA2 = lit |
| **Stanford STORM** | autonomous | single (multi-perspective) | cloud/self-host | OSS | topic→article; no code |
| **AIDE (Weco)** | autonomous | single (code-search) | single | closed | Kaggle/MLE-Bench solver |
| **OpenAI Deep Research** | reviews report | single | cloud | closed | lit→report |
| **Deepnote / Hex / Julius / Lightning AI** | human-in-loop notebook | single assist | cloud | closed SaaS | analysis/collab; no fleet |
| **Cursor** | human-in-loop IDE | single | single/local | closed | code only |

**Findings.** (a) Autonomous-science systems (Sakana, Robin, AIDE) are **closed
pipelines, not workbenches** — no mid-run steering, no audit trail; TermiPod's
human-in-loop steward+task model is the governance-safe alternative. (b) Notebook
rivals (Deepnote/Hex/Julius/Lightning) are upstream analysis/collab tools, not
agent control planes — **interop** (export artifacts), don't compete on notebook
UX. (c) **Borrow from Claude Science:** the plan-then-ask compute consent, the
artifact = code+env+history bundle, the **reviewer-agent** auto-check pattern, and
the **domain-connector pack** (20–30 key connectors as YAML MCP tools closes most
of the "60 databases" gap cheaply — [ADR-033](../decisions/033-tool-catalog-naming-and-registration.md)
tool surface is already connector-shaped). (d) **Differentiate on:** multi-engine
(avoid Anthropic lock-in), multi-host A2A fleet, `audit_events`+policy governance,
data-on-your-hosts for regulated data, and the phone↔desktop continuum — none of
which any rival offers. Anthropic entering drug discovery is also a structural
opening for a vendor-neutral, operator-controlled alternative.

---

## 4. The synthesized register (the actionable output)

The whole lifecycle, one table — what to do per capability.

| Capability | Posture | Concretely |
|---|---|---|
| Paper reading | **EMBED** | Semantic Reader / PaperCraft (React SDK) |
| Literature discovery | **INTEGRATE** | Semantic Scholar API (backbone) + Undermind/Elicit/Perplexity as agent sub-tools + scite enrichment |
| Reference management | **INTEGRATE** | Zotero REST API v3 as the store |
| Grounded doc Q&A | **BUILD** | over our agents/corpus (NotebookLM pattern), cite-back to References tile |
| Ideation canvas | **BUILD** | on **tldraw** (papers/notes as typed-edge cards; backlink resurfacing) |
| Code / diff pane | **EMBED** | **Monaco** (+ `MonacoDiffEditor`) |
| Log / snippet highlight | **EMBED** | **CodeMirror 6 + Lezer**; git-diff-view; xterm.js |
| Session-replay artifact | **EMBED** | **marimo** WASM export per closed run |
| Coding agents | **INTEROP** | MCP/spawn (Claude Code already in use) |
| Compute launch | **BUILD (thin) + INTEGRATE** | `compute_plan` propose→approve primitive; Slurm(`slurmrestd`) + dstack adapters first, then SkyPilot/Ray, Modal/Flyte interop |
| Experiment tracking | **BUILD** | project hub digest/`agent_turns`/OTLP → `(run,params,metric)` |
| **Multi-run comparison wall** | **BUILD** | runs table + parallel-coords + per-metric overlays + config diff (Plotly/Vega-Lite) — *headline surface* |
| Sweep / HPO panel | **EMBED** | optuna-dashboard React components |
| Metric / scientific plots | **EMBED** | Plotly.js + ECharts + Vega-Lite (agent-emitted) |
| Agent-run traces | **INTEGRATE** | self-hosted **Phoenix** sidecar (OTLP), GenAI semconv |
| Infra traces/metrics | **EMBED** | Grafana panels (iframe) |
| Note / deliverable authoring | **EMBED** | **BlockNote** + KaTeX |
| Reproducible report / PDF | **INTEGRATE** | Quarto (+ Typst fast path); agents write `.qmd` |
| Slides | **EMBED** | Slidev (Marp fallback) |
| Provenance / artifacts | **BUILD** | code+env+history bundle on `Deliverable` (Claude Science pattern) |
| Fleet mission-control | **BUILD (have it)** | promote the control plane to a pinned desktop rail |
| Domain connectors | **BUILD (YAML)** | 20–30 key science DBs as MCP tools |

**Read of the table:** ~two-thirds of the surface is EMBED/INTEGRATE of proven
components; the BUILD list is small and *fleet-native* — grounded Q&A, ideation
canvas, the compute-consent primitive, tracking + the **comparison wall**,
provenance, mission-control, connectors. That is the correct concentration of
effort under ADR-050.

## 5. First surfaces (sequencing)

Ship one at a time, most-valuable-and-most-mobile-hostile first:

1. **Multi-run comparison wall** (BUILD) — the biggest research win, doesn't
   exist anywhere for our data, intrinsically wide-screen. Foundations already
   shipped (run-detail, digest, OTLP).
2. **Read + Author pair** (EMBED Semantic Reader + BlockNote) — realizes the
   [research-reading-and-ideation-ui.md](research-reading-and-ideation-ui.md)
   dual-pane; the most mobile-hostile jobs.
3. **Compute-consent primitive** (BUILD thin + Slurm/dstack adapters) — turns the
   fleet from "runs happen" into "director approves the spend."
4. **Ideation canvas** (BUILD on tldraw) + **Phoenix trace sidecar** (INTEGRATE).

Each resolves a slice of this doc into a plan; the control-plane half rides the
existing hub API and can lag or be embedded from mobile at first (ADR-050 D-5).

## 6. Open questions

1. **Web framework + shell** — React/Svelte/Solid; plain browser vs Tauri; how
   much design-system to re-express vs. sharing tokens (ADR-047).
2. **Control-plane on desktop** — rebuild web-tech, embed the Flutter surface, or
   defer to mobile and ship desktop workbench-only first?
3. **Comparison-wall charting lib** — Plotly vs Vega-Lite vs ECharts as the
   primary (parallel-coords + overlays); all MIT/BSD, pick for perf + agent-spec.
4. **marimo vs. Observable** for the session-replay artifact (Python-reactive vs
   JS-static) — or both, for different surfaces.
5. **Phoenix embed vs. build** — sidecar iframe now, native trace view later?
6. **Connector-pack scope** — which 20–30 databases; generic (PubMed/arXiv/PDB)
   vs. domain-targeted, given we are ML-research-first not bio-first.

## Appendix — sources (representative, mid-2026)

- **Literature:** Semantic Scholar API + Semantic Reader / PaperCraft
  (openreader.semanticscholar.org; arXiv 2303.14334); Zotero Web API v3; Undermind
  / Elicit / Consensus / Perplexity comparisons; tldraw.dev.
- **Code/notebooks:** Monaco vs CodeMirror6 (pkgpulse, Sourcegraph migration
  post); marimo WASM embedding docs; JupyterLite 0.8; Observable Framework;
  git-diff-view; Claude Code/Aider/Cline comparisons.
- **Tracking:** W&B parallel-coordinates docs; MLflow 3 self-host + issue #17388;
  Aim; ClearML; optuna-dashboard; Neptune shutdown notices; trackio (HF).
- **Compute:** SkyPilot Agent Skill + GPU Compass; dstack REST API; Slurm
  `slurmrestd`; Ray Jobs; Modal billing; Runhouse; Claude Science compute model.
- **Observability/viz/writing:** Arize Phoenix + OpenInference; Langfuse OTel;
  Grafana Tempo; Plotly/Vega-Lite/ECharts/Observable comparisons; Tiptap vs
  Lexical vs BlockNote; Quarto+Typst; Slidev/Marp/reveal.js.
- **Competitive:** Anthropic Claude Science announcement + MIT Tech Review / STAT
  / Forbes / HPCwire coverage; Sakana AI Scientist v2; Google Co-Scientist;
  FutureHouse Robin/PaperQA2; Stanford STORM; Weco AIDE; OpenAI Deep Research.

## Related

- [`decisions/050-desktop-workbench-delivery-model.md`](../decisions/050-desktop-workbench-delivery-model.md)
  — the decision this register serves.
- [`desktop-research-surface.md`](desktop-research-surface.md) — the role/work
  derivation (J1–J7) and the two-halves split.
- [`research-reading-and-ideation-ui.md`](research-reading-and-ideation-ui.md) —
  the reading/ideation content model (grounded dialogue + incubation notes).
- [`positioning.md`](positioning.md) §3 — the Claude Science competitive axis.
- [`decisions/038-per-run-event-digest.md`](../decisions/038-per-run-event-digest.md)
  + [`045`](../decisions/045-hub-storage-scaling.md) — the digest/`agent_turns`/OTLP
  substrate the tracking + comparison wall build on.
