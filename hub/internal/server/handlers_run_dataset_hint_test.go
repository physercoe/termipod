package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// seedRunInProject puts a run in an EXISTING project, which the run<->dataset
// edge needs — `seedTestRun` makes a project of its own, and a run and a
// dataset in two different projects can never link.
func seedRunInProject(t *testing.T, s *Server, projectID string) string {
	t.Helper()
	runID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO runs (id, project_id, status, created_at)
		VALUES (?, ?, 'running', ?)`, runID, projectID, NowUTC()); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return runID
}

func putRunConfig(t *testing.T, s *Server, token, runID string, cfg any) {
	t.Helper()
	status, body := doReq(t, s, token, http.MethodPut,
		"/v1/teams/"+defaultTeamID+"/runs/"+runID+"/config", map[string]any{"config": cfg})
	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusCreated {
		t.Fatalf("put run config: status=%d body=%s", status, body)
	}
}

func getHint(t *testing.T, s *Server, token, runID string) runDatasetHintOut {
	t.Helper()
	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/runs/"+runID+"/dataset_hint", nil)
	if status != http.StatusOK {
		t.Fatalf("hint: status=%d body=%s", status, body)
	}
	var out runDatasetHintOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode hint: %v (body=%s)", err, body)
	}
	return out
}

func TestRunDatasetHintResolvesARegisteredRoot(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/data/lerobot/pusht",
	})
	runID := seedRunInProject(t, s, proj)
	putRunConfig(t, s, token, runID, map[string]any{
		"dataset": map[string]any{"repo_id": "lerobot/pusht", "root": "/data/lerobot/pusht"},
		"policy":  map[string]any{"repo_id": "me/act"},
	})

	got := getHint(t, s, token, runID)
	if got.Hint == nil {
		t.Fatal("no hint from a config that names a registered dataset")
	}
	if got.Hint.Value != "/data/lerobot/pusht" {
		t.Errorf("hint value = %q", got.Hint.Value)
	}
	if got.DatasetID != ds.ID {
		t.Errorf("dataset_id = %q, want %q", got.DatasetID, ds.ID)
	}
	if got.LinkedDatasetID != "" {
		t.Errorf("nothing has been linked yet, got %q", got.LinkedDatasetID)
	}
}

func TestRunDatasetHintProposesAnUnregisteredRoot(t *testing.T) {
	// The hint still comes back with no dataset_id: the client turns that into
	// "register this root", which is the whole point of proposing a location
	// the project has never seen.
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	runID := seedRunInProject(t, s, proj)
	putRunConfig(t, s, token, runID, map[string]any{"dataset.repo_id": "lerobot/unseen"})

	got := getHint(t, s, token, runID)
	if got.Hint == nil || got.Hint.Value != "lerobot/unseen" {
		t.Fatalf("hint = %+v", got.Hint)
	}
	if got.DatasetID != "" {
		t.Errorf("dataset_id = %q, want empty for an unregistered root", got.DatasetID)
	}
}

func TestRunDatasetHintIsSilentWhenTheConfigNamesNoDataset(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	runID := seedRunInProject(t, s, proj)

	// No config at all.
	if got := getHint(t, s, token, runID); got.Hint != nil {
		t.Errorf("hint = %+v, want none for a run with no config", got.Hint)
	}
	// A config, but only a policy in it.
	putRunConfig(t, s, token, runID, map[string]any{"policy": map[string]any{"repo_id": "me/act"}, "steps": 1000})
	if got := getHint(t, s, token, runID); got.Hint != nil {
		t.Errorf("hint = %+v, want none — that repo_id is the model", got.Hint)
	}
}

func TestRunDatasetHintFallsBackToTheFrozenConfig(t *testing.T) {
	// `run_config` is what the process logged; `runs.config_json` is what the
	// caller froze at create time. A run that never logged still has the
	// second, and reading only the first would lose every such run.
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	runID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO runs (id, project_id, status, config_json, created_at)
		VALUES (?, ?, 'running', ?, ?)`,
		runID, proj, `{"dataset":{"repo_id":"lerobot/frozen"}}`, NowUTC()); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	got := getHint(t, s, token, runID)
	if got.Hint == nil || got.Hint.Value != "lerobot/frozen" {
		t.Fatalf("hint = %+v", got.Hint)
	}
}

