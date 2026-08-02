/// Tests for the D2.1 global annotate trigger's target resolution
/// (docs/plans/desktop-ui-context-and-pointing.md §3.4 steps 2+4): a GLOBAL
/// arm (status-bar chip / palette — no companion origin) offers "Attach to
/// kimi web" first when the panel is open and the bound companions after it; a
/// companion arm keeps D2 isolation (only the arming mount, only when bound);
/// nothing reachable → an empty list, which the overlay renders as the
/// noTarget hint. Plus the companion-registry reducers (mount-order-preserving
/// upsert, removal).
///
/// Since vision-parity F2 the result is an ORDERED LIST of uniform rows rather
/// than `{kimi: boolean, companion}` — order is part of the contract, so these
/// assert on whole lists, not on the presence of a field.
/// Run locally: `node --test src/state/annotationTargets.test.ts` from
/// `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  GLOBAL_ORIGIN,
  removeCompanion,
  resolveTargets,
  upsertCompanion,
  type AnnotationTarget,
  type CompanionTarget,
} from './annotationTargets.ts';

// The dock companion's key is the only registered one in the app now (the
// per-surface mounts are retired); a second fixture keeps the multi-entry
// registry semantics covered.
const COMPANION_A: CompanionTarget = { storageKey: 'termipod.dock.agent', agentId: 'ag_1', agentLabel: 'kimi-1' };
const COMPANION_B: CompanionTarget = { storageKey: 'termipod.dock.agent.b', agentId: 'ag_2', agentLabel: 'kimi-2' };
const ROW_A: AnnotationTarget = { kind: 'companion', ...COMPANION_A };
const ROW_B: AnnotationTarget = { kind: 'companion', ...COMPANION_B };
const KIMI: AnnotationTarget = { kind: 'kimi' };

// ── Global arm (D2.1 — status-bar chip / palette) ───────────────────────────

test('global origin carries no storageKey (no companion scopes the handoff)', () => {
  assert.equal(GLOBAL_ORIGIN.storageKey, undefined);
  assert.equal(GLOBAL_ORIGIN.agentId, undefined);
});

test('global arm, kimi open, no companion: the kimi row alone', () => {
  assert.deepEqual(resolveTargets({ kimiOpen: true, origin: GLOBAL_ORIGIN, companions: [] }), [KIMI]);
});

test('global arm, kimi closed, no companion: empty → the noTarget hint', () => {
  assert.deepEqual(resolveTargets({ kimiOpen: false, origin: GLOBAL_ORIGIN, companions: [] }), []);
});

test('global arm offers the bound companions in mount order', () => {
  assert.deepEqual(
    resolveTargets({ kimiOpen: false, origin: GLOBAL_ORIGIN, companions: [COMPANION_A, COMPANION_B] }),
    [ROW_A, ROW_B],
  );
});

test('kimi leads the list whenever its panel is open (D2.2 ordering)', () => {
  // The ordering is the contract the overlay renders straight through, so it
  // is asserted positionally: kimi at index 0, companions after it.
  assert.deepEqual(
    resolveTargets({ kimiOpen: true, origin: GLOBAL_ORIGIN, companions: [COMPANION_A, COMPANION_B] }),
    [KIMI, ROW_A, ROW_B],
  );
});

test('global arm skips unbound registrations', () => {
  const unbound: CompanionTarget = { storageKey: 'termipod.dock.agent', agentId: '', agentLabel: '' };
  assert.deepEqual(resolveTargets({ kimiOpen: false, origin: GLOBAL_ORIGIN, companions: [unbound, COMPANION_B] }), [
    ROW_B,
  ]);
});

test('a null origin resolves exactly like a global arm', () => {
  assert.deepEqual(resolveTargets({ kimiOpen: false, origin: null, companions: [COMPANION_A] }), [ROW_A]);
});

// ── Companion arm (D2 semantics, unchanged) ─────────────────────────────────

test('companion arm, bound: only the arming mount is offered', () => {
  assert.deepEqual(
    resolveTargets({
      kimiOpen: true,
      origin: { storageKey: 'termipod.dock.agent.b', agentId: 'ag_2', agentLabel: 'kimi-2' },
      companions: [COMPANION_A, COMPANION_B],
    }),
    [KIMI, ROW_B],
  );
});

test('companion arm, UNBOUND: no companion row even when another mount is bound (D2 isolation)', () => {
  assert.deepEqual(
    resolveTargets({
      kimiOpen: false,
      origin: { storageKey: 'termipod.dock.agent', agentId: '', agentLabel: '' },
      companions: [COMPANION_A],
    }),
    [],
  );
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
