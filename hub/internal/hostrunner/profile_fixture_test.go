package hostrunner

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
	"github.com/termipod/hub/internal/hostrunner/profile_eval"
)

// Cross-language parity fixtures for the frame-profile interpreter.
//
// The desktop Companion runs agents locally (vision-parity L3/L4), which means
// a *second* implementation of the ADR-010 interpreter — in TypeScript, in
// Electron main (`desktop/electron/src/frameprofile/`). Two interpreters over
// one rule language is exactly the shape that drifts: both stay green, both
// stay plausible, and the transcripts they produce quietly stop agreeing.
//
// So neither side is trusted to describe the other. This test generates what Go
// actually produces — the profiles verbatim, every corpus frame's events, and a
// set of expression cases aimed at the grammar's corners — and the TypeScript
// suite replays the same inputs through its own interpreter and diffs. Go owns
// the answers; TS has to match them.
//
// The fixture is checked in, and this test FAILS when it is stale. That is the
// whole enforcement mechanism: edit a rule in agent_families.yaml, or add a
// corpus row, and CI goes red until you regenerate —
//
//	go test ./internal/hostrunner/ -run Fixture -update-frame-fixture
//
// — at which point the TypeScript side sees the new expectations on its next
// run. Parity by construction rather than by anyone remembering.

var updateFrameFixture = flag.Bool("update-frame-fixture", false,
	"rewrite the cross-language frame-profile parity fixtures")

// fixtureFamilies pairs a family name with its corpus directory. They differ
// for gemini (family `gemini-cli`, testdata dir `gemini`).
var fixtureFamilies = []struct{ family, dir string }{
	{"claude-code", "claude-code"},
	{"codex", "codex"},
	{"gemini-cli", "gemini"},
}

// fixtureEvent is EmittedEvent with the wire's field names. EmittedEvent has no
// json tags — it never crosses a wire in Go — and `{"Kind":…}` would be a
// vocabulary this repo doesn't otherwise use.
type fixtureEvent struct {
	Kind     string         `json:"kind"`
	Producer string         `json:"producer"`
	Payload  map[string]any `json:"payload"`
}

// parityFixture is what one family's `parity.json` holds. Frames are NOT
// duplicated here: `events[i]` lines up with corpus line i, so corpus.jsonl
// stays the single copy of every frame and the TS side has to read it too. A
// parser that drifts and yields a different frame count fails the length check
// before it can quietly test less.
type parityFixture struct {
	Family  string                      `json:"family"`
	Corpus  string                      `json:"corpus"`
	Profile *agentfamilies.FrameProfile `json:"profile"`
	Events  [][]fixtureEvent            `json:"events"`
}

// grammarFixture is the expression-level half: a handful of named scopes, then
// the cases that reference them. Scopes are named rather than inlined per case
// so the file stays readable — eighty copies of one frame is a diff nobody can
// review.
type grammarFixture struct {
	Scopes map[string]map[string]any `json:"scopes"`
	Cases  []grammarCase             `json:"cases"`
}

// grammarCase is one expression evaluated against fixed scopes. The corpus
// exercises the rules the profiles actually use; these exercise the ones a
// future profile could reach for and the ports could disagree about — indexing,
// scope crossing, the presence predicates, literal escapes, and the JS-only
// traps (`constructor`, `__proto__`) that a naive property read would resolve
// to something no frame ever carried.
//
// Inner and Outer name a key of grammarFixture.Scopes; an empty Outer is the
// nil scope, which is what `$$.` sees outside a for_each.
type grammarCase struct {
	Name   string `json:"name"`
	Expr   string `json:"expr"`
	Inner  string `json:"inner"`
	Outer  string `json:"outer"`
	Result any    `json:"result"`
}

