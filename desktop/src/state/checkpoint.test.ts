/// Tree/collapse checks for the model view (plan §4b ×N repeat-collapse). Pure
/// renderer logic; the frontend has no CI runner, so run locally with
/// `node --test src/state/checkpoint.test.ts` from `desktop/`. tsc covers types.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildTree, collapseRepeats, parseHfConfig, parsePolicyConfig, classifyPolicy, classifyArch, estimateParamsFromConfig, normalizeModelConfig, linearHybridInfo, TEMPLATE_LABEL, type ArchCard, type TensorInfo, type TreeNode } from './checkpoint.ts';

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

// ── LeRobot policy configs (VLA / robot policies) — verified vs live HF configs ──
test('parsePolicyConfig: pi0.5 reads I/O features, horizon, and named backbones', () => {
  const cfg = {
    type: 'pi05',
    input_features: {
      'observation.images.base_0_rgb': { type: 'VISUAL', shape: [3, 224, 224] },
      'observation.images.left_wrist_0_rgb': { type: 'VISUAL', shape: [3, 224, 224] },
      'observation.state': { type: 'STATE', shape: [32] },
    },
    output_features: { action: { type: 'ACTION', shape: [32] } },
    chunk_size: 50,
    n_action_steps: 50,
    n_obs_steps: 1,
    paligemma_variant: 'gemma_2b',
    action_expert_variant: 'gemma_300m',
  };
  const card = parsePolicyConfig(JSON.stringify(cfg))!;
  assert.equal(card.family, 'π0.5 (Pi0.5)');
  assert.equal(card.type, 'pi05');
  assert.equal(card.cameras.length, 2); // two VISUAL inputs
  assert.equal(card.states.length, 1);
  assert.deepEqual(card.actions[0].shape, [32]);
  assert.equal(card.chunkSize, 50);
  // both named backbones surface with their roles
  assert.deepEqual(
    card.backbones.map((b) => b.name).sort(),
    ['gemma_2b', 'gemma_300m'],
  );
  assert.ok(card.chips.includes('VLA') && card.chips.includes('flow-matching'));
});

test('classifyPolicy: ACT inlines encoder/decoder dims (surfaced as dim rows)', () => {
  const card = classifyPolicy({
    type: 'act',
    input_features: { 'observation.state': { type: 'STATE', shape: [14] } },
    output_features: { action: { type: 'ACTION', shape: [14] } },
    dim_model: 512,
    n_heads: 8,
    n_encoder_layers: 4,
    n_decoder_layers: 1,
    vision_backbone: 'resnet18',
    use_vae: true,
  })!;
  assert.equal(card.family, 'ACT (Action Chunking Transformer)');
  const dims = Object.fromEntries(card.dims.map((d) => [d.label, d.value]));
  assert.equal(dims['Model dim'], '512');
  assert.equal(dims['Encoder layers'], '4');
  assert.ok(card.chips.includes('action chunking') && card.chips.includes('CVAE'));
});

test('classifyPolicy: VLA-JEPA reads world-model backbone + head dims', () => {
  const card = classifyPolicy({
    type: 'vla_jepa',
    input_features: { 'observation.images.image': { type: 'VISUAL', shape: [3, 224, 224] } },
    output_features: { action: { type: 'ACTION', shape: [7] } },
    qwen_model_name: 'Qwen/Qwen3-VL-2B-Instruct',
    jepa_encoder_name: 'facebook/vjepa2-vitl-fpc64-256',
    action_hidden_size: 1024,
    action_num_layers: 16,
    enable_world_model: true,
  })!;
  assert.ok(card.backbones.some((b) => b.name.includes('Qwen3-VL')));
  assert.ok(card.backbones.some((b) => b.role === 'World-model encoder'));
  assert.ok(card.chips.includes('JEPA world-model'));
});

test('classifyPolicy: a minimal VLA stub that only names a backbone still resolves', () => {
  // robbyant/lingbot-vla-v2-6b ships literally `{"vlm_family":"qwen3_vl"}`.
  const card = classifyPolicy({ vlm_family: 'qwen3_vl' })!;
  assert.equal(card.family, 'VLA policy');
  assert.deepEqual(card.backbones, [{ role: 'VLM family', name: 'qwen3_vl' }]);
});

