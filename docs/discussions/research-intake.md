# Research intake

> **Type:** discussion
> **Status:** Open (2026-07-15) — raised by the director after two adjacent gaps
> surfaced in the desktop research surface: (1) no Zotero-Connector-style
> browser capture, and (2) no RSS/subscription or topic/event monitor for new
> material. This doc argues they are one primitive — *research intake* — and
> proposes a first slice. Companion to
> [reference-library-and-reading.md](reference-library-and-reading.md) and
> [research-material-data-model.md](research-material-data-model.md).
> **Audience:** contributors · principal
> **Last verified vs code:** desktop-v0.3.52

**TL;DR.** Getting new material *into* the library is currently a manual,
one-item-at-a-time act. Two requests — a browser **connector** and
**feeds/monitors** — look like separate features but are the same primitive
seen from two ends: *research intake* = **new material arriving at the
library**, either **one-shot** (I found this, take it) or **standing** (watch
for this, tell me). The engine that turns a URL/DOI/query into a full library
item **already exists** (`src/discovery/` — an 8-source search + enrichment
layer feeding `state/library.ts`). What is missing is only the two *doorways*
into that engine: a capture channel from the browser, and a scheduler that runs
saved sources/queries and pushes what's new. Build them against **one** ingestion
target and **one** inbox, not as two disjoint features. The single real
architecture fork is **where the standing-poll loop runs** — client-open,
Rust-background, or hub-daemon — because only a hub-side loop catches events
while the desktop is closed and unifies with the mobile Activity surface.

---

## 1. Why now — two gaps, one shape

The desktop research surface (J1 Read,
[reference-library-and-reading.md](reference-library-and-reading.md)) can read,
enrich, and organise material well. But *acquisition* is the weak link:

- **Connector gap.** Zotero's Connector is a one-click "save this page to my
  library" from the browser. TermiPod has no equivalent — the director
  copy-pastes a URL or DOI, or imports from a Zotero export.
- **Standing-intake gap.** There is no RSS/feed subscription (Zotero *Feeds*)
  and no topic/event monitor (Google Scholar Alerts, PubMed saved searches,
  arXiv category listings). Nothing watches an interest and surfaces what's new.

These are the same act at two cadences. Both answer *"new material should end up
in my library without me hand-carrying each item."* Naming them as one primitive
— **intake** — means one data model, one inbox, one notification path, and two
thin source adapters, instead of two overlapping subsystems that drift.

```
                       ┌─────────────── research intake ───────────────┐
  one-shot  ──▶  connector (browser → app)  ──┐
                                              ├──▶  discovery/enrich  ──▶  library
  standing  ──▶  feed subscription (RSS/…)  ──┤        (already exists)      + inbox
            ──▶  topic/event monitor (query) ─┘                              + notify
```

## 2. What already exists (the engine)

Verified against `desktop/src/` at `desktop-v0.3.52`:

- **`src/discovery/`** — an 8-source layer: `arxiv`, `openAlex`, `pubmed`,
  `crossref`, `semanticScholar`, `core`, `unpaywall`, plus `scrape.ts`. All
  CORS-free through the Rust `hub_request` transport (`discovery/http.ts`).
- **`scrape.ts`** resolves a seed (DOI / arXiv id / OpenAlex id / title / URL)
  into a full `ScrapePatch` — bibliographic fields, PDF URL, OA status, citation
  graph — and is documented to build a **new** item from an identifier alone.
- **`state/library.ts` + `attachments.ts`** — the Reference/attachment model
  (ADR-053) the results land in; `librarySync.ts` carries the sync story.

So intake is **not** a new engine. It is two doorways into a running one. The
connector's hardest part (metadata extraction, PDF resolution) and the monitor's
hardest part (querying scholarly sources) are both already shipped and used on
demand today. Intake makes them *triggerable from outside* and *on a schedule*.

## 3. The connector (one-shot intake)

Zotero's Connector = a browser extension (MV3) + per-site translators + a POST
to the desktop app's localhost server (port 23119) + optional page snapshot.
We do not need to rebuild that stack; we need the doorway. Three tiers, by build
*and distribution* cost (store review, cross-browser upkeep):

