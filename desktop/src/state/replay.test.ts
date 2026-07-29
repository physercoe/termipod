import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pageOffsetFor, useReplay } from './replay.ts';

test('an episode jump seeks to the page that holds it', () => {
  // The episodes table is windowed and the player renders from the CURRENT
  // page's rows, so landing on episode 1400 with the table at offset 0 shows an
  // empty player over a table that does not contain it — broken, not "out of
  // range".
  assert.equal(pageOffsetFor(0, 200), 0);
  assert.equal(pageOffsetFor(199, 200), 0);
  assert.equal(pageOffsetFor(200, 200), 200);
  assert.equal(pageOffsetFor(1400, 200), 1400);
  assert.equal(pageOffsetFor(1401, 200), 1400);
});

test('a nonsense index lands on the first page rather than a negative offset', () => {
  for (const [ep, size] of [[-1, 200], [Number.NaN, 200], [5, 0], [5, -1]] as const) {
    assert.equal(pageOffsetFor(ep, size), 0, `episode ${ep} / page ${size}`);
  }
});

test('a target by id supersedes a pending location handoff', () => {
  // They answer the same question with different certainty: a handoff is a
  // location that may or may not be registered, a target names the row. Leaving
  // a stale handoff queued would pop the register form open on arrival.
  const s = useReplay.getState();
  s.openDataset({ rootPath: '/data/ds' });
  assert.notEqual(useReplay.getState().handoff, null);

  s.openRegistered({ datasetId: 'ds-1', projectId: 'proj-1', episode: 3 });
  assert.equal(useReplay.getState().handoff, null);
  assert.deepEqual(useReplay.getState().target, { datasetId: 'ds-1', projectId: 'proj-1', episode: 3 });

  useReplay.getState().clearTarget();
  assert.equal(useReplay.getState().target, null);
});

test('a location handoff still clears the selection, and a target does not', () => {
  // A handoff cannot know which row it will resolve to, so the surface must not
  // flash the previously-open dataset while it works that out. A target already
  // names the row, and the surface sets the selection from it directly.
  const s = useReplay.getState();
  s.select('ds-old');
  s.openDataset({ rootPath: '/data/other' });
  assert.equal(useReplay.getState().selectedId, '');

  s.select('ds-old');
  s.openRegistered({ datasetId: 'ds-new', projectId: 'p' });
  assert.equal(useReplay.getState().selectedId, 'ds-old');
  useReplay.getState().clearTarget();
  useReplay.getState().clearHandoff();
});
