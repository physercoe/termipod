package agentfamilies

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// The desktop's runtime copy of the embedded family registry (vision-parity
// L3).
//
// The Companion runs claude locally, in Electron main, with no hub process
// anywhere (plan D-7). To do that it needs two things this file owns: the
// per-mode LAUNCH argv that puts the engine into M2 stream-json, and the
// FRAME PROFILE that translates what comes back. Both are data in
// agent_families.yaml, and the whole point of ADR-010 is that they stay data.
//
// So the desktop reads them — but not from the YAML. Three reasons, in
// descending order of how much trouble each would have caused:
//
//   - The Go loader IS the semantics. It resolves the embedded set, applies
//     defaults, and decides what a field means when it is absent. A YAML
//     parser on the TypeScript side would be a second implementation of that,
//     and the one thing this lane has already learned (L2) is that two
//     implementations of one rule language drift while both stay green.
//   - The Electron main bundle has no YAML parser and does not need one. The
//     parity fixture established this boundary already: profiles cross as JSON
//     marshalled from these very structs, so a `yaml:` tag with no `json:`
//     twin cannot silently reach the interpreter.
//   - Generated-and-drift-checked beats copied. A hand-maintained mirror is
//     correct exactly until someone edits the YAML and forgets, and it fails
//     silently — a rule that never fires, a launch flag that never ships.
//
// The whole Family is marshalled rather than a hand-picked subset. Picking
// fields would be a third thing to keep in sync, and L3 already needs three of
// them (launch, frame_profile, the prompt_* capability flags F3 reads from the
// hub and a local source has no hub to ask).
//
// EMBEDDED ONLY — deliberately. `readEmbedded` is the vendored set, not
// `Registry.Families()`, which layers an operator's overlay directory on top.
// An overlay is host-side operator config; the desktop ships the bundle it was
// built with, and a file on some machine quietly changing how the Companion
// translates frames is a debugging problem nobody would enjoy.

var updateFamiliesArtifact = flag.Bool("update-families-artifact", false,
	"rewrite desktop/electron/resources/agent_families.generated.json")

// artifactRelPath is where the generated registry lands: beside the Electron
// shell's other non-asar resources, because that is where it has to ship from.
// electron-builder copies `resources/` entries to `process.resourcesPath`, and
// main resolves dev vs packaged with the same two-branch helper the stdio relay
// uses (`stdioBridgePath`).
var artifactRelPath = filepath.Join(
	"..", "..", "..", "desktop", "electron", "resources", "agent_families.generated.json")

// TestFamiliesArtifactIsCurrent regenerates the desktop's registry copy and
// diffs it against the checked-in file.
//
// This test FAILS when the artifact is stale, which is the entire enforcement
// mechanism: edit agent_families.yaml and CI goes red until you run
//
//	go test ./internal/agentfamilies/ -run Artifact -update-families-artifact
//
// The alternative — regenerating at desktop build time — would mean the file
// on disk and the file in the bundle could disagree, and only the bundle
// matters.
func TestFamiliesArtifactIsCurrent(t *testing.T) {
	fams, err := readEmbedded()
	if err != nil {
		t.Fatalf("read embedded families: %v", err)
	}
	if len(fams) == 0 {
		t.Fatal("embedded family set is empty; the artifact would ship nothing")
	}

	encoded, err := json.MarshalIndent(fams, "", "  ")
	if err != nil {
		t.Fatalf("marshal families: %v", err)
	}
	encoded = append(encoded, '\n')

	// A hub-only checkout has no desktop tree to write into. Skip rather than
	// fail — but only on the DIRECTORY's absence, so a present-but-stale
	// artifact still fails. Keying the skip on the file would make deleting it
	// a way to silence this test.
	resourcesDir := filepath.Dir(artifactRelPath)
	if _, err := os.Stat(resourcesDir); os.IsNotExist(err) {
		t.Skipf("no desktop tree at %s; nothing to generate into", resourcesDir)
	}

	if *updateFamiliesArtifact {
		if err := os.WriteFile(artifactRelPath, encoded, 0o644); err != nil {
			t.Fatalf("write %s: %v", artifactRelPath, err)
		}
		t.Logf("wrote %s (%d bytes, %d families)", artifactRelPath, len(encoded), len(fams))
		return
	}

	got, err := os.ReadFile(artifactRelPath)
	if err != nil {
		t.Fatalf("read %s: %v\nRegenerate with:\n"+
			"  go test ./internal/agentfamilies/ -run Artifact -update-families-artifact",
			artifactRelPath, err)
	}
	if !bytes.Equal(got, encoded) {
		t.Errorf("%s is stale: agent_families.yaml no longer marshals to what it "+
			"holds (%d bytes on disk, %d generated).\n"+
			"The desktop's local agent service (vision-parity L3) reads this file "+
			"for its launch argv and frame profiles, so a stale copy means the "+
			"Companion drives the engine with yesterday's contract.\n"+
			"Regenerate with:\n"+
			"  go test ./internal/agentfamilies/ -run Artifact -update-families-artifact",
			artifactRelPath, len(got), len(encoded))
	}
}

// TestFamiliesArtifactCarriesWhatTheLocalServiceNeeds pins the three things
// L3's claude driver reads out of the artifact. Without this, "the artifact is
// current" is satisfied by an artifact that faithfully reproduces a registry
// which lost the M2 launch contract — current and useless.
func TestFamiliesArtifactCarriesWhatTheLocalServiceNeeds(t *testing.T) {
	fams, err := readEmbedded()
	if err != nil {
		t.Fatalf("read embedded families: %v", err)
	}
	var claude *Family
	for i := range fams {
		if fams[i].Family == "claude-code" {
			claude = &fams[i]
			break
		}
	}
	if claude == nil {
		t.Fatal("no claude-code family; the local service has nothing to launch")
	}

	// Round-trip through JSON: what the desktop reads is the marshalled form,
	// not the struct, so assert against that. A field that fails to cross (a
	// yaml: tag with no json: twin) is invisible to an assertion on `claude`.
	blob, err := json.Marshal(claude)
	if err != nil {
		t.Fatalf("marshal claude-code: %v", err)
	}
	var wire Family
	if err := json.Unmarshal(blob, &wire); err != nil {
		t.Fatalf("unmarshal claude-code: %v", err)
	}

	if wire.Bin == "" {
		t.Error("bin is empty on the wire; the service has no binary to spawn")
	}
	args := wire.LaunchArgs("M2")
	if len(args) == 0 {
		t.Fatal("launch.M2.mode_args is empty on the wire; the child would " +
			"start in interactive mode and never speak stream-json")
	}
	// The two flags that make it a bidirectional JSON pipe rather than a
	// one-shot. The probe that established L3's topology (a child surviving N
	// turns on one held-open stdin) depends on --input-format specifically.
	wantFlags := []string{"--print", "--output-format", "--input-format"}
	for _, want := range wantFlags {
		found := false
		for _, got := range args {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("launch.M2.mode_args %v is missing %s", args, want)
		}
	}
	if wire.FrameProfile == nil || len(wire.FrameProfile.Rules) == 0 {
		t.Error("frame_profile did not cross to the wire; every frame would " +
			"fall to the raw fallback and the transcript would be JSON dumps")
	}
}
