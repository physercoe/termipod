# Reference library + reading surface

> **Type:** discussion
> **Status:** Resolving (2026-07-10) — designs the **J1 Read** surface as a
> Zotero-shaped **reference library** fused with literature **discovery**. Round-1
> frontend shipped (`ReadSurface.tsx` — library + Semantic Scholar discovery,
> device-local storage); this doc records the borrowed-feature survey, the
> library **data model** and its mapping onto the hub data-ownership law, and the
> build sequence toward a hub-backed, fleet-shared, agent-readable library. Feeds
> [desktop-workbench-jobs.md](../plans/desktop-workbench-jobs.md) (J1 deepening)
> and the postures in [research-tooling-landscape.md](research-tooling-landscape.md)
> §3.1.
> **Audience:** contributors · principal
> **Last verified vs code:** desktop v0.3.16

**TL;DR.** The J1 Read tab shipped rough (paste-Markdown ↔ notes). This redesigns
it into a real **reference manager** — the director's ask: "borrow the leading
products (Semantic Scholar, Elicit, Undermind) and build a doc/file database like
Zotero." The synthesis: **Zotero owns the *library* model** (collections, items
with rich metadata, tags, notes, attachments, citation export) — build that;
**Semantic Scholar owns *discovery*** (200M-paper graph, TLDR summaries,
citations) — INTEGRATE its free API; **Elicit and Undermind own *agent-driven
extraction/recall*** (structured tables across a paper set, exhaustive recursive
search) — these map onto TermiPod's own agents, not a third-party embed. Round 1
shipped the library + Semantic Scholar search device-local; the target is a
**hub-backed `Reference` entity** (metadata in the hub, PDF **bytes** as
content-addressed blobs on hosts — the data-ownership law) so the library is
shared across the mobile↔desktop continuum and readable by the fleet.

---

## 1. What each leading product is *for* (and what we borrow)

| Product | Its core competence | TermiPod posture | What we borrow |
|---|---|---|---|
| **Zotero** | the **library**: collections, item types + metadata schema, tags, notes, attachments (PDFs), citation styles, sync | **BUILD** (on the hub) | the whole library model + cite export; Zotero is itself local-first, which validates round-1 device-local |
| **Semantic Scholar** | **discovery**: 200M-paper graph, free Graph API, **TLDR** summaries, citation/reference edges, open-access PDF links | **INTEGRATE** (API) | search → import; TLDR + abstract + citation count in the reader; DOI/arXiv resolution; PaperCraft reader later (EMBED) |
| **Elicit** | **structured extraction**: ask a question, get a table of findings across many papers (method, sample, result columns) | **BUILD on our agents** | the "extraction table over a collection" UX — a steward-dispatched task over library items, not a SaaS |
| **Undermind** | **exhaustive recursive recall**: a slow, deep agent that keeps searching until saturation | **BUILD on our agents** | a "deep-search this question" agent mode that writes results back as library items |
| **Zotero connectors / DOI resolvers** | one-click "save this" from a page or identifier | **INTEGRATE** | add-by-DOI / add-by-arXiv via a metadata resolver (Semantic Scholar / Crossref) |

**The load-bearing distinction:** the *work surfaces* (library UI, reader,
citation formatting) are ours to BUILD; the *discovery/extraction intelligence*
is either a free API to INTEGRATE (Semantic Scholar) or a job for our **own
agents** (Elicit/Undermind patterns) — which is exactly TermiPod's differentiator
(a fleet of agents), not a feature to outsource.

## 2. The library data model

The `Reference` shape (shipped in `desktop/src/state/library.ts`), designed as a
clean projection of the eventual hub entity:

```
Reference { id, type(article|preprint|book|report|webpage|note), title,
            authors[], year, venue, doi, arxivId, url, pdfUrl, abstract,
            tldr, citationCount, source, externalId, tags[], collectionIds[],
            notes, bodyMarkdown, addedAt }
Collection { id, name }
```

`externalId` (Semantic Scholar `paperId`) is the dedupe key on import.
`source` records provenance (semantic-scholar | manual | paste). `bodyMarkdown`
holds captured reading content; `notes` holds the reader's own annotations —
kept separate so notes survive a re-fetch of the content.

### 2.1 Mapping onto the hub ownership law

The [blueprint](../spine/blueprint.md) data-ownership law: **the hub owns names +
events (metadata); hosts own bytes.** A reference library splits cleanly along it:

