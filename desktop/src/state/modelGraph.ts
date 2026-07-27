/// Model-graph adapters (plan §5, W4 — Model Explorer graph). Converts a parsed
/// checkpoint's operator graph into a **Model Explorer `GraphCollection`** — the
/// exact JSON the `ai-edge-model-explorer-visualizer` WebGL custom element consumes
/// — and, until that (7 MB, asset-hosted, device-verified) element is wired, a
/// `graphCollectionToDot` bridge that renders the same structure in the existing
/// Graphviz DOT viewer. Pure: no bridge / electron / storage, so `node --test` runs
/// it; the schema types below are pinned verbatim to the visualizer's
/// `common/input_graph.ts` + `common/types.ts` (v0.1.2, verified 2026-07-23).
import type { OnnxGraphData, TensorInfo } from './checkpoint.ts';

// ── Model Explorer input schema (minimal, load-bearing subset) ───────────────────
export interface KeyValue {
  key: string;
  value: string;
}
/// An input/output slot's metadata (its id is referenced by an edge endpoint).
export interface MetadataItem {
  id: string;
  attrs: KeyValue[];
}
export interface IncomingEdge {
  sourceNodeId: string;
  sourceNodeOutputId: string;
  targetNodeInputId: string;
}
export interface GraphNode {
  id: string;
  label: string;
  /// "/"-delimited hierarchy path (no leading slash); groups the node. "" = root.
  namespace: string;
  attrs?: KeyValue[];
  incomingEdges?: IncomingEdge[];
  inputsMetadata?: MetadataItem[];
  outputsMetadata?: MetadataItem[];
}
export interface Graph {
  id: string;
  nodes: GraphNode[];
}
export interface GraphCollection {
  label: string;
  graphs: Graph[];
}

/// Derive a Model Explorer namespace from an ONNX node name: the path minus the
/// final segment, leading slash stripped. `/model/layers.0/Add` → `model/layers.0`.
/// A name without a slash (or empty) groups at the root.
export function onnxNamespace(name: string): string {
  const i = name.lastIndexOf('/');
  if (i <= 0) return '';
  return name.slice(0, i).replace(/^\/+/, '');
}

/// Build a Model Explorer `GraphCollection` from a parsed ONNX operator graph.
/// Nodes get index-based ids (stable, collision-free); edges are wired by matching
/// a producer node's output tensor name to a consumer node's input tensor name.
/// Inputs that are initializers (weights) or graph inputs have no producer → no
/// edge (they surface as `inputsMetadata`, flagged `const` when an initializer).
export function onnxToGraphCollection(graph: OnnxGraphData, initializerNames: Set<string>, label = 'onnx'): GraphCollection {
  // tensor name -> the producing node's id + which output slot it is.
  const producer = new Map<string, { nodeId: string; outIdx: number }>();
  const ids: string[] = graph.nodes.map((_, i) => `n${i}`);
  graph.nodes.forEach((n, i) => {
    n.outputs.forEach((out, k) => {
      if (out !== '' && !producer.has(out)) producer.set(out, { nodeId: ids[i], outIdx: k });
    });
  });

  const nodes: GraphNode[] = graph.nodes.map((n, i) => {
    const incomingEdges: IncomingEdge[] = [];
    const inputsMetadata: MetadataItem[] = [];
    n.inputs.forEach((inp, j) => {
      const p = producer.get(inp);
      if (p !== undefined && p.nodeId !== ids[i]) {
        incomingEdges.push({ sourceNodeId: p.nodeId, sourceNodeOutputId: String(p.outIdx), targetNodeInputId: String(j) });
      }
      const attrs: KeyValue[] = [{ key: 'tensor', value: inp }];
      if (initializerNames.has(inp)) attrs.push({ key: 'kind', value: 'const' });
      inputsMetadata.push({ id: String(j), attrs });
    });
    const outputsMetadata: MetadataItem[] = n.outputs.map((out, k) => ({ id: String(k), attrs: [{ key: 'tensor', value: out }] }));
    const attrs: KeyValue[] = [{ key: 'op', value: n.opType }];
    if (n.name !== '') attrs.push({ key: 'name', value: n.name });
    const node: GraphNode = { id: ids[i], label: n.opType, namespace: onnxNamespace(n.name), attrs };
    if (incomingEdges.length > 0) node.incomingEdges = incomingEdges;
    if (inputsMetadata.length > 0) node.inputsMetadata = inputsMetadata;
    if (outputsMetadata.length > 0) node.outputsMetadata = outputsMetadata;
    return node;
  });

  return { label, graphs: [{ id: 'main', nodes }] };
}

/// Namespace for a dotted weight-tensor name: the "."-delimited path minus the
/// final segment, re-joined with "/" (Model Explorer's delimiter). A name without a
/// dot groups at the root. `model.layers.0.q_proj.weight` → `model/layers.0/q_proj`.
export function checkpointNamespace(name: string): string {
  const i = name.lastIndexOf('.');
  if (i < 0) return '';
  return name.slice(0, i).replace(/\./g, '/');
}

