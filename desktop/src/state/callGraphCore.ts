/// Pure core of the code→call-graph slice (plan §5, W4) — the vendored code2flow
/// helper, its probe, and the remote-command assembly, with **no** bridge / ssh /
/// storage imports, so it is unit-testable with `node --test`. The IPC-driven
/// `runCallGraph` / `detectCode2flow` / persistence live in [[callGraph]].
///
/// This is the tracer's sibling: instead of torchview's meta-device *architecture*
/// graph, it runs [code2flow](https://github.com/scottrogowski/code2flow) over one
/// or more source files/dirs to emit a **static call graph** in Graphviz DOT, which
/// the shared graph viewer renders. code2flow is pure-Python for `py` (JS/Ruby/PHP
/// need Acorn / the Parser gem / PHP-Parser on the venue); a `.gv` output writes DOT
/// directly with no `dot` binary. Reuses the tracer's DOT sentinels + extraction and
/// the shared [[traceCore]] `base64ShellCommand` remote assembly.
// Explicit `.ts` extension (allowed by `allowImportingTsExtensions`) so this pure
// module — and its `node --test` suite — resolves the sibling without a bundler.
import { base64ShellCommand } from './traceCore.ts';

/// Re-exported so the IPC layer pulls DOT extraction from one place.
export { extractDot } from './traceCore.ts';

/// Vendored code2flow helper — piped to the interpreter's stdin. Reads its inputs
/// from the environment (never interpolated into a command). `C2F_TARGETS` is a
/// newline-separated list of paths; `C2F_LANG` is the language (`py`/`js`/`rb`/`php`)
/// or empty for auto-detect. ASCII-only so it base64-encodes with a plain `btoa`.
/// The DOT is wrapped in the same sentinels the tracer uses so it survives
/// code2flow's own progress logging (which goes to stderr).
export const CODE2FLOW_HELPER = `import os, sys, tempfile, traceback

targets_s = os.environ.get("C2F_TARGETS", "").strip()
lang = os.environ.get("C2F_LANG", "").strip()
if not targets_s:
    sys.stderr.write("no target files (pick a source file or directory)\\n")
    sys.exit(2)
targets = [t for t in targets_s.split("\\n") if t.strip() != ""]

try:
    from code2flow.engine import code2flow as _c2f
except Exception as e:
    sys.stderr.write("missing code2flow on this interpreter: %s\\n" % e)
    sys.exit(3)

fd, out_path = tempfile.mkstemp(suffix=".gv")
os.close(fd)
try:
    kwargs = dict(raw_source_paths=targets, output_file=out_path, skip_parse_errors=True)
    if lang:
        kwargs["language"] = lang
    _c2f(**kwargs)
    with open(out_path) as f:
        dot = f.read()
except SystemExit:
    raise
except Exception:
    sys.stderr.write("call-graph failed:\\n" + traceback.format_exc())
    sys.exit(5)
finally:
    try:
        os.remove(out_path)
    except OSError:
        pass

if "digraph" not in dot:
    sys.stderr.write("no call graph produced (empty or unrecognized source)\\n")
    sys.exit(6)

sys.stdout.write("===DOT-START===\\n")
sys.stdout.write(dot)
sys.stdout.write("\\n===DOT-END===\\n")
`;

/// Probe helper for the **Detect** action — confirms code2flow imports. (code2flow
/// exposes no `__version__`, so we report a bare OK line rather than a version.)
export const CODE2FLOW_PROBE = `import sys
try:
    import code2flow
    sys.stdout.write("OK code2flow %s\\n" % getattr(code2flow, "__version__", "installed"))
except Exception as e:
    sys.stderr.write("MISSING: %s\\n" % e)
    sys.exit(1)
`;

/// A code2flow language: the four code2flow supports, plus `` for auto-detect.
export type CallGraphLang = '' | 'py' | 'js' | 'rb' | 'php';

export interface CallGraphParams {
  /// Newline-separated target file/dir paths as seen on the venue.
  targets: string;
  /// Language, or `''` for code2flow's extension-based auto-detect.
  lang: CallGraphLang;
  /// Interpreter preset (whitespace-split into argv locally; run as-is remotely).
  command: string;
  /// Working directory / repo root (targets are resolved relative to it).
  repoRoot: string;
}

/// Environment for the helper — shared by the local (`trace_run`) and remote paths.
export function callGraphEnv(p: CallGraphParams): Record<string, string> {
  return { C2F_TARGETS: p.targets, C2F_LANG: p.lang };
}

/// Build the remote one-liner: cd into the repo, decode the base64 helper, feed it
/// to the interpreter on stdin with the call-graph params as environment variables.
export function remoteCallGraphCommand(p: CallGraphParams, helper: string = CODE2FLOW_HELPER): string {
  return base64ShellCommand(helper, callGraphEnv(p), p.command, p.repoRoot);
}

/// Build the remote probe one-liner for the Detect action.
export function remoteCallGraphProbe(command: string): string {
  return base64ShellCommand(CODE2FLOW_PROBE, {}, command);
}
