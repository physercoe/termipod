/// Tests for the Terminal surface's pane arithmetic (#319). The surface renders
/// the PANE set, not the active tab, so a session that is active-but-untiled is
/// invisible — the shape of the reconnect bug. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { reconcilePanes } from './panes.ts';

test('prunes ids whose tab has closed', () => {
  assert.deepEqual(reconcilePanes(['t1', 't2'], ['t2'], 't2'), ['t2']);
});

test('seeds from the active tab when nothing survives', () => {
  assert.deepEqual(reconcilePanes(['t1'], ['t2'], 't2'), ['t2']);
  assert.deepEqual(reconcilePanes([], ['t2'], 't2'), ['t2']);
  assert.deepEqual(reconcilePanes([], [], null), []);
});

// The regression this module exists for. A session opened while another tab was
// already tiled became the active tab but was tiled nowhere, so the surface kept
// showing the OLD pane. After a dropped SSH session that old pane is the dead
// one — you reconnect, it succeeds, and the screen still reads "session ended".
test('an active tab that is tiled nowhere takes the surface', () => {
  assert.deepEqual(
    reconcilePanes(['t1'], ['t1', 't2'], 't2'),
    ['t2'],
    'a newly opened session must become visible, not hide behind the tab that was tiled',
  );
});

// A split adds its new pane in the same React batch as addTab, so by the time
// this runs the new id is already tiled — rule 3 must leave the split alone
// rather than collapsing it back to one pane.
test('a split survives: an active tab already tiled keeps every other pane', () => {
  assert.deepEqual(reconcilePanes(['t1', 't2'], ['t1', 't2'], 't2'), ['t1', 't2']);
  // …and focusing the other half of the split does not drop it either.
  assert.deepEqual(reconcilePanes(['t1', 't2'], ['t1', 't2'], 't1'), ['t1', 't2']);
});

// Reconnect rebinds a tab in place (same UI id), so the pane set must not move
// at all — including when the dead tab was one half of a split.
test('rebinding a tab in place keeps its slot, split included', () => {
  const prev = ['t1', 't2'];
  assert.equal(reconcilePanes(prev, ['t1', 't2'], 't1'), prev, 'unchanged input should return the same array identity');
});

test('closing one half of a split leaves the other tiled', () => {
  assert.deepEqual(reconcilePanes(['t1', 't2'], ['t1'], 't1'), ['t1']);
});

// activeId and the tab list land in separate state updates, so a prune can be a
// render ahead of the active id. An id that names no tab must never be tiled —
// unguarded, rule 3 answers a stale activeId by evicting the live pane and
// tiling a tab that no longer exists, leaving a blank surface with sessions open.
test('a stale activeId never evicts a live pane', () => {
  assert.deepEqual(reconcilePanes(['t1'], ['t1'], 'gone'), ['t1']);
  assert.deepEqual(reconcilePanes(['t9'], [], 'gone'), []);
});
