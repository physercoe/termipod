/// The local event source (vision-parity L3a). Run with `node --test`.
///
/// The interesting cases are the ordering ones: a live event that arrives
/// while the gap fetch is in flight, a close() that races the attach, and the
/// watch refcount that decides whether a second view silences the first.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { localAgentSource, LOCAL_AGENT_EVENT, type LocalAgentBackend } from './localAgentSource.ts';
import type { Entity } from '../hub/types.ts';

interface Call {
  cmd: string;
  args: Record<string, unknown> | undefined;
}

class FakeBackend implements LocalAgentBackend {
  calls: Call[] = [];
  listeners: ((ev: { payload: unknown }) => void)[] = [];
  unlistenCount = 0;
  /// Resolvers for `localagent_since`, so a test can hold the gap fetch open.
  gapQueue: ((page: { events: Entity[]; cursor: number; resyncRequired: boolean }) => void)[] = [];
  history: Entity[] = [];
  autoGap: Entity[] | null = [];

  invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
    this.calls.push({ cmd, args });
    if (cmd === 'localagent_history') {
      return Promise.resolve({ events: this.history, cursor: 0, resyncRequired: false } as T);
    }
    if (cmd === 'localagent_since') {
      if (this.autoGap !== null) {
        return Promise.resolve({ events: this.autoGap, cursor: 0, resyncRequired: false } as T);
      }
      return new Promise((resolve) => {
        this.gapQueue.push(resolve as (p: unknown) => void);
      }) as Promise<T>;
    }
    return Promise.resolve({} as T);
  }

  listen<T>(event: string, cb: (ev: { payload: T }) => void): Promise<() => void> {
    assert.equal(event, LOCAL_AGENT_EVENT);
    this.listeners.push(cb as (ev: { payload: unknown }) => void);
    return Promise.resolve(() => {
      this.unlistenCount += 1;
      this.listeners = this.listeners.filter((l) => l !== (cb as unknown));
    });
  }

  push(sessionId: string, event: Entity): void {
    for (const l of [...this.listeners]) l({ payload: { session_id: sessionId, event } });
  }

  cmds(): string[] {
    return this.calls.map((c) => c.cmd);
  }
}

function ev(seq: number, kind = 'text'): Entity {
  return { id: `e${seq}`, seq, session_ordinal: seq, kind, producer: 'agent', payload: { text: `t${seq}` } };
}

const settle = async (): Promise<void> => {
  // Two microtask drains: the subscribe path awaits listen(), then invoke().
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
};

test('the local source degrades attention to absence, not to a stub', () => {
  // A stubbed no-op resolve() would make R1's card look like it worked.
  const src = localAgentSource(new FakeBackend());
  assert.equal(src.kind, 'local');
  assert.equal(src.attention, undefined);
});

test('history reads the log page', async () => {
  const be = new FakeBackend();
  be.history = [ev(1), ev(2)];
  const src = localAgentSource(be);
  assert.deepEqual(await src.history('s1', { tail: 50 }), [ev(1), ev(2)]);
  assert.deepEqual(be.calls[0], { cmd: 'localagent_history', args: { session_id: 's1', tail: 50 } });
});

test('history omits tail when the caller did not ask for one', async () => {
  const be = new FakeBackend();
  await localAgentSource(be).history('s1');
  assert.deepEqual(be.calls[0].args, { session_id: 's1' });
});

test('an event emitted during the gap fetch is buffered, not lost', async () => {
  // The window this closes: history() has returned, the child emits, and a
  // listener attached only after the gap fetch would never see it.
  const be = new FakeBackend();
  be.autoGap = null; // hold the gap fetch open
  const seen: Entity[] = [];
  const src = localAgentSource(be);
  const h = src.subscribe('s1', { since: '2', onEvent: (e) => seen.push(e as Entity) });
  await settle();

  be.push('s1', ev(4));
  assert.equal(seen.length, 0, 'must not deliver before the gap is filled');

  be.gapQueue[0]({ events: [ev(3)], cursor: 3, resyncRequired: false });
  await settle();

  // Gap first, then the buffered live event — in seq order, no duplicate.
  assert.deepEqual(seen.map((e) => e.seq), [3, 4]);
  h.close();
});

test('an event already covered by the gap fetch is not delivered twice', async () => {
  const be = new FakeBackend();
  be.autoGap = null;
  const seen: Entity[] = [];
  const h = localAgentSource(be).subscribe('s1', { since: '2', onEvent: (e) => seen.push(e as Entity) });
  await settle();

  be.push('s1', ev(3)); // live copy of what the gap will also carry
  be.gapQueue[0]({ events: [ev(3), ev(4)], cursor: 4, resyncRequired: false });
  await settle();

  assert.deepEqual(seen.map((e) => e.seq), [3, 4]);
  h.close();
});

