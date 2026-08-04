package hubmcpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Lane J1. The dataset family was the last surface that was REST-complete
// and MCP-empty, so these tests pin the two things a catalog gap hides:
// the call reaches the right hub endpoint, and the caps/refusals that make
// a proxied read honest survive the trip to the agent.

// callTool runs one tools/call line against a fake hub and returns the
// tool's text content plus whether the tool reported an error. Every test
// below asserts on the decoded content rather than the raw envelope: JSON-RPC
// escaping turns `"dataset" is required` into `\"dataset\"` and `>` into
// `>`, so grepping the envelope tests the encoder, not the tool.
func callTool(t *testing.T, c *hubClient, id int, name string, args string) (string, bool) {
	t.Helper()
	line := []byte(`{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"` +
		name + `","arguments":` + args + `}}` + "\n")
	raw, ok := handleLine(c, buildTools(), line)
	if !ok {
		t.Fatalf("%s: expected a response", name)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *jsonrpcError `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%s: unmarshal envelope: %v (%s)", name, err, raw)
	}
	if resp.Error != nil {
		t.Fatalf("%s: unexpected rpc error: %+v", name, resp.Error)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("%s: empty content: %s", name, raw)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

// datasets_list carries the filters through and reduces each row to its
// headline counts. The reduction is the point: a full digest per row is a
// page of per-feature stats, and an agent choosing a dataset does not need
// them yet.
func TestToolsCall_DatasetsList(t *testing.T) {
	var sawPath, sawQuery string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath, sawQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"ds_1","project_id":"pr_1","host_id":"h_1","name":"pick","root_path":"/data/pick",
			 "source":"local","format":"lerobot_v30","registered_at":"2026-08-01T00:00:00Z",
			 "digest_ts":"2026-08-02T00:00:00Z",
			 "digest":{"total_episodes":120,"total_frames":45000,"total_tasks":3,"duration_sec":900,
			           "fps":50,"robot_type":"so101","codebase_version":"v3.0",
			           "stats":{"observation.state":{"mean":[1,2,3]}}}},
			{"id":"ds_2","project_id":"pr_1","name":"unread","root_path":"/data/unread","source":"local",
			 "registered_at":"2026-08-01T00:00:00Z"}
		]`))
	})

	text, isErr := callTool(t, c, 1, "datasets_list", `{"project":"pr_1","host":"h_1"}`)
	if isErr {
		t.Fatalf("datasets_list reported an error: %s", text)
	}
	if sawPath != "/v1/teams/team-alpha/datasets" {
		t.Errorf("hub saw path %q", sawPath)
	}
	if !strings.Contains(sawQuery, "project=pr_1") || !strings.Contains(sawQuery, "host=h_1") {
		t.Errorf("filters did not reach the hub: %q", sawQuery)
	}

	var out struct {
		Count    int `json:"count"`
		Datasets []struct {
			ID       string  `json:"id"`
			Read     bool    `json:"read"`
			Episodes int64   `json:"episodes"`
			Frames   int64   `json:"frames"`
			FPS      float64 `json:"fps"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode tool result: %v (%s)", err, text)
	}
	if out.Count != 2 || len(out.Datasets) != 2 {
		t.Fatalf("want 2 rows, got count=%d len=%d", out.Count, len(out.Datasets))
	}
	if !out.Datasets[0].Read || out.Datasets[0].Episodes != 120 || out.Datasets[0].Frames != 45000 {
		t.Errorf("headline counts lost: %+v", out.Datasets[0])
	}
	// The heavy half of the digest must NOT ride along in a listing.
	if strings.Contains(text, "observation.state") {
		t.Errorf("per-feature stats leaked into the listing: %s", text)
	}
	// A never-read dataset says so, and does not claim zero episodes.
	if out.Datasets[1].Read {
		t.Error("a dataset with no digest was reported as read")
	}
	if strings.Contains(text, `"episodes":0`) {
		t.Errorf("an unread dataset rendered as zero episodes — that is a different fact: %s", text)
	}
}

// datasets_get hands the hub's row back untouched, including the parts a
// listing drops. An agent that asked for one dataset asked for all of it.
func TestToolsCall_DatasetsGet(t *testing.T) {
	var sawPath string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ds_1","name":"pick","digest":{"stats_partial":true,
			"stats_episodes":50,"tasks":["pick the cube"],"stats":{"observation.state":{"mean":[1]}}}}`))
	})

	text, isErr := callTool(t, c, 2, "datasets_get", `{"dataset":"ds_1"}`)
	if isErr {
		t.Fatalf("datasets_get reported an error: %s", text)
	}
	if sawPath != "/v1/teams/team-alpha/datasets/ds_1" {
		t.Errorf("hub saw path %q", sawPath)
	}
	for _, want := range []string{"stats_partial", "stats_episodes", "observation.state"} {
		if !strings.Contains(text, want) {
			t.Errorf("full digest lost %q in the passthrough: %s", want, text)
		}
	}
}

