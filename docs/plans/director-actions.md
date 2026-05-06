# Director-actions wedge plan — annotation primitive + send-back-with-notes

> **Type:** plan
> **Status:** Proposed (2026-05-06)
> **Audience:** contributors
> **Last verified vs code:** v1.0.360-alpha

**TL;DR.** Two wedges to ship the MVP slice of [ADR-020](../decisions/020-director-action-surface.md). W1 lands the `document_annotations` primitive end-to-end (schema + REST + section-overlay UI + structured-deliverable-viewer overlay). W2 adds the `revision_requested` attention kind plus the "Send back with notes" button on the deliverable viewer. Roughly 3–4 days of work. One schema migration (`0035_document_annotations`). No new ADRs after ADR-020.

The five Deferred director-actions in ADR-020 §Deferred are explicitly **not** in this plan; each has a one-line sketch in the ADR for when its turn comes.

---

## Out of scope

The following are intentionally not part of this plan and shouldn't grow into it:

- Threaded replies on annotations. The schema reserves `parent_annotation_id` (D1) but the MVP renderer ignores it.
- Real-time collaborative annotation. Polling refresh on viewer open is enough.
- Diff vs. last-ratified, reassign-producer, quote-to-steward, pin/flag, branch/fork deliverable. See [ADR-020 §Deferred](../decisions/020-director-action-surface.md#deferred--the-other-five-moves).
- Cross-section annotations (one annotation spanning two sections). Anchor is single section.

---

## W1 — Annotation primitive end-to-end

**Goal:** A director can leave anchored notes on any section of a typed document or structured deliverable. Notes render in the viewer, can be marked resolved, and survive across reads. This is the foundation; W2 depends on its annotation rows.

**Files (hub):**

- `hub/migrations/0035_document_annotations.up.sql` + `…down.sql` — new. Table per ADR-020 §Migration. Indexes on `(document_id, section_slug)` and `(author_handle, status)`.
- `hub/internal/server/handlers_document_annotations.go` — new. Five handlers:
  - `POST /documents/{doc}/annotations` — create. Body: `{section_slug, char_start?, char_end?, kind, body}`. Author derived from `actorFromContext(ctx)`.
  - `GET /documents/{doc}/annotations?section={slug}&status={open|resolved|all}` — list. Default `status=open`.
  - `PATCH /annotations/{id}` — update body or kind. Only the author can edit.
  - `POST /annotations/{id}/resolve` and `…/reopen` — status toggle. Anyone with project read-write can resolve.
  - `DELETE /annotations/{id}` — **rejected with 405**. Per ADR-020 D3 the table is append-only-on-content; resolve is the soft-close.
- `hub/internal/server/handlers_document_annotations_test.go` — new. Coverage:
  - Create + list round-trip.
  - char_range nullable (deliverable-level note via D6 → section-level note on intro).
  - Status filter.
  - Edit-by-non-author rejected.
  - Resolve + reopen.
  - DELETE returns 405 with the canonical error string.
- `hub/internal/server/seed_demo_lifecycle.go` — extend the lit-review and method-design seeded projects with 2–3 sample annotations each (one `comment`, one `redline`, one `suggestion`), so the dress-rehearsal tile shows the overlay populated.
- Audit: every create / patch / resolve emits an `audit_events` row with `action ∈ {annotation.created, annotation.edited, annotation.resolved, annotation.reopened}`.

**Files (mobile):**

- `lib/services/hub_client/annotations.dart` — new. Five client methods matching the REST surface; typed `Annotation` model.
- `lib/widgets/annotation_overlay.dart` — new. Section-scoped widget that:
  - Loads `GET /documents/{doc}/annotations?section={slug}&status=open` on mount.
  - Renders a margin-aligned indicator per annotation (kind-specific glyph: comment, redline strikethrough, suggestion arrow, question).
  - Tap → bottom sheet with full body, author, created_at, kind, "Resolve" / "Reopen" / "Edit" (edit only if author).
  - Long-press on a paragraph in the section → "Add annotation" sheet seeded with kind picker.
- `lib/screens/documents/section_detail_screen.dart` — wire AnnotationOverlay into the section body; long-press handler binds to char offsets via the existing markdown renderer (best-effort; annotations without a recoverable range render as section-level notes).
- `lib/screens/deliverables/structured_deliverable_viewer.dart` — for each component of `kind = 'document_section'`, render the same AnnotationOverlay. Composed deliverable-level note (per ADR-020 D6) is a section-level note on the intro section.
- `lib/screens/projects/project_detail_screen.dart` — Activity tile picks up annotation events from `audit_events` automatically once the kinds land.

**Verification:**

1. Run `go test ./hub/...` — all annotation handler tests pass.
2. `flutter analyze` clean (CI runs it; the SDK isn't installed on the maintainer's dev machine, so analyze validation is CI-only).
3. Re-seed lifecycle demo (`hub-server seed-demo --shape lifecycle`); open the lit-review project on mobile; confirm the seeded annotations render with the right glyphs.
4. Add a comment via long-press; refresh viewer; confirm it persists and renders.
5. Resolve an annotation; confirm it disappears from the default `status=open` view; toggle filter to "all" and confirm it shows collapsed.
6. Try to DELETE via curl — confirm 405 with the canonical message.
7. Inspect Activity tile — confirm `annotation.created` and `annotation.resolved` rows render.

**Effort:** ~2 days. Long pole is the AnnotationOverlay positioning logic against the markdown renderer's char-offset model; if it gets gnarly, fall back to section-level rendering and ship char-range as a follow-up.

---

## W2 — Send back with notes

**Goal:** A director viewing a deliverable in `draft` or `in-review` taps "Send back with notes," writes a one-line summary, optionally selects which open annotations to attach, and submits. The hub creates an `attention_items` row of kind `revision_requested` and transitions the deliverable to `in-review`. The steward picks it up via the normal attention loop.

**Depends on W1** because the annotation-attachment UI references annotation rows. Without W1, send-back is a single-line note with no anchors — viable but flatter than the design intent.

**Files (hub):**

- `hub/internal/server/handlers_deliverables.go` — add handler:
  - `POST /deliverables/{id}/send-back` — body: `{note: string, annotation_ids: string[]}`. Validates deliverable state ∈ `{draft, in-review}`. Transitions to `in-review` via the existing state column. Inserts `attention_items` row with `kind = 'revision_requested'`, `target_kind = 'deliverable'`, `target_id = {id}`, `pending_payload_json = {note, annotation_ids}`, actor from context.
  - Rejects with 409 if state is `ratified` (per ADR-020 D5; director must unratify first, deliberately).
- `hub/internal/server/handlers_deliverables_test.go` — extend:
  - Send-back from draft → state becomes in-review, attention item created.
  - Send-back from in-review → idempotent state, attention item created.
  - Send-back from ratified → 409.
  - Annotation IDs must belong to a section of a document referenced by one of the deliverable's components; otherwise 422 (prevents cross-deliverable leakage).
- `hub/internal/server/seed_demo_lifecycle.go` — add one seeded `revision_requested` attention item on the method-design phase project, with two annotation IDs from the seeded W1 set, so the steward inbox is populated for the dress-rehearsal.

**Files (mobile):**

- `lib/services/hub_client/deliverables.dart` — add `sendBackDeliverable(deliverableId, note, annotationIds)` client method.
- `lib/screens/deliverables/structured_deliverable_viewer.dart` — add "Send back with notes" action to the deliverable's overflow menu (visible when state ∈ `{draft, in-review}`):
  - Bottom sheet: text field for note + checkbox list of open annotations on this deliverable's sections.
  - Submit calls `sendBackDeliverable`; on success, snackbar + refresh deliverable state pip.
- `lib/screens/attention/attention_detail_screen.dart` — register `revision_requested` kind:
  - Title: "Revision requested on {deliverable.title}"
  - Body: the note from `pending_payload_json`.
  - "Open deliverable" tap → push StructuredDeliverableViewer for the target.
  - The annotation_ids in the payload render as a list of "Annotated section X" rows that deep-link into the section detail with the annotation expanded.
- Steward template overlay (`hub/internal/server/templates/embedded/research.v1.yaml` or successor) — append a section telling the steward how to handle `revision_requested`: read the note, read each linked annotation by anchor, address each, then mark annotations resolved as it edits.

**Verification:**

1. `go test ./hub/...` — new send-back tests pass; existing deliverable tests still pass.
2. `flutter analyze` clean (CI; SDK not local).
3. Re-seed lifecycle demo; open the seeded `revision_requested` attention item on Me tab; confirm it renders with the note + linked annotations; confirm tap → deliverable viewer.
4. From the lit-review deliverable, tap "Send back with notes," type a one-line note, select two open annotations, submit. Confirm:
   - Deliverable pip transitions to in-review.
   - A new attention item appears on Me tab.
   - The attention item's payload includes the two annotation IDs.
5. Try send-back on a ratified deliverable — confirm 409 surfaces as a snackbar with the correct message.
6. Steward dry-run: spawn a steward, attach it to the project, post the seeded `revision_requested` attention item, confirm the steward's first turn references the note and the linked annotations.

**Effort:** ~1.5 days. Long pole is the attention-detail rendering for the new kind, since it's the first attention kind that contains a structured payload referencing other rows.

---

## Order

W1 must ship before W2 because W2's "select annotations to attach" UI depends on annotation rows existing. Both wedges should ship before the next lifecycle dress-rehearsal so the deliberation loop is exercised end-to-end.

After both wedges ship:

- ADR-020 status moves Proposed → Accepted.
- This plan moves In flight → Done.
- Update [run-lifecycle-demo.md](../how-to/run-lifecycle-demo.md) Pre-flight to walk the new annotation overlay + send-back path.

---

## References

- [ADR-020 director-action-surface](../decisions/020-director-action-surface.md) — the architectural decision this plan implements.
- [Plan: research-demo-lifecycle-wedges](research-demo-lifecycle-wedges.md) — the W5/W6 substrate this plan layers on.
- [How-to: run-lifecycle-demo](../how-to/run-lifecycle-demo.md) — the acceptance walkthrough that gains the new steps post-ship.
- Schema: [`hub/migrations/0034_project_lifecycle.up.sql`](../../hub/migrations/0034_project_lifecycle.up.sql) — the lifecycle substrate; new migration `0035` layers on top.
- Code: [`hub/internal/server/handlers_document_sections.go`](../../hub/internal/server/handlers_document_sections.go), [`hub/internal/server/handlers_deliverables.go`](../../hub/internal/server/handlers_deliverables.go), [`lib/screens/documents/section_detail_screen.dart`](../../lib/screens/documents/section_detail_screen.dart), [`lib/screens/deliverables/structured_deliverable_viewer.dart`](../../lib/screens/deliverables/structured_deliverable_viewer.dart) — the surfaces the wedges modify.
