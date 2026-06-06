package server

// Operator OTLP export (ADR-038 §4): project the stored turn index +
// tool/error events of a session into an OTLP trace and ship it to the
// operator's backend (Phoenix / Jaeger / an OTel Collector).
//
//   trace  = session         trace_id = sha256(session_id)[:16]
//   span   = turn            span_id  = sha256(session_id|turn_id)[:8]
//   child  = tool call       span_id  = sha256(session_id|tool_call_id)[:8]
//
// It is a *direct projection* of rows the hub already stores — no boundary
// synthesis — so it is purely a read. Deterministic IDs make re-export
// idempotent: re-emitting a grown prefix at each idle just updates the
// trace, which is how a long-running agent exports without live streaming.
//
// ADR-045 P2: the export loop scans the digest/event stores globally
// (sessionsWithClosedTurnsSince, loadFoldEvents, agentTurnsOrdered), so it stays
// on the global P1 handles here. It is converted to a per-team-shard iteration
// in P2 inc 3 (the per-team store split) — enumerate teams, scan each shard —
// rather than the simple team-keyed routing the single-shard sites use.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/termipod/hub/internal/otlptrace"
)

// otlpExportInterval is the idle/terminal batch cadence. A turn that closes
// (agent goes idle, or terminates) becomes visible to the next tick; the
// watermark + deterministic IDs make re-export of a grown session idempotent.
const otlpExportInterval = 30 * time.Second

// traceIDForSession — one trace per session; every agent of a resumed
// session shares it (ADR-038 §4).
func traceIDForSession(session string) [16]byte {
	h := sha256.Sum256([]byte(session))
	var id [16]byte
	copy(id[:], h[:16])
	return id
}

// spanIDFor derives a stable 8-byte span id from the trace-scoping session
// id joined with the span's own id (turn_id or tool_call_id).
func spanIDFor(session, ownID string) [8]byte {
	h := sha256.Sum256([]byte(session + "|" + ownID))
	var id [8]byte
	copy(id[:], h[:8])
	return id
}

// tsNano parses an RFC3339Nano timestamp (the format every agent_event /
// turn row uses) to Unix nanoseconds. ok=false on an empty/unparseable ts,
// so the caller skips a span it can't place on the timeline.
func tsNano(ts string) (uint64, bool) {
	if ts == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0, false
	}
	n := t.UnixNano()
	if n < 0 {
		return 0, false
	}
	return uint64(n), true
}

func (s *Server) agentKind(ctx context.Context, agentID string) string {
	var kind string
	_ = s.db.QueryRowContext(ctx, `SELECT kind FROM agents WHERE id = ?`, agentID).Scan(&kind)
	return kind
}

func (s *Server) agentTurnsOrdered(ctx context.Context, agentID string) ([]turnJSON, error) {
	rows, err := s.digestDB.QueryContext(ctx,
		`SELECT `+turnCols+` FROM agent_turns WHERE agent_id = ? ORDER BY idx ASC`, agentID)
	return scanTurns(rows, err)
}

// turnRef indexes a built turn span for child/error attachment by seq.
type turnRef struct {
	startSeq int64
	endSeq   int64
	spanID   [8]byte
	pos      int // index into the spans slice
}

func enclosingTurn(refs []turnRef, seq int64) (turnRef, bool) {
	for _, r := range refs {
		if seq >= r.startSeq && seq <= r.endSeq {
			return r, true
		}
	}
	return turnRef{}, false
}

func turnSpanStatus(t turnJSON) otlptrace.Status {
	if t.Status == "" || t.Status == "success" {
		return otlptrace.Status{Code: otlptrace.StatusOK}
	}
	return otlptrace.Status{Code: otlptrace.StatusError, Message: t.Status}
}

func turnSpanAttrs(kind, agentID string, t turnJSON) []otlptrace.Attr {
	attrs := []otlptrace.Attr{
		otlptrace.String("termipod.agent_id", agentID),
		otlptrace.Int("termipod.turn.idx", int64(t.Idx)),
		otlptrace.Int("termipod.tool_count", t.ToolCount),
		otlptrace.Int("termipod.error_count", t.ErrorCount),
	}
	if kind != "" {
		// OTel GenAI convention: the agent engine is the gen_ai system.
		attrs = append(attrs, otlptrace.String("gen_ai.system", kind))
	}
	if t.InTokens > 0 {
		attrs = append(attrs, otlptrace.Int("gen_ai.usage.input_tokens", t.InTokens))
	}
	if t.OutTokens > 0 {
		attrs = append(attrs, otlptrace.Int("gen_ai.usage.output_tokens", t.OutTokens))
	}
	if t.CostUSD > 0 {
		attrs = append(attrs, otlptrace.Float("cost_usd", t.CostUSD))
	}
	return attrs
}

