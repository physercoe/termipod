# Dense per-session ordinal — phased execution

> **Type:** plan
> **Status:** In progress (2026-06-04) — implements
> [ADR-042](../decisions/042-dense-session-ordinal.md). Hub phases (P0–P3) are
> Go-testable locally and land green before mobile (P4), which is CI-verified
> (no local Flutter). Director-directed: "Option C … a clean and solid
> foundation," full foundation, phased. **P0 done (`42a4e3d`); P1 done (column
> + assignment, migration 0052). P2 next.**
> **Audience:** contributors
> **Last verified vs code:** v1.0.801-alpha

**TL;DR.** Introduce `session_ordinal` — a dense, per-session, insert-time
event coordinate — as the canonical identity for the session/Insight surface,
fixing the resume/navigator wrong-row bug at its root
([ADR-042](../decisions/042-dense-session-ordinal.md)). Hub-first: centralize
event insertion (P0), add the column + assignment (P1), express digest/turn/error
anchors in ordinal space (P2), echo it on the read path + RA keyset (P3); then
the mobile transcript keys anchors and landing on the ordinal (P4).

## Starting point

`agent_events.seq` is per-agent (`UNIQUE(agent_id, seq)`); a resumed session
spans agents with overlapping seqs; the Insight surface keys anchors on bare
`seq` and collides (`insight-resume-seq-identity.md`). Event insertion is
**not centralized** — 10 inline `COALESCE(MAX(seq),0)+1 … WHERE agent_id=?`
sites: `handlers_agent_events.go`, `handlers_agent_input.go`, `a2a_notify.go`,
`handlers_attention.go`, `mcp_orchestrate.go`, `run_notify.go`, `task_notify.go`,
`loop_hooks.go`, `loop_sweep.go`, `handlers_sessions.go`.

## P0 — centralize event insertion (refactor, no behavior change)

One helper that every site calls:

```go
// insertAgentEvent assigns seq (per-agent) and ts, inserts the row, and
// returns (seq, ts). Single atomic statement; UNIQUE(agent_id, seq) backstops.
func (s *Server) insertAgentEvent(ctx, tx, ev agentEventInsert) (seq int64, ts string, err error)
```

- Define `agentEventInsert{AgentID, SessionID, Kind, Producer, PayloadJSON}`.
- Replace all 10 inline inserts; preserve each call's exact kind/producer/session
  resolution (some already call `lookupSessionForAgent`, some pass a known
  session). Keep the existing `RETURNING seq` semantics where callers use it.
- **Tests:** a sweep test asserting no inline `COALESCE(MAX(seq)` remains outside
  the helper (a ratchet, like `lint-legacy-markers.sh`); behavior tests that the
  helper assigns monotonic per-agent seq and is race-safe (concurrent inserts).
- **Gate:** `go build ./... && go test ./...` green. No schema change yet.

## P1 — the coordinate

- **Migration `0052_agent_events_session_ordinal`** (0051 was taken by
  `run_extras`): `ALTER TABLE agent_events
  ADD COLUMN session_ordinal INTEGER`; `CREATE UNIQUE INDEX
  ux_agent_events_session_ordinal ON agent_events(session_id, session_ordinal)
  WHERE session_id IS NOT NULL`; `CREATE INDEX idx_agent_events_session_ordinal
  ON agent_events(session_id, session_ordinal)`. Optional backfill in the same
  migration: a single `UPDATE … ROW_NUMBER() OVER (PARTITION BY session_id ORDER
  BY ts, agent_id, seq)` pass (no prod data, so cosmetic — but keeps dev DBs
  consistent).
- **Assignment in the helper:** within the one insert statement, also set
  `session_ordinal = (SELECT COALESCE(MAX(session_ordinal),0)+1 FROM agent_events
  WHERE session_id = NEW.session_id)` when `session_id` is non-null, else NULL.
- **Tests:** monotonic dense ordinal per session **across two agents** sharing a
  session_id (the resume case); NULL for session-less events; uniqueness backstop.
- **Gate:** Go green.

## P2 — digest + turns in ordinal space

- `digest_fold.go`: alongside `start_seq` and the error `sample_seqs`, record
  `start_ordinal` and `sample_ordinals` (mirror how `sample_ts` was threaded via
  `addSampleTS`). Digest schema bump (v→v+1); old digests refold or degrade.
- `agent_turns` (migration `0053`): add `start_ordinal INTEGER`; `digest_fold`
  populates it; `handlers_agent_turns.go` selects + emits it.
- `handlers_agent_digest.go`: session digest emits `sample_ordinals`; turn rows
  emit `start_ordinal`.
- **Tests:** digest fold over a two-agent session yields ordinals that are dense
  and session-unique; turn `start_ordinal` matches the event's stored ordinal.
- **Gate:** Go green.

## P3 — read path + RA keyset

- `handlers_agent_events.go`: include `session_ordinal` in the list `cols` and
  the JSON out (`agentEventOut`). Add a session-scoped keyset branch on
  `session_ordinal` (`session_id=? AND session_ordinal {<,>} ?`) for the RA
  loader's window-around-anchor and load-older/newer — replacing the `(ts, seq)`
  tiebreak for session-scoped fetches (ts stays for legacy/agent-scoped).
- **Tests:** keyset paging by ordinal returns a contiguous, gap-free window
  across the resume boundary; `error=true` and `kind=` branches still compose.
- **Gate:** Go green + CI.

## P4 — mobile (CI-verified)

- `session_analysis_view.dart`: build `runTurnOrdinals` / `runErrorOrdinals` and
  an ordinal-keyed `runAnchorTs` / class / label map from `start_ordinal` /
  `sample_ordinals`. The seek controller carries the ordinal.
- `insight_transcript.dart`: `_seqIsLoaded` → `_ordinalIsLoaded`,
  `_jumpToContext` / `_landOnSeq` / `runAnchorTs` lookups keyed on ordinal;
  `RandomAccessLoader` keysets on `session_ordinal`. The demoted "N of M" becomes
  exact (`ordinal / event_count`).
- Gated to the Insight surface; `LiveFeed` untouched. Unit-test the
  `RandomAccessLoader` ordinal keyset contract (no widget).
- **Gate:** CI (analyze + test) green; director device-test (resume → several
  turns → Navigator lands correctly).

## Verification model

Hub phases: `PATH=/usr/local/go/bin:$PATH; cd hub && go build ./... && go test
./...`. Mobile: CI only (no local Flutter). Each phase is its own commit; no
release tag until the director asks.

## Open questions

- Digest schema-bump mechanics: refold-on-read vs. lazy migrate (follow ADR-038
  P0's refold pattern).
- Whether the RA loader keeps a `(ts, seq)` fallback for pre-migration digests
  during the transition, or assumes ordinals are always present post-P1.
- Glossary entries for `seq` (agent-scoped) vs `session_ordinal` (session-scoped)
  so the two coordinates are never conflated.
