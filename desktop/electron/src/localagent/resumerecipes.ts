/// Native-resume recipes, TypeScript side (vision-parity L3b; table from N1).
///
/// How does the Companion reattach to a claude session after the app restarts?
/// The answer is a row in `hub/internal/resumerecipes/recipes.yaml`, and N1
/// shipped that table as data precisely so this file would not have to be a
/// second opinion about it. What crosses is
/// `resources/resume_recipes.generated.json`, marshalled from the hub's own
/// structs by a Go test that fails when the two disagree.
///
/// **This is a reader, not a re-derivation.** The one piece of behaviour it
/// reimplements — building the argv from `style` + `token` — is pinned case for
/// case against the hub's conformance corpus in `resumerecipes.test.ts`, the
/// same arrangement the L2 frame-profile interpreter has with the profile
/// corpus. Two implementations of one rule is the shape that drifts quietly;
/// a shared fixture is what makes the drift loud.
///
/// **What is deliberately absent.**
///
///   - `ShellQuote` / `ShellCommand`. The hub needs them because it splices
///     resume into `backend.cmd`, a shell string. We call `spawn(bin, argv)`,
///     so a session id never becomes a shell token and there is nothing to
///     quote. Porting the quoter would be porting a hazard we do not have —
///     and a quoter with no caller is a quoter nobody notices is wrong.
///   - `path` refs. No engine we drive locally accepts one (claude is
///     `ref_kinds: [id]`), and `resumeArgv` refuses the kind rather than
///     silently building a command the engine will reject.
///   - The non-argv mechanisms (`acp_session_load`, `appserver_thread_resume`).
///     They are real rows and this reader reports them faithfully, but acting
///     on them is a protocol conversation, not an argv — L4's problem.

/// One engine CLI's recipe, as it crosses from the hub.
export interface ResumeEngine {
  engine: string;
  bin: string;
  windows_bin: string;
  style: string;
  token: string;
  ref_kinds: string[];
  source: string;
  verified: string;
  note: string;
}

/// How one of our agent families reattaches.
export interface ResumeFamily {
  family: string;
  engine: string;
  mechanism: string;
  note: string;
}

export interface ResumeTable {
  version: number;
  engines: ResumeEngine[];
  families: ResumeFamily[];
}

/// Mechanisms, mirroring the Go constants.
export const MECHANISM_ARGV = 'argv';

/// Argv styles. `flag_pair` and `subcommand` build the same three tokens and
/// stay distinct because one is a flag and one is a verb — a surface rendering
/// the command for a person should be able to say which.
const STYLE_FLAG_PAIR = 'flag_pair';
const STYLE_FLAG_EQUALS = 'flag_equals';
const STYLE_SUBCOMMAND = 'subcommand';

/// Session-reference kinds.
export type RefKind = 'id' | 'path';

/// Bounds ported from the Go side (which took them from herdr). A session
/// reference arrives *from the engine*, so it is screened before it is spliced
/// into anything, even an argv array where it cannot be misread as syntax.
///
/// **These are UTF-8 BYTES, not string units.** Go's `len(string)` counts
/// bytes; JavaScript's `String.length` counts UTF-16 code units, and the two
/// diverge the moment a session id leaves ASCII. The hub's fixture pins the
/// divergence on purpose — it carries a `café` case annotated "5 bytes but 4 JS
/// string units", and an é-repeated id sitting exactly on the limit. Comparing
/// `value.length` here would accept ids the hub rejects, which is a validator
/// that reports agreement it does not have.
export const MAX_SESSION_ID_LEN = 512;
export const MAX_SESSION_PATH_LEN = 4096;

const UTF8 = new TextEncoder();

function byteLength(s: string): number {
  return UTF8.encode(s).length;
}

export interface SessionRef {
  kind: RefKind;
  value: string;
}

/// Validate an `id` reference. Mirrors Go's `NewID`.
///
/// Returns the ref or `null`; the caller decides what a rejection means, which
/// differs by site (a bad id from the engine is a bug, a bad id from a stale
/// meta.json on disk is a session we decline to rebind).
export function newSessionID(value: string): SessionRef | null {
  if (value === '' || byteLength(value) > MAX_SESSION_ID_LEN) return null;
  if (hasControl(value)) return null;
  return { kind: 'id', value };
}

/// Validate a `path` reference. Mirrors Go's `NewPath`: absolute, bounded, no
/// control characters. Kept because the fixture exercises it — an engine we do
/// not drive today (pi, omp) accepts one, and a reader that silently lacked the
/// kind would report "invalid" for a reference that is merely unsupported here.
export function newSessionPath(value: string): SessionRef | null {
  if (value === '' || byteLength(value) > MAX_SESSION_PATH_LEN) return null;
  if (hasControl(value)) return null;
  if (!value.startsWith('/')) return null;
  return { kind: 'path', value };
}

