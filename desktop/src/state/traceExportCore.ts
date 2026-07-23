/// Pure core of the tracer **Tier 2** (plan §5 — `torch.export` → Model Explorer):
/// the vendored helper, its JSON extraction, and remote-command assembly. No bridge /
/// ssh / storage imports, so `node --test` runs it. The IPC-driven `runTraceExport`
/// lives in [[traceExport]]; the `ExportGraph → GraphCollection` build in [[modelGraph]].
///
/// Where Tier 1 ([[traceCore]]) draws torchview's weightless **architecture** boxes,
/// Tier 2 runs `torch.export.export(model, args, strict=False)` on the **meta device**
/// (no weights/GPU/compute) and walks the resulting FX graph into the **measured**
/// ATen op graph — namespace from each node's `nn_module_stack`, edges from
/// `all_input_nodes`, shapes from `node.meta['val']`. The graph renders in the Model
/// Explorer WebGL element. `torch.export` needs torch ≥ 2.1; torchview is NOT required.
import { base64ShellCommand } from './traceCore.ts';
import type { ExportGraph, ExportNode } from './modelGraph.ts';

const EXPORT_START = '===EXPORT-START===';
const EXPORT_END = '===EXPORT-END===';

/// Vendored torch.export helper — piped to the interpreter's stdin, inputs from the
/// environment (`TRACE_ENTRY` / `TRACE_INPUT` / `TRACE_FILE`, same as Tier 1). ASCII
/// (btoa-safe). Emits a flat node list `{id, op, target, namespace, inputs, shape,
/// dtype}` between sentinels; every metadata read is guarded so an unusual node can't
/// abort the walk. Runtime is device-bound (needs torch); the code is py_compile-clean.
export const TORCH_EXPORT_HELPER = `import os, sys, runpy, traceback, json

entry = os.environ.get("TRACE_ENTRY", "").strip()
shape_s = os.environ.get("TRACE_INPUT", "").strip()
trace_file = os.environ.get("TRACE_FILE", "").strip()

if not entry:
    sys.stderr.write("no entry expression (e.g. Model(dim=512))\\n"); sys.exit(2)
try:
    import torch
except Exception as e:
    sys.stderr.write("missing torch on this interpreter: %s\\n" % e); sys.exit(3)

ns = {}
if trace_file:
    try:
        ns = runpy.run_path(trace_file, run_name="__trace__")
    except Exception:
        sys.stderr.write("could not import the model file:\\n" + traceback.format_exc()); sys.exit(4)

shape = tuple(int(x) for x in shape_s.split(",") if x.strip() != "") if shape_s else None
try:
    with torch.device("meta"):
        model = eval(entry, ns)
        example = (torch.randn(*shape),) if shape else (torch.randn(1),)
    ep = torch.export.export(model, example, strict=False)
except Exception:
    sys.stderr.write("torch.export failed:\\n" + traceback.format_exc()); sys.exit(5)

def ns_of(node):
    try:
        stk = node.meta.get("nn_module_stack")
        if stk:
            return str(list(stk.values())[-1][0]).replace(".", "/")
    except Exception:
        pass
    return ""

def shape_dtype(node):
    try:
        v = node.meta.get("val")
        if hasattr(v, "shape") and hasattr(v, "dtype"):
            return [int(d) for d in v.shape], str(v.dtype)
    except Exception:
        pass
    return None, None

nodes = []
try:
    graph = ep.graph
    for node in graph.nodes:
        sh, dt = shape_dtype(node)
        try:
            inputs = [n.name for n in node.all_input_nodes]
        except Exception:
            inputs = []
        nodes.append({
            "id": str(node.name),
            "op": str(node.op),
            "target": str(node.target) if node.op == "call_function" else str(node.op),
            "namespace": ns_of(node),
            "inputs": inputs,
            "shape": sh,
            "dtype": dt,
        })
except Exception:
    sys.stderr.write("could not walk the exported graph:\\n" + traceback.format_exc()); sys.exit(6)

sys.stdout.write("${EXPORT_START}\\n")
sys.stdout.write(json.dumps({"nodes": nodes}))
sys.stdout.write("\\n${EXPORT_END}\\n")
`;

/// Torch-only probe for the Tier-2 Detect action (torchview not needed).
export const TORCH_PROBE = `import sys
try:
    import torch
    sys.stdout.write("OK torch %s (export %s)\\n" % (torch.__version__, "yes" if hasattr(torch, "export") else "no"))
except Exception as e:
    sys.stderr.write("MISSING: %s\\n" % e); sys.exit(1)
`;

/// Pull the JSON payload out of the (warning-polluted) helper output.
export function extractExportJson(text: string): string | null {
  const s = text.indexOf(EXPORT_START);
  if (s < 0) return null;
  const e = text.indexOf(EXPORT_END, s + EXPORT_START.length);
  if (e < 0) return null;
  return text.slice(s + EXPORT_START.length, e).trim();
}

/// Parse + validate the helper output into an `ExportGraph`. Null on missing
/// sentinels (a plain error) or malformed JSON.
export function parseExportGraph(text: string): ExportGraph | null {
  const json = extractExportJson(text);
  if (json === null) return null;
  try {
    const obj = JSON.parse(json) as { nodes?: unknown };
    if (!Array.isArray(obj.nodes)) return null;
    const nodes: ExportNode[] = obj.nodes.map((n) => {
      const nn = n as Record<string, unknown>;
      return {
        id: typeof nn.id === 'string' ? nn.id : '',
        op: typeof nn.op === 'string' ? nn.op : '',
        target: typeof nn.target === 'string' ? nn.target : '',
        namespace: typeof nn.namespace === 'string' ? nn.namespace : '',
        inputs: Array.isArray(nn.inputs) ? nn.inputs.filter((x): x is string => typeof x === 'string') : [],
        shape: Array.isArray(nn.shape) ? nn.shape.filter((x): x is number => typeof x === 'number') : null,
        dtype: typeof nn.dtype === 'string' ? nn.dtype : null,
      };
    });
    return { nodes };
  } catch {
    return null;
  }
}

/// Build the remote one-liner: decode the base64 helper, feed it to the interpreter
/// on stdin with the trace params as environment variables.
export function remoteExportCommand(entry: string, shape: string, filePath: string, command: string, repoRoot: string, helper: string = TORCH_EXPORT_HELPER): string {
  return base64ShellCommand(helper, { TRACE_ENTRY: entry, TRACE_INPUT: shape, TRACE_FILE: filePath }, command, repoRoot);
}

/// Remote torch-only probe for the Detect action.
export function remoteTorchProbe(command: string): string {
  return base64ShellCommand(TORCH_PROBE, {}, command);
}
