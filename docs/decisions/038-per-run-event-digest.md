# 038. Per-run event digest, turn index, and the OTLP projection

> **Type:** decision
> **Status:** Proposed (2026-06-01) — the data-model substrate for the
> agent-run **analysis mode** (see
> [`plans/agent-run-analysis-mode.md`](../plans/agent-run-analysis-mode.md))
> and the operator-facing **OTLP trace** export. Revised after discussion to
> pull session rollup and incremental maintenance into MVP and to make turns
> first-class (so traces are a direct projection, not a synthesis). Multi-
> backend deployment and Parquet/OLAP export remain post-MVP.
> **Audience:** contributors
> **Last verified vs code:** v1.0.783

**TL;DR.** Per-run insight is recomputed ad hoc on every surface, with
inconsistent definitions, over inconsistent data scopes — so the same run
reports different numbers in different places, there is no count or structure
index to navigate a long log accurately, and there is no clean way to emit
traces. This ADR introduces three linked things: (1) a **per-agent event
digest** (`agent_event_digests`) — scalar rollups + a canonical error count +
a mergeable latency histogram — **maintained incrementally** on event append;
(2) a first-class **turn model** (`turn.start`/`turn.result` correlated by
`turn_id`) materialized as a queryable **turn index** (`agent_turns`) that
serves both mobile jump-to-turn *and* OTLP spans; (3) an **OTLP projection**
(trace = session, span = turn, child span = tool call) the hub can emit to an
operator's trace backend. A **session** view is the ts-ordered rollup of its
agents' digests. *Distinct from `run_metrics` (migration `0014`), which
digests host-owned numeric curves; this digests the hub-owned event log.*

## Context

`agent_events` (migration `0011`) is an append-only, per-agent log
(`UNIQUE(agent_id, seq)`, `seq` monotonic per agent, plus a nullable
`session_id` indexed `(session_id, ts)`). It is consumed two ways — a **live
stream** (SSE tail, keyset cursor) and a **bounded dataset** to summarize,
navigate, and analyze. We only ever served the second from the first.

An audit (discussion doc §14) found per-run insight is not merely thin but
**wrong**: the transcript Errors lens (`agentEventIsError`,
`lib/widgets/agent_feed/feed_reducer.dart:1000`) and `/v1/insights`
(`readInsightsErrors`, `hub/internal/server/handlers_insights.go:546`) use
**disjoint** definitions of "error"; per-run telemetry is summed over the
lazily-loaded window only (`lib/widgets/agent_feed.dart:1238`); and there is
no maintained count or structure index, so a position bar can't be monotonic
and "jump to turn/error/tool" has nothing to seek to. Three further facts
from the schema shape the design:

- **Sessions are multi-agent.** `agents` has no `session_id`; the only link
  is `agent_events.session_id`. A resumed session spans several agents
  (`DISTINCT agent_id … WHERE session_id=?`), and the session transcript is
  already the **ts-ordered union** across them (session paging uses
  `before_ts`, not `seq` — `handlers_agent_events.go:240`). The mobile user
  sees the **session**, not the agent.
- **There is no turn boundary event today** — only `turn.result` (210
  occurrences; zero `turn.start`). A turn's *duration* is carried on
  `turn.result.duration_ms`, but its *start anchor in the log* is implicit.
- **Idle/terminal-only digesting is too stale.** Every turn leaves a window
  where the digest lags until the next idle — so the digest must be
  maintained incrementally, not only at a watermark.

## Decision

### 1. `agent_event_digests` — a per-agent materialized read model

Keyed by `agent_id`, carrying `team_id` (ADR-037 isolation). Columns:

- `agent_id` (PK), `team_id`, `schema_version`, `updated_at`;
- `watermark_seq` — max `seq` folded so far (the consistent cut);
- `event_count`, `turn_count`, `first_ts`, `last_ts`, `duration_ms`;
- `cost_usd`, `by_model_json` (per-model tokens + cost);
- `error_count`, `errors_json` — taxonomy `class → {count, sample_seqs[]}`;
- `tool_total`, `tool_failed`, `tools_json` — `name → {calls, failed,
  sample_seqs[]}`;