| Tier | Mechanism | Reach | Cost |
|---|---|---|---|
| **C1** | In-app *Add by URL/DOI* field + a `javascript:` **bookmarklet** that hands the current page's URL/DOI to the app | scholarly pages (the 80% case) | ~1–2 days, no store |
| **C2** | Register a `termipod://save?url=…` **deep-link** scheme so the bookmarklet/extension launches straight into a capture | click → it's in TermiPod | small Rust add on C1 |
| **C3** | Full **MV3 extension + localhost capture server** (Zotero's exact model) | arbitrary-page snapshot, PDFs behind a logged-in session | separate build/distribution artifact + a new attack surface |

**Translators are out of scope.** Zotero's per-site translators are separately
licensed and would need a provenance/license check before reuse (per the repo's
"check OSS provenance first" rule). Our OpenAlex-first resolution sidesteps them:
resolve from the DOI/identifier rather than scraping each site's DOM. C3's
snapshot path is the only case that genuinely needs DOM capture, and that is the
extension's own capture, not a borrowed translator.

## 4. Feeds and monitors (standing intake)

Two source-types, deliberately distinct but sharing one record:

- **Feed** — you follow a *source*: an arXiv category, a journal, a lab's page,
  any RSS/Atom URL. Pull model → new items land in the inbox. (Zotero *Feeds*.)
  RSS/Atom is XML; the Rust core already parses XML for WebDAV/PubMed/arXiv, so
  a feed parser is a small add on the existing transport.
- **Monitor** — you save a *query* ("RAG + KV-cache", "new CVEs in tokio") run
  against the discovery APIs, filtered to *new since last check*. OpenAlex and
  PubMed already take a `from_date` filter, so "new works since I last looked"
  is a filter parameter, not new infrastructure. (Scholar Alerts / PubMed saved
  searches.)

Both reduce to one mechanism:

```
subscription { id, kind: feed | query, source, lastCheckedAt, seen: Set<id> }
        │
   scheduler ── runs each subscription on an interval
        │
   dedup vs seen ── only genuinely-new items survive
        │
     inbox ── a review surface: accept → library, or dismiss
        │
   notify ── OS notification / attention item
```

The `seen` set is load-bearing: without it every poll re-surfaces the same
items. Dedup by stable identifier (DOI / arXiv id / feed GUID), not by title.

## 5. The one real fork — where the standing-poll loop runs

A monitor only earns its keep if it catches events **while you are not looking**.
That makes *where the loop lives* the decision, not an implementation detail:

| Home | Fires when app closed? | Lands in | Cost / stance |
|---|---|---|---|
| **Client, app-open** (JS interval + catch-up query on launch) | ✗ | desktop inbox | smallest; pure TS; local-first (ADR-050-aligned) |
| **Rust background task** + OS notifications | ✗ (only while process alive) | desktop inbox | small; native notifications; window may be hidden |
| **Hub-side monitor** → `attention_items` | ✓ always-on daemon | **mobile Activity + desktop** | Go change; cross-device; cuts against desktop local-first |

Only the hub-side loop fires with the desktop closed and lights up *both*
clients — because the hub already owns `attention_items` and the mobile
**Activity** tab (see
[auto-notification-coverage.md](auto-notification-coverage.md)). But it lands in
Go and pulls against ADR-050's local-first posture. The connector (C1/C2) has no
such fork — it is inherently client-triggered.

**A promotion path avoids choosing wrong now.** Ship the loop client-side first;
the subscription record, inbox surface, and notification contract are identical
whether the loop ticks in JS, in Rust, or in the hub. Only the *scheduler* moves.
So the client-first slice is not throwaway — it is the hub slice minus the daemon.

## 6. Term/naming gap — pick before it spreads

Per glossary-first, the vocabulary here is collision-prone and must be settled in
[../reference/glossary.md](../reference/glossary.md) before it lands across
layers:

- **"feed"** already means the transcript live-feed (`ui/feedLens.ts`, LiveFeed).
- **"monitor"** overlaps a harness/tooling concept and "observability."
- **"watch" / "alert" / "subscription"** all overlap.

Candidate names for the umbrella primitive: **intake**, **follow**,
**subscription**. This doc uses *intake* for the umbrella and *feed* / *monitor*
for the two standing source-types provisionally — not a decision. The glossary
entry is a precondition of the first PR, not a follow-up.

## 7. Relationship to existing docs

- **Ingestion target** — intake feeds the Reference/element model in
  [research-material-data-model.md](research-material-data-model.md) and the J1
  library in [reference-library-and-reading.md](reference-library-and-reading.md).
  Design the target once; connector and feeds/monitors both write to it.
- **Notifications** — the standing path is a new producer for the primitives
  catalogued in [auto-notification-coverage.md](auto-notification-coverage.md)
  (`attention_items`), if promoted hub-side.
- **Delivery model** — client-first vs hub-side is an application of the
  local-first stance in [ADR-050](../decisions/050-desktop-workbench-delivery-model.md)
  and the derivation in [desktop-research-surface.md](desktop-research-surface.md).

## 8. Open questions / forks

1. **Loop home** (§5) — client-open, Rust-background, or hub-daemon? Recommend
   **client-first with a hub promotion path**.
2. **Connector tier** (§3) — C1 bookmarklet first, or commit to C3's extension?
   Recommend **C1 → C2**, treat C3 as a later, security-gated wedge.
3. **Naming** (§6) — glossary entry for the umbrella + source-types before code.
4. **Inbox placement** — a new surface, or a filtered view of the existing
   library? Leaning: a *state* on library items (`unreviewed`) + a filter, not a
   separate store, so accepted items are already in place.
5. **Security** — any localhost server or `termipod://` handler is untrusted
   input; it belongs with the items in
   [desktop-design-review.md](desktop-design-review.md) (origin/token check from
   day one), and gates C3 in particular.

## 9. Proposed first slice

Local-first, all TS + a thin Rust feed-parser, reusing the discovery engine:

1. A `subscription` store (`kind: feed | query`, source, `lastCheckedAt`, seen-set).
2. **Add by URL/DOI** field + a bookmarklet (**C1**) — the one-shot doorway.
3. A client-side scheduler + catch-up-on-launch query for the standing doorway.
4. An **inbox** = an `unreviewed` state + filter on the existing library, with
   accept/dismiss.
5. OS notifications via Tauri; leave the `attention_items`/hub promotion to a
   follow-up once the workflow is validated.

Resolve into an ADR (the loop-home + naming decisions) and a plan entry once the
director picks the forks in §8.
