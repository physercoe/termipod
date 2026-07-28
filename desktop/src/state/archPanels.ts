/// Zoom-in panel specs for the architecture schematic (archgraph plan W4) — the
/// dotted "expansion" panels of the reference figures: what is INSIDE an
/// attention block (the projection chain) and inside a MoE block (router →
/// experts). Pure and unit-tested, like the rest of the schematic spec (D-1).
///
/// Everything here is **config-derived** (D-2): each item's dims come from the
/// parsed `config.json`, never invented. Display strings stay out — items carry
/// an i18n key suffix (resolved under `archgraph.panel.`) plus an already-stringified
/// `value` for the dims, so this module needs no i18n and stays testable.
/// The one narrative element (a per-family note) is gated on a config key the
/// family actually ships, so it is never asserted for a model that lacks it.
import type { ArchCard } from './checkpoint.ts';
import type { AttnKind } from './archSchematic.ts';

export type PanelShape = 'proj' | 'op' | 'router' | 'expert' | 'more' | 'shared';

export interface ArchPanelItem {
  id: string;
  /// i18n key suffix under `archgraph.panel.` — the renderer translates it.
  key: string;
  /// Muted dims line (already formatted, e.g. "7168 → 1536"); omitted when the
  /// config doesn't disclose the shape.
  value?: string;
  shape: PanelShape;
}

export interface ArchPanel {
  id: string;
  /// Which block this panel expands — also the leader-line anchor.
  kind: 'attention' | 'moe';
  /// i18n key suffix for the panel title.
  titleKey: string;
  items: ArchPanelItem[];
  /// Per-family narrative (D-2 overlay), gated on a config key the family really
  /// ships. i18n key suffix; absent for models that don't carry it.
  noteKey?: string;
}

function num(config: Record<string, unknown>, ...keys: string[]): number | undefined {
  for (const k of keys) {
    const v = config[k];
    if (typeof v === 'number' && Number.isFinite(v)) return v;
  }
  return undefined;
}

/// "a → b" when both sides are known, else undefined (honest blank).
function dims(a: number | undefined, b: number | undefined): string | undefined {
  return a !== undefined && b !== undefined ? `${a} → ${b}` : undefined;
}

/// Max expert chips drawn before collapsing into a "+N more" chip — the figure
/// idiom is a short row (`1 2 3 … N`), not 896 boxes.
const EXPERT_CHIPS = 4;

/// The attention block's internals for one operator kind. MLA expands its
/// low-rank chain (q_a/q_b, kv_a/kv_b), GQA/MHA the plain Q/K/V projections, and
/// the linear operators their conv + gate + delta-rule path. Returns null when
/// the config is too bare to say anything true.
export function buildAttentionPanel(card: ArchCard, config: Record<string, unknown>, attn: AttnKind): ArchPanel | null {
  const hidden = card.hidden ?? num(config, 'hidden_size', 'n_embd');
  const heads = card.heads ?? num(config, 'num_attention_heads');
  const kvHeads = card.kvHeads ?? num(config, 'num_key_value_heads');
  const headDim = num(config, 'head_dim') ?? (hidden !== undefined && heads !== undefined && heads > 0 ? hidden / heads : undefined);
  const items: ArchPanelItem[] = [];

  if (attn === 'KDA' || attn === 'GatedDeltaNet') {
    const lac = config.linear_attn_config;
    const l = lac !== null && typeof lac === 'object' && !Array.isArray(lac) ? (lac as Record<string, unknown>) : {};
    const lHeads = num(l, 'num_heads') ?? num(config, 'linear_num_value_heads');
    const lHeadDim = num(l, 'head_dim') ?? num(config, 'linear_value_head_dim');
    const kernel = num(l, 'short_conv_kernel_size') ?? num(config, 'linear_conv_kernel_dim');
    items.push({ id: 'qkv', key: 'qkvProj', value: dims(hidden, lHeads !== undefined && lHeadDim !== undefined ? lHeads * lHeadDim : undefined), shape: 'proj' });
    if (kernel !== undefined) items.push({ id: 'conv', key: 'shortConv', value: `k=${kernel}`, shape: 'op' });
    items.push({ id: 'gate', key: 'gate', shape: 'op' });
    items.push({ id: 'delta', key: attn === 'KDA' ? 'deltaRuleKda' : 'deltaRule', value: lHeads !== undefined ? `${lHeads} heads` : undefined, shape: 'op' });
    items.push({ id: 'o', key: 'oProj', value: dims(lHeads !== undefined && lHeadDim !== undefined ? lHeads * lHeadDim : undefined, hidden), shape: 'proj' });
    return { id: `panel-attn-${attn}`, kind: 'attention', titleKey: 'attnInternals', items, noteKey: noteFor(config) };
  }

  const kvLora = num(config, 'kv_lora_rank');
  if (kvLora !== undefined || attn === 'MLA') {
    const qLora = num(config, 'q_lora_rank');
    const qkNope = num(config, 'qk_nope_head_dim');
    const qkRope = num(config, 'qk_rope_head_dim');
    const vDim = num(config, 'v_head_dim') ?? headDim;
    const qHeadDim = qkNope !== undefined && qkRope !== undefined ? qkNope + qkRope : headDim;
    if (qLora !== undefined) {
      items.push({ id: 'qa', key: 'qAProj', value: dims(hidden, qLora), shape: 'proj' });
      items.push({ id: 'qb', key: 'qBProj', value: dims(qLora, heads !== undefined && qHeadDim !== undefined ? heads * qHeadDim : undefined), shape: 'proj' });
    } else {
      items.push({ id: 'q', key: 'qProj', value: dims(hidden, heads !== undefined && qHeadDim !== undefined ? heads * qHeadDim : undefined), shape: 'proj' });
    }
    if (kvLora !== undefined) {
      items.push({ id: 'kva', key: 'kvAProj', value: dims(hidden, qkRope !== undefined ? kvLora + qkRope : kvLora), shape: 'proj' });
      items.push({ id: 'kvb', key: 'kvBProj', value: dims(kvLora, heads !== undefined && qkNope !== undefined && vDim !== undefined ? heads * (qkNope + vDim) : undefined), shape: 'proj' });
    }
    items.push({ id: 'softmax', key: 'softmax', value: qkRope !== undefined && qkNope !== undefined ? `RoPE ${qkRope} + NoPE ${qkNope}` : undefined, shape: 'op' });
    items.push({ id: 'o', key: 'oProj', value: dims(heads !== undefined && vDim !== undefined ? heads * vDim : undefined, hidden), shape: 'proj' });
    return { id: `panel-attn-${attn}`, kind: 'attention', titleKey: 'attnInternals', items, noteKey: noteFor(config) };
  }

  if (hidden === undefined || heads === undefined || headDim === undefined) return null;
  const kvh = kvHeads ?? heads;
  items.push({ id: 'q', key: 'qProj', value: dims(hidden, heads * headDim), shape: 'proj' });
  items.push({ id: 'k', key: 'kProj', value: dims(hidden, kvh * headDim), shape: 'proj' });
  items.push({ id: 'v', key: 'vProj', value: dims(hidden, kvh * headDim), shape: 'proj' });
  const window = num(config, 'sliding_window');
  items.push({ id: 'softmax', key: attn === 'sliding' ? 'softmaxWindowed' : 'softmax', value: attn === 'sliding' && window !== undefined ? `window ${window}` : undefined, shape: 'op' });
  items.push({ id: 'o', key: 'oProj', value: dims(heads * headDim, hidden), shape: 'proj' });
  return { id: `panel-attn-${attn}`, kind: 'attention', titleKey: 'attnInternals', items, noteKey: noteFor(config) };
}