- **Metadata → hub.** A `Reference` row (title/authors/DOI/tags/collection
  membership/notes) is small structured metadata — a new hub entity beside
  `Document`, with REST + MCP surfaces so **agents** can read the library, add
  findings, and cite. Collections are a lightweight grouping (like tags), not a
  new ownership boundary.
- **PDF bytes → blobs.** An attached PDF is content-addressed bytes → the
  existing blob store (`GET /v1/blobs/{sha}`, already used for run images and
  reachable from desktop via `hub_request_bytes`). The `Reference` holds the
  `blob_sha`, never the bytes.
- **Continuity.** Because metadata is hub-side, the same library appears on the
  phone (glance/triage) and the desktop (deep read) — the mobile↔desktop
  continuum, which a purely local (Zotero-desktop-style) store would break.

This is why round-1's device-local store is an **interim**, not the destination:
the model is already hub-shaped, so promotion is a sync layer, not a rewrite.

## 3. Discovery integration (shipped round 1)

`desktop/src/discovery/semanticScholar.ts` calls the **Semantic Scholar Graph
API** (`/paper/search`, keyless) through the existing **`hub_request` Rust proxy**
(`src-tauri/src/lib.rs:48`) — the same CORS-free reqwest path the hub SDK uses, so
**no new Rust code and no API key**. Fields fetched: `title, abstract, year,
venue, authors, externalIds(DOI/ArXiv), tldr, citationCount, openAccessPdf, url`.
The Discover panel renders results with the TLDR (Semantic Scholar's signature)
and imports a paper into the library in one click, deduped by `paperId`.

**Next discovery steps:** add-by-DOI/arXiv resolver (paper lookup endpoint);
Crossref fallback; citation/reference graph expansion ("find papers that cite
this"); Undermind-style deep recursive search as an agent job.

## 4. Sequencing

1. **Round 1 — shipped** (`ReadSurface.tsx`, device-local): three-pane library
   (collections/tags rail · items list · inspector Info/Read/Notes/Cite) +
   Semantic Scholar discovery + import + citation export (APA + BibTeX).
2. **Hub `Reference` entity** — **hub side shipped** ([ADR-053](../decisions/053-hub-reference-library-entity.md)):
   `reference_items` migration + REST (`/v1/teams/{team}/references`) + five
   `reference_*` MCP tools (agents can read/create/update/delete). **Still open:**
   sync the desktop's device-local library up to the hub, and Flutter mobile
   parity.
3. **PDF attachments** — upload a PDF → blob; `hub_request_bytes` to read;
   EMBED a PDF.js / Semantic Reader pane in the Read tab (replaces paste).
4. **Agent extraction (Elicit pattern)** — a steward task "extract {columns}
   across this collection" → writes a structured table deliverable.
5. **Deep recall (Undermind pattern)** — an agent mode that recursively searches
   and writes new references back into a collection.

## 5. Open questions

1. **Item-type schema depth.** Zotero has ~35 item types; we ship 6. Which extra
   types (thesis, dataset, software, patent) earn their metadata fields?
2. **De-dupe across sources.** `paperId` covers Semantic Scholar; DOI is the
   cross-source key — normalize on import (lowercase, strip `https://doi.org/`).
3. **Notes model.** One notes blob per reference now; do we want multiple
   timestamped annotations (Zotero child notes) linked to PDF locations?
4. **Cite styles.** APA + BibTeX shipped; is CSL (thousands of styles) worth
   pulling in, or are a handful hand-written formatters enough?
5. **Sync conflict policy** when the same library is edited on phone + desktop
   offline — last-write-wins on the hub, or per-field merge?

## Related

- [research-tooling-landscape.md](research-tooling-landscape.md) §3.1 — the
  literature-tooling register (Semantic Scholar INTEGRATE, Zotero INTEGRATE,
  Semantic Reader EMBED) this doc realizes.
- [desktop-workbench-jobs.md](../plans/desktop-workbench-jobs.md) — J1's place in
  the workbench; this is its deepening design.
- [research-reading-and-ideation-ui.md](research-reading-and-ideation-ui.md) — the
  grounded-dialogue + incubation-notes content model the notes/canvas side grows
  into.
- [spine/blueprint.md](../spine/blueprint.md) — the data-ownership law the
  hub-backed library obeys.
