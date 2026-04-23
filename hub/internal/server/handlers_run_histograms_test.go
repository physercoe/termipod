package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// Run histograms (migration 0018) cover the wandb "Distributions"
// archetype — per-step binned distributions of a tensor, not scalar
// time-series. Upserts by (run, metric_name, step).
func TestPutRunHistograms_InsertsAndUpserts(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)

	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/histograms"

	// Initial PUT — two metrics, one step each.
	status, body := doReq(t, s, token, http.MethodPut, base, map[string]any{
		"histograms": []map[string]any{
			{
				"name": "grads_hist/layer0",
				"step": 100,
				"buckets": map[string]any{
					"edges":  []float64{-0.1, -0.05, 0.0, 0.05, 0.1},
					"counts": []int{2, 18, 45, 20, 3},
				},
			},
			{
				"name": "weights_hist/all",
				"step": 100,
				"buckets": map[string]any{
					"edges":  []float64{0.0, 0.02, 0.04, 0.06},
					"counts": []int{120, 340, 80, 12},
				},
			},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("put: status=%d body=%s", status, body)
	}

	// Second PUT upserts the same (metric, step) — counts should be
	// replaced, not duplicated. Also lands a new step for layer0.
	status, body = doReq(t, s, token, http.MethodPut, base, map[string]any{
		"histograms": []map[string]any{
			{
				"name": "grads_hist/layer0",
				"step": 100, // same key as before
				"buckets": map[string]any{
					"edges":  []float64{-0.1, 0.0, 0.1},
					"counts": []int{30, 40, 18},
				},
			},
			{
				"name": "grads_hist/layer0",
				"step": 200, // new step
				"buckets": map[string]any{
					"edges":  []float64{-0.05, 0.0, 0.05},
					"counts": []int{60, 10},
				},
			},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("upsert put: status=%d body=%s", status, body)
	}

	// GET all — should return 3 rows total: (layer0, 100) replaced,
	// (layer0, 200) new, (all, 100) untouched.
	status, body = doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", status, body)
	}
	var rows []histogramOut
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(rows), rows)
	}
	// ORDER BY metric_name, step: grads_hist/layer0@100, grads_hist/layer0@200,
	// weights_hist/all@100.
	if rows[0].Name != "grads_hist/layer0" || rows[0].Step != 100 {
		t.Errorf("rows[0] = (%s, %d)", rows[0].Name, rows[0].Step)
	}
	if rows[1].Name != "grads_hist/layer0" || rows[1].Step != 200 {
		t.Errorf("rows[1] = (%s, %d)", rows[1].Name, rows[1].Step)
	}
	if rows[2].Name != "weights_hist/all" {
		t.Errorf("rows[2].Name = %s, want weights_hist/all", rows[2].Name)
	}

	// Row 0 was upserted: buckets should be the second PUT's counts.
	var first map[string]any
	if err := json.Unmarshal(rows[0].Buckets, &first); err != nil {
		t.Fatalf("decode row0 buckets: %v", err)
	}
	counts, _ := first["counts"].([]any)
	if len(counts) != 3 || counts[0].(float64) != 30 {
		t.Errorf("upsert didn't replace buckets: %+v", first)
	}
}

func TestGetRunHistograms_MetricFilter(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/histograms"

	_, _ = doReq(t, s, token, http.MethodPut, base, map[string]any{
		"histograms": []map[string]any{
			{"name": "a", "step": 10,
				"buckets": map[string]any{"edges": []int{0, 1}, "counts": []int{5}}},
			{"name": "b", "step": 10,
				"buckets": map[string]any{"edges": []int{0, 1}, "counts": []int{9}}},
		},
	})

	status, body := doReq(t, s, token, http.MethodGet, base+"?metric=b", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var rows []histogramOut
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "b" {
		t.Errorf("filter got %+v, want 1 row named 'b'", rows)
	}
}

func TestRunHistograms_RunNotFoundReturns404(t *testing.T) {
	s, token := newA2ATestServer(t)

	url := "/v1/teams/" + defaultTeamID + "/runs/no-such-run/histograms"
	status, _ := doReq(t, s, token, http.MethodGet, url, nil)
	if status != http.StatusNotFound {
		t.Errorf("get 404 = %d", status)
	}

	status, _ = doReq(t, s, token, http.MethodPut, url, map[string]any{
		"histograms": []map[string]any{
			{"name": "x", "step": 0,
				"buckets": map[string]any{"edges": []int{0, 1}, "counts": []int{1}}},
		},
	})
	if status != http.StatusNotFound {
		t.Errorf("put 404 = %d", status)
	}
}

func TestRunHistograms_RunCrossTeamForbidden(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Create a second team and a run owned by it.
	otherTeam := "other-team"
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		otherTeam, "other", NowUTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	runID := seedTestRun(t, s, otherTeam)

	url := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/histograms"
	// Team token is defaultTeam — the runID lives in otherTeam, so a
	// cross-team lookup must 404 (leaks no existence signal).
	status, _ := doReq(t, s, token, http.MethodGet, url, nil)
	if status != http.StatusNotFound {
		t.Errorf("cross-team get = %d, want 404", status)
	}
}

func TestPutRunHistograms_RejectsEmptyBody(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/histograms"

	// Missing histograms[].
	status, _ := doReq(t, s, token, http.MethodPut, base, map[string]any{})
	if status != http.StatusBadRequest {
		t.Errorf("empty body = %d, want 400", status)
	}

	// Missing edges/counts in a bucket.
	status, _ = doReq(t, s, token, http.MethodPut, base, map[string]any{
		"histograms": []map[string]any{
			{"name": "x", "step": 0, "buckets": map[string]any{}},
		},
	})
	if status != http.StatusBadRequest {
		t.Errorf("missing buckets = %d, want 400", status)
	}
}
