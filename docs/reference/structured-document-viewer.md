# Structured Document Viewer

> **Type:** reference
> **Status:** Draft (2026-05-05) — viewer not yet shipped; pending plan + ADR
> **Audience:** contributors (mobile)
> **Last verified vs code:** v1.0.351

**TL;DR.** Mobile-side viewer specification for **typed structured
documents** (D7 in
[`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)).
A document with `kind != null` and `schema_id != null` resolves a
template-declared section schema and renders as a section-aware screen
with: an index of sections (state pips + titles + snippets) and a
focused per-section detail screen (markdown body + actions). Plain
markdown documents (`kind = null`) bypass this viewer and continue
using the existing flutter_markdown rendering. The viewer is the
chassis primitive consumed by the Structured Deliverable Viewer (A5)
when a deliverable has a `kind=document` component, and is also
reachable standalone for free-floating typed docs. Three section
states (`empty / draft / ratified` per 2026-05-05 §B.2) drive visual
encoding throughout.

---

## 1. Why this reference / scope

The discussion locks the *concept* of section-targeted authoring; A1
defines the data; A2 defines section schema declarations; A3 defines
the wire protocol. This file pins the **mobile UX** so an engineer
can build the viewer from a screenshot's worth of detail.

**In scope:**
- When the viewer renders vs falls back
- Schema resolution (kind + schema_id → template section schema)
- Two-screen layout: section index + section detail
- State pips, sibling navigation, action affordances
- Section-targeted steward session entry
- Direct manual edit with optimistic concurrency
- Ratification action with role gating
- Plain-markdown fallback path
- Empty / loading / error states
- Cache strategy + offline behavior

**Out of scope:**
- The wrapping Structured Deliverable Viewer — see
  `structured-deliverable-viewer.md` (TBD; A5).
- Document edit history / version diff — post-MVP.
- Multi-author concurrent editing UI — post-MVP.
- Document creation flow — handled by deliverable hydration on phase
  entry, or by manual creation under existing document endpoints.

---

## 2. When this viewer is used

```
                      Document fetched
                            ↓
                  has kind != null
                  AND schema_id != null
                  AND schema resolves?
                ┌──────┴───────┐
              yes               no
               ↓                 ↓
   Structured Document    Plain markdown
   Viewer (this spec)      viewer (existing)
```

**Resolution:** the viewer reads the document's `schema_id` and looks
up the template's `section_schemas[<schema_id>]` block (per A2 §7).
Templates are loaded via `GET /v1/teams/{team}/project-templates`
(A3 §8) and cached in `HubSnapshotCache`.

**Schema-not-found degradation:** if `schema_id` doesn't resolve (template
removed, version mismatch, network unavailable + cache cold), the
viewer falls back to **plain-markdown rendering of the section bodies
concatenated with H2 headers**, plus a warning banner ("This document's
schema is unavailable; showing all sections as plain markdown"). The
director can still read; they just lose section navigation +
state-aware actions.

---

## 3. Layout overview

Two screens pushed in sequence on the navigation stack:

```
┌─────────────────────────────────────────┐
│   Section Index                         │
│   ────────────────────────────────────  │
│   AppBar: doc title + kind chip         │
│            [edit doc] [overflow ⋮]      │
│   ────────────────────────────────────  │
│   Header: progress bar (N/M ratified)   │
│           + open-attention chip          │
│   ────────────────────────────────────  │
│   Section list (vertical):              │
│     [pip] Motivation                    │
│           "Why does this project…"      │
│           ratified · 2h ago             │
│     [pip] Method                        │
│           "Sketch of the approach…"     │
│           draft · 5m ago                │
│     [pip] Risks   (required)            │
│           empty · author with steward   │
│   ────────────────────────────────────  │
│   FAB: "Direct steward on document"     │
└─────────────────────────────────────────┘

           tap a section ↓

┌─────────────────────────────────────────┐
│   Section Detail                        │
│   ────────────────────────────────────  │
│   AppBar: ‹ back · Method · [pip]       │
│           [overflow ⋮]                  │
│   ────────────────────────────────────  │
│   Optional: guidance banner (collapsed) │
│   ────────────────────────────────────  │
│   Markdown body (scrollable)            │
│   ────────────────────────────────────  │
│   Action bar (sticky bottom):           │
│     [Direct steward]  [Edit]  [Ratify]  │
│   ────────────────────────────────────  │
│   Sibling pager dots: ● ○ ○ ○ ○         │
│   Swipe left/right → prev/next section  │
└─────────────────────────────────────────┘
```

---

## 4. Section index screen

### 4.1 AppBar

- Title: document title (from the document, not the schema). Falls
  back to schema's `display_name` if document has none.
- Kind chip: small monospaced label of `document.kind` (e.g.,
  `proposal`).
- Trailing actions:
  - Edit doc icon — opens metadata edit sheet (title, description,
    not section bodies).
  - Overflow menu:
    - Export as PDF (post-MVP)
    - View edit history (filtered Activity)
    - Show as plain markdown (debug toggle)
    - Document settings (admin)

### 4.2 Header strip

A compact progress + signals line below the AppBar:

```
[━━━━━━━━━━━━░░░░░] 4/6 ratified           ⚠ 2 reviews
```

- Progress bar: `ratified / total` (only `required` sections counted
  for the bar; optional sections shown as a smaller hash-tick).
- Open attention chip: shows count of `attention_items` referencing
  this document (e.g., a ratify-prompt for a metric criterion bound
  to this doc).

### 4.3 Section list

One row per section in **template-declared order**. Sections present
in the doc body but absent from the schema render at the bottom under
a "Deprecated sections" subhead with a warning glyph (gracefully
shown but not editable; admin removes them).

Each row:

```
┌──────────────────────────────────────────────────┐
│  ●ratified   Motivation                          │
│              "Termipod's lifecycle work targets…" │
│              ratified · 2h ago · @director       │
└──────────────────────────────────────────────────┘
```

Components:
- **State pip** — colored circle + text, see §6 for color spec.
- **Title** — from schema's `title`.
- **Snippet** — first 1–2 lines of body, plain text (markdown stripped),
  ellipsis-truncated. For `empty` sections: the schema's `guidance`
  text in muted italic.
- **Footer line** — `state · last_authored_at relative · author`.
  Author = `last_authored_by_session_id`'s actor (resolved client-side
  from cache).

Required sections show a small "(required)" tag in muted text after
the title.

Tap → push the section detail screen (§5).

Long-press → context menu: Direct steward, Mark ratified (gated),
View history, Copy link.

### 4.4 FAB — "Direct steward on document"

Opens a non-section-targeted steward session for the whole document
("review the proposal"). Uses POST `/documents/{id}/distill` with no
section target. Useful when the director wants to discuss
cross-section concerns ("are Method and Risks consistent?").

If the project's `phase_state.steward_state` is `not-spawned`, the FAB
is replaced by "Start steward" (which spawns then opens).

---

## 5. Section detail screen

### 5.1 AppBar

- Back affordance (system back).
- Title: section title.
- Trailing: state pip + overflow.
- Overflow menu:
  - Direct steward on this section
  - Edit body (manual)
  - Mark ratified / Unratify (gated)
  - View section history (filtered Activity)
  - Copy section deeplink
  - Show schema guidance

### 5.2 Optional guidance banner

If the schema declares `guidance` for this section, a collapsible
banner appears above the body:

```
ℹ Why does this project matter? What outcome does success enable?
                                                          [▾ collapse]
```

Default: collapsed if section state is `ratified` (the director knows
what's there); expanded if `empty` or `draft`.

### 5.3 Markdown body

Full-screen scrollable markdown view rendered with the existing
flutter_markdown + flutter_highlight pipeline (per the
chat-polish work in v1.0.299). For `empty` sections, render a
placeholder card:

```
This section hasn't been authored yet.

[Direct steward to draft]   [Write manually]
```

### 5.4 Action bar (sticky bottom)

Three primary actions:

| Button | Visibility | Auth | Behavior |
|---|---|---|---|
| **Direct steward** | always when steward state ≠ `not-spawned` | director or project-steward | POST `/documents/{id}/sections/{slug}/distill`; navigate to opened session |
| **Edit** | always | director, project-steward | Open in-app markdown editor with `body`; on save, PATCH `/documents/{id}/sections/{slug}` with `expected_last_authored_at` |
| **Ratify** / **Unratify** | when state allows | director only | POST `/documents/{id}/sections/{slug}/status` |

Action bar adapts when state-gated:

- `empty` → only Direct steward + Edit shown; Ratify hidden (can't
  ratify empty content).
- `draft` → all three shown; Ratify is the primary CTA (filled).
- `ratified` → Direct steward + Edit visible (rework path); Ratify
  becomes Unratify (admin gesture, confirm dialog).

For non-director actors, Ratify/Unratify is always hidden.

### 5.5 Sibling pager

Five-dot indicator at bottom (or arrows for >5 sections). Horizontal
swipe between sections. Swipe is the primary cross-section
navigation; the index screen is for overview/jump. Pre-fetch the next
section when the current one mounts.

### 5.6 Edit-mode behavior

Tapping Edit opens an in-app markdown editor (text-area with simple
toolbar: H1/H2/H3, list, link, code block — not a rich editor). On
save:

1. PATCH `/documents/{id}/sections/{slug}` with body +
   `expected_last_authored_at` (from the loaded section).
2. On 412 (mismatch): show "This section was edited elsewhere — show
   diff?" with options to discard, force-overwrite, or merge.
3. On 200: refresh local cache, return to section detail screen.

Edits do NOT auto-promote state (`empty → draft → ratified` is a
separate action). After a save, state is the lower of `current` and
`draft` — i.e., a manual edit on a `ratified` section moves it back
to `draft` (asks director "this will require re-ratification, ok?"
confirm dialog).

---

## 6. State pip visual encoding

Three states with consistent visual encoding across both screens.

| State | Pip glyph | Color | Label |
|---|---|---|---|
| `empty` | ○ open circle | `DesignColors.textMuted` | "empty" |
| `draft` | ◐ half-filled circle | `DesignColors.warning` (amber) | "draft" |
| `ratified` | ● filled circle | `DesignColors.terminalGreen` (success green) | "ratified" |

Color values match existing design tokens — no new tokens introduced.
Text style: `GoogleFonts.jetBrainsMono(fontSize: 10, fontWeight: w700)`
matching the existing `_Chip` pattern in
`overview_widgets/portfolio_header.dart`.

---

## 7. Section-targeted session entry

Tap "Direct steward" (action bar or context menu) at section detail
→ POST `/v1/teams/{team}/documents/{document_id}/sections/{slug}/distill`
(A3 §7.4). Server returns `session_id` + `open_url`.

Mobile navigates to the opened session screen (existing
`open_steward_session.dart` plumbing). When the session distills, the
section's `body` updates and `last_authored_*` fields stamp; mobile
re-fetches via cache invalidation triggered by the `agent_session.distilled`
audit event.

The session UI (existing) gains a small **section-target chip** in
its AppBar showing "Method · Proposal" so the user remembers what
they're working on. Tap chip → returns to section detail.

---

## 8. Plain-markdown fallback

When `document.kind == null` (or `schema_id == null`, or schema
resolution fails), the viewer is bypassed entirely:

- Mobile pushes the existing plain-markdown viewer
  (`doc_viewer_screen.dart`) instead of this one.
- All existing behavior preserved.
- This is the path for free-floating notes, legacy docs, etc.

**Failed schema resolution** (kind set but schema not found) shows a
warning banner above the plain-markdown render:

```
⚠ This document references schema `research-proposal-v1` which is
unavailable. Showing as plain markdown. Section-aware editing is
disabled.
```

Direct-steward / edit / ratify actions hidden; the document is
read-only until the schema resolves.

---

## 9. Empty / loading / error states

### 9.1 Loading

Use existing skeleton-loading pattern (matches Overview hero loading):
shimmer placeholder for the section list rows. Header strip shows
indeterminate progress bar.

### 9.2 Error (document fetch failed)

Standard error card with retry button (matches `_ErrorView` pattern in
`projects_screen.dart`).

### 9.3 Schema cache cold (offline first-load)

If templates haven't been cached and the device is offline, fall back
to plain-markdown rendering with a banner explaining "schema
unavailable; reconnect to enable section-aware view".

### 9.4 Document with empty `sections: []`

Render an empty-state card per the deliverable's template:

```
This document has no sections yet.

[Direct steward to author]
```

This case occurs when a typed document is created (deliverable
hydration on phase entry) but no sections have been instantiated yet.

---

## 10. Caching + offline

### 10.1 Cache table

`HubSnapshotCache` gains `cache_documents_typed`:

```sql
CREATE TABLE cache_documents_typed (
  document_id TEXT PRIMARY KEY,
  team_id     TEXT NOT NULL,
  project_id  TEXT,
  kind        TEXT,
  schema_id   TEXT,
  body_json   TEXT NOT NULL,           -- the {sections:[...]} blob
  etag        TEXT,
  fetched_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL
);
```

TTL: 5 minutes (matches A3 §2.8 max-age=300 for relatively stable
resources). Cache-first per ADR-006 — render from cache, revalidate
in background.

### 10.2 Section schema cache

Templates already cached in `cache_project_templates` (A3 §11). The
viewer reads `section_schemas[document.schema_id]` from that cache.

### 10.3 Offline mutations

If the user edits or ratifies offline:

- **Manual edit** → queue PATCH; show "queued, will sync" indicator.
  On reconnect, attempt with the cached `expected_last_authored_at`.
  On 412, surface conflict resolution UI.
- **Ratify** → queue POST status. Locally apply optimistic state
  change; mark as "pending sync" with a small clock icon next to the
  pip. On success, clear pending. On failure (auth or precondition),
  revert and show error toast.
- **Direct steward** → requires connectivity (session opens server-
  side); show "Connect to direct steward" if offline.

Queue is per-device, persisted in SharedPreferences with a TTL of 24h
to bound failure-mode complexity.

---

## 11. Composition with Structured Deliverable Viewer

When this viewer is reached *via* a deliverable (the common path for
phase deliverables), the deliverable viewer (A5) routes here when the
component's `kind=document` is tapped:

```
Project Detail → Phase ribbon → Deliverable Viewer (A5)
                                  → tap document component
                                    → Structured Document Viewer (this spec)
                                      → tap section
                                        → Section Detail
```

The viewer is **navigation-stack-pushed** — back button returns to the
deliverable viewer. The deliverable viewer's "Ratify deliverable"
action is *not* mirrored here; this viewer ratifies sections only.
Whole-deliverable ratification stays in the wrapper.

When standalone (free-floating typed doc not bound to a deliverable),
back returns to wherever the user came from (Project Detail's
Documents tile, Activity feed, search result, etc.).

---

## 12. Interaction with the steward strip

The Project Detail's steward strip (W3, §6.6 of the discussion)
remains visible *behind* this viewer when reached via Project Detail
navigation. Its state updates push through normally. When a
section-targeted session is open, the strip displays:

```
🤖 Project steward · drafting Method (Proposal)
```

— matching the §10.1 `current_action` field from the steward state
endpoint. Tapping the strip opens the session's screen (resume).

---

## 13. Accessibility + RTL

- All pips have text labels alongside (no color-only signaling).
- Sibling-pager swipe direction reverses in RTL locales.
- Markdown body inherits the system text size; honor user-set scale
  factors.
- VoiceOver/TalkBack announces section state alongside title:
  "Method, draft, last edited 5 minutes ago."

---

## 14. Validation rules (mobile-side)

The mobile client validates the document body before rendering:

1. **Body parses as JSON.** If not, fall back to plain-markdown.
2. **`schema_version` ≤ client's max supported.** If newer, show a
   "this document was authored with a newer app version, update to
   view" banner.
3. **`schema_id` resolves to a cached template.** If not, fallback per
   §8 with banner.
4. **Section slugs match schema slugs at least partially.** Sections
   in body but not in schema render under "Deprecated sections"
   subhead (§4.3). Sections in schema but not in body render as
   `empty` placeholders.
5. **Section state values are in {empty, draft, ratified}.** Unknown
   values default to `draft` with a console warning.

---

## 15. Open follow-ups

1. **Markdown editor sophistication.** MVP ships a basic toolbar.
   Rich-editor (block-based, like Notion) is post-MVP.
2. **Inline section comments / redlines.** Future: tap a paragraph,
   add a comment that the steward sees in its session context. Out
   of MVP scope.
3. **Section diff view.** When unratifying or merging conflicting
   edits, show a side-by-side diff. Reuses the existing
   syntax-highlighted diff pipeline from chat polish v1.0.299.
4. **Concurrent multi-author live editing.** Multi-user (F-1)
   territory; post-MVP.
5. **PDF export.** Compose all sections into a single document and
   export. Useful for sharing a ratified proposal externally. Post-MVP.
6. **Section reordering.** Currently template-fixed. Allowing the
   author to reorder violates schema-stability (A2 §7); deferred.
7. **Inline media upload to a section.** Today images/files attach to
   the document at the document level; section-level attachments need
   a small schema extension. Post-MVP.

---

## 16. Cross-references

- [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  — D7 (sections as primitive) + D10 (overall lifecycle context)
- [`reference/project-phase-schema.md`](project-phase-schema.md) §3.2
  — `documents.body` JSON shape
- [`reference/template-yaml-schema.md`](template-yaml-schema.md) §7
  — `section_schemas:` declarations
- [`reference/hub-api-deliverables.md`](hub-api-deliverables.md) §7
  — typed-doc endpoints (get with sections, PATCH section, set state,
  distill)
- [`decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md)
  — cache-first rendering
- `reference/structured-deliverable-viewer.md` (TBD; A5) — wrapper
  viewer
- `reference/research-template-spec.md` (TBD; A6) — templates this
  viewer renders for the demo
