/// Config-only architecture **schematic** (round-3 §5a follow-on): a paper-style
/// block diagram of a decoder-only transformer, derived purely from a parsed HF
/// `config.json` + the shipped `ArchCard` classification. No weights, no compute
/// graph — a `config.json` cannot yield a true tensor graph (that needs the ONNX
/// export or the real module tree). What it CAN yield is the canonical stacked
/// figure people draw on slides: token embedding → N× decoder block
/// {norm → attention → norm → MLP/MoE, with residual skips} → final norm → head.
///
/// This module is the PURE spec (nodes + residual edges + N + labels), so the
/// item set / labels / block membership are unit-tested without React Flow. The
/// renderer (`ui/ArchSchematicView.tsx`) lays it out and styles it.
import { humanCount, type ArchCard } from './checkpoint.ts';

/// A block kind → drives the node's colour band in the renderer.
export type ArchNodeKind = 'embed' | 'norm' | 'attention' | 'ffn' | 'moe' | 'finalnorm' | 'head';

export interface ArchNode {
  id: string;
  kind: ArchNodeKind;
  /// The bold primary line.
  label: string;
  /// The muted secondary line (dims / head split / expert counts); omitted when
  /// the config didn't carry it.
  sub?: string;
  /// Inside the ×N repeated-decoder container.
  inBlock: boolean;
}

export interface ArchSchematic {
  /// Ordered input→output (top→bottom in the figure).
  nodes: ArchNode[];
  /// Residual skip connections (routed on the side in the renderer).
  residuals: Array<{ from: string; to: string }>;
  /// Repeat count of the decoder block (the ×N badge). 0 when unknown.
  layers: number;
  /// W2 — heterogeneous-stack layout, present ONLY when the stack is NOT uniform
  /// (a hybrid attention interleave, a first-K-dense FFN, or a sliding/global
  /// window cadence). A uniform decoder omits it entirely and the schematic is
  /// exactly the classic single ×N container (no regression). The renderer (W3)
  /// draws the pattern strip + nested groups; W4 reads `annotations`.
  layout?: LayerLayout;
}

/// The per-layer attention operator. Full (softmax) variants keep a growing KV
/// cache; the linear/recurrent ones (KDA, Gated DeltaNet) do not. `sliding` /
/// `global` distinguish Gemma-3's windowed vs full-context softmax layers.
export type AttnKind = 'MLA' | 'GQA' | 'MHA' | 'KDA' | 'GatedDeltaNet' | 'sliding' | 'global';
/// The per-layer FFN: a dense MLP or a Mixture-of-Experts block.
export type FfnKind = 'dense' | 'moe';

/// One layer of the pattern strip (0-based `index`, top→bottom).
export interface LayerCell {
  index: number;
  attn: AttnKind;
  ffn: FfnKind;
}

/// A maximal run of consecutive same-attention layers (the FFN may still vary
/// within it; FFN heterogeneity is surfaced via `annotations`, not the run).
export interface ArchRun {
  attn: AttnKind;
  count: number;
  from: number;
  to: number;
}

/// A repeating cycle of runs (the `3×`/`1×` interleave idiom): `repeat` copies of
/// `unit` cover layers `from..to`. Detected from the run sequence, so an
/// irregular head/tail stays as plain runs around it.
export interface ArchCycle {
  repeat: number;
  unit: ArchRun[];
  from: number;
  to: number;
}

/// A top-level group in the stack: either a plain run or a detected cycle.
export type ArchGroup = { kind: 'run'; run: ArchRun } | { kind: 'cycle'; cycle: ArchCycle };

/// A config-derived callout (the leader-line annotations of the reference
/// figures): the interleave ratio, the first-K-dense FFN split, the
/// sliding/global cadence, multi-token prediction. `id` is stable for the
/// renderer; `text` is English prose (i18n happens at render, W3/W4).
export interface ArchAnnotation {
  id: string;
  text: string;
}

export interface LayerLayout {
  /// One cell per layer (length === `layers`), top→bottom.
  strip: LayerCell[];
  /// Top-level groups (runs + detected cycles) covering the whole stack in order.
  groups: ArchGroup[];
  /// Config-derived callouts.
  annotations: ArchAnnotation[];
}

function readNum(config: Record<string, unknown>, ...keys: string[]): number | undefined {
  for (const k of keys) {
    const v = config[k];
    if (typeof v === 'number' && Number.isFinite(v)) return v;
  }
  return undefined;
}

