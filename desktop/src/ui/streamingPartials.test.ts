/// Stream-folding port checks (agent-transcript-redesign §6 P1): text/thought/
/// plan chain folding, no-message_id passthrough, final-replaces-chain, and
/// kind-namespaced chains. The reference is mobile's collapseStreamingPartials
/// (feed_reducer.dart:1236) — these cases pin byte-parity. The frontend package
/// has no CI test runner; run locally with
/// `node --test src/ui/streamingPartials.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { collapseStreamingPartials } from './streamingPartials.ts';
import type { FeedEvent } from './EventCard';

let n = 0;
function ev(kind: string, payload: Record<string, unknown>): FeedEvent {
  n += 1;
  return { id: `e${n}`, seq: n, ord: n, kind, producer: 'agent', payload };
}

test('text chain: partials and the final fold into one row at the chain root', () => {
  const out = collapseStreamingPartials([
    ev('tool_call', { id: 't1', name: 'Bash' }),
    ev('text', { message_id: 'm1', partial: true, text: 'a' }),
    ev('text', { message_id: 'm1', partial: true, text: 'ab' }),
    ev('text', { message_id: 'm1', text: 'abc' }), // final — no partial flag
    ev('tool_call', { id: 't2', name: 'Read' }),
  ]);
  assert.equal(out.length, 3);
  assert.equal(out[0].kind, 'tool_call');
  assert.equal(out[1].payload['text'], 'abc'); // final replaced the chain entry
  assert.equal(out[2].payload['id'], 't2');
});

test('thought chain folds the same way (gemini-style streamed thinking)', () => {
  const out = collapseStreamingPartials([
    ev('thought', { message_id: 'th1', partial: true, text: 'hmm' }),
    ev('thought', { message_id: 'th1', partial: true, text: 'hmm…' }),
  ]);
  assert.equal(out.length, 1);
  assert.equal(out[0].payload['text'], 'hmm…');
});

test('plan chain folds — N todo snapshots become one card updated in place (G3)', () => {
  const out = collapseStreamingPartials([
    ev('plan', { message_id: 'p1', partial: true, entries: [{ content: 'a', status: 'in_progress' }] }),
    ev('plan', { message_id: 'p1', partial: true, entries: [{ content: 'a', status: 'completed' }] }),
  ]);
  assert.equal(out.length, 1);
  const entries = out[0].payload['entries'] as { status: string }[];
  assert.equal(entries[0].status, 'completed');
});

test("partial flag as the string 'true' also opens a chain (frame-profile shape)", () => {
  const out = collapseStreamingPartials([
    ev('text', { message_id: 'm1', partial: 'true', text: 'a' }),
    ev('text', { message_id: 'm1', text: 'ab' }),
  ]);
  assert.equal(out.length, 1);
  assert.equal(out[0].payload['text'], 'ab');
});

test('events without message_id pass through untouched', () => {
  const out = collapseStreamingPartials([ev('text', { text: 'a' }), ev('text', { text: 'b' })]);
  assert.equal(out.length, 2);
});

test("non-partial with message_id and no preceding partial appends (claude's shape)", () => {
  const out = collapseStreamingPartials([
    ev('text', { message_id: 'm1', text: 'a' }),
    ev('text', { message_id: 'm1', text: 'b' }),
  ]);
  assert.equal(out.length, 2);
});

test('chains are namespaced by kind — text and thought sharing a message_id stay apart', () => {
  const out = collapseStreamingPartials([
    ev('text', { message_id: 'm1', partial: true, text: 'a' }),
    ev('thought', { message_id: 'm1', partial: true, text: 'x' }),
    ev('text', { message_id: 'm1', partial: true, text: 'ab' }),
    ev('thought', { message_id: 'm1', partial: true, text: 'xy' }),
  ]);
  assert.equal(out.length, 2);
  assert.equal(out[0].kind, 'text');
  assert.equal(out[0].payload['text'], 'ab');
  assert.equal(out[1].kind, 'thought');
  assert.equal(out[1].payload['text'], 'xy');
});

test('a non-fold kind inside a chain neither folds nor disturbs the chain index', () => {
  const out = collapseStreamingPartials([
    ev('text', { message_id: 'm1', partial: true, text: 'a' }),
    ev('tool_call', { id: 't1', name: 'Bash' }),
    ev('text', { message_id: 'm1', partial: true, text: 'ab' }),
  ]);
  assert.equal(out.length, 2);
  assert.equal(out[0].payload['text'], 'ab'); // replaced at the root, before the tool_call
  assert.equal(out[1].kind, 'tool_call');
});
