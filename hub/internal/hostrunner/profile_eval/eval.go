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
//	term   := path | string
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

// evalTerm dispatches one term — either a quoted literal or a path.
// Anything not parseable returns nil (caller-side diagnostics, not a
// panic).
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
	if strings.HasPrefix(term, "$$.") {
		return walkPath(outer, term[3:])
	}
	if strings.HasPrefix(term, "$.") {
		return walkPath(inner, term[2:])
	}
	return nil
}

// splitCoalesce splits "a || b || c" into ["a", "b", "c"] without
// breaking inside quoted literals. The grammar doesn't allow nested
// expressions, so a flat scan is sufficient.
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
