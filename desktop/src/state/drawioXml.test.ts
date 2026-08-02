import { test } from 'node:test';
import assert from 'node:assert/strict';
import { domCheck, prepareDiagramBody, structuralCheck, validateMxCell, wrapWithMxFile } from './drawioXml.ts';

/// The draw.io XML validator (coworking C1). Upstream (next-ai-draw-io) ships
/// no unit tests for `lib/utils.ts` — only Playwright e2e specs — so there were
/// no test cases to keep and these are written here.
///
/// Every case below is a body an agent plausibly emits. That is the point: the
/// validator's job is to refuse a bad `author_apply` with a diagnosis the agent
/// can act on, so a check that never fires on real model output is dead weight
/// and a check that misfires blocks a legitimate write.

const CELLS = '<mxCell id="a" value="A" vertex="1" parent="1"><mxGeometry as="geometry"/></mxCell>';

// ── structuralCheck: one test per refusal ───────────────────────────────────

test('a well-formed cell list passes every structural check', () => {
  assert.equal(structuralCheck(wrapWithMxFile(CELLS)), null);
});

test('CDATA at the root is refused', () => {
  const err = structuralCheck('<![CDATA[<mxCell id="a"/>]]>');
  assert.match(String(err), /CDATA/);
});

test('a duplicated structural attribute is refused; a duplicated cosmetic one is not', () => {
  assert.match(String(structuralCheck('<mxCell id="a" id="b"/>')), /Duplicate structural attribute/);
  // `style` twice is sloppy, not structural — refusing it would block writes
  // that draw.io itself accepts.
  assert.equal(structuralCheck('<mxCell id="a" style="x" style="y"/>'), null);
});

test('an unescaped < inside an attribute value is refused', () => {
  assert.match(String(structuralCheck('<mxCell id="a" value="x < y"/>')), /Unescaped </);
  assert.equal(structuralCheck('<mxCell id="a" value="x &lt; y"/>'), null);
});

test('duplicate ids are refused and the message names them', () => {
  const err = String(structuralCheck('<mxCell id="a"/><mxCell id="a"/>'));
  assert.match(err, /duplicate ID/i);
  assert.match(err, /'a' \(2x\)/);
});

test('an unclosed tag is refused, and a quoted > does not fake one', () => {
  assert.match(String(structuralCheck('<root><mxCell id="a">')), /unclosed tag/);
  // The reason `parseXmlTags` tracks quotes: a naive tag split cuts this
  // style value in half and reports a mismatch that is not there.
  assert.equal(structuralCheck('<mxCell id="a" style="shape=x;html=1;a>b"/>'), null);
});

test('a mismatched closing tag names both tags', () => {
  assert.match(String(structuralCheck('<root><mxCell id="a"></root>')), /Expected closing tag <\/mxCell> but found <\/root>/);
});

test('malformed character references are refused', () => {
  assert.match(String(structuralCheck('<mxCell id="a" value="&#x2G;"/>')), /hex character reference/);
  assert.equal(structuralCheck('<mxCell id="a" value="&#x2013;"/>'), null);
  assert.equal(structuralCheck('<mxCell id="a" value="&#8212;"/>'), null);
});

test('a bare ampersand is refused with the fix in the message', () => {
  assert.match(String(structuralCheck('<mxCell id="a" value="Tom & Jerry"/>')), /Replace & with &amp;/);
  assert.equal(structuralCheck('<mxCell id="a" value="Tom &amp; Jerry"/>'), null);
});

test('an HTML entity draw.io does not accept is refused', () => {
  // `&nbsp;` is caught by the BARE-AMPERSAND check, not the entity-name one:
  // the lookahead `&(?!lt|gt|amp|quot|apos|#)` matches at `&n`. The named-entity
  // message is reachable only for entities that begin with a valid prefix.
  assert.match(String(structuralCheck('<mxCell id="a" value="&nbsp;"/>')), /Replace & with &amp;/);
  assert.match(String(structuralCheck('<mxCell id="a" value="&ltx;"/>')), /Invalid entity reference: &ltx;/);
});

test('a double hyphen inside a comment is refused', () => {
  assert.match(String(structuralCheck('<root><!-- a -- b --></root>')), /double hyphen/);
});

test('an empty mxCell id is refused', () => {
  assert.match(String(structuralCheck('<mxCell id="" value="x"></mxCell>')), /empty id attribute/);
});

test('a nested mxCell is refused, but a cell geometry child is not', () => {
  assert.match(
    String(structuralCheck('<mxCell id="a"><mxCell id="b"></mxCell></mxCell>')),
    /nested mxCell/,
  );
  assert.equal(structuralCheck(CELLS), null, "a geometry child is a cell's own, not a nested cell");
});

test('a SELF-CLOSING nested cell is caught by the DOM half only — the two checks are not equivalent', () => {
  // `checkNestedMxCells` tracks a stack of OPENING cell tags, and a
  // self-closing one opens nothing, so it slips past. The DOM check catches it
  // by parentElement. Worth pinning because it inverts the usual reading: the
  // structural checks are not a superset of the DOM check, and under
  // `node --test` (no DOMParser) this body validates clean.
  const body = '<root><mxCell id="a" vertex="1"><mxCell id="b" vertex="1"/></mxCell></root>';
  assert.equal(structuralCheck(body), null);
  assert.match(String(domCheck(body, stubParser({ nested: 'b' }))), /nested mxCell/);
});

// ── domCheck ────────────────────────────────────────────────────────────────

