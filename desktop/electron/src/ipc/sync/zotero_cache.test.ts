import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  emptyZoteroSyncCache,
  loadZoteroSyncCache,
  planIncrementalZoteroWork,
  saveZoteroSyncCache,
  zoteroCacheScope,
} from './zotero_cache.ts';

test('incremental planner omits only unchanged local-hash + remote-ETag pairs', () => {
  const locals = new Map([
    ['SAME1234', { hash: 'aaaa' }],
    ['LOCAL234', { hash: 'bbbb' }],
    ['NEWL1234', { hash: 'cccc' }],
  ]);
  const remotes = new Map([
    ['SAME1234', '"etag-1"'],
    ['LOCAL234', '"etag-1"'],
    ['NEWR1234', '"etag-3"'],
    ['NOET1234', ''],
  ]);
  const cached = {
    SAME1234: { marker: '"etag-1"', mtime: 10, hash: 'AAAA' },
    LOCAL234: { marker: '"etag-1"', mtime: 10, hash: 'different' },
    NEWR1234: { marker: '"old-etag"', mtime: 10, hash: 'dddd' },
    NOET1234: { marker: '"etag-4"', mtime: 10, hash: 'eeee' },
  };

  const plan = planIncrementalZoteroWork(locals, remotes, cached);
  assert.equal(plan.skipped, 1);
  assert.deepEqual(plan.remote, { SAME1234: cached.SAME1234 });
  assert.deepEqual(plan.work, ['LOCAL234', 'NEWL1234', 'NEWR1234', 'NOET1234']);
});

test('cache scope changes with backend or storage identity', () => {
  const a = zoteroCacheScope('webdav', ['/storage', 'https://dav/zotero/', 'user']);
  assert.equal(a, zoteroCacheScope('webdav', ['/storage', 'https://dav/zotero/', 'user']));
  assert.notEqual(a, zoteroCacheScope('webdav', ['/other', 'https://dav/zotero/', 'user']));
  assert.notEqual(a, zoteroCacheScope('s3', ['/storage', 'https://dav/zotero/', 'user']));
});

test('cache persists atomically and malformed scopes degrade to empty', async () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'tp-zot-cache-'));
  try {
    const cache = emptyZoteroSyncCache();
    cache.local.ABCD1234 = { signature: 'sig', hash: '900150983cd24fb0d6963f7d28e17f72' };
    cache.remote.ABCD1234 = { marker: '"etag"', mtime: 123, hash: '900150983cd24fb0d6963f7d28e17f72' };
    await saveZoteroSyncCache(dir, 'scope', cache);
    assert.deepEqual(await loadZoteroSyncCache(dir, 'scope'), cache);
    assert.deepEqual(await loadZoteroSyncCache(dir, 'missing'), emptyZoteroSyncCache());
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
