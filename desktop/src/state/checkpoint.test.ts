/// Tree/collapse checks for the model view (plan §4b ×N repeat-collapse). Pure
/// renderer logic; the frontend has no CI runner, so run locally with
/// `node --test src/state/checkpoint.test.ts` from `desktop/`. tsc covers types.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildTree, collapseRepeats, type TensorInfo, type TreeNode } from './checkpoint.ts';

function tensors(names: Array<[string, number[]]>, dtype = 'F16'): TensorInfo[] {
  return names.map(([name, shape]) => ({ name, dtype, shape, params: shape.reduce((a, b) => a * b, 1) }));
}
function child(node: TreeNode, key: string): TreeNode | undefined {
  return node.children.find((c) => c.key === key);
}

// A regular decoder-layer block: attn + mlp weights, per layer.
function layerBlock(layers: number): TensorInfo[] {
  const t: Array<[string, number[]]> = [];
  for (let i = 0; i < layers; i += 1) {
    t.push([`model.layers.${i}.self_attn.q_proj.weight`, [16, 16]]);
    t.push([`model.layers.${i}.mlp.gate_proj.weight`, [32, 16]]);
  }
  t.push(['model.embed_tokens.weight', [100, 16]]);
  return tensors(t);
}

test('collapseRepeats: N identical layers become one × N node with aggregate params', () => {
  const tree = buildTree(layerBlock(8));
  const layers = child(child(tree, 'model')!, 'layers')!;
  assert.equal(layers.children.length, 8); // raw: 8 siblings

  const collapsed = collapseRepeats(tree);
  const cl = child(child(collapsed, 'model')!, 'layers')!;
  assert.equal(cl.children.length, 1); // collapsed to one group
  const grp = cl.children[0];
  assert.equal(grp.repeat?.count, 8);
  assert.equal(grp.key, '[0–7]');
  // aggregate = 8 × (q_proj 256 + gate_proj 512) = 8 × 768.
  assert.equal(grp.params, 8 * (256 + 512));
  // children are ONE member's structure (self_attn + mlp), per-member params.
  assert.deepEqual(grp.children.map((c) => c.key).sort(), ['mlp', 'self_attn']);
});

test('collapseRepeats: a run below minRun is left expanded', () => {
  const collapsed = collapseRepeats(buildTree(layerBlock(2)));
  const cl = child(child(collapsed, 'model')!, 'layers')!;
  assert.equal(cl.children.length, 2); // 2 < minRun(3) → untouched
  assert.ok(!cl.children[0].repeat);
});

test('collapseRepeats: heterogeneous stack splits by structure, not force-merged', () => {
  // layers 0-2 are dense (mlp.gate), layers 3-7 are MoE (mlp.experts.*).
  const t: Array<[string, number[]]> = [];
  for (let i = 0; i < 3; i += 1) t.push([`model.layers.${i}.mlp.gate_proj.weight`, [32, 16]]);
  for (let i = 3; i < 8; i += 1) {
    for (let e = 0; e < 4; e += 1) t.push([`model.layers.${i}.mlp.experts.${e}.w1.weight`, [32, 16]]);
  }
  const collapsed = collapseRepeats(buildTree(tensors(t)));
  const cl = child(child(collapsed, 'model')!, 'layers')!;
  // two groups: dense [0–2] ×3 and MoE [3–7] ×5.
  assert.equal(cl.children.length, 2);
  const counts = cl.children.map((c) => c.repeat?.count).sort();
  assert.deepEqual(counts, [3, 5]);
  const moe = cl.children.find((c) => c.repeat?.from === 3)!;
  // nested collapse: the 4 experts inside a MoE layer are themselves a × 4 group.
  const experts = child(child(moe, 'mlp')!, 'experts')!;
  assert.equal(experts.children.length, 1);
  assert.equal(experts.children[0].repeat?.count, 4);
});

test('collapseRepeats: differing shapes are NOT collapsed together', () => {
  // Two "layers" with different weight shapes must stay separate.
  const t = tensors([
    ['blocks.0.w.weight', [16, 16]],
    ['blocks.1.w.weight', [16, 16]],
    ['blocks.2.w.weight', [32, 32]], // different shape
  ]);
  const collapsed = collapseRepeats(buildTree(t), 2);
  const blocks = child(collapsed, 'blocks')!;
  // 0 and 1 (same shape) collapse to [0–1]; 2 stays a singleton.
  assert.equal(blocks.children.length, 2);
  assert.ok(blocks.children.some((c) => c.repeat?.count === 2));
  assert.ok(blocks.children.some((c) => c.key === '2'));
});
