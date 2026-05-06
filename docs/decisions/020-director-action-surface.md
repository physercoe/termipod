# 020. Director-action surface on typed documents and deliverables

> **Type:** decision
> **Status:** Proposed (2026-05-06)
> **Audience:** contributors
> **Last verified vs code:** v1.0.360-alpha

**TL;DR.** The control plane today gives the director two coarse moves on a typed document or deliverable: *edit the body directly*, or *promote/ratify*. That's a binary verdict layer with no deliberation layer in between. This ADR enumerates the seven director-actions a control plane should expose, locks the **annotation** primitive and the **send-back-with-notes** handoff as the MVP pair, and defers the other five with a note on each. Rationale: the annotation row + a typed return-to-sender attention item cover the bulk of director-on-doc behaviour without adding new primitives or rewriting the W5/W6 surface.

---

## Context

After W5a/W5b/W6 ([plans/research-demo-lifecycle-wedges.md](../plans/research-demo-lifecycle-wedges.md)) the director can:

- PATCH a section body (`/documents/{doc}/sections/{slug}`).
- Move section status empty → draft → ratified.
- PATCH a deliverable in draft / in-review.
- POST `…/ratify` and `…/unratify` on a deliverable.
- Mark a criterion met / failed / waived.
- Approve or deny a `reviews` row with a single free-text `comment` field ([handlers_reviews.go](../../hub/internal/server/handlers_reviews.go)).
- Triage `attention_items` (open / dismiss).

What is missing is the **deliberation layer** between "look at it" and "decide on it." A real director redlines a paragraph, asks for a rewrite of one section, or flags a passage for the steward to revisit. None of those are first-class today: the only return path is "type into chat and hope the steward picks it up," which loses anchoring (which paragraph?) and loses the audit trail (was the rewrite request acted on?).

This ADR reads the gap, enumerates the seven canonical director-on-doc moves, and decides which subset becomes load-bearing now versus later. It does **not** design the schema in full — that lives in the wedge plan ([plans/director-actions.md](../plans/director-actions.md)).

### Why this ADR exists now

Three pressures forced the question:

1. **Lifecycle demo dress-rehearsal (2026-05-06).** Walking the seeded 5-phase portfolio reveals that ratify/unratify is too blunt: the realistic director response to a flawed lit-review section is *"fix this paragraph,"* not *"unratify the whole deliverable."*
2. **MVP parity gap.** Single-engine remote-control apps (claudecode-remote, Happy) all expose comment-on-message. We're behind on a primitive every reviewer expects. See [feedback no-short-board](../../../.claude/projects/-home-ubuntu-mux-pod/memory/feedback_no_short_board.md) — bookkeeping note, not load-bearing.
3. **IA axiom IA-A3.** "The steward does the work; the director ratifies." Ratification without a return-to-sender path collapses into either rubber-stamp or director-does-the-work, both of which violate IA-A3. The handoff is the missing link.

---

## The seven moves — full enumeration

Before the decision, the full surface, so the decision can be evaluated against the whole space rather than the chosen slice. Each row names the canonical verb, the entity it operates on, and what gets persisted.

| # | Verb | Operates on | Persists | Status |
|---|------|-------------|----------|--------|
| 1 | **Annotate** (redline / comment / suggestion) | section + optional char range | annotation row (anchor, body, status) | **MVP** |
| 2 | **Send back with notes** | deliverable in draft/in-review | attention item + state event | **MVP** |
| 3 | **Diff vs. last-ratified** | section, deliverable | snapshot row at ratify-time | Deferred |
| 4 | **Reassign producer** | section, component | producer-agent column on row | Deferred |
| 5 | **Quote-to-steward** | section + selected text | steward turn seeded with quote | Deferred |
| 6 | **Pin / flag** | section | per-user bookmark row | Deferred |
| 7 | **Branch / fork deliverable** | deliverable | parent_deliverable_id column | Deferred |

The "MVP" rows are load-bearing for the demo and for control-plane parity with single-engine clients. The five Deferred rows are valuable but each has a viable workaround in the MVP shape (see §Deferred below).

### Why these two are top-of-stack

