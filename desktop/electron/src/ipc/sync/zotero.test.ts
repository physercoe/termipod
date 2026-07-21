/// Tests for the shared Zotero content-addressing helpers (ADR-055 M2.5c). MD5 is
/// checked against the canonical RFC-1321 vectors (an independent oracle); the
/// zip create→extract round-trip and the KEY enumeration run against real temp
/// files. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { md5Hex, buildProp, parseProp, enumerateLocalZotero, zipFiles, unzipInto } from './zotero.ts';

test('md5Hex: canonical RFC-1321 vectors', () => {
  assert.equal(md5Hex(Buffer.from('')), 'd41d8cd98f00b204e9800998ecf8427e');
  assert.equal(md5Hex(Buffer.from('abc')), '900150983cd24fb0d6963f7d28e17f72');
  assert.equal(md5Hex(Buffer.from('message digest')), 'f96b697d7cb7938d525a2f31aaf161d0');
});

test('buildProp / parseProp round-trip + a real Zotero prop', () => {
  const p = buildProp(1712345678000, 'abc123');
  assert.equal(p, '<properties version="1"><mtime>1712345678000</mtime><hash>abc123</hash></properties>');
  assert.deepEqual(parseProp(p), { mtime: 1712345678000, hash: 'abc123' });
  // A missing/garbled prop degrades to 0 / '' (never throws).
  assert.deepEqual(parseProp('<properties/>'), { mtime: 0, hash: '' });
  assert.deepEqual(parseProp('<properties><mtime>x</mtime></properties>'), { mtime: 0, hash: '' });
});

test('zipFiles → unzipInto round-trip (flat, byte-identical)', async () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'tp-zot-'));
  try {
    const f1 = path.join(dir, 'doc.pdf');
    const f2 = path.join(dir, 'note.txt');
    writeFileSync(f1, Buffer.from([0, 1, 2, 3, 255]));
    writeFileSync(f2, 'hello zotero');
    const zip = await zipFiles([f1, f2]);
    assert.ok(Buffer.isBuffer(zip) && zip.length > 0);

    const out = path.join(dir, 'extracted');
    const n = await unzipInto(zip, out);
    assert.equal(n, 2);
    assert.deepEqual([...readFileSync(path.join(out, 'doc.pdf'))], [0, 1, 2, 3, 255]);
    assert.equal(readFileSync(path.join(out, 'note.txt'), 'utf8'), 'hello zotero');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('unzipInto: basename-only (no path traversal), skips hidden', async () => {
  // Craft a zip whose entry name tries to escape; unzipInto must reduce to base.
  const dir = mkdtempSync(path.join(os.tmpdir(), 'tp-zot-'));
  try {
    const src = path.join(dir, 'evil.txt');
    writeFileSync(src, 'x');
    // zipFiles uses basenames, so build a raw entry via jszip directly to test escape.
    const { default: JSZip } = await import('jszip');
    const z = new JSZip();
    z.file('../../escape.txt', 'nope');
    z.file('.hidden', 'skip');
    z.file('ok.txt', 'yes');
    const bytes = await z.generateAsync({ type: 'nodebuffer' });
    const out = path.join(dir, 'ex');
    const n = await unzipInto(bytes, out);
    assert.equal(n, 2); // escape.txt (basenamed) + ok.txt; .hidden skipped
    assert.equal(readFileSync(path.join(out, 'escape.txt'), 'utf8'), 'nope'); // landed IN dest, not escaped
    assert.equal(readFileSync(path.join(out, 'ok.txt'), 'utf8'), 'yes');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('enumerateLocalZotero: only 8-char KEY dirs, primary-file hash + max mtime', async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'tp-zot-'));
  try {
    mkdirSync(path.join(root, 'ABCD1234'));
    writeFileSync(path.join(root, 'ABCD1234', 'a.pdf'), 'abc'); // md5 known
    writeFileSync(path.join(root, 'ABCD1234', '.DS_Store'), 'x'); // hidden → excluded
    mkdirSync(path.join(root, 'notakey')); // non-KEY → excluded
    writeFileSync(path.join(root, 'notakey', 'f.txt'), 'z');
    mkdirSync(path.join(root, 'EMPTY123')); // no files → excluded

    const map = await enumerateLocalZotero(root);
    assert.deepEqual([...map.keys()], ['ABCD1234']);
    const att = map.get('ABCD1234')!;
    assert.equal(att.hash, '900150983cd24fb0d6963f7d28e17f72'); // md5('abc')
    assert.equal(att.files.length, 1); // hidden excluded
    assert.ok(att.mtimeMs > 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
