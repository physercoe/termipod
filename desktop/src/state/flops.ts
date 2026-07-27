/// FLOPS / throughput estimator for the Inspect (J3) model viewer — the compute
/// companion to the VRAM card. VRAM answers "does it fit?"; this answers "how
/// fast will it run, and how long will a training step take on this GPU?".
///
/// The compute model is the textbook forward-pass approximation: `2·N` FLOPs per
/// token for the linear (matmul) work, where N is the **active** parameter count
/// (multiply + add = 2 FLOPs), plus the causal-attention term that grows with
/// context (∝ s per decoded token, ∝ s² summed over a prefill) and dominates at
/// long context. Training multiplies the whole pass by 3 (forward + backward ≈
/// 3× forward — the well-known `C ≈ 6·N·D`).
///
/// Real throughput is `peak × MFU`, where MFU (Model FLOPs Utilization) is the
/// fraction of the GPU's peak actually reached — typically 0.3–0.5 for training
/// and lower for memory-bound single-token decode. Like the VRAM card this is an
/// **order-of-magnitude** estimate: it ignores communication/pipeline bubbles,
/// kernel-launch overhead, and the fact that decode is usually
/// memory-bandwidth-bound rather than compute-bound.
import type { VramMode } from './vram';

function pos(n: number | undefined): n is number {
  return typeof n === 'number' && Number.isFinite(n) && n > 0;
}

/// Compute precision the matmuls run in — picks which peak-throughput number of
/// the GPU applies. (Distinct from the VRAM serving dtype: a model can be stored
/// in one precision and computed in another.)
export type ComputePrecision = 'bf16' | 'fp8' | 'fp4';

export interface GpuSpec {
  id: string;
  label: string;
  /// Peak DENSE tensor-core throughput (TFLOP/s) by precision. Dense — not the
  /// 2:4-sparsity marketing doubles — since dense LLM matmuls are the norm. A
  /// missing entry means the precision isn't supported on that GPU (e.g. A100
  /// has no FP8/FP4, pre-Blackwell has no FP4).
  tflops: Partial<Record<ComputePrecision, number>>;
  /// On-board memory (GB) — shown alongside the VRAM estimate for a fit check.
  memGb: number;
}

/// Verified against NVIDIA datasheets. H100 and H200 share the GH100 compute die
/// → identical FLOP/s (H200 only adds HBM). Blackwell (B200) adds FP4. Values are
/// dense peak; real work lands at `peak × MFU`.
export const GPU_PRESETS: GpuSpec[] = [
  { id: 'a100', label: 'A100 80GB', tflops: { bf16: 312 }, memGb: 80 },
  { id: 'h100', label: 'H100 SXM', tflops: { bf16: 495, fp8: 989 }, memGb: 80 },
  { id: 'h200', label: 'H200 SXM', tflops: { bf16: 495, fp8: 989 }, memGb: 141 },
  { id: 'b200', label: 'B200', tflops: { bf16: 2250, fp8: 4500, fp4: 9000 }, memGb: 192 },
  { id: 'rtx4090', label: 'RTX 4090', tflops: { bf16: 165, fp8: 330 }, memGb: 24 },
];

/// Default Model FLOPs Utilization — a realistic mid-point (well-tuned large
/// training runs report ~0.35–0.5; decode is lower). The user can override.
export const DEFAULT_MFU = 0.4;

export interface FlopsInputs {
  /// Active params — the params that do work per token. Dense: total params.
  /// MoE: dense + top-k routed experts (see estimateParamsFromConfig activeOnly).
  activeParams: number;
  layers?: number;
  /// Query-side attention width = heads × headDim (≈ hidden for MHA; larger for
  /// MLA). Drives the causal-attention term; absent → linear-only estimate.
  attnDim?: number;
}

export interface FlopsRuntime {
  mode: VramMode; // 'inference' (prefill + decode) | 'training' (one fwd+bwd step)
  context: number; // sequence length s
  batch: number; // b (sequences per step)
  /// Effective device throughput (FLOP/s) = peak × numGpus × MFU. Precomputed by
  /// the caller (see effectiveDeviceFlops) so this module stays a pure formula.
  deviceFlops: number;
}

