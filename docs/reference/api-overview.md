# API overview

> **Type:** reference
> **Status:** Current (2026-05-08)
> **Audience:** contributors (mobile, hub, integrators)
> **Last verified vs code:** v1.0.435

**TL;DR.** Single canonical entry for "what HTTP endpoints does the
hub expose?" Indexes every `/v1/...` route grouped by resource +
extracts cross-cutting conventions (auth, errors, ETags, idempotency,
pagination) so per-resource detail docs can stop redefining them.

Scope detail lives in per-resource refs:
[`hub-agents.md`](hub-agents.md),
[`hub-api-deliverables.md`](hub-api-deliverables.md),
[`audit-events.md`](audit-events.md),
[`attention-delivery-surfaces.md`](attention-delivery-surfaces.md),
[`rate-limiting.md`](rate-limiting.md). The OpenAPI spec
(`openapi.yaml`, P2.8 in [`../plans/doc-uplift.md`](../plans/doc-uplift.md))
will become the machine-readable form of this index.

---

## 1. Base URL + versioning

```
https://<hub>/v1/...
```

Currently one major API version: `v1`. The hub reports version + commit
metadata at the unauthenticated meta endpoint:

```
GET /v1/_info
→ { "server_version": "1.0.351-alpha",
    "supported_api_versions": ["v1"],
    "schema_versions_supported": [1],
    "commit": "...", "build_time": "...", "modified": false }
```

Mobile checks `server_version` at startup to decide whether to warn the
user about a major-mismatch (see
[`hub-agents.md`](hub-agents.md)). New endpoints land under `/v1/`;
breaking changes would bump to `/v2/`.

---

## 2. Authentication

Every authenticated endpoint requires a bearer token:

```
Authorization: Bearer <token>
```

Tokens resolve to an `actor` (`actor_kind`, `actor_handle`, optional
`agent_id`) — see [`permission-model.md`](permission-model.md). Token
kinds (column `auth_tokens.kind`):

| Kind | Issued to | Capabilities |
|---|---|---|
| `owner` | Hub operator | All endpoints; can mint tokens |
| `user` (`role=principal`) | A director / human teammate | Project + agent CRUD; resolve attention |
| `agent` | A specific agent | Tool surface bound to that agent's identity |
| `host` | A specific host-runner | Register / heartbeat / spawn-list / command-list / agent-patch |

Tokens are stored as SHA-256 hashes; plaintext leaves the issuer once.
Issuance is via CLI:

```
hub-server tokens issue -kind <k> -team <t> -role <r> [-agent-id <id>] [-handle <h>]
```

Role-derived authorization (e.g., `director` vs `project-steward`)
is computed from the actor at request time — see
[`hub-api-deliverables.md §2.2`](hub-api-deliverables.md).

---

## 3. Endpoint groups

Each row links to the per-resource detail doc when one exists.

### 3.1 Meta + system

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/_info` | Server version + commit metadata (unauthenticated) |
| GET | `/v1/search` | Full-text search across events / agent_events |

### 3.2 Identity, hosts, A2A

Detail: [`hub-agents.md`](hub-agents.md), [ADR-003](../decisions/003-a2a-relay-required.md).

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/teams/{team}/hosts` | Register a host (host-runner) |
| GET | `/v1/teams/{team}/hosts` | List hosts |
| GET / DELETE | `/v1/teams/{team}/hosts/{host}` | Get / delete host |
| POST | `/v1/teams/{team}/hosts/{host}/heartbeat` | Heartbeat with build metadata + capabilities |
| GET | `/v1/teams/{team}/hosts/{host}/commands` | Pull queued out-of-band commands |
| PATCH | `/v1/teams/{team}/hosts/{host}/ssh_hint` | Update non-secret SSH hint |
| PUT | `/v1/teams/{team}/hosts/{host}/capabilities` | Update binary-presence map |
| PUT | `/v1/teams/{team}/hosts/{host}/a2a/cards` | Publish per-host A2A agent-cards |
| GET / POST | `/v1/teams/{team}/hosts/{host}/a2a/tunnel/...` | A2A reverse-tunnel relay |
| GET | `/v1/teams/{team}/a2a/cards` | Team-wide A2A directory |
| PATCH | `/v1/teams/{team}/commands/{cmd}` | Update command status (host-runner side) |

