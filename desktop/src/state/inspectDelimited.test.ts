import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compareDelimitedCells, matchesDelimitedQuery, parseDelimited } from './inspectDelimited.ts';

test('CSV preview handles quoting, escaped quotes, embedded commas and newlines', () => {
  const parsed = parseDelimited('name,note,count\nalpha,"hello, world",10\nbeta,"two\nlines and ""quotes""",2\n', ',');
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.deepEqual(parsed.table.headers, ['name', 'note', 'count']);
  assert.deepEqual(parsed.table.rows, [
    { number: 1, cells: ['alpha', 'hello, world', '10'] },
    { number: 2, cells: ['beta', 'two\nlines and "quotes"', '2'] },
  ]);
  assert.equal(parsed.table.unevenRows, 0);
});

test('TSV preview uses tabs as delimiters while preserving commas', () => {
  const parsed = parseDelimited('name\tvalue\nitem\t1,200\n', '\t');
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.deepEqual(parsed.table.rows[0].cells, ['item', '1,200']);
});

test('ragged rows are padded and reported rather than silently discarded', () => {
  const parsed = parseDelimited('a,b,c\n1,2\n3,4,5,6\n', ',');
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.deepEqual(parsed.table.headers, ['a', 'b', 'c', 'Column 4']);
  assert.deepEqual(parsed.table.rows[0].cells, ['1', '2', '', '']);
  assert.deepEqual(parsed.table.rows[1].cells, ['3', '4', '5', '6']);
  assert.equal(parsed.table.unevenRows, 2);
});

test('empty headers receive stable visible labels', () => {
  const parsed = parseDelimited(',name,\n1,a,3', ',');
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.deepEqual(parsed.table.headers, ['Column 1', 'name', 'Column 3']);
});

test('malformed quotes fail honestly so the UI can keep source available', () => {
  const unclosed = parseDelimited('a,b\n1,"two', ',');
  assert.equal(unclosed.ok, false);
  if (!unclosed.ok) assert.match(unclosed.message, /not closed/);

  const trailing = parseDelimited('a,b\n1,"two"x', ',');
  assert.equal(trailing.ok, false);
  if (!trailing.ok) assert.match(trailing.message, /closing quote/);
});

test('sorting compares numeric cells numerically and text naturally', () => {
  assert.ok(compareDelimitedCells('2', '10') < 0);
  assert.ok(compareDelimitedCells('file2', 'file10') < 0);
  assert.ok(compareDelimitedCells('Beta', 'alpha') > 0);
});

test('filtering is case-insensitive and searches every cell', () => {
  const row = { number: 7, cells: ['Alpha', 'New York', '42'] };
  assert.equal(matchesDelimitedQuery(row, 'alpha'), true);
  assert.equal(matchesDelimitedQuery(row, 'YORK'), true);
  assert.equal(matchesDelimitedQuery(row, 'missing'), false);
  assert.equal(matchesDelimitedQuery(row, '  '), true);
});
