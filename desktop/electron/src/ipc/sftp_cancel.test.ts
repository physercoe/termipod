import assert from 'node:assert/strict';
import { test } from 'node:test';
import { SftpCancelRegistry } from './sftp_cancel.ts';

test('cancellation is scoped by renderer and transfer id and fires once', () => {
  const registry = new SftpCancelRegistry();
  let calls = 0;
  registry.register(10, 'tx1', () => {
    calls += 1;
  });

  assert.equal(registry.cancel(11, 'tx1'), false);
  assert.equal(registry.cancel(10, 'other'), false);
  assert.equal(registry.cancel(10, 'tx1'), true);
  assert.equal(registry.cancel(10, 'tx1'), false);
  assert.equal(calls, 1);
});

test('cleanup cannot unregister a newer stream reusing the same key', () => {
  const registry = new SftpCancelRegistry();
  let cancelled = '';
  const cleanupOld = registry.register(10, 'tx1', () => {
    cancelled = 'old';
  });
  registry.register(10, 'tx1', () => {
    cancelled = 'new';
  });

  cleanupOld();
  assert.equal(registry.cancel(10, 'tx1'), true);
  assert.equal(cancelled, 'new');
});

test('empty transfer ids are not registered', () => {
  const registry = new SftpCancelRegistry();
  registry.register(10, '', () => {
    throw new Error('must not run');
  });
  assert.equal(registry.cancel(10, ''), false);
});

test('cancel-before-register closes the IPC race and cancels the future stream', async () => {
  const registry = new SftpCancelRegistry();
  let calls = 0;
  assert.equal(registry.cancel(10, 'tx-race'), false);
  registry.register(10, 'tx-race', () => {
    calls += 1;
  });
  await Promise.resolve();
  assert.equal(calls, 1);
  assert.equal(registry.cancel(10, 'tx-race'), false);
});
