/// Tests for the hub-doc tree fold (round-3 T2): a flat `docs_root` file list
/// (with or without explicit dir rows) folds into a parent→children map with
/// synthesized ancestors, dirs-first ordering, and a flat file list for the
/// exact filter. Run locally: `node --test src/state/inspectTree.test.ts` from
/// `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { foldHubDocs, nodeMatches } from './inspectTree.ts';

test('foldHubDocs: nests by parent, synthesizes missing ancestors', () => {
  const { children, files } = foldHubDocs([
    { path: 'README.md', is_dir: false },
    { path: 'weights/model.safetensors', is_dir: false }, // no explicit 'weights' dir row
    { path: 'src', is_dir: true },
    { path: 'src/main.py', is_dir: false },
  ]);
  const root = children.get('') ?? [];
  // dirs first (src, weights), then files (README.md), each group name-sorted.
  assert.deepEqual(root.map((n) => n.name), ['src', 'weights', 'README.md']);
  assert.deepEqual(root.map((n) => n.is_dir), [true, true, false]);
  // synthesized 'weights' dir has the right children
  assert.deepEqual((children.get('weights') ?? []).map((n) => n.key), ['weights/model.safetensors']);
  assert.deepEqual((children.get('src') ?? []).map((n) => n.key), ['src/main.py']);
  // file list is exactly the non-dir nodes
  assert.deepEqual(files.map((n) => n.key).sort(), ['README.md', 'src/main.py', 'weights/model.safetensors']);
});

test('foldHubDocs: dedupes when a dir row and its synthesized ancestor collide', () => {
  const { children } = foldHubDocs([
    { path: 'a/b/c.txt', is_dir: false }, // synthesizes a, a/b
    { path: 'a', is_dir: true }, // explicit — must not double-insert
    { path: 'a/b', is_dir: true },
  ]);
  assert.deepEqual((children.get('') ?? []).map((n) => n.name), ['a']);
  assert.deepEqual((children.get('a') ?? []).map((n) => n.name), ['b']);
  assert.deepEqual((children.get('a/b') ?? []).map((n) => n.name), ['c.txt']);
});

test('foldHubDocs: tolerates leading/trailing slashes and empty rows', () => {
  const { children, files } = foldHubDocs([
    { path: '/docs/x.md', is_dir: false },
    { path: 'docs/', is_dir: true },
    { path: '', is_dir: true },
  ]);
  assert.deepEqual((children.get('') ?? []).map((n) => n.name), ['docs']);
  assert.deepEqual(files.map((n) => n.key), ['docs/x.md']);
});

test('nodeMatches: matches on name or full key, case-insensitively', () => {
  const n = { name: 'main.py', key: 'src/app/main.py', is_dir: false };
  assert.ok(nodeMatches(n, 'main'));
  assert.ok(nodeMatches(n, 'src/app'));
  assert.ok(nodeMatches(n, 'MAIN'.toLowerCase()));
  assert.ok(!nodeMatches(n, 'test'));
});
