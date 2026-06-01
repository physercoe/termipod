package server

import (
	"context"
	"net/http"

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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	d, err := ensureAgentDigest(r.Context(), s.db, agent, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, digestJSON(d))
}

func (s *Server) handleGetSessionDigest(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	session := chi.URLParam(r, "session")
	ok, err := s.sessionBelongsToTeam(r, team, session)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}

	agentIDs, err := s.sessionAgentIDs(r.Context(), session)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rollup := newAgentDigest("", team)
	for _, aid := range agentIDs {
		d, derr := ensureAgentDigest(r.Context(), s.db, aid, team)
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
	writeJSON(w, http.StatusOK, out)
}

// sessionAgentIDs returns the agents that produced events in a session,
// ordered by first activity (the ts order the session transcript uses).
func (s *Server) sessionAgentIDs(ctx context.Context, session string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
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
	return out, rows.Err()
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
		for _, seq := range c.SampleSeqs {
			addSample(&d.SampleSeqs, seq)
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
