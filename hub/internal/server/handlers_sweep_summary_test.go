package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// Sweep-summary endpoint covers the wandb "parallel coords" archetype —
// one row per run carrying config + final metric values. Feeds the
// mobile cross-run scatter panel on the project detail screen.
func TestProjectSweepSummary_ReturnsRunsWithFinalMetrics(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Two runs in a project, each with two metrics (loss/val, eval/accuracy)
	// plus one metric that has a NULL last_value (should be skipped in the
	// final_metrics map — only useful metrics surface).
	projID := NewID()
	now := NowUTC()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO projects (id, team_id, name, status, created_at)
		VALUES (?, ?, 'sweep', 'active', ?)`,
		projID, defaultTeamID, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	runA, runB := NewID(), NewID()
	insertRun := func(id, configJSON string) {
		t.Helper()
		if _, err := s.db.ExecContext(context.Background(), `
			INSERT INTO runs (id, project_id, status, config_json, created_at)
			VALUES (?, ?, 'completed', ?, ?)`,
			id, projID, configJSON, now); err != nil {
			t.Fatalf("seed run %s: %v", id, err)
		}
	}
	insertRun(runA, `{"n_embd":128,"optimizer":"adamw"}`)
	insertRun(runB, `{"n_embd":256,"optimizer":"lion"}`)

	insertMetric := func(runID, name string, lastValue *float64) {
		t.Helper()
		var lv any = nil
		if lastValue != nil {
			lv = *lastValue
		}
		if _, err := s.db.ExecContext(context.Background(), `
			INSERT INTO run_metrics
				(id, run_id, metric_name, points_json, sample_count,
				 last_step, last_value, updated_at)
			VALUES (?, ?, ?, '[]', 1, 100, ?, ?)`,
			NewID(), runID, name, lv, now); err != nil {
			t.Fatalf("seed metric %s/%s: %v", runID, name, err)
		}
	}
	fvA, fvB := 1.85, 1.23
	accA, accB := 0.74, 0.86
	insertMetric(runA, "loss/val", &fvA)
	insertMetric(runA, "eval/accuracy", &accA)
	insertMetric(runB, "loss/val", &fvB)
	insertMetric(runB, "eval/accuracy", &accB)
	insertMetric(runB, "pending/metric", nil) // NULL → should not appear

	url := "/v1/teams/" + defaultTeamID + "/projects/" + projID + "/sweep-summary"
	status, body := doReq(t, s, token, http.MethodGet, url, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out []sweepRunOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d runs, want 2", len(out))
	}
	byID := map[string]*sweepRunOut{}
	for i := range out {
		byID[out[i].RunID] = &out[i]
	}
	a, b := byID[runA], byID[runB]
	if a == nil || b == nil {
		t.Fatalf("missing run in response: %+v", out)
	}
	if a.Status != "succeeded" || b.Status != "succeeded" {
		t.Errorf("statuses = (%s,%s), want both 'succeeded'", a.Status, b.Status)
	}
	if a.FinalMetrics["loss/val"] != fvA {
		t.Errorf("runA loss/val = %v, want %v", a.FinalMetrics["loss/val"], fvA)
	}
	if b.FinalMetrics["eval/accuracy"] != accB {
		t.Errorf("runB eval/accuracy = %v, want %v", b.FinalMetrics["eval/accuracy"], accB)
	}
	if _, ok := b.FinalMetrics["pending/metric"]; ok {
		t.Errorf("NULL last_value leaked into final_metrics: %+v", b.FinalMetrics)
	}
	if a.ConfigJSON != `{"n_embd":128,"optimizer":"adamw"}` {
		t.Errorf("runA config_json = %q", a.ConfigJSON)
	}
}

func TestProjectSweepSummary_EmptyProjectReturns200EmptyList(t *testing.T) {
	s, token := newA2ATestServer(t)
	projID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO projects (id, team_id, name, status, created_at)
		VALUES (?, ?, 'empty', 'active', ?)`,
		projID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	url := "/v1/teams/" + defaultTeamID + "/projects/" + projID + "/sweep-summary"
	status, body := doReq(t, s, token, http.MethodGet, url, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out []sweepRunOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("len=%d, want 0: %+v", len(out), out)
	}
}

func TestProjectSweepSummary_ProjectNotFoundReturns404(t *testing.T) {
	s, token := newA2ATestServer(t)
	url := "/v1/teams/" + defaultTeamID + "/projects/does-not-exist/sweep-summary"
	status, _ := doReq(t, s, token, http.MethodGet, url, nil)
	if status != http.StatusNotFound {
		t.Errorf("status=%d, want 404", status)
	}
}
