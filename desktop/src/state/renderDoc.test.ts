import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EXPORT_TIMEOUT_MS, RENDER_TRANSPORT_TIMEOUT_MS } from './renderDeadlines.ts';
import {
  decodeDataUri,
  mimeForFormat,
  narrowRenderFormat,
  RENDER_BASE64_MAX,
  renderKindRefusal,
  renderNotOpenRefusal,
  renderPathFor,
  renderResultText,
  renderTooLargeRefusal,
  svgFromDataUri,
} from './renderDoc.ts';

/// `author_render`'s decisions (coworking W2). The drawing itself needs a DOM
/// and two vendor libraries, so it lives in `renderDocHost.ts`; everything a
/// test can reach is here.
///
/// Run: node --test src/state/renderDoc.test.ts  (CI does NOT run these)

test('every document kind has an answer, and the three renderable ones are named', () => {
  // Pinned as an exhaustive map rather than a spot check: a kind added to
  // `DocKind` and not to this switch is a TypeScript error today, and a kind
  // silently answering the wrong path is a picture of the wrong thing.
  assert.equal(renderPathFor('figure'), 'body');
  assert.equal(renderPathFor('excalidraw'), 'body');
  // The one kind only its editor can draw — draw.io owns the model.
  assert.equal(renderPathFor('diagram'), 'live');
  for (const kind of ['markdown', 'table', 'canvas'] as const) {
    assert.equal(renderPathFor(kind), null, kind);
  }
});

test('the refusal for an undrawable kind names the recovery, not just the fault', () => {
  const table = renderKindRefusal('table');
  assert.match(table, /text, not a drawing/);
  assert.match(table, /author_read/);
  // Canvas is a drawing that has no exporter yet, so its sentence says that
  // rather than calling a board "text" — an agent told the wrong reason draws
  // the wrong conclusion about what to try next.
  const canvas = renderKindRefusal('canvas');
  assert.match(canvas, /no renderer yet/);
  assert.doesNotMatch(canvas, /text, not a drawing/);
});

test('a closed diagram refuses with the title and the way out', () => {
  const msg = renderNotOpenRefusal('Architecture');
  assert.match(msg, /Architecture/);
  assert.match(msg, /not open in Author/);
  assert.match(msg, /Ask the user to open it/);
});

test('format narrowing accepts exactly two values', () => {
  assert.equal(narrowRenderFormat('svg'), 'svg');
  assert.equal(narrowRenderFormat('png'), 'png');
  for (const bad of ['jpeg', 'PNG', '', null, undefined, 3, ['svg']]) {
    assert.equal(narrowRenderFormat(bad), null, JSON.stringify(bad));
  }
  assert.equal(mimeForFormat('svg'), 'image/svg+xml');
  assert.equal(mimeForFormat('png'), 'image/png');
});

test('the over-cap refusal steers png callers to svg and says why', () => {
  const png = renderTooLargeRefusal(RENDER_BASE64_MAX + 1, 'png');
  assert.match(png, /ask for format 'svg'/);
  // An svg that is already over the cap has no smaller format to fall back to,
  // so it gets different advice rather than "ask for svg" pointing at itself.
  const svg = renderTooLargeRefusal(RENDER_BASE64_MAX + 1, 'svg');
  assert.doesNotMatch(svg, /ask for format 'svg'/);
  assert.match(svg, /read the source/);
});

test('the caption names the document, because an image block carries none', () => {
  const text = renderResultText('Arch', 'diagram', 'svg', 1234);
  assert.match(text, /“Arch”/);
  assert.match(text, /diagram/);
  assert.match(text, /svg/);
  // The distinction the tool description also makes. An agent that thinks this
  // is a screenshot will describe the app instead of the drawing.
  assert.match(text, /not a screenshot/);
});

// ── data URIs: the encoding draw.io answers with is not fixed ────────────────

test('decodeDataUri reads both base64 and percent-encoded payloads', () => {
  const b64 = decodeDataUri('data:image/svg+xml;base64,PHN2Zy8+');
  assert.deepEqual(b64.ok && { mime: b64.mime, base64: b64.base64, payload: b64.payload }, {
    mime: 'image/svg+xml',
    base64: true,
    payload: 'PHN2Zy8+',
  });
  const pct = decodeDataUri('data:image/svg+xml;charset=utf-8,%3Csvg%2F%3E');
  assert.equal(pct.ok && pct.base64, false);
  // The charset parameter is not part of the type.
  assert.equal(pct.ok && pct.mime, 'image/svg+xml');
});

test('decodeDataUri refuses what is not a data URI rather than guessing', () => {
  assert.equal(decodeDataUri('<svg/>').ok, false);
  assert.equal(decodeDataUri('data:image/png;base64').ok, false, 'no comma = truncated');
});

test('svgFromDataUri handles both encodings and rejects the wrong type', () => {
  const decode = (b64: string): string => Buffer.from(b64, 'base64').toString('utf8');
  const fromB64 = svgFromDataUri('data:image/svg+xml;base64,PHN2ZyB3aWR0aD0iMSIvPg==', decode);
  assert.equal(fromB64.ok && fromB64.svg, '<svg width="1"/>');
  const fromPct = svgFromDataUri('data:image/svg+xml;charset=utf-8,%3Csvg%20width%3D%221%22%2F%3E', decode);
  assert.equal(fromPct.ok && fromPct.svg, '<svg width="1"/>');

  // A `split(',')[1]` at the call site would hand the PNG's base64 back as if
  // it were SVG source, and the failure would surface as a corrupt picture
  // rather than as an error.
  const png = svgFromDataUri('data:image/png;base64,iVBORw0KGgo=', decode);
  assert.equal(png.ok, false);
  if (!png.ok) assert.match(png.message, /exported image\/png when svg was asked for/);

  const notSvg = svgFromDataUri('data:image/svg+xml;base64,aGVsbG8=', decode);
  assert.equal(notSvg.ok, false);
  if (!notSvg.ok) assert.match(notSvg.message, /not an SVG document/);
});

test('the render transport deadline outlasts the export deadline', () => {
  // The adapter's timeout message ("draw.io did not answer within 20s") is
  // only ever composed for an agent if main is still listening when it fires.
  // If this inverts, every slow export reports the generic transport timeout
  // instead, and the one-in-flight export lock outlives the call it served —
  // a retry inside the gap is told an export is in flight that nobody is
  // waiting on.
  assert.ok(RENDER_TRANSPORT_TIMEOUT_MS > EXPORT_TIMEOUT_MS);
});
