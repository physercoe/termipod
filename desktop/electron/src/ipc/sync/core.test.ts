/// Fixture test suite for the shared sync decision core (ADR-055 M2.5). The plan
/// makes the decision logic — not the HTTP — the risk of the sync port, so it is
/// pinned here: the never-delete direction rule, the dependency-free XML
/// scanning, and the date parsing that drives mtime comparison. Dates are checked
/// against `Date.UTC` as an INDEPENDENT oracle (not the code under test). Run with
/// `node --test` (Node 22 strips the TS types natively).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  decideBoth,
  willTransfer,
  enumerateLocalTree,
  elementBlocks,
  extractAll,
  xmlUnescape,
  pctDecode,
  daysFromCivil,
  parseHttpDateMs,
  iso8601ToMs,
  isKey,
  MAX_FILE_BYTES,
} from './core.ts';

test('decideBoth: equal size ⇒ skip (identical, no ping-pong)', () => {
  assert.equal(decideBoth(100, 5, 100, 9), 'skip');
  assert.equal(decideBoth(0, null, 0, null), 'skip');
});

test('decideBoth: different size, newest mtime wins', () => {
  assert.equal(decideBoth(200, 9, 100, 5), 'upload'); // local newer
  assert.equal(decideBoth(100, 5, 200, 9), 'download'); // remote newer
});

test('decideBoth: same or unknown mtime ⇒ conflict, never guessed', () => {
  assert.equal(decideBoth(200, 5, 100, 5), 'conflict'); // equal mtime
  assert.equal(decideBoth(200, null, 100, 5), 'conflict'); // local mtime unknown
  assert.equal(decideBoth(200, 5, 100, null), 'conflict'); // remote mtime unknown
  assert.equal(decideBoth(200, null, 100, null), 'conflict'); // both unknown
});

test('NEVER-DELETE invariant: no decision or transfer ever removes', () => {
  // Structural: the only outcomes are copy-one-way (upload/download), skip, or
  // conflict — there is no delete. Exhaustively fuzz the decision + the
  // one-side-present cases and assert none is a removal.
  const legal = new Set(['upload', 'download', 'skip', 'conflict']);
  for (const ls of [0, 1, 100, MAX_FILE_BYTES + 1]) {
    for (const lm of [null, 1, 2] as (number | null)[]) {
      for (const rs of [0, 1, 100]) {
        for (const rm of [null, 1, 2] as (number | null)[]) {
          assert.ok(legal.has(decideBoth(ls, lm, rs, rm)));
        }
      }
    }
  }
  // A path present on only ONE side always copies to the other, never deletes.
  assert.equal(willTransfer({ size: 10, mtime: 1 }, null), true); // local-only → upload
  assert.equal(willTransfer(null, { size: 10, mtime: 1 }), true); // remote-only → download
  assert.equal(willTransfer(null, null), false); // nothing to do — not a delete
});

test('willTransfer: remote-only respects the 100 MB cap', () => {
  assert.equal(willTransfer(null, { size: MAX_FILE_BYTES, mtime: 1 }), true);
  assert.equal(willTransfer(null, { size: MAX_FILE_BYTES + 1, mtime: 1 }), false);
});

test('willTransfer: both-present tracks decideBoth', () => {
  assert.equal(willTransfer({ size: 200, mtime: 9 }, { size: 100, mtime: 5 }), true); // upload
  assert.equal(willTransfer({ size: 100, mtime: 9 }, { size: 100, mtime: 5 }), false); // skip (equal size)
  assert.equal(willTransfer({ size: 200, mtime: 5 }, { size: 100, mtime: 5 }), false); // conflict
});

test('elementBlocks: WebDAV <response>, prefix-safe', () => {
  const xml =
    '<D:multistatus xmlns:D="DAV:">' +
    '<D:response><D:href>/dav/a.md</D:href></D:response>' +
    '<D:response><D:href>/dav/b.md</D:href></D:response>' +
    '<D:responsedescription>ignore me</D:responsedescription>' +
    '</D:multistatus>';
  const blocks = elementBlocks(xml, 'response');
  assert.equal(blocks.length, 2); // responsedescription must NOT match
  assert.deepEqual(extractAll(blocks[0], 'href'), ['/dav/a.md']);
  assert.deepEqual(extractAll(blocks[1], 'href'), ['/dav/b.md']);
});

test('elementBlocks: S3 <Contents>, ignores CommonPrefixes', () => {
  const xml =
    '<ListBucketResult>' +
    '<Contents><Key>a/x.md</Key><Size>12</Size></Contents>' +
    '<CommonPrefixes><Prefix>a/</Prefix></CommonPrefixes>' +
    '<Contents><Key>a/y.md</Key><Size>34</Size></Contents>' +
    '</ListBucketResult>';
  const blocks = elementBlocks(xml, 'Contents');
  assert.equal(blocks.length, 2);
  assert.deepEqual(extractAll(blocks[1], 'Key'), ['a/y.md']);
  assert.deepEqual(extractAll(blocks[1], 'Size'), ['34']);
});

