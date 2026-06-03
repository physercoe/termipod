package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRunConfig_PutGetReplace(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/config"

	// Empty before any PUT — 200 with null config (the run exists).
	status, body := doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("get empty: status=%d body=%s", status, body)
	}
	var empty struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(body, &empty); err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if string(empty.Config) != "null" {
		t.Errorf("empty config = %s, want null", empty.Config)
	}

	// PUT a config, then GET it back.
	status, body = doReq(t, s, token, http.MethodPut, base, map[string]any{
		"config": map[string]any{"lr": 0.001, "batch": 64, "model": "nanoGPT"},
	})
	if status != http.StatusOK {
		t.Fatalf("put: status=%d body=%s", status, body)
	}
	status, body = doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", status, body)
	}
	var got struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Config["model"] != "nanoGPT" || got.Config["batch"].(float64) != 64 {
		t.Errorf("config = %+v, want model+batch", got.Config)
	}

	// Upsert replaces. Decode into a FRESH struct — unmarshalling into a
	// reused non-nil map would merge, masking a whole-row replace.
	doReq(t, s, token, http.MethodPut, base, map[string]any{
		"config": map[string]any{"lr": 0.01},
	})
	_, body = doReq(t, s, token, http.MethodGet, base, nil)
	var after struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if _, ok := after.Config["model"]; ok {
		t.Errorf("after replace config still has model: %+v", after.Config)
	}
	if after.Config["lr"].(float64) != 0.01 {
		t.Errorf("lr = %v, want 0.01", after.Config["lr"])
	}
}

func TestRunConfig_RejectsNonJSON(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/config"
	status, _ := doReq(t, s, token, http.MethodPut, base, map[string]any{})
	if status != http.StatusBadRequest {
		t.Errorf("missing config: status=%d want 400", status)
	}
}

func TestRunSystemMetrics_PutGet(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/system_metrics"

	status, body := doReq(t, s, token, http.MethodPut, base, map[string]any{
		"metrics": []map[string]any{
			{"name": "gpu.0.util", "points": [][]any{{0, 10}, {1, 80}}, "sample_count": 2, "last_value": 80},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("put: status=%d body=%s", status, body)
	}
	status, body = doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", status, body)
	}
	var rows []metricPointsOut
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "gpu.0.util" {
		t.Fatalf("rows = %+v, want gpu.0.util", rows)
	}
	if rows[0].LastValue == nil || *rows[0].LastValue != 80 {
		t.Errorf("last_value = %v, want 80", rows[0].LastValue)
	}
}

func TestRunAlerts_PutGet(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + "/alerts"

	status, body := doReq(t, s, token, http.MethodPut, base, map[string]any{
		"alerts": []map[string]any{
			{"title": "Loss spike", "text": "loss jumped", "level": "error", "step": 1200, "ts": "t1"},
			{"title": "Note"}, // level defaults to warn, step null
		},
	})
	if status != http.StatusOK {
		t.Fatalf("put: status=%d body=%s", status, body)
	}
	status, body = doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", status, body)
	}
	var rows []alertOut
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2", rows)
	}
	// "Loss spike" has ts → orders before the ts-less "Note".
	if rows[0].Title != "Loss spike" || rows[0].Level != "error" ||
		rows[0].Step == nil || *rows[0].Step != 1200 {
		t.Errorf("row0 = %+v, want Loss spike error step=1200", rows[0])
	}
	if rows[1].Title != "Note" || rows[1].Level != "warn" || rows[1].Step != nil {
		t.Errorf("row1 = %+v, want Note warn no-step", rows[1])
	}

	// Replace clears the prior set.
	doReq(t, s, token, http.MethodPut, base, map[string]any{
		"alerts": []map[string]any{{"title": "Only one"}},
	})
	_, body = doReq(t, s, token, http.MethodGet, base, nil)
	json.Unmarshal(body, &rows)
	if len(rows) != 1 || rows[0].Title != "Only one" {
		t.Errorf("after replace = %+v, want only [Only one]", rows)
	}
}

func TestRunExtras_RejectsCrossTeam(t *testing.T) {
	s, token := newA2ATestServer(t)
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		"other-team-x", "other", NowUTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	runID := seedTestRun(t, s, "other-team-x")
	for _, suffix := range []string{"/config", "/system_metrics", "/alerts"} {
		base := "/v1/teams/" + defaultTeamID + "/runs/" + runID + suffix
		status, _ := doReq(t, s, token, http.MethodGet, base, nil)
		if status != http.StatusNotFound {
			t.Errorf("%s cross-team get: status=%d want 404", suffix, status)
		}
	}
}
