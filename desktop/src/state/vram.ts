/// VRAM estimator for the Inspect (J3) model viewer (plan §4b, W4b wedge). Pure
/// arithmetic — "will this checkpoint fit on this host?" — from the parsed
/// checkpoint (total params) plus the architecture dims the arch card already
/// classified (layers / hidden / heads / KV heads, and the **MLA** latent dims,
/// where the KV cache is compressed). No Python, no deps.
///
/// This is an **approximation** (the plan's provenance discipline): weights are
/// exact from the parsed param count × the serving dtype, but the KV-cache and
/// activation terms are the textbook formulas — real runtimes add framework
/// overhead, paged-attention block padding, and fragmentation on top. The card
/// labels itself approximate; it answers order-of-magnitude, not to-the-MB.
import type { ArchCard, ArchTemplate } from './checkpoint';

export type ServingDtype = 'fp32' | 'bf16' | 'fp16' | 'fp8' | 'int8' | 'int4';

/// Bytes per element for each serving precision (int4 is 0.5 = 4-bit packed).
export const DTYPE_BYTES: Record<ServingDtype, number> = {
  fp32: 4,
  bf16: 2,
  fp16: 2,
  fp8: 1,
  int8: 1,
  int4: 0.5,
};

export interface VramInputs {
  totalParams: number;
  /// Bytes per weight at the chosen serving precision.
  weightBytes: number;
  layers?: number;
  hidden?: number;
  heads?: number;
  kvHeads?: number;
  headDim?: number;
  /// MLA (DeepSeek-family) compressed-KV dims; when present the KV cache stores a
  /// single latent per token/layer (`kv_lora_rank` + the decoupled rope key
  /// `qk_rope_head_dim`), not per-head K and V — a large reduction.
  kvLoraRank?: number;
  qkRopeHeadDim?: number;
  isMla: boolean;
}

export interface VramRuntime {
  batch: number;
  context: number;
  /// Bytes per element held in the KV cache (usually 2 — fp16/bf16).
  kvBytes: number;
}

export interface VramEstimate {
  weightsBytes: number;
  kvBytes: number;
  activationBytes: number;
  totalBytes: number;
  /// True when we had enough dims (layers + attention or MLA rank) to size the
  /// KV cache; false → only the weights term is trustworthy.
  kvComputable: boolean;
}

function pos(n: number | undefined): n is number {
  return typeof n === 'number' && Number.isFinite(n) && n > 0;
}

/// Estimate inference-time VRAM for a batch/context point.
export function estimateVram(inp: VramInputs, rt: VramRuntime): VramEstimate {
  const weightsBytes = inp.totalParams * inp.weightBytes;

  let kvBytes = 0;
  let kvComputable = false;
  if (pos(inp.layers)) {
    if (inp.isMla) {
      // MLA stores one compressed latent per token per layer (no ×2 for separate
      // K/V). Without the latent rank we CANNOT size it — do not fall back to the
      // dense formula, which would massively overestimate (MLA's whole point is
      // KV compression); leave it non-computable and honest.
      if (pos(inp.kvLoraRank)) {
        const latent = inp.kvLoraRank + (pos(inp.qkRopeHeadDim) ? inp.qkRopeHeadDim : 0);
        kvBytes = inp.layers * rt.context * rt.batch * latent * rt.kvBytes;
        kvComputable = true;
      }
    } else if (pos(inp.hidden) && pos(inp.heads)) {
      const headDim = pos(inp.headDim) ? inp.headDim : inp.hidden / inp.heads;
      const kvH = pos(inp.kvHeads) ? inp.kvHeads : inp.heads;
      // K and V, per layer, per token: 2 × kvHeads × headDim.
      kvBytes = 2 * inp.layers * rt.context * rt.batch * kvH * headDim * rt.kvBytes;
      kvComputable = true;
    }
  }

  // Rough transient activation working set — a couple of live hidden-state
  // buffers (layers run sequentially and free, so this does not scale with depth).
  const activationBytes = pos(inp.hidden) ? 2 * rt.batch * rt.context * inp.hidden * rt.kvBytes : 0;

  return {
    weightsBytes,
    kvBytes,
    activationBytes,
    totalBytes: weightsBytes + kvBytes + activationBytes,
    kvComputable,
  };
}

function readNum(src: Record<string, unknown> | null | undefined, ...keys: string[]): number | undefined {
  if (!src) return undefined;
  for (const k of keys) {
    const v = src[k];
    if (typeof v === 'number' && Number.isFinite(v)) return v;
  }
  return undefined;
}

/// Assemble `VramInputs` from the parsed checkpoint + the classified card + the
/// raw HF `config.json` (safetensors/onnx sidecar) and/or gguf metadata. The card
/// already carries the common dims; config/metadata fill the extras (explicit
/// `head_dim`, and the MLA latent ranks) the card does not surface.
export function deriveVramInputs(opts: {
  totalParams: number;
  weightBytes: number;
  template: ArchTemplate;
  card: ArchCard | null;
  config?: Record<string, unknown> | null;
  metadata?: Record<string, string | number>;
}): VramInputs {
  const { card, config } = opts;
  const md = opts.metadata;
  const arch = md && typeof md['general.architecture'] === 'string' ? (md['general.architecture'] as string) : '';
  const gguf = (suffix: string): number | undefined => (md ? readNum(md as Record<string, unknown>, `${arch}.${suffix}`) : undefined);

  const isMla = opts.template === 'mla' || opts.template === 'mla-moe';
  return {
    totalParams: opts.totalParams,
    weightBytes: opts.weightBytes,
    layers: card?.layers ?? readNum(config, 'num_hidden_layers', 'n_layer') ?? gguf('block_count'),
    hidden: card?.hidden ?? readNum(config, 'hidden_size', 'n_embd') ?? gguf('embedding_length'),
    heads: card?.heads ?? readNum(config, 'num_attention_heads') ?? gguf('attention.head_count'),
    kvHeads: card?.kvHeads ?? readNum(config, 'num_key_value_heads') ?? gguf('attention.head_count_kv'),
    headDim: readNum(config, 'head_dim') ?? gguf('attention.key_length'),
    kvLoraRank: readNum(config, 'kv_lora_rank') ?? gguf('attention.kv_lora_rank'),
    qkRopeHeadDim: readNum(config, 'qk_rope_head_dim') ?? gguf('attention.qk_rope_head_dim'),
    isMla,
  };
}

// dtype-histogram label -> a serving dtype default (the checkpoint's own precision
// is the natural first guess; the user can override in the card).
export function defaultServingDtype(hist: Record<string, number>): ServingDtype {
  let best = '';
  let bestParams = -1;
  for (const [label, params] of Object.entries(hist)) {
    if (params > bestParams) {
      bestParams = params;
      best = label;
    }
  }
  const l = best.toLowerCase();
  if (l.includes('bf16') || l.includes('bfloat16')) return 'bf16';
  if (l.includes('f16') || l.includes('float16') || l === 'half') return 'fp16';
  if (l.includes('f32') || l.includes('float32') || l.includes('f64') || l.includes('float64')) return 'fp32';
  if (l.includes('f8') || l.includes('float8')) return 'fp8';
  if (l.includes('q4') || l.includes('int4') || l.includes('iq4')) return 'int4';
  if (l.includes('q8') || l.includes('int8')) return 'int8';
  return 'bf16';
}
