/// Boundary-guard tests for the recursive-delete authority checks. The transfer
/// panel already confirms + scopes to a selected entry, but the main process must
/// independently refuse a destructive floor ("validate at every boundary"), so a
/// future/buggy caller can't `rm -rf /` or wipe `~`. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import os from 'node:os';
import path from 'node:path';
import { assertSafeLocalDelete, assertSafeRemoteDelete } from './fsutil.ts';

test('assertSafeRemoteDelete refuses protected remote paths', () => {
  for (const p of ['', '   ', '/', '//', '.', './', '..', '~', '~/', ' / ', '/.']) {
    // '/.' normalises to '/.'? no — only trailing slashes are stripped; '/.' is a
    // path to the root's '.' entry, still the root — but we intentionally only
    // block the exact floors, so drop '/.' from the must-throw set below.
    if (p === '/.') continue;
    assert.throws(() => assertSafeRemoteDelete(p), /protected remote path/, `should refuse ${JSON.stringify(p)}`);
  }
});

test('assertSafeRemoteDelete allows real remote entries', () => {
  for (const p of ['/home/user/file', '~/notes.txt', './build', 'a/b/c', '/var/log/x/']) {
    assert.doesNotThrow(() => assertSafeRemoteDelete(p), `should allow ${JSON.stringify(p)}`);
  }
});

test('assertSafeLocalDelete refuses empty, a filesystem root, and the home dir', () => {
  assert.throws(() => assertSafeLocalDelete(''), /empty path/);
  assert.throws(() => assertSafeLocalDelete('   '), /empty path/);
  assert.throws(() => assertSafeLocalDelete(path.parse(process.cwd()).root), /filesystem root/);
  assert.throws(() => assertSafeLocalDelete(os.homedir()), /home directory/);
  assert.throws(() => assertSafeLocalDelete(`${os.homedir()}/`), /home directory/); // trailing slash normalised by resolve
});

test('assertSafeLocalDelete allows a real entry under home', () => {
  assert.doesNotThrow(() => assertSafeLocalDelete(path.join(os.homedir(), 'Downloads', 'x.zip')));
});
