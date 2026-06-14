package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
	"github.com/termipod/hub/internal/events"
)

// eventSender resolves the authoritative sender for a posted channel
// event from the authenticated caller, not the request body (F-08).
// It returns the from_id to record and the agent id to charge spend
// against (empty unless the caller is a bound agent, so a forged
// usage_tokens block can never auto-pause a victim agent).
//
//   - agent token: bound to its own scope.agent_id; the body's from_id
//     is ignored — an agent can only speak (and spend) as itself.
//   - host token: the deputy relaying for its agents. Trust the
//     X-Agent-Id header host-runner stamps (see mcp_gateway.go); the
//     comment there always intended the hub to derive identity from it.
//   - owner/user (human via mobile): keep the supplied from_id for chat
//     provenance, but never accrue spend to a human-named target.
func eventSender(r *http.Request, bodyFromID string) (fromID, spendAgentID string) {
	fromID = bodyFromID
	tok, ok := auth.FromContext(r.Context())
	if !ok || tok == nil {
		return fromID, ""
	}
	switch tok.Kind {
	case "agent":
		var sc mcpScope
		_ = json.Unmarshal([]byte(tok.ScopeJSON), &sc)
		if sc.AgentID != "" {
			fromID = sc.AgentID
			spendAgentID = sc.AgentID
		}
	case "host":
		if h := strings.TrimSpace(r.Header.Get("X-Agent-Id")); h != "" {
			fromID = h
		}
		spendAgentID = fromID
	}
	return fromID, spendAgentID
}

