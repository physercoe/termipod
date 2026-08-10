/// Session-log contract (vision-parity L3a). Run with `node --test`.
///
/// The cases that matter are the ones where a reader would otherwise be handed
/// a transcript with a hole in it and no way to know: an evicted cursor, an
/// empty page, a cursor from the future.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { SessionLog, DEFAULT_CAPACITY } from './log.ts';

function push(log: SessionLog, n: number, kind = 'text'): void {
  for (let i = 0; i < n; i++) {
    log.append({ id: `e${i}`, ts: '2026-08-10T00:00:00Z', kind, producer: 'agent', payload: { i } });
  }
}

test('append assigns seq from 1 and mirrors it into session_ordinal', () => {
  const log = new SessionLog();
  const a = log.append({ id: 'a', ts: 't', kind: 'text', producer: 'agent', payload: {} });
  const b = log.append({ id: 'b', ts: 't', kind: 'text', producer: 'agent', payload: {} });
  assert.equal(a.seq, 1);
  assert.equal(b.seq, 2);
  // The renderer navigates on session_ordinal and cursors on seq; locally they
  // are one counter, and a drift between them would misplace the anchor.
  assert.equal(a.session_ordinal, a.seq);
  assert.equal(b.session_ordinal, b.seq);
  assert.equal(log.highWater, 2);
});

test('append returns the stored row, not a copy', () => {
  // The service hands this exact object to live subscribers. If append
  // returned a copy, a subscriber and a later since() reader could disagree
  // about the same event.
  const log = new SessionLog();
  const returned = log.append({ id: 'a', ts: 't', kind: 'text', producer: 'agent', payload: {} });
  const stored = log.tail().events[0];
  assert.equal(returned, stored);
});

test('tail returns ascending by seq and cursors on the last event', () => {
  const log = new SessionLog();
  push(log, 5);
  const page = log.tail(3);
  assert.deepEqual(page.events.map((e) => e.seq), [3, 4, 5]);
  assert.equal(page.cursor, 5);
  assert.equal(page.resyncRequired, false);
});

test('tail on an empty log cursors at the high-water mark, not at a stale 0', () => {
  const log = new SessionLog();
  push(log, 4);
  // tail(0) is a legitimate "just give me the cursor" request. Reporting 0
  // would make the caller's next subscribe replay the whole transcript.
  const page = log.tail(0);
  assert.deepEqual(page.events, []);
  assert.equal(page.cursor, 4);

  const fresh = new SessionLog();
  assert.equal(fresh.tail().cursor, 0);
});

test('since returns only what follows the cursor', () => {
  const log = new SessionLog();
  push(log, 5);
  const page = log.since(2);
  assert.deepEqual(page.events.map((e) => e.seq), [3, 4, 5]);
  assert.equal(page.cursor, 5);
  assert.equal(page.resyncRequired, false);
});

test('since at the high-water mark is empty and holds the cursor', () => {
  const log = new SessionLog();
  push(log, 3);
  const page = log.since(3);
  assert.deepEqual(page.events, []);
  assert.equal(page.cursor, 3);
  assert.equal(page.resyncRequired, false);
});

test('a cursor ahead of the log keeps its own position', () => {
  // A persisted log (L3b) can hand back a cursor this incarnation has not
  // reached. Clamping it DOWN would replay events the caller already has.
  const log = new SessionLog();
  push(log, 2);
  const page = log.since(9);
  assert.deepEqual(page.events, []);
  assert.equal(page.cursor, 9);
});

test('eviction drops the oldest and an evicted cursor demands a resync', () => {
  const log = new SessionLog(3);
  push(log, 5);
  assert.equal(log.size, 3);
  assert.deepEqual(log.tail().events.map((e) => e.seq), [3, 4, 5]);

  // Cursor 1 wanted event 2, which is gone: the reply cannot be a
  // continuation, so it is a snapshot and says so.
  const page = log.since(1);
  assert.equal(page.resyncRequired, true);
  assert.deepEqual(page.events.map((e) => e.seq), [3, 4, 5]);
  assert.equal(page.cursor, 5);
});

test('the boundary cursor is servable, one before it is not', () => {
  // Off-by-one here is the difference between a silent hole and a resync, and
  // both look fine from the outside — pin the exact boundary.
  const log = new SessionLog(3);
  push(log, 5); // retains 3,4,5
  // cursor 2 wants event 3, which IS retained.
  assert.equal(log.since(2).resyncRequired, false);
  assert.deepEqual(log.since(2).events.map((e) => e.seq), [3, 4, 5]);
  // cursor 1 wants event 2, which is not.
  assert.equal(log.since(1).resyncRequired, true);
});

test('a cursor of 0 on a live log is a normal full replay, not a resync', () => {
  // followAgent sends 0 (well, undefined → the source sends nothing) on a
  // first attach; a fresh reader must not be told it lost history it never had.
  const log = new SessionLog();
  push(log, 3);
  const page = log.since(0);
  assert.equal(page.resyncRequired, false);
  assert.deepEqual(page.events.map((e) => e.seq), [1, 2, 3]);
});

test('a negative or non-finite cursor resyncs instead of guessing', () => {
  const log = new SessionLog();
  push(log, 3);
  for (const bad of [-1, Number.NaN, Number.POSITIVE_INFINITY * -1]) {
    const page = log.since(bad);
    assert.equal(page.resyncRequired, true, `cursor ${String(bad)}`);
    assert.deepEqual(page.events.map((e) => e.seq), [1, 2, 3]);
  }
});

test('capacity must be a positive integer', () => {
  // A zero capacity evicts every event as it arrives — a log that silently
  // stores nothing is worse than one that refuses to be built.
  assert.throws(() => new SessionLog(0), /positive integer/);
  assert.throws(() => new SessionLog(-1), /positive integer/);
  assert.throws(() => new SessionLog(1.5), /positive integer/);
  assert.equal(DEFAULT_CAPACITY > 0, true);
});

test('seq keeps climbing across eviction', () => {
  // The counter is the session's, not the retained window's. If eviction reset
  // it, a reader's cursor would suddenly point at a different event.
  const log = new SessionLog(2);
  push(log, 10);
  assert.equal(log.highWater, 10);
  assert.deepEqual(log.tail().events.map((e) => e.seq), [9, 10]);
});
