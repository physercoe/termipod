import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  IDLE_EXPORT,
  advanceExport,
  canCancel,
  isLocalArtifact,
  isOpenableRecording,
  isPolling,
  progressLabel,
  readCommand,
  type ExportState,
} from './rerunExport.ts';

// The wire shapes below mirror what hub/internal/server/handlers_commands.go
// serialises for a `dataset_export_rrd` row, including the omitempty fields
// that are simply absent rather than null.

function running(progress?: Record<string, unknown>) {
  return {
    id: 'cmd-1',
    kind: 'dataset_export_rrd',
    status: 'delivered',
    ...(progress !== undefined ? { progress } : {}),
  };
}

function finished(result?: Record<string, unknown>) {
  return {
    id: 'cmd-1',
    kind: 'dataset_export_rrd',
    status: 'done',
    ...(result !== undefined ? { result } : {}),
  };
}

test('readCommand reads status, progress and the result path', () => {
  const v = readCommand(
    finished({
      path: '/home/w/.termipod/hostrunner/jobcache/dataset_export_rrd/c1/lerobot_pusht_episode_0.rrd',
      bytes: 12345,
      sha256: 'deadbeef',
    }),
  );
  assert.equal(v.status, 'done');
  assert.equal(v.path?.endsWith('lerobot_pusht_episode_0.rrd'), true);
  assert.equal(v.error, null);
});

test('readCommand survives a row with no progress, result or error', () => {
  const v = readCommand({ id: 'c', status: 'pending' });
  assert.equal(v.status, 'pending');
  assert.equal(v.progress, null);
  assert.equal(v.path, null);
  assert.equal(v.error, null);
});

test('readCommand survives junk', () => {
  for (const junk of [null, undefined, 42, 'nope', []]) {
    const v = readCommand(junk);
    assert.equal(v.status, '');
    assert.equal(v.path, null);
  }
  // progress arriving as the wrong type must not become a broken progress bar
  const v = readCommand({ status: 'delivered', progress: 'decoding' });
  assert.equal(v.progress, null);
});

test('pending and delivered both read as running', () => {
  for (const status of ['pending', 'delivered']) {
    const s = advanceExport({ ...IDLE_EXPORT, phase: 'submitting' }, readCommand({ status }));
    assert.equal(s.phase, 'running');
  }
});

test('a progress heartbeat updates the label without changing phase', () => {
  let s: ExportState = { ...IDLE_EXPORT, phase: 'running', commandId: 'cmd-1' };
  s = advanceExport(s, readCommand(running({ phase: 'decoding', done: 25, total: 100 })));
  assert.equal(s.phase, 'running');
  assert.equal(progressLabel(s), 'decoding 25%');
});

test('progress with no total still says what it is doing', () => {
  const s = advanceExport(
    { ...IDLE_EXPORT, phase: 'running' },
    readCommand(running({ phase: 'exporting' })),
  );
  assert.equal(progressLabel(s), 'exporting…');
});

test('a later poll with no progress keeps the last known progress', () => {
  let s: ExportState = { ...IDLE_EXPORT, phase: 'running' };
  s = advanceExport(s, readCommand(running({ phase: 'decoding', done: 1, total: 4 })));
  s = advanceExport(s, readCommand(running()));
  assert.equal(progressLabel(s), 'decoding 25%');
});

test('done with a path moves to opening', () => {
  const s = advanceExport(
    { ...IDLE_EXPORT, phase: 'running', commandId: 'cmd-1' },
    readCommand(finished({ path: '/tmp/x.rrd', bytes: 1, sha256: 'a' })),
  );
  assert.equal(s.phase, 'opening');
  assert.equal(s.path, '/tmp/x.rrd');
  assert.equal(s.error, null);
});

// Reporting ready with nothing to open would point the viewer at undefined. The
// host also refuses to report done without a path; these are two guards on the
// same mistake, on opposite sides of a network, and that is deliberate.
test('done WITHOUT a path is a failure, not a success', () => {
  const s = advanceExport({ ...IDLE_EXPORT, phase: 'running' }, readCommand(finished()));
  assert.equal(s.phase, 'failed');
  assert.match(s.error ?? '', /no file/);
});

test('failed carries the host reason through to the label', () => {
  const s = advanceExport(
    { ...IDLE_EXPORT, phase: 'running' },
    readCommand({
      status: 'failed',
      error: 'dataset_export_rrd: the pinned (lerobot, rerun-sdk) pair is not available on this host',
    }),
  );
  assert.equal(s.phase, 'failed');
  assert.match(progressLabel(s) ?? '', /lerobot, rerun-sdk/);
});

