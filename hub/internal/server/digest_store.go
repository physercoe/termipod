package server

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Persistence for the per-run digest + turn index (ADR-038). The fold logic
// lives in digest_fold.go; this file maps the folder state to/from the
// agent_event_digests and agent_turns rows and drives the one-time lazy
// backfill.

// digestStore is the minimal subset of *sql.DB / *sql.Tx the digest reads and
// writes need — so the incremental fold can run inside the agent_events POST
// transaction while the backfill and reads run on the pool.
type digestStore interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// loadAgentDigest reads the persisted row. ok=false means no row yet (the
// caller backfills). Maps not present in the JSON blobs default to empty.
func loadAgentDigest(ctx context.Context, q digestStore, agentID string) (*agentDigest, bool, error) {
	d := newAgentDigest(agentID, "")
	var byModel, errors, tools, latency string
	err := q.QueryRowContext(ctx, `
		SELECT team_id, schema_version, watermark_seq, event_count, turn_count,
		       first_ts, last_ts, duration_ms, cost_usd, by_model_json,
		       error_count, errors_json, tool_total, tool_failed, tools_json,
		       latency_hist_json, outcome
		  FROM agent_event_digests WHERE agent_id = ?`, agentID,
	).Scan(
		&d.TeamID, &d.SchemaVersion, &d.WatermarkSeq, &d.EventCount, &d.TurnCount,
		&d.FirstTS, &d.LastTS, &d.DurationMs, &d.CostUSD, &byModel,
		&d.ErrorCount, &errors, &d.ToolTotal, &d.ToolFailed, &tools,
		&latency, &d.Outcome,
	)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	_ = json.Unmarshal([]byte(byModel), &d.ByModel)
	_ = json.Unmarshal([]byte(errors), &d.Errors)
	_ = json.Unmarshal([]byte(tools), &d.Tools)
	_ = json.Unmarshal([]byte(latency), &d.Latency)
	if d.ByModel == nil {
		d.ByModel = map[string]*byModelAgg{}
	}
	if d.Errors == nil {
		d.Errors = map[string]*errorClassAgg{}
	}
	if d.Tools == nil {
		d.Tools = map[string]*toolAgg{}
	}
	if len(d.Latency.Counts) != len(latencyBoundsMs)+1 {
		d.Latency = newLatencyHist()
	}
	return d, true, nil
}

// saveAgentDigest upserts the digest row.
func saveAgentDigest(ctx context.Context, q digestStore, d *agentDigest) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO agent_event_digests (
			agent_id, team_id, schema_version, updated_at, watermark_seq,
			event_count, turn_count, first_ts, last_ts, duration_ms, cost_usd,
			by_model_json, error_count, errors_json, tool_total, tool_failed,
			tools_json, latency_hist_json, outcome
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(agent_id) DO UPDATE SET
			team_id=excluded.team_id, schema_version=excluded.schema_version,
			updated_at=excluded.updated_at, watermark_seq=excluded.watermark_seq,
			event_count=excluded.event_count, turn_count=excluded.turn_count,
			first_ts=excluded.first_ts, last_ts=excluded.last_ts,
			duration_ms=excluded.duration_ms, cost_usd=excluded.cost_usd,
			by_model_json=excluded.by_model_json, error_count=excluded.error_count,
			errors_json=excluded.errors_json, tool_total=excluded.tool_total,
			tool_failed=excluded.tool_failed, tools_json=excluded.tools_json,
			latency_hist_json=excluded.latency_hist_json, outcome=excluded.outcome`,
		d.AgentID, d.TeamID, d.SchemaVersion, NowUTC(), d.WatermarkSeq,
		d.EventCount, d.TurnCount, d.FirstTS, d.LastTS, d.DurationMs, d.CostUSD,
		marshalJSON(d.ByModel), d.ErrorCount, marshalJSON(d.Errors), d.ToolTotal,
		d.ToolFailed, marshalJSON(d.Tools), marshalJSON(d.Latency), d.Outcome,
	)
	return err
}

// saveTurnRow upserts one agent_turns row.
func saveTurnRow(ctx context.Context, q digestStore, agentID, teamID string, t *turnRow) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO agent_turns (
			agent_id, turn_id, team_id, idx, start_seq, start_ts, end_seq,
			end_ts, duration_ms, status, cost_usd, in_tokens, out_tokens,
			tool_count, tool_failed, error_count
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(agent_id, turn_id) DO UPDATE SET
			team_id=excluded.team_id, idx=excluded.idx, start_seq=excluded.start_seq,
			start_ts=excluded.start_ts, end_seq=excluded.end_seq, end_ts=excluded.end_ts,
			duration_ms=excluded.duration_ms, status=excluded.status,
			cost_usd=excluded.cost_usd, in_tokens=excluded.in_tokens,
			out_tokens=excluded.out_tokens, tool_count=excluded.tool_count,
			tool_failed=excluded.tool_failed, error_count=excluded.error_count`,
		agentID, t.TurnID, teamID, t.Idx, t.StartSeq, t.StartTS, t.EndSeq,
		t.EndTS, t.DurationMs, t.Status, t.CostUSD, t.InTokens, t.OutTokens,
		t.ToolCount, t.ToolFailed, t.ErrorCount,
	)
	return err
}

