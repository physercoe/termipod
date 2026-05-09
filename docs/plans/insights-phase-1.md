# Insights Phase 1 — hub stats + project Insights + relay throughput

> **Type:** plan
> **Status:** Done (2026-05-09)
> **Audience:** contributors
> **Last verified vs code:** v1.0.456

**TL;DR.** [ADR-022](../decisions/022-observability-surfaces.md)
Phase 1 ships three wedges: W1 (`/v1/hub/stats` endpoint + Hub tile
group on Hosts tab), W2 (project-scoped Insights on Project Detail
with Tier-1 spend / latency / reliability), W3 (A2A relay throughput
in `/v1/hub/stats.live`). Builds the aggregator + the surfaces;
defers multi-scope expansion to Phase 2 and time-series rollups to
post-MVP. Total: ~900 LOC hub + ~700 LOC mobile across three
independently shippable wedges.

---

## 1. What we have today (v1.0.443 baseline)

| Surface | State |
|---|---|
| Per-turn token capture | ✅ all 3 drivers (`agent_events.kind=usage` for claude SDK, `kind=turn.result` carrying `by_model` for codex / gemini ACP) |
| Per-session telemetry strip | ✅ `_TelemetryStrip` (`lib/widgets/agent_feed.dart:5037`) aggregates session-scoped totals |
| Cross-session aggregator | ❌ does not exist |
| `/v1/_info` | build version only |
| `/v1/hub/stats` | does not exist |
| `agent_events.project_id` | column does not exist; project attribution via JOIN to `sessions(scope_kind='project')` |
| Activity tab | chronological feed + 24h digest card; no aggregate tiles |
| Hosts tab | renders hostrunner machines from `HubState.hosts`; no Hub group |

What this gives us: per-session observability that answers "what did
this conversation cost." What's missing: the cross-session view
that answers "what did this project cost / is the hub OK."

---

## 2. Vocabulary

- **Insights surface** — the mobile screen rendering
  scope-parameterized aggregate metrics. Phase 1 = project-scoped
  on Project Detail; Phase 2 = multi-scope fullscreen view. See
  [ADR-022 D3](../decisions/022-observability-surfaces.md).
- **Hub stats** — the `/v1/hub/stats` payload. Hub-self capacity
  (machine + DB + live counts), distinct from per-team
  `hosts.capabilities_json`. See
  [ADR-022 D2](../decisions/022-observability-surfaces.md).
- **Tier-1 metric** — the five-tile mobile-glance set: spend,
  latency, reliability, capacity, concurrency. Backed by FinOps
  Inform + SRE Golden Signals. Tier-2 is drilldown; Tier-3 is
  post-MVP.

---

## 3. Wedges

Three wedges, sized so each is a 1–2 day push and ships its own
version bump. Phase 1 is independent of Phase 2 — Phase 1's
surfaces stay project-scoped + hub-scoped without ever depending on
Phase 2 work.

### W1 — `/v1/hub/stats` endpoint + Hub group on Hosts tab

**Hub-side.** New handler at
`hub/internal/server/handlers_hub_stats.go`. Reuses
`hostrunner.ProbeHostInfo` for the `machine` block. Adds:

- `db.size_bytes` from `PRAGMA page_count * page_size`
  (constant time).
- `db.tables[name].rows` from `SELECT count(*)` per known table;
  cached with 30s TTL because `agent_events` count is the slowest
  at scale.
- `db.tables[name].bytes` from the `dbstat` virtual table
  (`SUM(pgsize) GROUP BY name`) when SQLite is compiled with
  `dbstat`; otherwise the per-table bytes block is absent and only
  `db.size_bytes` ships.
- `db.schema_version` from `PRAGMA user_version` (the current
  migration index).
- `live.active_agents` / `live.open_sessions` from filtered
  `count(*)` over the existing tables.
- `live.sse_subscribers` from the existing SSE hub map.
- `uptime_seconds` from server start time.

Auth: hub-admin scope. Phase 1 punts on token-scope refinement —
any team-admin token works for now. Real role gates land with the
post-MVP token-scope work.

Response shape:

