package resumerecipes

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// The desktop's runtime copy of the resume-recipe table (vision-parity L3b).
//
// recipes.yaml already declares, in its own header, that the hub is not its
// only consumer: the Companion's local agent service runs in Electron main and
// rebinds a local session across an app restart by engine-native resume. This
// file is what makes that literal rather than aspirational — the table crosses
// as JSON marshalled from these very structs, so the desktop answers "how do I
// reattach to a claude session" from the same row the hub reads.
//
// Two artifacts, two jobs, and it is worth being precise about which is which:
//
//   - testdata/resume_recipes_fixture.json is a CONFORMANCE CORPUS — inputs
//     paired with the outputs Go produced. The TypeScript port is tested
//     against it (localagent/resumerecipes.test.ts) exactly the way the L2
//     interpreter is tested against the profile corpus.
//   - resume_recipes.generated.json, written here, is the TABLE ITSELF. A
//     corpus pins behaviour; it cannot tell a running process that
//     `claude-code` resumes via `--resume`. That needs the rows.
//
// Same generated-and-drift-checked discipline as agentfamilies/artifact_test.go,
// and for the same reason: a hand-kept mirror is correct until someone edits
// the YAML and forgets, and then it is wrong in the direction that fails
// silently. recipes.yaml's own header says it — "a wrong resume command does
// not error, it silently starts a fresh conversation."
//
// The whole table crosses rather than the claude row, even though claude is the
// only family the desktop drives locally today. Picking rows would be a third
// thing to keep in step, and L4 (codex) is the next wedge in this lane.

var updateRecipesArtifact = flag.Bool("update-resume-recipes-artifact", false,
	"rewrite desktop/electron/resources/resume_recipes.generated.json")

// recipesArtifactRelPath sits beside the family registry the same service
// loads, under the Electron shell's non-asar resources — electron-builder
// copies those to process.resourcesPath.
var recipesArtifactRelPath = filepath.Join(
	"..", "..", "..", "desktop", "electron", "resources", "resume_recipes.generated.json")

// artifactTable is the wire shape: the validated table, minus the private
// lookup maps. Marshalling *Table directly would emit only what has struct
// tags, which is the same set — this type exists so the JSON field names are
// stated here rather than inherited from yaml tags that happen to double as
// json ones.
type artifactTable struct {
	Version  int      `json:"version"`
	Engines  []Engine `json:"engines"`
	Families []Family `json:"families"`
}

// TestRecipesArtifactIsCurrent regenerates the desktop's copy and diffs it
// against the checked-in file, failing when stale. Regenerate with
//
//	go test ./internal/resumerecipes/ -run Artifact -update-resume-recipes-artifact
func TestRecipesArtifactIsCurrent(t *testing.T) {
	tbl, err := Load()
	if err != nil {
		t.Fatalf("load recipe table: %v", err)
	}
	if len(tbl.Engines) == 0 || len(tbl.Families) == 0 {
		t.Fatal("recipe table is empty; the artifact would ship nothing")
	}

	encoded, err := json.MarshalIndent(artifactTable{
		Version:  tbl.Version,
		Engines:  tbl.Engines,
		Families: tbl.Families,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal table: %v", err)
	}
	encoded = append(encoded, '\n')

	// Skip on the DIRECTORY's absence (a hub-only checkout), never the file's —
	// keying the skip on the file would make deleting it a way to silence this.
	resourcesDir := filepath.Dir(recipesArtifactRelPath)
	if _, err := os.Stat(resourcesDir); os.IsNotExist(err) {
		t.Skipf("no desktop tree at %s; nothing to generate into", resourcesDir)
	}

	if *updateRecipesArtifact {
		if err := os.WriteFile(recipesArtifactRelPath, encoded, 0o644); err != nil {
			t.Fatalf("write %s: %v", recipesArtifactRelPath, err)
		}
		t.Logf("wrote %s (%d bytes, %d engines, %d families)",
			recipesArtifactRelPath, len(encoded), len(tbl.Engines), len(tbl.Families))
		return
	}

	got, err := os.ReadFile(recipesArtifactRelPath)
	if err != nil {
		t.Fatalf("read %s: %v\nRegenerate with:\n"+
			"  go test ./internal/resumerecipes/ -run Artifact -update-resume-recipes-artifact",
			recipesArtifactRelPath, err)
	}
	if !bytes.Equal(got, encoded) {
		t.Errorf("%s is stale: recipes.yaml no longer marshals to what it holds "+
			"(%d bytes on disk, %d generated).\n"+
			"The desktop's local agent service (vision-parity L3b) reads this file to "+
			"rebind a session after an app restart, so a stale copy means the Companion "+
			"reattaches with yesterday's recipe — which does not error, it silently "+
			"starts a fresh conversation.\n"+
			"Regenerate with:\n"+
			"  go test ./internal/resumerecipes/ -run Artifact -update-resume-recipes-artifact",
			recipesArtifactRelPath, len(got), len(encoded))
	}
}

// TestRecipesArtifactCarriesWhatRebindNeeds pins the fields the TypeScript
// reader dereferences. "The artifact is current" is otherwise satisfied by an
// artifact faithfully reproducing a table that lost the claude row's token.
func TestRecipesArtifactCarriesWhatRebindNeeds(t *testing.T) {
	tbl, err := Load()
	if err != nil {
		t.Fatalf("load recipe table: %v", err)
	}

	// Round-trip through JSON: the desktop reads the marshalled form, so a
	// field that fails to cross is invisible to an assertion on the struct.
	blob, err := json.Marshal(artifactTable{Version: tbl.Version, Engines: tbl.Engines, Families: tbl.Families})
	if err != nil {
		t.Fatalf("marshal table: %v", err)
	}
	var wire artifactTable
	if err := json.Unmarshal(blob, &wire); err != nil {
		t.Fatalf("unmarshal table: %v", err)
	}

	var fam *Family
	for i := range wire.Families {
		if wire.Families[i].Family == "claude-code" {
			fam = &wire.Families[i]
			break
		}
	}
	if fam == nil {
		t.Fatal("no claude-code family on the wire; the local service cannot rebind")
	}
	if fam.Mechanism != MechanismArgv {
		t.Errorf("claude-code resumes via %q on the wire, want %q", fam.Mechanism, MechanismArgv)
	}

	var eng *Engine
	for i := range wire.Engines {
		if wire.Engines[i].Engine == fam.Engine {
			eng = &wire.Engines[i]
			break
		}
	}
	if eng == nil {
		t.Fatalf("claude-code references engine %q, absent on the wire", fam.Engine)
	}
	if eng.Style == "" || eng.Token == "" || len(eng.RefKinds) == 0 {
		t.Errorf("engine %q crossed incomplete: style=%q token=%q ref_kinds=%v",
			eng.Engine, eng.Style, eng.Token, eng.RefKinds)
	}
	// The desktop spawns argv and never builds a shell string, so `bin` is the
	// one field it deliberately ignores — it already knows which binary it is
	// running, from the family registry's own `bin`. Assert it crossed anyway:
	// a reader that starts trusting it later should not have to discover the
	// field was empty all along.
	if eng.Bin == "" {
		t.Errorf("engine %q has an empty bin on the wire", eng.Engine)
	}
}
