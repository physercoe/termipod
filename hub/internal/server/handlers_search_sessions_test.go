package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Phase 1.5c: FTS5 over agent event payloads, scoped to the
// caller's team. A query that matches text in one session's
// transcript returns a single row pointing back to that session
// + the matching event seq.
func TestSessionSearch_HappyPath(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-x")

	// Open a session and stamp three events with distinguishable
	// content so we can assert the right one comes back.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "search test", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	for _, txt := range []string{"AdamW with cosine schedule", "lion with constant", "another unrelated note"} {
		st, body := doReq(t, s, token, http.MethodPost,
			"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
			map[string]any{
				"kind":     "text",
				"producer": "agent",
				"payload":  map[string]any{"text": txt},
			})
		if st != http.StatusCreated {
			t.Fatalf("post event: %s", body)
		}
	}

	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/search?q=AdamW", nil)
	if status != http.StatusOK {
		t.Fatalf("search: status=%d body=%s", status, body)
	}
	var results []map[string]any
	_ = json.Unmarshal(body, &results)
	if len(results) != 1 {
		t.Fatalf("hits=%d; want 1; body=%s", len(results), body)
	}
	hit := results[0]
	if hit["session_id"] != ses.ID {
		t.Errorf("session_id=%q; want %q", hit["session_id"], ses.ID)
	}
	if !strings.Contains((hit["snippet"]).(string), "AdamW") {
		t.Errorf("snippet did not echo the match: %s", hit["snippet"])
	}
}

// Soft-deleted sessions drop out of search even though the FTS
// row still exists (delete NULLs session_id on agent_events; the
// JOIN with sessions filters via team_id + status != deleted).
func TestSessionSearch_HidesDeletedSessions(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-x")

	// Open a session, stamp an event, archive + delete the session.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "doomed", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "text",
			"producer": "agent",
			"payload":  map[string]any{"text": "uniqueDoomedString123"},
		})
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/archive", nil)
	doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID, nil)

	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/search?q=uniqueDoomedString123",
		nil)
	if status != http.StatusOK {
		t.Fatalf("search: status=%d body=%s", status, body)
	}
	var results []map[string]any
	_ = json.Unmarshal(body, &results)
	if len(results) != 0 {
		t.Errorf("deleted session leaked into results: %s", body)
	}
}

// Empty q → 400, not a runaway match-all query.
func TestSessionSearch_RequiresQuery(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/search?q=", nil)
	if status != http.StatusBadRequest {
		t.Errorf("empty q: status=%d; want 400", status)
	}
}