/// Which normalization the config implies. Modern decoders (LLaMA/Qwen/Mistral/
/// DeepSeek/…) carry `rms_norm_eps`; GPT-2/BERT-era configs carry
/// `layer_norm_epsilon`. Fall back to a neutral "Norm" when neither is present.
function normLabel(config: Record<string, unknown>): string {
  if (readNum(config, 'rms_norm_eps') !== undefined) return 'RMSNorm';
  if (readNum(config, 'layer_norm_epsilon', 'layer_norm_eps') !== undefined) return 'LayerNorm';
  return 'Norm';
}

// ── W2: heterogeneous layer-pattern derivation (all config-derived, pure) ──────

// Short human labels for the ratio/annotation prose.
const ATTN_LABEL: Record<AttnKind, string> = {
  MLA: 'full attention (MLA)',
  GQA: 'full attention (GQA)',
  MHA: 'full attention (MHA)',
  KDA: 'KDA',
  GatedDeltaNet: 'Gated DeltaNet',
  sliding: 'sliding-window',
  global: 'global attention',
};

/// The single attention kind of a uniform stack, from the classifier's card.
function uniformAttnKind(card: ArchCard): AttnKind {
  if (card.template === 'mla' || card.template === 'mla-moe' || card.chips.includes('MLA')) return 'MLA';
  if (card.kvHeads !== undefined && card.heads !== undefined && card.kvHeads < card.heads) return 'GQA';
  return 'MHA';
}

/// Per-layer attention kinds (length `layers`). Recognises the three real
/// heterogeneous schemes; falls back to the uniform kind otherwise. Kimi's index
/// arrays are 1-based (layer 1..N); everything here is emitted 0-based.
function perLayerAttn(card: ArchCard, config: Record<string, unknown>, layers: number): AttnKind[] {
  const lac = config.linear_attn_config;
  if (lac !== null && typeof lac === 'object' && !Array.isArray(lac)) {
    const full = (lac as Record<string, unknown>).full_attn_layers;
    if (Array.isArray(full)) {
      const fullSet = new Set(full.filter((n): n is number => typeof n === 'number'));
      // Kimi full-attention layers are Gated-MLA; the rest are the linear operator
      // named on the card (Kimi Delta Attention).
      const linear: AttnKind = card.linearKind === 'Gated DeltaNet' ? 'GatedDeltaNet' : 'KDA';
      return Array.from({ length: layers }, (_, i) => (fullSet.has(i + 1) ? 'MLA' : linear));
    }
  }
  const interval = readNum(config, 'full_attention_interval');
  const hasLinear = readNum(config, 'linear_num_value_heads', 'linear_num_key_heads', 'linear_key_head_dim') !== undefined;
  if (hasLinear && interval !== undefined && interval > 0) {
    // Qwen3-Next: Gated DeltaNet everywhere except every Nth layer (full GQA).
    return Array.from({ length: layers }, (_, i) => ((i + 1) % interval === 0 ? 'GQA' : 'GatedDeltaNet'));
  }
  const swPattern = readNum(config, 'sliding_window_pattern');
  if (swPattern !== undefined && swPattern > 0 && readNum(config, 'sliding_window') !== undefined) {
    // Gemma-3: sliding-window attention except every Pth layer (global).
    return Array.from({ length: layers }, (_, i) => ((i + 1) % swPattern === 0 ? 'global' : 'sliding'));
  }
  return Array.from({ length: layers }, () => uniformAttnKind(card));
}

/// Per-layer FFN kinds. A MoE stack keeps its first `first_k_dense_replace`
/// layers dense (DeepSeek-V3/Kimi convention), the rest MoE; a dense model is all
/// dense.
function perLayerFfn(card: ArchCard, config: Record<string, unknown>, layers: number): FfnKind[] {
  const isMoe = card.template === 'moe' || card.template === 'mla-moe' || (card.experts !== undefined && card.experts > 0) || card.chips.includes('MoE');
  if (!isMoe) return Array.from({ length: layers }, () => 'dense');
  const k = Math.min(Math.max(readNum(config, 'first_k_dense_replace') ?? 0, 0), layers);
  return Array.from({ length: layers }, (_, i) => (i < k ? 'dense' : 'moe'));
}

/// Run-length encode the attention track into maximal same-kind runs.
function runLengthAttn(attn: AttnKind[]): ArchRun[] {
  const runs: ArchRun[] = [];
  for (let i = 0; i < attn.length; i += 1) {
    const last = runs[runs.length - 1];
    if (last && last.attn === attn[i]) {
      last.count += 1;
      last.to = i;
    } else {
      runs.push({ attn: attn[i], count: 1, from: i, to: i });
    }
  }
  return runs;
}

