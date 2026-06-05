package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: a project template authored via the documented template write
// path lands under the per-team overlay <dataRoot>/teams/<team>/templates/
// projects (resolveTeamTemplatePath, W4 / ADR-037 D5). Before this fix the two
// readers that bypass HTTP — loadProjectTemplates (DB seed + phase/widget
// resolution) and readProjectTemplateYAML (phase-criteria hydration) — only
// scanned the legacy global <dataRoot>/team/templates/projects, so the authored
// template was written but never read (the write/read path mismatch a tester
// reported). Both readers must now find it, including a versioned basename
// matched by its `name:` field.
func TestProjectTemplate_PerTeamOverlayIsRead(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// Write exactly where the template PUT handler does: the per-team overlay,
	// with a versioned basename whose logical id is the `name:` field.
	overlay := filepath.Join(dir, "teams", defaultTeamID, "templates", "projects")
	if err := os.MkdirAll(overlay, 0o700); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	const body = "name: code-migration\nkind: migration\ngoal: Migrate a codebase\n" +
		"phases:\n  - assess\n  - migrate\n  - verify\n"
	if err := os.WriteFile(
		filepath.Join(overlay, "code-migration.v1.yaml"), []byte(body), 0o600,
	); err != nil {
		t.Fatalf("write overlay template: %v", err)
	}

	// loadProjectTemplates must now register it (it backs the seeded DB row +
	// phase/overview-widget resolution).
	docs, err := loadProjectTemplates(dir)
	if err != nil {
		t.Fatalf("loadProjectTemplates: %v", err)
	}
	var got *projectTemplateDoc
	for i := range docs {
		if docs[i].Name == "code-migration" {
			got = &docs[i]
			break
		}
	}
	if got == nil {
		names := make([]string, 0, len(docs))
		for _, d := range docs {
			names = append(names, d.Name)
		}
		t.Fatalf("code-migration not registered from per-team overlay; loaded=%v", names)
	}
	if len(got.Phases) != 3 || got.Phases[0] != "assess" || got.Phases[2] != "verify" {
		t.Fatalf("phases=%v want [assess migrate verify]", got.Phases)
	}

	// readProjectTemplateYAML must resolve the logical id to the versioned file
	// (matched by `name:`, not by guessing `<id>.yaml`).
	yaml := srv.readProjectTemplateYAML("code-migration")
	if !strings.Contains(yaml, "Migrate a codebase") {
		t.Fatalf("readProjectTemplateYAML missed the per-team overlay; got %q", yaml)
	}
}

// The legacy global <dataRoot>/team/templates/projects path still resolves, so
// pre-W4 / operator-baseline templates keep working alongside the per-team
// overlay.
func TestProjectTemplate_LegacyGlobalPathStillRead(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	global := filepath.Join(dir, "team", "templates", "projects")
	if err := os.MkdirAll(global, 0o700); err != nil {
		t.Fatalf("mkdir global: %v", err)
	}
	const body = "name: legacy-proj\ngoal: Legacy baseline\n"
	if err := os.WriteFile(
		filepath.Join(global, "legacy-proj.yaml"), []byte(body), 0o600,
	); err != nil {
		t.Fatalf("write global template: %v", err)
	}

	if y := srv.readProjectTemplateYAML("legacy-proj"); !strings.Contains(y, "Legacy baseline") {
		t.Fatalf("legacy global path not read; got %q", y)
	}
}
