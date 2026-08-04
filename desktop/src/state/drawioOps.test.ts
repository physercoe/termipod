import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applyDiagramOperations,
  diagramOpsBytes,
  diagramOpsSummary,
  narrowDiagramOperations,
  scanRoot,
  type DiagramOperation,
} from './drawioOps.ts';

/// Lane D1 — `author_apply {mode:'ops'}` for diagrams. Upstream ships no unit
/// tests for `applyDiagramOperations`, and this implementation is not a port
/// (see the module header), so every case here is written fresh.
///
/// Two properties are load-bearing and each has its own group below:
///
///   - **all-or-nothing** — a batch with one bad op changes nothing, because a
///     half-applied batch is a document the user did not ask for;
///   - **byte preservation** — everything outside the named cells survives
///     verbatim, because this writes into the user's own file.

const DOC = [
  '<mxfile host="app">',
  '  <diagram name="Page-1" id="p1">',
  '    <mxGraphModel dx="100" grid="1">',
  '      <root>',
  '        <mxCell id="0"/>',
  '        <mxCell id="1" parent="0"/>',
  '        <mxCell id="n1" value="A" vertex="1" parent="1"><mxGeometry x="10" y="10" as="geometry"/></mxCell>',
  '        <mxCell id="n2" value="B" vertex="1" parent="1"><mxGeometry x="10" y="90" as="geometry"/></mxCell>',
  '        <mxCell id="e1" edge="1" parent="1" source="n1" target="n2"><mxGeometry relative="1" as="geometry"/></mxCell>',
  '      </root>',
  '    </mxGraphModel>',
  '  </diagram>',
  '</mxfile>',
].join('\n');

function ops(...list: [kind: 'add' | 'update' | 'delete', id: string, xml?: string][]): DiagramOperation[] {
  return list.map(([operation, cell_id, new_xml]) => ({ operation, cell_id, new_xml: new_xml ?? '' }));
}

function applied(xml: string, list: DiagramOperation[]): string {
  const r = applyDiagramOperations(xml, list);
  assert.equal(r.ok, true, r.ok ? '' : r.message);
  return r.ok ? r.result.xml : '';
}

function refused(xml: string, list: DiagramOperation[]): string {
  const r = applyDiagramOperations(xml, list);
  assert.equal(r.ok, false, 'expected a refusal');
  return r.ok ? '' : r.message;
}

// ── scanRoot: reading the document as text ──────────────────────────────────

test('scanRoot splits root children and round-trips the document byte for byte', () => {
  const s = scanRoot(DOC);
  assert.equal(s.ok, true);
  if (!s.ok) return;
  assert.deepEqual(
    s.scan.entries.map((e) => e.id),
    ['0', '1', 'n1', 'n2', 'e1'],
  );
  const rebuilt = s.scan.head + s.scan.entries.map((e) => e.lead + e.text).join('') + s.scan.tailLead + s.scan.tail;
  assert.equal(rebuilt, DOC);
});

test('scanRoot reads the graph links off an <object> wrapper, id included', () => {
  const s = scanRoot('<root><object label="L" id="n9"><mxCell vertex="1" parent="1"/></object></root>');
  assert.equal(s.ok, true);
  if (!s.ok) return;
  const [e] = s.scan.entries;
  assert.equal(e.id, 'n9');
  assert.equal(e.parent, '1');
});

test('an id attribute is read from the tag, never out of another attribute value', () => {
  // draw.io's HTML labels escape < and > but not the inner single quotes, so
  // the literal text ` id='trap'` survives inside the value. A per-name regex
  // returns `trap`; walking the tag knows it is inside a value.
  const s = scanRoot(`<root><mxCell value="&lt;p id='trap'&gt;Hi&lt;/p&gt;" id="real" parent="1"/></root>`);
  assert.equal(s.ok, true);
  if (!s.ok) return;
  assert.equal(s.scan.entries[0].id, 'real');
});

