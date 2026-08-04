import { test } from 'node:test';
import assert from 'node:assert/strict';
import { asNavigateOrder, runNavigateOrder, useAgentNavigate } from './agentNavigate.ts';
import { useWorkbench } from './workbench.ts';
import { useReadTabs } from './readTabs.ts';
import { useInspect } from './inspect.ts';
import { useReplay } from './replay.ts';

/// The renderer half of `desktop_open` (coworking lane H). This is the only
/// module in the app where something an agent SAID changes what is on the
/// user's screen, so the two properties that make that acceptable are what the
/// suite is about: it is attributed, and it is undoable.
///
/// Run: node --test src/state/agentNavigate.test.ts  (CI does NOT run these)

function order(over: Partial<Parameters<typeof runNavigateOrder>[0]> = {}): Parameters<typeof runNavigateOrder>[0] {
  return { id: 'nav-1', ref: { surface: 'replay', params: {} }, note: '', by: 'kimi-1', at: '', ...over };
}

function reset(): void {
  useAgentNavigate.setState({ banner: null });
  useWorkbench.getState().setJob('fleet');
  useReadTabs.setState({ tabs: [], activeId: null });
  useInspect.setState({ tabs: [], activeId: null });
  useReplay.setState({ selectedId: '', target: null, handoff: null });
}

test('a navigation switches the surface and raises an attributed banner', () => {
  reset();
  const depth = runNavigateOrder(order({ ref: { surface: 'compare', params: {} } }));
  assert.equal(depth, 'surface');
  assert.equal(useWorkbench.getState().job, 'compare');
  const banner = useAgentNavigate.getState().banner;
  assert.equal(banner?.by, 'kimi-1');
  assert.notEqual(banner?.undo, null);
});

test('undo puts the user back on the surface they were on', () => {
  reset();
  useWorkbench.getState().setJob('author');
  runNavigateOrder(order({ ref: { surface: 'replay', params: {} } }));
  assert.equal(useWorkbench.getState().job, 'replay');
  useAgentNavigate.getState().undo();
  assert.equal(useWorkbench.getState().job, 'author');
  // Using the undo clears the banner: leaving it up would offer to undo an undo.
  assert.equal(useAgentNavigate.getState().banner, null);
});

test('a ref that resolves to nothing raises NO banner — nothing moved to attribute', () => {
  reset();
  const depth = runNavigateOrder(order({ ref: { surface: 'nonsense', params: {} } }));
  assert.equal(depth, 'unknown');
  assert.equal(useAgentNavigate.getState().banner, null);
  assert.equal(useWorkbench.getState().job, 'fleet');
});

test('a second navigation replaces the first rather than stacking undos', () => {
  reset();
  useWorkbench.getState().setJob('author');
  runNavigateOrder(order({ id: 'nav-1', ref: { surface: 'replay', params: {} } }));
  runNavigateOrder(order({ id: 'nav-2', ref: { surface: 'compare', params: {} } }));
  assert.equal(useAgentNavigate.getState().banner?.id, 'nav-2');
  // "Put me back" after two agent jumps means back to before the last one.
  useAgentNavigate.getState().undo();
  assert.equal(useWorkbench.getState().job, 'replay');
});

test('dismiss leaves the screen where it is — it is not a silent undo', () => {
  reset();
  runNavigateOrder(order({ ref: { surface: 'compare', params: {} } }));
  useAgentNavigate.getState().dismiss();
  assert.equal(useAgentNavigate.getState().banner, null);
  assert.equal(useWorkbench.getState().job, 'compare');
});

// ── H3: ui://read?url= opens a web tab ──────────────────────────────────────

test('a read ref with a url OPENS a web tab, and undo closes the tab it opened', () => {
  reset();
  const depth = runNavigateOrder(order({ ref: { surface: 'read', params: { url: 'https://example.org/paper' } } }));
  assert.equal(depth, 'entity');
  const tabs = useReadTabs.getState().tabs;
  assert.equal(tabs.length, 1);
  assert.equal(tabs[0].kind, 'web');
  assert.equal(tabs[0].url, 'https://example.org/paper');
  // The strip label is the host until the guest reports its real title — a full
  // URL in a tab strip is unreadable.
  assert.equal(tabs[0].title, 'example.org');

  // This undo is a complete one: the tab did not exist a moment ago.
  useAgentNavigate.getState().undo();
  assert.deepEqual(useReadTabs.getState().tabs, []);
  assert.equal(useReadTabs.getState().activeId, null);
});

test('a url already open is FOCUSED, not opened twice', () => {
  reset();
  const existing = useReadTabs.getState().open({ kind: 'web', url: 'https://example.org/paper', title: 'Paper' });
  useReadTabs.getState().setActive(null);
  const depth = runNavigateOrder(order({ ref: { surface: 'read', params: { url: 'https://example.org/paper' } } }));
  assert.equal(depth, 'entity');
  assert.equal(useReadTabs.getState().tabs.length, 1, 'a second copy of a page the user already has is the wrong reading of "show me this"');
  assert.equal(useReadTabs.getState().activeId, existing);
});

test('a non-http url mints no tab at all', () => {
  reset();
  for (const url of ['file:///etc/passwd', 'javascript:alert(1)', 'app://shell/index.html', 'data:text/html,<b>x']) {
    const depth = runNavigateOrder(order({ ref: { surface: 'read', params: { url } } }));
    // The user still lands on Read — the surface is navigable — but nothing was
    // opened, and the agent is told `surface` so it cannot claim otherwise.
    assert.equal(depth, 'surface', url);
    assert.deepEqual(useReadTabs.getState().tabs, [], url);
  }
});

test('a read ref naming a tab that is gone does not focus a random one', () => {
  reset();
  useReadTabs.getState().open({ kind: 'web', url: 'https://a.example', title: 'A' });
  useReadTabs.getState().setActive(null);
  const depth = runNavigateOrder(order({ ref: { surface: 'read', params: { tab_id: 'tab_gone' } } }));
  assert.equal(depth, 'surface');
  assert.equal(useReadTabs.getState().activeId, null);
});

// ── narrowing ───────────────────────────────────────────────────────────────

test('narrowing drops a payload with no id or no surface, and never trusts `by`', () => {
  assert.equal(asNavigateOrder(null), null);
  assert.equal(asNavigateOrder({ ref: { surface: 'replay' } }), null, 'no id');
  assert.equal(asNavigateOrder({ id: 'n1' }), null, 'no ref');
  assert.equal(asNavigateOrder({ id: 'n1', ref: { surface: '' } }), null, 'empty surface');
  const anon = asNavigateOrder({ id: 'n1', ref: { surface: 'replay' }, by: '' });
  assert.equal(anon?.by, 'an agent');
  assert.deepEqual(anon?.ref.params, {});
  // Main clips the note; re-clipped here because a renderer store takes nothing
  // on trust from an IPC boundary agent input reached.
  const long = asNavigateOrder({ id: 'n1', ref: { surface: 'replay' }, note: 'x'.repeat(400) });
  assert.equal(long?.note.length, 140);
});