test('parsePolicyConfig: a transformer config is NOT mistaken for a policy', () => {
  // Has model_type + decoder dims → belongs to parseHfConfig, not the policy path.
  assert.equal(parsePolicyConfig(JSON.stringify({ model_type: 'llama', num_hidden_layers: 32, hidden_size: 4096, num_attention_heads: 32 })), null);
  // Random JSON with neither a policy `type`+features nor a backbone key → null.
  assert.equal(parsePolicyConfig(JSON.stringify({ foo: 1, bar: [1, 2, 3] })), null);
});

// ── W1a: hybrid linear-attention + honest chips (archgraph plan §5 W1) ─────────
// Fixtures are trimmed to the LM fields under test, but every KEY is copied
// verbatim from the real HF `config.json` at the pinned revision (D-3: verify the
// real config keys before coding — do not guess post-cutoff key names). Re-fetch
// the full file at `https://huggingface.co/<repo>/blob/<rev>/config.json`.

// Kimi K3 — moonshotai/Kimi-K3 @ 9f62e4e9fffbd0a83ddd60e1c209d828994b3569. A
// multimodal wrapper (`model_type: kimi_k3`) nesting a `kimi_linear` LM under
// `text_config`; KDA (linear) interleaved with full Gated-MLA attention 3:1.
const KIMI_K3_FULL_ATTN = [4, 8, 12, 16, 20, 24, 28, 32, 36, 40, 44, 48, 52, 56, 60, 64, 68, 72, 76, 80, 84, 88, 92, 93]; // 24
const KIMI_K3_KDA = [1, 2, 3, 5, 6, 7, 9, 10, 11, 13, 14, 15, 17, 18, 19, 21, 22, 23, 25, 26, 27, 29, 30, 31, 33, 34, 35, 37, 38, 39, 41, 42, 43, 45, 46, 47, 49, 50, 51, 53, 54, 55, 57, 58, 59, 61, 62, 63, 65, 66, 67, 69, 70, 71, 73, 74, 75, 77, 78, 79, 81, 82, 83, 85, 86, 87, 89, 90, 91]; // 69
const KIMI_K3 = {
  model_type: 'kimi_k3',
  architectures: ['KimiK3ForConditionalGeneration'],
  text_config: {
    model_type: 'kimi_linear',
    architectures: ['KimiLinearForCausalLM'],
    hidden_size: 7168, num_hidden_layers: 93, num_attention_heads: 96, num_key_value_heads: 96,
    kv_lora_rank: 512, q_lora_rank: 1536, qk_nope_head_dim: 128, qk_rope_head_dim: 64, v_head_dim: 128,
    mla_use_nope: true, mla_use_output_gate: true, hidden_act: 'situ', rms_norm_eps: 1e-5,
    intermediate_size: 33792, moe_intermediate_size: 3072, num_experts: 896, num_experts_per_token: 16,
    num_shared_experts: 2, first_k_dense_replace: 1, num_nextn_predict_layers: 0,
    max_position_embeddings: 1048576, vocab_size: 163840, tie_word_embeddings: false,
    linear_attn_config: { full_attn_layers: KIMI_K3_FULL_ATTN, kda_layers: KIMI_K3_KDA, num_heads: 96, short_conv_kernel_size: 4 },
  },
  vision_config: { hidden_size: 1024, vt_num_hidden_layers: 27 },
};

// Kimi Linear 48B-A3B — moonshotai/Kimi-Linear-48B-A3B-Instruct @ e1df551a447157d4658b573f9a695d57658590e9 (flat).
const KIMI_LINEAR = {
  model_type: 'kimi_linear', architectures: ['KimiLinearForCausalLM'],
  hidden_size: 2304, num_hidden_layers: 27, num_attention_heads: 32, num_key_value_heads: 32, head_dim: 72,
  kv_lora_rank: 512, q_lora_rank: null, qk_nope_head_dim: 128, qk_rope_head_dim: 64, v_head_dim: 128,
  mla_use_nope: true, hidden_act: 'silu', rms_norm_eps: 1e-5, rope_theta: 10000.0, rope_scaling: null,
  intermediate_size: 9216, moe_intermediate_size: 1024, num_experts: 256, num_experts_per_token: 8,
  num_shared_experts: 1, first_k_dense_replace: 1, num_nextn_predict_layers: 0, vocab_size: 163840, tie_word_embeddings: false,
  linear_attn_config: { full_attn_layers: [4, 8, 12, 16, 20, 24, 27], kda_layers: [1, 2, 3, 5, 6, 7, 9, 10, 11, 13, 14, 15, 17, 18, 19, 21, 22, 23, 25, 26], num_heads: 32, short_conv_kernel_size: 4 },
};

