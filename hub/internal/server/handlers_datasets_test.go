package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeHostVerb stands in for a host-runner: it long-polls the tunnel, answers
// the first envelope matching kind, and hands the request payload back to the
// test so assertions can be made about what the hub actually asked for.
//
// The reply is driven by a callback rather than a fixed body because most of
// these tests care about the request as much as the response — whether offset
// and limit were forwarded, whether the root path was the registered one.
type fakeHostVerb struct {
	payloads chan []byte
	stop     context.CancelFunc
	wg       *sync.WaitGroup
	url      string
}

func serveHostVerb(t *testing.T, s *Server, token, hostID, kind string,
	reply func(payload []byte) (int, any)) *fakeHostVerb {
	t.Helper()
	ts := httptest.NewServer(s.router)
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeHostVerb{payloads: make(chan []byte, 8), stop: cancel, wg: &sync.WaitGroup{}, url: ts.URL}
	base := ts.URL + "/v1/teams/" + defaultTeamID + "/hosts/" + hostID + "/a2a/tunnel"

	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer ts.Close()
		for ctx.Err() == nil {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/next?wait_ms=1000", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				continue
			}
			var env tunnelRequest
			_ = json.NewDecoder(resp.Body).Decode(&env)
			resp.Body.Close()
			if env.Kind != kind {
				continue
			}
			select {
			case f.payloads <- env.Payload:
			default:
			}
			status, body := reply(env.Payload)
			raw, _ := json.Marshal(body)
			b, _ := json.Marshal(tunnelResponse{
				ReqID:   env.ReqID,
				Status:  status,
				Headers: map[string]string{"Content-Type": "application/json"},
				BodyB64: base64.StdEncoding.EncodeToString(raw),
			})
			pr, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/responses", bytes.NewReader(b))
			pr.Header.Set("Authorization", "Bearer "+token)
			pr.Header.Set("Content-Type", "application/json")
			if pResp, err := http.DefaultClient.Do(pr); err == nil {
				pResp.Body.Close()
			}
		}
	}()
	// Give the poller a moment to reach the tunnel before the hub enqueues.
	time.Sleep(50 * time.Millisecond)
	t.Cleanup(func() { cancel(); f.wg.Wait() })
	return f
}

func (f *fakeHostVerb) lastPayload(t *testing.T) map[string]any {
	t.Helper()
	select {
	case raw := <-f.payloads:
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode verb payload: %v", err)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("host verb was never dispatched")
		return nil
	}
}

