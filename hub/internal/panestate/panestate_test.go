package panestate

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// --- provenance -------------------------------------------------------------

// upstreamBlobs pins every vendored manifest to its git blob SHA in
// herdrdev/herdr at 6f311498aeeb27c0973781961ef94e8d0016ed17. Plan D-1: the
// vendored files are byte-exact and never hand-edited, so they can be diffed
// against upstream on a re-vendor. Editing one — even to "fix" a rule — fails
// here, which is the point: corrections belong in the overlay.
var upstreamBlobs = map[string]string{
	"amp.toml":            "1fc5dfa6bbabcf47386d04f9c5ed4ec889426c47",
	"antigravity.toml":    "8b9bcfcf4aa5139768d816877bace1ab23d59c8e",
	"claude.toml":         "906235a7ff77f1add05931e4ebd5fc3a6413b20f",
	"cline.toml":          "b30f4417ce6fe05523897dca879a59c4b578bedf",
	"codex.toml":          "a4a4ca9fa2f96e9444bc5fbf34d9b32baa57ec28",
	"cursor.toml":         "ee03e6db9d754225a8cd5b58fb677a69c5e48eed",
	"devin.toml":          "c9564f7cc134c00e1185922d3fa418b36169f278",
	"droid.toml":          "c41d71b43bfd2ef4ac8b2061ea97ad317823a316",
	"gemini.toml":         "9d7a28e112d62661e22642c53bd7509363b970ba",
	"github-copilot.toml": "4233c563eb4738e6ad39ba264c251456d17348b3",
	"grok.toml":           "30b6e9c70438002e078f241064d27495525cba7d",
	"hermes.toml":         "17542184971e41e330ea6ad5eef126c82450c6cc",
	"kilo.toml":           "4ef004e1de4a0e8aaf641d357a63968267d5c249",
	"kimi.toml":           "b4d0100fbae796be8a20555d718a362993f1c5f1",
	"kiro.toml":           "59ac0a061b278d82ffc3d7efa01cde7f4d694f2f",
	"maki.toml":           "5c58404a52a9326f66227142da90dc389947cad3",
	"opencode.toml":       "5245238371da8db0ed722312e457abaf29df137a",
	"pi.toml":             "a58d30e9032611ebff27800025b00fb3068e6763",
	"qodercli.toml":       "51ca805ed77fdce3136da16a2e5643fca5677075",
}

