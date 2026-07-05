# Research material data model

> **Type:** discussion
> **Status:** Open (2026-07-05) — director directive: all research materials
> scattered across machines must be retrievable from the desktop workbench, so a
> report/paper can be composed by combining them; and any input/paper/report/
> digest must decompose into reusable elements (figure, table, chart, quote, …)
> for recomposition. Companion to [ADR-050](../decisions/050-desktop-workbench-delivery-model.md).
> **Audience:** contributors · maintainers · principal
> **Last verified vs code:** v1.0.820

**TL;DR.** Model every research material as a typed, addressable **Research
element** — `figure · chart · table · quote · equation · code · citation ·
claim/finding · note · rollout-video · trajectory · 3d-asset · section`. The
**hub owns an index of these elements** (stable id + content-address + provenance
+ backlinks + retrieval text/embedding + a *pointer to where the bytes live*);
**hosts keep the bytes**, fetched on demand — which preserves the data-ownership
law and is exactly how **git-annex / RO-Crate / OAI-PMH** already work at scale.
Documents/reports are **not bags of bytes** but ordered trees of **element
references** (transclusion, pointer-not-copy — the Notion/Roam/MyST pattern), so
a composed paper carries full, inspectable lineage and edits to a source element
propagate. Three flows: **decompose** (ingest → typed elements), **retrieve**
(hybrid search over the one hub index, always available), **recompose** (assemble
element-refs → render fetches bytes from whichever host holds them). This element
store is also the **shared substrate for agent memory** (see the skills/memory
companion) — one knowledge index, scoped views.

---

## 1. The requirement, and the hard tension

The director's directive has two halves:

1. **Retrieve everything from one app.** Materials are scattered across the
   fleet's machines (a VPS, several GPU boxes); the workbench must let the
   director find and pull *any* of them to compose a report or paper.
2. **Decompose ↔ recompose.** Any input/paper/report/digest must break into
   **reusable elements** (graph, chart, sheet/table, quote, figure, equation,
   code, citation, claim), and those elements must recombine into new documents.

Half (1) collides head-on with the blueprint's **data-ownership law**
(`spine/blueprint.md`): *the hub owns names, policies, events, references —
metadata, **not bytes**; hosts hold the bytes.* Naively "pull all materials into
the app" would turn the hub into a blob store that (a) breaks the law and (b)
will not fit on a 2 GB / 2-vCPU VPS once rollout videos and checkpoints are in
play. The whole design hinges on resolving this without breaking either.

## 2. Resolution — an index in the hub, bytes on the hosts

**Decision (director, 2026-07-05): index + on-demand fetch.** The hub stores only
an **index of element references** — metadata, provenance, backlinks, retrieval
text/embeddings, and a **byte-locator** (which host holds the bytes, and where).
"Retrieve everything" is a query over that index, which is *always available*.
"Open / compose" resolves an element's byte-locator and **fetches the bytes from
the host on demand**, caching locally. Large bytes (videos, checkpoints) never
enter the hub; small composition-critical bytes *may* be cached, but the model of
record is fetch-on-demand.

This is not novel — it is the settled pattern of four mature systems, which we
adopt as reference architectures:

- **git-annex** — the closest existing analogue to our law: a versioned metadata
  branch tracks *which of N machines holds each content-key*; the working tree is
  symlinks; `get` fetches bytes on demand; no single remote holds everything.
  **This is our reference model for the byte-locator + fetch layer.**
- **RO-Crate** — a JSON-LD manifest can describe **remote** data entities by
  absolute URI with **zero bytes bundled**. "Index all, bundle none" is a
  first-class, standardized shape.
- **OAI-PMH / Fedora** — decades of institutional precedent for *harvest the
  metadata centrally, serve the bytes at origin*; a Fedora datastream can be a
  redirect to bytes held elsewhere.
- **FAIR principle A2** — *metadata must remain accessible even when the data is
  not.* This licenses (indeed requires) that an element's metadata, provenance,
  and backlinks stay queryable when its host is offline — show a tombstone/landing
  record (DataCite discipline), not an error.

Content-addressing (Git blob SHA / IPFS CID) supplies the last piece: **identity
is separate from location.** The hash names the bytes (dedupe + integrity
verification for free); the byte-locator says where copies live. The hub owns the
name; hosts serve the location.

## 3. The ResearchElement

The atomic decomposable/composable unit. The **hub stores this record**; the
**host stores the bytes it points at**. Proposed shape (a discussion sketch, not
a final schema):

