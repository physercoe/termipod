/// VRAM-estimator arithmetic checks against known models (plan §4b). The frontend
/// package has no CI test runner (renderer logic is tsc + E2E only), so this file
/// is run locally with `node --test src/state/vram.test.ts` from `desktop/`; it
/// documents the expected numbers and pins the GQA vs MLA branches.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { estimateVram, deriveVramInputs, defaultServingDtype, kvCacheBytesPerToken, classifyKvCache, deriveKvCacheClass, DTYPE_BYTES, OPTIMIZER_STATE_BYTES, type VramInputs } from './vram.ts';
import { classifyArch, parseHfConfig } from './checkpoint.ts';

const GiB = 1024 ** 3;

// Llama-3-8B: 32 layers, hidden 4096, 32 heads, 8 KV heads (GQA), head_dim 128.
const LLAMA3_8B: VramInputs = {
  totalParams: 8.03e9,
  weightBytes: DTYPE_BYTES.bf16,
  layers: 32,
  hidden: 4096,
  heads: 32,
  kvHeads: 8,
  headDim: 128,
  isMla: false,
};

test('GQA KV cache: Llama-3-8B ≈ 1 GiB at 8k context, batch 1, fp16 KV', () => {
  const e = estimateVram(LLAMA3_8B, { batch: 1, context: 8192, kvBytes: 2 });
  assert.ok(e.kvComputable);
  // 2 × 32 × 8192 × 1 × 8 × 128 × 2 = exactly 1 GiB.
  assert.equal(e.kvBytes, 2 * 32 * 8192 * 1 * 8 * 128 * 2);
  assert.equal(e.kvBytes, GiB);
  // Weights = 8.03B × 2 bytes ≈ 16 GB.
  assert.ok(e.weightsBytes > 15e9 && e.weightsBytes < 17e9);
});

test('GQA KV scales linearly with batch and context', () => {
  const a = estimateVram(LLAMA3_8B, { batch: 1, context: 8192, kvBytes: 2 });
  const b = estimateVram(LLAMA3_8B, { batch: 4, context: 8192, kvBytes: 2 });
  const c = estimateVram(LLAMA3_8B, { batch: 1, context: 32768, kvBytes: 2 });
  assert.equal(b.kvBytes, a.kvBytes * 4);
  assert.equal(c.kvBytes, a.kvBytes * 4);
});

test('GQA is smaller than full MHA (kvHeads < heads)', () => {
  const gqa = estimateVram(LLAMA3_8B, { batch: 1, context: 8192, kvBytes: 2 });
  const mha = estimateVram({ ...LLAMA3_8B, kvHeads: 32 }, { batch: 1, context: 8192, kvBytes: 2 });
  assert.equal(mha.kvBytes, gqa.kvBytes * 4); // 32 / 8
});

// DeepSeek-V2: 60 layers, 128 heads, kv_lora_rank 512, qk_rope_head_dim 64.
const DEEPSEEK_V2_MLA: VramInputs = {
  totalParams: 236e9,
  weightBytes: DTYPE_BYTES.fp8,
  layers: 60,
  hidden: 5120,
  heads: 128,
  headDim: 192, // qk_nope 128 + qk_rope 64
  kvLoraRank: 512,
  qkRopeHeadDim: 64,
  isMla: true,
};

test('MLA compresses the KV cache dramatically vs the equivalent MHA', () => {
  const mla = estimateVram(DEEPSEEK_V2_MLA, { batch: 1, context: 8192, kvBytes: 2 });
  assert.ok(mla.kvComputable);
  // MLA: 60 × 8192 × 1 × (512 + 64) × 2 bytes.
  assert.equal(mla.kvBytes, 60 * 8192 * 1 * (512 + 64) * 2);
  // The naive full-MHA cache for the same shapes would be > 20× larger.
  const mha = estimateVram({ ...DEEPSEEK_V2_MLA, isMla: false }, { batch: 1, context: 8192, kvBytes: 2 });
  assert.ok(mha.kvBytes > mla.kvBytes * 20);
});

test('MLA without a known rank falls back to non-computable KV', () => {
  const e = estimateVram({ ...DEEPSEEK_V2_MLA, kvLoraRank: undefined }, { batch: 1, context: 8192, kvBytes: 2 });
  // isMla but no rank and heads/hidden present → GQA branch is NOT taken (isMla short-circuits),
  // so KV stays 0 / non-computable rather than silently wrong.
  assert.equal(e.kvBytes, 0);
  assert.equal(e.kvComputable, false);
});

