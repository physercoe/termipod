import { test, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { hasLiveApply, liveApply, registerLiveApply, resetLiveApply, type ApplyOutcome } from './liveApply.ts';

/// The live-apply registry (coworking lane B). Its rules are all about
/// *absence* — no target, an evicted target, a target that threw — and each
/// wrong answer is the same failure: `author_apply` tells the agent a write
/// landed on the user's screen when it did not.

beforeEach(() => resetLiveApply());

test('no registered editor is "no_target", never a silent success', () => {
  assert.equal(liveApply('doc1', '<x/>'), 'no_target');
  assert.equal(hasLiveApply('doc1'), false);
});

test('a registered editor receives the body and its outcome is returned', () => {
  const seen: string[] = [];
  registerLiveApply('doc1', (body) => {
    seen.push(body);
    return 'applied_live';
  });
  assert.equal(hasLiveApply('doc1'), true);
  assert.equal(liveApply('doc1', '<x/>'), 'applied_live');
  assert.deepEqual(seen, ['<x/>']);
});

test('a rejection is passed through untouched', () => {
  registerLiveApply('doc1', () => 'rejected');
  assert.equal(liveApply('doc1', 'garbage'), 'rejected');
});

test('an adapter that throws is a rejection, not a crash and not a success', () => {
  registerLiveApply('doc1', () => {
    throw new Error('parse blew up');
  });
  assert.equal(liveApply('doc1', 'garbage'), 'rejected');
});

test('unregister removes the target', () => {
  const off = registerLiveApply('doc1', () => 'applied_live');
  off();
  assert.equal(hasLiveApply('doc1'), false);
  assert.equal(liveApply('doc1', 'x'), 'no_target');
});

test('a remount replaces the target, and the OLD cleanup does not evict the new one', () => {
  // React can mount the next editor before unmounting the previous one (strict
  // mode does it deliberately). An unregister that deleted the key
  // unconditionally would leave the document with no target while an editor is
  // on screen — and the next apply would report `applied_store_only` while the
  // user watched a live board not change.
  const first: ApplyOutcome = 'rejected';
  const offFirst = registerLiveApply('doc1', () => first);
  const offSecond = registerLiveApply('doc1', () => 'applied_live');
  offFirst();
  assert.equal(hasLiveApply('doc1'), true, 'the second registration must survive the first cleanup');
  assert.equal(liveApply('doc1', 'x'), 'applied_live');
  offSecond();
  assert.equal(hasLiveApply('doc1'), false);
});

test('targets are per document — one editor never answers for another', () => {
  registerLiveApply('doc1', () => 'applied_live');
  assert.equal(liveApply('doc2', 'x'), 'no_target');
});

test('unregistering twice is harmless', () => {
  const off = registerLiveApply('doc1', () => 'applied_live');
  off();
  registerLiveApply('doc1', () => 'rejected');
  off(); // the stale cleanup fires again
  assert.equal(liveApply('doc1', 'x'), 'rejected', 'a stale cleanup must not evict the current target');
});
