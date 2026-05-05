# System flows

> **Type:** axiom
> **Status:** Current (2026-05-05)
> **Audience:** contributors, reviewers
> **Last verified vs code:** v1.0.351

**TL;DR.** Sequence diagrams for the 8 critical cross-component flows.
Each diagram shows who-talks-to-whom, in what order, with what payload,
and where audit / cache effects land. Prose narrative for each flow
lives in its sister doc; the diagrams here complement, not replace.

This is arc42 §6 (Runtime View). Containers + protocols are at
[`../reference/architecture-overview.md`](../reference/architecture-overview.md);
data shapes at
[`../reference/database-schema.md`](../reference/database-schema.md);
endpoints at
[`../reference/api-overview.md`](../reference/api-overview.md).

---

## 1. Flow index

| # | Flow | Sister doc with prose |
|---|---|---|
| 1 | Project creation | [`blueprint.md §6.1`](blueprint.md), [`hub-api-deliverables.md`](../reference/hub-api-deliverables.md) |
| 2 | Session lifecycle | [`sessions.md`](sessions.md) |
| 3 | Phase advance | [`../reference/project-phase-schema.md`](../reference/project-phase-schema.md) |
| 4 | Run lifecycle | [`blueprint.md §6.5`](blueprint.md) |
| 5 | Attention item | [`../reference/attention-delivery-surfaces.md`](../reference/attention-delivery-surfaces.md) |
| 6 | Audit event emission | [`../reference/audit-events.md`](../reference/audit-events.md) |
| 7 | Auth + token resolution | [`../reference/permission-model.md`](../reference/permission-model.md), [`../reference/api-overview.md §2`](../reference/api-overview.md) |
| 8 | Cache-first cold start | [`../decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md) |

---

## 2. Flow 1 — Project creation

A director taps "New project" → mobile POSTs `/projects` → hub creates
the row + spawns a steward + audits the action → mobile cache
invalidates and re-renders.

```mermaid
sequenceDiagram
  autonumber
  actor D as Director
  participant M as Mobile
  participant H as Hub
  participant R as Host-runner
  participant SA as Steward (agent)
  participant Cache as HubSnapshotCache

  D->>M: tap "New project"
  M->>H: POST /v1/teams/{team}/projects {name, template_id}
  H->>H: insert project row
  H->>H: hydrate from template (channels, plan, schedules)
  H->>H: emit audit_events (project.create + per-hydrated row)
  H-->>M: 201 {project_id, audit_event_id}
  M->>Cache: invalidate /projects + /audit
  H->>R: queue spawn (general or domain steward)
  R->>R: tmux pane + agent process
  R->>H: PATCH /agents/{id} status=running
  H->>H: emit audit_events (agent.spawn)
  H-->>M: SSE event (audit, agent state)
  M->>D: render new project + steward online
```

Audit chain: `project.create` → `channel.create` (×N) →
`schedule.create` (×N) → `agent.spawn`.

---

## 3. Flow 2 — Session lifecycle

Director taps "Direct Steward" → mobile opens a session via the hub
→ host-runner spawns or resumes the engine → AG-UI events stream back
→ director input loops → close-with-distill writes the artifact.

```mermaid
sequenceDiagram
  autonumber
  actor D as Director
  participant M as Mobile
  participant H as Hub
  participant R as Host-runner
  participant E as Engine (Claude / Codex / Gemini)

  D->>M: tap "Direct Steward"
  M->>H: POST /agents/{id}/sessions {scope, system_prompt_args}
  H->>H: open session row + assemble system prompt
  H->>R: command: open or resume session
  R->>E: spawn or attach (M1 ACP / M2 stream-json)
  E-->>R: lifecycle event (session.init)
  R->>H: POST /agents/{id}/events (lifecycle)
  H-->>M: SSE event (session active)

  loop conversation
    D->>M: type message
    M->>H: POST /agents/{id}/input
    H->>R: forward input
    R->>E: stdin (engine-native)
    E-->>R: tool calls / text / diffs
    R->>H: POST /agents/{id}/events (per AG-UI kind)
    H-->>M: SSE event
    M->>D: render card
  end

  D->>M: close-with-distill (Decision / Brief / Plan / Template)
  M->>H: POST /agents/{id}/sessions/{sid}/archive {distill_kind}
  H->>H: write artifact (document or template)
  H->>H: emit audit_events (session.archive + document.create)
  H-->>M: 200 + artifact_id
```

Replace-keeps-session (v1.0.281+): if the engine process crashes mid-
conversation, the host-runner re-attaches with the same `engine_session_id`
([ADR-014](../decisions/014-claude-code-resume-cursor.md)) and replays
the transcript so far; the same session id continues.

---

## 4. Flow 3 — Phase advance

A steward (or director) advances the project phase → hub checks
acceptance criteria → either advances + audits or returns 409 with the
unmet criteria → mobile cache invalidates.

```mermaid
sequenceDiagram
  autonumber
  participant SA as Steward
  participant M as Mobile
  participant H as Hub
  participant Cache as HubSnapshotCache

  SA->>H: POST /projects/{id}/phase/advance {target_phase}
  Note over H: Resolve current phase + criteria for target
  H->>H: check acceptance_criteria.status (per phase)
  alt all required criteria met
    H->>H: update projects.phase + append phase_history_json
    H->>H: emit audit_events (project.phase_advance + criterion.met×N)
    H-->>SA: 200 {phase, audit_event_ids}
    H-->>M: SSE (phase change)
    M->>Cache: invalidate /projects/{id}, /overview
  else unmet criterion
    H-->>SA: 409 {unmet_criteria: [...], audit_event_id}
  end
```

When the steward auto-marks a criterion (e.g., `metric_threshold`)
the same chain emits `criterion.met` ahead of `project.phase_advance`.
A ratify-prompt attention item may be created (Flow 5) for any
human-gated criteria still outstanding.

---

## 5. Flow 4 — Run lifecycle

Steward registers a run (frozen config + seed + trackio URI) → host-
runner's poller sees it and spawns a worker / mock-trainer → metrics
file accumulates points → host-runner digests + PUTs to the hub →
mobile sparkline animates → run completes + criteria re-evaluate.

```mermaid
sequenceDiagram
  autonumber
  participant SA as Steward
  participant H as Hub
  participant R as Host-runner
  participant W as Worker / trainer
  participant FS as Metric file (trackio / wandb)
  participant M as Mobile

  SA->>H: POST /runs {project_id, config_json, seed, trackio_run_uri}
  H->>H: insert runs row, status=pending
  H->>H: emit audit_events (run.register)

  R->>H: GET /agents/spawns?host_id=...&status=pending
  H-->>R: pending spawn (worker template)
  R->>W: spawn in tmux pane
  W->>FS: writes metric points

  loop every 20s while run is active
    R->>FS: read latest points
    R->>R: downsample to ≤100 points
    R->>H: PUT /runs/{id}/metrics
    H-->>M: SSE event (metric digest)
    M->>M: animate sparkline
  end

  W->>R: process exits (success / failure)
  R->>H: POST /runs/{id}/complete {status, final_metrics_json}
  H->>H: emit audit_events (run.completed)
  H->>H: re-evaluate metric_threshold criteria
  H->>H: maybe emit criterion.met / attention_item.create
  H-->>M: SSE (run state, criteria delta)
```

Audit chain for a successful run: `run.register` →
(many `metric.digest` if instrumented) → `run.completed` →
optionally `criterion.met` / `attention.create`.

---

## 6. Flow 5 — Attention item

A blocker is created (decision needed, approval gated, idle agent,
metric ratify) → SSE pushes to mobile → director acts on Me tab →
resolution audited.

```mermaid
sequenceDiagram
  autonumber
  participant SA as Steward (or hub-internal)
  participant H as Hub
  participant M as Mobile
  actor D as Director

  SA->>H: POST /attention {scope_kind, kind, severity, summary, ref_*}
  H->>H: insert attention_items row, emit audit (attention.create)
  H-->>M: SSE event
  M->>D: badge on Me tab

  D->>M: tap, review context
  M->>H: GET /attention/{id}/context
  H-->>M: surrounding events / referenced rows

  alt director decides
    D->>M: pick choice (approve / reject / redirect)
    M->>H: POST /attention/{id}/decide {choice, comment}
    H->>H: update attention_items.decisions_json + status=resolved
    H->>H: execute pending_payload if present (e.g., gated spawn)
    H->>H: emit audit (attention.decide [+ chained events])
    H-->>M: SSE (resolved)
  else director resolves without choice (info / digest)
    M->>H: POST /attention/{id}/resolve
    H->>H: update status=resolved
    H->>H: emit audit (attention.resolve)
  end
```

For the `permission_prompt` kind (turn-based delivery per
[ADR-011](../decisions/011-turn-based-attention-delivery.md)), the
agent stays in `waiting_attention` state until the director answers;
reaching the agent is via `Resume` / `Retry` after resolution.

---

## 7. Flow 6 — Audit event emission

Every mutation funnels through `recordAudit()` → `audit_events` row +
SSE fan-out + Activity tab feed.

```mermaid
sequenceDiagram
  autonumber
  participant Caller as Hub handler
  participant Audit as recordAudit()
  participant DB as audit_events
  participant Bus as eventbus
  participant M as Mobile

  Caller->>Audit: recordAudit(actor, action, target, meta)
  Audit->>DB: INSERT row (id, ts, actor_*, action, target_*, meta_json)
  Audit->>Bus: publish ae-<id>
  Bus-->>M: SSE event (Activity stream)
  Audit-->>Caller: ae-<id>
  Caller-->>Caller: include in response payload
```

Every handler that mutates state calls `recordAudit()` synchronously
*after* the mutation succeeds; the response includes the
`audit_event_id`. See [`../reference/audit-events.md`](../reference/audit-events.md)
for the action taxonomy and `meta_json` shape per action.

---

## 8. Flow 7 — Auth + token resolution

Each authenticated request is dispatched through middleware that
resolves bearer → token row → actor → role → handler.

```mermaid
sequenceDiagram
  autonumber
  participant C as Client
  participant Mw as Auth middleware
  participant DB as auth_tokens
  participant H as Handler
  participant Audit as recordAudit()

  C->>Mw: GET /v1/... + Authorization: Bearer <plaintext>
  Mw->>Mw: SHA-256(plaintext)
  Mw->>DB: SELECT WHERE token_hash = ?
  alt match
    DB-->>Mw: row (kind, scope_json, expires_at)
    Mw->>Mw: build actor (kind, handle, agent_id?)
    Mw->>Mw: derive role (director / project-steward / general-steward)
    Mw->>H: dispatch with actor + role in context
    H->>H: enforce per-endpoint role gate
    H->>H: do work
    H->>Audit: recordAudit(actor, action, target, meta)
    H-->>C: 2xx + audit_event_id
  else no match / expired / revoked
    Mw-->>C: 401 problem-detail
  end
```

For host-tokens, the host-runner reuses a single token across all
agents on its host; the agent identity is stamped on relayed MCP
calls inside the host-runner's gateway (not by the hub). The audit
row records both the relayed agent's identity and the host-runner's
host id in `meta_json`.

---

## 9. Flow 8 — Cache-first cold start

Mobile launch path: read cache → render → revalidate over network →
apply diff. Per [ADR-006](../decisions/006-cache-first-cold-start.md).

```mermaid
sequenceDiagram
  autonumber
  participant App as Mobile launch
  participant Cache as HubSnapshotCache
  participant UI as ProviderRefs
  participant H as Hub
  actor D as Director

  App->>Cache: read /projects, /me, /hosts
  Cache-->>App: last-known-good rows (with fetched_at)
  App->>UI: hydrate providers
  UI->>D: render with "Last updated <T>" badges
  par revalidate
    App->>H: GET /projects (If-None-Match: <etag>)
    alt content unchanged
      H-->>App: 304 Not Modified
    else content changed
      H-->>App: 200 + new body + new ETag
      App->>Cache: PUT new rows
      App->>UI: invalidate providers
      UI->>D: re-render with fresh data
    end
  end
```

The "Last updated" badge stays visible until the revalidation lands.
On revalidate failure (hub unreachable), the cache stays served and
the banner switches to "Hub offline" — no spinner, no blocking
behaviour.

---

## 10. Cross-cutting timing notes

| Concern | Convention |
|---|---|
| Heartbeat (host-runner → hub) | 10 s |
| Spawn poll (host-runner → hub) | 3 s |
| Metric digest poll (host-runner → trackio file) | 20 s |
| SSE server-side buffer | ≥ 30 s for `Last-Event-ID` resume |
| Mobile revalidation concurrency cap | 6 in-flight per hub |
| Idempotency-Key TTL | 24 h |
| Audit emission ordering | synchronous after mutation, before response |

Retries use exponential backoff (1s → 30s, capped). Mobile mutations
always carry an Idempotency-Key. Host-runner POSTs always do.

---

## 11. Cross-references

- [`blueprint.md`](blueprint.md) — axioms, ontology, protocol layering
- [`agent-lifecycle.md`](agent-lifecycle.md) — per-agent state
- [`sessions.md`](sessions.md) — session ontology and lifecycle (Flow 2)
- [`information-architecture.md`](information-architecture.md) —
  surface architecture
- [`../reference/architecture-overview.md`](../reference/architecture-overview.md)
  — C4 view
- [`../reference/database-schema.md`](../reference/database-schema.md)
  — physical schema
- [`../reference/api-overview.md`](../reference/api-overview.md) —
  endpoint index
- [`../reference/audit-events.md`](../reference/audit-events.md) —
  audit row shape (Flow 6)
- [`../reference/attention-delivery-surfaces.md`](../reference/attention-delivery-surfaces.md)
  — attention surfaces (Flow 5)
- [`../reference/permission-model.md`](../reference/permission-model.md)
  — actor + scope (Flow 7)
- [`../decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md)
  — cache rationale (Flow 8)