// Qwen3-Next 80B-A3B — Qwen/Qwen3-Next-80B-A3B-Instruct @ 9c7f2fbe84465e40164a94cc16cd30b6999b0cc7 (flat).
// Gated DeltaNet (linear) on most layers, full GQA attention every 4th; MoE.
const QWEN3_NEXT = {
  model_type: 'qwen3_next', architectures: ['Qwen3NextForCausalLM'],
  hidden_size: 2048, num_hidden_layers: 48, num_attention_heads: 16, num_key_value_heads: 2, head_dim: 256,
  full_attention_interval: 4, linear_conv_kernel_dim: 4, linear_key_head_dim: 128, linear_num_key_heads: 16,
  linear_num_value_heads: 32, linear_value_head_dim: 128, partial_rotary_factor: 0.25, use_sliding_window: false,
  hidden_act: 'silu', rms_norm_eps: 1e-6, rope_theta: 10000000, rope_scaling: null,
  intermediate_size: 5120, moe_intermediate_size: 512, num_experts: 512, num_experts_per_tok: 10,
  shared_expert_intermediate_size: 512, max_position_embeddings: 262144, vocab_size: 151936, tie_word_embeddings: false,
};

// Gemma 3 27B — unsloth/gemma-3-27b-it @ 7a5a3053dbd5d1d58e48159e87b9df2fc545a49a (ungated mirror of
// google/gemma-3-27b-it). Wrapper nesting a `gemma3_text` LM; dense GQA with a 5:1 sliding/global window pattern.
const GEMMA3 = {
  model_type: 'gemma3', architectures: ['Gemma3ForConditionalGeneration'],
  text_config: {
    model_type: 'gemma3_text', hidden_size: 5376, num_hidden_layers: 62, num_attention_heads: 32, num_key_value_heads: 16,
    head_dim: 128, hidden_activation: 'gelu_pytorch_tanh', rms_norm_eps: 1e-6, sliding_window: 1024, sliding_window_pattern: 6,
    rope_theta: 1000000.0, rope_local_base_freq: 10000.0, rope_scaling: { rope_type: 'linear', factor: 8.0 },
    intermediate_size: 21504, max_position_embeddings: 131072, vocab_size: 262208,
  },
  vision_config: { model_type: 'siglip_vision_model', hidden_size: 1152 },
};

// DeepSeek-V3 — deepseek-ai/DeepSeek-V3 @ e815299b0bcbac849fa540c768ef21845365c9eb (flat). Uniform MLA + MoE
// with an MTP head — a REGRESSION fixture: it must stay `mla-moe`, never `linear-hybrid`.
const DEEPSEEK_V3 = {
  model_type: 'deepseek_v3', architectures: ['DeepseekV3ForCausalLM'],
  hidden_size: 7168, num_hidden_layers: 61, num_attention_heads: 128, num_key_value_heads: 128,
  kv_lora_rank: 512, q_lora_rank: 1536, qk_nope_head_dim: 128, qk_rope_head_dim: 64, v_head_dim: 128,
  hidden_act: 'silu', rms_norm_eps: 1e-6, rope_theta: 10000, rope_scaling: { type: 'yarn', factor: 40 },
  intermediate_size: 18432, moe_intermediate_size: 2048, n_routed_experts: 256, n_shared_experts: 1,
  num_experts_per_tok: 8, first_k_dense_replace: 3, num_nextn_predict_layers: 1, vocab_size: 129280, tie_word_embeddings: false,
};

// Kimi K2 — moonshotai/Kimi-K2-Instruct @ fd1984e2b7a3350dbf7305fe73a4ede25c14de50 (flat). Uniform MLA + MoE
// (DeepSeek-V3 arch, no MTP). REGRESSION fixture: stays `mla-moe`.
const KIMI_K2 = {
  model_type: 'kimi_k2', architectures: ['DeepseekV3ForCausalLM'],
  hidden_size: 7168, num_hidden_layers: 61, num_attention_heads: 64, num_key_value_heads: 64,
  kv_lora_rank: 512, q_lora_rank: 1536, qk_nope_head_dim: 128, qk_rope_head_dim: 64, v_head_dim: 128,
  hidden_act: 'silu', rms_norm_eps: 1e-6, rope_theta: 50000.0, rope_scaling: { type: 'yarn', factor: 32.0 },
  intermediate_size: 18432, moe_intermediate_size: 2048, n_routed_experts: 384, n_shared_experts: 1,
  num_experts_per_tok: 8, first_k_dense_replace: 1, num_nextn_predict_layers: 0, vocab_size: 163840, tie_word_embeddings: false,
};