// errorMessageOf pulls a human-readable message from an error/tool_result
// payload, trying the keys different engines use.
func errorMessageOf(p map[string]any) string {
	for _, k := range []string{"message", "error", "text", "reason", "content"} {
		if v := stringOf(p[k]); v != "" {
			return v
		}
	}
	return ""
}

func exceptionEvent(timeNano uint64, class, message string) otlptrace.Event {
	attrs := []otlptrace.Attr{otlptrace.String("exception.type", class)}
	if message != "" {
		attrs = append(attrs, otlptrace.String("exception.message", message))
	}
	return otlptrace.Event{Name: "exception", TimeNano: timeNano, Attrs: attrs}
}

type toolResultInfo struct {
	ts      string
	isError bool
}

// buildSessionSpans is the projection: it reads the session's agents, their
// closed turns, and the tool/error events, and returns the OTLP span tree.
// Open turns (no end yet) are skipped until they close — the next export
// tick picks them up. Output order is deterministic (events in seq order).
func (s *Server) buildSessionSpans(ctx context.Context, session string) ([]otlptrace.Span, error) {
	trace := traceIDForSession(session)
	// OTLP only ever exports a real (control-row-backed) session, so its shard
	// resolves from the session row (ADR-045 P2). The per-team-shard fan-out of
	// the surrounding export loop lands in P2 inc 3.
	team, err := s.teamForSession(ctx, session)
	if err != nil {
		return nil, err
	}
	agentIDs, err := s.sessionAgentIDs(ctx, team, session)
	if err != nil {
		return nil, err
	}
	var spans []otlptrace.Span

	for _, agentID := range agentIDs {
		kind := s.agentKind(ctx, agentID)
		turns, err := s.agentTurnsOrdered(ctx, agentID)
		if err != nil {
			return nil, err
		}
		var refs []turnRef
		for _, t := range turns {
			if t.EndSeq == 0 || t.EndTS == "" {
				continue // open turn — export once it closes
			}
			start, ok1 := tsNano(t.StartTS)
			end, ok2 := tsNano(t.EndTS)
			if !ok1 || !ok2 {
				continue
			}
			sid := spanIDFor(session, t.TurnID)
			spans = append(spans, otlptrace.Span{
				TraceID:   trace,
				SpanID:    sid,
				Name:      fmt.Sprintf("turn %d", t.Idx),
				Kind:      otlptrace.SpanKindInternal,
				StartNano: start,
				EndNano:   end,
				Attrs:     turnSpanAttrs(kind, agentID, t),
				Status:    turnSpanStatus(t),
			})
			refs = append(refs, turnRef{t.StartSeq, t.EndSeq, sid, len(spans) - 1})
		}

		events, err := loadFoldEvents(ctx, s.eventsDB, agentID)
		if err != nil {
			return nil, err
		}

		// Pre-scan tool results so a tool_call can be paired in one pass.
		results := map[string]toolResultInfo{}
		for _, e := range events {
			if e.Kind != "tool_result" {
				continue
			}
			if id := eventToolID(e.Kind, e.Payload); id != "" {
				results[id] = toolResultInfo{ts: e.TS, isError: boolOf(e.Payload["is_error"])}
			}
		}

		// Tool spans (child of the enclosing turn) + error span events, in
		// seq order for deterministic output.
		for _, e := range events {
			switch e.Kind {
			case "tool_call":
				id := eventToolID(e.Kind, e.Payload)
				if id == "" {
					continue
				}
				start, ok := tsNano(e.TS)
				if !ok {
					continue
				}
				ref, ok := enclosingTurn(refs, e.Seq)
				if !ok {
					continue // tool with no closed enclosing turn — skip for now
				}
				end := start
				isErr := false
				if r, ok := results[id]; ok {
					if n, ok2 := tsNano(r.ts); ok2 {
						end = n
					}
					isErr = r.isError
				}
				name := stringOf(e.Payload["name"])
				if name == "" {
					name = "tool"
				}
				status := otlptrace.Status{Code: otlptrace.StatusOK}
				if isErr {
					status = otlptrace.Status{Code: otlptrace.StatusError}
				}
				sp := otlptrace.Span{
					TraceID:   trace,
					SpanID:    spanIDFor(session, id),
					ParentID:  ref.spanID,
					Name:      name,
					Kind:      otlptrace.SpanKindInternal,
					StartNano: start,
					EndNano:   end,
					Attrs: []otlptrace.Attr{
						otlptrace.String("gen_ai.tool.name", name),
						otlptrace.String("termipod.tool.id", id),
						otlptrace.String("termipod.agent_id", agentID),
					},
					Status: status,
				}
				if isErr {
					sp.Events = append(sp.Events,
						exceptionEvent(end, "tool_error", errorMessageOf(e.Payload)))
				}
				spans = append(spans, sp)
			case "error":
				en, ok := tsNano(e.TS)
				if !ok {
					continue
				}
				ref, ok := enclosingTurn(refs, e.Seq)
				if !ok {
					continue
				}
				spans[ref.pos].Events = append(spans[ref.pos].Events,
					exceptionEvent(en, "error", errorMessageOf(e.Payload)))
			}
		}
	}
	return spans, nil
}

