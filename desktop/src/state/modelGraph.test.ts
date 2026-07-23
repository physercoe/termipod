/// Pure-logic tests for the ONNX → Model Explorer GraphCollection adapter and its
/// interim DOT bridge (plan §5 W4). The WebGL visualizer element renders only in a
/// real browser (device-test); this pins the *data* it consumes. Run locally:
/// `node --test src/state/modelGraph.test.ts`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { graphCollectionToDot, onnxNamespace, onnxToGraphCollection } from './modelGraph.ts';
import type { OnnxGraphData } from './checkpoint.ts';

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

test('graphCollectionToDot: labels with quotes/backslashes are escaped', () => {
  const gc: ReturnType<typeof onnxToGraphCollection> = {
    label: 'x',
    graphs: [{ id: 'main', nodes: [{ id: 'a', label: 'Op"x\\y', namespace: '' }] }],
  };
  const dot = graphCollectionToDot(gc);
  assert.ok(dot.includes('label="Op\\"x\\\\y"'));
});