// postEventIn accepts a subset of events.Event from clients.
// Server stamps id, received_ts, and schema_version.
type postEventIn struct {
	Ts            *time.Time      `json:"ts,omitempty"`
	Type          string          `json:"type"`
	FromID        string          `json:"from_id,omitempty"`
	ToIDs         []string        `json:"to_ids,omitempty"`
	Parts         []events.Part   `json:"parts,omitempty"`
	TaskID        *string         `json:"task_id,omitempty"`
	CorrelationID *string         `json:"correlation_id,omitempty"`
	PaneRef       *events.PaneRef `json:"pane_ref,omitempty"`
	UsageTokens   *events.Usage   `json:"usage_tokens,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

func (s *Server) handlePostEvent(w http.ResponseWriter, r *http.Request) {
	ch := chi.URLParam(r, "channel")
	if !s.requireChannelTeam(w, r, ch) {
		return
	}

	var in postEventIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if in.Type == "" {
		writeErr(w, http.StatusBadRequest, "type required")
		return
	}
	if reason := validateEventParts(in.Parts); reason != "" {
		writeErr(w, http.StatusUnprocessableEntity, reason)
		return
	}

	now := time.Now().UTC()
	ts := now
	if in.Ts != nil {
		ts = in.Ts.UTC()
	}

	partsJSON, _ := json.Marshal(coalesceParts(in.Parts))
	toIDsJSON, _ := json.Marshal(coalesceStrings(in.ToIDs))
	var paneRefJSON, usageJSON []byte
	if in.PaneRef != nil {
		paneRefJSON, _ = json.Marshal(in.PaneRef)
	}
	if in.UsageTokens != nil {
		usageJSON, _ = json.Marshal(in.UsageTokens)
	}
	meta := in.Metadata
	if len(meta) == 0 {
		meta = []byte("{}")
	}

	id := NewID()

	// F-08: the sender is derived from the authenticated token, never
	// the body. spendAgentID is empty unless the caller is a bound
	// agent, so a forged usage_tokens block cannot pause a victim.
	fromID, spendAgentID := eventSender(r, in.FromID)

	_, err := s.writeDB.ExecContext(r.Context(), `
		INSERT INTO events (
			id, schema_version, ts, received_ts, channel_id, type,
			from_id, to_ids_json, parts_json, task_id, correlation_id,
			pane_ref_json, usage_tokens_json, metadata_json
		) VALUES (?, 1, ?, ?, ?, ?,
		          NULLIF(?, ''), ?, ?, ?, ?,
		          ?, ?, ?)`,
		id,
		ts.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		ch,
		in.Type,
		fromID,
		string(toIDsJSON),
		string(partsJSON),
		nullStr(in.TaskID),
		nullStr(in.CorrelationID),
		nullBytes(paneRefJSON),
		nullBytes(usageJSON),
		string(meta),
	)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	s.logEventJSONL(r.Context(), id)

	// If the event carries usage cost, accumulate onto the sender agent
	// and auto-pause if spent ≥ budget. Enforcement is inline so there's no
	// race between "over budget" and the next tool call spending more.
	if in.UsageTokens != nil && in.UsageTokens.CostCents != 0 && spendAgentID != "" {
		s.accumulateSpend(r.Context(), spendAgentID, in.UsageTokens.CostCents)
	}

	evt := map[string]any{
		"id":             id,
		"schema_version": 1,
		"ts":             ts.Format(time.RFC3339Nano),
		"received_ts":    now.Format(time.RFC3339Nano),
		"channel_id":     ch,
		"type":           in.Type,
		"from_id":        fromID,
		"to_ids":         json.RawMessage(toIDsJSON),
		"parts":          json.RawMessage(partsJSON),
		"metadata":       json.RawMessage(meta),
	}
	if in.TaskID != nil {
		evt["task_id"] = *in.TaskID
	}
	if in.CorrelationID != nil {
		evt["correlation_id"] = *in.CorrelationID
	}
	if len(paneRefJSON) > 0 {
		evt["pane_ref"] = json.RawMessage(paneRefJSON)
	}
	if len(usageJSON) > 0 {
		evt["usage_tokens"] = json.RawMessage(usageJSON)
	}
	s.bus.Publish(ch, evt)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          id,
		"received_ts": now.Format(time.RFC3339Nano),
	})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	ch := chi.URLParam(r, "channel")
	if !s.requireChannelTeam(w, r, ch) {
		return
	}
	since := r.URL.Query().Get("since") // received_ts exclusive
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	var (
		rows *sql.Rows
		err  error
	)
	if since != "" {
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT id, schema_version, ts, received_ts, channel_id, type,
			       COALESCE(from_id, ''), to_ids_json, parts_json,
			       task_id, correlation_id,
			       pane_ref_json, usage_tokens_json, metadata_json
			FROM events
			WHERE channel_id = ? AND received_ts > ?
			ORDER BY received_ts ASC LIMIT ?`, ch, since, limit)
	} else {
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT id, schema_version, ts, received_ts, channel_id, type,
			       COALESCE(from_id, ''), to_ids_json, parts_json,
			       task_id, correlation_id,
			       pane_ref_json, usage_tokens_json, metadata_json
			FROM events
			WHERE channel_id = ?
			ORDER BY received_ts DESC LIMIT ?`, ch, limit)
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		m, err := scanEventRow(rows)
		if err != nil {
			s.writeDBErr(w, err)
			return
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

// scanEventRow decodes one row from the events SELECT used by list and stream
// backfill. The SELECT shape is fixed: callers must use the column order
// documented at the top of handleListEvents.
func scanEventRow(r rowScanner) (map[string]any, error) {
	var (
		id, ts, received, channelID, typ, fromID string
		toIDs, parts, meta                       string
		taskID, correlationID                    sql.NullString
		paneRef, usage                           sql.NullString
		schemaVersion                            int
	)
	if err := r.Scan(&id, &schemaVersion, &ts, &received, &channelID, &typ,
		&fromID, &toIDs, &parts, &taskID, &correlationID, &paneRef, &usage, &meta); err != nil {
		return nil, err
	}
	m := map[string]any{
		"id":             id,
		"schema_version": schemaVersion,
		"ts":             ts,
		"received_ts":    received,
		"channel_id":     channelID,
		"type":           typ,
		"from_id":        fromID,
		"to_ids":         json.RawMessage(toIDs),
		"parts":          json.RawMessage(parts),
		"metadata":       json.RawMessage(meta),
	}
	if taskID.Valid {
		m["task_id"] = taskID.String
	}
	if correlationID.Valid {
		m["correlation_id"] = correlationID.String
	}
	if paneRef.Valid {
		m["pane_ref"] = json.RawMessage(paneRef.String)
	}
	if usage.Valid {
		m["usage_tokens"] = json.RawMessage(usage.String)
	}
	return m, nil
}

func coalesceParts(p []events.Part) []events.Part {
	if p == nil {
		return []events.Part{}
	}
	return p
}

func coalesceStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nullStr(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
