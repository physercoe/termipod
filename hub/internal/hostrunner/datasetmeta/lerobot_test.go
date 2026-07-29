package datasetmeta

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// Ground truth for lerobot/nyu_rot_dataset, read off the fixture files rather
// than off this package's output: 14 episodes, 440 frames at 5 fps, 12 tasks.
var (
	nyuLengths = []int64{40, 30, 30, 30, 30, 40, 30, 30, 30, 30, 30, 30, 30, 30}
	nyuTasks   = []string{
		"close the door",
		"erase the board",
		"hang the bag on the hook",
		"hang the hanger on the rod",
		"hang the mug on the hook",
		"insert the peg in the cup",
		"open the box",
		"pour the almonds into the cup",
		"press the button",
		"reach the blue mark on the table",
		"stack the cups",
		"turn the knob",
	}
)

func TestDigestHeadlineFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		root   string
		format Format
		stats  string
	}{
		{"v2.1", fixNyuV21, FormatLeRobotV21, pathEpisodesStatsJSONL},
		{"v3.0", fixNyuV30, FormatLeRobotV30, pathStatsJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ReadDigest(src(t, tc.root))
			if err != nil {
				t.Fatal(err)
			}
			if d.SchemaVersion != DigestSchemaVersion {
				t.Errorf("schema version = %d", d.SchemaVersion)
			}
			if d.Format != tc.format {
				t.Errorf("format = %q, want %q", d.Format, tc.format)
			}
			if d.TotalEpisodes != 14 || d.TotalFrames != 440 || d.TotalTasks != 12 {
				t.Errorf("counts = %d eps / %d frames / %d tasks, want 14/440/12",
					d.TotalEpisodes, d.TotalFrames, d.TotalTasks)
			}
			if d.FPS != 5 {
				t.Errorf("fps = %v, want 5", d.FPS)
			}
			// 440 frames at 5 fps is 88 seconds.
			if math.Abs(d.DurationSec-88) > 1e-9 {
				t.Errorf("duration = %v, want 88", d.DurationSec)
			}
			if !reflect.DeepEqual(d.Tasks, nyuTasks) {
				t.Errorf("tasks = %#v", d.Tasks)
			}
			if d.TasksTruncated {
				t.Error("12 tasks must not report truncation")
			}
			if d.StatsSource != tc.stats {
				t.Errorf("stats source = %q, want %q", d.StatsSource, tc.stats)
			}
			if d.EpisodesScanned != 14 || d.EpisodesTruncated {
				t.Errorf("scanned = %d, truncated = %v", d.EpisodesScanned, d.EpisodesTruncated)
			}
			// One camera, 84x84.
			if len(d.VideoStreams) != 1 || d.VideoStreams[0].Key != "observation.images.image" {
				t.Fatalf("video streams = %+v", d.VideoStreams)
			}
			if d.VideoStreams[0].Width != 84 || d.VideoStreams[0].Height != 84 {
				t.Errorf("geometry = %dx%d, want 84x84",
					d.VideoStreams[0].Width, d.VideoStreams[0].Height)
			}
			// The histogram must account for all 14 episodes.
			var counted int64
			for _, b := range d.LengthHistogram {
				counted += b.Count
			}
			if counted != 14 {
				t.Errorf("histogram counts %d of 14: %+v", counted, d.LengthHistogram)
			}
			// bool features must survive: next.done is dtype bool.
			var sawBool bool
			for _, f := range d.Features {
				if f.Key == "next.done" && f.DType == "bool" {
					sawBool = true
				}
			}
			if !sawBool {
				t.Error("the bool feature next.done did not reach the digest")
			}
		})
	}
}

