# Literature reading app landscape

> **Type:** discussion
> **Status:** Open (2026-07-15) — a focused competitive scan of AI-assisted
> paper/PDF *reading* products, requested by the director to inform TermiPod's
> J1 Read surface. A point-in-time web-research snapshot; deep-dives the
> read/discover slice that the broader
> [research-app-product-landscape.md](research-app-product-landscape.md) surveys
> at a glance. Companion to
> [reference-library-and-reading.md](reference-library-and-reading.md) and
> [research-intake.md](research-intake.md).
> **Audience:** contributors · principal
> **Last verified vs code:** desktop-v0.3.52
> **Freshness:** snapshot

**TL;DR.** The AI-reading market has split into **four clusters** that barely
compete head-on — in-PDF **reading copilots**, cross-corpus **review/evidence
agents**, **citation-graph discovery**, and **reference-manager/note-first**
readers. Bare "chat with one PDF" is fully commoditized (ChatPDF is the
reference point) and the underlying frontier model (GPT-5 / Claude 4.x /
Gemini 2.5) is *not* a durable differentiator — nearly everyone wraps the same
few. What differentiates leaders is **corpus + discovery integration**,
**cross-paper synthesis** (contradiction-finding, evidence tables), **parsing
IP**, **citation-intent-as-data**, and — the emerging real moat —
**evidentiary credibility** (open benchmarks, peer-reviewed accuracy). The
clearest whitespace maps onto TermiPod's own ADR-050 stance: **no product
combines a local-first store + on-device/BYO-model inference + E2E privacy**,
and the citation layer is starting to package itself as an **MCP tool surface**
for external agents — precisely TermiPod's framing.

Scope: excludes the first-party products of Anthropic (Claude), OpenAI (ChatGPT),
and Google AI (Gemini/NotebookLM); products *built on* those models are included.
All pricing / user-counts / accuracy figures are vendor-stated unless an
independent source is named, and several vendor pages blocked automated fetch —
treat as directional and re-check live before quoting. See §8 for flags.

---

## 1. The four clusters

The market does not sort by "which is best" but by **what the innovation
optimizes**. A tool strong in one cluster is usually absent from the others.

### 1a. In-PDF reading copilots ("chat with paper")

| Product | Core / key innovation | Note |
|---|---|---|
| **SciSpace** (Copilot) | Chat wired into a **269M-paper corpus + a ~150-tool "Research Super Agent"** — one paper flows into cross-corpus discovery | "Deep Review" → ~1,000-word cited draft |
| **Anara** (ex-Unriddle) | **Cross-document synthesis that surfaces *contradictions* between studies** across ~10k files | 2025 rebrand: chat tool → workspace |
| **ChatDOC** | **Proprietary PDF layout/table/formula parsing** with bounding-box grounding — the one defensible *technical* moat; sold as a Parser API | arXiv:2401.12599; benchmarked vs Gemini 2.0 |
| **Explainpaper** | **Highlight-to-explain as the whole product**, re-highlight to go progressively simpler | Narrow, student-focused |
| **ChatPDF** | The commodity baseline — frictionless single-PDF Q&A, click-to-passage citations | Degrades on 300+ page docs |

Adjacent: Humata (multi-file + team/security), Petal & Enago Read (Zotero-with-AI
academic workspaces), AskYourPDF & ChatDOC (developer **API** plays), Sharly
(citation-accountable enterprise), Afforai→Logically.

### 1b. Review / evidence agents (search across *many* papers)

| Product | Core / key innovation |
|---|---|
| **Elicit** | **Structured extraction into evidence tables** (row/paper, per-cell source grounding) + a real PRISMA systematic-review workflow. Most methodologically serious |
| **Consensus** | The **Consensus Meter** — quantified % of studies supporting/mixed/contradicting a yes/no question; "AI only *after* retrieval" |
| **Undermind** | **Certified-completeness recall** — agentic multi-hop search that statistically estimates the total relevant papers and signals *when to stop* |
| **Ai2 Asta** (Allen Institute) | **Attribution-first + open self-benchmark** — nonprofit, open-source, ships AstaBench which publicly ranks it vs rivals |
| **FutureHouse PaperQA2** | **Agentic RAG over full-text science with peer-reviewed *superhuman* precision** vs PhD annotators (arXiv:2409.13740) |

Also: Scinapse (bibliometric/expert discovery), Paperguide (end-to-end pipeline),
Stanford **STORM/Co-STORM** (multi-perspective question-asking → cited article,
open-source), SciSpace Deep Review.

### 1c. Citation-graph discovery & smart citations

