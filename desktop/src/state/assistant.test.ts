/// Tests for the unified assistant dock's tab state (state/assistant.ts):
/// `view` persistence + switching, the `reveal` semantics the annotation flows
/// use (kimi attach → open + kimi tab; companion handoff → open + companion
/// tab), the kimi-attach availability condition (the guest is mounted — the
/// dock may be hidden), and the F1 split of the kimi tab's lifecycle out of
/// the dock's.
/// Run locally: `node --test src/state/assistant.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { effectiveView, kimiAttachable, loadView, useAssistant } from './assistant.ts';

/// Minimal in-memory localStorage stand-in (node has no Web Storage).
function stubLocalStorage(): Map<string, string> {
  const m = new Map<string, string>();
  (globalThis as { localStorage?: unknown }).localStorage = {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
  return m;
}

test('loadView: parses the persisted tab, defaults to kimi', () => {
  const store = stubLocalStorage();
  assert.equal(loadView(), 'kimi');
  store.set('termipod.assistant.view', 'companion');
  assert.equal(loadView(), 'companion');
  store.set('termipod.assistant.view', 'kimi');
  assert.equal(loadView(), 'kimi');
  // Anything unrecognized degrades to kimi.
  store.set('termipod.assistant.view', 'garbage');
  assert.equal(loadView(), 'kimi');
});

test('setView: switches the tab and persists it (like dockSide)', () => {
  const store = stubLocalStorage();
  useAssistant.getState().setView('companion');
  assert.equal(useAssistant.getState().view, 'companion');
  assert.equal(store.get('termipod.assistant.view'), 'companion');
  useAssistant.getState().setView('kimi');
  assert.equal(useAssistant.getState().view, 'kimi');
  assert.equal(store.get('termipod.assistant.view'), 'kimi');
});

test('reveal: opens the dock (starting it if needed) on the given tab', () => {
  stubLocalStorage();
  useAssistant.setState({ open: false, started: false, kimiStarted: false, detached: false, view: 'kimi' });
  // The companion-handoff case: the dock must MOUNT (started) so the
  // companion is there to take the chip, and land on the companion tab.
  useAssistant.getState().reveal('companion');
  assert.deepEqual(
    {
      open: useAssistant.getState().open,
      started: useAssistant.getState().started,
      view: useAssistant.getState().view,
    },
    { open: true, started: true, view: 'companion' },
  );
  // The kimi-attach case: from a hidden-but-started dock back onto kimi.
  useAssistant.setState({ open: false, view: 'companion' });
  useAssistant.getState().reveal('kimi');
  assert.deepEqual(
    { open: useAssistant.getState().open, started: useAssistant.getState().started, view: useAssistant.getState().view },
    { open: true, started: true, view: 'kimi' },
  );
});

test('kimiAttachable: the guest is mounted and embedded — open is irrelevant (it hides, never unmounts)', () => {
  assert.equal(kimiAttachable({ started: true, kimiStarted: true, detached: false }), true);
  assert.equal(kimiAttachable({ started: false, kimiStarted: true, detached: false }), false); // dock never opened
  // F1's new case, and the one that matters: an open dock parked on the
  // Companion has started no kimi guest, so there is nothing to inject into.
  // Before the split this read `started` alone and would have said yes.
  assert.equal(kimiAttachable({ started: true, kimiStarted: false, detached: false }), false);
  assert.equal(kimiAttachable({ started: true, kimiStarted: true, detached: true }), false); // popped out
});

test('effectiveView: a persisted kimi tab falls back to the Companion where kimi is absent', () => {
  assert.equal(effectiveView('kimi', true), 'kimi');
  assert.equal(effectiveView('companion', true), 'companion');
  assert.equal(effectiveView('kimi', false), 'companion'); // no install → no tab to show
  assert.equal(effectiveView('companion', false), 'companion');
});

test('setView: switching to kimi is what starts its server (F1 lazy lifecycle)', () => {
  stubLocalStorage();
  useAssistant.setState({ open: true, started: true, kimiStarted: false, view: 'companion' });
  // Opening the dock alone must NOT have started kimi…
  assert.equal(useAssistant.getState().kimiStarted, false);
  useAssistant.getState().setView('kimi');
  assert.equal(useAssistant.getState().kimiStarted, true);
  // …and leaving the tab does not stop it: the guest hides, it never unmounts.
  useAssistant.getState().setView('companion');
  assert.equal(useAssistant.getState().kimiStarted, true);
});

test('closeKimi: releases the kimi hold, keeps the dock and the Companion alive', () => {
  const store = stubLocalStorage();
  useAssistant.setState({ open: true, started: true, kimiStarted: true, detached: true, view: 'kimi' });
  useAssistant.getState().closeKimi();
  const s = useAssistant.getState();
  assert.equal(s.kimiStarted, false); // the server hold is gone
  assert.equal(s.detached, false);
  assert.equal(s.view, 'companion'); // fall back to the tab that always exists
  assert.equal(s.open, true); // …but the dock stays up —
  assert.equal(s.started, true); // — and the Companion keeps its stream.
  // Persisted, so the next launch doesn't respawn the server the user just stopped.
  assert.equal(store.get('termipod.assistant.view'), 'companion');
});

test('close: still resets the daemon bits but keeps the persisted view', () => {
  stubLocalStorage();
  useAssistant.setState({ open: true, started: true, kimiStarted: true, detached: false, view: 'companion' });
  useAssistant.getState().close();
  assert.equal(useAssistant.getState().open, false);
  assert.equal(useAssistant.getState().started, false);
  assert.equal(useAssistant.getState().kimiStarted, false); // the dock close drops every mount
  assert.equal(useAssistant.getState().view, 'companion'); // the tab choice survives a close
});
