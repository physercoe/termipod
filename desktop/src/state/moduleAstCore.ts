/// Pure core of the W4b **module reader** (plan §4b) — a stdlib-only Python `ast`
/// helper that extracts an nn.Module class hierarchy (bases + submodule composition
/// + source spans) from a `modeling_*.py`, plus the sentinel extraction and remote
/// command assembly. No bridge / ssh / storage imports, so `node --test` runs it.
/// The IPC-driven `runModuleAst` lives in [[moduleAst]]; the graph build in
/// [[moduleGraph]].
///
/// This is deliberately **AST-only, not a trace**: for regular HF modeling files the
/// AST yields structure + spans reliably (the class → submodule composition, for
/// the code-synced drill-down graph). `forward()` dataflow stays approximate — the
/// measured truth is the meta-device tracer ([[traceCore]]). The helper is stdlib
/// (`ast`, `json`) so **any** python3 venue runs it — no torch needed.
import { base64ShellCommand } from './traceCore.ts';

const AST_START = '===AST-START===';
const AST_END = '===AST-END===';

/// Vendored stdlib-`ast` helper — piped to the interpreter's stdin, reads the target
/// file path from `AST_FILE` (never interpolated). ASCII-only (btoa-safe for SSH).
/// For each class: bases, `[lineno, end_lineno]` span, and per-submodule the attr +
/// every class name called in `self.<attr> = …(…)` (so `nn.ModuleList([Block(…)])`
/// yields `["nn.ModuleList", "Block"]` → the local element class is captured).
export const MODULE_AST_HELPER = `import os, sys, ast, json

path = os.environ.get("AST_FILE", "").strip()
if not path:
    sys.stderr.write("no file (set AST_FILE)\\n"); sys.exit(2)
try:
    with open(path, "r", encoding="utf-8") as f:
        src = f.read()
    tree = ast.parse(src, filename=path)
except Exception as e:
    sys.stderr.write("could not parse the file: %s\\n" % e); sys.exit(3)

def base_name(b):
    if isinstance(b, ast.Name):
        return b.id
    if isinstance(b, ast.Attribute):
        parts = []; cur = b
        while isinstance(cur, ast.Attribute):
            parts.append(cur.attr); cur = cur.value
        if isinstance(cur, ast.Name):
            parts.append(cur.id)
        return ".".join(reversed(parts))
    return ""

def called_names(value):
    out = []
    for n in ast.walk(value):
        if isinstance(n, ast.Call):
            nm = base_name(n.func) if isinstance(n.func, (ast.Name, ast.Attribute)) else ""
            if nm and nm not in out:
                out.append(nm)
    return out

classes = []
for node in ast.walk(tree):
    if not isinstance(node, ast.ClassDef):
        continue
    bases = [b for b in (base_name(x) for x in node.bases) if b]
    submods = []
    for item in node.body:
        if isinstance(item, ast.FunctionDef) and item.name == "__init__":
            for stmt in ast.walk(item):
                if isinstance(stmt, ast.Assign):
                    for tgt in stmt.targets:
                        if isinstance(tgt, ast.Attribute) and isinstance(tgt.value, ast.Name) and tgt.value.id == "self":
                            cn = called_names(stmt.value)
                            if cn:
                                submods.append({"attr": tgt.attr, "classes": cn, "lineno": getattr(stmt, "lineno", 0)})
    classes.append({"name": node.name, "bases": bases, "lineno": getattr(node, "lineno", 0),
                    "endLineno": getattr(node, "end_lineno", 0), "submodules": submods})

sys.stdout.write("${AST_START}\\n")
sys.stdout.write(json.dumps({"classes": classes}))
sys.stdout.write("\\n${AST_END}\\n")
`;

export interface ModuleSubmodule {
  attr: string;
  /// Class names called in the RHS (dedup, in order); `classes[0]` is the primary.
  classes: string[];
  lineno: number;
}
export interface ModuleClass {
  name: string;
  bases: string[];
  lineno: number;
  endLineno: number;
  submodules: ModuleSubmodule[];
}
export interface ModuleModel {
  classes: ModuleClass[];
}

/// Pull the JSON payload out of the (possibly warning-polluted) helper output.
export function extractModuleJson(text: string): string | null {
  const s = text.indexOf(AST_START);
  if (s < 0) return null;
  const e = text.indexOf(AST_END, s + AST_START.length);
  if (e < 0) return null;
  return text.slice(s + AST_START.length, e).trim();
}

