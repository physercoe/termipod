// tools_datasets.go — MCP read tools for the Replay dataset library
// (coworking plan lane J1).
//
// Datasets were REST-complete and MCP-empty: the hub has routed
// /v1/teams/{team}/datasets since the replay wedge (server.go §6.9) and
// neither tool registry carried an entry, so a dataset a director opened
// in Replay was a thing the agent beside them could not name, let alone
// read.
//
// The four tools split along the data-ownership law (blueprint §4), and
// their descriptions say which half they are in:
//
//   - datasets_list / datasets_get read hub rows. Cheap and always
//     available, because the hub owns the name and the folded digest.
//   - dataset_episodes_list / dataset_episode_series are PROXIED to the
//     host that owns the bytes. They cost a tunnel round-trip; they can
//     refuse for reasons that are facts about the dataset rather than
//     failures (no host attached, a non-local root, a LeRobot generation
//     the reader does not support); and every cap that shaped the answer
//     travels in the answer. A tool that silently truncated would teach
//     an agent that a 50k-episode dataset has 200 episodes.

package hubmcpserver

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// datasetToolDefs returns lane J1's four read tools. Called from
// buildTools() and appended to the dispatch table, the way the template
// family is.
func datasetToolDefs() []toolDef {
	return []toolDef{
		datasetsListTool(),
		datasetsGetTool(),
		datasetEpisodesListTool(),
		datasetEpisodeSeriesTool(),
	}
}

// datasetDigest is the part of a dataset's folded digest a LISTING needs.
// Decoding only these fields is what keeps the listing small: the full
// digest carries per-feature stats, a task list and a length histogram,
// which is a page of JSON per dataset and the wrong thing to spend an
// agent's context on before it has chosen one.
type datasetDigest struct {
	TotalEpisodes   int64   `json:"total_episodes"`
	TotalFrames     int64   `json:"total_frames"`
	TotalTasks      int64   `json:"total_tasks"`
	DurationSec     float64 `json:"duration_sec"`
	FPS             float64 `json:"fps"`
	RobotType       string  `json:"robot_type"`
	CodebaseVersion string  `json:"codebase_version"`
}

// datasetRow decodes one hub dataset row down to what a listing shows.
type datasetRow struct {
	ID           string         `json:"id"`
	ProjectID    string         `json:"project_id"`
	HostID       string         `json:"host_id"`
	Name         string         `json:"name"`
	RootPath     string         `json:"root_path"`
	Source       string         `json:"source"`
	Format       string         `json:"format"`
	EnvRef       string         `json:"env_ref"`
	DigestTS     string         `json:"digest_ts"`
	RegisteredAt string         `json:"registered_at"`
	Digest       *datasetDigest `json:"digest"`
}

// datasetListEntry is one row as the tool reports it. The digest is
// flattened to its headline counts rather than nested, so there is no
// object here that LOOKS like a digest but is missing most of one, and
// `read` states the distinction the Replay surface also makes visible: a
// dataset nobody has read yet is not a dataset with zero episodes.
type datasetListEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	HostID    string `json:"host_id,omitempty"`
	RootPath  string `json:"root_path"`
	Source    string `json:"source"`
	Format    string `json:"format,omitempty"`
	EnvRef    string `json:"env_ref,omitempty"`

	Read     bool   `json:"read"`
	DigestTS string `json:"digest_ts,omitempty"`

	Episodes        int64   `json:"episodes,omitempty"`
	Frames          int64   `json:"frames,omitempty"`
	Tasks           int64   `json:"tasks,omitempty"`
	DurationSec     float64 `json:"duration_sec,omitempty"`
	FPS             float64 `json:"fps,omitempty"`
	RobotType       string  `json:"robot_type,omitempty"`
	CodebaseVersion string  `json:"codebase_version,omitempty"`

	RegisteredAt string `json:"registered_at,omitempty"`
}