// TestFrameProfile_ParityFixtureIsCurrent regenerates each family's fixture and
// diffs it against the checked-in file.
func TestFrameProfile_ParityFixtureIsCurrent(t *testing.T) {
	for _, fam := range fixtureFamilies {
		t.Run(fam.family, func(t *testing.T) {
			fix := buildParityFixture(t, fam.family, fam.dir)
			path := filepath.Join("testdata", "profiles", fam.dir, "parity.json")
			assertFixtureCurrent(t, path, fix)
		})
	}
}

// TestFrameProfile_GrammarFixtureIsCurrent does the same for the
// expression-level cases, which are family-independent.
func TestFrameProfile_GrammarFixtureIsCurrent(t *testing.T) {
	fix := grammarFixtureCases(t)
	for i := range fix.Cases {
		c := &fix.Cases[i]
		c.Result = profile_eval.Eval(c.Expr, fix.Scopes[c.Inner], fix.Scopes[c.Outer])
	}
	path := filepath.Join("testdata", "profiles", "grammar.json")
	assertFixtureCurrent(t, path, fix)
}

// TestFrameProfile_TranslateFixtureIsCurrent covers the rule shapes the three
// shipped profiles don't happen to use.
//
// The corpus proves the ports agree on what termipod ships today. It says
// nothing about the parts of the rule language nobody has reached for yet —
// an empty catch-all match, a `for_each` over a non-array, a projection whose
// source is missing — and those are exactly where a port drifts, because
// nothing exercises them until an engine profile needs them and by then the
// disagreement is a transcript bug on one client.
//
// So these are synthetic: hand-authored profiles aimed at one semantic each,
// with Go's answer recorded. Parity on the language, not just on the corpus.
func TestFrameProfile_TranslateFixtureIsCurrent(t *testing.T) {
	cases := translateCases(t)
	for i := range cases {
		c := &cases[i]
		c.Events = make([]fixtureEvent, 0, 2)
		for _, e := range ApplyProfile(c.Frame, c.Profile) {
			payload := e.Payload
			if payload == nil {
				payload = map[string]any{}
			}
			c.Events = append(c.Events, fixtureEvent{Kind: e.Kind, Producer: e.Producer, Payload: payload})
		}
	}
	path := filepath.Join("testdata", "profiles", "translate.json")
	assertFixtureCurrent(t, path, cases)
}

// TestFrameProfile_MatchValuesAreStrings guards the one semantic the two
// interpreters cannot share.
//
// Go compares match values as `any != any`, so a YAML integer (`int`) never
// equals a JSON number decoded from a frame (`float64`) — the rule silently
// never fires. TypeScript has one number type and would match. Worse, two
// values of the same uncomparable dynamic type (a list matcher against a list
// field) panic the comparison outright.
//
// Every match value across the shipped profiles is a string today, where both
// languages agree exactly. This test is what keeps that true: a numeric or
// structured matcher is a divergence between the ports, and it should fail
// here — at the moment it is authored — rather than as a transcript that
// differs on one client.
func TestFrameProfile_MatchValuesAreStrings(t *testing.T) {
	for _, fam := range fixtureFamilies {
		f, ok := agentfamilies.ByName(fam.family)
		if !ok || f.FrameProfile == nil {
			t.Fatalf("%s frame_profile not embedded", fam.family)
		}
		walkRules(f.FrameProfile.Rules, func(path string, r agentfamilies.Rule) {
			for k, v := range r.Match {
				if _, isStr := v.(string); !isStr {
					t.Errorf("%s %s: match[%q] = %#v (%T), want a string. "+
						"Non-string matchers do not mean the same thing in the "+
						"Go and TypeScript interpreters — see this test's doc.",
						fam.family, path, k, v, v)
				}
			}
		})
	}
}

// walkRules visits every rule and sub-rule, naming each by its position so a
// failure points at one rule rather than at the profile.
func walkRules(rules []agentfamilies.Rule, fn func(path string, r agentfamilies.Rule)) {
	var walk func(prefix string, rs []agentfamilies.Rule)
	walk = func(prefix string, rs []agentfamilies.Rule) {
		for i, r := range rs {
			path := fmt.Sprintf("%srules[%d]", prefix, i)
			fn(path, r)
			if len(r.SubRules) > 0 {
				walk(path+".sub_", r.SubRules)
			}
		}
	}
	walk("", rules)
}