| Product | Core / key innovation |
|---|---|
| **Scite.ai** | **Smart Citations** — a classifier labels 1.6B citation statements *supporting / contrasting / mentioning* with the exact citing sentence; now exposed as an **MCP tool** for ChatGPT/Claude/Cursor |
| **Semantic Scholar** (Ai2) | **Semantic Reader** (inline citation cards on hover) + **TLDR** one-line summaries + Research Feeds — and the free **data substrate** most others build on |
| **Connected Papers** | **Similarity graph from co-citation + bibliographic coupling** (not direct-citation edges) from a single seed |
| **Research Rabbit** | **Collection-centric iterative discovery** ("Spotify for papers"); *acquired by Litmaps, late 2025*, went freemium |
| **Litmaps** | **Living/monitored maps** — background daily "monitored searches" alert you as new matching papers publish |
| **Inciteful** | **Network-science link-prediction** on the citation graph + a two-paper "Literature Connector" bridge; free, inspectable SQL |

Open substrate (free, powers nearly everyone): **OpenAlex** (250M-work open
graph), **CORE** (46M full texts), **Semantic Scholar** API.

### 1d. Reference-manager + AI & note-first readers

| Product | Core / key innovation | Local-first? |
|---|---|---|
| **Zotero 7** + plugins | Deliberately **AI-neutral, fully local library**; AI only via plugins — **PapersGPT** (real offline/local-LLM mode), **Beaver** (Harvard; sentence-level cited retrieval over your library) | ✓ (PapersGPT = strongest true-offline AI) |
| **Mendeley** (Elsevier) | **"Ask My Library"** — AI grounded in *your own* PDFs; "Compare Experiments" table of ≤10 PDFs (Dec 2025) | cloud |
| **Readwise Reader** | **Ghostreader** — the highlight → spaced-repetition → in-context-AI retention loop | cloud |
| **Recall** | **Auto-summarize → auto-connect → spaced-repetition** — the knowledge graph builds itself | cloud |
| **Heptabase** | **Canvas + cards + PDF + AI as one workflow**; local-first store but AI calls out to cloud | ◐ |

Also: ReadCube Papers (Dimensions graph), EndNote 2025 (institutional
privacy-first AI), Paperpal (writing + AI-disclosure "AI Footprint"), Mem 2.0
(offline-first AI notes), Sider/Monica (model-agnostic sidebars, cloud-only).

## 2. Table-stakes vs genuinely differentiating

**Commoditized — everyone has it, none of it differentiates:**
click-to-passage citations · OCR · multi-document chat · summarization ·
multilingual · freemium → ~$8–20/mo · and *being a wrapper over a frontier
model*. Model quality is not a durable moat; the same GPT-5 / Claude 4.x /
Gemini 2.5 sit under most of the field.

**The five axes that actually define leaders:**

1. **Corpus + discovery integration** — chat wired to a 200M+ paper index, not
   just the open PDF (SciSpace 269M, Elicit ~138M, Afforai/Paperguide ~200M via
   Semantic Scholar).
2. **Cross-paper synthesis** — contradiction-finding, evidence tables, agreement
   meters, AI comparison tables (Anara, Elicit, Consensus, Petal).
3. **Parsing-quality IP** — the one defensible *technical* edge (ChatDOC's
   layout/table/formula recognition).
4. **Citation-intent as data** — supporting/contrasting classification (Scite).
5. **Evidentiary credibility** — open benchmarks + peer-reviewed accuracy (Ai2
   Asta, FutureHouse) — the emerging real moat (see §3).

## 3. Credibility is the real moat, and it stratifies sharply

Accuracy claims are *not* comparable across vendors, and the honest ones say so:

- **Peer-reviewed / open-benchmark evidence** — Ai2 (AstaBench, ALCE citation
  precision/recall), FutureHouse (superhuman synthesis, arXiv:2409.13740),
  Stanford STORM (NAACL 2024), Elicit (noninferiority preprint). ≫
- **Marketing "zero-hallucination" claims with no benchmark** — Scinapse,
  Paperguide, Consensus's structural "AI only after retrieval" argument. ≫
- **Testimonial-only** — SciSummary, most consumer copilots.

Two accuracy *vocabularies* coexist and must not be conflated: **fabrication /
citation-support** (Elicit, Ai2) vs **recall / completeness** (Undermind) — a
tool can be high-recall and still misquote. And vendor numbers are
query-dependent: an independent 2025 study (Cochrane/SAGE) found Elicit's
screening sensitivity fall from a headline **96.9% → ~38%** under realistic
search strategies. **Lesson for us:** if TermiPod ever advertises accuracy,
ground it in a reproducible harness, not a testimonial — the open flank
(Ai2/FutureHouse) sets the bar commercial vendors get judged against.

