/// The claude child's frame path (vision-parity L3a). Run with `node --test`.
///
/// The process is faked. What is under test is everything between the child's
/// bytes and the session log: line framing across chunk boundaries, the
/// translation hand-off, the lifecycle events, and the input direction.
///
/// The profile used here is a two-rule toy, not claude's. Profile *semantics*
/// are pinned against the Go interpreter by `frameprofile/parity.test.ts`;
/// re-asserting them here would test the same thing twice and couple this file
/// to the generated artifact.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';

import { ClaudeChild, MAX_FRAME_BYTES, type DriverEvent, type SpawnedChild } from './claudechild.ts';
import type { Family } from './families.ts';

class FakeChild extends EventEmitter {
  stdout = new PassThrough();
  stderr = new PassThrough();
  stdin = new PassThrough();
  killed: (NodeJS.Signals | undefined)[] = [];
  written = '';

  constructor() {
    super();
    this.stdin.on('data', (c: Buffer) => {
      this.written += c.toString('utf-8');
    });
  }

  kill(signal?: NodeJS.Signals): void {
    this.killed.push(signal);
  }
}

const FAMILY: Family = {
  family: 'claude-code',
  bin: 'claude',
  launch: { M2: { mode_args: ['--print', '--output-format', 'stream-json'] } },
  frame_profile: {
    rules: [
      { match: { type: 'assistant' }, emit: { kind: 'text', producer: 'agent', payload: { text: '$.body' } } },
      { match: { type: 'result' }, emit: { kind: 'turn.result', producer: 'agent', payload: { cost_usd: '$.total_cost_usd' } } },
    ],
  },
};

interface Harness {
  child: ClaudeChild;
  fake: FakeChild;
  events: DriverEvent[];
  spawned: { bin: string; args: string[]; cwd: string; env: NodeJS.ProcessEnv } | null;
}

function harness(opts: { posture?: 'converse' | 'read_local' | 'unrestricted'; family?: Family } = {}): Harness {
  const fake = new FakeChild();
  const events: DriverEvent[] = [];
  const h: Harness = { child: null as unknown as ClaudeChild, fake, events, spawned: null };
  h.child = new ClaudeChild({
    family: opts.family ?? FAMILY,
    cwd: '/work',
    posture: opts.posture,
    configHome: '/home/u/.claude',
    env: { PATH: '/bin' },
    now: () => new Date('2026-08-10T12:00:00.000Z'),
    spawnFn: (bin, args, o) => {
      h.spawned = { bin, args, cwd: o.cwd, env: o.env };
      return fake as unknown as SpawnedChild;
    },
    onEvent: (ev) => events.push(ev),
  });
  return h;
}

/// Let the stream's 'data' events land before asserting.
const settle = (): Promise<void> => new Promise((r) => setImmediate(r));

test('start spawns from the registry and records the posture on the transcript', async () => {
  const h = harness({ posture: 'converse' });
  h.child.start();
  await settle();

  assert.equal(h.spawned?.bin, 'claude');
  assert.deepEqual(h.spawned?.args, ['--print', '--output-format', 'stream-json', '--tools', '']);
  assert.equal(h.spawned?.cwd, '/work');
  // The child must resolve the root WE resolved, not re-derive its own.
  assert.equal(h.spawned?.env.CLAUDE_CONFIG_DIR, '/home/u/.claude');

  const started = h.events[0];
  assert.equal(started.kind, 'lifecycle');
  assert.equal(started.producer, 'system');
  assert.equal(started.payload.phase, 'started');
  // A transcript that does not say what the agent was allowed to do leaves a
  // reader to infer it from what it happened to call.
  assert.equal(started.payload.tool_posture, 'converse');
  assert.equal(started.payload.source, 'local');
});

test('the default posture applies when none is named', async () => {
  const h = harness();
  h.child.start();
  await settle();
  assert.deepEqual(h.spawned?.args.slice(-2), ['--tools', 'Read,Glob,Grep']);
  assert.equal(h.events[0].payload.tool_posture, 'read_local');
});

test('frames translate through the profile', async () => {
  const h = harness();
  h.child.start();
  h.fake.stdout.write(JSON.stringify({ type: 'assistant', body: 'hi' }) + '\n');
  await settle();
  const text = h.events.filter((e) => e.kind === 'text');
  assert.equal(text.length, 1);
  assert.equal(text[0].payload.text, 'hi');
  assert.equal(text[0].producer, 'agent');
});

