/// Tests for the config-only architecture schematic builder (§5a follow-on).
/// The builder is pure over a classifier `ArchCard` + the raw config, so the
/// node set / labels / block membership / residuals are pinned here without
/// React Flow. Run with `node --test` (Node strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildArchSchematic, type ArchSchematic } from './archSchematic.ts';
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