test('an escaped id is compared in its decoded form', () => {
  const doc = '<root><mxCell id="a&amp;b" parent="1"/></root>';
  // `author_read` hands the agent `a&b`; sending that back must find the cell.
  assert.match(applied(doc, ops(['delete', 'a&b'])), /^<root><\/root>$/);
});

test('a quoted > inside a style does not end the tag early', () => {
  const doc = '<root><mxCell id="n1" style="shape=x;a>b" parent="1"/></root>';
  const s = scanRoot(doc);
  assert.equal(s.ok, true);
  if (!s.ok) return;
  assert.equal(s.scan.entries.length, 1);
  assert.equal(s.scan.entries[0].id, 'n1');
});

test('comments and CDATA inside root are carried through, not parsed as cells', () => {
  const doc = '<root><!-- <mxCell id="ghost"/> --><mxCell id="n1" parent="1"/></root>';
  const s = scanRoot(doc);
  assert.equal(s.ok, true);
  if (!s.ok) return;
  assert.deepEqual(s.scan.entries.map((e) => e.id), ['n1']);
  assert.equal(applied(doc, ops(['delete', 'n1'])), '<root><!-- <mxCell id="ghost"/> --></root>');
});

test('a document with no <root> is refused with a diagnosis, not silently rewritten', () => {
  const msg = refused('<mxfile><diagram>7VpbU9s4FP41ec...</diagram></mxfile>', ops(['delete', 'n1']));
  assert.match(msg, /uncompressed/);
  assert.match(msg, /mode:'replace'/);
});

test('a multi-page mxfile is refused: cell ids are only unique within a page', () => {
  const two = DOC.replace('</diagram>', '</diagram>\n  <diagram name="Page-2" id="p2"><mxGraphModel><root><mxCell id="n1"/></root></mxGraphModel></diagram>');
  const msg = refused(two, ops(['delete', 'n1']));
  assert.match(msg, /2 pages/);
});

test('a document with duplicate ids is refused rather than edited at random', () => {
  const dup = DOC.replace('id="n2"', 'id="n1"');
  assert.match(refused(dup, ops(['update', 'n1', '<mxCell id="n1"/>'])), /more than one cell with id "n1"/);
});

// ── update ──────────────────────────────────────────────────────────────────

test('update replaces exactly one cell and leaves every other byte alone', () => {
  const out = applied(DOC, ops(['update', 'n1', '<mxCell id="n1" value="A2" vertex="1" parent="1"><mxGeometry x="10" y="10" as="geometry"/></mxCell>']));
  assert.equal(out, DOC.replace('value="A"', 'value="A2"'));
});

test('update keeps the indentation the cell was on', () => {
  const out = applied(DOC, ops(['update', 'n2', '<mxCell id="n2"/>']));
  assert.match(out, /\n {8}<mxCell id="n2"\/>\n/);
});

test('update of a missing cell refuses and points at author_read', () => {
  const msg = refused(DOC, ops(['update', 'nope', '<mxCell id="nope"/>']));
  assert.match(msg, /not found/);
  assert.match(msg, /author_read/);
});

test('an id that disagrees with new_xml is refused rather than guessed', () => {
  assert.match(refused(DOC, ops(['update', 'n1', '<mxCell id="n2"/>'])), /ID mismatch/);
});

test('new_xml holding two cells is refused, not half-applied', () => {
  // Upstream takes the first mxCell it finds and drops the rest.
  const msg = refused(DOC, ops(['update', 'n1', '<mxCell id="n1"/><mxCell id="zz"/>']));
  assert.match(msg, /2 elements/);
});

test('new_xml that is not an element at all is refused', () => {
  assert.match(refused(DOC, ops(['update', 'n1', 'just text'])), /must contain an mxCell/);
  assert.match(refused(DOC, ops(['update', 'n1', '<mxCell id="n1">'])), /well-formed/);
});

test("the layer cells are not content: update of 0 or 1 refuses", () => {
  assert.match(refused(DOC, ops(['update', '1', '<mxCell id="1"/>'])), /layer cells/);
});

// ── add ─────────────────────────────────────────────────────────────────────

