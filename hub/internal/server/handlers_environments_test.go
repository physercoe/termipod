package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// registerEnvironment POSTs an environment and returns the decoded row.
func registerEnvironment(t *testing.T, s *Server, token string, body map[string]any) (int, environmentOut) {
	t.Helper()
	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/environments", body)
	var out environmentOut
	if status == http.StatusCreated || status == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode environment: %v (body=%s)", err, raw)
		}
	}
	return status, out
}

func TestEnvironmentCRUD(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)

	status, env := registerEnvironment(t, s, token, map[string]any{
		"project_id": proj, "family": "maniskill", "env_id": "PickCube-v1",
		"version": "0.6", "success_desc": "cube is inside the goal region",
		"task_ref": "envs/pick_cube.py", "task_format": "python",
		"config": map[string]any{"randomize": map[string]any{"lighting": true}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create status = %d", status)
	}
	// The handle is assembled server-side so every consumer builds it one way.
	if env.EnvRef != "maniskill:PickCube-v1@0.6" {
		t.Errorf("env_ref = %q", env.EnvRef)
	}
	if env.TeamID != defaultTeamID {
		t.Errorf("team_id = %q, want the row team-scoped regardless of project", env.TeamID)
	}
	if string(env.Config) == "" {
		t.Errorf("config blob was not round-tripped")
	}

	// GET
	status, raw := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/environments/"+env.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d", status)
	}
	var got environmentOut
	_ = json.Unmarshal(raw, &got)
	if got.ID != env.ID || got.SuccessDesc != "cube is inside the goal region" {
		t.Errorf("get returned %+v", got)
	}

	// LIST, including the project filter.
	status, raw = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/environments?project="+proj, nil)
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	var list []environmentOut
	_ = json.Unmarshal(raw, &list)
	if len(list) != 1 || list[0].ID != env.ID {
		t.Errorf("list = %+v", list)
	}

	// PATCH a non-identity field.
	status, raw = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+env.ID,
		map[string]any{"embodiment_ref": "franka_panda"})
	if status != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", status, raw)
	}
	_ = json.Unmarshal(raw, &got)
	if got.EmbodimentRef != "franka_panda" {
		t.Errorf("embodiment_ref = %q", got.EmbodimentRef)
	}

	// DELETE
	status, _ = doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/environments/"+env.ID, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d", status)
	}
	status, _ = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/environments/"+env.ID, nil)
	if status != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", status)
	}
}

// Registration is idempotent on the handle: the same handle IS the same
// environment, so a second register selects rather than duplicating. E3's
// recalibration story depends on this — a site is re-registered from its
// calibration bundle, and only changed content bumps the version.
func TestEnvironmentRegisterIsIdempotentOnTheHandle(t *testing.T) {
	s, token := newA2ATestServer(t)
	body := map[string]any{"family": "mujoco", "env_id": "reach", "version": "v1"}

	status, first := registerEnvironment(t, s, token, body)
	if status != http.StatusCreated {
		t.Fatalf("first register status = %d", status)
	}
	status, second := registerEnvironment(t, s, token, body)
	if status != http.StatusOK {
		t.Fatalf("second register status = %d, want 200 with the existing row", status)
	}
	if second.ID != first.ID {
		t.Errorf("second register minted a new row (%q vs %q)", second.ID, first.ID)
	}
}

// Register never edits, which is the other half of idempotency: PATCH is the
// only writer of the non-identity fields, so a script that re-registers with a
// sparse body cannot flatten a curated success_desc. One writer per field.
func TestEnvironmentRegisterDoesNotEditTheExistingRow(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, env := registerEnvironment(t, s, token, map[string]any{
		"family": "maniskill", "env_id": "PickCube-v1",
		"success_desc": "cube is inside the goal region",
		"task_ref":     "envs/pick_cube.py",
	})
	if status != http.StatusCreated {
		t.Fatalf("register status = %d", status)
	}

	status, again := registerEnvironment(t, s, token, map[string]any{
		"family": "maniskill", "env_id": "PickCube-v1",
		"success_desc": "", "task_ref": "somewhere/else.py",
	})
	if status != http.StatusOK {
		t.Fatalf("re-register status = %d, want 200", status)
	}
	if again.ID != env.ID {
		t.Fatalf("re-register minted a new row")
	}
	if again.SuccessDesc != "cube is inside the goal region" || again.TaskRef != "envs/pick_cube.py" {
		t.Errorf("re-register edited the row: %+v", again)
	}
}

