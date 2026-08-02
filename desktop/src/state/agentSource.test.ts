import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  appendEvent,
  followAgent,
  hubAgentSource,
  sortBySeq,
  type AgentEventSource,
  type HubAgentBackend,
} from './agentSource.ts';
import type { Entity } from '../hub/types.ts';
import type { SseHandle, SseOptions } from '../hub/sse';

/// The AgentEventSource contract (vision-parity L1). These tests are the only
/// thing that can hold it: the seam has no visible behaviour of its own, so a
/// mistake here shows up as "the Companion feed is subtly wrong" long after the
/// refactor, on a machine with a display.

interface Call {
  fn: string;
  args: unknown[];
}

function fakeBackend(over: Partial<HubAgentBackend> = {}): { backend: HubAgentBackend; calls: Call[] } {
  const calls: Call[] = [];
  const rec =
    (fn: string, ret: unknown) =>
    (...args: unknown[]): never => {
      calls.push({ fn, args });
      return ret as never;
    };
  const backend: HubAgentBackend = {
    listAgentEvents: rec('listAgentEvents', Promise.resolve([])),
    streamAgent: rec('streamAgent', { close: () => undefined }),
    postAgentInput: rec('postAgentInput', Promise.resolve(undefined)),
    approveAgentInput: rec('approveAgentInput', Promise.resolve(undefined)),
    answerAgentInput: rec('answerAgentInput', Promise.resolve(undefined)),
    decideAttention: rec('decideAttention', Promise.resolve(undefined)),
    ...over,
  };
  return { backend, calls };
}

// --- the hub adapter -------------------------------------------------------

test('hub source forwards every verb to the SDK, unchanged', async () => {
  const { backend, calls } = fakeBackend();
  const src = hubAgentSource(backend);

  await src.history('a1', { tail: 120 });
  await src.send('a1', 'hello', { images: [{ data: 'AA', mime: 'image/png' }] } as never);
  await src.approve('a1', 'req-1', 'allow', 'proceed_once');
  await src.answer('a1', 'req-2', 'Red');
  await src.attention?.resolve('att-9', 'approve');

  assert.deepEqual(
    calls.map((c) => c.fn),
    ['listAgentEvents', 'postAgentInput', 'approveAgentInput', 'answerAgentInput', 'decideAttention'],
  );
  assert.deepEqual(calls[0].args, ['a1', { tail: 120 }]);
  assert.deepEqual(calls[2].args, ['a1', 'req-1', 'allow', 'proceed_once']);
  assert.deepEqual(calls[3].args, ['a1', 'req-2', 'Red']);
  assert.deepEqual(calls[4].args, ['att-9', 'approve']);
});

test('hub source names itself hub and offers the attention capability', () => {
  const src = hubAgentSource(fakeBackend().backend);
  assert.equal(src.kind, 'hub');
  assert.notEqual(src.attention, undefined);
});

test('an agent-side option id rides the decision field verbatim', async () => {
  // `approvalWire` sends {decision: optionId, optionId} for an ACP option set;
  // the hub reads option_id as authoritative. If the adapter ever "normalised"
  // the decision to a semantic verb, an agent offering proceed_always_server
  // would silently get proceed_once.
  const { backend, calls } = fakeBackend();
  await hubAgentSource(backend).approve('a1', 'r', 'proceed_always_server', 'proceed_always_server');
  assert.deepEqual(calls[0].args, ['a1', 'r', 'proceed_always_server', 'proceed_always_server']);
});

test('a source without a hub degrades to absent attention, not a stub', async () => {
  // The shape plan L3/L4 will ship: same verbs, no attention table. Checking
  // for the capability is what the surfaces do; a stub that resolved nothing
  // would leave a live button that silently does nothing (D-4).
  const local: AgentEventSource = {
    kind: 'local',
    history: async () => [],
    subscribe: () => ({ close: () => undefined }),
    send: async () => undefined,
    approve: async () => undefined,
    answer: async () => undefined,
  };
  assert.equal(local.attention, undefined);
  assert.equal(local.kind, 'local');
});

// --- ordering + dedupe -----------------------------------------------------

test('sortBySeq orders ascending and does not mutate its input', () => {
  const page: Entity[] = [{ seq: 9 }, { seq: 3 }, { seq: 7 }];
  const out = sortBySeq(page);
  assert.deepEqual(
    out.map((e) => e.seq),
    [3, 7, 9],
  );
  assert.deepEqual(
    page.map((e) => e.seq),
    [9, 3, 7],
  );
});

test('sortBySeq treats a missing seq as 0 rather than dropping the row', () => {
  const out = sortBySeq([{ seq: 2 }, { kind: 'text' }]);
  assert.equal(out.length, 2);
  assert.deepEqual(out[0], { kind: 'text' });
});

test('appendEvent drops a replayed seq and returns the same array', () => {
  const prev: Entity[] = [{ seq: 1 }, { seq: 2 }];
  const out = appendEvent(prev, { seq: 2 });
  assert.equal(out, prev, 'a duplicate must not produce a new array (React would re-render)');
});

test('appendEvent appends a new seq', () => {
  const out = appendEvent([{ seq: 1 }], { seq: 2 });
  assert.deepEqual(
    out.map((e) => e.seq),
    [1, 2],
  );
});