/// Detect the single best (longest-covering) repeating cycle in a run sequence —
/// the `3×`/`1×` interleave idiom. Returns the start run-index, the unit length
/// in runs, and the repeat count (≥2), or null when no run-unit repeats.
function detectCycle(runs: ArchRun[]): { start: number; unitLen: number; repeat: number } | null {
  const sig = runs.map((r) => `${r.attn}:${r.count}`);
  let best: { start: number; unitLen: number; repeat: number; covered: number } | null = null;
  for (let start = 0; start < sig.length; start += 1) {
    for (let unit = 1; unit <= (sig.length - start) / 2; unit += 1) {
      const base = sig.slice(start, start + unit).join('|');
      let repeat = 1;
      while (start + unit * (repeat + 1) <= sig.length && sig.slice(start + unit * repeat, start + unit * (repeat + 1)).join('|') === base) {
        repeat += 1;
      }
      const covered = unit * repeat;
      if (repeat >= 2 && (best === null || covered > best.covered)) best = { start, unitLen: unit, repeat, covered };
    }
  }
  return best;
}

/// Fold runs into top-level groups, wrapping the best-detected cycle.
function buildGroups(runs: ArchRun[]): ArchGroup[] {
  const cyc = detectCycle(runs);
  if (cyc === null) return runs.map((run) => ({ kind: 'run', run }));
  const groups: ArchGroup[] = [];
  for (let i = 0; i < cyc.start; i += 1) groups.push({ kind: 'run', run: runs[i] });
  const unit = runs.slice(cyc.start, cyc.start + cyc.unitLen);
  const end = cyc.start + cyc.unitLen * cyc.repeat;
  groups.push({
    kind: 'cycle',
    cycle: { repeat: cyc.repeat, unit, from: runs[cyc.start].from, to: runs[end - 1].to },
  });
  for (let i = end; i < runs.length; i += 1) groups.push({ kind: 'run', run: runs[i] });
  return groups;
}

/// Config-derived callouts (the reference figures' leader-line annotations).
function buildAnnotations(card: ArchCard, config: Record<string, unknown>, groups: ArchGroup[], layers: number): ArchAnnotation[] {
  const out: ArchAnnotation[] = [];

  // Interleave ratio, read off the detected cycle's unit (the 3:1 / 5:1 idiom).
  const cycle = groups.find((g): g is { kind: 'cycle'; cycle: ArchCycle } => g.kind === 'cycle')?.cycle;
  if (cycle && cycle.unit.length >= 2) {
    const ratio = cycle.unit.map((r) => `${r.count} ${ATTN_LABEL[r.attn]}`).join(' : ');
    out.push({ id: 'interleave', text: `${ratio} per block, repeated ×${cycle.repeat}` });
  }

  // First-K-dense FFN split.
  const isMoe = card.template === 'moe' || card.template === 'mla-moe' || (card.experts !== undefined && card.experts > 0) || card.chips.includes('MoE');
  const k = readNum(config, 'first_k_dense_replace') ?? 0;
  if (isMoe && k > 0 && k < layers) {
    const head = k === 1 ? 'Layer 0 dense FFN' : `Layers 0–${k - 1} dense FFN`;
    out.push({ id: 'first-k-dense', text: `${head}; layers ${k}–${layers - 1} Mixture-of-Experts` });
  }

  // Multi-token prediction head(s).
  const mtp = readNum(config, 'num_nextn_predict_layers', 'num_mtp_layers') ?? 0;
  if (mtp > 0) out.push({ id: 'mtp', text: `+${mtp} multi-token-prediction layer${mtp > 1 ? 's' : ''}` });

  return out;
}

/// Build the heterogeneous-stack layout, or null when the stack is uniform in
/// BOTH attention and FFN (the common case — the classic single ×N container
/// already expresses it, so the schematic stays byte-for-byte as before).
export function buildLayerLayout(card: ArchCard, config: Record<string, unknown>): LayerLayout | null {
  const layers = card.layers ?? readNum(config, 'num_hidden_layers', 'n_layer', 'n_layers', 'num_layers') ?? 0;
  if (layers <= 0) return null;
  const attn = perLayerAttn(card, config, layers);
  const ffn = perLayerFfn(card, config, layers);
  const attnHetero = new Set(attn).size > 1;
  const ffnHetero = new Set(ffn).size > 1;
  if (!attnHetero && !ffnHetero) return null;

  const strip: LayerCell[] = Array.from({ length: layers }, (_, i) => ({ index: i, attn: attn[i], ffn: ffn[i] }));
  const groups = buildGroups(runLengthAttn(attn));
  const annotations = buildAnnotations(card, config, groups, layers);
  return { strip, groups, annotations };
}