func buildParityFixture(t *testing.T, family, dir string) parityFixture {
	t.Helper()
	corpusPath := filepath.Join("testdata", "profiles", dir, "corpus.jsonl")
	corpus := readCorpus(t, corpusPath)
	if len(corpus) == 0 {
		t.Fatalf("corpus %q is empty", corpusPath)
	}
	f, ok := agentfamilies.ByName(family)
	if !ok || f.FrameProfile == nil {
		t.Fatalf("%s frame_profile not embedded", family)
	}

	events := make([][]fixtureEvent, 0, len(corpus))
	for _, frame := range corpus {
		// ApplyProfile returns a nil slice when every winning rule gated
		// itself off. Normalize to empty here so the fixture always carries a
		// JSON array — `null` and `[]` would be a difference between the ports
		// that says nothing about either interpreter.
		row := make([]fixtureEvent, 0, 2)
		for _, e := range ApplyProfile(frame, f.FrameProfile) {
			payload := e.Payload
			if payload == nil {
				payload = map[string]any{}
			}
			row = append(row, fixtureEvent{Kind: e.Kind, Producer: e.Producer, Payload: payload})
		}
		events = append(events, row)
	}
	return parityFixture{
		Family:  family,
		Corpus:  "corpus.jsonl",
		Profile: f.FrameProfile,
		Events:  events,
	}
}

// assertFixtureCurrent marshals `want`, compares it byte-for-byte with the file
// at `path`, and either rewrites it (-update-frame-fixture) or fails.
func assertFixtureCurrent(t *testing.T, path string, want any) {
	t.Helper()
	encoded, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	encoded = append(encoded, '\n')

	if *updateFrameFixture {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(encoded))
		return
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\nRegenerate with:\n"+
			"  go test ./internal/hostrunner/ -run Fixture -update-frame-fixture",
			path, err)
	}
	if !bytes.Equal(got, encoded) {
		t.Errorf("%s is stale: the Go interpreter no longer produces what it "+
			"holds (%d bytes on disk, %d generated).\n"+
			"The TypeScript interpreter in desktop/electron/src/frameprofile/ "+
			"is pinned to this file, so regenerate it and re-run the electron "+
			"suite:\n"+
			"  go test ./internal/hostrunner/ -run Fixture -update-frame-fixture\n"+
			"  cd desktop/electron && npm test",
			path, len(got), len(encoded))
	}
}

