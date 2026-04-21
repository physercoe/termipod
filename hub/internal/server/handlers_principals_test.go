package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/auth"
)

// rawCall issues an authed request and returns (status, raw body bytes).
// The e2e helper's c.call() JSON-decodes into a map which doesn't fit list
// responses; we need raw bytes here.
func rawCall(t *testing.T, token, url, method string, body any) (int, []byte) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, buf)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// TestListPrincipals_CoalesceByHandle verifies that three tokens — two
// sharing a handle, one anonymous — collapse into exactly two rows and
// that the unnamed bucket is sorted last.
func TestListPrincipals_CoalesceByHandle(t *testing.T) {
	c := newE2E(t)

	// Seed extra principal tokens so the coalescing logic actually has
	// something to merge. The owner token from Init() covers the default
	// case; we add one named pair and one explicit unnamed.
	ctx := context.Background()
	for _, scope := range []map[string]any{
		{"team": defaultTeamID, "role": "principal", "handle": "alice"},
		{"team": defaultTeamID, "role": "principal", "handle": "alice"},
		{"team": defaultTeamID, "role": "principal"}, // unnamed
	} {
		b, _ := json.Marshal(scope)
		tok := auth.NewToken()
		if err := auth.InsertToken(ctx, c.s.db, "user", string(b), tok, NewID(), NowUTC()); err != nil {
			t.Fatalf("seed token: %v", err)
		}
	}

	status, raw := rawCall(t, c.token, c.srv.URL+"/v1/teams/"+c.teamID+"/principals", "GET", nil)
	if status != 200 {
		t.Fatalf("list principals = %d body=%s", status, raw)
	}
	var got []principalOut
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	// Expected shape: alice (2 tokens) + one unnamed bucket. The Init()
	// owner token is scope.role="principal" without a handle, so it merges
	// into the unnamed bucket (making its count 2 including our seeded one).
	if len(got) != 2 {
		t.Fatalf("want 2 principals, got %d: %+v", len(got), got)
	}
	if got[0].Handle != "alice" || got[0].TokenCount != 2 || got[0].Unnamed {
		t.Errorf("row 0 = %+v, want alice/2/named", got[0])
	}
	if !got[1].Unnamed || got[1].TokenCount < 1 {
		t.Errorf("row 1 = %+v, want unnamed with >=1 tokens", got[1])
	}
}

// TestTeamChannels_CreateListAndEventRoundTrip exercises the new
// /v1/teams/{team}/channels tree end-to-end: list the auto-seeded
// #hub-meta channel, create another, post + list an event via the reused
// project-scope event handlers.
func TestTeamChannels_CreateListAndEventRoundTrip(t *testing.T) {
	c := newE2E(t)

	// List: hub-meta should already exist from Init().
	status, raw := rawCall(t, c.token, c.srv.URL+"/v1/teams/"+c.teamID+"/channels", "GET", nil)
	if status != 200 {
		t.Fatalf("list team channels = %d body=%s", status, raw)
	}
	var channels []channelOut
	if err := json.Unmarshal(raw, &channels); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	var metaID string
	for _, ch := range channels {
		if ch.Name == "hub-meta" && ch.ScopeKind == "team" {
			metaID = ch.ID
			break
		}
	}
	if metaID == "" {
		t.Fatalf("#hub-meta not auto-seeded; got %+v", channels)
	}

	// Post a message to hub-meta.
	evtPath := filepath.Join("/v1/teams/", c.teamID, "channels", metaID, "events")
	status, raw = rawCall(t, c.token, c.srv.URL+evtPath, "POST", map[string]any{
		"type":  "message",
		"parts": []map[string]any{{"kind": "text", "text": "hello from test"}},
	})
	if status != 201 {
		t.Fatalf("post team event = %d body=%s", status, raw)
	}

	// Read it back.
	status, raw = rawCall(t, c.token, c.srv.URL+evtPath, "GET", nil)
	if status != 200 {
		t.Fatalf("list team events = %d body=%s", status, raw)
	}
	var events []map[string]any
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("decode events: %v body=%s", err, raw)
	}
	if len(events) == 0 {
		t.Fatalf("no events returned")
	}

	// Create a second team-scope channel.
	status, raw = rawCall(t, c.token, c.srv.URL+"/v1/teams/"+c.teamID+"/channels", "POST",
		map[string]any{"name": "ops-announcements"})
	if status != 201 {
		t.Fatalf("create team channel = %d body=%s", status, raw)
	}
}