// loadOpenTurn returns the currently-open turn (end_seq = 0), if any, plus
// the idx the next turn should take (max idx seen + 1).
func loadOpenTurn(ctx context.Context, q digestStore, agentID string) (*turnRow, int, error) {
	var maxIdx int
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(idx), -1) FROM agent_turns WHERE agent_id = ?`, agentID,
	).Scan(&maxIdx); err != nil {
		return nil, 0, err
	}
	nextIdx := maxIdx + 1

	t := &turnRow{}
	err := q.QueryRowContext(ctx, `
		SELECT turn_id, idx, start_seq, start_ts, end_seq, end_ts, duration_ms,
		       status, cost_usd, in_tokens, out_tokens, tool_count, tool_failed,
		       error_count
		  FROM agent_turns
		 WHERE agent_id = ? AND end_seq = 0
		 ORDER BY idx DESC LIMIT 1`, agentID,
	).Scan(
		&t.TurnID, &t.Idx, &t.StartSeq, &t.StartTS, &t.EndSeq, &t.EndTS,
		&t.DurationMs, &t.Status, &t.CostUSD, &t.InTokens, &t.OutTokens,
		&t.ToolCount, &t.ToolFailed, &t.ErrorCount,
	)
	if err == sql.ErrNoRows {
		return nil, nextIdx, nil
	}
	if err != nil {
		return nil, nextIdx, err
	}
	return t, nextIdx, nil
}

// foldEventIncremental folds one freshly-inserted event into the agent's
// digest + turn index, inside the caller's transaction. The digest row is
// assumed to exist (the POST path calls ensureAgentDigest first).
func foldEventIncremental(ctx context.Context, tx digestStore, agentID, teamID string, e foldEvent) error {
	d, ok, err := loadAgentDigest(ctx, tx, agentID)
	if err != nil {
		return err
	}
	if !ok {
		// No digest yet. A brand-new agent's first event (seq 1) starts
		// fresh; a pre-existing agent (events but no digest — the migration
		// window) is backfilled from its prefix [1, e.Seq) inside this same
		// transaction so the fold doesn't undercount. This is the one-time
		// lazy O(n) pass (ADR-038 §2); the row exists for every later event.
		if e.Seq > 1 {
			prior, perr := loadFoldEventsBefore(ctx, tx, agentID, e.Seq)
			if perr != nil {
				return perr
			}
			pd, turns := computeAgentDigest(agentID, teamID, prior)
			if serr := saveAgentDigest(ctx, tx, pd); serr != nil {
				return serr
			}
			for i := range turns {
				if serr := saveTurnRow(ctx, tx, agentID, teamID, &turns[i]); serr != nil {
					return serr
				}
			}
			d, _, err = loadAgentDigest(ctx, tx, agentID)
			if err != nil {
				return err
			}
		}
		if d == nil {
			d = newAgentDigest(agentID, teamID)
		}
	}
	if d.TeamID == "" {
		d.TeamID = teamID
	}
	open, nextIdx, err := loadOpenTurn(ctx, tx, agentID)
	if err != nil {
		return err
	}

	f := newDigestFolder(d)
	f.open = open
	f.nextIdx = nextIdx
	f.callName = nil // incremental resolves tool names from the DB, not memory
	f.resolve = func(id string) string { return resolveToolName(ctx, tx, agentID, id) }

	f.step(e)

	if err := saveAgentDigest(ctx, tx, d); err != nil {
		return err
	}
	// At most one turn closed this step, and at most one is open.
	for i := range f.closed {
		t := f.closed[i]
		if err := saveTurnRow(ctx, tx, agentID, teamID, &t); err != nil {
			return err
		}
	}
	if f.open != nil {
		if err := saveTurnRow(ctx, tx, agentID, teamID, f.open); err != nil {
			return err
		}
	}
	return nil
}

// foldEventIntoDigest runs the incremental fold for one event in its own
// transaction immediately after the (already-committed) agent_events insert.
// Best-effort: a fold error is logged and swallowed so it can never block
// ingestion — the digest is a derived read model and the read path repairs
// any resulting lag via digestIsStale.
func (s *Server) foldEventIntoDigest(ctx context.Context, team, agent string, seq int64, kind, ts, producer, payloadJSON string) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || payload == nil {
		payload = map[string]any{}
	}
	e := foldEvent{Seq: seq, Kind: kind, TS: ts, Producer: producer, Payload: payload}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if err := foldEventIncremental(ctx, tx, agent, team, e); err != nil {
		return
	}
	_ = tx.Commit()
}

// finalizeDigestOutcome stamps the digest's terminal outcome when a session
// stops (ADR-038 §2 — the O(1) terminal-hook step). Best-effort.
func (s *Server) finalizeDigestOutcome(ctx context.Context, team, agentID string) {
	if agentID == "" {
		return
	}
	d, err := ensureAgentDigest(ctx, s.db, agentID, team)
	if err != nil {
		return
	}
	outcome := s.deriveDigestOutcome(ctx, agentID)
	if outcome == "" || outcome == d.Outcome {
		return
	}
	d.Outcome = outcome
	_ = saveAgentDigest(ctx, s.db, d)
}

// deriveDigestOutcome resolves the agent's run outcome: the assigned task's
// terminal state when it has one (done > blocked > cancelled), else the last
// closed turn's status, else "terminated".
func (s *Server) deriveDigestOutcome(ctx context.Context, agentID string) string {
	var taskStatus string
	_ = s.db.QueryRowContext(ctx, `
		SELECT status FROM tasks
		 WHERE assignee_id = ? AND status IN ('done','cancelled','blocked')
		 ORDER BY CASE status WHEN 'done' THEN 0 WHEN 'blocked' THEN 1 ELSE 2 END
		 LIMIT 1`, agentID).Scan(&taskStatus)
	if taskStatus != "" {
		return taskStatus
	}
	var lastTurnStatus string
	_ = s.db.QueryRowContext(ctx, `
		SELECT status FROM agent_turns
		 WHERE agent_id = ? AND end_seq > 0 AND status != ''
		 ORDER BY idx DESC LIMIT 1`, agentID).Scan(&lastTurnStatus)
	if lastTurnStatus != "" {
		return lastTurnStatus
	}
	return "terminated"
}

// digestIsStale reports whether the persisted digest has fallen behind the
// event log (watermark_seq < the agent's max seq) — e.g. a best-effort fold
// errored. The read path re-backfills when true.
func digestIsStale(ctx context.Context, q digestStore, agentID string, watermarkSeq int64) bool {
	var maxSeq int64
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM agent_events WHERE agent_id = ?`, agentID,
	).Scan(&maxSeq); err != nil {
		return false
	}
	return maxSeq > watermarkSeq
}