// The env-drift guard §0 asks for: benchmarks version their env ids precisely
// because drift silently invalidates comparison. One handle claiming two
// different content hashes is that drift, so it fails loudly rather than
// letting the last writer redefine what a recorded run was measured in.
func TestEnvironmentRegisterRefusesADifferentContentHashForOneHandle(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := registerEnvironment(t, s, token, map[string]any{
		"family": "isaac-lab", "env_id": "Lift-Cube", "version": "v2",
		"content_hash": "sha256:aaa",
	})
	if status != http.StatusCreated {
		t.Fatalf("register status = %d", status)
	}
	status, _ = registerEnvironment(t, s, token, map[string]any{
		"family": "isaac-lab", "env_id": "Lift-Cube", "version": "v2",
		"content_hash": "sha256:bbb",
	})
	if status != http.StatusConflict {
		t.Errorf("re-register with a different content_hash = %d, want 409", status)
	}
	// Filling in a hash nobody knew at registration is a different act, and is
	// allowed — the guard is about redefinition, not about learning.
	status, unhashed := registerEnvironment(t, s, token, map[string]any{
		"family": "isaac-lab", "env_id": "Lift-Cube", "version": "v3",
	})
	if status != http.StatusCreated {
		t.Fatalf("register v3 status = %d", status)
	}
	status, raw := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+unhashed.ID,
		map[string]any{"content_hash": "sha256:ccc"})
	if status != http.StatusOK {
		t.Fatalf("filling in a content_hash = %d body=%s", status, raw)
	}
	status, _ = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+unhashed.ID,
		map[string]any{"content_hash": "sha256:ddd"})
	if status != http.StatusConflict {
		t.Errorf("changing a recorded content_hash = %d, want 409", status)
	}
	// Clearing is a change too: were "" accepted, clear-then-set would
	// redefine the handle in two PATCHes and the drift 409 would guard
	// nothing.
	status, _ = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+unhashed.ID,
		map[string]any{"content_hash": ""})
	if status != http.StatusConflict {
		t.Errorf("clearing a recorded content_hash = %d, want 409", status)
	}
	// Re-sending the recorded value is a no-op, not a conflict.
	status, _ = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+unhashed.ID,
		map[string]any{"content_hash": "sha256:ccc"})
	if status != http.StatusOK {
		t.Errorf("re-sending the recorded content_hash = %d, want 200", status)
	}
}

// The handle is the identity, and rows elsewhere already point at it as an
// opaque string. Patching it would silently unresolve every one of them, so it
// is refused out loud rather than dropped as an unknown field.
func TestEnvironmentIdentityFieldsAreNotPatchable(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, env := registerEnvironment(t, s, token,
		map[string]any{"family": "mujoco", "env_id": "reach"})

	for _, field := range []string{"family", "env_id", "version"} {
		status, _ := doReq(t, s, token, http.MethodPatch,
			"/v1/teams/"+defaultTeamID+"/environments/"+env.ID,
			map[string]any{field: "changed"})
		if status != http.StatusBadRequest {
			t.Errorf("patch %s = %d, want 400", field, status)
		}
	}
	status, raw := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/environments/"+env.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d", status)
	}
	var got environmentOut
	_ = json.Unmarshal(raw, &got)
	if got.EnvRef != "mujoco:reach" {
		t.Errorf("env_ref = %q, want it untouched by the refused patches", got.EnvRef)
	}
}

