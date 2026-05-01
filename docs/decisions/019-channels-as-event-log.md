# 019. Channels are append-only event logs with `task_id` / `correlation_id`, not a separate exchanges table

> **Type:** decision
> **Status:** Accepted (2026-04-19) — back-dated from when the schema was committed
> **Audience:** contributors
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** Hub's primary cross-agent / cross-time data model is `events` rows belonging to `channels`. Each event is append-only; correlation between an A2A request and its reply, or between a task and its events, uses optional `task_id` and `correlation_id` fields rather than a separate `exchanges` or `threads` table. This decision was made implicitly when the initial schema (`0001_initial.up.sql`) shipped on 2026-04-19; this ADR back-documents the reasoning so future "let's add an exchanges table" or "let's split A2A traffic into its own model" PRs are easier to evaluate against the original intent.

---

## Context

A multi-agent system needs to model several adjacent concepts:

- **Free messages** — the steward chats with the director, an agent posts a status update.
- **Tasks** — a director-or-steward-issued unit of work assigned to an agent.
- **A2A requests and replies** — a steward calls another agent's tool; the reply needs to be routed back.
- **Audit trail** — who did what, when, for the principal's review.
- **Filterable views** — "show me everything related to task T," "show me the conversation in channel C."

Two natural shapes exist for this data:

### Shape A — multiple specialized tables (rejected)

`channels` (room metadata) + `channel_messages` (free chat) + `tasks` (work items) + `task_results` (replies) + `a2a_exchanges` (request/response pairs) + `audit_events` (audit trail). Each table has its own schema, indexes, and renderer.

Pros: each table is shaped to its concept; queries within a concept are simple; schema documents the intent.

Cons: queries *across* concepts (give me everything about a project, ordered by time) need union-all joins; rendering a channel needs to interleave free messages with task replies which originate in different tables; correlation across concepts (this A2A reply is about that task) needs cross-table foreign keys.

### Shape B — one event log per channel with optional correlation (adopted)

`channels` (room metadata, scope = team or project) + `events` (append-only, every cross-channel-or-cross-agent thing is an event row). Events carry optional `task_id` and `correlation_id` to wire concepts together without separate tables.

Pros: one table holds the cross-time, cross-agent record. Channel rendering is one query (`SELECT * FROM events WHERE channel_id = ? ORDER BY received_ts`). Filtering by task is a simple WHERE. New concept categories add a `type` field, not a new table. FTS5 indexes one table.

Cons: schema is generic (`type`, `parts_json`, `metadata_json` carry the meaning); the polymorphism is in the application, not the schema. Queries that *only* care about one concept are slightly less direct.

---

## Decision

**D1. `channels` is the room; `events` is the content.** A channel scopes a stream of events. Channels have `scope_kind ∈ {team, project}` plus a name; events belong to exactly one channel.

**D2. Events are append-only.** No `events.update` operation. To "edit" an event, post a new event with `metadata_json: {"edits": "<original_id>"}` and let the renderer fold it. This preserves the audit trail.

**D3. Correlation via optional fields, not separate tables.**

```sql
CREATE TABLE events (
    id                TEXT PRIMARY KEY,
    ts                TEXT NOT NULL,
    received_ts       TEXT NOT NULL,
    channel_id        TEXT NOT NULL,
    type              TEXT NOT NULL,
    from_id           TEXT,           -- agent emitting; null for principal events
    to_ids_json       TEXT,           -- recipients
    parts_json        TEXT,           -- AG-UI part array
    task_id           TEXT,           -- present when this event relates to a task
    correlation_id    TEXT,           -- present when paired with another event (A2A request↔reply)
    metadata_json     TEXT
);
CREATE INDEX idx_events_task ON events(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_events_correlation ON events(correlation_id) WHERE correlation_id IS NOT NULL;
```

A2A request/reply pair: the request event has `correlation_id = <new ULID>`; the reply event has the same `correlation_id`. Filter `WHERE correlation_id = ?` to retrieve the pair.

A task and its events: events emitted while working on a task carry that task's id. Filter `WHERE task_id = ?` for the task's history.

**D4. `type` field carries the polymorphism.** Known types include `message` (free text), `tool_call`, `tool_result`, `attention.*` (cross-reference to attention items), `a2a.invoke`, `a2a.reply`, `task.create`, `task.update`, `agent.spawn`, etc. Renderers and filters branch on `type`. Adding a type does not require a schema migration.

**D5. Audit log lives in the same table.** There is no separate `audit_events` table for events that are emitted by agents. (There *is* a separate primitive — see [memory: project_activity_feed_foundation](../../.claude/projects/-home-ubuntu-mux-pod/memory/project_activity_feed_foundation.md) — for system-level audit actions like `project.create`, `template.edit`, `policy.update` that don't have a natural channel. That table is `audit_events`, distinct from `events`. The two co-exist; activity feed UI joins them at render time.) Cross-link to ADR-016: governance is structural, not log-based, but the log is read-side.

**D6. FTS5 over the unified table.** Search indexes one virtual table (`events_fts`) populated from every event's text parts. A unified search across messages, tool calls, A2A traffic, and replies works because they're one table.

---

## Consequences

**Becomes possible:**
- Single render path for any channel — render is one ORDER BY query.
- Cross-concept queries like "everything about task T, in time order" or "the A2A round-trip whose request has correlation X" are simple WHERE clauses.
- New event types ship as application logic, not schema migrations.
- Search is unified.

**Becomes harder:**
- Queries that genuinely need only one concept (e.g. "list all tasks in this project") still hit `events` and filter on `type` + a specific task-related field. The index on `task_id` mitigates the cost; if it stops being enough, a denormalized `tasks` lookup table can be added without changing this ADR (the events log stays authoritative).
- Schema-level invariants on a concept ("a tool_result must always reference an existing tool_call") are enforced in application code rather than foreign keys. Worth a CI check (per-type validators run against a pinned event corpus).

**Becomes forbidden:**
- A separate `exchanges` / `threads` / `task_results` table that duplicates events. If a future need arises that this model can't satisfy, *prove it can't* before adding storage.
- Mutating an event row in place. Every change is a new event with `metadata_json` carrying the relationship (`edits`, `redacts`, `corrects`).
- Cross-table foreign keys from a hypothetical specialized table back to events. The events table is the spine; nothing else owns its rows.

---

## Migration

This ADR back-documents `0001_initial.up.sql` (2026-04-19). No schema migration. Future schema changes that touch the events ↔ correlation pattern should reference this ADR as the source-of-truth for *why* we don't split.

For new contributors:

- Adding a new event type: pick a type string (lowercase, dot-separated namespace), document its `parts_json` shape in `reference/audit-events.md` (T3-F backfill), and emit. No migration.
- Adding a new correlation pattern: prefer reusing `correlation_id`. Adding a new column is a real migration; only do it if the new pattern can't fit the existing fields.

---

## References

- Schema: `hub/migrations/0001_initial.up.sql` (channels + events + indexes).
- Code: `hub/internal/server/handlers_events.go`, `handlers_channels.go`, `handlers_search.go`.
- [Reference: audit-events](../reference/audit-events.md) — the *separate* `audit_events` primitive for system-level actions (D5).
- [ADR-007](007-mcp-vs-a2a-protocol-roles.md) — the protocol roles whose traffic lands in this table.
- [ADR-008](008-orchestrator-worker-slice.md) — the orchestrator-worker pattern that emits A2A request/reply pairs.
