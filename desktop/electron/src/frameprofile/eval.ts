/// The frame-profile expression subset (ADR-010) — the TypeScript twin of
/// `hub/internal/hostrunner/profile_eval/eval.go`. Authoring reference:
/// `docs/reference/frame-profiles.md` §3.
///
/// Grammar:
///
///   expr   := term ( '||' term )*
///   term   := path | string | pred
///   pred   := ('present' | 'absent' | 'nonempty') '(' expr ')'
///   path   := ('$.' | '$$.') segments
///   seg    := identifier | identifier '[' digits ']'
///   string := '"' anything-not-quote '"'
///
/// This is a port, not a reimplementation: every resolution rule below exists
/// because the Go evaluator has it, and the two are pinned against each other
/// by the generated grammar fixture (`parity.test.ts`). Where a rule looks
/// arbitrary, it is Go's, and changing it here breaks parity rather than
/// improving anything.
///
/// **Null, never undefined.** Go's "nothing here" is `nil`, which marshals to
/// JSON `null`. Every path that finds nothing returns `null` so that an
/// emitted payload field is `{"x": null}` and not a key that vanishes — the
/// two are different events, and a deep-equal against the Go fixture sees the
/// difference.
///
/// **Known narrowing from Go.** `strconv.Unquote` resolves `\xHH` and octal
/// escapes to raw BYTES, which a UTF-16 JS string cannot hold above 0x7F.
/// Those escapes resolve here only when they name an ASCII code point, and are
/// malformed (→ null) above it: inventing U+00FF for byte 0xFF would be a
/// silent lie in a payload field. No shipped profile uses either escape — every
/// literal in `agent_families.yaml` is a bare identifier.

import type { Scope } from './types.ts';

/// Resolve `expr` against the given scopes. `inner` is the active scope (the
/// `for_each` element, or the frame itself outside one); `outer` is the parent
/// frame referenced by `$$.`. Either may be null.
///
/// Returns null for empty expressions, malformed paths, missing keys,
/// out-of-bounds indices and type mismatches. The caller decides whether null
/// is signal or noise.
export function evalExpr(expr: string, inner: Scope, outer: Scope): unknown {
  const trimmed = expr.trim();
  if (trimmed === '') return null;
  for (const term of splitCoalesce(trimmed)) {
    const v = evalTerm(term.trim(), inner, outer);
    if (v !== null) return v;
  }
  return null;
}

/// Dispatch one term — a quoted literal, a predicate, or a path. Anything not
/// parseable returns null; diagnostics are the caller's, not a throw.
function evalTerm(term: string, inner: Scope, outer: Scope): unknown {
  if (term === '') return null;
  if (term[0] === '"') return unquote(term);

  const present = predicateArg(term, 'present');
  if (present !== null) return isPresent(evalExpr(present, inner, outer));

  const absent = predicateArg(term, 'absent');
  if (absent !== null) return !isPresent(evalExpr(absent, inner, outer));

  const nonempty = predicateArg(term, 'nonempty');
  if (nonempty !== null) {
    const v = evalExpr(nonempty, inner, outer);
    return isPresent(v) ? v : null;
  }

  if (term.startsWith('$$.')) return walkPath(outer, term.slice(3));
  if (term.startsWith('$.')) return walkPath(inner, term.slice(2));
  return null;
}

/// Match `name(<expr>)` and return the inner expression, or null when the term
/// isn't that predicate. A flat prefix/suffix check rather than a parser
/// because the grammar has no nested predicates — `present(...)` takes a path
/// or a coalesce, never another predicate. A missing closing paren fails the
/// match and falls through to the path branch, where it resolves to null: the
/// same silent-but-observable failure every other malformed expression gets.
function predicateArg(term: string, name: string): string | null {
  if (!term.startsWith(`${name}(`) || !term.endsWith(')')) return null;
  return term.slice(name.length + 1, term.length - 1);
}

/// Does a resolved value count as "carrying something"?
///
/// Empty string, empty array, empty object and `false` are all absent: the wire
/// uses each of them to mean "no value here". Zero is NOT absent — a token
/// count of 0 is a real measurement.
///
/// Exported for the same reason Go exports it: a second definition of
/// "present" would be a divergence no output-comparing test could see.
export function isPresent(v: unknown): boolean {
  if (v === null || v === undefined) return false;
  if (typeof v === 'string') return v !== '';
  if (typeof v === 'boolean') return v;
  if (Array.isArray(v)) return v.length > 0;
  if (typeof v === 'object') return Object.keys(v).length > 0;
  return true;
}

/// Split `a || b || c` into its terms without breaking inside a quoted literal
/// or inside a predicate's parentheses. `present($.a || $.b)` is ONE term whose
/// `||` belongs to the predicate; splitting there would hand `evalTerm` two
/// halves of a broken call.
function splitCoalesce(expr: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let inStr = false;
  let start = 0;
  for (let i = 0; i < expr.length; i++) {
    const c = expr[i];
    if (c === '"') {
      // Honor backslash escapes so an embedded `\"` doesn't flip us out early.
      if (inStr && i > 0 && expr[i - 1] === '\\') continue;
      inStr = !inStr;
    } else if (c === '(') {
      if (!inStr) depth++;
    } else if (c === ')') {
      if (!inStr && depth > 0) depth--;
    } else if (c === '|') {
      if (inStr || depth > 0) continue;
      if (i + 1 < expr.length && expr[i + 1] === '|') {
        out.push(expr.slice(start, i));
        i++; // skip the second '|'
        start = i + 1;
      }
    }
  }
  out.push(expr.slice(start));
  return out;
}

