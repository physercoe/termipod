/// Tests for the D1 bridge additions (docs/plans/desktop-ui-context-and-pointing.md
/// §3.2, ADR-062 D-6): the `ui_get_focus` tool (catalog-gated on the
/// desktop's sharing toggle, empty-never-block answer) and the `ui://focus`
/// MCP resource (list + read — the portable floor; no subscription). Runs
/// against the electron-free core like browserbridge.test.ts, with a fake
/// backend and an in-memory focus provider. Run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  dispatchHubInvoke,
  handleMcpMessage,
  READ_TOOLS,
  UI_FOCUS_RESOURCE_URI,
  type BridgeBackend,
  type McpServerDeps,
} from './browserbridge.ts';

const backend: BridgeBackend = {
  listTargets: () => [],
  sendCommand: async () => {
    throw new Error('unexpected CDP call — ui_get_focus never touches guests');
  },
};

function deps(focus: Record<string, unknown> | null, available: boolean): McpServerDeps {
  return {
    backend,
    serverInfo: { name: 'termipod-browser', version: '0.0.0-test' },
    uiFocusAvailable: () => available,
    getUiFocus: () => focus,
  };
}

const SNAPSHOT = {
  surface: 'read',
  tab: { kind: 'web', title: 'a paper', url: 'https://arxiv.org/abs/2401.00001' },
  captured_at: '2026-07-30T00:00:00.000Z',
};

interface RpcOut {
  result?: {
    tools?: Array<{ name: string; description?: string }>;
    resources?: Array<{ uri: string }>;
    contents?: Array<{ uri: string; mimeType: string; text: string }>;
    content?: Array<{ type: string; text: string }>;
    isError?: boolean;
    capabilities?: { resources?: { subscribe?: boolean } };
  };
  error?: { code: number; message: string };
}

async function rpc(d: McpServerDeps, method: string, params?: unknown): Promise<RpcOut> {
  const out = (await handleMcpMessage(
    { jsonrpc: '2.0', id: 1, method, ...(params !== undefined ? { params } : {}) },
    d,
    { scope: 'read', agentId: null },
  )) as RpcOut | null;
  assert.ok(out !== null, `${method} must answer`);
  return out;
}

// ── Catalog gating (plan §3.2 layer 1: off = no tool in any catalog) ─────────

test('tools/list: ui_get_focus appears only while the sharing toggle is on', async () => {
  assert.ok(READ_TOOLS.some((t) => t.name === 'ui_get_focus'), 'ui_get_focus must be a READ tool (every injected scope)');
  const off = await rpc(deps(SNAPSHOT, false), 'tools/list');
  assert.ok(!off.result?.tools?.some((t) => t.name === 'ui_get_focus'), 'toggle off must hide the tool');
  const on = await rpc(deps(SNAPSHOT, true), 'tools/list');
  const tool = on.result?.tools?.find((t) => t.name === 'ui_get_focus');
  assert.ok(tool !== undefined, 'toggle on must list the tool');
  // The deictic-cue steering (plan §3.2 layer 2) rides the description.
  assert.match(tool.description ?? '', /do NOT call by default/i);
  assert.match(tool.description ?? '', /what I'?m looking at/i);
});

test('tools/call ui_get_focus: returns the cached snapshot as JSON text', async () => {
  const out = await rpc(deps(SNAPSHOT, true), 'tools/call', { name: 'ui_get_focus', arguments: {} });
  const text = out.result?.content?.[0]?.text ?? '';
  assert.deepEqual(JSON.parse(text), SNAPSHOT);
  assert.equal(out.result?.isError, undefined);
});

test('tools/call ui_get_focus: no snapshot cached yet → explicit empty answer, never a block', async () => {
  const out = await rpc(deps(null, true), 'tools/call', { name: 'ui_get_focus', arguments: {} });
  const text = out.result?.content?.[0]?.text ?? '';
  assert.deepEqual(JSON.parse(text), { surface: null, captured_at: null });
  assert.equal(out.result?.isError, undefined);
});

test('tools/call ui_get_focus: toggle off → actionable isError, not unknown-tool', async () => {
  const out = await rpc(deps(SNAPSHOT, false), 'tools/call', { name: 'ui_get_focus', arguments: {} });
  assert.equal(out.result?.isError, true);
  assert.match(out.result?.content?.[0]?.text ?? '', /UI_UNAVAILABLE/);
});

test('hub-leg ui_get_focus: the sharing gate binds the tunnel path too (D1 is local-first, D5 unchanged)', async () => {
  // dispatchHubInvoke classifies ui_get_focus as a read tool — the gate at
  // the tool (not just the catalog filter) is what keeps a remote call
  // consent-gated until D5 builds its own stack.
  const off = await dispatchHubInvoke(deps(SNAPSHOT, false), { tool: 'ui_get_focus', args: {}, agent_id: 'ag_1' }, new Set(), 'desktop');
  assert.equal(off.ok, false);
  if (!off.ok) assert.match(off.error, /UI_UNAVAILABLE/);
  const on = await dispatchHubInvoke(deps(SNAPSHOT, true), { tool: 'ui_get_focus', args: {}, agent_id: 'ag_1' }, new Set(), 'desktop');
  assert.equal(on.ok, true);
});

// ── The ui://focus resource ──────────────────────────────────────────────────

test('initialize: resources capability is advertised, subscribe honestly false', async () => {
  const out = await rpc(deps(null, false), 'initialize', { protocolVersion: '2025-06-18' });
  assert.deepEqual(out.result?.capabilities?.resources, { subscribe: false, listChanged: false });
});

test('resources/list: ui://focus appears only while sharing is on', async () => {
  const off = await rpc(deps(SNAPSHOT, false), 'resources/list');
  assert.deepEqual(off.result?.resources, []);
  const on = await rpc(deps(SNAPSHOT, true), 'resources/list');
  assert.deepEqual(on.result?.resources?.map((r) => r.uri), [UI_FOCUS_RESOURCE_URI]);
});

test('resources/read ui://focus: the snapshot as application/json contents', async () => {
  const out = await rpc(deps(SNAPSHOT, true), 'resources/read', { uri: UI_FOCUS_RESOURCE_URI });
  const contents = out.result?.contents ?? [];
  assert.equal(contents.length, 1);
  assert.equal(contents[0]?.uri, UI_FOCUS_RESOURCE_URI);
  assert.equal(contents[0]?.mimeType, 'application/json');
  assert.deepEqual(JSON.parse(contents[0]?.text ?? ''), SNAPSHOT);
});

test('resources/read ui://focus: toggle off → refused, never the cached snapshot', async () => {
  // Parity with tools/call: hiding the resource from resources/list is not
  // the gate — a stray cache write while off must not be readable either.
  const out = await rpc(deps(SNAPSHOT, false), 'resources/read', { uri: UI_FOCUS_RESOURCE_URI });
  assert.equal(out.error?.code, -32002);
  assert.match(out.error?.message ?? '', /sharing is off/);
  assert.equal(out.result, undefined);
});

test('resources/read: an unknown uri is a params error, and subscriptions are not faked', async () => {
  const unknown = await rpc(deps(SNAPSHOT, true), 'resources/read', { uri: 'ui://other' });
  assert.equal(unknown.error?.code, -32602);
  // No subscribe support by design (see UI_FOCUS_RESOURCE_URI's comment):
  // the method gets the honest method-not-found, never a silent no-op.
  const sub = await rpc(deps(SNAPSHOT, true), 'resources/subscribe', { uri: UI_FOCUS_RESOURCE_URI });
  assert.equal(sub.error?.code, -32601);
});