// Two refusals with two different enforcers. The absent argument is caught
// by the schema's `required`; the EMPTY one is not — `required` accepts ""
// happily — so that case pins the closure's own guard. Without it the call
// would GET .../datasets/, a 404 dressed up as a routing accident.
func TestToolsCall_DatasetsGet_RequiresDataset(t *testing.T) {
	for _, args := range []string{`{}`, `{"dataset":""}`} {
		called := false
		c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		text, isErr := callTool(t, c, 3, "datasets_get", args)
		if called {
			t.Errorf("datasets_get %s still reached the hub", args)
		}
		if !isErr {
			t.Fatalf("datasets_get %s reported success: %s", args, text)
		}
		if !strings.Contains(text, "dataset") {
			t.Errorf("refusal for %s does not name the argument: %s", args, text)
		}
	}
}

// The episodes window is proxied, and the caps that shaped it must arrive
// with it. `truncated` is the load-bearing one: without it a clamped page
// looks like the whole table.
func TestToolsCall_DatasetEpisodesList(t *testing.T) {
	var sawPath, sawQuery string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath, sawQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"episodes":[{"index":7,"length":300}],"offset":200,"limit":1000,
			"total":50000,"truncated":true}`))
	})

	text, isErr := callTool(t, c, 4, "dataset_episodes_list", `{"dataset":"ds_1","offset":200,"limit":9000}`)
	if isErr {
		t.Fatalf("dataset_episodes_list reported an error: %s", text)
	}
	if sawPath != "/v1/teams/team-alpha/datasets/ds_1/episodes" {
		t.Errorf("hub saw path %q", sawPath)
	}
	if !strings.Contains(sawQuery, "offset=200") || !strings.Contains(sawQuery, "limit=9000") {
		t.Errorf("window arguments did not reach the hub: %q", sawQuery)
	}
	for _, want := range []string{`"total":50000`, `"limit":1000`, `"truncated":true`} {
		if !strings.Contains(text, want) {
			t.Errorf("cap %s did not survive to the agent: %s", want, text)
		}
	}
}

// A proxied read can refuse for reasons that are facts about the dataset.
// Those must reach the agent with their cause intact — "only local dataset
// roots can be read today" is actionable; "tool failed" is not.
func TestToolsCall_DatasetEpisodesList_RefusalCarriesItsCause(t *testing.T) {
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"only local dataset roots can be read today"}`))
	})
	text, isErr := callTool(t, c, 5, "dataset_episodes_list", `{"dataset":"ds_sftp"}`)
	if !isErr {
		t.Fatalf("a 501 from the host leg reported success: %s", text)
	}
	if !strings.Contains(text, "only local dataset roots") {
		t.Errorf("the host's own reason was flattened away: %s", text)
	}
	if !strings.Contains(text, "501") {
		t.Errorf("refusal does not carry its status: %s", text)
	}
}

