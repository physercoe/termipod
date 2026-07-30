/// Tests for the dock companion's binding-key migration
/// (state/companionBinding.ts): the unified dock's `termipod.dock.agent` key
/// falls back to the retired per-surface mounts' keys so an existing agent
/// binding survives the move.
/// Run locally: `node --test src/state/companionBinding.test.ts` from
/// `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DOCK_COMPANION_KEY, loadCompanionBinding } from './companionBinding.ts';

const get =
  (m: Record<string, string>) =>
  (k: string): string | null =>
    m[k] ?? null;

test('the dock key wins when set (no fallback)', () => {
  const m = { [DOCK_COMPANION_KEY]: 'ag_new', 'termipod.read.agent': 'ag_old' };
  assert.equal(loadCompanionBinding(get(m), DOCK_COMPANION_KEY), 'ag_new');
});

test('unset dock key falls back to the retired Read mount', () => {
  assert.equal(loadCompanionBinding(get({ 'termipod.read.agent': 'ag_r' }), DOCK_COMPANION_KEY), 'ag_r');
});

test('unset dock key falls back to the retired Author mount', () => {
  assert.equal(loadCompanionBinding(get({ 'termipod.author.agent': 'ag_a' }), DOCK_COMPANION_KEY), 'ag_a');
});

test('both legacy keys present: Read wins (priority order)', () => {
  const m = { 'termipod.read.agent': 'ag_r', 'termipod.author.agent': 'ag_a' };
  assert.equal(loadCompanionBinding(get(m), DOCK_COMPANION_KEY), 'ag_r');
});

test('nothing anywhere: unbound', () => {
  assert.equal(loadCompanionBinding(get({}), DOCK_COMPANION_KEY), '');
});

test('an empty-string dock binding counts as unset and falls back', () => {
  const m = { [DOCK_COMPANION_KEY]: '', 'termipod.author.agent': 'ag_a' };
  assert.equal(loadCompanionBinding(get(m), DOCK_COMPANION_KEY), 'ag_a');
});

test('non-dock mounts do NOT fall back (their own key or nothing)', () => {
  assert.equal(loadCompanionBinding(get({ 'termipod.read.agent': 'ag_r' }), 'some.other.key'), '');
  assert.equal(loadCompanionBinding(get({ 'some.other.key': 'ag_x' }), 'some.other.key'), 'ag_x');
});
