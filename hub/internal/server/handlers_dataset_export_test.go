package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/termipod/hub/internal/hostjobs"
)

// handlers_dataset_export_test.go — the submit/poll/cancel surface of ADR-058 §3.

func exportFixture(t *testing.T) (*Server, string, datasetOut) {
	t.Helper()
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	status, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/data/lerobot/pusht",
	})
	if status != http.StatusCreated {
		t.Fatalf("register dataset: %d", status)
	}
	return s, token, ds
}

func setHostTools(t *testing.T, s *Server, hostID string, tools map[string]any) {
	t.Helper()
	caps, err := json.Marshal(map[string]any{"agents": map[string]any{}, "tools": tools})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.writeDB.ExecContext(context.Background(),
		`UPDATE hosts SET capabilities_json = ? WHERE id = ?`, string(caps), hostID); err != nil {
		t.Fatalf("set caps: %v", err)
	}
}

func commandRow(t *testing.T, s *Server, id string) (kind, status, args string) {
	t.Helper()
	if err := s.db.QueryRow(
		`SELECT kind, status, args_json FROM host_commands WHERE id = ?`, id).
		Scan(&kind, &status, &args); err != nil {
		t.Fatalf("read command %s: %v", id, err)
	}
	return kind, status, args
}

func TestExportDatasetEpisode_EnqueuesTheJobKind(t *testing.T) {
	s, token, ds := exportFixture(t)

	code, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/export",
		map[string]any{"episode_index": 4})
	if code != http.StatusAccepted {
		t.Fatalf("submit = %d %s, want 202", code, raw)
	}
	var out datasetExportOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if out.CommandID == "" {
		t.Fatal("no command_id returned; the desktop has nothing to poll")
	}
	if out.Kind != hostjobs.KindDatasetExportRRD {
		t.Fatalf("kind = %q", out.Kind)
	}

	kind, status, args := commandRow(t, s, out.CommandID)
	if kind != hostjobs.KindDatasetExportRRD {
		t.Fatalf("row kind = %q", kind)
	}
	if status != "pending" {
		t.Fatalf("row status = %q, want pending until the host pulls it", status)
	}
	var parsed struct {
		RootPath string `json:"root_path"`
		Episode  int64  `json:"episode_index"`
	}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Fatalf("args: %v (%s)", err, args)
	}
	if parsed.RootPath != "/data/lerobot/pusht" {
		t.Fatalf("root_path = %q, want the registered root, not a caller-supplied one", parsed.RootPath)
	}
	if parsed.Episode != 4 {
		t.Fatalf("episode_index = %d, want 4", parsed.Episode)
	}
}

// Episode 0 is an ordinary episode. A missing field and a zero must not be the
// same thing, which is why the body field is a pointer.
func TestExportDatasetEpisode_EpisodeZeroIsValidButMissingIsNot(t *testing.T) {
	s, token, ds := exportFixture(t)
	path := "/v1/teams/" + defaultTeamID + "/datasets/" + ds.ID + "/export"

	if code, raw := doReq(t, s, token, http.MethodPost, path, map[string]any{"episode_index": 0}); code != http.StatusAccepted {
		t.Fatalf("episode 0 = %d %s, want 202", code, raw)
	}
	if code, _ := doReq(t, s, token, http.MethodPost, path, map[string]any{}); code != http.StatusBadRequest {
		t.Fatalf("missing episode_index = %d, want 400", code)
	}
	if code, _ := doReq(t, s, token, http.MethodPost, path, map[string]any{"episode_index": -1}); code != http.StatusBadRequest {
		t.Fatalf("negative episode_index = %d, want 400", code)
	}
}

