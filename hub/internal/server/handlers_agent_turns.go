package server

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ADR-038 §3 / plan P2: the turn index as a keyset-paginated listing — the
// "Turns" filtered view of the analysis surface and a queryable structure
// index for jump-to-turn. Reads the materialized agent_turns rows (one per
// turn), so it is full-run complete (unlike a client filter of the loaded
// transcript window). Each row carries start_seq, which the mobile loader
// uses to reset the All-view window around that turn.
//
// Agent scope orders by the per-agent turn idx; session scope is the
// ts-ordered union of the session's agents' turns (a session can span several
// agents after a resume), keyed by start_ts since idx is per-agent.

type turnJSON struct {
	AgentID      string  `json:"agent_id"`
	TurnID       string  `json:"turn_id"`
	Idx          int     `json:"idx"`
	StartSeq     int64   `json:"start_seq"`
	StartOrdinal int64   `json:"start_ordinal"`
	StartTS      string  `json:"start_ts"`
	EndSeq       int64   `json:"end_seq"`
	EndTS        string  `json:"end_ts"`
	DurationMs   int64   `json:"duration_ms"`
	Status       string  `json:"status"`
	Open         bool    `json:"open"`
	CostUSD      float64 `json:"cost_usd"`
	InTokens     int64   `json:"in_tokens"`
	OutTokens    int64   `json:"out_tokens"`
	ToolCount    int64   `json:"tool_count"`
	ToolFailed   int64   `json:"tool_failed"`
	ErrorCount   int64   `json:"error_count"`
}

func (s *Server) handleListAgentTurns(w http.ResponseWriter, r *http.Request) {
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
	// Ensure the turn index is materialized (and current) before listing —
	// same lazy backfill the digest read does, so a never-folded agent or a
	// best-effort fold that lagged still lists every turn.
	if _, err := ensureAgentDigest(r.Context(), s.digestWriteDB, agent, team); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	after, limit := turnQueryParams(r)
	turns, err := s.listAgentTurns(r.Context(), agent, after, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agent,
		"turns":    turns,
	})
}

func (s *Server) handleListSessionTurns(w http.ResponseWriter, r *http.Request) {
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
	for _, aid := range agentIDs {
		if _, derr := ensureAgentDigest(r.Context(), s.digestWriteDB, aid, team); derr != nil {
			writeErr(w, http.StatusInternalServerError, derr.Error())
			return
		}
	}

	afterTS, limit := turnSessionQueryParams(r)
	turns, err := s.listSessionTurns(r.Context(), session, afterTS, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": session,
		"agent_ids":  agentIDs,
		"turns":      turns,
	})
}

// turnQueryParams parses the agent-scope cursor: after=<idx> pages turns with
// idx > after (ascending), so the client walks the timeline forward.
func turnQueryParams(r *http.Request) (after int, limit int) {
	after = -1
	if v := strings.TrimSpace(r.URL.Query().Get("after")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			after = n
		}
	}
	return after, turnLimit(r)
}

// turnSessionQueryParams parses the session-scope cursor: after_ts=<iso> pages
// turns with start_ts > after_ts (the cross-agent total order).
func turnSessionQueryParams(r *http.Request) (afterTS string, limit int) {
	return strings.TrimSpace(r.URL.Query().Get("after_ts")), turnLimit(r)
}

func turnLimit(r *http.Request) int {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}
	return limit
}

const turnCols = `agent_id, turn_id, idx, start_seq, start_ordinal, start_ts, end_seq, end_ts,
	duration_ms, status, cost_usd, in_tokens, out_tokens, tool_count,
	tool_failed, error_count`

func (s *Server) listAgentTurns(ctx context.Context, agent string, after, limit int) ([]turnJSON, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+turnCols+`
		  FROM agent_turns
		 WHERE agent_id = ? AND idx > ?
		 ORDER BY idx ASC
		 LIMIT ?`, agent, after, limit)
	return scanTurns(rows, err)
}

func (s *Server) listSessionTurns(ctx context.Context, session, afterTS string, limit int) ([]turnJSON, error) {
	// The session's turns are the union of its agents' turns. Join through
	// agent_events to resolve the session's agents, ordered by the
	// cross-agent total order (start_ts). DISTINCT because an agent has many
	// events; we only need the turn rows once.
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT `+turnCols+`
		  FROM agent_turns t
		 WHERE t.agent_id IN (
		           SELECT DISTINCT agent_id FROM agent_events WHERE session_id = ?
		       )
		   AND (? = '' OR t.start_ts > ?)
		 ORDER BY t.start_ts ASC, t.agent_id, t.idx ASC
		 LIMIT ?`, session, afterTS, afterTS, limit)
	return scanTurns(rows, err)
}

func scanTurns(rows *sql.Rows, err error) ([]turnJSON, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []turnJSON{}
	for rows.Next() {
		var t turnJSON
		var startOrd sql.NullInt64
		if err := rows.Scan(
			&t.AgentID, &t.TurnID, &t.Idx, &t.StartSeq, &startOrd, &t.StartTS, &t.EndSeq,
			&t.EndTS, &t.DurationMs, &t.Status, &t.CostUSD, &t.InTokens,
			&t.OutTokens, &t.ToolCount, &t.ToolFailed, &t.ErrorCount,
		); err != nil {
			return nil, err
		}
		t.StartOrdinal = startOrd.Int64 // 0 for pre-v5 / session-less rows
		t.Open = t.EndSeq == 0
		out = append(out, t)
	}
	return out, rows.Err()
}
