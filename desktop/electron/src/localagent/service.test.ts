/// The local agent service (vision-parity L3a). Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';

import { LocalAgentService } from './service.ts';
import type { SpawnedChild } from './claudechild.ts';
import type { Family } from './families.ts';
import type { LocalAgentEvent } from './log.ts';

class FakeChild extends EventEmitter {
  stdout = new PassThrough();
  stderr = new PassThrough();
  stdin = new PassThrough();
  killed = 0;
  written = '';
  constructor() {
    super();
    this.stdin.on('data', (c: Buffer) => {
      this.written += c.toString('utf-8');
    });
  }
  kill(): void {
    this.killed += 1;
  }
}

const CLAUDE: Family = {
  family: 'claude-code',
  bin: 'claude',
  supports: ['M1', 'M2', 'M4'],
  launch: { M2: { mode_args: ['--print', '--output-format', 'stream-json'] } },
  frame_profile: {
    rules: [
      {
        match: { type: 'system', subtype: 'init' },
        emit: { kind: 'session.init', producer: 'agent', payload: { session_id: '$.session_id' } },
      },
      { match: { type: 'assistant' }, emit: { kind: 'text', producer: 'agent', payload: { text: '$.body' } } },
    ],
  },
};
// Declares M2 but ships no launch contract — offered by the hub, undrivable here.
const CODEX: Family = { family: 'codex', bin: 'codex', supports: ['M2'] };

const settle = (): Promise<void> => new Promise((r) => setImmediate(r));

function service(families: Family[] = [CLAUDE, CODEX]): { svc: LocalAgentService; children: FakeChild[] } {
  const children: FakeChild[] = [];
  const svc = new LocalAgentService({
    families,
    env: { PATH: '/bin' },
    homeDir: '/home/u',
    now: () => new Date('2026-08-10T12:00:00.000Z'),
    spawnFn: () => {
      const c = new FakeChild();
      children.push(c);
      return c as unknown as SpawnedChild;
    },
  });
  return { svc, children };
}

test('only families with a local driver are offered', () => {
  // codex declares M2 but has no launch contract in this build; offering it
  // would be a picker row that fails on click.
  const { svc } = service();
  assert.deepEqual(svc.localFamilies().map((f) => f.family), ['claude-code']);
});

test('claude-code without an M2 launch contract is not offered either', () => {
  // The `family !== 'claude-code'` check shadows the launch-contract check for
  // every other family, so this is the only input that can reach it. It is not
  // hypothetical: the contract is generated from agent_families.yaml, and a
  // registry that lost its M2 block would otherwise be offered, accepted, and
  // then spawn an interactive child on a pipe that never answers.
  const gutted: Family = { family: 'claude-code', bin: 'claude', supports: ['M1', 'M2', 'M4'] };
  const { svc } = service([gutted]);
  assert.deepEqual(svc.localFamilies(), []);

  assert.throws(() => svc.create({ cwd: '/w' }), /cannot drive family claude-code/);
});

test('creating an undrivable family is refused', () => {
  const { svc } = service();
  assert.throws(() => svc.create({ family: 'codex', cwd: '/w' }), /cannot drive family codex/);
  assert.throws(() => svc.create({ family: 'nope', cwd: '/w' }), /cannot drive family nope/);
});

test('a session needs a working directory', () => {
  const { svc } = service();
  assert.throws(() => svc.create({ cwd: '   ' }), /cwd is required/);
});

test('create starts a child and logs its lifecycle', async () => {
  const { svc, children } = service();
  const desc = svc.create({ cwd: '/work' });
  await settle();

  assert.equal(children.length, 1);
  assert.equal(desc.status, 'running');
  assert.equal(desc.posture, 'read_local');
  assert.match(desc.id, /^local-/);

  // The lifecycle event is emitted during start(); if the session were
  // registered after start() it would be dropped on the floor.
  const page = svc.history(desc.id);
  assert.equal(page.events.length, 1);
  assert.equal(page.events[0].kind, 'lifecycle');
  assert.equal(page.events[0].seq, 1);
});

test('sessions are independent logs', async () => {
  const { svc, children } = service();
  const a = svc.create({ cwd: '/a' });
  const b = svc.create({ cwd: '/b' });
  children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'from-a' }) + '\n');
  await settle();

  assert.equal(svc.history(a.id).events.filter((e) => e.kind === 'text').length, 1);
  assert.equal(svc.history(b.id).events.filter((e) => e.kind === 'text').length, 0);
  // Each session counts from 1 — a shared counter would make cursors from one
  // session skip events in the other.
  assert.equal(svc.history(b.id).events[0].seq, 1);
});

