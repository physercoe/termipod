/// The parts of the interpreter that the cross-language fixture cannot state.
/// Run with `node --test`.
///
/// `parity.test.ts` is the real test of this module: it replays the hub's own
/// corpus and grammar cases and demands the same answers Go gave. What is left
/// for here is exactly what a shared fixture cannot hold — the two escapes
/// where the ports deliberately DISAGREE, and the invariants that are about how
/// this code is called from TypeScript rather than about the rule language.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { evalExpr, isPresent } from './eval.ts';
import { applyProfile } from './translate.ts';

test('byte escapes above ASCII are malformed here, deliberately', () => {
  // `strconv.Unquote` resolves `\xHH` and `\OOO` to raw BYTES. Above 0x7F there
  // is no faithful UTF-16 answer: Go's `"\xff"` is one byte that is not valid
  // UTF-8, and the nearest JS string, U+00FF, is a different value that would
  // travel into a payload field looking correct.
  //
  // These cases are absent from the shared fixture on purpose — recording them
  // would assert a divergence as if it were parity. They are pinned here so the
  // narrowing is a decision on the record and not an oversight someone later
  // "fixes" into a silent mismatch. Every literal in agent_families.yaml is a
  // bare identifier, so nothing shipped reaches this.
  for (const expr of ['"\\xff"', '"\\xFF"', '"\\200"', '"\\377"']) {
    assert.equal(evalExpr(expr, {}, null), null, expr);
  }
  // Below 0x80 the byte IS the code point, so both sides agree — and the shared
  // fixture carries these.
  assert.equal(evalExpr('"\\x41"', {}, null), 'A');
  assert.equal(evalExpr('"\\101"', {}, null), 'A');
});

test('a resolved value is never undefined', () => {
  // Go has one nothing (`nil`), which marshals to `null`. TypeScript has two,
  // and the wrong one silently deletes a payload key: `JSON.stringify` drops
  // `{x: undefined}` to `{}`, so an event that should carry an explicit null
  // would travel as one that never had the field.
  for (const expr of ['$.nope', '$.a.b.c', '$.arr[9]', '$$.anything', 'garbage', '']) {
    assert.equal(evalExpr(expr, { a: 1 }, null), null, expr);
    assert.notEqual(evalExpr(expr, { a: 1 }, null), undefined, expr);
  }
});

test('isPresent counts zero as a measurement and false as an absence', () => {
  // The predicate that `thought.signature_present` is built on. Stated here as
  // the invariant rather than only exercised through expressions, because both
  // halves are counter-intuitive in JS terms: `0` is falsy but present, and
  // `false` is a value but absent.
  assert.equal(isPresent(0), true);
  assert.equal(isPresent(false), false);
  assert.equal(isPresent(''), false);
  assert.equal(isPresent([]), false);
  assert.equal(isPresent({}), false);
  assert.equal(isPresent(null), false);
  assert.equal(isPresent(undefined), false);
  assert.equal(isPresent('x'), true);
  assert.equal(isPresent([1]), true);
  assert.equal(isPresent({ a: 1 }), true);
});

test('translating does not mutate the frame it was handed', () => {
  // The raw fallback hands the frame straight back as the payload — Go does the
  // same — so a caller that mutates a returned payload is editing the frame. A
  // driver replaying a frame through two profiles must not see the first one's
  // edits, and nothing else in this module writes to its input.
  const frame = { type: 'assistant', message: { content: [{ type: 'text', text: 'hi' }] } };
  const before = JSON.stringify(frame);
  applyProfile(frame, {
    rules: [
      {
        match: { type: 'assistant' },
        for_each: '$.message.content',
        emit: { kind: 'text', payload: { text: '$.text' } },
      },
    ],
  });
  assert.equal(JSON.stringify(frame), before);
});

test('a missing profile falls back to raw rather than throwing', () => {
  // A TS-only call shape: Go takes a `*FrameProfile` that is either nil or
  // valid, where here the profile arrives from JSON and may be null, undefined,
  // or an object whose `rules` never decoded. All three are "no rules", and the
  // frame has to survive all three — losing bytes is the one outcome ADR-010 D5
  // rules out.
  const frame = { type: 'surprise' };
  for (const profile of [null, undefined, {}, { rules: [] }, { profile_version: 1 }]) {
    assert.deepStrictEqual(applyProfile(frame, profile), [
      { kind: 'raw', producer: 'agent', payload: frame },
    ]);
  }
});
