/// Tests for the split-pane rules (`plans/desktop-shell-split-pane.md` §3.1/§5).
/// The reducers are pure over `PaneState`, so every rule the shell depends on is
/// asserted here without React — the store actions only add persistence.
/// Run locally: `node --test src/state/workbench.test.ts` from `desktop/`
/// (CI does NOT run the desktop frontend unit tests).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  activeJob,
  clampSplitRatio,
  DEFAULT_SPLIT_RATIO,
  firstPinCandidate,
  applyFocusPane,
  applySetJob,
  applySetSecondary,
  applySwapPanes,
  healPanes,
  isSplitEligible,
  isSplitVisible,
  parseSplit,
  useWorkbench,
  type PaneState,
} from './workbench.ts';

const solo: PaneState = { job: 'read', secondary: null, activePane: 'primary' };
const split: PaneState = { job: 'read', secondary: 'compare', activePane: 'primary' };

test('isSplitEligible: work surfaces pair, chrome jobs do not', () => {
  assert.equal(isSplitEligible('read'), true);
  assert.equal(isSplitEligible('fleet'), true); // §7 Q1 — eligible from S1
  assert.equal(isSplitEligible('replay'), true);
  assert.equal(isSplitEligible('terminal'), false); // the always-mounted panel
  assert.equal(isSplitEligible('settings'), false); // a full-surface switch
});

test('setSecondary: pins an eligible job and focuses it', () => {
  const s = applySetSecondary(solo, 'compare');
  assert.deepEqual(s, { job: 'read', secondary: 'compare', activePane: 'secondary' });
});

test('setSecondary: the primary job is refused — no job in both panes', () => {
  assert.equal(applySetSecondary(solo, 'read'), solo);
});

test('setSecondary: terminal and settings are refused as a pane', () => {
  assert.equal(applySetSecondary(solo, 'terminal'), solo);
  assert.equal(applySetSecondary(solo, 'settings'), solo);
});

test('setSecondary(null): closes the split and forces focus to the primary', () => {
  const focused: PaneState = { ...split, activePane: 'secondary' };
  assert.deepEqual(applySetSecondary(focused, null), { job: 'read', secondary: null, activePane: 'primary' });
});

test('setJob: with no split it just switches the primary', () => {
  assert.deepEqual(applySetJob(solo, 'author'), { job: 'author', secondary: null, activePane: 'primary' });
});

test('setJob: targets the ACTIVE pane', () => {
  assert.deepEqual(applySetJob(split, 'author'), { job: 'author', secondary: 'compare', activePane: 'primary' });
  assert.deepEqual(applySetJob({ ...split, activePane: 'secondary' }, 'author'), {
    job: 'read',
    secondary: 'author',
    activePane: 'secondary',
  });
});

test('setJob: the already-pinned job swaps instead of duplicating', () => {
  // Clicking Compare's rail icon from the primary pane brings it to the primary.
  assert.deepEqual(applySetJob(split, 'compare'), { job: 'compare', secondary: 'read', activePane: 'primary' });
  // Symmetrically from the secondary pane: the clicked job lands where the user is.
  assert.deepEqual(applySetJob({ ...split, activePane: 'secondary' }, 'read'), {
    job: 'compare',
    secondary: 'read',
    activePane: 'secondary',
  });
});

test('setJob: the job already in the active pane is a no-op', () => {
  assert.equal(applySetJob(split, 'read'), split);
  const focused: PaneState = { ...split, activePane: 'secondary' };
  assert.equal(applySetJob(focused, 'compare'), focused);
});

test('setJob: a chrome job takes the primary and keeps the pin', () => {
  // Settings is a full-surface switch, so it never lands in the secondary pane
  // even when that pane is active — and the pin survives the visit.
  const s = applySetJob({ ...split, activePane: 'secondary' }, 'settings');
  assert.deepEqual(s, { job: 'settings', secondary: 'compare', activePane: 'primary' });
  assert.equal(isSplitVisible(s), false); // pinned but off screen
  assert.equal(isSplitVisible(applySetJob(s, 'read')), true); // …and back
});