test('the engine session id is captured from init, once', async () => {
  const { svc, children } = service();
  const desc = svc.create({ cwd: '/w' });
  assert.equal(desc.engine_session_id, undefined);

  children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: 'eng-1' }) + '\n');
  await settle();
  assert.equal(svc.get(desc.id)?.engine_session_id, 'eng-1');

  // claude re-emits init after a /clear; the handle a resume would use is the
  // first one, and silently swapping it would rebind to the wrong transcript.
  children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: 'eng-2' }) + '\n');
  await settle();
  assert.equal(svc.get(desc.id)?.engine_session_id, 'eng-1');
});

test('a blank engine session id is not captured', async () => {
  const { svc, children } = service();
  const desc = svc.create({ cwd: '/w' });
  children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: '' }) + '\n');
  await settle();
  assert.equal(svc.get(desc.id)?.engine_session_id, undefined);
});

test('subscribers see live events with their assigned seq', async () => {
  const { svc, children } = service();
  const seen: { id: string; ev: LocalAgentEvent }[] = [];
  const unsubscribe = svc.subscribe((id, ev) => seen.push({ id, ev }));
  const desc = svc.create({ cwd: '/w' });
  children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'hi' }) + '\n');
  await settle();

  assert.equal(seen.length, 2); // lifecycle + text
  assert.equal(seen[0].id, desc.id);
  // The seq a subscriber sees must be the one the log stored, or a client
  // resuming from it would re-read or skip.
  assert.deepEqual(seen.map((s) => s.ev.seq), [1, 2]);
  assert.equal(seen[1].ev.kind, 'text');

  unsubscribe();
  children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'after' }) + '\n');
  await settle();
  assert.equal(seen.length, 2);
});

test('a throwing subscriber does not stop the others or the transcript', async () => {
  const { svc, children } = service();
  const good: string[] = [];
  svc.subscribe(() => {
    throw new Error('bad subscriber');
  });
  svc.subscribe((_id, ev) => good.push(ev.kind));
  const desc = svc.create({ cwd: '/w' });
  children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'hi' }) + '\n');
  await settle();

  assert.deepEqual(good, ['lifecycle', 'text']);
  assert.equal(svc.history(desc.id).events.length, 2);
});

test('input reaches the child', async () => {
  const { svc, children } = service();
  const desc = svc.create({ cwd: '/w' });
  svc.input(desc.id, 'text', { body: 'hello' });
  await settle();
  assert.match(children[0].written, /"text":"hello"/);
});

test('unknown session ids are refused rather than silently ignored', () => {
  const { svc } = service();
  assert.throws(() => svc.history('nope'), /no local session nope/);
  assert.throws(() => svc.since('nope', 0), /no local session nope/);
  assert.throws(() => svc.input('nope', 'text', { body: 'x' }), /no local session nope/);
});

test('stop is idempotent and marks the descriptor', async () => {
  const { svc, children } = service();
  const desc = svc.create({ cwd: '/w' });
  svc.stop(desc.id);
  svc.stop(desc.id);
  await settle();
  assert.equal(children[0].killed, 1);
  assert.equal(svc.get(desc.id)?.status, 'stopped');
  // Stopping something that isn't there is a no-op, not a throw: quit paths
  // race with a child that already exited.
  svc.stop('nope');
});

test('a child that exits on its own marks the session stopped', async () => {
  const { svc, children } = service();
  const desc = svc.create({ cwd: '/w' });
  children[0].emit('exit', 1, null);
  await settle();
  assert.equal(svc.get(desc.id)?.status, 'stopped');
});

test('disposeAll stops every session', async () => {
  const { svc, children } = service();
  svc.create({ cwd: '/a' });
  svc.create({ cwd: '/b' });
  svc.disposeAll();
  await settle();
  assert.deepEqual(children.map((c) => c.killed), [1, 1]);
  assert.deepEqual(svc.list().map((s) => s.status), ['stopped', 'stopped']);
});

test('forget removes a stopped session but refuses a live one', async () => {
  const { svc } = service();
  const desc = svc.create({ cwd: '/w' });
  assert.throws(() => svc.forget(desc.id), /still running/);
  svc.stop(desc.id);
  assert.equal(svc.forget(desc.id), true);
  assert.equal(svc.get(desc.id), undefined);
  assert.equal(svc.forget(desc.id), false);
});

test('list returns copies, so a caller cannot mutate the registry', () => {
  const { svc } = service();
  const desc = svc.create({ cwd: '/w' });
  const listed = svc.list();
  listed[0].status = 'stopped';
  listed[0].cwd = '/hacked';
  assert.equal(svc.get(desc.id)?.status, 'running');
  assert.equal(svc.get(desc.id)?.cwd, '/w');
});

test('since serves a cursor over the live log', async () => {
  const { svc, children } = service();
  const desc = svc.create({ cwd: '/w' });
  children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'one' }) + '\n');
  await settle();
  const first = svc.history(desc.id);
  children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'two' }) + '\n');
  await settle();

  const next = svc.since(desc.id, first.cursor);
  assert.deepEqual(next.events.map((e) => e.payload.text), ['two']);
  assert.equal(next.resyncRequired, false);
});
