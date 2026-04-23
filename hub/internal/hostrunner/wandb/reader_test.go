package wandb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseURI(t *testing.T) {
	cases := []struct {
		in      string
		project string
		run     string
		wantErr bool
	}{
		{"wandb://nano-ablation/run-20260423_123456-abc123", "nano-ablation", "run-20260423_123456-abc123", false},
		{"wandb://p/r", "p", "r", false},
		{"wandb://p/", "", "", true},
		{"wandb://", "", "", true},
		{"trackio://p/r", "", "", true},
		{"http://p/r", "", "", true},
		{"not a uri at all", "", "", true},
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
			if u.Project != c.project || u.RunDir != c.run {
				t.Errorf("got {%q, %q}, want {%q, %q}", u.Project, u.RunDir, c.project, c.run)
			}
		})
	}
}

// seedWandbRun builds a minimal wandb offline-run layout under root with
// one history file at <root>/<runDir>/files/wandb-history.jsonl, writing
// the supplied rows one JSON object per line. Returns the history path.
func seedWandbRun(t *testing.T, root, runDir string, rows []map[string]any) string {
	t.Helper()
	filesDir := filepath.Join(root, runDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(filesDir, "wandb-history.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	return path
}

func TestReadRun_ReturnsSeriesByMetric(t *testing.T) {
	root := t.TempDir()
	runDir := "run-20260423_123456-abc123"
	path := seedWandbRun(t, root, runDir, []map[string]any{
		{"_step": 0, "_timestamp": 1700000000, "loss": 2.5, "acc": 0.1},
		{"_step": 50, "_timestamp": 1700000050, "loss": 1.8, "acc": 0.4},
		{"_step": 100, "_timestamp": 1700000100, "loss": 1.23, "acc": 0.7},
	})
	if got := RunHistoryPath(root, runDir); got != path {
		t.Fatalf("RunHistoryPath = %q, want %q", got, path)
	}

	series, err := ReadRun(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("metric count = %d, want 2 (loss, acc): %+v", len(series), series)
	}
	loss := series["loss"]
	if len(loss) != 3 || loss[0].Step != 0 || loss[2].Step != 100 {
		t.Errorf("loss = %+v, want 3 points 0→100", loss)
	}
	if loss[2].Value != 1.23 {
		t.Errorf("loss final value = %v, want 1.23", loss[2].Value)
	}
	if _, ok := series["_timestamp"]; ok {
		t.Errorf("metadata key _timestamp leaked into series")
	}
	if _, ok := series["_step"]; ok {
		t.Errorf("metadata key _step leaked into series")
	}
}

func TestReadRun_SkipsNonNumericMetrics(t *testing.T) {
	root := t.TempDir()
	runDir := "run-1"
	path := seedWandbRun(t, root, runDir, []map[string]any{
		{"_step": 0, "loss": 2.5, "note": "started", "shape": []int{1, 2},
			"hist": map[string]any{"bins": []int{1, 2, 3}}},
		{"_step": 10, "loss": 1.0, "done": true, "null_metric": nil},
	})

	series, err := ReadRun(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if _, ok := series["loss"]; !ok {
		t.Errorf("expected loss series")
	}
	if _, ok := series["note"]; ok {
		t.Errorf("string metric leaked: %+v", series["note"])
	}
	if _, ok := series["shape"]; ok {
		t.Errorf("array metric leaked: %+v", series["shape"])
	}
	if _, ok := series["hist"]; ok {
		t.Errorf("nested object leaked: %+v", series["hist"])
	}
	if _, ok := series["null_metric"]; ok {
		t.Errorf("null metric leaked")
	}
	// bool should convert to 1.
	done, ok := series["done"]
	if !ok || len(done) != 1 || done[0].Value != 1 {
		t.Errorf("done bool series = %+v, want [{10, 1}]", done)
	}
}

func TestReadRun_SkipsRowsWithoutStep(t *testing.T) {
	root := t.TempDir()
	runDir := "run-1"
	path := seedWandbRun(t, root, runDir, []map[string]any{
		{"loss": 9.9},                       // no _step — skip
		{"_step": 0, "loss": 2.5},           // keep
		{"_step": "bad", "loss": 1.0},       // non-numeric _step — skip
		{"_step": 1, "loss": 1.5},           // keep
	})
	series, err := ReadRun(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	loss := series["loss"]
	if len(loss) != 2 {
		t.Fatalf("loss = %+v, want 2 points", loss)
	}
	if loss[0].Step != 0 || loss[1].Step != 1 {
		t.Errorf("loss steps = %+v, want [0, 1]", loss)
	}
}

func TestReadRun_MissingHistoryIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "files", "wandb-history.jsonl")
	series, err := ReadRun(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("missing history gave %+v, want empty", series)
	}
}

func TestReadRun_EmptyHistoryIsEmpty(t *testing.T) {
	root := t.TempDir()
	runDir := "run-1"
	path := seedWandbRun(t, root, runDir, nil)
	series, err := ReadRun(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("empty history gave %+v, want empty", series)
	}
}

func TestReadRun_DedupsDuplicateSteps(t *testing.T) {
	root := t.TempDir()
	runDir := "run-1"
	path := seedWandbRun(t, root, runDir, []map[string]any{
		{"_step": 0, "loss": 2.5},
		{"_step": 0, "loss": 2.0}, // same step logged twice (resume)
		{"_step": 10, "loss": 1.0},
	})
	series, err := ReadRun(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	loss := series["loss"]
	if len(loss) != 2 {
		t.Fatalf("loss = %+v, want 2 points (step 0 deduped)", loss)
	}
	if loss[0].Value != 2.0 {
		t.Errorf("loss[0] = %v, want 2.0 (last write wins)", loss[0].Value)
	}
}

func TestReadRun_SkipsCorruptLines(t *testing.T) {
	root := t.TempDir()
	runDir := "run-1"
	// Build history manually with a deliberately broken middle line.
	filesDir := filepath.Join(root, runDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(filesDir, "wandb-history.jsonl")
	body := `{"_step": 0, "loss": 2.5}
not valid json
{"_step": 1, "loss": 1.0}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	series, err := ReadRun(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	loss := series["loss"]
	if len(loss) != 2 {
		t.Fatalf("loss = %+v, want 2 points (corrupt line skipped)", loss)
	}
}

func TestDefaultDir_UsesEnvWhenSet(t *testing.T) {
	t.Setenv("WANDB_DIR", "/tmp/mywandb")
	if d := DefaultDir(); d != "/tmp/mywandb" {
		t.Errorf("DefaultDir = %q, want /tmp/mywandb", d)
	}
}

func TestDefaultDir_UnsetReturnsEmpty(t *testing.T) {
	t.Setenv("WANDB_DIR", "")
	if d := DefaultDir(); d != "" {
		t.Errorf("DefaultDir = %q, want empty (no cwd fallback)", d)
	}
}

func TestRunHistoryPath(t *testing.T) {
	got := RunHistoryPath("/root", "run-abc")
	want := filepath.Join("/root", "run-abc", "files", "wandb-history.jsonl")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