### 3.3 Agents + spawning

Detail: [`hub-agents.md`](hub-agents.md), [`../spine/agent-lifecycle.md`](../spine/agent-lifecycle.md).

| Method | Path | Purpose |
|---|---|---|
| POST / GET | `/v1/teams/{team}/agents` | Create + list agents |
| POST | `/v1/teams/{team}/agents/spawn` | Spawn from spec YAML |
| GET | `/v1/teams/{team}/agents/spawns` | List spawn requests |
| GET / PATCH / DELETE | `/v1/teams/{team}/agents/{agent}` | Get / patch / archive |
| GET / POST | `/v1/teams/{team}/agents/{agent}/journal` | Read + append journal |
| POST | `/v1/teams/{team}/agents/{agent}/pause` | Pause |
| POST | `/v1/teams/{team}/agents/{agent}/resume` | Resume |
| GET | `/v1/teams/{team}/agents/{agent}/pane` | Snapshot pane buffer |
| POST / GET | `/v1/teams/{team}/agents/{agent}/events` | Append + list AG-UI events |
| GET | `/v1/teams/{team}/agents/{agent}/stream` | SSE — live AG-UI stream |
| POST | `/v1/teams/{team}/agents/{agent}/input` | Structured input — text, approval, answer, attention_reply, cancel, attach, set_mode/set_model (ADR-021 W2.1), with optional `images:[]` (ADR-021 W4.1) |
| POST | `/v1/teams/{team}/steward.general/ensure` | Idempotently ensure team's general steward exists |
| GET / PUT / DELETE / PATCH | `/v1/teams/{team}/agent-families/{family}` | Agent-family templates |

### 3.4 Sessions