// gitBlobSHA reproduces `git hash-object`: sha1 over "blob <len>\0<content>".
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func TestVendoredManifestsAreByteExactUpstream(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("manifests", "vendor"))
	if err != nil {
		t.Fatalf("read vendor dir: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		seen[e.Name()] = true
		want, ok := upstreamBlobs[e.Name()]
		if !ok {
			t.Errorf("%s is in manifests/vendor but has no upstream blob pin — "+
				"a file here must be a byte-exact upstream copy, not ours "+
				"(ours belong in manifests/overlay)", e.Name())
			continue
		}
		data, err := os.ReadFile(filepath.Join("manifests", "vendor", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if got := gitBlobSHA(data); got != want {
			t.Errorf("%s drifted from upstream: blob %s, want %s "+
				"(re-vendor by copying from herdr, or move the change to the overlay)",
				e.Name(), got, want)
		}
	}
	for name := range upstreamBlobs {
		if !seen[name] {
			t.Errorf("%s is pinned but missing from manifests/vendor", name)
		}
	}
}

// --- loading ----------------------------------------------------------------

func TestAllVendoredManifestsParseValidateAndCompile(t *testing.T) {
	r, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(r.ManifestIDs()); got != len(upstreamBlobs) {
		t.Errorf("loaded %d manifests, want %d", got, len(upstreamBlobs))
	}
}

// TestOnlyVerifiedFamiliesAreMapped — plan D-3. An unmapped family gets NO
// evaluation, never another engine's rules. kimi-code-ts is the deliberate
// omission: upstream detects a CLI it calls `kimi` and nobody has confirmed
// that is our compiled-TypeScript one (ADR-054).
func TestOnlyVerifiedFamiliesAreMapped(t *testing.T) {
	r := MustLoad()
	want := []string{"antigravity", "claude-code", "codex", "gemini-cli"}
	got := r.Families()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("mapped families = %v, want %v", got, want)
	}
	if _, ok := r.ManifestForFamily("kimi-code-ts"); ok {
		t.Error("kimi-code-ts is mapped; it must stay unmapped until someone captures a real " +
			"kimi-code-ts pane — screen rules are more version-fragile than a resume flag")
	}
	if _, err := r.EvaluateFamily("kimi-code-ts", Input{Screen: "anything"}); err == nil {
		t.Error("evaluating an unmapped family must fail, not guess")
	}
}

// --- the corpus -------------------------------------------------------------

type corpusFile struct {
	Note  string       `json:"note"`
	Cases []corpusCase `json:"cases"`
}

type corpusCase struct {
	Name        string `json:"name"`
	Manifest    string `json:"manifest"`
	TestFn      string `json:"test_fn"`
	Screen      string `json:"screen"`
	OSCTitle    string `json:"osc_title"`
	OSCProgress string `json:"osc_progress"`
	WantState   string `json:"want_state"`
	WantRule    string `json:"want_rule"`
}

// TestUpstreamScreenCorpus is the cross-implementation parity check. Both the
// screens and the expected answers come from herdr's own tests, so a
// divergence here means this Go port disagrees with the reference
// implementation on a real screen — not that my expectations were wrong.
func TestUpstreamScreenCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "screen_corpus.json"))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var cf corpusFile
	if err := json.Unmarshal(data, &cf); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(cf.Cases) == 0 {
		t.Fatal("corpus is empty")
	}
	r := MustLoad()
	byManifest := map[string]int{}
	byState := map[string]int{}
	for _, c := range cf.Cases {
		byManifest[c.Manifest]++
		byState[c.WantState]++
		t.Run(c.Name, func(t *testing.T) {
			ex, err := r.EvaluateManifest(c.Manifest, Input{
				Screen: c.Screen, OSCTitle: c.OSCTitle, OSCProgress: c.OSCProgress,
			})
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if string(ex.State) != c.WantState {
				t.Errorf("state = %q, want %q (matched %v)\nscreen: %q\ntitle: %q",
					ex.State, c.WantState, ruleID(ex), c.Screen, c.OSCTitle)
			}
			if c.WantRule != "" && ruleID(ex) != c.WantRule {
				t.Errorf("matched rule = %q, want %q", ruleID(ex), c.WantRule)
			}
		})
	}
	// Coverage is narrow on purpose and stated rather than implied: upstream
	// only publishes screens for the agents it tests hardest. Per-agent
	// captures for the rest are device-verify debt the plan already books.
	t.Logf("corpus coverage: %v across states %v", byManifest, byState)
	for _, want := range []string{"idle", "working", "blocked"} {
		if byState[want] == 0 {
			t.Errorf("corpus has no %s case — all three forms must be exercised", want)
		}
	}
}

func ruleID(ex Explain) string {
	if ex.MatchedRule == nil {
		return ""
	}
	return ex.MatchedRule.ID
}

// TestEveryManifestFallsBackToIdleOnAnEmptyScreen — "strict blocked": a known
// agent showing nothing classifies idle with a recorded fallback reason, never
// blocked. That asymmetry is why a missed dialog degrades to today's behaviour
// instead of producing a false attention.
func TestEveryManifestFallsBackToIdleOnAnEmptyScreen(t *testing.T) {
	r := MustLoad()
	for _, id := range r.ManifestIDs() {
		ex, err := r.EvaluateManifest(id, Input{})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if ex.State != StateIdle {
			t.Errorf("%s: empty screen => %q, want idle", id, ex.State)
		}
		if ex.FallbackReason != FallbackKnownAgentIdle {
			t.Errorf("%s: fallback reason = %q, want %q", id, ex.FallbackReason, FallbackKnownAgentIdle)
		}
		if ex.MatchedRule != nil {
			t.Errorf("%s: empty screen matched rule %q", id, ex.MatchedRule.ID)
		}
	}
}

