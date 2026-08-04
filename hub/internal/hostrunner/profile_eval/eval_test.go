package profile_eval

import (
	"reflect"
	"testing"
)

// TestEval_PathAccess covers the bread-and-butter case: dotted path
// against a nested map. Missing keys at any depth return nil, not an
// error or panic — the rule evaluator treats nil as "no value, try
// the next coalesce term."
func TestEval_PathAccess(t *testing.T) {
	frame := map[string]any{
		"type":       "rate_limit_event",
		"session_id": "abc-123",
		"rate_limit_info": map[string]any{
			"status":        "allowed",
			"rateLimitType": "five_hour",
			"resetsAt":      float64(1777443000),
		},
	}
	cases := []struct {
		expr string
		want any
	}{
		{"$.type", "rate_limit_event"},
		{"$.session_id", "abc-123"},
		{"$.rate_limit_info.status", "allowed"},
		{"$.rate_limit_info.rateLimitType", "five_hour"},
		{"$.rate_limit_info.resetsAt", float64(1777443000)},
		// Missing keys propagate nil cleanly, no panic.
		{"$.nonexistent", nil},
		{"$.rate_limit_info.gone", nil},
		{"$.session_id.too.deep", nil}, // walks into a string → not a map → nil
	}
	for _, c := range cases {
		got := Eval(c.expr, frame, nil)
		if got != c.want {
			t.Errorf("Eval(%q) = %v; want %v", c.expr, got, c.want)
		}
	}
	// Empty path returns the scope itself — checked separately because
	// maps aren't comparable with == and the table form would panic on
	// the comparison.
	if got := Eval("$.", frame, nil); !mapsEqual(got, frame) {
		t.Errorf(`Eval("$.") = %v; want frame itself`, got)
	}
}

// TestEval_Coalesce locks the fallback semantics ADR-010 leans on for
// the SDK-shape variants. The expression `a || b || c` returns the
// first non-nil term, where "non-nil" includes empty strings — those
// are intentional defaults.
func TestEval_Coalesce(t *testing.T) {
	frame := map[string]any{
		"present": "first",
		"empty":   "",
		"info": map[string]any{
			"new_path": "ok",
		},
	}
	cases := []struct {
		expr string
		want any
	}{
		// First branch present.
		{`$.present || $.missing || "default"`, "first"},
		// First missing, second present.
		{`$.missing || $.present || "default"`, "first"},
		// Both missing, literal default kicks in.
		{`$.gone || $.also_gone || "default"`, "default"},
		// Empty string is non-nil and wins (intentional — operators
		// can model "the SDK does emit '' for this field").
		{`$.empty || "default"`, ""},
		// Three-term chain, only third hits.
		{`$.gone || $.also_gone || $.info.new_path`, "ok"},
		// All missing, no literal → nil.
		{`$.gone || $.also_gone`, nil},
		// Whitespace tolerance.
		{`$.gone   ||    $.present`, "first"},
	}
	for _, c := range cases {
		got := Eval(c.expr, frame, nil)
		if got != c.want {
			t.Errorf("Eval(%q) = %v; want %v", c.expr, got, c.want)
		}
	}
}

// TestEval_OuterScope verifies $$ resolves against the outer frame
// during for_each iteration. Used by the assistant.message.content[]
// rule to lift message_id from the parent frame onto each per-block
// emit.
func TestEval_OuterScope(t *testing.T) {
	outer := map[string]any{
		"message": map[string]any{
			"id":    "msg_42",
			"model": "claude-opus-4-7",
		},
	}
	inner := map[string]any{
		"type": "text",
		"text": "hello",
	}
	cases := []struct {
		expr string
		want any
	}{
		{"$.text", "hello"},
		{"$$.message.id", "msg_42"},
		{"$$.message.model", "claude-opus-4-7"},
		{`$.text || $$.message.id`, "hello"},
		{`$.missing || $$.message.id`, "msg_42"},
		// Outer scope nil propagates through.
		{"$$.missing", nil},
	}
	for _, c := range cases {
		if got := Eval(c.expr, inner, outer); got != c.want {
			t.Errorf("Eval(%q) = %v; want %v", c.expr, got, c.want)
		}
	}
	// $$ with nil outer returns nil, doesn't panic.
	if got := Eval("$$.message.id", inner, nil); got != nil {
		t.Errorf("$$ against nil outer = %v; want nil", got)
	}
}

// TestEval_ArrayIndex covers the `name[N]` segment form. Out-of-bounds
// and type mismatches all collapse to nil (consistent with the
// "missing key" contract).
func TestEval_ArrayIndex(t *testing.T) {
	frame := map[string]any{
		"tools": []any{"Read", "Write", "Bash"},
		"models": []any{
			map[string]any{"name": "opus", "ctx": float64(200000)},
			map[string]any{"name": "sonnet", "ctx": float64(200000)},
		},
		"not_a_list": "scalar",
	}
	cases := []struct {
		expr string
		want any
	}{
		{"$.tools[0]", "Read"},
		{"$.tools[2]", "Bash"},
		{"$.models[0].name", "opus"},
		{"$.models[1].ctx", float64(200000)},
		// Out of bounds.
		{"$.tools[99]", nil},
		// Indexing a scalar (not a list).
		{"$.not_a_list[0]", nil},
		// Negative index treated as bad bracket; falls through to nil.
		{"$.tools[-1]", nil},
	}
	for _, c := range cases {
		got := Eval(c.expr, frame, nil)
		if got != c.want {
			t.Errorf("Eval(%q) = %v; want %v", c.expr, got, c.want)
		}
	}
}

