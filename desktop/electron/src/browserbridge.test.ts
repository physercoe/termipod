/// Tests for the browser-bridge core (plan W1+W2+W3): fragment redaction (the
/// kimiweb-token regression), AX compaction, the MCP handshake/tool dispatch
/// against a fake backend, bearer auth, the discovery-file lifecycle — and
/// the W2 halves: scope gating (read vs action bearer), the action tools'
/// CDP sequences, partition/nav-policy refusal, and the audit trail (ring +
/// arg redaction); plus the W3 hub dispatch (dispatchHubInvoke: unknown_tool
/// / revoked gating, via:'hub' audit) and the remote-sessions fold. Run with
/// `node --test` (Node strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  ACTION_TOOLS,
  BridgeAuditRing,
  BridgeError,
  compactAxTree,
  dispatchHubInvoke,
  foldRemoteSessions,
  shouldMirrorAudit,
  handleMcpMessage,
  mintToken,
  pruneSnapshotRefs,
  READ_TOOLS,
  bridgeDiscoveryPath,
  redactBridgeArgs,
  removeBridgeDiscovery,
  startBridgeServer,
  stripFragment,
  UI_TOOL_NAMES,
  writeBridgeDiscovery,
  type AxNode,
  type BridgeAuditEntry,
  type BridgeBackend,
  type BridgeRequestContext,
  type BridgeTarget,
} from './browserbridge.ts';

const KIMIWEB_URL = 'http://127.0.0.1:17331/#token=9OmdWua4fvUgNh1nQsvdOoySJgoXxUE14APKVCeJxuk';

const TABS: BridgeTarget[] = [
  { tabId: 7, url: 'https://arxiv.org/abs/2401.00001', title: 'a paper', partition: 'persist:webtab', bridge: 'full' },
  { tabId: 9, url: KIMIWEB_URL, title: 'Kimi', partition: 'kimiweb', bridge: 'read' },
];

/// A fake backend: static tab list, canned CDP answers by method.
function fakeBackend(overrides: Partial<BridgeBackend> = {}): BridgeBackend & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    listTargets: () => TABS,
    sendCommand: async (tabId, method) => {
      calls.push(`${String(tabId)}:${method}`);
      if (method === 'Accessibility.getFullAXTree') {
        return {
          nodes: [
            { nodeId: '1', role: { value: 'WebArea' }, name: { value: 'a paper' }, childIds: ['2', '3'] },
            { nodeId: '2', role: { value: 'link' }, name: { value: 'PDF' }, backendDOMNodeId: 42 },
            { nodeId: '3', role: { value: 'StaticText' }, name: { value: 'fixture body text' } },
          ] satisfies AxNode[],
        };
      }
      if (method === 'Runtime.evaluate') return { result: { value: 'fixture body text' } };
      if (method === 'Page.captureScreenshot') return { data: 'aGVsbG8=' };
      throw new Error(`unexpected method ${method}`);
    },
    ...overrides,
  };
}

const DEPS = { serverInfo: { name: 'termipod-browser', version: '0.0.0-test' } };

// ── stripFragment ────────────────────────────────────────────────────────────

test('stripFragment: removes the hash tail, keeps everything else', () => {
  assert.equal(stripFragment(KIMIWEB_URL), 'http://127.0.0.1:17331/');
  assert.equal(stripFragment('https://a.b/c?d=e#frag'), 'https://a.b/c?d=e');
  assert.equal(stripFragment('https://a.b/c'), 'https://a.b/c');
  assert.equal(stripFragment('about:blank'), 'about:blank');
  assert.equal(stripFragment(''), '');
});

// ── discovery file lifecycle ─────────────────────────────────────────────────

test('discovery file: write (0o600) + remove', () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'tp-bb-'));
  try {
    const target = writeBridgeDiscovery(home, {
      url: 'http://127.0.0.1:41001/mcp',
      token: mintToken(),
      action_token: mintToken(),
      pid: process.pid,
      started_at: new Date().toISOString(),
      app_version: '0.0.0-test',
      bridge_path: '/app/resources/browser_bridge_stdio.mjs',
    });
    assert.equal(target, bridgeDiscoveryPath(home));
    const mode = fs.statSync(target).mode & 0o777;
    assert.equal(mode, 0o600, `mode ${mode.toString(8)}`);
    const parsed = JSON.parse(fs.readFileSync(target, 'utf8')) as { token: string };
    assert.equal(typeof parsed.token, 'string');
    removeBridgeDiscovery(home);
    assert.equal(fs.existsSync(target), false);
    removeBridgeDiscovery(home); // idempotent
  } finally {
    fs.rmSync(home, { recursive: true, force: true });
  }
});

// ── AX compaction ────────────────────────────────────────────────────────────

test('compactAxTree: roles, indent, refs on interactive nodes only', () => {
  const out = compactAxTree([
    { nodeId: '1', role: { value: 'WebArea' }, name: { value: 't' }, childIds: ['2', '3', '4'] },
    { nodeId: '2', role: { value: 'link' }, name: { value: 'Home' }, backendDOMNodeId: 11 },
    { nodeId: '3', role: { value: 'StaticText' }, name: { value: 'hello' } },
    { nodeId: '4', role: { value: 'textbox' }, name: { value: 'Search' }, value: { value: 'kimi' }, backendDOMNodeId: 12 },
  ]);
  assert.equal(
    out.text,
    ['- webarea "t"', '  - link "Home" [ref=@e1]', '  - statictext "hello"', '  - textbox "Search" = kimi [ref=@e2]'].join('\n'),
  );
  assert.deepEqual([...out.refs.entries()], [
    ['@e1', 11],
    ['@e2', 12],
  ]);
  assert.equal(out.truncated, false);
});