// grammarFixtureCases authors the expression-level corner cases. Scopes are
// written as JSON so both interpreters start from bytes rather than from a Go
// literal — a `map[string]any{"n": 1}` would hand Go an `int` where the TS side
// gets a `number`, and the fixture would be comparing two different inputs.
func grammarFixtureCases(t *testing.T) grammarFixture {
	t.Helper()
	const frameJSON = `{
		"a": "alpha", "empty": "", "zero": 0, "f": false, "t": true,
		"nested": {"deep": {"leaf": "found"}},
		"arr": [{"name": "first"}, {"name": "second"}],
		"nums": [10, 20, 30],
		"emptyArr": [], "emptyObj": {}, "nullv": null,
		"obj": {"k": "v"},
		"__proto__": "own-property",
		"weird.key": "dotted-literal"
	}`
	const parentJSON = `{"a": "outer-alpha", "message": {"id": "msg_42"}}`

	scopes := map[string]map[string]any{
		"frame":  decodeScope(t, frameJSON),
		"parent": decodeScope(t, parentJSON),
	}

	cases := []struct{ name, expr string }{
		// Paths.
		{"path/simple", "$.a"},
		{"path/nested", "$.nested.deep.leaf"},
		{"path/missing", "$.nope"},
		{"path/missing-midway", "$.nested.nope.leaf"},
		{"path/through-null", "$.nullv.leaf"},
		{"path/scope-itself", "$."},
		{"path/object", "$.obj"},
		{"path/array", "$.nums"},
		{"path/no-dollar-prefix", "a"},
		{"path/dotted-key-is-not-one-key", "$.weird.key"},

		// Indexing.
		{"index/first", "$.arr[0].name"},
		{"index/second", "$.arr[1].name"},
		{"index/out-of-bounds", "$.arr[2]"},
		{"index/negative", "$.arr[-1]"},
		{"index/scalar-element", "$.nums[1]"},
		{"index/into-non-array", "$.obj[0]"},
		{"index/unclosed", "$.arr[0"},
		{"index/empty", "$.arr[]"},
		{"index/non-numeric", "$.arr[x]"},
		// strconv.Atoi is stricter than JS `Number`, which happily reads
		// " 1 " as 1. Every row below is one way the two could disagree
		// about what counts as a digit string.
		{"index/sign-prefix", "$.nums[+1]"},
		{"index/leading-zero", "$.nums[01]"},
		{"index/padded", "$.nums[ 1 ]"},
		{"index/trailing-space", "$.nums[1 ]"},
		{"index/hex-literal", "$.nums[0x1]"},
		{"index/underscore-separator", "$.nums[1_0]"},

		// Scope crossing.
		{"scope/outer", "$$.message.id"},
		{"scope/outer-missing", "$$.nope"},
		{"scope/inner-not-outer", "$.a"},
		{"scope/outer-shadows", "$$.a"},

		// JS-only property traps: neither resolves to anything in Go, and a
		// naive property read in TS would return a function or a prototype.
		{"js-trap/constructor", "$.constructor"},
		{"js-trap/toString", "$.toString"},
		{"js-trap/own-proto-key", "$.__proto__"},

		// Coalesce.
		{"coalesce/first-wins", "$.a || $.nope"},
		{"coalesce/falls-through", "$.nope || $.a"},
		{"coalesce/default-literal", "$.nope || \"fallback\""},
		{"coalesce/empty-string-wins", "$.empty || \"fallback\""},
		{"coalesce/zero-wins", "$.zero || \"fallback\""},
		{"coalesce/false-wins", "$.f || \"fallback\""},
		{"coalesce/null-value-falls-through", "$.nullv || \"fallback\""},
		{"coalesce/three-terms", "$.nope || $.alsoNope || $.a"},
		{"coalesce/all-missing", "$.nope || $.alsoNope"},

		// Literals.
		{"literal/plain", "\"hello\""},
		{"literal/empty", "\"\""},
		{"literal/escaped-quote", "\"say \\\"hi\\\"\""},
		{"literal/tab", "\"a\\tb\""},
		{"literal/backslash", "\"\\\\\""},
		{"literal/unicode-escape", "\"\\u0041\""},
		{"literal/hex-ascii-escape", "\"\\x41\""},
		{"literal/astral-escape", "\"\\U0001F600\""},
		{"literal/bell", "\"\\a\""},
		{"literal/bad-escape", "\"\\z\""},
		{"literal/single-quote-escape", "\"\\'\""},
		{"literal/lone-surrogate", "\"\\ud800\""},
		{"literal/unterminated", "\"oops"},

		// Presence predicates.
		{"present/string", "present($.a)"},
		{"present/empty-string", "present($.empty)"},
		{"present/zero", "present($.zero)"},
		{"present/false", "present($.f)"},
		{"present/true", "present($.t)"},
		{"present/missing", "present($.nope)"},
		{"present/null", "present($.nullv)"},
		{"present/empty-array", "present($.emptyArr)"},
		{"present/empty-object", "present($.emptyObj)"},
		{"present/array", "present($.nums)"},
		{"present/outer", "present($$.message)"},
		{"absent/missing", "absent($.nope)"},
		{"absent/string", "absent($.a)"},
		{"present/never-falls-through", "present($.nope) || \"fallback\""},
		{"absent/never-falls-through", "absent($.a) || \"fallback\""},
		{"present/coalesce-inside-parens", "present($.nope || $.a)"},
		{"present/both-missing-inside-parens", "present($.nope || $.alsoNope)"},
		{"present/unclosed-is-a-bad-path", "present($.a"},
		{"nonempty/present", "nonempty($.a)"},
		{"nonempty/empty-falls-through", "nonempty($.empty) || \"fallback\""},
		{"nonempty/empty-alone", "nonempty($.empty)"},
		{"nonempty/zero-is-present", "nonempty($.zero) || \"fallback\""},
		{"nonempty/empty-array-falls-through", "nonempty($.emptyArr) || \"fallback\""},

		// Whitespace and empties.
		{"whitespace/padded", "  $.a  "},
		{"whitespace/padded-coalesce", "$.nope   ||   $.a"},
		{"empty/expression", ""},
		{"empty/whitespace-only", "   "},
	}

	out := make([]grammarCase, 0, len(cases)+1)
	for _, c := range cases {
		out = append(out, grammarCase{Name: c.name, Expr: c.expr, Inner: "frame", Outer: "parent"})
	}
	// One case needs a nil outer scope: `$$.` outside a for_each. Appended
	// rather than folded into the table because it is the only row whose scopes
	// differ, and a second scope column for one row would be noise.
	out = append(out, grammarCase{
		Name:  "scope/outer-is-nil-outside-for-each",
		Expr:  "$$.a",
		Inner: "frame",
		Outer: "",
	})
	return grammarFixture{Scopes: scopes, Cases: out}
}