test('missing arch dims → weights only, KV not computable', () => {
  const e = estimateVram({ totalParams: 7e9, weightBytes: 2, isMla: false }, { batch: 1, context: 4096, kvBytes: 2 });
  assert.equal(e.kvBytes, 0);
  assert.equal(e.activationBytes, 0);
  assert.equal(e.kvComputable, false);
  assert.equal(e.weightsBytes, 14e9);
});

test('deriveVramInputs prefers the card, fills MLA ranks from config', () => {
  const inp = deriveVramInputs({
    totalParams: 236e9,
    weightBytes: 1,
    template: 'mla-moe',
    card: { family: 'DeepSeek-V2', template: 'mla-moe', layers: 60, hidden: 5120, heads: 128, chips: [], provenance: 'config' },
    config: { kv_lora_rank: 512, qk_rope_head_dim: 64, num_key_value_heads: 128 },
  });
  assert.equal(inp.layers, 60);
  assert.equal(inp.kvLoraRank, 512);
  assert.equal(inp.qkRopeHeadDim, 64);
  assert.equal(inp.isMla, true);
});

test('deriveVramInputs: DeepSeek-V4 MLA (no kv_lora_rank) falls back to kvHeads×headDim', () => {
  // V4 dropped kv_lora_rank; it expresses the compressed KV as num_key_value_heads:1 + head_dim:512.
  const inp = deriveVramInputs({
    totalParams: 685e9,
    weightBytes: 1,
    template: 'mla-moe',
    card: { family: 'DeepSeek-V4', template: 'mla-moe', layers: 61, hidden: 7168, heads: 128, kvHeads: 1, chips: [], provenance: 'config' },
    config: { num_key_value_heads: 1, head_dim: 512, qk_rope_head_dim: 64, q_lora_rank: 1536 },
  });
  assert.equal(inp.isMla, true);
  assert.equal(inp.kvLoraRank, 512); // 1 × 512 derived, since kv_lora_rank is absent
  assert.equal(inp.qkRopeHeadDim, 64);
  // And the estimate is now computable (latent 512 + rope 64 = 576 / token / layer).
  const e = estimateVram(inp, { batch: 1, context: 8192, kvBytes: 2 });
  assert.equal(e.kvComputable, true);
  assert.equal(e.kvBytes, 61 * 8192 * 1 * (512 + 64) * 2);
});

test('deriveVramInputs: fallback does NOT fire for dense-GQA (kvHeads×headDim is the real cache)', () => {
  const inp = deriveVramInputs({
    totalParams: 8e9,
    weightBytes: 2,
    template: 'dense-gqa',
    card: null,
    config: { num_hidden_layers: 32, hidden_size: 4096, num_attention_heads: 32, num_key_value_heads: 8, head_dim: 128 },
  });
  assert.equal(inp.isMla, false);
  assert.equal(inp.kvLoraRank, undefined); // no MLA latent invented for a GQA model
});

test('deriveVramInputs reads gguf metadata with the arch prefix', () => {
  const inp = deriveVramInputs({
    totalParams: 8e9,
    weightBytes: 2,
    template: 'dense-gqa',
    card: null,
    metadata: {
      'general.architecture': 'llama',
      'llama.block_count': 32,
      'llama.embedding_length': 4096,
      'llama.attention.head_count': 32,
      'llama.attention.head_count_kv': 8,
    },
  });
  assert.equal(inp.layers, 32);
  assert.equal(inp.hidden, 4096);
  assert.equal(inp.heads, 32);
  assert.equal(inp.kvHeads, 8);
});

test('defaultServingDtype maps the dominant checkpoint precision', () => {
  assert.equal(defaultServingDtype({ BF16: 8e9 }), 'bf16');
  assert.equal(defaultServingDtype({ F16: 8e9, F32: 1e6 }), 'fp16');
  assert.equal(defaultServingDtype({ float32: 8e9 }), 'fp32');
  assert.equal(defaultServingDtype({ Q4_K: 8e9 }), 'int4');
  assert.equal(defaultServingDtype({ weird: 1 }), 'bf16');
});