```
ResearchElement {
  id:            "elt_<uuid>"     // STABLE logical id — hub-owned, survives edits, never renumbers
  type:          figure | chart | table | quote | equation | code_block |
                 citation | claim | finding | note | section |
                 rollout_video | trajectory | 3d_asset          // last three = embodied-AI (companion doc)
  title:         string
  proxy_text:    string           // non-text elements get an LLM caption/summary — this is what gets
                                   //   embedded and shown in search (a table→summary, figure→caption)
  embedding:     vector | ref     // lives in the hub index (it is metadata); or a ref to it

  // --- content address + where the bytes live (the hub law) ---
  content_address: "sha256:<hash>"   // hash of the bytes: dedupe + integrity; changes when bytes change
  byte_locator: {
     remotes:    ["gpu-box-2", "vps-1"],   // git-annex-style: which machines hold a copy (0..N)
     path_or_uri:"file://…/fig3.svg" | "https://…",
     media_type: "image/svg+xml",
     size:        48211
  }

  // --- provenance (W3C PROV-O), event-shaped → hub-native ---
  provenance: {
     wasGeneratedBy:  "run_<id>" | "ingest_<id>",     // the agent run / decompose activity
     wasDerivedFrom:  ["elt_<uuid>", "doi:10.…"],     // source element(s) / paper
     wasAttributedTo: "agent_<id>" | "host_<id>",
     generated_at:    ts
  }

  // --- graph edges ---
  backlinks:     ["elt_<uuid>", "doc_<uuid>"]   // DENORMALIZED on write (Anytype/Roam pattern)
  transclusions: ["elt_<uuid>"]                 // elements this one references (pointer, not copy)
  evidence:      [{ target: "elt_<uuid>", role: supports|refutes|neutral, rating: float }]  // claim/finding

  labels: [...], created_at, updated_at, scope: {team, project?, visibility}
}
```

Design rules distilled from the prior-art survey (§ sources):

- **Two orthogonal identifiers.** A **stable logical id** (hub-owned, names the
  element, never changes across edits) *and* a **content-address** (hash of the
  bytes, changes when bytes change). git-annex, IPFS, and DataCite all separate
  name from location; the hub owns the first, hosts serve the second.
- **The id must survive editing, not just creation.** BlockNote/Tiptap's win is
  that block IDs persist through split/merge/undo by tracking node identity in
  transaction mapping. Whatever mints element ids must not renumber on edit.
- **Provenance is PROV triples, not free text** — `wasGeneratedBy` /
  `wasDerivedFrom` / `wasAttributedTo`. This is already *event*-shaped, a natural
  fit for a hub whose core primitive is events.
- **Claims/findings are first-class elements, distinct from the document they came
  from** (the ORKG contribution-graph and the nanopublication pattern: assertion +
  provenance + publication-info as the smallest citable unit). That separation is
  what lets cross-run/cross-paper comparison tables be built by query, and it
  connects directly to the run-comparison wall (ADR-050).
- **Claim + evidence has a settled shape** — a claim with typed evidence edges
  (`supports/refutes/neutral`, à la FEVER / schema.org `ClaimReview`) + confidence.

## 4. Documents are trees of element references

A **CompositeDoc** (report / paper / digest / slide deck) is **not** a byte blob —
it is an ordered tree of **ElementRefs**:

```
CompositeDoc { id, title, body: [ ElementRef{ element_id, display_override?, keep_lens? } … ] }
```

Composition stores only `element_id` **pointers**, never element bytes — the
universal transclusion pattern (Notion `synced_from`, Roam `{{embed ((uid))}}`,
MyST `#target` resolved at build, org `#+transclude`). Consequences:

- The element stays **single-source-of-truth**; editing it propagates to every
  report that references it (Roam live-window semantics).
