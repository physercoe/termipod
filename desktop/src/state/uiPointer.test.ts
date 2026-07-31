/// Tests for the D4 pointer vocabulary (docs/plans/desktop-ui-context-and-pointing.md
/// §3.4 step 5): the two renderings of a resolved element — the chip the user
/// sees before sending, and the line the AGENT reads — plus the note fold that
/// puts the user's words first. Run locally: `node --test
/// src/state/uiPointer.test.ts` from `desktop/` (CI does not run these).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { appendPointerNote, formatPointerLabel, formatPointerNote, type UiPointer } from './uiPointer.ts';

const DEPLOY: UiPointer = { tab_id: 3, ref: '@e42', role: 'button', name: 'Deploy', actionable: true };

test('the chip reads as the plan writes it', () => {
  assert.equal(formatPointerLabel(DEPLOY), '@e42 · button · "Deploy"');
  assert.equal(formatPointerLabel({ tab_id: 3, role: 'button', actionable: true }), 'button');
  // Nothing resolved but the tab: say which tab rather than showing an empty
  // chip that reads as a rendering bug.
  assert.equal(formatPointerLabel({ tab_id: 7, actionable: false }), 'tab 7');
});

test('the agent line names the element and — only when true — how to act', () => {
  const actionable = formatPointerNote(DEPLOY);
  assert.match(actionable, /the button "Deploy"/);
  assert.match(actionable, /browser tab 3/);
  assert.match(actionable, /browser_click \{ tabId: 3, ref: "@e42" \}/);

  // kimiweb / rerunweb are read-only for agents (ADR-059 D-1) — promising a
  // click there would be a lie the agent discovers only on refusal.
  const readOnly = formatPointerNote({ ...DEPLOY, actionable: false });
  assert.ok(!readOnly.includes('browser_click'));
  assert.match(readOnly, /read-only/);

  // No ref: still worth saying what was pointed at, without inventing a handle.
  const refless = formatPointerNote({ tab_id: 3, role: 'statictext', name: 'last run 3m ago', actionable: true });
  assert.ok(!refless.includes('@'));
  assert.match(refless, /the statictext "last run 3m ago"/);

  // Nothing named at all degrades to a shape, never to empty quotes.
  assert.match(formatPointerNote({ tab_id: 3, actionable: true }), /^Pointing at an? element in browser tab 3\.$/);
});

test('appendPointerNote puts the user first and never fabricates a message', () => {
  assert.equal(appendPointerNote('why is this red?', DEPLOY), `why is this red?\n${formatPointerNote(DEPLOY)}`);
  // "Just point at this" — an empty note still says something useful.
  assert.equal(appendPointerNote('   ', DEPLOY), formatPointerNote(DEPLOY));
  // No pointer, no change: the shell path (and a failed resolution) must leave
  // the user's note exactly as they typed it.
  assert.equal(appendPointerNote('why is this red?', null), 'why is this red?');
  assert.equal(appendPointerNote('why is this red?', undefined), 'why is this red?');
  assert.equal(appendPointerNote('', undefined), '');
});

test('the pointer carries references and labels only — never content', () => {
  // The type has no field for page text, and the renderings only read the
  // ones it does have. Stated as a test so a future field addition has to
  // argue with this line (ADR-062 D-2).
  const keys = Object.keys(DEPLOY).sort();
  assert.deepEqual(keys, ['actionable', 'name', 'ref', 'role', 'tab_id']);
});