// The plan's scope-honesty anchor, executable: a real site is team-scoped from
// the first migration. Two projects sharing a bench must share its calibration
// history and its twin edge, and a project-scoped interim is the migration this
// decision exists to avoid.
func TestEnvironmentRealSiteCannotBeProjectScoped(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)

	status, _ := registerEnvironment(t, s, token, map[string]any{
		"family": "real-site", "env_id": "bench-3", "project_id": proj,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("project-scoped real site = %d, want 400", status)
	}
	// Team-scoped is the way to register one.
	status, site := registerEnvironment(t, s, token, map[string]any{
		"family": "real-site", "env_id": "bench-3",
	})
	if status != http.StatusCreated {
		t.Fatalf("team-scoped real site = %d", status)
	}
	if site.ProjectID != "" {
		t.Errorf("project_id = %q, want empty", site.ProjectID)
	}
	// And it cannot be narrowed later either — the rule lives at every writer,
	// not only at registration.
	status, _ = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+site.ID,
		map[string]any{"project_id": proj})
	if status != http.StatusBadRequest {
		t.Errorf("patching a site into a project = %d, want 400", status)
	}
}

// A project's rail asks "what can this project reference?", which is its own
// rows PLUS the team-scoped ones — filtering to project_id = P alone would hide
// exactly the benches the project runs on.
func TestEnvironmentListByProjectIncludesTeamScopedRows(t *testing.T) {
	s, token := newA2ATestServer(t)
	projA := seedTestProject(t, s, defaultTeamID)
	projB := seedTestProject(t, s, defaultTeamID)

	_, simA := registerEnvironment(t, s, token,
		map[string]any{"family": "maniskill", "env_id": "PickCube-v1", "project_id": projA})
	_, simB := registerEnvironment(t, s, token,
		map[string]any{"family": "maniskill", "env_id": "StackCube-v1", "project_id": projB})
	_, site := registerEnvironment(t, s, token,
		map[string]any{"family": "real-site", "env_id": "bench-3"})

	status, raw := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/environments?project="+projA, nil)
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	var list []environmentOut
	_ = json.Unmarshal(raw, &list)
	ids := map[string]bool{}
	for _, e := range list {
		ids[e.ID] = true
	}
	if !ids[simA.ID] || !ids[site.ID] {
		t.Errorf("project A's list is missing its own sim or the shared site: %+v", list)
	}
	if ids[simB.ID] {
		t.Errorf("project A's list leaked project B's environment")
	}
	// Sim families sort before sites, which is the rail's order (plan §2).
	if len(list) >= 2 && list[len(list)-1].Family != "real-site" {
		t.Errorf("sites must sort last, got %+v", list)
	}
}