func datasetsListTool() toolDef {
	return toolDef{
		Name: "datasets_list",
		Description: "List the datasets registered in this team — the Replay library. Optional `project` and `host` filters.\n\n" +
			"Each row is hub metadata plus the HEADLINE numbers of its digest: `id`, `name`, `root_path`, `source` (local|sftp|hf), `host_id`, `format`, `env_ref`, and `episodes`/`frames`/`tasks`/`duration_sec`/`fps`/`robot_type`. " +
			"Call `datasets_get` for one dataset's full digest (per-feature stats, the task list, the length histogram, video streams).\n\n" +
			"`read: false` means nobody has ever read this root — the row was registered and no digest was ever folded. That is NOT the same as a dataset with zero episodes, and the counts are absent rather than 0 to keep the two apart.",
		InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"},"host":{"type":"string"}}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			q := url.Values{}
			if p, ok := args["project"].(string); ok && p != "" {
				q.Set("project", p)
			}
			if h, ok := args["host"].(string); ok && h != "" {
				q.Set("host", h)
			}
			var rows []datasetRow
			if err := c.do("GET", c.teamPath("/datasets"), q, nil, &rows); err != nil {
				return nil, err
			}
			out := make([]datasetListEntry, 0, len(rows))
			for _, r := range rows {
				e := datasetListEntry{
					ID:           r.ID,
					Name:         r.Name,
					ProjectID:    r.ProjectID,
					HostID:       r.HostID,
					RootPath:     r.RootPath,
					Source:       r.Source,
					Format:       r.Format,
					EnvRef:       r.EnvRef,
					DigestTS:     r.DigestTS,
					RegisteredAt: r.RegisteredAt,
				}
				if r.Digest != nil {
					e.Read = true
					e.Episodes = r.Digest.TotalEpisodes
					e.Frames = r.Digest.TotalFrames
					e.Tasks = r.Digest.TotalTasks
					e.DurationSec = r.Digest.DurationSec
					e.FPS = r.Digest.FPS
					e.RobotType = r.Digest.RobotType
					e.CodebaseVersion = r.Digest.CodebaseVersion
				}
				out = append(out, e)
			}
			return map[string]any{"datasets": out, "count": len(out)}, nil
		},
	}
}