func TestRunDatasetHint404sOutsideTheTeam(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/runs/nope/dataset_hint", nil)
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
}

func TestRunLinksToADatasetInItsOwnProjectOnly(t *testing.T) {
	s, token := newA2ATestServer(t)
	projA := seedTestProject(t, s, defaultTeamID)
	projB := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, mine := registerDataset(t, s, token, map[string]any{
		"project_id": projA, "host_id": "host-a", "root_path": "/data/mine",
	})
	_, theirs := registerDataset(t, s, token, map[string]any{
		"project_id": projB, "host_id": "host-a", "root_path": "/data/theirs",
	})
	runID := seedRunInProject(t, s, projA)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID

	// The column has no foreign key (0069 — SQLite cannot add one with ALTER
	// TABLE), so this rule lives in the handler or nowhere.
	status, body := doReq(t, s, token, http.MethodPatch, base, map[string]any{"dataset_id": theirs.ID})
	if status != http.StatusBadRequest {
		t.Fatalf("linking another project's dataset: status=%d body=%s", status, body)
	}
	status, body = doReq(t, s, token, http.MethodPatch, base, map[string]any{"dataset_id": "no-such-dataset"})
	if status != http.StatusBadRequest {
		t.Fatalf("linking a dataset that does not exist: status=%d body=%s", status, body)
	}

	status, body = doReq(t, s, token, http.MethodPatch, base, map[string]any{"dataset_id": mine.ID})
	if status != http.StatusOK {
		t.Fatalf("linking own project's dataset: status=%d body=%s", status, body)
	}
	var ro runOut
	if err := json.Unmarshal(body, &ro); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ro.DatasetID != mine.ID {
		t.Errorf("dataset_id = %q, want %q", ro.DatasetID, mine.ID)
	}
	// A linked run reports it on the hint endpoint too, so the client can tell
	// "nothing proposed because nothing matched" from "already linked".
	if got := getHint(t, s, token, runID); got.LinkedDatasetID != mine.ID {
		t.Errorf("linked_dataset_id = %q, want %q", got.LinkedDatasetID, mine.ID)
	}

	// Unlinking is an empty string, not an absent field — the pointer is what
	// distinguishes "leave it" from "clear it".
	status, body = doReq(t, s, token, http.MethodPatch, base, map[string]any{"dataset_id": ""})
	if status != http.StatusOK {
		t.Fatalf("unlink: status=%d body=%s", status, body)
	}
	// A FRESH struct: `dataset_id` is `omitempty`, so an unlinked run omits the
	// key entirely and unmarshalling into the populated `ro` above would leave
	// the old id in place and pass. The first draft of this test did exactly
	// that and reported a bug in the handler that was not there.
	var unlinked runOut
	if err := json.Unmarshal(body, &unlinked); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if unlinked.DatasetID != "" {
		t.Errorf("dataset_id = %q after unlink", unlinked.DatasetID)
	}
}

func TestDeletingADatasetClearsTheRunsPointingAtIt(t *testing.T) {
	// Nothing in the schema cascades, so a dangling id would read downstream as
	// "this run has a dataset" right up until the episodes fail to load.
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/data/doomed",
	})
	runID := seedRunInProject(t, s, proj)
	base := "/v1/teams/" + defaultTeamID + "/runs/" + runID
	if status, body := doReq(t, s, token, http.MethodPatch, base,
		map[string]any{"dataset_id": ds.ID}); status != http.StatusOK {
		t.Fatalf("link: status=%d body=%s", status, body)
	}

	if status, body := doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID, nil); status != http.StatusNoContent {
		t.Fatalf("delete dataset: status=%d body=%s", status, body)
	}

	status, body := doReq(t, s, token, http.MethodGet, base, nil)
	if status != http.StatusOK {
		t.Fatalf("get run: status=%d body=%s", status, body)
	}
	var ro runOut
	if err := json.Unmarshal(body, &ro); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ro.DatasetID != "" {
		t.Errorf("dataset_id = %q, want cleared by the delete", ro.DatasetID)
	}
}