// MiniMax-M2 — MiniMaxAI/MiniMax-M2 @ 757303d492a50514c312788b5247a4f696a4c6a3 (flat). Carries an
// `attn_type_list` (hybrid-CAPABLE schema) but M2 sets it uniformly to full attention — so it must classify
// as a plain GQA + MoE `moe`, NOT `linear-hybrid` (D-4: never a guessed hybrid).
const MINIMAX_M2 = {
  model_type: 'minimax_m2', architectures: ['MiniMaxM2ForCausalLM'],
  hidden_size: 3072, num_hidden_layers: 62, num_attention_heads: 48, num_key_value_heads: 8, head_dim: 128,
  hidden_act: 'silu', rms_norm_eps: 1e-6, rope_theta: 5000000, attn_type_list: Array(62).fill(1),
  intermediate_size: 1536, num_local_experts: 256, num_experts_per_tok: 8, vocab_size: 200064, tie_word_embeddings: false,
};

function classifyFixture(cfg: Record<string, unknown>): ArchCard {
  const parsed = parseHfConfig(JSON.stringify(cfg));
  assert.ok(parsed, 'fixture should parse as an HF config');
  const card = classifyArch({ config: parsed, tensorNames: [] });
  assert.ok(card, 'fixture should classify to a card');
  return card as ArchCard;
}

test('linearHybridInfo: Kimi K3 reads the KDA/full-attn split from linear_attn_config', () => {
  const info = linearHybridInfo(parseHfConfig(JSON.stringify(KIMI_K3))!)!;
  assert.equal(info.kind, 'Kimi Delta Attention');
  assert.equal(info.fullAttnLayers, 24);
  assert.equal(info.linearAttnLayers, 69); // 3:1 KDA:full (69 ÷ 24 ≈ 2.9)
  assert.equal(info.fullAttnLayers! + info.linearAttnLayers!, 93);
});

test('linearHybridInfo: Qwen3-Next derives the split from full_attention_interval', () => {
  const info = linearHybridInfo(QWEN3_NEXT)!;
  assert.equal(info.kind, 'Gated DeltaNet');
  assert.equal(info.fullAttnLayers, 12); // 48 / 4
  assert.equal(info.linearAttnLayers, 36);
});

test('linearHybridInfo: a homogeneous config (DeepSeek-V3) is not hybrid', () => {
  assert.equal(linearHybridInfo(DEEPSEEK_V3), null);
  assert.equal(linearHybridInfo(MINIMAX_M2), null); // attn_type_list all-full → not hybrid
});

test('classifyArch W1a: Kimi K3 is linear-hybrid, NOT uniform mla-moe (the core fix)', () => {
  const card = classifyFixture(KIMI_K3);
  assert.equal(card.family, 'Kimi K3'); // wrapper name wins over the inner kimi_linear id
  assert.equal(card.template, 'linear-hybrid');
  assert.notEqual(card.template, 'mla-moe'); // G1/G2: the exact misrender this fixes
  assert.equal(card.linearKind, 'Kimi Delta Attention');
  assert.equal(card.fullAttnLayers, 24);
  assert.equal(card.linearAttnLayers, 69);
  assert.equal(card.expertsPerTok, 16); // num_experts_per_token (Kimi spelling) now read
  assert.equal(card.experts, 896);
  assert.equal(card.sharedExperts, 2);
  for (const chip of ['MLA', 'MoE', 'shared-experts', 'NoPE', 'gated-attn', 'RMSNorm', 'Kimi Delta Attention']) {
    assert.ok(card.chips.includes(chip), `expected chip ${chip} in [${card.chips.join(', ')}]`);
  }
  // `situ` is a novel activation — no guessed GLU chip (honest).
  assert.ok(!card.chips.includes('SwiGLU') && !card.chips.includes('GeGLU'));
});

