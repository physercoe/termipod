import test from 'node:test';
import assert from 'node:assert/strict';
import { formatModifiedTime, formatPermissions, nextFileSort, sortFileEntries } from './fileListing.ts';

const rows = [
  { name: 'file10.txt', is_dir: false, modified_ms: 20 },
  { name: 'old.txt', is_dir: false, modified_ms: 10 },
  { name: 'unknown.txt', is_dir: false, modified_ms: null },
  { name: 'folder', is_dir: true, modified_ms: 5 },
  { name: 'file2.txt', is_dir: false, modified_ms: 30 },
];

test('file listing keeps directories first and naturally sorts filenames', () => {
  assert.deepEqual(
    sortFileEntries(rows, { key: 'name', direction: 'asc' }).map((row) => row.name),
    ['folder', 'file2.txt', 'file10.txt', 'old.txt', 'unknown.txt'],
  );
});

test('file listing sorts newest first and leaves unknown timestamps last', () => {
  assert.deepEqual(
    sortFileEntries(rows, { key: 'modified', direction: 'desc' }).map((row) => row.name),
    ['folder', 'file2.txt', 'file10.txt', 'old.txt', 'unknown.txt'],
  );
});

test('switching to modified defaults to newest first, then toggles direction', () => {
  const modified = nextFileSort({ key: 'name', direction: 'asc' }, 'modified');
  assert.deepEqual(modified, { key: 'modified', direction: 'desc' });
  assert.deepEqual(nextFileSort(modified, 'modified'), { key: 'modified', direction: 'asc' });
});

test('file permissions render normal and special Unix modes', () => {
  assert.equal(formatPermissions(0o100644, false), '-rw-r--r--');
  assert.equal(formatPermissions(0o040755, true), 'drwxr-xr-x');
  assert.equal(formatPermissions(0o120777, false), 'lrwxrwxrwx');
  assert.equal(formatPermissions(0o104755, false), '-rwsr-xr-x');
  assert.equal(formatPermissions(0o041777, true), 'drwxrwxrwt');
  assert.equal(formatPermissions(null, false), '—');
});

test('missing modified time renders as unavailable', () => {
  assert.equal(formatModifiedTime(null), '—');
});
