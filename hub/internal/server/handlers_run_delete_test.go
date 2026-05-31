package server

import (
	"context"
	"net/http"
	"testing"
)

// TestDeleteRun_DropsRunCascadesDigestsDetachesArtifacts confirms a run
// delete removes the run + its metric digests (FK cascade) and detaches
// (does not delete) artifacts it produced.
func TestDeleteRun_DropsRunCascadesDigestsDetachesArtifacts(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)

	// Find the run's project for the artifact insert.
	var projID string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT project_id FROM runs WHERE id = ?`, runID).Scan(&projID); err != nil {
		t.Fatalf("read project: %v", err)
	}

	// A metric digest (cascades) and an artifact (detaches).
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO run_metrics (id, run_id, metric_name, points_json, sample_count, updated_at)
		VALUES (?, ?, 'loss', '[[0,1.0]]', 1, ?)`,
		NewID(), runID, NowUTC()); err != nil {
		t.Fatalf("seed metric: %v", err)
	}
	artID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO artifacts (id, project_id, run_id, kind, name, uri, created_at)
		VALUES (?, ?, ?, 'log', 'a', 'blob:sha256/deadbeef', ?)`,
		artID, projID, runID, NowUTC()); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID
	status, body := doReq(t, s, token, http.MethodDelete, base, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", status, body)
	}

	// Run gone.
	if status, _ := doReq(t, s, token, http.MethodGet, base, nil); status != http.StatusNotFound {
		t.Errorf("run still present: get status=%d", status)
	}
	// Metric digest cascaded away.
	var nMetrics int
	_ = s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM run_metrics WHERE run_id = ?`, runID).Scan(&nMetrics)
	if nMetrics != 0 {
		t.Errorf("run_metrics not cascaded: %d rows", nMetrics)
	}
	// Artifact survives but is detached.
	var runRef interface{}
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT run_id FROM artifacts WHERE id = ?`, artID).Scan(&runRef); err != nil {
		t.Fatalf("artifact should survive: %v", err)
	}
	if runRef != nil {
		t.Errorf("artifact run_id not detached: %v", runRef)
	}
}

// TestDetachRunArtifact unlinks an artifact from a run, keeping it.
func TestDetachRunArtifact(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	var projID string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT project_id FROM runs WHERE id = ?`, runID).Scan(&projID); err != nil {
		t.Fatalf("read project: %v", err)
	}
	artID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO artifacts (id, project_id, run_id, kind, name, uri, created_at)
		VALUES (?, ?, ?, 'log', 'a', 'blob:sha256/deadbeef', ?)`,
		artID, projID, runID, NowUTC()); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/artifacts/" + artID
	status, body := doReq(t, s, token, http.MethodDelete, base, nil)
	if status != http.StatusNoContent {
		t.Fatalf("detach: status=%d body=%s", status, body)
	}
	// Artifact survives, run_id cleared.
	var runRef interface{}
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT run_id FROM artifacts WHERE id = ?`, artID).Scan(&runRef); err != nil {
		t.Fatalf("artifact should survive: %v", err)
	}
	if runRef != nil {
		t.Errorf("run_id not cleared: %v", runRef)
	}
	// Detaching again → 404 (no longer attached).
	if status, _ := doReq(t, s, token, http.MethodDelete, base, nil); status != http.StatusNotFound {
		t.Errorf("second detach: want 404, got %d", status)
	}
}