/// Build a Model Explorer `GraphCollection` from a weight checkpoint's tensor list
/// (safetensors / GGUF, which have no operator graph). Each tensor is a leaf node
/// grouped by its namespace; there are no edges (weights carry no dataflow), so the
/// value is the hierarchical, collapsible grouping the WebGL element renders. dtype
/// / shape / params ride as node attrs.
export function checkpointToGraphCollection(tensors: TensorInfo[], label = 'checkpoint'): GraphCollection {
  const nodes: GraphNode[] = tensors.map((tsr, i) => {
    const dot = tsr.name.lastIndexOf('.');
    const leaf = dot >= 0 ? tsr.name.slice(dot + 1) : tsr.name;
    return {
      id: `t${i}`,
      label: leaf,
      namespace: checkpointNamespace(tsr.name),
      attrs: [
        { key: 'dtype', value: tsr.dtype },
        { key: 'shape', value: `[${tsr.shape.join(', ')}]` },
        { key: 'params', value: String(tsr.params) },
      ],
    };
  });
  return { label, graphs: [{ id: 'weights', nodes }] };
}

// ── torch.export traced graph (tracer Tier 2) ───────────────────────────────────
/// One FX node of a `torch.export` traced program — the flat intermediate the
/// vendored helper emits (see [[traceExportCore]]); `exportToGraphCollection` wires
/// it into the schema. `inputs` are upstream node ids (the data-flow edges).
export interface ExportNode {
  id: string;
  op: string;
  target: string;
  namespace: string;
  inputs: string[];
  shape: number[] | null;
  dtype: string | null;
}
export interface ExportGraph {
  nodes: ExportNode[];
}

/// Shorten an ATen op target for a node label: `torch.ops.aten.addmm.default` /
/// `aten.addmm.default` → `addmm`; a non-aten target keeps its last dotted segment.
export function shortOpLabel(target: string, op: string): string {
  if (target === '' || target === op) return op;
  const parts = target.split('.').filter((p) => p !== '' && p !== 'default');
  return parts[parts.length - 1] ?? op;
}

/// Build a Model Explorer `GraphCollection` from a `torch.export` traced graph — the
/// **measured** ATen op graph (Tier 2), richer than torchview's architecture boxes.
/// Nodes = FX nodes (namespace from `nn_module_stack`), edges = data flow (each input
/// node → this node), output tensor shape/dtype ride as `outputsMetadata`.
export function exportToGraphCollection(graph: ExportGraph, label = 'traced'): GraphCollection {
  const ids = new Set(graph.nodes.map((n) => n.id));
  const nodes: GraphNode[] = graph.nodes.map((n) => {
    const incomingEdges: IncomingEdge[] = [];
    n.inputs.forEach((inp, j) => {
      if (ids.has(inp) && inp !== n.id) incomingEdges.push({ sourceNodeId: inp, sourceNodeOutputId: '0', targetNodeInputId: String(j) });
    });
    const attrs: KeyValue[] = [{ key: 'op', value: n.op }];
    if (n.target !== '' && n.target !== n.op) attrs.push({ key: 'target', value: n.target });
    const node: GraphNode = { id: n.id, label: shortOpLabel(n.target, n.op), namespace: n.namespace, attrs };
    if (incomingEdges.length > 0) node.incomingEdges = incomingEdges;
    if (n.shape !== null) {
      const a: KeyValue[] = [{ key: 'shape', value: `[${n.shape.join(', ')}]` }];
      if (n.dtype !== null) a.push({ key: 'dtype', value: n.dtype });
      node.outputsMetadata = [{ id: '0', attrs: a }];
    }
    return node;
  });
  return { label, graphs: [{ id: 'traced', nodes }] };
}

// Escape a string for a double-quoted Graphviz DOT label/id.
function dotEscape(s: string): string {
  return s.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\r?\n/g, '\\n');
}

/// Render a `GraphCollection` to Graphviz DOT for the existing viewer — the interim
/// path before the Model Explorer WebGL element lands. Flat (one node per operator,
/// edges by data flow); the namespace hierarchy is preserved in the collection for
/// the richer element but not clustered here. The node label carries the op type;
/// the full node name rides in the tooltip.
export function graphCollectionToDot(gc: GraphCollection): string {
  const g = gc.graphs[0];
  if (g === undefined || g.nodes.length === 0) return 'digraph G {\n}\n';
  // Plain (unfilled) boxes so the render is theme-neutral in the SVG viewer.
  const lines: string[] = ['digraph G {', '  rankdir="LR";', '  node [shape=box, fontsize=10];'];
  for (const n of g.nodes) {
    const tip = n.attrs?.find((a) => a.key === 'name')?.value ?? n.label;
    lines.push(`  "${dotEscape(n.id)}" [label="${dotEscape(n.label)}" tooltip="${dotEscape(tip)}"];`);
  }
  const seen = new Set<string>();
  for (const n of g.nodes) {
    for (const e of n.incomingEdges ?? []) {
      const key = `${e.sourceNodeId}\u0000${n.id}`;
      if (seen.has(key)) continue;
      seen.add(key);
      lines.push(`  "${dotEscape(e.sourceNodeId)}" -> "${dotEscape(n.id)}";`);
    }
  }
  lines.push('}');
  return `${lines.join('\n')}\n`;
}