// The cross-generation check: the SAME dataset, published in both layouts,
// must fold to the same statistics.
//
// This is not an A==B tautology. v2.1 has no dataset-level stats file at all —
// the numbers are recovered by folding 14 per-episode blocks out of a JSONL
// file — while v3.0 reads a precomputed stats.json. Different files, different
// decoders, no shared code below the comparison. If the weighted mean or the
// second-moment recovery of the standard deviation were wrong, only this test
// would notice; every within-generation assertion would still pass.
func TestStatsFoldMatchesV30(t *testing.T) {
	folded, err := ReadDigest(src(t, fixNyuV21))
	if err != nil {
		t.Fatal(err)
	}
	direct, err := ReadDigest(src(t, fixNyuV30))
	if err != nil {
		t.Fatal(err)
	}
	if len(folded.Stats) == 0 {
		t.Fatal("v2.1 produced no stats")
	}
	if len(folded.Stats) != len(direct.Stats) {
		t.Fatalf("feature count: v2.1 %d vs v3.0 %d", len(folded.Stats), len(direct.Stats))
	}
	// Float round-off in the second-moment identity; measured worst case on
	// this fixture is ~2.5e-9.
	const tol = 1e-6
	closeEnough := func(a, b float64) bool {
		return math.Abs(a-b) <= tol*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
	}
	for feature, want := range direct.Stats {
		got, ok := folded.Stats[feature]
		if !ok {
			t.Errorf("%s: absent from the v2.1 fold", feature)
			continue
		}
		if got.Count != want.Count {
			t.Errorf("%s: count = %d, want %d", feature, got.Count, want.Count)
		}
		for _, part := range []struct {
			name string
			a, b []float64
		}{
			{"min", got.Min, want.Min},
			{"max", got.Max, want.Max},
			{"mean", got.Mean, want.Mean},
			{"std", got.Std, want.Std},
		} {
			if len(part.a) != len(part.b) {
				t.Errorf("%s.%s: %d components, want %d", feature, part.name, len(part.a), len(part.b))
				continue
			}
			for i := range part.a {
				if !closeEnough(part.a[i], part.b[i]) {
					t.Errorf("%s.%s[%d] = %v, want %v", feature, part.name, i, part.a[i], part.b[i])
				}
			}
		}
	}
	// Both must report how many episodes backed the number.
	if folded.StatsEpisodes != 14 || folded.StatsPartial {
		t.Errorf("v2.1 stats episodes = %d partial = %v", folded.StatsEpisodes, folded.StatsPartial)
	}
}