/// Build the schematic from the classifier's card + the raw config. Returns null
/// when the config lacks the two facts a stack needs (a hidden size and a
/// positive layer count) — the caller then keeps the plain params card.
export function buildArchSchematic(card: ArchCard, config: Record<string, unknown>): ArchSchematic | null {
  const layers = card.layers ?? readNum(config, 'num_hidden_layers', 'n_layer', 'n_layers', 'num_layers') ?? 0;
  const hidden = card.hidden ?? readNum(config, 'hidden_size', 'n_embd', 'd_model', 'n_embed');
  if (hidden === undefined || layers <= 0) return null;

  // Until the heterogeneous-stack spec lands (W2), a `linear-hybrid` card renders
  // the honest uniform fallback: derive MLA/MoE from the chips/experts the
  // classifier read, so K3 still shows its MLA-style attention + MoE FFN blocks
  // rather than dropping to a dense MLP (D-4: uniform stack, never a guessed hybrid).
  const isMla = card.template === 'mla' || card.template === 'mla-moe' || card.chips.includes('MLA');
  const isMoe = card.template === 'moe' || card.template === 'mla-moe' || (card.experts !== undefined && card.experts > 0);
  const norm = normLabel(config);
  // Prefer the classifier's readings, but fall back to the config directly — the
  // card only carries head counts for some families, while the schematic wants
  // them for every dense/GQA/MHA stack.
  const heads = card.heads ?? readNum(config, 'num_attention_heads', 'n_head');
  const kvHeads = card.kvHeads ?? readNum(config, 'num_key_value_heads', 'num_kv_heads', 'n_head_kv');
  const vocab = card.vocab ?? readNum(config, 'vocab_size');

  // Attention sub-label: MLA vs GQA vs MHA, with the head split when known.
  let attnLabel = 'Self-Attention';
  let attnSub: string | undefined;
  if (isMla) {
    attnLabel = 'Multi-head Latent Attention';
    attnSub = heads !== undefined ? `MLA · ${heads} heads` : 'MLA';
  } else if (heads !== undefined && kvHeads !== undefined && kvHeads < heads) {
    attnLabel = 'Grouped-query Attention';
    attnSub = `GQA · ${heads} Q / ${kvHeads} KV heads`;
  } else if (heads !== undefined) {
    attnLabel = 'Multi-head Attention';
    attnSub = `MHA · ${heads} heads`;
  }

  // FFN sub-label: MoE (experts · top-k [+shared]) vs a dense MLP (width).
  const ffnKind: ArchNodeKind = isMoe ? 'moe' : 'ffn';
  let ffnLabel: string;
  let ffnSub: string | undefined;
  if (isMoe) {
    ffnLabel = 'Mixture-of-Experts FFN';
    const parts: string[] = [];
    if (card.experts !== undefined) parts.push(`${card.experts} experts`);
    if (card.expertsPerTok !== undefined) parts.push(`top-${card.expertsPerTok}`);
    if (card.sharedExperts !== undefined && card.sharedExperts > 0) parts.push(`+${card.sharedExperts} shared`);
    ffnSub = parts.length > 0 ? parts.join(' · ') : undefined;
  } else {
    ffnLabel = 'Feed-Forward (MLP)';
    const inter = readNum(config, 'intermediate_size', 'ffn_dim', 'n_inner');
    ffnSub = inter !== undefined ? `hidden ${humanCount(hidden)} → ${humanCount(inter)}` : `hidden ${humanCount(hidden)}`;
  }

  const tied = config.tie_word_embeddings === true;
  const nodes: ArchNode[] = [
    { id: 'embed', kind: 'embed', label: 'Token Embedding', sub: vocab !== undefined ? `${humanCount(vocab)} vocab × ${humanCount(hidden)}` : `d = ${humanCount(hidden)}`, inBlock: false },
    { id: 'norm1', kind: 'norm', label: norm, inBlock: true },
    { id: 'attn', kind: 'attention', label: attnLabel, sub: attnSub, inBlock: true },
    { id: 'norm2', kind: 'norm', label: norm, inBlock: true },
    { id: 'ffn', kind: ffnKind, label: ffnLabel, sub: ffnSub, inBlock: true },
    { id: 'finalnorm', kind: 'finalnorm', label: `Final ${norm}`, inBlock: false },
    { id: 'head', kind: 'head', label: 'LM Head', sub: `→ ${vocab !== undefined ? humanCount(vocab) : ''} logits${tied ? ' (tied)' : ''}`.trim(), inBlock: false },
  ];

  // The two canonical residual streams: around attention, around the FFN.
  const residuals = [
    { from: 'norm1', to: 'norm2' },
    { from: 'norm2', to: 'finalnorm' },
  ];

  // W2: attach the heterogeneous-stack layout only when the stack is NOT uniform
  // — a uniform decoder keeps exactly the classic single ×N schematic (no new
  // key), so existing callers and the golden test see no change.
  const layout = buildLayerLayout(card, config);
  return layout === null ? { nodes, residuals, layers } : { nodes, residuals, layers, layout };
}

