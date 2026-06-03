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

// seedSiblingTables creates the configs / system_metrics / alerts tables on an
// existing project DB so the extras readers have something to read.
func seedSiblingTables(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	stmts := []string{
		`CREATE TABLE configs (id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT, run_name TEXT, config TEXT, created_at TEXT)`,
		`CREATE TABLE system_metrics (id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT, timestamp TEXT, run_name TEXT, metrics TEXT)`,
		`CREATE TABLE alerts (id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT, timestamp TEXT, run_name TEXT, title TEXT, text TEXT,
			level TEXT, step INTEGER, alert_id TEXT)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	return db
}

func TestReadConfig(t *testing.T) {
	dir := t.TempDir()
	path := seedTrackioDB(t, dir, "nano", nil)
	db := seedSiblingTables(t, path)
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO configs (run_id, run_name, config, created_at) VALUES (?,?,?,?)`,
		"r1", "run-a", `{"lr": 0.001, "batch": 64, "model": "nanoGPT"}`, "t0"); err != nil {
		t.Fatal(err)
	}

	cfg, err := ReadConfig(context.Background(), path, "run-a")
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if cfg["lr"] != 0.001 || cfg["model"] != "nanoGPT" {
		t.Errorf("config = %+v, want lr+model", cfg)
	}

	// Missing run → nil, no error.
	empty, err := ReadConfig(context.Background(), path, "no-such-run")
	if err != nil || empty != nil {
		t.Errorf("missing run: got (%+v, %v), want (nil, nil)", empty, err)
	}
}

func TestReadConfig_MissingTableIsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := seedTrackioDB(t, dir, "nano", nil) // metrics table only, no configs
	cfg, err := ReadConfig(context.Background(), path, "run-a")
	if err != nil || cfg != nil {
		t.Errorf("no configs table: got (%+v, %v), want (nil, nil)", cfg, err)
	}
}

func TestReadSystemMetrics(t *testing.T) {
	dir := t.TempDir()
	path := seedTrackioDB(t, dir, "nano", nil)
	db := seedSiblingTables(t, path)
	defer db.Close()
	rows := []struct{ ts, run, m string }{
		{"t0", "run-a", `{"gpu.0.util": 10, "cpu": 5}`},
		{"t1", "run-a", `{"gpu.0.util": 80, "cpu": 7}`},
		{"t2", "run-b", `{"gpu.0.util": 99}`}, // other run, excluded
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO system_metrics (run_id, timestamp, run_name, metrics) VALUES (?,?,?,?)`,
			"x", r.ts, r.run, r.m); err != nil {
			t.Fatal(err)
		}
	}

	series, err := ReadSystemMetrics(context.Background(), path, "run-a")
	if err != nil {
		t.Fatalf("ReadSystemMetrics: %v", err)
	}
	gpu := series["gpu.0.util"]
	if len(gpu) != 2 || gpu[0].Step != 0 || gpu[1].Step != 1 || gpu[1].Value != 80 {
		t.Errorf("gpu series = %+v, want ordinals 0,1 ending 80", gpu)
	}
	if len(series["cpu"]) != 2 {
		t.Errorf("cpu series = %+v, want 2 points", series["cpu"])
	}
}

func TestReadAlerts(t *testing.T) {
	dir := t.TempDir()
	path := seedTrackioDB(t, dir, "nano", nil)
	db := seedSiblingTables(t, path)
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO alerts (run_id, timestamp, run_name, title, text, level, step, alert_id)
		 VALUES (?,?,?,?,?,?,?,?)`,
		"r1", "t1", "run-a", "Loss spike", "loss jumped", "warn", 1200, "a1"); err != nil {
		t.Fatal(err)
	}

	alerts, err := ReadAlerts(context.Background(), path, "run-a")
	if err != nil {
		t.Fatalf("ReadAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v, want 1", alerts)
	}
	a := alerts[0]
	if a.Title != "Loss spike" || a.Level != "warn" || a.Step == nil || *a.Step != 1200 {
		t.Errorf("alert = %+v, want titled warn step=1200", a)
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

func TestSafeProjectName(t *testing.T) {
	// Mirrors trackio's get_project_db_filename sanitization.
	cases := []struct{ in, want string }{
		{"nano-ablation", "nano-ablation"},
		{"keeps_under_score", "keeps_under_score"},
		{"with space", "withspace"},
		{"slash/and.dot:colon", "slashanddotcolon"},
		{"trail   ", "trail"},
		{"   ", "default"},   // all dropped → default
		{"!@#$%", "default"}, // nothing kept → default
		{"", "default"},
		{"café-2", "café-2"}, // Unicode letters + digits kept
	}
	for _, c := range cases {
		if got := SafeProjectName(c.in); got != c.want {
			t.Errorf("SafeProjectName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