/// Control characters, including DEL. `\p{Cc}` would be the elegant spelling
/// but the Go side tests `unicode.IsControl`, whose set is exactly C0 + DEL +
/// C1 — so this enumerates the same ranges rather than trusting two regex
/// engines to agree on a property name.
function hasControl(s: string): boolean {
  for (const ch of s) {
    const c = ch.codePointAt(0) ?? 0;
    if (c < 0x20 || c === 0x7f || (c >= 0x80 && c <= 0x9f)) return true;
  }
  return false;
}

export function engineByID(table: ResumeTable, id: string): ResumeEngine | undefined {
  return table.engines.find((e) => e.engine === id);
}

export function familyByName(table: ResumeTable, name: string): ResumeFamily | undefined {
  return table.families.find((f) => f.family === name);
}

/// The binary for a platform. `windows_bin` overrides on win32 only, and an
/// empty override means "same name everywhere".
export function binFor(engine: ResumeEngine, platform: string): string {
  if (platform === 'win32' && engine.windows_bin !== '') return engine.windows_bin;
  return engine.bin;
}

export function acceptsRef(engine: ResumeEngine, kind: string): boolean {
  return engine.ref_kinds.includes(kind);
}

/// Why an argv could not be built. Strings rather than an enum so the fixture's
/// `error` column compares directly.
export type ResumeError = 'unknown_family' | 'not_argv_resume' | 'unknown_engine' | 'unsupported_ref_kind' | 'unknown_style';

export type ResumeArgvResult =
  | { ok: true; engine: string; argv: string[] }
  | { ok: false; error: ResumeError };

/// Build the full resume argv for a family — `[bin, ...tokens]`, exactly what
/// the hub's `PlanForFamily` produces.
export function resumeArgv(
  table: ResumeTable,
  family: string,
  ref: SessionRef,
  platform: string,
): ResumeArgvResult {
  const fam = familyByName(table, family);
  if (fam === undefined) return { ok: false, error: 'unknown_family' };
  if (fam.mechanism !== MECHANISM_ARGV || fam.engine === '') {
    return { ok: false, error: 'not_argv_resume' };
  }
  const eng = engineByID(table, fam.engine);
  if (eng === undefined) return { ok: false, error: 'unknown_engine' };
  return engineResumeArgv(eng, ref, platform);
}

/// Build the resume argv for one engine directly. Split out because the
/// fixture's `engine_argv` cases are keyed by engine, not family.
export function engineResumeArgv(
  engine: ResumeEngine,
  ref: SessionRef,
  platform: string,
): ResumeArgvResult {
  if (!acceptsRef(engine, ref.kind)) return { ok: false, error: 'unsupported_ref_kind' };
  const bin = binFor(engine, platform);
  switch (engine.style) {
    case STYLE_FLAG_PAIR:
    case STYLE_SUBCOMMAND:
      return { ok: true, engine: engine.engine, argv: [bin, engine.token, ref.value] };
    case STYLE_FLAG_EQUALS:
      return { ok: true, engine: engine.engine, argv: [bin, `${engine.token}=${ref.value}`] };
    default:
      return { ok: false, error: 'unknown_style' };
  }
}

/// The tokens to APPEND to an existing launch argv, i.e. the recipe minus its
/// binary.
///
/// This is the shape the local agent service actually needs, and it is not the
/// same as the hub's. The hub replaces a whole command; we already have a
/// launch argv — `--print --output-format stream-json --input-format
/// stream-json …` — and resume is one more flag pair on the end of it. Taking
/// `argv[0]` off is safe because every style puts the bin there and the service
/// spawns the family registry's own `bin`, which is where the binary decision
/// belongs.
export function resumeSplice(
  table: ResumeTable,
  family: string,
  ref: SessionRef,
  platform: string,
): { ok: true; tokens: string[] } | { ok: false; error: ResumeError } {
  const r = resumeArgv(table, family, ref, platform);
  if (!r.ok) return r;
  return { ok: true, tokens: r.argv.slice(1) };
}

/// Parse the generated table, rejecting anything that is not the shape above.
///
/// Validation here is deliberately shallow — the hub's loader already validated
/// these rows before marshalling them, and re-implementing `validateEngine`
/// would be the second opinion this file exists to avoid. What is checked is
/// that the JSON *is a table*: a truncated or half-written artifact must fail
/// at load, not at the moment someone tries to rebind.
export function parseResumeTable(json: string): ResumeTable {
  const raw: unknown = JSON.parse(json);
  if (typeof raw !== 'object' || raw === null) throw new Error('resume recipes: not an object');
  const obj = raw as Record<string, unknown>;
  if (!Array.isArray(obj.engines) || !Array.isArray(obj.families)) {
    throw new Error('resume recipes: missing engines or families');
  }
  if (obj.engines.length === 0 || obj.families.length === 0) {
    throw new Error('resume recipes: table is empty');
  }
  return {
    version: typeof obj.version === 'number' ? obj.version : 0,
    engines: obj.engines as ResumeEngine[],
    families: obj.families as ResumeFamily[],
  };
}
