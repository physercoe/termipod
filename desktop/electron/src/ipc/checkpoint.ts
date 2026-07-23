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
import { open, readFile, stat } from 'node:fs/promises';
import { gguf, GGMLQuantizationType } from '@huggingface/gguf';
import protobuf from 'protobufjs';

export interface TensorInfo {
  name: string;
  dtype: string;
  shape: number[];
  params: number;
}

export interface CheckpointInfo {
  format: 'safetensors' | 'gguf' | 'onnx';
  path: string;
  fileSize: number;
  totalParams: number;
  tensorCount: number;
  tensors: TensorInfo[];
  /// Format-level metadata, scalars only (safetensors `__metadata__`, gguf KV,
  /// ONNX producer/opset/graph stats) — array-valued gguf keys (e.g. the
  /// 100k-entry tokenizer vocab) are dropped so the IPC payload stays small.
  metadata: Record<string, string | number>;
  /// dtype -> total parameter count carried in that dtype (the summary strip's
  /// precision/quant distribution).
  dtypeHistogram: Record<string, number>;
  /// ONNX only: op_type -> node count (the graph's operator mix). Absent for
  /// weight-only checkpoints, which have no operator graph.
  ops?: Record<string, number>;
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

// ── ONNX ───────────────────────────────────────────────────────────────────────
//
// ONNX is a protobuf `ModelProto`. We decode it with a **minimal** vendored schema
// that deliberately OMITS `raw_data` (TensorProto field 9) and the typed `*_data`
// bulk fields, so protobufjs *skips* the embedded weight bytes rather than
// materializing them (verified: a 2 MiB `raw_data` decodes with `has rawData? false`).
// Field numbers are pinned to onnx.in.proto (verified 2026-07-23) — a wrong number
// silently misreads, so they must not drift. We parse the graph + initializer
// metadata only; tensor *bytes* never enter the payload.
const ONNX_PROTO = `
syntax = "proto3";
package onnx;
message StringStringEntryProto { string key = 1; string value = 2; }
message OperatorSetIdProto { string domain = 1; int64 version = 2; }
message ValueInfoProto { string name = 1; }
message TensorProto {
  repeated int64 dims = 1;
  int32 data_type = 2;
  string name = 8;
  int32 data_location = 14;
}
message NodeProto {
  repeated string input = 1;
  repeated string output = 2;
  string name = 3;
  string op_type = 4;
  string domain = 7;
}
message GraphProto {
  repeated NodeProto node = 1;
  string name = 2;
  repeated TensorProto initializer = 5;
  repeated ValueInfoProto input = 11;
  repeated ValueInfoProto output = 12;
}
message ModelProto {
  int64 ir_version = 1;
  string producer_name = 2;
  string producer_version = 3;
  GraphProto graph = 7;
  repeated OperatorSetIdProto opset_import = 8;
  repeated StringStringEntryProto metadata_props = 14;
}
`;

// Embedded-weights ONNX is loaded whole to decode; big models keep weights in
// external data files and stay small, so cap at 256 MiB with a typed error.
const ONNX_MAX = 256 * 1024 * 1024;

// TensorProto.DataType (onnx.in.proto) -> readable label.
const ONNX_DTYPE: Record<number, string> = {
  0: 'undefined', 1: 'float32', 2: 'uint8', 3: 'int8', 4: 'uint16', 5: 'int16',
  6: 'int32', 7: 'int64', 8: 'string', 9: 'bool', 10: 'float16', 11: 'float64',
  12: 'uint32', 13: 'uint64', 14: 'complex64', 15: 'complex128', 16: 'bfloat16',
  17: 'float8e4m3fn', 18: 'float8e4m3fnuz', 19: 'float8e5m2', 20: 'float8e5m2fnuz',
  21: 'uint4', 22: 'int4', 23: 'float4e2m1', 24: 'float8e8m0', 25: 'uint2', 26: 'int2',
};

let onnxModelType: protobuf.Type | null = null;
function modelProtoType(): protobuf.Type {
  if (onnxModelType === null) onnxModelType = protobuf.parse(ONNX_PROTO).root.lookupType('onnx.ModelProto');
  return onnxModelType;
}

interface OnnxInit { dims?: unknown; dataType?: unknown; name?: unknown }
interface OnnxNode { opType?: unknown }
interface OnnxKV { key?: unknown; value?: unknown }
interface OnnxOpset { domain?: unknown; version?: unknown }

/// Parse an `.onnx` graph: initializer tensors (the weights, metadata only) plus
/// the operator mix and producer/opset stats. The weight bytes are skipped by the
/// minimal schema above — but the whole file is still read to decode, so a large
/// embedded-weights model is refused (see `ONNX_MAX`).
export async function parseOnnx(path: string, fileSize: number): Promise<CheckpointInfo> {
  if (fileSize > ONNX_MAX)
    throw new Error(
      `ONNX file too large to inspect (${Math.round(fileSize / 1024 / 1024)} MiB); models with embedded weights over 256 MiB are unsupported — re-export with external data files.`,
    );
  const buf = await readFile(path);
  const Model = modelProtoType();
  let obj: Record<string, unknown>;
  try {
    obj = Model.toObject(Model.decode(buf), { longs: Number, enums: Number, defaults: false }) as Record<string, unknown>;
  } catch {
    throw new Error('not a valid ONNX file (protobuf decode failed)');
  }
  const graph = (obj.graph ?? {}) as Record<string, unknown>;
  const inits = Array.isArray(graph.initializer) ? (graph.initializer as OnnxInit[]) : [];
  const tensors: TensorInfo[] = [];
  let totalParams = 0;
  let truncated = 0;
  for (const it of inits) {
    if (tensors.length >= MAX_TENSORS) {
      truncated += 1;
      continue;
    }
    const dims = Array.isArray(it.dims) ? it.dims.map((n) => Number(n)) : [];
    const params = product(dims);
    totalParams += params;
    const dt = typeof it.dataType === 'number' ? it.dataType : 0;
    tensors.push({
      name: typeof it.name === 'string' && it.name ? it.name : '(unnamed)',
      dtype: ONNX_DTYPE[dt] ?? `dt${dt}`,
      shape: dims,
      params,
    });
  }
  const nodes = Array.isArray(graph.node) ? (graph.node as OnnxNode[]) : [];
  const ops: Record<string, number> = {};
  for (const n of nodes) {
    const op = typeof n.opType === 'string' && n.opType ? n.opType : '(unknown)';
    ops[op] = (ops[op] ?? 0) + 1;
  }
  const metadata: Record<string, string | number> = {};
  const producer = typeof obj.producerName === 'string' ? obj.producerName : '';
  const pver = typeof obj.producerVersion === 'string' ? obj.producerVersion : '';
  if (producer) metadata.producer = pver ? `${producer} ${pver}` : producer;
  if (typeof obj.irVersion === 'number') metadata.ir_version = obj.irVersion;
  const opset = Array.isArray(obj.opsetImport)
    ? (obj.opsetImport as OnnxOpset[])
        .map((o) => `${typeof o.domain === 'string' && o.domain ? `${o.domain}:` : ''}${Number(o.version ?? 0)}`)
        .join(', ')
    : '';
  if (opset) metadata.opset = opset;
  if (typeof graph.name === 'string' && graph.name) metadata.graph = graph.name;
  metadata.nodes = nodes.length;
  const gi = Array.isArray(graph.input) ? graph.input.length : 0;
  const go = Array.isArray(graph.output) ? graph.output.length : 0;
  if (gi) metadata.inputs = gi;
  if (go) metadata.outputs = go;
  if (Array.isArray(obj.metadataProps))
    for (const kv of obj.metadataProps as OnnxKV[])
      if (kv && typeof kv.key === 'string' && typeof kv.value === 'string') metadata[kv.key] = kv.value;
  return {
    format: 'onnx',
    path,
    fileSize,
    totalParams,
    tensorCount: tensors.length + truncated,
    tensors,
    metadata,
    dtypeHistogram: histogram(tensors),
    ...(Object.keys(ops).length > 0 ? { ops } : {}),
    ...(truncated > 0 ? { truncatedTensors: truncated } : {}),
  };
}

/// Inspect a checkpoint by path, dispatching on extension (safetensors, gguf, onnx).
export async function inspectCheckpoint(path: string): Promise<CheckpointInfo> {
  const st = await stat(path);
  const ext = path.toLowerCase().split('.').pop() ?? '';
  if (ext === 'safetensors') return parseSafetensors(path, st.size);
  if (ext === 'gguf') return parseGguf(path, st.size);
  if (ext === 'onnx') return parseOnnx(path, st.size);
  throw new Error(`unsupported checkpoint format: .${ext}`);
}
