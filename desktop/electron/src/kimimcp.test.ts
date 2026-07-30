/// Tests for the user-level ~/.kimi-code/mcp.json injection (D1 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.5): the merge/remove is
/// additive-only and preserves foreign keys, round-trips cleanly, never
/// clobbers a corrupt file, and the entry references the stable
/// ~/.termipod/bridge/ relay copy — never process.resourcesPath (the AppImage
/// pin). Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  installStableRelay,
  kimiMcpConfigPath,
  KIMI_MCP_ENTRY_NAME,
  mergeSharingEntry,
  removeSharingEntry,
  sharingEntry,
  stableRelayCopyPath,
} from './kimimcp.ts';

function tmpHome(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'tp-kimimcp-'));
}

function readCfg(home: string): Record<string, unknown> {
  return JSON.parse(fs.readFileSync(kimiMcpConfigPath(home), 'utf8')) as Record<string, unknown>;
}

const NOLOG = (): void => {};

// ── The entry itself ─────────────────────────────────────────────────────────

test('sharingEntry: stdio node + the stable relay copy, no env, never resourcesPath', () => {
  const home = tmpHome();
  try {
    const entry = sharingEntry(home);
    assert.equal(entry.command, 'node');
    assert.deepEqual(entry.args, [stableRelayCopyPath(home)]);
    assert.ok(entry.args[0].includes('.termipod/bridge/'), `entry must reference the stable copy: ${entry.args[0]}`);
    assert.ok(!entry.args[0].includes('resources'), `entry must never reference resourcesPath: ${entry.args[0]}`);
    assert.ok(!('env' in entry), 'the static entry carries no env — the relay fallback does discovery');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('installStableRelay: copies the relay to ~/.termipod/bridge/', () => {
  const home = tmpHome();
  try {
    const src = path.join(home, 'src-relay.mjs');
    fs.writeFileSync(src, '// relay fixture\n');
    const target = installStableRelay(home, src);
    assert.equal(target, stableRelayCopyPath(home));
    assert.equal(fs.readFileSync(target, 'utf8'), '// relay fixture\n');
    // A second install (the per-start refresh) overwrites cleanly.
    fs.writeFileSync(src, '// relay v2\n');
    installStableRelay(home, src);
    assert.equal(fs.readFileSync(target, 'utf8'), '// relay v2\n');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

// ── Merge ────────────────────────────────────────────────────────────────────

test('mergeSharingEntry: creates a missing file with only our entry, 0o600', () => {
  const home = tmpHome();
  try {
    assert.equal(mergeSharingEntry(home, NOLOG), 'written');
    const target = kimiMcpConfigPath(home);
    assert.equal(fs.statSync(target).mode & 0o777, 0o600);
    assert.deepEqual(readCfg(home), { mcpServers: { [KIMI_MCP_ENTRY_NAME]: sharingEntry(home) } });
    // Idempotent — the per-start refresh lands here too.
    assert.equal(mergeSharingEntry(home, NOLOG), 'written');
    assert.deepEqual(readCfg(home), { mcpServers: { [KIMI_MCP_ENTRY_NAME]: sharingEntry(home) } });
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('mergeSharingEntry: additive-only — foreign keys and servers pass through', () => {
  const home = tmpHome();
  try {
    const target = kimiMcpConfigPath(home);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    const foreign = {
      theme: 'dark',
      mcpServers: {
        'my-server': { command: 'python', args: ['-m', 'srv'], env: { KEY: 'v' } },
      },
      disabled: { 'my-server': true },
    };
    fs.writeFileSync(target, JSON.stringify(foreign, null, 2));
    assert.equal(mergeSharingEntry(home, NOLOG), 'written');
    const cfg = readCfg(home);
    assert.equal(cfg.theme, 'dark');
    assert.deepEqual(cfg.disabled, { 'my-server': true });
    const servers = cfg.mcpServers as Record<string, unknown>;
    assert.deepEqual(servers['my-server'], { command: 'python', args: ['-m', 'srv'], env: { KEY: 'v' } });
    assert.deepEqual(servers[KIMI_MCP_ENTRY_NAME], sharingEntry(home));
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('merge → remove round-trips: only our entry ever changes', () => {
  const home = tmpHome();
  try {
    const target = kimiMcpConfigPath(home);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    const foreign = { mcpServers: { 'my-server': { command: 'x' } }, otherTop: [1, 2] };
    fs.writeFileSync(target, JSON.stringify(foreign));
    mergeSharingEntry(home, NOLOG);
    assert.equal(removeSharingEntry(home, NOLOG), 'written');
    assert.deepEqual(readCfg(home), foreign);
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

// ── Remove ───────────────────────────────────────────────────────────────────

test('removeSharingEntry: absent file / absent entry → noop, nothing created or changed', () => {
  const home = tmpHome();
  try {
    assert.equal(removeSharingEntry(home, NOLOG), 'noop');
    assert.ok(!fs.existsSync(kimiMcpConfigPath(home)), 'remove must not create the file');
    const target = kimiMcpConfigPath(home);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, '{"mcpServers":{"other":{"command":"x"}}}');
    const before = fs.readFileSync(target, 'utf8');
    assert.equal(removeSharingEntry(home, NOLOG), 'noop');
    assert.equal(fs.readFileSync(target, 'utf8'), before, 'a noop remove must not even reformat the file');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

// ── Corrupt → untouched, never clobbered ─────────────────────────────────────

test('merge/remove: an unreadable-but-present config gets the corrupt treatment, never a reseed', () => {
  const home = tmpHome();
  try {
    // A directory at the config path makes every read fail WITHOUT ENOENT —
    // the deterministic stand-in for EACCES-style unreadability. Treating it
    // as absent would reseed a fresh config over foreign servers.
    fs.mkdirSync(kimiMcpConfigPath(home), { recursive: true });
    assert.equal(mergeSharingEntry(home, NOLOG), 'corrupt');
    assert.equal(removeSharingEntry(home, NOLOG), 'corrupt');
    assert.ok(fs.statSync(kimiMcpConfigPath(home)).isDirectory(), 'the path must be left untouched');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('merge/remove: a corrupt file is left byte-identical and reported', () => {
  const home = tmpHome();
  try {
    const target = kimiMcpConfigPath(home);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    for (const bad of ['not json{', '[1,2]', '{"mcpServers":["x"]}', '42']) {
      fs.writeFileSync(target, bad);
      assert.equal(mergeSharingEntry(home, NOLOG), 'corrupt', `merge over ${bad}`);
      assert.equal(fs.readFileSync(target, 'utf8'), bad, 'merge clobbered a corrupt file');
      assert.equal(removeSharingEntry(home, NOLOG), 'corrupt', `remove over ${bad}`);
      assert.equal(fs.readFileSync(target, 'utf8'), bad, 'remove clobbered a corrupt file');
    }
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});