/// A key/value row in the schematic's per-node detail panel.
export interface ArchDetailRow {
  label: string;
  value: string;
}

/// The facts to show when a schematic node is selected — pulled from the same
/// card + config the diagram was built from, but at full detail (the cards only
/// have room for one sub-line). Pure + English (like the rest of the schematic
/// spec), so it's unit-tested without React. Empty rows are dropped.
export function archNodeDetails(node: ArchNode, config: Record<string, unknown>, card: ArchCard): ArchDetailRow[] {
  const rows: ArchDetailRow[] = [];
  const push = (label: string, v: string | number | undefined): void => {
    if (v !== undefined && v !== '') rows.push({ label, value: typeof v === 'number' ? String(v) : v });
  };
  const hidden = card.hidden ?? readNum(config, 'hidden_size', 'n_embd', 'd_model', 'n_embed');
  const heads = card.heads ?? readNum(config, 'num_attention_heads', 'n_head');
  const kvHeads = card.kvHeads ?? readNum(config, 'num_key_value_heads', 'num_kv_heads', 'n_head_kv');
  const vocab = card.vocab ?? readNum(config, 'vocab_size');
  const act = typeof config.hidden_act === 'string' ? config.hidden_act : undefined;

  switch (node.kind) {
    case 'embed':
      push('Vocab size', vocab !== undefined ? humanCount(vocab) : undefined);
      push('Hidden size', hidden !== undefined ? humanCount(hidden) : undefined);
      push('Max context', readNum(config, 'max_position_embeddings', 'n_positions'));
      break;
    case 'attention': {
      const isMla = card.template === 'mla' || card.template === 'mla-moe' || card.chips.includes('MLA');
      const type = isMla
        ? 'Multi-head Latent (MLA)'
        : heads !== undefined && kvHeads !== undefined && kvHeads < heads
          ? 'Grouped-query (GQA)'
          : 'Multi-head (MHA)';
      push('Type', type);
      push('Attention heads', heads);
      push('KV heads', kvHeads);
      push('Head dim', readNum(config, 'head_dim') ?? (hidden !== undefined && heads !== undefined && heads > 0 ? hidden / heads : undefined));
      push('kv_lora_rank', readNum(config, 'kv_lora_rank'));
      push('q_lora_rank', readNum(config, 'q_lora_rank'));
      push('qk_rope_head_dim', readNum(config, 'qk_rope_head_dim'));
      push('v_head_dim', readNum(config, 'v_head_dim'));
      break;
    }
    case 'ffn':
      push('Type', 'Dense MLP');
      push('Hidden size', hidden !== undefined ? humanCount(hidden) : undefined);
      push('Intermediate size', readNum(config, 'intermediate_size', 'ffn_dim', 'n_inner'));
      push('Activation', act);
      break;
    case 'moe': {
      push('Type', 'Mixture-of-Experts');
      push('Routed experts', card.experts);
      push('Active per token', card.expertsPerTok);
      push('Shared experts', card.sharedExperts !== undefined && card.sharedExperts > 0 ? card.sharedExperts : undefined);
      push('Expert intermediate', readNum(config, 'moe_intermediate_size'));
      const denseK = readNum(config, 'first_k_dense_replace');
      push('Dense first-K layers', denseK !== undefined && denseK > 0 ? denseK : undefined);
      push('Activation', act);
      break;
    }
    case 'norm':
    case 'finalnorm': {
      push('Type', node.label.replace(/^Final /, ''));
      const eps = readNum(config, 'rms_norm_eps', 'layer_norm_epsilon', 'layer_norm_eps');
      push('Epsilon', eps !== undefined ? eps.toExponential() : undefined);
      break;
    }
    case 'head':
      push('Output logits', vocab !== undefined ? humanCount(vocab) : undefined);
      push('Tied to embedding', config.tie_word_embeddings === true ? 'yes' : 'no');
      break;
  }
  return rows;
}
