/// Graphviz DOT → SVG render substrate for the Inspect (J3) graph viewer (plan §5).
/// This is the shared render path every W4 graph producer targets: the DOT
/// viewer (`.dot`/`.gv` files, DVC dags), the **code2flow** call graph, and the
/// **torchview** model tracer all emit DOT and render through here.
///
/// `@hpcc-js/wasm-graphviz` runs the real Graphviz layout engine in WebAssembly,
/// fully offline (the wasm is inlined as base64 in the package — no external
/// asset to self-host, unlike tree-sitter; it just needs `wasm-unsafe-eval` in
/// the CSP, already granted). The import is **dynamic** so the ~800 KB engine
/// lands in its own lazy chunk, never the boot bundle
/// ([[feedback_eager_import_drags_heavy_dep_to_boot]]).

/// A Graphviz DOT graph opens with an optional `strict`, then `graph`/`digraph`,
/// an optional id, then `{`. Anchored to the start (past leading whitespace and
/// `//` / `/* */` comments) and requiring the brace, so it does not match code
/// that merely mentions "digraph".
export function looksLikeDot(text: string): boolean {
  const head = text.replace(/^\s*(\/\/[^\n]*\n|\/\*[\s\S]*?\*\/\s*)*/, '').slice(0, 512);
  return /^(strict\s+)?(di)?graph\b[^{;]*\{/.test(head);
}

type GraphvizInstance = { dot(src: string, format?: string): string };

let enginePromise: Promise<GraphvizInstance> | null = null;

async function engine(): Promise<GraphvizInstance> {
  if (enginePromise === null) {
    enginePromise = import('@hpcc-js/wasm-graphviz').then((m) => m.Graphviz.load() as Promise<GraphvizInstance>);
  }
  return enginePromise;
}

/// Render a DOT string to an SVG string. Throws with Graphviz's own message on a
/// syntax error (the caller shows it in an error pane).
export async function renderDot(dot: string): Promise<string> {
  const gv = await engine();
  return gv.dot(dot, 'svg');
}