test('add appends the cell and inherits the file indentation', () => {
  const out = applied(DOC, ops(['add', 'n3', '<mxCell id="n3" value="C" vertex="1" parent="1"/>']));
  assert.match(out, /\n {8}<mxCell id="n3" value="C" vertex="1" parent="1"\/>\n {6}<\/root>/);
  assert.equal(out.replace(/\n {8}<mxCell id="n3"[^\n]*/, ''), DOC);
});

test('add of an existing id refuses and names the operation that would work', () => {
  assert.match(refused(DOC, ops(['add', 'n1', '<mxCell id="n1"/>'])), /already exists.*"update"/);
});

test('add into an empty <root/> works — a blank page still takes a cell', () => {
  const out = applied('<mxfile><diagram><mxGraphModel><root/></mxGraphModel></diagram></mxfile>', ops(['add', 'n1', '<mxCell id="n1" parent="1"/>']));
  assert.equal(out, '<mxfile><diagram><mxGraphModel><root><mxCell id="n1" parent="1"/></root></mxGraphModel></diagram></mxfile>');
});

// ── delete + cascade ────────────────────────────────────────────────────────

test('deleting a vertex takes the edges that hang off it', () => {
  const r = applyDiagramOperations(DOC, ops(['delete', 'n1']));
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.result.deleted.sort(), ['e1', 'n1']);
  assert.deepEqual(r.result.cascaded, ['e1']);
  assert.doesNotMatch(r.result.xml, /id="n1"|id="e1"/);
  assert.match(r.result.xml, /id="n2"/);
});

test('the cascade reaches a group member and its edges, transitively', () => {
  const doc = [
    '<root>',
    '<mxCell id="0"/><mxCell id="1" parent="0"/>',
    '<mxCell id="g" vertex="1" parent="1"/>',
    '<mxCell id="c" vertex="1" parent="g"/>',
    '<mxCell id="out" vertex="1" parent="1"/>',
    '<mxCell id="ec" edge="1" parent="1" source="c" target="out"/>',
    '</root>',
  ].join('');
  const r = applyDiagramOperations(doc, ops(['delete', 'g']));
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.result.deleted.sort(), ['c', 'ec', 'g']);
  assert.match(r.result.xml, /id="out"/);
});

test('a layer cell that points into content cannot be dragged out by the cascade', () => {
  // draw.io would never write this, and a `mode:'replace'` body can perfectly
  // well contain it — nothing validates that a parent makes sense. Without the
  // guard, deleting n1 takes the layer and orphans every cell hanging off it.
  const doc = '<root><mxCell id="0"/><mxCell id="1" parent="n1"/><mxCell id="n1" parent="1"/><mxCell id="keep" parent="1"/></root>';
  const r = applyDiagramOperations(doc, ops(['delete', 'n1']));
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.match(r.result.xml, /id="0"/);
  assert.match(r.result.xml, /id="1"/);
  assert.match(r.result.xml, /id="keep"/);
  assert.deepEqual(r.result.deleted, ['n1']);
});

test('the cascade follows references INTO the deleted cell, not out of it', () => {
  // An edge attached to n1 goes; the cells that edge also touches stay. The
  // direction is the whole rule — the other reading would delete the diagram.
  const doc = '<root><mxCell id="0"/><mxCell id="1" parent="0"/><mxCell id="n1" parent="1"/><mxCell id="n2" parent="1"/><mxCell id="e" edge="1" parent="1" source="n1" target="n2"/></root>';
  const r = applyDiagramOperations(doc, ops(['delete', 'n1']));
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.result.deleted.sort(), ['e', 'n1']);
  assert.match(r.result.xml, /id="n2"/);
});

test('the root cells cannot be deleted', () => {
  assert.match(refused(DOC, ops(['delete', '0'])), /Cannot delete root cell/);
  assert.match(refused(DOC, ops(['delete', '1'])), /Cannot delete root cell/);
});

