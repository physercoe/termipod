package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/termipod/hub/internal/hostjobs"
)

// job_sweep_test.go covers the hub half of ADR-058 §3: the progress heartbeat on
// a command row, and the sweep that fails a job whose host stopped reporting.

func seedJobHost(t *testing.T, s *Server) string {
	t.Helper()
	id := NewID()
	if _, err := s.writeDB.Exec(
		`INSERT INTO hosts (id, team_id, name, status, created_at) VALUES (?, ?, ?, 'online', ?)`,
		id, defaultTeamID, "job-host-"+id, NowUTC()); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return id
}

// seedCommand inserts a command row in `delivered` with an explicit liveness
// timestamp, matching what the delivery flip and heartbeats would have written.
func seedCommand(t *testing.T, s *Server, hostID, kind string, deliveredAgo time.Duration, progressAgo *time.Duration) string {
	t.Helper()
	id := NewID()
	stamp := func(d time.Duration) string {
		return time.Now().UTC().Add(-d).Format("2006-01-02T15:04:05.000000000Z07:00")
	}
	var progressAt any
	var progressJSON any
	if progressAgo != nil {
		progressAt = stamp(*progressAgo)
		progressJSON = `{"phase":"decoding"}`
	}
	if _, err := s.writeDB.Exec(`
		INSERT INTO host_commands
			(id, host_id, kind, args_json, status, created_at, delivered_at,
			 progress_json, progress_at)
		VALUES (?, ?, ?, '{}', 'delivered', ?, ?, ?, ?)`,
		id, hostID, kind, stamp(deliveredAgo), stamp(deliveredAgo),
		progressJSON, progressAt); err != nil {
		t.Fatalf("seed command: %v", err)
	}
	return id
}

func commandState(t *testing.T, s *Server, id string) (status, errText, progress string, progressAt *string) {
	t.Helper()
	var pa, pj *string
	if err := s.db.QueryRow(
		`SELECT status, COALESCE(error,''), progress_json, progress_at FROM host_commands WHERE id = ?`, id).
		Scan(&status, &errText, &pj, &pa); err != nil {
		t.Fatalf("read command %s: %v", id, err)
	}
	if pj != nil {
		progress = *pj
	}
	return status, errText, progress, pa
}

func patchCommand(t *testing.T, s *Server, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cmd", id)
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(string(raw))).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.handlePatchHostCommand(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// the heartbeat
// ---------------------------------------------------------------------------

// A progress patch carries no status, and must therefore leave the lifecycle
// exactly as it found it. If it completed the row, every job would report done
// the first time it said where it was.
func TestPatchHostCommand_ProgressOnlyDoesNotCompleteTheRow(t *testing.T) {
	s, _ := newTestServer(t)
	host := seedJobHost(t, s)
	id := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, time.Minute, nil)

	rec := patchCommand(t, s, id, map[string]any{
		"progress": map[string]any{"phase": "decoding", "done": 3, "total": 10},
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("progress patch = %d %s, want 204", rec.Code, rec.Body.String())
	}

	status, errText, progress, progressAt := commandState(t, s, id)
	if status != "delivered" {
		t.Fatalf("status = %q after a heartbeat, want it left at delivered", status)
	}
	if errText != "" {
		t.Fatalf("error = %q after a heartbeat, want empty", errText)
	}
	if !strings.Contains(progress, `"phase":"decoding"`) {
		t.Fatalf("progress_json = %q, want the reported phase", progress)
	}
	if progressAt == nil || *progressAt == "" {
		t.Fatal("progress_at was not stamped; the sweep has nothing to compare")
	}
	var completed *string
	if err := s.db.QueryRow(`SELECT completed_at FROM host_commands WHERE id = ?`, id).
		Scan(&completed); err != nil {
		t.Fatalf("read completed_at: %v", err)
	}
	if completed != nil {
		t.Fatalf("completed_at was set by a heartbeat: %q", *completed)
	}
}

// The hub stamps progress_at from its own clock. A host's clock must never get a
// vote on whether the hub declares that host's jobs dead.
func TestPatchHostCommand_ProgressAtIgnoresTheHostsClock(t *testing.T) {
	s, _ := newTestServer(t)
	host := seedJobHost(t, s)
	id := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, time.Minute, nil)

	// A host whose clock is a year behind.
	patchCommand(t, s, id, map[string]any{
		"progress": map[string]any{"phase": "x", "at": "2025-01-01T00:00:00Z"},
	})

	_, _, _, progressAt := commandState(t, s, id)
	if progressAt == nil {
		t.Fatal("progress_at not stamped")
	}
	if strings.HasPrefix(*progressAt, "2025") {
		t.Fatalf("progress_at = %q; the hub took the timestamp from the payload", *progressAt)
	}
}

