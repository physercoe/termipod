package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// handleStreamEvents serves a channel's event stream over Server-Sent Events.
// Format: `data: {event-json}\n\n`, plus periodic `: ping` comments to keep
// intermediaries from timing the connection out.
//
// Backfill semantics: clients pass ?since=<received_ts> to receive a replay
// of anything they missed before the live stream takes over. If the client
// disconnects mid-replay we just stop — they'll reconnect with a newer since.
func (s *Server) handleStreamEvents(w http.ResponseWriter, r *http.Request) {
	ch := chi.URLParam(r, "channel")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: don't buffer SSE
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe first so nothing slips between backfill and live.
	sub := s.bus.Subscribe(ch)
	defer s.bus.Unsubscribe(ch, sub)

	if since := r.URL.Query().Get("since"); since != "" {
		s.backfill(r, w, flusher, ch, since)
	}

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

func (s *Server) backfill(r *http.Request, w http.ResponseWriter, f http.Flusher, ch, since string) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, schema_version, ts, received_ts, channel_id, type,
		       COALESCE(from_id, ''), to_ids_json, parts_json,
		       task_id, correlation_id,
		       pane_ref_json, usage_tokens_json, metadata_json
		FROM events
		WHERE channel_id = ? AND received_ts > ?
		ORDER BY received_ts ASC LIMIT 500`, ch, since)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanEventRow(rows)
		if err != nil {
			return
		}
		writeSSE(w, f, m)
	}
}

func writeSSE(w http.ResponseWriter, f http.Flusher, evt map[string]any) {
	b, err := json.Marshal(evt)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
		return
	}
	f.Flush()
}