test('classifyArch W1a: Kimi Linear 48B (standalone) is Kimi Linear / linear-hybrid', () => {
  const card = classifyFixture(KIMI_LINEAR);
  assert.equal(card.family, 'Kimi Linear'); // no wrapper → inner family
  assert.equal(card.template, 'linear-hybrid');
  assert.equal(card.linearKind, 'Kimi Delta Attention');
  assert.equal(card.fullAttnLayers, 7);
  assert.equal(card.linearAttnLayers, 20);
  assert.equal(card.expertsPerTok, 8);
  for (const chip of ['MLA', 'MoE', 'NoPE', 'SwiGLU', 'Kimi Delta Attention']) assert.ok(card.chips.includes(chip), chip);
});

test('classifyArch W1a: Qwen3-Next is Gated-DeltaNet linear-hybrid (GQA, no MLA)', () => {
  const card = classifyFixture(QWEN3_NEXT);
  assert.equal(card.family, 'Qwen3-Next');
  assert.equal(card.template, 'linear-hybrid');
  assert.equal(card.linearKind, 'Gated DeltaNet');
  assert.equal(card.expertsPerTok, 10);
  for (const chip of ['GQA', 'MoE', 'partial-RoPE', 'SwiGLU', 'Gated DeltaNet']) assert.ok(card.chips.includes(chip), chip);
  assert.ok(!card.chips.includes('MLA')); // no kv_lora_rank
  assert.ok(!card.chips.includes('sliding-window')); // use_sliding_window: false
});

test('classifyArch W1a: Gemma 3 (wrapper→gemma3_text) is dense GQA with a sliding-window chip', () => {
  const card = classifyFixture(GEMMA3);
  assert.equal(card.family, 'Gemma 3'); // gemma3_text now maps cleanly (was "Gemma3_text")
  assert.equal(card.template, 'dense-gqa');
  for (const chip of ['GQA', 'GeGLU', 'sliding-window', 'RMSNorm']) assert.ok(card.chips.includes(chip), chip);
  assert.ok(!card.chips.includes('MoE') && !card.chips.includes('MLA') && !card.chips.includes('YaRN')); // rope_type: linear ≠ yarn
});

test('classifyArch W1a: DeepSeek-V3 stays mla-moe with YaRN + MTP chips (regression)', () => {
  const card = classifyFixture(DEEPSEEK_V3);
  assert.equal(card.family, 'DeepSeek-V3');
  assert.equal(card.template, 'mla-moe');
  assert.equal(card.expertsPerTok, 8);
  for (const chip of ['MLA', 'MoE', 'shared-experts', 'YaRN', 'MTP', 'SwiGLU']) assert.ok(card.chips.includes(chip), chip);
});

test('classifyArch W1a: Kimi K2 stays mla-moe, no MTP (regression)', () => {
  const card = classifyFixture(KIMI_K2);
  assert.equal(card.family, 'Kimi K2');
  assert.equal(card.template, 'mla-moe');
  assert.ok(card.chips.includes('MLA') && card.chips.includes('MoE') && card.chips.includes('YaRN'));
  assert.ok(!card.chips.includes('MTP')); // num_nextn_predict_layers: 0
});

test('classifyArch W1a: MiniMax-M2 is a plain GQA MoE, not a guessed hybrid', () => {
  const card = classifyFixture(MINIMAX_M2);
  assert.equal(card.family, 'MiniMax-M2');
  assert.equal(card.template, 'moe'); // attn_type_list is uniformly full attention → NOT linear-hybrid
  assert.equal(card.linearKind, undefined);
  assert.ok(card.chips.includes('GQA') && card.chips.includes('MoE'));
});

test('estimateParamsFromConfig: Kimi active-expert count reads num_experts_per_token', () => {
  // Before the key fix, Kimi's `num_experts_per_token` was unread → activeOnly
  // fell back to ALL experts, so active === total. Now active « total.
  const total = estimateParamsFromConfig(KIMI_LINEAR)!;
  const active = estimateParamsFromConfig(KIMI_LINEAR, { activeOnly: true })!;
  assert.ok(active < total * 0.35, `active ${(active / 1e9).toFixed(2)}B should be « total ${(total / 1e9).toFixed(2)}B`);
});

test('TEMPLATE_LABEL: every ArchTemplate (incl. linear-hybrid) has a label', () => {
  for (const t of ['dense-gqa', 'moe', 'mla', 'mla-moe', 'linear-hybrid', 'unknown'] as const) {
    assert.ok(typeof TEMPLATE_LABEL[t] === 'string' && TEMPLATE_LABEL[t].length > 0);
  }
});