// resolveToolName looks up a tool_call's name by its id (rare — only on a
// tool failure). Mirrors the brute-force folder's in-memory id→name map.
func resolveToolName(ctx context.Context, q digestStore, agentID, id string) string {
	var name string
	_ = q.QueryRowContext(ctx, `
		SELECT COALESCE(json_extract(payload_json, '$.name'), '')
		  FROM agent_events
		 WHERE agent_id = ? AND kind = 'tool_call'
		   AND COALESCE(json_extract(payload_json, '$.id'),
		               json_extract(payload_json, '$.toolCallId')) = ?
		 ORDER BY seq DESC LIMIT 1`, agentID, id,
	).Scan(&name)
	return name
}

// ensureAgentDigest backfills the digest + turn index for an agent that has
// events but no digest row yet (the one-time lazy O(n) pass — ADR-038 §2).
// Returns the loaded-or-backfilled digest. A no-op when the row exists.
func ensureAgentDigest(ctx context.Context, db *sql.DB, agentID, teamID string) (*agentDigest, error) {
	d, ok, err := loadAgentDigest(ctx, db, agentID)
	if err != nil {
		return nil, err
	}
	if ok && !digestIsStale(ctx, db, agentID, d.WatermarkSeq) {
		return d, nil
	}
	// Missing, or a best-effort fold lagged behind the log — (re)compute.
	return backfillAgentDigest(ctx, db, agentID, teamID)
}

