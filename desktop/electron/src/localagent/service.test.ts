/// The local agent service (vision-parity L3a, plus L3b's durable half).
/// Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { mkdtempSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { LocalAgentService } from './service.ts';
import type { SpawnedChild } from './claudechild.ts';
import type { Family } from './families.ts';
import type { LocalAgentEvent } from './log.ts';
import type { ResumeTable } from './resumerecipes.ts';
import { readSessionMeta, sessionPaths, writeSessionMeta } from './store.ts';

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

/// A minimal stand-in for the generated N1 table. The real one is pinned
/// against the hub's corpus in `resumerecipes.test.ts`; here we only need the
/// claude row plus one family that does not resume by argv.
const RESUME_TABLE: ResumeTable = {
  version: 1,
  engines: [
    {
      engine: 'claude',
      bin: 'claude',
      windows_bin: '',
      style: 'flag_pair',
      token: '--resume',
      ref_kinds: ['id'],
      source: 'herdr',
      verified: 'probe',
      note: '',
    },
  ],
  families: [
    { family: 'claude-code', engine: 'claude', mechanism: 'argv', note: '' },
    { family: 'codex', engine: '', mechanism: 'appserver_thread_resume', note: '' },
  ],
};

interface Harness {
  svc: LocalAgentService;
  children: FakeChild[];
  dataDir: string;
  /// Build a second service over the same data directory — what an app restart
  /// looks like from the service's point of view.
  restart: () => Harness;
  cleanup: () => void;
}

/// Every temp data directory this file makes, removed on exit. Tests that care
/// about isolation still call `cleanup()`; this is the backstop so the ones
/// that do not are not silently littering /tmp on every run.
const tempDirs: string[] = [];
process.on('exit', () => {
  for (const d of tempDirs) rmSync(d, { recursive: true, force: true });
});

function service(families: Family[] = [CLAUDE, CODEX], dataDir?: string): Harness {
  const dir = dataDir ?? mkdtempSync(path.join(tmpdir(), 'termipod-l3b-svc-'));
  if (dataDir === undefined) tempDirs.push(dir);
  const children: FakeChild[] = [];
  const svc = new LocalAgentService({
    families,
    resumeTable: RESUME_TABLE,
    env: { PATH: '/bin' },
    homeDir: '/home/u',
    dataDir: dir,
    platform: 'linux',
    now: () => new Date('2026-08-10T12:00:00.000Z'),
    spawnFn: () => {
      const c = new FakeChild();
      children.push(c);
      return c as unknown as SpawnedChild;
    },
  });
  return {
    svc,
    children,
    dataDir: dir,
    restart: () => service(families, dir),
    cleanup: () => rmSync(dir, { recursive: true, force: true }),
  };
}

/// Capture the argv a spawn was called with, which is how the resume flags are
/// observed — the FakeChild above is deliberately ignorant of them.
function spawnRecorder(): { calls: Array<{ bin: string; args: string[] }>; children: FakeChild[]; fn: (bin: string, args: string[]) => SpawnedChild } {
  const calls: Array<{ bin: string; args: string[] }> = [];
  const children: FakeChild[] = [];
  return {
    calls,
    children,
    fn: (bin: string, args: string[]) => {
      calls.push({ bin, args });
      const c = new FakeChild();
      children.push(c);
      return c as unknown as SpawnedChild;
    },
  };
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

test('the engine session id is ASSIGNED at create, before any frame arrives', async () => {
  // L3a learned this from the init frame, which left a window where a session
  // existed with a transcript and no resume handle — a child that died before
  // its first frame could never be reattached to. L3b passes `--session-id`
  // (honoured by claude 2.1.220, probed) so the handle exists from the start.
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    assert.ok(desc.engine_session_id !== undefined && desc.engine_session_id !== '');
    // A valid UUID, because claude rejects anything else.
    assert.match(desc.engine_session_id, /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    // And it is on disk before the child has said anything.
    assert.equal(readSessionMeta(h.dataDir, desc.id)?.engine_session_id, desc.engine_session_id);
  } finally {
    h.cleanup();
  }
});

test("the engine's own id wins over the one we assigned, and is persisted", async () => {
  // The engine is authoritative: its id is what `--resume` looks up. If a build
  // ever ignored `--session-id`, keeping ours would persist a handle that
  // resolves to nothing at the next launch.
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: 'eng-1' }) + '\n');
    await settle();
    assert.equal(h.svc.get(desc.id)?.engine_session_id, 'eng-1');
    assert.equal(readSessionMeta(h.dataDir, desc.id)?.engine_session_id, 'eng-1');
  } finally {
    h.cleanup();
  }
});

