/// Tests for the per-kind body rules an agent write must clear (coworking
/// lane A, ADR-064 D5). The property under test is one sentence: **a body we
/// cannot read is refused, never absorbed.** Every structured kind's parser
/// has a lenient path built for a human opening a file, and every one of those
/// paths is a silent-data-loss bug when an agent's malformed commit takes it.
///
/// Run: node --test src/state/authorBody.test.ts  (CI does NOT run these)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { composeAuthorBody, isExcalidrawBody, rendersFromBody, supportsAppend, supportsOps, validateAuthorBody } from './authorBody.ts';

const TABLE = JSON.stringify({ columns: [{ id: 'c1', name: 'Name', type: 'text' }], rows: [{ id: 'r1', c1: 'a' }] });
const CANVAS = JSON.stringify({ nodes: [], edges: [] });
const SCENE = JSON.stringify({ type: 'excalidraw', elements: [] });

test('markdown takes anything, including empty (it is prose)', () => {
  assert.deepEqual(validateAuthorBody('markdown', ''), { ok: true, body: '' });
  assert.deepEqual(validateAuthorBody('markdown', '# hi'), { ok: true, body: '# hi' });
});

test('every structured kind refuses an empty body', () => {
  // The regression this guards: `parseTable('')` returns a SEEDED grid and
  // `parseCanvas('')` an empty board — both correct for "a human made a new
  // file" and both a blanked document when an agent sends "".
  for (const kind of ['diagram', 'canvas', 'table', 'excalidraw', 'figure'] as const) {
    const out = validateAuthorBody(kind, '   ');
    assert.equal(out.ok, false, kind);
    if (!out.ok) assert.equal(out.code, 'EMPTY_BODY', kind);
  }
});

test('a table body that does not parse is refused, not seeded into a blank grid', () => {
  const out = validateAuthorBody('table', '{ oops');
  assert.equal(out.ok, false);
  if (!out.ok) assert.equal(out.code, 'INVALID_TABLE');
  assert.deepEqual(validateAuthorBody('table', TABLE), { ok: true, body: TABLE });
});

test('a canvas body that does not parse is refused, not opened read-only', () => {
  // parseCanvas answers `readOnly: true` for an unrecognized body — the right
  // human behaviour (look, do not clobber) and the wrong agent behaviour, so
  // the same signal becomes a refusal here.
  const out = validateAuthorBody('canvas', '[1,2,3]');
  assert.equal(out.ok, false);
  if (!out.ok) assert.equal(out.code, 'INVALID_CANVAS');
  assert.deepEqual(validateAuthorBody('canvas', CANVAS), { ok: true, body: CANVAS });
});

test('an excalidraw body must carry the discriminator AND an element array', () => {
  assert.equal(validateAuthorBody('excalidraw', JSON.stringify({ type: 'excalidraw' })).ok, false);
  assert.equal(validateAuthorBody('excalidraw', JSON.stringify({ elements: [] })).ok, false);
  assert.equal(validateAuthorBody('excalidraw', SCENE).ok, true);
});

test('a diagram body is WRAPPED and then validated — the answer is what to store', () => {
  const bare = '<mxGraphModel><root><mxCell id="0"/></root></mxGraphModel>';
  const out = validateAuthorBody('diagram', bare);
  assert.equal(out.ok, true);
  if (out.ok) {
    // The stored body is the wrapped one, not the input: storing the input
    // would leave a document that the editor re-wraps on every open.
    assert.ok(out.body.startsWith('<mxfile'), out.body.slice(0, 40));
    assert.ok(out.body.includes('mxGraphModel'));
  }
});

test('a malformed diagram is refused with the validator diagnosis', () => {
  const out = validateAuthorBody('diagram', '<mxGraphModel><root><mxCell id="a"><mxCell id="a"></mxCell></mxCell>');
  assert.equal(out.ok, false);
  if (!out.ok) {
    assert.equal(out.code, 'INVALID_DIAGRAM');
    // The message is the parser's, not a generic "invalid" — it is what the
    // agent needs to fix the XML on the next attempt.
    assert.notEqual(out.message, '');
  }
});

test('a figure body is accepted: only the renderer can judge it, and it errors visibly', () => {
  // Documented gap, not an oversight — lane B5 adds the dry-run render. The
  // failure mode meanwhile is an inline render error a human sees, not a lost
  // document, which is why it is the one kind allowed through unchecked.
  assert.deepEqual(validateAuthorBody('figure', 'graph TD; a-->b'), { ok: true, body: 'graph TD; a-->b' });
});

test('append is markdown-only, and refuses rather than degrading to replace', () => {
  assert.equal(supportsAppend('markdown'), true);
  for (const kind of ['diagram', 'canvas', 'table', 'figure', 'excalidraw'] as const) {
    assert.equal(supportsAppend(kind), false, kind);
    const out = composeAuthorBody(kind, TABLE, { mode: 'append', body: 'more', operations: [] });
    assert.equal(out.ok, false, kind);
    // Silently treating it as a replace would commit the FRAGMENT as the whole
    // document — the worst reading of an unsupported mode.
    if (!out.ok) assert.equal(out.code, 'MODE_UNSUPPORTED', kind);
  }
});

