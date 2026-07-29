/// Tests for the remote-media routing helpers (J8 remote datasets): resolving
/// a persisted connection choice to a live ssh session via the terminal dock's
/// tabs. Pure functions only — the persisted map itself is a loadJson wrapper.
/// Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { liveConnIds, liveSessionFor } from './replayRemote.ts';

const tabs = [
  { kind: 'local', sessionId: 'p1' },
  { kind: 'ssh', sessionId: 's1', connId: 'conn-a' },
  { kind: 'ssh', sessionId: 's2', connId: 'conn-a' }, // duplicated tab, same conn
  { kind: 'ssh', sessionId: 's3', connId: 'conn-b' },
  { kind: 'ssh', sessionId: 's4' }, // ad-hoc connect, no saved connection
];

test('liveSessionFor resolves a connection to its first live session', () => {
  assert.equal(liveSessionFor('conn-a', tabs), 's1');
  assert.equal(liveSessionFor('conn-b', tabs), 's3');
  assert.equal(liveSessionFor('conn-gone', tabs), null);
  assert.equal(liveSessionFor('', tabs), null);
});

test('liveConnIds lists connections with open ssh tabs, deduped, no ad-hoc', () => {
  assert.deepEqual(liveConnIds(tabs), ['conn-a', 'conn-b']);
  assert.deepEqual(liveConnIds([{ kind: 'local', sessionId: 'p1' }]), []);
});
