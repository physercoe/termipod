package trackio

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestParseURI(t *testing.T) {
	cases := []struct {
		in      string
		project string
		run     string
		wantErr bool
	}{
		{"trackio://nano-ablation/run-1", "nano-ablation", "run-1", false},
		{"trackio://p/r", "p", "r", false},
		{"trackio://p/", "", "", true},
		{"trackio://", "", "", true},
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
			if u.Project != c.project || u.RunName != c.run {
				t.Errorf("got {%q, %q}, want {%q, %q}", u.Project, u.RunName, c.project, c.run)
			}
		})
	}
}

// seedTrackioDB builds a minimal trackio SQLite file with the documented
// `metrics` schema and inserts a few rows. Returns the path.
func seedTrackioDB(t *testing.T, dir, project string, rows []metricRow) string {
	t.Helper()
	path := filepath.Join(dir, project+".db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT,
			run_name TEXT,
			step INTEGER,
			metrics TEXT
		)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`
			INSERT INTO metrics (timestamp, run_name, step, metrics)
			VALUES (?, ?, ?, ?)`,
			r.ts, r.runName, r.step, r.metricsJSON); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return path
}

type metricRow struct {
	ts          string
	runName     string
	step        int64
	metricsJSON string
}

func TestReadRun_ReturnsSeriesByMetric(t *testing.T) {
	dir := t.TempDir()
	path := seedTrackioDB(t, dir, "nano", []metricRow{
		{"t0", "run-a", 0, `{"loss": 2.5, "acc": 0.1}`},
		{"t1", "run-a", 50, `{"loss": 1.8, "acc": 0.4}`},
		{"t2", "run-a", 100, `{"loss": 1.23, "acc": 0.7}`},
		{"t3", "run-b", 0, `{"loss": 9.9}`}, // different run, must be excluded
	})

	series, err := ReadRun(context.Background(), path, "run-a")
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
}

func TestReadRun_SkipsNonNumericMetrics(t *testing.T) {
	dir := t.TempDir()
	path := seedTrackioDB(t, dir, "nano", []metricRow{
		{"t0", "run-a", 0, `{"loss": 2.5, "note": "started", "shape": [1, 2]}`},
		{"t1", "run-a", 10, `{"loss": 1.0, "done": true}`},
	})

	series, err := ReadRun(context.Background(), path, "run-a")
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
	// bool should convert to 1.
	done, ok := series["done"]
	if !ok || len(done) != 1 || done[0].Value != 1 {
		t.Errorf("done bool series = %+v, want [{10, 1}]", done)
	}
}

func TestReadRun_MissingDBIsEmpty(t *testing.T) {
	series, err := ReadRun(context.Background(),
		filepath.Join(t.TempDir(), "nope.db"), "run-a")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("missing db gave %+v, want empty", series)
	}
}

func TestReadRun_DedupsDuplicateSteps(t *testing.T) {
	dir := t.TempDir()
	path := seedTrackioDB(t, dir, "nano", []metricRow{
		{"t0", "run-a", 0, `{"loss": 2.5}`},
		{"t1", "run-a", 0, `{"loss": 2.0}`}, // same step logged twice
		{"t2", "run-a", 10, `{"loss": 1.0}`},
	})
	series, err := ReadRun(context.Background(), path, "run-a")
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

func TestDefaultDir_UsesEnvWhenSet(t *testing.T) {
	t.Setenv("TRACKIO_DIR", "/tmp/mytrackio")
	if d := DefaultDir(); d != "/tmp/mytrackio" {
		t.Errorf("DefaultDir = %q, want /tmp/mytrackio", d)
	}
}

func TestProjectDBPath(t *testing.T) {
	got := ProjectDBPath("/root", "nano-ablation")
	want := "/root/nano-ablation.db"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
