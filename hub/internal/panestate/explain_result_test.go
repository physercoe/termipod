package panestate

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// keysOf marshals a value and returns its top-level JSON keys, sorted.
func keysOf(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertKeys(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s wire keys =\n  %v\nwant\n  %v\n"+
			"A field added to this struct now ships to every client. If that is "+
			"intended, add it here; if not, tag it `json:\"-\"`.", what, got, want)
	}
}

// The wire contract of `host.pane_explain` (plan P4).
//
// Explain is both the evaluator's internal record and the wire shape — one
// type instead of two that drift. The price of that choice is that a field
// added for evaluation ships by default, to a payload that already carries
// bounded pane text. This test is the deliberateness gate: adding a field is
// fine, adding it without noticing is not.
func TestExplainWireKeysAreDeliberate(t *testing.T) {
	// Fully populated: `omitempty` fields must be present to be checked.
	ex := Explain{
		ManifestID:          "codex",
		ManifestVersion:     "1",
		Source:              "vendor",
		State:               StateBlocked,
		MatchedRule:         &MatchedRule{ID: "r", Priority: 1, Region: "whole_recent", State: StateBlocked},
		FallbackReason:      "x",
		VisibleIdle:         true,
		VisibleBlocker:      true,
		VisibleWorking:      true,
		SkipStateUpdate:     true,
		SkippedUpdateReason: "matched_rule:r",
		EvaluatedRules:      []EvaluatedRule{},
	}
	assertKeys(t, "Explain", keysOf(t, ex), []string{
		"fallback_reason", "manifest_id", "manifest_version", "matched_rule",
		"rules", "skip_state_update", "skipped_update_reason", "source",
		"state", "visible_blocker", "visible_idle", "visible_working",
	})

	assertKeys(t, "EvaluatedRule", keysOf(t, EvaluatedRule{}), []string{
		"evidence", "id", "matched", "priority", "region", "state",
	})

	assertKeys(t, "Evidence", keysOf(t, Evidence{
		Contains: []string{"a"}, Regex: []string{"b"}, LineRegex: []string{"c"},
		AllCount: 1, AnyCount: 1, NotCount: 1, RegionBytes: 1, RegionPreview: "p",
	}), []string{
		"all_count", "any_count", "contains", "line_regex", "not_count",
		"regex", "region_bytes", "region_preview",
	})

	assertKeys(t, "ExplainResult", keysOf(t, ExplainResult{
		Mode: "live", AgentID: "a", PaneID: "%1", HostID: "h", Family: "codex",
		ScreenBytes: 1, ScreenLines: 1, OSCTitle: "t",
	}), []string{
		"agent_id", "explain", "family", "host_id", "mode", "osc_title",
		"pane_id", "screen_bytes", "screen_lines",
	})
}

// The record carries bounded previews, never the screen. A pane can hold a
// token, a diff, or a customer's data; the difference between this verb and a
// screenshot is that this one is bounded per region and says so.
func TestExplainResultDoesNotCarryTheWholeScreen(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	secret := strings.Repeat("SECRET-", 200) // 1400 chars, far over the bound
	screen := "• Working (4s • esc to interrupt)\n" + secret + "\n"

	ex, err := reg.EvaluateManifest("codex", Input{Screen: screen})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	res := NewExplainResult("live", "codex", Input{Screen: screen}, ex)
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), secret) {
		t.Fatal("the whole screen reached the wire")
	}
	if res.ScreenBytes != len(screen) {
		t.Errorf("screen_bytes = %d, want %d — the size must travel even though "+
			"the bytes do not", res.ScreenBytes, len(screen))
	}
	// Every preview is individually bounded, not just the total.
	for _, r := range res.Explain.EvaluatedRules {
		if n := len([]rune(r.Evidence.RegionPreview)); n > maxPreviewChars+3 {
			t.Errorf("rule %s preview is %d runes, over the bound", r.ID, n)
		}
	}
	if len(res.Explain.EvaluatedRules) == 0 {
		t.Fatal("no rules evaluated; the record would explain nothing")
	}
}

func TestCountLinesMatchesHowAReaderCounts(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1}, // a trailing newline does not add an empty row
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"\n", 1}, // one empty row
	} {
		if got := countLines(tc.in); got != tc.want {
			t.Errorf("countLines(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
