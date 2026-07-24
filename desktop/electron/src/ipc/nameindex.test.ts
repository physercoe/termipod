/// Tests for the Inspect tree's recursive name-index (plan §3 T1): hidden files
/// included, SKIP_DIRS listed but not descended, root-relative POSIX paths, and
/// the depth / entry-count truncation flags. Exercises `walkNameIndex` directly
/// (the `tree_index` handler is a one-line delegate). Fixtures are real temp
/// trees — no mocking. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { walkNameIndex, searchTree } from './fsutil.ts';

async function withDir(fn: (dir: string) => Promise<void>): Promise<void> {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'insptree-'));
  try {
    await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

test('walkNameIndex: recurses, root-relative paths, includes hidden files', async () => {
  await withDir(async (dir) => {
    await mkdir(path.join(dir, 'src'));
    await writeFile(path.join(dir, 'src', 'main.py'), 'x');
    await writeFile(path.join(dir, '.gitignore'), 'y'); // hidden file — an inspection target
    await mkdir(path.join(dir, '.github'));
    await writeFile(path.join(dir, '.github', 'ci.yml'), 'z'); // hidden dir IS descended

    const { entries, truncated } = await walkNameIndex(dir, 12, 20_000);
    assert.equal(truncated, false);
    const rels = entries.map((e) => e.rel).sort();
    assert.deepEqual(rels, ['.github', '.github/ci.yml', '.gitignore', 'src', 'src/main.py'].sort());
    assert.equal(entries.find((e) => e.rel === 'src')?.is_dir, true);
    assert.equal(entries.find((e) => e.rel === 'src/main.py')?.is_dir, false);
    assert.ok(!rels.some((r) => r.includes('\\')), 'rel paths are POSIX-joined');
  });
});

test('walkNameIndex: lists SKIP_DIRS as a node but never descends into them', async () => {
  await withDir(async (dir) => {
    await mkdir(path.join(dir, 'node_modules', 'left-pad'), { recursive: true });
    await writeFile(path.join(dir, 'node_modules', 'left-pad', 'index.js'), 'x');
    await writeFile(path.join(dir, 'app.js'), 'y');

    const { entries } = await walkNameIndex(dir, 12, 20_000);
    const rels = entries.map((e) => e.rel);
    assert.ok(rels.includes('node_modules'), 'the skip-dir itself is still listed');
    assert.ok(rels.includes('app.js'));
    assert.ok(!rels.some((r) => r.startsWith('node_modules/')), 'never descends a skip-dir');
  });
});

test('walkNameIndex: depth cap stops descent and flags truncation', async () => {
  await withDir(async (dir) => {
    // /a/b/c/deep.txt — with maxDepth 2 we index a, a/b, but not a/b/c contents.
    await mkdir(path.join(dir, 'a', 'b', 'c'), { recursive: true });
    await writeFile(path.join(dir, 'a', 'b', 'c', 'deep.txt'), 'x');

    const { entries, truncated } = await walkNameIndex(dir, 2, 20_000);
    const rels = entries.map((e) => e.rel);
    assert.ok(rels.includes('a'));
    assert.ok(rels.includes('a/b'));
    assert.ok(!rels.some((r) => r.startsWith('a/b/c')), 'stops at the depth cap');
    assert.equal(truncated, true);
  });
});

test('walkNameIndex: entry cap hard-stops and flags truncation', async () => {
  await withDir(async (dir) => {
    for (let i = 0; i < 10; i += 1) await writeFile(path.join(dir, `f${i}.txt`), 'x');
    const { entries, truncated } = await walkNameIndex(dir, 12, 4);
    assert.equal(entries.length, 4);
    assert.equal(truncated, true);
  });
});

test('walkNameIndex: rejects a non-directory / missing path', async () => {
  await withDir(async (dir) => {
    const f = path.join(dir, 'file.txt');
    await writeFile(f, 'x');
    await assert.rejects(() => walkNameIndex(f, 12, 20_000), /not a folder/);
    await assert.rejects(() => walkNameIndex(path.join(dir, 'nope'), 12, 20_000), /not a folder/);
  });
});

// ── searchTree (content search, T4a) ─────────────────────────────────────────
test('searchTree: literal match with rel path + 1-based line + capped text', async () => {
  await withDir(async (dir) => {
    await mkdir(path.join(dir, 'src'));
    await writeFile(path.join(dir, 'src', 'a.ts'), 'const x = 1;\nTODO: fix this\nconst y = 2;\n');
    await writeFile(path.join(dir, 'b.ts'), 'nothing here\n');
    const { hits, truncated } = await searchTree(dir, { query: 'TODO' });
    assert.equal(truncated, false);
    assert.equal(hits.length, 1);
    assert.deepEqual(hits[0], { rel: 'src/a.ts', line: 2, text: 'TODO: fix this' });
  });
});

test('searchTree: regex + case-insensitive by default, case-sensitive on request', async () => {
  await withDir(async (dir) => {
    await writeFile(path.join(dir, 'f.txt'), 'Foo\nfoo\nFOOBAR\n');
    assert.equal((await searchTree(dir, { query: 'foo' })).hits.length, 3); // insensitive
    assert.equal((await searchTree(dir, { query: 'foo', caseSensitive: true })).hits.length, 1);
    assert.equal((await searchTree(dir, { query: '^foo\\w+$', regex: true })).hits.length, 1); // FOOBAR (insensitive)
    await assert.rejects(() => searchTree(dir, { query: '(', regex: true }), /invalid regular expression/);
  });
});

test('searchTree: skips binary files (NUL) and SKIP_DIRS by default', async () => {
  await withDir(async (dir) => {
    await writeFile(path.join(dir, 'text.txt'), 'match me\n');
    await writeFile(path.join(dir, 'bin.dat'), Buffer.from([0x6d, 0x00, 0x6d, 0x61, 0x74, 0x63, 0x68])); // has a NUL
    await mkdir(path.join(dir, 'node_modules'));
    await writeFile(path.join(dir, 'node_modules', 'dep.js'), 'match me too\n');
    const { hits } = await searchTree(dir, { query: 'match' });
    assert.deepEqual(hits.map((h) => h.rel), ['text.txt']); // binary + skip-dir excluded
    const withSkip = await searchTree(dir, { query: 'match', includeSkip: true });
    assert.ok(withSkip.hits.some((h) => h.rel === 'node_modules/dep.js'));
  });
});

test('searchTree: caps total hits and flags truncation', async () => {
  await withDir(async (dir) => {
    await writeFile(path.join(dir, 'many.txt'), Array.from({ length: 10 }, () => 'hit').join('\n'));
    const { hits, truncated } = await searchTree(dir, { query: 'hit', maxHits: 4 });
    assert.equal(hits.length, 4);
    assert.equal(truncated, true);
  });
});

test('searchTree: empty query returns nothing, non-dir rejects', async () => {
  await withDir(async (dir) => {
    assert.deepEqual((await searchTree(dir, { query: '' })).hits, []);
    const f = path.join(dir, 'x.txt');
    await writeFile(f, 'y');
    await assert.rejects(() => searchTree(f, { query: 'y' }), /not a folder/);
  });
});

test('searchTree: only the first 2000 chars of a line are tested (main-process regex guard)', async () => {
  await withDir(async (dir) => {
    // One long line with the needle inside the tested prefix, one with it past
    // the cap — the pathological-backtracking guard trades tail matches for a
    // bounded regex input.
    await writeFile(path.join(dir, 'long.txt'), `${'x'.repeat(1900)}early\n${'x'.repeat(2100)}late`);
    const early = await searchTree(dir, { query: 'early' });
    assert.equal(early.hits.length, 1);
    const late = await searchTree(dir, { query: 'late' });
    assert.equal(late.hits.length, 0, 'a match past the test cap is (documentedly) missed');
  });
});
