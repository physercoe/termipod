/// The IPC boundary's argument readers (vision-parity L3a). Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  optionalString,
  readInputPayload,
  readPosture,
  readTail,
  requireCursor,
  requireInputKind,
  requireString,
} from './hostargs.ts';

test('requireString refuses absent, blank and non-string', () => {
  assert.equal(requireString({ a: 'x' }, 'a'), 'x');
  for (const bad of [{}, { a: '' }, { a: '   ' }, { a: 7 }, { a: null }, { a: ['x'] }]) {
    assert.throws(() => requireString(bad as Record<string, unknown>, 'a'), /a is required/);
  }
});

test('optionalString treats blank as absent', () => {
  assert.equal(optionalString({ a: 'x' }, 'a'), 'x');
  assert.equal(optionalString({ a: '  ' }, 'a'), undefined);
  assert.equal(optionalString({}, 'a'), undefined);
  assert.equal(optionalString({ a: 3 }, 'a'), undefined);
});

test('requireCursor refuses what would silently become a full replay', () => {
  assert.equal(requireCursor({ cursor: 7 }), 7);
  // Numeric strings are accepted — the renderer's cursor round-trips through
  // JSON and a "7" is unambiguous.
  assert.equal(requireCursor({ cursor: '7' }), 7);
  // null and '' are the dangerous ones: a blanket Number() turns both into 0,
  // which is a legal cursor meaning "replay everything".
  for (const bad of [{}, { cursor: 'abc' }, { cursor: null }, { cursor: '' }, { cursor: '  ' }, { cursor: {} }, { cursor: [] }, { cursor: true }, { cursor: NaN }]) {
    assert.throws(() => requireCursor(bad as Record<string, unknown>), /cursor must be a number/);
  }
});

test('readTail clamps and ignores nonsense', () => {
  assert.equal(readTail({ tail: 50 }), 50);
  assert.equal(readTail({ tail: 12.7 }), 12);
  assert.equal(readTail({ tail: -5 }), 0);
  assert.equal(readTail({}), undefined);
  assert.equal(readTail({ tail: 'many' }), undefined);
  assert.equal(readTail({ tail: Infinity }), undefined);
});

test('requireInputKind admits exactly the four local kinds', () => {
  for (const ok of ['text', 'approval', 'answer', 'cancel']) {
    assert.equal(requireInputKind({ kind: ok }), ok);
  }
  // `attention_reply` and `attach` are hub concepts the local driver has no
  // equivalent for — refused, not silently coerced to text.
  for (const bad of ['attention_reply', 'attach', 'set_mode', '', undefined, 7]) {
    assert.throws(() => requireInputKind({ kind: bad }), /unsupported input kind/);
  }
});

test('readPosture passes the three postures and refuses anything else', () => {
  assert.equal(readPosture({}), undefined);
  assert.equal(readPosture({ posture: 'converse' }), 'converse');
  assert.equal(readPosture({ posture: 'unrestricted' }), 'unrestricted');
  // A typo must not fall back to a default — least of all to a permissive one.
  assert.throws(() => readPosture({ posture: 'full' }), /unknown tool posture/);
  assert.throws(() => readPosture({ posture: 7 }), /unknown tool posture/);
});

test('readInputPayload keeps only the fields the frame builder knows', () => {
  const out = readInputPayload({
    payload: {
      body: 'hello',
      request_id: 'tu_1',
      decision: 'allow',
      note: 'n',
      reason: 'r',
      // Not part of the input vocabulary. A spread would carry these into the
      // frame builder, where a future field name could collide with one.
      type: 'user',
      role: 'system',
      __proto__: { polluted: true },
    },
  });
  assert.deepEqual(out, { body: 'hello', request_id: 'tu_1', decision: 'allow', note: 'n', reason: 'r' });
});

test('non-string scalars are dropped rather than coerced', () => {
  assert.deepEqual(readInputPayload({ payload: { body: 42, decision: null } }), {});
});

test('a missing or non-object payload reads as empty', () => {
  assert.deepEqual(readInputPayload({}), {});
  assert.deepEqual(readInputPayload({ payload: null }), {});
  assert.deepEqual(readInputPayload({ payload: 'text' }), {});
});

test('attachments need both mime and data', () => {
  const out = readInputPayload({
    payload: {
      images: [
        { mime: 'image/png', data: 'A' },
        { mime: 'image/png' }, // no data — an empty block the engine rejects
        { data: 'B' }, // no mime — one it cannot decode
        null,
        'nope',
        ['x'],
        { mime: 'image/png', data: 'C', filename: 'shot.png' },
      ],
    },
  });
  assert.deepEqual(out.images, [
    { mime: 'image/png', data: 'A' },
    { mime: 'image/png', data: 'C', filename: 'shot.png' },
  ]);
});

test('an empty attachment list is omitted, not sent as []', () => {
  const out = readInputPayload({ payload: { body: 'x', images: [], pdfs: [{ nope: 1 }] } });
  assert.deepEqual(out, { body: 'x' });
});

test('a non-array attachment field is ignored', () => {
  assert.deepEqual(readInputPayload({ payload: { images: 'AAAA' } }), {});
});
