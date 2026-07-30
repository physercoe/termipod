# Citation bridge — closing the library→Author loop (J1→J2)

> **Type:** plan
> **Status:** Proposed (2026-07-30) — wedges C1–C5; C1 (cite core + exports)
> is useful standalone and unblocks everything else. Implements
> `desktop-design-review.md` §2.1 ("the single highest-value missing
> feature") with the landscape's amendments (`research-app-product-
> landscape.md` §3.4/§9.2), plus the two adjacent J1 gaps from the same
> review: quote-based annotation anchoring (§2.4) and discovery fan-out
> (§2.2).
> **Audience:** principal · contributors
> **Last verified vs code:** 2026.727.206-alpha (origin/main `de201ca9`)

**TL;DR.** A year after the design review named it, the desktop still has no
path from the library to a citation in a draft: zero citation code exists, and
the "one app for research" loop breaks exactly at writing. The pieces are
unusually ready: `Reference` (`desktop/src/state/library.ts:49`) is ~90% of a
CSL-JSON item, the Author editor is CodeMirror 6, references and annotations
already sync to hub tables with an agent-facing annotation MCP surface, and
ADR-062's UIRef makes "the paper I'm reading" agent-addressable. The bridge:
**CSL-JSON as the internal citation format**, stable human-legible cite keys
minted per reference, an `@`-trigger insert command writing pandoc `[@key]`
into markdown, live inline rendering + auto-bibliography in preview,
continuous `.bib`/CSL-JSON export per workspace (the Paperpile pattern, which
also feeds the future Typst path via BibLaTeX — hayagriva does not read
CSL-JSON), and the same verbs exposed to agents so a drafting agent cites
*checkably* instead of hallucinating references. Alongside: W3C text-quote
selectors so annotations survive arXiv v1→v2, and search-time fan-out/merge
so discovery stops being a per-source query tool.

## 1. Context and grounding

- **Reference shape** (`library.ts:49`): title/authors/year/venue/doi/
  arxivId/url + a `details` record already holding the CSL long tail
  (volume, issue, pages, publisher, ISSN…). `type: RefType` maps onto CSL
  item types. Dedup keys (doi→arxivId→title+year) exist at import time.
- **Editor**: `ui/MarkdownEditor.tsx` on CodeMirror 6 (`@codemirror/lang-
  markdown`, `view`, `state`, `search`, `merge` are already dependencies;
  `@codemirror/autocomplete` is **not** — C2 adds it). Preview renders via
  `ui/Markdown.tsx`; AuthorSurface saves to workspace files
  (`state/workspaceFiles.ts`).
- **Annotations** (`state/annotations.ts:42`): geometry anchors
  (page + PDF-point rects) with the selected `text` already captured for
  highlights — the quote selector is half-collected today. Hub sync exists
  (`reference_annotations`, agent-facing via
  `hub/internal/server/mcp_reference_annotations.go`).
- **Discovery** (`discovery/index.ts:17`): a `SOURCES` registry queried one
  source at a time from ReadSurface; enrichment (scrape/Unpaywall) exists.
- **Chosen formats** (review §2.1 + landscape §3.4/§9.2): pandoc `[@key]`
  syntax (keeps the Quarto/Typst/MyST export paths clean — MyST uses the
  same syntax), CSL-JSON internally, BibLaTeX at the Typst boundary.

## 2. Goals / non-goals

**Goals**

1. Insert a citation from the library into a markdown draft in ≤2 keystrokes
   past `@`, with fuzzy search over title/author/year/key.
2. Preview renders `[@key]` as a formatted inline cite and appends a
   References section of cited items; unresolved keys render as visible
   warnings (the hallucinated-citation guard).
3. Deterministic exports: per-document cited-only `.bib` and CSL-JSON;
   per-workspace continuous `references.bib` regeneration.
4. Agents get the same verbs (search/get/cite-key over the library) so
   `[@key]` written by an agent resolves against the same registry.
5. Annotations gain quote+position selectors and orphan-preserving
   re-anchoring; geometry stays the fast path.
6. Discovery searches 2–3 keyless sources concurrently and merges on strong
   keys with prefer-richest-field.

**Non-goals**

- No in-app styled rendering of all 2,600 CSL styles in v1 (see 3.2 and open
  question 1 — the styling engine is the one genuinely open decision).
- No Typst compile pipeline (separate plan when J2 export matures); this plan
  only guarantees its bibliography input exists (BibLaTeX).
- No block IDs/transclusion (landscape §3.1) — compatible, later.
- No Zotero live-sync (landscape §11.2) — the import path exists; live-sync
  is its own integration.

## 3. Design

### 3.1 Cite core (C1) — `desktop/src/cite/`

- `toCSL(ref: Reference): CSLItem` — pure mapping, `details` fields promoted
  by a fixed table, RefType→CSL type table, authors split family/given with
  a literal-name fallback. The inverse is not needed (CSL-JSON is derived,
  never canonical — the library row stays the source of truth).
- **Key minting**: `familyYearFirstContentWord` (e.g. `vaswani2017attention`),
  ASCII-folded, collision-suffixed `a/b/c` in mint order. Minted lazily on
  first cite, stored on the Reference (`citeKey?: string`) and synced like
  any field — **stable once minted** (keys are in users' documents; a rename
  never cascades). Manual override allowed with uniqueness enforced.
- **Serializers**: CSL-JSON array; BibLaTeX writer (field mapping incl.
  `eprint`/`eprinttype` for arXiv, `doi`, `urldate`) — this single writer is
  both the user-facing `.bib` export and the future hayagriva input
  (landscape §9.2's amendment).
- **Doc scan**: `citedKeys(md: string): string[]` — pandoc citation syntax
  (`[@a; @b]`, bare `@a`, suppressed-author `[-@a]`), fenced-code-aware.
  Pure, heavily tested.

### 3.2 Rendering (C1 minimal, C2 full)

v1 preview ships **two built-in styles** — author-date (APA-shaped) and
numeric — implemented directly over CSL-JSON (~200 lines, zero new
dependencies): inline `(Vaswani et al., 2017)` / `[3]` plus a References
section in reading order (numeric) or alphabetical (author-date), each entry
linking back to the library item (open in Read surface). Full CSL via an
embedded processor is **deliberately an open question** (q1): citeproc-js is
the standard but is AGPL/CPAL-dual-licensed — a licensing call the built-in
styles let us defer without blocking the loop. The style choice lives in doc
frontmatter (`csl: author-date|numeric`), defaulting to author-date.

### 3.3 Editor integration (C2)

- Add `@codemirror/autocomplete`; an `@`-trigger completion source (active
  in markdown text, not in code fences) fuzzy-matches the library and
  inserts `[@key]`; a `/cite` palette command does the same for
  discoverability.
- Hover tooltip on `[@key]`: title · authors · year · venue, with "open in
  library". Unknown keys get a squiggle via `@codemirror/lint`-style
  decoration (dependency already implied by the merge/search stack; if not,
  a plain mark decoration suffices).
- Preview (`Markdown.tsx`): resolve citations through the cite core; the
  References section is generated at render time, never written into the
  file (the file keeps only `[@key]` — greppable, git-stable, agent-legible).

### 3.4 Workspace export + agent verbs (C3)

- **Continuous export** (Paperpile's shape): a workspace setting enables
  `references.bib` + `references.csl.json` regeneration (cited-only across
  workspace docs, or whole-collection by choice) on library/doc change,
  debounced, deterministic ordering (by key) so diffs are reviewable.
- **Agent verbs** (hub MCP, beside `mcp_reference_annotations.go`): audit
  what exists, then fill to: `reference_search(query)`,
  `reference_get(id)`, `reference_cite(id) → {key, csl}` (mints exactly as
  the UI does — one registry, two consumers). With ADR-062, the read-surface
  UIRef `{reference_id}` joins here: "cite what the user is reading" is
  `ui_get_focus` → `reference_cite`. A drafting agent's `[@key]` then
  resolves in preview like anyone else's — and a Paperpile-style "citation
  check" (flag keys whose metadata doesn't support the claim) becomes a
  natural later agent task, because keys are checkable ids, not strings.

### 3.5 Annotation anchoring (C4)

- `AnnotationPosition` gains optional sibling selectors (W3C Web Annotation
  model — the spec blesses redundancy): `quote?: { exact, prefix, suffix }`
  (~32-char affixes) and `textPos?: { start, end }` over a document-global
  string built from pdf.js `getTextContent()` — captured at creation time
  (highlights already store `exact` as `text` today).
- **Normalization is the correctness core**: match on NFKD +
  whitespace-stripped text with an offset map back to raw positions —
  pdf.js text extraction shifts across versions and silently breaks naive
  offsets (landscape §1.2).
- Render geometry-first (unchanged, fast). Re-anchor via quote search
  (vendor `approx-string-match`, MIT) when geometry misses — concretely:
  attachment bytes changed (sha differs) or the rect's page text no longer
  contains the quote. Failures become **orphans**: kept, badged in the
  annotation list, retried on next open — never deleted.
- Hub sync: the selectors ride the existing annotation sync as new optional
  fields; old rows simply lack them (grandfathered, re-captured on next
  edit).

### 3.6 Discovery fan-out (C5)

`searchAll(query)`: fan out to the keyless sources concurrently (OpenAlex,
Crossref, Semantic Scholar), merge on the existing strong-key ladder
(doi→arxivId→title+year — lifted from import-time dedup into a shared
module), prefer-richest-field on conflicts (S2 `tldr`, OpenAlex topics,
Crossref authoritative metadata, Unpaywall OA PDF via the existing
enrichment). UI: one result list with per-source chips; the single-source
picker remains as an "advanced" filter. Respect per-source rate limits with
a small concurrency gate; partial failures degrade to the sources that
answered (never block the list on the slowest API).

## 4. Wedges

- **C1 — cite core + exports**: `cite/` module (mapping, keys, serializers,
  doc scan), built-in author-date/numeric renderers, per-document `.bib` /
  CSL-JSON export from the Author surface. No editor changes yet; already
  useful (export a draft's bibliography today).
- **C2 — editor loop**: `@`-completion, hover cards, unresolved-key
  warnings, preview inline cites + References section, `/cite` command.
- **C3 — workspace export + agent verbs**: continuous `references.bib`,
  MCP tools, UIRef→cite join.
- **C4 — anchoring**: selectors + capture + re-anchor + orphans (independent
  of C1–C3; can run in parallel).
- **C5 — discovery fan-out** (independent; smallest).

## 5. Testing

Pure-function coverage is the point of this design (`node --test`, manual —
CI does not run desktop state tests):

- CSL mapping: every RefType; `details` promotion; author parsing incl.
  literal names; round-trip stability (same input → identical CSL-JSON).
- Keys: mint determinism, ASCII folding, collision suffixing, stability
  under reference edits, manual-override uniqueness.
- Doc scan: `[@a; @b]`, bare `@a`, `[-@a]`, keys inside code fences ignored,
  punctuation-adjacent keys.
- BibLaTeX writer: escaping, arXiv eprint mapping, deterministic order —
  golden-file tests.
- Anchoring: fixture pair of "v1/v2" extracted text (shifted offsets,
  re-typeset whitespace) — quote re-anchors within scoring bounds; NFKD
  offset-map round-trip; orphan on genuine deletion; geometry untouched when
  sha unchanged.
- Merge: same paper from three sources unifies; richest-field wins;
  title+year fallback does not over-merge distinct papers (regression corpus
  from real library rows).

## 6. Risks

- **Key stability vs. metadata fixes**: minting from possibly-wrong metadata
  (scraped year off by one) bakes the error into the key. Mitigation: keys
  are opaque-but-legible identifiers, not claims — never re-mint on metadata
  edit; the rendered citation shows corrected metadata.
- **CSL styling expectations**: users will eventually ask for venue-exact
  styles; the built-in pair must not grow ad hoc — that pressure is the
  trigger to resolve open question 1, not to hand-roll a third style.
- **pdf.js version drift** breaking text extraction offsets — pinned by the
  fixture tests above; treat a pdf.js upgrade that shifts fixtures as a
  re-anchoring event, not a test to update blindly.
- **API etiquette in fan-out**: three sources per keystroke is abusive —
  debounce, search-on-enter, and per-source caching are part of C5, not
  polish.

## 7. Open questions

1. **CSL engine**: embed citeproc-js (complete, but AGPL/CPAL dual license —
   CPAL's attribution requirement needs a deliberate call for a distributed
   app), embed citation-js (MIT packaging around the same engine — verify
   what it actually vendors), or keep built-ins until Typst export lands and
   let **hayagriva** (MIT/Apache, reads real `.csl`) be the styled path at
   the export boundary? Lean: built-ins now, hayagriva at export, no GPL-ish
   JS in the bundle — but this is the director's licensing call.
2. Should `citeKey` live on the hub `reference_items` row from day one (so
   mobile and agents see identical keys), or desktop-local until the sync
   3-way-merge work lands? Lean: hub row — keys are exactly the kind of
   field last-write-wins can tolerate.
3. Bibliography placement: auto-appended References section in preview only,
   or also an explicit `<!-- refs -->` marker for users who want it mid-doc?
   Lean: marker supported, auto-append as fallback.