test('setJob: an unknown id is refused', () => {
  assert.equal(applySetJob(solo, 'canvas' as never), solo);
});

test('swapPanes: swaps content, keeps the active POSITION', () => {
  assert.deepEqual(applySwapPanes(split), { job: 'compare', secondary: 'read', activePane: 'primary' });
  assert.deepEqual(applySwapPanes({ ...split, activePane: 'secondary' }), {
    job: 'compare',
    secondary: 'read',
    activePane: 'secondary',
  });
  assert.equal(applySwapPanes(solo), solo); // nothing to swap
});

test('setSecondary: pinning under a chrome primary parks the pin unfocused', () => {
  // Unreachable from the S1 palette (gated on an eligible primary) but the S2
  // entry points (rail Alt-click, Mod+\) call in from anywhere — the reducer
  // must never mint the state healPanes exists to repair: focus naming a pane
  // that is off screen.
  const onSettings: PaneState = { job: 'settings', secondary: null, activePane: 'primary' };
  const s = applySetSecondary(onSettings, 'compare');
  assert.deepEqual(s, { job: 'settings', secondary: 'compare', activePane: 'primary' });
  assert.deepEqual(healPanes(s), s); // already healed — nothing to repair
  assert.equal(isSplitVisible(s), false);
});

test('setJob: clicking the parked pin promotes it — chrome never swaps into a pane', () => {
  // read+compare split → visit Settings (the pin parks) → click Compare's rail
  // icon. The unguarded swap branch seated SETTINGS as the secondary pane —
  // an id `setSecondary` refuses — and isSplitVisible rendered it half-width.
  const parked: PaneState = { job: 'settings', secondary: 'compare', activePane: 'primary' };
  assert.deepEqual(applySetJob(parked, 'compare'), { job: 'compare', secondary: null, activePane: 'primary' });
});

test('swapPanes: a parked split cannot swap', () => {
  const parked: PaneState = { job: 'settings', secondary: 'compare', activePane: 'primary' };
  assert.equal(applySwapPanes(parked), parked);
});

test('focusPane: refuses a pane that is not there', () => {
  assert.equal(applyFocusPane(solo, 'secondary'), solo);
  assert.deepEqual(applyFocusPane(split, 'secondary'), { ...split, activePane: 'secondary' });
  assert.equal(applyFocusPane(split, 'primary'), split); // already active
});

test('every reducer returns its INPUT object for a no-op', () => {
  // The store treats identity as "nothing changed" and skips the persist —
  // `focusPane` fires from a capture-phase mousedown on every click in the app,
  // so a reducer that returned a fresh equal object would rewrite localStorage
  // each time. Pinning the contract here keeps that guard honest.
  assert.equal(applySetJob(solo, 'read'), solo);
  assert.equal(applySetJob(solo, 'nope' as never), solo);
  assert.equal(applySetSecondary(solo, 'read'), solo);
  assert.equal(applySetSecondary(solo, 'terminal'), solo);
  assert.equal(applyFocusPane(solo, 'primary'), solo);
  assert.equal(applyFocusPane(solo, 'secondary'), solo);
  assert.equal(applySwapPanes(solo), solo);
});

test('activeJob: the active pane wins, the primary is the fallback', () => {
  assert.equal(activeJob(solo), 'read');
  assert.equal(activeJob(split), 'read');
  assert.equal(activeJob({ ...split, activePane: 'secondary' }), 'compare');
});

test('isSplitVisible: needs a pin AND a split-eligible primary', () => {
  assert.equal(isSplitVisible(solo), false);
  assert.equal(isSplitVisible(split), true);
  assert.equal(isSplitVisible({ ...split, job: 'settings' }), false);
  assert.equal(isSplitVisible({ ...split, job: 'terminal' }), false);
});

