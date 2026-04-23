package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// seedTestRun inserts a project + run owned by the given team and returns
// the run's ID. Shared by the run-metrics tests.
func seedTestRun(t *testing.T, s *Server, team string) string {
	t.Helper()
	projID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO projects (id, team_id, name, status, created_at)
		VALUES (?, ?, 'p', 'active', ?)`,
		projID, team, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	runID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO runs (id, project_id, status, created_at)
		VALUES (?, ?, 'running', ?)`,
		runID, projID, NowUTC()); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return runID
}

func TestPutRunMetrics_InsertsAndReplaces(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)

	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/metrics"

	// Initial PUT with two metrics.
	lastStep := int64(100)
	lastValue := 1.23
	status, body := doReq(t, s, token, http.MethodPut, base, map[string]any{
		"metrics": []map[string]any{
			{
				"name":         "loss",
				"points":       [][]any{{0, 2.5}, {50, 1.8}, {100, 1.23}},
				"sample_count": 3,
				"last_step":    lastStep,
				"last_value":   lastValue,
			},
			{
				"name":         "acc",
				"points":       [][]any{{0, 0.1}, {100, 0.7}},
				"sample_count": 2,
			},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("put: status=%d body=%s", status, body)
	}

	// GET returns both, sorted by name.
	status, body = doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", status, body)
	}
	var rows []metricPointsOut
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[0].Name != "acc" || rows[1].Name != "loss" {
		t.Errorf("order = [%s, %s], want [acc, loss]", rows[0].Name, rows[1].Name)
	}
	if rows[1].LastStep == nil || *rows[1].LastStep != 100 {
		t.Errorf("loss.last_step = %v, want 100", rows[1].LastStep)
	}
	if rows[1].LastValue == nil || *rows[1].LastValue != 1.23 {
		t.Errorf("loss.last_value = %v, want 1.23", rows[1].LastValue)
	}
	if rows[0].LastStep != nil {
		t.Errorf("acc.last_step = %v, want nil", rows[0].LastStep)
	}

	// Replace with a single metric — old rows must be gone.
	status, _ = doReq(t, s, token, http.MethodPut, base, map[string]any{
		"metrics": []map[string]any{
			{"name": "loss", "points": [][]any{{0, 0.5}}, "sample_count": 1},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("replace: status=%d", status)
	}
	_, body = doReq(t, s, token, http.MethodGet, base, nil)
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "loss" {
		t.Errorf("after replace got %+v, want only loss", rows)
	}
}

func TestPutRunMetrics_RejectsCrossTeamRun(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Run belongs to a different team.
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		"other-team", "other", NowUTC())
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}
	runID := seedTestRun(t, s, "other-team")

	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/metrics"
	status, _ := doReq(t, s, token, http.MethodPut, base, map[string]any{
		"metrics": []map[string]any{
			{"name": "loss", "points": [][]any{{0, 1.0}}, "sample_count": 1},
		},
	})
	if status != http.StatusNotFound {
		t.Errorf("cross-team put: status=%d want 404", status)
	}

	status, _ = doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusNotFound {
		t.Errorf("cross-team get: status=%d want 404", status)
	}
}

func TestPutRunMetrics_ValidatesPayload(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/metrics"

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"metrics": []map[string]any{
			{"points": [][]any{{0, 1.0}}},
		}}},
		{"missing points", map[string]any{"metrics": []map[string]any{
			{"name": "loss"},
		}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, _ := doReq(t, s, token, http.MethodPut, base, c.body)
			if status != http.StatusBadRequest {
				t.Errorf("status=%d want 400", status)
			}
		})
	}
}

func TestGetRunMetrics_UnknownRun(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/runs/does-not-exist/metrics", nil)
	if status != http.StatusNotFound {
		t.Errorf("status=%d want 404", status)
	}
}
