/// Main-process checkpoint inspection for the Inspect (J3) model viewer (plan §5,
/// W4 core). Pure core — no `electron` import — so it runs under `node --test`
/// (the `logindex.ts` / `download.ts` precedent); the IPC handler wrapper lives
/// in `checkpointfile.ts`.
///
/// **Never `localfs_read`.** A `.safetensors` can be tens of GB; reading it whole
/// over IPC would OOM the renderer (the plan's §5 explicit ban). We fd-read only
/// the header region (safetensors) or let `@huggingface/gguf` seek the header via
/// a FileBlob (gguf) — tensor *bytes* never leave disk. The renderer receives a
/// small JSON summary: format, params, a dtype histogram, format metadata, and a
/// per-tensor list (name / dtype / shape / params).
import { open, stat } from 'node:fs/promises';
import { gguf, GGMLQuantizationType } from '@huggingface/gguf';

export interface TensorInfo {
  name: string;
  dtype: string;
  shape: number[];
  params: number;
}

export interface CheckpointInfo {
  format: 'safetensors' | 'gguf';
  path: string;
  fileSize: number;
  totalParams: number;
  tensorCount: number;
  tensors: TensorInfo[];
  /// Format-level metadata, scalars only (safetensors `__metadata__`, gguf KV) —
  /// array-valued gguf keys (e.g. the 100k-entry tokenizer vocab) are dropped so
  /// the IPC payload stays small.
  metadata: Record<string, string | number>;
  /// dtype -> total parameter count carried in that dtype (the summary strip's
  /// precision/quant distribution).
  dtypeHistogram: Record<string, number>;
  /// Set when the tensor list was capped (pathological checkpoints).
  truncatedTensors?: number;
}

// A safetensors header JSON is bounded in practice (a few MB); refuse a length
// that is absurd or larger than the file (a corrupt / non-safetensors file).
const SAFET_MAX_HEADER = 200 * 1024 * 1024;
// Guard the per-tensor list against a pathological file; real checkpoints are
// well under this (a 61-layer model is a few thousand tensors).
const MAX_TENSORS = 200_000;

function product(shape: number[]): number {
  let p = 1;
  for (const d of shape) p *= d;
  return p;
}

function histogram(tensors: TensorInfo[]): Record<string, number> {
  const h: Record<string, number> = {};
  for (const t of tensors) h[t.dtype] = (h[t.dtype] ?? 0) + t.params;
  return h;
}

/// Parse a `.safetensors` header: 8-byte LE u64 header length, then that many
/// bytes of UTF-8 JSON `{ name: { dtype, shape, data_offsets }, __metadata__ }`.
/// Only the header bytes are read.
export async function parseSafetensors(path: string, fileSize: number): Promise<CheckpointInfo> {
  const fh = await open(path, 'r');
  try {
    const lenBuf = Buffer.alloc(8);
    const { bytesRead } = await fh.read(lenBuf, 0, 8, 0);
    if (bytesRead < 8) throw new Error('not a safetensors file (truncated header length)');
    const headerLen = Number(lenBuf.readBigUInt64LE(0));
    if (headerLen <= 0 || headerLen > SAFET_MAX_HEADER || 8 + headerLen > fileSize)
      throw new Error('invalid safetensors header length (corrupt or not a safetensors file)');
    const jsonBuf = Buffer.alloc(headerLen);
    await fh.read(jsonBuf, 0, headerLen, 8);
    let header: Record<string, unknown>;
    try {
      header = JSON.parse(jsonBuf.toString('utf8')) as Record<string, unknown>;
    } catch {
      throw new Error('safetensors header is not valid JSON (truncated?)');
    }
    const tensors: TensorInfo[] = [];
    const metadata: Record<string, string | number> = {};
    let totalParams = 0;
    let truncated = 0;
    for (const [name, raw] of Object.entries(header)) {
      if (name === '__metadata__') {
        if (raw !== null && typeof raw === 'object') {
          for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
            if (typeof v === 'string' || typeof v === 'number') metadata[k] = v;
          }
        }
        continue;
      }
      if (tensors.length >= MAX_TENSORS) {
        truncated += 1;
        continue;
      }
      const e = raw as { dtype?: unknown; shape?: unknown };
      const dtype = typeof e.dtype === 'string' ? e.dtype : 'unknown';
      const shape = Array.isArray(e.shape) ? e.shape.map((n) => Number(n)) : [];
      const params = product(shape);
      totalParams += params;
      tensors.push({ name, dtype, shape, params });
    }
    return {
      format: 'safetensors',
      path,
      fileSize,
      totalParams,
      tensorCount: tensors.length + truncated,
      tensors,
      metadata,
      dtypeHistogram: histogram(tensors),
      ...(truncated > 0 ? { truncatedTensors: truncated } : {}),
    };
  } finally {
    await fh.close();
  }
}

/// Parse a `.gguf` header via `@huggingface/gguf` (reads the header region only).
export async function parseGguf(path: string, fileSize: number): Promise<CheckpointInfo> {
  const out = await gguf(path, { allowLocalFile: true, computeParametersCount: true });
  const tensors: TensorInfo[] = [];
  let truncated = 0;
  for (const ti of out.tensorInfos) {
    if (tensors.length >= MAX_TENSORS) {
      truncated += 1;
      continue;
    }
    const shape = ti.shape.map((n) => Number(n));
    tensors.push({
      name: ti.name,
      dtype: GGMLQuantizationType[ti.dtype] ?? `type${ti.dtype}`,
      shape,
      params: product(shape),
    });
  }
  // Keep scalar KV only — arrays (tokenizer vocab/merges) would bloat the payload.
  const metadata: Record<string, string | number> = {};
  for (const [k, v] of Object.entries(out.metadata)) {
    if (typeof v === 'string' || typeof v === 'number') metadata[k] = v;
    else if (typeof v === 'bigint') metadata[k] = Number(v);
    else if (typeof v === 'boolean') metadata[k] = String(v);
  }
  const totalParams = typeof out.parameterCount === 'number' ? out.parameterCount : tensors.reduce((a, t) => a + t.params, 0);
  return {
    format: 'gguf',
    path,
    fileSize,
    totalParams,
    tensorCount: tensors.length + truncated,
    tensors,
    metadata,
    dtypeHistogram: histogram(tensors),
    ...(truncated > 0 ? { truncatedTensors: truncated } : {}),
  };
}

/// Inspect a checkpoint by path, dispatching on extension. safetensors + gguf ship
/// in W4 core; ONNX (needs a build-time protobuf schema) is a later slice.
export async function inspectCheckpoint(path: string): Promise<CheckpointInfo> {
  const st = await stat(path);
  const ext = path.toLowerCase().split('.').pop() ?? '';
  if (ext === 'safetensors') return parseSafetensors(path, st.size);
  if (ext === 'gguf') return parseGguf(path, st.size);
  if (ext === 'onnx') throw new Error('ONNX checkpoint inspection ships in a later W4 slice.');
  throw new Error(`unsupported checkpoint format: .${ext}`);
}
