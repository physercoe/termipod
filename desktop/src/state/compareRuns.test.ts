/// Tests for the comparison wall's pure run math (plan §5: "pure-function
/// tests … these are the 'silently corrupt data' class the design review said
/// to cover first").
/// Run locally: `node --test src/state/compareRuns.test.ts` from `desktop/`
/// (CI does NOT run the desktop frontend unit tests).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  aggregateCurves,
  configDiffRows,
  deltaOf,
  deltaSign,
  emaSmooth,
  extremesOf,
  flattenConfig,
  formatDelta,
  groupRunsBy,
  interpolateAt,
  mergeConfigSources,
  runHaystack,
  runMatchesFilter,
  toRelativeX,
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

// ── A2: curve math ──────────────────────────────────────────────────────────

test('extremesOf skips non-finite samples instead of poisoning the pair', () => {
  const pts = [
    { x: 0, y: 3 },
    { x: 1, y: Number.NaN },
    { x: 2, y: 1 },
    { x: 3, y: 5 },
  ];
  // One NaN in a long run would otherwise make every extreme NaN — which
  // renders as "no data" for a run that has plenty.
  assert.deepEqual(extremesOf(pts), { last: 5, min: 1, max: 5 });
  assert.deepEqual(extremesOf([]), { last: undefined, min: undefined, max: undefined });
  assert.deepEqual(extremesOf([{ x: 0, y: Number.POSITIVE_INFINITY }]), {
    last: undefined,
    min: undefined,
    max: undefined,
  });
});

test('emaSmooth is debiased — the head is not dragged toward zero', () => {
  const flat = [0, 1, 2, 3, 4, 5].map((x) => ({ x, y: 10 }));
  const smoothed = emaSmooth(flat, 0.9);
  // A constant series must smooth to itself. A plain (un-debiased) EMA seeded
  // at 0 would start near 1 and creep up — a loss curve that appears to begin
  // far below where it did.
  for (const p of smoothed) assert.ok(Math.abs(p.y - 10) < 1e-9, `got ${p.y}`);
  // Weight 0 is identity, so a caller can always smooth.
  assert.deepEqual(emaSmooth(flat, 0), flat);
  assert.deepEqual(emaSmooth(flat, Number.NaN), flat);
  // Smoothing pulls a spike down without moving x.
  const spike = [
    { x: 0, y: 1 },
    { x: 1, y: 100 },
  ];
  const out = emaSmooth(spike, 0.8);
  assert.equal(out[1].x, 1);
  assert.ok(out[1].y < 100 && out[1].y > 1, `spike smoothed to ${out[1].y}`);
  assert.equal(out.length, spike.length, 'smoothing never shortens a curve');
});

test('toRelativeX re-bases each curve on its own first step', () => {
  // A run resumed at step 5000 and a run started from scratch are comparable
  // by progress-since-start; on an absolute axis they sit in different halves.
  const resumed = [
    { x: 5000, y: 1 },
    { x: 5100, y: 2 },
  ];
  assert.deepEqual(toRelativeX(resumed), [
    { x: 0, y: 1 },
    { x: 100, y: 2 },
  ]);
  assert.deepEqual(toRelativeX([]), []);
});

// ── A2: the diff-only comparer ──────────────────────────────────────────────

test('mergeConfigSources prefers what RAN and names the conflict', () => {
  const registered = flattenConfig('{"lr":0.001,"epochs":10}');
  const logged = flattenConfig('{"lr":0.0003,"seed":7}');
  const { entries, conflicts } = mergeConfigSources(registered, logged);
  const byKey = new Map(entries.map((e) => [e.key, e.value]));
  assert.equal(byKey.get('lr'), '0.0003', 'the logged value is what actually ran');
  assert.equal(byKey.get('epochs'), '10', 'registered-only keys survive');
  assert.equal(byKey.get('seed'), '7', 'logged-only keys survive');
  // The disagreement is a finding, not something to swallow.
  assert.deepEqual(conflicts, ['lr']);
  assert.deepEqual(mergeConfigSources(registered, registered).conflicts, []);
  // Sorted, so the comparer's rows are stable.
  assert.deepEqual(
    entries.map((e) => e.key),
    ['epochs', 'lr', 'seed'],
  );
});

test('configDiffRows treats an ABSENT key as a difference', () => {
  const rows = configDiffRows([
    { id: 'a', entries: flattenConfig('{"lr":0.1,"resume_from":"ckpt"}') },
    { id: 'b', entries: flattenConfig('{"lr":0.1}') },
  ]);
  const byKey = new Map(rows.map((r) => [r.key, r]));
  assert.deepEqual(byKey.get('lr')?.values, ['0.1', '0.1']);
  assert.equal(byKey.get('lr')?.identical, true);
  // Only one run sets resume_from — that IS the difference between them, and
  // hiding the row would hide it.
  assert.deepEqual(byKey.get('resume_from')?.values, ['ckpt', undefined]);
  assert.equal(byKey.get('resume_from')?.identical, false);
});

test('configDiffRows returns the sorted key union, one cell per run in order', () => {
  const rows = configDiffRows([
    { id: 'a', entries: flattenConfig('{"z":1}') },
    { id: 'b', entries: flattenConfig('{"a":2}') },
    { id: 'c', entries: flattenConfig('{"m":3}') },
  ]);
  assert.deepEqual(
    rows.map((r) => r.key),
    ['a', 'm', 'z'],
  );
  for (const r of rows) assert.equal(r.values.length, 3, 'every row has a cell per run');
  assert.deepEqual(rows[0].values, [undefined, '2', undefined]);
  assert.equal(rows.every((r) => !r.identical), true);
});