test('extractAll: namespace-stripped, trims, self-closing ignored', () => {
  assert.deepEqual(extractAll('<a:mtime> 1712345 </a:mtime>', 'mtime'), ['1712345']);
  assert.deepEqual(extractAll('<collection/><getcontentlength>7</getcontentlength>', 'collection'), []);
  assert.deepEqual(extractAll('<getcontentlength>7</getcontentlength>', 'getcontentlength'), ['7']);
});

test('daysFromCivil: epoch is 0; agrees with Date.UTC', () => {
  assert.equal(daysFromCivil(1970, 1, 1), 0);
  for (const [y, m, d] of [[1970, 1, 1], [2000, 2, 29], [2026, 7, 15], [1999, 12, 31]]) {
    assert.equal(daysFromCivil(y, m, d) * 86400_000, Date.UTC(y, m - 1, d));
  }
});

test('parseHttpDateMs: RFC-1123 vs Date.UTC oracle; null on garbage', () => {
  assert.equal(parseHttpDateMs('Thu, 01 Jan 1970 00:00:00 GMT'), 0);
  assert.equal(parseHttpDateMs('Wed, 15 Jul 2026 10:20:30 GMT'), Date.UTC(2026, 6, 15, 10, 20, 30));
  assert.equal(parseHttpDateMs('not a date'), null);
  assert.equal(parseHttpDateMs('Wed, 15 Zzz 2026 10:20:30 GMT'), null); // bad month
  assert.equal(parseHttpDateMs('Wed, 15 Jul 2026 10:20 GMT'), null); // missing seconds
});

test('iso8601ToMs: S3 LastModified vs Date.UTC oracle; null on garbage', () => {
  assert.equal(iso8601ToMs('1970-01-01T00:00:00.000Z'), 0);
  assert.equal(iso8601ToMs('2026-07-15T09:43:10.000Z'), Date.UTC(2026, 6, 15, 9, 43, 10));
  assert.equal(iso8601ToMs('2026-07-15'), null); // no time
  assert.equal(iso8601ToMs('garbage'), null);
});

test('xmlUnescape: entities, faithful to Rust sequential replace', () => {
  assert.equal(xmlUnescape('a&amp;b'), 'a&b');
  assert.equal(xmlUnescape('&lt;k&gt; &quot;q&quot; &apos;a&apos;'), '<k> "q" \'a\'');
  // Rust runs the replaces in sequence (`&amp;`→`&` first), so `&amp;lt;`
  // double-unescapes to `<`. We mirror that exactly rather than "fix" it.
  assert.equal(xmlUnescape('&amp;lt;'), '<');
});

test('pctDecode: %XX incl. UTF-8, leaves bare % alone', () => {
  assert.equal(pctDecode('a%20b'), 'a b');
  assert.equal(pctDecode('caf%C3%A9'), 'café'); // multi-byte UTF-8
  assert.equal(pctDecode('100%'), '100%'); // trailing bare % untouched
});

test('isKey: exactly 8 alphanumeric', () => {
  assert.equal(isKey('ABCD1234'), true);
  assert.equal(isKey('ABCD123'), false); // 7
  assert.equal(isKey('ABCD-234'), false); // non-alnum
  assert.equal(isKey('storage1x'), false); // 9
});

test('enumerateLocalTree: rel paths, skips hidden + SKIP_DIRS + depth', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'tp-sync-'));
  try {
    writeFileSync(path.join(root, 'note.md'), 'hello'); // 5 bytes
    mkdirSync(path.join(root, 'sub'));
    writeFileSync(path.join(root, 'sub', 'deep.md'), 'xy');
    writeFileSync(path.join(root, '.hidden'), 'no'); // hidden → skipped
    mkdirSync(path.join(root, '.obsidian')); // dot dir → skipped
    writeFileSync(path.join(root, '.obsidian', 'app.json'), '{}');
    mkdirSync(path.join(root, 'node_modules')); // SKIP_DIRS → skipped
    writeFileSync(path.join(root, 'node_modules', 'x.js'), 'z');

    const map = enumerateLocalTree(root);
    assert.deepEqual([...map.keys()].sort(), ['note.md', 'sub/deep.md']); // POSIX rel, no dirs/hidden/skip
    assert.equal(map.get('note.md')?.size, 5);
    assert.equal(typeof map.get('note.md')?.mtimeMs, 'number');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
