/// The committed §7a device-test fixtures must actually parse (plan §7a: "the
/// unit tests for `checkpoint.ts`/`logfile.ts` reuse the binary fixtures
/// directly") — CI pins the same inputs a device tester opens by hand, so a
/// regenerated or corrupted fixture fails here, not on a device. Run with
/// `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { inspectCheckpoint } from './checkpoint.ts';
import { openIndex, lineCount, slice, search } from './logindex.ts';

const fix = (rel: string): string => fileURLToPath(new URL(`../../e2e/fixtures/inspect/${rel}`, import.meta.url));

test('fixture tiny.safetensors: layered namespacing, params, metadata', async () => {
  const info = await inspectCheckpoint(fix('model/tiny.safetensors'));
  assert.equal(info.format, 'safetensors');
  assert.equal(info.tensorCount, 21); // embed + 2×(4 attn + 3 mlp + 2 norm) + norm + head
  assert.ok(info.tensors.filter((t) => t.name.startsWith('model.layers.1.')).length === 9);
  assert.equal(info.totalParams, info.tensors.reduce((a, t) => a + t.params, 0));
  assert.equal(info.dtypeHistogram.F32, info.totalParams);
  assert.equal(info.metadata.format, 'pt');
});

test('fixture truncated.safetensors: typed error, never a crash', async () => {
  await assert.rejects(inspectCheckpoint(fix('model/truncated.safetensors')), /not valid JSON/);
});

test('fixture not-a-model.bin: unsupported format is a typed error', async () => {
  await assert.rejects(inspectCheckpoint(fix('model/not-a-model.bin')), /unsupported checkpoint format/);
});

test('fixture tiny.gguf: llama arch metadata + blk tensors parse', async () => {
  const info = await inspectCheckpoint(fix('model/tiny.gguf'));
  assert.equal(info.format, 'gguf');
  assert.equal(info.metadata['general.architecture'], 'llama');
  assert.equal(info.metadata['llama.attention.head_count_kv'], 2);
  assert.equal(info.tensorCount, 9);
  assert.ok(info.tensors.some((t) => t.name === 'blk.1.attn_q.weight' && t.params === 256));
});

test('fixture tiny.onnx: operator graph parses, raw_data skipped', async () => {
  const info = await inspectCheckpoint(fix('model/tiny.onnx'));
  assert.equal(info.format, 'onnx');
  assert.deepEqual(info.ops, { MatMul: 1, Add: 1, Relu: 1 });
  assert.equal(info.totalParams, 16 * 16 + 16); // w0 + b0 metadata, not bytes
  assert.deepEqual(info.graph?.inputs, ['input']);
  assert.equal(info.graph?.nodes.length, 3);
  assert.equal(info.metadata.purpose, 'inspect device-test fixture');
});

test('fixture train-small.log: index, slice and search line up', async () => {
  const idx = await openIndex(fix('log/train-small.log'));
  try {
    const total = lineCount(idx);
    assert.ok(total >= 200 && total <= 220, `unexpected line count ${total}`);
    const warn = await search(idx, 'WARN loss spike', 'i', 10);
    assert.equal(warn.hits.length, 1);
    const [line] = await slice(idx, warn.hits[0].line, 1);
    assert.match(line, /data shard 12/);
    assert.equal((await search(idx, 'Traceback', '', 10)).hits.length, 1);
    assert.equal((await slice(idx, 0, total)).length, total);
  } finally {
    await idx.fh.close();
  }
});
