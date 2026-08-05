import { test } from 'node:test';
import assert from 'node:assert/strict';
import { hasLiveRender, liveRender, registerLiveRender, resetLiveRender } from './liveRender.ts';

/// The live-render registry (`author_render`, coworking W2). Sibling of
/// `liveApply.ts` and tested for the same eviction rule, which is the one thing
/// here that is easy to get subtly wrong.
///
/// Run: node --test src/state/liveRender.test.ts  (CI does NOT run these)

test('an unregistered document answers null, not an error', async () => {
  resetLiveRender();
  // Distinct from "the export failed": the executor turns null into
  // DOCUMENT_NOT_OPEN, which is the refusal that has a recovery.
  assert.equal(await liveRender('doc_a'), null);
  assert.equal(hasLiveRender('doc_a'), false);
});

test('a registered target answers, and unregister removes it', async () => {
  resetLiveRender();
  const off = registerLiveRender('doc_a', () => Promise.resolve('<svg id="a"/>'));
  assert.equal(hasLiveRender('doc_a'), true);
  assert.equal(await liveRender('doc_a'), '<svg id="a"/>');
  off();
  assert.equal(await liveRender('doc_a'), null);
});

test('a stale unregister cannot evict the target that replaced it', async () => {
  resetLiveRender();
  // React can mount the next editor before unmounting the previous one. An
  // unconditional delete here would drop the live target and every later render
  // of an OPEN diagram would refuse as "not open".
  const offFirst = registerLiveRender('doc_a', () => Promise.resolve('first'));
  registerLiveRender('doc_a', () => Promise.resolve('second'));
  offFirst();
  assert.equal(await liveRender('doc_a'), 'second');
});

test('a rejection propagates — a failed render has no destructive outcome to hide', async () => {
  resetLiveRender();
  // The opposite choice from `liveApply`, where a throw is coerced to
  // `rejected` because the alternative is reporting a write that may have half
  // landed. Nothing is written here, so the adapter's message is the most
  // useful thing available.
  registerLiveRender('doc_a', () => Promise.reject(new Error('draw.io did not answer')));
  await assert.rejects(() => liveRender('doc_a'), /draw\.io did not answer/);
});