/// Parse the helper output into a validated `ModuleModel`. Returns null when the
/// sentinels are absent (a plain error) or the JSON is malformed.
export function parseModuleAst(text: string): ModuleModel | null {
  const json = extractModuleJson(text);
  if (json === null) return null;
  try {
    const obj = JSON.parse(json) as { classes?: unknown };
    if (!Array.isArray(obj.classes)) return null;
    const classes: ModuleClass[] = obj.classes.map((c) => {
      const cc = c as Record<string, unknown>;
      return {
        name: typeof cc.name === 'string' ? cc.name : '',
        bases: Array.isArray(cc.bases) ? cc.bases.filter((b): b is string => typeof b === 'string') : [],
        lineno: typeof cc.lineno === 'number' ? cc.lineno : 0,
        endLineno: typeof cc.endLineno === 'number' ? cc.endLineno : 0,
        submodules: Array.isArray(cc.submodules)
          ? cc.submodules.map((s) => {
              const ss = s as Record<string, unknown>;
              return {
                attr: typeof ss.attr === 'string' ? ss.attr : '',
                classes: Array.isArray(ss.classes) ? ss.classes.filter((x): x is string => typeof x === 'string') : [],
                lineno: typeof ss.lineno === 'number' ? ss.lineno : 0,
              };
            })
          : [],
      };
    });
    return { classes };
  } catch {
    return null;
  }
}

/// Build the remote one-liner: decode the base64 helper, feed it to the interpreter
/// on stdin with the target file path as an environment variable.
export function remoteModuleAstCommand(filePath: string, command: string, repoRoot: string, helper: string = MODULE_AST_HELPER): string {
  return base64ShellCommand(helper, { AST_FILE: filePath }, command, repoRoot);
}

// ── class graph (the code-synced drill-down) ─────────────────────────────────────
export interface ModuleGraphSubmodule {
  attr: string;
  type: string;
  /// True when `type` is another class defined in this file (a drill-down target).
  local: boolean;
  lineno: number;
}
export interface ModuleGraphNode {
  id: string;
  label: string;
  /// Local base classes (defined in this file); external bases are dropped as noise.
  bases: string[];
  submodules: ModuleGraphSubmodule[];
  lineno: number;
  endLineno: number;
}
export interface ModuleGraphEdge {
  source: string;
  target: string;
  kind: 'composition' | 'inheritance';
  /// The attribute name, for a composition edge.
  label?: string;
}
export interface ModuleGraph {
  nodes: ModuleGraphNode[];
  edges: ModuleGraphEdge[];
}

/// Turn the parsed module model into a class graph: nodes = classes, edges =
/// **composition** (a class → each *local* class it instantiates, one per attr) and
/// **inheritance** (a class → each *local* base). External types (`nn.Linear`, …)
/// stay as node metadata (submodule rows) but draw no edge — only in-file classes are
/// drill-down targets. Self-references are dropped. Composition edges dedup by pair.
export function buildModuleGraph(model: ModuleModel): ModuleGraph {
  const local = new Set(model.classes.map((c) => c.name).filter((n) => n !== ''));
  const nodes: ModuleGraphNode[] = model.classes
    .filter((c) => c.name !== '')
    .map((c) => ({
      id: c.name,
      label: c.name,
      bases: c.bases.filter((b) => local.has(b)),
      submodules: c.submodules.map((s) => {
        const type = s.classes[0] ?? '';
        const localTarget = s.classes.find((x) => local.has(x) && x !== c.name);
        return { attr: s.attr, type, local: localTarget !== undefined, lineno: s.lineno };
      }),
      lineno: c.lineno,
      endLineno: c.endLineno,
    }));
  const edges: ModuleGraphEdge[] = [];
  const seen = new Set<string>();
  for (const c of model.classes) {
    if (c.name === '') continue;
    for (const b of c.bases) {
      if (local.has(b) && b !== c.name) edges.push({ source: c.name, target: b, kind: 'inheritance' });
    }
    for (const s of c.submodules) {
      for (const cls of s.classes) {
        if (!local.has(cls) || cls === c.name) continue;
        const key = `${c.name} ${cls}`;
        if (seen.has(key)) continue;
        seen.add(key);
        edges.push({ source: c.name, target: cls, kind: 'composition', label: s.attr });
      }
    }
  }
  return { nodes, edges };
}