test('compactAxTree: ignored nodes fold but keep their children', () => {
  const out = compactAxTree([
    { nodeId: '1', role: { value: 'WebArea' }, name: { value: 't' }, childIds: ['2'] },
    { nodeId: '2', ignored: true, role: { value: 'generic' }, childIds: ['3'] },
    { nodeId: '3', role: { value: 'button' }, name: { value: 'Go' }, backendDOMNodeId: 7 },
  ]);
  assert.equal(out.text, ['- webarea "t"', '  - button "Go" [ref=@e1]'].join('\n'));
});

test('compactAxTree: node budget truncates and says so', () => {
  const nodes: AxNode[] = [{ nodeId: '1', role: { value: 'WebArea' }, childIds: ['2', '3', '4'] }];
  for (const id of ['2', '3', '4']) nodes.push({ nodeId: id, role: { value: 'link' }, name: { value: id } });
  const out = compactAxTree(nodes, { maxNodes: 2 });
  assert.equal(out.truncated, true);
  assert.match(out.text, /truncated/);
});

// ── MCP handshake + tools ────────────────────────────────────────────────────

test('initialize echoes the client protocol version + serverInfo', async () => {
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18' } },
    { ...DEPS, backend: fakeBackend() },
  );
  assert.ok(res && 'result' in res);
  const result = res.result as { protocolVersion: string; serverInfo: { name: string } };
  assert.equal(result.protocolVersion, '2025-06-18');
  assert.equal(result.serverInfo.name, 'termipod-browser');
});

test('notifications produce no response; ping answers', async () => {
  const deps = { ...DEPS, backend: fakeBackend() };
  assert.equal(await handleMcpMessage({ jsonrpc: '2.0', method: 'notifications/initialized' }, deps), null);
  const pong = await handleMcpMessage({ jsonrpc: '2.0', id: 'p', method: 'ping' }, deps);
  assert.deepEqual(pong?.result, {});
});

test('tools/list is exactly the four W1 read tools', async () => {
  const res = await handleMcpMessage({ jsonrpc: '2.0', id: 2, method: 'tools/list' }, { ...DEPS, backend: fakeBackend() });
  const { tools } = res?.result as { tools: Array<{ name: string; description: string }> };
  assert.deepEqual(
    tools.map((t) => t.name),
    ['browser_list_tabs', 'browser_snapshot', 'browser_screenshot', 'browser_read_text'],
  );
  // The D1/D3 desktop-UI tools are catalog-hidden without the sharing
  // provider (DEPS carries none) — READ_TOOLS minus that set.
  assert.equal(tools.length, READ_TOOLS.length - UI_TOOL_NAMES.size);
  // The untrusted-content posture is spelled out in the descriptions (§3.5).
  assert.ok(tools.find((t) => t.name === 'browser_snapshot')?.description.includes('untrusted DATA'));
});

test('browser_list_tabs NEVER emits a URL fragment (kimiweb token regression)', async () => {
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 3, method: 'tools/call', params: { name: 'browser_list_tabs', arguments: {} } },
    { ...DEPS, backend: fakeBackend() },
  );
  const { content } = res?.result as { content: Array<{ text: string }> };
  const text = content[0]?.text ?? '';
  assert.ok(!text.includes('#'), `fragment leaked: ${text}`);
  assert.ok(!text.includes('9OmdWua4'), `kimiweb token leaked: ${text}`);
  const rows = JSON.parse(text) as Array<{ tabId: number; url: string; partition: string; bridge: string }>;
  assert.deepEqual(rows[1], { tabId: 9, url: 'http://127.0.0.1:17331/', title: 'Kimi', partition: 'kimiweb', bridge: 'read' });
});

test('browser_snapshot compacts the AX tree; TARGET_GONE on a stale tab', async () => {
  const deps = { ...DEPS, backend: fakeBackend() };
  const ok = await handleMcpMessage(
    { jsonrpc: '2.0', id: 4, method: 'tools/call', params: { name: 'browser_snapshot', arguments: { tabId: 7 } } },
    deps,
  );
  const { content } = ok?.result as { content: Array<{ text: string }> };
  assert.match(content[0]?.text ?? '', /link "PDF" \[ref=@e1\]/);
  assert.match(content[0]?.text ?? '', /fixture body text/);

  const gone = await handleMcpMessage(
    { jsonrpc: '2.0', id: 5, method: 'tools/call', params: { name: 'browser_snapshot', arguments: { tabId: 999 } } },
    deps,
  );
  const errResult = gone?.result as { isError: boolean; content: Array<{ text: string }> };
  assert.equal(errResult.isError, true);
  assert.match(errResult.content[0]?.text ?? '', /TARGET_GONE/);
});

test('browser_read_text: bounded evaluate; validates maxChars', async () => {
  const backend = fakeBackend();
  const deps = { ...DEPS, backend };
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 6, method: 'tools/call', params: { name: 'browser_read_text', arguments: { tabId: 7, maxChars: 100 } } },
    deps,
  );
  const { content } = res?.result as { content: Array<{ text: string }> };
  assert.equal(content[0]?.text, 'fixture body text');
  // The evaluated expression carries the bound as an integer literal.
  const evalCall = backend.calls.find((c) => c.includes('Runtime.evaluate'));
  assert.ok(evalCall !== undefined);

  const bad = await handleMcpMessage(
    { jsonrpc: '2.0', id: 7, method: 'tools/call', params: { name: 'browser_read_text', arguments: { tabId: 7, maxChars: -5 } } },
    deps,
  );
  assert.equal((bad?.result as { isError?: boolean }).isError, true);
});

test('browser_screenshot returns image content', async () => {
  const res = await handleMcpMessage(
    { jsonrpc: '2.0', id: 8, method: 'tools/call', params: { name: 'browser_screenshot', arguments: { tabId: 7 } } },
    { ...DEPS, backend: fakeBackend() },
  );
  const { content } = res?.result as { content: Array<{ type: string; mimeType?: string; data?: string }> };
  assert.equal(content[0]?.type, 'image');
  assert.equal(content[0]?.mimeType, 'image/png');
});