// TestEval_Malformed catches typos and returns nil rather than
// panicking. Operators learn about syntax errors via the caller's
// diagnostic logging, not by tearing down the host-runner.
func TestEval_Malformed(t *testing.T) {
	frame := map[string]any{"x": "ok"}
	for _, expr := range []string{
		"",
		"   ",
		"junk",         // no leading $.
		"$x",           // missing dot
		"$.[",          // bare bracket
		"$.tools[abc]", // non-numeric index
		"$.unterminated_string || \"oops",
		`||`,
		`$. || $.`, // empty paths between coalesces — both resolve to frame
	} {
		_ = Eval(expr, frame, nil) // assert no panic
	}
}

// mapsEqual is a thin reflect.DeepEqual wrapper kept under that name
// so the call sites read as the equality intent ("are these the same
// map?") rather than as a generic deep-equality check.
func mapsEqual(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

// ── present / absent / nonempty (vision-parity E1) ───────────────────
//
// These exist because typed payloads carry booleans DERIVED from
// whether a source field was populated (claude's
// `thought.signature_present`). Without them a rule could only ship the
// raw value, which is a different field with a different meaning — and
// the hand-written translator, which can write `x != ""` in Go, would
// be unmatchable.

func TestEval_PresentAbsent(t *testing.T) {
	frame := map[string]any{
		"sig":    "EqQBCkY",
		"empty":  "",
		"zero":   float64(0),
		"no":     false,
		"yes":    true,
		"arr":    []any{},
		"filled": []any{"x"},
		"obj":    map[string]any{},
	}
	cases := []struct {
		expr string
		want any
	}{
		{"present($.sig)", true},
		{"absent($.sig)", false},
		{"present($.empty)", false}, // "" is how the wire says "nothing here"
		{"absent($.empty)", true},
		{"present($.missing)", false},
		{"absent($.missing)", true},
		// Zero is a real measurement, not an absence — a token count of 0
		// must not read as "no data".
		{"present($.zero)", true},
		{"present($.no)", false}, // an unset flag is an absent flag
		{"present($.yes)", true},
		{"present($.arr)", false},
		{"present($.filled)", true},
		{"present($.obj)", false},
	}
	for _, c := range cases {
		if got := Eval(c.expr, frame, nil); got != c.want {
			t.Errorf("Eval(%q) = %v (%T); want %v", c.expr, got, got, c.want)
		}
	}
}

func TestEval_PredicatesNeverFallThroughCoalesce(t *testing.T) {
	frame := map[string]any{"x": "here"}
	// `false` is a real answer, so a coalesce must STOP at it. If the
	// predicate returned nil for "not present" the default would fire
	// and the payload would carry a string where a bool was promised.
	if got := Eval(`present($.nope) || "fallback"`, frame, nil); got != false {
		t.Errorf(`present($.nope) || "fallback" = %v; want false`, got)
	}
	if got := Eval(`absent($.x) || "fallback"`, frame, nil); got != false {
		t.Errorf(`absent($.x) || "fallback" = %v; want false`, got)
	}
}

func TestEval_NonemptyFallsThroughOnEmpty(t *testing.T) {
	frame := map[string]any{"empty": "", "text": "real"}
	// The whole point: a bare coalesce STOPS at an empty string, because
	// "" is a perfectly good non-nil value. nonempty() is how a rule says
	// "treat empty as missing" — claude's signed `thinking` blocks are
	// the case that needs it.
	if got := Eval(`$.empty || "dflt"`, frame, nil); got != "" {
		t.Errorf(`bare coalesce should stop at ""; got %v`, got)
	}
	if got := Eval(`nonempty($.empty) || "dflt"`, frame, nil); got != "dflt" {
		t.Errorf(`nonempty($.empty) || "dflt" = %v; want "dflt"`, got)
	}
	if got := Eval(`nonempty($.text) || "dflt"`, frame, nil); got != "real" {
		t.Errorf(`nonempty($.text) || "dflt" = %v; want "real"`, got)
	}
	if got := Eval(`nonempty($.missing) || "dflt"`, frame, nil); got != "dflt" {
		t.Errorf(`nonempty($.missing) || "dflt" = %v; want "dflt"`, got)
	}
}

func TestEval_PredicateArgMayCoalesce(t *testing.T) {
	// splitCoalesce must not break INSIDE the parens: `present(a || b)` is
	// one term. Before parens entered the grammar its depth counter was
	// declared, read, and never incremented, so this would have split into
	// "present($.missing" and "$.sig)" and resolved to nil.
	frame := map[string]any{"sig": "x"}
	if got := Eval(`present($.missing || $.sig)`, frame, nil); got != true {
		t.Errorf("present($.missing || $.sig) = %v; want true", got)
	}
	if got := Eval(`present($.missing || $.alsoMissing)`, frame, nil); got != false {
		t.Errorf("present($.missing || $.alsoMissing) = %v; want false", got)
	}
}

func TestEval_PredicateMalformedIsNotAPanic(t *testing.T) {
	frame := map[string]any{"x": "ok"}
	for _, expr := range []string{
		"present($.x", // no closing paren → falls through to the path branch
		"present()",   // empty arg
		"absent(",     //
		"nonempty(",   //
		"presentish($.x)",
	} {
		_ = Eval(expr, frame, nil) // assert no panic
	}
}