// A bare "still here" is not evidence enough to undo a terminal verdict — only a
// real terminal patch, which carries a result, speaks for the work.
func TestPatchHostCommand_ProgressCannotReviveATerminalRow(t *testing.T) {
	s, _ := newTestServer(t)
	host := seedJobHost(t, s)
	id := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, time.Minute, nil)

	if rec := patchCommand(t, s, id, map[string]any{"status": "failed", "error": "boom"}); rec.Code != http.StatusNoContent {
		t.Fatalf("terminal patch = %d %s", rec.Code, rec.Body.String())
	}
	rec := patchCommand(t, s, id, map[string]any{"progress": map[string]any{"phase": "late"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("late heartbeat = %d, want 409", rec.Code)
	}
	status, errText, _, _ := commandState(t, s, id)
	if status != "failed" || errText != "boom" {
		t.Fatalf("row = %q/%q; a heartbeat undid a terminal state", status, errText)
	}
}

func TestPatchHostCommand_ProgressMustBeABoundedObject(t *testing.T) {
	s, _ := newTestServer(t)
	host := seedJobHost(t, s)
	id := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, time.Minute, nil)

	// Not an object.
	rec := patchCommand(t, s, id, map[string]any{"progress": []int{1, 2, 3}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("array progress = %d, want 400", rec.Code)
	}
	// Oversized: progress arrives every ~30s for a job's whole life, so an
	// unbounded payload is unbounded growth in a column nothing truncates.
	rec = patchCommand(t, s, id, map[string]any{
		"progress": map[string]any{"phase": strings.Repeat("x", maxCommandProgressBytes+1)},
	})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized progress = %d, want 413", rec.Code)
	}
}

// A patch with neither a status nor progress is still rejected — omitting the
// status is meaningful only when progress is what is being reported.
func TestPatchHostCommand_EmptyPatchIsStillRejected(t *testing.T) {
	s, _ := newTestServer(t)
	host := seedJobHost(t, s)
	id := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, time.Minute, nil)

	if rec := patchCommand(t, s, id, map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch = %d, want 400", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// the sweep
// ---------------------------------------------------------------------------

func TestSweepStaleJobs_FailsSilentJobsAndSparesTheRest(t *testing.T) {
	s, _ := newTestServer(t)
	host := seedJobHost(t, s)

	fresh := 10 * time.Second
	stale := JobStaleThreshold + time.Minute

	// Heartbeat went quiet: the host is gone.
	silent := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, 2*stale, &stale)
	// Heartbeating normally.
	alive := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, 2*stale, &fresh)
	// Delivered but not yet heartbeat — delivered_at stands in, and it is new.
	justStarted := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, fresh, nil)
	// Delivered long ago and never heartbeat at all: also gone.
	neverReported := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, 2*stale, nil)
	// An inline kind. It runs long, never heartbeats, and is not this sweep's
	// business — sweeping it would fail healthy work.
	teleport := seedCommand(t, s, host, "session_handoff_pack", 2*stale, nil)

	s.sweepStaleJobsOnce(context.Background())

	for _, id := range []string{silent, neverReported} {
		status, errText, _, _ := commandState(t, s, id)
		if status != "failed" {
			t.Fatalf("stale job %s = %q, want failed", id, status)
		}
		if errText != "host stopped reporting" {
			t.Fatalf("stale job %s error = %q", id, errText)
		}
	}
	for _, id := range []string{alive, justStarted, teleport} {
		status, _, _, _ := commandState(t, s, id)
		if status != "delivered" {
			t.Fatalf("healthy row %s was swept to %q", id, status)
		}
	}
}

// The sweep must not touch a row that already reached a terminal state, or a
// completed job would be relabelled "host stopped reporting".
func TestSweepStaleJobs_LeavesTerminalRowsAlone(t *testing.T) {
	s, _ := newTestServer(t)
	host := seedJobHost(t, s)
	stale := JobStaleThreshold + time.Minute
	id := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, 2*stale, &stale)
	if _, err := s.writeDB.Exec(
		`UPDATE host_commands SET status='done', result_json='{"path":"/x.rrd"}' WHERE id = ?`, id); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	s.sweepStaleJobsOnce(context.Background())

	status, errText, _, _ := commandState(t, s, id)
	if status != "done" || errText != "" {
		t.Fatalf("a finished job was swept: %q/%q", status, errText)
	}
}

// The list endpoint must surface progress, or the desktop has nothing to poll.
func TestListHostCommands_ExposesProgress(t *testing.T) {
	s, _ := newTestServer(t)
	host := seedJobHost(t, s)
	fresh := 5 * time.Second
	id := seedCommand(t, s, host, hostjobs.KindDatasetExportRRD, time.Minute, &fresh)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("host", host)
	req := httptest.NewRequest(http.MethodGet, "/?status=delivered", nil).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.handleListHostCommands(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}

	var out []commandOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].ID != id {
		t.Fatalf("listed %d rows, want the seeded one", len(out))
	}
	if !strings.Contains(string(out[0].Progress), `"phase":"decoding"`) {
		t.Fatalf("progress = %s, want the stored phase", out[0].Progress)
	}
	if out[0].ProgressAt == nil {
		t.Fatal("progress_at missing from the wire; a poller cannot tell a stalled job from a slow one")
	}
}
