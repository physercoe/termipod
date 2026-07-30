package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Runs carry `env_ref`, the opaque environment handle reserved by the
// environments plan's E0 wedge (migration 0072). These tests pin the three
// things that wedge actually promises: the field round-trips through every
// run representation, it is unvalidated, and it is never inferred.

func TestRunEnvRef_RoundTripsThroughCreateGetListPatch(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)

	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/runs",
		map[string]any{"project_id": proj, "env_ref": "maniskill:PickCube-v1@0.6"})
	if status != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", status, raw)
	}
	var created runOut
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.EnvRef != "maniskill:PickCube-v1@0.6" {
		t.Errorf("create echoed env_ref = %q", created.EnvRef)
	}

	// GET and LIST must serialise the stored row identically to the create
	// response — a field the creator sees but a reader does not is worse than
	// no field at all.
	status, raw = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/runs/"+created.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d", status)
	}
	var got runOut
	_ = json.Unmarshal(raw, &got)
	if got.EnvRef != "maniskill:PickCube-v1@0.6" {
		t.Errorf("get env_ref = %q", got.EnvRef)
	}

	status, raw = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/runs?project="+proj, nil)
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	var list []runOut
	_ = json.Unmarshal(raw, &list)
	if len(list) != 1 || list[0].EnvRef != "maniskill:PickCube-v1@0.6" {
		t.Errorf("list = %+v", list)
	}

	// PATCH replaces it, and the empty string retracts a wrong one rather than
	// meaning "leave as-is" — absence is what means that.
	status, raw = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/runs/"+created.ID,
		map[string]any{"env_ref": "lab:bench-3@2026-07"})
	if status != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", status, raw)
	}
	_ = json.Unmarshal(raw, &got)
	if got.EnvRef != "lab:bench-3@2026-07" {
		t.Errorf("patched env_ref = %q", got.EnvRef)
	}

	status, raw = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/runs/"+created.ID,
		map[string]any{"status": "running"})
	if status != http.StatusOK {
		t.Fatalf("unrelated patch status = %d body=%s", status, raw)
	}
	_ = json.Unmarshal(raw, &got)
	if got.EnvRef != "lab:bench-3@2026-07" {
		t.Errorf("an unrelated patch cleared env_ref: %q", got.EnvRef)
	}

	status, raw = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/runs/"+created.ID,
		map[string]any{"env_ref": ""})
	if status != http.StatusOK {
		t.Fatalf("clear status = %d body=%s", status, raw)
	}
	// A fresh destination, not `got`: env_ref is `omitempty`, so a cleared one
	// is ABSENT from the response, and unmarshalling over a reused struct
	// would leave the old value standing and pass a test that proved nothing.
	var cleared runOut
	_ = json.Unmarshal(raw, &cleared)
	if cleared.EnvRef != "" {
		t.Errorf("env_ref = %q, want cleared", cleared.EnvRef)
	}
}

// E0's review anchor: env_ref is unvalidated BY DESIGN. Validation arrives
// with E2 resolution, which types an unmatched handle "unresolved" rather than
// refusing the write — so a string that is nothing like "family:env_id@version"
// must still be stored verbatim.
func TestRunEnvRef_IsUnvalidated(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)

	const junk = "not even close to a handle"
	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/runs",
		map[string]any{"project_id": proj, "env_ref": junk})
	if status != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", status, raw)
	}
	var created runOut
	_ = json.Unmarshal(raw, &created)
	if created.EnvRef != junk {
		t.Errorf("env_ref = %q, want it stored verbatim", created.EnvRef)
	}
}

// The stated invariant behind migration 0072: a run's env_ref is never
// inferred from the dataset it is linked to. A dataset's handle says where its
// DATA was collected; an eval run rolls out somewhere that may differ, which
// is exactly the distinction env identity exists to draw. Linking a dataset
// that has an env_ref must leave the run's own empty.
func TestRunEnvRef_IsNotInferredFromTheLinkedDataset(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-env")

	status, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-env",
		"root_path": "/data/lerobot/pick", "env_ref": "lerobot:so100_follower",
	})
	if status != http.StatusCreated {
		t.Fatalf("register dataset status = %d", status)
	}
	if ds.EnvRef != "lerobot:so100_follower" {
		t.Fatalf("dataset env_ref = %q, the fixture needs one to be meaningful", ds.EnvRef)
	}

	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/runs", map[string]any{"project_id": proj})
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, raw)
	}
	var run runOut
	_ = json.Unmarshal(raw, &run)

	status, raw = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/runs/"+run.ID,
		map[string]any{"dataset_id": ds.ID})
	if status != http.StatusOK {
		t.Fatalf("link dataset status = %d body=%s", status, raw)
	}
	var linked runOut
	_ = json.Unmarshal(raw, &linked)
	if linked.DatasetID != ds.ID {
		t.Fatalf("dataset_id = %q, want the link to have been made", linked.DatasetID)
	}
	if linked.EnvRef != "" {
		t.Errorf("env_ref = %q — the run inherited the dataset's, which is the "+
			"guess 0072 exists to refuse", linked.EnvRef)
	}
}