/// D-2 overlay: a narrative note is offered ONLY when the config carries the key
/// that family actually ships — never inferred from a family name.
function noteFor(config: Record<string, unknown>): string | undefined {
  if (num(config, 'attn_res_block_size') !== undefined) return 'noteAttnRes';
  return undefined;
}

/// The MoE block's internals: the router, a short row of expert chips (collapsed
/// with a "+N more" chip), and the always-on shared experts. Returns null for a
/// dense model.
export function buildMoePanel(card: ArchCard, config: Record<string, unknown>): ArchPanel | null {
  const experts = card.experts ?? num(config, 'num_local_experts', 'n_routed_experts', 'num_experts');
  if (experts === undefined || experts <= 0) return null;
  const hidden = card.hidden ?? num(config, 'hidden_size');
  const topk = card.expertsPerTok ?? num(config, 'num_experts_per_tok', 'num_experts_per_token', 'moe_topk');
  const inter = num(config, 'moe_intermediate_size');
  const shared = card.sharedExperts ?? num(config, 'n_shared_experts', 'num_shared_experts');

  const items: ArchPanelItem[] = [
    { id: 'router', key: 'router', value: topk !== undefined ? `top-${topk} of ${experts}` : `${experts}`, shape: 'router' },
  ];
  const chips = Math.min(EXPERT_CHIPS, experts);
  for (let i = 0; i < chips; i += 1) {
    items.push({ id: `e${i}`, key: 'expert', value: inter !== undefined && hidden !== undefined ? `${hidden} → ${inter}` : undefined, shape: 'expert' });
  }
  if (experts > chips) items.push({ id: 'more', key: 'moreExperts', value: `+${experts - chips}`, shape: 'more' });
  if (shared !== undefined && shared > 0) {
    items.push({ id: 'shared', key: 'sharedExpert', value: `×${shared}`, shape: 'shared' });
  }
  return { id: 'panel-moe', kind: 'moe', titleKey: 'moeRouting', items };
}

/// Both panels for a model, in render order. `attn` selects which attention
/// operator to expand (a hybrid stack expands its full-attention kind, since that
/// is the one whose projections the config fully describes).
export function buildArchPanels(card: ArchCard, config: Record<string, unknown>, attn: AttnKind): ArchPanel[] {
  const out: ArchPanel[] = [];
  const a = buildAttentionPanel(card, config, attn);
  if (a !== null) out.push(a);
  const m = buildMoePanel(card, config);
  if (m !== null) out.push(m);
  return out;
}
