/// Reader checks for the Issues drawer (transcript P5 A3). CI does NOT run
/// these — `node --test src/state/digestIssues.test.ts` from `desktop/` is
/// manual (CLAUDE.md), so they run locally before any claim that this is green.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { readDigestIssues } from './digestIssues.ts';

const digest = (issues: unknown, worst?: string): Record<string, unknown> => ({
  issues,
  ...(worst === undefined ? {} : { issue_worst_severity: worst }),
});

test('a hub that predates the field yields an empty summary, not a clean bill', () => {
  // The distinction matters: "no issues" and "never looked" must not render the
  // same, or an old hub would claim a run is clean.
  for (const d of [undefined, {}, digest(undefined), digest(null), digest('nope'), digest([])]) {
    const got = readDigestIssues(d as Record<string, unknown> | undefined);
    assert.equal(got.total, 0);
    assert.equal(got.classes.length, 0);
    assert.equal(got.worst, undefined);
  }
});

test('classes sort severity-first, then loudest, then alphabetical', () => {
  const got = readDigestIssues(
    digest({
      mixed_id_shape: { count: 9, severity: 'info', sample_seqs: [1] },
      orphan_tool_result: { count: 2, severity: 'warning', sample_seqs: [2] },
      abnormal_stop: { count: 7, severity: 'warning', sample_seqs: [3] },
      missing_tool_result: { count: 1, severity: 'error', sample_seqs: [4] },
    }),
  );
  assert.deepEqual(
    got.classes.map((c) => c.cls),
    ['missing_tool_result', 'abnormal_stop', 'orphan_tool_result', 'mixed_id_shape'],
  );
  assert.equal(got.total, 19);
  assert.equal(got.worst, 'error');
});

test('equal severity and count falls back to a stable alphabetical order', () => {
  const got = readDigestIssues(
    digest({
      orphan_tool_result: { count: 3, severity: 'warning', sample_seqs: [] },
      abnormal_stop: { count: 3, severity: 'warning', sample_seqs: [] },
      incomplete_turn: { count: 3, severity: 'warning', sample_seqs: [] },
    }),
  );
  assert.deepEqual(
    got.classes.map((c) => c.cls),
    ['abnormal_stop', 'incomplete_turn', 'orphan_tool_result'],
  );
});

test('the seek coord prefers the session ordinal and falls back to the seq', () => {
  const got = readDigestIssues(
    digest({
      missing_tool_result: {
        count: 3,
        severity: 'error',
        sample_seqs: [10, 20, 30],
        // A session-less agent folds every ordinal as 0; a partial list (the
        // pre-v5 degrade) leaves the tail undefined. Both must fall back.
        sample_ordinals: [101, 0],
        sample_ts: ['t1', 't2', 't3'],
        sample_labels: ['Bash', '', 'Edit'],
      },
    }),
  );
  const [c] = got.classes;
  assert.deepEqual(
    c.samples.map((s) => s.coord),
    [101, 20, 30],
  );
  assert.deepEqual(
    c.samples.map((s) => s.label),
    ['Bash', undefined, 'Edit'],
  );
  assert.deepEqual(
    c.samples.map((s) => s.ts),
    ['t1', 't2', 't3'],
  );
});

test('a capped sample list is flagged so the UI can say so', () => {
  const got = readDigestIssues(
    digest({
      missing_tool_result: { count: 250, severity: 'error', sample_seqs: [1, 2, 3] },
      orphan_tool_result: { count: 2, severity: 'warning', sample_seqs: [4, 5] },
    }),
  );
  assert.equal(got.classes[0].capped, true);
  assert.equal(got.classes[1].capped, false);
});

test('a class with no samples at all is still counted and marked capped', () => {
  const got = readDigestIssues(digest({ incomplete_turn: { count: 4, severity: 'warning' } }));
  assert.equal(got.total, 4);
  assert.equal(got.classes[0].samples.length, 0);
  assert.equal(got.classes[0].capped, true);
});

test('zero-count and malformed classes are dropped', () => {
  const got = readDigestIssues(
    digest({
      incomplete_turn: { count: 0, severity: 'error' },
      abnormal_stop: null,
      orphan_tool_result: 'nope',
      mixed_id_shape: { count: 1, severity: 'info', sample_seqs: [1] },
    }),
  );
  assert.deepEqual(
    got.classes.map((c) => c.cls),
    ['mixed_id_shape'],
  );
  assert.equal(got.total, 1);
});

test('an unknown severity degrades to warning rather than dropping the row', () => {
  const got = readDigestIssues(digest({ future_rule: { count: 1, severity: 'catastrophe', sample_seqs: [1] } }));
  assert.equal(got.classes[0].severity, 'warning');
});

test('the hub own worst-severity rollup wins over the client table', () => {
  // So a severity this client cannot rank yet still tints from the server.
  const got = readDigestIssues(digest({ future_rule: { count: 1, severity: 'info', sample_seqs: [1] } }, 'error'));
  assert.equal(got.worst, 'error');
});

test('worst severity is computed when the hub predates the rollup field', () => {
  const got = readDigestIssues(
    digest({
      mixed_id_shape: { count: 5, severity: 'info', sample_seqs: [1] },
      orphan_tool_result: { count: 1, severity: 'warning', sample_seqs: [2] },
    }),
  );
  assert.equal(got.worst, 'warning');
});