**Annotation is the universal substrate.** A director who can anchor a comment to a section + offset can express: redline ("delete this"), suggestion ("replace with X"), question ("source?"), praise ("keep this framing"), and todo ("revisit after experiment"). Quote-to-steward (#5) is annotation + a *send to steward* button. Pin/flag (#6) is annotation with an empty body. Reassign-producer (#4) is annotation with a typed action. **Three of the five Deferred moves degenerate to annotation + one extra action**, so building annotation first makes them additive instead of new primitives.

**Send-back-with-notes closes the deliberation loop.** Without it, the only "no" the director can express is unratify or self-edit. Both are unsatisfying: unratify wipes the verdict but doesn't say what's wrong; self-edit teaches the steward nothing. A typed `attention_kind = 'revision_requested'` carrying the director's note + (when present) the annotations referenced is the smallest unit that lets the steward queue a revision turn without the director leaving the doc surface.

The two compose: a director redlines three paragraphs (annotations), then taps "Send back with notes" once. The attention item carries the three anchors; the steward opens the doc with annotations rendered and revises in place.

---

## Decision

**D1. Annotation is a first-class primitive, not a comment field on `reviews`.** A new `document_annotations` table with anchor (section_id + optional char_start/char_end), body, author, kind, status. Anchored to sections, not paragraphs (paragraph IDs are unstable; char offsets are stable for the lifetime of a section body). One annotation per row; threading is deferred (one-level reply via `parent_annotation_id` is the only thread shape, and not in MVP).

**D2. Annotation kinds are an enum, not free text.** MVP set: `comment` (default), `redline` (suggests deletion), `suggestion` (suggests replacement, body carries proposed text), `question`. The kind shapes the renderer (strikethrough for redline, replacement preview for suggestion). New kinds add through migration; the renderer's default for unknown kinds is `comment`.

**D3. Annotations have a status: `open` or `resolved`.** Resolved annotations stay in the row but render collapsed. The steward can mark resolved when revising; the director can re-open. No deletion — annotations are part of the audit trail per the same logic as ADR-019 D2 (events are append-only for audit reasons).

**D4. Send-back-with-notes is a typed `attention_items` row, not a new table.** The existing attention queue is the right inbox; adding a new top-level table for revision requests would duplicate routing, audit, and dismissal. New `attention_kind = 'revision_requested'` carries `target_kind = 'deliverable'`, `target_id`, plus `pending_payload_json` shaped as `{note: string, annotation_ids: string[]}`. The steward picks up the item via the normal attention loop.

**D5. Send-back transitions deliverable state to `in-review`.** Not back to `draft`. Rationale: `in-review` is the state that already gates ratify; transitioning to `draft` would lose the fact that the deliverable was once review-ready. Reaching `in-review` from `ratified` requires unratify-then-send-back; reaching `in-review` from `draft` is the no-op forward path. The state column is the public surface; `phase_history` already tracks transitions, no new table.

**D6. Annotations target sections, not deliverables.** A deliverable-level note is a section-level note on the deliverable's overview / first section. Rationale: every annotation needs an anchor for the renderer to position it; deliverables don't have a renderable body of their own (they're a composition of components). When a director needs a "comment on the whole deliverable," they comment on the introduction section. When a deliverable has no introduction, the send-back-with-notes action is the right primitive — D4 already handles deliverable-scope feedback.

**D7. The five Deferred moves are *not* forbidden — they're sequenced.** Each has a one-line note in §Deferred describing the eventual shape, so future PRs don't re-litigate the design. Specifically: diff (#3) is a per-section snapshot table populated at ratify-time; reassign (#4) adds `producer_agent_id` to `deliverable_components` and `document_sections`; quote-to-steward (#5) is annotation + an MCP `steward.invoke` carrying the anchor; pin (#6) is annotation with empty body and `kind = 'pin'`; branch (#7) is `parent_deliverable_id` on `deliverables` plus a clone helper.

---

## Consequences

**Becomes possible:**

- A director can leave anchored feedback on any section without leaving the doc viewer.
- A director can return a deliverable to the steward with structured notes, replacing the "type-in-chat-and-hope" workaround.
- The activity feed renders annotation events alongside ratification events, so the deliberation history is visible.
- The five Deferred moves slot into the same primitive without new tables (annotation + kind), keeping the schema small.

**Becomes harder:**

- Renderers in the typed-document viewer and the structured-deliverable viewer both need an annotation overlay. Two surfaces, one renderer (factor it).
- The steward prompt overlay needs to know *how* to act on a `revision_requested` attention item — the prompt must instruct the steward to read the annotations and address each by anchor. This is content work, not infra.
- Annotation char-range stability across edits is best-effort. If a section body changes underneath an annotation, the renderer falls back to "anchored to section, range unrecoverable." Acceptable because annotations are deliberation, not invariants.

**Becomes forbidden:**

- A second free-text comment field on any existing table (don't add `documents.note`, don't add `deliverables.director_comment`). All deliberation is annotations.
- A separate `revision_requests` or `feedback` top-level table. D4 says the attention queue is the inbox.
- Treating `reviews.comment` as a redline channel. That field stays — it's the single-line decision rationale on the reviews surface, distinct from anchored annotation.

---

## Deferred — the other five moves

Not in MVP, but each has a sketch so future PRs land cleanly:

**#3 Diff vs. last-ratified.** Add `section_snapshots(document_id, slug, body, ratified_at, ratified_by_actor)` populated at every section status → ratified transition. Diff renderer compares current body vs. latest snapshot. Same shape for deliverables (snapshot the composed overview at ratify-time). Effort: ~1 day.

**#4 Reassign producer.** Add `producer_agent_id TEXT NULL` to `deliverable_components` and `document_sections`. Director picks from project's agent roster. Steward routes revision turns to the named producer. Effort: ~1 day; the roster picker UI is the long pole.

**#5 Quote-to-steward.** Annotation primitive + a "Ask steward" button on each annotation that posts an MCP `steward.invoke` carrying the section + char range + annotation body as context. Effort: ~0.5 day after MVP annotation lands.

**#6 Pin / flag.** Annotation `kind = 'pin'` with empty body. Renderer shows a bookmark glyph in the section header; "My pins" view filters annotations by `author = me, kind = pin`. Effort: ~0.5 day after MVP annotation lands.

**#7 Branch / fork deliverable.** `parent_deliverable_id` on `deliverables` + a clone helper that copies components and criteria. Branch renderer shows alternates side-by-side; ratify on one auto-archives siblings. Effort: ~2 days; UX for switching between branches is the long pole.

The order above (#3 → #4 → #5 → #6 → #7) reflects expected demand from the lifecycle demo dress-rehearsals; the order can shift without re-litigating this ADR.

---

## Migration

New migration `0035_document_annotations.up.sql`:

- `document_annotations` table — id, document_id, section_slug, char_start, char_end (both nullable), kind, body, status, author_kind, author_handle, parent_annotation_id (nullable, for one-level reply, not in MVP UI), created_at, resolved_at, resolved_by_actor.
- Index on `(document_id, section_slug)` for the per-section overlay query.
- Index on `(author_handle, status)` for the "my open notes" view.

`attention_items` already accepts arbitrary `attention_kind`; no schema change for D4 — the new kind ships as application logic per ADR-019 D4's principle.

Down-migration drops the table; no data loss concern because annotations are not load-bearing for any other table.

---

## References

- [ADR-005 owner-authority-model](005-owner-authority-model.md) — director vs. steward authority split this ADR operationalizes for the typed-doc surface.
- [ADR-019 channels-as-event-log](019-channels-as-event-log.md) — D2 (append-only) is the precedent for annotation status (resolve, don't delete).
- [Spine: information-architecture §IA-A3](../spine/information-architecture.md) — director ratifies, steward does the work.
- [Spine: governance-roles](../spine/governance-roles.md) — the role ontology this ADR refines for typed docs.
- [Plans: director-actions](../plans/director-actions.md) — the wedge plan implementing D1–D6.
- Schema: [`hub/migrations/0034_project_lifecycle.up.sql`](../../hub/migrations/0034_project_lifecycle.up.sql) — the deliverables/components/criteria substrate this ADR layers onto.
- Code: [`hub/internal/server/handlers_document_sections.go`](../../hub/internal/server/handlers_document_sections.go), [`hub/internal/server/handlers_deliverables.go`](../../hub/internal/server/handlers_deliverables.go), [`hub/internal/server/handlers_reviews.go`](../../hub/internal/server/handlers_reviews.go) — the surfaces this ADR extends.