export interface FlopsEstimate {
  /// Linear matmul FLOPs per token: 2·N (inference forward) or 6·N (training step).
  linearFlopsPerToken: number;
  /// Attention FLOPs for one token at full context s: fwdMult · 4 · layers · s ·
  /// attnDim (0 when attnDim/layers unknown).
  attnFlopsPerToken: number;
  /// Tokens in one step = batch × context (a prefill, or one training step).
  stepTokens: number;
  /// Total FLOPs for the step — linear (∝ tokens) + causal attention (∝ s²).
  stepFlops: number;
  /// Wall-clock for the step at MFU (seconds). Infinity when deviceFlops is 0.
  stepSeconds: number;
  /// Sustained prefill/training throughput (tokens/s).
  tokensPerSecond: number;
  /// Inference only: incremental single-token decode latency at full context
  /// (ms/token). Optimistic — real decode is usually memory-bandwidth-bound.
  decodeMsPerToken?: number;
  /// True when the attention (∝ s²) term could be sized (had layers + attnDim);
  /// false → only the linear term is included and long-context cost is understated.
  attnComputable: boolean;
}

/// Peak dense TFLOP/s for a GPU at a precision, or undefined if unsupported.
export function peakTflops(gpu: GpuSpec, precision: ComputePrecision): number | undefined {
  return gpu.tflops[precision];
}

/// Effective device throughput (FLOP/s): peak (TFLOP/s) × device count × MFU.
export function effectiveDeviceFlops(peakTflopsValue: number, numGpus: number, mfu: number): number {
  return peakTflopsValue * 1e12 * Math.max(1, numGpus) * mfu;
}

/// Estimate compute for one step — a prefill of `context` tokens (inference) or a
/// single forward+backward training step over batch×context tokens.
export function estimateFlops(inp: FlopsInputs, rt: FlopsRuntime): FlopsEstimate {
  const training = rt.mode === 'training';
  const fwdMult = training ? 3 : 1; // fwd+bwd ≈ 3× forward
  const s = rt.context;
  const b = rt.batch;
  const n = inp.activeParams;

  const linearFlopsPerToken = 2 * n * fwdMult;
  const attnComputable = pos(inp.layers) && pos(inp.attnDim);
  const layers = inp.layers ?? 0;
  const attnDim = inp.attnDim ?? 0;

  // Per-token attention at full context s (used for decode latency + display):
  // QK^T (2·s·attnDim) + softmax·V (2·s·attnDim) per layer → 4·s·attnDim.
  const attnFlopsPerToken = attnComputable ? fwdMult * 4 * layers * s * attnDim : 0;

  const stepTokens = b * s;
  const linearStep = linearFlopsPerToken * stepTokens;
  // Causal attention summed over the sequence ≈ 2·attnDim·s² per layer (the ½ of
  // the per-token 4·attnDim·s comes from the causal mask), per sequence in the batch.
  const attnStep = attnComputable ? fwdMult * b * layers * 2 * attnDim * s * s : 0;
  const stepFlops = linearStep + attnStep;

  const stepSeconds = rt.deviceFlops > 0 ? stepFlops / rt.deviceFlops : Infinity;
  const tokensPerSecond = Number.isFinite(stepSeconds) && stepSeconds > 0 ? stepTokens / stepSeconds : 0;

  const est: FlopsEstimate = {
    linearFlopsPerToken,
    attnFlopsPerToken,
    stepTokens,
    stepFlops,
    stepSeconds,
    tokensPerSecond,
    attnComputable,
  };
  if (!training) {
    // Decoding one more token: 2·N linear + one token's attention over context s.
    const decodeFlops = 2 * n + attnFlopsPerToken;
    est.decodeMsPerToken = rt.deviceFlops > 0 ? (decodeFlops / rt.deviceFlops) * 1000 : Infinity;
  }
  return est;
}

/// Human FLOP count — e.g. 4.4e10 → "44 GFLOP", 1.3e15 → "1.3 PFLOP".
export function humanFlops(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  const units = ['FLOP', 'KFLOP', 'MFLOP', 'GFLOP', 'TFLOP', 'PFLOP', 'EFLOP'];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i += 1;
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/// Human duration from seconds — sub-ms → "µs", then ms / s / min / h / d.
export function humanDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—';
  if (seconds < 1e-3) return `${(seconds * 1e6).toFixed(0)} µs`;
  if (seconds < 1) return `${(seconds * 1e3).toFixed(0)} ms`;
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 2 : 1)} s`;
  if (seconds < 3600) return `${(seconds / 60).toFixed(1)} min`;
  if (seconds < 86400) return `${(seconds / 3600).toFixed(1)} h`;
  return `${(seconds / 86400).toFixed(1)} d`;
}
