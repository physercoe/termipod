/// Standalone SVG export checks (archgraph plan W5 / D-6). The exported file has
/// to render correctly *outside* the app, so these tests pin the properties that
/// makes that true: a well-formed self-contained document, no CSS variables or
/// external references, real colours per theme, and XML-escaped text (labels come
/// from model configs — untrusted).
/// Run with `node --test src/state/archSvg.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { archToSvg, archSvgSize, esc } from './archSvg.ts';
import { layoutArch, type ArchLabels } from './archLayout.ts';
import { buildArchSchematic } from './archSchematic.ts';
import { buildArchPanels } from './archPanels.ts';
import { classifyArch, parseHfConfig } from './checkpoint.ts';

const L: ArchLabels = { attn: (k) => k, norm: 'RMSNorm', ffnDense: 'MLP', ffnMoe: 'MoE', linearSub: 'O(1) state', container: '×N layers' };

const k3Full = [...Array.from({ length: 23 }, (_, i) => (i + 1) * 4), 93];
const KIMI_K3 = {
  model_type: 'kimi_linear', hidden_size: 7168, num_hidden_layers: 93, num_attention_heads: 96,
  num_key_value_heads: 96, kv_lora_rank: 512, q_lora_rank: 1536, qk_nope_head_dim: 128, qk_rope_head_dim: 64,
  v_head_dim: 128, mla_use_nope: true, num_experts: 896, num_experts_per_token: 16, moe_intermediate_size: 3072,
  num_shared_experts: 2, first_k_dense_replace: 1, vocab_size: 163840, rms_norm_eps: 1e-5,
  linear_attn_config: { full_attn_layers: k3Full, num_heads: 96, short_conv_kernel_size: 4, head_dim: 128 },
};
const LLAMA = {
  model_type: 'llama', hidden_size: 4096, num_hidden_layers: 32, num_attention_heads: 32,
  num_key_value_heads: 8, head_dim: 128, intermediate_size: 11008, vocab_size: 32000, rms_norm_eps: 1e-5,
};

function laidFor(cfg: Record<string, unknown>, withPanels = true) {
  const config = parseHfConfig(JSON.stringify(cfg))!;
  const card = classifyArch({ config, tensorNames: [] })!;
  const s = buildArchSchematic(card, config)!;
  const panels = withPanels ? buildArchPanels(card, config, card.chips.includes('MLA') ? 'MLA' : 'GQA') : [];
  return { laid: layoutArch(s, L, panels), card, s };
}

test('W5 export: a well-formed, self-contained SVG document', () => {
  const { laid } = laidFor(KIMI_K3);
  const svg = archToSvg(laid, { theme: 'dark', title: 'Kimi K3' });
  assert.ok(svg.startsWith('<svg xmlns="http://www.w3.org/2000/svg"'));
  assert.ok(svg.trimEnd().endsWith('</svg>'));
  // Self-contained: no CSS variables, no external stylesheet/image/font refs.
  assert.ok(!svg.includes('var(--'), 'exported SVG must not reference CSS variables');
  assert.ok(!svg.includes('<link'), 'no external stylesheet');
  assert.ok(!/https?:\/\/(?!www\.w3\.org)/.test(svg), 'no external resource URLs');
  assert.ok(!svg.includes('<image'), 'no raster references');
  // Balanced tags for the elements we emit.
  const open = (svg.match(/<svg[\s>]/g) ?? []).length;
  const close = (svg.match(/<\/svg>/g) ?? []).length;
  assert.equal(open, close);
  // Title is drawn.
  assert.ok(svg.includes('Kimi K3'));
});

test('W5 export: both themes inline real colours and differ', () => {
  const { laid } = laidFor(LLAMA);
  const dark = archToSvg(laid, { theme: 'dark' });
  const light = archToSvg(laid, { theme: 'light' });
  assert.ok(dark.includes('#171717')); // dark page
  assert.ok(light.includes('#ffffff')); // light page
  assert.notEqual(dark, light);
  // Same geometry either way — only the palette changes.
  assert.deepEqual(archSvgSize(laid, { theme: 'dark' }), archSvgSize(laid, { theme: 'light' }));
});

test('W5 export: every card label reaches the document', () => {
  const { laid } = laidFor(LLAMA);
  const svg = archToSvg(laid);
  for (const c of laid.cards) {
    // Labels are clipped for width, so match a safe prefix.
    assert.ok(svg.includes(esc(c.node.label.slice(0, 10))), `missing card label ${c.node.label}`);
  }
});

test('W5 export: hybrid figure carries the strip cells, nested boxes and panels', () => {
  const { laid } = laidFor(KIMI_K3);
  const svg = archToSvg(laid, { annotations: ['3 KDA : 1 MLA per block'], legend: [{ label: 'KDA', attn: 'KDA' }] });
  // A rect per strip cell (93) plus its FFN edge marker — plenty of rects.
  assert.ok(laid.strip !== null && laid.strip.cells.length === 93);
  assert.ok((svg.match(/<rect /g) ?? []).length > 93);
  // Nested repeat boxes are differentiated: solid cycle, dashed inner runs.
  assert.ok(svg.includes('stroke-dasharray="6 4"'), 'dashed inner repeat containers');
  assert.match(svg, /fill-opacity="0\.38" stroke="#[0-9a-f]+" stroke-width="1\.2"\/>/, 'solid outer cycle container');
  // The annotation and legend text made it under the figure.
  assert.ok(svg.includes('3 KDA : 1 MLA per block'));
  assert.ok(svg.includes('>KDA</text>'));
  // The restrained teal novelty accent is used for the linear operator.
  assert.ok(svg.includes('#68bac3'));
});

test('W5 export: panel keys are resolved through labelFor, not printed raw', () => {
  const { laid } = laidFor(KIMI_K3);
  const raw = archToSvg(laid);
  const resolved = archToSvg(laid, { labelFor: (k) => `L:${k}` });
  assert.ok(raw.includes('qAProj'), 'identity default prints the key');
  assert.ok(resolved.includes('L:qAProj'), 'a resolver is applied to panel item keys');
  assert.ok(resolved.includes('L:attnInternals'), 'and to the panel title');
});

test('W5 export: untrusted label text is XML-escaped (config-sourced strings)', () => {
  const { laid } = laidFor(LLAMA);
  // A config could name anything; the exporter must never emit raw markup.
  laid.cards[0].node = { ...laid.cards[0].node, label: '<script>alert("x")</script>&' };
  const svg = archToSvg(laid);
  assert.ok(!svg.includes('<script>'), 'markup in a label must not survive into the document');
  assert.ok(svg.includes('&lt;script&gt;'));
  assert.equal(esc(`<a href="x">&'`), '&lt;a href=&quot;x&quot;&gt;&amp;&apos;');
});

test('W5 export: size grows with the figure and leaves margins', () => {
  const small = archSvgSize(laidFor(LLAMA, false).laid);
  const big = archSvgSize(laidFor(KIMI_K3).laid);
  assert.ok(big.height > small.height, 'a 93-layer hybrid exports taller than a plain stack');
  assert.ok(big.width > small.width, 'panels widen the document');
  assert.ok(small.width > 0 && small.height > 0);
});
