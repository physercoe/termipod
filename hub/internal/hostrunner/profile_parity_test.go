package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// TestProfile_ClaudeCode_ParityWithLegacy is the load-bearing
// safety net for ADR-010 Phase 2 (cutover). It runs every frame in
// the captured JSONL corpus through both translators (the legacy
// hardcoded translate() in driver_stdio.go and the data-driven
// ApplyProfile) and asserts the resulting agent_event slices match.
//
// **Operator workflow** (see plan §3.5):
//
//  1. Run a real claude-code session with HUB_STREAM_DEBUG_DIR set:
//
//         HUB_STREAM_DEBUG_DIR=/tmp/recordings termipod-hub
//
//  2. Exercise the SDK shapes you want covered (initial spawn,
//     tool calls, rate-limit pings, end-of-turn results, errors).
//
//  3. Append the captured frames to
//     hub/internal/hostrunner/testdata/profiles/claude-code/corpus.jsonl
//     and re-run this test:
//
//         go test ./internal/hostrunner/ -run TestProfile_ClaudeCode_ParityWithLegacy -v
//
//  4. Any divergence the test surfaces is a profile bug (or a
//     known-gap field — see `parityIgnoreFields`). Fix the YAML rule
//     in agent_families.yaml and re-run until green.
//
// The corpus ships with a synthetic seed covering every translate()
// branch so the test is meaningful even before any real recording
// lands. Real recordings are net-add coverage.
func TestProfile_ClaudeCode_ParityWithLegacy(t *testing.T) {
	corpusPath := filepath.Join(
		"testdata", "profiles", "claude-code", "corpus.jsonl")
	corpus := readCorpus(t, corpusPath)
	if len(corpus) == 0 {
		t.Fatalf("corpus %q is empty", corpusPath)
	}

	f, ok := agentfamilies.ByName("claude-code")
	if !ok || f.FrameProfile == nil {
		t.Fatal("claude-code frame_profile not embedded")
	}
	profile := f.FrameProfile

	var divergent int
	for i, frame := range corpus {
		legacy := runLegacyTranslate(t, frame)
		profileEvts := ApplyProfile(frame, profile)

		if diff := DiffEvents(legacy, profileEvts, ParityIgnoreFields); diff != "" {
			divergent++
			t.Errorf("\n=== frame %d/%d diverged ===\n%s\nframe was: %s\n",
				i, len(corpus), diff, MustEncodeJSON(frame))
		}
	}
	if divergent > 0 {
		t.Logf("%d/%d frames diverged. Either fix the rule in "+
			"agent_families.yaml claude-code.frame_profile, or — "+
			"if the divergence is a documented known-gap field — "+
			"add it to ParityIgnoreFields in profile_diff.go.",
			divergent, len(corpus))
	}
}

// readCorpus loads a JSONL file (one JSON object per line; blank +
// '#'-prefixed lines skipped). Each parsed map is a stream-json
// frame ready for translation.
func readCorpus(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open corpus %q: %v", path, err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("corpus %q line %d: %v", path, lineNo, err)
		}
		out = append(out, frame)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan corpus %q: %v", path, err)
	}
	return out
}

// runLegacyTranslate runs the legacy translator against one frame
// and returns the agent_events it produced. Bypasses the goroutine
// + scanner machinery (Start / readLoop / Stop) so we get
// synchronous, deterministic results suitable for diff'ing.
//
// Calls legacyTranslate directly (not translate, which dispatches
// based on FrameTranslator) so the parity comparison stays anchored
// to the legacy implementation regardless of mode flags.
func runLegacyTranslate(t *testing.T, frame map[string]any) []EmittedEvent {
	t.Helper()
	cap := &capturingPoster{}
	drv := &StdioDriver{
		AgentID: "parity-test",
		Poster:  cap,
	}
	drv.legacyTranslate(context.Background(), frame)
	return cap.events
}
