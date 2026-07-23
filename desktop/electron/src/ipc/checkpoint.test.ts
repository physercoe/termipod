/// Tests for the checkpoint parsers (plan §5 W4 acceptance): a hand-written
/// safetensors header round-trips into tensors + params + dtype histogram +
/// metadata; a truncated header is a typed error; a hand-written GGUF parses via
/// `@huggingface/gguf` with shapes/dtype-labels/param-count mapped. Binary
/// fixtures are synthesized in-test (no torch / no committed blobs). Run with
/// `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import protobuf from 'protobufjs';
import { parseSafetensors, parseGguf, parseOnnx, inspectCheckpoint } from './checkpoint.ts';

async function withDir(fn: (dir: string) => Promise<void>): Promise<void> {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'ckpt-'));
  try {
    await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

// A safetensors file = u64 LE header length + JSON header + tensor bytes. Bytes
// are irrelevant to the header parser, so we pad with zeros to the declared size.
function makeSafetensors(header: Record<string, unknown>): Buffer {
  const json = Buffer.from(JSON.stringify(header), 'utf8');
  const len = Buffer.alloc(8);
  len.writeBigUInt64LE(BigInt(json.length));
  // Total data span = the max data_offsets end, so 8 + headerLen + dataLen holds.
  let dataLen = 0;
  for (const [k, v] of Object.entries(header)) {
    if (k === '__metadata__') continue;
    const off = (v as { data_offsets?: [number, number] }).data_offsets;
    if (off) dataLen = Math.max(dataLen, off[1]);
  }
  return Buffer.concat([len, json, Buffer.alloc(dataLen)]);
}

test('parseSafetensors: tensors, params, dtype histogram, metadata', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'tiny.safetensors');
    const header = {
      __metadata__: { format: 'pt', foo: 'bar' },
      'model.layers.0.attn.weight': { dtype: 'F16', shape: [4, 4], data_offsets: [0, 32] },
      'model.layers.1.attn.weight': { dtype: 'F16', shape: [4, 4], data_offsets: [32, 64] },
      'lm_head.weight': { dtype: 'F32', shape: [8, 4], data_offsets: [64, 192] },
    };
    await writeFile(p, makeSafetensors(header));
    const info = await parseSafetensors(p, (await import('node:fs')).statSync(p).size);
    assert.equal(info.format, 'safetensors');
    assert.equal(info.tensorCount, 3);
    assert.equal(info.totalParams, 16 + 16 + 32);
    assert.deepEqual(info.dtypeHistogram, { F16: 32, F32: 32 });
    assert.equal(info.metadata.format, 'pt');
    const lm = info.tensors.find((t) => t.name === 'lm_head.weight');
    assert.deepEqual(lm?.shape, [8, 4]);
    assert.equal(lm?.params, 32);
  });
});

test('parseSafetensors: a truncated header length is a typed error', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'bad.safetensors');
    await writeFile(p, Buffer.from([1, 2, 3])); // < 8 bytes
    await assert.rejects(() => parseSafetensors(p, 3), /truncated header length/);
  });
});

test('parseSafetensors: a header longer than the file is refused', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'liar.safetensors');
    const len = Buffer.alloc(8);
    len.writeBigUInt64LE(BigInt(1_000_000)); // claims a 1MB header in a 12-byte file
    await writeFile(p, Buffer.concat([len, Buffer.from('{}')]));
    await assert.rejects(() => parseSafetensors(p, 10), /invalid safetensors header length/);
  });
});

test('inspectCheckpoint: an unknown extension is a typed error', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'weights.bin');
    await writeFile(p, Buffer.alloc(16));
    await assert.rejects(() => inspectCheckpoint(p), /unsupported checkpoint format/);
  });
});

// Minimal GGUF v3 writer (matches the probe that verified the @huggingface/gguf API).
function u32(n: number): Buffer {
  const b = Buffer.alloc(4);
  b.writeUInt32LE(n);
  return b;
}
function u64(n: number): Buffer {
  const b = Buffer.alloc(8);
  b.writeBigUInt64LE(BigInt(n));
  return b;
}
function gstr(s: string): Buffer {
  const t = Buffer.from(s, 'utf8');
  return Buffer.concat([u64(t.length), t]);
}
function makeGguf(): Buffer {
  const parts = [
    Buffer.from('GGUF', 'ascii'),
    u32(3), // version
    u64(1), // tensor_count
    u64(2), // metadata_kv_count
    gstr('general.architecture'), u32(8), gstr('llama'), // STRING = 8
    gstr('llama.block_count'), u32(4), u32(4), // UINT32 = 4
    gstr('token_embd.weight'), u32(2), u64(8), u64(4), u32(0), u64(0), // F32 tensor [8,4]
  ];
  const head = Buffer.concat(parts);
  const pad = (32 - (head.length % 32)) % 32;
  return Buffer.concat([head, Buffer.alloc(pad), Buffer.alloc(8 * 4 * 4)]);
}

test('parseGguf: maps tensor shapes, dtype labels, params and scalar metadata', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'tiny.gguf');
    await writeFile(p, makeGguf());
    const info = await parseGguf(p, (await import('node:fs')).statSync(p).size);
    assert.equal(info.format, 'gguf');
    assert.equal(info.tensorCount, 1);
    assert.equal(info.totalParams, 32);
    assert.equal(info.metadata['general.architecture'], 'llama');
    assert.equal(info.metadata['llama.block_count'], 4);
    assert.equal(info.tensors[0].dtype, 'F32');
    assert.deepEqual(info.tensors[0].shape, [8, 4]);
    assert.deepEqual(info.dtypeHistogram, { F32: 32 });
  });
});

