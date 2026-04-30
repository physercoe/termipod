package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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

// sessionBelongsToTeam gates the cross-agent session-scoped query path.
// The events endpoint is keyed on agent_id in its URL, but a session
// outlives the agents it spans (resume mints a fresh agent and keeps
// the session row), so when session=<id> is set we ignore the URL
// agent and query by session — provided the session is in the team
// the URL claims.
func (s *Server) sessionBelongsToTeam(r *http.Request, team, session string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(r.Context(),
		`SELECT COUNT(1) FROM sessions WHERE id = ? AND team_id = ?`,
		session, team).Scan(&n)
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
	s.captureEngineSessionID(r.Context(), sessionID, in.Kind, in.Producer, payload)

	evt := map[string]any{
		"id":         id,
		"agent_id":   agent,
		"seq":        seq,
		"ts":         ts,
		"kind":       in.Kind,
		"producer":   in.Producer,
		"payload":    json.RawMessage(payload),
		"session_id": sessionID,
	}
	s.bus.Publish(agentBusKey(agent), evt)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "seq": seq, "ts": ts,
	})
}

func (s *Server) handleListAgentEvents(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	agent := chi.URLParam(r, "agent")
	// session=<id> scopes the backfill to one session. Two complementary
	// reasons it has to override the URL's agent filter, not AND with it:
	//   1. New-session flow keeps the same agent and opens a fresh
	//      session row; an agent-only list would replay the prior
	//      closed session's transcript into a "fresh" chat.
	//   2. Resume mints a *new* agent attached to the existing session;
	//      events from the prior agent_id stay stamped with that prior
	//      id, so an agent-only list returns nothing on cold open and
	//      the transcript looks empty (the bug this branch fixes).
	// When session is set we authorise the session against the team
	// instead of the URL agent — the URL agent is only a hint at that
	// point; the session is the durable scope.
	sessionFilter := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionFilter != "" {
		ok, err := s.sessionBelongsToTeam(r, team, sessionFilter)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeErr(w, http.StatusNotFound, "session not found")
			return
		}
	} else {
		ok, err := s.agentBelongsToTeam(r, team, agent)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeErr(w, http.StatusNotFound, "agent not found")
			return
		}
	}

	since := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			since = n
		}
	}
	// before=<seq> requests the page of events older than seq, used by
	// the mobile "load older" trigger when the user scrolls past the
	// top of the loaded transcript. Returned in seq DESC so the caller
	// can prepend without re-sorting the full list. Mutually exclusive
	// with since/tail; the first non-empty wins in the order
	// before_ts > before > tail > since.
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			before = n
		}
	}
	// before_ts=<iso> is the session-scoped variant of `before`. Per-agent
	// seq is unique within an agent but not across agents; once we cross
	// agent boundaries inside a session the seq cursor stops being a
	// total order. ts is. The mobile feed sends before_ts when it has a
	// session filter and the page being paginated may span multiple
	// agents — the cold-open page of a resumed session is the canonical
	// case.
	beforeTS := strings.TrimSpace(r.URL.Query().Get("before_ts"))
	// tail=true returns the newest N events in seq DESC. Without this
	// the cold-open path used `since=0 ORDER BY seq ASC LIMIT N` which
	// silently truncated long sessions to their oldest N events; the
	// chat surface needs newest-first to be useful. SSE backfill keeps
	// using `since` (ASC) because it really does want incremental tail.
	tail := r.URL.Query().Get("tail") == "true"
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	var (
		q    string
		args []any
	)
	switch {
	case sessionFilter != "" && beforeTS != "":
		// Session-scoped load-older. Use ts because seq is per-agent
		// and a session can span multiple agents (resume).
		q = `
			SELECT id, agent_id, seq, ts, kind, producer, payload_json
			  FROM agent_events
			 WHERE session_id = ? AND ts < ?
			 ORDER BY ts DESC, agent_id, seq DESC LIMIT ?`
		args = []any{sessionFilter, beforeTS, limit}
	case sessionFilter != "" && tail:
		q = `
			SELECT id, agent_id, seq, ts, kind, producer, payload_json
			  FROM agent_events
			 WHERE session_id = ?
			 ORDER BY ts DESC, agent_id, seq DESC LIMIT ?`
		args = []any{sessionFilter, limit}
	case sessionFilter != "":
		// Session-scoped incremental ("since" makes no sense across
		// agents because seq is per-agent; treat as "tail-equivalent
		// in ASC order"). Used by SSE backfill paths that pass since=0
		// or by callers that just want the oldest page.
		q = `
			SELECT id, agent_id, seq, ts, kind, producer, payload_json
			  FROM agent_events
			 WHERE session_id = ?
			 ORDER BY ts ASC, agent_id, seq ASC LIMIT ?`
		args = []any{sessionFilter, limit}
	case before > 0:
		q = `
			SELECT id, agent_id, seq, ts, kind, producer, payload_json
			  FROM agent_events
			 WHERE agent_id = ? AND seq < ?
			 ORDER BY seq DESC LIMIT ?`
		args = []any{agent, before, limit}
	case tail:
		q = `
			SELECT id, agent_id, seq, ts, kind, producer, payload_json
			  FROM agent_events
			 WHERE agent_id = ?
			 ORDER BY seq DESC LIMIT ?`
		args = []any{agent, limit}
	default:
		q = `
			SELECT id, agent_id, seq, ts, kind, producer, payload_json
			  FROM agent_events
			 WHERE agent_id = ? AND seq > ?
			 ORDER BY seq ASC LIMIT ?`
		args = []any{agent, since, limit}
	}

	rows, err := s.db.QueryContext(r.Context(), q, args...)
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
	// Same session= scoping as the list endpoint. Backfill SQL and the
	// live publish loop both filter; a live event for a different session
	// on the same agent (rare today, possible once parallel sessions land)
	// is silently dropped from this stream.
	sessionFilter := strings.TrimSpace(r.URL.Query().Get("session"))
	s.backfillAgentEvents(r, w, flusher, agent, since, sessionFilter)

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
			if sessionFilter != "" && eventSessionID(evt) != sessionFilter {
				continue
			}
			writeSSE(w, flusher, evt)
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// eventSessionID extracts the session_id field from a published agent
// event. The publish path in handlePostAgentEvent doesn't currently set
// it explicitly — drivers go through s.bus.Publish without the
// session_id key — so we look up the value in the persisted row when the
// envelope doesn't carry it. Best-effort: an unparseable id falls back
// to "" and is treated as "not in this session".
func eventSessionID(evt map[string]any) string {
	if v, ok := evt["session_id"].(string); ok {
		return v
	}
	return ""
}

func (s *Server) backfillAgentEvents(
	r *http.Request, w http.ResponseWriter, f http.Flusher,
	agent string, sinceSeq int64, sessionFilter string,
) {
	q := `
		SELECT id, agent_id, seq, ts, kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND seq > ?`
	args := []any{agent, sinceSeq}
	if sessionFilter != "" {
		q += " AND session_id = ?"
		args = append(args, sessionFilter)
	}
	q += " ORDER BY seq ASC LIMIT 500"
	rows, err := s.db.QueryContext(r.Context(), q, args...)
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