func datasetsGetTool() toolDef {
	return toolDef{
		Name: "datasets_get",
		Description: "Fetch one dataset by id, with its full folded digest. Required: `dataset`.\n\n" +
			"The digest is metadata about metadata — everything in it was derived from the dataset's `meta/` tree, never from the frames: `total_episodes`/`total_frames`/`total_tasks`, `fps`, `duration_sec`, `robot_type`, `codebase_version`, `features`, `video_streams`, `tasks`, per-feature `stats`, and `length_histogram`.\n\n" +
			"Read the digest's own cap flags before quoting a number as the dataset's: `stats_partial` (the stats fold stopped early — `stats_episodes` says how many episodes it saw), `tasks_truncated`, `episodes_truncated`, and `warnings`. `stats_source` names which file the stats came from, which differs by LeRobot generation and matters when comparing two datasets.\n\n" +
			"No `digest` at all means the root has never been read. `digest_ts` is when it was last folded; the desktop's Refresh action re-folds it, and a digest older than the data on disk is stale rather than wrong.",
		InputSchema: schema(`{"type":"object","required":["dataset"],"properties":{"dataset":{"type":"string"}}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			id, _ := args["dataset"].(string)
			if id == "" {
				return nil, fmt.Errorf("dataset is required")
			}
			var out json.RawMessage
			if err := c.do("GET", c.teamPath("/datasets/"+url.PathEscape(id)), nil, nil, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
	}
}

func datasetEpisodesListTool() toolDef {
	return toolDef{
		Name: "dataset_episodes_list",
		Description: "List one dataset's episodes, windowed. Required: `dataset`. Optional: `offset` (episode position, not a byte offset), `limit` (default 200, hard cap 1000).\n\n" +
			"Returns `{episodes, offset, limit, total, truncated}`. `total` is the dataset's episode count from its own info.json, so you can page without walking the table; `limit` is the page size ACTUALLY applied and `truncated: true` means your limit was clamped down. Each episode carries `index`, `length` (frames), `duration_sec`, its `tasks`, and — on LeRobot v3.0 — where its rows live (`data_chunk`/`data_file`/`from_index`/`to_index`) plus per-feature video slices.\n\n" +
			"This is NOT stored on the hub. It is proxied live to the host that owns the bytes, so it costs a round-trip and can answer with a refusal that is a fact about the dataset rather than a failure: 409 = the dataset has no host attached; 501 = its root is not `local` (sftp/hf roots are registered but not yet readable); 422 = the host does not support that LeRobot generation, and names the `codebase_version` it refused; 504 = the host did not answer.",
		InputSchema: schema(`{"type":"object","required":["dataset"],"properties":{"dataset":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":1000}}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			id, _ := args["dataset"].(string)
			if id == "" {
				return nil, fmt.Errorf("dataset is required")
			}
			q := url.Values{}
			for _, key := range []string{"offset", "limit"} {
				n, ok, err := intArg(args, key)
				if err != nil {
					return nil, err
				}
				if ok {
					q.Set(key, fmt.Sprintf("%d", n))
				}
			}
			var out json.RawMessage
			if err := c.do("GET", c.teamPath("/datasets/"+url.PathEscape(id)+"/episodes"), q, nil, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
	}
}

func datasetEpisodeSeriesTool() toolDef {
	return toolDef{
		Name: "dataset_episode_series",
		Description: "Read one episode's numeric channels — the curves behind a recording. Required: `dataset`, `episode` (the episode_index, which is what `dataset_episodes_list` reports as `index`, NOT a position in the page). Optional: `features` (feature keys such as `observation.state` or `action`; omit for every numeric feature), `max_points` (per-channel budget, default 1000, hard cap 5000).\n\n" +
			"Returns `{episode, length, fps, stride, points, timestamps, series, downsampled, truncated, warnings}`. `series` is one entry per feature — `{key, dtype, channels}` — and a channel is one scalar track (a joint, a gripper, a reward) with a `name` when the dataset labels it. `timestamps` are seconds from the START of the episode, one per point.\n\n" +
			"Read the caps before drawing a conclusion from the shape: `length` is the episode's frame count BEFORE decimation, `stride` is the decimation applied (1 = every frame), `points` is what you actually got, and `downsampled: true` means the curve is not frame-exact — do not claim a one-frame spike from a downsampled series. `truncated: true` means a cap dropped whole features or channels (at most 24 features and 96 channels per call). A feature key that does not exist comes back in `warnings` rather than failing the call, because a saved selection outliving a dataset edit is normal.\n\n" +
			"Proxied to the host that owns the bytes, with the same refusals as `dataset_episodes_list` (409 no host, 501 non-local root, 422 unsupported generation, 504 host silent). The host reads the parquet and returns decimated floats; raw frames and video never travel.",
		InputSchema: schema(`{"type":"object","required":["dataset","episode"],"properties":{"dataset":{"type":"string"},"episode":{"type":"integer","minimum":0},"features":{"type":"array","items":{"type":"string"}},"max_points":{"type":"integer","minimum":1,"maximum":5000}}}`),
		call: func(c *hubClient, args map[string]any) (any, error) {
			id, _ := args["dataset"].(string)
			if id == "" {
				return nil, fmt.Errorf("dataset is required")
			}
			episode, ok, err := intArg(args, "episode")
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("episode is required")
			}
			// No negative-index guard here: the schema's `minimum: 0` is the
			// layer that enforces it, on both dispatch paths (run.go and
			// server/mcp.go both call ValidateArgs first). A second check here
			// could not change any answer — it would only obscure which layer
			// is load-bearing. The empty-string guards above are different:
			// `required` accepts "" quite happily, so those DO fire.
			q := url.Values{}
			// The REST leg takes a comma list. Feature keys contain dots
			// ("observation.state") but never commas, so no escaping is needed;
			// blank entries are dropped rather than forwarded, since an empty key
			// comes back as a warning about a feature nobody asked for.
			if raw, present := args["features"]; present && raw != nil {
				keys, err := stringsArg(raw, "features")
				if err != nil {
					return nil, err
				}
				if len(keys) > 0 {
					q.Set("features", strings.Join(keys, ","))
				}
			}
			if n, ok, err := intArg(args, "max_points"); err != nil {
				return nil, err
			} else if ok {
				q.Set("max_points", fmt.Sprintf("%d", n))
			}
			var out json.RawMessage
			path := fmt.Sprintf("/datasets/%s/episodes/%d/series", url.PathEscape(id), episode)
			if err := c.do("GET", c.teamPath(path), q, nil, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
	}
}

// intArg reads an optional integer argument. MCP arguments arrive as
// decoded JSON, so a whole number is a float64 here; the schema check in
// run.go/mcp.go rejects a non-integer before the closure runs, and this
// refuses rather than silently dropping the argument if one ever reaches
// it by another path — a dropped `limit` would answer a different
// question than the one asked.
func intArg(args map[string]any, key string) (int64, bool, error) {
	v, present := args[key]
	if !present || v == nil {
		return 0, false, nil
	}
	switch n := v.(type) {
	case float64:
		if n != float64(int64(n)) {
			return 0, false, fmt.Errorf("%s must be a whole number", key)
		}
		return int64(n), true, nil
	case int:
		return int64(n), true, nil
	case int64:
		return n, true, nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false, fmt.Errorf("%s must be a whole number", key)
		}
		return i, true, nil
	default:
		return 0, false, fmt.Errorf("%s must be a whole number", key)
	}
}

// stringsArg reads an array-of-strings argument, dropping blanks. A
// non-array (or an array holding something that is not a string) is
// refused by name rather than partially honoured.
func stringsArg(v any, key string) ([]string, error) {
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be an array of strings", key)
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}