// The episodes table is browsed and the button is a button. A double-click must
// not cost a second pass over every frame, and two exports would race for the
// same artifact path.
func TestExportDatasetEpisode_SecondSubmitJoinsTheFirst(t *testing.T) {
	s, token, ds := exportFixture(t)
	path := "/v1/teams/" + defaultTeamID + "/datasets/" + ds.ID + "/export"
	body := map[string]any{"episode_index": 2}

	_, raw1 := doReq(t, s, token, http.MethodPost, path, body)
	_, raw2 := doReq(t, s, token, http.MethodPost, path, body)
	var a, b datasetExportOut
	_ = json.Unmarshal(raw1, &a)
	_ = json.Unmarshal(raw2, &b)

	if a.CommandID != b.CommandID {
		t.Fatalf("two command ids for one export: %q and %q", a.CommandID, b.CommandID)
	}
	if !b.Reused {
		t.Fatal("the second submit did not report that it joined the first")
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM host_commands WHERE kind = ?`, hostjobs.KindDatasetExportRRD).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d command rows for one export", n)
	}

	// A *different* episode is a different export and must queue its own row.
	_, raw3 := doReq(t, s, token, http.MethodPost, path, map[string]any{"episode_index": 3})
	var c datasetExportOut
	_ = json.Unmarshal(raw3, &c)
	if c.CommandID == a.CommandID {
		t.Fatal("a different episode reused the first export's command")
	}
}

// Once the first export is over, a repeat is a fresh export — the artifact may
// have been evicted from the host's jobcache since.
func TestExportDatasetEpisode_AFinishedExportDoesNotBlockAReRun(t *testing.T) {
	s, token, ds := exportFixture(t)
	path := "/v1/teams/" + defaultTeamID + "/datasets/" + ds.ID + "/export"
	body := map[string]any{"episode_index": 1}

	_, raw1 := doReq(t, s, token, http.MethodPost, path, body)
	var first datasetExportOut
	_ = json.Unmarshal(raw1, &first)
	if _, err := s.writeDB.Exec(
		`UPDATE host_commands SET status='done' WHERE id = ?`, first.CommandID); err != nil {
		t.Fatal(err)
	}

	_, raw2 := doReq(t, s, token, http.MethodPost, path, body)
	var second datasetExportOut
	_ = json.Unmarshal(raw2, &second)
	if second.CommandID == first.CommandID || second.Reused {
		t.Fatalf("a finished export was reused: %+v", second)
	}
}

func TestExportDatasetEpisode_RefusesWithoutAHostOrForARemoteRoot(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)

	// No host: nobody to run it.
	_, hostless := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "root_path": "/data/lerobot/a",
	})
	if code, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+hostless.ID+"/export",
		map[string]any{"episode_index": 0}); code != http.StatusConflict {
		t.Fatalf("hostless export = %d, want 409", code)
	}

	// A non-local root: honest refusal, the same posture the read verbs take.
	seedDatasetHost(t, s, "host-b")
	_, remote := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-b", "root_path": "/data/lerobot/b", "source": "sftp",
	})
	if code, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+remote.ID+"/export",
		map[string]any{"episode_index": 0}); code != http.StatusNotImplemented {
		t.Fatalf("sftp-source export = %d, want 501", code)
	}
}

// The point of publishing tools[] is to refuse at submit, so a caller is told
// now rather than polling a job to its failure minutes later (#394).
func TestExportDatasetEpisode_RefusedWhenTheHostSaysItCannot(t *testing.T) {
	s, token, ds := exportFixture(t)
	setHostTools(t, s, "host-a", map[string]any{
		hostjobs.ToolLeRobotExport: map[string]any{
			"installed": false,
			"detail":    "the exporter is present but rerun-sdk could not be resolved in /usr/bin/python3",
		},
	})

	code, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/export",
		map[string]any{"episode_index": 0})
	if code != http.StatusConflict {
		t.Fatalf("submit = %d, want 409", code)
	}
	// The host's own detail has to survive to the caller, or the message is
	// "it didn't work" and the operator has nothing to fix.
	if !jsonContains(raw, "rerun-sdk") {
		t.Fatalf("body = %s, want the host's detail naming the missing half", raw)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM host_commands`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d commands enqueued despite the refusal", n)
	}
}

// A host that has not reported yet must not be blocked — the job itself is the
// authority, and a fresh host has an empty capabilities blob.
func TestExportDatasetEpisode_UnreportedOrInstalledToolIsAllowed(t *testing.T) {
	s, token, ds := exportFixture(t)
	path := "/v1/teams/" + defaultTeamID + "/datasets/" + ds.ID + "/export"

	// Nothing reported at all.
	if code, raw := doReq(t, s, token, http.MethodPost, path, map[string]any{"episode_index": 0}); code != http.StatusAccepted {
		t.Fatalf("unreported caps = %d %s, want 202", code, raw)
	}
	// Reported installed.
	setHostTools(t, s, "host-a", map[string]any{
		hostjobs.ToolLeRobotExport: map[string]any{
			"installed": true,
			"versions":  map[string]any{"lerobot": "0.6.1", "rerun-sdk": "0.25.0"},
		},
	})
	if code, raw := doReq(t, s, token, http.MethodPost, path, map[string]any{"episode_index": 7}); code != http.StatusAccepted {
		t.Fatalf("installed = %d %s, want 202", code, raw)
	}
	// Caps present but this tool absent from them (an older host-runner).
	setHostTools(t, s, "host-a", map[string]any{"something-else": map[string]any{"installed": false}})
	if code, raw := doReq(t, s, token, http.MethodPost, path, map[string]any{"episode_index": 8}); code != http.StatusAccepted {
		t.Fatalf("tool absent from caps = %d %s, want 202 (the job is the authority)", code, raw)
	}
}