test('a LATER init from the same child is ignored — that is a /clear, not our session', async () => {
  // claude re-emits init after a /clear, reporting the id of a conversation
  // this transcript is not a record of. Adopting it would point a rebind at
  // the wrong history.
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: 'eng-1' }) + '\n');
    await settle();
    h.children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: 'eng-2' }) + '\n');
    await settle();
    assert.equal(h.svc.get(desc.id)?.engine_session_id, 'eng-1');
  } finally {
    h.cleanup();
  }
});

test('a blank engine session id does not erase the assigned handle', async () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    const assigned = desc.engine_session_id;
    h.children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: '' }) + '\n');
    await settle();
    assert.equal(h.svc.get(desc.id)?.engine_session_id, assigned);
  } finally {
    h.cleanup();
  }
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

// ── L3b: surviving a restart ───────────────────────────────────────────────

test('a session and its transcript come back after a restart', async () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w', posture: 'unrestricted', model: 'sonnet' });
    h.children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: 'eng-1' }) + '\n');
    h.children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'hello from before' }) + '\n');
    await settle();
    h.svc.disposeAll();

    const next = h.restart();
    const report = next.svc.reload();
    assert.deepEqual(report.restored, [desc.id]);
    assert.deepEqual(report.unreadable, []);

    const got = next.svc.get(desc.id);
    assert.ok(got !== undefined);
    assert.equal(got.status, 'stopped', 'a session read off disk has no child attached');
    assert.equal(got.restored, true);
    assert.equal(got.engine_session_id, 'eng-1');
    assert.equal(got.posture, 'unrestricted', 'the posture granted is the posture restored');
    assert.equal(got.model, 'sonnet');
    assert.equal(got.cwd, '/w');

    // The transcript, which native resume would NOT have given us.
    const page = next.svc.history(desc.id);
    assert.ok(page.events.some((e) => e.payload.text === 'hello from before'),
      'the words said before the restart must still be readable');
  } finally {
    h.cleanup();
  }
});

test('the first input after a restart rebinds with --resume and keeps numbering', async () => {
  const h = service();
  let desc;
  try {
    desc = h.svc.create({ cwd: '/w' });
    h.children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: 'eng-1' }) + '\n');
    h.children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'before' }) + '\n');
    await settle();
    const beforeHigh = h.svc.history(desc.id).cursor;
    h.svc.disposeAll();

    // Restart with a recorder so the resume argv is observable.
    const rec = spawnRecorder();
    const svc2 = new LocalAgentService({
      families: [CLAUDE, CODEX],
      resumeTable: RESUME_TABLE,
      env: { PATH: '/bin' },
      homeDir: '/home/u',
      dataDir: h.dataDir,
      platform: 'linux',
      now: () => new Date('2026-08-10T12:00:00.000Z'),
      spawnFn: rec.fn,
    });
    svc2.reload();
    assert.equal(rec.calls.length, 0, 'reload must not spawn anything — a restart is not a resume');

    svc2.input(desc.id, 'text', { body: 'after' });
    assert.equal(rec.calls.length, 1, 'the first input is what rebinds');
    const args = rec.calls[0].args;
    assert.ok(args.includes('--resume'), `expected --resume in ${JSON.stringify(args)}`);
    assert.equal(args[args.indexOf('--resume') + 1], 'eng-1');
    assert.ok(!args.includes('--session-id'),
      '--session-id names a NEW conversation; passing both asks the engine to be two sessions');
    assert.equal(svc2.get(desc.id)?.status, 'running');
    assert.equal(svc2.get(desc.id)?.restored, false);

    // Numbering continues rather than restarting inside one transcript.
    const page = svc2.history(desc.id);
    assert.ok(page.cursor > beforeHigh, `cursor went backwards: ${page.cursor} <= ${beforeHigh}`);
  } finally {
    h.cleanup();
  }
});

