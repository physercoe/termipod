/// Stack-trace parsing for the Inspect (J3) trace lens — pure functions, no deps.
///
/// No viable OSS component parses multi-language traces the way the director's
/// job needs ("jump `file:line`, correlate a failure against the code"), so we
/// hand-roll compact parsers for the four traces that actually show up in this
/// workflow: Python tracebacks, Rust panics + backtraces, Go goroutine dumps,
/// and V8/Node JS stacks. Each is a pure `string → TraceFrame[]`; `parseTrace`
/// dispatches by trying all of them and keeping the richest parse.
///
/// Frames are ordered **innermost-first** (the failing site at the top of the
/// panel), regardless of the language's native ordering — Python prints
/// outermost-first ("most recent call last"), so that parse is reversed.

export interface TraceFrame {
  /// The source path exactly as the trace printed it (may be relative,
  /// absolute, or a `<frozen ...>` pseudo-path). Resolution to a real file is
  /// the surface's job, not the parser's.
  file: string;
  line: number;
  /// The function / method name, when the format carries one.
  func?: string;
  /// A best-effort "this is library/stdlib, not the user's code" flag, so the
  /// lens can collapse the noise by default.
  lib: boolean;
  /// The raw source line(s) this frame came from, for display/debugging.
  raw: string;
}

export type TraceKind = 'python' | 'rust' | 'go' | 'js';

export interface ParsedTrace {
  kind: TraceKind;
  frames: TraceFrame[];
}

// Path fragments that mark a frame as library/runtime rather than user code.
const LIB_MARKERS = [
  'site-packages',
  'dist-packages',
  'lib/python',
  'lib64/python',
  '<frozen',
  'node_modules',
  'node:internal',
  '/rustc/',
  '/cargo/registry/',
  'runtime/',
  'GOROOT',
  '/usr/lib/',
  '/usr/local/go/',
];

function isLib(file: string): boolean {
  return LIB_MARKERS.some((m) => file.includes(m));
}

// ── Python ──────────────────────────────────────────────────────────────────
// Frames are `  File "path", line N, in func`, optionally followed by the
// offending source line. Python prints outermost-first; we reverse to
// innermost-first.
const PY_FRAME = /^\s*File "(.+?)", line (\d+)(?:, in (.+))?\s*$/;

export function parsePython(text: string): TraceFrame[] {
  const frames: TraceFrame[] = [];
  for (const raw of text.split('\n')) {
    const m = PY_FRAME.exec(raw);
    if (m === null) continue;
    const file = m[1];
    frames.push({ file, line: Number(m[2]), func: m[3]?.trim(), lib: isLib(file), raw: raw.trim() });
  }
  return frames.reverse();
}

// ── Rust ────────────────────────────────────────────────────────────────────
// `thread 'main' panicked at src/main.rs:10:5:` is the primary site; a captured
// backtrace adds `   N: 0x… - func` / `             at ./path/to/file.rs:line`
// pairs (already innermost-first). We take the panic site first, then any
// `at FILE:LINE` frames in order.
const RS_PANIC = /panicked at (.+?):(\d+):(\d+)/;
const RS_AT = /^\s*at (.+?):(\d+)(?::\d+)?\s*$/;
const RS_FUNC = /^\s*\d+:\s+(?:0x[0-9a-f]+ - )?(.+?)\s*$/;

export function parseRust(text: string): TraceFrame[] {
  const lines = text.split('\n');
  const frames: TraceFrame[] = [];
  const panic = lines.map((l) => RS_PANIC.exec(l)).find((m) => m !== null);
  if (panic) {
    frames.push({ file: panic[1], line: Number(panic[2]), lib: isLib(panic[1]), raw: panic[0].trim() });
  }
  let pendingFunc: string | undefined;
  for (const raw of lines) {
    const at = RS_AT.exec(raw);
    if (at !== null) {
      frames.push({ file: at[1], line: Number(at[2]), func: pendingFunc, lib: isLib(at[1]), raw: raw.trim() });
      pendingFunc = undefined;
      continue;
    }
    const fn = RS_FUNC.exec(raw);
    if (fn !== null) pendingFunc = fn[1];
  }
  return frames;
}

// ── Go ──────────────────────────────────────────────────────────────────────
// A goroutine dump alternates `funcname(args)` with `\t/abs/path/file.go:line
// +0xNN`. The file line is what we anchor on; the preceding non-tab line is the
// function. Already innermost-first.
const GO_FILE = /^\t(.+?\.go):(\d+)(?:\s+\+0x[0-9a-f]+)?\s*$/;

export function parseGo(text: string): TraceFrame[] {
  const lines = text.split('\n');
  const frames: TraceFrame[] = [];
  for (let i = 0; i < lines.length; i++) {
    const m = GO_FILE.exec(lines[i]);
    if (m === null) continue;
    const prev = lines[i - 1] ?? '';
    const func = /^\S/.test(prev) ? prev.replace(/\(.*$/, '').trim() : undefined;
    frames.push({ file: m[1], line: Number(m[2]), func, lib: isLib(m[1]), raw: lines[i].trim() });
  }
  return frames;
}

// ── JavaScript / Node ───────────────────────────────────────────────────────
// V8 stacks: `    at func (path:line:col)` or `    at path:line:col`. Already
// innermost-first.
const JS_NAMED = /^\s*at (.+?) \((.+?):(\d+):(\d+)\)\s*$/;
const JS_BARE = /^\s*at (.+?):(\d+):(\d+)\s*$/;

export function parseJs(text: string): TraceFrame[] {
  const frames: TraceFrame[] = [];
  for (const raw of text.split('\n')) {
    const named = JS_NAMED.exec(raw);
    if (named !== null) {
      frames.push({ file: named[2], line: Number(named[3]), func: named[1], lib: isLib(named[2]), raw: raw.trim() });
      continue;
    }
    const bare = JS_BARE.exec(raw);
    if (bare !== null) {
      frames.push({ file: bare[1], line: Number(bare[2]), lib: isLib(bare[1]), raw: raw.trim() });
    }
  }
  return frames;
}

const PARSERS: { kind: TraceKind; run: (t: string) => TraceFrame[] }[] = [
  { kind: 'python', run: parsePython },
  { kind: 'go', run: parseGo },
  { kind: 'rust', run: parseRust },
  { kind: 'js', run: parseJs },
];

/// Parse the richest trace present in `text`, or `null` if none is found. Runs
/// every parser and keeps the one that recovered the most frames — the formats
/// are distinct enough that the real trace wins decisively (a stray "at x:1:2"
/// in a Python log yields one JS frame, far fewer than the real Python parse).
export function parseTrace(text: string): ParsedTrace | null {
  let best: ParsedTrace | null = null;
  for (const p of PARSERS) {
    const frames = p.run(text);
    if (frames.length > (best?.frames.length ?? 0)) best = { kind: p.kind, frames };
  }
  return best !== null && best.frames.length > 0 ? best : null;
}

/// Cheap yes/no for whether a body is worth showing the trace lens for.
export function hasTrace(text: string): boolean {
  return parseTrace(text) !== null;
}