// TestExplainEvaluatesEveryRuleNotJustTheWinner — P4's explain surface has to
// show why the rules that did NOT match did not match, so evaluation must not
// short-circuit.
func TestExplainEvaluatesEveryRuleNotJustTheWinner(t *testing.T) {
	r := MustLoad()
	m, _ := r.Manifest("claude")
	ex, err := r.EvaluateManifest("claude", Input{OSCTitle: "⠂ project"})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(ex.EvaluatedRules) != len(m.Rules) {
		t.Errorf("evaluated %d rules, want all %d", len(ex.EvaluatedRules), len(m.Rules))
	}
	var withEvidence int
	for _, er := range ex.EvaluatedRules {
		if er.Evidence.RegionBytes > 0 || len(er.Evidence.Contains)+len(er.Evidence.Regex)+len(er.Evidence.LineRegex) > 0 {
			withEvidence++
		}
	}
	if withEvidence == 0 {
		t.Error("no rule carried evidence; explain would have nothing to show")
	}
}

// --- semantics --------------------------------------------------------------

func evalInline(t *testing.T, tomlSrc string, in Input) Explain {
	t.Helper()
	m, err := ParseManifest([]byte(tomlSrc), "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cm, err := compileManifest(m)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return cm.Evaluate(in)
}

// TestPriorityArgmaxWithFileOrderTieBreak — the winner is the highest
// priority; EQUAL priorities go to the earlier rule in file order. This is
// why the vendored files are byte-exact: reordering rules can change the
// answer even with no edit to any rule.
func TestPriorityArgmaxWithFileOrderTieBreak(t *testing.T) {
	src := `
id = "t"
[[rules]]
id = "first_tie"
state = "working"
priority = 10
contains = ["x"]
[[rules]]
id = "second_tie"
state = "blocked"
priority = 10
contains = ["x"]
[[rules]]
id = "loser"
state = "idle"
priority = 1
contains = ["x"]
`
	ex := evalInline(t, src, Input{Screen: "x"})
	if ruleID(ex) != "first_tie" {
		t.Errorf("tie went to %q, want first_tie (file order breaks ties)", ruleID(ex))
	}
	if ex.State != StateWorking {
		t.Errorf("state = %q, want working", ex.State)
	}
}

func TestGateSemantics(t *testing.T) {
	cases := []struct {
		name   string
		rule   string
		screen string
		match  bool
	}{
		{"contains is case-insensitive", `contains = ["ALLOW"]`, "please allow this", true},
		{"contains all must hit", `contains = ["a", "zzz"]`, "a", false},
		{"regex is case-SENSITIVE", `regex = ['Allow']`, "allow", false},
		{"regex matches raw", `regex = ['Allow']`, "Allow", true},
		{"line_regex anchors per line", `line_regex = ['^b$']`, "a\nb\nc", true},
		{"line_regex not whole text", `line_regex = ['^b$']`, "a b c", false},
		{"any needs one", `any = [{ contains = ["p"] }, { contains = ["q"] }]`, "q", true},
		{"any needs at least one", `any = [{ contains = ["p"] }, { contains = ["q"] }]`, "z", false},
		{"not excludes", `contains = ["a"]
not = [{ contains = ["b"] }]`, "a b", false},
		{"not passes when absent", `contains = ["a"]
not = [{ contains = ["b"] }]`, "a c", true},
		{"all nested", `all = [{ contains = ["a"] }, { contains = ["b"] }]`, "a b", true},
		{"all nested fails", `all = [{ contains = ["a"] }, { contains = ["b"] }]`, "a", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\n" + c.rule + "\n"
			ex := evalInline(t, src, Input{Screen: c.screen})
			got := ex.MatchedRule != nil
			if got != c.match {
				t.Errorf("matched = %v, want %v", got, c.match)
			}
		})
	}
}

