package server

import (
	"database/sql"
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
	// SessionID is the hub session this event belongs to. Mobile resolves
	// an agent's run session from the newest event's session_id to anchor
	// the Insights analysis surface (digest + turns), so the list endpoint
	// must echo it — the single-event POST response already does.
	SessionID string `json:"session_id,omitempty"`
	// SessionOrdinal is the event's dense, session-unique position (ADR-042) —
	// the coordinate the Insight transcript keys anchors and landing on, so a
	// jump resolves the right row even after a resume (seq collides across the
	// session's agents). Omitted (0) for session-less / pre-migration rows.
	SessionOrdinal int64 `json:"session_ordinal,omitempty"`
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
	// hub-scaling lever 1: externalize any oversized string leaf (e.g. an
	// `attach` tool_call's multi-MB base64) to the content-addressed blob
	// store, replacing it with a blob:sha256/<hex> ref before the row is
	// written. Lossless + deduped; keeps the transcript row small. No-op for
	// normal small events (single length check). See payload_externalize.go.
	payload = s.externalizeLargePayload(r.Context(), payload)

	sessionID := s.lookupSessionForAgent(r.Context(), agent)
	id, seq, _, ts, err := insertAgentEvent(r.Context(), s.writeDB, agentEventInsert{
		AgentID:     agent,
		SessionID:   sessionID,
		Kind:        in.Kind,
		Producer:    in.Producer,
		PayloadJSON: payload,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// ADR-038 (amended 2026-06-06, store-separation step 1): the digest fold
	// is the heaviest per-event write (~half the cost), so it runs OFF this
	// hot path. Mark trigger accounting (O(1), in-memory) and return;
	// runDigestFold folds the agent on a bounded-staleness trigger (turn
	// close, N events, or tau). Passing the kind lets a turn.result fold
	// promptly. The digest is eventually consistent; the read path repairs
	// any lag (digestIsStale).
	s.markDigestDirty(team, agent, in.Kind)

	s.touchSession(r.Context(), sessionID)
	s.captureEngineSessionID(r.Context(), sessionID, in.Kind, in.Producer, payload)
	s.captureSessionNameHint(r.Context(), sessionID, in.Kind, in.Producer, payload)
	// ADR-034 D-2: an event from this agent is progress on any open task
	// it owns — slide that task's inactivity deadline forward.
	s.bumpLoopProgress(r.Context(), agent)
	// ADR-034 D-5: an agent going idle while it still owns open
	// loop-entities is re-woken with the open set (the PreAgentIdle hook).
	if in.Kind == "lifecycle" && lifecycleIsIdle(payload) {
		s.onPreAgentIdle(r.Context(), agent)
	}

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
	// after_ts=<iso> is the session-scoped FORWARD window — the complement
	// of before_ts (ADR-038 §5). The analysis-mode random-access loader
	// fetches the block *after* a target ts when it relocates the window
	// around a jump anchor. The (session_id, ts) index already supports
	// `ts > ? ASC`; this just wires it. Agent scope keeps using `since`.
	afterTS := strings.TrimSpace(r.URL.Query().Get("after_ts"))
	// after_seq / before_seq are the SECONDARY tiebreak for the session-scoped
	// ts cursors (plan P2, the analysis-mode random-access loader). ts alone
	// can't window around a precise point: a session's events frequently share
	// a ts (sub-second bursts), and a strict `ts > ?` / `ts < ?` either drops
	// those same-ts siblings or re-includes the anchor. Pairing ts with seq
	// gives a complete `(ts, seq)` keyset — `ts > ? OR (ts = ? AND seq > ?)` —
	// so a window-around-anchor fetch loses no events and the two halves are
	// contiguous. Only honored alongside after_ts / before_ts; the plain ts
	// branches (the live-tail feed's load-older) are untouched.
	var afterSeq, beforeSeq int64
	var haveAfterSeq, haveBeforeSeq bool
	if v := strings.TrimSpace(r.URL.Query().Get("after_seq")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			afterSeq, haveAfterSeq = n, true
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("before_seq")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			beforeSeq, haveBeforeSeq = n, true
		}
	}
	// after_ordinal / before_ordinal are the session-scoped random-access cursor
	// keyed on the dense session_ordinal (ADR-042). A single gap-free, totally
	// ordered, session-unique coordinate — so the analysis-mode loader windows
	// around an anchor without the (ts, seq) compound keyset, and lands on the
	// right row even after a resume (where seq collides across the session's
	// agents). Honored only with a session filter; the per-agent live tail is
	// untouched.
	var afterOrdinal, beforeOrdinal int64
	var haveAfterOrdinal, haveBeforeOrdinal bool
	if v := strings.TrimSpace(r.URL.Query().Get("after_ordinal")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			afterOrdinal, haveAfterOrdinal = n, true
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("before_ordinal")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			beforeOrdinal, haveBeforeOrdinal = n, true
		}
	}
	// tail=true returns the newest N events in seq DESC. Without this
	// the cold-open path used `since=0 ORDER BY seq ASC LIMIT N` which
	// silently truncated long sessions to their oldest N events; the
	// chat surface needs newest-first to be useful. SSE backfill keeps
	// using `since` (ASC) because it really does want incremental tail.
	tail := r.URL.Query().Get("tail") == "true"
	// kind=<a,b,c> filters to a set of event kinds (ADR-038 §5) — a keyset
	// listing of just the matches, so a filtered analysis view (Tools, Text)
	// is full-run server-side rather than a client filter of the loaded
	// window. Applies to every cursor branch; the index covers it.
	var kindList []string
	if v := strings.TrimSpace(r.URL.Query().Get("kind")); v != "" {
		for _, k := range strings.Split(v, ",") {
			if k = strings.TrimSpace(k); k != "" {
				kindList = append(kindList, k)
			}
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

	// error=true (ADR-039 P3) — the canonical error events of this scope,
	// keyset-paged. Errors are a *derived* predicate (not a kind), so we scan
	// candidate-kind rows and filter with the SAME canonicalErrorClass the
	// digest fold uses (no SQL/Go divergence). Lifts the digest's per-class
	// sample cap so the mobile Errors lens can list EVERY error.
	if r.URL.Query().Get("error") == "true" {
		s.respondErrorEvents(w, r, sessionFilter, agent,
			beforeTS, beforeSeq, haveBeforeSeq,
			afterTS, afterSeq, haveAfterSeq, limit)
		return
	}

	// Build the cursor clause per branch, then append the optional kind
	// filter and the limit uniformly.
	const cols = `id, agent_id, seq, ts, kind, producer, payload_json, session_id, session_ordinal`
	var (
		where string
		order string
		args  []any
	)
	switch {
	case sessionFilter != "" && haveAfterOrdinal:
		// Session-scoped forward window by the dense session_ordinal (ADR-042) —
		// the analysis-mode random-access loader's load-newer / jump-forward.
		// A single gap-free coordinate (no ts+seq tiebreak), unique across the
		// session's agents, so it never drops or duplicates a same-ts sibling.
		where = `session_id = ? AND session_ordinal > ?`
		order = `session_ordinal ASC`
		args = []any{sessionFilter, afterOrdinal}
	case sessionFilter != "" && haveBeforeOrdinal:
		// Session-scoped backward window by session_ordinal (load-older /
		// jump-back / window-around-anchor).
		where = `session_id = ? AND session_ordinal < ?`
		order = `session_ordinal DESC`
		args = []any{sessionFilter, beforeOrdinal}
	case sessionFilter != "" && afterTS != "" && haveAfterSeq:
		// Session-scoped forward window with the `(ts, seq)` keyset tiebreak —
		// the analysis-mode random-access loader's load-newer / jump-forward.
		// Self-consistent order + cursor (ts, seq) so same-ts siblings aren't
		// dropped or duplicated across the page boundary.
		where = `session_id = ? AND (ts > ? OR (ts = ? AND seq > ?))`
		order = `ts ASC, seq ASC`
		args = []any{sessionFilter, afterTS, afterTS, afterSeq}
	case sessionFilter != "" && beforeTS != "" && haveBeforeSeq:
		// Session-scoped backward window with the `(ts, seq)` keyset tiebreak.
		where = `session_id = ? AND (ts < ? OR (ts = ? AND seq < ?))`
		order = `ts DESC, seq DESC`
		args = []any{sessionFilter, beforeTS, beforeTS, beforeSeq}
	case sessionFilter != "" && afterTS != "":
		// Session-scoped forward window (the load-newer / jump-forward path).
		where = `session_id = ? AND ts > ?`
		order = `ts ASC, agent_id, seq ASC`
		args = []any{sessionFilter, afterTS}
	case sessionFilter != "" && beforeTS != "":
		// Session-scoped load-older. Use ts because seq is per-agent
		// and a session can span multiple agents (resume).
		where = `session_id = ? AND ts < ?`
		order = `ts DESC, agent_id, seq DESC`
		args = []any{sessionFilter, beforeTS}
	case sessionFilter != "" && tail:
		where = `session_id = ?`
		order = `ts DESC, agent_id, seq DESC`
		args = []any{sessionFilter}
	case sessionFilter != "":
		// Session-scoped incremental ("since" makes no sense across
		// agents because seq is per-agent; treat as "tail-equivalent
		// in ASC order"). Used by SSE backfill paths that pass since=0
		// or by callers that just want the oldest page.
		where = `session_id = ?`
		order = `ts ASC, agent_id, seq ASC`
		args = []any{sessionFilter}
	case before > 0:
		where = `agent_id = ? AND seq < ?`
		order = `seq DESC`
		args = []any{agent, before}
	case tail:
		where = `agent_id = ?`
		order = `seq DESC`
		args = []any{agent}
	default:
		where = `agent_id = ? AND seq > ?`
		order = `seq ASC`
		args = []any{agent, since}
	}
	if len(kindList) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(kindList)), ",")
		where += ` AND kind IN (` + ph + `)`
		for _, k := range kindList {
			args = append(args, k)
		}
	}
	args = append(args, limit)
	q := `SELECT ` + cols + ` FROM agent_events WHERE ` + where + ` ORDER BY ` + order + ` LIMIT ?`

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
		// session_id is nullable for legacy rows written before the column
		// existed; scan through NullString so those don't error the whole list.
		var sessionID sql.NullString
		var sessionOrdinal sql.NullInt64
		if err := rows.Scan(
			&evt.ID, &evt.AgentID, &evt.Seq, &evt.TS, &evt.Kind,
			&evt.Producer, &payload, &sessionID, &sessionOrdinal,
		); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		evt.Payload = json.RawMessage(payload)
		evt.SessionID = sessionID.String
		evt.SessionOrdinal = sessionOrdinal.Int64
		out = append(out, evt)
	}
	writeJSON(w, http.StatusOK, out)
}

