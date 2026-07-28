/// Zoom-in panel spec checks (archgraph plan W4). Every item must be
/// config-derived — the point of these tests is that a panel never asserts a
/// projection or a narrative the config didn't disclose (D-2/D-4).
/// Run with `node --test src/state/archPanels.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildArchPanels, buildAttentionPanel, buildMoePanel, type ArchPanel } from './archPanels.ts';
import { classifyArch, parseHfConfig, type ArchCard } from './checkpoint.ts';

function cardFor(cfg: Record<string, unknown>): { card: ArchCard; config: Record<string, unknown> } {
  const config = parseHfConfig(JSON.stringify(cfg))!;
  return { card: classifyArch({ config, tensorNames: [] })!, config };
}
const keys = (p: ArchPanel): string[] => p.items.map((i) => i.key);
const item = (p: ArchPanel, id: string) => {
  const i = p.items.find((x) => x.id === id);
  assert.ok(i, `expected item ${id} in [${p.items.map((x) => x.id).join(', ')}]`);
  return i;
};

const KIMI_K3 = {
  model_type: 'kimi_linear', hidden_size: 7168, num_hidden_layers: 93, num_attention_heads: 96,
  num_key_value_heads: 96, kv_lora_rank: 512, q_lora_rank: 1536, qk_nope_head_dim: 128, qk_rope_head_dim: 64,
  v_head_dim: 128, mla_use_nope: true, attn_res_block_size: 12, num_experts: 896, num_experts_per_token: 16,
  moe_intermediate_size: 3072, num_shared_experts: 2, vocab_size: 163840, rms_norm_eps: 1e-5,
  linear_attn_config: { full_attn_layers: [4, 8, 12], kda_layers: [1, 2, 3], num_heads: 96, head_dim: 128, short_conv_kernel_size: 4 },
};
const LLAMA = {
  model_type: 'llama', hidden_size: 4096, num_hidden_layers: 32, num_attention_heads: 32,
  num_key_value_heads: 8, head_dim: 128, intermediate_size: 11008, vocab_size: 32000,
};
const GEMMA3 = {
  model_type: 'gemma3', architectures: ['Gemma3ForConditionalGeneration'],
  text_config: { model_type: 'gemma3_text', hidden_size: 5376, num_hidden_layers: 62, num_attention_heads: 32, num_key_value_heads: 16, head_dim: 128, sliding_window: 1024, sliding_window_pattern: 6, intermediate_size: 21504, vocab_size: 262208, rms_norm_eps: 1e-6 },
};

test('W4 attention panel: MLA expands the low-rank chain with real dims', () => {
  const { card, config } = cardFor(KIMI_K3);
  const p = buildAttentionPanel(card, config, 'MLA')!;
  assert.equal(p.kind, 'attention');
  assert.deepEqual(keys(p), ['qAProj', 'qBProj', 'kvAProj', 'kvBProj', 'softmax', 'oProj']);
  // q_lora 1536; q head dim = nope 128 + rope 64 = 192 → 96 × 192 = 18432.
  assert.equal(item(p, 'qa').value, '7168 → 1536');
  assert.equal(item(p, 'qb').value, '1536 → 18432');
  // kv_a carries the latent + the decoupled rope key: 512 + 64 = 576.
  assert.equal(item(p, 'kva').value, '7168 → 576');
  // kv_b expands to heads × (nope + v) = 96 × 256 = 24576.
  assert.equal(item(p, 'kvb').value, '512 → 24576');
  assert.equal(item(p, 'o').value, '12288 → 7168'); // 96 × 128 → hidden
  assert.match(item(p, 'softmax').value ?? '', /RoPE 64 \+ NoPE 128/);
});