// ── training mode + new precisions ────────────────────────────────────────────

test('new precisions: fp4 = 0.5 B, int8 = 1 B (distinct labels, shared bytes)', () => {
  assert.equal(DTYPE_BYTES.fp4, 0.5);
  assert.equal(DTYPE_BYTES.int8, 1);
  assert.equal(DTYPE_BYTES.int4, 0.5);
  assert.equal(DTYPE_BYTES.fp8, 1);
});

test('inference estimate is tagged and carries zero training terms', () => {
  const e = estimateVram(LLAMA3_8B, { batch: 1, context: 8192, kvBytes: 2 });
  assert.equal(e.mode, 'inference');
  assert.equal(e.gradientBytes, 0);
  assert.equal(e.optimizerBytes, 0);
});

test('training: weights + gradients + optimizer, no KV cache, mixed-precision 16 B/param', () => {
  const e = estimateVram(LLAMA3_8B, { batch: 1, context: 8192, kvBytes: 2, mode: 'training', optimizer: 'adamw', gradCheckpoint: true });
  assert.equal(e.mode, 'training');
  assert.equal(e.kvBytes, 0);
  assert.equal(e.weightsBytes, 8.03e9 * 2);
  assert.equal(e.gradientBytes, 8.03e9 * 2);
  assert.equal(e.optimizerBytes, 8.03e9 * OPTIMIZER_STATE_BYTES.adamw);
  // weights(2) + grads(2) + AdamW optimizer(12) = the classic 16 B/param.
  assert.equal(e.weightsBytes + e.gradientBytes + e.optimizerBytes, 8.03e9 * 16);
});

test('training optimizer choice moves only the optimizer term', () => {
  const rt = { batch: 1, context: 8192, kvBytes: 2, mode: 'training' as const, gradCheckpoint: true };
  const adamw = estimateVram(LLAMA3_8B, { ...rt, optimizer: 'adamw' });
  const adam8 = estimateVram(LLAMA3_8B, { ...rt, optimizer: 'adam8bit' });
  const sgd = estimateVram(LLAMA3_8B, { ...rt, optimizer: 'sgd' });
  assert.equal(adamw.optimizerBytes, 8.03e9 * 12);
  assert.equal(adam8.optimizerBytes, 8.03e9 * 6);
  assert.equal(sgd.optimizerBytes, 8.03e9 * 8);
  assert.equal(adamw.weightsBytes, sgd.weightsBytes);
  assert.equal(adamw.gradientBytes, sgd.gradientBytes);
});

test('gradient checkpointing cuts the activation stash; param terms unchanged', () => {
  const base = { batch: 2, context: 8192, kvBytes: 2, mode: 'training' as const, optimizer: 'adamw' as const };
  const on = estimateVram(LLAMA3_8B, { ...base, gradCheckpoint: true });
  const off = estimateVram(LLAMA3_8B, { ...base, gradCheckpoint: false });
  assert.ok(off.activationBytes > on.activationBytes);
  // Checkpointed stash = the per-layer input: 2 × layers × ctx × batch × hidden.
  assert.equal(on.activationBytes, 2 * 32 * 8192 * 2 * 4096);
  assert.equal(on.weightsBytes, off.weightsBytes);
  assert.equal(on.optimizerBytes, off.optimizerBytes);
});

test('training activations grow super-linearly with context (∝ s² attention term)', () => {
  const base = { batch: 1, kvBytes: 2, mode: 'training' as const, optimizer: 'adamw' as const, gradCheckpoint: false };
  const a = estimateVram(LLAMA3_8B, { ...base, context: 8192 }).activationBytes;
  const b = estimateVram(LLAMA3_8B, { ...base, context: 16384 }).activationBytes;
  assert.ok(b > 2 * a);
});

test('training with no dims: param terms trustworthy, activations flagged unknown', () => {
  const bare: VramInputs = { totalParams: 7e9, weightBytes: 2, isMla: false };
  const e = estimateVram(bare, { batch: 1, context: 8192, kvBytes: 2, mode: 'training', optimizer: 'adamw' });
  assert.equal(e.weightsBytes, 7e9 * 2);
  assert.equal(e.optimizerBytes, 7e9 * 12);
  assert.equal(e.activationBytes, 0);
  assert.equal(e.kvComputable, false);
});