test('events at or below the cursor are dropped', async () => {
  const be = new FakeBackend();
  const seen: Entity[] = [];
  const h = localAgentSource(be).subscribe('s1', { since: '5', onEvent: (e) => seen.push(e as Entity) });
  await settle();
  be.push('s1', ev(5));
  be.push('s1', ev(4));
  be.push('s1', ev(6));
  assert.deepEqual(seen.map((e) => e.seq), [6]);
  h.close();
});

test('another session on the same channel is ignored', async () => {
  const be = new FakeBackend();
  const seen: Entity[] = [];
  const h = localAgentSource(be).subscribe('s1', { onEvent: (e) => seen.push(e as Entity) });
  await settle();
  be.push('s2', ev(1));
  be.push('s1', ev(2));
  assert.deepEqual(seen.map((e) => e.seq), [2]);
  h.close();
});

test('close before the attach resolves neither delivers nor leaks a listener', async () => {
  // Switching agents while a slow attach is in flight is the ordinary case.
  const be = new FakeBackend();
  be.autoGap = null;
  const seen: Entity[] = [];
  const h = localAgentSource(be).subscribe('s1', { onEvent: (e) => seen.push(e as Entity) });
  h.close();
  await settle();

  be.push('s1', ev(1));
  assert.equal(seen.length, 0);
  assert.equal(be.listeners.length, 0, 'the listener attached after close must be torn down');
});

test('close is idempotent and unwatches exactly once', async () => {
  const be = new FakeBackend();
  const h = localAgentSource(be).subscribe('s1', { onEvent: () => {} });
  await settle();
  h.close();
  h.close();
  assert.equal(be.cmds().filter((c) => c === 'localagent_unwatch').length, 1);
});

test('a second view does not silence the first', async () => {
  // Watching is per-window in main, so an unrefcounted unwatch would stop
  // delivery to every other open agent view.
  const be = new FakeBackend();
  const src = localAgentSource(be);
  const seenA: Entity[] = [];
  const a = src.subscribe('s1', { onEvent: (e) => seenA.push(e as Entity) });
  const b = src.subscribe('s2', { onEvent: () => {} });
  await settle();
  assert.equal(be.cmds().filter((c) => c === 'localagent_watch').length, 1, 'watch is acquired once');

  b.close();
  assert.equal(be.cmds().includes('localagent_unwatch'), false, 'still one live view');

  be.push('s1', ev(1));
  assert.deepEqual(seenA.map((e) => e.seq), [1]);

  a.close();
  assert.equal(be.cmds().filter((c) => c === 'localagent_unwatch').length, 1);
});

test('a failing attach reports through onError', async () => {
  const be = new FakeBackend();
  be.listen = () => Promise.reject(new Error('bridge down'));
  let err: unknown;
  const h = localAgentSource(be).subscribe('s1', { onEvent: () => {}, onError: (e) => (err = e) });
  await settle();
  assert.match(String(err), /bridge down/);
  h.close();
});

test('send maps hub attachment field names onto the local wire', async () => {
  const be = new FakeBackend();
  await localAgentSource(be).send('s1', 'caption', {
    images: [{ mime_type: 'image/png', data: 'AAA' }],
    pdfs: [{ mime_type: 'application/pdf', data: 'BBB', filename: 'spec.pdf' }],
    audios: [{ mime_type: 'audio/wav', data: 'CCC' }],
  });
  const args = be.calls[0].args as Record<string, unknown>;
  assert.equal(args.kind, 'text');
  const payload = args.payload as Record<string, unknown>;
  assert.deepEqual(payload.images, [{ mime: 'image/png', data: 'AAA' }]);
  assert.deepEqual(payload.pdfs, [{ mime: 'application/pdf', data: 'BBB', filename: 'spec.pdf' }]);
  // claude takes neither audio nor video; forwarding them would build a frame
  // the engine rejects.
  assert.equal('audios' in payload, false);
});

test('send with no attachments carries no empty arrays', async () => {
  const be = new FakeBackend();
  await localAgentSource(be).send('s1', 'hi');
  const payload = (be.calls[0].args as Record<string, unknown>).payload as Record<string, unknown>;
  assert.deepEqual(payload, { body: 'hi' });
});

test('approve prefers the agent-offered option id', async () => {
  const be = new FakeBackend();
  const src = localAgentSource(be);
  await src.approve('s1', 'tu_1', 'allow');
  assert.deepEqual((be.calls[0].args as Record<string, unknown>).payload, { request_id: 'tu_1', decision: 'allow' });

  await src.approve('s1', 'tu_2', 'approve', 'proceed_once');
  assert.deepEqual((be.calls[1].args as Record<string, unknown>).payload, { request_id: 'tu_2', decision: 'proceed_once' });
});

test('answer sends the body verbatim', async () => {
  const be = new FakeBackend();
  await localAgentSource(be).answer('s1', 'tu_3', 'the second one');
  const args = be.calls[0].args as Record<string, unknown>;
  assert.equal(args.kind, 'answer');
  assert.deepEqual(args.payload, { request_id: 'tu_3', body: 'the second one' });
});
