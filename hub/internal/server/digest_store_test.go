package server

import (
	"context"
	"encoding/json"
	"testing"
)

// TestDigestIncrementalMatchesBrute drives the vector through the real POST
// fold path (insert + foldEventIntoDigest per event) and asserts the
// persisted digest equals a brute-force scan of the same events — the
// ADR-038 §2 invariant ("incremental digest == a brute-force scan at every
// watermark").
func TestDigestIncrementalMatchesBrute(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	v, events := loadDigestVector(t)

	if _, err := s.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		 VALUES (?,?,?,?,?,?)`,
		v.AgentID, defaultTeamID, "vec-worker", "claude-code", "running", NowUTC(),
	); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	for _, e := range events {
		payload, _ := json.Marshal(e.Payload)
		if _, err := s.db.Exec(
			`INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
			 VALUES (?,?,?,?,?,?,?)`,
			NewID(), v.AgentID, e.Seq, e.TS, e.Kind, e.Producer, string(payload),
		); err != nil {
			t.Fatalf("insert event seq %d: %v", e.Seq, err)
		}
		s.foldEventIntoDigest(ctx, defaultTeamID, v.AgentID, e.Seq, e.Kind, e.TS, e.Producer, string(payload))
	}

	got, ok, err := loadAgentDigest(ctx, s.db, v.AgentID)
	if err != nil || !ok {
		t.Fatalf("load digest: ok=%v err=%v", ok, err)
	}
	want, _ := computeAgentDigest(v.AgentID, defaultTeamID, events)

	if got.EventCount != want.EventCount {
		t.Errorf("event_count incr=%d brute=%d", got.EventCount, want.EventCount)
	}
	if got.TurnCount != want.TurnCount {
		t.Errorf("turn_count incr=%d brute=%d", got.TurnCount, want.TurnCount)
	}
	if got.WatermarkSeq != want.WatermarkSeq {
		t.Errorf("watermark incr=%d brute=%d", got.WatermarkSeq, want.WatermarkSeq)
	}
	if got.CostUSD != want.CostUSD {
		t.Errorf("cost incr=%v brute=%v", got.CostUSD, want.CostUSD)
	}
	if got.ErrorCount != want.ErrorCount {
		t.Errorf("error_count incr=%d brute=%d", got.ErrorCount, want.ErrorCount)
	}
	if got.ToolTotal != want.ToolTotal || got.ToolFailed != want.ToolFailed {
		t.Errorf("tools incr=(%d,%d) brute=(%d,%d)", got.ToolTotal, got.ToolFailed, want.ToolTotal, want.ToolFailed)
	}
	if got.DurationMs != want.DurationMs {
		t.Errorf("duration incr=%d brute=%d", got.DurationMs, want.DurationMs)
	}
	// Per-tool and per-class maps.
	for name, w := range want.Tools {
		g := got.Tools[name]
		if g == nil || g.Calls != w.Calls || g.Failed != w.Failed {
			t.Errorf("tools[%q] incr=%v brute=%v", name, g, w)
		}
	}
	for class, w := range want.Errors {
		g := got.Errors[class]
		if g == nil || g.Count != w.Count {
			t.Errorf("errors[%q] incr=%v brute=%v", class, g, w)
		}
	}
	for model, w := range want.ByModel {
		g := got.ByModel[model]
		if g == nil || g.In != w.In || g.Out != w.Out {
			t.Errorf("by_model[%q] incr=%v brute=%v", model, g, w)
		}
	}

	// Turn rows persisted match brute force.
	turns, err := loadAllTurns(ctx, s.db, v.AgentID)
	if err != nil {
		t.Fatalf("load turns: %v", err)
	}
	if len(turns) != len(v.Expected.Turns) {
		t.Fatalf("turn rows = %d, want %d", len(turns), len(v.Expected.Turns))
	}
	for i, w := range v.Expected.Turns {
		g := turns[i]
		if g.TurnID != w.TurnID || g.Idx != w.Idx || g.StartSeq != w.StartSeq ||
			g.EndSeq != w.EndSeq || g.Status != w.Status || g.ToolFailed != w.ToolFailed ||
			g.ErrorCount != w.ErrorCount {
			t.Errorf("turn[%d] incr=%+v want=%+v", i, g, w)
		}
	}
}

// TestDigestLazyBackfill verifies a pre-existing agent (events but no digest)
// is backfilled correctly on the first event that triggers a fold, with no
// undercount of the prefix.
func TestDigestLazyBackfill(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	v, events := loadDigestVector(t)

	if _, err := s.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		 VALUES (?,?,?,?,?,?)`,
		v.AgentID, defaultTeamID, "vec-worker", "claude-code", "running", NowUTC(),
	); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// Insert the whole prefix WITHOUT folding (simulates a pre-digest agent).
	for _, e := range events[:len(events)-1] {
		payload, _ := json.Marshal(e.Payload)
		if _, err := s.db.Exec(
			`INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
			 VALUES (?,?,?,?,?,?,?)`,
			NewID(), v.AgentID, e.Seq, e.TS, e.Kind, e.Producer, string(payload),
		); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	// Now insert + fold the LAST event — the fold must backfill the prefix.
	last := events[len(events)-1]
	payload, _ := json.Marshal(last.Payload)
	if _, err := s.db.Exec(
		`INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
		 VALUES (?,?,?,?,?,?,?)`,
		NewID(), v.AgentID, last.Seq, last.TS, last.Kind, last.Producer, string(payload),
	); err != nil {
		t.Fatalf("insert last: %v", err)
	}
	s.foldEventIntoDigest(ctx, defaultTeamID, v.AgentID, last.Seq, last.Kind, last.TS, last.Producer, string(payload))

	got, ok, err := loadAgentDigest(ctx, s.db, v.AgentID)
	if err != nil || !ok {
		t.Fatalf("load digest: ok=%v err=%v", ok, err)
	}
	if got.EventCount != v.Expected.EventCount {
		t.Errorf("event_count = %d, want %d (backfill undercount?)", got.EventCount, v.Expected.EventCount)
	}
	if got.ErrorCount != v.Expected.ErrorCount {
		t.Errorf("error_count = %d, want %d", got.ErrorCount, v.Expected.ErrorCount)
	}
	if got.TurnCount != v.Expected.TurnCount {
		t.Errorf("turn_count = %d, want %d", got.TurnCount, v.Expected.TurnCount)
	}
}

// loadAllTurns reads every turn row for an agent, ordered by idx.
func loadAllTurns(ctx context.Context, q digestStore, agentID string) ([]turnRow, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT turn_id, idx, start_seq, start_ts, end_seq, end_ts, duration_ms,
		       status, cost_usd, in_tokens, out_tokens, tool_count, tool_failed, error_count
		  FROM agent_turns WHERE agent_id = ? ORDER BY idx ASC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []turnRow
	for rows.Next() {
		var t turnRow
		if err := rows.Scan(&t.TurnID, &t.Idx, &t.StartSeq, &t.StartTS, &t.EndSeq, &t.EndTS,
			&t.DurationMs, &t.Status, &t.CostUSD, &t.InTokens, &t.OutTokens,
			&t.ToolCount, &t.ToolFailed, &t.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
