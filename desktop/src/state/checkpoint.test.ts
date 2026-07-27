/// Tree/collapse checks for the model view (plan §4b ×N repeat-collapse). Pure
/// renderer logic; the frontend has no CI runner, so run locally with
/// `node --test src/state/checkpoint.test.ts` from `desktop/`. tsc covers types.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildTree, collapseRepeats, parseHfConfig, classifyArch, estimateParamsFromConfig, normalizeModelConfig, type TensorInfo, type TreeNode } from './checkpoint.ts';

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

test('estimateParamsFromConfig: DeepSeek-V3 (MLA + MoE + first_k_dense) lands ~671B', () => {
  const cfg = {
    model_type: 'deepseek_v3', hidden_size: 7168, num_hidden_layers: 61, num_attention_heads: 128, vocab_size: 129280,
    q_lora_rank: 1536, kv_lora_rank: 512, qk_nope_head_dim: 128, qk_rope_head_dim: 64, v_head_dim: 128,
    intermediate_size: 18432, moe_intermediate_size: 2048, n_routed_experts: 256, n_shared_experts: 1,
    num_experts_per_tok: 8, first_k_dense_replace: 3, tie_word_embeddings: false,
  };
  const p = estimateParamsFromConfig(cfg)!;
  assert.ok(p > 650e9 && p < 690e9, `expected ~671B, got ${(p / 1e9).toFixed(1)}B`);
});

test('estimateParamsFromConfig: non-gated legacy MLP (GPT-2) uses the 2× FFN', () => {
  const base = { hidden_size: 768, num_hidden_layers: 12, num_attention_heads: 12, vocab_size: 50257, n_inner: 3072, tie_word_embeddings: true };
  const gpt2 = estimateParamsFromConfig({ ...base, model_type: 'gpt2' })!;
  const gated = estimateParamsFromConfig({ ...base, model_type: 'llama' })!;
  // Same dims, gated adds one more hidden×inter matrix per layer → strictly more.
  assert.ok(gated > gpt2);
  assert.ok(gpt2 > 100e6 && gpt2 < 130e6, `GPT-2 ~124M, got ${(gpt2 / 1e6).toFixed(0)}M`);
});

test('estimateParamsFromConfig: null only for genuinely missing fields', () => {
  assert.equal(estimateParamsFromConfig({ model_type: 'llama', hidden_size: 4096 }), null); // no layers/heads/vocab/inter
});

// The classics as they REALLY ship on HF: gpt2's config carries `n_inner: null`
// and falcon has no FFN-width field at all — both default to 4·hidden in
// transformers. falcon-7b also signals multi-query via `multi_query: true`.
test('estimateParamsFromConfig: real gpt2 config (n_inner null) lands ~124M', () => {
  const cfg = { model_type: 'gpt2', n_embd: 768, n_layer: 12, n_head: 12, vocab_size: 50257, n_inner: null };
  const p = estimateParamsFromConfig(cfg)!;
  assert.ok(p !== null && p > 100e6 && p < 130e6, `expected ~124M, got ${p === null ? 'null' : (p / 1e6).toFixed(0) + 'M'}`);
});

test('estimateParamsFromConfig: real falcon-7b config (no FFN field, multi_query) lands ~7B', () => {
  const cfg = { model_type: 'falcon', hidden_size: 4544, num_hidden_layers: 32, num_attention_heads: 71, vocab_size: 65024, multi_query: true, tie_word_embeddings: false };
  const p = estimateParamsFromConfig(cfg)!;
  assert.ok(p !== null && p > 6.4e9 && p < 7.6e9, `expected ~7B, got ${p === null ? 'null' : (p / 1e9).toFixed(2) + 'B'}`);
});

// ── newest influential MoE models on HF (reuse DeepSeek-V3 field conventions) ──
test('estimateParamsFromConfig: Qwen3-235B-A22B (qwen3_moe, GQA+MoE) lands ~235B', () => {
  const cfg = { model_type: 'qwen3_moe', hidden_size: 4096, num_hidden_layers: 94, num_attention_heads: 64, num_key_value_heads: 4, head_dim: 128, moe_intermediate_size: 1536, num_experts: 128, num_experts_per_tok: 8, vocab_size: 151936, tie_word_embeddings: false };
  const p = estimateParamsFromConfig(cfg)!;
  assert.ok(p > 215e9 && p < 255e9, `expected ~235B, got ${(p / 1e9).toFixed(1)}B`);
});

test('estimateParamsFromConfig: Kimi K2 (kimi_k2, MLA+MoE) lands ~1.0T', () => {
  const cfg = { model_type: 'kimi_k2', hidden_size: 7168, num_hidden_layers: 61, num_attention_heads: 64, q_lora_rank: 1536, kv_lora_rank: 512, qk_nope_head_dim: 128, qk_rope_head_dim: 64, v_head_dim: 128, intermediate_size: 18432, moe_intermediate_size: 2048, n_routed_experts: 384, n_shared_experts: 1, num_experts_per_tok: 8, first_k_dense_replace: 1, vocab_size: 163840, tie_word_embeddings: false };
  const p = estimateParamsFromConfig(cfg)!;
  assert.ok(p > 0.95e12 && p < 1.15e12, `expected ~1.0T, got ${(p / 1e12).toFixed(2)}T`);
});

