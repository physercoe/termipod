/// Tests for the wall state (plan §3.1 + §5 "wall state: persistence
/// round-trip"). The reducers are pure over `WallView`, so every invariant the
/// panels depend on is asserted here without React; the store adds only
/// per-project persistence, which the last block exercises through its public
/// actions.
/// Run locally: `node --test src/state/compareWall.test.ts` from `desktop/`
/// (CI does NOT run the desktop frontend unit tests).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applySetBaseline,
  applySetFilter,
  applySetGroupBy,
  applySetSelected,
  applySetShowIdentical,
  applySetSmoothing,
  applySetXAxis,
  applyToggleBaseline,
  applyToggleRun,
  EMPTY_VIEW,
  healView,
  MAX_SMOOTHING,
  parseWall,
  trimProjects,
  useCompareWall,
  type WallView,
} from './compareWall.ts';

function view(over: Partial<WallView> = {}): WallView {
  return { ...EMPTY_VIEW, ...over };
}

test('toggleRun adds in pick order and removes', () => {
  let v = applyToggleRun(EMPTY_VIEW, 'a');
  v = applyToggleRun(v, 'b');
  // Pick order is what assigns swatch colours — appending, not sorting.
  assert.deepEqual(v.selected, ['a', 'b']);
  v = applyToggleRun(v, 'a');
  assert.deepEqual(v.selected, ['b']);
});

test('un-selecting the baseline clears the baseline', () => {
  // The invariant that matters: a baseline off the wall would keep every delta
  // column measuring against a run the user can no longer see.
  const v = applySetBaseline(applyToggleRun(applyToggleRun(EMPTY_VIEW, 'a'), 'b'), 'a');
  assert.equal(v.baseline, 'a');
  const off = applyToggleRun(v, 'a');
  assert.deepEqual(off.selected, ['b']);
  assert.equal(off.baseline, null);
});

test('pinning a run that is not on the wall selects it', () => {
  // Otherwise healView would drop the pin and the star click would look broken.
  const v = applySetBaseline(EMPTY_VIEW, 'z');
  assert.deepEqual(v.selected, ['z']);
  assert.equal(v.baseline, 'z');
});

test('toggleBaseline unpins the pinned run but leaves it selected', () => {
  const pinned = applySetBaseline(applyToggleRun(EMPTY_VIEW, 'a'), 'a');
  const off = applyToggleBaseline(pinned, 'a');
  assert.equal(off.baseline, null);
  assert.deepEqual(off.selected, ['a']);
  // A different run steals the pin rather than clearing it.
  const moved = applyToggleBaseline(applyToggleRun(pinned, 'b'), 'b');
  assert.equal(moved.baseline, 'b');
});

test('healView dedupes, drops empties, clamps smoothing and the enums', () => {
  const v = healView({
    selected: ['a', 'a', '', 'b'],
    baseline: 'ghost',
    filter: 'x',
    smoothing: 4,
    xAxis: 'wall-clock' as unknown as WallView['xAxis'],
    groupBy: '',
    showIdentical: false,
  });
  assert.deepEqual(v.selected, ['a', 'b']);
  assert.equal(v.baseline, null, 'a baseline outside the selection is not a baseline');
  assert.equal(v.smoothing, MAX_SMOOTHING);
  assert.equal(healView(view({ smoothing: -1 })).smoothing, 0);
  assert.equal(healView(view({ smoothing: Number.NaN })).smoothing, 0);
  assert.equal(v.xAxis, 'step', 'an unknown x-axis falls back, it does not ship');
  assert.equal(v.groupBy, null);
  assert.equal(v.filter, 'x', 'untouched fields survive');
  // A blob from a build that never had the field reads as the safe default.
  assert.equal(healView({ ...view(), showIdentical: undefined as unknown as boolean }).showIdentical, false);
});

