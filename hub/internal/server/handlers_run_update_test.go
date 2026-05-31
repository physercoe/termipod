package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestUpdateRun_PatchesFields confirms PATCH /runs/{run} changes only the
// supplied fields and leaves the rest intact.
func TestUpdateRun_PatchesFields(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID

	status, body := doReq(t, s, token, http.MethodPatch, base, map[string]any{
		"status":          "completed",
		"trackio_run_uri": "trackio://proj-a/run-1",
		"trackio_host_id": "host-xyz",
	})
	if status != http.StatusOK {
		t.Fatalf("patch: status=%d body=%s", status, body)
	}
	var ro runOut
	if err := json.Unmarshal(body, &ro); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ro.Status != "completed" {
		t.Errorf("status=%q want completed", ro.Status)
	}
	if ro.TrackioRunURI != "trackio://proj-a/run-1" {
		t.Errorf("trackio_run_uri=%q", ro.TrackioRunURI)
	}
	if ro.TrackioHostID != "host-xyz" {
		t.Errorf("trackio_host_id=%q want host-xyz", ro.TrackioHostID)
	}
}

// TestUpdateRun_RejectsBadStatus rejects an out-of-enum status.
func TestUpdateRun_RejectsBadStatus(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID

	status, _ := doReq(t, s, token, http.MethodPatch, base, map[string]any{
		"status": "bogus",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("want 400 for bad status, got %d", status)
	}
}

// TestUpdateRun_DerivesTrackioHostFromAgent confirms that linking a
// trackio URI without a host fills trackio_host_id from the run's agent.
func TestUpdateRun_DerivesTrackioHostFromAgent(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Seed a host + an agent on it, then a run owned by that agent.
	hostID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO hosts (id, team_id, name, status, created_at)
		VALUES (?, ?, 'h', 'connected', ?)`,
		hostID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	projID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO projects (id, team_id, name, status, created_at)
		VALUES (?, ?, 'p', 'active', ?)`,
		projID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	agentID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO agents (id, team_id, handle, kind, status, host_id, created_at)
		VALUES (?, ?, 'w', 'claude-code', 'running', ?, ?)`,
		agentID, defaultTeamID, hostID, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	runID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO runs (id, project_id, agent_id, status, created_at)
		VALUES (?, ?, ?, 'running', ?)`,
		runID, projID, agentID, NowUTC()); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID
	status, body := doReq(t, s, token, http.MethodPatch, base, map[string]any{
		"trackio_run_uri": "trackio://proj-a/run-1",
		// trackio_host_id intentionally omitted — should derive from agent.
	})
	if status != http.StatusOK {
		t.Fatalf("patch: status=%d body=%s", status, body)
	}
	var ro runOut
	if err := json.Unmarshal(body, &ro); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ro.TrackioHostID != hostID {
		t.Errorf("trackio_host_id=%q want derived %q", ro.TrackioHostID, hostID)
	}
}
