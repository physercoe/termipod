import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { openScene, parseExcalidrawScene } from './excalidrawScene.ts';
import { isExcalidrawBody } from './authorBody.ts';

/// Coworking B3. The property under test is the one that was missing: a body we
/// cannot read must be REFUSED, never coerced into a blank scene — because the
/// editor writes whatever it is showing back over the document.

const SCENE = JSON.stringify({
  type: 'excalidraw',
  version: 2,
  source: 'termipod',
  elements: [{ id: 'a', type: 'rectangle' }],
  appState: { viewBackgroundColor: '#fff' },
  files: { f1: { id: 'f1', dataURL: 'data:image/png;base64,AA' } },
});

test('parseExcalidrawScene: a real scene keeps its elements, appState and files', () => {
  const scene = parseExcalidrawScene(SCENE);
  assert.notEqual(scene, null);
  assert.equal(scene?.elements.length, 1);
  assert.deepEqual(scene?.appState, { viewBackgroundColor: '#fff' });
  assert.equal(Object.keys(scene?.files ?? {}).length, 1);
});

test('parseExcalidrawScene: JSON without the excalidraw discriminator is refused', () => {
  // The exact shape the old reader coerced: valid JSON, an element array, no
  // `type`. It would have opened as a scene with one element and then been
  // serialized back as a DIFFERENT document.
  assert.equal(parseExcalidrawScene('{"elements":[{"id":"a"}]}'), null);
  assert.equal(parseExcalidrawScene('{"type":"tldraw","elements":[]}'), null);
});

test('parseExcalidrawScene: a scene without an element array is refused', () => {
  assert.equal(parseExcalidrawScene('{"type":"excalidraw"}'), null);
  assert.equal(parseExcalidrawScene('{"type":"excalidraw","elements":{}}'), null);
});

test('parseExcalidrawScene: non-JSON and non-object JSON are refused', () => {
  assert.equal(parseExcalidrawScene('not json at all'), null);
  assert.equal(parseExcalidrawScene('[1,2,3]'), null);
  assert.equal(parseExcalidrawScene('null'), null);
  assert.equal(parseExcalidrawScene('42'), null);
});

test('parseExcalidrawScene: a malformed appState is dropped, not fatal', () => {
  // Presentation state is recoverable; the drawing is not. Losing a scroll
  // offset must never cost the user their elements.
  const scene = parseExcalidrawScene('{"type":"excalidraw","elements":[],"appState":"broken"}');
  assert.notEqual(scene, null);
  assert.deepEqual(scene?.appState, {});
  assert.equal(scene?.files, undefined);
});

test('openScene: empty is blank, garbage is unreadable — they are not the same answer', () => {
  // The whole point of the three-way answer. A new document has nothing to
  // lose and opens editable; a document we could not read opens read-only.
  assert.equal(openScene('').state, 'blank');
  assert.equal(openScene('   \n ').state, 'blank');
  assert.equal(openScene('{"type":"excalidraw","elements":[]}').state, 'scene');
  assert.equal(openScene('{"nodes":[]}').state, 'unreadable');
  assert.equal(openScene('<svg/>').state, 'unreadable');
});

test('isExcalidrawBody agrees with the parser it is now defined by', () => {
  // Pins the "one predicate, two names" claim in authorBody.ts. If someone
  // re-implements isExcalidrawBody with its own checks, the first body the two
  // disagree about fails here rather than in a user's document.
  for (const body of [
    SCENE,
    '',
    '{"elements":[]}',
    '{"type":"excalidraw","elements":[]}',
    '{"type":"excalidraw"}',
    'not json',
    '[1,2,3]',
  ]) {
    assert.equal(isExcalidrawBody(body), parseExcalidrawScene(body) !== null, `disagreed on: ${body}`);
  }
});