// The second cross-generation check: the episodes table itself.
func TestEpisodesAgreeAcrossGenerations(t *testing.T) {
	v21, err := ReadEpisodes(src(t, fixNyuV21), EpisodeRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	v30, err := ReadEpisodes(src(t, fixNyuV30), EpisodeRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(v21.Episodes) != 14 || len(v30.Episodes) != 14 {
		t.Fatalf("episode counts: v2.1 %d, v3.0 %d", len(v21.Episodes), len(v30.Episodes))
	}
	for i := range v21.Episodes {
		a, b := v21.Episodes[i], v30.Episodes[i]
		if a.Index != b.Index {
			t.Errorf("row %d: index %d vs %d", i, a.Index, b.Index)
		}
		if a.Length != b.Length {
			t.Errorf("episode %d: length %d vs %d", a.Index, a.Length, b.Length)
		}
		if a.Length != nyuLengths[i] {
			t.Errorf("episode %d: length %d, want %d from the fixture", a.Index, a.Length, nyuLengths[i])
		}
		if !reflect.DeepEqual(a.Tasks, b.Tasks) {
			t.Errorf("episode %d: tasks %v vs %v", a.Index, a.Tasks, b.Tasks)
		}
		if math.Abs(a.DurationSec-b.DurationSec) > 1e-9 {
			t.Errorf("episode %d: duration %v vs %v", a.Index, a.DurationSec, b.DurationSec)
		}
	}
	// Episode 0 runs 40 frames at 5 fps.
	if got := v30.Episodes[0].DurationSec; math.Abs(got-8) > 1e-9 {
		t.Errorf("episode 0 duration = %v, want 8", got)
	}
	if got := v21.Episodes[0].Tasks; len(got) != 1 || got[0] != "erase the board" {
		t.Errorf("episode 0 tasks = %v", got)
	}
}

// v3.0's whole reason for existing is that an episode is a slice of a shared
// file. If the offsets are read wrong, the player shows the wrong episode —
// and it shows it perfectly happily, which is why this is pinned rather than
// eyeballed.
func TestEpisodeOffsetsAreContiguousAndMatchLengths(t *testing.T) {
	page, err := ReadEpisodes(src(t, fixNyuV30), EpisodeRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var expect int64
	for _, ep := range page.Episodes {
		if ep.FromIndex == nil || ep.ToIndex == nil {
			t.Fatalf("episode %d has no row range", ep.Index)
		}
		if *ep.FromIndex != expect {
			t.Errorf("episode %d starts at %d, want %d (contiguous)", ep.Index, *ep.FromIndex, expect)
		}
		if got := *ep.ToIndex - *ep.FromIndex; got != ep.Length {
			t.Errorf("episode %d spans %d rows but declares length %d", ep.Index, got, ep.Length)
		}
		expect = *ep.ToIndex
	}
	// The walk must end exactly on the dataset's frame count.
	if expect != 440 {
		t.Errorf("offsets end at %d, want total_frames 440", expect)
	}
	// The episodes all share one data file in this fixture.
	for _, ep := range page.Episodes {
		if ep.DataChunk == nil || ep.DataFile == nil {
			t.Fatalf("episode %d has no data file location", ep.Index)
		}
		if *ep.DataChunk != 0 || *ep.DataFile != 0 {
			t.Errorf("episode %d located at chunk %d file %d, want 0/0",
				ep.Index, *ep.DataChunk, *ep.DataFile)
		}
	}
}

// Per-video time slices are what W2 turns into a playable range.
func TestVideoSlicesCarryTimeRanges(t *testing.T) {
	page, err := ReadEpisodes(src(t, fixNyuV30), EpisodeRequest{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	ep := page.Episodes[0]
	slice, ok := ep.Videos["observation.images.image"]
	if !ok {
		t.Fatalf("episode 0 videos = %+v", ep.Videos)
	}
	// 40 frames at 5 fps = 8 seconds, starting at 0.
	if slice.FromTS != 0 {
		t.Errorf("from_ts = %v, want 0", slice.FromTS)
	}
	if math.Abs(slice.ToTS-8) > 1e-6 {
		t.Errorf("to_ts = %v, want 8", slice.ToTS)
	}
	// Episode 1 must start where episode 0 ended.
	next := page.Episodes[1].Videos["observation.images.image"]
	if math.Abs(next.FromTS-slice.ToTS) > 1e-6 {
		t.Errorf("episode 1 starts at %v, episode 0 ended at %v", next.FromTS, slice.ToTS)
	}
}

// Paging must be a window onto the same ordering, not a re-read from the top.
func TestEpisodePagingWindows(t *testing.T) {
	for _, root := range []string{fixNyuV21, fixNyuV30} {
		s := src(t, root)
		all, err := ReadEpisodes(s, EpisodeRequest{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		var walked []Episode
		for off := int64(0); off < 14; off += 5 {
			p, err := ReadEpisodes(s, EpisodeRequest{Offset: off, Limit: 5})
			if err != nil {
				t.Fatal(err)
			}
			if p.Offset != off {
				t.Errorf("%s: page offset = %d, want %d", root, p.Offset, off)
			}
			walked = append(walked, p.Episodes...)
		}
		if len(walked) != len(all.Episodes) {
			t.Fatalf("%s: paged walk saw %d episodes, single read saw %d",
				root, len(walked), len(all.Episodes))
		}
		for i := range walked {
			if walked[i].Index != all.Episodes[i].Index || walked[i].Length != all.Episodes[i].Length {
				t.Errorf("%s: row %d differs between paged and whole reads", root, i)
			}
		}
		// Past the end is an empty page, not an error.
		p, err := ReadEpisodes(s, EpisodeRequest{Offset: 1000, Limit: 5})
		if err != nil {
			t.Fatalf("%s: reading past the end: %v", root, err)
		}
		if len(p.Episodes) != 0 {
			t.Errorf("%s: offset past the end returned %d episodes", root, len(p.Episodes))
		}
	}
}

// The task string's column name is not stable across exporters. Curated
// lerobot/* datasets leave it in the pandas index column "__index_level_0__";
// a dataset recorded with a newer LeRobot names it "task". Reading only the
// curated fixtures would have hardcoded the pandas artifact and broken on
// every freshly recorded dataset — which is most real ones.
func TestTasksParquetColumnNaming(t *testing.T) {
	t.Run("pandas index column", func(t *testing.T) {
		got, err := readTasksParquet(src(t, fixNyuV30))
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, nyuTasks) {
			t.Errorf("tasks = %#v", got)
		}
	})
	t.Run("explicit task column", func(t *testing.T) {
		got, err := readTasksParquet(src(t, fixTaccap))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"start eraze, repeat twice,home"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tasks = %#v, want %#v", got, want)
		}
	})
}

// Column choice is precedence, not position.
//
// Reading the fixtures alone cannot prove this: both of them have exactly two
// columns, so the positional fallback happens to land on the right one and a
// reader that ignored the names entirely would still pass. What must hold is
// that a recognised name outranks the guess.
func TestPickTaskColumnPrefersKnownNamesOverPosition(t *testing.T) {
	for _, tc := range []struct {
		name string
		cols []string
		want int
	}{
		{"curated pandas export", []string{"task_index", "__index_level_0__"}, 1},
		{"freshly recorded export", []string{"task_index", "task"}, 1},
		{"named column beats an earlier decoy", []string{"task_index", "notes", "task"}, 2},
		{"index column beats an earlier decoy", []string{"task_index", "notes", "__index_level_0__"}, 2},
		{"task wins over __index_level_0__", []string{"__index_level_0__", "task"}, 1},
		{"unknown spelling falls back positionally", []string{"task_index", "label"}, 1},
		{"nothing but the index", []string{"task_index"}, -1},
		{"no columns at all", nil, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickTaskColumn(tc.cols); got != tc.want {
				t.Errorf("pickTaskColumn(%v) = %d, want %d", tc.cols, got, tc.want)
			}
		})
	}
}

// Every pinned fixture is a single shard, because a real multi-shard dataset
// is far too large to vendor. But large datasets are exactly the ones that
// shard, so the cross-shard walk — enumeration order and the running episode
// count that carries between files — cannot go untested.
//
// The parquet bytes here are the real fixture's; only the directory
// arrangement is synthetic, and the arrangement is precisely what is under
// test. Two copies of a 14-episode shard must read as 28 episodes in shard
// order, and a page must be able to straddle the boundary.
func newMultiShardRoot(t *testing.T, shards int) *DirSource {
	t.Helper()
	realShard := filepath.Join(fixNyuV30, "meta", "episodes", "chunk-000", "file-000.parquet")
	body, err := os.ReadFile(realShard)
	if err != nil {
		t.Fatalf("fixture missing (run testdata/fetch-fixtures.sh): %v", err)
	}
	info, err := os.ReadFile(filepath.Join(fixNyuV30, "meta", "info.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "info.json"), info, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < shards; i++ {
		chunk := filepath.Join(dir, "meta", "episodes", fmt.Sprintf("chunk-%03d", i))
		if err := os.MkdirAll(chunk, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chunk, "file-000.parquet"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return NewDirSource(dir)
}

func TestEpisodesAcrossMultipleShards(t *testing.T) {
	s := newMultiShardRoot(t, 3)
	shards, err := listEpisodeShards(s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"meta/episodes/chunk-000/file-000.parquet",
		"meta/episodes/chunk-001/file-000.parquet",
		"meta/episodes/chunk-002/file-000.parquet",
	}
	if !reflect.DeepEqual(shards, want) {
		t.Fatalf("shard order = %#v", shards)
	}

	all, err := ReadEpisodes(s, EpisodeRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Episodes) != 42 {
		t.Fatalf("episodes across 3 shards = %d, want 42", len(all.Episodes))
	}
	// Each shard repeats the same 14 episodes, so position i must carry the
	// length of episode i%14. A walk that lost its place between files — or
	// restarted its count — shows up here immediately.
	for i, ep := range all.Episodes {
		if got, want := ep.Length, nyuLengths[i%14]; got != want {
			t.Errorf("row %d: length %d, want %d", i, got, want)
		}
	}

	// A page straddling the first shard boundary.
	page, err := ReadEpisodes(s, EpisodeRequest{Offset: 12, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Episodes) != 4 {
		t.Fatalf("straddling page = %d episodes, want 4", len(page.Episodes))
	}
	for i, ep := range page.Episodes {
		if got, want := ep.Length, nyuLengths[(12+i)%14]; got != want {
			t.Errorf("straddling row %d: length %d, want %d", i, got, want)
		}
	}
	// An offset landing exactly on a shard boundary is the case where a
	// row-group skip that is off by one row silently drops an episode.
	edge, err := ReadEpisodes(s, EpisodeRequest{Offset: 14, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(edge.Episodes) != 2 || edge.Episodes[0].Length != nyuLengths[0] {
		t.Fatalf("boundary page = %+v", edge.Episodes)
	}

	// The digest's histogram must also span every shard.
	d, err := ReadDigest(s)
	if err != nil {
		t.Fatal(err)
	}
	if d.EpisodesScanned != 42 {
		t.Errorf("scanned = %d, want 42 across 3 shards", d.EpisodesScanned)
	}
	var counted int64
	for _, b := range d.LengthHistogram {
		counted += b.Count
	}
	if counted != 42 {
		t.Errorf("histogram counts %d of 42", counted)
	}
}

// A slash inside a v3.0 column name is part of the name, not a nesting level.
// Reading it as a path finds nothing, and finding nothing looks exactly like
// "this dataset has no offsets" — a silent wrong answer.
func TestLeafNameTreatsSlashesAsLiteral(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"episode_index"}, "episode_index"},
		{[]string{"data/chunk_index"}, "data/chunk_index"},
		{[]string{"videos/observation.images.up/from_timestamp"}, "videos/observation.images.up/from_timestamp"},
		{[]string{"tasks", "list", "element"}, "tasks"},
		{[]string{"stats/action/min", "list", "element"}, "stats/action/min"},
	} {
		if got := leafName(tc.in); got != tc.want {
			t.Errorf("leafName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMissingOptionalFilesWarnRatherThanFail(t *testing.T) {
	// A v2.1 root with only info.json + episodes.jsonl still yields a digest;
	// the absent files become warnings, not an error.
	s := writeRoot(t, map[string]string{
		"meta/info.json": `{"codebase_version":"v2.1","fps":10,"total_episodes":2,
			"total_frames":20,"features":{"action":{"dtype":"float32","shape":[2]}}}`,
		"meta/episodes.jsonl": `{"episode_index":0,"tasks":["a"],"length":10}
{"episode_index":1,"tasks":["b"],"length":10}`,
	})
	d, err := ReadDigest(s)
	if err != nil {
		t.Fatalf("missing optional metadata must not fail the digest: %v", err)
	}
	if len(d.Warnings) != 2 {
		t.Errorf("warnings = %v, want one each for tasks.jsonl and episodes_stats.jsonl", d.Warnings)
	}
	if d.Stats != nil {
		t.Errorf("stats = %v, want none", d.Stats)
	}
	if d.EpisodesScanned != 2 {
		t.Errorf("scanned = %d, want 2", d.EpisodesScanned)
	}
}

func TestEpisodesJSONLOverlongLineIsRefused(t *testing.T) {
	// A truncated read here would under-report the episode count, which reads
	// as a smaller dataset rather than as a failure.
	long := make([]byte, maxJSONLLine+16)
	for i := range long {
		long[i] = 'x'
	}
	s := writeRoot(t, map[string]string{
		"meta/info.json":      `{"codebase_version":"v2.1","fps":10}`,
		"meta/episodes.jsonl": string(long),
	})
	_, err := ReadDigest(s)
	if err == nil {
		t.Fatal("want an error for an overlong JSONL line")
	}
}