// sessionsWithClosedTurnsSince returns sessions that have at least one closed
// turn, each with the max end_ts across its agents' turns — the watermark the
// export loop compares against to decide what to (re-)ship.
func (s *Server) sessionsWithClosedTurnsSince(ctx context.Context) (map[string]string, error) {
	// session_id is denormalized onto agent_turns (migration 0054), so the
	// watermark is a single-store read — no JOIN to agent_events, which lives
	// in a separate file post-split (ADR-045 step 4).
	rows, err := s.digestDB.QueryContext(ctx, `
		SELECT session_id, MAX(end_ts) AS max_end
		  FROM agent_turns
		 WHERE end_ts != '' AND session_id != ''
		 GROUP BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var session, maxEnd string
		if err := rows.Scan(&session, &maxEnd); err != nil {
			return nil, err
		}
		out[session] = maxEnd
	}
	return out, rows.Err()
}

// runOTLPExport is the export loop (ADR-038 §4). On each tick it ships every
// session whose latest closed-turn end_ts advanced past what we last exported.
// Runs until ctx is cancelled. Only launched when s.otlp != nil.
func (s *Server) runOTLPExport(ctx context.Context) {
	ticker := time.NewTicker(otlpExportInterval)
	defer ticker.Stop()
	// One sweep promptly after start so a hub restart re-ships open work
	// without waiting a full interval.
	s.exportDueSessions(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.exportDueSessions(ctx)
		}
	}
}

// exportDueSessions ships each session whose max closed-turn end_ts is newer
// than its watermark. The watermark only advances on a successful export, so a
// transient backend outage is retried on the next tick.
func (s *Server) exportDueSessions(ctx context.Context) {
	if s.otlp == nil {
		return
	}
	due, err := s.sessionsWithClosedTurnsSince(ctx)
	if err != nil {
		s.log.Warn("otlp export: scan due sessions", "err", err)
		return
	}
	for session, maxEnd := range due {
		// Compare numerically, not lexically: RFC3339Nano trims trailing
		// fractional zeros, so a string compare can invert (".5Z" > ".50001Z")
		// and *miss* an export — a silent data loss, worse than a redundant
		// (idempotent) re-ship.
		newN, ok := tsNano(maxEnd)
		if !ok {
			continue
		}
		s.otlpMu.Lock()
		prevN, _ := tsNano(s.otlpWatermark[session])
		s.otlpMu.Unlock()
		if newN <= prevN {
			continue // nothing new since last export
		}
		spans, err := s.buildSessionSpans(ctx, session)
		if err != nil {
			s.log.Warn("otlp export: build spans", "session", session, "err", err)
			continue
		}
		if err := s.otlp.Export(ctx, spans); err != nil {
			s.log.Warn("otlp export: ship", "session", session, "err", err)
			continue // leave the watermark so the next tick retries
		}
		s.otlpMu.Lock()
		s.otlpWatermark[session] = maxEnd
		s.otlpMu.Unlock()
		s.log.Debug("otlp export: shipped session", "session", session, "spans", len(spans))
	}
}
