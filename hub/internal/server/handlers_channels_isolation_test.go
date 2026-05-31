package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// listTeamChannels GETs a team's team-scope channels as the given bearer.
func listTeamChannels(t *testing.T, s *Server, tok, team string) []channelOut {
	t.Helper()
	st, body := doReq(t, s, tok, http.MethodGet, "/v1/teams/"+team+"/channels", nil)
	if st != http.StatusOK {
		t.Fatalf("list channels for %s: status=%d body=%s", team, st, body)
	}
	var out []channelOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	return out
}

func metaID(chans []channelOut, name string) string {
	for _, c := range chans {
		if c.Name == name {
			return c.ID
		}
	}
	return ""
}

// TestChannelIsolation_TeamsDoNotShareHubMeta is the W6 acceptance test
// (ADR-037 G6, the channel leak surfaced in W3): each provisioned team
// gets its OWN #hub-meta, and one team can neither see, fetch, post to,
// nor list events on another team's team-scope channel — even though its
// own-team path is authorized by the W1 gate.
func TestChannelIsolation_TeamsDoNotShareHubMeta(t *testing.T) {
	s, operatorTok := newA2ATestServer(t)
	a := provisionTeam(t, s, operatorTok, "team-a", "A")
	b := provisionTeam(t, s, operatorTok, "team-b", "B")

	chA := listTeamChannels(t, s, a.OwnerToken, "team-a")
	chB := listTeamChannels(t, s, b.OwnerToken, "team-b")
	idA := metaID(chA, "hub-meta")
	idB := metaID(chB, "hub-meta")
	if idA == "" || idB == "" {
		t.Fatalf("each team needs its own hub-meta: a=%v b=%v", chA, chB)
	}
	if idA == idB {
		t.Fatalf("teams share one hub-meta channel id %q", idA)
	}

	// team-a's list must not include team-b's channel.
	for _, c := range chA {
		if c.ID == idB {
			t.Fatalf("team-a list leaked team-b channel %q", idB)
		}
	}

	// team-a cannot GET team-b's channel (404 — never distinguishing
	// "missing" from "foreign").
	if st, _ := doReq(t, s, a.OwnerToken, http.MethodGet,
		"/v1/teams/team-a/channels/"+idB, nil); st != http.StatusNotFound {
		t.Errorf("team-a GET team-b channel: status=%d want 404", st)
	}

	// team-a cannot POST an event into team-b's channel, nor list its
	// events — the class-level requireChannelTeam guard blocks both.
	msg := map[string]any{"type": "message",
		"parts": []map[string]any{{"kind": "text", "text": "x"}}}
	if st, _ := doReq(t, s, a.OwnerToken, http.MethodPost,
		"/v1/teams/team-a/channels/"+idB+"/events", msg); st != http.StatusNotFound {
		t.Errorf("team-a POST to team-b channel: status=%d want 404", st)
	}
	if st, _ := doReq(t, s, a.OwnerToken, http.MethodGet,
		"/v1/teams/team-a/channels/"+idB+"/events", nil); st != http.StatusNotFound {
		t.Errorf("team-a LIST team-b events: status=%d want 404", st)
	}

	// Sanity: team-a CAN post into its own hub-meta (guard passes for the
	// same team), so the guard isn't simply rejecting everything.
	if st, body := doReq(t, s, a.OwnerToken, http.MethodPost,
		"/v1/teams/team-a/channels/"+idA+"/events", msg); st != http.StatusCreated {
		t.Errorf("team-a POST own hub-meta: status=%d body=%s want 201", st, body)
	}
}

// TestSearchIsolation_ScopedToTokenTeam is the W6 acceptance test for the
// /v1/search leak: full-text search returns only events from the caller's
// own team, never another team's message text.
func TestSearchIsolation_ScopedToTokenTeam(t *testing.T) {
	s, operatorTok := newA2ATestServer(t)
	a := provisionTeam(t, s, operatorTok, "team-a", "A")
	b := provisionTeam(t, s, operatorTok, "team-b", "B")

	idA := metaID(listTeamChannels(t, s, a.OwnerToken, "team-a"), "hub-meta")
	idB := metaID(listTeamChannels(t, s, b.OwnerToken, "team-b"), "hub-meta")

	post := func(tok, team, ch, word string) {
		t.Helper()
		st, body := doReq(t, s, tok, http.MethodPost,
			"/v1/teams/"+team+"/channels/"+ch+"/events",
			map[string]any{"type": "message",
				"parts": []map[string]any{{"kind": "text", "text": word}}})
		if st != http.StatusCreated {
			t.Fatalf("post %q: status=%d body=%s", word, st, body)
		}
	}
	post(a.OwnerToken, "team-a", idA, "zebraword")
	post(b.OwnerToken, "team-b", idB, "giraffeword")

	hits := func(tok, q string) int {
		t.Helper()
		st, body := doReq(t, s, tok, http.MethodGet, "/v1/search?q="+q, nil)
		if st != http.StatusOK {
			t.Fatalf("search %q: status=%d body=%s", q, st, body)
		}
		var out []map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode search: %v", err)
		}
		return len(out)
	}

	// Each team finds its own word and NOT the other team's.
	if n := hits(a.OwnerToken, "zebraword"); n != 1 {
		t.Errorf("team-a search own word: %d hits, want 1", n)
	}
	if n := hits(a.OwnerToken, "giraffeword"); n != 0 {
		t.Errorf("team-a search team-b's word: %d hits, want 0 (leak)", n)
	}
	if n := hits(b.OwnerToken, "giraffeword"); n != 1 {
		t.Errorf("team-b search own word: %d hits, want 1", n)
	}
	if n := hits(b.OwnerToken, "zebraword"); n != 0 {
		t.Errorf("team-b search team-a's word: %d hits, want 0 (leak)", n)
	}
}