// TestEmptyAnyIsVacuouslyTrue — an absent `any` must not kill a rule that
// declares only `contains`. Nearly every vendored rule depends on this.
func TestEmptyAnyIsVacuouslyTrue(t *testing.T) {
	src := "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\ncontains = [\"a\"]\n"
	if ex := evalInline(t, src, Input{Screen: "a"}); ex.MatchedRule == nil {
		t.Error("a rule with no `any` did not match")
	}
}

// TestVisibleHintsRequireTheMatchingState — a rule marked visible_blocker
// that resolved to working must not raise attention.
func TestVisibleHintsRequireTheMatchingState(t *testing.T) {
	src := `
id = "t"
[[rules]]
id = "r"
state = "working"
visible_blocker = true
contains = ["x"]
`
	ex := evalInline(t, src, Input{Screen: "x"})
	if ex.VisibleBlocker {
		t.Error("visible_blocker survived on a working rule; P3 would raise attention on it")
	}
}

func TestSkipStateUpdateRecordsWhichRuleFroze(t *testing.T) {
	src := `
id = "t"
[[rules]]
id = "viewer"
state = "idle"
skip_state_update = true
contains = ["transcript"]
`
	ex := evalInline(t, src, Input{Screen: "transcript"})
	if !ex.SkipStateUpdate {
		t.Fatal("skip_state_update not carried")
	}
	if ex.SkippedUpdateReason != "matched_rule:viewer" {
		t.Errorf("reason = %q, want matched_rule:viewer", ex.SkippedUpdateReason)
	}
}

// --- regions ----------------------------------------------------------------

func TestRegionScanners(t *testing.T) {
	screen := "top line\n\nmiddle\n────────────────\nprompt body\n────────────────\nfooter"
	cases := []struct{ region, want string }{
		{"whole_recent", screen},
		{"bottom_non_empty_lines(1)", "footer"},
		{"bottom_non_empty_lines(2)", "────────────────\nfooter"},
		{"top_non_empty_lines(1)", "top line\n"},
		{"after_last_horizontal_rule", "footer"},
		{"prompt_box_body", "prompt body\n"},
	}
	in := Input{Screen: screen}
	for _, c := range cases {
		t.Run(c.region, func(t *testing.T) {
			if got := in.Resolve(c.region); got != c.want {
				t.Errorf("Resolve(%s) = %q, want %q", c.region, got, c.want)
			}
		})
	}
}

func TestBottomNonEmptyLinesSkipsBlanks(t *testing.T) {
	in := Input{Screen: "a\n\n\nb\n\n"}
	if got := in.Resolve("bottom_non_empty_lines(1)"); !strings.HasPrefix(got, "b") {
		t.Errorf("got %q, want the last NON-EMPTY line", got)
	}
}

func TestAfterLastPromptMarkerFallsBackToWholeScreen(t *testing.T) {
	in := Input{Screen: "no marker here"}
	if got := in.Resolve("after_last_prompt_marker"); got != in.Screen {
		t.Errorf("got %q, want the whole screen when no marker is present", got)
	}
	in2 := Input{Screen: "before\n› \nafter"}
	if got := in2.Resolve("after_last_prompt_marker"); got != "after" {
		t.Errorf("got %q, want text after the marker", got)
	}
}