test('unknown tool → -32602; backend BridgeError → isError with its code', async () => {
  const deps = { ...DEPS, backend: fakeBackend() };
  const unknown = await handleMcpMessage(
    { jsonrpc: '2.0', id: 9, method: 'tools/call', params: { name: 'browser_defenestrate', arguments: { tabId: 7 } } },
    deps,
  );
  assert.equal(unknown?.error?.code, -32602);

  const busy = await handleMcpMessage(
    { jsonrpc: '2.0', id: 10, method: 'tools/call', params: { name: 'browser_snapshot', arguments: { tabId: 7 } } },
    {
      ...DEPS,
      backend: fakeBackend({
        sendCommand: async () => {
          throw new BridgeError('DEBUGGER_BUSY', 'devtools is attached');
        },
      }),
    },
  );
  const errResult = busy?.result as { isError: boolean; content: Array<{ text: string }> };
  assert.match(errResult.content[0]?.text ?? '', /DEBUGGER_BUSY/);
});

test('unknown method → -32601', async () => {
  const res = await handleMcpMessage({ jsonrpc: '2.0', id: 11, method: 'prompts/list' }, { ...DEPS, backend: fakeBackend() });
  assert.equal(res?.error?.code, -32601);
});

// ── HTTP server: bearer auth + framing ───────────────────────────────────────

