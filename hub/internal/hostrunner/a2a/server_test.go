package a2a

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fixedSource(agents []AgentInfo) AgentSource {
	return func(ctx context.Context) ([]AgentInfo, error) { return agents, nil }
}

func TestAgentCardForLiveAgent(t *testing.T) {
	s := &Server{
		PublicURL: "http://host.example:8801",
		Source: fixedSource([]AgentInfo{
			{ID: "ag-1", Handle: "ml-worker-1"},
		}),
	}
	req := httptest.NewRequest(http.MethodGet, "/a2a/ag-1/.well-known/agent.json", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	var card AgentCard
	if err := json.Unmarshal(rr.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if card.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion: got %q want %q", card.ProtocolVersion, ProtocolVersion)
	}
	if card.Name != "ml-worker-1" {
		t.Errorf("name: got %q", card.Name)
	}
	if card.URL != "http://host.example:8801/a2a/ag-1" {
		t.Errorf("url: got %q", card.URL)
	}
	if len(card.Skills) == 0 || card.Skills[0].ID != "train" {
		t.Errorf("skills: got %+v, want train", card.Skills)
	}
}

func TestAgentCardUnknownAgentIs404(t *testing.T) {
	s := &Server{Source: fixedSource(nil)}
	req := httptest.NewRequest(http.MethodGet, "/a2a/ag-nope/.well-known/agent.json", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", rr.Code)
	}
}

func TestAgentCardDerivesURLFromHostHeader(t *testing.T) {
	s := &Server{Source: fixedSource([]AgentInfo{{ID: "a1", Handle: "steward-x"}})}
	req := httptest.NewRequest(http.MethodGet, "/a2a/a1/.well-known/agent.json", nil)
	req.Host = "127.0.0.1:9999"
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var card AgentCard
	_ = json.Unmarshal(rr.Body.Bytes(), &card)
	if card.URL != "http://127.0.0.1:9999/a2a/a1" {
		t.Errorf("url: got %q", card.URL)
	}
	// steward handle -> plan + brief skills
	if len(card.Skills) != 2 {
		t.Errorf("skills count: got %d want 2", len(card.Skills))
	}
}

func TestListAgents(t *testing.T) {
	s := &Server{Source: fixedSource([]AgentInfo{
		{ID: "a", Handle: "steward-1"},
		{ID: "b", Handle: "ml-worker-1"},
	})}
	req := httptest.NewRequest(http.MethodGet, "/a2a/agents", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var body struct {
		Agents []string `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Agents) != 2 || body.Agents[0] != "a" || body.Agents[1] != "b" {
		t.Errorf("agents: got %+v", body.Agents)
	}
}

func TestUnknownSubpath404(t *testing.T) {
	s := &Server{Source: fixedSource([]AgentInfo{{ID: "a1", Handle: "x"}})}
	req := httptest.NewRequest(http.MethodGet, "/a2a/a1/tasks", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404 (task endpoints land later)", rr.Code)
	}
}

func TestListenServesOverTCP(t *testing.T) {
	s := &Server{Source: fixedSource([]AgentInfo{{ID: "a1", Handle: "steward-1"}})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := s.Listen(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer cancel()

	resp, err := http.Get("http://" + addr + "/a2a/a1/.well-known/agent.json")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d body=%s", resp.StatusCode, string(b))
	}
	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if card.Name != "steward-1" {
		t.Errorf("name: got %q want steward-1", card.Name)
	}
	// Request came from 127.0.0.1:<port>; URL should reflect it.
	if card.URL == "" {
		t.Errorf("url empty")
	}
	// Give shutdown a moment when ctx cancels.
	_ = time.Now
}

func TestSkillsForHandle(t *testing.T) {
	cases := []struct {
		handle string
		want   int
	}{
		{"steward-v1", 2},
		{"ml-worker-xyz", 1},
		{"worker-abc", 1},
		{"briefing-42", 1},
		{"random-agent", 0},
	}
	for _, c := range cases {
		got := SkillsForHandle(c.handle)
		if len(got) != c.want {
			t.Errorf("handle=%q: got %d skills want %d", c.handle, len(got), c.want)
		}
	}
}
