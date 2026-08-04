// Package profile_eval evaluates the small expression subset used by
// frame profiles (ADR-010). The grammar is intentionally tiny —
// pure-data lookups with one fallback operator — because every
// translation rule in driver_stdio.go's translate() fits within it
// today, and a richer language would inflate both the binary and the
// authoring learning curve.
//
// Grammar:
//
//	expr   := term ( '||' term )*
//	term   := path | string | pred
//	pred   := ('present' | 'absent' | 'nonempty') '(' expr ')'
//	path   := ('$.' | '$$.') segments
//	seg    := identifier | identifier '[' digits ']'
//	string := '"' anything-not-quote '"'
//
// Semantics:
//
//   - $.a.b.c       walks dotted keys against the inner scope. Missing
//                   keys at any depth return nil (no error).
//   - $.a[0]        indexed array access. Out-of-bounds returns nil.
//   - $$.x          path access against the outer scope (the frame
//                   above a for_each iteration).
//   - "literal"     a string literal; supports escaped quotes and
//                   backslashes via the standard Go strconv.Unquote.
//   - a || b || "x" coalesce: returns the first non-nil term. A
//                   trailing string literal acts as a default.
//   - present(x)    `true` when x resolves to a non-empty value,
//                   `false` otherwise. absent(x) is its negation.
//                   Never nil, so a coalesce never falls past it.
//   - nonempty(x)   x when present, else **nil** — so it DOES fall
//                   through a coalesce: `nonempty($.a) || "dflt"`
//                   takes the default for an empty string, which bare
//                   `$.a || "dflt"` does not (an empty string is a
//                   perfectly good non-nil value).
//
// present/absent exist because several typed payloads carry a BOOLEAN
// derived from whether a source field was populated — claude's
// `thought.signature_present` / `thought.marker_only` are the reason
// they were added — and without them those rules could only ship the
// raw value, which is a different field with a different meaning. A
// hand-written translator writes `x != ""` in Go; a profile needs a
// way to say the same thing, or the two paths cannot agree.
//
// "Non-empty" rather than merely "non-nil": an empty string, an empty
// array and an empty map are all things the wire uses to mean "nothing
// here", and a profile author asking `present(...)` means the field
// carries something. `false` is likewise not present — it is the
// absence of the flag.
//
// Eval is deliberately strict about syntax — a malformed expression
// returns nil and is logged once per evaluation by the caller, so
// operators see "rule X produced nil for field Y" instead of "no rule
// matched at all".
package profile_eval

import (
	"strconv"
	"strings"
)

// Eval resolves expr against the given scopes. inner is the active
// scope (the for_each element, or the frame itself outside of one);
// outer is the parent frame referenced by $$. Either may be nil; nil
// scopes resolve to nil for all paths into them.
//
// Returns nil for: empty expressions, malformed paths, missing keys,
// out-of-bounds indices, type mismatches (e.g. indexing into a
// non-array). The caller decides whether nil is signal or noise.
func Eval(expr string, inner, outer map[string]any) any {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	for _, term := range splitCoalesce(expr) {
		if v := evalTerm(strings.TrimSpace(term), inner, outer); v != nil {
			return v
		}
	}
	return nil
}

// evalTerm dispatches one term — a quoted literal, a present/absent
// predicate, or a path. Anything not parseable returns nil
// (caller-side diagnostics, not a panic).
func evalTerm(term string, inner, outer map[string]any) any {
	if term == "" {
		return nil
	}
	if term[0] == '"' {
		s, err := strconv.Unquote(term)
		if err != nil {
			return nil
		}
		return s
	}
	if arg, ok := predicateArg(term, "present"); ok {
		return IsPresent(Eval(arg, inner, outer))
	}
	if arg, ok := predicateArg(term, "absent"); ok {
		return !IsPresent(Eval(arg, inner, outer))
	}
	if arg, ok := predicateArg(term, "nonempty"); ok {
		if v := Eval(arg, inner, outer); IsPresent(v) {
			return v
		}
		return nil
	}
	if strings.HasPrefix(term, "$$.") {
		return walkPath(outer, term[3:])
	}
	if strings.HasPrefix(term, "$.") {
		return walkPath(inner, term[2:])
	}
	return nil
}

