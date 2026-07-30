/// Tests for the D2.1 global annotate trigger's target resolution
/// (docs/plans/desktop-ui-context-and-pointing.md §3.4 steps 2+4): a GLOBAL
/// arm (status-bar chip / palette — no companion origin) offers "Attach to
/// kimi web" first when the panel is open and the first registered bound
/// companion second; a companion arm keeps D2 isolation (only the arming
/// mount, only when bound); nothing reachable → the noTarget hint. Plus the
/// companion-registry reducers (mount-order-preserving upsert, removal).
/// Run locally: `node --test src/state/annotationTargets.test.ts` from
/// `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  GLOBAL_ORIGIN,
  removeCompanion,
  resolveTargets,
  upsertCompanion,
  type CompanionTarget,
} from './annotationTargets.ts';

// The dock companion's key is the only registered one in the app now (the
// per-surface mounts are retired); a second fixture keeps the multi-entry
// registry semantics covered.
const COMPANION_A: CompanionTarget = { storageKey: 'termipod.dock.agent', agentId: 'ag_1', agentLabel: 'kimi-1' };
const COMPANION_B: CompanionTarget = { storageKey: 'termipod.dock.agent.b', agentId: 'ag_2', agentLabel: 'kimi-2' };

// ── Global arm (D2.1 — status-bar chip / palette) ───────────────────────────

test('global origin carries no storageKey (no companion scopes the handoff)', () => {
  assert.equal(GLOBAL_ORIGIN.storageKey, undefined);
  assert.equal(GLOBAL_ORIGIN.agentId, undefined);
});

test('global arm, kimi open, no companion: kimi-first row, no companion row', () => {
  const t = resolveTargets({ kimiOpen: true, origin: GLOBAL_ORIGIN, companions: [] });
  assert.equal(t.kimi, true);
  assert.equal(t.companion, null);
});

test('global arm, kimi closed, no companion: nothing reachable → noTarget hint', () => {
  const t = resolveTargets({ kimiOpen: false, origin: GLOBAL_ORIGIN, companions: [] });
  assert.equal(t.kimi, false);
  assert.equal(t.companion, null);
});

test('global arm offers the first registered BOUND companion', () => {
  const t = resolveTargets({ kimiOpen: false, origin: GLOBAL_ORIGIN, companions: [COMPANION_A, COMPANION_B] });
  assert.deepEqual(t.companion, COMPANION_A);
  // kimi open at the same time: both rows, kimi still first in the contract.
  const both = resolveTargets({ kimiOpen: true, origin: GLOBAL_ORIGIN, companions: [COMPANION_A] });
  assert.equal(both.kimi, true);
  assert.deepEqual(both.companion, COMPANION_A);
});

test('global arm skips unbound registrations', () => {
  const unbound: CompanionTarget = { storageKey: 'termipod.dock.agent', agentId: '', agentLabel: '' };
  const t = resolveTargets({ kimiOpen: false, origin: GLOBAL_ORIGIN, companions: [unbound, COMPANION_B] });
  assert.deepEqual(t.companion, COMPANION_B);
});

test('a null origin resolves exactly like a global arm', () => {
  const t = resolveTargets({ kimiOpen: false, origin: null, companions: [COMPANION_A] });
  assert.deepEqual(t.companion, COMPANION_A);
});

// ── Companion arm (D2 semantics, unchanged) ─────────────────────────────────

test('companion arm, bound: only the arming mount is offered', () => {
  const t = resolveTargets({
    kimiOpen: true,
    origin: { storageKey: 'termipod.dock.agent.b', agentId: 'ag_2', agentLabel: 'kimi-2' },
    companions: [COMPANION_A, COMPANION_B],
  });
  assert.equal(t.kimi, true);
  assert.deepEqual(t.companion, COMPANION_B);
});

test('companion arm, UNBOUND: no companion row even when another mount is bound (D2 isolation)', () => {
  const t = resolveTargets({
    kimiOpen: false,
    origin: { storageKey: 'termipod.dock.agent', agentId: '', agentLabel: '' },
    companions: [COMPANION_A],
  });
  assert.equal(t.kimi, false);
  assert.equal(t.companion, null);
});

// ── Registry reducers ────────────────────────────────────────────────────────

test('upsertCompanion appends new mounts and replaces in place (mount order preserved)', () => {
  let list: CompanionTarget[] = [];
  list = upsertCompanion(list, COMPANION_A);
  list = upsertCompanion(list, COMPANION_B);
  assert.deepEqual(list, [COMPANION_A, COMPANION_B]);
  // A re-report (label refresh / rebinding) replaces WITHOUT moving the entry.
  const refreshed: CompanionTarget = { ...COMPANION_A, agentLabel: 'kimi-1-renamed' };
  list = upsertCompanion(list, refreshed);
  assert.deepEqual(list, [refreshed, COMPANION_B]);
});

test('removeCompanion drops the entry (unmount / unbind)', () => {
  const list = removeCompanion([COMPANION_A, COMPANION_B], COMPANION_A.storageKey);
  assert.deepEqual(list, [COMPANION_B]);
  // Removing an unknown key is a no-op.
  assert.deepEqual(removeCompanion(list, 'nope'), [COMPANION_B]);
});