- A composed paper carries **inspectable lineage** — every figure still points at
  the run + code that produced it (the Claude Science "artifact = code + env +
  history" property, but fleet-wide and cross-machine). Xanadu's rule: provenance
  is inescapable.
- The authoring surface (BlockNote, per the landscape) composes *over the element
  store* — dragging an element in inserts an ElementRef, not a copy.

## 5. The three flows

- **DECOMPOSE** (paper/report/digest → elements). An agent parses **structure-
  aware** (respect the document's own section/heading hierarchy — never split
  across a boundary; that is exactly where reusable elements live), segments into
  typed elements, and for each: mints the stable id, computes the content-address,
  writes bytes to a host + records the byte-locator, generates `proxy_text` for
  non-text types, embeds into the hub's unified index with a `type` tag, and
  records PROV `wasDerivedFrom` the source. Claims/findings are extracted as
  first-class elements separate from their source doc.
- **RETRIEVE** (all materials, one app). Query the **hub index only** — **hybrid
  BM25 + dense, RRF-fused, cross-encoder re-ranked** (the 2026 default), over
  **one** vector table filtered by `type`/host/provenance (not federated per-type
  indices). Non-text elements are found via their `proxy_text`. Always available
  (FAIR A2), even when a host is offline. Results are element *references*; bytes
  are fetched lazily.
- **RECOMPOSE** (elements → new report). Assemble a CompositeDoc of ElementRefs;
  render resolves each `element_id` → byte-locator → **`get` from whichever host
  holds the bytes**, cache locally (git-annex `get` / IPFS pin / OAI-PMH harvest).
  Provenance + backlinks travel with every transcluded element, so the composed
  report is auto-cited and reproducible.

## 6. Retrieval, concretely

- **One unified index, `type`-filtered** — LlamaIndex/RAG-Anything default; "find
  by meaning" across heterogeneous elements works because everything shares one
  embedding table keyed by element type.
- **Non-text via a text proxy** — tables → LLM table+caption summary; figures →
  caption + (optional) vision embedding; equations → symbolic form. The proxy is
  stored on the element, so the same element is both retrievable and renderable.
- **Structure-aware + late chunking** beat fixed-size — segment on the ~3-level
  heading hierarchy; embed long context first, then pool per element, so each
  element vector is conditioned on its document.
- **Embeddings are metadata** → they live in the hub index; only the raw
  figure/table/video **bytes** stay on the host.

## 7. Relationship to existing primitives — and the unification

This model does not invent a parallel world; it is the **substrate under**
several things already in flight:

- The **note/excerpt** entity proposed in
  [`research-reading-and-ideation-ui.md`](research-reading-and-ideation-ui.md) §7
  is simply the `note`/`quote` element type — the reading surface *deposits*
  elements; this doc generalizes them.
- **Document / Deliverable / Artifact** and their sections (glossary) become
  CompositeDocs and elements; the **References tile** (`citation` artifact-kind)
  is the `citation` element type; `paper`/`lit-review` document kinds are
  CompositeDocs.
- The **embodied-AI** artifacts — rollout videos, trajectories, 3D/USD assets —
  are element types, so robotics materials compose into reports the same way. The
  concrete `robot.episode` element (MCAP raw log + LeRobot v3 export, provenance +
  physical-coherence, failure-as-data outcome) is specified in
  [`embodied-ai-tooling-landscape.md`](embodied-ai-tooling-landscape.md) §3.3 — it
  is a direct instance of this model (hub index + bytes-on-host).
- **Agent memory** (skills/memory companion) is the *same shape* — typed,
  retrievable, backlinked knowledge with provenance. The lean is **one knowledge
  substrate, scoped views**: an agent's operational memory and the director's
  research materials share the index and retrieval engine, differing by `scope`
  and audience, so a finding an agent surfaces can *graduate* into the director's
  materials without a second store.

## 8. Build / embed / integrate / interop

- **BUILD** — the ResearchElement index + byte-locator + provenance graph in the
  hub (new tables; additive to the event/digest stores of
  [ADR-045](../decisions/045-hub-storage-scaling.md)); the decompose/retrieve/
  recompose services; the retrieval index (hybrid). This is fleet-native and is
  the reason the hub exists — squarely a BUILD.
- **EMBED** — BlockNote for composition over element-refs (landscape §3.7); the
  per-type element viewers (PDF/figure/table/chart, and the robotics viewers in
  the companion doc).
- **INTEGRATE** — an embedding/vector index; optionally a re-ranker.
- **INTEROP / adopt-standard** — **PROV-O** vocabulary for provenance;
  **content-addressing** (SHA/CID) for identity; the **git-annex** location model
  for the byte layer; **RO-Crate** as the export/interchange format for a bundle
  of materials (so a composed report + its elements is a portable, standard
  research object); **nanopublication** shape for claim elements.

## 9. Open questions / forks

1. **Storage of the index** — new hub tables vs. extend the event/digest stores;
   per-team shard alignment (ADR-045) for elements.
2. **Byte layer implementation** — reuse a git-annex-style locator ourselves, or
   a thinner host-runner `fetch(content_address)` MCP call; caching + gc policy.
3. **Embedding location** — in-hub (SQLite-vec / a small vector store) vs. a
   sidecar; keep it local-first and air-gappable.
4. **Decompose engine** — which agent/skill does structure-aware extraction; how
   much is automatic on ingest vs. on demand.
5. **Element vs. memory unification** — one table with a `scope` discriminator, or
   two tables over a shared retrieval engine (resolve with the skills/memory doc).
6. **Mutable elements + propagation** — when a source element is edited, how far
   does propagation to referencing CompositeDocs go (live vs. pinned-version
   refs); versioning of elements.

## Related

- [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) — the desktop
  workbench this data model lives under.
- [`research-tooling-landscape.md`](research-tooling-landscape.md) — the
  build/embed/integrate register (authoring = BlockNote; retrieval libs).
- [`research-reading-and-ideation-ui.md`](research-reading-and-ideation-ui.md) —
  the note/excerpt entity this generalizes into the element model.
- [`embodied-ai-research-workbench.md`](embodied-ai-research-workbench.md) — the
  robotics artifact types (rollout-video / trajectory / 3d-asset) as elements.
- [`spine/blueprint.md`](../spine/blueprint.md) — the data-ownership law this
  model is engineered to preserve.
