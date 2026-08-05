/// The feed's hide model. Run locally: `node --test src/ui/feedLens.test.ts`
/// from `desktop/`.
///
/// These pin the two R3 sweeps against the rule the plan sets (D-2: every
/// new/promoted kind is walked through both clients' allowlists deliberately),
/// because a hide rule fails in the direction nobody notices — the row is
/// simply not there, and no error says so.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isHiddenInFeed } from './feedLens.ts';
import type { FeedEvent } from './EventCard';

function ev(kind: string, payload: Record<string, unknown> = {}): FeedEvent {
  return { id: 'e1', seq: 1, ord: 1, kind, producer: 'agent', payload };
}

test('turn.result is no longer hidden — it is the turn footer now', () => {
  // It sat in ALWAYS_HIDDEN, which was itself the divergence: mobile has never
  // hidden it. Without the row, a long run is one undifferentiated scroll.
  assert.equal(isHiddenInFeed(ev('turn.result', { status: 'success' }), false), false);
  assert.equal(isHiddenInFeed(ev('turn.result', { status: 'success' }), true), false);
});

test('the opening marker and pure telemetry stay hidden', () => {
  // turn.start marks the same boundary the footer already marks, from the
  // other side; showing both would double every turn.
  assert.equal(isHiddenInFeed(ev('turn.start'), true), true);
  assert.equal(isHiddenInFeed(ev('usage', { input_tokens: 1 }), true), true);
  assert.equal(isHiddenInFeed(ev('rate_limit'), true), true);
  assert.equal(isHiddenInFeed(ev('status_line'), true), true);
});

test('a compaction boundary escapes the verbose-only tier', () => {
  // It arrives as `system`, which is verbose-only lifecycle chatter. This one
  // is not chatter: it is where the engine stopped remembering what is still
  // on screen above it.
  assert.equal(isHiddenInFeed(ev('system', { subtype: 'compact_boundary' }), false), false);
  assert.equal(isHiddenInFeed(ev('system', { subtype: 'compaction', tokens_before: 9 }), false), false);
});

test('the exemption is narrow — ordinary system events stay verbose-only', () => {
  assert.equal(isHiddenInFeed(ev('system', { text: 'hello' }), false), true);
  assert.equal(isHiddenInFeed(ev('system', { subtype: 'task_started', task_id: 't1' }), false), true);
  assert.equal(isHiddenInFeed(ev('system', {}), false), true);
  // …and are still revealed by the verbose toggle, which the exemption must
  // not have broken.
  assert.equal(isHiddenInFeed(ev('system', { text: 'hello' }), true), false);
});

test('the hub input-route markers were never hidden and still are not', () => {
  for (const kind of ['context.compacted', 'context.cleared', 'context.rewound']) {
    assert.equal(isHiddenInFeed(ev(kind, { verb: 'compact' }), false), false);
  }
});

test('user input is never hidden', () => {
  assert.equal(isHiddenInFeed(ev('input.text', { text: 'hi' }), false), false);
  assert.equal(isHiddenInFeed(ev('input.cancel'), false), false);
});