// predicateArg matches `name(<expr>)` and returns the inner expression.
// The scan is a flat prefix/suffix check rather than a parser because
// the grammar has no nested predicates: `present(...)` takes a path or
// a coalesce, never another predicate. A missing closing paren fails
// the match and the term falls through to the path branch, where it
// resolves to nil — the same silent-but-observable failure every other
// malformed expression gets.
func predicateArg(term, name string) (string, bool) {
	if !strings.HasPrefix(term, name+"(") || !strings.HasSuffix(term, ")") {
		return "", false
	}
	return term[len(name)+1 : len(term)-1], true
}

// IsPresent reports whether a resolved value counts as "carrying
// something". Exported because the hand-written translators need the
// identical predicate to stay byte-compatible with the profile path —
// two definitions of "present" would be a divergence the parity test
// could not see, since it compares outputs, not rules.
//
// Empty string, empty array, empty map and `false` are all absent: the
// wire uses each of them to mean "no value here". Zero is NOT absent —
// a token count of 0 is a real measurement.
func IsPresent(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}

// splitCoalesce splits "a || b || c" into ["a", "b", "c"] without
// breaking inside quoted literals or inside a predicate's parentheses.
//
// `depth` counts parens: `present($.a || $.b)` is ONE term whose `||`
// belongs to the predicate, so splitting it there would hand evalTerm
// two halves of a broken call. (The counter predates present/absent —
// it was declared and read but never incremented, so until parens
// entered the grammar it could only ever be 0.)
func splitCoalesce(expr string) []string {
	var out []string
	depth := 0
	inStr := false
	start := 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '"':
			// Toggle string state, honoring backslash escapes so an
			// embedded `\"` doesn't flip us out prematurely.
			if inStr && i > 0 && expr[i-1] == '\\' {
				continue
			}
			inStr = !inStr
		case '(':
			if !inStr {
				depth++
			}
		case ')':
			if !inStr && depth > 0 {
				depth--
			}
		case '|':
			if inStr || depth > 0 {
				continue
			}
			if i+1 < len(expr) && expr[i+1] == '|' {
				out = append(out, expr[start:i])
				i++ // skip second '|'
				start = i + 1
			}
		}
	}
	out = append(out, expr[start:])
	return out
}

// walkPath dereferences a dotted path against root. Each segment is
// either a bare identifier or `name[N]` for indexed array access.
// Returns nil on missing keys, nil scopes, type mismatches, or
// malformed segments. Empty path returns root itself (so `$.` is the
// scope itself — useful inside for_each rules that emit the element
// verbatim).
func walkPath(root any, path string) any {
	if path == "" {
		return root
	}
	cur := any(root)
	for _, seg := range strings.Split(path, ".") {
		if cur == nil {
			return nil
		}
		name, idx, hasIdx := splitSegment(seg)
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		v, present := m[name]
		if !present {
			return nil
		}
		if hasIdx {
			arr, ok := v.([]any)
			if !ok {
				return nil
			}
			if idx < 0 || idx >= len(arr) {
				return nil
			}
			v = arr[idx]
		}
		cur = v
	}
	return cur
}

// splitSegment splits a path segment into (name, index, indexed?).
// "foo" → ("foo", 0, false). "foo[3]" → ("foo", 3, true). Malformed
// brackets fall back to literal name (so a typo like "foo[" returns
// the bare key, which is then almost certainly nil — failure is
// silent but observable).
func splitSegment(seg string) (string, int, bool) {
	open := strings.IndexByte(seg, '[')
	if open < 0 {
		return seg, 0, false
	}
	close := strings.IndexByte(seg, ']')
	if close < 0 || close <= open+1 {
		return seg, 0, false
	}
	idx, err := strconv.Atoi(seg[open+1 : close])
	if err != nil {
		return seg, 0, false
	}
	return seg[:open], idx, true
}