- `latency_hist_json` — a **fixed-bucket turn-latency histogram** (so
  percentiles *merge* across agents; see §5);
- `outcome` — terminal status / last `turn.result` status (best-effort).

**Canonical "error" (the reconciliation).** The error set is the **union**:
`kind=='error'` ∪ `tool_result.is_error==true` ∪ a `tool_call` whose resolved
result/update failed ∪ `turn.result.status != 'success'`. This superset is
what the director sees in the log. The hub (Go) is the **source of truth**;
`/v1/insights` and the mobile lens read it (the lens computes locally only
for the live tail beyond `watermark_seq`). A shared test vector pins the Go
and Dart implementations together.

### 2. Incremental maintenance (primary), watermark reconcile (checkpoint)

Fold each event into its agent's digest **in the same transaction as the
`agent_events` insert** (`handlers_agent_events.go` POST). All hot fields are
O(1): `event_count++`; on `turn.result` → `turn_count++`, `cost +=`, merge
`by_model`, add `duration_ms` to `latency_hist`; on a union-matching event →
`error_count++` (+ sample seq); on `tool_call` → `tool_total++`; on
`tool_result.is_error` → `tool_failed++`, attributed to the tool via the
`tool_call` id pairing. A `tool_call`'s failure is known only when its
`tool_result` arrives, so `error_count` is **eventually consistent within the
run** (correct by turn end) — the same resolution the mobile lens uses.

The watermark hooks — `onPreAgentIdle` (`loop_hooks.go:105`) and terminal
(`stopSessionInternal`, `stop_session.go:55`) — become a **reconcile +
finalize-outcome** checkpoint (a full O(n) recompute that corrects any drift
and seals the outcome), not the primary path. Historical agents with no
digest are backfilled lazily on first read.

### 3. First-class turns — `turn.start` + the `agent_turns` index

Make a **turn** an explicit, correlated span of the log:

- **Event contract.** A turn is bracketed by `turn.start {turn_id, ts}` and
  `turn.result {turn_id, ts, status, cost_usd, by_model, duration_ms}`
  (`turn.result` gains `turn_id`). Drivers emit `turn.start` natively as they
  adopt it; until then the hub **synthesizes** a turn (boundary = the first
  event after the prior `turn.result`; `turn_id` synthetic) so the index
  exists for every engine. Tool/assistant events belong to the turn whose
  `[start, result]` ts-window encloses them (or by carried `turn_id`).
- **`agent_turns` child table — the turn index.** One row per turn:
  `(agent_id, turn_id)` PK, `team_id`, `idx` (0-based per agent),
  `start_seq`, `start_ts`, `end_seq`, `end_ts`, `duration_ms`, `status`,
  `cost_usd`, `in_tokens`, `out_tokens`, `tool_count`, `tool_failed`,
  `error_count`; indexed `(agent_id, idx)` and `(agent_id, start_seq)`.
  Maintained incrementally alongside the digest (open a row on `turn.start`/
  first event, close it on `turn.result`).

This one structure serves both consumers: **navigation** ("jump to turn k" →
`start_seq`; the session timeline = the union of agents' turns ordered by
`start_ts`) and **OTLP** (each row is a span, §4). A child table (not a JSON
blob) keeps it queryable and paginated for long runs.

### 4. OTLP projection — `agent_turns` → spans (operator-facing)

The hub runs an **optional OTLP exporter** (`--otlp-endpoint`, off by
default — the hub already stores the events; a backend buys query/viz UX, not
storage). It is a direct projection, no synthesis guesswork:

- **Trace = session.** `trace_id = sha256(session_id)[:16]` (one trace per
  session; spans from every agent of a resumed session share it).
- **Turn span.** `span_id = sha256(session_id|turn_id)[:8]`; name `turn {idx}`;
  **timing `[end_ts − duration_ms, end_ts]`** (accurate from `turn.result`
  even before native `turn.start`; `turn.start.ts` refines the log anchor and
  the tool-grouping window); status from `turn.result.status`; attributes
  follow **OTel GenAI conventions** — `gen_ai.system = agent.kind`,
  `gen_ai.request.model`, `gen_ai.usage.input_tokens` / `output_tokens`,
  `cost_usd`.