test('configDiffRows over one run marks everything identical', () => {
  // Trivially true, and the surface refuses to render the comparer under two
  // runs — but a caller that asks anyway must not get "everything differs".
  const rows = configDiffRows([{ id: 'a', entries: flattenConfig('{"lr":0.1}') }]);
  assert.equal(rows[0].identical, true);
  assert.deepEqual(configDiffRows([]), []);
});

// ── The shared fixture: one row model, two implementations ───────────────────
// `run_config_diff` (hub/internal/hubmcpserver/run_config_diff.go) answers the
// same question for agents that this module answers for the wall, and the plan
// promises they return the SAME row model. Two implementations of one contract
// drift unless something pins them together — this file is that pin, read by
// both suites. If a flattening rule changes here, the Go test fails, and vice
// versa.

test('the comparer reproduces the shared Go/TS fixture exactly', () => {
  const here = path.dirname(fileURLToPath(import.meta.url));
  const fixture = JSON.parse(
    readFileSync(path.join(here, '../../../hub/internal/hubmcpserver/testdata/config_diff_fixture.json'), 'utf8'),
  ) as {
    runs: { id: string; registered: unknown; logged: unknown }[];
    expect_conflicts: Record<string, string[]>;
    expect_differing: number;
    expect_rows: { key: string; values: (string | null)[]; identical: boolean }[];
  };

  const conflicts: Record<string, string[]> = {};
  const runs = fixture.runs.map((r) => {
    const merged = mergeConfigSources(flattenConfig(r.registered), flattenConfig(r.logged));
    if (merged.conflicts.length > 0) conflicts[r.id] = merged.conflicts;
    return { id: r.id, entries: merged.entries };
  });
  const rows = configDiffRows(runs);

  // `null` on the wire is `undefined` in the row model — same meaning (the key
  // is absent for that run), different spelling in the two languages.
  const asWire = rows.map((r) => ({
    key: r.key,
    values: r.values.map((v) => v ?? null),
    identical: r.identical,
  }));
  assert.deepEqual(asWire, fixture.expect_rows);
  assert.deepEqual(conflicts, fixture.expect_conflicts);
  assert.equal(rows.filter((r) => !r.identical).length, fixture.expect_differing);
});

// ── A3: grouping + seed aggregation ─────────────────────────────────────────

test('groupRunsBy keeps the runs that lack the key as their own group', () => {
  const runs = [
    { id: 'a', values: new Map([['#seed', '1']]) },
    { id: 'b', values: new Map([['#seed', '1']]) },
    { id: 'c', values: new Map([['#seed', '2']]) },
    { id: 'd', values: new Map<string, string>() },
  ];
  const groups = groupRunsBy(runs, '#seed');
  assert.deepEqual(
    groups.map((g) => g.runIds),
    [['a', 'b'], ['c'], ['d']],
  );
  // A run with no seed must not vanish from a chart it was selected for.
  assert.equal(groups[2].value, undefined);
  assert.equal(groups[2].label, '—');
  assert.equal(groups[0].label, '#seed=1');
});

test('interpolateAt reads between samples and refuses to extrapolate', () => {
  const c = [
    { x: 0, y: 0 },
    { x: 10, y: 10 },
    { x: 20, y: 30 },
  ];
  assert.equal(interpolateAt(c, 0), 0);
  assert.equal(interpolateAt(c, 10), 10);
  assert.equal(interpolateAt(c, 5), 5);
  assert.equal(interpolateAt(c, 15), 20);
  // A run that stopped at step 20 must not contribute an invented value at 25.
  assert.equal(interpolateAt(c, 25), undefined);
  assert.equal(interpolateAt(c, -1), undefined);
  assert.equal(interpolateAt([], 1), undefined);
});

test('aggregateCurves averages on the union grid, not only where samples coincide', () => {
  // Two members logging at different steps — the case that makes a
  // coincidence-only mean an interleaving of raw curves wearing the word
  // "mean". At x=10 the second member is interpolated to 20.
  const a = [
    { x: 0, y: 0 },
    { x: 10, y: 10 },
    { x: 20, y: 20 },
  ];
  const b = [
    { x: 0, y: 10 },
    { x: 20, y: 30 },
  ];
  const agg = aggregateCurves([a, b]);
  assert.deepEqual(
    agg.map((p) => p.x),
    [0, 10, 20],
  );
  assert.equal(agg[1].mean, 15);
  assert.equal(agg[1].n, 2);
  // ±1 sample std: values 10 and 20 → sd = 7.0710678…
  assert.ok(Math.abs(agg[1].hi - agg[1].lo - 2 * 7.0710678) < 1e-5);
});

test('aggregateCurves drops a member outside its own range and says so', () => {
  const long = [
    { x: 0, y: 1 },
    { x: 100, y: 2 },
  ];
  const short = [
    { x: 0, y: 3 },
    { x: 10, y: 4 },
  ];
  const agg = aggregateCurves([long, short]);
  const at100 = agg.find((p) => p.x === 100);
  assert.equal(at100?.n, 1, 'the short run contributes nothing past its last step');
  assert.equal(at100?.mean, 2);
  // With one member there is no spread to estimate — a zero-width band, not a
  // band invented from a single sample.
  assert.equal(at100?.lo, 2);
  assert.equal(at100?.hi, 2);
  assert.deepEqual(aggregateCurves([]), []);
});