test('failed with no reason still says something', () => {
  const s = advanceExport({ ...IDLE_EXPORT, phase: 'running' }, readCommand({ status: 'failed' }));
  assert.equal(s.phase, 'failed');
  assert.notEqual(progressLabel(s), null);
});

// A newer hub could add a status. Declaring failure on anything unrecognised
// would turn a healthy export into a spurious error.
test('an unknown status holds the phase and keeps polling', () => {
  const prev: ExportState = { ...IDLE_EXPORT, phase: 'running', commandId: 'cmd-1' };
  const s = advanceExport(prev, readCommand({ status: 'quarantined' }));
  assert.equal(s.phase, 'running');
  assert.equal(isPolling(s), true);
});

test('polling stops once the flow is over', () => {
  assert.equal(isPolling({ ...IDLE_EXPORT, phase: 'submitting' }), true);
  assert.equal(isPolling({ ...IDLE_EXPORT, phase: 'running' }), true);
  for (const phase of ['idle', 'opening', 'ready', 'failed'] as const) {
    assert.equal(isPolling({ ...IDLE_EXPORT, phase }), false, phase);
  }
});

test('cancel is offered only for a running job that has an id', () => {
  assert.equal(canCancel({ ...IDLE_EXPORT, phase: 'running', commandId: 'c' }), true);
  assert.equal(canCancel({ ...IDLE_EXPORT, phase: 'running' }), false);
  assert.equal(canCancel({ ...IDLE_EXPORT, phase: 'opening', commandId: 'c' }), false);
  assert.equal(canCancel({ ...IDLE_EXPORT, phase: 'ready', commandId: 'c' }), false);
});

test('idle and ready say nothing rather than rendering an empty row', () => {
  assert.equal(progressLabel({ ...IDLE_EXPORT, phase: 'idle' }), null);
  assert.equal(progressLabel({ ...IDLE_EXPORT, phase: 'ready' }), null);
});

// The path comes from a host over the network and is about to become a process
// argument. The Electron side checks it too; this check is what makes a bad one
// this flow's error rather than an opaque bridge rejection.
test('only an absolute .rrd is openable', () => {
  assert.equal(isOpenableRecording('/data/x.rrd'), true);
  assert.equal(isOpenableRecording('/data/X.RRD'), true);
  assert.equal(isOpenableRecording('C:\\data\\x.rrd'), true);
  assert.equal(isOpenableRecording('\\\\server\\share\\x.rrd'), true);
  assert.equal(isOpenableRecording('relative/x.rrd'), false);
  assert.equal(isOpenableRecording('/data/x.txt'), false);
  assert.equal(isOpenableRecording('/data/passwd'), false);
  assert.equal(isOpenableRecording(''), false);
  assert.equal(isOpenableRecording('   '), false);
  assert.equal(isOpenableRecording(null), false);
});

// The export runs where the bytes are. When that is not this machine, the path
// it returns names a file that does not exist here — and handing it to the
// viewer produces "rerun exited before serving", which tells nobody anything.
test('an artifact is local only when no remote connection is configured', () => {
  assert.equal(isLocalArtifact(null), true);
  assert.equal(isLocalArtifact(''), true, 'an empty connection id is not a remote host');
  assert.equal(isLocalArtifact('conn-gpu-box'), false);
});

// The whole happy path, in the order a poll actually delivers it.
test('submit → running → progress → done → opening', () => {
  let s: ExportState = { ...IDLE_EXPORT, phase: 'submitting', commandId: 'cmd-1' };
  assert.equal(isPolling(s), true);
  s = advanceExport(s, readCommand({ status: 'pending' }));
  assert.equal(s.phase, 'running');
  s = advanceExport(s, readCommand(running({ phase: 'probing' })));
  assert.equal(progressLabel(s), 'probing…');
  s = advanceExport(s, readCommand(running({ phase: 'exporting', done: 3, total: 4 })));
  assert.equal(progressLabel(s), 'exporting 75%');
  s = advanceExport(s, readCommand(finished({ path: '/c/j/out.rrd', bytes: 9, sha256: 'b' })));
  assert.equal(s.phase, 'opening');
  assert.equal(isOpenableRecording(s.path), true);
  assert.equal(isPolling(s), false);
  assert.equal(s.commandId, 'cmd-1', 'the command id survives so cancel/re-read still work');
});
