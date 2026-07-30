import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  MAX_FETCH_BYTES,
  cachedRecordingPath,
  fetchRecording,
  isContentDigest,
  pruneRecordingCache,
  pruneVictims,
  type RemoteFile,
} from './rerun_cache.ts';

/// rerun_cache.test.ts — the recording fetch (J8 Replay W4b-2).
///
/// The remote side is a local file behind the same `{size, open, close}` shape
/// `openSftpFile` returns, so everything below is the real streaming path: real
/// read streams, real writes, real hashing. What is NOT covered is ssh2 itself;
/// what a session does with a bounded `createReadStream` is its business, and
/// the media scheme has been exercising that call since W2d.

function tmpdir(t: { after: (fn: () => void) => void }, tag: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `rerun-${tag}-`));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

/// A `RemoteFile` backed by a local file, plus a record of whether it was closed
/// — the channel leak is the failure this shape exists to prevent.
function fileSource(p: string): RemoteFile & { closed: () => number } {
  let closes = 0;
  return {
    size: fs.statSync(p).size,
    open: (start, end) => fs.createReadStream(p, { start, end }),
    close: () => {
      closes += 1;
    },
    closed: () => closes,
  };
}

function writeRecording(dir: string, name: string, body: string): { path: string; sha: string } {
  const p = path.join(dir, name);
  fs.writeFileSync(p, body);
  return { path: p, sha: createHash('sha256').update(body).digest('hex') };
}

const SHA_ZERO = '0'.repeat(64);

test('isContentDigest accepts a sha-256 and nothing else', () => {
  assert.equal(isContentDigest(SHA_ZERO), true);
  assert.equal(isContentDigest('A'.repeat(64)), false, 'uppercase is not the form we emit');
  assert.equal(isContentDigest('0'.repeat(63)), false);
  assert.equal(isContentDigest(`${'0'.repeat(63)}/../x`), false);
  assert.equal(isContentDigest(''), false);
});

test('cachedRecordingPath refuses anything that is not a digest', () => {
  const p = cachedRecordingPath('/cache', SHA_ZERO);
  assert.equal(p, path.join('/cache', `${SHA_ZERO}.rrd`));
  // The remote host supplies both the path and the digest; a digest that is
  // really a traversal must not become a local filename.
  assert.throws(() => cachedRecordingPath('/cache', '../../etc/passwd'), /not a sha-256 digest/);
  assert.throws(() => cachedRecordingPath('/cache', ''), /not a sha-256 digest/);
});

test('a fetch streams the file in, verifies it, and names it by its digest', async (t) => {
  const remote = tmpdir(t, 'remote');
  const cache = tmpdir(t, 'cache');
  const body = 'RRD'.repeat(50_000); // 150 KB — several stream chunks
  const rec = writeRecording(remote, 'pusht_episode_0.rrd', body);
  const src = fileSource(rec.path);

  const ticks: number[] = [];
  const out = await fetchRecording({
    root: cache,
    sha256: rec.sha,
    source: src,
    onProgress: (done) => ticks.push(done),
  });

  assert.equal(out.cached, false);
  assert.equal(out.bytes, body.length);
  assert.equal(out.path, path.join(cache, `${rec.sha}.rrd`));
  assert.equal(fs.readFileSync(out.path, 'utf8'), body, 'the bytes that arrived are the bytes sent');
  assert.equal(src.closed(), 1, 'the SFTP channel is released');
  assert.deepEqual(ticks.at(-1), body.length, 'the last tick is the exact total');
  assert.deepEqual(
    fs.readdirSync(cache).filter((n) => n.includes('.part-')),
    [],
    'no temp file survives a success',
  );
});

test('a second fetch of the same digest transfers nothing', async (t) => {
  const remote = tmpdir(t, 'remote');
  const cache = tmpdir(t, 'cache');
  const rec = writeRecording(remote, 'ep.rrd', 'RRD-BODY');
  await fetchRecording({ root: cache, sha256: rec.sha, source: fileSource(rec.path) });

  const second = fileSource(rec.path);
  const out = await fetchRecording({ root: cache, sha256: rec.sha, source: second });
  assert.equal(out.cached, true);
  assert.equal(out.bytes, 'RRD-BODY'.length);
  assert.equal(second.closed(), 1, 'the channel is released on the hit path too');
});

