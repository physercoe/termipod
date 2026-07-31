/// Tests for D5's two-class tunnel dispatch
/// (docs/plans/desktop-ui-context-and-pointing.md §3.6): the desktop now
/// answers TWO envelope kinds, and a tool must arrive under its own. That
/// check is the desktop refusing to trust the hub's routing — the hub gates
/// BY CLASS, so a ui_screenshot smuggled in a browser.invoke envelope would
/// be a capture nobody approved. `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ACTION_TOOLS,
  dispatchHubInvoke,
  READ_TOOLS,
  TUNNEL_KINDS,
  tunnelClassForTool,
  UI_TOOL_NAMES,
  type BridgeBackend,
  type McpServerDeps,
  type UiCaptureResult,
} from './browserbridge.ts';

const backend: BridgeBackend = {
  listTargets: () => [{ tabId: 7, url: 'https://a.b/c', title: 't', partition: 'persist:webtab', bridge: 'full' }],
  sendCommand: async () => ({ data: 'aGVsbG8=' }),
};

function deps(): McpServerDeps {
  return {
    backend,
    serverInfo: { name: 'termipod-browser', version: '0.0.0-test' },
    uiFocusAvailable: () => true,
    getUiFocus: () => ({ surface: 'read', captured_at: 'x' }),
    captureUi: async (): Promise<UiCaptureResult> => ({ ok: true, data_b64: 'aGk=', width: 10, height: 10 }),
  };
}

test('every tool belongs to exactly one class, and the kinds are distinct', () => {
  for (const t of READ_TOOLS) {
    assert.equal(tunnelClassForTool(t.name), UI_TOOL_NAMES.has(t.name) ? 'desktop' : 'browser', t.name);
  }
  for (const t of ACTION_TOOLS) assert.equal(tunnelClassForTool(t.name), 'browser', t.name);
  assert.equal(tunnelClassForTool('browser_nuke'), null);
  assert.notEqual(TUNNEL_KINDS.browser, TUNNEL_KINDS.desktop);
});

test('a desktop-UI tool smuggled in a browser envelope is refused', async () => {
  // The hub's browser_invoke never raises a desktop_action card, so routing
  // ui_screenshot as browser.invoke would land an unapproved capture. The
  // desktop is the authority for its own pixels: it checks rather than trusts.
  const out = await dispatchHubInvoke(deps(), { tool: 'ui_screenshot', args: {}, agent_id: 'ag_1' }, new Set(), 'browser');
  assert.equal(out.ok, false);
  if (!out.ok) assert.match(out.error, /tool_kind_mismatch/);

  const focus = await dispatchHubInvoke(deps(), { tool: 'ui_get_focus', args: {}, agent_id: 'ag_1' }, new Set(), 'browser');
  assert.equal(focus.ok, false);
});

test('a browser tool smuggled in a desktop envelope is refused too', async () => {
  const out = await dispatchHubInvoke(deps(), { tool: 'browser_list_tabs', args: {}, agent_id: 'ag_1' }, new Set(), 'desktop');
  assert.equal(out.ok, false);
  if (!out.ok) assert.match(out.error, /tool_kind_mismatch/);
});

test('each tool routes under its own kind', async () => {
  const focus = await dispatchHubInvoke(deps(), { tool: 'ui_get_focus', args: {}, agent_id: 'ag_1' }, new Set(), 'desktop');
  assert.equal(focus.ok, true);
  const tabs = await dispatchHubInvoke(deps(), { tool: 'browser_list_tabs', args: {}, agent_id: 'ag_1' }, new Set(), 'browser');
  assert.equal(tabs.ok, true);
});

test('an unknown tool is unknown before it is mismatched', async () => {
  // Order matters for the message an agent reads: "I have never heard of
  // this" is more useful than "wrong envelope".
  for (const cls of ['browser', 'desktop'] as const) {
    const out = await dispatchHubInvoke(deps(), { tool: 'ui_mind_read', args: {}, agent_id: 'ag_1' }, new Set(), cls);
    assert.deepEqual(out, { ok: false, error: 'unknown_tool' });
  }
});

test('revocation still outranks the class check', async () => {
  const out = await dispatchHubInvoke(deps(), { tool: 'ui_get_focus', args: {}, agent_id: 'ag_1' }, new Set(['ag_1']), 'desktop');
  assert.equal(out.ok, false);
  if (!out.ok) assert.match(out.error, /revoked/);
});
