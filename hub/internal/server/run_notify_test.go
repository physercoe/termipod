package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// W2.10: when runs.status flips to a terminal value, notifyRunOwner
// injects a kind='run.notify' producer='system' event into the owning
// agent's most-recent active session.
func TestNotifyRunOwner_DeliversOnTerminal(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-run-notify")
	workerID := seedAgentWithActiveSession(t, s, "@worker.run", "ml-worker.v1")

	runID := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO runs (id, project_id, agent_id, status, started_at, created_at)
		VALUES (?, ?, ?, 'running', ?, ?)`,
		runID, proj, workerID, now, now); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// Simulate a terminal status update (the real path is handleCompleteRun).
	if _, err := s.db.Exec(
		`UPDATE runs SET status='completed', finished_at=? WHERE id=?`,
		now, runID,
	); err != nil {
		t.Fatalf("update run: %v", err)
	}
	s.notifyRunOwner(context.Background(), defaultTeamID, runID, "completed")

	var (
		kind, producer, payloadJSON string
	)
	if err := s.db.QueryRow(`
		SELECT kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND kind = 'run.notify'
		 ORDER BY seq DESC LIMIT 1`, workerID,
	).Scan(&kind, &producer, &payloadJSON); err != nil {
		t.Fatalf("query event: %v", err)
	}
	if producer != "system" {
		t.Errorf("producer = %q, want system", producer)
	}
	var p struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.RunID != runID {
		t.Errorf("run_id = %q, want %q", p.RunID, runID)
	}
	if p.Status != "completed" {
		t.Errorf("status = %q, want completed", p.Status)
	}
	if !strings.Contains(p.Body, "completed") {
		t.Errorf("body missing status word: %q", p.Body)
	}
}

func TestNotifyRunOwner_SkipsNonTerminal(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-run-running")
	workerID := seedAgentWithActiveSession(t, s, "@worker.running", "ml-worker.v1")
	runID := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO runs (id, project_id, agent_id, status, started_at, created_at)
		VALUES (?, ?, ?, 'running', ?, ?)`,
		runID, proj, workerID, now, now); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	s.notifyRunOwner(context.Background(), defaultTeamID, runID, "running")
	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'run.notify'`, workerID,
	).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Errorf("non-terminal fired %d notify events; want 0", count)
	}
}

func TestNotifyRunOwner_StandaloneRunSilent(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-run-standalone")
	runID := NewID()
	now := NowUTC()
	// agent_id NULL — operator-triggered run with no owning worker.
	if _, err := s.db.Exec(`
		INSERT INTO runs (id, project_id, status, created_at)
		VALUES (?, ?, 'running', ?)`,
		runID, proj, now); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	// Must not panic; must not insert any event.
	s.notifyRunOwner(context.Background(), defaultTeamID, runID, "failed")
}
