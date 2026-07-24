/// Tree/collapse checks for the model view (plan §4b ×N repeat-collapse). Pure
/// renderer logic; the frontend has no CI runner, so run locally with
/// `node --test src/state/checkpoint.test.ts` from `desktop/`. tsc covers types.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildTree, collapseRepeats, parseHfConfig, classifyArch, estimateParamsFromConfig, type TensorInfo, type TreeNode } from './checkpoint.ts';

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

// ── §5a config-only entry gate + classification ──────────────────────────────
test('parseHfConfig: accepts a transformers config, rejects generic JSON', () => {
  assert.notEqual(parseHfConfig('{"model_type":"llama","hidden_size":4096}'), null);
  assert.notEqual(parseHfConfig('{"architectures":["LlamaForCausalLM"]}'), null);
  assert.equal(parseHfConfig('{"name":"x","version":1}'), null); // no model_type/architectures
  assert.equal(parseHfConfig('[1,2,3]'), null); // an array, not a config object
  assert.equal(parseHfConfig('not json'), null);
  assert.equal(parseHfConfig(''), null);
  assert.equal(parseHfConfig(undefined), null);
});

test('classifyArch: config-only (no tensors) still yields a card; index names corroborate MoE', () => {
  const config = { model_type: 'mixtral', num_hidden_layers: 32, hidden_size: 4096, num_attention_heads: 32, num_key_value_heads: 8, num_local_experts: 8, num_experts_per_tok: 2 };
  const bare = classifyArch({ config, tensorNames: [] });
  assert.equal(bare?.family, 'Mixtral');
  assert.equal(bare?.template, 'moe');
  assert.ok(bare?.chips.includes('GQA'));
  assert.ok(bare?.chips.includes('MoE'));
  // A sibling index.json's weight-map keys corroborate the expert layout.
  const withIdx = classifyArch({ config: { model_type: 'qwen2' }, tensorNames: ['model.layers.0.mlp.experts.0.gate_proj.weight'] });
  assert.equal(withIdx?.template, 'moe');
});

// ── §5a analytic params from config (T-followup) ─────────────────────────────
test('estimateParamsFromConfig: Llama-3-8B (dense GQA, untied) lands ~8.0B', () => {
  const cfg = { model_type: 'llama', hidden_size: 4096, num_hidden_layers: 32, num_attention_heads: 32, num_key_value_heads: 8, head_dim: 128, intermediate_size: 14336, vocab_size: 128256, tie_word_embeddings: false };
  const p = estimateParamsFromConfig(cfg)!;
  assert.ok(p > 7.8e9 && p < 8.2e9, `expected ~8.0B, got ${(p / 1e9).toFixed(2)}B`);
});

test('estimateParamsFromConfig: Mixtral-8x7B (MoE) lands ~46.7B', () => {
  const cfg = { model_type: 'mixtral', hidden_size: 4096, num_hidden_layers: 32, num_attention_heads: 32, num_key_value_heads: 8, intermediate_size: 14336, vocab_size: 32000, num_local_experts: 8, num_experts_per_tok: 2, tie_word_embeddings: false };
  const p = estimateParamsFromConfig(cfg)!;
  assert.ok(p > 45e9 && p < 48e9, `expected ~46.7B, got ${(p / 1e9).toFixed(2)}B`);
});

test('estimateParamsFromConfig: tied embeddings drop the lm_head', () => {
  const base = { model_type: 'qwen2', hidden_size: 1024, num_hidden_layers: 4, num_attention_heads: 16, intermediate_size: 2816, vocab_size: 151936 };
  const tied = estimateParamsFromConfig({ ...base, tie_word_embeddings: true })!;
  const untied = estimateParamsFromConfig({ ...base, tie_word_embeddings: false })!;
  assert.equal(untied - tied, 1024 * 151936); // exactly one vocab×hidden embedding matrix
});

test('estimateParamsFromConfig: null for MLA and for missing fields', () => {
  assert.equal(estimateParamsFromConfig({ model_type: 'deepseek_v3', hidden_size: 7168, num_hidden_layers: 61, num_attention_heads: 128, vocab_size: 129280, intermediate_size: 18432, kv_lora_rank: 512 }), null);
  assert.equal(estimateParamsFromConfig({ model_type: 'llama', hidden_size: 4096 }), null); // missing layers/heads/vocab/inter
});
