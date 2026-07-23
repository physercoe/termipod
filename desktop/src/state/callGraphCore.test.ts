/// Pure-logic tests for the call-graph core (plan §5 W4): the vendored code2flow
/// helper shape, and the remote one-liner assembly (shell-quoting + base64 helper +
/// env). The code2flow run itself needs a venue with the package installed; the
/// local IPC plumbing is the reused generic `trace_run` (covered by
/// `electron/src/ipc/trace.test.ts`). Run: `node --test src/state/callGraphCore.test.ts`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CODE2FLOW_HELPER,
  CODE2FLOW_PROBE,
  callGraphEnv,
  extractDot,
  remoteCallGraphCommand,
  remoteCallGraphProbe,
  type CallGraphParams,
} from './callGraphCore.ts';

const P: CallGraphParams = {
  targets: 'model.py\npkg/',
  lang: 'py',
  command: 'conda run -n rl python',
  repoRoot: '/home/me/repo',
};

test('CODE2FLOW_HELPER is ASCII (base64-safe with btoa) and self-delimiting', () => {
  // eslint-disable-next-line no-control-regex
  assert.ok(/^[\x00-\x7F]*$/.test(CODE2FLOW_HELPER));
  assert.ok(CODE2FLOW_HELPER.includes('===DOT-START==='));
  assert.ok(CODE2FLOW_HELPER.includes('===DOT-END==='));
  assert.ok(CODE2FLOW_HELPER.includes('from code2flow.engine import code2flow'));
  // Reads its inputs from the environment, never a command interpolation.
  assert.ok(CODE2FLOW_HELPER.includes('C2F_TARGETS'));
  assert.ok(CODE2FLOW_HELPER.includes('C2F_LANG'));
});

test('CODE2FLOW_PROBE imports code2flow and reports OK', () => {
  // eslint-disable-next-line no-control-regex
  assert.ok(/^[\x00-\x7F]*$/.test(CODE2FLOW_PROBE));
  assert.ok(CODE2FLOW_PROBE.includes('import code2flow'));
  assert.ok(CODE2FLOW_PROBE.includes('OK code2flow'));
});

test('callGraphEnv maps targets + language, empty language stays empty (auto-detect)', () => {
  assert.deepEqual(callGraphEnv(P), { C2F_TARGETS: 'model.py\npkg/', C2F_LANG: 'py' });
  assert.deepEqual(callGraphEnv({ ...P, lang: '' }), { C2F_TARGETS: 'model.py\npkg/', C2F_LANG: '' });
});

test('remoteCallGraphCommand: cd + base64-decode helper + env + interpreter, all quoted', () => {
  const cmd = remoteCallGraphCommand(P, 'print(1)');
  assert.match(cmd, /^cd '\/home\/me\/repo' && /);
  assert.ok(cmd.includes('base64 -d |'));
  // The newline-joined targets ride as a single shell-quoted env value.
  assert.ok(cmd.includes("C2F_TARGETS='model.py\npkg/'"));
  assert.ok(cmd.includes("C2F_LANG='py'"));
  assert.ok(cmd.trimEnd().endsWith('conda run -n rl python'));
  const b64 = /printf %s '([A-Za-z0-9+/=]+)'/.exec(cmd)?.[1] ?? '';
  assert.equal(Buffer.from(b64, 'base64').toString('utf8'), 'print(1)');
});

test('remoteCallGraphCommand: no repoRoot omits the cd', () => {
  const cmd = remoteCallGraphCommand({ ...P, repoRoot: '' }, 'print(1)');
  assert.ok(!cmd.startsWith('cd '));
  assert.match(cmd, /^printf %s /);
});

test('remoteCallGraphCommand: a single-quote in a target path is escaped safely', () => {
  const cmd = remoteCallGraphCommand({ ...P, targets: "a'b.py" }, 'print(1)');
  assert.ok(cmd.includes("C2F_TARGETS='a'\\''b.py'"));
});

test('remoteCallGraphProbe: decodes the probe helper and ends in the interpreter', () => {
  const cmd = remoteCallGraphProbe('python3');
  const b64 = /printf %s '([A-Za-z0-9+/=]+)'/.exec(cmd)?.[1] ?? '';
  assert.match(Buffer.from(b64, 'base64').toString('utf8'), /import code2flow/);
  assert.ok(cmd.trimEnd().endsWith('python3'));
});

test('extractDot is re-exported and pulls DOT out of code2flow log noise', () => {
  const out = ['Code2Flow: Found 1 files', '===DOT-START===', 'digraph G {', '}', '===DOT-END==='].join('\n');
  assert.equal(extractDot(out), 'digraph G {\n}');
});
