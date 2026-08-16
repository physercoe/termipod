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
  MCP_ENTRY_NAME,
  mergeSharingEntries,
  removeSharingEntries,
  sharingEntry,
  stableRelayCopyPath,
  claudeMcpConfigPath,
  claudeSharingEntry,
  codexEntryBlock,
  tomlString,
  type McpWrite,
} from './usermcp.ts';

/// These cases pin the KIMI arm of the three-engine reseed (they predate F4 and
/// are the round-trip/corruption contract every engine now shares). An empty
/// env keeps claude and codex resolving inside the same throwaway home, so a
/// case that only seeds a kimi file leaves the other two as no-ops.
const ENV: NodeJS.ProcessEnv = {};
const KIMI_MCP_ENTRY_NAME = MCP_ENTRY_NAME;
const mergeSharingEntry = (home: string, log?: (m: string) => void): McpWrite =>
  mergeSharingEntries(home, ENV, log ?? ((): void => {})).kimi;
const removeSharingEntry = (home: string, log?: (m: string) => void): McpWrite =>
  removeSharingEntries(home, ENV, log ?? ((): void => {})).kimi;

function tmpHome(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'tp-usermcp-'));
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

// ── F4: claude + codex ───────────────────────────────────────────────────────

/// Fixtures below are the shapes the vendors' OWN CLIs produce, captured by
/// running them against throwaway config homes (`claude mcp add -s user`,
/// `codex mcp add`) — not transcribed from documentation.

function claudeEnv(dir: string): NodeJS.ProcessEnv {
  return { CLAUDE_CONFIG_DIR: dir };
}
function codexEnv(dir: string): NodeJS.ProcessEnv {
  return { CODEX_HOME: dir };
}