// errorCandidateKinds are the only kinds canonicalErrorClass can classify as an
// error. Scanning just these (then applying the SAME predicate the digest fold
// uses) yields the canonical error list with no SQL/Go divergence — the exact
// "disjoint error definitions" pitfall ADR-038 warns about.
var errorCandidateKinds = []string{
	"error", "tool_result", "tool_call_update", "turn.result",
}

// respondErrorEvents serves GET …/events?error=true (ADR-039 P3): the scope's
// canonical error events, keyset-paged. "Error" is a derived predicate, not a
// kind, so this scans candidate-kind rows over the (ts, seq) keyset in batches
// and filters each with canonicalErrorClass — reusing the digest's classifier
// rather than reimplementing it in SQL. Errors are sparse, hence the
// scan-in-batches loop until `limit` errors are collected or the scan is
// exhausted. The client pages with the oldest (before_ts/seq) or newest
// (after_ts/seq) returned error, matching the events endpoint's cursor shape.
func (s *Server) respondErrorEvents(w http.ResponseWriter, r *http.Request,
	session, agent, beforeTS string, beforeSeq int64, haveBeforeSeq bool,
	afterTS string, afterSeq int64, haveAfterSeq bool, limit int) {
	const cols = `id, agent_id, seq, ts, kind, producer, payload_json, session_id, session_ordinal`
	const scanBatch = 500
	sessionScoped := session != ""
	ascending := haveAfterSeq && afterTS != ""

	// Running keyset cursor; advances past the last *scanned* (not just
	// matched) row so a sparse batch doesn't re-scan.
	curTS, curSeq, haveCur := beforeTS, beforeSeq, haveBeforeSeq
	if ascending {
		curTS, curSeq, haveCur = afterTS, afterSeq, haveAfterSeq
	}
	kindPH := strings.TrimSuffix(strings.Repeat("?,", len(errorCandidateKinds)), ",")

	out := []agentEventOut{}
	for len(out) < limit {
		where := "agent_id = ?"
		args := []any{agent}
		if sessionScoped {
			where = "session_id = ?"
			args = []any{session}
		}
		if haveCur {
			if ascending {
				where += " AND (ts > ? OR (ts = ? AND seq > ?))"
			} else {
				where += " AND (ts < ? OR (ts = ? AND seq < ?))"
			}
			args = append(args, curTS, curTS, curSeq)
		}
		where += " AND kind IN (" + kindPH + ")"
		for _, k := range errorCandidateKinds {
			args = append(args, k)
		}
		order := "ts DESC, seq DESC"
		if ascending {
			order = "ts ASC, seq ASC"
		}
		args = append(args, scanBatch)
		q := "SELECT " + cols + " FROM agent_events WHERE " + where +
			" ORDER BY " + order + " LIMIT ?"
		rows, err := s.db.QueryContext(r.Context(), q, args...)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		scanned := 0
		for rows.Next() {
			var evt agentEventOut
			var payload string
			var sid sql.NullString
			var sord sql.NullInt64
			if err := rows.Scan(&evt.ID, &evt.AgentID, &evt.Seq, &evt.TS,
				&evt.Kind, &evt.Producer, &payload, &sid, &sord); err != nil {
				rows.Close()
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			evt.SessionOrdinal = sord.Int64
			scanned++
			curTS, curSeq, haveCur = evt.TS, evt.Seq, true
			var p map[string]any
			_ = json.Unmarshal([]byte(payload), &p)
			if _, isErr := canonicalErrorClass(foldEvent{
				Seq: evt.Seq, Kind: evt.Kind, TS: evt.TS,
				Producer: evt.Producer, Payload: p,
			}); !isErr {
				continue
			}
			evt.Payload = json.RawMessage(payload)
			evt.SessionID = sid.String
			out = append(out, evt)
			if len(out) >= limit {
				break
			}
		}
		rows.Close()
		if len(out) >= limit || scanned < scanBatch {
			break // collected enough, or the candidate scan is exhausted
		}
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

	// 5s ping cadence (was 15s) — keeps mobile carrier NATs / reverse
	// proxies happy. Idle reaps on quiet streams (after a turn ends)
	// were triggering visible reconnect noise on the mobile client.
	ping := time.NewTicker(5 * time.Second)
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
