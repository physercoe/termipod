/// VRAM-estimator arithmetic checks against known models (plan §4b). The frontend
/// package has no CI test runner (renderer logic is tsc + E2E only), so this file
/// is run locally with `node --test src/state/vram.test.ts` from `desktop/`; it
/// documents the expected numbers and pins the GQA vs MLA branches.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { estimateVram, deriveVramInputs, defaultServingDtype, DTYPE_BYTES, type VramInputs } from './vram.ts';

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
