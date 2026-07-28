/// Geometry checks for the architecture schematic layout (archgraph plan W3).
/// The renderer is a thin mapping over `layoutArch`, so these assertions are what
/// stands in for looking at the figure: no overlapping rows, every container box
/// actually wrapping its own cards, every edge endpoint real, ids unique, and the
/// uniform stack laid out exactly as it was before W3.
/// Run with `node --test src/state/archLayout.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GEO, layoutArch, isLinearAttn, panelRows, type ArchLabels, type ArchLayoutResult } from './archLayout.ts';
import { buildArchSchematic } from './archSchematic.ts';
import { buildArchPanels } from './archPanels.ts';
import { classifyArch, parseHfConfig } from './checkpoint.ts';

// Stub labels: identity-ish, so assertions read clearly and no i18n is needed.
const L: ArchLabels = {
  attn: (k) => k,
  norm: 'RMSNorm',
  ffnDense: 'MLP',
  ffnMoe: 'MoE',
  linearSub: 'O(1) state',
  container: '×N layers',
};

function laidFor(cfg: Record<string, unknown>, withPanels = false): ArchLayoutResult {
  const config = parseHfConfig(JSON.stringify(cfg))!;
  const card = classifyArch({ config, tensorNames: [] })!;
  const s = buildArchSchematic(card, config)!;
  const panels = withPanels ? buildArchPanels(card, config, card.chips.includes('MLA') ? 'MLA' : 'GQA') : [];
  return layoutArch(s, L, panels);
}

const LLAMA = {
  model_type: 'llama', num_hidden_layers: 32, hidden_size: 4096, num_attention_heads: 32,
  num_key_value_heads: 8, intermediate_size: 11008, vocab_size: 32000, rms_norm_eps: 1e-5,
};
const k3Full = [...Array.from({ length: 23 }, (_, i) => (i + 1) * 4), 93]; // 4,8,…,92,93
const KIMI_K3 = {
  model_type: 'kimi_linear', hidden_size: 7168, num_hidden_layers: 93, num_attention_heads: 96,
  num_key_value_heads: 96, kv_lora_rank: 512, q_lora_rank: 1536, mla_use_nope: true,
  num_experts: 896, num_experts_per_token: 16, moe_intermediate_size: 3072, num_shared_experts: 2,
  first_k_dense_replace: 1, vocab_size: 163840, rms_norm_eps: 1e-5,
  linear_attn_config: { full_attn_layers: k3Full, num_heads: 96 },
};
const QWEN3_NEXT = {
  model_type: 'qwen3_next', hidden_size: 2048, num_hidden_layers: 48, num_attention_heads: 16,
  num_key_value_heads: 2, head_dim: 256, full_attention_interval: 4, linear_num_value_heads: 32,
  linear_key_head_dim: 128, num_experts: 512, num_experts_per_tok: 10, moe_intermediate_size: 512,
  vocab_size: 151936, rms_norm_eps: 1e-6,
};
const DEEPSEEK_V3 = {
  model_type: 'deepseek_v3', hidden_size: 7168, num_hidden_layers: 61, num_attention_heads: 128,
  num_key_value_heads: 128, kv_lora_rank: 512, q_lora_rank: 1536, n_routed_experts: 256,
  n_shared_experts: 1, num_experts_per_tok: 8, moe_intermediate_size: 2048, first_k_dense_replace: 3,
  vocab_size: 129280, rms_norm_eps: 1e-6,
};

// ── invariants every layout must satisfy ──────────────────────────────────────
function assertSane(l: ArchLayoutResult, what: string): void {
  const ids = [...l.cards.map((c) => c.id), ...l.boxes.map((b) => b.id)];
  assert.equal(new Set(ids).size, ids.length, `${what}: node ids must be unique`);

  // Cards never overlap vertically (they are a single column).
  const sorted = [...l.cards].sort((a, b) => a.y - b.y);
  for (let i = 0; i < sorted.length - 1; i += 1) {
    const a = sorted[i];
    const b = sorted[i + 1];
    assert.ok(a.y + a.h <= b.y, `${what}: card ${a.id} (y ${a.y}+${a.h}) overlaps ${b.id} (y ${b.y})`);
  }
  // Every card has a positive box.
  for (const c of l.cards) assert.ok(c.w > 0 && c.h > 0, `${what}: card ${c.id} has a degenerate box`);
  // Every edge endpoint resolves: cards for the stack flow, and a panel for the
  // target of a dotted leader line.
  const cardIds = new Set(l.cards.map((c) => c.id));
  const panelIds = new Set(l.panels.map((p) => p.id));
  for (const e of l.edges) {
    assert.ok(cardIds.has(e.source), `${what}: edge ${e.id} source ${e.source} missing`);
    const targets = e.kind === 'leader' ? panelIds : cardIds;
    assert.ok(targets.has(e.target), `${what}: edge ${e.id} target ${e.target} missing`);
    assert.notEqual(e.source, e.target, `${what}: edge ${e.id} is a self-loop`);
  }
  // Every box wraps at least one card, and wraps them COMPLETELY (this is the
  // assertion that would catch a mis-sized container in the figure).
  for (const b of l.boxes) {
    const inside = l.cards.filter((c) => c.y >= b.y && c.y + c.h <= b.y + b.h);
    assert.ok(inside.length > 0, `${what}: box ${b.id} wraps no card`);
    for (const c of inside) {
      assert.ok(c.x >= b.x && c.x + c.w <= b.x + b.w, `${what}: box ${b.id} does not span card ${c.id} horizontally`);
    }
  }
}

