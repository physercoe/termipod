/// Pure core of the code→model-graph tracer (plan §5, W4 Tier 1) — the vendored
/// helper, DOT extraction, and remote-command assembly, with **no** bridge / ssh /
/// storage imports, so it is unit-testable with `node --test`. The IPC-driven
/// `runTrace` / `detectInterpreter` / persistence live in [[trace]].
///
/// Tracing is **weightless meta-device**: torchview runs the module on
/// `torch.device('meta')`, so an LLM-scale model graphs with no weights, memory,
/// or GPU. The output is a Graphviz DOT string rendered by the shared graph viewer.

// The DOT is wrapped in sentinels so it survives interpreter warnings on stdout
// and (over SSH) stderr folded into the same stream.
const DOT_START = '===DOT-START===';
const DOT_END = '===DOT-END===';

/// Vendored torchview helper — piped to the interpreter's stdin. Reads its inputs
/// from the environment (never interpolated into a command). ASCII-only so it
/// base64-encodes with a plain `btoa` for the SSH path.
export const TORCHVIEW_HELPER = `import os, sys, runpy, traceback

entry = os.environ.get("TRACE_ENTRY", "").strip()
shape_s = os.environ.get("TRACE_INPUT", "").strip()
trace_file = os.environ.get("TRACE_FILE", "").strip()
try:
    depth = int(os.environ.get("TRACE_DEPTH", "3") or "3")
except Exception:
    depth = 3

if not entry:
    sys.stderr.write("no entry expression (e.g. Model(dim=512))\\n")
    sys.exit(2)
try:
    import torch
    from torchview import draw_graph
except Exception as e:
    sys.stderr.write("missing torch/torchview on this interpreter: %s\\n" % e)
    sys.exit(3)

# Execute the model file for its namespace. run_name != "__main__" so a training
# "if __name__ == '__main__'" block does not fire. The repo must be importable on
# this venue (the import-locality rule).
ns = {}
if trace_file:
    try:
        ns = runpy.run_path(trace_file, run_name="__trace__")
    except Exception:
        sys.stderr.write("could not import the model file:\\n" + traceback.format_exc())
        sys.exit(4)

shape = tuple(int(x) for x in shape_s.split(",") if x.strip() != "") if shape_s else None
try:
    with torch.device("meta"):
        model = eval(entry, ns)
    g = draw_graph(model, input_size=shape, device="meta", depth=depth, expand_nested=True)
    dot = g.visual_graph.source
except Exception:
    sys.stderr.write("trace failed:\\n" + traceback.format_exc())
    sys.exit(5)

sys.stdout.write("${DOT_START}\\n")
sys.stdout.write(dot)
sys.stdout.write("\\n${DOT_END}\\n")
`;

/// Probe helper for the **Detect** action — confirms torch + torchview import.
export const PROBE_HELPER = `import sys
try:
    import torch, torchview
    sys.stdout.write("OK torch %s torchview %s\\n" % (torch.__version__, getattr(torchview, "__version__", "?")))
except Exception as e:
    sys.stderr.write("MISSING: %s\\n" % e)
    sys.exit(1)
`;

/// Pull the DOT payload out of the (possibly warning-polluted) output.
export function extractDot(text: string): string | null {
  const s = text.indexOf(DOT_START);
  if (s < 0) return null;
  const e = text.indexOf(DOT_END, s + DOT_START.length);
  if (e < 0) return null;
  return text.slice(s + DOT_START.length, e).replace(/^\r?\n/, '').replace(/\r?\n$/, '');
}

// Single-quote a value for a POSIX shell command (SSH path).
function shq(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

export interface TraceParams {
  entry: string;
  shape: string;
  depth: number;
  /// Interpreter preset (whitespace-split into argv locally; run as-is remotely).
  command: string;
  /// Working directory / repo root (importable root for the model file).
  repoRoot: string;
  /// The model file path as seen on the venue.
  filePath: string;
}

/// Build the remote one-liner: cd into the repo, decode the base64 helper, feed it
/// to the interpreter on stdin with the trace params as environment variables.
export function remoteTraceCommand(p: TraceParams, helper: string = TORCHVIEW_HELPER): string {
  const b64 = btoa(helper);
  const env = [
    `TRACE_ENTRY=${shq(p.entry)}`,
    `TRACE_INPUT=${shq(p.shape)}`,
    `TRACE_DEPTH=${shq(String(p.depth))}`,
    `TRACE_FILE=${shq(p.filePath)}`,
  ].join(' ');
  const cd = p.repoRoot.trim() !== '' ? `cd ${shq(p.repoRoot)} && ` : '';
  return `${cd}printf %s ${shq(b64)} | base64 -d | ${env} ${p.command}`;
}

/// Build the remote probe one-liner for the Detect action.
export function remoteProbeCommand(command: string): string {
  return `printf %s ${shq(btoa(PROBE_HELPER))} | base64 -d | ${command}`;
}
