# 038. Per-run event digest — a canonical run summary + navigation index

> **Type:** decision
> **Status:** Proposed (2026-06-01) — the data-model substrate for the
> agent-run **analysis mode** (see
> [`plans/agent-run-analysis-mode.md`](../plans/agent-run-analysis-mode.md)).
> Scoped to the MVP slice: a per-agent digest computed at a watermark, read
> by both the mobile analysis surface and `/v1/insights`. Multi-agent
> session rollup, incremental sealing, and Parquet/OTLP export are deferred
> (see [the transcript-as-dataset discussion](../discussions/transcript-paging-vs-forum-model.md)).
> **Audience:** contributors
> **Last verified vs code:** v1.0.783

**TL;DR.** Per-run insight is recomputed ad hoc on every surface, with
inconsistent definitions, over inconsistent data scopes — so the same run
reports different numbers in different places, and there is no count or
structure index to navigate a long log accurately. This ADR introduces a
**per-run event digest**: a materialized read model (`agent_event_digests`)
computed in one pass over the frozen event range at an idle/terminal
**watermark**, holding the canonical overview stats **and** the navigation
anchors (the `seq` of every error / tool / turn, plus the total count). It
is the single source of truth that both the analysis UI and `/v1/insights`
read. *Distinct from `run_metrics` (ADR-era migration `0014`), which digests
host-owned numeric curves; this digests the hub-owned structural event log.*

## Context

`agent_events` (migration `0011`) is an append-only, per-agent log
(`UNIQUE(agent_id, seq)`, `seq` monotonic per agent). It is consumed two
ways — as a **live stream** (SSE tail-follow, keyset cursor) and as a
**bounded dataset** to be summarized and navigated. We have only ever served
the second from the first, and it shows. An audit (recorded in the
discussion doc §14) found:

- **"Error" means two disjoint things.** The transcript Errors lens
  (`agentEventIsError`, `lib/widgets/agent_feed/feed_reducer.dart:1000`)
  counts `kind=='error'` ∪ `tool_result.is_error` ∪ a failed `tool_call`.
  `/v1/insights` (`readInsightsErrors`,
  `hub/internal/server/handlers_insights.go:546`) counts failed turns
  (`turn.result.status != 'success'`) ∪ open attention ∪ a hardcoded-0
  disconnect count. The sets are disjoint; the surfaces disagree.
- **Per-run telemetry counts only the loaded window.** The inline
  TelemetryStrip sums `turnCount` / `modelTotals` over `_events`
  (`lib/widgets/agent_feed.dart:1238`) — the lazily-loaded slice — so turns
  and tokens undercount on long, partially-loaded runs.
- **No count, no structure index.** There is no maintained total or
  per-structure ordinal, so a position indicator cannot be monotonic
  ("event N of M") and "jump to error/turn/tool k" has nothing to seek to.

The root cause is the absence of a **canonical, full-scope per-run summary**.
The right shape (discussion §§7–13) is to recognise the cold/sealed regime:
the immutability we need is "events ≤ watermark never change" (always true of
an append-only log), so any prefix is analyzable at any **watermark**, and we
persist a digest at natural points (idle, terminal).

## Decision

Introduce **`agent_event_digests`**, a per-agent materialized read model.

1. **Schema (new migration).** Keyed by `agent_id`, carrying `team_id` for
   isolation (ADR-037). Columns (JSON for the structured rollups, SQLite
   pure-Go friendly):
   - `agent_id` (PK), `team_id`, `schema_version`, `computed_at`;
   - `watermark_seq` — the max `seq` this digest covers (the consistent cut);
   - `event_count`, `turn_count`, `first_ts`, `last_ts`, `duration_ms`;
   - `cost_usd`, `by_model_json` (per-model token + cost);
   - `error_count`, `errors_json` — taxonomy `class → {count, sample_seqs[]}`;
   - `tool_total`, `tool_failed`, `tools_json` — `name → {calls, failed,
     sample_seqs[]}`;
   - `turn_index_json` — the `seq` of each turn anchor (jump targets);
   - `outcome` — terminal status / last `turn.result` status (best-effort).
   The `*_seqs` arrays + `event_count` + `turn_index_json` are the
   **navigation anchors**: monotonic position is `rank(seq)/event_count`;
   "jump to error/turn/tool k" seeks to a stored `seq` via the existing
   convergent index seek (v1.0.782–783).

