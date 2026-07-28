/// Tests for the config-only architecture schematic builder (§5a follow-on).
/// The builder is pure over a classifier `ArchCard` + the raw config, so the
/// node set / labels / block membership / residuals are pinned here without
/// React Flow. Run with `node --test` (Node strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { archNodeDetails, buildArchSchematic, type ArchSchematic } from './archSchematic.ts';
import { classifyArch } from './checkpoint.ts';

// Build the way the tab does: classify then synthesize.
function fromConfig(config: Record<string, unknown>): ArchSchematic | null {
  const card = classifyArch({ config, tensorNames: [] });
  assert.ok(card !== null, 'expected a classifiable config');
  return buildArchSchematic(card, config);
}

const ids = (s: ArchSchematic): string[] => s.nodes.map((n) => n.id);
const byId = (s: ArchSchematic, id: string): ArchSchematic['nodes'][number] => {
  const n = s.nodes.find((x) => x.id === id);
  assert.ok(n !== undefined, `node ${id} present`);
  return n;
};
const subOf = (s: ArchSchematic, id: string): string => {
  const v = byId(s, id).sub;
  assert.ok(v !== undefined, `node ${id} has a sub-label`);
  return v;
};

test('null when the config is not stackable (no layers)', () => {
  const card = classifyArch({ config: { model_type: 'llama', hidden_size: 4096, num_attention_heads: 32, vocab_size: 32000 }, tensorNames: [] });
  // A card may classify, but with 0 layers there is no stack to draw.
  const s = card !== null ? buildArchSchematic(card, { hidden_size: 4096 }) : null;
  assert.equal(s, null);
});

test('dense GQA decoder — canonical stack, layers, residuals', () => {
  const s = fromConfig({
    model_type: 'llama',
    num_hidden_layers: 32,
    hidden_size: 4096,
    intermediate_size: 11008,
    num_attention_heads: 32,
    num_key_value_heads: 8,
    vocab_size: 32000,
    rms_norm_eps: 1e-5,
  });
  assert.ok(s !== null);
  assert.equal(s.layers, 32);
  assert.deepEqual(ids(s), ['embed', 'norm1', 'attn', 'norm2', 'ffn', 'finalnorm', 'head']);
  // Attention is GQA with the head split; FFN is a dense MLP (not MoE).
  assert.match(subOf(s, 'attn'), /GQA · 32 Q \/ 8 KV heads/);
  assert.equal(byId(s, 'ffn').kind, 'ffn');
  assert.match(subOf(s, 'ffn'), /11\.0K/);
  // RMSNorm (rms_norm_eps present) reflected in both norms + the final norm.
  assert.equal(byId(s, 'norm1').label, 'RMSNorm');
  assert.equal(byId(s, 'finalnorm').label, 'Final RMSNorm');
  // Exactly the in-block rows are wrapped; the two residual streams are present.
  assert.deepEqual(
    s.nodes.filter((n) => n.inBlock).map((n) => n.id),
    ['norm1', 'attn', 'norm2', 'ffn'],
  );
  assert.deepEqual(s.residuals, [
    { from: 'norm1', to: 'norm2' },
    { from: 'norm2', to: 'finalnorm' },
  ]);
});

test('MoE + MLA — expert counts and latent-attention labels', () => {
  const s = fromConfig({
    model_type: 'deepseek_v3',
    num_hidden_layers: 61,
    hidden_size: 7168,
    num_attention_heads: 128,
    kv_lora_rank: 512,
    q_lora_rank: 1536,
    n_routed_experts: 256,
    num_experts_per_tok: 8,
    n_shared_experts: 1,
    moe_intermediate_size: 2048,
    vocab_size: 129280,
    rms_norm_eps: 1e-6,
  });
  assert.ok(s !== null);
  assert.equal(byId(s, 'ffn').kind, 'moe');
  assert.match(subOf(s, 'ffn'), /256 experts · top-8 · \+1 shared/);
  assert.match(byId(s, 'attn').label, /Latent/);
  assert.match(subOf(s, 'attn'), /MLA · 128 heads/);
});

