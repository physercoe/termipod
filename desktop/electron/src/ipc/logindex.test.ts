/// Tests for the log line index (plan §4 W3 acceptance): the offset index
/// round-trips slices, counts lines with/without a trailing newline, finds match
/// positions, and extends when the file grows (follow mode) or rotates. Uses a
/// real temp file + fd reads — the whole point is the no-whole-file-slurp path.
/// Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, appendFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { openIndex, extend, slice, search, lineCount } from './logindex.ts';

async function withFile(body: string, fn: (p: string) => Promise<void>): Promise<void> {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'logidx-'));
  const p = path.join(dir, 'x.log');
  await writeFile(p, body);
  try {
    await fn(p);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

test('lineCount + slice: trailing newline yields no phantom line', async () => {
  await withFile('a\nb\nc\n', async (p) => {
    const idx = await openIndex(p);
    assert.equal(lineCount(idx), 3);
    assert.deepEqual(await slice(idx, 0, 3), ['a', 'b', 'c']);
    assert.deepEqual(await slice(idx, 1, 1), ['b']);
    assert.deepEqual(await slice(idx, 2, 10), ['c']); // clamps past EOF
    await idx.fh.close();
  });
});

test('slice: no trailing newline keeps the last line', async () => {
  await withFile('a\nb\nc', async (p) => {
    const idx = await openIndex(p);
    assert.equal(lineCount(idx), 3);
    assert.deepEqual(await slice(idx, 0, 3), ['a', 'b', 'c']);
    await idx.fh.close();
  });
});

test('slice: preserves interior empty lines and strips CR (CRLF)', async () => {
  await withFile('a\r\n\r\nb\r\n', async (p) => {
    const idx = await openIndex(p);
    assert.equal(lineCount(idx), 3);
    assert.deepEqual(await slice(idx, 0, 3), ['a', '', 'b']);
    await idx.fh.close();
  });
});

test('empty file has zero lines', async () => {
  await withFile('', async (p) => {
    const idx = await openIndex(p);
    assert.equal(lineCount(idx), 0);
    assert.deepEqual(await slice(idx, 0, 5), []);
    await idx.fh.close();
  });
});

test('search: reports first-match column per line, honours max', async () => {
  await withFile('info start\nWARN low disk\nerror boom\nWARN again\n', async (p) => {
    const idx = await openIndex(p);
    const all = await search(idx, 'WARN', 'i', 100);
    assert.deepEqual(
      all.hits.map((h) => h.line),
      [1, 3],
    );
    assert.equal(all.hits[0].col, 0);
    assert.equal(all.truncated, false);

    const capped = await search(idx, 'WARN', 'i', 1);
    assert.equal(capped.hits.length, 1);
    assert.equal(capped.truncated, true);
    await idx.fh.close();
  });
});

test('search: an invalid regex throws a typed error', async () => {
  await withFile('x\n', async (p) => {
    const idx = await openIndex(p);
    await assert.rejects(() => search(idx, '(', '', 10), /invalid search pattern/);
    await idx.fh.close();
  });
});

test('extend: a growing file re-indexes appended lines (follow mode)', async () => {
  await withFile('l1\nl2\n', async (p) => {
    const idx = await openIndex(p);
    assert.equal(lineCount(idx), 2);
    await appendFile(p, 'l3\nl4\n');
    assert.equal(await extend(idx), true);
    assert.equal(lineCount(idx), 4);
    assert.deepEqual(await slice(idx, 2, 2), ['l3', 'l4']);
    assert.equal(await extend(idx), false); // no growth → no-op
    await idx.fh.close();
  });
});

test('extend: completing a partial final line does not split it', async () => {
  await withFile('done\npartial', async (p) => {
    const idx = await openIndex(p);
    assert.equal(lineCount(idx), 2);
    assert.deepEqual(await slice(idx, 1, 1), ['partial']);
    await appendFile(p, '-rest\nnext\n');
    await extend(idx);
    assert.equal(lineCount(idx), 3);
    assert.deepEqual(await slice(idx, 1, 2), ['partial-rest', 'next']);
    await idx.fh.close();
  });
});

test('extend: a truncated (rotated) file re-indexes from scratch', async () => {
  await withFile('old1\nold2\nold3\n', async (p) => {
    const idx = await openIndex(p);
    assert.equal(lineCount(idx), 3);
    await writeFile(p, 'fresh\n'); // rotation: smaller file
    await extend(idx);
    assert.equal(lineCount(idx), 1);
    assert.deepEqual(await slice(idx, 0, 1), ['fresh']);
    await idx.fh.close();
  });
});