test('a frame split across chunks is reassembled', async () => {
  const h = harness();
  h.child.start();
  const line = JSON.stringify({ type: 'assistant', body: 'split' }) + '\n';
  h.fake.stdout.write(line.slice(0, 10));
  await settle();
  assert.equal(h.events.some((e) => e.kind === 'text'), false, 'must not emit on a partial line');
  h.fake.stdout.write(line.slice(10));
  await settle();
  assert.equal(h.events.filter((e) => e.kind === 'text')[0].payload.text, 'split');
});

test('several frames in one chunk all land, in order', async () => {
  const h = harness();
  h.child.start();
  h.fake.stdout.write(
    JSON.stringify({ type: 'assistant', body: 'a' }) + '\n' + JSON.stringify({ type: 'assistant', body: 'b' }) + '\n',
  );
  await settle();
  assert.deepEqual(h.events.filter((e) => e.kind === 'text').map((e) => e.payload.text), ['a', 'b']);
});

test('a non-JSON line becomes raw rather than vanishing', async () => {
  const h = harness();
  h.child.start();
  h.fake.stdout.write('not json at all\n');
  await settle();
  const raw = h.events.filter((e) => e.kind === 'raw');
  assert.equal(raw.length, 1);
  assert.equal(raw[0].payload.text, 'not json at all');
});

test('a JSON scalar or array is raw, not a frame', async () => {
  // JSON.parse succeeds on `[1,2]` and `"x"`; handing either to the profile
  // would index a non-object.
  const h = harness();
  h.child.start();
  h.fake.stdout.write('[1,2]\n"just a string"\nnull\n');
  await settle();
  assert.equal(h.events.filter((e) => e.kind === 'raw').length, 3);
});

test('blank lines are ignored', async () => {
  const h = harness();
  h.child.start();
  const before = h.events.length;
  h.fake.stdout.write('\n   \n\n');
  await settle();
  assert.equal(h.events.length, before);
});

test('an oversized frame is dropped and its tail does not parse as a frame', async () => {
  const h = harness();
  h.child.start();
  // No newline: the buffer grows past the cap and is discarded.
  h.fake.stdout.write('x'.repeat(MAX_FRAME_BYTES + 10));
  await settle();
  const errs = h.events.filter((e) => e.kind === 'error');
  assert.equal(errs.length, 1);
  assert.match(String(errs[0].payload.text), /dropped a stream-json frame/);

  // The remainder of that giant line arrives next; it is a fragment, not a
  // frame, and must not be emitted as `raw` (which would look like agent
  // output) — then the stream recovers.
  h.fake.stdout.write('tail-of-the-giant-line\n');
  await settle();
  assert.equal(h.events.filter((e) => e.kind === 'raw').length, 0);

  h.fake.stdout.write(JSON.stringify({ type: 'assistant', body: 'recovered' }) + '\n');
  await settle();
  assert.equal(h.events.filter((e) => e.kind === 'text')[0].payload.text, 'recovered');
});

test('stderr rides its own kind, not the frame path', async () => {
  // Auth diagnostics on stderr are not agent output; translating them as raw
  // would put engine warnings in the transcript as if the model had said them.
  const h = harness();
  h.child.start();
  h.fake.stderr.write('warning: something\n');
  await settle();
  const errs = h.events.filter((e) => e.kind === 'error');
  assert.equal(errs.length, 1);
  assert.equal(errs[0].payload.stream, 'stderr');
  assert.equal(errs[0].producer, 'system');
  assert.equal(h.events.some((e) => e.kind === 'raw'), false);
});

test('a text input opens a turn and writes one frame', async () => {
  const h = harness();
  h.child.start();
  h.child.input('text', { body: 'hello' });
  await settle();

  const turn = h.events.filter((e) => e.kind === 'turn.start');
  assert.equal(turn.length, 1);
  assert.equal(turn[0].payload.turn_id, 't-1');
  assert.equal(turn[0].payload.ts, '2026-08-10T12:00:00.000Z');
  assert.equal(h.fake.written, JSON.stringify({ type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'hello' }] } }) + '\n');
});

