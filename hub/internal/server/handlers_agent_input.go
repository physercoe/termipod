package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// resolveRuntimeModeSwitch returns the routing token for the given
// agent's family + driving_mode (ADR-021 D4 / W2.1). Returns:
//   - "rpc" / "respawn" / "per_turn_argv" — declared route the handler
//     dispatches on.
//   - "unsupported" — declared explicitly, OR the family didn't declare
//     a route for this driving_mode (missing-key fallback so the picker
//     degrades safely instead of crashing).
//   - error — only on unexpected SQL failures; agent-not-found is folded
//     into "unsupported" because the agent_belongs_to_team check has
//     already ruled it out at the call site.
func (s *Server) resolveRuntimeModeSwitch(ctx context.Context, agentID string) (string, error) {
	var kind, drivingMode sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT kind, driving_mode FROM agents WHERE id = ?`,
		agentID).Scan(&kind, &drivingMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "unsupported", nil
		}
		return "", err
	}
	mode := drivingMode.String
	if mode == "" {
		mode = "M4"
	}
	fam, ok := s.agentFamilies.ByName(kind.String)
	if !ok {
		return "unsupported", nil
	}
	route := fam.RuntimeModeSwitch[mode]
	if route == "" {
		return "unsupported", nil
	}
	return route, nil
}

// P1.8: structured user input sink. Writes land in agent_events with
// producer='user' and kind='input.<kind>' so they share the monotonic
// seq + SSE fan-out P1.7 established. Driver dispatch (M1 ACP / M2
// stdio) reads this table — it is not triggered from here.

type agentInputIn struct {
	Kind string `json:"kind"`
	// Producer overrides the default "user" attribution stamped on the
	// agent_events row. Today the only non-default caller is the A2A
	// dispatcher, which passes "a2a" so peer-originated input is
	// distinguishable in the audit trail. Unknown values are rejected to
	// keep the vocabulary small. Empty defaults to "user".
	Producer string `json:"producer,omitempty"`
	// text
	Body string `json:"body,omitempty"`
	// approval
	Decision  string `json:"decision,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Note      string `json:"note,omitempty"`
	// OptionID lets the phone pass the exact ACP/agent-assigned option
	// identifier; M1 drivers forward it as the `optionId` in
	// session/request_permission's response outcome.
	OptionID string `json:"option_id,omitempty"`
	// cancel
	Reason string `json:"reason,omitempty"`
	// attach
	DocumentID string `json:"document_id,omitempty"`
	// set_mode / set_model (ADR-021 D4 / W2.1). ModeID is the agent's
	// availableModes id ("default", "yolo", "plan", …); ModelID is the
	// availableModels id. The hub validates the family routing token at
	// the agent's driving_mode and the driver (W2.2) validates the id
	// against the cached availableModes/availableModels list.
	ModeID  string `json:"mode_id,omitempty"`
	ModelID string `json:"model_id,omitempty"`
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
		// Valid decisions: approve/allow/deny map to "selected" on the
		// M1 wire; cancel maps to "cancelled". "approve" and "allow" are
		// aliases — "allow" matches Claude Code / ACP option naming,
		// "approve" was the original hub vocabulary before M1 landed.
		//
		// ACP M1 agents (gemini-cli) ship optionId-based decisions like
		// "proceed_once" / "proceed_always_server" / "cancel" rather
		// than the legacy semantic vocabulary. Mobile forwards the
		// optionId verbatim as both `decision` and `option_id`. When
		// option_id is set we trust it as the source of truth and
		// allow any decision string — the driver's M1 path forwards
		// option_id as the "selected" outcome regardless. Without an
		// option_id we still need a recognizable semantic decision so
		// the hub can route correctly.
		switch in.Decision {
		case "approve", "allow", "deny", "cancel":
		default:
			if in.OptionID == "" {
				writeErr(w, http.StatusBadRequest,
					"decision must be approve|allow|deny|cancel (or any value paired with option_id)")
				return
			}
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
		if in.OptionID != "" {
			payloadMap["option_id"] = in.OptionID
		}
	case "answer":
		// Generic tool-question response: the body is whatever the
		// user picked (option label, free-text reply, …) and the
		// driver wraps it as a tool_result on stdin keyed by
		// request_id. Carved off `approval` because AskUserQuestion's
		// expected reply is "the chosen option string" — overloading
		// approval would surface a clunky "allow: Red" payload to the
		// agent. RequestID is the originating tool_call id; Body is
		// the answer text.
		if in.RequestID == "" {
			writeErr(w, http.StatusBadRequest, "request_id required")
			return
		}
		if in.Body == "" {
			writeErr(w, http.StatusBadRequest, "body required")
			return
		}
		payloadMap["request_id"] = in.RequestID
		payloadMap["body"] = in.Body
	case "attention_reply":
		// Turn-based wake-up for request_approval / request_select /
		// request_help. The agent's tool returned immediately with
		// awaiting_response and the agent ended its turn; this delivery
		// is a fresh user turn (NOT a tool_result, that's `answer`'s job).
		// Server-side fan-out from /decide is the primary producer; we
		// also accept it via this HTTP handler for completeness so an
		// operator can manually wake an agent from the CLI in a pinch.
		// `kind` here is the attention's kind (approval_request | select |
		// help_request) — the driver uses it to format the user turn.
		if in.RequestID == "" {
			writeErr(w, http.StatusBadRequest, "request_id required")
			return
		}
		payloadMap["request_id"] = in.RequestID
		if in.Body != "" {
			payloadMap["body"] = in.Body
		}
		if in.Decision != "" {
			payloadMap["decision"] = in.Decision
		}
		if in.OptionID != "" {
			payloadMap["option_id"] = in.OptionID
		}
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
	case "set_mode":
		// ADR-021 D4 / W2.1 — runtime mode picker.
		if in.ModeID == "" {
			writeErr(w, http.StatusBadRequest, "mode_id required")
			return
		}
		payloadMap["mode_id"] = in.ModeID
	case "set_model":
		// ADR-021 D4 / W2.1 — runtime model picker.
		if in.ModelID == "" {
			writeErr(w, http.StatusBadRequest, "model_id required")
			return
		}
		payloadMap["model_id"] = in.ModelID
	default:
		writeErr(w, http.StatusBadRequest,
			"kind must be text|approval|answer|attention_reply|cancel|attach|set_mode|set_model")
		return
	}

	// Producer defaults to "user" to preserve legacy callers; the only
	// accepted override today is "a2a" so the audit trail can tell peer
	// submissions apart from phone/web input.
	producer := "user"
	switch in.Producer {
	case "", "user":
		producer = "user"
	case "a2a":
		producer = "a2a"
	default:
		writeErr(w, http.StatusBadRequest, "producer must be user|a2a")
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

	// ADR-021 D4 / W2.1 — set_mode/set_model routing. The family entry
	// declares one of rpc | respawn | per_turn_argv | unsupported per
	// driving_mode; we dispatch here so mobile sees a single contract
	// and only the wire path varies. rpc and per_turn_argv land as
	// regular input.* events the driver picks up via InputRouter; the
	// driver-side handlers ship in W2.2 and W2.4. respawn is hub-side
	// orchestration (W2.3) — currently a stub returning 501. unsupported
	// is the explicit "this engine path can't switch at runtime" signal.
	if in.Kind == "set_mode" || in.Kind == "set_model" {
		route, routeErr := s.resolveRuntimeModeSwitch(r.Context(), agent)
		if routeErr != nil {
			writeErr(w, http.StatusInternalServerError, routeErr.Error())
			return
		}
		switch route {
		case "rpc", "per_turn_argv":
			// Fall through to the standard event-emit path below.
		case "respawn":
			field := "mode"
			value := in.ModeID
			if in.Kind == "set_model" {
				field = "model"
				value = in.ModelID
			}
			if err := s.respawnWithSpecMutation(r.Context(), agent, field, value); err != nil {
				if errors.Is(err, errRespawnSpecMutationNotImplemented) {
					writeErr(w, http.StatusNotImplemented, err.Error())
					return
				}
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			// W2.3 will emit its own audit event from inside the helper;
			// for the W2.1 stub we 202-style ack with no row written.
			writeJSON(w, http.StatusAccepted, map[string]any{
				"routed": "respawn",
			})
			return
		case "unsupported", "":
			writeErr(w, http.StatusUnprocessableEntity,
				"engine does not support runtime "+
					strings.TrimPrefix(in.Kind, "set_")+
					" switching for this driving_mode")
			return
		default:
			writeErr(w, http.StatusInternalServerError,
				"unknown runtime_mode_switch route: "+route)
			return
		}
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
	sessionID := s.lookupSessionForAgent(r.Context(), agent)
	var seq int64
	// Same COALESCE(MAX)+1 idiom as handlePostAgentEvent — SQLite
	// serializes writes and UNIQUE(agent_id, seq) backstops any race.
	err = s.db.QueryRowContext(r.Context(), `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, NULLIF(?, '')
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, agent, ts, kind, producer, payload, sessionID, agent).Scan(&seq)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.touchSession(r.Context(), sessionID)

	evt := map[string]any{
		"id":         id,
		"agent_id":   agent,
		"seq":        seq,
		"ts":         ts,
		"kind":       kind,
		"producer":   producer,
		"payload":    json.RawMessage(payload),
		"session_id": sessionID,
	}
	s.bus.Publish(agentBusKey(agent), evt)

	// ADR-014 OQ-4: when the user types a context-mutation slash
	// command (claude `/compact` `/clear` `/rewind`, gemini
	// `/compress`), the engine truncates / rewrites its view of the
	// conversation but emits no frame back to us. Drop a typed
	// marker into agent_events so the transcript reads as an
	// operation log — same hub session, but the transcript shows
	// "this is where the engine context diverged from what you can
	// scroll through above." Best-effort: if the marker emit fails
	// the input has already been recorded so the engine still
	// receives the command; we just lose the visual marker.
	if in.Kind == "text" {
		s.maybeEmitContextMutationMarker(r.Context(), team, agent,
			sessionID, in.Body)
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "seq": seq, "ts": ts,
	})
}
