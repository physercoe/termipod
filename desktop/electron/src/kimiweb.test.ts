/// Tests for the kimiweb manager's pure pieces (agent-transcript-redesign P0):
/// the stdout token-URL parse (against the real kimi-code 0.28.1 banner),
/// binary resolution (well-known path vs PATH fallback), and the free-port
/// picker. The spawn lifecycle itself is smoke-tested manually against the
/// real binary. Run with `node --test` (Node strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { extractServerUrl, kimiBinaryPath, resolveKimiBinary, pickFreePort, expandWinVars, mergePathDirs } from './kimiweb.ts';

// The banner as actually printed by `kimi web --no-open --port 17331`
// (kimi-code 0.28.1), captured 2026-07-23.
const REAL_BANNER = `
  ▐█▛█▛█▌  Kimi server ready  0.28.1
  ▐█████▌  Local web UI is available from this machine.

  Local:    http://127.0.0.1:17331/#token=9OmdWua4fvUgNh1nQsvdOoySJgoXxUE14APKVCeJxuk
  Network:  off  use --host to enable

  Token:    9OmdWua4fvUgNh1nQsvdOoySJgoXxUE14APKVCeJxuk

  Logs:     off  use --log-level info to enable
  Stop:     Ctrl+C
`;

test('extractServerUrl: parses the real kimi web banner', () => {
  assert.equal(
    extractServerUrl(REAL_BANNER),
    'http://127.0.0.1:17331/#token=9OmdWua4fvUgNh1nQsvdOoySJgoXxUE14APKVCeJxuk',
  );
});

test('extractServerUrl: matches the URL, not the banner wording', () => {
  // The parse must survive label/whitespace drift — only the hash-token URL
  // shape is load-bearing.
  assert.equal(extractServerUrl('noise\n  http://localhost:42/#token=abc123  trailing'), 'http://localhost:42/#token=abc123');
  assert.equal(extractServerUrl('Kimi server: http://127.0.0.1:7/#token=t\n'), 'http://127.0.0.1:7/#token=t');
});

test('extractServerUrl: null when the token line never prints', () => {
  assert.equal(extractServerUrl(''), null);
  assert.equal(extractServerUrl('Local:    http://127.0.0.1:17331/\n'), null); // no token
  assert.equal(extractServerUrl('error: address already in use\n'), null);
});

test('kimiBinaryPath: honours KIMI_CODE_HOME, else ~/.kimi-code', () => {
  assert.equal(kimiBinaryPath({ KIMI_CODE_HOME: '/opt/kimi' }, '/home/u'), path.join('/opt/kimi', 'bin', process.platform === 'win32' ? 'kimi.cmd' : 'kimi'));
  assert.equal(kimiBinaryPath({}, '/home/u'), path.join('/home/u', '.kimi-code', 'bin', process.platform === 'win32' ? 'kimi.cmd' : 'kimi'));
});

test('resolveKimiBinary: the well-known path when it exists, PATH fallback otherwise', () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'kimiweb-test-'));
  try {
    const binDir = path.join(home, '.kimi-code', 'bin');
    fs.mkdirSync(binDir, { recursive: true });
    const bin = path.join(binDir, process.platform === 'win32' ? 'kimi.cmd' : 'kimi');
    fs.writeFileSync(bin, '');
    assert.equal(resolveKimiBinary({}, home), bin);
    // Missing binary → the bare name, so a PATH install still resolves (and a
    // truly absent one fails the spawn with a clear ENOENT error).
    assert.equal(resolveKimiBinary({}, home, () => false), process.platform === 'win32' ? 'kimi.cmd' : 'kimi');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('expandWinVars: expands %VAR% from the env, leaves unknowns verbatim', () => {
  const env = { USERPROFILE: 'C:\\Users\\me', APPDATA: 'C:\\Users\\me\\AppData\\Roaming' };
  assert.equal(expandWinVars('%USERPROFILE%\\bin;%APPDATA%\\npm', env), 'C:\\Users\\me\\bin;C:\\Users\\me\\AppData\\Roaming\\npm');
  // Case-insensitive var names (registry values vary in case).
  assert.equal(expandWinVars('%userprofile%\\x', env), 'C:\\Users\\me\\x');
  // An undefined var is left as-is rather than turned into an empty string.
  assert.equal(expandWinVars('%NOPE%\\x', env), '%NOPE%\\x');
});

test('mergePathDirs: dedups + drops empties, preserving first-seen order', () => {
  assert.equal(mergePathDirs(['/a:/b', '/b:/c', null, '', '/a'], ':'), '/a:/b:/c');
  // Trims per-entry whitespace and skips blank segments from trailing separators.
  assert.equal(mergePathDirs([' /a : /b ', '/a:'], ':'), '/a:/b');
  assert.equal(mergePathDirs([null, undefined, ''], ':'), '');
});

test('pickFreePort: returns a bindable loopback port', async () => {
  const port = await pickFreePort();
  assert.ok(Number.isInteger(port) && port > 0 && port < 65536);
  // Prove it is actually free by binding it ourselves.
  await new Promise<void>((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(port, '127.0.0.1', () => srv.close(() => resolve()));
  });
});
