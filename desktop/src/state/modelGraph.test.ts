/// Pure-logic tests for the ONNX → Model Explorer GraphCollection adapter and its
/// interim DOT bridge (plan §5 W4). The WebGL visualizer element renders only in a
/// real browser (device-test); this pins the *data* it consumes. Run locally:
/// `node --test src/state/modelGraph.test.ts`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  checkpointNamespace,
  checkpointToGraphCollection,
  exportToGraphCollection,
  graphCollectionToDot,
  onnxNamespace,
  onnxToGraphCollection,
  shortOpLabel,
  type ExportGraph,
} from './modelGraph.ts';
import type { OnnxGraphData, TensorInfo } from './checkpoint.ts';

// A tiny linear graph: input x + weight W → MatMul → Relu → output y.
const G: OnnxGraphData = {
  nodes: [
    { name: '/model/layers.0/MatMul', opType: 'MatMul', inputs: ['x', 'W'], outputs: ['h'] },
    { name: '/model/layers.0/Relu', opType: 'Relu', inputs: ['h'], outputs: ['y'] },
  ],
  inputs: ['x'],
  outputs: ['y'],
};

test('onnxNamespace: strips the final segment and the leading slash', () => {
  assert.equal(onnxNamespace('/model/layers.0/MatMul'), 'model/layers.0');
  assert.equal(onnxNamespace('Add'), '');
  assert.equal(onnxNamespace(''), '');
  assert.equal(onnxNamespace('/Top'), '');
});

test('onnxToGraphCollection: wires an edge producer-output → consumer-input', () => {
  const gc = onnxToGraphCollection(G, new Set(['W']), 'm');
  assert.equal(gc.label, 'm');
  assert.equal(gc.graphs.length, 1);
  const [matmul, relu] = gc.graphs[0].nodes;
  assert.equal(matmul.id, 'n0');
  assert.equal(matmul.label, 'MatMul');
  assert.equal(matmul.namespace, 'model/layers.0');
  // MatMul's inputs x (graph input) and W (initializer) have no producer → no edge.
  assert.equal(matmul.incomingEdges, undefined);
  // Relu consumes h, produced by MatMul's output slot 0 into Relu's input slot 0.
  assert.deepEqual(relu.incomingEdges, [{ sourceNodeId: 'n0', sourceNodeOutputId: '0', targetNodeInputId: '0' }]);
});

test('onnxToGraphCollection: initializer inputs are flagged const in metadata', () => {
  const gc = onnxToGraphCollection(G, new Set(['W']));
  const matmul = gc.graphs[0].nodes[0];
  // input slot 1 is W (an initializer) → tensor + kind=const; slot 0 is x → tensor only.
  assert.deepEqual(matmul.inputsMetadata, [
    { id: '0', attrs: [{ key: 'tensor', value: 'x' }] },
    { id: '1', attrs: [{ key: 'tensor', value: 'W' }, { key: 'kind', value: 'const' }] },
  ]);
  assert.deepEqual(matmul.outputsMetadata, [{ id: '0', attrs: [{ key: 'tensor', value: 'h' }] }]);
  assert.deepEqual(matmul.attrs, [{ key: 'op', value: 'MatMul' }, { key: 'name', value: '/model/layers.0/MatMul' }]);
});

test('onnxToGraphCollection: a self-referential output does not make a self-edge', () => {
  const self: OnnxGraphData = { nodes: [{ name: 'loop', opType: 'Loop', inputs: ['s'], outputs: ['s'] }], inputs: [], outputs: [] };
  const gc = onnxToGraphCollection(self, new Set());
  assert.equal(gc.graphs[0].nodes[0].incomingEdges, undefined);
});

