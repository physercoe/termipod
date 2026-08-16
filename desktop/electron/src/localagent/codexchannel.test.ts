/// L4b channel checks — the decoders are pure, and both transports are opened
/// against injected fakes so the contract is provable with no codex on the box.
/// The live round trip is `codexattach.e2e.test.ts`, which is the only place
/// that can prove the handshake actually works.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';

import {
  CODEX_WS_OPTIONS,
  codexSocketUrl,
  decodeMessage,
  LineBuffer,
  openCodexChannel,
  type ChannelDeps,
  type CodexChannelHandlers,
  type CodexFrame,
  type WsLike,
} from './codexchannel.ts';
import type { CodexAttachPlan } from './codexattach.ts';

test('decodeMessage parses one unit and keeps unparseable output as junk', () => {
  const r = decodeMessage('{"a":1}\n\n  \nnot json\n{"b":2}');
  assert.deepEqual(r.frames, [{ a: 1 }, { b: 2 }]);
  // Blank lines are framing, not junk; a non-JSON line is junk and must not be
  // silently dropped — swallowing engine output is how E3 shipped invisible.
  assert.deepEqual(r.junk, ['not json']);
});

test('decodeMessage rejects non-object JSON', () => {
  // A bare scalar or array is valid JSON but not a JSON-RPC frame; treating it
  // as one would hand the driver something with no `method` and no `id`.
  const r = decodeMessage('42\n["a"]\n"text"\nnull');
  assert.deepEqual(r.frames, []);
  assert.equal(r.junk.length, 4);
});

test('LineBuffer reassembles a frame split across chunk boundaries', () => {
  const b = new LineBuffer();
  assert.deepEqual(b.push('{"me').frames, [], 'a partial line yields nothing yet');
  assert.deepEqual(b.push('thod":"x"}').frames, [], 'still no newline, still nothing');
  assert.deepEqual(b.push('\n').frames, [{ method: 'x' }]);
});

test('LineBuffer holds the tail and flush() releases a final unterminated frame', () => {
  const b = new LineBuffer();
  const first = b.push('{"a":1}\n{"b":2}');
  assert.deepEqual(first.frames, [{ a: 1 }], 'only the completed line is emitted');
  // A process that exits without a trailing newline still said something.
  assert.deepEqual(b.flush().frames, [{ b: 2 }]);
  assert.deepEqual(b.flush().frames, [], 'flush is idempotent');
});

test('LineBuffer handles several frames in one chunk, in order', () => {
  const b = new LineBuffer();
  assert.deepEqual(b.push('{"n":1}\n{"n":2}\n{"n":3}\n').frames, [{ n: 1 }, { n: 2 }, { n: 3 }]);
});

test('the socket URL is the ws+unix form, and deflate is OFF', () => {
  assert.equal(codexSocketUrl('/home/u/.codex/x.sock'), 'ws+unix:///home/u/.codex/x.sock:/');
  // Measured against the live daemon: `ws` offers permessage-deflate by
  // default and the daemon HANGS UP on that handshake instead of declining the
  // extension. Isolated by varying one option at a time. This is not a
  // preference; flipping it breaks the daemon rung entirely.
  assert.equal(CODEX_WS_OPTIONS.perMessageDeflate, false);
});

class FakeWs extends EventEmitter implements WsLike {
  sent: string[] = [];
  closed = false;
  send(data: string): void {
    this.sent.push(data);
  }
  close(): void {
    this.closed = true;
  }
}

class FakeChild extends EventEmitter {
  stdout = new PassThrough();
  stderr = new PassThrough();
  stdin = new PassThrough();
  killed = false;
  kill(): void {
    this.killed = true;
  }
}

function collector(): { handlers: CodexChannelHandlers; frames: CodexFrame[]; junk: string[]; closes: string[] } {
  const frames: CodexFrame[] = [];
  const junk: string[] = [];
  const closes: string[] = [];
  return {
    frames,
    junk,
    closes,
    handlers: {
      onFrame: (f) => frames.push(f),
      onJunk: (j) => junk.push(j),
      onClose: (i) => closes.push(i.reason),
    },
  };
}

const DAEMON_PLAN: CodexAttachPlan = {
  mode: 'daemon',
  socketPath: '/tmp/x.sock',
  startArgv: ['codex', 'app-server', 'daemon', 'start'],
  reason: 'attached to the shared codex app-server daemon; it outlives this app',
};

const SPAWN_PLAN: CodexAttachPlan = {
  mode: 'spawn',
  argv: ['codex', 'app-server'],
  reason: 'no installer-managed codex, so running a per-session app server',
};

function deps(over: Partial<ChannelDeps> = {}): { deps: ChannelDeps; ws: FakeWs; child: FakeChild; started: string[][] } {
  const ws = new FakeWs();
  const child = new FakeChild();
  const started: string[][] = [];
  return {
    ws,
    child,
    started,
    deps: {
      connect: async () => ws,
      spawn: () => child as never,
      startDaemon: async (argv) => {
        started.push(argv);
      },
      ...over,
    },
  };
}

