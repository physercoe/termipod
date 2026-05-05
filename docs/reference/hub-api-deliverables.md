# Hub API — phase, deliverable, criterion endpoints

> **Type:** reference
> **Status:** Draft (2026-05-05) — endpoints not yet shipped; pending plan + ADR
> **Audience:** contributors (hub backend, mobile)
> **Last verified vs code:** v1.0.351

**TL;DR.** HTTP API specification for the project-lifecycle work
(D1–D10 in
[`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)).
Adds new endpoints under
`/v1/teams/{team}/projects/{project_id}/...` for phase advancement,
deliverable CRUD + ratification, criterion mark-met, and
section-targeted document operations. Adds template-loading endpoints
under `/v1/teams/{team}/project-templates/...`. Every mutation emits
an `audit_events` row per [`audit-events.md`](audit-events.md) and the
new event kinds in [`project-phase-schema.md`](project-phase-schema.md)
§6. Authorization gates are role-aware: directors ratify; project
stewards may auto-mark criteria + update component refs; general
steward read-only across projects. All mutating endpoints accept an
`Idempotency-Key` header so retries are safe.

---

## 1. Why this reference / scope

The schema reference (A1) defines the data; the template-YAML
reference (A2) defines the declaration. This file (A3) defines the
**wire protocol** between the mobile client and the hub for the
lifecycle work.

**In scope:**
- HTTP method, path, request body, response body for every new
  endpoint
- Authorization rules (role / token gates per endpoint)
- Audit-event emission per endpoint
- Error semantics (status codes, problem-detail bodies)
- Idempotency, caching headers, pagination
- Mobile cache implications (which payloads land in `HubSnapshotCache`)

**Out of scope:**
- The hub schema — see [`project-phase-schema.md`](project-phase-schema.md).
- Template YAML structure — see [`template-yaml-schema.md`](template-yaml-schema.md).
- A2A protocol between stewards — covered in
  [`decisions/003-a2a-relay-required.md`](../decisions/003-a2a-relay-required.md)
  and steward-side specs (TBD).
- Existing endpoints not affected by this work (project create,
  archive, etc.) — those keep their contracts; this file only notes
  *additions* to their response shapes.

---

## 2. Conventions

### 2.1 Base path

```
/v1/teams/{team}/projects/{project_id}/...
/v1/teams/{team}/project-templates/...
/v1/teams/{team}/documents/{document_id}/...
```

The team prefix is mandatory and matches the existing endpoint
convention (cf. `/v1/teams/{team}/steward.general/ensure`).

### 2.2 Authentication

All endpoints require a bearer token in `Authorization: Bearer <token>`.
Token resolves to an `actor` (`actor_kind`, `actor_handle`, optional
`agent_id`) per migration 0016. Role gates per endpoint reference this
actor.

Roles for MVP:
- `director` — full access within the team's projects
- `project-steward` — auto-derived for an agent with
  `agents.scope='project'` and `agents.project_id` matching the
  request's project_id
- `general-steward` — auto-derived for an agent with
  `agents.scope='team'` and matching team
- `observer` (post-MVP) — read-only

### 2.3 Content type

- Request: `application/json` for bodies; `application/x-www-form-urlencoded`
  rejected.
- Response: `application/json`; errors as RFC 7807 problem-detail
  documents.

### 2.4 Error format

```json
{
  "type": "https://termipod/errors/<slug>",
  "title": "<short>",
  "status": 409,
  "detail": "<actionable description>",
  "audit_event_id": "ae-...",
  "context": { ... }
}
```

Status code policy:
- `400` — malformed request (validation failure)
- `401` — missing/invalid token
- `403` — token valid, role insufficient
- `404` — resource not found
- `409` — state conflict (e.g., advancing phase when criteria unmet)
- `422` — semantic validation failure (e.g., template-declared
  authority mismatches request actor)
- `429` — rate-limited
- `5xx` — hub-side fault

### 2.5 Audit emission

Every mutating endpoint emits one or more `audit_events` rows per the
table in [`project-phase-schema.md`](project-phase-schema.md) §6.
Successful response payloads include `audit_event_id` for the primary
event. Multi-event endpoints (e.g., phase advance also emits
`criterion.met` for any auto-met criteria) include
`audit_event_ids: [...]`.

### 2.6 Idempotency

All mutating endpoints honor `Idempotency-Key: <client-generated-uuid>`.
Two requests with the same key + same body return the same response
without re-executing. Hub stores keys for 24h. Retries past 24h are
treated as new requests.

### 2.7 Listing + filtering

List endpoints return:

```json
{
  "items": [...],
  "next_cursor": "<opaque>",
  "total_estimate": 42
}
```

Pagination: `?cursor=<opaque>&limit=N` (default 50, max 200). Cursor
is opaque (server-defined). Sort order is endpoint-specific; documented
per endpoint.

### 2.8 Caching

GET responses include `ETag` + `Last-Modified` headers. Mobile uses
`If-None-Match` for conditional requests. `Cache-Control: private,
max-age=15` for the composed overview endpoint (frequently re-fetched
during active session); `max-age=300` for relatively stable resources
(deliverable list, criterion list).

---

## 3. Phase endpoints

### 3.1 Get project phase state

```
GET /v1/teams/{team}/projects/{project_id}/phase
```

Returns the current phase + history.

**Auth:** any team member.

**Response 200:**

```json
{
  "phase": "experiment",
  "phase_history": [
    { "from": "idea", "to": "initiation", "at": "2026-05-04T10:00:00Z",
      "by_actor": "user:director-id", "audit_event_id": "ae-..." }
  ],
  "template_id": "research",
  "template_version": 3,
  "phase_set": ["idea","lit-review","method","experiment","paper"],
  "phase_index": 3
}
```

`phase: null` for projects without lifecycle (workspaces or legacy
goal projects). UI falls back per A1 §3.1.

### 3.2 Advance phase

```
POST /v1/teams/{team}/projects/{project_id}/phase/advance
```

Advances to the next phase declared by the template, or to a specified
target phase (skip support per D4).

**Auth:** `director` only (MVP). Future: any actor whose role is in
the transition's declared `ratification_authority` set.

**Request body:**

```json
{
  "to": "paper",                   // optional; defaults to next phase in template order
  "skipped_phases": ["analysis"],  // optional; required if `to` is non-adjacent
  "rationale": "Skipping analysis — already covered in lit review."
}
```

**Preconditions** (validated; 409 on failure):

- All required criteria (`required=1`) for the *current* phase are
  `state ∈ {met, waived}`.
- `to` is a phase declared by the template.
- A valid transition `(from=current, to=target)` exists in the
  template, OR `to` is reachable via skipping (skipped phases are
  recorded).

**Response 200:**

```json
{
  "phase": "paper",
  "previous_phase": "experiment",
  "skipped_phases": [],
  "audit_event_id": "ae-...",
  "auto_advanced_criteria": []
}
```

**Audit:** emits `project.phase_advanced`. Skipped phases each emit
their own `project.phase_advanced` with `from=skipped_phase,
to=skipped_phase` and `meta.skipped: true`.

**409 example** (criteria unmet):

```json
{
  "type": "https://termipod/errors/phase-criteria-unmet",
  "title": "Phase advancement blocked",
  "status": 409,
  "detail": "2 required criteria for phase 'experiment' are still pending.",
  "context": {
    "unmet_criteria": ["eval-accuracy-threshold", "experiment-results-ratified"]
  }
}
```

### 3.3 Set phase (admin / template hydration)

```
POST /v1/teams/{team}/projects/{project_id}/phase
```

Sets the phase directly without going through advance preconditions.
Used by template hydration (project creation) and rare admin actions.

**Auth:** `director` (admin scope) or hub-internal calls only.

**Request body:**

```json
{
  "phase": "idea",
  "reason": "template hydration"
}
```

**Response 200:** as 3.1.

**Audit:** emits `project.phase_set`.

---

## 4. Deliverable endpoints

### 4.1 List deliverables for a project

```
GET /v1/teams/{team}/projects/{project_id}/deliverables
    ?phase=<phase_id>         # optional
    &state=<state>            # optional: draft|in-review|ratified
    &include=components       # optional: include components inline
```

**Auth:** any team member.

**Response 200:**

```json
{
  "items": [
    {
      "id": "deliv-...",
      "project_id": "proj-...",
      "phase": "initiation",
      "kind": "proposal",
      "ratification_state": "in-review",
      "ratified_at": null,
      "ratified_by_actor": null,
      "required": true,
      "ord": 0,
      "created_at": "...",
      "updated_at": "...",
      "components": [...]
    }
  ],
  "next_cursor": null,
  "total_estimate": 1
}
```

Sort: `(phase ASC by template order, ord ASC)`.

### 4.2 Get one deliverable

```
GET /v1/teams/{team}/projects/{project_id}/deliverables/{deliverable_id}
```

**Auth:** any team member.

**Response 200:** single deliverable with components inline + a
`criteria_referencing_this` array of criterion IDs that reference this
deliverable.

### 4.3 Create deliverable (admin / template hydration)

```
POST /v1/teams/{team}/projects/{project_id}/deliverables
```

Used by template hydration when a phase is entered. Manual creation
allowed for admin-tier actors.

**Auth:** `director` (admin) or hub-internal.

**Request body:**

```json
{
  "phase": "initiation",
  "kind": "proposal",
  "required": true,
  "ord": 0,
  "components": [
    { "kind": "document", "ref_id": "doc-...", "required": true, "ord": 0 }
  ]
}
```

**Response 201:** created deliverable.

**Audit:** `deliverable.created` + one `deliverable_component.added`
per component.

### 4.4 Update deliverable

```
PATCH /v1/teams/{team}/projects/{project_id}/deliverables/{deliverable_id}
```

Partial updates: `ratification_state`, `required`, `ord`. Cannot
change `phase` or `kind` (would invalidate the template binding).

**Auth:** `director` for `ratification_state ∈ {draft, in-review}`;
ratification (`ratified`) goes through the dedicated endpoint (4.5)
which checks authority.

**Request body** (all fields optional):

```json
{
  "ratification_state": "in-review",
  "required": false,
  "ord": 1
}
```

**Response 200:** updated deliverable.

**Audit:** `deliverable.updated` with `meta.changed_fields`.

### 4.5 Ratify deliverable

```
POST /v1/teams/{team}/projects/{project_id}/deliverables/{deliverable_id}/ratify
```

Atomically transitions `ratification_state → 'ratified'` and stamps
`ratified_at`, `ratified_by_actor`.

**Auth:** depends on the template-declared `ratification_authority`:
- `director` — only the director may ratify
- `auto` — chassis-internal calls only (after criteria-met check)
- `council` — rejected in MVP with 422

**Preconditions:**
- `ratification_state` must be `draft` or `in-review`.
- All required components must themselves be present (existence check)
  and in a ratified state where applicable (e.g., document component's
  required sections all `status='ratified'`).

**Request body:** empty, or:

```json
{
  "rationale": "All sections reviewed and approved."
}
```

**Response 200:** ratified deliverable.

**Audit:** `deliverable.ratified`. May trigger criterion auto-met for
gate-criteria referencing this deliverable (which emit their own
`criterion.met` events; all returned in `audit_event_ids`).

### 4.6 Unratify deliverable (admin)

```
POST /v1/teams/{team}/projects/{project_id}/deliverables/{deliverable_id}/unratify
```

Reverses ratification. Rare; admin-gated. Cascades to phase state if
unratification revokes a criterion that gated phase advancement
(409 if doing so would orphan a downstream phase).

**Auth:** `director` (admin scope).

**Request body:**

```json
{
  "reason": "Found inconsistency in section 3."
}
```

**Audit:** `deliverable.unratified`.

---

## 5. Component endpoints

### 5.1 Add component to deliverable

```
POST /v1/teams/{team}/projects/{project_id}/deliverables/{deliverable_id}/components
```

**Auth:** `director` or `project-steward` for that project.

**Request body:**

```json
{
  "kind": "run",                 // document | artifact | run | commit
  "ref_id": "run-...",           // existence-checked per kind
  "required": true,
  "ord": 1
}
```

**Response 201:** created component.

**Audit:** `deliverable_component.added`.

### 5.2 Remove component

```
DELETE /v1/teams/{team}/projects/{project_id}/deliverables/{deliverable_id}/components/{component_id}
```

**Auth:** `director` for required components; `project-steward` may
remove its own non-required components.

**Response 204:** no body.

**Audit:** `deliverable_component.removed`.

---

## 6. Criterion endpoints

### 6.1 List criteria

```
GET /v1/teams/{team}/projects/{project_id}/criteria
    ?phase=<phase_id>
    &state=<state>
    &deliverable_id=<id>
    &kind=<text|metric|gate>
```

**Auth:** any team member.

**Response 200:**

```json
{
  "items": [
    {
      "id": "crit-...",
      "project_id": "proj-...",
      "phase": "initiation",
      "deliverable_id": "deliv-...",
      "kind": "gate",
      "body": { "gate": "deliverable.ratified", "params": {...} },
      "state": "met",
      "met_at": "...",
      "met_by_actor": "agent:auto",
      "evidence_ref": "deliverable://deliv-...",
      "required": true,
      "ord": 0
    }
  ],
  "next_cursor": null
}
```

### 6.2 Get one criterion

```
GET /v1/teams/{team}/projects/{project_id}/criteria/{criterion_id}
```

### 6.3 Create criterion (admin / template hydration)

```
POST /v1/teams/{team}/projects/{project_id}/criteria
```

**Auth:** `director` or hub-internal (template hydration).

**Request body:**

```json
{
  "phase": "experiment",
  "deliverable_id": "deliv-...",
  "kind": "metric",
  "body": {
    "metric": "experiment.eval_accuracy",
    "operator": ">=",
    "threshold": 0.85,
    "evaluation": "auto"
  },
  "required": true,
  "ord": 2
}
```

**Audit:** `criterion.created`.

### 6.4 Mark criterion met

```
POST /v1/teams/{team}/projects/{project_id}/criteria/{criterion_id}/mark-met
```

**Auth:**
- `kind=text` — `director` (manual mark)
- `kind=metric` with `evaluation=auto` — hub-internal only
- `kind=metric` with `evaluation=manual` — `director`
- `kind=gate` — hub-internal only (gate evaluation is chassis-driven)

**Request body:**

```json
{
  "evidence_ref": "document://doc-...#method",
  "rationale": "Method section ratified by director on 2026-05-04."
}
```

**Behavior:** auto-met criteria (per D6 + 2026-05-05 §B.5) trigger a
ratify-prompt attention item, **not** silent state advancement —
**unless** the project's auto-advance opt-in flag is set for the
relevant transition. The endpoint always sets criterion.state='met';
the ratify-prompt vs auto-advance distinction is at the *phase*
level.

**Response 200:** updated criterion + `attention_item_id` if a
ratify-prompt was posted.

**Audit:** `criterion.met`.

### 6.5 Mark criterion failed / waived

```
POST /v1/teams/{team}/projects/{project_id}/criteria/{criterion_id}/mark-failed
POST /v1/teams/{team}/projects/{project_id}/criteria/{criterion_id}/waive
```

**Auth:** `director` for both.

**Request body:**

```json
{
  "reason": "Threshold cannot be met given current data; pivot to weaker claim."
}
```

**Audit:** `criterion.failed` or `criterion.waived`.

### 6.6 Update criterion body / evidence

```
PATCH /v1/teams/{team}/projects/{project_id}/criteria/{criterion_id}
```

**Auth:** `director`.

**Request body** (all fields optional):

```json
{
  "body": { ... },
  "evidence_ref": "...",
  "required": true,
  "ord": 0
}
```

**Audit:** none specifically; falls under `criterion.created` if a
re-author or `criterion.met` if state changes.

---

## 7. Document section endpoints (typed-doc-aware)

These extend the existing `/documents/{document_id}` endpoints
(unchanged contract) with section-level operations for typed
structured documents (D7).

### 7.1 Get document with sections

```
GET /v1/teams/{team}/documents/{document_id}
```

(Existing endpoint; response gains fields when `kind` is set.)

**Response 200** (typed doc):

```json
{
  "id": "doc-...",
  "kind": "proposal",
  "schema_id": "research-proposal-v1",
  "body": {
    "schema_version": 1,
    "schema_id": "research-proposal-v1",
    "sections": [
      {
        "slug": "motivation",
        "title": "Motivation",
        "body": "<markdown>",
        "status": "ratified",
        "last_authored_at": "...",
        "last_authored_by_session_id": "sess-...",
        "ratified_at": "...",
        "ratified_by_actor": "user:director-id"
      }
    ]
  },
  ...
}
```

For plain markdown documents (`kind=null`), `body` is a string
(existing behavior).

### 7.2 Update section body

```
PATCH /v1/teams/{team}/documents/{document_id}/sections/{section_slug}
```

Direct edit (e.g., director redlines manually). Does NOT change
`status` — that's a separate endpoint.

**Auth:** `director` or `project-steward` of the project containing
this document (resolved via deliverable_components → deliverable →
project, OR via the document's project_id if free-floating).

**Request body:**

```json
{
  "body": "<markdown>",
  "expected_last_authored_at": "..."   // optimistic concurrency check
}
```

`expected_last_authored_at` prevents lost-update when steward + director
edit concurrently. 412 returned if mismatch.

**Response 200:** updated section.

**Audit:** `document.section_authored` with `prior_status` /
`new_status` (status unchanged on direct edit; both fields equal).

### 7.3 Set section status

```
POST /v1/teams/{team}/documents/{document_id}/sections/{section_slug}/status
```

Three-state per D7 / 2026-05-05 §B.2: `empty | draft | ratified`.

**Auth:**
- `empty → draft` — any author (steward or director)
- `draft → ratified` — `director` only
- `ratified → draft` — `director` only (rare; rework)

**Request body:**

```json
{
  "status": "ratified",
  "rationale": "Reviewed and accepted."
}
```

**Audit:** `document.section_ratified` for `→ ratified`;
`document.section_authored` for state transitions otherwise.

### 7.4 Section-targeted distillation (steward session entry)

```
POST /v1/teams/{team}/documents/{document_id}/sections/{section_slug}/distill
```

Opens a steward session targeted at this section. The session's
distillation on close updates the section's `body` and stamps
`last_authored_at` + `last_authored_by_session_id`.

**Auth:** `director` (initiates) or `project-steward` (on the
director's behalf via attention item flow).

**Request body:**

```json
{
  "session_kind": "section-target",
  "initial_prompt": "Draft a Method section per the proposal draft.",
  "context_refs": [                      // optional; pinned context for the session
    { "kind": "document", "id": "doc-...", "section": "motivation" },
    { "kind": "document", "id": "doc-other" }
  ]
}
```

**Response 201:**

```json
{
  "session_id": "sess-...",
  "open_url": "termipod://session/sess-...",
  "audit_event_id": "ae-..."
}
```

**Audit:** `agent_session.opened` (existing event kind, with
`meta.target = { document_id, section_slug }`).

When the session closes (existing `agent_session.distilled` event),
the session's distillation is applied to the section as if via PATCH
7.2, with state transition `empty → draft` or `draft → draft` (does
not auto-promote to ratified — director ratifies separately).

---

## 8. Project template endpoints

These wrap the YAML-loaded spec (per A2) for client consumption.

### 8.1 List templates

```
GET /v1/teams/{team}/project-templates
    ?include=spec              # optional; include full parsed spec inline
```

**Auth:** any team member.

**Response 200:**

```json
{
  "items": [
    {
      "id": "research",
      "format_version": 1,
      "template_version": 3,
      "display_name": "Research project",
      "description": "...",
      "kind": "goal",
      "phase_count": 5,
      "default_overview_widget": "portfolio_header"
    }
  ]
}
```

With `?include=spec`: items also carry the full parsed YAML spec.

### 8.2 Get template by id

```
GET /v1/teams/{team}/project-templates/{template_id}
```

**Auth:** any team member.

**Response 200:** full parsed spec (structured per A2 §4).

### 8.3 Reload templates (admin)

```
POST /v1/teams/{team}/project-templates/_reload
```

Triggers re-walk of overlay paths. Bundled templates already loaded
at startup are not reloaded; restart for that.

**Auth:** `director` (admin scope) or hub-internal.

**Response 200:**

```json
{
  "loaded": ["research","ablation-sweep","workspace"],
  "errors": [
    { "path": "<DataRoot>/teams/T1/templates/projects/foo.yaml",
      "detail": "phase ids must be unique" }
  ]
}
```

---

## 9. Composed-overview endpoint

### 9.1 Get project overview chassis

```
GET /v1/teams/{team}/projects/{project_id}/overview
```

Returns everything the mobile Project Detail screen needs for the
phase-aware Overview render:

- Project core fields
- Current phase + phase ribbon state (which phases done / current /
  future)
- Active phase's overview widget slug
- Active phase's deliverables (with components inline)
- Active phase's criteria (with current state)
- Phase-filtered tile set
- Vital strip data (status, progress, budget, steward liveness counts,
  attention count)

**Auth:** any team member.

**Response 200:**

```json
{
  "project": { ... },
  "phase_state": {
    "current": "experiment",
    "ribbon": [
      { "id": "idea", "abbrev": "Idea", "state": "done" },
      { "id": "lit-review", "abbrev": "Lit-rev", "state": "done" },
      { "id": "method", "abbrev": "Method", "state": "done" },
      { "id": "experiment", "abbrev": "Exp", "state": "current" },
      { "id": "paper", "abbrev": "Paper", "state": "future" }
    ]
  },
  "active_phase": {
    "id": "experiment",
    "overview_widget": "experiment_dash",
    "tiles": ["Outputs","Documents","Experiments"],
    "deliverables": [...],
    "criteria": [...],
    "criteria_met_count": 1,
    "criteria_required_count": 4
  },
  "vitals": {
    "status": "active",
    "progress_ratio": 0.6,
    "budget_used_cents": 4200,
    "budget_cap_cents": 20000,
    "steward_state": "working",
    "open_attention_count": 2,
    "last_activity_at": "2026-05-05T09:30:00Z"
  },
  "next_action": {
    "kind": "review_section",
    "label": "Review Method section",
    "deeplink": "termipod://document/doc-.../sections/method"
  }
}
```

`Cache-Control: private, max-age=15`. Returns `304 Not Modified` on
ETag match. Mobile uses this as the Project Detail render source.

### 9.2 Get past-phase deliverable view

```
GET /v1/teams/{team}/projects/{project_id}/phases/{phase_id}/snapshot
```

For when the director taps a past phase in the ribbon — read-only
historical view of that phase's ratified deliverables + criteria.

**Auth:** any team member.

**Response 200:** same shape as `active_phase` block above.

---

## 10. Steward handoff indicator

Per the 2026-05-05 §B.6 closure: when a steward routes via another
steward, the director sees a brief indicator. Powered by:

### 10.1 Get steward live state

```
GET /v1/teams/{team}/projects/{project_id}/steward/state
```

**Auth:** any team member.

**Response 200:**

```json
{
  "scope": "project",
  "agent_id": "agent-...",
  "state": "working",
  "current_action": {
    "kind": "drafting_section",
    "target": { "document_id": "doc-...", "section": "method" },
    "started_at": "...",
    "expected_until": null
  },
  "handoff": null
}
```

When mid-handoff:

```json
{
  "state": "handoff_in_progress",
  "handoff": {
    "from_scope": "project",
    "to_scope": "team",
    "to_agent_id": "agent-general-...",
    "purpose": "consulting_general_steward",
    "started_at": "..."
  }
}
```

`Cache-Control: private, no-cache` — the strip polls this every few
seconds during active sessions; ETag honored.

### 10.2 Server-Sent-Events stream (optional, post-MVP)

```
GET /v1/teams/{team}/projects/{project_id}/steward/state/sse
```

Push live updates rather than polling. MVP can poll; SSE upgrade is
a follow-up.

---

## 11. Mobile cache implications

`HubSnapshotCache` (sqflite) gains tables mirroring the read-side
shapes:

| Cache table | Sourced from |
|---|---|
| `cache_project_overview` | §9.1 — keyed by project_id, ETag-versioned |
| `cache_deliverables` | §4.1 — keyed by project_id |
| `cache_criteria` | §6.1 — keyed by project_id |
| `cache_documents_typed` | §7.1 — keyed by document_id, sections inline |
| `cache_project_templates` | §8.1 — keyed by template_id |
| `cache_steward_state` | §10.1 — keyed by project_id |

Cache TTL aligns with `Cache-Control` max-age. Mobile reads cache
first per the cache-first-with-refresh pattern (
[ADR-006](../decisions/006-cache-first-cold-start.md)), then revalidates with
`If-None-Match`.

---

## 12. Rate limiting

- Phase advance: 1 / 10s per project (prevent thrashing).
- Deliverable ratify: 1 / 5s per deliverable.
- Criterion mark-met: no specific limit (within global per-token).
- Section distill open: 5 / minute per project.
- Composed-overview GET: 30 / minute per actor (covers UI revalidation).

429 responses include `Retry-After` header.

---

## 13. Open follow-ups

1. **A2A handoff endpoints.** §10.1 surfaces handoff state for the
   indicator UI but the actual A2A protocol between stewards is in
   ADR-003; specify here once finalized.
2. **SSE for steward state.** §10.2 — implementable post-MVP; polling
   is fine for the demo.
3. **Council ratification authority.** All endpoints reject `council`
   with 422 in MVP. Spec when councils are designed (post-F-1).
4. **Document version snapshots.** Per A1 §9, deliverable history is
   preserved via audit; a dedicated `GET .../snapshots` endpoint may
   be useful when revocation lands.
5. **Bulk operations.** Marking many criteria met in one call (e.g.,
   on phase entry, hub may bulk-create criteria from template). MVP
   is N requests; bulk endpoint deferred.
6. **Webhook outbound.** External systems (CI, paper repos) may want
   to be notified when a phase advances. Out of MVP scope.

---

## 14. Cross-references

- [`reference/project-phase-schema.md`](project-phase-schema.md) — DB
  schema + audit event kinds
- [`reference/template-yaml-schema.md`](template-yaml-schema.md) —
  template authoring contract that drives template-loading endpoints
- [`reference/audit-events.md`](audit-events.md) — base audit taxonomy
- [`reference/permission-model.md`](permission-model.md) — role + token
  resolution
- [`reference/attention-kinds.md`](attention-kinds.md) — ratify-prompt
  attention items emitted by §6.4 + phase advance auto-paths
- [`decisions/003-a2a-relay-required.md`](../decisions/003-a2a-relay-required.md)
  — A2A protocol context for §10
- [`decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md)
  — mobile cache strategy
- [`decisions/017-layered-stewards.md`](../decisions/017-layered-stewards.md)
  — steward state + handoff
- [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  — design discussion