// translateCase is one synthetic (profile, frame) pair and the events Go
// produced for it. Both the profile and the frame are authored as JSON so the
// two interpreters start from identical bytes — a Go struct literal would hand
// Go typed fields the TS side reconstructs by decoding, and the fixture would
// no longer be comparing the same input.
type translateCase struct {
	Name    string                      `json:"name"`
	Why     string                      `json:"why"`
	Profile *agentfamilies.FrameProfile `json:"profile"`
	Frame   map[string]any              `json:"frame"`
	Events  []fixtureEvent              `json:"events"`
}

func translateCases(t *testing.T) []translateCase {
	t.Helper()
	raw := []struct{ name, why, profile, frame string }{
		{
			"rules/empty-profile-falls-back-to-raw",
			"a profile with no rules keeps the bytes rather than dropping the frame",
			`{"profile_version": 1, "rules": []}`,
			`{"type": "surprise", "n": 1}`,
		},
		{
			"rules/no-match-falls-back-to-raw",
			"an unprofiled frame type stays in the transcript verbatim (ADR-010 D5)",
			`{"rules": [{"match": {"type": "known"}, "emit": {"kind": "text"}}]}`,
			`{"type": "unknown", "n": 1}`,
		},
		{
			"match/empty-matches-anything",
			"an empty match is a catch-all at the lowest possible specificity",
			`{"rules": [{"match": {}, "emit": {"kind": "catchall", "payload": {"t": "$.type"}}}]}`,
			`{"type": "whatever"}`,
		},
		{
			"match/empty-loses-to-a-keyed-rule",
			"specificity is keyset size, so a catch-all sits dormant when a real rule matches",
			`{"rules": [
				{"match": {}, "emit": {"kind": "catchall"}},
				{"match": {"type": "a"}, "emit": {"kind": "specific"}}
			]}`,
			`{"type": "a"}`,
		},
		{
			"match/most-specific-wins",
			"two keys beat one; the one-key rule does not also fire",
			`{"rules": [
				{"match": {"type": "system"}, "emit": {"kind": "system"}},
				{"match": {"type": "system", "subtype": "init"}, "emit": {"kind": "session.init"}}
			]}`,
			`{"type": "system", "subtype": "init"}`,
		},
		{
			"match/ties-fire-in-declaration-order",
			"an assistant frame's text walk and its usage rule both fire, in order",
			`{"rules": [
				{"match": {"type": "a"}, "emit": {"kind": "first"}},
				{"match": {"type": "a"}, "emit": {"kind": "second"}}
			]}`,
			`{"type": "a"}`,
		},
		{
			"match/absent-key-does-not-match",
			"a match key the frame lacks fails rather than comparing against nil",
			`{"rules": [{"match": {"type": "a", "missing": "x"}, "emit": {"kind": "no"}}]}`,
			`{"type": "a"}`,
		},
		{
			"match/dotted-key-walks-nested-objects",
			"JSON-RPC envelopes dispatch on a discriminator one level down",
			`{"rules": [
				{"match": {"method": "item/started", "params.item.type": "commandExecution"},
				 "emit": {"kind": "tool_call", "payload": {"id": "$.params.item.id"}}},
				{"match": {"method": "item/started", "params.item.type": "agentMessage"},
				 "emit": {"kind": "system"}}
			]}`,
			`{"method": "item/started", "params": {"item": {"type": "commandExecution", "id": "c1"}}}`,
		},
		{
			"match/dotted-key-missing-depth-fails",
			"a dotted key that runs out of object mid-walk does not match",
			`{"rules": [{"match": {"method": "m", "params.item.type": "x"}, "emit": {"kind": "no"}}]}`,
			`{"method": "m", "params": {"item": "not-an-object"}}`,
		},
		{
			"when-present/gated-off-emits-nothing-not-raw",
			"the author chose to skip; that is not the same as no rule matching",
			`{"rules": [{"match": {"type": "a"}, "when_present": "$.usage",
			  "emit": {"kind": "usage", "payload": {"in": "$.usage.input"}}}]}`,
			`{"type": "a"}`,
		},
		{
			"when-present/gated-on",
			"the same rule fires once the gated field arrives",
			`{"rules": [{"match": {"type": "a"}, "when_present": "$.usage",
			  "emit": {"kind": "usage", "payload": {"in": "$.usage.input"}}}]}`,
			`{"type": "a", "usage": {"input": 12}}`,
		},
		{
			"when-present/empty-string-is-present-enough",
			"when_present gates on non-nil, not on non-empty — an empty string passes",
			`{"rules": [{"match": {"type": "a"}, "when_present": "$.s", "emit": {"kind": "gated"}}]}`,
			`{"type": "a", "s": ""}`,
		},
		{
			"for-each/dispatches-sub-rules-per-element",
			"the canonical assistant content walk: text and tool_use from one frame",
			`{"rules": [{"match": {"type": "assistant"}, "for_each": "$.message.content",
			  "emit": {"kind": "unused"},
			  "sub_rules": [
				{"match": {"type": "text"}, "emit": {"kind": "text",
				 "payload": {"text": "$.text", "message_id": "$$.message.id"}}},
				{"match": {"type": "tool_use"}, "emit": {"kind": "tool_call",
				 "payload": {"id": "$.id", "name": "$.name"}}}
			  ]}]}`,
			`{"type": "assistant", "message": {"id": "msg_1", "content": [
				{"type": "text", "text": "hi"},
				{"type": "tool_use", "id": "t1", "name": "Read"},
				{"type": "unprofiled", "x": 1}
			]}}`,
		},
		{
			"for-each/non-array-source-emits-nothing",
			"a for_each whose source is a scalar is skipped, not raw-fallen-back",
			`{"rules": [{"match": {"type": "a"}, "for_each": "$.items",
			  "sub_rules": [{"match": {}, "emit": {"kind": "el"}}]}]}`,
			`{"type": "a", "items": "not-an-array"}`,
		},
		{
			"for-each/missing-source-emits-nothing",
			"same for an absent source",
			`{"rules": [{"match": {"type": "a"}, "for_each": "$.items",
			  "sub_rules": [{"match": {}, "emit": {"kind": "el"}}]}]}`,
			`{"type": "a"}`,
		},
		{
			"for-each/skips-non-object-elements",
			"a string in an array of blocks is skipped rather than emitted empty",
			`{"rules": [{"match": {"type": "a"}, "for_each": "$.items",
			  "sub_rules": [{"match": {}, "emit": {"kind": "el", "payload": {"v": "$.v"}}}]}]}`,
			`{"type": "a", "items": [{"v": 1}, "scalar", null, [2], {"v": 3}]}`,
		},
		{
			"for-each/without-sub-rules-emits-the-rules-own-emit",
			"less common but supported: one event per element from the rule itself",
			`{"rules": [{"match": {"type": "a"}, "for_each": "$.items",
			  "emit": {"kind": "el", "payload": {"v": "$.v", "parent": "$$.type"}}}]}`,
			`{"type": "a", "items": [{"v": 1}, {"v": 2}]}`,
		},
		{
			"for-each/first-matching-sub-rule-wins",
			"per element only one sub-rule fires, even when two match",
			`{"rules": [{"match": {"type": "a"}, "for_each": "$.items",
			  "sub_rules": [
				{"match": {}, "emit": {"kind": "first"}},
				{"match": {"type": "x"}, "emit": {"kind": "second"}}
			  ]}]}`,
			`{"type": "a", "items": [{"type": "x"}]}`,
		},
		{
			"for-each/sub-rule-when-present-gates",
			"a gated sub-rule drops its element without dropping the others",
			`{"rules": [{"match": {"type": "a"}, "for_each": "$.items",
			  "sub_rules": [{"match": {}, "when_present": "$.v",
			   "emit": {"kind": "el", "payload": {"v": "$.v"}}}]}]}`,
			`{"type": "a", "items": [{"v": 1}, {"other": 2}, {"v": 3}]}`,
		},
		{
			"emit/kindless-rule-emits-nothing",
			"a rule with no emit kind is a no-op, not a raw fallback",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": ""}}]}`,
			`{"type": "a"}`,
		},
		{
			"emit/producer-defaults-to-agent",
			"the historical attribution for agent-output frames",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "text"}}]}`,
			`{"type": "a"}`,
		},
		{
			"emit/producer-can-be-overridden",
			"lifecycle frames mark themselves producer=system",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "system", "producer": "system"}}]}`,
			`{"type": "a"}`,
		},
		{
			"payload/missing-fields-are-null-not-absent",
			"a nil field is a key carrying null; dropping it would be a different event",
			`{"rules": [{"match": {"type": "a"},
			  "emit": {"kind": "text", "payload": {"there": "$.x", "missing": "$.nope"}}}]}`,
			`{"type": "a", "x": "v"}`,
		},
		{
			"payload-expr/passes-the-whole-frame",
			"the legacy translator's raw-frame passthrough",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "system", "payload_expr": "$."}}]}`,
			`{"type": "a", "nested": {"k": "v"}}`,
		},
		{
			"payload-expr/non-map-yields-empty-payload",
			"defensive rather than a panic; surfaces as a parity finding",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "system", "payload_expr": "$.s"}}]}`,
			`{"type": "a", "s": "scalar"}`,
		},
		{
			"payload-expr/beats-payload-when-both-set",
			"documented precedence for a profile that declares both",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "system",
			  "payload": {"k": "\"from-payload\""}, "payload_expr": "$.obj"}}]}`,
			`{"type": "a", "obj": {"k": "from-expr"}}`,
		},
		{
			"payload-maps/projects-values-and-keeps-keys",
			"claude's modelUsage: keyed by data, re-shaped per value",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "usage",
			  "payload_maps": {"by_model": {"source": "$.modelUsage || $.model_usage",
			   "fields": {"input": "$.inputTokens || $.input_tokens", "output": "$.outputTokens"}}}}}]}`,
			`{"type": "a", "modelUsage": {"opus": {"inputTokens": 10, "outputTokens": 4},
			  "sonnet": {"input_tokens": 7}}}`,
		},
		{
			"payload-maps/absent-source-omits-the-field",
			"no per-model data and data for zero models are different claims",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "usage",
			  "payload_maps": {"by_model": {"source": "$.modelUsage", "fields": {"input": "$.i"}}}}}]}`,
			`{"type": "a"}`,
		},
		{
			"payload-maps/empty-source-projects-an-empty-map",
			"present but empty is the engine's claim, and it survives",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "usage",
			  "payload_maps": {"by_model": {"source": "$.modelUsage", "fields": {"input": "$.i"}}}}}]}`,
			`{"type": "a", "modelUsage": {}}`,
		},
		{
			"payload-maps/skips-non-object-values",
			"one malformed entry cannot void the whole map",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "usage",
			  "payload_maps": {"by_model": {"source": "$.m", "fields": {"input": "$.i"}}}}}]}`,
			`{"type": "a", "m": {"good": {"i": 1}, "bad": "scalar", "alsoBad": [1]}}`,
		},
		{
			"payload-maps/outer-scope-is-the-rules-own",
			"inside fields, $. is the value and $$. is the rule's scope",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "usage",
			  "payload_maps": {"by_model": {"source": "$.m",
			   "fields": {"input": "$.i", "frame_type": "$$.type"}}}}}]}`,
			`{"type": "a", "m": {"opus": {"i": 1}}}`,
		},
		{
			"payload-lists/projects-elements-in-order",
			"codex's plan steps: one field, not N events",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "plan",
			  "payload_lists": {"entries": {"source": "$.params.plan",
			   "fields": {"content": "$.step", "status": "$.status"}}}}}]}`,
			`{"type": "a", "params": {"plan": [{"step": "one", "status": "completed"},
			  {"step": "two", "status": "inProgress"}]}}`,
		},
		{
			"payload-lists/empty-source-projects-an-empty-list",
			"a plan with no steps is a claim the engine made, unlike a missing field",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "plan",
			  "payload_lists": {"entries": {"source": "$.plan", "fields": {"content": "$.step"}}}}}]}`,
			`{"type": "a", "plan": []}`,
		},
		{
			"payload-lists/absent-source-omits-the-field",
			"the frame carried no plan at all",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "plan",
			  "payload_lists": {"entries": {"source": "$.plan", "fields": {"content": "$.step"}}}}}]}`,
			`{"type": "a"}`,
		},
		{
			"payload-lists/non-array-source-omits-the-field",
			"a scalar where a list was declared is absent, not empty",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "plan",
			  "payload_lists": {"entries": {"source": "$.plan", "fields": {"content": "$.step"}}}}}]}`,
			`{"type": "a", "plan": "not-a-list"}`,
		},
		{
			"payload-lists/skips-non-object-elements",
			"the list shortens rather than holding a gap: no honest placeholder exists",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "plan",
			  "payload_lists": {"entries": {"source": "$.plan", "fields": {"content": "$.step"}}}}}]}`,
			`{"type": "a", "plan": [{"step": "one"}, "scalar", null, {"step": "two"}]}`,
		},
		{
			"payload/plain-field-wins-a-projection-collision",
			"the simpler declaration wins; a profile declaring both has a bug",
			`{"rules": [{"match": {"type": "a"}, "emit": {"kind": "usage",
			  "payload": {"by_model": "\"plain\""},
			  "payload_maps": {"by_model": {"source": "$.m", "fields": {"i": "$.i"}}}}}]}`,
			`{"type": "a", "m": {"opus": {"i": 1}}}`,
		},
	}

	out := make([]translateCase, 0, len(raw))
	for _, c := range raw {
		var profile agentfamilies.FrameProfile
		if err := json.Unmarshal([]byte(c.profile), &profile); err != nil {
			t.Fatalf("%s: decode profile: %v", c.name, err)
		}
		out = append(out, translateCase{
			Name:    c.name,
			Why:     c.why,
			Profile: &profile,
			Frame:   decodeScope(t, c.frame),
		})
	}
	return out
}

func decodeScope(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode fixture scope: %v", err)
	}
	return m
}