test('daemon rung: starts the daemon, opens the socket, relays frames both ways', async () => {
  const c = collector();
  const d = deps();
  const open = openCodexChannel(DAEMON_PLAN, c.handlers, { cwd: '/w' }, d.deps);
  setImmediate(() => d.ws.emit('open'));
  const ch = await open;

  assert.equal(ch.mode, 'daemon');
  assert.deepEqual(d.started, [['codex', 'app-server', 'daemon', 'start']], 'the daemon must be brought up first');

  ch.send({ jsonrpc: '2.0', id: 1, method: 'initialize' });
  assert.deepEqual(JSON.parse(d.ws.sent[0] ?? ''), { jsonrpc: '2.0', id: 1, method: 'initialize' });

  // One WebSocket message is one complete unit — no reassembly, and a message
  // with no trailing newline must still be delivered.
  d.ws.emit('message', '{"id":1,"result":{"codexHome":"/h"}}');
  assert.deepEqual(c.frames, [{ id: 1, result: { codexHome: '/h' } }]);
});

test('daemon rung: closing our channel must NOT stop the shared daemon', async () => {
  const c = collector();
  const d = deps();
  const open = openCodexChannel(DAEMON_PLAN, c.handlers, { cwd: '/w' }, d.deps);
  setImmediate(() => d.ws.emit('open'));
  const ch = await open;
  ch.close();
  assert.ok(d.ws.closed, 'our socket closes');
  // The rung's whole purpose is that the session outlives us. Nothing in this
  // path may issue `daemon stop`.
  assert.deepEqual(d.started, [['codex', 'app-server', 'daemon', 'start']]);
});

test('daemon rung: a daemon that will not start falls back to spawn, and SAYS so', async () => {
  const c = collector();
  const d = deps({
    startDaemon: async () => {
      throw new Error('managed standalone Codex install not found');
    },
  });
  const ch = await openCodexChannel(DAEMON_PLAN, c.handlers, { cwd: '/w' }, d.deps);
  assert.equal(ch.mode, 'spawn', 'we still get a working session');
  // "shared with your TUI" and "dies with this window" are different promises.
  // A silent downgrade would leave the user believing the wrong one.
  assert.match(ch.reason, /could not be reached/);
  assert.match(ch.reason, /managed standalone Codex install not found/);
  assert.match(ch.reason, /will not outlive/);
});

test('daemon rung: a socket that never finishes the handshake times out, not hangs', async () => {
  const c = collector();
  const d = deps();
  // No 'open' is ever emitted — the failure mode a silent daemon produces.
  const ch = await openCodexChannel(DAEMON_PLAN, c.handlers, { cwd: '/w', connectTimeoutMs: 20 }, d.deps);
  assert.equal(ch.mode, 'spawn');
  assert.match(ch.reason, /timed out/);
});

test('daemon rung: a close after open is reported as a close, not a failed open', async () => {
  const c = collector();
  const d = deps();
  const open = openCodexChannel(DAEMON_PLAN, c.handlers, { cwd: '/w' }, d.deps);
  setImmediate(() => d.ws.emit('open'));
  await open;
  d.ws.emit('close', 1006);
  assert.equal(c.closes.length, 1);
  assert.match(c.closes[0] ?? '', /1006/);
});

test('daemon rung: a socket that fails reports close exactly once', async () => {
  const c = collector();
  const d = deps();
  const open = openCodexChannel(DAEMON_PLAN, c.handlers, { cwd: '/w' }, d.deps);
  setImmediate(() => d.ws.emit('open'));
  await open;
  // `ws` emits BOTH on a broken connection. A driver told twice that its
  // channel ended would tear the same session down twice.
  d.ws.emit('error', new Error('ECONNRESET'));
  d.ws.emit('close', 1006);
  assert.equal(c.closes.length, 1, 'error+close is one ending, not two');
  assert.match(c.closes[0] ?? '', /ECONNRESET/, 'the first, more specific reason wins');
});

test('spawn rung: stdout is a byte stream, so frames reassemble across chunks', async () => {
  const c = collector();
  const d = deps();
  const ch = await openCodexChannel(SPAWN_PLAN, c.handlers, { cwd: '/w' }, d.deps);
  assert.equal(ch.mode, 'spawn');

  d.child.stdout.write('{"method":"thread/');
  d.child.stdout.write('started","params":{}}\n');
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(c.frames, [{ method: 'thread/started', params: {} }]);
});

test('spawn rung: send writes a newline-delimited frame; close ends stdin', async () => {
  const c = collector();
  const d = deps();
  const ch = await openCodexChannel(SPAWN_PLAN, c.handlers, { cwd: '/w' }, d.deps);
  const written: string[] = [];
  d.child.stdin.on('data', (b: Buffer) => written.push(b.toString('utf8')));

  ch.send({ id: 1, method: 'initialize' });
  await new Promise((r) => setImmediate(r));
  assert.equal(written.join(''), '{"id":1,"method":"initialize"}\n', 'the trailing newline IS the framing');

  let stdinEnded = false;
  d.child.stdin.on('finish', () => {
    stdinEnded = true;
  });
  ch.close();
  await new Promise((r) => setImmediate(r));
  // Ending stdin is what retires an app-server session — the same contract
  // claudechild.ts documents. The kill is only the backstop, so asserting the
  // kill alone would pass on a close() that never let the engine finish.
  assert.ok(stdinEnded, 'close() must end stdin, not just kill');
  assert.ok(d.child.killed);
});

test('spawn rung: an exit flushes the unterminated tail before reporting close', async () => {
  const c = collector();
  const d = deps();
  await openCodexChannel(SPAWN_PLAN, c.handlers, { cwd: '/w' }, d.deps);
  d.child.stdout.write('{"method":"last"}');
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(c.frames, [], 'not yet — no newline');
  d.child.emit('exit', 0);
  // A final frame with no trailing newline is still something the engine said.
  assert.deepEqual(c.frames, [{ method: 'last' }]);
  assert.equal(c.closes.length, 1);
});
