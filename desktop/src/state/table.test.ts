import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseTable, reconcileExternalTable, serializeTable, tableBodyToCsv } from './table.ts';

/// The table read-only guard (coworking A5). `parseTable` used to answer an
/// unparseable body with a blank three-row grid and no signal, and
/// `TableEditor.mutate` serializes on EVERY change — so one click on a table
/// document whose body failed to parse wrote the blank grid over it.
///
/// That was live with no agent involved. `author_apply` would have made it
/// routine: the write reports success and the rows are gone.

const GOOD = '{"columns":[{"id":"c0","name":"Name","type":"text"}],"rows":[{"id":"r0","cells":{"c0":"a"}}]}';

test('a real table body parses and is NOT read-only', () => {
  const d = parseTable(GOOD, 'Name');
  assert.equal(d.readOnly, undefined);
  assert.equal(d.rows.length, 1);
  assert.equal(d.columns.length, 1);
});

test('an EMPTY body is a new document — editable, not read-only', () => {
  // Nothing to lose, so this must stay writable; treating it as unreadable
  // would make every freshly-created table permanently inert.
  for (const body of ['', '   ', '\n']) {
    assert.equal(parseTable(body, 'Name').readOnly, undefined, `'${body}' should open editable`);
  }
});

test('unparseable JSON opens READ-ONLY rather than as a blank editable grid', () => {
  const d = parseTable('{"columns":[{"id":"c0"', 'Name');
  assert.equal(d.readOnly, true, 'without this the next click serializes a blank grid over the user rows');
});

test('valid JSON of the wrong shape is read-only too', () => {
  // Content we can read but not interpret is still content. A bare array or an
  // object without columns/rows is somebody's data, not an empty table.
  for (const body of ['[]', '{"foo":1}', '"just a string"', 'null', '{"columns":[]}']) {
    assert.equal(parseTable(body, 'Name').readOnly, true, `${body} should open read-only`);
  }
});

test('a read-only parse still yields a renderable grid', () => {
  // The editor renders `columns`/`rows` before it checks the flag, so an
  // unreadable body must not produce a shape that throws on the way to the
  // banner explaining it.
  const d = parseTable('not json at all', 'Name');
  assert.ok(Array.isArray(d.columns) && Array.isArray(d.rows));
  assert.equal(d.columns.length, 1);
});

test('a round trip through serialize stays writable', () => {
  const d = parseTable(GOOD, 'Name');
  assert.equal(parseTable(serializeTable(d), 'Name').readOnly, undefined);
});

// ── the second mouth of the same hole ───────────────────────────────────────

test('CSV export of an unreadable table REFUSES instead of writing an empty file', () => {
  // `bodyToFile` lowers a table through the parser on the way out, so before
  // this an unreadable body became a zero-row CSV written over whatever path
  // the user picked in the save dialog.
  assert.throws(() => tableBodyToCsv('{"columns":[', 'Name'), /could not be read/);
});

test('CSV export of a real table still works', () => {
  const csv = tableBodyToCsv(GOOD, 'Name');
  assert.match(csv, /Name/);
  assert.match(csv, /a/);
});

test('an empty body exports the blank starter grid rather than refusing', () => {
  // An empty body is a new document, not an unreadable one — exporting it
  // gives the starter column and its blank rows, not an error.
  const csv = tableBodyToCsv('', 'Name');
  assert.equal(csv.split('\n')[0], 'Name');
  assert.equal(csv.split('\n').length, 4, 'header + the three seed rows');
});

// `bodyToFile` in documents.ts routes `.csv` here and leaves `.json`
// byte-verbatim; that module reaches localStorage and cannot load under
// `node --test`, so the guard lives beside the parser where it can be tested.

/// ---- B4: adopting an external write into a mounted grid ----
///
/// The grid re-emits its own body on every mutation, so the store hands that
/// body straight back as a new `value`. Telling that echo apart from a real
/// external write is the whole job — and the undo stack is where getting it
/// wrong costs the user a document rather than a keystroke.

test('B4: the grid\'s own echo is not adopted', () => {
  // Re-parsing here would build fresh row/column objects on every keystroke and
  // take the caret with them.
  const current = parseTable(GOOD, 'Name');
  assert.equal(reconcileExternalTable(GOOD, GOOD, current, 'Name'), null);
});

test('B4: an external write is adopted and made undoable', () => {
  const current = parseTable(GOOD, 'Name');
  const incoming = '{"columns":[{"id":"c0","name":"Name","type":"text"}],"rows":[]}';
  const step = reconcileExternalTable(GOOD, incoming, current, 'Name');
  assert.notEqual(step, null);
  assert.equal(step?.next.rows.length, 0);
  // Cmd+Z must reach the agent's write — B2's rule for the canvas, here.
  assert.equal(step?.pushUndo, true);
});

test('B4: a read-only PLACEHOLDER is never pushed onto the undo stack', () => {
  // The A5 class arriving through undo. `undo` serializes whatever it pops, so
  // a blank read-only grid on that stack is one keystroke from being written
  // over the document it merely stands in for. Reproduce the sequence: an
  // unreadable body is open, then a real table arrives externally.
  const placeholder = parseTable('{"columns":[', 'Name');
  assert.equal(placeholder.readOnly, true);
  const step = reconcileExternalTable('{"columns":[', GOOD, placeholder, 'Name');
  assert.notEqual(step, null);
  assert.equal(step?.next.readOnly, undefined, 'the good body opens editable');
  assert.equal(step?.pushUndo, false, 'the placeholder must not become an undo target');
});

test('B4: an external write we cannot read is adopted as read-only, not as a blank grid', () => {
  // Adopting it is right — the document really did change — but it must arrive
  // wearing the A5 flag, so the grid refuses writes instead of serializing a
  // placeholder back over the new bytes.
  const current = parseTable(GOOD, 'Name');
  const step = reconcileExternalTable(GOOD, 'not a table at all', current, 'Name');
  assert.equal(step?.next.readOnly, true);
});