test('healPanes: restore repairs states the persisted fields can disagree on', () => {
  // Both panes on the same job (a legacy blob, or a job renamed under it).
  assert.deepEqual(healPanes({ job: 'read', secondary: 'read', activePane: 'secondary' }), solo);
  // Ids no longer in the registry.
  assert.deepEqual(healPanes({ job: 'canvas' as never, secondary: 'nope' as never, activePane: 'secondary' }), {
    job: 'fleet',
    secondary: null,
    activePane: 'primary',
  });
  // A pin that is no longer split-eligible.
  assert.deepEqual(healPanes({ job: 'read', secondary: 'terminal', activePane: 'secondary' }), solo);
  // activePane pointing at a pane that isn't there.
  assert.deepEqual(healPanes({ job: 'read', secondary: null, activePane: 'secondary' }), solo);
  // A VALID pin under a chrome primary: the pin survives (it comes back when a
  // work surface does) but the split is off screen, so focus cannot name it —
  // otherwise `activeJob()` would report a surface the user cannot see.
  const parked = healPanes({ job: 'settings', secondary: 'compare', activePane: 'secondary' });
  assert.deepEqual(parked, { job: 'settings', secondary: 'compare', activePane: 'primary' });
  assert.equal(activeJob(parked), 'settings');
  // A healthy state is preserved verbatim (both panes, the focused one restored).
  const restored: PaneState = { job: 'read', secondary: 'compare', activePane: 'secondary' };
  assert.deepEqual(healPanes(restored), restored);
});

test('parseSplit: missing, malformed and hostile blobs degrade to no split', () => {
  const none = { secondary: null, activePane: 'primary', ratio: DEFAULT_SPLIT_RATIO, lastSecondary: null };
  assert.deepEqual(parseSplit(null), none); // never split before
  assert.deepEqual(parseSplit('{'), none); // truncated write
  assert.deepEqual(parseSplit('"compare"'), none); // legacy bare string
  assert.deepEqual(parseSplit('null'), none);
  // Field-wise by design: an unknown id drops to `null` but `activePane` is
  // parsed independently, so the pair can still disagree here. `healPanes` — which
  // every restore path runs — is what closes it, and the store never sees the gap.
  const orphan = parseSplit('{"secondary":"nope","activePane":"secondary"}');
  assert.deepEqual(orphan, { ...none, activePane: 'secondary' });
  assert.deepEqual(healPanes({ job: 'read', secondary: orphan.secondary, activePane: orphan.activePane }), solo);
  assert.deepEqual(parseSplit('{"secondary":"compare","activePane":"nonsense"}'), {
    ...none,
    secondary: 'compare',
  });
  assert.deepEqual(parseSplit('{"secondary":"compare","activePane":"secondary"}'), {
    ...none,
    secondary: 'compare',
    activePane: 'secondary',
  });
});

// ── S2: divider ratio, the toggle's memory, and the rail entry points ─────────

test('clampSplitRatio: the 0.25–0.75 band, and NaN degrades to the default', () => {
  assert.equal(clampSplitRatio(0.5), 0.5);
  assert.equal(clampSplitRatio(0.1), 0.25);
  assert.equal(clampSplitRatio(0.9), 0.75);
  assert.equal(clampSplitRatio(Number.NaN), DEFAULT_SPLIT_RATIO); // corrupt persisted value
  assert.equal(clampSplitRatio(Number.POSITIVE_INFINITY), DEFAULT_SPLIT_RATIO);
});

test('clampSplitRatio: a width also honours the per-pane pixel minimum', () => {
  // 2000px wide, 480px minimum → the band narrows to 0.25–0.75 either side of it.
  assert.equal(clampSplitRatio(0.25, 2000), 0.25); // 500px, still above the minimum
  assert.equal(clampSplitRatio(0.75, 2000), 0.75);
  // 1200px: 480/1200 = 0.4, so a 0.25 drag is refused down to 0.4 (=480px).
  assert.equal(clampSplitRatio(0.25, 1200), 0.4);
  assert.equal(clampSplitRatio(0.75, 1200), 0.6);
  // Too narrow for two minimum panes: the pixel rule is unsatisfiable, so the
  // plain band stands rather than freezing the divider at 0.5.
  assert.equal(clampSplitRatio(0.3, 800), 0.3);
  assert.equal(clampSplitRatio(0.1, 800), 0.25);
  // Exactly 2× the minimum pins it to the centre — lo === hi, not an empty band.
  assert.equal(clampSplitRatio(0.7, 960), 0.5);
});