// The series read addresses an episode by its episode_index, and its
// optional narrowing arguments have to arrive in the shape the REST leg
// parses: a comma list for features, a plain integer for max_points.
func TestToolsCall_DatasetEpisodeSeries(t *testing.T) {
	var sawPath, sawQuery string
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		sawPath, sawQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"episode":7,"length":40000,"stride":40,"points":1000,
			"downsampled":true,"truncated":true,"warnings":["feature \"nope\" is not a numeric feature of this dataset"],
			"series":[{"key":"observation.state","channels":[{"name":"shoulder","values":[0.1]}]}]}`))
	})

	text, isErr := callTool(t, c, 6, "dataset_episode_series",
		`{"dataset":"ds_1","episode":7,"features":["observation.state"," ","action"],"max_points":1000}`)
	if isErr {
		t.Fatalf("dataset_episode_series reported an error: %s", text)
	}
	if sawPath != "/v1/teams/team-alpha/datasets/ds_1/episodes/7/series" {
		t.Errorf("hub saw path %q", sawPath)
	}
	// Blank entries are dropped, not forwarded: an empty key comes back as a
	// warning about a feature nobody asked for.
	if !strings.Contains(sawQuery, "features=observation.state%2Caction") {
		t.Errorf("feature list did not reach the hub as a comma list: %q", sawQuery)
	}
	if !strings.Contains(sawQuery, "max_points=1000") {
		t.Errorf("max_points did not reach the hub: %q", sawQuery)
	}
	for _, want := range []string{`"stride":40`, `"downsampled":true`, `"truncated":true`, "not a numeric feature"} {
		if !strings.Contains(text, want) {
			t.Errorf("honesty field %s did not survive to the agent: %s", want, text)
		}
	}
}

// A negative episode index never becomes a URL. The enforcing layer is the
// schema's `minimum: 0` — a closure-side check was tried here and proved
// unkillable by mutation, so it was removed rather than left to look
// load-bearing. This asserts the outcome, which is what an agent sees.
func TestToolsCall_DatasetEpisodeSeries_RejectsNegativeEpisode(t *testing.T) {
	called := false
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	text, isErr := callTool(t, c, 7, "dataset_episode_series", `{"dataset":"ds_1","episode":-3}`)
	if called {
		t.Error("a negative episode index still reached the hub")
	}
	if !isErr {
		t.Fatalf("a negative episode index reported success: %s", text)
	}
	if !strings.Contains(text, "episode") {
		t.Errorf("refusal does not name the argument: %s", text)
	}
}

// intArg is the boundary between decoded JSON and a URL parameter. A
// fractional "limit" must be refused rather than silently floored — an
// answer to a different question is worse than an error.
func TestIntArg(t *testing.T) {
	if _, ok, err := intArg(map[string]any{}, "limit"); ok || err != nil {
		t.Errorf("absent argument: got ok=%v err=%v, want false/nil", ok, err)
	}
	if _, ok, err := intArg(map[string]any{"limit": nil}, "limit"); ok || err != nil {
		t.Errorf("null argument: got ok=%v err=%v, want false/nil", ok, err)
	}
	n, ok, err := intArg(map[string]any{"limit": float64(50)}, "limit")
	if err != nil || !ok || n != 50 {
		t.Errorf("whole float: got %d/%v/%v", n, ok, err)
	}
	if _, _, err := intArg(map[string]any{"limit": 1.5}, "limit"); err == nil {
		t.Error("a fractional limit was accepted")
	}
	if _, _, err := intArg(map[string]any{"limit": "50"}, "limit"); err == nil {
		t.Error("a string limit was accepted — the schema declares an integer")
	}
}

func TestStringsArg(t *testing.T) {
	got, err := stringsArg([]any{"a", "  ", "b"}, "features")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("blank entries not dropped: %#v", got)
	}
	if _, err := stringsArg("a,b", "features"); err == nil {
		t.Error("a bare string was accepted where an array is declared")
	}
	if _, err := stringsArg([]any{"a", 3}, "features"); err == nil {
		t.Error("a non-string element was accepted")
	}
}

// The catalog/spec/meta trio must move together — a handler without a
// registry entry is invisible to agents (CLAUDE.md), and a read tool that
// inherits the fail-closed ReadOnly=false default is advertised as
// side-effecting and unsafe to batch.
func TestDatasetTools_AreAdvertisedAsReads(t *testing.T) {
	specs := map[string]ToolSpec{}
	for _, s := range toolRegistry() {
		specs[s.Name] = s
	}
	for _, name := range []string{"datasets_list", "datasets_get", "dataset_episodes_list", "dataset_episode_series"} {
		s, found := specs[name]
		if !found {
			t.Errorf("%s is missing from the ToolSpec registry", name)
			continue
		}
		if s.Short == "" {
			t.Errorf("%s has no one-line contract (ADR-031 W2.a)", name)
		}
		if s.Description == "" {
			t.Errorf("%s has no description — the spec did not find its catalog closure", name)
		}
		if !s.ReadOnly {
			t.Errorf("%s observes and must be ReadOnly", name)
		}
		if s.Backend != name {
			t.Errorf("%s Backend = %q, want the authority dispatch key", name, s.Backend)
		}
		if !s.WorkerEligible {
			t.Errorf("%s reads a library the director already opened; a worker needs no elevation", name)
		}
	}
	// And the family is reachable from the run an agent lands on first —
	// runs.dataset_id is the only link between the two halves.
	if !containsName(toolMeta["runs_get"].seeAlso, "datasets_get") {
		t.Error("runs_get should point at datasets_get — a run names the dataset it trained on")
	}
}