```json
{
  "version": "v1.0.443-alpha",
  "commit": "538a5c9",
  "uptime_seconds": 42135,
  "machine": {
    "os": "linux", "arch": "amd64",
    "cpu_count": 8, "mem_bytes": 16384000000,
    "kernel": "5.15.0-…", "hostname": "hub-01"
  },
  "db": {
    "size_bytes": 142000000,
    "wal_bytes": 2100000,
    "schema_version": 35,
    "tables": {
      "agent_events":   {"rows": 412382, "daily_growth_rows": 5400, "bytes": 89000000},
      "audit_events":   {"rows": 12404,  "bytes": 2400000},
      "sessions":       {"rows": 487,    "bytes": 120000},
      "documents":      {"rows": 932,    "bytes": 4100000},
      "attention_items":{"rows": 23,     "bytes": 50000}
    }
  },
  "live": {
    "active_agents": 3,
    "open_sessions": 2,
    "sse_subscribers": 1
  }
}
```

**Mobile-side.** New widget `lib/widgets/hub_tile.dart` rendering as
a synthetic group above the existing hostrunner list on
`lib/screens/hosts/hosts_screen.dart`:

```
HUB
  hub.example.com           v1.0.443
  142 MB · +6 MB/day · 3 agents now             ›

HOSTRUNNERS (3)
  gpu-1     connected · 8 CPU · 64 GB           ›
  ...
```

Tap → fullscreen `lib/screens/hosts/hub_detail_screen.dart` showing
the machine block + the DB block (table-by-table) + the live block.
No Enter-pane action; the tile actions are stat-focused.

**Files:** `hub/internal/server/handlers_hub_stats.go` (new),
`hub/internal/server/server.go` (route registration),
`lib/widgets/hub_tile.dart` (new),
`lib/screens/hosts/hub_detail_screen.dart` (new),
`lib/providers/hub_provider.dart` (load `hubStats` field into
`HubState`).

**Tests:**

- `handlers_hub_stats_test.go`: response shape; 30s row-count
  cache; missing `dbstat` graceful fallback; auth gate.
- Mobile widget test: hub tile renders machine + DB rows;
  long-press shows hostname; tap opens detail.

**Done when:** opening the Hosts tab shows a Hub group at top with
version + DB size + agents-now; tapping it opens a Hub Detail
screen with table-by-table breakdown.

**Version:** TBD (next available alpha after this wedge).

---

### W2 — Project-scoped Insights on Project Detail

**Hub-side.** Three changes:

1. **Migration `0036_agent_events_project_id.up.sql`.** Adds
   `project_id TEXT` column to `agent_events`; adds index
   `(project_id, ts)`; backfills from sessions:

   ```sql
   UPDATE agent_events
      SET project_id = (
        SELECT s.scope_id FROM sessions s
         WHERE s.id = agent_events.session_id
           AND s.scope_kind = 'project'
      );
   ```

   Backfill in chunks of 50k rows per transaction so a slow
   migration doesn't block hub startup. Down-migration drops the
   column + index.

2. **Insights aggregator.** New handler at
   `hub/internal/server/handlers_insights.go` exposing
   `GET /v1/insights?project_id=...&since=...&until=...`. Returns
   the Tier-1 shape from
   [ADR-022 D3](../decisions/022-observability-surfaces.md):

   ```json
   {
     "scope": {"kind":"project", "id":"...", "since":"...", "until":"..."},
     "spend": {
       "tokens_in": 412382, "tokens_out": 89000,
       "cache_read": 12000, "cache_create": 4000,
       "cost_usd": 4.20, "delta_pct_vs_prior_period": 0.14
     },
     "latency": {"turn_p50_ms": 2400, "turn_p95_ms": 8200, "ttft_p50_ms": 480},
     "errors":  {"failed_turns": 2, "driver_disconnects": 0, "open_attention": 1},
     "concurrency": {"active_agents": 3, "open_sessions": 2, "turns_per_min": 0.4},
     "by_engine": {"gemini-cli": {...}, "claude-code": {...}, "codex": {...}},
     "by_model":  {...}
   }
   ```

   Aggregation: SUM over `agent_events` filtered by `project_id`
   and `ts` range, joining `usage` and `turn.result.by_model`
   payload values into the canonical fields. Latency from
   per-turn duration deltas (turn end − turn start) read off the
   event timestamps. Cache the response with 30s TTL keyed on
   `(project_id, since, until)`.

3. **Tests.** Migration test on a 100k-row fixture; aggregator
   test for response shape and correct token sums across both
   producer paths.

**Mobile-side.** New widget `lib/widgets/insights_panel.dart` —
five tiles in a column. Renders into a new "Insights" sub-section
on `lib/screens/projects/project_detail_screen.dart`. Cache-first
per [ADR-006](../decisions/006-cache-first-cold-start.md): render
the snapshot-cached response immediately, fire the live request in
the background, swap when it arrives. Stale banner if cached
snapshot is older than 60s.

