#!/usr/bin/env bash
# measure-event-rate.sh — measure REAL agent event rate from a hub DB.
#
# The hub records every agent event in agent_events with a timestamp, so the
# real workload rate is a query away — no instrumentation needed. The metric
# that decides the fold/concurrency design is the FLEET-WIDE events/sec in a
# 1-second window: that is what the single SQLite writer sees. See
# docs/discussions/hub-store-separation-and-fold-policy.md §3.5 — the digest
# fold keeps up while fleet load stays under the ~600-650 ev/s single-writer
# ceiling, and defers to read-repair above it.
#
# DECISION RULE:
#   peak_evps (Q1) comfortably < ~600  -> writer never saturates; the
#       bounded-staleness fold (step 1) closed the fold axis. Do storage
#       (blob-refs) next, not more concurrency work.
#   peak_evps >= ~600 sustained        -> saturation is real; the digest-writer
#       split (store-separation step 2) matters.
#
# Then close the loop: feed peak_evps_per_agent (Q3) back into the load harness
# as a think-time (think_ms ~= 1000 / rate) at the measured concurrency (Q2):
#
#   HUB_LOADTEST=1 HUB_LOADTEST_WORKER=1 HUB_LOADTEST_AGENTS=<Q2 peak> \
#     HUB_LOADTEST_THINK_MS=<from Q3> go test ./internal/server \
#     -run TestLoad_AgentEventIngest -v -count=1 -timeout 5m
#
# Usage:
#   scripts/measure-event-rate.sh path/to/hub.db
#
# Requires the sqlite3 CLI. ts is RFC3339Nano ("2006-01-02T15:04:05.…Z"), so
# substr(ts,1,19) is the per-second bucket and julianday() parses it for spans.

set -euo pipefail

DB="${1:-}"
if [[ -z "$DB" ]]; then
  echo "usage: $0 path/to/hub.db" >&2
  exit 2
fi
if [[ ! -f "$DB" ]]; then
  echo "error: no such file: $DB" >&2
  exit 2
fi
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "error: sqlite3 CLI not found on PATH" >&2
  exit 2
fi

# Guard: empty / pre-event DBs give meaningless rates.
n=$(sqlite3 "$DB" "SELECT COUNT(*) FROM agent_events;" 2>/dev/null || echo 0)
if [[ "$n" == "0" ]]; then
  echo "no agent_events rows in $DB — nothing to measure" >&2
  exit 1
fi

echo "════════ event-rate report: $DB ════════"
echo "agent_events rows: $n"
echo

echo "──── Q1: fleet events/sec (the single-writer ceiling test) ────"
sqlite3 -box "$DB" "
WITH per_sec AS (
  SELECT substr(ts,1,19) AS sec, COUNT(*) AS nev
  FROM agent_events GROUP BY sec
)
SELECT COUNT(*)            AS active_secs,
       SUM(nev)            AS total_events,
       ROUND(AVG(nev),1)   AS avg_evps,
       MAX(nev)            AS peak_evps
FROM per_sec;"

echo
echo "  top per-second rates (the saturation tail; compare peak to ~600):"
sqlite3 -box "$DB" "
WITH per_sec AS (SELECT substr(ts,1,19) AS s, COUNT(*) AS nev FROM agent_events GROUP BY s)
SELECT nev AS evps, COUNT(*) AS seconds_at_rate
FROM per_sec GROUP BY nev ORDER BY nev DESC LIMIT 15;"

echo
echo "──── Q2: peak concurrent writers (distinct agents per second) ────"
sqlite3 -box "$DB" "
WITH per_sec AS (
  SELECT substr(ts,1,19) AS sec, COUNT(DISTINCT agent_id) AS agents
  FROM agent_events GROUP BY sec
)
SELECT MAX(agents) AS peak_concurrent, ROUND(AVG(agents),1) AS avg_concurrent
FROM per_sec;"

echo
echo "──── Q3: per-agent active rate (feeds harness think-time) ────"
sqlite3 -box "$DB" "
WITH span AS (
  SELECT agent_id, COUNT(*) AS events,
         (julianday(MAX(ts)) - julianday(MIN(ts))) * 86400.0 AS span_sec
  FROM agent_events GROUP BY agent_id
)
SELECT ROUND(AVG(events/NULLIF(span_sec,0)),2) AS avg_evps_per_agent,
       ROUND(MAX(events/NULLIF(span_sec,0)),2) AS peak_evps_per_agent,
       COUNT(*)                                AS agents
FROM span WHERE span_sec > 0;"

echo
echo "DECISION: peak_evps (Q1) < ~600  -> fold axis is closed; do storage next."
echo "          peak_evps (Q1) >= ~600 -> saturation real; do the digest-writer split."
