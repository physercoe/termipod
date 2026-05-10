package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// handlers_mobile_intent.go — POST /v1/teams/{team}/mobile/intent
// (agent-driven mobile UI prototype, v1.0.464+).
//
// The endpoint accepts a `termipod://` URI from a steward MCP tool
// (`mobile.navigate`) and fans it out to mobile clients via the
// general steward's existing SSE channel. Mobile, while the
// floating overlay is open, subscribes to that channel and reacts
// to `mobile.intent` events by parsing the URI and pushing the
// matching Navigator stack.
//
// Why the general steward's channel and not a per-team one? Two
// reasons. (a) Mobile already subscribes to the steward's stream
// when the overlay is open — no new subscription path. (b) Pinning
// delivery to "the channel the user is talking on" matches the
// shared-state model from `discussions/agent-driven-mobile-ui.md`
// §2.4: the steward and the user are co-located on one channel.
//
// Future versions may add a per-team mobile channel for intents
// that originate outside any steward conversation; the wedge plan
// (post-MVP) covers it.

// mobileIntentIn is the request body shape. `uri` is required; any
// other fields are accepted but ignored to leave room for forward-
// compat additions (kind=tap/set_text/etc when write intents land).
type mobileIntentIn struct {
	URI string `json:"uri"`
}

// mobileIntentOut echoes back the acknowledged intent + the agent
// channel it landed on. Useful for the MCP tool's stdout (the
// steward sees confirmation that mobile received it).
type mobileIntentOut struct {
	URI       string `json:"uri"`
	Delivered bool   `json:"delivered"`
	Channel   string `json:"channel,omitempty"`
}

// handleMobileIntent validates a navigate intent and publishes it
// onto the general steward's bus channel. Read-only verbs only at
// this stage — write intents (set_text, approve, ratify) are
// post-prototype per the discussion doc's wedge plan.
func (s *Server) handleMobileIntent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	ctx := r.Context()

	var body mobileIntentIn
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	body.URI = strings.TrimSpace(body.URI)
	if body.URI == "" {
		writeErr(w, http.StatusBadRequest, "uri is required")
		return
	}

	// Validate the URI grammar. We accept termipod:// (current) and
	// the legacy muxpod:// scheme so existing deep-link infrastructure
	// composes (DeepLinkService.parseUri already accepts both).
	parsed, err := url.Parse(body.URI)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "uri parse: "+err.Error())
		return
	}
	if parsed.Scheme != "termipod" && parsed.Scheme != "muxpod" {
		writeErr(w, http.StatusBadRequest,
			"uri scheme must be termipod:// or muxpod://")
		return
	}

	// Look up the team's general steward — that's the bus channel we
	// publish to. If no steward is running, the intent has nowhere to
	// land; surface that explicitly so the caller (steward MCP tool)
	// can decide whether to retry after ensure-spawn.
	stewardID, err := s.findRunningGeneralSteward(ctx, team)
	if errors.Is(err, sql.ErrNoRows) || stewardID == "" {
		writeErr(w, http.StatusFailedDependency,
			"no general steward is running for team "+team)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "steward lookup: "+err.Error())
		return
	}

	channel := agentBusKey(stewardID)
	// Stamp session_id so the mobile overlay's session-filtered SSE
	// subscription doesn't drop us. handleStreamAgentEvents filters
	// `eventSessionID(evt) != sessionFilter`; without this lookup the
	// event has no session_id and the subscriber sees nothing — the
	// pill never appears, navigation never fires (v1.0.479 fix).
	sessionID := s.lookupSessionForAgent(ctx, stewardID)
	evt := map[string]any{
		"kind":       "mobile.intent",
		"intent":     "navigate",
		"uri":        body.URI,
		"agent_id":   stewardID,
		"team_id":    team,
		"session_id": sessionID,
		"ts":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.bus.Publish(channel, evt)

	// Audit trail — every steward-driven UI action lands in
	// audit_events so the user (and the future audit feed UI) can
	// review what the steward did. Per Q5 in the discussion doc.
	s.recordAudit(ctx, team, "mobile.intent", "agent", stewardID,
		"steward → navigate "+body.URI,
		map[string]any{
			"uri":    body.URI,
			"intent": "navigate",
		})

	writeJSON(w, http.StatusOK, mobileIntentOut{
		URI:       body.URI,
		Delivered: true,
		Channel:   channel,
	})
}
