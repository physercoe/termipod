/// Tests for the unified assistant dock's tab state (state/assistant.ts):
/// `view` persistence + switching, the `reveal` semantics the annotation flows
/// use (kimi attach → open + kimi tab; companion handoff → open + companion
/// tab), and the kimi-attach availability condition (started && !detached —
/// the dock may be hidden, the guest stays mounted).
/// Run locally: `node --test src/state/assistant.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { kimiAttachable, loadView, useAssistant } from './assistant.ts';

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
  useAssistant.setState({ open: false, started: false, detached: false, view: 'kimi' });
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

test('kimiAttachable: started && !detached — open is irrelevant (the guest hides, never unmounts)', () => {
  assert.equal(kimiAttachable({ started: true, detached: false }), true);
  assert.equal(kimiAttachable({ started: false, detached: false }), false); // never opened: no guest
  assert.equal(kimiAttachable({ started: true, detached: true }), false); // popped out: dock has no guest
  assert.equal(kimiAttachable({ started: false, detached: true }), false);
});

test('close: still resets the daemon bits but keeps the persisted view', () => {
  stubLocalStorage();
  useAssistant.setState({ open: true, started: true, detached: false, view: 'companion' });
  useAssistant.getState().close();
  assert.equal(useAssistant.getState().open, false);
  assert.equal(useAssistant.getState().started, false);
  assert.equal(useAssistant.getState().view, 'companion'); // the tab choice survives a close
});
