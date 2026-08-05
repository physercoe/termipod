/// Tests for the UIRef → focus dispatch (D6 — plan §3.4b, §5 "ref-chip parse
/// + dispatch"). The three stores it drives are pure zustand, so a click's
/// whole effect — which tier it reached, what the stores now say — is provable
/// under `node --test`. Run locally from `desktop/` (CI does not run these).
import { test } from 'node:test';
import assert from 'node:assert/strict';

// The Inspect store reads localStorage at module load; give node one before
// the module graph comes in (dynamic import below, after the stub).
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: () => null,
  setItem: () => undefined,
  removeItem: () => undefined,
};

const { canFocusUiRef, focusUiRef, focusUiRefWithUndo, isNavigableUrl } = await import('./uiRefFocus.ts');
const { useWorkbench } = await import('./workbench.ts');
const { useReplay } = await import('./replay.ts');
const { useInspect } = await import('./inspect.ts');

test('canFocusUiRef: workbench jobs yes; pseudo-surfaces and junk no', () => {
  assert.equal(canFocusUiRef({ surface: 'replay', params: {} }), true);
  assert.equal(canFocusUiRef({ surface: 'debug', params: {} }), true);
  // `vault` is a policy pseudo-surface, not a job — the chip renders but is
  // not clickable, and dispatch refuses it outright.
  assert.equal(canFocusUiRef({ surface: 'vault', params: {} }), false);
  assert.equal(canFocusUiRef({ surface: 'nonsense', params: {} }), false);
});

test('an unknown surface is refused without touching the workbench', () => {
  const before = useWorkbench.getState().job;
  assert.equal(focusUiRef({ surface: 'nonsense', params: { dataset_id: 'ds_1' } }), 'unknown');
  assert.equal(useWorkbench.getState().job, before);
});

test('replay ref with dataset+project reaches the entity tier', () => {
  const res = focusUiRef({ surface: 'replay', params: { dataset_id: 'ds_1', project_id: 'proj_1', episode_id: '3' } });
  assert.equal(res, 'entity');
  assert.equal(useWorkbench.getState().job, 'replay');
  assert.deepEqual(useReplay.getState().target, { datasetId: 'ds_1', projectId: 'proj_1', episode: 3 });
});

test('replay ref with only a dataset preselects and still counts as entity', () => {
  useReplay.setState({ target: null, selectedId: '' });
  assert.equal(focusUiRef({ surface: 'replay', params: { dataset_id: 'ds_9' } }), 'entity');
  assert.equal(useReplay.getState().selectedId, 'ds_9');
  assert.equal(useReplay.getState().target, null);
});

test('debug ref prefers the already-open tab; an unopened file stops at the surface', () => {
  useInspect.setState({
    tabs: [{ id: 'tab_a', kind: 'code', path: 'src/foo.ts', title: 'foo.ts' } as never],
    activeId: null,
  });
  assert.equal(focusUiRef({ surface: 'debug', params: { file: 'src/foo.ts' } }), 'entity');
  assert.equal(useInspect.getState().activeId, 'tab_a');
  assert.equal(useWorkbench.getState().job, 'debug');

  // The agent points at a file the user does not have open: the chip lands
  // the user on Inspect and stops — honest about its depth, no new tab.
  assert.equal(focusUiRef({ surface: 'debug', params: { file: 'src/other.ts' } }), 'surface');
  assert.equal(useInspect.getState().tabs.length, 1, 'dispatch never opens tabs');
});

test('a bare surface ref switches the job and reports the surface tier', () => {
  assert.equal(focusUiRef({ surface: 'read', params: {} }), 'surface');
  assert.equal(useWorkbench.getState().job, 'read');
});

// ── Coworking H: the undo half ──────────────────────────────────────────────

test('focusUiRefWithUndo reverses the job, and focusUiRef discards that undo', () => {
  useWorkbench.getState().setJob('author');
  const out = focusUiRefWithUndo({ surface: 'compare', params: {} });
  assert.equal(out.result, 'surface');
  assert.equal(useWorkbench.getState().job, 'compare');
  out.undo?.();
  assert.equal(useWorkbench.getState().job, 'author');

  // The click path is unchanged: a user who clicked a chip is already where
  // they wanted to be, so the wrapper drops the undo rather than offering one.
  useWorkbench.getState().setJob('author');
  assert.equal(focusUiRef({ surface: 'compare', params: {} }), 'surface');
  assert.equal(useWorkbench.getState().job, 'compare');
});

test('isNavigableUrl is http(s) only — the earlier of the two walls', () => {
  // The webview partition enforces the same rule at the guest layer; this one
  // stops a tab being MINTED for a URL that would then refuse to load.
  assert.equal(isNavigableUrl('https://example.org'), true);
  assert.equal(isNavigableUrl('http://example.org'), true);
  assert.equal(isNavigableUrl('HTTPS://EXAMPLE.ORG'), true);
  for (const bad of ['file:///etc/passwd', 'javascript:alert(1)', 'app://shell', 'data:text/html,x', '//example.org', 'example.org']) {
    assert.equal(isNavigableUrl(bad), false, bad);
  }
});
