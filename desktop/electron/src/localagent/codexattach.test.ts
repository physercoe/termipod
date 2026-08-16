/// L4a/L4b attach-rung checks. Every expectation here was measured against
/// codex-cli 0.147.0 (`codex app-server --help`, `... daemon --help`, a real
/// `daemon start`, and a logging relay placed between the CLI and its own
/// control socket), not read from the plan. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import {
  codexBinary,
  codexBinDirs,
  codexHome,
  controlSocketPath,
  findCodexOnPath,
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
  assert.deepEqual(p.mode === 'spawn' ? p.argv : null, ['codex', 'app-server']);
  assert.match(p.reason, /packages\/standalone\/current\/codex/);
});

test('installer-managed codex: the daemon rung is a SOCKET, never an argv', () => {
  const p = planCodexAttach(HOME, { managedInstallPresent: true }, {});
  assert.equal(p.mode, 'daemon');
  if (p.mode !== 'daemon') return;
  assert.equal(p.socketPath, '/home/u/.codex/app-server-control/app-server-control.sock');
  // Bringing the daemon UP is a command; talking to it is not.
  assert.deepEqual(p.startArgv, ['codex', 'app-server', 'daemon', 'start']);
  // L4a shipped `app-server proxy --sock <path>` as the data path. Measured
  // against the live daemon, that subcommand relays raw stdin bytes without the
  // WebSocket upgrade the socket requires, so the daemon closes the connection
  // and the proxy exits 0 with no output at all. Nothing may reintroduce it.
  assert.ok(!('argv' in p), 'a daemon plan must not carry an argv to run');
  assert.ok(
    !JSON.stringify(p).includes('proxy'),
    'the `app-server proxy` subcommand cannot carry this protocol — it never upgrades',
  );
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
  const p = planCodexAttach(HOME, { managedInstallPresent: false }, { TERMIPOD_CODEX_BIN: '/opt/bin/codex' });
  assert.equal(p.mode === 'spawn' ? p.argv[0] : null, '/opt/bin/codex');
  // The override also reaches the daemon rung's start command, which is the
  // only argv that rung has.
  const d = planCodexAttach(HOME, { managedInstallPresent: true }, { TERMIPOD_CODEX_BIN: '/opt/bin/codex' });
  assert.equal(d.mode === 'daemon' ? d.startArgv[0] : null, '/opt/bin/codex');
});

test('the well-known dirs cover the installer location a GUI app cannot see', () => {
  // The official installer writes ~/.local/bin/codex and appends its PATH line
  // to .bashrc — which an Electron app launched from a Dock icon never sources.
  const dirs = codexBinDirs(HOME, {});
  assert.ok(dirs.includes('/home/u/.local/bin'), 'the installer target must be searched');
  assert.ok(
    dirs.includes('/home/u/.codex/packages/standalone/current/bin'),
    'the managed standalone bin dir must be searched',
  );
  // A relocated CODEX_HOME moves the managed dir with it.
  assert.ok(codexBinDirs(HOME, { CODEX_HOME: '/srv/cx' }).includes('/srv/cx/packages/standalone/current/bin'));
});

test('findCodexOnPath returns an absolute path, and null when nothing exists', () => {
  const present = new Set(['/home/u/.local/bin/codex']);
  const exists = (p: string): boolean => present.has(p);
  assert.equal(findCodexOnPath('/usr/bin:/bin', ['/home/u/.local/bin'], exists), '/home/u/.local/bin/codex');
  // PATH wins when it has one, since that is what the user's shell would run.
  present.add('/usr/bin/codex');
  assert.equal(findCodexOnPath('/usr/bin:/bin', ['/home/u/.local/bin'], exists), '/usr/bin/codex');
  assert.equal(findCodexOnPath('/usr/bin', [], () => false), null);
  // Empty PATH segments must not produce a relative candidate like "codex".
  assert.equal(findCodexOnPath('', [], () => true), null);
});

test('codexBinary prefers the override, then a real file, then the bare name', () => {
  assert.equal(codexBinary(HOME, { TERMIPOD_CODEX_BIN: '/opt/x' }, () => false), '/opt/x');
  assert.equal(codexBinary(HOME, { PATH: '/usr/bin' }, (p) => p === '/usr/bin/codex'), '/usr/bin/codex');
  // Nothing found: hand back the bare name and let the OS have its say, rather
  // than failing here on a guess about what is installed.
  assert.equal(codexBinary(HOME, { PATH: '/usr/bin' }, () => false), 'codex');
});

test('codexBinary finds an installer-managed codex that is NOT on PATH', () => {
  // The separating input, and the entire reason `codexBinDirs` exists: a GUI
  // -launched Electron app inherits a PATH that never sourced .bashrc, so the
  // official installer's ~/.local/bin/codex is invisible to it. A test whose
  // codex is also on PATH cannot tell whether the well-known dirs are consulted
  // at all — searching PATH alone would pass it just as well.
  const onlyInInstallerDir = (p: string): boolean => p === '/home/u/.local/bin/codex';
  assert.equal(
    codexBinary(HOME, { PATH: '/usr/bin:/bin' }, onlyInInstallerDir),
    '/home/u/.local/bin/codex',
    'a codex reachable only from the installer dir must still be found',
  );
  // Same for the managed standalone tree, which is where the installer's
  // symlink actually points.
  const onlyInManagedDir = (p: string): boolean => p === '/home/u/.codex/packages/standalone/current/bin/codex';
  assert.equal(codexBinary(HOME, { PATH: '/usr/bin' }, onlyInManagedDir), '/home/u/.codex/packages/standalone/current/bin/codex');
});
