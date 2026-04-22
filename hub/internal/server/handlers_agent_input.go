package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// P1.8: structured user input sink. Writes land in agent_events with
// producer='user' and kind='input.<kind>' so they share the monotonic
// seq + SSE fan-out P1.7 established. Driver dispatch (M1 ACP / M2
// stdio) reads this table — it is not triggered from here.

type agentInputIn struct {
	Kind string `json:"kind"`
	// text
	Body string `json:"body,omitempty"`
	// approval
	Decision  string `json:"decision,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Note      string `json:"note,omitempty"`
	// cancel
	Reason string `json:"reason,omitempty"`
	// attach
	DocumentID string `json:"document_id,omitempty"`
}

func (s *Server) handlePostAgentInput(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	agent := chi.URLParam(r, "agent")
	var in agentInputIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	// Build the per-kind payload and validate required fields. The wire
	// format is flat ({"kind":"text","body":"..."}) but the stored
	// payload carries only the kind-specific fields; `kind` itself is
	// already on the event row.
	payloadMap := map[string]any{}
	switch in.Kind {
	case "text":
		if in.Body == "" {
			writeErr(w, http.StatusBadRequest, "body required")
			return
		}
		payloadMap["body"] = in.Body
	case "approval":
		if in.Decision != "approve" && in.Decision != "deny" {
			writeErr(w, http.StatusBadRequest, "decision must be approve|deny")
			return
		}
		if in.RequestID == "" {
			writeErr(w, http.StatusBadRequest, "request_id required")
			return
		}
		payloadMap["decision"] = in.Decision
		payloadMap["request_id"] = in.RequestID
		if in.Note != "" {
			payloadMap["note"] = in.Note
		}
	case "cancel":
		if in.Reason != "" {
			payloadMap["reason"] = in.Reason
		}
	case "attach":
		if in.DocumentID == "" {
			writeErr(w, http.StatusBadRequest, "document_id required")
			return
		}
		payloadMap["document_id"] = in.DocumentID
	default:
		writeErr(w, http.StatusBadRequest, "kind must be text|approval|cancel|attach")
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

	payloadBytes, err := json.Marshal(payloadMap)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload := string(payloadBytes)

	kind := "input." + in.Kind
	id := NewID()
	ts := NowUTC()
	var seq int64
	// Same COALESCE(MAX)+1 idiom as handlePostAgentEvent — SQLite
	// serializes writes and UNIQUE(agent_id, seq) backstops any race.
	err = s.db.QueryRowContext(r.Context(), `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, ?, 'user', ?
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, agent, ts, kind, payload, agent).Scan(&seq)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	evt := map[string]any{
		"id":       id,
		"agent_id": agent,
		"seq":      seq,
		"ts":       ts,
		"kind":     kind,
		"producer": "user",
		"payload":  json.RawMessage(payload),
	}
	s.bus.Publish(agentBusKey(agent), evt)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "seq": seq, "ts": ts,
	})
}
