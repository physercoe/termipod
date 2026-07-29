package datasetmeta

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixture roots. These are real LeRobot meta/ trees — see
// testdata/fetch-fixtures.sh for what each one is here to prove.
const (
	fixNyuV21  = "testdata/lerobot/nyu_rot_dataset/v2.1"
	fixNyuV30  = "testdata/lerobot/nyu_rot_dataset/v3.0"
	fixSvlaV30 = "testdata/lerobot/svla_so101_pickplace/v3.0"
	fixTaccap  = "testdata/taccap-g1/v3.0"
)

func src(t *testing.T, root string) *DirSource {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "meta")); err != nil {
		t.Fatalf("fixture %s missing (run testdata/fetch-fixtures.sh): %v", root, err)
	}
	return NewDirSource(root)
}

// writeRoot builds a throwaway dataset root. Used only for negative cases —
// anything asserting how a real file parses uses the pinned fixtures, because
// a hand-written info.json only ever agrees with what its author assumed.
func writeRoot(t *testing.T, files map[string]string) *DirSource {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return NewDirSource(dir)
}

func TestSniffIdentifiesBothGenerations(t *testing.T) {
	for _, tc := range []struct {
		root string
		want Format
		ver  string
	}{
		{fixNyuV21, FormatLeRobotV21, "v2.1"},
		{fixNyuV30, FormatLeRobotV30, "v3.0"},
		{fixSvlaV30, FormatLeRobotV30, "v3.0"},
	} {
		got, info, err := Sniff(src(t, tc.root))
		if err != nil {
			t.Fatalf("%s: %v", tc.root, err)
		}
		if got != tc.want {
			t.Errorf("%s: format = %q, want %q", tc.root, got, tc.want)
		}
		if info.CodebaseVersion != tc.ver {
			t.Errorf("%s: codebase_version = %q, want %q", tc.root, info.CodebaseVersion, tc.ver)
		}
	}
}