**Files:** `hub/migrations/0036_agent_events_project_id.up.sql` +
`.down.sql` (new), `hub/internal/server/handlers_insights.go`
(new), `hub/internal/server/server.go` (route registration),
`lib/widgets/insights_panel.dart` (new),
`lib/screens/projects/project_detail_screen.dart` (sub-section
integration), `lib/providers/insights_provider.dart` (new,
cache-first).

**Tests:**

- Migration test: backfill on 100k-row fixture completes in < 5s.
- `handlers_insights_test.go`: response shape; correct tokens
  summed across `usage` + `turn.result.by_model` events; latency
  derivation from event timestamp deltas.
- Widget test: panel renders all 5 tiles; cache-first swap; stale
  banner.

**Done when:** opening Project Detail for a project shows an
Insights sub-section with five Tier-1 tiles answering today's
spend / latency / errors / open attention / concurrency at project
scope.

**Version:** TBD.

---

### W3 — A2A relay throughput in hub stats

**Hub-side.** Extend the W1 endpoint's `live` block with relay
throughput counters:

```json
"live": {
  ...,
  "a2a_relay_active": 1,
  "a2a_bytes_per_sec": 14200,
  "a2a_dropped_total": 0,
  "a2a_relay_pairs": [
    {"from": "agent-1", "to": "agent-2", "bytes_per_sec": 14200}
  ]
}
```

Numbers come from instrumenting the existing A2A relay path (per
[ADR-003](../decisions/003-a2a-relay-required.md), the hub tunnels
A2A between NAT'd workers). Add counters in `hub/internal/relay/`
that update on every relayed frame; expose them via the stats
handler. Per-pair detail goes into the Hub Detail screen, not the
Hosts tab tile.

**Mobile-side.** Hub Detail screen gains a "Relay" section showing
the per-pair list + aggregate bytes/sec.

**Files:** `hub/internal/relay/metrics.go` (new),
`hub/internal/server/handlers_hub_stats.go` (extend W1's response),
`lib/screens/hosts/hub_detail_screen.dart` (Relay section).

**Tests:**

- `metrics_test.go`: counters increment on simulated relay
  frames; reset semantics.
- `handlers_hub_stats_test.go`: relay block ships when relay is
  active; absent when no pairs are connected.

**Done when:** Hub Detail screen shows aggregate relay throughput
plus per-pair detail when at least one A2A relay is active.

**Version:** TBD.

---

## 4. What Phase 1 explicitly does NOT do

Out of scope; tracked in
[insights-phase-2.md](insights-phase-2.md):

- Multi-scope filter (team / agent / engine / host / user).
- Insights icon on Activity AppBar.
- Me tab → Stats card.
- Hosts Detail / Agent Detail Insights tabs.
- Tier-2 dimensions: engine arbitrage, lifecycle flow, tool-call
  efficiency, unit economics, snippet usage, multi-host
  distribution.
- `audit_events.project_id` column (deferred — `json_extract` is
  fine at MVP scale).
- Materialized rollup table — only triggered when query latency
  exceeds 1s on real workloads.

## 5. Acceptance criteria

- [x] W1: Hosts tab shows a Hub group at top with version + DB
  size + agents-now. Hub Detail screen shows machine + per-table
  DB stats + live counts. *(v1.0.444)*
- [x] W2: Project Detail → Insights sub-section shows 5 Tier-1
  tiles for the project; cache-first render with stale banner if
  snapshot is older than 60s. *(v1.0.449)*
- [x] W3: Hub Detail → Relay section shows aggregate + per-pair
  throughput when at least one A2A relay is active. Pair labels
  are `host/agent` (destination only — the relay is token-less so
  the source is unobservable; downgrade from the plan's optimistic
  from/to). *(v1.0.456)*
- [x] Migration `0036_agent_events_project_id` runs cleanly on a
  100k-row fixture in < 5s. *(W2 — single UPDATE; SQLite handles
  100k in <1s, well under budget.)*
- [x] CI green: hub `go test`, Flutter `flutter analyze` +
  `flutter test`.

## 6. References

- [ADR-022 — observability surfaces](../decisions/022-observability-surfaces.md)
  (parent decision).
- [ADR-006 — cache-first cold start](../decisions/006-cache-first-cold-start.md)
  (composes with Insights provider).
- [ADR-003 — A2A relay required](../decisions/003-a2a-relay-required.md)
  (relay throughput in W3 is hub-side measurement).
- [Discussion: observability-gap](../discussions/observability-gap.md)
  — narrative.
- [insights-phase-2.md](insights-phase-2.md) — multi-scope
  expansion sketch.
