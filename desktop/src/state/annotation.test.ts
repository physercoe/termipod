/// Tests for the annotation flow's dock interplay (D2.2): the annotating-hide
/// flag the assistant dock reads (`dockHiddenForPhase` — arm → hidden,
/// cancel/discard/target-pick → restored, all WITHOUT flipping the dock's
/// `open` state), the handoff routing (`handoffKey`), and the handoff reveal
/// (`handoffRevealsDock` → the store calls `reveal('companion')`, whose
/// open + companion-tab semantics are covered in assistant.test.ts, as is
/// `reveal('kimi')` for the kimi-attach case). The store itself imports the
/// shell bridge, which node ESM cannot resolve — so the decisions live in
/// annotationTargets.ts and are tested here directly.
/// Run locally: `node --test src/state/annotation.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  dockHiddenForPhase,
  GLOBAL_ORIGIN,
  handoffKey,
  handoffRevealsDock,
} from './annotationTargets.ts';
import { useAssistant } from './assistant.ts';
import { DOCK_COMPANION_KEY } from './companionBinding.ts';

test('annotating-hide: armed (selecting OR target) hides; idle restores', () => {
  assert.equal(dockHiddenForPhase('selecting'), true); // arm → hidden
  assert.equal(dockHiddenForPhase('target'), true); // the target row keeps the dock aside
  assert.equal(dockHiddenForPhase('idle'), false); // cancel / discard / pick → restored
});

test('handoffKey: explicit key (global arm) wins; origin key routes a companion arm', () => {
  assert.equal(handoffKey(GLOBAL_ORIGIN, DOCK_COMPANION_KEY), DOCK_COMPANION_KEY);
  assert.equal(handoffKey({ storageKey: DOCK_COMPANION_KEY, agentId: 'ag_1' }), DOCK_COMPANION_KEY);
  assert.equal(handoffKey(GLOBAL_ORIGIN), null); // no route → the store no-ops
  assert.equal(handoffKey(null), null);
});

test('handoffRevealsDock: only the dock companion reveals', () => {
  assert.equal(handoffRevealsDock(DOCK_COMPANION_KEY), true);
  assert.equal(handoffRevealsDock('some.other.companion'), false);
});

test('the reveal a dock-companion handoff triggers: open + started + companion tab', () => {
  useAssistant.setState({ open: false, started: false, detached: false, view: 'kimi' });
  assert.equal(handoffRevealsDock(DOCK_COMPANION_KEY), true);
  useAssistant.getState().reveal('companion');
  const s = useAssistant.getState();
  assert.deepEqual({ open: s.open, started: s.started, view: s.view }, {
    open: true,
    started: true,
    view: 'companion',
  });
});