/// Dereference a dotted path against `root`. Each segment is a bare identifier
/// or `name[N]` for indexed array access. Returns null on missing keys, null
/// scopes, type mismatches or malformed segments. An empty path returns `root`
/// itself, which is how `$.` names the scope.
function walkPath(root: Scope, path: string): unknown {
  if (path === '') return root;
  let cur: unknown = root;
  for (const seg of path.split('.')) {
    if (cur === null || cur === undefined) return null;
    const [name, idx, hasIdx] = splitSegment(seg);
    const m = asMap(cur);
    if (m === null) return null;
    // hasOwnProperty, not `in` or a bare read: Go's map lookup finds nothing
    // for `$.constructor`, and an inherited property here would resolve to a
    // function that no JSON frame ever carried.
    if (!Object.prototype.hasOwnProperty.call(m, name)) return null;
    let v: unknown = m[name];
    if (hasIdx) {
      if (!Array.isArray(v)) return null;
      // `idx < 0` is Go's guard, and here it is shadowed: `arr[-1]` is already
      // undefined in JS and normalizes to null two lines down. Kept because
      // this is a port — the line exists in eval.go, and a reader diffing the
      // two should find them the same shape. Mutating it away does not fail a
      // test, which is a property of JS arrays and not a gap in the fixture.
      if (idx < 0 || idx >= v.length) return null;
      v = v[idx];
    }
    cur = v === undefined ? null : v;
  }
  return cur === undefined ? null : cur;
}

/// Narrow to Go's `map[string]any`: a JSON object, which excludes arrays and
/// null. Go's type assertion fails for a `[]any`, so this must too.
function asMap(v: unknown): Record<string, unknown> | null {
  if (v === null || typeof v !== 'object' || Array.isArray(v)) return null;
  return v as Record<string, unknown>;
}

/// Split a path segment into (name, index, indexed?). `foo` → no index;
/// `foo[3]` → index 3. Malformed brackets fall back to the literal name, which
/// is then almost certainly missing — failure stays silent but observable.
///
/// The `]` is searched from the start of the segment, not from the `[`, so a
/// segment like `a]b[0` fails the ordering check exactly as Go's does.
function splitSegment(seg: string): [string, number, boolean] {
  const open = seg.indexOf('[');
  if (open < 0) return [seg, 0, false];
  const close = seg.indexOf(']');
  // `close <= open + 1` rejects `foo[]`; like the negative-index guard above it
  // is shadowed here, because the empty slice it prevents would fail `atoi`
  // anyway. Same reason for keeping it: the port stays line-comparable to Go.
  if (close < 0 || close <= open + 1) return [seg, 0, false];
  const idx = atoi(seg.slice(open + 1, close));
  if (idx === null) return [seg, 0, false];
  return [seg.slice(0, open), idx, true];
}

/// `strconv.Atoi`: an optional sign then digits, nothing else. No whitespace,
/// no hex, no partial parse — `parseInt` would accept `3a` and Go does not.
/// Out-of-range values fail, which sends the segment down the literal-name
/// path just as Go's range error does.
function atoi(s: string): number | null {
  if (!/^[+-]?\d+$/.test(s)) return null;
  const n = Number(s);
  return Number.isSafeInteger(n) ? n : null;
}

/// `strconv.Unquote` for a double-quoted Go string literal. Returns null when
/// the literal is malformed, which makes the term resolve to null — the same
/// outcome Go gives, since its error return is discarded there.
///
/// See the module header for the deliberate narrowing on `\x` and octal.
function unquote(lit: string): string | null {
  if (lit.length < 2 || !lit.startsWith('"') || !lit.endsWith('"')) return null;
  const body = lit.slice(1, -1);
  let out = '';
  for (let i = 0; i < body.length; i++) {
    const c = body[i];
    // An unescaped quote would have ended the literal; a raw newline is
    // illegal in a Go interpreted string literal.
    if (c === '"' || c === '\n') return null;
    if (c !== '\\') {
      out += c;
      continue;
    }
    i++;
    if (i >= body.length) return null;
    const esc = body[i];
    switch (esc) {
      case 'a': out += '\x07'; break;
      case 'b': out += '\b'; break;
      case 'f': out += '\f'; break;
      case 'n': out += '\n'; break;
      case 'r': out += '\r'; break;
      case 't': out += '\t'; break;
      case 'v': out += '\v'; break;
      case '\\': out += '\\'; break;
      case '"': out += '"'; break;
      case 'x':
      case 'u':
      case 'U': {
        const width = esc === 'x' ? 2 : esc === 'u' ? 4 : 8;
        const hex = body.slice(i + 1, i + 1 + width);
        if (hex.length !== width || !/^[0-9a-fA-F]+$/.test(hex)) return null;
        const code = parseInt(hex, 16);
        // `\x` names a byte, not a code point: above 0x7F there is no faithful
        // UTF-16 answer, so treat it as malformed rather than guess one.
        if (esc === 'x' && code > 0x7f) return null;
        if (code > 0x10ffff || (code >= 0xd800 && code <= 0xdfff)) return null;
        out += String.fromCodePoint(code);
        i += width;
        break;
      }
      default: {
        // Octal is a byte too, with the same ceiling as `\x`.
        if (esc < '0' || esc > '7') return null;
        const oct = body.slice(i, i + 3);
        if (oct.length !== 3 || !/^[0-7]{3}$/.test(oct)) return null;
        const code = parseInt(oct, 8);
        if (code > 0x7f) return null;
        out += String.fromCharCode(code);
        i += 2;
        break;
      }
    }
  }
  return out;
}