test('W3 uniform: geometry is exactly the pre-W3 stack (no regression)', () => {
  const l = laidFor(LLAMA);
  assert.equal(l.strip, null); // no pattern strip for a uniform model
  assert.equal(l.boxes.length, 1);
  assert.equal(l.boxes[0].id, '__container');
  assert.equal(l.boxes[0].label, '×N layers');
  // Seven cards at the classic fixed pitch, all H tall.
  assert.deepEqual(l.cards.map((c) => c.id), ['embed', 'norm1', 'attn', 'norm2', 'ffn', 'finalnorm', 'head']);
  l.cards.forEach((c, i) => {
    assert.equal(c.h, GEO.H);
    assert.equal(c.y, i * (GEO.H + GEO.GAP));
    assert.equal(c.x, GEO.X);
  });
  // The container wraps exactly the four in-block rows.
  assert.equal(l.boxes[0].y, GEO.H + GEO.GAP - GEO.PAD);
  assertSane(l, 'llama');
});

test('W3 Kimi K3: nested cycle box wraps the inner run boxes, strip has 93 cells', () => {
  const l = laidFor(KIMI_K3);
  assertSane(l, 'kimi-k3');

  // The pattern strip spans the block region, left of the stack.
  assert.ok(l.strip !== null);
  assert.equal(l.strip.cells.length, 93);
  assert.ok(l.strip.x + l.strip.w < GEO.X - GEO.PAD * 2, 'strip must sit clear of the outer container');
  assert.ok(l.strip.h > 0);

  // One outer cycle box, and it must be WIDER than (and behind) the run boxes it
  // wraps — the bug the geometry test exists to catch.
  const cycles = l.boxes.filter((b) => b.variant === 'cycle');
  const runs = l.boxes.filter((b) => b.variant === 'run');
  assert.equal(cycles.length, 1);
  assert.ok(runs.length >= 2);
  const cyc = cycles[0];
  const nested = runs.filter((r) => r.y >= cyc.y && r.y + r.h <= cyc.y + cyc.h);
  assert.equal(nested.length, 2, 'the KDA×3 / MLA×1 unit renders two nested run boxes');
  for (const r of nested) {
    assert.ok(cyc.x < r.x && cyc.x + cyc.w > r.x + r.w, 'the cycle box must be wider than its inner run boxes');
    assert.ok(cyc.z < r.z, 'the cycle box must sit behind its inner run boxes');
  }
  // The cycle's label is the repeat count; the inner runs carry the 3:1 idiom.
  assert.match(cyc.label, /^×\d+$/);
  assert.deepEqual(nested.map((r) => r.label), ['×3 · KDA', '×1 · MLA']);

  // Cards sit above every box.
  const maxBoxZ = Math.max(...l.boxes.map((b) => b.z));
  for (const c of l.cards) assert.ok(c.z > maxBoxZ, `card ${c.id} must render above the containers`);
});

test('W3 Kimi K3: linear blocks get the novelty attn tag + honest O(1) sub-line', () => {
  const l = laidFor(KIMI_K3);
  const kda = l.cards.filter((c) => c.attn === 'KDA');
  const mla = l.cards.filter((c) => c.attn === 'MLA');
  assert.ok(kda.length >= 1 && mla.length >= 1);
  assert.ok(isLinearAttn('KDA') && !isLinearAttn('MLA'));
  // A linear operator has no KV projection to quote — it says so instead of
  // borrowing the MLA head split.
  assert.equal(kda[0].node.sub, 'O(1) state');
  assert.match(mla[0].node.sub ?? '', /MLA/);
  // First-K-dense: layer 0 is dense, so the first KDA block renders a dense MLP.
  assert.equal(l.strip?.cells[0].ffn, 'dense');
});