// Encoder proto INCLUDING raw_data (field 9) — the real parser's minimal schema
// omits it, so a round-trip proves the weight bytes are skipped, not materialized.
const ONNX_ENC = `
syntax = "proto3";
package onnx;
message StringStringEntryProto { string key = 1; string value = 2; }
message OperatorSetIdProto { string domain = 1; int64 version = 2; }
message TensorProto { repeated int64 dims = 1; int32 data_type = 2; string name = 8; bytes raw_data = 9; }
message ValueInfoProto { string name = 1; }
message NodeProto { repeated string input = 1; repeated string output = 2; string name = 3; string op_type = 4; }
message GraphProto {
  repeated NodeProto node = 1; string name = 2; repeated TensorProto initializer = 5;
  repeated ValueInfoProto input = 11; repeated ValueInfoProto output = 12;
}
message ModelProto {
  int64 ir_version = 1; string producer_name = 2; string producer_version = 3;
  GraphProto graph = 7; repeated OperatorSetIdProto opset_import = 8;
  repeated StringStringEntryProto metadata_props = 14;
}
`;
function makeOnnx(): Buffer {
  const Model = protobuf.parse(ONNX_ENC).root.lookupType('onnx.ModelProto');
  const msg = Model.create({
    irVersion: 9,
    producerName: 'pytorch',
    producerVersion: '2.4',
    opsetImport: [{ domain: '', version: 18 }],
    metadataProps: [{ key: 'framework', value: 'onnx' }],
    graph: {
      name: 'main_graph',
      input: [{ name: 'x' }],
      output: [{ name: 'y' }],
      node: [
        { opType: 'MatMul', name: 'mm0', input: ['x', 'model.layers.0.weight'], output: ['h0'] },
        { opType: 'MatMul', name: 'mm1', input: ['h0', 'model.layers.1.weight'], output: ['h1'] },
        { opType: 'Relu', name: 'act0', input: ['h1'], output: ['y'] },
      ],
      initializer: [
        // 2 KiB of raw_data — must be skipped, params come from dims.
        { name: 'model.layers.0.weight', dataType: 1, dims: [4, 4], rawData: Buffer.alloc(2048, 7) },
        { name: 'model.layers.1.weight', dataType: 10, dims: [4, 8] },
      ],
    },
  });
  return Buffer.from(Model.encode(msg).finish());
}

test('parseOnnx: initializers, op mix, opset/producer metadata; raw_data skipped', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'tiny.onnx');
    await writeFile(p, makeOnnx());
    const info = await parseOnnx(p, (await import('node:fs')).statSync(p).size);
    assert.equal(info.format, 'onnx');
    assert.equal(info.tensorCount, 2);
    assert.equal(info.totalParams, 16 + 32);
    assert.deepEqual(info.dtypeHistogram, { float32: 16, float16: 32 });
    assert.equal(info.tensors[0].dtype, 'float32');
    assert.deepEqual(info.tensors[0].shape, [4, 4]);
    assert.deepEqual(info.ops, { MatMul: 2, Relu: 1 });
    assert.equal(info.metadata.producer, 'pytorch 2.4');
    assert.equal(info.metadata.opset, '18');
    assert.equal(info.metadata.nodes, 3);
    assert.equal(info.metadata.graph, 'main_graph');
    assert.equal(info.metadata.framework, 'onnx');
    assert.equal(info.metadata.ir_version, 9);
  });
});

test('parseOnnx: retains the operator graph (nodes + tensor-name wiring)', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'tiny.onnx');
    await writeFile(p, makeOnnx());
    const info = await parseOnnx(p, (await import('node:fs')).statSync(p).size);
    assert.ok(info.graph);
    assert.equal(info.graph.nodes.length, 3);
    assert.deepEqual(info.graph.nodes[0], { name: 'mm0', opType: 'MatMul', inputs: ['x', 'model.layers.0.weight'], outputs: ['h0'] });
    assert.deepEqual(info.graph.nodes[2], { name: 'act0', opType: 'Relu', inputs: ['h1'], outputs: ['y'] });
    assert.deepEqual(info.graph.inputs, ['x']);
    assert.deepEqual(info.graph.outputs, ['y']);
    assert.equal(info.graph.truncatedNodes, undefined);
  });
});

test('inspectCheckpoint dispatches .onnx to the ONNX parser', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'model.onnx');
    await writeFile(p, makeOnnx());
    const info = await inspectCheckpoint(p);
    assert.equal(info.format, 'onnx');
    assert.equal(info.tensorCount, 2);
  });
});

test('parseOnnx: an oversized file is refused before reading', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'huge.onnx');
    await writeFile(p, makeOnnx()); // tiny on disk; the cap check uses the passed size
    await assert.rejects(() => parseOnnx(p, 300 * 1024 * 1024), /too large/);
  });
});

test('parseOnnx: a non-protobuf file is a typed error', async () => {
  await withDir(async (dir) => {
    const p = path.join(dir, 'bad.onnx');
    // tag 0x3a = field 7 (graph), wire-type 2 (length-delimited); length 5 with no
    // bytes to follow → the reader runs off the end → decode throws.
    await writeFile(p, Buffer.from([0x3a, 0x05]));
    await assert.rejects(() => parseOnnx(p, 2), /not a valid ONNX file/);
  });
});
