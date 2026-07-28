/// Pure arch-card diff checks (plan §5 W5). Run with `node --test
/// src/state/archDiff.test.ts` from `desktop/` (not run by CI).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { diffArchCards, type ArchDiffSide } from './archDiff.ts';
import { classifyArch, parseHfConfig } from './checkpoint.ts';

function side(cfg: Record<string, unknown>): ArchDiffSide {
  const config = parseHfConfig(JSON.stringify(cfg))!;
  const card = classifyArch({ config, tensorNames: [] })!;
  return { card, config };
}

const k3Full = [...Array.from({ length: 23 }, (_, i) => (i + 1) * 4), 93]; // 4,8,…,92,93
const K3 = side({
  model_type: 'kimi_linear', architectures: ['KimiLinearForCausalLM'],
  hidden_size: 7168, num_hidden_layers: 93, num_attention_heads: 96, num_key_value_heads: 96,
  kv_lora_rank: 512, q_lora_rank: 1536, qk_rope_head_dim: 64, mla_use_nope: true, mla_use_output_gate: true,
  hidden_act: 'situ', rms_norm_eps: 1e-5, num_experts: 896, num_experts_per_token: 16, moe_intermediate_size: 3072,
  num_shared_experts: 2, first_k_dense_replace: 1, max_position_embeddings: 1048576, vocab_size: 163840,
  linear_attn_config: { full_attn_layers: k3Full, num_heads: 96 },
});
const K2 = side({
  model_type: 'kimi_k2', architectures: ['DeepseekV3ForCausalLM'],
  hidden_size: 7168, num_hidden_layers: 61, num_attention_heads: 64, num_key_value_heads: 64,
  kv_lora_rank: 512, q_lora_rank: 1536, qk_rope_head_dim: 64, hidden_act: 'silu', rms_norm_eps: 1e-6,
  rope_scaling: { type: 'yarn', factor: 32 }, n_routed_experts: 384, n_shared_experts: 1, num_experts_per_tok: 8,
  moe_intermediate_size: 2048, first_k_dense_replace: 1, max_position_embeddings: 131072, vocab_size: 163840,
});

const row = (d: ReturnType<typeof diffArchCards>, key: string) => {
  const r = d.rows.find((x) => x.key === key);
  assert.ok(r, `expected row ${key} in [${d.rows.map((x) => x.key).join(', ')}]`);
  return r;
};

test('diffArchCards: K3 vs K2 highlights template, experts, context, linear attention', () => {
  const d = diffArchCards(K3, K2);
  assert.ok(d.anyChange);
  // Template: hybrid linear-attention vs MLA + MoE.
  const tmpl = row(d, 'template');
  assert.ok(tmpl.changed);
  assert.equal(tmpl.a, 'Hybrid linear-attention');
  assert.equal(tmpl.b, 'MLA + MoE');
  // Expert count and active-experts differ.
  assert.ok(row(d, 'experts').changed);
  assert.deepEqual([row(d, 'experts').a, row(d, 'experts').b], ['896', '384']);
  assert.ok(row(d, 'expertsPerTok').changed);
  // K3 has a linear operator; K2 has none → the row shows the asymmetry.
  const lin = row(d, 'linearKind');
  assert.equal(lin.a, 'Kimi Delta Attention');
  assert.equal(lin.b, '');
  assert.ok(lin.changed);
  // Context window differs (1M vs 131K).
  assert.ok(row(d, 'context').changed);
});

test('diffArchCards: chip sets diff into added/removed (order-independent)', () => {
  const d = diffArchCards(K3, K2);
  // Going K3 → K2: KDA/NoPE/gated-attn drop out; YaRN appears.
  assert.ok(d.chipsRemoved.includes('Kimi Delta Attention'));
  assert.ok(d.chipsRemoved.includes('NoPE'));
  assert.ok(d.chipsAdded.includes('YaRN'));
  // Shared chips (MLA, MoE) are in neither list.
  assert.ok(!d.chipsAdded.includes('MLA') && !d.chipsRemoved.includes('MLA'));
});

test('diffArchCards: full-attention-layer row shows the hybrid split vs uniform', () => {
  const d = diffArchCards(K3, K2);
  const fa = row(d, 'fullAttnLayers');
  assert.equal(fa.a, '24'); // K3: 24 full-attn (MLA) layers
  assert.equal(fa.b, ''); // K2: uniform, no split
  assert.ok(fa.changed);
});

test('diffArchCards: a card against itself has no changes', () => {
  const d = diffArchCards(K2, K2);
  assert.equal(d.anyChange, false);
  assert.equal(d.chipsAdded.length, 0);
  assert.equal(d.chipsRemoved.length, 0);
  assert.ok(d.rows.length > 0);
  assert.ok(d.rows.every((r) => !r.changed));
});

test('diffArchCards: KV-cache class row present; both Kimi models read low', () => {
  const d = diffArchCards(K3, K2);
  const kv = row(d, 'kvClass');
  assert.equal(kv.a, 'low'); // MLA hybrid, 24 caching layers
  assert.equal(kv.b, 'low'); // MLA, compressed latent
  assert.ok(!kv.changed);
});
