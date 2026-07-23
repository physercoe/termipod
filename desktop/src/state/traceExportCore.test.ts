/// Pure-logic tests for tracer Tier 2 (plan §5): JSON extraction from the torch.export
/// helper, the ExportGraph parse, and remote-command assembly. The export itself needs
/// a torch venue (device-test); the generic IPC is covered by trace.test.ts. Run:
/// `node --test src/state/traceExportCore.test.ts`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  TORCH_EXPORT_HELPER,
  TORCH_PROBE,
  extractExportJson,
  parseExportGraph,
  remoteExportCommand,
  remoteTorchProbe,
} from './traceExportCore.ts';

const OUT = [
  'W0101 some torch warning',
  '===EXPORT-START===',
  JSON.stringify({
    nodes: [
      { id: 'x', op: 'placeholder', target: 'placeholder', namespace: '', inputs: [], shape: [1, 4], dtype: 'torch.float32' },
      { id: 'linear', op: 'call_function', target: 'aten.addmm.default', namespace: 'blocks/0', inputs: ['x'], shape: [1, 8], dtype: 'torch.float32' },
    ],
  }),
  '===EXPORT-END===',
].join('\n');

test('TORCH_EXPORT_HELPER is ASCII, self-delimiting, torch.export-based', () => {
  // eslint-disable-next-line no-control-regex
  assert.ok(/^[\x00-\x7F]*$/.test(TORCH_EXPORT_HELPER));
  assert.ok(TORCH_EXPORT_HELPER.includes('===EXPORT-START==='));
  assert.ok(TORCH_EXPORT_HELPER.includes('torch.export.export'));
  assert.ok(TORCH_EXPORT_HELPER.includes('nn_module_stack'));
  assert.ok(TORCH_EXPORT_HELPER.includes('device("meta")'));
});

test('TORCH_PROBE imports torch and reports the version + export capability', () => {
  assert.ok(TORCH_PROBE.includes('import torch'));
  assert.ok(TORCH_PROBE.includes('OK torch'));
});

test('extractExportJson / parseExportGraph: pull + validate out of warning noise', () => {
  assert.equal(extractExportJson('no sentinels'), null);
  const g = parseExportGraph(OUT);
  assert.ok(g);
  assert.equal(g.nodes.length, 2);
  assert.equal(g.nodes[1].target, 'aten.addmm.default');
  assert.deepEqual(g.nodes[1].inputs, ['x']);
  assert.deepEqual(g.nodes[0].shape, [1, 4]);
});

test('parseExportGraph: malformed / sentinel-less input returns null', () => {
  assert.equal(parseExportGraph('Traceback ...'), null);
  assert.equal(parseExportGraph('===EXPORT-START===\n{bad}\n===EXPORT-END==='), null);
});

test('remoteExportCommand: base64 helper + TRACE_* env + interpreter, all quoted', () => {
  const cmd = remoteExportCommand('Model(d=8)', '1, 3, 224, 224', 'model.py', 'conda run -n rl python', '/repo', 'print(1)');
  assert.match(cmd, /^cd '\/repo' && /);
  assert.ok(cmd.includes("TRACE_ENTRY='Model(d=8)'"));
  assert.ok(cmd.includes("TRACE_INPUT='1, 3, 224, 224'"));
  assert.ok(cmd.includes("TRACE_FILE='model.py'"));
  assert.ok(cmd.trimEnd().endsWith('conda run -n rl python'));
});

test('remoteTorchProbe: decodes the torch probe, ends in the interpreter', () => {
  const cmd = remoteTorchProbe('python3');
  const b64 = /printf %s '([A-Za-z0-9+/=]+)'/.exec(cmd)?.[1] ?? '';
  assert.match(Buffer.from(b64, 'base64').toString('utf8'), /import torch/);
  assert.ok(cmd.trimEnd().endsWith('python3'));
});
