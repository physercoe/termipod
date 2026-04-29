package hostrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

		if diff := diffEventSlices(legacy, profileEvts); diff != "" {
			divergent++
			t.Errorf("\n=== frame %d/%d diverged ===\n%s\nframe was: %s\n",
				i, len(corpus), diff, mustEncodeJSON(frame))
		}
	}
	if divergent > 0 {
		t.Logf("%d/%d frames diverged. Either fix the rule in "+
			"agent_families.yaml claude-code.frame_profile, or — "+
			"if the divergence is a documented known-gap field — "+
			"add it to parityIgnoreFields in profile_parity_test.go.",
			divergent, len(corpus))
	}
}

// parityIgnoreFields lists payload field names that the parity diff
// skips — known gaps where the profile-driven and legacy translators
// produce different shapes by design until a future grammar
// extension lands. Adding a field here is a *deliberate* policy
// decision; review the comment above the entry before extending.
var parityIgnoreFields = map[string]string{
	// Legacy normalizeTurnResult walks modelUsage's per-model map and
	// renames the inner camelCase keys (inputTokens → input,
	// cacheReadInputTokens → cache_read, …). The v1 profile grammar
	// has no map-iter construct; by_model passes through with the
	// original keys. Tracked in plan §7 ("Subset DSL escape hatch").
	"by_model": "v1 grammar lacks map-iter; modelUsage passes through verbatim",
	// Legacy translateRateLimit derives a boolean from
	// `firstNonNil(reason) != nil`. The v1 grammar has no
	// "is-this-non-nil" predicate at expression level; the profile
	// emits the reason string directly and lets the mobile renderer
	// derive the boolean. Mobile reads `reason`, not `overage_disabled`,
	// so this is a wire-shape difference without functional impact.
	// Reconsider when the next engine needs a boolean projection.
	"overage_disabled": "v1 grammar lacks bool-from-nullable; mobile reads reason directly",
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

// runLegacyTranslate runs the legacy translate() against one frame
// and returns the agent_events it produced. Bypasses the goroutine
// + scanner machinery (Start / readLoop / Stop) so we get
// synchronous, deterministic results suitable for diff'ing.
func runLegacyTranslate(t *testing.T, frame map[string]any) []postedEvent {
	t.Helper()
	poster := &fakePoster{}
	drv := &StdioDriver{
		AgentID: "parity-test",
		Poster:  poster,
	}
	drv.translate(context.Background(), frame)
	return poster.snapshot()
}

// diffEventSlices compares legacy postedEvents to profile-emitted
// EmittedEvents. Returns an empty string when they match (modulo
// parityIgnoreFields), or a multi-line human-readable diff suitable
// for an AI-agent maintainer to act on. The diff format is:
//
//	count differs:    legacy=N  profile=M
//	event[i] kind:    legacy=…  profile=…
//	event[i].field:   legacy=…  profile=…  (extra/missing/mismatch)
//
// Order matters: legacy emits events in a fixed order
// (per-block first, then usage; turn.result before completion); the
// profile must produce them in the same order. If a future change
// makes order non-deterministic we'll need a multiset compare, but
// that's a regression we want to surface, not paper over.
func diffEventSlices(legacy []postedEvent, profile []EmittedEvent) string {
	var b strings.Builder
	if len(legacy) != len(profile) {
		fmt.Fprintf(&b, "  count differs: legacy=%d  profile=%d\n",
			len(legacy), len(profile))
		fmt.Fprintf(&b, "  legacy kinds:  %v\n", kindsOf(legacy))
		fmt.Fprintf(&b, "  profile kinds: %v\n", kindsOfEmitted(profile))
		return b.String()
	}
	for i := range legacy {
		L := legacy[i]
		P := profile[i]
		if L.Kind != P.Kind {
			fmt.Fprintf(&b, "  event[%d] kind:     legacy=%q  profile=%q\n",
				i, L.Kind, P.Kind)
		}
		legacyProducer := L.Producer
		profileProducer := P.Producer
		if legacyProducer != profileProducer {
			fmt.Fprintf(&b, "  event[%d] producer: legacy=%q  profile=%q\n",
				i, legacyProducer, profileProducer)
		}
		// Payload diff: union of keys, skipping ignored fields. We
		// align on legacy-keys-not-in-profile (missing) +
		// profile-keys-not-in-legacy (extra) + present-with-different-value
		// (mismatch). reflect.DeepEqual handles the recursive case.
		keys := unionKeys(L.Payload, P.Payload)
		for _, k := range keys {
			if _, ignore := parityIgnoreFields[k]; ignore {
				continue
			}
			lv, lOK := L.Payload[k]
			pv, pOK := P.Payload[k]
			switch {
			case lOK && !pOK:
				fmt.Fprintf(&b, "  event[%d].%s missing in profile: legacy=%v\n",
					i, k, lv)
			case pOK && !lOK:
				fmt.Fprintf(&b, "  event[%d].%s extra in profile: profile=%v\n",
					i, k, pv)
			case !reflect.DeepEqual(lv, pv):
				fmt.Fprintf(&b, "  event[%d].%s differs: legacy=%v  profile=%v\n",
					i, k, lv, pv)
			}
		}
	}
	return b.String()
}

func kindsOf(events []postedEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func kindsOfEmitted(events []EmittedEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func unionKeys(a, b map[string]any) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mustEncodeJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