test('the rebind lifecycle row says it resumed, so a reader can tell', async () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.children[0].stdout.write(JSON.stringify({ type: 'system', subtype: 'init', session_id: 'eng-1' }) + '\n');
    await settle();
    h.svc.disposeAll();

    const next = h.restart();
    next.svc.reload();
    next.svc.rebind(desc.id);
    await settle();

    const starts = next.svc.history(desc.id).events.filter(
      (e) => e.kind === 'lifecycle' && e.payload.phase === 'started',
    );
    assert.equal(starts.length, 2, 'one start per child');
    assert.equal(starts[0].payload.resumed, false);
    assert.equal(starts[1].payload.resumed, true,
      'without this the transcript shows a second start mid-conversation with no explanation');
  } finally {
    h.cleanup();
  }
});

test('a rebind is a no-op on a session that is already running', () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.rebind(desc.id);
    assert.equal(h.children.length, 1, 'rebinding a live session must not spawn a second child');
  } finally {
    h.cleanup();
  }
});

test('a session with no engine handle refuses to rebind rather than cold-starting', () => {
  // A cold start here would be the silent failure recipes.yaml warns about:
  // the agent forgets everything and nobody is told.
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.stop(desc.id);
    // Simulate a descriptor that never captured one.
    const meta = readSessionMeta(h.dataDir, desc.id);
    assert.ok(meta !== null);
    delete meta.engine_session_id;
    writeSessionMeta(h.dataDir, meta);

    const next = h.restart();
    next.svc.reload();
    assert.throws(() => next.svc.rebind(desc.id), /no engine session id/);
  } finally {
    h.cleanup();
  }
});

test('a family that does not resume by argv refuses to rebind', () => {
  const h = service([CLAUDE, { ...CODEX, launch: { M2: { mode_args: ['--x'] } } }]);
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.stop(desc.id);
    const meta = readSessionMeta(h.dataDir, desc.id);
    assert.ok(meta !== null);
    writeSessionMeta(h.dataDir, { ...meta, family: 'codex' });

    const next = h.restart();
    next.svc.reload();
    assert.throws(() => next.svc.rebind(desc.id), /not_argv_resume/);
  } finally {
    h.cleanup();
  }
});

test('a session whose transcript is gone is reported, not reopened empty', () => {
  // The refusal that makes the missing epoch safe: reopening at seq 1 under an
  // id whose events were numbered in the hundreds would make every stale cursor
  // point at a real-looking row that is the wrong event.
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.stop(desc.id);
    const { dir } = sessionPaths(h.dataDir, desc.id);
    rmSync(path.join(dir, 'events.jsonl'), { force: true });

    const next = h.restart();
    const report = next.svc.reload();
    assert.deepEqual(report.restored, []);
    assert.deepEqual(report.unreadable, [desc.id]);
    assert.equal(next.svc.get(desc.id), undefined, 'it must not appear as a usable session');
  } finally {
    h.cleanup();
  }
});

test('reload is idempotent — a second call does not duplicate a live session', async () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    await settle();
    const report = h.svc.reload();
    assert.deepEqual(report.restored, [], 'the session is already in the registry');
    assert.equal(h.svc.list().length, 1);
    assert.equal(h.svc.get(desc.id)?.status, 'running', 'reload must not stop a running session');
  } finally {
    h.cleanup();
  }
});

