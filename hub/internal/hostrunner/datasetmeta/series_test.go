package datasetmeta

import (
	"bytes"
	"math"
	"testing"

	"github.com/parquet-go/parquet-go"
)

// The series reader is the first thing here that opens data files, so the
// fixtures gained two: nyu_rot v2.1's per-episode parquets for episodes 0 and
// 1, and v3.0's single 440-row file holding all fourteen. That pairing is what
// makes the cross-generation test below possible — the same recorded numbers,
// written by two different exporters into two different layouts.

// findSeries returns one feature's series from a page, or fails the test.
func findSeries(t *testing.T, p *SeriesPage, key string) FeatureSeries {
	t.Helper()
	for _, s := range p.Series {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("feature %q not in series (have %d)", key, len(p.Series))
	return FeatureSeries{}
}

func TestSeriesReadsOneEpisode(t *testing.T) {
	for _, tc := range []struct {
		name string
		root string
	}{
		{"v2.1", fixNyuV21},
		{"v3.0", fixNyuV30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ReadSeries(src(t, tc.root), SeriesRequest{Episode: 0})
			if err != nil {
				t.Fatal(err)
			}
			// Episode 0 is 40 frames at 5 fps — the ground truth the digest
			// tests already pin, reached here through a different file.
			if p.Length != 40 {
				t.Errorf("length = %d, want 40", p.Length)
			}
			if p.Points != 40 {
				t.Errorf("points = %d, want 40", p.Points)
			}
			if p.Stride != 1 || p.Downsampled {
				t.Errorf("stride = %d downsampled = %v, want 1/false", p.Stride, p.Downsampled)
			}
			if p.FPS != 5 {
				t.Errorf("fps = %v, want 5", p.FPS)
			}
			state := findSeries(t, p, "observation.state")
			if len(state.Channels) != 7 {
				t.Fatalf("observation.state channels = %d, want 7", len(state.Channels))
			}
			for i, ch := range state.Channels {
				if len(ch.Values) != 40 {
					t.Errorf("channel %d has %d points, want 40", i, len(ch.Values))
				}
			}
			// Timestamps are seconds from the episode start at 5 fps.
			if p.Timestamps[0] != 0 || math.Abs(p.Timestamps[1]-0.2) > 1e-6 {
				t.Errorf("timestamps start %v %v, want 0 and 0.2", p.Timestamps[0], p.Timestamps[1])
			}
		})
	}
}

func TestSeriesAgreeAcrossGenerations(t *testing.T) {
	// The strongest check available, and not an A==B tautology: v2.1 reads a
	// file that holds exactly one episode, v3.0 slices rows [40,70) out of a
	// 440-row file located through meta/episodes offsets. Different files,
	// different layouts, different code paths — the numbers must still match.
	for _, episode := range []int64{0, 1} {
		a, err := ReadSeries(src(t, fixNyuV21), SeriesRequest{Episode: episode})
		if err != nil {
			t.Fatal(err)
		}
		b, err := ReadSeries(src(t, fixNyuV30), SeriesRequest{Episode: episode})
		if err != nil {
			t.Fatal(err)
		}
		if a.Length != b.Length {
			t.Fatalf("episode %d: length %d vs %d", episode, a.Length, b.Length)
		}
		if len(a.Series) != len(b.Series) {
			t.Fatalf("episode %d: %d features vs %d", episode, len(a.Series), len(b.Series))
		}
		for i := range a.Timestamps {
			if math.Abs(a.Timestamps[i]-b.Timestamps[i]) > 1e-6 {
				t.Fatalf("episode %d ts[%d]: %v vs %v", episode, i, a.Timestamps[i], b.Timestamps[i])
			}
		}
		for _, sa := range a.Series {
			sb := findSeries(t, b, sa.Key)
			if len(sa.Channels) != len(sb.Channels) {
				t.Fatalf("episode %d %s: %d channels vs %d", episode, sa.Key, len(sa.Channels), len(sb.Channels))
			}
			for c := range sa.Channels {
				va, vb := sa.Channels[c].Values, sb.Channels[c].Values
				if len(va) != len(vb) {
					t.Fatalf("episode %d %s ch%d: %d points vs %d", episode, sa.Key, c, len(va), len(vb))
				}
				for i := range va {
					if va[i] != vb[i] {
						t.Fatalf("episode %d %s ch%d[%d]: %v vs %v", episode, sa.Key, c, i, va[i], vb[i])
					}
				}
			}
		}
	}
}