- **Tool span (child of its turn).** `span_id = sha256(session_id|tool_call_id)[:8]`;
  timing `[tool_call.ts, tool_result.ts]`; status from `is_error`; name = tool.
- **Errors** → span events (exception) on the enclosing turn/tool span.
- **Deterministic IDs** → re-export is idempotent (backends dedupe by id).
- Export a run at terminal (and optionally per-turn for live operator
  tracing). The operator points `--otlp-endpoint` at Phoenix / Jaeger / etc.

### 5. Reads, sessions, and insights

- **Session digest = ts-ordered rollup of its agents' digests.** Computed on
  read by summing/merging the per-agent rows for `DISTINCT agent_id … WHERE
  session_id=?` (counts sum, taxonomies merge, latency histograms add, turn
  indices concatenate ordered by `start_ts`). Mobile (session-centric) reads
  this; the session position "event N of M" is the **ts-rank** across the
  union (no new cross-agent ordinal needed — ts *is* the session order).
- **Read endpoints.** `GET /v1/teams/{team}/agents/{agent}/digest` and
  `…/sessions/{session}/digest` (the rollup). A later MCP `transcript.summary`
  wraps the same data.
- **`/v1/insights` sums digests.** Non-agent scopes (team/project/fleet/
  engine/host) become a **sum/merge of the in-scope per-agent digests**
  instead of an event scan — O(#agents) not O(#events), and the error number
  is now the canonical union (so insights and the transcript reconcile).
  Percentiles come from merging the per-agent **latency histograms** (§1).

## Consequences

**Positive.**
- One canonical per-run summary; transcript, analysis screen, and
  `/v1/insights` reconcile by construction; full-run scope (no window
  undercount).
- Count + turn index enable a **monotonic position** and **accurate
  structure navigation** (jump to turn/error/tool), at session granularity.
- The turn index doubles as the **OTLP span tree**, so operator tracing is a
  direct projection — no fragile boundary synthesis.
- Incremental maintenance keeps the digest fresh per turn (no idle lag).
- Foundation for Parquet/OLAP export and MCP summary/query (post-MVP).

**Negative / costs.**
- New tables (`agent_event_digests`, `agent_turns`) + an extra in-tx
  `UPDATE`/turn-row write per event (fine at these volumes; SQLite single
  writer). A reconcile recompute at idle/terminal.
- **Canonical-error duplication** across Go and Dart — mitigated by the
  digest being source of truth + a shared test vector.
- **`turn.start` adoption is per-driver work**; synthesis covers the gap, and
  span timing is accurate from `duration_ms` regardless.
- **Staleness**: a live run's digest is as-of `watermark_seq`; the UI labels
  it and analysis mode is offered for idle/terminal runs.

## Alternatives considered

- **Compute on read** (status quo, generalized): O(n) per mobile open, window-
  limited or full-scan — rejected.
- **Extend `run_metrics`**: wrong axis (numeric curves), wrong owner (host
  bulk) — rejected (new tables).
- **Client-side only**: can't reconcile across surfaces, window-limited —
  rejected.
- **Synthesize OTLP spans at export from `turn.result` alone**: works for
  timing but gives no log anchor and brittle tool grouping — superseded by
  the first-class turn model (synthesis kept only as the `turn.start`
  fallback).
- **turn_index as a JSON blob on the digest**: not queryable/paginable for
  long runs and awkward for OTLP — rejected in favor of `agent_turns`.

## Open questions

- **Latency histogram shape** — fixed log-scale buckets vs. a t-digest sketch
  (both merge; buckets are simpler, t-digest is more accurate at the tails).
- **`turn.start` rollout order** across engines (claude-code M4 first; others
  follow) and whether to record it in `docs/spine/protocols.md` §event-vocab.
- **OTLP export cadence** — terminal-only vs. per-turn live operator tracing
  (the latter wants a streaming exporter).
- **Tool→turn association** when `turn_id` isn't carried on tool events
  (ts-window grouping is the fallback; carrying `turn_id` is cleaner).
