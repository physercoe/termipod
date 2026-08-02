/// Tests for the comparison wall's pure run math (plan §5: "pure-function
/// tests … these are the 'silently corrupt data' class the design review said
/// to cover first").
/// Run locally: `node --test src/state/compareRuns.test.ts` from `desktop/`
/// (CI does NOT run the desktop frontend unit tests).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  deltaOf,
  deltaSign,
  flattenConfig,
  formatDelta,
  runHaystack,
  runMatchesFilter,
  type ConfigEntry,
} from './compareRuns.ts';

function keys(entries: ConfigEntry[]): string[] {
  return entries.map((e) => e.key);
}

test('flattenConfig reads the hub STRING shape, not just an object', () => {
  // runOut.ConfigJSON is a `string` (handlers_runs.go) — if the flattener only
  // handled objects, every real run would filter as if it had no config, and
  // nothing on screen would say so.
  const fromString = flattenConfig('{"lr":0.001,"model":{"depth":12}}');
  assert.deepEqual(keys(fromString), ['lr', 'model.depth']);
  assert.deepEqual(fromString[0], { key: 'lr', value: '0.001' });
  // The already-parsed shape works too (an agent-supplied object, A2's digest).
  assert.deepEqual(keys(flattenConfig({ lr: 0.001, model: { depth: 12 } })), ['lr', 'model.depth']);
});

test('flattenConfig walks arrays by index and keeps empty containers', () => {
  const e = flattenConfig({ layers: [{ w: 8 }, { w: 16 }], tags: [], opts: {}, note: null });
  assert.deepEqual(keys(e), ['layers[0].w', 'layers[1].w', 'note', 'opts', 'tags']);
  const byKey = new Map(e.map((x) => [x.key, x.value]));
  // Absent vs empty must stay distinguishable — A2's diff turns on it.
  assert.equal(byKey.get('tags'), '[]');
  assert.equal(byKey.get('opts'), '{}');
  assert.equal(byKey.get('note'), 'null');
});

test('flattenConfig sorts, so two runs written in different key order line up', () => {
  const a = flattenConfig('{"z":1,"a":2}');
  const b = flattenConfig('{"a":2,"z":1}');
  assert.deepEqual(keys(a), keys(b));
  assert.deepEqual(keys(a), ['a', 'z']);
});

test('flattenConfig degrades to no rows rather than throwing', () => {
  // One run with a hand-edited config must not blank the whole wall.
  assert.deepEqual(flattenConfig('not json at all'), []);
  assert.deepEqual(flattenConfig(''), []);
  assert.deepEqual(flattenConfig('   '), []);
  assert.deepEqual(flattenConfig(undefined), []);
  assert.deepEqual(flattenConfig(null), []);
  // A bare scalar has no key to hang on — dropped, not invented.
  assert.deepEqual(flattenConfig('42'), []);
});

const facts = {
  id: 'run_9f3a2b',
  status: 'failed',
  config: flattenConfig('{"lr":0.0003,"optimizer":{"name":"adamw"}}'),
};

test('runMatchesFilter ANDs its terms — typing narrows', () => {
  assert.equal(runMatchesFilter(facts, ''), true);
  assert.equal(runMatchesFilter(facts, '   '), true);
  assert.equal(runMatchesFilter(facts, 'adamw'), true);
  assert.equal(runMatchesFilter(facts, 'adamw failed'), true);
  // Both terms exist separately; only their conjunction decides.
  assert.equal(runMatchesFilter(facts, 'adamw completed'), false);
});

test('runMatchesFilter searches id, status, config keys AND values', () => {
  assert.equal(runMatchesFilter(facts, '9f3a'), true, 'id');
  assert.equal(runMatchesFilter(facts, 'fail'), true, 'status');
  assert.equal(runMatchesFilter(facts, 'optimizer.name'), true, 'config key');
  assert.equal(runMatchesFilter(facts, '0.0003'), true, 'config value');
  assert.equal(runMatchesFilter(facts, 'ADAMW'), true, 'case-insensitive');
  assert.equal(runMatchesFilter(facts, 'sgd'), false);
});

test('runHaystack joins on a separator a term cannot span', () => {
  // Fields are newline-joined so "failed lr" matches (two terms) but the
  // accidental concatenation "failedlr" does not.
  const hay = runHaystack(facts);
  assert.equal(hay.includes('failed\nlr=0.0003'), true);
  assert.equal(hay.includes('failedlr'), false);
});

test('deltaOf: a missing number is null, never zero', () => {
  assert.equal(deltaOf(1.5, 1), 0.5);
  assert.equal(deltaOf(1, 1), 0);
  assert.equal(deltaOf(undefined, 1), null);
  assert.equal(deltaOf(1, undefined), null);
  // "—" and "no change from baseline" read identically on a wall and mean
  // opposite things.
  assert.equal(deltaOf(Number.NaN, 1), null);
  assert.equal(deltaOf(1, Number.POSITIVE_INFINITY), null);
});

test('deltaSign reports direction only', () => {
  assert.equal(deltaSign(0.2), 'up');
  assert.equal(deltaSign(-0.2), 'down');
  assert.equal(deltaSign(0), 'flat');
});

test('formatDelta signs every value and stays compact', () => {
  assert.equal(formatDelta(0), '0');
  assert.equal(formatDelta(0.5), '+0.500');
  assert.equal(formatDelta(-0.5), '-0.500');
  assert.equal(formatDelta(2.5), '+2.50');
  assert.equal(formatDelta(-12345), '-1.2e+4');
  assert.equal(formatDelta(0.000004), '+4.0e-6');
  assert.equal(formatDelta(Number.NaN), '');
});