func TestSeriesDecimationIsRelativeToTheEpisodeStart(t *testing.T) {
	// The stride phase must be measured from the episode's FIRST row, not from
	// the file's. For v2.1 the two are the same thing (one file per episode),
	// so only the v3.0 slice can tell them apart: episode 1 begins at row 40,
	// and a stride computed on the absolute row number would return a
	// different set of frames. Comparing the two generations at the same
	// stride pins the phase without hardcoding which frames are "right".
	// MaxPoints is chosen so the stride does NOT divide the episode's start
	// row: episode 1 begins at row 40 and 11 points over 30 rows gives stride
	// 3, so an absolute-row phase would start the plot at row 42 instead of 40.
	// A stride of 4 or 5 would have passed either way — 40 is divisible by
	// both, and the bug would have hidden behind the arithmetic.
	for _, episode := range []int64{0, 1} { // the episodes the v2.1 fixture carries
		a, err := ReadSeries(src(t, fixNyuV21), SeriesRequest{Episode: episode, MaxPoints: 11})
		if err != nil {
			t.Fatal(err)
		}
		b, err := ReadSeries(src(t, fixNyuV30), SeriesRequest{Episode: episode, MaxPoints: 11})
		if err != nil {
			t.Fatal(err)
		}
		if a.Stride != b.Stride || a.Points != b.Points {
			t.Fatalf("episode %d: stride %d/%d points %d/%d", episode, a.Stride, b.Stride, a.Points, b.Points)
		}
		if a.Timestamps[0] != 0 || b.Timestamps[0] != 0 {
			t.Fatalf("episode %d: decimation dropped the first frame (%v / %v)", episode, a.Timestamps[0], b.Timestamps[0])
		}
		sa := findSeries(t, a, "action")
		sb := findSeries(t, b, "action")
		for i := range sa.Channels[0].Values {
			if sa.Channels[0].Values[i] != sb.Channels[0].Values[i] {
				t.Fatalf("episode %d decimated point %d: %v vs %v", episode, i, sa.Channels[0].Values[i], sb.Channels[0].Values[i])
			}
		}
	}
}