// An unknown generation must be refused by name, not parsed hopefully. A
// best-effort parse of a layout nobody has read would emit confident numbers
// that are simply wrong, which is worse than an honest "unsupported".
func TestUnsupportedCodebaseVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		info string
		want string
	}{
		{"future major", `{"codebase_version":"v4.0"}`, "v4.0"},
		{"unreleased minor", `{"codebase_version":"v3.1"}`, "v3.1"},
		{"v2.0 predates the supported pair", `{"codebase_version":"v2.0"}`, "v2.0"},
		{"absent", `{"robot_type":"so100"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Sniff(writeRoot(t, map[string]string{"meta/info.json": tc.info}))
			var ufe *UnsupportedFormatError
			if !errors.As(err, &ufe) {
				t.Fatalf("err = %v, want *UnsupportedFormatError", err)
			}
			if ufe.CodebaseVersion != tc.want {
				t.Errorf("CodebaseVersion = %q, want %q", ufe.CodebaseVersion, tc.want)
			}
			// The version must survive into the message: the UI shows it.
			if tc.want != "" && !strings.Contains(ufe.Error(), tc.want) {
				t.Errorf("message %q does not name the version", ufe.Error())
			}
		})
	}
}

// `names` is three different shapes in files LeRobot actually ships. All three
// are present in the pinned fixtures; a []string field would have failed the
// whole parse on the object form.
func TestFeatureNamesShapes(t *testing.T) {
	_, info, err := Sniff(src(t, fixNyuV21))
	if err != nil {
		t.Fatal(err)
	}
	// object form: {"motors": ["motor_0", ...]}
	action := info.Features["action"]
	if len(action.Names) != 7 {
		t.Fatalf("action names = %v, want 7 flattened motor labels", action.Names)
	}
	if action.Names[0] != "motor_0" || action.Names[6] != "motor_6" {
		t.Errorf("action names = %v, want motor_0..motor_6", action.Names)
	}
	// list form
	img := info.Features["observation.images.image"]
	if got := strings.Join(img.Names, ","); got != "height,width,channel" {
		t.Errorf("image names = %q", got)
	}
	// null form — and the dimension must still come through, from shape.
	ts := info.Features["timestamp"]
	if ts.Names != nil {
		t.Errorf("timestamp names = %v, want nil", ts.Names)
	}
	if len(ts.Shape) != 1 || ts.Shape[0] != 1 {
		t.Errorf("timestamp shape = %v, want [1]", ts.Shape)
	}
	// The reason this matters: dimensionality is read from shape, never from
	// len(names), which would be 0 for every scalar feature in this file.
	if len(action.Shape) != 1 || action.Shape[0] != 7 {
		t.Errorf("action shape = %v, want [7]", action.Shape)
	}
}

func TestUnparsableNamesDoNotFailTheFile(t *testing.T) {
	s := writeRoot(t, map[string]string{
		"meta/info.json": `{"codebase_version":"v2.1","fps":10,
			"features":{"weird":{"dtype":"float32","shape":[2],"names":[1,2]}}}`,
	})
	_, info, err := Sniff(s)
	if err != nil {
		t.Fatalf("a cosmetic names field must not fail the parse: %v", err)
	}
	if info.Features["weird"].Names != nil {
		t.Errorf("names = %v, want nil", info.Features["weird"].Names)
	}
}

// Multi-camera extraction. nyu_rot has one camera, so on its own it cannot
// catch a reader that returns only the first video stream.
//
// The svla fixture is meta/info.json alone, so this drives info.split()
// directly rather than ReadDigest: a dataset with no episode index is
// incomplete, and both generations refuse it (TestEpisodeIndexIsRequired).
func TestVideoStreamsMultiCamera(t *testing.T) {
	_, info, err := Sniff(src(t, fixSvlaV30))
	if err != nil {
		t.Fatal(err)
	}
	streams, features := info.split()
	if len(streams) != 2 {
		t.Fatalf("video streams = %d, want 2: %+v", len(streams), streams)
	}
	// Sorted by key, so .side precedes .up.
	if streams[0].Key != "observation.images.side" || streams[1].Key != "observation.images.up" {
		t.Fatalf("keys = %q, %q", streams[0].Key, streams[1].Key)
	}
	for _, s := range streams {
		if s.Width != 640 || s.Height != 480 {
			t.Errorf("%s geometry = %dx%d, want 640x480", s.Key, s.Width, s.Height)
		}
		if s.Codec != "av1" {
			t.Errorf("%s codec = %q, want av1", s.Key, s.Codec)
		}
		if s.FPS != 30 {
			t.Errorf("%s fps = %v, want 30", s.Key, s.FPS)
		}
	}
	if streams[1].Name != "up" {
		t.Errorf("pane label = %q, want %q", streams[1].Name, "up")
	}
	// Video features must not leak into Features.
	for _, f := range features {
		if strings.Contains(f.Key, "images") {
			t.Errorf("video feature %q leaked into Features", f.Key)
		}
	}
	// The 6-DoF action space keeps its named joints.
	var action *Feature
	for i := range features {
		if features[i].Key == "action" {
			action = &features[i]
		}
	}
	if action == nil || len(action.Names) != 6 || action.Names[0] != "shoulder_pan.pos" {
		t.Fatalf("action feature = %+v", action)
	}
}

func TestEnvRefDerivation(t *testing.T) {
	// robot_type "unknown" is not an embodiment; recording it would seed the
	// future registry with a row that has to be un-picked.
	d, err := ReadDigest(src(t, fixNyuV30))
	if err != nil {
		t.Fatal(err)
	}
	if d.RobotType != "unknown" {
		t.Fatalf("fixture robot_type = %q, expected the literal \"unknown\"", d.RobotType)
	}
	if d.EnvRef != "" {
		t.Errorf("env_ref = %q, want empty for robot_type=unknown", d.EnvRef)
	}
	_, info, err := Sniff(src(t, fixSvlaV30))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.envRef(); got != "lerobot:so100_follower" {
		t.Errorf("env_ref = %q, want lerobot:so100_follower", got)
	}
}

// The episode index is the dataset's spine: without it there is no episodes
// table and no length histogram, so a digest built anyway would be a summary
// of a dataset nobody can open. Both generations refuse, and they must refuse
// alike — an asymmetry here would mean a v3.0 root silently degrades where the
// same damage to a v2.1 root errors.
func TestEpisodeIndexIsRequired(t *testing.T) {
	v21 := writeRoot(t, map[string]string{
		"meta/info.json": `{"codebase_version":"v2.1","fps":10,"total_episodes":1}`,
	})
	if _, err := ReadDigest(v21); err == nil {
		t.Error("v2.1: want an error when meta/episodes.jsonl is absent")
	}
	v30 := writeRoot(t, map[string]string{
		"meta/info.json": `{"codebase_version":"v3.0","fps":10,"total_episodes":1}`,
	})
	if _, err := ReadDigest(v30); err == nil {
		t.Error("v3.0: want an error when meta/episodes/ is absent")
	}
}

func TestPathTraversalRefused(t *testing.T) {
	s := NewDirSource(t.TempDir())
	for _, bad := range []string{
		"../etc/passwd",
		"meta/../../escape",
		"meta/./../../escape",
	} {
		if _, err := s.Open(bad); err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Errorf("Open(%q) err = %v, want an escape refusal", bad, err)
		}
		if _, _, err := s.OpenReaderAt(bad); err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Errorf("OpenReaderAt(%q) err = %v, want an escape refusal", bad, err)
		}
		if _, err := s.List(bad); err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Errorf("List(%q) err = %v, want an escape refusal", bad, err)
		}
	}
	// A NUL byte would truncate the path at the syscall boundary.
	if _, err := s.Open("meta/info.json\x00.txt"); err == nil {
		t.Error("a NUL byte in a path must be refused")
	}
}

func TestReadCappedRefusesRatherThanTruncates(t *testing.T) {
	s := writeRoot(t, map[string]string{"meta/big.json": strings.Repeat("x", 100)})
	if _, err := readCapped(s, "meta/big.json", 99); err == nil {
		t.Fatal("want a cap error")
	}
	// Exactly at the cap is fine — the reader must not be off by one.
	b, err := readCapped(s, "meta/big.json", 100)
	if err != nil || len(b) != 100 {
		t.Fatalf("at-cap read: %d bytes, err %v", len(b), err)
	}
}

func TestEpisodePageLimitIsClampedAndDisclosed(t *testing.T) {
	s := src(t, fixNyuV21)
	p, err := ReadEpisodes(s, EpisodeRequest{Limit: MaxEpisodePageLimit + 500})
	if err != nil {
		t.Fatal(err)
	}
	if p.Limit != MaxEpisodePageLimit {
		t.Errorf("limit = %d, want clamp to %d", p.Limit, MaxEpisodePageLimit)
	}
	if !p.Truncated {
		t.Error("a clamped limit must be disclosed, not applied silently")
	}
	// An unclamped request must NOT claim truncation.
	p2, err := ReadEpisodes(s, EpisodeRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if p2.Truncated {
		t.Error("Truncated set on a request that was honoured in full")
	}
	if p2.Limit != 5 || len(p2.Episodes) != 5 {
		t.Errorf("limit = %d, episodes = %d, want 5/5", p2.Limit, len(p2.Episodes))
	}
	if p2.Total != 14 {
		t.Errorf("total = %d, want 14 (from info.json, not the page)", p2.Total)
	}
}

func TestLengthHistogram(t *testing.T) {
	t.Run("uniform lengths collapse to one closed bucket", func(t *testing.T) {
		h := buildLengthHistogram([]int64{30, 30, 30})
		if len(h) != 1 || h[0].From != 30 || h[0].To != 30 || h[0].Count != 3 {
			t.Fatalf("h = %+v", h)
		}
	})
	t.Run("every episode lands in exactly one bucket", func(t *testing.T) {
		in := []int64{1, 2, 3, 10, 50, 99, 100}
		h := buildLengthHistogram(in)
		var total int64
		for _, b := range h {
			total += b.Count
		}
		if total != int64(len(in)) {
			t.Fatalf("counted %d of %d episodes: %+v", total, len(in), h)
		}
		if len(h) > MaxLengthHistogramBuckets {
			t.Fatalf("%d buckets exceeds the cap", len(h))
		}
	})
	t.Run("the longest episode is inside the last bucket", func(t *testing.T) {
		h := buildLengthHistogram([]int64{1, 1000})
		last := h[len(h)-1]
		if last.To != 1000 || last.Count == 0 {
			t.Fatalf("last bucket = %+v, want the max inside it", last)
		}
	})
	t.Run("empty input has no histogram", func(t *testing.T) {
		if h := buildLengthHistogram(nil); h != nil {
			t.Fatalf("h = %+v", h)
		}
	})
}

func TestFingerprintStatsWithoutParsing(t *testing.T) {
	fp, err := ReadFingerprint(src(t, fixNyuV30))
	if err != nil {
		t.Fatal(err)
	}
	// info.json + stats.json + tasks.parquet + episodes/chunk-000/file-000.parquet
	if fp.Files != 4 {
		t.Errorf("files = %d, want 4 (the nested episodes shard must be walked)", fp.Files)
	}
	if fp.Bytes <= 0 || fp.MaxModTime == "" {
		t.Errorf("fingerprint = %+v", fp)
	}
	// It must change when a file does — that is the whole point of the token.
	fp2, _ := ReadFingerprint(src(t, fixNyuV21))
	if fp2.Bytes == fp.Bytes {
		t.Error("two different meta trees produced the same byte total")
	}
}
