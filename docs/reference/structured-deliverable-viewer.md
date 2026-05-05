# Structured Deliverable Viewer

> **Type:** reference
> **Status:** Draft (2026-05-05) — viewer not yet shipped; pending plan + ADR
> **Audience:** contributors (mobile)
> **Last verified vs code:** v1.0.351

**TL;DR.** Mobile-side viewer for the **Deliverable** chassis primitive
(D8 in
[`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)).
Composes a Components panel (which can host
[`structured-document-viewer.md`](structured-document-viewer.md) for
document components plus reference cards for artifact / run / commit
components) with an Acceptance Criteria panel (D9) and a sticky Action
Bar (Direct steward, Ratify deliverable). Reached via the Project
Detail phase ribbon: tapping the current phase opens this viewer in
**active mode**; tapping a past phase opens the phase's
ratified-snapshot (read-only). Phases with 0..N deliverables (per A2
§5) use a phase-summary intermediate screen when N > 1; common case
(N == 1) opens the viewer directly. Ratification authority gates per
template (D8) — `director` is the MVP path; `auto` is chassis-internal;
`council` rejected with 422 in MVP.

---

## 1. Why this reference / scope

A4 specifies the document viewer that handles section-aware authoring.
A5 (this file) specifies the **wrapper** that surfaces a deliverable's
full state — components + criteria + ratification — and serves as the
director's primary phase-completion read-out.

**In scope:**
- Entry points + operating modes (active vs read-only)
- Layout: header / components panel / criteria panel / action bar
- Per-component-kind rendering (document, artifact, run, commit)
- Per-criterion-kind rendering (text, metric, gate)
- State indicators across the screen
- Ratification action with role + criteria gating
- Phase-summary screen for N > 1 deliverables
- Caching + offline behavior
- Composition with A4 (the document viewer)
- Interaction with phase ribbon + steward strip

**Out of scope:**
- The Structured Document Viewer itself — see A4.
- Phase ribbon widget — covered by W1 wedge spec (TBD; lives in the
  Project Detail spec when wedge lands).
- Phase advance flow — handled by the phase ribbon's ratify-prompt
  affordance, sourced from this viewer's action bar but executed via
  A3 §3.2.
- Deliverable creation — handled by template hydration on phase entry;
  not a user action in MVP.

---

## 2. Entry points + operating modes

```
                     User taps a phase node in the Project Detail
                                    phase ribbon
                                         ↓
                     Resolve phase against project's phase_state
                     ┌──────────────┬──────────────┬─────────────┐
                  current          past         future
                     ↓               ↓              ↓
              Active mode    Read-only mode   "Not yet started"
                                              placeholder card
                                              (no viewer screen)
```

**Active mode** — the deliverable belongs to the current phase. All
authoring affordances visible (gated by role). Real-time data; ETag
revalidation against `GET /overview` (A3 §9.1).

**Read-only mode** — the deliverable belongs to a past phase. Source
endpoint is `GET .../phases/{phase_id}/snapshot` (A3 §9.2). Action
bar is hidden; criteria show as historical (state at ratification);
components are tappable but their child viewers (e.g., A4) inherit
read-only.

**Other entry points** (all open in the appropriate mode):

- Activity feed: tap on `deliverable.created` / `deliverable.ratified`
- Attention items: ratify-prompts (per D6 + A3 §6.4) deeplink here
- Search results
- Steward chat replies that reference a deliverable

---

## 3. Phase-summary screen (N > 1 deliverables)

When a phase has multiple deliverables (e.g., research-Convergence
phase declares both `experiment-results` and `paper-draft`), the
phase-ribbon tap opens a **phase summary** instead of jumping to one
deliverable:

```
┌──────────────────────────────────────┐
│ [‹ back] Convergence                 │
│                                       │
│ Phase ribbon (compact, current high.) │
│                                       │
│ Deliverables (2)                      │
│ ┌──────────────────────────────────┐ │
│ │ 📊 Experiment results             │ │
│ │    in-review · 3/4 criteria met   │ │
│ └──────────────────────────────────┘ │
│ ┌──────────────────────────────────┐ │
│ │ 📄 Paper draft                    │ │
│ │    draft · 0/3 criteria met       │ │
│ └──────────────────────────────────┘ │
│                                       │
│ Phase-level criteria (1 of 1 met)    │
│  ✓ Reviewer signoff — auto · 2026-…  │
│                                       │
│ [Direct steward on phase]            │
└──────────────────────────────────────┘
```

Tap a deliverable card → the deliverable viewer (this spec). Phase-
level criteria (those with `deliverable_id == null`) live here; per-
deliverable criteria live inside each deliverable's viewer.

**N == 0** (e.g., Idea phase typically): phase summary still appears,
but Deliverables panel is replaced by an empty-state card encouraging
director-steward conversation. Phase advance criteria (text criterion
"Director ratifies scope direction") rendered + ratifiable from this
screen.

**N == 1** (common case): skip the summary; phase-ribbon tap opens
the deliverable viewer directly. Phase-level criteria (if any) render
in a **collapsed** section above the deliverable's own criteria. Saves
a tap.

---

## 4. Deliverable viewer layout

```
┌──────────────────────────────────────┐
│ AppBar                                │
│   [‹ back] Initiation · Proposal     │
│              [overflow ⋮]             │
│ ────────────────────────────────────  │
│ Header card                           │
│   Phase: Initiation                   │
│   Deliverable: Proposal (kind=…)      │
│   State: ◐ in-review                 │
│   Description (optional, 2-line)      │
│   Ratification authority: director    │
│ ────────────────────────────────────  │
│ Components panel  (1 of 1 ready)      │
│   [Document card / Artifact card / …] │
│ ────────────────────────────────────  │
│ Acceptance criteria  (3 of 4 met)     │
│   [Criterion rows]                    │
│ ────────────────────────────────────  │
│ Sticky action bar                     │
│   [Direct steward]   [Ratify ▸]       │
└──────────────────────────────────────┘
```

The whole thing is one scroll surface (scrollable) with a sticky
action bar at the bottom in active mode. AppBar sits above with the
back button and overflow.

---

## 5. AppBar

- Title: `<phase display_name> · <deliverable display_name>`. Phase
  prefix gives directors who deeplink in (e.g., from search) the
  context they need.
- Trailing: kind chip (`proposal`, `paper-draft`, etc.) — small mono
  label.
- Overflow menu:
  - Direct steward on this deliverable
  - Mark in-review (when state=draft, director only)
  - Ratify (mirrors action bar, but available even when scrolled)
  - Unratify (when ratified, admin-gated, confirm dialog)
  - View deliverable history (filtered Activity)
  - Copy deliverable deeplink

In read-only mode: only "View history" + "Copy deeplink" remain.

---

## 6. Header card

```
┌────────────────────────────────────────┐
│ Initiation · Proposal                   │
│                                          │
│ State: ◐ in-review                      │
│                                          │
│ End-of-Initiation deliverable. Section-  │
│ targeted authoring with the project      │
│ steward; ratified as a whole when all   │
│ required sections are ratified.          │
│                                          │
│ Ratification authority: director         │
└────────────────────────────────────────┘
```

Components:

- **State pip** — same encoding as A4 §6 but at deliverable-level:
  - `draft` → ◐ amber
  - `in-review` → ◑ deeper amber/orange
  - `ratified` → ● green
- **Description** — optional; from template's
  `deliverables[].description`. Rendered as plain text with simple
  word-wrap; no markdown to keep the card compact.
- **Ratification authority** — small muted line at bottom. Lets the
  director see at a glance "auto-ratified" vs "requires my ratify".

For deliverables marked `required: false`, append a muted "(optional)"
tag after the kind in AppBar title.

---

## 7. Components panel

Renders each component (per A1 §2.2) using a kind-specific card.
Header shows readiness ratio, e.g., "1 of 1 ready" — a component is
"ready" when:

- `kind=document` — all required sections of the doc are `ratified`
- `kind=artifact` — referenced artifact exists + completed
- `kind=run` — referenced run is `succeeded` or `completed`
- `kind=commit` — commit exists + reachable

Cards listed in `ord` order. Required components are tagged
"(required)"; optional are tagged "(optional)".

### 7.1 Document component card

```
┌──────────────────────────────────────┐
│ 📄 Proposal document                  │
│    7 sections · 5 ratified · 2 draft  │
│    last activity: 12 min ago          │
│    [open]                              │
└──────────────────────────────────────┘
```

Tap → push the Structured Document Viewer (A4) for the referenced
document.

The breakdown line uses the cached typed-doc body's section states
to render `<count by status>`. If schema unresolved, show `7 sections
· schema unavailable` and the deeplink falls back to plain-markdown.

### 7.2 Artifact component card

```
┌──────────────────────────────────────┐
│ 📦 best-checkpoint                    │
│    model · 245 MB · committed         │
│    by: ml-worker-3 · 2026-05-04       │
│    [view]                              │
└──────────────────────────────────────┘
```

Glyph: 📦 (package emoji) or `Icons.inventory_2_outlined` —
distinguished from doc and run cards. Tap → push the existing
artifacts screen filtered to this artifact.

If artifact doesn't exist or is tombstoned: render a stub card
"Component unavailable — referenced artifact missing".

### 7.3 Run component card

```
┌──────────────────────────────────────┐
│ 🏃 ablation-sweep-v1                  │
│    completed · 4h 13m · accuracy=0.81 │
│    on: gpu-host-east                  │
│    [view]                              │
└──────────────────────────────────────┘
```

Status glyph + status text + duration + key metric (templated, depends
on run).

For metrics: pull from the run's `latest_metrics_json`. Template can
hint which metric to surface (post-MVP); MVP shows the first metric
in the run's payload.

For running runs: animate the status glyph (e.g., pulsing dot).

Tap → push the existing run-detail screen.

### 7.4 Commit component card

```
┌──────────────────────────────────────┐
│ 🔗 a3f2c91 — refactor: extract sweep  │
│    on: gpu-host-east                  │
│    by: coder agent · 2026-05-04       │
│    [view]                              │
└──────────────────────────────────────┘
```

Short SHA + commit message first line. Tap → push the existing commit
viewer (or open the host's terminal at the repo path if no commit
viewer exists).

If commit unreachable (host offline, ref deleted): render "Component
unavailable — commit unreachable".

### 7.5 Empty components panel

If a deliverable has zero components (rare; mostly applies to
deliverables that are "criteria only" with no content artifact),
render:

```
This deliverable has no components yet.
[Direct steward to draft]
```

---

## 8. Acceptance criteria panel

Lists all criteria where:

- `phase` matches the active phase, AND
- `deliverable_id == this deliverable's id` OR (in the N==1 case)
  `deliverable_id == null` (phase-level criteria collapsed in)

Header shows the readiness ratio: "<met> of <required> met".

Each criterion is a row:

```
┌──────────────────────────────────────┐
│ ✓ Motivation section ratified         │
│   auto · 2026-05-04 · evidence: §1   │
└──────────────────────────────────────┘
┌──────────────────────────────────────┐
│ ✗ Best checkpoint metric > 0.85       │
│   auto · current: 0.81                │
│   [why?]                               │
└──────────────────────────────────────┘
┌──────────────────────────────────────┐
│ ◯ Director reviews                    │
│   manual · pending                    │
│   [mark met]                           │
└──────────────────────────────────────┘
```

### 8.1 Per-state visual encoding

| State | Glyph | Color | Label |
|---|---|---|---|
| `pending` | ◯ | `DesignColors.textMuted` | (no label; pending is implicit) |
| `met` | ✓ | `DesignColors.terminalGreen` | "met" |
| `failed` | ✗ | `DesignColors.error` | "failed" |
| `waived` | ⊘ | `DesignColors.textMuted` | "waived" |

Rows are ordered: pending+required first, then pending+optional, then
met, then failed, then waived. (Director's eye lands on what they need
to act on.)

### 8.2 Per-kind rendering

The criterion's `body` (per A1 §4.2) is rendered differently per
`kind`:

- **`kind=text`** — body.text is the row label. No additional rendering.
- **`kind=metric`** — body's metric name + operator + threshold render
  in compact form: "Best checkpoint metric > 0.85". For `state=pending`
  with `evaluation=auto`, render the latest observed value too:
  "current: 0.81" in muted.
- **`kind=gate`** — body's gate handle + params render in plain English
  via a chassis lookup (e.g., `deliverable.ratified` →
  "Proposal ratified"; `all-sections-ratified` → "All required sections
  of <doc>"). MVP gate library has 4 entries (per A2 §8.3); each maps
  to one English template.

### 8.3 Per-criterion affordances

| State | Action | Auth | Behavior |
|---|---|---|---|
| `pending`, kind=text | "[mark met]" inline button | director | POST mark-met (A3 §6.4) |
| `pending`, kind=metric, evaluation=manual | "[enter value]" | director | sheet for value entry; auto-marks met if threshold satisfied |
| `pending`, kind=metric, evaluation=auto | "[why?]" | any | opens a sheet explaining the metric path + how the chassis evaluates |
| `pending`, kind=gate | "[why?]" | any | opens a sheet explaining the gate logic |
| any | overflow ⋮ → "Mark failed" | director | sheet to enter reason; POST mark-failed |
| any | overflow ⋮ → "Waive" | director | sheet to confirm + reason; POST waive |

Long-press → context menu with same options as overflow.

### 8.4 Evidence ref display

When `evidence_ref` is set, render after the timestamp:

- `document://<id>#<section>` → "evidence: §Method" (tappable, opens
  A4 at that section)
- `run://<id>` → "evidence: run-abc12" (tappable, opens run detail)
- `commit://...` → "evidence: a3f2c91" (tappable, opens commit)
- `manual://<actor>` → "evidence: manual" (no tap)

---

## 9. Action bar (sticky bottom, active mode only)

Two primary buttons:

```
[Direct steward on deliverable]    [Ratify ▸]
```

### 9.1 Direct steward on deliverable

Always visible (in active mode). Opens a steward session targeted at
the deliverable as a whole (no specific section / component target).
POST `/v1/teams/{team}/projects/{project_id}/deliverables/{id}/distill`
(non-section variant; aligns with A3 §7.4 but at deliverable scope —
actual endpoint shape TBD in A3 follow-up).

If steward state is `not-spawned`, the button reads "Start steward".

### 9.2 Ratify ▸

Visibility + enablement depends on `ratification_authority` (template-
declared, surfaced from A3 §4.1 / §9.1):

| Authority | Visibility | Enabled when |
|---|---|---|
| `director` | always shown | all `required` criteria met OR director overrides via overflow "Force ratify" |
| `auto` | hidden | n/a (chassis ratifies internally) |
| `council` | shown but disabled with tooltip "Council ratification not yet supported (post-MVP)" | never (rejected at server with 422) |

When enabled, primary CTA filled-color. When disabled, outlined-grey
with a hint sub-label: "1 required criterion still pending."

Tap → confirm dialog "Ratify the Proposal? This will…" with a
rationale text field (optional). On confirm: POST ratify (A3 §4.5).
On 200, the screen refreshes; a `criterion.met` cascade may auto-mark
gate criteria referencing this deliverable; phase advance prompt may
surface if all phase criteria are now met.

**Phase-advance follow-up:** when a ratify completes and the phase's
required criteria are *all* met, the screen surfaces a non-blocking
banner above the action bar:

```
✨ Phase ready to advance to Method.   [Advance phase]   [Not yet]
```

Tap `[Advance phase]` → POST advance (A3 §3.2). Tap `[Not yet]` →
banner dismisses but reappears on next screen visit until acted on.
Per the 2026-05-05 §B.5 closure, phase advance is **always** an
explicit director action, not auto-advance.

### 9.3 Ratify-prompt attention items

When the chassis posts a ratify-prompt attention item (D6 + A3 §6.4),
the deeplink lands here with the action bar's Ratify button
highlighted and an inline hint:

```
🔔 Steward suggests ratifying — eval-accuracy criterion just met.
```

---

## 10. Read-only mode (past phase)

When the deliverable is past-phase (sourced from
`GET .../phases/{phase_id}/snapshot`):

- AppBar overflow trims to "View history" + "Copy deeplink".
- Header card shows state pip + ratified-at timestamp + ratifying actor.
- Components are tappable but their pushed views (A4, run detail,
  etc.) inherit a read-only banner: "This <kind> was authored during
  a past phase; viewing as historical."
- Acceptance criteria show their state at the time of ratification
  (snapshot). State changes after ratification are visible only via
  the deliverable history endpoint.
- Action bar is **hidden entirely**. Long screens look like an Email
  archive — informational, no CTAs.

The read-only mode is identifiable via a small "(archived)" muted
badge in the AppBar title.

---

## 11. Empty / loading / error states

### 11.1 Loading

Skeleton placeholders in each panel — header card shimmer, 2 component
card shimmers, 3 criterion row shimmers, action bar disabled.

### 11.2 Empty deliverable (no components, no criteria)

Edge case (template misconfiguration). Render an info card:

```
This deliverable is configured with no components or criteria.
Check the project template.
```

Action bar still allows Direct-steward (debugging) but Ratify is
disabled with hint "Nothing to ratify yet."

### 11.3 Fetch failure

Standard error card with retry, matching `_ErrorView` pattern.

### 11.4 Stale data + offline

Cache-first per ADR-006. If render is from cache, show a small
"updated 2 min ago" muted timestamp at the bottom of the header
card. Pull-to-refresh re-fetches.

---

## 12. Caching + offline

### 12.1 Cache table

`HubSnapshotCache` gains `cache_deliverables`:

```sql
CREATE TABLE cache_deliverables (
  deliverable_id TEXT PRIMARY KEY,
  team_id        TEXT NOT NULL,
  project_id     TEXT NOT NULL,
  phase          TEXT NOT NULL,
  body_json      TEXT NOT NULL,           -- full deliverable + components
  etag           TEXT,
  fetched_at     TEXT NOT NULL,
  expires_at     TEXT NOT NULL
);
```

Plus `cache_criteria` (already proposed in A3 §11):

```sql
CREATE TABLE cache_criteria (
  criterion_id TEXT PRIMARY KEY,
  team_id      TEXT NOT NULL,
  project_id   TEXT NOT NULL,
  phase        TEXT NOT NULL,
  deliverable_id TEXT,
  body_json    TEXT NOT NULL,
  etag         TEXT,
  fetched_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL
);
```

The composed-overview endpoint (A3 §9.1) populates both tables in one
fetch. Direct fetches against §4.1 and §6.1 also populate them.

### 12.2 Composed-overview as primary source

For active-mode renders, mobile prefers the composed overview's
inline `active_phase.deliverables` and `active_phase.criteria`
sub-objects (A3 §9.1). For past-phase, `GET .../phases/{phase_id}/snapshot`
is the source. Direct §4 and §6 endpoints are used for refresh on
mutation, not initial render.

### 12.3 Offline mutations

- **Mark criterion met** (text kind) — queue POST mark-met. Apply
  optimistic state change locally with "pending sync" clock glyph.
- **Ratify deliverable** — queue POST ratify. Optimistic state change
  with "pending sync"; cascade ratifies optimistically applied.
  On reconnect failure → revert + error toast.
- **Direct steward** — requires connectivity (session opens server-
  side); show "Connect to direct steward" if offline.
- **Phase advance** — requires connectivity (server validates
  preconditions); offline button shows "Connect to advance phase".

Same 24h TTL on the queue as A4 §10.3.

---

## 13. Composition with A4

The Document component card in §7.1 is the entry point to A4. Tap →
push A4 with the document_id. A4's back returns to A5. A4's section
ratification (POST status) emits `document.section_ratified` audit
events; mobile listens for these (via cache invalidation on the
deliverable's component) and refreshes the document card's "5/6
ratified" line.

Conversely, A5's deliverable ratification (`POST .../ratify`)
**does not** auto-ratify document sections. A document component
must have its sections ratified independently (per the gate-criterion
pattern); only after all sections are ratified can the document
component be considered "ready", which is a precondition (often) for
the deliverable to be ratified.

---

## 14. Interaction with phase ribbon + steward strip

### 14.1 Phase ribbon visibility

When deliverable viewer is open, the phase ribbon (W1) is **not**
visible (it lives at Project Detail's top, which is on the previous
nav stack). Back button returns to Project Detail; the ribbon
re-renders showing the current phase highlighted.

### 14.2 Steward strip persistence

The steward strip (W3) lives on Project Detail and is not in this
viewer's stack. However, when this viewer triggers a steward session
(via Direct steward), the session screen's app bar surfaces a small
"return to deliverable" chip for one-tap return.

### 14.3 Phase advance from this viewer

Per §9.2, the phase-advance affordance is bannered above the action
bar after a ratify. Tapping `[Advance phase]` triggers the API call
and on success **pops back to Project Detail** with the phase ribbon
showing the new phase highlighted. Mobile re-fetches the composed
overview for the new phase.

---

## 15. Accessibility + RTL

- All state pips have text labels; no color-only signaling.
- Action bar buttons have explicit semantic roles (button + state).
- VoiceOver: header card reads "Initiation, Proposal, in review,
  ratification authority director".
- RTL: action bar buttons reverse position (Ratify on the left).
- Tap targets: ≥44×44pt per HIG.

---

## 16. Validation rules (mobile-side)

1. **Deliverable's phase matches project's current phase OR is in the
   project's phase_history.** Otherwise, treat as "future phase" → render
   placeholder, not viewer.
2. **All component refs resolve in cache OR are fetched on demand.**
   Stub cards for unreachable.
3. **Criterion bodies parse per kind.** Unknown kind → render row as
   "Unknown criterion type" with the body as raw JSON in muted code.
4. **State enums are within {pending, met, failed, waived} for criteria,
   {draft, in-review, ratified} for deliverable.** Unknown → console
   warning + treat as `pending` / `draft`.

---

## 17. Open follow-ups

1. **Force-ratify override.** Per §9.2, an admin override that
   ratifies despite unmet criteria is mentioned but not pinned; UX
   is a confirm dialog with a strong warning. Define when scheduled.
2. **Multi-criterion bulk operations.** Marking 3 criteria met in
   one action (rare; e.g., reviewing a whole batch). MVP is N
   individual taps.
3. **Criterion edit by director.** Today criteria are template-
   declared and director-immutable beyond marking. Allowing director
   to add/remove ad-hoc criteria is post-MVP.
4. **Deliverable as a steward conversation surface.** Per A3
   follow-ups, a "Direct steward on deliverable" endpoint is alluded
   to but not fully specified; pin in next API revision.
5. **Per-component ratification states.** Currently document components
   have section-state; artifact/run/commit components are atomic
   (exists vs not). When a component itself has internal state to
   ratify (e.g., a multi-figure artifact bundle), this primitive may
   need extension. Not for MVP.
6. **Deliverable history view.** "View history" overflow option
   currently routes to filtered Activity. A dedicated diff view that
   shows what changed between ratifications (when revocation lands)
   is post-MVP.
7. **Phase summary screen visual.** §3 sketches a layout but exact
   visual hierarchy with phase-level criteria requires prototyping.

---

## 18. Cross-references

- [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  — D8 (Deliverable primitive), D9 (criteria placement), §7.3
  (composed-viewer sketch)
- [`reference/project-phase-schema.md`](project-phase-schema.md) §2
  — `deliverables`, `deliverable_components`, `acceptance_criteria`
  schema
- [`reference/template-yaml-schema.md`](template-yaml-schema.md) §6
  — deliverable specs in templates
- [`reference/template-yaml-schema.md`](template-yaml-schema.md) §8
  — criterion specs in templates
- [`reference/hub-api-deliverables.md`](hub-api-deliverables.md) §4–§9
  — deliverable + criterion + composed-overview endpoints
- [`reference/structured-document-viewer.md`](structured-document-viewer.md)
  — A4; document component renders via this child viewer
- [`decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md)
  — cache-first strategy
- `reference/research-template-spec.md` (TBD; A6) — template content
  this viewer renders for the demo