test('claude: the entry matches what `claude mcp add -s user` writes', () => {
  const home = tmpHome();
  try {
    const e = claudeSharingEntry(home);
    // claude's CLI writes an explicit transport discriminator.
    assert.equal(e.type, 'stdio');
    assert.equal(e.command, 'node');
    assert.deepEqual(e.args, [stableRelayCopyPath(home)]);
    // The pinned constraint: no env rides the entry.
    assert.ok(!('env' in e), 'the entry must carry no env');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('claude: CLAUDE_CONFIG_DIR relocates the file, per account', () => {
  const home = tmpHome();
  const alt = tmpHome();
  try {
    assert.equal(claudeMcpConfigPath(home, {}), path.join(home, '.claude.json'));
    assert.equal(claudeMcpConfigPath(home, claudeEnv(alt)), path.join(alt, '.claude.json'));
    // Reseeding with the env set must write the RELOCATED file and leave the
    // home-directory one absent — resolving it blindly would seed the wrong
    // account (localagent/store.ts persists this per session for that reason).
    mergeSharingEntries(home, claudeEnv(alt), NOLOG);
    assert.ok(fs.existsSync(path.join(alt, '.claude.json')), 'relocated file not written');
    assert.ok(!fs.existsSync(path.join(home, '.claude.json')), 'wrote the wrong account file');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
    fs.rmSync(alt, { recursive: true, force: true });
  }
});

test('claude: a live state file keeps every foreign key and round-trips', () => {
  const home = tmpHome();
  try {
    // A trimmed capture of a real ~/.claude.json: state, not config, and with
    // no mcpServers key at all until something adds one.
    const before = {
      firstStartTime: '2026-08-16T10:23:51.814Z',
      userID: 'deadbeef',
      migrationVersion: 13,
      projects: { '/home/u/work': { lastCost: 1.5 } },
      seenNotifications: {},
    };
    const target = claudeMcpConfigPath(home, {});
    fs.writeFileSync(target, JSON.stringify(before, null, 2));
    assert.equal(mergeSharingEntries(home, {}, NOLOG).claude, 'written');
    const after = JSON.parse(fs.readFileSync(target, 'utf8')) as Record<string, unknown>;
    for (const k of Object.keys(before)) {
      assert.deepEqual(after[k], (before as Record<string, unknown>)[k], `foreign key ${k} was disturbed`);
    }
    assert.ok((after.mcpServers as Record<string, unknown>)[KIMI_MCP_ENTRY_NAME] !== undefined);
    // Round trip: remove restores the original exactly.
    assert.equal(removeSharingEntries(home, {}, NOLOG).claude, 'written');
    const restored = JSON.parse(fs.readFileSync(target, 'utf8')) as Record<string, unknown>;
    assert.deepEqual(restored.mcpServers, {});
    delete restored.mcpServers;
    assert.deepEqual(restored, before, 'round trip did not restore the state file');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('claude: a foreign MCP server is never touched', () => {
  const home = tmpHome();
  try {
    const target = claudeMcpConfigPath(home, {});
    const foreign = { type: 'stdio', command: 'npx', args: ['sentry-mcp'], env: { TOKEN: 'x' } };
    fs.writeFileSync(target, JSON.stringify({ mcpServers: { sentry: foreign } }, null, 2));
    mergeSharingEntries(home, {}, NOLOG);
    removeSharingEntries(home, {}, NOLOG);
    const after = JSON.parse(fs.readFileSync(target, 'utf8')) as { mcpServers: Record<string, unknown> };
    assert.deepEqual(after.mcpServers.sentry, foreign);
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('codex: the block matches what `codex mcp add` writes', () => {
  const home = tmpHome();
  try {
    assert.equal(
      codexEntryBlock(home),
      `[mcp_servers.termipod-desktop]\ncommand = "node"\nargs = ["${stableRelayCopyPath(home)}"]\n`,
    );
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

test('codex: comments, trailing comments and foreign tables all survive a round trip', () => {
  const home = tmpHome();
  const dir = tmpHome();
  try {
    // Verified against the real CLI: `codex mcp add` then `codex mcp remove`
    // restores this file byte-for-byte, so ours must too.
    const before = '# my important comment\nmodel = "gpt-5"  # trailing note\n\n[mcp_servers.existing]\ncommand = "npx"\n';
    const target = path.join(dir, 'config.toml');
    fs.writeFileSync(target, before);
    assert.equal(mergeSharingEntries(home, codexEnv(dir), NOLOG).codex, 'written');
    const merged = fs.readFileSync(target, 'utf8');
    assert.ok(merged.includes('# my important comment'), 'lost a leading comment');
    assert.ok(merged.includes('model = "gpt-5"  # trailing note'), 'lost a trailing comment');
    assert.ok(merged.includes('[mcp_servers.existing]'), 'lost a foreign server');
    assert.ok(merged.includes('[mcp_servers.termipod-desktop]'), 'did not add our table');
    assert.equal(removeSharingEntries(home, codexEnv(dir), NOLOG).codex, 'written');
    assert.equal(fs.readFileSync(target, 'utf8'), before, 'round trip was not byte-identical');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('codex: merging twice is idempotent (the per-start refresh)', () => {
  const home = tmpHome();
  const dir = tmpHome();
  try {
    const target = path.join(dir, 'config.toml');
    fs.writeFileSync(target, 'model = "gpt-5"\n');
    mergeSharingEntries(home, codexEnv(dir), NOLOG);
    const once = fs.readFileSync(target, 'utf8');
    mergeSharingEntries(home, codexEnv(dir), NOLOG);
    assert.equal(fs.readFileSync(target, 'utf8'), once, 'a second merge duplicated the table');
    assert.equal(once.match(/\[mcp_servers\.termipod-desktop\]/g)?.length, 1);
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('codex: an absent config is seeded, and remove on absent is a noop', () => {
  const home = tmpHome();
  const dir = tmpHome();
  try {
    const target = path.join(dir, 'config.toml');
    fs.rmSync(target, { force: true });
    assert.equal(removeSharingEntries(home, codexEnv(dir), NOLOG).codex, 'noop');
    assert.equal(mergeSharingEntries(home, codexEnv(dir), NOLOG).codex, 'written');
    // A seeded file must not open with a blank line.
    assert.ok(fs.readFileSync(target, 'utf8').startsWith('[mcp_servers.'));
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('codex: a TOML form we will not rewrite is refused, not guessed at', () => {
  const home = tmpHome();
  const dir = tmpHome();
  try {
    const target = path.join(dir, 'config.toml');
    // An inline table and a dotted root key both express mcp_servers in a shape
    // this line-splicer cannot edit safely. Destroying a user's model config
    // beats nothing at all, so we decline and say which.
    for (const src of [
      'mcp_servers = { existing = { command = "npx" } }\n',
      'mcp_servers.existing.command = "npx"\n',
    ]) {
      fs.writeFileSync(target, src);
      assert.equal(mergeSharingEntries(home, codexEnv(dir), NOLOG).codex, 'unsupported', src);
      assert.equal(fs.readFileSync(target, 'utf8'), src, 'refused but still wrote');
      assert.equal(removeSharingEntries(home, codexEnv(dir), NOLOG).codex, 'unsupported', src);
      assert.equal(fs.readFileSync(target, 'utf8'), src, 'refused but still wrote');
    }
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('codex: our table is removed whole, not line-by-line', () => {
  const home = tmpHome();
  const dir = tmpHome();
  try {
    const target = path.join(dir, 'config.toml');
    // A hand-written variant: quoted key, extra keys inside our table, and a
    // foreign table AFTER ours that must survive.
    fs.writeFileSync(
      target,
      '[mcp_servers."termipod-desktop"]\ncommand = "node"\nargs = ["old"]\nstartup_timeout_ms = 20000\n\n[mcp_servers.after]\ncommand = "x"\n',
    );
    assert.equal(removeSharingEntries(home, codexEnv(dir), NOLOG).codex, 'written');
    const after = fs.readFileSync(target, 'utf8');
    assert.ok(!after.includes('termipod-desktop'), 'left our table behind');
    assert.ok(!after.includes('startup_timeout_ms'), 'left an orphan key from our table');
    assert.ok(after.includes('[mcp_servers.after]') && after.includes('command = "x"'), 'ate the next table');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('codex: a Windows relay path is escaped, not read as escapes', () => {
  // tomlString is what stands between a backslash path and a corrupt config.
  assert.equal(tomlString('C:\\Users\\dr\\.termipod\\bridge\\r.mjs'), '"C:\\\\Users\\\\dr\\\\.termipod\\\\bridge\\\\r.mjs"');
  assert.equal(tomlString('has "quotes"'), '"has \\"quotes\\""');
  assert.equal(tomlString('tab\there'), '"tab\\there"');
});

test(
  'a WRITE failure on one engine still reseeds the others',
  { skip: process.getuid?.() === 0 ? 'chmod cannot forbid root' : false },
  () => {
    const home = tmpHome();
    const ro = tmpHome();
    try {
      // A read-only config dir: the read comes back ENOENT (benign), so the
      // failure happens in the WRITE — which used to throw out of
      // mergeSharingEntries, discarding kimi's result and skipping codex.
      fs.chmodSync(ro, 0o500);
      const r = mergeSharingEntries(home, claudeEnv(ro), NOLOG);
      assert.equal(r.claude, 'failed');
      assert.equal(r.kimi, 'written');
      assert.equal(r.codex, 'written', 'a claude write failure must not stop the codex reseed');
      assert.ok(!fs.existsSync(path.join(ro, '.claude.json')), 'the unwritable target must stay absent');
    } finally {
      fs.chmodSync(ro, 0o700);
      fs.rmSync(home, { recursive: true, force: true });
      fs.rmSync(ro, { recursive: true, force: true });
    }
  },
);

test('one engine failing never stops the others', () => {
  const home = tmpHome();
  try {
    // kimi's file is unreadable; claude and codex must still be reseeded.
    const kimi = kimiMcpConfigPath(home);
    fs.mkdirSync(path.dirname(kimi), { recursive: true });
    fs.writeFileSync(kimi, 'not json{');
    const r = mergeSharingEntries(home, {}, NOLOG);
    assert.equal(r.kimi, 'corrupt');
    assert.equal(r.claude, 'written');
    assert.equal(r.codex, 'written');
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});