test('linear-hybrid card renders the honest uniform fallback (MLA attn + MoE FFN)', () => {
  // Until W2's heterogeneous spec lands, a `linear-hybrid` model (Kimi K3-like:
  // MLA full-attn layers + MoE) must NOT drop to a dense MLP — it renders the
  // current uniform MLA/MoE stack (D-4). Derived from chips/experts, not template.
  const s = fromConfig({
    model_type: 'kimi_linear',
    num_hidden_layers: 93, hidden_size: 7168, num_attention_heads: 96,
    kv_lora_rank: 512, q_lora_rank: 1536, mla_use_nope: true,
    num_experts: 896, num_experts_per_token: 16, moe_intermediate_size: 3072, num_shared_experts: 2,
    linear_attn_config: { full_attn_layers: [4, 8, 12], kda_layers: [1, 2, 3] },
    vocab_size: 163840, rms_norm_eps: 1e-5,
  });
  assert.ok(s !== null);
  assert.equal(byId(s, 'ffn').kind, 'moe'); // MoE block kept (not dense MLP)
  assert.match(byId(s, 'attn').label, /Latent/); // MLA attention kept
  assert.match(subOf(s, 'ffn'), /896 experts · top-16 · \+2 shared/);
});

test('MHA (kv == q heads) labelled multi-head, LayerNorm variant', () => {
  const s = fromConfig({
    model_type: 'gpt2',
    n_layer: 12,
    n_embd: 768,
    n_head: 12,
    n_inner: 3072,
    vocab_size: 50257,
    layer_norm_epsilon: 1e-5,
  });
  assert.ok(s !== null);
  assert.equal(byId(s, 'norm1').label, 'LayerNorm');
  // With no GQA split, attention reads as MHA.
  assert.match(subOf(s, 'attn'), /MHA|heads/);
});

// ── per-node detail rows (the schematic's click-to-inspect panel) ──
const detailMap = (rows: { label: string; value: string }[]): Record<string, string> => Object.fromEntries(rows.map((r) => [r.label, r.value]));

test('archNodeDetails: MLA+MoE attention & FFN expose the full config facts', () => {
  const config = {
    model_type: 'deepseek_v3', num_hidden_layers: 61, hidden_size: 7168, num_attention_heads: 128,
    kv_lora_rank: 512, q_lora_rank: 1536, qk_rope_head_dim: 64, v_head_dim: 128,
    n_routed_experts: 256, num_experts_per_tok: 8, n_shared_experts: 1, moe_intermediate_size: 2048,
    first_k_dense_replace: 3, vocab_size: 129280, hidden_act: 'silu',
  };
  const card = classifyArch({ config, tensorNames: [] })!;
  const s = buildArchSchematic(card, config)!;
  const attn = detailMap(archNodeDetails(s.nodes.find((n) => n.id === 'attn')!, config, card));
  assert.equal(attn['Type'], 'Multi-head Latent (MLA)');
  assert.equal(attn['kv_lora_rank'], '512');
  assert.equal(attn['qk_rope_head_dim'], '64');
  const ffn = detailMap(archNodeDetails(s.nodes.find((n) => n.id === 'ffn')!, config, card));
  assert.equal(ffn['Type'], 'Mixture-of-Experts');
  assert.equal(ffn['Routed experts'], '256');
  assert.equal(ffn['Active per token'], '8');
  assert.equal(ffn['Shared experts'], '1');
  assert.equal(ffn['Dense first-K layers'], '3');
});

test('archNodeDetails: dense GQA — no MLA rows; embed shows vocab/hidden', () => {
  const config = { model_type: 'llama', num_hidden_layers: 32, hidden_size: 4096, num_attention_heads: 32, num_key_value_heads: 8, head_dim: 128, intermediate_size: 11008, vocab_size: 32000 };
  const card = classifyArch({ config, tensorNames: [] })!;
  const s = buildArchSchematic(card, config)!;
  const attn = detailMap(archNodeDetails(s.nodes.find((n) => n.id === 'attn')!, config, card));
  assert.equal(attn['Type'], 'Grouped-query (GQA)');
  assert.equal(attn['KV heads'], '8');
  assert.equal(attn['Head dim'], '128');
  assert.equal(attn['kv_lora_rank'], undefined); // GQA has no MLA latent
  const embed = detailMap(archNodeDetails(s.nodes.find((n) => n.id === 'embed')!, config, card));
  assert.equal(embed['Vocab size'], '32.0K');
});