test('HTTP server: 401 without bearer, 404 off-path, round-trip with it', async () => {
  const token = mintToken();
  const server = await startBridgeServer({ ...DEPS, backend: fakeBackend(), token });
  try {
    const base = `http://127.0.0.1:${String(server.port)}`;

    const noAuth = await fetch(`${base}/mcp`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'ping' }),
    });
    assert.equal(noAuth.status, 401);

    const wrongPath = await fetch(`${base}/nope`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}` },
      body: '{}',
    });
    assert.equal(wrongPath.status, 404);

    const ok = await fetch(`${base}/mcp`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18' } }),
    });
    assert.equal(ok.status, 200);
    const body = (await ok.json()) as { result: { serverInfo: { name: string } } };
    assert.equal(body.result.serverInfo.name, 'termipod-browser');

    // A notification gets 202 + an empty body (the stdio relay writes nothing).
    const notif = await fetch(`${base}/mcp`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
      body: JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }),
    });
    assert.equal(notif.status, 202);
    assert.equal(await notif.text(), '');

    const garbage = await fetch(`${base}/mcp`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
      body: 'not json',
    });
    assert.equal(garbage.status, 400);
  } finally {
    await server.close();
  }
});

// ── W2: scope gating, action tools, audit ────────────────────────────────────

const FULL_CTX: BridgeRequestContext = { scope: 'full', agentId: 'agent-test' };

/// An action-capable fake: params-aware canned CDP answers, with the call log
/// carrying params so tests can assert the exact dispatch sequence.
function actionBackend(
  overrides: Partial<BridgeBackend> = {},
): BridgeBackend & { calls: Array<{ tabId: number; method: string; params?: Record<string, unknown> }> } {
  const calls: Array<{ tabId: number; method: string; params?: Record<string, unknown> }> = [];
  return {
    calls,
    listTargets: () => TABS,
    activeTabId: () => 7,
    sendCommand: async (tabId, method, params) => {
      calls.push({ tabId, method, params });
      switch (method) {
        case 'Accessibility.getFullAXTree':
          return {
            nodes: [
              { nodeId: '1', role: { value: 'WebArea' }, name: { value: 'a paper' }, childIds: ['2', '3'] },
              { nodeId: '2', role: { value: 'link' }, name: { value: 'PDF' }, backendDOMNodeId: 42 },
              { nodeId: '3', role: { value: 'StaticText' }, name: { value: 'fixture body text' } },
            ] satisfies AxNode[],
          };
        case 'DOM.resolveNode':
          return { object: { objectId: `obj-${String(params?.backendNodeId ?? 'x')}` } };
        case 'Runtime.callFunctionOn': {
          const decl = String(params?.functionDeclaration ?? '');
          if (decl.includes('getBoundingClientRect')) return { result: { value: { x: 100, y: 50, w: 40, h: 20 } } };
          return { result: { value: null } }; // focus()
        }
        case 'Runtime.evaluate': {
          const expr = String(params?.expression ?? '');
          if (expr.startsWith('document.querySelector')) {
            return expr.includes('missing') ? { result: { subtype: 'null' } } : { result: { objectId: 'obj-sel', subtype: 'node' } };
          }
          if (expr.startsWith('[window.innerWidth')) return { result: { value: [800, 600] } };
          if (expr === 'thrower()') return { exceptionDetails: { text: 'Uncaught', exception: { description: 'Error: boom' } } };
          return { result: { value: 'fixture body text' } };
        }
        case 'Page.navigate':
          return params?.url === 'http://127.0.0.1:9/dead' ? { errorText: 'net::ERR_CONNECTION_REFUSED' } : {};
        case 'Page.captureScreenshot':
          return { data: 'aGVsbG8=' };
        case 'Input.dispatchMouseEvent':
        case 'Input.dispatchKeyEvent':
        case 'Input.insertText':
        case 'DOM.setFileInputFiles':
        case 'Runtime.releaseObject':
          return {};
        default:
          throw new Error(`unexpected method ${method}`);
      }
    },
    ...overrides,
  };
}

interface ToolResult {
  isError?: boolean;
  content: Array<{ text: string }>;
}

async function callTool2(deps: Parameters<typeof handleMcpMessage>[1], name: string, args: Record<string, unknown>, ctx: BridgeRequestContext = FULL_CTX): Promise<ToolResult> {
  const res = await handleMcpMessage({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } }, deps, ctx);
  return res?.result as ToolResult;
}

function auditDeps(backend: BridgeBackend): { deps: Parameters<typeof handleMcpMessage>[1]; entries: BridgeAuditEntry[] } {
  const entries: BridgeAuditEntry[] = [];
  return { deps: { ...DEPS, backend, onAction: (e) => entries.push(e) }, entries };
}

test('W2 scope gating: tools/list per scope; action call refused under read scope', async () => {
  const deps = { ...DEPS, backend: actionBackend() };
  // DEPS has no D1 ui-focus provider: the desktop-UI tools stay catalog-hidden.
  const visibleReads = READ_TOOLS.filter((t) => !UI_TOOL_NAMES.has(t.name));
  const readList = await handleMcpMessage({ jsonrpc: '2.0', id: 1, method: 'tools/list' }, deps, { scope: 'read', agentId: null });
  const readTools = (readList?.result as { tools: Array<{ name: string }> }).tools.map((t) => t.name);
  assert.deepEqual(readTools, visibleReads.map((t) => t.name));

  const fullList = await handleMcpMessage({ jsonrpc: '2.0', id: 2, method: 'tools/list' }, deps, FULL_CTX);
  const fullTools = (fullList?.result as { tools: Array<{ name: string }> }).tools.map((t) => t.name);
  assert.deepEqual(fullTools, [...visibleReads, ...ACTION_TOOLS].map((t) => t.name));
  assert.ok(fullTools.includes('browser_click') && fullTools.includes('browser_eval'));

  const refused = await callTool2(deps, 'browser_click', { tabId: 7, selector: '#go' }, { scope: 'read', agentId: null });
  assert.equal(refused.isError, true);
  assert.match(refused.content[0]?.text ?? '', /SCOPE_READ_ONLY/);
  assert.match(refused.content[0]?.text ?? '', /browser_bridge: true/);
});

test('W2 partition gate: action tools refuse read-only partitions', async () => {
  const deps = { ...DEPS, backend: actionBackend() };
  const click = await callTool2(deps, 'browser_click', { tabId: 9, selector: '#go' });
  assert.match(click.content[0]?.text ?? '', /PARTITION_READ_ONLY/);
  // The gate fires before any navigation policy is consulted.
  const nav = await callTool2(deps, 'browser_navigate', { tabId: 9, url: 'http://127.0.0.1:1/' });
  assert.match(nav.content[0]?.text ?? '', /PARTITION_READ_ONLY/);
});

test('W2 browser_navigate: partition policy enforced, fragment stripped in result', async () => {
  const deps = { ...DEPS, backend: actionBackend() };
  const ok = await callTool2(deps, 'browser_navigate', { tabId: 7, url: 'https://example.com/page#secret' });
  assert.notEqual(ok.isError, true);
  assert.ok(!(ok.content[0]?.text ?? '').includes('#'), `fragment leaked: ${ok.content[0]?.text ?? ''}`);

  for (const url of ['javascript:alert(1)', 'data:text/html,<b>x</b>', 'file:///etc/passwd']) {
    const denied = await callTool2(deps, 'browser_navigate', { tabId: 7, url });
    assert.equal(denied.isError, true, url);
    assert.match(denied.content[0]?.text ?? '', /NAVIGATION_DENIED/);
  }
  const failed = await callTool2(deps, 'browser_navigate', { tabId: 7, url: 'http://127.0.0.1:9/dead' });
  assert.match(failed.content[0]?.text ?? '', /NAVIGATION_FAILED/);
  const empty = await callTool2(deps, 'browser_navigate', { tabId: 7, url: ' ' });
  assert.match(empty.content[0]?.text ?? '', /INVALID_PARAMS/);
});

test('W2 browser_find_tab: url/title/active matching, ambiguity, no-match', async () => {
  const deps = { ...DEPS, backend: actionBackend() };
  const byUrl = await callTool2(deps, 'browser_find_tab', { url: 'ARXIV' });
  assert.match(byUrl.content[0]?.text ?? '', /"tabId": 7/);
  const byTitle = await callTool2(deps, 'browser_find_tab', { title: 'kimi' });
  assert.match(byTitle.content[0]?.text ?? '', /"tabId": 9/);
  const active = await callTool2(deps, 'browser_find_tab', { active: true });
  assert.match(active.content[0]?.text ?? '', /"tabId": 7/);

  const noArgs = await callTool2(deps, 'browser_find_tab', {});
  assert.match(noArgs.content[0]?.text ?? '', /INVALID_PARAMS/);
  const noMatch = await callTool2(deps, 'browser_find_tab', { url: 'no-such-host' });
  assert.match(noMatch.content[0]?.text ?? '', /TARGET_GONE/);
  const noActive = await callTool2(deps, 'browser_find_tab', { active: true });
  assert.notEqual(noActive.isError, true); // baseline sanity before the override below

  const inactiveDeps = { ...DEPS, backend: actionBackend({ activeTabId: undefined }) };
  const noActiveTab = await callTool2(inactiveDeps, 'browser_find_tab', { active: true });
  assert.match(noActiveTab.content[0]?.text ?? '', /TARGET_GONE/);

  const dupTabs: BridgeTarget[] = [
    { tabId: 7, url: 'https://arxiv.org/abs/1', title: 'one', partition: 'persist:webtab', bridge: 'full' },
    { tabId: 8, url: 'https://arxiv.org/abs/2', title: 'two', partition: 'persist:webtab', bridge: 'full' },
  ];
  const dupDeps = { ...DEPS, backend: actionBackend({ listTargets: () => dupTabs }) };
  const ambiguous = await callTool2(dupDeps, 'browser_find_tab', { url: 'arxiv' });
  assert.match(ambiguous.content[0]?.text ?? '', /AMBIGUOUS/);
});

test('W2 browser_click: ref path resolves through the latest snapshot, audited', async () => {
  const backend = actionBackend();
  const { deps, entries } = auditDeps(backend);
  await callTool2(deps, 'browser_snapshot', { tabId: 7 }); // mints @e1 → 42
  const ok = await callTool2(deps, 'browser_click', { tabId: 7, ref: '@e1' });
  assert.notEqual(ok.isError, true);
  assert.match(ok.content[0]?.text ?? '', /clicked at \(100, 50\)/);

  const methods = backend.calls.map((c) => c.method);
  assert.deepEqual(
    methods.slice(1),
    ['DOM.resolveNode', 'Runtime.callFunctionOn', 'Input.dispatchMouseEvent', 'Input.dispatchMouseEvent', 'Runtime.releaseObject'],
  );
  assert.equal(backend.calls[1]?.params?.backendNodeId, 42);
  assert.equal(backend.calls[3]?.params?.type, 'mousePressed');
  assert.equal(backend.calls[4]?.params?.type, 'mouseReleased');

  assert.equal(entries.length, 1);
  assert.deepEqual(entries[0]?.args, { tabId: 7, ref: '@e1' });
  assert.equal(entries[0]?.ok, true);
  assert.equal(entries[0]?.agent_id, 'agent-test');
  assert.equal(entries[0]?.partition, 'persist:webtab');
});

test('W2 pruneSnapshotRefs: a destroyed tab\'s refs stop resolving (REF_STALE)', async () => {
  // The host calls this from the guest's `destroyed` hook — without it the
  // last ref map of every tab that ever snapshotted leaks for the process
  // lifetime, and a recycled webContents id could resolve refs minted on the
  // tab that previously owned the id.
  const backend = actionBackend();
  const { deps } = auditDeps(backend);
  await callTool2(deps, 'browser_snapshot', { tabId: 7 }); // mints @e1 → 42
  pruneSnapshotRefs(7);
  const out = await callTool2(deps, 'browser_click', { tabId: 7, ref: '@e1' });
  assert.equal(out.isError, true);
  assert.match(out.content[0]?.text ?? '', /REF_STALE/);
});

test('W2 browser_click: selector path + element failure codes', async () => {
  const backend = actionBackend();
  const { deps } = auditDeps(backend);
  const ok = await callTool2(deps, 'browser_click', { tabId: 7, selector: '#go' });
  assert.notEqual(ok.isError, true);

  const missing = await callTool2(deps, 'browser_click', { tabId: 7, selector: '#missing' });
  assert.match(missing.content[0]?.text ?? '', /ELEMENT_NOT_FOUND/);

  await callTool2(deps, 'browser_snapshot', { tabId: 7 });
  const stale = await callTool2(deps, 'browser_click', { tabId: 7, ref: '@e9' });
  assert.match(stale.content[0]?.text ?? '', /REF_STALE/);

  const zeroDeps = {
    ...DEPS,
    backend: actionBackend({
      sendCommand: async (tabId, method, params) => {
        if (method === 'Runtime.callFunctionOn') return { result: { value: { x: 0, y: 0, w: 0, h: 0 } } };
        if (method === 'Runtime.evaluate') return { result: { objectId: 'o', subtype: 'node' } };
        if (method === 'Runtime.releaseObject') return {};
        throw new Error(`unexpected ${method} on tab ${String(tabId)} ${String(params)}`);
      },
    }),
  };
  const invisible = await callTool2(zeroDeps, 'browser_click', { tabId: 7, selector: '#hidden' });
  assert.match(invisible.content[0]?.text ?? '', /ELEMENT_NOT_VISIBLE/);

  const neither = await callTool2(deps, 'browser_click', { tabId: 7 });
  assert.match(neither.content[0]?.text ?? '', /INVALID_PARAMS/);
  const both = await callTool2(deps, 'browser_click', { tabId: 7, ref: '@e1', selector: '#go' });
  assert.match(both.content[0]?.text ?? '', /INVALID_PARAMS/);
});

test('W2 browser_type: focus + insertText; text redacted in reply AND audit', async () => {
  const backend = actionBackend();
  const { deps, entries } = auditDeps(backend);
  const ok = await callTool2(deps, 'browser_type', { tabId: 7, selector: '#q', text: 's3cret' });
  assert.notEqual(ok.isError, true);
  assert.equal(ok.content[0]?.text, 'typed 6 chars');
  assert.ok(!JSON.stringify(ok).includes('s3cret'), 'reply echoes the typed text');

  const calls = backend.calls.map((c) => `${c.method}:${String((c.params?.functionDeclaration ?? c.method) as string).slice(0, 20)}`);
  assert.ok(backend.calls.some((c) => c.method === 'Input.insertText' && c.params?.text === 's3cret'), calls.join('\n'));

  assert.equal(entries.length, 1);
  assert.equal(entries[0]?.args.text, '<redacted 6 chars>');
  assert.ok(!JSON.stringify(entries).includes('s3cret'), 'audit leaks the typed text');

  const empty = await callTool2(deps, 'browser_type', { tabId: 7, selector: '#q', text: '' });
  assert.match(empty.content[0]?.text ?? '', /INVALID_PARAMS/);
});

test('W2 browser_send_keys: named keys, chords, single-char audit redaction', async () => {
  const backend = actionBackend();
  const { deps, entries } = auditDeps(backend);

  const enter = await callTool2(deps, 'browser_send_keys', { tabId: 7, keys: 'Enter' });
  assert.notEqual(enter.isError, true);
  const down = backend.calls.find((c) => c.method === 'Input.dispatchKeyEvent');
  assert.equal(down?.params?.type, 'keyDown');
  assert.equal(down?.params?.text, '\r');
  assert.equal(down?.params?.windowsVirtualKeyCode, 13);

  backend.calls.length = 0;
  await callTool2(deps, 'browser_send_keys', { tabId: 7, keys: 'Control+A' });
  const chord = backend.calls.find((c) => c.method === 'Input.dispatchKeyEvent');
  assert.equal(chord?.params?.type, 'rawKeyDown');
  assert.equal(chord?.params?.modifiers, 2);
  assert.equal(chord?.params?.text, undefined);

  backend.calls.length = 0;
  await callTool2(deps, 'browser_send_keys', { tabId: 7, keys: 'Shift+ArrowLeft' });
  const arrow = backend.calls.find((c) => c.method === 'Input.dispatchKeyEvent');
  assert.equal(arrow?.params?.modifiers, 8);
  assert.equal(arrow?.params?.key, 'ArrowLeft');

  await callTool2(deps, 'browser_send_keys', { tabId: 7, keys: 'x' });
  const char = backend.calls.findLast((c) => c.method === 'Input.dispatchKeyEvent' && c.params?.type === 'keyDown');
  assert.equal(char?.params?.text, 'x');
  assert.equal(char?.params?.code, 'KeyX');
  assert.equal(entries.at(-1)?.args.keys, '<redacted char>');

  const bad1 = await callTool2(deps, 'browser_send_keys', { tabId: 7, keys: 'Control+Enter+A' });
  assert.match(bad1.content[0]?.text ?? '', /INVALID_PARAMS/);
  const bad2 = await callTool2(deps, 'browser_send_keys', { tabId: 7, keys: 'Foonicate' });
  assert.match(bad2.content[0]?.text ?? '', /INVALID_PARAMS/);
});

test('W2 browser_scroll: wheel deltas at the viewport center', async () => {
  const backend = actionBackend();
  const { deps } = auditDeps(backend);
  await callTool2(deps, 'browser_scroll', { tabId: 7 });
  const wheel = backend.calls.find((c) => c.method === 'Input.dispatchMouseEvent');
  assert.deepEqual(
    { type: wheel?.params?.type, x: wheel?.params?.x, y: wheel?.params?.y, deltaY: wheel?.params?.deltaY },
    { type: 'mouseWheel', x: 400, y: 300, deltaY: 240 },
  );

  backend.calls.length = 0;
  await callTool2(deps, 'browser_scroll', { tabId: 7, dy: -100 });
  assert.equal(backend.calls.find((c) => c.method === 'Input.dispatchMouseEvent')?.params?.deltaY, -100);

  const bad = await callTool2(deps, 'browser_scroll', { tabId: 7, dy: 'lots' });
  assert.match(bad.content[0]?.text ?? '', /INVALID_PARAMS/);
});

test('W2 browser_upload_file: path validation + setFileInputFiles', async () => {
  const backend = actionBackend();
  const { deps } = auditDeps(backend);
  const tmp = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'tp-bb-up-')), 'a.txt');
  fs.writeFileSync(tmp, 'hi');

  const ok = await callTool2(deps, 'browser_upload_file', { tabId: 7, selector: 'input[type=file]', paths: [tmp] });
  assert.notEqual(ok.isError, true);
  const set = backend.calls.find((c) => c.method === 'DOM.setFileInputFiles');
  assert.deepEqual(set?.params?.files, [tmp]);

  for (const [label, paths, pattern] of [
    ['relative', ['docs/a.txt'], /INVALID_PARAMS/],
    ['missing', ['/no/such/file.bin'], /INVALID_PARAMS/],
    ['a directory', [os.tmpdir()], /INVALID_PARAMS/],
    ['empty', [], /INVALID_PARAMS/],
  ] as const) {
    const res = await callTool2(deps, 'browser_upload_file', { tabId: 7, selector: 'input[type=file]', paths: [...paths] });
    assert.match(res.content[0]?.text ?? '', pattern, label);
  }
});

test('W2 browser_eval: JSON result, exception, truncation cap', async () => {
  const { deps } = auditDeps(actionBackend());
  const ok = await callTool2(deps, 'browser_eval', { tabId: 7, expression: 'document.title' });
  assert.equal(ok.content[0]?.text, '"fixture body text"');

  const thrown = await callTool2(deps, 'browser_eval', { tabId: 7, expression: 'thrower()' });
  assert.equal(thrown.isError, true);
  assert.match(thrown.content[0]?.text ?? '', /EVAL_EXCEPTION/);
  assert.match(thrown.content[0]?.text ?? '', /boom/);

  const empty = await callTool2(deps, 'browser_eval', { tabId: 7, expression: ' ' });
  assert.match(empty.content[0]?.text ?? '', /INVALID_PARAMS/);

  const longDeps = {
    ...DEPS,
    backend: actionBackend({
      sendCommand: async (_tabId, method) => {
        if (method === 'Runtime.evaluate') return { result: { value: 'x'.repeat(9000) } };
        throw new Error(`unexpected ${method}`);
      },
    }),
  };
  const capped = await callTool2(longDeps, 'browser_eval', { tabId: 7, expression: 'big()' });
  assert.match(capped.content[0]?.text ?? '', /truncated/);
  assert.ok((capped.content[0]?.text ?? '').length < 9000);
});

test('W2 audit: one redacted entry per action call, success or failure; reads unaudited', async () => {
  const { deps, entries } = auditDeps(actionBackend());
  await callTool2(deps, 'browser_snapshot', { tabId: 7 });
  assert.equal(entries.length, 0, 'read tools are not audited');

  await callTool2(deps, 'browser_navigate', { tabId: 999, url: 'https://example.com/' });
  assert.equal(entries.length, 1);
  assert.equal(entries[0]?.ok, false);
  assert.equal(entries[0]?.error, 'TARGET_GONE');
  assert.equal(entries[0]?.url, null);

  await callTool2(deps, 'browser_navigate', { tabId: 7, url: 'https://example.com/#frag' }, { scope: 'full', agentId: null });
  assert.equal(entries.length, 2);
  assert.equal(entries[1]?.ok, true);
  assert.equal(entries[1]?.agent_id, 'unknown');
  assert.equal(entries[1]?.args.url, 'https://example.com/', 'fragment stripped in the audit args');
});

test('W2 redactBridgeArgs + BridgeAuditRing units', () => {
  assert.equal(redactBridgeArgs('browser_type', { text: 'hunter2' }).text, '<redacted 7 chars>');
  assert.equal(redactBridgeArgs('browser_eval', { expression: 'x'.repeat(300) }).expression, `${'x'.repeat(200)}… (300 chars)`);
  assert.equal(redactBridgeArgs('browser_eval', { expression: '1+1' }).expression, '1+1');
  assert.equal(redactBridgeArgs('browser_send_keys', { keys: 'a' }).keys, '<redacted char>');
  assert.equal(redactBridgeArgs('browser_send_keys', { keys: 'Enter' }).keys, 'Enter');
  assert.equal(redactBridgeArgs('browser_navigate', { url: 'https://a.b/c#tok' }).url, 'https://a.b/c');

  const ring = new BridgeAuditRing(50);
  for (let i = 0; i < 60; i += 1) {
    ring.push({
      ts: `t${String(i)}`,
      tool: 'browser_click',
      agent_id: 'a',
      via: 'local',
      tab_id: 7,
      url: null,
      partition: null,
      args: {},
      ok: true,
      error: null,
    });
  }
  assert.equal(ring.list().length, 50);
  assert.equal(ring.list()[0]?.ts, 't10', 'oldest entries drop off');
});

test('W2 HTTP: action bearer grants full scope; x-tp-agent-id flows into the audit', async () => {
  const token = mintToken();
  const actionToken = mintToken();
  const entries: BridgeAuditEntry[] = [];
  const server = await startBridgeServer({ ...DEPS, backend: actionBackend(), token, actionToken, onAction: (e) => entries.push(e) });
  try {
    const base = `http://127.0.0.1:${String(server.port)}`;
    const post = (bearer: string, body: unknown, headers: Record<string, string> = {}): Promise<Response> =>
      fetch(`${base}/mcp`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${bearer}`, ...headers },
        body: JSON.stringify(body),
      });

    const readList = (await (await post(token, { jsonrpc: '2.0', id: 1, method: 'tools/list' })).json()) as {
      result: { tools: Array<{ name: string }> };
    };
    // DEPS has no D1 ui-focus provider: the desktop-UI tools stay
    // catalog-hidden (the sharing-toggle gate).
    assert.equal(readList.result.tools.length, READ_TOOLS.length - UI_TOOL_NAMES.size);

    const fullList = (await (await post(actionToken, { jsonrpc: '2.0', id: 2, method: 'tools/list' })).json()) as {
      result: { tools: Array<{ name: string }> };
    };
    assert.equal(fullList.result.tools.length, READ_TOOLS.length - UI_TOOL_NAMES.size + ACTION_TOOLS.length);

    const clickBody = { jsonrpc: '2.0', id: 3, method: 'tools/call', params: { name: 'browser_click', arguments: { tabId: 7, selector: '#go' } } };
    const refused = (await (await post(token, clickBody)).json()) as { result: ToolResult };
    assert.equal(refused.result.isError, true);
    assert.match(refused.result.content[0]?.text ?? '', /SCOPE_READ_ONLY/);
    assert.equal(entries.length, 0, 'a scope-refused call never reaches the audit hook');

    const allowed = (await (await post(actionToken, clickBody, { 'x-tp-agent-id': 'agent-9' })).json()) as { result: ToolResult };
    assert.notEqual(allowed.result.isError, true);
    assert.equal(entries.length, 1);
    assert.equal(entries[0]?.agent_id, 'agent-9');
    assert.equal(entries[0]?.ok, true);

    // The two bearers must be distinct — a minting bug must fail closed.
    assert.throws(() => void startBridgeServer({ ...DEPS, backend: actionBackend(), token, actionToken: token }));
  } finally {
    await server.close();
  }
});

// ── W3: hub dispatch (remote agents via the reverse tunnel) ──────────────────
// dispatchHubInvoke is the in-process entry the host's tunnel loop funnels
// browser.invoke envelopes into: same tool machinery as the MCP path, bearer
// parse skipped, action calls pre-authorized hub-side but re-gated here by
// the per-run revoked set, audited once with via:'hub'.

test('W3 dispatch: hub-leg read runs, ring-audited via:hub — but never hub-mirrored', async () => {
  const { deps, entries } = auditDeps(actionBackend());
  const out = await dispatchHubInvoke(
    deps,
    { tool: 'browser_list_tabs', args: {}, agent_id: 'agent-remote', agent_handle: 'r2d2' },
    new Set(),
    'browser',
  );
  assert.equal(out.ok, true);
  if (out.ok) assert.match((out.result as ToolResult).content[0]?.text ?? '', /"tabId": 7/);
  // Remote access to the user's tabs must be visible in Settings → Remote
  // driving, so hub-leg READS are audited (unlike local reads)…
  assert.equal(entries.length, 1, 'hub-leg reads are ring-audited');
  assert.equal(entries[0]?.via, 'hub');
  assert.equal(entries[0]?.tool, 'browser_list_tabs');
  // …but stay ring-only: the hub routed the call, a mirror row adds nothing.
  assert.equal(shouldMirrorAudit(entries[0] as BridgeAuditEntry), false, 'hub reads never mirror to the hub');
});

test('W3 dispatch: local reads stay unaudited; actions and hub entries mirror', async () => {
  const { deps, entries } = auditDeps(actionBackend());
  await callTool2(deps, 'browser_list_tabs', {});
  assert.equal(entries.length, 0, 'local reads keep the W2 posture: unaudited');

  const action: BridgeAuditEntry = {
    ts: '2026-07-30T00:00:00Z', tool: 'browser_click', agent_id: 'a', via: 'hub',
    tab_id: 7, url: null, partition: null, args: {}, ok: true, error: null,
  };
  assert.equal(shouldMirrorAudit(action), true, 'hub actions mirror (W2 contract)');
  assert.equal(shouldMirrorAudit({ ...action, via: 'local' }), true, 'local actions mirror');
  assert.equal(shouldMirrorAudit({ ...action, via: 'local', tool: 'browser_list_tabs' }), true, 'local reads never reach recordAction, so the gate is moot but safe');
});

test('W3 dispatch: a revoked agent is refused for READS too — revoked means gone', async () => {
  const backend = actionBackend();
  const { deps, entries } = auditDeps(backend);
  const revoked = await dispatchHubInvoke(deps, { tool: 'browser_list_tabs', args: {}, agent_id: 'agent-remote' }, new Set(['agent-remote']), 'browser');
  assert.deepEqual(revoked, { ok: false, error: 'revoked by user on desktop' });
  assert.equal(backend.calls.length, 0, 'refused before any CDP call');
  assert.equal(entries.length, 0, 'a revoked refusal is a gate event, not an audited call');
});

test('W3 dispatch: unknown tool refused without touching the backend', async () => {
  const backend = actionBackend();
  const { deps, entries } = auditDeps(backend);
  const out = await dispatchHubInvoke(deps, { tool: 'browser_nuke', args: {}, agent_id: 'agent-remote' }, new Set(), 'browser');
  assert.deepEqual(out, { ok: false, error: 'unknown_tool' });
  assert.equal(backend.calls.length, 0);
  assert.equal(entries.length, 0);
});

test('W3 dispatch: action call runs pre-authorized, audited once with via:hub', async () => {
  const { deps, entries } = auditDeps(actionBackend());
  const out = await dispatchHubInvoke(
    deps,
    { tool: 'browser_navigate', args: { tabId: 7, url: 'https://example.com/#frag' }, agent_id: 'agent-remote', agent_handle: 'r2d2' },
    new Set(),
    'browser',
  );
  assert.equal(out.ok, true);
  assert.equal(entries.length, 1, 'exactly one audit entry per action call');
  assert.equal(entries[0]?.via, 'hub');
  assert.equal(entries[0]?.agent_id, 'agent-remote');
  assert.equal(entries[0]?.agent_handle, 'r2d2');
  assert.equal(entries[0]?.tool, 'browser_navigate');
  assert.equal(entries[0]?.ok, true);
  assert.equal(entries[0]?.args.url, 'https://example.com/', 'fragment stripped in the audit args');

  // A failed action is audited once too, and the envelope carries CODE: msg.
  const gone = await dispatchHubInvoke(
    deps,
    { tool: 'browser_navigate', args: { tabId: 999, url: 'https://example.com/' }, agent_id: 'agent-remote' },
    new Set(),
    'browser',
  );
  assert.equal(gone.ok, false);
  if (!gone.ok) assert.match(gone.error, /^TARGET_GONE: /);
  assert.equal(entries.length, 2);
  assert.equal(entries[1]?.ok, false);
  assert.equal(entries[1]?.error, 'TARGET_GONE');
  assert.equal(entries[1]?.via, 'hub');
});

test('W3 dispatch: a revoked agent is refused before the tool machinery', async () => {
  const backend = actionBackend();
  const { deps, entries } = auditDeps(backend);
  const out = await dispatchHubInvoke(
    deps,
    { tool: 'browser_click', args: { tabId: 7, selector: '#go' }, agent_id: 'agent-remote' },
    new Set(['agent-remote']),
    'browser',
  );
  assert.deepEqual(out, { ok: false, error: 'revoked by user on desktop' });
  assert.equal(backend.calls.length, 0, 'refused before any CDP call');
  assert.equal(entries.length, 0, 'a revoked refusal is a gate event, not an audited action');
});

test('W3 dispatch: a tool-level isError result maps to the error half of the envelope', async () => {
  const { deps, entries } = auditDeps(actionBackend());
  const out = await dispatchHubInvoke(
    deps,
    { tool: 'browser_eval', args: { tabId: 7, expression: 'thrower()' }, agent_id: 'agent-remote' },
    new Set(),
    'browser',
  );
  assert.equal(out.ok, false);
  if (!out.ok) {
    assert.match(out.error, /EVAL_EXCEPTION/);
    assert.match(out.error, /boom/);
  }
  assert.equal(entries.length, 1, 'the action ran and was audited');
  assert.equal(entries[0]?.ok, true, 'runTool returned (an isError result), so the call itself did not throw');
});

test('W3 via: the local MCP path keeps recording via:local', async () => {
  const { deps, entries } = auditDeps(actionBackend());
  await callTool2(deps, 'browser_navigate', { tabId: 7, url: 'https://example.com/' });
  assert.equal(entries.length, 1);
  assert.equal(entries[0]?.via, 'local');
  assert.equal(entries[0]?.agent_handle, undefined, 'local calls carry no hub handle');
});

test('W3 foldRemoteSessions: hub entries fold per-agent, newest first, revoke flag', () => {
  const mk = (ts: string, agentId: string, tool: string, via: 'local' | 'hub', handle?: string): BridgeAuditEntry => ({
    ts,
    tool,
    agent_id: agentId,
    via,
    tab_id: 7,
    url: null,
    partition: null,
    args: {},
    ok: true,
    error: null,
    ...(handle !== undefined ? { agent_handle: handle } : {}),
  });
  const entries = [
    mk('2026-07-29T10:00:00Z', 'agent-a', 'browser_navigate', 'hub', 'r2d2'),
    mk('2026-07-29T10:01:00Z', 'agent-b', 'browser_click', 'hub'),
    mk('2026-07-29T10:02:00Z', 'agent-a', 'browser_type', 'hub', 'r2d2'),
    mk('2026-07-29T10:03:00Z', 'agent-c', 'browser_click', 'local'), // local calls never fold in
  ];
  const sessions = foldRemoteSessions(entries, new Set(['agent-b']));
  assert.deepEqual(sessions, [
    { agent_id: 'agent-a', agent_handle: 'r2d2', last_tool: 'browser_type', last_ts: '2026-07-29T10:02:00Z', revoked: false },
    { agent_id: 'agent-b', last_tool: 'browser_click', last_ts: '2026-07-29T10:01:00Z', revoked: true },
  ]);
  assert.deepEqual(foldRemoteSessions([], new Set()), []);
});