test('every reducer returns its INPUT on a no-op', () => {
  // The store treats identity as "nothing changed" and skips the localStorage
  // write; a reducer that always allocated would make every keystroke a write.
  const v = view({ selected: ['a'], baseline: 'a', filter: 'q', smoothing: 0.5, groupBy: 'seed' });
  assert.equal(applyToggleRun(v, ''), v);
  assert.equal(applySetSelected(v, ['a']), v);
  assert.equal(applySetBaseline(v, 'a'), v);
  const unpinned = view();
  assert.equal(applySetBaseline(unpinned, null), unpinned, 'clearing an absent baseline changes nothing');
  assert.equal(applySetFilter(v, 'q'), v);
  assert.equal(applySetSmoothing(v, 0.5), v);
  assert.equal(applySetXAxis(v, 'step'), v);
  assert.equal(applySetGroupBy(v, 'seed'), v);
  assert.equal(applySetShowIdentical(v, false), v);
  // …and a real change does allocate.
  assert.notEqual(applySetFilter(v, 'r'), v);
});

test('parseWall: nothing, garbage, and a partial blob all land on a usable state', () => {
  assert.deepEqual(parseWall(null), { projectId: '', byProject: {} });
  assert.deepEqual(parseWall('{oops'), { projectId: '', byProject: {} });
  assert.deepEqual(parseWall('[]'), { projectId: '', byProject: {} });
  assert.deepEqual(parseWall('"a string"'), { projectId: '', byProject: {} });
  const partial = parseWall(JSON.stringify({ projectId: 'p1', byProject: { p1: { selected: ['a'] } } }));
  assert.equal(partial.projectId, 'p1');
  assert.deepEqual(partial.byProject.p1, view({ selected: ['a'] }));
});

test('parseWall heals each project independently', () => {
  const blob = JSON.stringify({
    projectId: 'p1',
    byProject: {
      p1: { selected: ['a', 'a'], baseline: 'gone', smoothing: 9, xAxis: 'nope' },
      p2: 'not an object',
      '': { selected: ['x'] },
      p3: { selected: ['c'], baseline: 'c', filter: 'lr' },
    },
  });
  const out = parseWall(blob);
  // One bad project must not cost the user the others.
  assert.deepEqual(Object.keys(out.byProject).sort(), ['p1', 'p3']);
  assert.deepEqual(out.byProject.p1.selected, ['a']);
  assert.equal(out.byProject.p1.baseline, null);
  assert.equal(out.byProject.p1.smoothing, MAX_SMOOTHING);
  assert.equal(out.byProject.p1.xAxis, 'step');
  assert.equal(out.byProject.p3.baseline, 'c');
  assert.equal(out.byProject.p3.filter, 'lr');
});

test('a wall view survives the JSON round-trip unchanged', () => {
  const v = view({
    selected: ['a', 'b'],
    baseline: 'b',
    filter: 'lr=3e-4',
    smoothing: 0.6,
    xAxis: 'relative',
    groupBy: 'seed',
    showIdentical: true,
  });
  const raw = JSON.stringify({ projectId: 'p1', byProject: { p1: v } });
  assert.deepEqual(parseWall(raw).byProject.p1, v);
});

test('trimProjects drops the oldest and never the current one', () => {
  const many: Record<string, WallView> = {};
  for (let i = 0; i < 5; i += 1) many[`p${i}`] = view({ filter: `f${i}` });
  const kept = trimProjects(many, 'p0', 3);
  assert.deepEqual(Object.keys(kept), ['p0', 'p3', 'p4']);
  // Under the cap nothing is copied at all.
  assert.equal(trimProjects(many, 'p0', 5), many);
});

test('the store remembers a view per project and restores it on return', () => {
  const s = useCompareWall.getState();
  s.setProject('proj-a');
  useCompareWall.getState().toggleRun('run-1');
  useCompareWall.getState().toggleBaseline('run-1');
  useCompareWall.getState().setFilter('adamw');
  assert.deepEqual(useCompareWall.getState().view.selected, ['run-1']);

  useCompareWall.getState().setProject('proj-b');
  // A different project starts clean — not with the previous project's runs,
  // which do not even exist under it.
  assert.deepEqual(useCompareWall.getState().view, EMPTY_VIEW as WallView);
  useCompareWall.getState().toggleRun('run-9');

  useCompareWall.getState().setProject('proj-a');
  const back = useCompareWall.getState().view;
  assert.deepEqual(back.selected, ['run-1']);
  assert.equal(back.baseline, 'run-1');
  assert.equal(back.filter, 'adamw');
});
