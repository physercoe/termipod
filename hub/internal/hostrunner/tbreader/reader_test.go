package tbreader

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestParseURI(t *testing.T) {
	cases := []struct {
		in      string
		runPath string
		wantErr bool
	}{
		{"tb://run-1", "run-1", false},
		{"tb://ablation/lr1e-4/seed0", "ablation/lr1e-4/seed0", false},
		{"tb://", "", true},
		{"tb:///", "", true},
		{"trackio://p/r", "", true},
		{"not a uri", "", true},
		{"tb://../escape", "", true},
		{"tb://ok/../escape", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			u, err := ParseURI(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if err != nil {
				return
			}
			if u.RunPath != c.runPath {
				t.Errorf("got %q, want %q", u.RunPath, c.runPath)
			}
		})
	}
}

// writeTFEventsFile builds a tfevents file on disk populated with a
// leading file_version record and one Event per supplied sample.
// `samples` is a slice of {step, tag, value} triples; each becomes one
// Event with a single-value Summary.
func writeTFEventsFile(t *testing.T, dir, name string, samples []sample) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var buf bytes.Buffer

	// file_version
	var fv []byte
	fv = append(fv, pbFixed64(1, math.Float64bits(0.0))...)
	fv = append(fv, pbLenDelim(3, []byte("brain.Event:2"))...)
	if err := WriteRecord(&buf, fv); err != nil {
		t.Fatalf("WriteRecord file_version: %v", err)
	}

	for _, s := range samples {
		ev := buildEvent(s.step, 1.0,
			buildSummary(buildValueSimple(s.tag, s.value)))
		if err := WriteRecord(&buf, ev); err != nil {
			t.Fatalf("WriteRecord: %v", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

type sample struct {
	step  int64
	tag   string
	value float32
}

func TestReadRun_ReturnsSeriesByTag(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTFEventsFile(t, runDir, "events.out.tfevents.1000.host.1.v2", []sample{
		{0, "loss", 2.5},
		{50, "loss", 1.8},
		{100, "loss", 1.23},
		{0, "acc", 0.1},
		{50, "acc", 0.4},
		{100, "acc", 0.7},
	})

	series, err := ReadRun(root, "run-a")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("metrics=%d, want 2: %+v", len(series), series)
	}
	loss := series["loss"]
	if len(loss) != 3 || loss[0].Step != 0 || loss[2].Step != 100 {
		t.Errorf("loss=%+v, want 3 points 0..100", loss)
	}
	if math.Abs(loss[2].Value-float64(float32(1.23))) > 1e-6 {
		t.Errorf("loss final=%v, want ~1.23", loss[2].Value)
	}
}

func TestReadRun_MergesMultipleFilesLexOrder(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two files; the second one's timestamp makes it sort after the
	// first, which matches how TB rotates logs.
	writeTFEventsFile(t, runDir, "events.out.tfevents.1000.host.1.v2", []sample{
		{0, "loss", 2.5},
		{50, "loss", 1.8},
	})
	writeTFEventsFile(t, runDir, "events.out.tfevents.2000.host.1.v2", []sample{
		{100, "loss", 1.23},
		{150, "loss", 1.0},
	})

	series, err := ReadRun(root, "run-a")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	loss := series["loss"]
	if len(loss) != 4 {
		t.Fatalf("loss=%+v, want 4 points across two files", loss)
	}
	if loss[0].Step != 0 || loss[3].Step != 150 {
		t.Errorf("loss endpoints wrong: %+v", loss)
	}
}

func TestReadRun_DedupsDuplicateSteps(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTFEventsFile(t, runDir, "events.out.tfevents.1.host.1.v2", []sample{
		{0, "loss", 2.5},
		{0, "loss", 2.0}, // same step, later write — must win
		{10, "loss", 1.0},
	})

	series, err := ReadRun(root, "run-a")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	loss := series["loss"]
	if len(loss) != 2 {
		t.Fatalf("loss=%+v, want 2 (step 0 deduped)", loss)
	}
	if math.Abs(loss[0].Value-float64(float32(2.0))) > 1e-6 {
		t.Errorf("dedup winner=%v, want 2.0 (last write)", loss[0].Value)
	}
}

func TestReadRun_MissingRunDirIsEmpty(t *testing.T) {
	root := t.TempDir()
	series, err := ReadRun(root, "nope")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("expected empty series, got %+v", series)
	}
}

func TestReadRun_IgnoresNonEventsFiles(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stray README or unrelated file must not blow up the reader.
	if err := os.WriteFile(filepath.Join(runDir, "README.md"),
		[]byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTFEventsFile(t, runDir, "events.out.tfevents.1.host.1.v2", []sample{
		{0, "loss", 1.0},
	})
	series, err := ReadRun(root, "run-a")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if len(series["loss"]) != 1 {
		t.Errorf("loss=%+v, want one sample", series["loss"])
	}
}

func TestReadRun_NestedRunPath(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "ablation", "lr1e-4", "seed0")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTFEventsFile(t, runDir, "events.out.tfevents.1.host.1.v2", []sample{
		{0, "loss", 2.0},
	})
	series, err := ReadRun(root, "ablation/lr1e-4/seed0")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if len(series["loss"]) != 1 {
		t.Errorf("loss=%+v, want one sample", series["loss"])
	}
}

func TestDefaultDir_UsesEnv(t *testing.T) {
	t.Setenv("TENSORBOARD_LOGDIR", "/tmp/mytb")
	if d := DefaultDir(); d != "/tmp/mytb" {
		t.Errorf("DefaultDir=%q, want /tmp/mytb", d)
	}
}

func TestDefaultDir_UnsetIsEmpty(t *testing.T) {
	t.Setenv("TENSORBOARD_LOGDIR", "")
	if d := DefaultDir(); d != "" {
		t.Errorf("DefaultDir=%q, want empty", d)
	}
}
