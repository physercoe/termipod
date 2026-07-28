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

export type ServingDtype = 'fp32' | 'bf16' | 'fp16' | 'fp8' | 'int8' | 'fp4' | 'int4';

/// Bytes per element for each serving precision (int4/fp4 are 0.5 = 4-bit packed).
export const DTYPE_BYTES: Record<ServingDtype, number> = {
  fp32: 4,
  bf16: 2,
  fp16: 2,
  fp8: 1,
  int8: 1,
  fp4: 0.5,
  int4: 0.5,
};

/// Inference (KV cache + a transient activation buffer) vs training (weights +
/// gradients + optimizer states + the full backward activation stash).
export type VramMode = 'inference' | 'training';

/// The optimizer sets the per-parameter state cost under the standard
/// mixed-precision recipe (an fp32 master weight copy + the optimizer moments):
///   - AdamW: master(4) + m(4) + v(4) = 12 B/param
///   - 8-bit Adam (bitsandbytes): master(4) + m(1) + v(1) = 6 B/param
///   - SGD w/ momentum: master(4) + momentum(4) = 8 B/param
export type Optimizer = 'adamw' | 'adam8bit' | 'sgd';
export const OPTIMIZER_STATE_BYTES: Record<Optimizer, number> = {
  adamw: 12,
  adam8bit: 6,
  sgd: 8,
};

export interface VramInputs {
  totalParams: number;
  /// Bytes per weight at the chosen serving precision.
  weightBytes: number;
  layers?: number;
  /// Layers that hold a growing per-token KV cache — full (softmax) attention
  /// layers. For a homogeneous stack this equals `layers` (leave undefined). For
  /// a hybrid linear-attention stack (Kimi K3/Linear, Qwen3-Next) only the
  /// periodic full-attention layers cache; the linear/recurrent layers keep an
  /// O(1) state, so the KV cache is sized by THIS count, not `layers`.
  fullAttnLayers?: number;
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
  /// Inference (default) or training. Training swaps the KV-cache term for the
  /// gradient + optimizer + full-backward-activation terms.
  mode?: VramMode;
  /// Training only — the optimizer whose per-param state cost applies (default
  /// AdamW). Ignored for inference.
  optimizer?: Optimizer;
  /// Training only — gradient (activation) checkpointing: recompute activations
  /// in the backward pass instead of stashing them, trading compute for a large
  /// activation-memory cut.
  gradCheckpoint?: boolean;
}

export interface VramEstimate {
  weightsBytes: number;
  /// Inference: the KV cache. Training: 0 (no decode-time cache).
  kvBytes: number;
  activationBytes: number;
  /// Training only (0 for inference): the gradient buffer and the optimizer
  /// state (fp32 master + moments).
  gradientBytes: number;
  optimizerBytes: number;
  totalBytes: number;
  /// True when we had enough dims (layers + attention or MLA rank) to size the
  /// KV cache — or, in training, the activation stash; false → only the
  /// weights/gradient/optimizer terms are trustworthy.
  kvComputable: boolean;
  mode: VramMode;
}

function pos(n: number | undefined): n is number {
  return typeof n === 'number' && Number.isFinite(n) && n > 0;
}

/// Training activation memory (bytes). The dominant, most variable training
/// term. Without recomputation we use the Megatron-LM per-layer formula
/// `s·b·h·(34 + 5·a·s/h)` (the constant already assumes 2-byte activations, i.e.
/// the bf16/fp16 compute the mixed-precision recipe uses); the `5·a·s/h` term is
/// the ∝s² attention-score stash. With gradient (activation) checkpointing only
/// each layer's input is kept (`2·s·b·h`), recomputed in the backward pass — the
/// standard memory/compute trade. `a` falls back to a hidden/128 head estimate.
function trainingActivationBytes(inp: VramInputs, rt: VramRuntime): number {
  if (!pos(inp.hidden) || !pos(inp.layers)) return 0;
  const s = rt.context;
  const b = rt.batch;
  const h = inp.hidden;
  const a = pos(inp.heads) ? inp.heads : Math.max(1, Math.round(h / 128));
  const perLayer = rt.gradCheckpoint === true ? 2 * s * b * h : s * b * h * (34 + (5 * a * s) / h);
  return inp.layers * perLayer;
}