func TestSeriesTimestampsAreEpisodeRelative(t *testing.T) {
	// Episode 1 sits at rows [40,70) of the shared v3.0 file, where the file's
	// own clock reads 8.0s. LeRobot resets `timestamp` at every episode
	// boundary, so the series must start at 0 — a player scrubbing an episode
	// works in episode time, and an unrebased axis would put the cursor 8
	// seconds into a 6-second clip.
	p, err := ReadSeries(src(t, fixNyuV30), SeriesRequest{Episode: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.Timestamps[0] != 0 {
		t.Errorf("episode 1 starts at t=%v, want 0", p.Timestamps[0])
	}
	if p.Length != 30 {
		t.Errorf("episode 1 length = %d, want 30", p.Length)
	}
}

func TestSeriesLocatesTheRightSliceOfTheSharedFile(t *testing.T) {
	// A wrong offset still returns plausible-looking numbers, so pin the slice
	// against a fact only the correct rows carry: every episode's own frames
	// are labelled with its index in the data file.
	s := src(t, fixNyuV30)
	for _, episode := range []int64{0, 1, 5, 13} {
		p, err := ReadSeries(s, SeriesRequest{Episode: episode, Features: []string{"episode_index", "frame_index"}})
		if err != nil {
			t.Fatal(err)
		}
		idx := findSeries(t, p, "episode_index")
		for i, v := range idx.Channels[0].Values {
			if v != float64(episode) {
				t.Fatalf("episode %d row %d is labelled %v", episode, i, v)
			}
		}
		frame := findSeries(t, p, "frame_index")
		if frame.Channels[0].Values[0] != 0 {
			t.Errorf("episode %d starts at frame %v, want 0", episode, frame.Channels[0].Values[0])
		}
		want := nyuLengths[episode]
		if int64(len(frame.Channels[0].Values)) != want {
			t.Errorf("episode %d has %d rows, want %d", episode, len(frame.Channels[0].Values), want)
		}
	}
}

func TestSeriesDownsamplesByDecimation(t *testing.T) {
	full, err := ReadSeries(src(t, fixNyuV30), SeriesRequest{Episode: 0})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ReadSeries(src(t, fixNyuV30), SeriesRequest{Episode: 0, MaxPoints: 10})
	if err != nil {
		t.Fatal(err)
	}
	if p.Stride != 4 || !p.Downsampled {
		t.Fatalf("stride = %d downsampled = %v, want 4/true", p.Stride, p.Downsampled)
	}
	if p.Points != 10 {
		t.Errorf("points = %d, want 10", p.Points)
	}
	// Length keeps reporting the episode's real frame count: a UI must be able
	// to say "1000 of 40000 frames", which it cannot if the cap rewrites the
	// denominator too.
	if p.Length != 40 {
		t.Errorf("length = %d, want the undecimated 40", p.Length)
	}
	// Decimation, not averaging: every returned point must be a real sample.
	fullState := findSeries(t, full, "observation.state")
	thinState := findSeries(t, p, "observation.state")
	for i, v := range thinState.Channels[0].Values {
		if v != fullState.Channels[0].Values[i*4] {
			t.Fatalf("point %d = %v, want the frame-%d sample %v", i, v, i*4, fullState.Channels[0].Values[i*4])
		}
	}
	if math.Abs(p.Timestamps[1]-0.8) > 1e-6 {
		t.Errorf("decimated ts[1] = %v, want 0.8 (4 frames at 5 fps)", p.Timestamps[1])
	}
}

func TestSeriesChannelNamesComeFromInfo(t *testing.T) {
	// nyu_rot spells its names in the OBJECT form ({"motors": [...]}), the
	// shape that breaks a plain []string field. Reaching a channel label here
	// proves the info.json path carries them all the way to a plot legend.
	p, err := ReadSeries(src(t, fixNyuV21), SeriesRequest{Episode: 0})
	if err != nil {
		t.Fatal(err)
	}
	state := findSeries(t, p, "observation.state")
	for i, want := range []string{"motor_0", "motor_1", "motor_2", "motor_3", "motor_4", "motor_5", "motor_6"} {
		if state.Channels[i].Name != want {
			t.Errorf("channel %d name = %q, want %q", i, state.Channels[i].Name, want)
		}
	}
}

func TestSeriesDefaultsExcludeBookkeepingColumns(t *testing.T) {
	p, err := ReadSeries(src(t, fixNyuV30), SeriesRequest{Episode: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Series {
		if bookkeepingColumns[s.Key] {
			t.Errorf("default selection includes the row identifier %q", s.Key)
		}
	}
	// …but naming one still works: plotting frame_index against time is how
	// you spot a dropped frame.
	p2, err := ReadSeries(src(t, fixNyuV30), SeriesRequest{Episode: 0, Features: []string{"frame_index"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Series) != 1 || p2.Series[0].Key != "frame_index" {
		t.Fatalf("explicit selection returned %d series", len(p2.Series))
	}
}

func TestSeriesUnknownFeatureWarnsRatherThanFails(t *testing.T) {
	// A UI's saved plot selection outliving a dataset edit is normal, and
	// losing every other channel over it is not a proportionate response.
	p, err := ReadSeries(src(t, fixNyuV21), SeriesRequest{Episode: 0, Features: []string{"action", "no.such.feature"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Series) != 1 || p.Series[0].Key != "action" {
		t.Fatalf("series = %d entries, want just action", len(p.Series))
	}
	if len(p.Warnings) == 0 {
		t.Error("an unknown feature produced no warning")
	}
}

func TestSeriesBooleanFeatureBecomesZeroOne(t *testing.T) {
	// next.done is a BOOLEAN column; a plot needs a number, and the last frame
	// of an episode is where it flips.
	p, err := ReadSeries(src(t, fixNyuV21), SeriesRequest{Episode: 0, Features: []string{"next.done"}})
	if err != nil {
		t.Fatal(err)
	}
	vals := findSeries(t, p, "next.done").Channels[0].Values
	if len(vals) != 40 {
		t.Fatalf("next.done has %d points", len(vals))
	}
	if vals[0] != 0 {
		t.Errorf("first frame done = %v, want 0", vals[0])
	}
	if vals[len(vals)-1] != 1 {
		t.Errorf("last frame done = %v, want 1", vals[len(vals)-1])
	}
}

func TestSeriesVideoFeaturesAreNeverSeries(t *testing.T) {
	// observation.images.image is a video feature; it has no numeric column
	// and asking for it must not be answered with an empty plot.
	p, err := ReadSeries(src(t, fixNyuV21), SeriesRequest{Episode: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Series {
		if s.Key == "observation.images.image" {
			t.Fatal("a video feature was returned as a series")
		}
	}
	p2, err := ReadSeries(src(t, fixNyuV21), SeriesRequest{Episode: 0, Features: []string{"observation.images.image"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Series) != 0 || len(p2.Warnings) == 0 {
		t.Errorf("asking for a video feature gave %d series and %d warnings", len(p2.Series), len(p2.Warnings))
	}
}

func TestSeriesRefusesAnEpisodeTheDatasetDoesNotHave(t *testing.T) {
	for _, tc := range []struct {
		name string
		root string
	}{
		{"v2.1", fixNyuV21},
		{"v3.0", fixNyuV30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ReadSeries(src(t, tc.root), SeriesRequest{Episode: 999}); err == nil {
				t.Fatal("episode 999 was accepted")
			}
			if _, err := ReadSeries(src(t, tc.root), SeriesRequest{Episode: -1}); err == nil {
				t.Fatal("a negative episode was accepted")
			}
		})
	}
}

func TestSeriesPointBudgetIsCapped(t *testing.T) {
	p, err := ReadSeries(src(t, fixNyuV30), SeriesRequest{Episode: 0, MaxPoints: MaxSeriesPoints + 1})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Truncated {
		t.Error("a request over the point cap did not report being clamped")
	}
}

// ── the path template ────────────────────────────────────────────────────────

func TestResolveDataPathHandlesBothGenerations(t *testing.T) {
	// The generations do not merely differ in layout — they use DIFFERENT
	// placeholder names, which is why the template is carried verbatim from
	// info.json and expanded rather than hardcoded per generation.
	v21 := "data/chunk-{episode_chunk:03d}/episode_{episode_index:06d}.parquet"
	got, err := resolveDataPath(v21, map[string]int64{"episode_chunk": 0, "episode_index": 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := "data/chunk-000/episode_000001.parquet"; got != want {
		t.Errorf("v2.1 = %q, want %q", got, want)
	}
	v30 := "data/chunk-{chunk_index:03d}/file-{file_index:03d}.parquet"
	got, err = resolveDataPath(v30, map[string]int64{"chunk_index": 2, "file_index": 17})
	if err != nil {
		t.Fatal(err)
	}
	if want := "data/chunk-002/file-017.parquet"; got != want {
		t.Errorf("v3.0 = %q, want %q", got, want)
	}
	// A value wider than its pad is not truncated.
	got, err = resolveDataPath("f-{file_index:03d}", map[string]int64{"file_index": 12345})
	if err != nil || got != "f-12345" {
		t.Errorf("wide value = %q (err %v), want f-12345", got, err)
	}
	// A placeholder with no format spec still expands.
	got, err = resolveDataPath("{chunk_index}/x", map[string]int64{"chunk_index": 7})
	if err != nil || got != "7/x" {
		t.Errorf("bare placeholder = %q (err %v), want 7/x", got, err)
	}
}

func TestResolveDataPathRefusesWhatItCannotResolve(t *testing.T) {
	// An unknown placeholder must fail loudly. Substituting an empty string
	// would build a plausible path that simply misses, and a miss reports as
	// "this episode has no data file" — a wrong answer that looks like a fact
	// about the dataset.
	if _, err := resolveDataPath("data/{mystery:03d}.parquet", map[string]int64{"chunk_index": 0}); err == nil {
		t.Error("an unknown placeholder was accepted")
	}
	if _, err := resolveDataPath("data/{chunk_index:03d", map[string]int64{"chunk_index": 0}); err == nil {
		t.Error("an unclosed placeholder was accepted")
	}
	if _, err := resolveDataPath("", nil); err == nil {
		t.Error("an empty template was accepted")
	}
	// The template is dataset-supplied, so it is untrusted input on a path.
	for _, bad := range []string{"/etc/{chunk_index}", "../../{chunk_index}", "data/../../{chunk_index}"} {
		if _, err := resolveDataPath(bad, map[string]int64{"chunk_index": 0}); err == nil {
			t.Errorf("escaping template %q was accepted", bad)
		}
	}
}

func TestPadInt(t *testing.T) {
	for _, tc := range []struct {
		n     int64
		width int
		want  string
	}{
		{0, 3, "000"},
		{7, 3, "007"},
		{1234, 3, "1234"},
		{0, 0, "0"},
		{-1, 3, "-001"},
	} {
		if got := padInt(tc.n, tc.width); got != tc.want {
			t.Errorf("padInt(%d, %d) = %q, want %q", tc.n, tc.width, got, tc.want)
		}
	}
}

// ── row-group boundaries ─────────────────────────────────────────────────────

// synthRow is a two-column stand-in for a data file: the frame number IS the
// value, so any row the reader picks up identifies itself.
type synthRow struct {
	Frame     int64   `parquet:"frame_index"`
	Timestamp float64 `parquet:"timestamp"`
}

// writeRowGroups builds a parquet file with `groups` row groups of
// `perGroup` rows each.
//
// Synthetic, unlike every other fixture here, and for a reason the real ones
// cannot cover: both pinned datasets are small enough to fit in a SINGLE row
// group, so the skip-whole-groups path — the one a 100 MB v3.0 data file takes
// on every read — never executes against them. Same blind spot the episodes
// walk had, and the same fix.
func writeRowGroups(t *testing.T, perGroup, groups int) *parquet.File {
	t.Helper()
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[synthRow](&buf)
	for g := 0; g < groups; g++ {
		rows := make([]synthRow, perGroup)
		for i := range rows {
			n := int64(g*perGroup + i)
			rows[i] = synthRow{Frame: n, Timestamp: float64(n) / 10}
		}
		if _, err := w.Write(rows); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	pf, err := parquet.OpenFile(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.RowGroups()) != groups {
		t.Fatalf("built %d row groups, want %d", len(pf.RowGroups()), groups)
	}
	return pf
}

func TestReadNumericColumnAcrossRowGroups(t *testing.T) {
	pf := writeRowGroups(t, 10, 4) // rows 0..39 in four groups
	col := columnIndex(pf)["frame_index"]

	for _, tc := range []struct {
		name     string
		from, to int64
		stride   int
		want     []float64
	}{
		{"inside one group", 2, 6, 1, []float64{2, 3, 4, 5}},
		{"a whole group after skipping three", 30, 40, 1, []float64{30, 31, 32, 33, 34, 35, 36, 37, 38, 39}},
		{"spanning a boundary", 8, 13, 1, []float64{8, 9, 10, 11, 12}},
		{"spanning three groups", 5, 25, 5, []float64{5, 10, 15, 20}},
		{"starting mid-group with a stride", 13, 21, 3, []float64{13, 16, 19}},
		{"the very last row", 39, 40, 1, []float64{39}},
		{"an empty range", 7, 7, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, capped, err := readNumericColumn(pf, col, tc.from, tc.to, tc.stride, MaxSeriesChannels)
			if err != nil {
				t.Fatal(err)
			}
			if capped {
				t.Error("a two-column file reported a channel cap")
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("empty range returned %v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("scalar column produced %d channels", len(got))
			}
			if len(got[0]) != len(tc.want) {
				t.Fatalf("got %v, want %v", got[0], tc.want)
			}
			for i := range tc.want {
				if got[0][i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got[0], tc.want)
				}
			}
		})
	}
}
