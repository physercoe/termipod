/// Pure-logic tests for the tracer core (plan §5): DOT extraction from noisy
/// output, and the remote one-liner assembly (shell-quoting + base64 helper). The
/// trace itself needs a torch venue; the IPC plumbing is covered by
/// `electron/src/ipc/trace.test.ts`. Run locally: `node --test src/state/traceCore.test.ts`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { base64ShellCommand, extractDot, remoteTraceCommand, remoteProbeCommand, TORCHVIEW_HELPER, type TraceParams } from './traceCore.ts';

test('extractDot: pulls the DOT out of warning-polluted output', () => {
  const out = [
    'UserWarning: meta tensor; some op fell back',
    '===DOT-START===',
    'digraph {',
    '  a -> b',
    '}',
    '===DOT-END===',
    'done.',
  ].join('\n');
  assert.equal(extractDot(out), 'digraph {\n  a -> b\n}');
});

test('extractDot: returns null when the sentinels are absent (a plain error)', () => {
  assert.equal(extractDot('Traceback (most recent call last): ...'), null);
  assert.equal(extractDot('===DOT-START===\nno end'), null);
});

const P: TraceParams = {
  entry: 'Model(dim=512)',
  shape: '1, 3, 224, 224',
  depth: 4,
  command: 'conda run -n rl python',
  repoRoot: '/home/me/repo',
  filePath: 'model.py',
};

test('remoteTraceCommand: cd + base64-decode helper + env + interpreter, all quoted', () => {
  const cmd = remoteTraceCommand(P, 'print(1)');
  assert.match(cmd, /^cd '\/home\/me\/repo' && /);
  assert.ok(cmd.includes('base64 -d |'));
  assert.ok(cmd.includes("TRACE_ENTRY='Model(dim=512)'"));
  assert.ok(cmd.includes("TRACE_INPUT='1, 3, 224, 224'"));
  assert.ok(cmd.includes("TRACE_DEPTH='4'"));
  assert.ok(cmd.includes("TRACE_FILE='model.py'"));
  assert.ok(cmd.trimEnd().endsWith('conda run -n rl python'));
  const b64 = /printf %s '([A-Za-z0-9+/=]+)'/.exec(cmd)?.[1] ?? '';
  assert.equal(Buffer.from(b64, 'base64').toString('utf8'), 'print(1)');
});

test('remoteTraceCommand: a single-quote in the entry expr is escaped safely', () => {
  const cmd = remoteTraceCommand({ ...P, entry: "M(name='x')" }, 'print(1)');
  assert.ok(cmd.includes("TRACE_ENTRY='M(name='\\''x'\\'')'"));
});

test('remoteTraceCommand: no repoRoot omits the cd', () => {
  const cmd = remoteTraceCommand({ ...P, repoRoot: '' }, 'print(1)');
  assert.ok(!cmd.startsWith('cd '));
  assert.match(cmd, /^printf %s /);
});

test('remoteProbeCommand: decodes the probe helper', () => {
  const cmd = remoteProbeCommand('python3');
  const b64 = /printf %s '([A-Za-z0-9+/=]+)'/.exec(cmd)?.[1] ?? '';
  assert.match(Buffer.from(b64, 'base64').toString('utf8'), /import torch, torchview/);
  assert.ok(cmd.trimEnd().endsWith('python3'));
});

test('base64ShellCommand: generic assembly — cd, base64 helper, quoted env, command', () => {
  const cmd = base64ShellCommand('print(1)', { A: 'x', B: "y'z" }, 'python3', '/r');
  assert.equal(cmd, "cd '/r' && printf %s 'cHJpbnQoMSk=' | base64 -d | A='x' B='y'\\''z' python3");
});

test('base64ShellCommand: empty env and no cwd → bare pipe into the command', () => {
  assert.equal(base64ShellCommand('p', {}, 'python3'), "printf %s 'cA==' | base64 -d | python3");
});

test('TORCHVIEW_HELPER is ASCII (base64-safe with btoa) and self-delimiting', () => {
  // eslint-disable-next-line no-control-regex
  assert.ok(/^[\x00-\x7F]*$/.test(TORCHVIEW_HELPER));
  assert.ok(TORCHVIEW_HELPER.includes('===DOT-START==='));
  assert.ok(TORCHVIEW_HELPER.includes('device="meta"'));
});
