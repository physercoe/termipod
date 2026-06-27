package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ADR-038 §5 read endpoints. The per-agent digest is the canonical run
// summary; the session digest is the ts-ordered rollup of its agents'
// digests (the surface mobile's analysis mode reads, since a resumed session
// spans several agents). Both (re)compute lazily on read if missing or stale.

func (s *Server) handleGetAgentDigest(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	agent := chi.URLParam(r, "agent")
	ok, err := s.agentBelongsToTeam(r, team, agent)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	d, err := s.ensureAgentDigest(r.Context(), agent, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	out := digestJSON(d)
	// active_ms = the run's *real running time* (the sum of per-turn
	// durations), as distinct from duration_ms = the full first→last wall-clock
	// span (which includes the idle gaps between turns while the agent waits on
	// the user). Summed from the materialised agent_turns rows at read time, so
	// it needs no extra digest column.
	active, _ := s.sumTurnActiveMs(r.Context(), []string{agent})
	out["active_ms"] = active
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSessionDigest(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	session := chi.URLParam(r, "session")
	ok, err := s.sessionBelongsToTeam(r, team, session)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}

	agentIDs, err := s.sessionAgentIDs(r.Context(), team, session)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	rollup := newAgentDigest("", team)
	for _, aid := range agentIDs {
		d, derr := s.ensureAgentDigest(r.Context(), aid, team)
		if derr != nil {
			writeErr(w, http.StatusInternalServerError, derr.Error())
			return
		}
		mergeDigest(rollup, d)
	}

	out := digestJSON(rollup)
	delete(out, "agent_id")
	out["session_id"] = session
	out["agent_ids"] = agentIDs
	// Real running time across the session's agents (sum of per-turn
	// durations) — see handleGetAgentDigest. The session span (duration_ms) is
	// already the ts-widened first→last across agents from the rollup.
	active, _ := s.sumTurnActiveMs(r.Context(), agentIDs)
	out["active_ms"] = active
	writeJSON(w, http.StatusOK, out)
}

// sumTurnActiveMs sums duration_ms over the closed turns of the given agents —
// the run's active running time. Open/in-progress turns have no final duration
// yet (duration_ms <= 0) and are excluded, so a live run reports the time spent
// on completed turns. Empty input → 0.
func (s *Server) sumTurnActiveMs(ctx context.Context, agentIDs []string) (int64, error) {
	if len(agentIDs) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(agentIDs))
	args := make([]any, len(agentIDs))
	for i, id := range agentIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT COALESCE(SUM(duration_ms), 0) FROM agent_turns
	       WHERE duration_ms > 0 AND agent_id IN (` + strings.Join(placeholders, ",") + `)`
	dr, err := s.digestReaderForAgents(ctx, agentIDs)
	if err != nil {
		return 0, err
	}
	var ms int64
	err = dr.QueryRowContext(ctx, q, args...).Scan(&ms)
	return ms, err
}

// sessionAgentIDs returns the agents that produced events in a session,
// ordered by first activity (the ts order the session transcript uses).
// sessionAgentIDs is team-keyed (ADR-045 P2): the session's events live in its
// team's shard, and the caller always knows the team — the /v1/teams/{team}/…
// URL, or (the OTLP export) by resolving it from the session row. Resolving it
// here from control would add a round-trip and fail for a session with events
// but no control row.
//
// #118 §1: the GROUP BY scan runs on every digest/turns read, so for a terminal
// (archived) session — whose agent set can no longer grow — the result is cached
// on sessions.agent_ids_json and served O(1) thereafter. A paused session can
// still resume and bind a new agent, so non-archived sessions always re-scan.
func (s *Server) sessionAgentIDs(ctx context.Context, team, session string) ([]string, error) {
	// Control-store probe: is this session sealed, and do we already have its
	// agent set cached? A session with events but no control row (the doc above)
	// scans the same as before — status stays "" so the cache path is skipped.
	var status string
	var cached sql.NullString
	_ = s.db.QueryRowContext(ctx,
		`SELECT status, agent_ids_json FROM sessions WHERE id = ?`, session,
	).Scan(&status, &cached)
	sealed := status == "archived"
	if sealed && cached.Valid && cached.String != "" {
		var ids []string
		if err := json.Unmarshal([]byte(cached.String), &ids); err == nil {
			return ids, nil
		}
		// A malformed blob falls through to the authoritative scan.
	}

	er, err := s.eventsReader(team)
	if err != nil {
		return nil, err
	}
	rows, err := er.QueryContext(ctx, `
		SELECT agent_id FROM agent_events
		 WHERE session_id = ?
		 GROUP BY agent_id
		 ORDER BY MIN(ts) ASC`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Read-repair: materialize the set onto the archived session so the next
	// read skips the scan. Best-effort — a write failure just means we scan
	// again next time. Only when non-empty (an empty set would re-scan anyway,
	// and avoids caching a transient "no events yet" state).
	if sealed && !cached.Valid && len(out) > 0 {
		if blob, err := json.Marshal(out); err == nil {
			_, _ = s.db.ExecContext(ctx,
				`UPDATE sessions SET agent_ids_json = ? WHERE id = ? AND agent_ids_json IS NULL`,
				string(blob), session)
		}
	}
	return out, nil
}

// mergeDigest folds src into dst (the session rollup): counts sum, taxonomies
// merge, latency histograms add, ts span widens (ADR-038 §5).
func mergeDigest(dst, src *agentDigest) {
	dst.EventCount += src.EventCount
	dst.TurnCount += src.TurnCount
	dst.CostUSD += src.CostUSD
	dst.ErrorCount += src.ErrorCount
	dst.ToolTotal += src.ToolTotal
	dst.ToolFailed += src.ToolFailed
	if src.WatermarkSeq > dst.WatermarkSeq {
		dst.WatermarkSeq = src.WatermarkSeq
	}
	if dst.FirstTS == "" || (src.FirstTS != "" && src.FirstTS < dst.FirstTS) {
		dst.FirstTS = src.FirstTS
	}
	if src.LastTS > dst.LastTS {
		dst.LastTS = src.LastTS
	}
	dst.DurationMs = tsDeltaMs(dst.FirstTS, dst.LastTS)
	for model, m := range src.ByModel {
		d := dst.ByModel[model]
		if d == nil {
			d = &byModelAgg{}
			dst.ByModel[model] = d
		}
		d.In += m.In
		d.Out += m.Out
		d.CacheRead += m.CacheRead
		d.CacheCreate += m.CacheCreate
		d.CostUSD += m.CostUSD
	}
	for class, c := range src.Errors {
		d := dst.Errors[class]
		if d == nil {
			d = &errorClassAgg{}
			dst.Errors[class] = d
		}
		d.Count += c.Count
		for i, seq := range c.SampleSeqs {
			var ts, label string
			var ord int64
			if i < len(c.SampleOrdinals) {
				ord = c.SampleOrdinals[i]
			}
			if i < len(c.SampleTSs) {
				ts = c.SampleTSs[i]
			}
			if i < len(c.SampleLabels) {
				label = c.SampleLabels[i]
			}
			addSampleTS(&d.SampleSeqs, &d.SampleOrdinals, &d.SampleTSs, &d.SampleLabels, seq, ord, ts, label)
		}
	}
	for name, tgt := range src.Tools {
		d := dst.Tools[name]
		if d == nil {
			d = &toolAgg{}
			dst.Tools[name] = d
		}
		d.Calls += tgt.Calls
		d.Failed += tgt.Failed
		for _, seq := range tgt.SampleSeqs {
			addSample(&d.SampleSeqs, seq)
		}
	}
	mergeLatencyHist(&dst.Latency, src.Latency)
}

// digestJSON renders a digest as the on-the-wire response. Latency exposes
// estimated percentiles (from the histogram) plus the raw bounds/counts so a
// client can recompute or merge.
func digestJSON(d *agentDigest) map[string]any {
	var samples int64
	for _, c := range d.Latency.Counts {
		samples += c
	}
	return map[string]any{
		"agent_id":      d.AgentID,
		"team_id":       d.TeamID,
		"watermark_seq": d.WatermarkSeq,
		"event_count":   d.EventCount,
		"turn_count":    d.TurnCount,
		"first_ts":      d.FirstTS,
		"last_ts":       d.LastTS,
		"duration_ms":   d.DurationMs,
		"cost_usd":      d.CostUSD,
		"by_model":      d.ByModel,
		"error_count":   d.ErrorCount,
		"errors":        d.Errors,
		"tool_total":    d.ToolTotal,
		"tool_failed":   d.ToolFailed,
		"tools":         d.Tools,
		"latency": map[string]any{
			"p50_ms":  histogramPercentile(d.Latency, 0.50),
			"p95_ms":  histogramPercentile(d.Latency, 0.95),
			"samples": samples,
			"bounds":  d.Latency.Bounds,
			"counts":  d.Latency.Counts,
		},
		"outcome": d.Outcome,
	}
}