func seedDatasetHost(t *testing.T, s *Server, id string) {
	t.Helper()
	seedTestHost(t, s, defaultTeamID, id, id)
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE hosts SET last_seen_at = datetime('now') WHERE id = ?`, id); err != nil {
		t.Fatalf("set last_seen: %v", err)
	}
}

// registerDataset POSTs a dataset and returns the decoded row.
func registerDataset(t *testing.T, s *Server, token string, body map[string]any) (int, datasetOut) {
	t.Helper()
	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets", body)
	var out datasetOut
	if status == http.StatusCreated || status == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode dataset: %v (body=%s)", err, raw)
		}
	}
	return status, out
}

func TestDatasetCRUD(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")

	status, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/data/lerobot/pick",
	})
	if status != http.StatusCreated {
		t.Fatalf("create status = %d", status)
	}
	// An unnamed registration takes the root's last segment, which for a
	// LeRobot root is the dataset's own directory.
	if ds.Name != "pick" {
		t.Errorf("name = %q, want %q derived from the path", ds.Name, "pick")
	}
	if ds.Source != "local" {
		t.Errorf("source = %q, want the local default", ds.Source)
	}
	if ds.Format != "" || ds.DigestTS != "" {
		t.Errorf("a freshly registered dataset must have no digest yet: %+v", ds)
	}

	// GET
	status, raw := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d", status)
	}
	var got datasetOut
	_ = json.Unmarshal(raw, &got)
	if got.ID != ds.ID || got.RootPath != "/data/lerobot/pick" {
		t.Errorf("get returned %+v", got)
	}

	// LIST, including the project filter.
	status, raw = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/datasets?project="+proj, nil)
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	var list []datasetOut
	_ = json.Unmarshal(raw, &list)
	if len(list) != 1 || list[0].ID != ds.ID {
		t.Errorf("list = %+v", list)
	}

	// PATCH the human-owned fields.
	status, raw = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID,
		map[string]any{"name": "Pick and place", "env_ref": "lerobot:so101@v2"})
	if status != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", status, raw)
	}
	_ = json.Unmarshal(raw, &got)
	if got.Name != "Pick and place" || got.EnvRef != "lerobot:so101@v2" {
		t.Errorf("patched = %+v", got)
	}

	// A patch with nothing patchable is a client error, not a silent no-op.
	status, _ = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID, map[string]any{})
	if status != http.StatusBadRequest {
		t.Errorf("empty patch status = %d, want 400", status)
	}

	// DELETE, then it is gone.
	status, _ = doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d", status)
	}
	status, _ = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID, nil)
	if status != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", status)
	}
}

// "Open in Replay" is a context-menu action on a tree row. Hitting it twice
// must select the same dataset, not mint a second row that then drifts.
func TestDatasetRegistrationIsIdempotent(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	body := map[string]any{"project_id": proj, "host_id": "host-a", "root_path": "/data/x"}

	status, first := registerDataset(t, s, token, body)
	if status != http.StatusCreated {
		t.Fatalf("first status = %d", status)
	}
	status, second := registerDataset(t, s, token, body)
	if status != http.StatusOK {
		t.Errorf("second status = %d, want 200 (existing row)", status)
	}
	if second.ID != first.ID {
		t.Errorf("second registration minted a new row: %s vs %s", second.ID, first.ID)
	}

	// A different root under the same project/host IS a different dataset.
	status, other := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/data/y",
	})
	if status != http.StatusCreated || other.ID == first.ID {
		t.Errorf("a distinct root must be a distinct dataset: status=%d id=%s", status, other.ID)
	}
}

func TestDatasetCreateValidation(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")

	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"no project", map[string]any{"root_path": "/d"}, http.StatusBadRequest},
		{"no root path", map[string]any{"project_id": proj}, http.StatusBadRequest},
		{"blank root path", map[string]any{"project_id": proj, "root_path": "   "}, http.StatusBadRequest},
		{"unknown source", map[string]any{"project_id": proj, "root_path": "/d", "source": "s3"}, http.StatusBadRequest},
		{"unknown project", map[string]any{"project_id": "nope", "root_path": "/d"}, http.StatusNotFound},
		{"unknown host", map[string]any{"project_id": proj, "root_path": "/d", "host_id": "nope"}, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := doReq(t, s, token, http.MethodPost,
				"/v1/teams/"+defaultTeamID+"/datasets", tc.body)
			if status != tc.want {
				t.Errorf("status = %d, want %d", status, tc.want)
			}
		})
	}
}

// A refresh is the only way a digest gets onto the row: the hub never reads a
// dataset itself.
func TestDatasetRefreshStoresTheHostFold(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/data/lerobot/nyu",
	})

	fake := serveHostVerb(t, s, token, "host-a", "host.dataset_digest",
		func([]byte) (int, any) {
			return http.StatusOK, map[string]any{
				"digest": map[string]any{
					"schema_version": 1,
					"format":         "lerobot_v3.0",
					"total_episodes": 14,
					"total_frames":   440,
					"fps":            5,
					"env_ref":        "lerobot:so100_follower",
				},
				"fingerprint": map[string]any{"files": 4, "bytes": 55883},
			}
		})

	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/refresh", nil)
	if status != http.StatusOK {
		t.Fatalf("refresh status = %d body=%s", status, raw)
	}
	// The host must have been asked about the registered root, not something
	// the hub invented.
	if got := fake.lastPayload(t)["root_path"]; got != "/data/lerobot/nyu" {
		t.Errorf("verb root_path = %v", got)
	}

	var out datasetOut
	_ = json.Unmarshal(raw, &out)
	if out.Format != "lerobot_v3.0" {
		t.Errorf("format = %q, want it lifted out of the digest", out.Format)
	}
	if out.DigestSchemaVersion != 1 {
		t.Errorf("digest schema version = %d", out.DigestSchemaVersion)
	}
	if out.DigestTS == "" {
		t.Error("digest_ts must be stamped so staleness can be judged")
	}
	if out.EnvRef != "lerobot:so100_follower" {
		t.Errorf("env_ref = %q, want it derived from the digest", out.EnvRef)
	}
	var digest map[string]any
	if err := json.Unmarshal(out.Digest, &digest); err != nil {
		t.Fatalf("digest is not valid JSON: %v", err)
	}
	if digest["total_episodes"].(float64) != 14 {
		t.Errorf("digest = %v", digest)
	}
	var fp map[string]any
	if err := json.Unmarshal(out.Fingerprint, &fp); err != nil {
		t.Fatalf("fingerprint is not valid JSON: %v", err)
	}
	if fp["files"].(float64) != 4 {
		t.Errorf("fingerprint = %v", fp)
	}
}

// A human-set env_ref is more specific than anything a robot_type string can
// yield, so a refresh must not quietly undo it.
func TestDatasetRefreshDoesNotOverwriteAHumanEnvRef(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/d",
		"env_ref": "lab:bench-3@2026-07",
	})
	serveHostVerb(t, s, token, "host-a", "host.dataset_digest", func([]byte) (int, any) {
		return http.StatusOK, map[string]any{
			"digest": map[string]any{"schema_version": 1, "format": "lerobot_v2.1",
				"env_ref": "lerobot:so100_follower"},
		}
	})
	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/refresh", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	var out datasetOut
	_ = json.Unmarshal(raw, &out)
	if out.EnvRef != "lab:bench-3@2026-07" {
		t.Errorf("env_ref = %q, want the human value preserved", out.EnvRef)
	}
	if out.Format != "lerobot_v2.1" {
		t.Errorf("format = %q, want the refresh still to apply", out.Format)
	}
}

// An unsupported generation is an answer about the dataset, not a hub failure.
// The version string is the only actionable part, so it has to survive the
// proxy rather than being flattened into "refresh failed".
func TestDatasetRefreshPassesThroughUnsupportedFormat(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/d",
	})
	serveHostVerb(t, s, token, "host-a", "host.dataset_digest", func([]byte) (int, any) {
		return http.StatusUnprocessableEntity, map[string]any{
			"error":            "unsupported_format",
			"codebase_version": "v4.0",
			"detail":           "datasetmeta: unsupported dataset format v4.0",
		}
	})
	status, raw := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/refresh", nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", status, raw)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if body["codebase_version"] != "v4.0" {
		t.Errorf("the refused version must reach the client: %v", body)
	}

	// And the row must be left untouched — a failed read is not a digest.
	status, raw = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID, nil)
	var out datasetOut
	_ = json.Unmarshal(raw, &out)
	if status != http.StatusOK || out.Format != "" || out.DigestTS != "" {
		t.Errorf("row was mutated by a failed refresh: %+v", out)
	}
}

func TestDatasetEpisodesProxy(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/data/nyu",
	})
	fake := serveHostVerb(t, s, token, "host-a", "host.dataset_episodes",
		func([]byte) (int, any) {
			return http.StatusOK, map[string]any{
				"episodes": []map[string]any{{"index": 5, "length": 40}},
				"offset":   5, "limit": 2, "total": 14,
			}
		})

	status, raw := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/datasets/"+ds.ID+"/episodes?offset=5&limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	p := fake.lastPayload(t)
	if p["root_path"] != "/data/nyu" {
		t.Errorf("root_path = %v", p["root_path"])
	}
	// The window must be forwarded, not silently dropped — dropping it would
	// return page one for every scroll position.
	if p["offset"].(float64) != 5 || p["limit"].(float64) != 2 {
		t.Errorf("window not forwarded: %v", p)
	}
	var page map[string]any
	_ = json.Unmarshal(raw, &page)
	if page["total"].(float64) != 14 {
		t.Errorf("page = %v", page)
	}
}

func TestDatasetEpisodesRejectsBadWindows(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/d",
	})
	base := "/v1/teams/" + defaultTeamID + "/datasets/" + ds.ID + "/episodes"
	for _, q := range []string{"?offset=-1", "?offset=abc", "?limit=0", "?limit=-3", "?limit=x"} {
		status, _ := doReq(t, s, token, http.MethodGet, base+q, nil)
		if status != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", q, status)
		}
	}
}

// Reading requires a host, and W1 can only read local roots. Both refusals are
// explicit: a dataset the hub cannot read must say so rather than return an
// empty page that reads as "this dataset has no episodes".
func TestDatasetReadsRefuseWhatTheyCannotServe(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")

	_, noHost := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "root_path": "/d/nohost",
	})
	status, _ := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/datasets/"+noHost.ID+"/episodes", nil)
	if status != http.StatusConflict {
		t.Errorf("hostless episodes status = %d, want 409", status)
	}
	status, _ = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/datasets/"+noHost.ID+"/refresh", nil)
	if status != http.StatusConflict {
		t.Errorf("hostless refresh status = %d, want 409", status)
	}

	_, remote := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/d/remote", "source": "sftp",
	})
	status, _ = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/datasets/"+remote.ID+"/episodes", nil)
	if status != http.StatusNotImplemented {
		t.Errorf("remote episodes status = %d, want 501", status)
	}
}

// A dataset registered in one team must be invisible to another — the join
// through projects is the only thing enforcing that, so it is worth pinning.
func TestDatasetTeamScoping(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedTestProject(t, s, defaultTeamID)
	seedDatasetHost(t, s, "host-a")
	_, ds := registerDataset(t, s, token, map[string]any{
		"project_id": proj, "host_id": "host-a", "root_path": "/d",
	})

	other := "team-other"
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, datetime('now'))`,
		other, "Other"); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/v1/teams/" + other + "/datasets/" + ds.ID, http.StatusNotFound},
		{http.MethodPatch, "/v1/teams/" + other + "/datasets/" + ds.ID, http.StatusNotFound},
		{http.MethodDelete, "/v1/teams/" + other + "/datasets/" + ds.ID, http.StatusNotFound},
		{http.MethodGet, "/v1/teams/" + other + "/datasets/" + ds.ID + "/episodes", http.StatusNotFound},
		{http.MethodPost, "/v1/teams/" + other + "/datasets/" + ds.ID + "/refresh", http.StatusNotFound},
	} {
		status, _ := doReq(t, s, token, tc.method, tc.path, map[string]any{"name": "x"})
		if status != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, status, tc.want)
		}
	}
	// And it must not appear in the other team's list.
	status, raw := doReq(t, s, token, http.MethodGet, "/v1/teams/"+other+"/datasets", nil)
	var list []datasetOut
	_ = json.Unmarshal(raw, &list)
	if status != http.StatusOK || len(list) != 0 {
		t.Errorf("cross-team list = %d %+v", status, list)
	}
}

func TestDatasetNameFromPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/data/lerobot/pick", "pick"},
		{"/data/lerobot/pick/", "pick"},
		{"/pick", "pick"},
		{"pick", "pick"},
		{"/", "/"},
	} {
		if got := datasetNameFromPath(tc.in); got != tc.want {
			t.Errorf("datasetNameFromPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