## 4. Market signals, 2025–26

- **Capital + consolidation:** Consensus $30M, Elicit $22M, FutureHouse/Edison
  ~$70M (~$250M val); Litmaps ← Research Rabbit; Research Rabbit dropped
  "free forever" for freemium.
- **A strong open/nonprofit flank** (Ai2 Asta, FutureHouse PaperQA2, Stanford
  STORM) competes on transparency and *publishes the benchmarks* commercial
  vendors are scored on.
- **Positioning drift "chat tool → research workspace":** two 2025 rebrands
  (Unriddle→Anara, Afforai→Logically).
- **The frontier is moving from summarize/search to autonomous discovery
  agents** (Ai2 DataVoyager, FutureHouse Kosmos, Elicit Research Agents).
- **The citation layer is packaging itself for agents:** Scite MCP exposes its
  database to external LLM tools — the clearest signal a reading product's value
  can be a *tool surface*, not only a human UI.

## 5. Implications for TermiPod

Three read straight onto current threads:

1. **The whitespace is our ADR-050 position.** No product combines *local-first
   store + on-device/BYO-model inference + E2E privacy*. Each near-neighbor
   breaks one leg: Zotero has no native AI; PapersGPT is a plugin not a reader;
   Heptabase/Mem keep server-side copies and route content to cloud LLMs; the
   pure copilots are cloud-only (Sider explicitly "no local mode"; Monica is
   China-owned cloud). A local-first, agent-native reader is genuinely un-owned
   ground — this is the [reference-library-and-reading.md](reference-library-and-reading.md)
   and [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) thesis,
   externally validated.
2. **The [research-intake.md](research-intake.md) gaps are parity features, not
   novelties.** Our "monitor" = Litmaps *monitored searches* / Research Rabbit
   *alerts* / Scite *alerts*; our "connector" = the browser extensions every
   copilot ships. We are closing a known gap, not inventing a category.
3. **Expose the library + citation graph as an MCP tool surface.** Scite's MCP
   move is the most TermiPod-relevant signal: don't build only a reader UI —
   surface the library so stewards/agents can query it. That is a differentiator
   none of the consumer copilots have and it fits our agent-native framing.

## 6. What to borrow (for the build/embed/integrate register)

Per the project's **BUILD · EMBED · INTEGRATE · BORROW · INTEROP** rule (see
[research-tooling-landscape.md](research-tooling-landscape.md)), initial reads —
to be classified when scheduled:

- **BORROW (UX pattern):** Semantic Reader's *inline citation cards on hover*;
  Elicit's *per-cell source-grounded extraction table*; Scite's *supporting /
  contrasting* colour coding; Connected Papers' *single-seed similarity map*.
- **INTEGRATE (data substrate):** OpenAlex / Semantic Scholar / CORE are already
  in `src/discovery/` — the same open corpora the whole field builds on.
- **INTEROP (agent surface):** Scite MCP as a model for exposing our own
  library/citations over MCP.
- **BUILD (our differentiator):** the local-first + BYO/on-device-model +
  E2E-private reader that no incumbent occupies.

## 7. Open questions

1. **Which cluster does J1 Read anchor in first** — reading-copilot (single
   paper deeply) or discovery-graph (find-what-I-didn't-know)? They imply
   different first surfaces.
2. **Accuracy stance** — do we commit to a reproducible citation-grounding
   harness early (§3), or defer until the reader is proven?
3. **MCP-first vs UI-first** — ship the library as an agent tool surface before,
   with, or after the human reader UI?
4. **Extent of on-device inference** — BYO-key (cheap, matches the field's
   emerging norm) vs true local models (the only fully-private option)?

Resolves into the J1 plan and, where a decision is load-bearing (accuracy stance,
MCP-first), an ADR.

## 8. Verification flags

Web-research snapshot (2026-07); agent-gathered. Soft spots to re-check before
any published claim: most pricing / user-counts / ARR are vendor-stated and
conflicted across aggregator sources (several vendor pages returned 403 to
automated fetch); paper-corpus sizes (138M–310M) are self-reported; headline
accuracy numbers are query-dependent and sometimes contradicted by independent
studies (Elicit 96.9%→38%); Research Rabbit ↔ Litmaps is reported as an
acquisition by secondary sources without a first-party terms/date confirmation;
Explainpaper and Humata show no verifiable 2025–26 development (possible
stagnation); "Recall" (getrecall.ai) is distinct from the "Recall.ai"
meeting-transcription API — sources conflate them.