test('append separates with exactly one blank line, whatever the tail looks like', () => {
  const cases: [string, string][] = [
    ['a', 'a\n\nb'],
    ['a\n', 'a\n\nb'],
    ['a\n\n', 'a\n\nb'],
    ['', 'b'],
  ];
  for (const [current, want] of cases) {
    const out = composeAuthorBody('markdown', current, { mode: 'append', body: 'b', operations: [] });
    assert.equal(out.ok, true);
    if (out.ok) assert.equal(out.body, want, JSON.stringify(current));
  }
});

test('replace ignores the current body for every kind', () => {
  const out = composeAuthorBody('table', TABLE, { mode: 'replace', body: '{}', operations: [] });
  assert.deepEqual(out, { ok: true, body: '{}' });
});

test('rendersFromBody is true exactly for the kinds whose editor reconciles value', () => {
  // Pinned because it is what `applied_live` vs `applied_store_only` reports:
  // a wrong `true` tells the user to look at a screen that did not change.
  assert.equal(rendersFromBody('markdown'), true);
  assert.equal(rendersFromBody('figure'), true);
  // `table` joined them in B4 — its grid now adopts an external `value`.
  assert.equal(rendersFromBody('table'), true);
  // The three whose editor owns its live state after mount answer `false` and
  // rely on their registered live-apply target instead: diagram (B1), canvas
  // (B2), excalidraw (B3).
  for (const kind of ['excalidraw', 'diagram', 'canvas'] as const) {
    assert.equal(rendersFromBody(kind), false, kind);
  }
});

test('isExcalidrawBody survives non-JSON without throwing', () => {
  assert.equal(isExcalidrawBody('not json'), false);
  assert.equal(isExcalidrawBody('null'), false);
  assert.equal(isExcalidrawBody(SCENE), true);
});

// ── D1: mode 'ops' ──────────────────────────────────────────────────────────

const DIAGRAM = [
  '<mxfile><diagram name="Page-1" id="p1"><mxGraphModel><root>',
  '<mxCell id="0"/><mxCell id="1" parent="0"/>',
  '<mxCell id="n1" value="A" vertex="1" parent="1"/>',
  '<mxCell id="e1" edge="1" parent="1" source="n1" target="n1"/>',
  '</root></mxGraphModel></diagram></mxfile>',
].join('');

test("ops is diagram-only, and refuses rather than degrading to replace", () => {
  assert.equal(supportsOps('diagram'), true);
  for (const kind of ['markdown', 'canvas', 'table', 'figure', 'excalidraw'] as const) {
    assert.equal(supportsOps(kind), false, kind);
    const out = composeAuthorBody(kind, '{}', { mode: 'ops', body: '', operations: [{ operation: 'delete', cell_id: 'n1', new_xml: '' }] });
    assert.equal(out.ok, false, kind);
    if (!out.ok) assert.equal(out.code, 'MODE_UNSUPPORTED', kind);
  }
});

test('ops composes against the CURRENT body and reports what it did', () => {
  const out = composeAuthorBody('diagram', DIAGRAM, {
    mode: 'ops',
    body: '',
    operations: [{ operation: 'update', cell_id: 'n1', new_xml: '<mxCell id="n1" value="A2" vertex="1" parent="1"/>' }],
  });
  assert.equal(out.ok, true);
  if (!out.ok) return;
  assert.equal(out.body, DIAGRAM.replace('value="A"', 'value="A2"'));
  assert.equal(out.note, 'updated n1');
});

test('the cascade is in the note, because the composed body cannot say it', () => {
  const out = composeAuthorBody('diagram', DIAGRAM, {
    mode: 'ops',
    body: '',
    operations: [{ operation: 'delete', cell_id: 'n1', new_xml: '' }],
  });
  assert.equal(out.ok, true);
  if (!out.ok) return;
  assert.doesNotMatch(out.body, /id="e1"/);
  assert.match(String(out.note), /e1 was removed by cascade/);
});

test('a bad op refuses with the engine diagnosis under one code the agent can branch on', () => {
  const out = composeAuthorBody('diagram', DIAGRAM, {
    mode: 'ops',
    body: '',
    operations: [{ operation: 'update', cell_id: 'ghost', new_xml: '<mxCell id="ghost"/>' }],
  });
  assert.equal(out.ok, false);
  if (out.ok) return;
  assert.equal(out.code, 'INVALID_OPERATIONS');
  assert.match(out.message, /not found/);
});

test('the ops result still goes through the kind validator — ops are not a way around it', () => {
  // An op batch can produce a body that parses as XML and is not a legal
  // diagram. `executeAuthorRequest` validates the COMPOSED body, so the check
  // below is the same one a replace gets.
  const composed = composeAuthorBody('diagram', DIAGRAM, {
    mode: 'ops',
    body: '',
    operations: [{ operation: 'add', cell_id: 'n2', new_xml: '<mxCell id="n2" value="x &amp; y" vertex="1" parent="1"/>' }],
  });
  assert.equal(composed.ok, true);
  if (!composed.ok) return;
  assert.equal(validateAuthorBody('diagram', composed.body).ok, true);
  // …and the same path refuses a body the engine let through structurally.
  const bad = validateAuthorBody('diagram', DIAGRAM.replace('id="e1"', 'id="n1"'));
  assert.equal(bad.ok, false);
});
