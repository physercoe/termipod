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

/// Build the schematic from the classifier's card + the raw config. Returns null
/// when the config lacks the two facts a stack needs (a hidden size and a
/// positive layer count) — the caller then keeps the plain params card.
export function buildArchSchematic(card: ArchCard, config: Record<string, unknown>): ArchSchematic | null {
  const layers = card.layers ?? readNum(config, 'num_hidden_layers', 'n_layer', 'n_layers', 'num_layers') ?? 0;
  const hidden = card.hidden ?? readNum(config, 'hidden_size', 'n_embd', 'd_model', 'n_embed');
  if (hidden === undefined || layers <= 0) return null;

  const isMla = card.template === 'mla' || card.template === 'mla-moe';
  const isMoe = card.template === 'moe' || card.template === 'mla-moe';
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

  return { nodes, residuals, layers };
}