test('W4 attention panel: the linear operator expands conv/gate/delta, not Q·Kᵀ', () => {
  const { card, config } = cardFor(KIMI_K3);
  const p = buildAttentionPanel(card, config, 'KDA')!;
  assert.deepEqual(keys(p), ['qkvProj', 'shortConv', 'gate', 'deltaRuleKda', 'oProj']);
  assert.equal(item(p, 'conv').value, 'k=4'); // short_conv_kernel_size
  assert.equal(item(p, 'delta').value, '96 heads');
  // No softmax / KV projection is claimed for a linear operator.
  assert.ok(!keys(p).includes('softmax') && !keys(p).includes('kvAProj'));
});

test('W4 attention panel: plain GQA expands Q/K/V at the GQA-shrunk widths', () => {
  const { card, config } = cardFor(LLAMA);
  const p = buildAttentionPanel(card, config, 'GQA')!;
  assert.deepEqual(keys(p), ['qProj', 'kProj', 'vProj', 'softmax', 'oProj']);
  assert.equal(item(p, 'q').value, '4096 → 4096'); // 32 heads × 128
  assert.equal(item(p, 'k').value, '4096 → 1024'); // 8 KV heads × 128
  assert.equal(item(p, 'v').value, '4096 → 1024');
});

test('W4 attention panel: sliding-window names its window from the config', () => {
  const { card, config } = cardFor(GEMMA3);
  const p = buildAttentionPanel(card, config, 'sliding')!;
  assert.equal(item(p, 'softmax').key, 'softmaxWindowed');
  assert.equal(item(p, 'softmax').value, 'window 1024');
  // The global-attention layers of the SAME model get the plain softmax item.
  const g = buildAttentionPanel(card, config, 'global')!;
  assert.equal(item(g, 'softmax').key, 'softmax');
});

test('W4 MoE panel: router + collapsed expert chips + shared experts', () => {
  const { card, config } = cardFor(KIMI_K3);
  const p = buildMoePanel(card, config)!;
  assert.equal(p.kind, 'moe');
  assert.equal(item(p, 'router').value, 'top-16 of 896'); // reads Kimi's num_experts_per_token
  // A short chip row, not 896 boxes.
  const chips = p.items.filter((i) => i.shape === 'expert');
  assert.equal(chips.length, 4);
  assert.equal(chips[0].value, '7168 → 3072'); // hidden → moe_intermediate_size
  assert.equal(item(p, 'more').value, '+892');
  assert.equal(item(p, 'shared').value, '×2');
});

test('W4 MoE panel: a dense model has none (no invented router)', () => {
  const { card, config } = cardFor(LLAMA);
  assert.equal(buildMoePanel(card, config), null);
  assert.deepEqual(buildArchPanels(card, config, 'GQA').map((p) => p.kind), ['attention']);
});

test('W4 D-2 overlay: the narrative note is gated on a config key the model ships', () => {
  // Kimi K3 ships `attn_res_block_size` → the attention-residual note is offered.
  const k3 = cardFor(KIMI_K3);
  assert.equal(buildAttentionPanel(k3.card, k3.config, 'MLA')!.noteKey, 'noteAttnRes');
  // A model without that key gets NO note — never inferred from the family name.
  const llama = cardFor(LLAMA);
  assert.equal(buildAttentionPanel(llama.card, llama.config, 'GQA')!.noteKey, undefined);
  const k3NoKey = cardFor({ ...KIMI_K3, attn_res_block_size: undefined });
  assert.equal(buildAttentionPanel(k3NoKey.card, k3NoKey.config, 'MLA')!.noteKey, undefined);
});

test('W4 honest blanks: a bare config yields no invented dims', () => {
  const { card, config } = cardFor({ model_type: 'llama', num_hidden_layers: 8, hidden_size: 512, num_attention_heads: 8, vocab_size: 1000, num_experts: 16, num_experts_per_tok: 2 });
  const moe = buildMoePanel(card, config)!;
  // No moe_intermediate_size in the config → the expert chips carry no dims line.
  assert.equal(moe.items.filter((i) => i.shape === 'expert')[0].value, undefined);
  assert.equal(item(moe, 'router').value, 'top-2 of 16');
});