test('estimateParamsFromConfig: GLM-4.5-Air (glm4_moe, GQA+MoE+first_k_dense) is modelled', () => {
  const cfg = { model_type: 'glm4_moe', hidden_size: 4096, num_hidden_layers: 46, num_attention_heads: 96, num_key_value_heads: 8, intermediate_size: 10944, moe_intermediate_size: 1408, n_routed_experts: 128, n_shared_experts: 1, num_experts_per_tok: 8, first_k_dense_replace: 1, vocab_size: 151552, tie_word_embeddings: false };
  const p = estimateParamsFromConfig(cfg)!;
  assert.ok(p > 80e9 && p < 130e9, `expected ~106B, got ${(p / 1e9).toFixed(1)}B`);
});

// ── active vs total params (powers the FLOPS estimator's per-token compute) ──
test('estimateParamsFromConfig activeOnly: MoE active « total, dense unchanged', () => {
  // Qwen3-235B-A22B: 128 experts, top-8 → ~22B active of ~235B total.
  const moe = { model_type: 'qwen3_moe', hidden_size: 4096, num_hidden_layers: 94, num_attention_heads: 64, num_key_value_heads: 4, head_dim: 128, moe_intermediate_size: 1536, num_experts: 128, num_experts_per_tok: 8, vocab_size: 151936, tie_word_embeddings: false };
  const total = estimateParamsFromConfig(moe)!;
  const active = estimateParamsFromConfig(moe, { activeOnly: true })!;
  assert.ok(active < total * 0.2, `active ${(active / 1e9).toFixed(1)}B should be «total ${(total / 1e9).toFixed(1)}B`);
  assert.ok(active > 15e9 && active < 30e9, `expected ~22B active, got ${(active / 1e9).toFixed(1)}B`);
  // Dense model: active === total (no routed experts to gate).
  const dense = { model_type: 'llama', hidden_size: 4096, num_hidden_layers: 32, num_attention_heads: 32, num_key_value_heads: 8, head_dim: 128, intermediate_size: 14336, vocab_size: 128256, tie_word_embeddings: false };
  assert.equal(estimateParamsFromConfig(dense, { activeOnly: true }), estimateParamsFromConfig(dense));
});

// ── multimodal wrapper flattening (Mistral3/Gemma-3/Llama-4 nest the LM config) ──
test('normalizeModelConfig: flattens a nested text_config up', () => {
  const wrapper = {
    model_type: 'mistral3',
    architectures: ['Mistral3ForConditionalGeneration'],
    vision_config: { hidden_size: 1024 },
    text_config: { model_type: 'mistral', num_hidden_layers: 40, hidden_size: 5120, num_attention_heads: 32, num_key_value_heads: 8, head_dim: 128 },
  };
  const flat = normalizeModelConfig(wrapper);
  assert.equal(flat.num_hidden_layers, 40); // decoder depth surfaced
  assert.equal(flat.model_type, 'mistral'); // sub model_type wins
  assert.deepEqual(flat.architectures, ['Mistral3ForConditionalGeneration']); // wrapper metadata survives
});

test('normalizeModelConfig: an already-flat config is returned untouched', () => {
  const flat = { model_type: 'llama', num_hidden_layers: 32, hidden_size: 4096 };
  assert.equal(normalizeModelConfig(flat), flat); // same reference, no wrapping
});

test('parseHfConfig: a multimodal wrapper parses to a flattened LM config', () => {
  const text = JSON.stringify({ model_type: 'gemma3', architectures: ['Gemma3ForConditionalGeneration'], text_config: { num_hidden_layers: 62, hidden_size: 5376, num_attention_heads: 32 } });
  const cfg = parseHfConfig(text)!;
  assert.equal(cfg.num_hidden_layers, 62);
});

// ── newest 2026 families (verified against live HF configs) ──
test('classifyArch: newest model_types map to clean family names', () => {
  const fam = (config: Record<string, unknown>): string => classifyArch({ config, tensorNames: [] })!.family;
  // GLM-5.2 (flat, glm_moe_dsa, MLA+MoE via kv_lora_rank)
  assert.equal(fam({ model_type: 'glm_moe_dsa', num_hidden_layers: 78, hidden_size: 6144, num_attention_heads: 64, kv_lora_rank: 512, n_routed_experts: 256, num_experts_per_tok: 8 }), 'GLM-5');
  // DeepSeek-V4
  assert.equal(fam({ model_type: 'deepseek_v4', num_hidden_layers: 61, hidden_size: 7168, num_attention_heads: 128, q_lora_rank: 1536, n_routed_experts: 384 }), 'DeepSeek-V4');
  // Qwen3.6 dense + MoE (post-normalize the model_type is the inner *_text form)
  assert.equal(fam({ model_type: 'qwen3_5_text', num_hidden_layers: 64, hidden_size: 5120, num_attention_heads: 24, num_key_value_heads: 4 }), 'Qwen3.6');
  assert.equal(fam({ model_type: 'qwen3_5_moe_text', num_hidden_layers: 62, hidden_size: 4096, num_attention_heads: 64, num_experts: 128, num_experts_per_tok: 8, moe_intermediate_size: 1536 }), 'Qwen3.6-MoE');
});
