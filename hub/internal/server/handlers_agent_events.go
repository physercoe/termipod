package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// P1.7: per-agent event queue (blueprint §5.5). Producers append events;
// clients backfill via GET /events and tail live via GET /stream. The
// eventBus is reused with key "agent:<id>" so agent topics are disjoint
// from channel topics.

type agentEventIn struct {
	Kind     string          `json:"kind"`
	Producer string          `json:"producer,omitempty"` // defaults to 'agent'
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type agentEventOut struct {
	ID       string          `json:"id"`
	AgentID  string          `json:"agent_id"`
	Seq      int64           `json:"seq"`
	TS       string          `json:"ts"`
	Kind     string          `json:"kind"`
	Producer string          `json:"producer"`
	Payload  json.RawMessage `json:"payload"`
}

func validAgentEventProducer(p string) bool {
	return p == "agent" || p == "user" || p == "system"
}

func agentBusKey(agentID string) string { return "agent:" + agentID }

func (s *Server) agentBelongsToTeam(r *http.Request, team, agent string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(r.Context(),
		`SELECT COUNT(1) FROM agents WHERE id = ? AND team_id = ?`,
		agent, team).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Server) handlePostAgentEvent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	agent := chi.URLParam(r, "agent")
	var in agentEventIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Kind == "" {
		writeErr(w, http.StatusBadRequest, "kind required")
		return
	}
	if in.Producer == "" {
		in.Producer = "agent"
	}
	if !validAgentEventProducer(in.Producer) {
		writeErr(w, http.StatusBadRequest, "producer must be agent|user|system")
		return
	}
	ok, err := s.agentBelongsToTeam(r, team, agent)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}

	payload := "{}"
	if len(in.Payload) > 0 {
		payload = string(in.Payload)
	}

	// Monotonic seq per agent. SQLite serializes writes, so a COALESCE(MAX)+1
	// inside a single statement is race-free against other INSERTs to the
	// same agent; the UNIQUE(agent_id, seq) constraint is the backstop.
	id := NewID()
	ts := NowUTC()
	sessionID := s.lookupSessionForAgent(r.Context(), agent)
	var seq int64
	err = s.db.QueryRowContext(r.Context(), `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, NULLIF(?, '')
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, agent, ts, in.Kind, in.Producer, payload, sessionID, agent).Scan(&seq)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.touchSession(r.Context(), sessionID)

	evt := map[string]any{
		"id":       id,
		"agent_id": agent,
		"seq":      seq,
		"ts":       ts,
		"kind":     in.Kind,
		"producer": in.Producer,
		"payload":  json.RawMessage(payload),
	}
	s.bus.Publish(agentBusKey(agent), evt)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "seq": seq, "ts": ts,
	})
}

func (s *Server) handleListAgentEvents(w http.ResponseWriter, r *http.Request) {
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

	since := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			since = n
		}
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, agent_id, seq, ts, kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND seq > ?
		 ORDER BY seq ASC LIMIT ?`, agent, since, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []agentEventOut{}
	for rows.Next() {
		var evt agentEventOut
		var payload string
		if err := rows.Scan(
			&evt.ID, &evt.AgentID, &evt.Seq, &evt.TS, &evt.Kind,
			&evt.Producer, &payload,
		); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		evt.Payload = json.RawMessage(payload)
		out = append(out, evt)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleStreamAgentEvents serves SSE for a single agent's event queue.
// Mirrors handleStreamEvents (channel stream) but keyed on agent_id and
// backfills by seq rather than received_ts.
func (s *Server) handleStreamAgentEvents(w http.ResponseWriter, r *http.Request) {
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
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe before backfill so no live event is missed in the gap.
	key := agentBusKey(agent)
	sub := s.bus.Subscribe(key)
	defer s.bus.Unsubscribe(key, sub)

	since := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			since = n
		}
	}
	s.backfillAgentEvents(r, w, flusher, agent, since)

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-sub:
			if !ok {
				return
			}
			writeSSE(w, flusher, evt)
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) backfillAgentEvents(
	r *http.Request, w http.ResponseWriter, f http.Flusher,
	agent string, sinceSeq int64,
) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, agent_id, seq, ts, kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND seq > ?
		 ORDER BY seq ASC LIMIT 500`, agent, sinceSeq)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, agentID, ts, kind, producer, payload string
			seq                                      int64
		)
		if err := rows.Scan(&id, &agentID, &seq, &ts, &kind, &producer, &payload); err != nil {
			return
		}
		evt := map[string]any{
			"id":       id,
			"agent_id": agentID,
			"seq":      seq,
			"ts":       ts,
			"kind":     kind,
			"producer": producer,
			"payload":  json.RawMessage(payload),
		}
		writeSSE(w, f, evt)
	}
}