test('bytes that do not hash to the promised digest are refused, not served', async (t) => {
  const remote = tmpdir(t, 'remote');
  const cache = tmpdir(t, 'cache');
  const rec = writeRecording(remote, 'ep.rrd', 'the real bytes');
  const src = fileSource(rec.path);

  // The digest the host promised, against bytes that are not it — a truncated
  // transfer, a substituted file, a stale result row.
  await assert.rejects(
    fetchRecording({ root: cache, sha256: SHA_ZERO, source: src }),
    /arrived corrupt/,
  );
  assert.deepEqual(fs.readdirSync(cache), [], 'nothing is left behind, not even the temp file');
  assert.equal(src.closed(), 1);
});

test('an empty or over-large recording is refused before any bytes move', async (t) => {
  const remote = tmpdir(t, 'remote');
  const cache = tmpdir(t, 'cache');
  const rec = writeRecording(remote, 'empty.rrd', '');
  await assert.rejects(
    fetchRecording({ root: cache, sha256: rec.sha, source: fileSource(rec.path) }),
    /empty recording/,
  );

  const big = writeRecording(remote, 'big.rrd', 'x');
  const huge: RemoteFile = {
    ...fileSource(big.path),
    size: MAX_FETCH_BYTES + 1,
  };
  await assert.rejects(
    fetchRecording({ root: cache, sha256: big.sha, source: huge }),
    /over the .* fetch limit/,
  );
  assert.deepEqual(fs.readdirSync(cache), []);
});

test('a bad digest fails the fetch without opening the channel', async (t) => {
  const remote = tmpdir(t, 'remote');
  const cache = tmpdir(t, 'cache');
  const rec = writeRecording(remote, 'ep.rrd', 'body');
  let opened = 0;
  const src: RemoteFile = {
    size: 4,
    open: (start, end) => {
      opened += 1;
      return fs.createReadStream(rec.path, { start, end });
    },
    close: () => undefined,
  };
  await assert.rejects(
    fetchRecording({ root: cache, sha256: 'not-a-digest', source: src }),
    /not a sha-256 digest/,
  );
  assert.equal(opened, 0);
});

test('pruneVictims evicts oldest-first and never the file just fetched', () => {
  const entries = [
    { name: 'new.rrd', size: 40, mtimeMs: 300 },
    { name: 'old.rrd', size: 40, mtimeMs: 100 },
    { name: 'mid.rrd', size: 40, mtimeMs: 200 },
  ];
  assert.deepEqual(pruneVictims(entries, 200, 'new.rrd'), [], 'under the cap, nothing goes');
  assert.deepEqual(pruneVictims(entries, 100, 'new.rrd'), ['old.rrd'], 'one eviction is enough');
  assert.deepEqual(pruneVictims(entries, 30, 'new.rrd'), ['old.rrd', 'mid.rrd']);
  // The freshly fetched file is the one thing eviction must not take: the viewer
  // is about to open it, and losing it means fetching it again immediately.
  assert.deepEqual(
    pruneVictims([{ name: 'only.rrd', size: 500, mtimeMs: 1 }], 10, 'only.rrd'),
    [],
  );
});

test('a fetch evicts older recordings once the cache is over its cap', async (t) => {
  const remote = tmpdir(t, 'remote');
  const cache = tmpdir(t, 'cache');
  const stale = path.join(cache, `${'a'.repeat(64)}.rrd`);
  fs.writeFileSync(stale, 'x'.repeat(2000));
  fs.utimesSync(stale, new Date(1000), new Date(1000)); // long ago

  const rec = writeRecording(remote, 'ep.rrd', 'fresh bytes');
  const out = await fetchRecording({
    root: cache,
    sha256: rec.sha,
    source: fileSource(rec.path),
    cap: 1000,
  });

  assert.equal(fs.existsSync(out.path), true, 'the recording we just fetched survives');
  assert.equal(fs.existsSync(stale), false, 'the older one is evicted');
});

test('pruneRecordingCache tolerates a cache directory that does not exist', async (t) => {
  const cache = path.join(tmpdir(t, 'cache'), 'never-created');
  assert.deepEqual(await pruneRecordingCache(cache, 0, ''), []);
});