/// Estimate VRAM for a batch/context point — inference (weights + KV cache +
/// transient activations) or training (weights + gradients + optimizer states +
/// the full backward activation stash).
export function estimateVram(inp: VramInputs, rt: VramRuntime): VramEstimate {
  const weightsBytes = inp.totalParams * inp.weightBytes;

  if (rt.mode === 'training') {
    // Gradients ride the compute precision (same bytes as the weights copy);
    // optimizer state is set by the optimizer (fp32 master + moments).
    const gradientBytes = inp.totalParams * inp.weightBytes;
    const optimizerBytes = inp.totalParams * OPTIMIZER_STATE_BYTES[rt.optimizer ?? 'adamw'];
    const activationBytes = trainingActivationBytes(inp, rt);
    return {
      weightsBytes,
      kvBytes: 0,
      activationBytes,
      gradientBytes,
      optimizerBytes,
      totalBytes: weightsBytes + gradientBytes + optimizerBytes + activationBytes,
      kvComputable: pos(inp.hidden) && pos(inp.layers),
      mode: 'training',
    };
  }

  // Only full-attention layers hold a growing KV cache; a hybrid stack's
  // linear/recurrent layers do not, so size the cache by `fullAttnLayers` when set.
  const cachingLayers = pos(inp.fullAttnLayers) ? inp.fullAttnLayers : inp.layers;
  let kvBytes = 0;
  let kvComputable = false;
  if (pos(cachingLayers)) {
    if (inp.isMla) {
      // MLA stores one compressed latent per token per layer (no ×2 for separate
      // K/V). Without the latent rank we CANNOT size it — do not fall back to the
      // dense formula, which would massively overestimate (MLA's whole point is
      // KV compression); leave it non-computable and honest.
      if (pos(inp.kvLoraRank)) {
        const latent = inp.kvLoraRank + (pos(inp.qkRopeHeadDim) ? inp.qkRopeHeadDim : 0);
        kvBytes = cachingLayers * rt.context * rt.batch * latent * rt.kvBytes;
        kvComputable = true;
      }
    } else if (pos(inp.hidden) && pos(inp.heads)) {
      const headDim = pos(inp.headDim) ? inp.headDim : inp.hidden / inp.heads;
      const kvH = pos(inp.kvHeads) ? inp.kvHeads : inp.heads;
      // K and V, per layer, per token: 2 × kvHeads × headDim.
      kvBytes = 2 * cachingLayers * rt.context * rt.batch * kvH * headDim * rt.kvBytes;
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
    gradientBytes: 0,
    optimizerBytes: 0,
    totalBytes: weightsBytes + kvBytes + activationBytes,
    kvComputable,
    mode: 'inference',
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

  // A hybrid linear-attention stack (`linear-hybrid`) whose full-attention layers
  // are MLA (Kimi K3/Linear carry `kv_lora_rank`) must use the compressed-latent
  // KV formula, not the dense one — else its cache is wildly overestimated.
  const isMla = opts.template === 'mla' || opts.template === 'mla-moe' || readNum(config, 'kv_lora_rank') !== undefined;
  const heads = card?.heads ?? readNum(config, 'num_attention_heads', 'n_head') ?? gguf('attention.head_count');
  const kvHeads = card?.kvHeads ?? readNum(config, 'num_key_value_heads', 'num_kv_heads', 'n_head_kv') ?? gguf('attention.head_count_kv');
  const headDim = readNum(config, 'head_dim') ?? gguf('attention.key_length');

  // MLA latent size. DeepSeek-V2/V3 name it `kv_lora_rank`; DeepSeek-V4 dropped
  // that key and instead expresses the compressed KV as a single KV head
  // (`num_key_value_heads: 1`, `head_dim: 512`). When the explicit rank is
  // absent, fall back to `kvHeads × headDim` so the (still-MLA) cache stays
  // sizeable instead of collapsing to the unhelpful "can't derive" hint. This is
  // an upper bound for sparse/compressed-attention variants (V4's sliding window
  // + per-layer compression cache even less) — safe for a "will it fit?" check.
  const kvLoraRank =
    readNum(config, 'kv_lora_rank') ??
    gguf('attention.kv_lora_rank') ??
    (isMla && pos(kvHeads) && pos(headDim) ? kvHeads * headDim : undefined);

  return {
    totalParams: opts.totalParams,
    weightBytes: opts.weightBytes,
    layers: card?.layers ?? readNum(config, 'num_hidden_layers', 'n_layer') ?? gguf('block_count'),
    // Hybrid linear-attention: only the full-attention layers cache (the
    // classifier counted them in `fullAttnLayers`).
    fullAttnLayers: card?.fullAttnLayers,
    hidden: card?.hidden ?? readNum(config, 'hidden_size', 'n_embd', 'd_model') ?? gguf('embedding_length'),
    heads,
    kvHeads,
    headDim,
    kvLoraRank,
    qkRopeHeadDim: readNum(config, 'qk_rope_head_dim') ?? gguf('attention.qk_rope_head_dim'),
    isMla,
  };
}

// ── KV-cache-per-token metric (arch card, archgraph plan §5 W1 / G6) ──────────

/// KV-cache size class, low→very-high — the Raschka gallery's per-token classing.
/// A recognisable at-a-glance figure: MLA and hybrid linear-attention models are
/// "low" (their whole selling point), wide dense-MHA stacks are "very-high".
export type KvCacheClass = 'low' | 'moderate' | 'high' | 'very-high';

// Thresholds in BYTES per token (whole model, all caching layers, 2-byte KV).
// Calibrated against real models: MLA + hybrid linear-attention (DeepSeek-V3
// ~69 KB, Kimi K3 ~28 KB, Qwen3-Next ~24 KB) → 'low'; small-KV-head GQA
// (Llama-3-8B ~128 KB, Qwen3-235B ~376 KB) → 'moderate'; wide GQA (MiniMax-M2
// ~496 KB, Gemma-3-27B ~992 KB) → 'high'; big dense MHA (>1 MB/token) →
// 'very-high'. The split is chosen so compressed-KV designs read 'low'.
const KV_CLASS_THRESHOLDS: Array<{ max: number; cls: KvCacheClass }> = [
  { max: 96 * 1024, cls: 'low' },
  { max: 384 * 1024, cls: 'moderate' },
  { max: 1024 * 1024, cls: 'high' },
];

export function classifyKvCache(bytesPerToken: number): KvCacheClass {
  for (const { max, cls } of KV_CLASS_THRESHOLDS) if (bytesPerToken < max) return cls;
  return 'very-high';
}

/// KV-cache bytes for ONE token across all caching layers (batch 1, 2-byte KV by
/// default) — the "per-token" figure the arch card shows. Reuses estimateVram's
/// exact KV term at context 1, so the card figure and the VRAM estimator can
/// never diverge. Returns null when the cache is not sizeable (MLA without a
/// disclosed latent rank), so the card can badge "unknown" honestly.
export function kvCacheBytesPerToken(inp: VramInputs, kvElemBytes = 2): number | null {
  const est = estimateVram(inp, { batch: 1, context: 1, kvBytes: kvElemBytes, mode: 'inference' });
  return est.kvComputable ? est.kvBytes : null;
}

/// Card-level convenience: KV bytes/token + its class straight from a classified
/// `ArchCard` + the raw config/metadata (the same inputs the VRAM card derives
/// from). Null when the cache can't be sized. Hybrid stacks size by their
/// full-attention layer count (carried on the card), so a linear-hybrid honestly
/// reports its small KV footprint.
export function deriveKvCacheClass(opts: {
  card: ArchCard | null;
  config?: Record<string, unknown> | null;
  metadata?: Record<string, string | number>;
}): { bytesPerToken: number; cls: KvCacheClass } | null {
  const inp = deriveVramInputs({
    totalParams: 0,
    weightBytes: 0,
    template: opts.card?.template ?? 'unknown',
    card: opts.card,
    config: opts.config,
    metadata: opts.metadata,
  });
  const b = kvCacheBytesPerToken(inp);
  return b === null ? null : { bytesPerToken: b, cls: classifyKvCache(b) };
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