test('control inputs continue the turn in flight', async () => {
  // An approval that opened a turn would split one exchange into two in the
  // digest, and the second would never get a result.
  const h = harness();
  h.child.start();
  h.child.input('text', { body: 'go' });
  h.child.input('approval', { request_id: 'tu_1', decision: 'allow' });
  h.child.input('answer', { request_id: 'tu_2', body: 'yes' });
  h.child.input('cancel', {});
  await settle();
  assert.equal(h.events.filter((e) => e.kind === 'turn.start').length, 1);
});

test('a malformed input opens no turn', async () => {
  // Order matters: build first, mark the turn second. Otherwise a rejected
  // payload leaves a turn open that no reply will ever close.
  const h = harness();
  h.child.start();
  assert.throws(() => h.child.input('text', {}), /no body and no attachments/);
  await settle();
  assert.equal(h.events.some((e) => e.kind === 'turn.start'), false);
  assert.equal(h.fake.written, '');
});

test('input before start and after stop are both refused', async () => {
  const cold = harness();
  assert.throws(() => cold.child.input('text', { body: 'x' }), /not started/);

  const h = harness();
  h.child.start();
  h.child.stop();
  assert.throws(() => h.child.input('text', { body: 'x' }), /has stopped/);
});

test('stop ends stdin before signalling', async () => {
  // EOF is what lets the child finish its current turn; the signal is the
  // backstop for one that will not.
  //
  // Asserted on CALL order, not on the stream's 'finish' event: `end()` flushes
  // asynchronously, so 'finish' always lands after a synchronous kill() and a
  // test watching it would report the wrong order however the code was written.
  const h = harness();
  h.child.start();
  const calls: string[] = [];
  const realEnd = h.fake.stdin.end.bind(h.fake.stdin);
  h.fake.stdin.end = ((...args: unknown[]) => {
    calls.push('end');
    return realEnd(...(args as []));
  }) as typeof h.fake.stdin.end;
  h.fake.kill = (signal?: NodeJS.Signals) => {
    calls.push(`kill:${String(signal)}`);
    h.fake.killed.push(signal);
  };

  h.child.stop();
  await settle();
  assert.deepEqual(calls, ['end', 'kill:SIGTERM']);
  assert.equal(h.child.running, false);
});

test('stop is idempotent', async () => {
  const h = harness();
  h.child.start();
  h.child.stop();
  h.child.stop();
  await settle();
  assert.equal(h.fake.killed.length, 1);
});

test('an exit we asked for and one we did not are distinguishable', async () => {
  // A crashed session and a closed one look identical without this flag, and
  // only one of them is worth telling the director about.
  const asked = harness();
  asked.child.start();
  asked.child.stop();
  asked.fake.emit('exit', 0, null);
  await settle();
  const a = asked.events.filter((e) => e.kind === 'lifecycle' && e.payload.phase === 'stopped')[0];
  assert.equal(a.payload.expected, true);

  const crashed = harness();
  crashed.child.start();
  crashed.fake.emit('exit', 1, null);
  await settle();
  const c = crashed.events.filter((e) => e.kind === 'lifecycle' && e.payload.phase === 'stopped')[0];
  assert.equal(c.payload.expected, false);
  assert.equal(c.payload.exit_code, 1);
});

test('the context-window supplement runs on the child path', async () => {
  // ContextWindows is unit-tested on its own; this pins that the child
  // actually routes emitted events through it.
  const family: Family = {
    ...FAMILY,
    frame_profile: {
      rules: [
        { match: { type: 'result' }, emit: { kind: 'turn.result', producer: 'agent', payload: {}, payload_expr: '$.' } },
        { match: { type: 'u' }, emit: { kind: 'usage', producer: 'agent', payload: { model: '$.model' } } },
      ],
    },
  };
  const h = harness({ family });
  h.child.start();
  h.fake.stdout.write(JSON.stringify({ type: 'result', by_model: { m1: { context_window: 123456 } } }) + '\n');
  h.fake.stdout.write(JSON.stringify({ type: 'u', model: 'm1' }) + '\n');
  await settle();
  const usage = h.events.filter((e) => e.kind === 'usage')[0];
  assert.equal(usage.payload.context_window, 123456);
});