// ---------------------------------------------------------------------------
// status read
// ---------------------------------------------------------------------------

func TestGetHostCommand_ReturnsProgressAndResult(t *testing.T) {
	s, token, ds := exportFixture(t)
	_, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/export",
		map[string]any{"episode_index": 0})
	var sub datasetExportOut
	_ = json.Unmarshal(raw, &sub)

	if _, err := s.writeDB.Exec(`
		UPDATE host_commands
		   SET status='delivered', progress_json='{"phase":"decoding","done":3,"total":10}',
		       progress_at=?
		 WHERE id = ?`, NowUTC(), sub.CommandID); err != nil {
		t.Fatal(err)
	}

	code, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/commands/"+sub.CommandID, nil)
	if code != http.StatusOK {
		t.Fatalf("get = %d %s", code, body)
	}
	var out commandOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if out.Status != "delivered" {
		t.Fatalf("status = %q", out.Status)
	}
	if !jsonContains(out.Progress, "decoding") {
		t.Fatalf("progress = %s, want the host's phase", out.Progress)
	}
	if out.ProgressAt == nil {
		t.Fatal("progress_at missing; a poller cannot tell a stalled job from a slow one")
	}
}

func TestGetHostCommand_UnknownIs404(t *testing.T) {
	s, token, _ := exportFixture(t)
	if code, _ := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/commands/nope", nil); code != http.StatusNotFound {
		t.Fatalf("unknown command = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------------
// cancel
// ---------------------------------------------------------------------------

// The hub cannot stop a subprocess. Flipping the row here while the export kept
// running would leave the host burning CPU on work nobody waits for, so cancel
// is a command to the host.
func TestCancelHostCommand_EnqueuesTheInlineCancelKind(t *testing.T) {
	s, token, ds := exportFixture(t)
	_, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/export",
		map[string]any{"episode_index": 0})
	var sub datasetExportOut
	_ = json.Unmarshal(raw, &sub)

	code, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/commands/"+sub.CommandID+"/cancel", nil)
	if code != http.StatusAccepted {
		t.Fatalf("cancel = %d %s, want 202", code, body)
	}

	var kind, args string
	if err := s.db.QueryRow(
		`SELECT kind, args_json FROM host_commands WHERE kind = ?`, hostjobs.KindCancel).
		Scan(&kind, &args); err != nil {
		t.Fatalf("no job_cancel enqueued: %v", err)
	}
	if !jsonContains([]byte(args), sub.CommandID) {
		t.Fatalf("cancel args = %s, want the target command id", args)
	}
	// The target row is deliberately untouched: the host reports what actually
	// happened to the work.
	_, status, _ := commandRow(t, s, sub.CommandID)
	if status != "pending" {
		t.Fatalf("target status = %q; the hub decided the outcome instead of the host", status)
	}
}

func TestCancelHostCommand_FinishedIsANoOpAndNonJobsAreRefused(t *testing.T) {
	s, token, ds := exportFixture(t)
	_, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/export",
		map[string]any{"episode_index": 0})
	var sub datasetExportOut
	_ = json.Unmarshal(raw, &sub)
	if _, err := s.writeDB.Exec(`UPDATE host_commands SET status='done' WHERE id = ?`, sub.CommandID); err != nil {
		t.Fatal(err)
	}
	if code, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/commands/"+sub.CommandID+"/cancel", nil); code != http.StatusNoContent {
		t.Fatalf("cancelling a finished job = %d, want 204", code)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM host_commands WHERE kind = ?`, hostjobs.KindCancel).Scan(&n)
	if n != 0 {
		t.Fatalf("%d cancels enqueued for a finished job", n)
	}

	// An inline kind has no cancel path — the executor only tracks job kinds.
	id, err := s.enqueueHostCommand(context.Background(), "host-a", "", "capture", map[string]any{"pane_id": "%1"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/commands/"+id+"/cancel", nil); code != http.StatusBadRequest {
		t.Fatalf("cancelling a non-job kind = %d, want 400", code)
	}
}

// jsonContains is a substring check on raw JSON — enough for asserting that a
// value survived serialisation without re-deriving the whole shape.
func jsonContains(raw []byte, needle string) bool {
	return bytes.Contains(raw, []byte(needle))
}