// ── KV-cache-per-token metric + class (archgraph plan §5 W1 / G6) ─────────────

test('kvCacheBytesPerToken: GQA and MLA per-token figures + classes', () => {
  // Llama-3-8B GQA: 2 × 32 × 8 × 128 × 2 = 128 KB/token → moderate.
  const gqa = kvCacheBytesPerToken(LLAMA3_8B)!;
  assert.equal(gqa, 2 * 32 * 8 * 128 * 2);
  assert.equal(gqa, 128 * 1024);
  assert.equal(classifyKvCache(gqa), 'moderate');
  // DeepSeek-V2 MLA: 60 × (512 + 64) × 2 ≈ 68 KB/token → low (compressed KV).
  const mla = kvCacheBytesPerToken(DEEPSEEK_V2_MLA)!;
  assert.equal(mla, 60 * (512 + 64) * 2);
  assert.equal(classifyKvCache(mla), 'low');
  assert.ok(mla < gqa);
});

test('kvCacheBytesPerToken: MLA without a rank is honest null (not a wrong number)', () => {
  assert.equal(kvCacheBytesPerToken({ ...DEEPSEEK_V2_MLA, kvLoraRank: undefined }), null);
});

test('classifyKvCache: bucket boundaries', () => {
  assert.equal(classifyKvCache(20 * 1024), 'low');
  assert.equal(classifyKvCache(96 * 1024 - 1), 'low');
  assert.equal(classifyKvCache(96 * 1024), 'moderate');
  assert.equal(classifyKvCache(384 * 1024 - 1), 'moderate');
  assert.equal(classifyKvCache(384 * 1024), 'high');
  assert.equal(classifyKvCache(1024 * 1024), 'very-high');
  assert.equal(classifyKvCache(6 * 1024 * 1024), 'very-high'); // big dense MHA
});

test('hybrid KV is sized by full-attention layers, not total depth', () => {
  // A Kimi-K3-shaped stack: 93 layers, only 24 full-attention (MLA) layers cache;
  // the 69 KDA layers hold an O(1) recurrent state (no growing KV).
  const hybrid: VramInputs = { totalParams: 0, weightBytes: 0, layers: 93, fullAttnLayers: 24, kvLoraRank: 512, qkRopeHeadDim: 64, isMla: true };
  const perTok = kvCacheBytesPerToken(hybrid)!;
  assert.equal(perTok, 24 * (512 + 64) * 2); // 24 caching layers, NOT 93
  assert.equal(classifyKvCache(perTok), 'low');
  // Sizing by total depth (the pre-W1b behaviour) would be ~4× larger.
  const naive = kvCacheBytesPerToken({ ...hybrid, fullAttnLayers: undefined })!;
  assert.equal(naive, 93 * (512 + 64) * 2);
  assert.ok(perTok < naive);
});

test('deriveKvCacheClass end-to-end: Kimi-K3 config → low KV via card.fullAttnLayers', () => {
  // Whole pipe: parse → classify (linear-hybrid, fullAttnLayers=24) → KV class.
  const K3 = {
    model_type: 'kimi_k3',
    architectures: ['KimiK3ForConditionalGeneration'],
    text_config: {
      model_type: 'kimi_linear',
      hidden_size: 7168, num_hidden_layers: 93, num_attention_heads: 96, num_key_value_heads: 96,
      kv_lora_rank: 512, qk_rope_head_dim: 64, mla_use_nope: true,
      num_experts: 896, num_experts_per_token: 16, moe_intermediate_size: 3072, vocab_size: 163840,
      linear_attn_config: {
        full_attn_layers: [4, 8, 12, 16, 20, 24, 28, 32, 36, 40, 44, 48, 52, 56, 60, 64, 68, 72, 76, 80, 84, 88, 92, 93],
        kda_layers: [1, 2, 3, 5], num_heads: 96,
      },
    },
  };
  const config = parseHfConfig(JSON.stringify(K3))!;
  const card = classifyArch({ config, tensorNames: [] })!;
  assert.equal(card.fullAttnLayers, 24);
  const kv = deriveKvCacheClass({ card, config })!;
  assert.equal(kv.bytesPerToken, 24 * (512 + 64) * 2); // 24 full-attn layers only
  assert.equal(kv.cls, 'low');
});
