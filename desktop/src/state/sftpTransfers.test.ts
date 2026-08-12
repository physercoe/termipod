import assert from 'node:assert/strict';
import { test } from 'node:test';
import { SftpTransferQueue } from './sftpTransfers.ts';

function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve = (): void => {};
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

test('same-session transfers queue in order and expose queued progress', async () => {
  let seq = 0;
  const queue = new SftpTransferQueue({ makeId: () => `t${++seq}`, retentionMs: -1 });
  const firstGate = deferred();
  const order: string[] = [];
  const first = queue.enqueue({
    sessionId: 'ssh-1',
    name: 'first.bin',
    dir: 'down',
    total: 100,
    run: async ({ update }) => {
      order.push('first:start');
      update({ done: 40 });
      await firstGate.promise;
      order.push('first:end');
    },
  });
  const second = queue.enqueue({
    sessionId: 'ssh-1',
    name: 'second.bin',
    dir: 'down',
    total: 20,
    run: async () => {
      order.push('second:start');
    },
  });

  await Promise.resolve();
  assert.deepEqual(
    queue.list('ssh-1').map(({ name, status, done }) => ({ name, status, done })),
    [
      { name: 'first.bin', status: 'active', done: 40 },
      { name: 'second.bin', status: 'queued', done: 0 },
    ],
  );
  assert.deepEqual(order, ['first:start']);

  firstGate.resolve();
  assert.equal((await first.completion).status, 'done');
  assert.equal((await second.completion).status, 'done');
  assert.deepEqual(order, ['first:start', 'first:end', 'second:start']);
});

test('jobs continue without subscribers and a remounted subscriber sees current progress', async () => {
  const queue = new SftpTransferQueue({ makeId: () => 'background', retentionMs: -1 });
  const gate = deferred();
  const handle = queue.enqueue({
    sessionId: 'ssh-1',
    name: 'dataset.tar',
    dir: 'down',
    total: 1_000,
    run: async ({ update }) => {
      update({ done: 600 });
      await gate.promise;
    },
  });
  await Promise.resolve();

  let notifications = 0;
  const unsubscribe = queue.subscribe('ssh-1', () => {
    notifications += 1;
  });
  assert.equal(queue.list('ssh-1')[0]?.done, 600);
  assert.equal(queue.list('ssh-1')[0]?.status, 'active');
  unsubscribe();

  gate.resolve();
  assert.equal((await handle.completion).status, 'done');
  assert.equal(queue.list('ssh-1')[0]?.done, 1_000);
  assert.equal(notifications, 0);
});

test('different SSH sessions run independently and failures do not block the next job', async () => {
  let seq = 0;
  const queue = new SftpTransferQueue({ makeId: () => `t${++seq}`, retentionMs: -1 });
  const gate = deferred();
  const sessionOne = queue.enqueue({
    sessionId: 'ssh-1',
    name: 'bad',
    dir: 'down',
    total: 1,
    run: async () => {
      await gate.promise;
      throw new Error('network lost');
    },
  });
  const afterFailure = queue.enqueue({
    sessionId: 'ssh-1',
    name: 'next',
    dir: 'down',
    total: 1,
    run: async () => {},
  });
  const otherSession = queue.enqueue({
    sessionId: 'ssh-2',
    name: 'parallel',
    dir: 'down',
    total: 1,
    run: async () => {},
  });

  assert.equal((await otherSession.completion).status, 'done');
  gate.resolve();
  const failed = await sessionOne.completion;
  assert.equal(failed.status, 'error');
  assert.equal(failed.error, 'network lost');
  assert.equal((await afterFailure.completion).status, 'done');
});