test('forget deletes the transcript so it does not reappear at the next launch', () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.stop(desc.id);
    const { dir } = sessionPaths(h.dataDir, desc.id);
    assert.equal(existsSync(dir), true);

    assert.equal(h.svc.forget(desc.id), true);
    assert.equal(existsSync(dir), false);

    const next = h.restart();
    assert.deepEqual(next.svc.reload().restored, []);
  } finally {
    h.cleanup();
  }
});

test('sessions from an unrelated data directory are not visible', () => {
  const a = service();
  const b = service();
  try {
    a.svc.create({ cwd: '/w' });
    assert.deepEqual(b.svc.reload().restored, []);
    assert.equal(b.svc.list().length, 0);
  } finally {
    a.cleanup();
    b.cleanup();
  }
});

test("the director's own message lands in the transcript, in the hub's shape", async () => {
  // Found by the restart e2e: everything else in the log is translated from an
  // engine frame, and the engine never echoes the prompt. Without this row the
  // Companion renders the agent's replies and none of the questions — which was
  // already true in L3a for anyone who closed the dock and reopened it.
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.input(desc.id, 'text', { body: 'what is the codeword?' });
    await settle();

    const events = h.svc.history(desc.id).events;
    const mine = events.find((e) => e.kind === 'input.text');
    assert.ok(mine !== undefined, `no input.text in ${JSON.stringify(events.map((e) => e.kind))}`);
    assert.equal(mine.producer, 'user', 'EventCard keys the user card on producer');
    assert.equal(mine.payload.body, 'what is the codeword?');

    // The prompt is the cause of the turn; a reader sorting by seq sees it first.
    const turn = events.find((e) => e.kind === 'turn.start');
    assert.ok(turn !== undefined);
    assert.ok(mine.seq < turn.seq, 'the prompt must precede the turn it opened');
  } finally {
    h.cleanup();
  }
});

test('attachments ride the input row in the shape InputImages reads', async () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.input(desc.id, 'text', {
      body: 'look at this',
      images: [{ mime: 'image/png', data: 'AAAA', filename: 'shot.png' }],
    });
    await settle();

    const mine = h.svc.history(desc.id).events.find((e) => e.kind === 'input.text');
    assert.ok(mine !== undefined);
    const images = mine.payload.images as Array<Record<string, unknown>>;
    assert.equal(images.length, 1);
    // `mime_type`, not `mime` — the renderer's key, not the driver's.
    assert.equal(images[0].mime_type, 'image/png');
    assert.equal(images[0].data, 'AAAA');
    assert.equal(images[0].filename, 'shot.png');
  } finally {
    h.cleanup();
  }
});

test('a control input is recorded under its own kind, not as text', async () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.input(desc.id, 'cancel', { reason: 'changed my mind' });
    await settle();

    const kinds = h.svc.history(desc.id).events.map((e) => e.kind);
    assert.ok(kinds.includes('input.cancel'), `expected input.cancel in ${JSON.stringify(kinds)}`);
    assert.ok(!kinds.includes('input.text'));
    // A cancel does not open a turn.
    assert.ok(!kinds.includes('turn.start'));
  } finally {
    h.cleanup();
  }
});

test('the input row survives the restart along with the reply', async () => {
  const h = service();
  try {
    const desc = h.svc.create({ cwd: '/w' });
    h.svc.input(desc.id, 'text', { body: 'remember PLATYPUS' });
    h.children[0].stdout.write(JSON.stringify({ type: 'assistant', body: 'OK' }) + '\n');
    await settle();
    h.svc.disposeAll();

    const next = h.restart();
    next.svc.reload();
    const restored = next.svc.history(desc.id).events;
    assert.ok(restored.some((e) => e.kind === 'input.text' && e.payload.body === 'remember PLATYPUS'),
      'the question is gone from the reloaded transcript');
    assert.ok(restored.some((e) => e.kind === 'text' && e.payload.text === 'OK'),
      'the answer is gone from the reloaded transcript');
  } finally {
    h.cleanup();
  }
});
