package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func seedTestHost(t *testing.T, s *Server, team, id, name string) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO hosts (id, team_id, name, status, created_at)
		 VALUES (?, ?, ?, 'online', ?)`,
		id, team, name, NowUTC())
	if err != nil {
		t.Fatalf("seed host %s: %v", id, err)
	}
}

func newA2ATestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	token, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, token
}

func doReq(t *testing.T, s *Server, token, method, path string, body any) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

func TestPutHostA2ACards_InsertsAndReplaces(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-gpu", "gpu-1")

	// Initial PUT with two cards.
	status, body := doReq(t, s, token, http.MethodPut,
		"/v1/teams/"+defaultTeamID+"/hosts/host-gpu/a2a/cards",
		map[string]any{
			"cards": []map[string]any{
				{
					"agent_id": "agent-1",
					"handle":   "worker.ml",
					"card":     map[string]any{"name": "worker.ml", "url": "http://host/a2a/agent-1"},
				},
				{
					"agent_id": "agent-2",
					"handle":   "briefing",
					"card":     map[string]any{"name": "briefing", "url": "http://host/a2a/agent-2"},
				},
			},
		})
	if status != http.StatusOK {
		t.Fatalf("initial put: status=%d body=%s", status, body)
	}

	// GET filtered by handle.
	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/a2a/cards?handle=worker.ml", nil)
	if status != http.StatusOK {
		t.Fatalf("get by handle: status=%d body=%s", status, body)
	}
	var cards []a2aCardOut
	if err := json.Unmarshal(body, &cards); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cards) != 1 || cards[0].AgentID != "agent-1" || cards[0].HostID != "host-gpu" {
		t.Errorf("filtered cards = %+v, want 1 card for agent-1 on host-gpu", cards)
	}

	// Replace with a smaller set — the old second card must disappear.
	status, _ = doReq(t, s, token, http.MethodPut,
		"/v1/teams/"+defaultTeamID+"/hosts/host-gpu/a2a/cards",
		map[string]any{
			"cards": []map[string]any{
				{
					"agent_id": "agent-1",
					"handle":   "worker.ml",
					"card":     map[string]any{"name": "worker.ml"},
				},
			},
		})
	if status != http.StatusOK {
		t.Fatalf("replacement put: status=%d", status)
	}
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/a2a/cards", nil)
	if err := json.Unmarshal(body, &cards); err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if len(cards) != 1 || cards[0].AgentID != "agent-1" {
		t.Errorf("after replace got %+v, want only agent-1", cards)
	}
}

func TestPutHostA2ACards_RejectsCrossTeamHost(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Host exists but in a different team.
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		"other-team", "other", NowUTC())
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}
	seedTestHost(t, s, "other-team", "host-x", "other-host")

	status, _ := doReq(t, s, token, http.MethodPut,
		"/v1/teams/"+defaultTeamID+"/hosts/host-x/a2a/cards",
		map[string]any{"cards": []map[string]any{}})
	if status != http.StatusNotFound {
		t.Errorf("cross-team put: status=%d want 404", status)
	}
}

func TestPutHostA2ACards_ValidatesPayload(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-gpu", "gpu-1")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing agent_id", map[string]any{"cards": []map[string]any{
			{"handle": "w", "card": map[string]any{"x": 1}},
		}}},
		{"missing handle", map[string]any{"cards": []map[string]any{
			{"agent_id": "a", "card": map[string]any{"x": 1}},
		}}},
		{"missing card", map[string]any{"cards": []map[string]any{
			{"agent_id": "a", "handle": "w"},
		}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, _ := doReq(t, s, token, http.MethodPut,
				"/v1/teams/"+defaultTeamID+"/hosts/host-gpu/a2a/cards", c.body)
			if status != http.StatusBadRequest {
				t.Errorf("status=%d want 400", status)
			}
		})
	}
}

func TestListTeamA2ACards_AcrossHosts(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-vps", "vps")
	seedTestHost(t, s, defaultTeamID, "host-gpu", "gpu-1")

	put := func(host, agent, handle string) {
		t.Helper()
		status, body := doReq(t, s, token, http.MethodPut,
			"/v1/teams/"+defaultTeamID+"/hosts/"+host+"/a2a/cards",
			map[string]any{"cards": []map[string]any{
				{"agent_id": agent, "handle": handle, "card": map[string]any{"name": handle}},
			}})
		if status != http.StatusOK {
			t.Fatalf("put %s/%s: %d %s", host, agent, status, body)
		}
	}
	put("host-vps", "agent-s", "steward")
	put("host-gpu", "agent-w", "worker.ml")

	_, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/a2a/cards", nil)
	var all []a2aCardOut
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d cards across hosts, want 2: %+v", len(all), all)
	}

	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/a2a/cards?handle=worker.ml", nil)
	var workers []a2aCardOut
	if err := json.Unmarshal(body, &workers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(workers) != 1 || workers[0].HostID != "host-gpu" {
		t.Errorf("handle filter: got %+v, want 1 card on host-gpu", workers)
	}
}
