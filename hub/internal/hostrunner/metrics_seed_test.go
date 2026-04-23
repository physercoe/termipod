package hostrunner

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/trackio"

	_ "modernc.org/sqlite"
)

// mustSeedTrackio builds a synthetic trackio SQLite DB at
// {root}/{project}.db matching the documented metrics schema. Each
// (step, value) is logged as a single JSON metric named "loss".
func mustSeedTrackio(t *testing.T, root, project, runName string, points [][2]any) {
	t.Helper()
	path := trackio.ProjectDBPath(root, project)
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
	for _, p := range points {
		payload, _ := json.Marshal(map[string]any{"loss": p[1]})
		if _, err := db.Exec(`
			INSERT INTO metrics (timestamp, run_name, step, metrics)
			VALUES ('t', ?, ?, ?)`, runName, p[0], string(payload)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
}
