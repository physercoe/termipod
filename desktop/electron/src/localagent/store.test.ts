/// Tests for the on-disk session store (vision-parity L3b).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

import {
  listSessionMetas,
  parseSessionMeta,
  readSessionMeta,
  removeSessionDir,
  sessionPaths,
  sessionsRoot,
  writeSessionMeta,
  type PersistedSession,
} from './store.ts';

function tmp(): string {
  return mkdtempSync(path.join(tmpdir(), 'termipod-l3b-store-'));
}

function meta(id: string, over: Partial<PersistedSession> = {}): PersistedSession {
  return {
    id,
    family: 'claude-code',
    cwd: '/home/dir/project',
    posture: 'read_local',
    created_at: '2026-08-14T00:00:00.000Z',
    engine_session_id: `engine-${id}`,
    ...over,
  };
}

test('a descriptor round-trips', () => {
  const dir = tmp();
  try {
    writeSessionMeta(dir, meta('local-a', { model: 'sonnet', config_home: '/home/dir/.claude' }));
    const got = readSessionMeta(dir, 'local-a');
    assert.ok(got !== null);
    assert.equal(got.id, 'local-a');
    assert.equal(got.family, 'claude-code');
    assert.equal(got.cwd, '/home/dir/project');
    assert.equal(got.posture, 'read_local');
    assert.equal(got.model, 'sonnet');
    assert.equal(got.engine_session_id, 'engine-local-a');
    assert.equal(got.config_home, '/home/dir/.claude');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('the posture survives the restart exactly as granted', () => {
  // Not bookkeeping: a rebind spawns a real child, and a posture that failed to
  // persist would be silently replaced by whatever the default is that day.
  const dir = tmp();
  try {
    for (const posture of ['converse', 'read_local', 'unrestricted'] as const) {
      writeSessionMeta(dir, meta(`s-${posture}`, { posture }));
      assert.equal(readSessionMeta(dir, `s-${posture}`)?.posture, posture);
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a rewrite replaces rather than appends', () => {
  const dir = tmp();
  try {
    writeSessionMeta(dir, meta('local-a'));
    writeSessionMeta(dir, meta('local-a', { engine_session_id: 'engine-second' }));
    assert.equal(readSessionMeta(dir, 'local-a')?.engine_session_id, 'engine-second');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('an absent descriptor reads as null, not as a throw', () => {
  const dir = tmp();
  try {
    assert.equal(readSessionMeta(dir, 'nope'), null);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('descriptors missing a field that becomes a spawn argument are rejected', () => {
  const cases: Array<[string, string]> = [
    ['no id', '{"family":"claude-code","cwd":"/w","posture":"read_local","created_at":"t"}'],
    ['empty id', '{"id":"","family":"claude-code","cwd":"/w","posture":"read_local","created_at":"t"}'],
    ['no family', '{"id":"a","cwd":"/w","posture":"read_local","created_at":"t"}'],
    ['no cwd', '{"id":"a","family":"claude-code","posture":"read_local","created_at":"t"}'],
    ['empty cwd', '{"id":"a","family":"claude-code","cwd":"","posture":"read_local","created_at":"t"}'],
    ['no posture', '{"id":"a","family":"claude-code","cwd":"/w","created_at":"t"}'],
    ['bogus posture', '{"id":"a","family":"claude-code","cwd":"/w","posture":"root","created_at":"t"}'],
    ['no created_at', '{"id":"a","family":"claude-code","cwd":"/w","posture":"read_local"}'],
    ['not an object', '"a string"'],
  ];
  for (const [why, json] of cases) {
    assert.equal(parseSessionMeta(json), null, `should reject: ${why}`);
  }
});

test('an unknown posture is rejected rather than coerced to the default', () => {
  // Coercing would mean a descriptor written by a future build with a wider
  // posture silently reopens as `read_local` — the session comes back with
  // different powers than the transcript beside it records.
  assert.equal(parseSessionMeta(JSON.stringify(meta('a', { posture: 'sudo' as never }))), null);
});

test('optional fields stay absent rather than becoming empty strings', () => {
  const got = parseSessionMeta('{"id":"a","family":"claude-code","cwd":"/w","posture":"read_local","created_at":"t","model":"","engine_session_id":""}');
  assert.ok(got !== null);
  assert.equal(got.model, undefined, 'an empty model would spawn with --model ""');
  assert.equal(got.engine_session_id, undefined, 'an empty engine id would resume against nothing');
});

test('listing returns newest first', () => {
  const dir = tmp();
  try {
    writeSessionMeta(dir, meta('old', { created_at: '2026-08-01T00:00:00.000Z' }));
    writeSessionMeta(dir, meta('new', { created_at: '2026-08-14T00:00:00.000Z' }));
    writeSessionMeta(dir, meta('mid', { created_at: '2026-08-07T00:00:00.000Z' }));
    assert.deepEqual(listSessionMetas(dir).sessions.map((s) => s.id), ['new', 'mid', 'old']);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('an unreadable directory is skipped and COUNTED, never silently dropped', () => {
  const dir = tmp();
  try {
    writeSessionMeta(dir, meta('good'));
    const bad = path.join(sessionsRoot(dir), 'bad');
    mkdirSync(bad, { recursive: true });
    writeFileSync(path.join(bad, 'meta.json'), '{ not json', 'utf-8');

    const got = listSessionMetas(dir);
    assert.deepEqual(got.sessions.map((s) => s.id), ['good']);
    assert.deepEqual(got.skipped, ['bad'], '"you have one session" must not quietly mean "you had two"');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a descriptor whose id disagrees with its directory is skipped', () => {
  // Otherwise forget('local-a') would delete the directory named 'impostor'.
  const dir = tmp();
  try {
    const d = path.join(sessionsRoot(dir), 'impostor');
    mkdirSync(d, { recursive: true });
    writeFileSync(path.join(d, 'meta.json'), JSON.stringify(meta('local-a')), 'utf-8');
    const got = listSessionMetas(dir);
    assert.equal(got.sessions.length, 0);
    assert.deepEqual(got.skipped, ['impostor']);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('listing an app that has never run a local session is empty, not an error', () => {
  const dir = tmp();
  try {
    const got = listSessionMetas(dir);
    assert.deepEqual(got.sessions, []);
    assert.deepEqual(got.skipped, []);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a stray file in the session root is ignored', () => {
  const dir = tmp();
  try {
    writeSessionMeta(dir, meta('good'));
    writeFileSync(path.join(sessionsRoot(dir), '.DS_Store'), 'junk', 'utf-8');
    assert.deepEqual(listSessionMetas(dir).sessions.map((s) => s.id), ['good']);
    assert.deepEqual(listSessionMetas(dir).skipped, []);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('removal deletes the whole session directory', () => {
  const dir = tmp();
  try {
    writeSessionMeta(dir, meta('local-a'));
    const { dir: sdir } = sessionPaths(dir, 'local-a');
    writeFileSync(path.join(sdir, 'events.jsonl'), '{}\n', 'utf-8');
    removeSessionDir(dir, 'local-a');
    assert.equal(existsSync(sdir), false);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('removal refuses an id that would escape the session root', () => {
  // `id` reaches this function from an IPC argument. A traversal here deletes
  // an arbitrary directory on the director's machine.
  const dir = tmp();
  try {
    const victim = path.join(dir, 'precious');
    mkdirSync(victim, { recursive: true });
    for (const evil of ['..', '../precious', 'a/../../precious', 'nested/deeper']) {
      assert.throws(() => removeSessionDir(dir, evil), /outside the session root/, `should refuse ${evil}`);
    }
    assert.equal(existsSync(victim), true, 'the traversal target must still be there');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('an absolute id is contained by the join rather than escaping through it', () => {
  // Worth stating rather than assuming: `path.join(root, '/etc')` is
  // `<root>/etc`, not `/etc`. So an absolute id is a strange directory name
  // inside the root, not a path to somewhere else — the guard has nothing to
  // catch, and a reader checking this file for "is /etc refused?" should find
  // the reason it does not need to be.
  const dir = tmp();
  try {
    writeSessionMeta(dir, meta('local-a'));
    const { dir: joined } = sessionPaths(dir, '/etc');
    assert.equal(path.dirname(path.resolve(joined)), path.resolve(sessionsRoot(dir)));
    assert.doesNotThrow(() => removeSessionDir(dir, '/etc'));
    assert.equal(existsSync('/etc'), true, 'the real /etc is untouched');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
