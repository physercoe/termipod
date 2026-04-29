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
		"junk",          // no leading $.
		"$x",            // missing dot
		"$.[",           // bare bracket
		"$.tools[abc]",  // non-numeric index
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