// TestHorizontalRuleNeedsThreeCharsOrNothingAfter — one box-drawing char
// followed by text is a tree glyph, not a rule.
func TestHorizontalRuleNeedsThreeCharsOrNothingAfter(t *testing.T) {
	cases := map[string]bool{
		"────────": true,
		"─":        true,
		"─── tail": true,
		"─ tail":   false,
		"text":     false,
		"":         false,
	}
	for line, want := range cases {
		if got := isHorizontalRule(line); got != want {
			t.Errorf("isHorizontalRule(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestUnimplementedRegionIsRefusedNotEmptied — upstream resolves an unknown
// region to "", which turns a typo or a newer-schema region into a rule that
// silently never fires. We refuse at load instead.
func TestUnimplementedRegionIsRefusedNotEmptied(t *testing.T) {
	for _, bad := range []string{
		"after_last_promt_marker", // typo
		"above_prompt_box",        // real upstream region we have not ported
		"bottom_lines(0)",
		"bottom_lines(x)",
		"",
	} {
		if err := ValidateRegion(bad); err == nil && bad != "" {
			t.Errorf("region %q was accepted", bad)
		}
	}
	if err := ValidateRegion("bottom_lines(3)"); err != nil {
		t.Errorf("bottom_lines(3) rejected: %v", err)
	}
}

// --- validation -------------------------------------------------------------

func TestValidationRefusals(t *testing.T) {
	cases := []struct{ name, src string }{
		{"unknown key", "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\ncontain = [\"x\"]\n"},
		{"no matchers", "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\n"},
		{"bad region", "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\nregion = \"nope\"\ncontains = [\"x\"]\n"},
		{"bad regex", "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\nregex = [\"[\"]\n"},
		{"bad nested regex", "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\nany = [{ line_regex = [\"[\"] }]\n"},
		{"unknown state", "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"confused\"\ncontains = [\"x\"]\n"},
		{"no id", "id = \"t\"\n[[rules]]\nstate = \"working\"\ncontains = [\"x\"]\n"},
		{"duplicate rule id", "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\ncontains = [\"x\"]\n[[rules]]\nid = \"r\"\nstate = \"idle\"\ncontains = [\"y\"]\n"},
		{"no manifest id", "[[rules]]\nid = \"r\"\nstate = \"working\"\ncontains = [\"x\"]\n"},
		{"empty matcher", "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\ncontains = [\"\"]\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(c.src), "test"); err == nil {
				t.Errorf("accepted an invalid manifest")
			}
		})
	}
}

func TestGateDepthCap(t *testing.T) {
	inner := `{ contains = ["x"] }`
	for i := 0; i < MaxGateDepth+2; i++ {
		inner = `{ all = [` + inner + `] }`
	}
	src := "id = \"t\"\n[[rules]]\nid = \"r\"\nstate = \"working\"\nall = [" + inner + "]\n"
	if _, err := ParseManifest([]byte(src), "test"); err == nil {
		t.Error("gate nested past the depth cap was accepted")
	}
}

// --- regex dialect ----------------------------------------------------------

// TestRegexTranslations pins the Rust-regex -> RE2 dialect fixes. 9 of the 58
// vendored patterns need one; without them those rules would not compile and
// the manifest would not load.
func TestRegexTranslations(t *testing.T) {
	cases := []struct {
		in        string
		wantOut   string
		wantExact bool
	}{
		{`[\u2800-\u28FF]`, `[\x{2800}-\x{28FF}]`, true},
		{`[\u{fe0e}\u{fe0f}]`, `[\x{fe0e}\x{fe0f}]`, true},
		{`\p{Alphabetic}+`, `[\p{L}\p{Nl}]+`, false},
		{`^\s*plain$`, `^\s*plain$`, true}, // untouched
	}
	for _, c := range cases {
		got, notes := translateRegex(c.in)
		if got != c.wantOut {
			t.Errorf("translateRegex(%q) = %q, want %q", c.in, got, c.wantOut)
		}
		if c.in == c.wantOut && len(notes) != 0 {
			t.Errorf("%q needed no translation but produced notes %v", c.in, describeNotes(notes))
		}
		if c.in != c.wantOut {
			if len(notes) == 0 {
				t.Errorf("%q was translated silently — every translation must be recorded", c.in)
			}
			for _, n := range notes {
				if !c.wantExact && n.Exact && strings.Contains(n.Rule, "Alphabetic") {
					t.Errorf("the Alphabetic approximation is labelled exact")
				}
			}
		}
	}
}

// TestTranslatedPatternsStillMatchTheirRealScreens — the translations are only
// worth anything if the rules they rescue still fire. These are the braille
// spinner lines the affected rules were written for.
func TestTranslatedPatternsStillMatchTheirRealScreens(t *testing.T) {
	r := MustLoad()
	// antigravity's spinner_working rule: braille spinner + a word ending "ing".
	ex, err := r.EvaluateManifest("agy", Input{Screen: "⠹ Thinking about it"})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if ex.State != StateWorking {
		t.Errorf("agy braille spinner => %q, want working (the \\u + \\p{Alphabetic} "+
			"translation is what makes this rule compile)", ex.State)
	}
}

// TestAllVendoredPatternsCompileAfterTranslation walks every pattern in every
// vendored manifest, so a re-vendor that introduces an untranslatable
// construct fails here with the pattern named, rather than at Load with only
// the first one.
func TestAllVendoredPatternsCompileAfterTranslation(t *testing.T) {
	r := MustLoad()
	var translated, inexact []string
	for _, id := range r.ManifestIDs() {
		m, _ := r.Manifest(id)
		for _, rule := range m.Rules {
			walkGate(&rule.Gate, func(pat string) {
				_, notes, err := compileTranslated(pat)
				if err != nil {
					t.Errorf("%s/%s: %v", id, rule.ID, err)
					return
				}
				for _, n := range notes {
					translated = append(translated, id+"/"+rule.ID)
					if !n.Exact {
						inexact = append(inexact, id+"/"+rule.ID+": "+n.Rule)
					}
				}
			})
		}
	}
	sort.Strings(translated)
	sort.Strings(inexact)
	t.Logf("patterns needing dialect translation: %d\n%s", len(translated), strings.Join(translated, "\n"))
	if len(inexact) == 0 {
		t.Error("expected the \\p{Alphabetic} approximation to be reported as inexact; " +
			"if it is gone, drop this assertion deliberately rather than letting it rot")
	}
	t.Logf("INEXACT translations (matched set differs, however narrowly):\n%s", strings.Join(inexact, "\n"))
}

func walkGate(g *Gate, fn func(string)) {
	for _, p := range g.Regex {
		fn(p)
	}
	for _, p := range g.LineRegex {
		fn(p)
	}
	for i := range g.All {
		walkGate(&g.All[i], fn)
	}
	for i := range g.Any {
		walkGate(&g.Any[i], fn)
	}
	for i := range g.Not {
		walkGate(&g.Not[i], fn)
	}
}

// --- overlay ----------------------------------------------------------------

// TestOverlayReplacesAVendoredManifestEntirely — plan D-1's precedence rule.
// A partial merge would make "what is in force?" unanswerable from either
// file alone.
func TestOverlayReplacesAVendoredManifestEntirely(t *testing.T) {
	reg := &Registry{byID: map[string]compiledManifest{}}
	vendored, err := ParseManifest([]byte("id = \"x\"\n[[rules]]\nid = \"v\"\nstate = \"idle\"\ncontains = [\"a\"]\n"), SourceVendor)
	if err != nil {
		t.Fatal(err)
	}
	cv, _ := compileManifest(vendored)
	reg.byID["x"] = cv

	overlay, err := ParseManifest([]byte("id = \"x\"\n[[rules]]\nid = \"o\"\nstate = \"blocked\"\ncontains = [\"a\"]\n"), SourceOverlay)
	if err != nil {
		t.Fatal(err)
	}
	co, _ := compileManifest(overlay)
	reg.byID["x"] = co

	ex := reg.byID["x"].Evaluate(Input{Screen: "a"})
	if ruleID(ex) != "o" || ex.Source != SourceOverlay {
		t.Errorf("overlay did not replace the vendored manifest: rule=%q source=%q", ruleID(ex), ex.Source)
	}
	if len(reg.byID["x"].manifest.Rules) != 1 {
		t.Errorf("rules were merged (%d), want replacement", len(reg.byID["x"].manifest.Rules))
	}
}