// Resolution is E2's half of E0's promise, and its shape matters as much as its
// answers: one entry per input ref, in input order, so a caller zipping results
// onto its rows cannot misalign them.
func TestEnvironmentResolve(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, env := registerEnvironment(t, s, token, map[string]any{
		"family": "maniskill", "env_id": "PickCube-v1", "version": "0.6",
	})

	status, raw := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/environments/resolve"+
			"?env_ref=maniskill:PickCube-v1@0.6"+
			"&env_ref=maniskill:PickCube-v1"+
			"&env_ref=not-a-handle"+
			"&env_ref=maniskill:PickCube-v1@0.6", nil)
	if status != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", status, raw)
	}
	var body struct {
		Results []envResolution `json:"results"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if len(body.Results) != 4 {
		t.Fatalf("want one result per input ref, got %d", len(body.Results))
	}
	if body.Results[0].Status != "resolved" || body.Results[0].Environment == nil ||
		body.Results[0].Environment.ID != env.ID {
		t.Errorf("exact handle did not resolve: %+v", body.Results[0])
	}
	// A versionless ref does NOT match a versioned row: which version it meant
	// is exactly the question, and picking one would put a guess where the plan
	// promised an honest "unresolved".
	if body.Results[1].Status != "unresolved" || body.Results[1].Reason != "no_match" {
		t.Errorf("versionless ref = %+v, want unresolved/no_match", body.Results[1])
	}
	// "not a handle" and "no row for that handle" send a reader to different
	// fixes, so the reason distinguishes them.
	if body.Results[2].Status != "unresolved" || body.Results[2].Reason != "malformed" {
		t.Errorf("garbage ref = %+v, want unresolved/malformed", body.Results[2])
	}
	// A repeated ref is answered again (order preserved) from one lookup.
	if body.Results[3].Status != "resolved" {
		t.Errorf("repeated ref = %+v", body.Results[3])
	}
}

// Deleting a registry row must leave the handles alone. E0 accumulated env_ref
// strings before any row existed, so "unresolved" is a state every consumer
// already renders — turning a delete into a dangling reference, or into a
// cascade that edited someone's run history, would both be worse.
func TestEnvironmentDeleteLeavesTheHandlesUnresolvedNotDangling(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	_, env := registerEnvironment(t, s, token, map[string]any{
		"family": "mujoco", "env_id": "reach", "version": "v1",
	})

	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/runs",
		map[string]any{"project_id": proj, "env_ref": "mujoco:reach@v1"})
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, raw)
	}
	var run runOut
	_ = json.Unmarshal(raw, &run)

	status, _ = doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/environments/"+env.ID, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d", status)
	}

	status, raw = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/runs/"+run.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get run status = %d", status)
	}
	var after runOut
	_ = json.Unmarshal(raw, &after)
	if after.EnvRef != "mujoco:reach@v1" {
		t.Errorf("run env_ref = %q, want the handle untouched by the delete", after.EnvRef)
	}
	status, raw = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/environments/resolve?env_ref=mujoco:reach@v1", nil)
	if status != http.StatusOK {
		t.Fatalf("resolve status = %d", status)
	}
	var body struct {
		Results []envResolution `json:"results"`
	}
	_ = json.Unmarshal(raw, &body)
	if len(body.Results) != 1 || body.Results[0].Status != "unresolved" ||
		body.Results[0].Reason != "no_match" {
		t.Errorf("after delete the handle resolves as %+v, want unresolved/no_match", body.Results)
	}
}

// A twin must be a real environment in the same team, and never the row itself:
// "this scene is its own sim counterpart" is not a claim the model can express.
func TestEnvironmentTwinValidation(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, site := registerEnvironment(t, s, token,
		map[string]any{"family": "real-site", "env_id": "bench-3"})
	_, sim := registerEnvironment(t, s, token,
		map[string]any{"family": "isaac-lab", "env_id": "bench-3-twin"})

	status, _ := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+site.ID,
		map[string]any{"twin_of": site.ID})
	if status != http.StatusBadRequest {
		t.Errorf("self-twin = %d, want 400", status)
	}
	status, _ = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+site.ID,
		map[string]any{"twin_of": "env-does-not-exist"})
	if status != http.StatusBadRequest {
		t.Errorf("unknown twin = %d, want 400", status)
	}
	status, raw := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/environments/"+site.ID,
		map[string]any{"twin_of": sim.ID})
	if status != http.StatusOK {
		t.Fatalf("valid twin = %d body=%s", status, raw)
	}
	var got environmentOut
	_ = json.Unmarshal(raw, &got)
	if got.TwinOf != sim.ID {
		t.Errorf("twin_of = %q, want %q", got.TwinOf, sim.ID)
	}
}

// A handle nobody can name is a handle nobody can resolve, so the write path
// refuses parts that would not survive the round trip.
func TestEnvironmentRefusesUnnameableParts(t *testing.T) {
	s, token := newA2ATestServer(t)
	for _, body := range []map[string]any{
		{"env_id": "PickCube-v1"},                                  // no family
		{"family": "maniskill"},                                    // no env_id
		{"family": "mani:skill", "env_id": "PickCube-v1"},          // family owns the ':'
		{"family": "maniskill", "env_id": "Pick@Cube"},             // env_id owns no '@'
		{"family": "maniskill", "env_id": "ok", "version": "1@2"},  // nor does a version
		{"family": " maniskill ", "env_id": "ok", "version": "@@"}, // trimmed, then still bad
	} {
		if status, _ := registerEnvironment(t, s, token, body); status != http.StatusBadRequest {
			t.Errorf("register %v = %d, want 400", body, status)
		}
	}
}