test('firstPinCandidate: first eligible job in rail order, never the primary', () => {
  assert.equal(firstPinCandidate('read'), 'fleet'); // JOBS[0]
  assert.equal(firstPinCandidate('fleet'), 'projects'); // skips the primary
  assert.equal(firstPinCandidate('settings'), 'fleet'); // a chrome primary still answers
});

test('parseSplit: an S1-era blob has no ratio/lastSecondary and each falls back', () => {
  const s1 = parseSplit('{"secondary":"compare","activePane":"secondary"}');
  assert.deepEqual(s1, {
    secondary: 'compare',
    activePane: 'secondary',
    ratio: DEFAULT_SPLIT_RATIO,
    lastSecondary: null,
  });
  // A ratio outside the band is clamped, not discarded; a chrome `lastSecondary`
  // is dropped (it could never be pinned).
  const s2 = parseSplit('{"secondary":null,"activePane":"primary","ratio":9,"lastSecondary":"terminal"}');
  assert.deepEqual(s2, { secondary: null, activePane: 'primary', ratio: 0.75, lastSecondary: null });
  const s3 = parseSplit('{"secondary":null,"activePane":"primary","ratio":0.62,"lastSecondary":"replay"}');
  assert.deepEqual(s3, { secondary: null, activePane: 'primary', ratio: 0.62, lastSecondary: 'replay' });
});

// The store, not the reducers: `toggleSplit` composes two rules with a memory, so
// there is no pure function to point at. Seed via setState, then drive the action.
function seed(p: Partial<ReturnType<typeof useWorkbench.getState>>): void {
  useWorkbench.setState({
    job: 'read',
    secondary: null,
    activePane: 'primary',
    ratio: DEFAULT_SPLIT_RATIO,
    lastSecondary: null,
    ...p,
  });
}

test('toggleSplit: closes a live split and reopens the SAME job', () => {
  seed({ secondary: 'compare', activePane: 'secondary', lastSecondary: 'compare' });
  useWorkbench.getState().toggleSplit();
  assert.equal(useWorkbench.getState().secondary, null);
  assert.equal(useWorkbench.getState().activePane, 'primary');
  // The memory is what makes the toggle a toggle rather than a one-way close.
  assert.equal(useWorkbench.getState().lastSecondary, 'compare');
  useWorkbench.getState().toggleSplit();
  assert.equal(useWorkbench.getState().secondary, 'compare');
});

test('toggleSplit: with no memory it pins the first candidate; never the primary', () => {
  seed({ job: 'fleet' });
  useWorkbench.getState().toggleSplit();
  assert.equal(useWorkbench.getState().secondary, 'projects'); // not 'fleet'
  // A remembered job that has since become the primary is skipped too.
  seed({ job: 'compare', lastSecondary: 'compare' });
  useWorkbench.getState().toggleSplit();
  assert.equal(useWorkbench.getState().secondary, 'fleet');
});

test('toggleSplit: inert under a chrome primary — nothing pairs with Settings', () => {
  seed({ job: 'settings', lastSecondary: 'compare' });
  useWorkbench.getState().toggleSplit();
  assert.equal(useWorkbench.getState().secondary, null);
  // …and it cannot close a parked pin it can't show either.
  seed({ job: 'settings', secondary: 'compare', lastSecondary: 'compare' });
  useWorkbench.getState().toggleSplit();
  assert.equal(useWorkbench.getState().secondary, 'compare');
});

test('setRatio: clamps, and a no-op does not touch the store', () => {
  seed({ secondary: 'compare' });
  useWorkbench.getState().setRatio(0.62);
  assert.equal(useWorkbench.getState().ratio, 0.62);
  useWorkbench.getState().setRatio(2);
  assert.equal(useWorkbench.getState().ratio, 0.75);
  const before = useWorkbench.getState();
  useWorkbench.getState().setRatio(0.75);
  assert.equal(useWorkbench.getState(), before); // identical state object
});