test('deleting a cell the same batch already cascaded away is a no-op, not an error', () => {
  // The agent was RIGHT about the document: e1 does hang off n1. Erroring here
  // would punish a correct model for being explicit.
  const r = applyDiagramOperations(DOC, ops(['delete', 'n1'], ['delete', 'e1']));
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.result.deleted.sort(), ['e1', 'n1']);
});

test('deleting an id that never existed is an error — upstream skips every miss silently', () => {
  assert.match(refused(DOC, ops(['delete', 'typo'])), /not found/);
});

// ── all-or-nothing ──────────────────────────────────────────────────────────

test('one bad op aborts the batch: the good ops before it do not land', () => {
  const r = applyDiagramOperations(DOC, ops(['delete', 'n2'], ['update', 'ghost', '<mxCell id="ghost"/>']));
  assert.equal(r.ok, false);
  // and the input is untouched — the caller writes `doc.body` only on ok
  assert.match(DOC, /id="n2"/);
});

test('an empty operations list is refused rather than reported as a successful no-op', () => {
  assert.match(refused(DOC, []), /operations is empty/);
});

test('an operation with no cell_id is refused', () => {
  assert.match(refused(DOC, ops(['delete', ''])), /cell_id is required/);
});

// ── ordering ────────────────────────────────────────────────────────────────

test('ops run against the state the earlier ones left: delete then add is a replace', () => {
  const r = applyDiagramOperations(DOC, ops(['delete', 'n2'], ['add', 'n2', '<mxCell id="n2" value="B2" vertex="1" parent="1"/>']));
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.match(r.result.xml, /id="n2" value="B2"/);
  // e1 pointed at n2 and went with it; the re-added cell does not bring it back
  assert.doesNotMatch(r.result.xml, /id="e1"/);
});

// ── what the agent is told ──────────────────────────────────────────────────

test('the summary names the cascade separately from what was asked for', () => {
  const r = applyDiagramOperations(DOC, ops(['delete', 'n1']));
  assert.equal(r.ok, true);
  if (!r.ok) return;
  const s = diagramOpsSummary(r.result);
  assert.match(s, /deleted /);
  assert.match(s, /e1 was removed by cascade/);
});

test('a summary with no cascade does not mention one', () => {
  const r = applyDiagramOperations(DOC, ops(['update', 'n1', '<mxCell id="n1" value="Z"/>']));
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.equal(diagramOpsSummary(r.result), 'updated n1');
});

// ── narrowing ───────────────────────────────────────────────────────────────

test('narrowing accepts a well-formed batch and normalises new_xml for delete', () => {
  const n = narrowDiagramOperations([
    { operation: 'delete', cell_id: 'n1' },
    { operation: 'add', cell_id: 'n3', new_xml: '<mxCell id="n3"/>' },
  ]);
  assert.equal(n.ok, true);
  if (!n.ok) return;
  assert.deepEqual(n.ops, [
    { operation: 'delete', cell_id: 'n1', new_xml: '' },
    { operation: 'add', cell_id: 'n3', new_xml: '<mxCell id="n3"/>' },
  ]);
});

test('narrowing names the index and the field it refused', () => {
  const notArray = narrowDiagramOperations('nope');
  assert.equal(notArray.ok, false);
  if (notArray.ok) return;
  assert.match(notArray.message, /must be an array/);
  const bad = narrowDiagramOperations([{ operation: 'add', cell_id: 'n1' }]);
  assert.equal(bad.ok, false);
  if (bad.ok) return;
  assert.match(bad.message, /operations\[0\]\.new_xml is required/);
  const worse = narrowDiagramOperations([{ operation: 'move', cell_id: 'n1' }]);
  assert.equal(worse.ok, false);
  if (worse.ok) return;
  assert.match(worse.message, /'add', 'update' or 'delete'/);
});

test('the size a batch counts is what the agent sent, not the diagram it edits', () => {
  assert.equal(diagramOpsBytes(ops(['delete', 'n1'])), 2);
  assert.equal(diagramOpsBytes(ops(['add', 'ab', '<x/>'])), 6);
});