Detail: [`../spine/sessions.md`](../spine/sessions.md), [ADR-009](../decisions/009-agent-state-and-identity.md), [ADR-014](../decisions/014-claude-code-resume-cursor.md).

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/teams/{team}/agents/{agent}/sessions` | Open session |
| GET | `/v1/teams/{team}/agents/{agent}/sessions` | List sessions |
| GET | `/v1/teams/{team}/agents/{agent}/sessions/search` | FTS5 over session events |
| GET / PATCH / DELETE | `/v1/teams/{team}/agents/{agent}/sessions/{session}` | Get / rename / hard-delete |
| POST | `…/sessions/{session}/archive` | Archive (retain history) |
| POST | `…/sessions/{session}/close` | Alias for archive |
| POST | `…/sessions/{session}/fork` | Fork from a cursor |
| POST | `…/sessions/{session}/resume` | Resume with engine-specific cursor |

### 3.5 Projects + lifecycle

Detail: [`hub-api-deliverables.md`](hub-api-deliverables.md), [`project-phase-schema.md`](project-phase-schema.md).

| Method | Path | Purpose |
|---|---|---|
| POST / GET | `/v1/teams/{team}/projects` | Create / list |
| GET / PATCH / DELETE | `/v1/teams/{team}/projects/{project}` | Get / update / archive |
| GET | `/v1/teams/{team}/projects/{project}/sweep-summary` | Computed multi-run summary |
| (lifecycle endpoints) | `…/{project}/phase`, `…/deliverables`, `…/criteria`, `…/overview` | See [`hub-api-deliverables.md`](hub-api-deliverables.md) — ships in [`../plans/project-lifecycle-mvp.md`](../plans/project-lifecycle-mvp.md) W1+ |

### 3.6 Plans + steps + schedules

| Method | Path | Purpose |
|---|---|---|
| POST / GET | `/v1/teams/{team}/projects/{project}/plans` | Create / list |
| GET / PATCH | `…/plans/{plan}` | Get / update |
| POST / GET | `…/plans/{plan}/steps` | Create / list steps |
| PATCH | `…/plans/{plan}/steps/{step}` | Update step status |
| POST / GET / PATCH / DELETE | `/v1/teams/{team}/schedules` | CRUD schedules (cron / manual / on_create) |
| POST | `/v1/teams/{team}/schedules/{id}/run` | Manual fire |

### 3.7 Runs (experiments) + metrics

| Method | Path | Purpose |
|---|---|---|
| POST / GET | `/v1/teams/{team}/runs` | Create / list |
| GET | `…/runs/{run}` | Get one |
| POST | `…/runs/{run}/complete` | Mark completed / failed |
| POST | `…/runs/{run}/metric_uri` | Attach trackio / wandb URI |
| PUT / GET | `…/runs/{run}/metrics` | Put / get scalar series |
| POST / GET | `…/runs/{run}/images` | Image-series checkpoints |
| PUT / GET | `…/runs/{run}/histograms` | Histogram-series checkpoints |

### 3.8 Documents, reviews, artifacts, blobs

| Method | Path | Purpose |
|---|---|---|
| GET / POST | `/v1/teams/{team}/projects/{project}/documents` | List / create |
| GET | `…/documents/{doc}` | Get one |
| GET | `…/documents/{doc}/versions` | Version history |
| GET / POST | `/v1/teams/{team}/projects/{project}/reviews` | List / create |
| GET | `…/reviews/{review}` | Get one |
| POST | `…/reviews/{review}/decide` | Approve / request-changes / reject |
| GET / POST | `/v1/teams/{team}/projects/{project}/artifacts` | List / create |
| GET | `…/artifacts/{artifact}` | Get one (metadata + URI) |
| POST | `/v1/teams/{team}/blobs` | Upload small attachment |
| GET | `/v1/teams/{team}/blobs/{sha}` | Download by sha256 |

Project-scoped doc tree: `GET /…/projects/{project}/docs` and
`GET /…/projects/{project}/docs/*` for read-only browsing.

### 3.9 Channels + events

Detail: [ADR-019](../decisions/019-channels-as-event-log.md).

| Method | Path | Purpose |
|---|---|---|
| POST / GET | `/v1/teams/{team}/projects/{project}/channels` | Create / list |
| GET | `…/channels/{channel}` | Get one |
| POST / GET | `…/channels/{channel}/events` | Append / list events |
| GET | `…/channels/{channel}/stream` | SSE — live events |
| POST / GET | `/v1/teams/{team}/channels` (team-scope) | Same shape, team scope |
| GET | `…/channels/{channel}/events`, `…/stream` | List / stream |

### 3.10 Tasks

| Method | Path | Purpose |
|---|---|---|
| POST / GET | `/v1/teams/{team}/projects/{project}/tasks` | Create / list |
| GET / PATCH | `…/tasks/{task}` | Get / patch |

### 3.11 Attention items

Detail: [`attention-delivery-surfaces.md`](attention-delivery-surfaces.md), [`attention-kinds.md`](attention-kinds.md).

| Method | Path | Purpose |
|---|---|---|
| POST / GET | `/v1/teams/{team}/attention` | Create / list |
| GET | `…/attention/{id}/context` | Surrounding events for triage |
| POST | `…/attention/{id}/resolve` | Resolve without choice (info / digest) |
| POST | `…/attention/{id}/decide` | Resolve with explicit choice |

### 3.12 Audit + admin

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/teams/{team}/audit` | List `audit_events` (filtered, paginated) |
| GET / PUT | `/v1/teams/{team}/policy` | Get / update team policy |
| GET / POST | `/v1/teams/{team}/auth-tokens` | List / issue tokens |
| POST | `…/auth-tokens/{id}/revoke` | Revoke a token |
| GET | `/v1/teams/{team}/principals` | List active principals (humans + agents) |

### 3.13 Templates

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/teams/{team}/templates` | List bundled + user templates |
| GET / PUT / DELETE / PATCH | `/v1/teams/{team}/templates/{category}/{name}` | CRUD on a template |

### 3.14 MCP gateway

| Method | Path | Purpose |
|---|---|---|
| POST | `/mcp/{token}` | JSON-RPC entry point — host-runner relays agent calls here |

Detail: [ADR-002](../decisions/002-mcp-consolidation.md) (single MCP
service consolidation).

---

## 4. Cross-cutting conventions

These apply to every endpoint unless an endpoint's per-resource doc
overrides them. The lifecycle deliverables doc
([`hub-api-deliverables.md §2`](hub-api-deliverables.md)) is the
canonical location for the lifecycle-endpoint subset; this section is
authoritative for the rest of the surface.

### 4.1 Content type

- Request: `application/json` for bodies; form-encoded rejected.
- Response: `application/json`; errors are RFC 7807 problem-detail.

### 4.2 Error format (RFC 7807)

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

| Code | Meaning |
|---|---|
| 200 / 201 / 204 | Success |
| 400 | Malformed request (validation) |
| 401 | Missing / invalid token |
| 403 | Token valid, role insufficient |
| 404 | Resource not found |
| 409 | State conflict (e.g., advancing phase when criteria unmet) |
| 412 | Precondition failed (`If-Match` mismatch) |
| 422 | Semantic validation failure |
| 429 | Rate-limited (see `rate-limiting.md`) |
| 5xx | Hub-side fault |

### 4.3 Idempotency

Mutating endpoints honor `Idempotency-Key: <client-generated-uuid>`.
Two requests with the same key + same body return the same response
without re-executing. Hub stores keys for 24h.

Idempotency is required for any mutation initiated from a flaky
network — host-runner POSTs (heartbeat, spawn patch, metric digest)
should always include a key.

### 4.4 Pagination

List endpoints return:

```json
{ "items": [...], "next_cursor": "<opaque>", "total_estimate": 42 }
```

`?cursor=<opaque>&limit=N`. Default 50, max 200. Cursor is server-
defined opaque; sort order is endpoint-specific.

### 4.5 Caching + ETags

GET responses include `ETag` + `Last-Modified`. Mobile sends
`If-None-Match` for conditional requests; the hub returns `304 Not
Modified` when content is unchanged. `Cache-Control` per endpoint:

- High-churn (composed overviews, agent stream) — `private, max-age=15`
- Stable (templates, schedules) — `private, max-age=300`
- Authoritative current-state (project, plan) — `no-cache, must-revalidate`

### 4.6 Audit emission

Every successful mutation emits one or more `audit_events` rows. The
primary event id appears in the response as `audit_event_id`; multi-
event responses use `audit_event_ids: [...]`. See
[`audit-events.md`](audit-events.md) for the row shape and the canonical
event-kind taxonomy.

### 4.7 SSE streams

SSE endpoints (`.../stream`) deliver line-delimited events:

```
id: <event-id>
event: <kind>
data: <json>

```

Mobile reconnects with `Last-Event-ID: <id>` to resume from a known
cursor. Servers should buffer ≥30 s of events for resumption; older
gaps require a follow-up GET on the underlying list endpoint.

### 4.8 Rate limiting

Per-token + per-endpoint limits live in
[`rate-limiting.md`](rate-limiting.md). Exceeding returns `429` with
`Retry-After`.

---

## 5. Versioning policy

- **Additive changes** (new endpoint, new optional field, new event
  kind) — non-breaking; ship under `/v1/`.
- **Breaking changes** — bump to `/v2/` and run both surfaces during
  the migration window. The mobile client checks `/v1/_info`'s
  `supported_api_versions` at startup and refuses to launch if its
  required version isn't listed.
- **Deprecation** — mark in the per-resource doc and emit a
  `Deprecation` header on the affected endpoint.

---

## 6. Webhooks

Out of MVP scope. Placeholder for post-MVP outbound delivery (e.g.,
push notifications via FCM / APNs, third-party integrations).

---

## 7. Cross-references

- [`architecture-overview.md`](architecture-overview.md) — C4 view
- [`database-schema.md`](database-schema.md) — physical schema
- [`hub-agents.md`](hub-agents.md) — agent lifecycle endpoints in depth
- [`hub-api-deliverables.md`](hub-api-deliverables.md) — lifecycle
  endpoints; canonical for `§2 Conventions` on that surface
- [`audit-events.md`](audit-events.md) — `audit_events` schema +
  event-kind catalogue
- [`attention-delivery-surfaces.md`](attention-delivery-surfaces.md) —
  attention API + UI surfaces
- [`rate-limiting.md`](rate-limiting.md) — per-token quotas
- [`permission-model.md`](permission-model.md) — actor / role
  resolution
- [`../decisions/002-mcp-consolidation.md`](../decisions/002-mcp-consolidation.md)
  — MCP entry point
- `hub/internal/server/server.go` — authoritative routing