test('W3 Qwen3-Next: a single 3:1 cycle, GQA full-attention blocks', () => {
  const l = laidFor(QWEN3_NEXT);
  assertSane(l, 'qwen3-next');
  const cyc = l.boxes.filter((b) => b.variant === 'cycle');
  assert.equal(cyc.length, 1);
  assert.equal(cyc[0].label, '×12'); // 48 / 4
  const runs = l.boxes.filter((b) => b.variant === 'run');
  assert.deepEqual(runs.map((r) => r.label), ['×3 · GatedDeltaNet', '×1 · GQA']);
  assert.equal(l.strip?.cells.length, 48);
});

test('W3 DeepSeek-V3: uniform attention → one run box, no cycle, strip still shown', () => {
  const l = laidFor(DEEPSEEK_V3);
  assertSane(l, 'deepseek-v3');
  assert.equal(l.boxes.filter((b) => b.variant === 'cycle').length, 0);
  const runs = l.boxes.filter((b) => b.variant === 'run');
  assert.equal(runs.length, 1);
  assert.equal(runs[0].label, '×61 · MLA');
  // The FFN heterogeneity (first 3 dense) is what earned the layout; the strip
  // carries it even though the attention track is uniform.
  assert.equal(l.strip?.cells.length, 61);
  assert.deepEqual(l.strip?.cells.slice(0, 4).map((c) => c.ffn), ['dense', 'dense', 'dense', 'moe']);
});

// ── W4: zoom-in panel placement ───────────────────────────────────────────────

test('W4 panels: placed clear of the stack, anchored, never overlapping', () => {
  const l = laidFor(KIMI_K3, true);
  assertSane(l, 'kimi-k3+panels');
  assert.equal(l.panels.length, 2); // attention + MoE
  const cardIds = new Set(l.cards.map((c) => c.id));
  for (const p of l.panels) {
    // Clear of the stack column, and tied to a real card.
    assert.ok(p.x >= GEO.X + GEO.W, `panel ${p.id} overlaps the stack column`);
    assert.ok(cardIds.has(p.anchorCardId), `panel ${p.id} anchor ${p.anchorCardId} is not a card`);
    assert.ok(p.h > GEO.PANEL_HEAD, `panel ${p.id} has no room for its rows`);
  }
  // The two panels do not overlap each other vertically.
  const [a, b] = [...l.panels].sort((x, y) => x.y - y.y);
  assert.ok(a.y + a.h <= b.y, 'panels must not overlap');
  // Each panel gets exactly one dotted leader edge from its anchor.
  const leaders = l.edges.filter((e) => e.kind === 'leader');
  assert.equal(leaders.length, 2);
  for (const e of leaders) {
    assert.ok(cardIds.has(e.source));
    assert.ok(l.panels.some((p) => p.id === e.target));
  }
});

test('W4 panels: the attention panel anchors an attention card, MoE a MoE card', () => {
  const l = laidFor(KIMI_K3, true);
  const byKind = new Map(l.panels.map((p) => [p.panel.kind, p]));
  const cardOf = (id: string) => l.cards.find((c) => c.id === id)!;
  assert.equal(cardOf(byKind.get('attention')!.anchorCardId).node.kind, 'attention');
  assert.equal(cardOf(byKind.get('moe')!.anchorCardId).node.kind, 'moe');
});

test('W4 panels: a dense model gets only the attention panel; none when not asked', () => {
  const withPanels = laidFor(LLAMA, true);
  assert.deepEqual(withPanels.panels.map((p) => p.panel.kind), ['attention']);
  // Panels are opt-in: the default layout carries none (and no leader edges).
  const bare = laidFor(LLAMA);
  assert.equal(bare.panels.length, 0);
  assert.equal(bare.edges.filter((e) => e.kind === 'leader').length, 0);
});

test('W4 panelRows: expert chips share one row instead of stacking', () => {
  const config = parseHfConfig(JSON.stringify(KIMI_K3))!;
  const card = classifyArch({ config, tensorNames: [] })!;
  const [, moe] = buildArchPanels(card, config, 'MLA');
  // router + (4 chips + "more" collapsed into ONE row) + shared = 3 rows.
  assert.equal(panelRows(moe), 3);
  assert.ok(moe.items.length > 3, 'the chip items are still all present in the spec');
});

test('W3 grouped: the main flow is one unbroken chain through every card', () => {
  const l = laidFor(KIMI_K3);
  const main = l.edges.filter((e) => e.kind === 'main');
  // n cards → n-1 main edges, chained in emission (top→bottom) order.
  assert.equal(main.length, l.cards.length - 1);
  const order = l.cards.map((c) => c.id);
  main.forEach((e, i) => {
    assert.equal(e.source, order[i]);
    assert.equal(e.target, order[i + 1]);
  });
  // Residuals exist per block and never dangle (assertSane already proved the
  // endpoints resolve).
  assert.ok(l.edges.some((e) => e.kind === 'residual'));
});
