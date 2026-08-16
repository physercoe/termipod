/// L4a attach-rung checks. Every expectation here was measured against
/// codex-cli 0.147.0 on 2026-08-16 (`codex app-server --help`, `... daemon
/// --help`, `... proxy --help`, and a real `daemon start` refusal), not read
/// from the plan — whose L4 line described a WebSocket and a bearer scheme that
/// do not exist. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import {
  codexHome,
  controlSocketPath,
  managedCodexPath,
  MAX_UNIX_SOCKET_PATH,
  planCodexAttach,
} from './codexattach.ts';

const HOME = '/home/u';

test('CODEX_HOME relocates the home, like CLAUDE_CONFIG_DIR does for claude', () => {
  assert.equal(codexHome(HOME, {}), '/home/u/.codex');
  assert.equal(codexHome(HOME, { CODEX_HOME: '/srv/cx' }), '/srv/cx');
  // An empty value is not a relocation.
  assert.equal(codexHome(HOME, { CODEX_HOME: '' }), '/home/u/.codex');
});

test('the measured control-socket and managed-binary paths', () => {
  assert.equal(controlSocketPath('/home/u/.codex'), '/home/u/.codex/app-server-control/app-server-control.sock');
  assert.equal(managedCodexPath('/home/u/.codex'), '/home/u/.codex/packages/standalone/current/codex');
});

test('no installer-managed codex: spawn, because `daemon start` would refuse', () => {
  // This is the COMMON case, not an edge one: an npm/homebrew/distro codex has
  // no managed standalone install, and `daemon start` fails outright on it.
  const p = planCodexAttach(HOME, { managedInstallPresent: false }, {});
  assert.equal(p.mode, 'spawn');
  assert.deepEqual(p.argv, ['codex', 'app-server']);
  assert.equal(p.startArgv, undefined, 'spawn needs no preparation step');
  assert.match(p.reason, /packages\/standalone\/current\/codex/);
});

test('installer-managed codex: attach to the shared daemon via the stdio proxy', () => {
  const p = planCodexAttach(HOME, { managedInstallPresent: true }, {});
  assert.equal(p.mode, 'daemon');
  // The transport is a Unix socket reached through `app-server proxy` — there
  // is no WebSocket URL and no token anywhere in this argv.
  assert.deepEqual(p.argv, [
    'codex',
    'app-server',
    'proxy',
    '--sock',
    '/home/u/.codex/app-server-control/app-server-control.sock',
  ]);
  assert.deepEqual(p.startArgv, ['codex', 'app-server', 'daemon', 'start']);
  assert.ok(!p.argv.some((a) => /ws:|wss:|token|bearer/i.test(a)), 'no WebSocket or bearer anywhere in the argv');
});

test('a socket path over SUN_LEN disqualifies the daemon rung up front', () => {
  // Measured: a deep CODEX_HOME yields `path must be shorter than SUN_LEN` at
  // CONNECT time, with nothing in the message pointing at path length. Decide
  // it here, where we can say so.
  const deep = path.join('/tmp', 'x'.repeat(120));
  const p = planCodexAttach(HOME, { managedInstallPresent: true }, { CODEX_HOME: deep });
  assert.equal(p.mode, 'spawn');
  assert.match(p.reason, /Unix socket/);
  // ...and a short relocation still gets the daemon.
  const ok = planCodexAttach(HOME, { managedInstallPresent: true }, { CODEX_HOME: '/srv/cx' });
  assert.equal(ok.mode, 'daemon');
  assert.ok(controlSocketPath('/srv/cx').length <= MAX_UNIX_SOCKET_PATH);
});

test('every rung explains itself', () => {
  // The user whose session is NOT shared with their TUI should be able to find
  // out why without reading code.
  for (const managed of [true, false]) {
    const p = planCodexAttach(HOME, { managedInstallPresent: managed }, {});
    assert.ok(p.reason.length > 20, 'reason must be a sentence, not a label');
  }
});

test('the codex binary is overridable for a non-PATH install', () => {
  // A GUI-launched Electron app does not inherit the login shell PATH — the
  // problem kimiweb.ts already had to solve for kimi.
  const p = planCodexAttach(HOME, { managedInstallPresent: false }, { TERMIPOD_CODEX_BIN: '/opt/bin/codex' });
  assert.equal(p.argv[0], '/opt/bin/codex');
});