test('graphCollectionToDot: emits nodes + de-duplicated edges', () => {
  const dot = graphCollectionToDot(onnxToGraphCollection(G, new Set(['W'])));
  assert.match(dot, /^digraph G \{/);
  assert.ok(dot.includes('rankdir="LR";'));
  assert.ok(dot.includes('"n0" [label="MatMul"'));
  assert.ok(dot.includes('"n1" [label="Relu"'));
  assert.ok(dot.includes('"n0" -> "n1";'));
  assert.ok(dot.trimEnd().endsWith('}'));
});

test('graphCollectionToDot: an empty graph is a valid empty digraph', () => {
  assert.equal(graphCollectionToDot({ label: 'x', graphs: [] }), 'digraph G {\n}\n');
});

test('checkpointNamespace: dotted weight path minus the leaf, re-joined with /', () => {
  // Every dot is a level (so layers→0 nest, matching buildTree + ×N collapse).
  assert.equal(checkpointNamespace('model.layers.0.q_proj.weight'), 'model/layers/0/q_proj');
  assert.equal(checkpointNamespace('lm_head.weight'), 'lm_head');
  assert.equal(checkpointNamespace('embedding'), '');
});

test('checkpointToGraphCollection: each tensor is a namespaced leaf node, no edges', () => {
  const tensors: TensorInfo[] = [
    { name: 'model.layers.0.q_proj.weight', dtype: 'bf16', shape: [4096, 4096], params: 16777216 },
    { name: 'lm_head.weight', dtype: 'bf16', shape: [128, 4096], params: 524288 },
  ];
  const gc = checkpointToGraphCollection(tensors, 'm');
  assert.equal(gc.label, 'm');
  const [n0, n1] = gc.graphs[0].nodes;
  assert.equal(n0.id, 't0');
  assert.equal(n0.label, 'weight');
  assert.equal(n0.namespace, 'model/layers/0/q_proj');
  assert.equal(n0.incomingEdges, undefined);
  assert.deepEqual(n0.attrs, [
    { key: 'dtype', value: 'bf16' },
    { key: 'shape', value: '[4096, 4096]' },
    { key: 'params', value: '16777216' },
  ]);
  assert.equal(n1.namespace, 'lm_head');
});

test('shortOpLabel: aten target → op short name; op fallback', () => {
  assert.equal(shortOpLabel('aten.addmm.default', 'call_function'), 'addmm');
  assert.equal(shortOpLabel('torch.ops.aten.relu.default', 'call_function'), 'relu');
  assert.equal(shortOpLabel('placeholder', 'placeholder'), 'placeholder');
});

test('exportToGraphCollection: FX nodes → schema graph, edges by data flow, shapes as metadata', () => {
  const g: ExportGraph = {
    nodes: [
      { id: 'x', op: 'placeholder', target: 'placeholder', namespace: '', inputs: [], shape: [1, 4], dtype: 'torch.float32' },
      { id: 'lin', op: 'call_function', target: 'aten.addmm.default', namespace: 'blocks/0', inputs: ['x'], shape: [1, 8], dtype: 'torch.float32' },
      { id: 'out', op: 'output', target: 'output', namespace: '', inputs: ['lin'], shape: null, dtype: null },
    ],
  };
  const gc = exportToGraphCollection(g, 'm');
  assert.equal(gc.label, 'm');
  const [x, lin, out] = gc.graphs[0].nodes;
  assert.equal(lin.label, 'addmm');
  assert.equal(lin.namespace, 'blocks/0');
  assert.deepEqual(lin.incomingEdges, [{ sourceNodeId: 'x', sourceNodeOutputId: '0', targetNodeInputId: '0' }]);
  assert.deepEqual(lin.outputsMetadata, [{ id: '0', attrs: [{ key: 'shape', value: '[1, 8]' }, { key: 'dtype', value: 'torch.float32' }] }]);
  assert.equal(x.incomingEdges, undefined); // a placeholder has no producer
  assert.deepEqual(out.incomingEdges, [{ sourceNodeId: 'lin', sourceNodeOutputId: '0', targetNodeInputId: '0' }]);
  assert.equal(out.outputsMetadata, undefined); // no shape → no metadata
});

test('exportToGraphCollection: an input referencing an unknown node draws no edge', () => {
  const g: ExportGraph = { nodes: [{ id: 'a', op: 'call_function', target: 't', namespace: '', inputs: ['ghost'], shape: null, dtype: null }] };
  assert.equal(exportToGraphCollection(g).graphs[0].nodes[0].incomingEdges, undefined);
});

test('graphCollectionToDot: labels with quotes/backslashes are escaped', () => {
  const gc: ReturnType<typeof onnxToGraphCollection> = {
    label: 'x',
    graphs: [{ id: 'main', nodes: [{ id: 'a', label: 'Op"x\\y', namespace: '' }] }],
  };
  const dot = graphCollectionToDot(gc);
  assert.ok(dot.includes('label="Op\\"x\\\\y"'));
});