2. **Canonical "error" definition (the reconciliation).** The digest's error
   set is the **union**: `kind=='error'` ∪ `tool_result.is_error==true` ∪ a
   `tool_call` whose resolved result/update failed ∪ `turn.result.status !=
   'success'`. This superset is what the director sees in the log. The hub
   (Go) computes it at digest time and is the **source of truth**;
   `/v1/insights` reads the digest's `error_count` instead of its current
   private query, and the mobile lens reads the digest for a sealed run
   (computing locally only for the live tail beyond the watermark). One
   definition, one number everywhere.

3. **Compute triggers (watermark).** Recompute the digest wholesale in one
   O(n) pass over `[base, watermark_seq = max(seq)]`:
   - on **idle** — the `onPreAgentIdle` hook (`loop_hooks.go:105`), when the
     tail is stable;
   - on **terminal** — the stop/terminate path (`stopSessionInternal`,
     `stop_session.go:55`).
   Wholesale recompute is acceptable for the MVP: runs are bounded and these
   transitions are infrequent. Incremental maintenance on the write path is
   **deferred** (a hot-path optimization, not needed for correctness).

4. **Read surface.** `GET /v1/teams/{team}/agents/{agent}/digest` returns the
   digest as JSON (the mobile app reads it as a `Map`, per house style). A
   later MCP `transcript.summary` tool (ADR-B / plan "Later") wraps the same
   row for stewards. `/v1/insights?agent_id=X` is refactored to read the
   digest rather than re-scan events.

5. **Scope (MVP).** Per **agent**. A resumed session spans multiple agents;
   the **per-session rollup + dense cross-agent ordinal is deferred** — the
   per-agent digest already fixes the accuracy bugs and powers analysis mode
   for the common single-agent case.

## Consequences

**Positive.**
- One canonical per-run summary; the transcript, the analysis screen, and
  `/v1/insights` reconcile by construction.
- Full-run scope (computed over the frozen range, not the loaded window) —
  counts stop undercounting.
- The count + anchors enable a **monotonic position** ("event N of M") and
  **accurate structure navigation** (jump to error/turn/tool), closing the
  recurring position-indicator complaint at its root.
- Foundation for the analysis mode (plan) and, later, Parquet/OTLP export and
  the MCP summary/query tools.

**Negative / costs.**
- A new table + two write-path hooks; an O(n) recompute at idle/terminal
  (bounded; mitigated by infrequency).
- **Canonical-error duplication risk**: the union must match between Go
  (digest) and the Dart lens. Mitigation: the digest is the source of truth —
  the lens reads it for sealed runs and only computes locally for the live
  tail; a shared test vector pins both.
- **Staleness**: a live run's digest is as-of its last idle watermark. The UI
  must label it ("as of <ts>" + refresh) and analysis mode is offered for
  idle/terminated runs, where the tail isn't moving.

## Alternatives considered

- **Compute on read** (status quo, generalized): O(n) per mobile open over a
  100k-event log, and either window-limited (wrong) or full-scan (slow) —
  rejected.
- **Extend `run_metrics`**: wrong axis (numeric curves) and wrong owner
  (host-owned bulk, hub stores a downsampled digest); the event digest is
  hub-owned and structural — rejected (a new table, §1).
- **Client-side only**: cannot reconcile across surfaces and is window-
  limited; the canonical definition must live server-side — rejected.
- **Seal only at terminal state**: leaves long-running and idle agents with
  no summary; the watermark generalization (idle + terminal) is strictly
  better — adopted.

## Open questions

- Per-session rollup + dense cross-agent ordinal (deferred; needed for
  multi-agent resumed sessions).
- Incremental digest maintenance vs. wholesale recompute (deferred
  optimization).
- Whether `/v1/insights` non-agent scopes (team/project/fleet) should sum
  digests or keep their own scan (the digest is per-agent; fleet aggregation
  is a separate roll-up).