// backfillAgentDigest recomputes the digest + all turn rows from the full
// event log and persists them. Used by the lazy backfill.
func backfillAgentDigest(ctx context.Context, db *sql.DB, agentID, teamID string) (*agentDigest, error) {
	if teamID == "" {
		_ = db.QueryRowContext(ctx, `SELECT team_id FROM agents WHERE id = ?`, agentID).Scan(&teamID)
	}
	events, err := loadFoldEvents(ctx, db, agentID)
	if err != nil {
		return nil, err
	}
	d, turns := computeAgentDigest(agentID, teamID, events)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := saveAgentDigest(ctx, tx, d); err != nil {
		return nil, err
	}
	for i := range turns {
		if err := saveTurnRow(ctx, tx, agentID, teamID, &turns[i]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d, nil
}

// loadFoldEvents reads an agent's full ordered event log as foldEvents.
func loadFoldEvents(ctx context.Context, q digestStore, agentID string) ([]foldEvent, error) {
	return scanFoldEvents(q.QueryContext(ctx, `
		SELECT seq, kind, ts, producer, payload_json
		  FROM agent_events WHERE agent_id = ? ORDER BY seq ASC`, agentID))
}

// loadFoldEventsBefore reads the ordered prefix [1, beforeSeq) — used by the
// in-tx prefix backfill of a pre-existing agent.
func loadFoldEventsBefore(ctx context.Context, q digestStore, agentID string, beforeSeq int64) ([]foldEvent, error) {
	return scanFoldEvents(q.QueryContext(ctx, `
		SELECT seq, kind, ts, producer, payload_json
		  FROM agent_events WHERE agent_id = ? AND seq < ? ORDER BY seq ASC`, agentID, beforeSeq))
}

func scanFoldEvents(rows *sql.Rows, err error) ([]foldEvent, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []foldEvent
	for rows.Next() {
		var e foldEvent
		var payload string
		if err := rows.Scan(&e.Seq, &e.Kind, &e.TS, &e.Producer, &payload); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &e.Payload)
		if e.Payload == nil {
			e.Payload = map[string]any{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
