/// Tests for the SSH-forward listener/pipe/teardown lifecycle with an injected
/// channel factory (a plain TCP connection to an in-process echo server stands
/// in for the ssh2 direct-tcpip channel). Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { connect, createServer, type Server, type Socket } from 'node:net';
import { autoCloseForwards, listForwards, startForward, stopForward } from './ssh_forward.ts';

function echoServer(): Promise<{ server: Server; port: number }> {
  return new Promise((resolve) => {
    const server = createServer((sock: Socket) => sock.pipe(sock));
    server.listen(0, '127.0.0.1', () => {
      resolve({ server, port: (server.address() as { port: number }).port });
    });
  });
}

function dial(port: number): Promise<Socket> {
  return new Promise((resolve, reject) => {
    const sock = connect(port, '127.0.0.1', () => resolve(sock));
    sock.once('error', reject);
  });
}

function readOnce(sock: Socket): Promise<Buffer> {
  return new Promise((resolve) => sock.once('data', resolve));
}

const dialChannel =
  (port: number) =>
  (): Promise<Socket> =>
    dial(port);

test('forward pipes bytes both ways over the channel', async () => {
  const { server, port } = await echoServer();
  try {
    const fwd = await startForward(dialChannel(port), 'remote.internal', 8080);
    assert.ok(fwd.local_port > 0);
    assert.equal(fwd.remote_host, 'remote.internal');
    const a = await dial(fwd.local_port);
    const b = await dial(fwd.local_port); // concurrent connections share the listener
    a.write('alpha');
    b.write('beta');
    assert.equal((await readOnce(a)).toString(), 'alpha');
    assert.equal((await readOnce(b)).toString(), 'beta');
    a.destroy();
    b.destroy();
    stopForward(fwd.forward_id);
  } finally {
    server.close();
  }
});

test('stop closes the listener and severs live streams; idempotent', async () => {
  const { server, port } = await echoServer();
  try {
    const fwd = await startForward(dialChannel(port), 'r', 1);
    const live = await dial(fwd.local_port);
    const closed = new Promise<void>((resolve) => live.once('close', resolve));
    stopForward(fwd.forward_id);
    stopForward(fwd.forward_id); // second stop is a no-op
    await closed;
    await assert.rejects(dial(fwd.local_port)); // listener gone
    assert.deepEqual(listForwards([fwd.forward_id]), []);
  } finally {
    server.close();
  }
});

test('channel refusal drops the one connection, listener survives', async () => {
  const { server, port } = await echoServer();
  try {
    let failFirst = true;
    const factory = (): Promise<Socket> => {
      if (failFirst) {
        failFirst = false;
        return Promise.reject(new Error('open channel: administratively prohibited'));
      }
      return dial(port);
    };
    const fwd = await startForward(factory, 'r', 1);
    const refused = await dial(fwd.local_port);
    await new Promise<void>((resolve) => refused.once('close', resolve));
    const ok = await dial(fwd.local_port);
    ok.write('still-up');
    assert.equal((await readOnce(ok)).toString(), 'still-up');
    ok.destroy();
    stopForward(fwd.forward_id);
  } finally {
    server.close();
  }
});

test('autoCloseForwards tears down and fires the callback; stopForward does not', async () => {
  const { server, port } = await echoServer();
  try {
    let autoClosed = 0;
    const f1 = await startForward(dialChannel(port), 'r', 1, () => {
      autoClosed += 1;
    });
    const f2 = await startForward(dialChannel(port), 'r', 2, () => {
      autoClosed += 1;
    });
    stopForward(f2.forward_id); // explicit — callback must NOT fire
    autoCloseForwards([f1.forward_id, f2.forward_id]); // f2 already gone — no double fire
    assert.equal(autoClosed, 1);
    await assert.rejects(dial(f1.local_port));
  } finally {
    server.close();
  }
});

test('listForwards reports only live forwards for the given ids', async () => {
  const { server, port } = await echoServer();
  try {
    const fwd = await startForward(dialChannel(port), 'db.internal', 5432);
    const listed = listForwards([fwd.forward_id, 'f-unknown']);
    assert.equal(listed.length, 1);
    assert.deepEqual(listed[0], fwd);
    stopForward(fwd.forward_id);
  } finally {
    server.close();
  }
});