test('appendEvent keeps a seq-less event — there is nothing to dedupe on', () => {
  const out = appendEvent([{ seq: 1 }], { kind: 'text' });
  assert.equal(out.length, 2);
  const out2 = appendEvent(out, { kind: 'text' });
  assert.equal(out2.length, 3);
});

// --- followAgent -----------------------------------------------------------

function deferred<T>(): { promise: Promise<T>; resolve: (v: T) => void; reject: (e: unknown) => void } {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function recordingSource(
  history: () => Promise<Entity[]>,
): { source: AgentEventSource; subs: SseOptions[]; closes: number[] } {
  const subs: SseOptions[] = [];
  const closes: number[] = [];
  const source: AgentEventSource = {
    kind: 'hub',
    history,
    subscribe: (_agentId, opts) => {
      const i = subs.push(opts) - 1;
      const h: SseHandle = { close: () => closes.push(i) };
      return h;
    },
    send: async () => undefined,
    approve: async () => undefined,
    answer: async () => undefined,
  };
  return { source, subs, closes };
}

test('followAgent backfills in order, then tails from the last seq', async () => {
  const { source, subs } = recordingSource(async () => [{ seq: 9 }, { seq: 3 }]);
  const seen: Entity[][] = [];
  const live: Entity[] = [];
  followAgent(source, 'a1', {
    tail: 120,
    onBackfill: (e) => seen.push(e),
    onEvent: (e) => live.push(e),
    onError: () => assert.fail('no error expected'),
  });
  await new Promise((r) => setImmediate(r));

  assert.equal(seen.length, 1, 'the backfill lands exactly once');
  assert.deepEqual(
    seen[0].map((e) => e.seq),
    [3, 9],
  );
  assert.equal(subs.length, 1);
  assert.equal(subs[0].since, '9', 'the cursor is the NEWEST backfilled seq, as a string');

  subs[0].onEvent({ seq: 10 });
  assert.deepEqual(live, [{ seq: 10 }]);
});

test('followAgent opens the stream with no cursor when the backfill is empty', async () => {
  const { source, subs } = recordingSource(async () => []);
  followAgent(source, 'a1', { onBackfill: () => undefined, onEvent: () => undefined, onError: () => undefined });
  await new Promise((r) => setImmediate(r));
  assert.equal(subs.length, 1);
  assert.equal(subs[0].since, undefined, 'an empty backfill must NOT send since=0 — that replays the whole agent');
});

test('followAgent passes tail through and omits the opts object when tail is unset', async () => {
  const calls: unknown[] = [];
  const { source } = recordingSource(async () => []);
  const spied: AgentEventSource = {
    ...source,
    history: async (_id, opts) => {
      calls.push(opts);
      return [];
    },
  };
  followAgent(spied, 'a1', { tail: 40, onBackfill: () => undefined, onEvent: () => undefined, onError: () => undefined });
  followAgent(spied, 'a1', { onBackfill: () => undefined, onEvent: () => undefined, onError: () => undefined });
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(calls, [{ tail: 40 }, undefined]);
});

test('close() before the backfill resolves renders nothing and opens no stream', async () => {
  const d = deferred<Entity[]>();
  const { source, subs } = recordingSource(() => d.promise);
  let rendered = false;
  const h = followAgent(source, 'a1', {
    onBackfill: () => {
      rendered = true;
    },
    onEvent: () => undefined,
    onError: () => undefined,
  });
  h.close();
  d.resolve([{ seq: 1 }]);
  await new Promise((r) => setImmediate(r));

  assert.equal(rendered, false, "a switched-away agent's page must not land in the new feed");
  assert.equal(subs.length, 0);
});

test('close() after the stream is open closes it', async () => {
  const { source, subs, closes } = recordingSource(async () => [{ seq: 1 }]);
  const h = followAgent(source, 'a1', {
    onBackfill: () => undefined,
    onEvent: () => undefined,
    onError: () => undefined,
  });
  await new Promise((r) => setImmediate(r));
  assert.equal(subs.length, 1);
  h.close();
  assert.deepEqual(closes, [0]);
});

test('a closed follow drops late stream callbacks', async () => {
  const { source, subs } = recordingSource(async () => []);
  const live: Entity[] = [];
  const errs: unknown[] = [];
  const h = followAgent(source, 'a1', {
    onBackfill: () => undefined,
    onEvent: (e) => live.push(e),
    onError: (e) => errs.push(e),
  });
  await new Promise((r) => setImmediate(r));
  h.close();
  // An SSE reader can have a frame in flight when the abort lands.
  subs[0].onEvent({ seq: 1 });
  subs[0].onError?.(new Error('aborted'));
  assert.deepEqual(live, []);
  assert.deepEqual(errs, []);
});

test('a failed backfill surfaces the error and opens no stream', async () => {
  const { source, subs } = recordingSource(async () => {
    throw new Error('hub down');
  });
  const errs: unknown[] = [];
  followAgent(source, 'a1', {
    onBackfill: () => assert.fail('no backfill expected'),
    onEvent: () => undefined,
    onError: (e) => errs.push(e),
  });
  await new Promise((r) => setImmediate(r));
  assert.equal(errs.length, 1);
  assert.match(String(errs[0]), /hub down/);
  assert.equal(subs.length, 0, 'streaming past a failed backfill would tail from an unknown cursor');
});