/// A stub standing in for the renderer's DOMParser. Node has none, so without
/// this the suite would silently only ever exercise the structural half — the
/// reason `domCheck` takes the constructor rather than reaching for a global.
function stubParser(result: { error?: boolean; nested?: string }): typeof DOMParser {
  class P {
    parseFromString(): unknown {
      return {
        querySelector: (sel: string) => (sel === 'parsererror' && result.error === true ? {} : null),
        querySelectorAll: () =>
          result.nested !== undefined
            ? [{ parentElement: { tagName: 'mxCell' }, getAttribute: () => result.nested }]
            : [],
      };
    }
  }
  return P as unknown as typeof DOMParser;
}

test('domCheck reports "no answer" when there is no parser, never "valid"', () => {
  // The distinction matters: `undefined` lets the caller fall through to the
  // structural checks. Returning null here would mean "the DOM says this is
  // fine" on a machine that never looked.
  assert.equal(domCheck('<x/>', undefined), undefined);
});

test('domCheck surfaces a parser error as the escaping diagnosis', () => {
  assert.match(String(domCheck('<x', stubParser({ error: true }))), /escape special characters/);
});

test('domCheck names the nested cell it found', () => {
  assert.match(String(domCheck('<x/>', stubParser({ nested: 'b7' }))), /id="b7"/);
});

test('domCheck passes a clean document', () => {
  assert.equal(domCheck(CELLS, stubParser({})), null);
});

test('validateMxCell runs the structural checks even when a DOM parser passed the document', () => {
  // Not a fallback chain: a well-formed document can still have duplicate ids,
  // and a stub that says "fine" must not be able to wave one through.
  const dupes = '<root><mxCell id="a"/><mxCell id="a"/></root>';
  assert.match(String(validateMxCell(dupes, stubParser({}))), /duplicate ID/i);
});

test('an oversized document is refused rather than swept', () => {
  const err = validateMxCell('<mxCell/>'.padEnd(1_000_001, ' '), undefined);
  assert.match(String(err), /over the 1000000-byte limit/);
});

// ── wrapWithMxFile ──────────────────────────────────────────────────────────

test('an empty body becomes a valid empty diagram', () => {
  const out = wrapWithMxFile('   ');
  assert.match(out, /^<mxfile>/);
  assert.equal(validateMxCell(out, undefined), null);
});

test('a complete mxfile is returned untouched', () => {
  const full = '<mxfile><diagram/></mxfile>';
  assert.equal(wrapWithMxFile(full), full);
});

test('an mxGraphModel gains only the mxfile wrapper', () => {
  assert.equal(
    wrapWithMxFile('<mxGraphModel><root/></mxGraphModel>'),
    '<mxfile><diagram name="Page-1" id="page-1"><mxGraphModel><root/></mxGraphModel></diagram></mxfile>',
  );
});

test("the agent's own root cells are dropped so ours cannot collide", () => {
  // A model that helpfully includes draw.io's layer scaffold would otherwise
  // produce duplicate id="0"/id="1" and fail validation on its own good manners.
  const out = wrapWithMxFile(`<root><mxCell id="0"/><mxCell id="1" parent="0"/>${CELLS}</root>`);
  assert.equal(out.match(/id="0"/g)?.length, 1);
  assert.equal(out.match(/id="1"/g)?.length, 1);
  assert.equal(validateMxCell(out, undefined), null);
});

test('a trailing wrapper of closing tags is stripped', () => {
  const out = wrapWithMxFile(`${CELLS}</diagram></mxfile>`);
  assert.ok(!out.includes('</diagram></mxfile></root>'));
  assert.equal(validateMxCell(out, undefined), null);
});

test('trailing CONTENT is kept, not silently dropped', () => {
  // Only closing tags are stripped. Anything else after the last cell is
  // something the agent meant — the validator refuses it and says why, which
  // beats writing a document quietly missing a piece.
  const out = wrapWithMxFile(`${CELLS} some stray text`);
  assert.ok(out.includes('some stray text'));
});

// ── prepareDiagramBody ──────────────────────────────────────────────────────

test('a cell wearing a scaffold id is stripped rather than written through', () => {
  const res = prepareDiagramBody('<mxCell id="1" value="mine" vertex="1" parent="0"/>');
  assert.equal(res.error, null);
  assert.ok(res.xml !== null && !res.xml.includes('mine'));
});

test('prepareDiagramBody validates the WRAPPED body — validating the raw input would let a duplicate id through', () => {
  // `id = "1"` with spaces: the scaffold-stripping regex requires a bare `id=`
  // and misses it, while `checkDuplicateIds` tolerates the spaces. So this
  // body is VALID on its own and INVALID once wrapped — the one shape that
  // tells the two orderings apart.
  const spaced = '<mxCell id = "1" value="mine" vertex="1" parent="0"/>';
  assert.equal(validateMxCell(spaced, undefined), null, 'valid before wrapping');
  const res = prepareDiagramBody(spaced);
  assert.equal(res.xml, null);
  assert.match(String(res.error), /duplicate ID/i);
});

test('prepareDiagramBody returns a diagnosis and NO xml when the body is bad', () => {
  const res = prepareDiagramBody('<mxCell id="a" value="Tom & Jerry"/>');
  assert.equal(res.xml, null);
  assert.match(String(res.error), /Replace & with &amp;/);
});

test('prepareDiagramBody returns the wrapped xml and no error when the body is good', () => {
  const res = prepareDiagramBody(CELLS);
  assert.equal(res.error, null);
  assert.match(String(res.xml), /^<mxfile>/);
  assert.ok(String(res.xml).includes('value="A"'));
});
